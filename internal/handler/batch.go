package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	internalpdf "github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/service"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// BatchHandler handles POST /api/v1/batch/sign and POST /api/v1/batch/upload.
type BatchHandler struct {
	signSvc       *service.SignService
	queries       *repository.Queries
	store         storage.Storage
	verifyBaseURL string
	ncanode       ncanode.NCANodeClient
}

func NewBatchHandler(
	signSvc *service.SignService,
	queries *repository.Queries,
	store storage.Storage,
	verifyBaseURL string,
	nc ncanode.NCANodeClient,
) *BatchHandler {
	return &BatchHandler{
		signSvc:       signSvc,
		queries:       queries,
		store:         store,
		verifyBaseURL: verifyBaseURL,
		ncanode:       nc,
	}
}

// ── POST /api/v1/batch/sign ───────────────────────────────────────────────────

type batchSignDocInput struct {
	DocumentID string `json:"document_id"`
	CMS        string `json:"cms"`
	Role       string `json:"role"`
}

type batchSignRequest struct {
	Documents []batchSignDocInput `json:"documents"`
}

type batchSignResult struct {
	DocumentID  string  `json:"document_id"`
	Status      string  `json:"status"`
	SignatureID *string `json:"signature_id,omitempty"`
	DownloadURL *string `json:"download_url,omitempty"`
	Error       *string `json:"error,omitempty"`
}

type batchSignResponse struct {
	Results   []batchSignResult `json:"results"`
	Total     int               `json:"total"`
	Succeeded int               `json:"succeeded"`
	Failed    int               `json:"failed"`
}

func (h *BatchHandler) HandleBatchSign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	var req batchSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Documents) == 0 {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	// 10-minute timeout for the whole batch.
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	results := make([]batchSignResult, 0, len(req.Documents))
	succeeded, failed := 0, 0

	for _, doc := range req.Documents {
		docID, err := uuid.Parse(doc.DocumentID)
		if err != nil {
			errMsg := "invalid document_id"
			results = append(results, batchSignResult{
				DocumentID: doc.DocumentID,
				Status:     "error",
				Error:      &errMsg,
			})
			failed++
			continue
		}

		result, err := h.signSvc.Sign(ctx, service.SignInput{
			DocumentID: docID,
			TenantID:   tenantID,
			CMS:        doc.CMS,
			Role:       doc.Role,
		})
		if err != nil {
			errMsg := err.Error()
			if ae, ok2 := err.(*apperr.AppError); ok2 {
				errMsg = ae.Message
			}
			results = append(results, batchSignResult{
				DocumentID: doc.DocumentID,
				Status:     "error",
				Error:      &errMsg,
			})
			failed++
			continue
		}

		sigIDStr := result.SignatureID.String()
		downloadURL := fmt.Sprintf("%s/api/v1/documents/%s/file", h.verifyBaseURL, docID)
		results = append(results, batchSignResult{
			DocumentID:  doc.DocumentID,
			Status:      "signed",
			SignatureID: &sigIDStr,
			DownloadURL: &downloadURL,
		})
		succeeded++
	}

	respondJSON(w, http.StatusOK, batchSignResponse{
		Results:   results,
		Total:     len(req.Documents),
		Succeeded: succeeded,
		Failed:    failed,
	})
}

// ── POST /api/v1/batch/upload ─────────────────────────────────────────────────

type batchUploadDocResult struct {
	DocumentID   string `json:"document_id,omitempty"`
	Title        string `json:"title"`
	SHA256Hash   string `json:"sha256_hash,omitempty"`
	Status       string `json:"status"`
	Deduplicated bool   `json:"deduplicated"`
	Error        string `json:"error,omitempty"`
}

type batchUploadResponse struct {
	Documents []batchUploadDocResult `json:"documents"`
	Total     int                    `json:"total"`
}

