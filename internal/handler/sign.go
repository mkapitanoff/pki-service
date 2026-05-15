package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/service"
)

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
		},
	})
}

type createDocumentRequest struct {
	S3Key    string          `json:"s3_key"`
	Title    string          `json:"title"`
	Metadata json.RawMessage `json:"metadata"`
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
