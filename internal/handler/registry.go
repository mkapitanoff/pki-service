package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// RegistryHandler exposes an admin-only registry of signing-session documents
// (across tenants) for operational tracking: status, download signed PDF,
// link to the public verify page.
type RegistryHandler struct {
	queries *repository.Queries
	store   storage.Storage
}

func NewRegistryHandler(queries *repository.Queries, store storage.Storage) *RegistryHandler {
	return &RegistryHandler{queries: queries, store: store}
}

const (
	registryDefaultLimit = 50
	registryMaxLimit     = 200
)

// HandleListRegistry — GET /api/admin/signing-documents?tenant_id=&status=&limit=&offset=
func (h *RegistryHandler) HandleListRegistry(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	var tenantFilter uuid.NullUUID
	if v := q.Get("tenant_id"); v != "" {
		id, err := uuid.Parse(v)
		if err != nil {
			respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid tenant_id: %w", err)))
			return
		}
		tenantFilter = uuid.NullUUID{UUID: id, Valid: true}
	}

	var statusFilter sql.NullString
	if v := q.Get("status"); v != "" {
		statusFilter = sql.NullString{String: v, Valid: true}
	}

	limit := registryDefaultLimit
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > registryMaxLimit {
		limit = registryMaxLimit
	}
	offset := 0
	if v := q.Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}

	ctx := r.Context()

	rows, err := h.queries.ListSigningSessionDocumentsForRegistry(ctx, repository.ListSigningSessionDocumentsForRegistryParams{
		TenantID:    tenantFilter,
		DocStatus:   statusFilter,
		LimitCount:  int32(limit),
		OffsetCount: int32(offset),
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	total, err := h.queries.CountSigningSessionDocumentsForRegistry(ctx, repository.CountSigningSessionDocumentsForRegistryParams{
		TenantID:  tenantFilter,
		DocStatus: statusFilter,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"documents": rows,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	})
}

// HandleDownloadRegistryDocument — GET /api/admin/signing-documents/{id}/file
// Отдаёт подписанный PDF из НАШЕГО хранилища (signed_s3_key) — та же копия,
// что sign_complete.go кладёт до асинхронной отправки в S3 клиента.
func (h *RegistryHandler) HandleDownloadRegistryDocument(w http.ResponseWriter, r *http.Request) {
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	doc, err := h.queries.GetSigningSessionDocument(r.Context(), docID)
	if err != nil {
		respondError(w, apperr.ErrDocumentNotFound)
		return
	}
	if !doc.SignedS3Key.Valid || doc.SignedS3Key.String == "" {
		respondError(w, apperr.ErrDocumentNotFound.WithCause(fmt.Errorf("document has no signed_s3_key")))
		return
	}

	data, err := h.store.DownloadFile(r.Context(), doc.SignedS3Key.String)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", doc.DocumentName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
