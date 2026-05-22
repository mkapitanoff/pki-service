package handler

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// RequireAdmin is a middleware that allows only users with role == "admin".
func RequireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		role, _ := r.Context().Value(userRoleKey).(string)
		if role != "admin" {
			respondError(w, apperr.ErrForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// AdminHandler handles admin endpoints for tenants, API keys, and users.
type AdminHandler struct {
	queries *repository.Queries
}

func NewAdminHandler(queries *repository.Queries) *AdminHandler {
	return &AdminHandler{queries: queries}
}

// HandleListTenants — GET /admin/tenants
func (h *AdminHandler) HandleListTenants(w http.ResponseWriter, r *http.Request) {
	rows, err := h.queries.ListTenantsWithKeyCount(r.Context())
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	respondJSON(w, http.StatusOK, rows)
}

type createTenantRequest struct {
	Name string `json:"name"`
	Type string `json:"type"` // "individual" | "legal_entity"
}

// HandleCreateTenant — POST /admin/tenants
func (h *AdminHandler) HandleCreateTenant(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	t := repository.TenantType(req.Type)
	if t != repository.TenantTypeIndividual && t != repository.TenantTypeLegalEntity {
		t = repository.TenantTypeIndividual
	}
	tenant, err := h.queries.CreateTenant(r.Context(), repository.CreateTenantParams{
		Name: req.Name,
		Type: t,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	respondJSON(w, http.StatusCreated, tenant)
}

// HandleListKeys — GET /admin/tenants/{tenant_id}/keys
func (h *AdminHandler) HandleListKeys(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	keys, err := h.queries.ListAPIKeysByTenant(r.Context(), tenantID)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	respondJSON(w, http.StatusOK, keys)
}

type createKeyRequest struct {
	Label     string  `json:"label"`
	ExpiresAt *string `json:"expires_at"` // RFC3339 or null
}

// HandleCreateKey — POST /admin/tenants/{tenant_id}/keys
// Returns the raw key exactly once; only the hash is stored.
func (h *AdminHandler) HandleCreateKey(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	var req createKeyRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Label == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	// Generate 32 random bytes → hex string as the raw key.
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	rawKey := hex.EncodeToString(raw)

	sum := sha256.Sum256([]byte(rawKey))
	keyHash := hex.EncodeToString(sum[:])

	var expiresAt sql.NullTime
	if req.ExpiresAt != nil && *req.ExpiresAt != "" {
		t, err := time.Parse(time.RFC3339, *req.ExpiresAt)
		if err != nil {
			respondError(w, apperr.ErrInvalidRequest)
			return
		}
		expiresAt = sql.NullTime{Time: t, Valid: true}
	}

	key, err := h.queries.CreateAPIKey(r.Context(), repository.CreateAPIKeyParams{
		TenantID:  tenantID,
		KeyHash:   keyHash,
		Label:     req.Label,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"id":         key.ID,
		"tenant_id":  key.TenantID,
		"label":      key.Label,
		"is_active":  key.IsActive,
		"expires_at": key.ExpiresAt,
		"created_at": key.CreatedAt,
		"key":        rawKey, // returned once, never stored in plaintext
	})
}

// HandleDeactivateKey — DELETE /admin/tenants/{tenant_id}/keys/{key_id}
func (h *AdminHandler) HandleDeactivateKey(w http.ResponseWriter, r *http.Request) {
	tenantID, err := uuid.Parse(chi.URLParam(r, "tenant_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	keyID, err := uuid.Parse(chi.URLParam(r, "key_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	if err := h.queries.DeactivateAPIKey(r.Context(), repository.DeactivateAPIKeyParams{
		ID:       keyID,
		TenantID: tenantID,
	}); err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// HandleListUsers — GET /admin/users
func (h *AdminHandler) HandleListUsers(w http.ResponseWriter, r *http.Request) {
	users, err := h.queries.ListUsers(r.Context())
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	out := make([]map[string]any, len(users))
	for i, u := range users {
		out[i] = map[string]any{
			"id":            u.ID,
			"email":         u.Email,
			"name":          u.Name,
			"role":          u.Role,
			"tenant_id":     u.TenantID,
			"is_active":     u.IsActive,
			"created_at":    u.CreatedAt,
			"last_login_at": u.LastLoginAt,
		}
	}
	respondJSON(w, http.StatusOK, out)
}

type updateUserRequest struct {
	Role     string `json:"role"`
	IsActive bool   `json:"is_active"`
}

// HandleUpdateUser — PATCH /admin/users/{user_id}
func (h *AdminHandler) HandleUpdateUser(w http.ResponseWriter, r *http.Request) {
	userID, err := uuid.Parse(chi.URLParam(r, "user_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	var req updateUserRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}
	user, err := h.queries.UpdateUserRole(r.Context(), repository.UpdateUserRoleParams{
		Role:     req.Role,
		IsActive: sql.NullBool{Bool: req.IsActive, Valid: true},
		ID:       userID,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"id":        user.ID,
		"email":     user.Email,
		"name":      user.Name,
		"role":      user.Role,
		"is_active": user.IsActive,
	})
}
