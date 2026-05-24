package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/service"
)

// signingJobs tracks async sign operations keyed by document_id string.
var signingJobs sync.Map

type signJobStatus struct {
	Status string
	Result *service.SignResult
	Error  string
}

type ctxKey string

// TenantIDKey holds the authenticated tenant uuid (set by auth middleware).
const TenantIDKey ctxKey = "tenant_id"

func WithTenant(ctx context.Context, tenantID uuid.UUID) context.Context {
	return context.WithValue(ctx, TenantIDKey, tenantID)
}

func tenantFromCtx(r *http.Request) (uuid.UUID, bool) {
	v, ok := r.Context().Value(TenantIDKey).(uuid.UUID)
	return v, ok
}

type SignHandler struct {
	signSvc *service.SignService
	queries *repository.Queries
}

func NewSignHandler(signSvc *service.SignService, q *repository.Queries) *SignHandler {
	return &SignHandler{signSvc: signSvc, queries: q}
}

type signRequest struct {
	CMS  string `json:"cms"`
	Role string `json:"role"`
}

func (h *SignHandler) HandleSign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	var req signRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CMS == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	result, err := h.signSvc.Sign(r.Context(), service.SignInput{
		DocumentID: docID,
		TenantID:   tenantID,
		CMS:        req.CMS,
		Role:       req.Role,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"signature_id":        result.SignatureID,
			"signed_document_url": result.SignedDocumentURL,
			"signature":           result.Signature,
			"redirect_url":        result.RedirectURL,
		},
	})
}

// HandleSignAsync starts signing in a goroutine and immediately returns 202.
func (h *SignHandler) HandleSignAsync(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	var req signRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.CMS == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	key := docID.String()
	signingJobs.Store(key, &signJobStatus{Status: "processing"})

	input := service.SignInput{
		DocumentID: docID,
		TenantID:   tenantID,
		CMS:        req.CMS,
		Role:       req.Role,
	}

	go func() {
		result, err := h.signSvc.Sign(context.Background(), input)
		if err != nil {
			msg := err.Error()
			signingJobs.Store(key, &signJobStatus{Status: "error", Error: msg})
			return
		}
		signingJobs.Store(key, &signJobStatus{Status: "done", Result: result})
	}()

	respondJSON(w, http.StatusAccepted, map[string]any{
		"status":      "processing",
		"document_id": docID,
	})
}

// HandleSignStatus returns the current async signing status for a document.
func (h *SignHandler) HandleSignStatus(w http.ResponseWriter, r *http.Request) {
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	key := docID.String()
	raw, ok := signingJobs.Load(key)
	if !ok {
		respondJSON(w, http.StatusOK, map[string]any{"status": "not_found"})
		return
	}

	job := raw.(*signJobStatus)
	switch job.Status {
	case "processing":
		respondJSON(w, http.StatusOK, map[string]any{"status": "processing"})
	case "error":
		respondJSON(w, http.StatusOK, map[string]any{
			"status": "error",
			"error":  job.Error,
		})
	case "done":
		signingJobs.Delete(key) // cleanup after client reads result
		respondJSON(w, http.StatusOK, map[string]any{
			"status":              "done",
			"signature_id":        job.Result.SignatureID,
			"signed_document_url": job.Result.SignedDocumentURL,
			"signature":           job.Result.Signature,
			"redirect_url":        job.Result.RedirectURL,
		})
	default:
		respondJSON(w, http.StatusOK, map[string]any{"status": "not_found"})
	}
}

type createDocumentRequest struct {
	S3Key       string          `json:"s3_key"`
	Title       string          `json:"title"`
	Metadata    json.RawMessage `json:"metadata"`
	CallbackURL string          `json:"callback_url"`
}

func (h *SignHandler) HandleCreateDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	var req createDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.S3Key == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	var meta pqtype.NullRawMessage
	if len(req.Metadata) > 0 {
		meta = pqtype.NullRawMessage{RawMessage: req.Metadata, Valid: true}
	}

	doc, err := h.queries.CreateDocument(r.Context(), repository.CreateDocumentParams{
		TenantID:       tenantID,
		Title:          toNullString(req.Title),
		S3KeyOriginal:  req.S3Key,
		S3KeyCurrent:   req.S3Key,
		CurrentVersion: 0,
		Status:         repository.DocStatusDraft,
		Metadata:       meta,
		CallbackUrl:    toNullString(req.CallbackURL),
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"id":         doc.ID,
			"status":     doc.Status,
			"created_at": doc.CreatedAt,
		},
	})
}
