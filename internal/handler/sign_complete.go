package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/qr"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/signer"
	"github.com/mkapitanoff/pki-service/internal/storage"
	"github.com/mkapitanoff/pki-service/internal/worker"
)

// SignCompleteHandler handles POST /api/v1/sign/complete.
type SignCompleteHandler struct {
	queries *repository.Queries
	nc      ncanode.NCANodeClient
	store   storage.Storage
	extS3   s3client.ExternalS3Client
}

func NewSignCompleteHandler(
	queries *repository.Queries,
	nc ncanode.NCANodeClient,
	store storage.Storage,
	extS3 s3client.ExternalS3Client,
) *SignCompleteHandler {
	return &SignCompleteHandler{queries: queries, nc: nc, store: store, extS3: extS3}
}

// --- request / response ---

type completeSignatureInput struct {
	DocID string `json:"doc_id"`
	CMS   string `json:"cms"`
}

type completeRequest struct {
	SessionID  string                   `json:"session_id"`
	Signatures []completeSignatureInput `json:"signatures"`
}

type completeDocResponse struct {
	DocID  string `json:"doc_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
	S3Key  string `json:"s3_key,omitempty"`
	Error  string `json:"error,omitempty"`
}

// HandleComplete — POST /api/v1/sign/complete
func (h *SignCompleteHandler) HandleComplete(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	sessionID, err := uuid.Parse(req.SessionID)
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid session_id: %w", err)))
		return
	}
	if len(req.Signatures) == 0 {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("signatures must not be empty")))
		return
	}

	ctx := r.Context()

	// Load and validate session.
	session, err := h.queries.GetSigningSession(ctx, repository.GetSigningSessionParams{
		ID:       sessionID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	// Expire check.
	if time.Now().After(session.ExpiresAt) {
		h.queries.UpdateSigningSessionStatus(ctx, repository.UpdateSigningSessionStatusParams{ //nolint:errcheck
			ID:     session.ID,
			Status: "expired",
		})
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusGone)
		json.NewEncoder(w).Encode(map[string]any{ //nolint:errcheck
			"error": map[string]any{"code": "SESSION_EXPIRED", "message": "signing session has expired"},
		})
		return
	}
	if session.Status == "completed" || session.Status == "failed" || session.Status == "expired" {
		respondError(w, &apperr.AppError{
			Code:    "SESSION_CLOSED",
			Status:  409,
			Message: fmt.Sprintf("session is already %s", session.Status),
		})
		return
	}

	// Process each signature.
	succeeded := 0
	failed := 0
	var results []completeDocResponse

	for _, sig := range req.Signatures {
		docID, err := uuid.Parse(sig.DocID)
		if err != nil {
			failed++
			results = append(results, completeDocResponse{DocID: sig.DocID, Status: "error", Error: "invalid doc_id UUID"})
			continue
		}
		if sig.CMS == "" {
			failed++
			results = append(results, completeDocResponse{DocID: sig.DocID, Status: "error", Error: "cms must not be empty"})
			continue
		}

		res, err := h.processSig(ctx, session, docID, sig.CMS)
		if err != nil {
			failed++
			results = append(results, completeDocResponse{DocID: sig.DocID, Status: "error", Error: err.Error()})
			continue
		}
		succeeded++
		results = append(results, *res)
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"succeeded": succeeded,
		"failed":    failed,
		"documents": results,
	})
}

// processSig handles a single signature in the complete request.
func (h *SignCompleteHandler) processSig(
	ctx context.Context,
	session repository.SigningSession,
	docID uuid.UUID,
	cmsBase64 string,
) (*completeDocResponse, error) {
	// 2. Load session document.
	doc, err := h.queries.GetSigningSessionDocument(ctx, docID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("document not found")
		}
		return nil, fmt.Errorf("load document: %w", err)
	}
	if doc.SessionID != session.ID {
		return nil, fmt.Errorf("document does not belong to this session")
	}
	if doc.Status != "ready" {
		return nil, fmt.Errorf("document status is %q, expected ready", doc.Status)
	}

	// 3. Decode CMS from base64.
	cmsBytes, err := base64.StdEncoding.DecodeString(cmsBase64)
	if err != nil {
		// Try URL-safe base64.
		cmsBytes, err = base64.URLEncoding.DecodeString(cmsBase64)
		if err != nil {
			return nil, fmt.Errorf("cms is not valid base64")
		}
	}

	// 4. Integrity check: extract messageDigest from CMS, compare to stored content_hash.
	cmsDigest, err := signer.ExtractHashFromCMS(cmsBytes)
	if err != nil {
		log.Printf("sign_complete: ExtractHashFromCMS doc=%s: %v (skipping integrity check)", doc.ID, err)
		// Non-fatal: NCANode verification will catch any forgery.
	} else {
		storedHashBytes, hexErr := hex.DecodeString(doc.ContentHash)
		if hexErr != nil {
			return nil, fmt.Errorf("stored content_hash is not valid hex")
		}
		if !bytesEqual(cmsDigest, storedHashBytes) {
			w := &hashMismatchError{}
			return nil, w
		}
	}

	// 5. Verify CMS via NCANode.
	vr, err := h.nc.VerifyCMS(ctx, cmsBase64, doc.ContentHash)
	if err != nil {
		switch {
		case errors.Is(err, ncanode.ErrCMSInvalid):
			return nil, &cmsInvalidError{msg: "CMS signature is invalid"}
		case errors.Is(err, ncanode.ErrCertRevoked):
			return nil, &cmsInvalidError{msg: "certificate is revoked"}
		default:
			return nil, fmt.Errorf("ncanode verify: %w", err)
		}
	}

	// 6. Download cached PDF from our MinIO.
	if !doc.CachedS3Key.Valid || doc.CachedS3Key.String == "" {
		return nil, fmt.Errorf("document has no cached_s3_key (fetch may not be complete)")
	}
	pdfBytes, err := h.store.DownloadFile(ctx, doc.CachedS3Key.String)
	if err != nil {
		return nil, fmt.Errorf("download cached pdf: %w", err)
	}

	// 7. Build signed PDF with QR stamp + sign page (same as service.Sign).
	signedPDF, err := h.buildSignedPDF(ctx, pdfBytes, cmsBase64, doc, session, vr)
	if err != nil {
		return nil, fmt.Errorf("build signed pdf: %w", err)
	}

	// 8. Store in our MinIO.
	signedKey := fmt.Sprintf("signed/%s/%s.pdf", session.ID, doc.ID)
	cmsKey := fmt.Sprintf("cms/%s.p7s", doc.ID)

	if err := h.store.UploadFile(ctx, signedKey, signedPDF, "application/pdf"); err != nil {
		return nil, fmt.Errorf("upload signed pdf: %w", err)
	}
	if err := h.store.UploadFile(ctx, cmsKey, cmsBytes, "application/pkcs7-signature"); err != nil {
		log.Printf("sign_complete: upload cms doc=%s: %v (non-fatal)", doc.ID, err)
	}

	// 9. Update DB.
	updatedDoc, err := h.queries.UpdateSessionDocumentAfterSign(ctx, repository.UpdateSessionDocumentAfterSignParams{
		ID:          doc.ID,
		CmsS3Key:    sql.NullString{String: cmsKey, Valid: true},
		SignedS3Key: sql.NullString{String: signedKey, Valid: true},
	})
	if err != nil {
		return nil, fmt.Errorf("update after sign: %w", err)
	}

	// 10. Upload to client S3 and dispatch webhook asynchronously.
	go h.uploadToClientS3(context.Background(), session, updatedDoc, signedPDF)

	s3Key := ""
	if updatedDoc.TargetS3Key.Valid {
		s3Key = updatedDoc.TargetS3Key.String
	}
	return &completeDocResponse{
		DocID:  doc.ID.String(),
		Name:   doc.DocumentName,
		Status: "signed",
		S3Key:  s3Key,
	}, nil
}

// buildSignedPDF applies QR stamp + sign page to the original PDF using the same
// mechanism as service.Sign (pdf.AddQRStamps + pdf.GenerateSignPage + pdf.ReplaceLastPage).
func (h *SignCompleteHandler) buildSignedPDF(
	ctx context.Context,
	pdfBytes []byte,
	cmsBase64 string,
	doc repository.SigningSessionDocument,
	session repository.SigningSession,
	vr *ncanode.VerifyResult,
) ([]byte, error) {
	// Compute document hash for the sign page display.
	sum := sha256.Sum256(pdfBytes)
	docHashHex := hex.EncodeToString(sum[:])

	// Generate QR pointing to a verify URL (we don't have a separate verify route for
	// session docs, so we use the CMS key as a stable identifier placeholder).
	qrURL := fmt.Sprintf("data:cms:%s", doc.ID)
	qrPNG, err := qr.GenerateQR(qrURL, qr.DefaultSize)
	if err != nil {
		return nil, fmt.Errorf("generate qr: %w", err)
	}

	sigInfo := pdf.SignatureInfo{
		SignerName:    vr.SignerName,
		OrgName:       vr.OrgName,
		BIN:           vr.SignerBIN,
		IIN:           vr.SignerIIN,
		Role:          session.SignerRole,
		SignerType:    vr.SignerType,
		Basis:         vr.Basis,
		CertSerial:    vr.CertSerial,
		CertNotBefore: vr.CertNotBefore,
		CertNotAfter:  vr.CertNotAfter,
		CAName:        vr.CAName,
		SignFormat:    vr.SignFormat,
		SHA256Hash:    docHashHex,
		Status:        "Подпись действительна",
		SignedAt:      vr.TSPTime,
		QRImagePNG:    qrPNG,
	}

	stamped, err := pdf.AddQRStamps(pdfBytes, []pdf.QRStamp{{
		SignerName: vr.SignerName,
		Role:       session.SignerRole,
		QRImagePNG: qrPNG,
	}})
	if err != nil {
		return nil, fmt.Errorf("add qr stamps: %w", err)
	}

	signPage, err := pdf.GenerateSignPage([]pdf.SignatureInfo{sigInfo})
	if err != nil {
		return nil, fmt.Errorf("generate sign page: %w", err)
	}

	finalPDF, err := pdf.ReplaceLastPage(stamped, signPage)
	if err != nil {
		return nil, fmt.Errorf("replace last page: %w", err)
	}
	return finalPDF, nil
}

// uploadToClientS3 uploads the signed PDF to the client's pre-signed target URL.
func (h *SignCompleteHandler) uploadToClientS3(
	ctx context.Context,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	signedPDF []byte,
) {
	if h.extS3 == nil || !doc.TargetUrl.Valid || doc.TargetUrl.String == "" {
		return
	}

	appID := ""
	if session.ApplicationID.Valid {
		appID = session.ApplicationID.String
	}

	signedAt := time.Now().UTC()
	if doc.SignedAt.Valid {
		signedAt = doc.SignedAt.Time
	}

	cmsKey := ""
	if doc.CmsS3Key.Valid {
		cmsKey = doc.CmsS3Key.String
	}

	meta := s3client.S3Metadata{
		ApplicationID:   appID,
		DocumentID:      doc.ID.String(),
		DocumentName:    doc.DocumentName,
		SignerRole:      session.SignerRole,
		SignedAt:        signedAt,
		SigningRound:    1,
		DocumentVersion: 1,
		CMSStorageKey:   cmsKey,
	}

	err := s3client.UploadWithRetry(ctx, h.extS3, doc.TargetUrl.String, signedPDF, "application/pdf", meta, 3)
	if err != nil {
		errMsg := err.Error()
		if errors.Is(err, s3client.ErrPresignedURLExpired) {
			errMsg = "presigned_url_expired"
		}
		log.Printf("sign_complete: upload to client S3 doc=%s: %v", doc.ID, err)
		h.queries.UpdateSessionDocumentStatus(ctx, repository.UpdateSessionDocumentStatusParams{ //nolint:errcheck
			ID:        doc.ID,
			Status:    "upload_failed",
			LastError: sql.NullString{String: errMsg, Valid: true},
		})
		h.checkSessionCompletion(ctx, session)
		return
	}

	h.queries.MarkSessionDocumentUploaded(ctx, doc.ID) //nolint:errcheck
	log.Printf("sign_complete: uploaded to client S3 doc=%s", doc.ID)

	callbackSecret := ""
	if session.CallbackSecret.Valid {
		callbackSecret = session.CallbackSecret.String
	}
	if session.CallbackUrl.Valid && session.CallbackUrl.String != "" {
		worker.CreateAndDispatchWebhook(ctx, h.queries, session.ID, "document_signed", map[string]any{ //nolint:errcheck
			"application_id": appID,
			"session_id":     session.ID.String(),
			"document_id":    doc.ID.String(),
			"document_name":  doc.DocumentName,
			"signer_role":    session.SignerRole,
			"signed_at":      signedAt.Format(time.RFC3339),
			"s3_key":         doc.TargetS3Key.String,
		}, callbackSecret)
	}

	h.checkSessionCompletion(ctx, session)
}

// checkSessionCompletion checks whether all documents in the session are
// uploaded/upload_failed and if so marks the session completed or failed.
func (h *SignCompleteHandler) checkSessionCompletion(ctx context.Context, session repository.SigningSession) {
	docs, err := h.queries.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		return
	}

	allDone := true
	anyFailed := false
	for _, d := range docs {
		switch d.Status {
		case "uploaded", "upload_failed":
			if d.Status == "upload_failed" {
				anyFailed = true
			}
		default:
			allDone = false
		}
	}
	if !allDone {
		return
	}

	newStatus := "completed"
	eventType := "session_completed"
	if anyFailed {
		newStatus = "failed"
		eventType = "session_failed"
	}

	h.queries.UpdateSigningSessionStatus(ctx, repository.UpdateSigningSessionStatusParams{ //nolint:errcheck
		ID:     session.ID,
		Status: newStatus,
	})

	callbackSecret := ""
	if session.CallbackSecret.Valid {
		callbackSecret = session.CallbackSecret.String
	}
	appID := ""
	if session.ApplicationID.Valid {
		appID = session.ApplicationID.String
	}

	if session.CallbackUrl.Valid && session.CallbackUrl.String != "" {
		worker.CreateAndDispatchWebhook(ctx, h.queries, session.ID, eventType, map[string]any{ //nolint:errcheck
			"application_id": appID,
			"session_id":     session.ID.String(),
			"status":         newStatus,
			"timestamp":      time.Now().UTC().Format(time.RFC3339),
		}, callbackSecret)
	}
}

// --- sentinel error types for typed HTTP responses ---

type hashMismatchError struct{}

func (e *hashMismatchError) Error() string { return "hash_mismatch: CMS signature does not match document" }

type cmsInvalidError struct{ msg string }

func (e *cmsInvalidError) Error() string { return "invalid_cms: " + e.msg }

// bytesEqual compares two byte slices in constant time.
func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