func (h *BatchHandler) HandleBatchUpload(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	if err := r.ParseMultipartForm(200 << 20); err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(err))
		return
	}

	fhs := r.MultipartForm.File["files[]"]
	if len(fhs) == 0 {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("no files provided in field 'files[]'")))
		return
	}

	titles := r.MultipartForm.Value["titles[]"]
	callbackURL := r.FormValue("callback_url")

	results := make([]batchUploadDocResult, 0, len(fhs))

	for i, fh := range fhs {
		// Determine title: prefer titles[i], fall back to filename without .pdf.
		title := fh.Filename
		if len(title) > 4 && title[len(title)-4:] == ".pdf" {
			title = title[:len(title)-4]
		}
		if i < len(titles) && titles[i] != "" {
			title = titles[i]
		}
		if title == "" {
			title = fmt.Sprintf("document_%d", i+1)
		}

		f, err := fh.Open()
		if err != nil {
			results = append(results, batchUploadDocResult{
				Title:  title,
				Status: "error",
				Error:  "failed to open file",
			})
			continue
		}
		pdfBytes, err := io.ReadAll(io.LimitReader(f, 50<<20))
		f.Close()
		if err != nil || len(pdfBytes) == 0 {
			results = append(results, batchUploadDocResult{
				Title:  title,
				Status: "error",
				Error:  "failed to read file",
			})
			continue
		}

		sum := sha256.Sum256(pdfBytes)
		docSHA256 := hex.EncodeToString(sum[:])

		// Dedup check 1: original hash of a signed document.
		if existing, err2 := h.queries.GetDocumentBySHA256(r.Context(), repository.GetDocumentBySHA256Params{
			TenantID:   tenantID,
			Sha256Hash: docSHA256,
		}); err2 == nil && existing.Status != repository.DocStatusDraft {
			results = append(results, batchUploadDocResult{
				DocumentID:   existing.ID.String(),
				Title:        existing.Title.String,
				SHA256Hash:   docSHA256,
				Status:       string(existing.Status),
				Deduplicated: true,
			})
			continue
		}

		// Dedup check 2: current hash (user re-uploaded a signed PDF).
		if cur, err2 := h.queries.GetDocumentByCurrentSHA256(r.Context(), repository.GetDocumentByCurrentSHA256Params{
			TenantID:          tenantID,
			Sha256HashCurrent: docSHA256,
		}); err2 == nil {
			results = append(results, batchUploadDocResult{
				DocumentID:   cur.ID.String(),
				Title:        cur.Title.String,
				SHA256Hash:   docSHA256,
				Status:       string(cur.Status),
				Deduplicated: true,
			})
			continue
		}

		// New document: upload to S3.
		pathID := uuid.New()
		s3Key := fmt.Sprintf("%s/%s/original.pdf", tenantID, pathID)

		if err2 := h.store.UploadFile(r.Context(), s3Key, pdfBytes, "application/pdf"); err2 != nil {
			results = append(results, batchUploadDocResult{
				Title:  title,
				Status: "error",
				Error:  "storage upload failed",
			})
			continue
		}

		// Detect embedded PAdES signatures to set initial status.
		extractedSigs := internalpdf.ExtractSignatures(pdfBytes)
		docStatus := repository.DocStatusDraft
		if h.ncanode != nil && len(extractedSigs) > 0 {
			docStatus = repository.DocStatusPartiallySigned
		}

		doc, err2 := h.queries.CreateDocument(r.Context(), repository.CreateDocumentParams{
			TenantID:       tenantID,
			Title:          toNullString(title),
			S3KeyOriginal:  s3Key,
			S3KeyCurrent:   s3Key,
			CurrentVersion: 0,
			Status:         docStatus,
			CallbackUrl:    toNullString(callbackURL),
			Sha256Hash:     docSHA256,
		})
		if err2 != nil {
			results = append(results, batchUploadDocResult{
				Title:  title,
				Status: "error",
				Error:  "database error",
			})
			continue
		}

		results = append(results, batchUploadDocResult{
			DocumentID:   doc.ID.String(),
			Title:        doc.Title.String,
			SHA256Hash:   docSHA256,
			Status:       string(doc.Status),
			Deduplicated: false,
		})
	}

	respondJSON(w, http.StatusCreated, batchUploadResponse{
		Documents: results,
		Total:     len(results),
	})
}
