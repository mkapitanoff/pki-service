package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/service"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// demoTenantID is the hardcoded tenant used for all demo endpoints.
const demoTenantID = "8ba64263-e516-4574-a7e8-fadad9663eea"

// DemoHandler serves unauthenticated demo endpoints for frontend testing.
type DemoHandler struct {
	queries     *repository.Queries
	signSvc     *service.SignService
	storage     storage.Storage
	nc          *ncanode.HTTPClient
	testKeyPath string
	testKeyPass string
}

func NewDemoHandler(
	queries *repository.Queries,
	signSvc *service.SignService,
	store storage.Storage,
	nc *ncanode.HTTPClient,
	testKeyPath string,
	testKeyPass string,
) *DemoHandler {
	return &DemoHandler{
		queries:     queries,
		signSvc:     signSvc,
		storage:     store,
		nc:          nc,
		testKeyPath: testKeyPath,
		testKeyPass: testKeyPass,
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

// HandleSign signs an existing document with the test NCA key.
//
// POST /api/demo/sign/:id?role=client
func (h *DemoHandler) HandleSign(w http.ResponseWriter, r *http.Request) {
	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	role := r.URL.Query().Get("role")
	if role == "" {
		role = "client"
	}

	tenantID := uuid.MustParse(demoTenantID)

	// Load document to find the current S3 key.
	doc, err := h.queries.GetDocument(r.Context(), repository.GetDocumentParams{
		ID:       docID,
		TenantID: tenantID,
	})
	if err != nil {
		respondError(w, apperr.ErrDocumentNotFound.WithCause(err))
		return
	}

	// Download the current PDF.
	pdfBytes, err := h.storage.DownloadFile(r.Context(), doc.S3KeyCurrent)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(fmt.Errorf("s3 download: %w", err)))
		return
	}

	// Sign with the test NCA key.
	cms, err := h.signWithTestKey(r.Context(), pdfBytes)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(fmt.Errorf("test key sign: %w", err)))
		return
	}

	result, err := h.signSvc.Sign(r.Context(), service.SignInput{
		DocumentID: docID,
		TenantID:   tenantID,
		CMS:        cms,
		Role:       role,
	})
	if err != nil {
		respondError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"document_id":         docID,
			"signature_id":        result.SignatureID,
			"signed_document_url": result.SignedDocumentURL,
			"signature":           result.Signature,
		},
	})
}

// signWithTestKey reads the PKCS#12 test key from testKeyPath, base64-encodes
// both the document and the key, and calls NCANode /cms/sign.
func (h *DemoHandler) signWithTestKey(ctx context.Context, pdfBytes []byte) (string, error) {
	p12Bytes, err := os.ReadFile(h.testKeyPath)
	if err != nil {
		return "", fmt.Errorf("read test key %q: %w", h.testKeyPath, err)
	}

	dataB64 := base64.StdEncoding.EncodeToString(pdfBytes)
	p12B64 := base64.StdEncoding.EncodeToString(p12Bytes)

	return h.nc.SignCMS(ctx, dataB64, p12B64, h.testKeyPass)
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
