package handler

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// WebhookHandler manages webhook subscriptions for a tenant.
type WebhookHandler struct {
	queries *repository.Queries
}

func NewWebhookHandler(queries *repository.Queries) *WebhookHandler {
	return &WebhookHandler{queries: queries}
}

// HandleCreate — POST /api/v1/webhooks
func (h *WebhookHandler) HandleCreate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	var req struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
		Secret string   `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	if req.URL == "" {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("url is required")))
		return
	}
	if _, err := url.ParseRequestURI(req.URL); err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("url is not valid: %w", err)))
		return
	}
	if len(req.Events) == 0 {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("events must not be empty")))
		return
	}

	// Auto-generate secret if not provided.
	secret := req.Secret
	if secret == "" {
		secret = generateWebhookSecret()
	}

	wh, err := h.queries.CreateWebhook(r.Context(), repository.CreateWebhookParams{
		TenantID: tenantID,
		Url:      req.URL,
		Events:   req.Events,
		Secret:   secret,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"id":         wh.ID,
		"url":        wh.Url,
		"events":     wh.Events,
		"secret":     secret, // returned only at creation
		"is_active":  wh.IsActive,
		"created_at": wh.CreatedAt,
	})
}

// HandleList — GET /api/v1/webhooks
func (h *WebhookHandler) HandleList(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	hooks, err := h.queries.ListWebhooksByTenant(r.Context(), tenantID)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	type item struct {
		ID        uuid.UUID `json:"id"`
		URL       string    `json:"url"`
		Events    []string  `json:"events"`
		IsActive  bool      `json:"is_active"`
		CreatedAt any       `json:"created_at"`
	}
	out := make([]item, 0, len(hooks))
	for _, wh := range hooks {
		out = append(out, item{
			ID:        wh.ID,
			URL:       wh.Url,
			Events:    wh.Events,
			IsActive:  wh.IsActive,
			CreatedAt: wh.CreatedAt,
		})
	}
	respondJSON(w, http.StatusOK, map[string]any{"data": out})
}

// HandleDelete — DELETE /api/v1/webhooks/{webhook_id}
func (h *WebhookHandler) HandleDelete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	webhookID, err := uuid.Parse(chi.URLParam(r, "webhook_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid webhook_id")))
		return
	}

	if err := h.queries.DeactivateWebhook(r.Context(), repository.DeactivateWebhookParams{
		ID:       webhookID,
		TenantID: tenantID,
	}); err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleTest — POST /api/v1/webhooks/{webhook_id}/test
// Sends a test ping to the webhook URL synchronously.
func (h *WebhookHandler) HandleTest(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	webhookID, err := uuid.Parse(chi.URLParam(r, "webhook_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid webhook_id")))
		return
	}

	wh, err := h.queries.GetWebhookByID(r.Context(), webhookID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}
	if wh.TenantID != tenantID {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	if !wh.IsActive {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("webhook is inactive")))
		return
	}

	// Schedule a test delivery via webhook_deliveries.
	payload := []byte(`{"event":"test","message":"PKI Service webhook test ping"}`)
	_, dbErr := h.queries.CreateWebhookDelivery(r.Context(), repository.CreateWebhookDeliveryParams{
		WebhookID:   wh.ID,
		Event:       "test",
		Payload:     payload,
		Attempt:     1,
		Status:      "pending",
		ScheduledAt: time.Now(),
	})
	if dbErr != nil {
		respondError(w, apperr.ErrInternal.WithCause(dbErr))
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "test delivery queued"})
}

// --- helpers -----------------------------------------------------------------

func generateWebhookSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

