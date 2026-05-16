package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	stderrors "errors"
	"database/sql"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// DocumentHandler handles production document upload and download endpoints.
type DocumentHandler struct {
	queries       *repository.Queries
	storage       storage.Storage
	verifyBaseURL string
}

func NewDocumentHandler(
	queries *repository.Queries,
	store storage.Storage,
	verifyBaseURL string,
) *DocumentHandler {
	return &DocumentHandler{
		queries:       queries,
		storage:       store,
		verifyBaseURL: verifyBaseURL,
	}
}

// HandleUploadDocument handles POST /api/v1/documents/upload.
// Accepts multipart/form-data: file (PDF), title (string).
// Requires APIKeyAuth — tenant is read from context.
func (h *DocumentHandler) HandleUploadDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(32 << 20); err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(err))
		return
	}

	f, _, err := r.FormFile("file")
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("field 'file': %w", err)))
		return
	}
	defer f.Close()

	pdfBytes, err := io.ReadAll(io.LimitReader(f, 50<<20)) // 50 MB max
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}
	if len(pdfBytes) == 0 {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("empty file")))
		return
	}

	title := r.FormValue("title")

	// SHA-256 of the original PDF (returned to caller for CMS signing).
	sum := sha256.Sum256(pdfBytes)
	docSHA256 := hex.EncodeToString(sum[:])

	// Use a stable UUID as the S3 path segment so the key is predictable.
	pathID := uuid.New()
	s3Key := fmt.Sprintf("%s/%s/original.pdf", tenantID, pathID)

	if err := h.storage.UploadFile(r.Context(), s3Key, pdfBytes, "application/pdf"); err != nil {
		respondError(w, apperr.ErrInternal.WithCause(fmt.Errorf("s3 upload: %w", err)))
		return
	}

	doc, err := h.queries.CreateDocument(r.Context(), repository.CreateDocumentParams{
		TenantID:       tenantID,
		Title:          toNullString(title),
		S3KeyOriginal:  s3Key,
		S3KeyCurrent:   s3Key,
		CurrentVersion: 0,
		Status:         repository.DocStatusDraft,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(fmt.Errorf("create document: %w", err)))
		return
	}

	respondJSON(w, http.StatusCreated, map[string]any{
		"data": map[string]any{
			"document_id": doc.ID,
			"title":       doc.Title.String,
			"sha256_hash": docSHA256,
			"status":      doc.Status,
			"sign_url":    fmt.Sprintf("%s/verify/%s", h.verifyBaseURL, doc.ID),
		},
	})
}

// HandleDownloadDocument handles GET /api/v1/documents/:id/download.
// Returns the current signed PDF.
// 404 if not found, belongs to another tenant, or document is still in draft
// (no signatures yet).
func (h *DocumentHandler) HandleDownloadDocument(w http.ResponseWriter, r *http.Request) {
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

	doc, err := h.queries.GetDocument(r.Context(), repository.GetDocumentParams{
		ID:       docID,
		TenantID: tenantID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
			return
		}
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	// Draft means no signatures — nothing to download yet.
	if doc.Status == repository.DocStatusDraft {
		respondError(w, apperr.ErrDocumentNotFound)
		return
	}

	data, err := h.storage.DownloadFile(r.Context(), doc.S3KeyCurrent)
	if err != nil {
		if stderrors.Is(err, storage.ErrNotFound) {
			respondError(w, apperr.ErrDocumentNotFound)
			return
		}
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	title := doc.Title.String
	if title == "" {
		title = "document"
	}
	filename := fmt.Sprintf("%s_signed.pdf", sanitizeFilename(title))

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// sanitizeFilename replaces characters unsafe in Content-Disposition filenames.
func sanitizeFilename(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '"' || c == '\\' || c == '/' || c == '\n' || c == '\r' {
			out = append(out, '_')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
