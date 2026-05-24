package handler

import (
	"crypto/sha256"
	stderrors "errors"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	internalpdf "github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// DocumentHandler handles production document upload and download endpoints.
type DocumentHandler struct {
	queries       *repository.Queries
	storage       storage.Storage
	verifyBaseURL string
	ncanode       ncanode.NCANodeClient
}

func NewDocumentHandler(
	queries *repository.Queries,
	store storage.Storage,
	verifyBaseURL string,
	nc ncanode.NCANodeClient,
) *DocumentHandler {
	return &DocumentHandler{
		queries:       queries,
		storage:       store,
		verifyBaseURL: verifyBaseURL,
		ncanode:       nc,
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
	callbackURL := r.FormValue("callback_url")

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

	// Extract any CMS signatures already embedded in the PDF (e.g. pre-signed by another party).
	extractedSigs := internalpdf.ExtractSignatures(pdfBytes)

	// Verify extracted signatures via NCANode (best-effort, non-blocking on failure).
	type existingSigInfo struct {
		SignerName string `json:"signer_name"`
		SignerIIN  string `json:"signer_iin"`
		SignedAt   string `json:"signed_at"`
		Valid      bool   `json:"valid"`
	}
	var verifiedSigs []existingSigInfo
	var verifyResults []*ncanode.VerifyResult

	if h.ncanode != nil && len(extractedSigs) > 0 {
		for _, es := range extractedSigs {
			vr, err := h.ncanode.VerifyCMS(r.Context(), es.CMS, docSHA256)
			if err != nil || vr == nil {
				continue
			}
			verifiedSigs = append(verifiedSigs, existingSigInfo{
				SignerName: vr.SignerName,
				SignerIIN:  internalpdf.MaskIIN(vr.SignerIIN),
				SignedAt:   vr.OCSPCheckedAt.Format(time.RFC3339),
				Valid:      vr.Valid && vr.OCSPStatus != ncanode.OCSPStatusRevoked,
			})
			verifyResults = append(verifyResults, vr)
		}
	}

	// Determine initial document status.
	docStatus := repository.DocStatusDraft
	if len(verifiedSigs) > 0 {
		docStatus = repository.DocStatusPartiallySigned
	}

	doc, err := h.queries.CreateDocument(r.Context(), repository.CreateDocumentParams{
		TenantID:       tenantID,
		Title:          toNullString(title),
		S3KeyOriginal:  s3Key,
		S3KeyCurrent:   s3Key,
		CurrentVersion: 0,
		Status:         docStatus,
		CallbackUrl:    toNullString(callbackURL),
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(fmt.Errorf("create document: %w", err)))
		return
	}

	// Persist verified existing signatures.
	for i, vr := range verifyResults {
		if i >= len(extractedSigs) {
			break
		}
		ocspStatus := repository.OcspStatusType(vr.OCSPStatus)
		sigID := uuid.New()
		qrURL := fmt.Sprintf("%s/verify/%s", h.verifyBaseURL, sigID)
		_, _ = h.queries.CreateSignatureWithID(r.Context(), repository.CreateSignatureWithIDParams{
			ID:            sigID,
			DocumentID:    doc.ID,
			TenantID:      tenantID,
			VersionNumber: 0,
			SequenceNum:   int32(i + 1),
			CmsB64:        extractedSigs[i].CMS,
			Role:          "external",
			SignerIin:     sql.NullString{String: vr.SignerIIN, Valid: vr.SignerIIN != ""},
			SignerName:    vr.SignerName,
			SignerBin:     sql.NullString{String: vr.SignerBIN, Valid: vr.SignerBIN != ""},
			OrgName:       sql.NullString{String: vr.OrgName, Valid: vr.OrgName != ""},
			SignerType:    vr.SignerType,
			Basis:         sql.NullString{String: vr.Basis, Valid: vr.Basis != ""},
			CertSerial:    vr.CertSerial,
			CertNotBefore: vr.CertNotBefore,
			CertNotAfter:  vr.CertNotAfter,
			CaName:        vr.CAName,
			OcspStatus:    ocspStatus,
			OcspCheckedAt: vr.OCSPCheckedAt,
			TspTime:       sql.NullTime{Time: vr.TSPTime, Valid: !vr.TSPTime.IsZero()},
			Sha256Hash:    docSHA256,
			SignFormat:    vr.SignFormat,
			QrUrl:         qrURL,
		})
	}

	respData := map[string]any{
		"document_id":  doc.ID,
		"title":        doc.Title.String,
		"sha256_hash":  docSHA256,
		"status":       doc.Status,
		"sign_url":     fmt.Sprintf("%s/document/%s", h.verifyBaseURL, doc.ID),
		"callback_url": doc.CallbackUrl.String,
	}
	if len(verifiedSigs) > 0 {
		respData["existing_signatures"] = verifiedSigs
	}

	respondJSON(w, http.StatusCreated, map[string]any{"data": respData})
}

// HandleDownloadDocument handles GET /api/v1/documents/:id/file.
// Returns the original PDF for draft documents and the current signed PDF otherwise.
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

	// For draft documents serve the original PDF; for signed/partially_signed serve the current (stamped) PDF.
	s3Key := doc.S3KeyOriginal
	if doc.Status == repository.DocStatusSigned || doc.Status == repository.DocStatusPartiallySigned {
		s3Key = doc.S3KeyCurrent
	}

	data, err := h.storage.DownloadFile(r.Context(), s3Key)
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
