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
	"strings"
	"time"

	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/qr"
	"github.com/mkapitanoff/pki-service/internal/reqctx"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/signer"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// SignCompleteHandler handles POST /api/v1/sign/complete.
type SignCompleteHandler struct {
	queries                 *repository.Queries
	nc                      ncanode.NCANodeClient
	store                   storage.Storage
	extS3                   s3client.ExternalS3Client
	verificationEnabled     bool          // фиче-флаг async-сверки
	verificationInitialWait time.Duration // задержка перед первой попыткой
	verifyBaseURL           string        // базовый URL для QR/verify-ссылки, напр. https://pki.fin4b.kz
}

func NewSignCompleteHandler(
	queries *repository.Queries,
	nc ncanode.NCANodeClient,
	store storage.Storage,
	extS3 s3client.ExternalS3Client,
	verificationEnabled bool,
	verificationInitialWait time.Duration,
	verifyBaseURL string,
) *SignCompleteHandler {
	if verificationInitialWait <= 0 {
		verificationInitialWait = 60 * time.Second
	}
	return &SignCompleteHandler{
		queries:                 queries,
		nc:                      nc,
		store:                   store,
		extS3:                   extS3,
		verificationEnabled:     verificationEnabled,
		verificationInitialWait: verificationInitialWait,
		verifyBaseURL:           verifyBaseURL,
	}
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
		respondErrorReq(w, r, apperr.ErrUnauthorized)
		return
	}

	var req completeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondErrorReq(w, r, apperr.ErrInvalidRequest)
		return
	}

	sessionID, err := uuid.Parse(req.SessionID)
	if err != nil {
		respondErrorReq(w, r, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid session_id: %w", err)))
		return
	}
	if len(req.Signatures) == 0 {
		respondErrorReq(w, r, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("signatures must not be empty")))
		return
	}

	ctx := r.Context()
	rid := reqctx.RequestID(ctx)
	log.Printf("complete.start request_id=%s tenant_id=%s session_id=%s signatures=%d",
		rid, tenantID, sessionID, len(req.Signatures))

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
			// Раньше per-doc причина падения уходила только в тело ответа
			// (documents[].error) и в stdout не писалась — диагностировать провал
			// complete по логам Chandra было нельзя. Логируем причину здесь, чтобы
			// не зависеть от коротких edge-логов клиента.
			log.Printf("complete.doc_failed request_id=%s session_id=%s doc_id=%s: %v",
				rid, sessionID, sig.DocID, err)
			results = append(results, completeDocResponse{DocID: sig.DocID, Status: "error", Error: err.Error()})
			continue
		}
		succeeded++
		results = append(results, *res)
	}

	log.Printf("complete.done request_id=%s session_id=%s succeeded=%d failed=%d",
		rid, sessionID, succeeded, failed)
	respondJSONReq(w, r, http.StatusOK, map[string]any{
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

	// 4. Integrity binding: подписанный eContent должен совпадать с хэшем нашего
	// документа. Это привязка «подпись ↔ именно этот PDF».
	//
	// SHADOW-режим: пока НЕ отклоняем — только логируем integrity.match /
	// integrity.mismatch / integrity.detached / integrity.extract_error. После
	// подтверждения на реальном трафике (0 ложных mismatch) отдельным шагом
	// проверка станет fatal (422). Крипто-безопасный поэтапный ввод контроля.
	if !doc.ContentHash.Valid || doc.ContentHash.String == "" {
		// На этапе complete content_hash должен быть выставлен либо клиентом
		// (hash_source='client'), либо фетчером. Если его нет — sanity fail.
		return nil, fmt.Errorf("document has no content_hash (fetch may not be complete)")
	}
	contentHashHex := strings.ToLower(doc.ContentHash.String)
	signedHashHex, attached, exErr := signer.ExtractSignedContentHash(cmsBytes)
	switch {
	case exErr != nil:
		log.Printf("integrity.extract_error doc=%s: %v (shadow, not enforced)", doc.ID, exErr)
	case !attached:
		// detached CMS — привязку обеспечивает NCANode через data (шаг 5).
		log.Printf("integrity.detached doc=%s (binding via ncanode)", doc.ID)
	case signedHashHex == contentHashHex:
		log.Printf("integrity.match doc=%s", doc.ID)
	default:
		log.Printf("integrity.mismatch doc=%s signed=%s content_hash=%s (shadow, not enforced)",
			doc.ID, signedHashHex, contentHashHex)
	}

	// 5. Verify CMS via NCANode.
	vr, err := h.nc.VerifyCMS(ctx, cmsBase64, contentHashHex)
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

	// 6. Получить оригинал PDF. Обычно он уже в кэше MinIO (положил фетчер).
	// Fast-path (client-hash): фоновый кэш мог не успеть к моменту /complete —
	// тогда докачиваем оригинал напрямую по source_url.
	var pdfBytes []byte
	if doc.CachedS3Key.Valid && doc.CachedS3Key.String != "" {
		b, derr := h.store.DownloadFile(ctx, doc.CachedS3Key.String)
		if derr != nil {
			return nil, fmt.Errorf("download cached pdf: %w", derr)
		}
		pdfBytes = b
	} else {
		if doc.SourceUrl == "" {
			return nil, fmt.Errorf("document has no cached_s3_key and no source_url")
		}
		b, _, derr := s3client.DownloadWithRetry(ctx, h.extS3, doc.SourceUrl, 3)
		if derr != nil {
			return nil, fmt.Errorf("on-demand download source pdf: %w", derr)
		}
		pdfBytes = b
	}

	// 7. Build signed PDF with QR stamp + sign page (same as service.Sign).
	qrURL := fmt.Sprintf("%s/verify/%s", h.verifyBaseURL, doc.ID)
	signedPDF, err := h.buildSignedPDF(ctx, pdfBytes, doc, session, vr, qrURL)
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

	// 9b. Персист signer/cert-полей + реального verify-URL — раньше vr
	// использовался только транзиентно для Листа подписей и терялся. Без
	// этого /verify/{doc.ID} не может отрендерить страницу верификации, а QR
	// в PDF указывал на нерабочий плейсхолдер. Non-fatal: PDF уже собран
	// корректно независимо от того, записалось ли это в БД.
	if _, err := h.queries.UpdateSessionDocumentSignerInfo(ctx, repository.UpdateSessionDocumentSignerInfoParams{
		ID:            doc.ID,
		SignerIin:     sql.NullString{String: vr.SignerIIN, Valid: vr.SignerIIN != ""},
		SignerName:    sql.NullString{String: vr.SignerName, Valid: vr.SignerName != ""},
		SignerBin:     sql.NullString{String: vr.SignerBIN, Valid: vr.SignerBIN != ""},
		OrgName:       sql.NullString{String: vr.OrgName, Valid: vr.OrgName != ""},
		SignerType:    sql.NullString{String: vr.SignerType, Valid: vr.SignerType != ""},
		Basis:         sql.NullString{String: vr.Basis, Valid: vr.Basis != ""},
		CertSerial:    sql.NullString{String: vr.CertSerial, Valid: vr.CertSerial != ""},
		CertNotBefore: sql.NullTime{Time: vr.CertNotBefore, Valid: !vr.CertNotBefore.IsZero()},
		CertNotAfter:  sql.NullTime{Time: vr.CertNotAfter, Valid: !vr.CertNotAfter.IsZero()},
		CaName:        sql.NullString{String: vr.CAName, Valid: vr.CAName != ""},
		OcspStatus:    sql.NullString{String: vr.OCSPStatus, Valid: vr.OCSPStatus != ""},
		TspTime:       sql.NullTime{Time: vr.TSPTime, Valid: !vr.TSPTime.IsZero()},
		SignFormat:    sql.NullString{String: vr.SignFormat, Valid: vr.SignFormat != ""},
		QrUrl:         sql.NullString{String: qrURL, Valid: true},
	}); err != nil {
		log.Printf("sign_complete: persist signer info doc=%s: %v (non-fatal)", doc.ID, err)
	}

	// 9a. Поставить документ в очередь async-верификации (внутренний контроль
	// целостности). Если воркер отключён — пропускаем; verification_status
	// остаётся NULL, наружу всё равно ничего не светится.
	if h.verificationEnabled {
		nextAt := time.Now().Add(h.verificationInitialWait)
		if err := h.queries.MarkSessionDocumentVerificationPending(ctx,
			repository.MarkSessionDocumentVerificationPendingParams{
				ID:                 doc.ID,
				VerificationNextAt: sql.NullTime{Time: nextAt, Valid: true},
			},
		); err != nil {
			log.Printf("sign_complete: mark verification pending doc=%s: %v (non-fatal)", doc.ID, err)
		}
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
	doc repository.SigningSessionDocument,
	session repository.SigningSession,
	vr *ncanode.VerifyResult,
	qrURL string,
) ([]byte, error) {
	// Compute document hash for the sign page display.
	sum := sha256.Sum256(pdfBytes)
	docHashHex := hex.EncodeToString(sum[:])

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

// uploadToClientS3 delegates to the shared package-level helper.
func (h *SignCompleteHandler) uploadToClientS3(
	ctx context.Context,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	signedPDF []byte,
) {
	uploadSessionDocToClientS3(ctx, h.queries, h.extS3, h.store, session, doc, signedPDF)
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
