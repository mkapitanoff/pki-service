package handler

import (
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// demoTenantID is the hardcoded tenant used for all demo endpoints.
const demoTenantID = "8ba64263-e516-4574-a7e8-fadad9663eea"

// DemoHandler serves unauthenticated demo endpoints for frontend testing.
type DemoHandler struct {
	queries *repository.Queries
	storage storage.Storage
}

func NewDemoHandler(
	queries *repository.Queries,
	store storage.Storage,
) *DemoHandler {
	return &DemoHandler{
		queries: queries,
		storage: store,
	}
}

// HandleUpload accepts a multipart PDF upload, stores it in S3, and registers
// the document in the DB. Returns {document_id, status} — no auto-signing.
//
// POST /api/demo/upload
// Form fields: file (PDF), title (string, optional)
func (h *DemoHandler) HandleUpload(w http.ResponseWriter, r *http.Request) {
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

	pdfBytes, err := io.ReadAll(io.LimitReader(f, 20<<20))
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	title := r.FormValue("title")
	tenantID := uuid.MustParse(demoTenantID)

	// Generate a path UUID for S3 (document DB ID is assigned by the DB).
	pathID := uuid.New()
	s3Key := fmt.Sprintf("%s/%s/original.pdf", tenantID, pathID)

	// Upload original PDF.
	if err := h.storage.UploadFile(r.Context(), s3Key, pdfBytes, "application/pdf"); err != nil {
		respondError(w, apperr.ErrInternal.WithCause(fmt.Errorf("s3 upload: %w", err)))
		return
	}

	// Register document in DB.
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

	respondJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"document_id": doc.ID,
			"status":      doc.Status,
		},
	})
}

// HandleDownload streams the current PDF from S3.
func (h *DemoHandler) HandleDownload(w http.ResponseWriter, r *http.Request) {
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	tenantID := uuid.MustParse(demoTenantID)
	doc, err := h.queries.GetDocument(r.Context(), repository.GetDocumentParams{
		ID: docID, TenantID: tenantID,
	})
	if err != nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	s3Key := doc.S3KeyCurrent
	if s3Key == "" {
		s3Key = doc.S3KeyOriginal
	}
	data, err := h.storage.DownloadFile(r.Context(), s3Key)
	if err != nil {
		http.Error(w, "storage error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="document.pdf"`)
	w.Write(data)
}
