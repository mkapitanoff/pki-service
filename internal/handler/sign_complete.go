package handler

import (
	"context"
	"database/sql"
	"encoding/base64"
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
	"github.com/mkapitanoff/pki-service/internal/reqctx"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/signer"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// SignCompleteHandler handles POST /api/v1/sign/complete. It only performs
// the fast, synchronous part of signing (CMS verification + signer-info
// persist); QR-stamping, sign-page regeneration and the client-S3 upload run
// in the background — see internal/postprocess and internal/worker's
// PostprocessWorker.
type SignCompleteHandler struct {
	queries                 *repository.Queries
	nc                      ncanode.NCANodeClient
	store                   storage.Storage
	verificationEnabled     bool          // фиче-флаг async-сверки
	verificationInitialWait time.Duration // задержка перед первой попыткой
	verifyBaseURL           string        // базовый URL для QR/verify-ссылки, напр. https://pki.fin4b.kz
}

func NewSignCompleteHandler(
	queries *repository.Queries,
	nc ncanode.NCANodeClient,
	store storage.Storage,
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

	// 5. Verify CMS via NCANode — юридически значимая проверка подписи,
	// остаётся синхронной (см. план: /Users/user/.claude/plans/synthetic-launching-blanket.md).
	vr, err := h.nc.VerifyCMS(ctx, cmsBase64, contentHashHex)
	if err != nil {
		switch {
		case errors.Is(err, ncanode.ErrCMSInvalid):
			return nil, &cmsInvalidError{msg: "CMS signature is invalid"}
		case errors.Is(err, ncanode.ErrCertRevoked):
			return nil, &cmsInvalidError{msg: "certificate is revoked"}
		case errors.Is(err, ncanode.ErrCertStatusUnknown):
			return nil, &cmsInvalidError{msg: "certificate revocation status could not be determined (OCSP unavailable)"}
		case errors.Is(err, ncanode.ErrCertInvalidUsage):
			return nil, &cmsInvalidError{msg: "certificate key usage does not permit signing (nonRepudiation required)"}
		default:
			return nil, fmt.Errorf("ncanode verify: %w", err)
		}
	}

	// 6. Персист signer/cert-полей + реального verify-URL. Это последнее, что
	// нужно постпроцессингу для сборки Листа подписей — воркер читает эти
	// колонки из БД, а не из транзиентного vr (которого у него нет).
	qrURL := fmt.Sprintf("%s/verify/%s", h.verifyBaseURL, doc.ID)
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
		return nil, fmt.Errorf("persist signer info: %w", err)
	}

	// 7. CMS-архив — маленький объект, не PDF-рендер, дёшево оставить
	// синхронным. cms_s3_key пишется вместе с флипом в 'signed' ниже, чтобы
	// воркер постпроцессинга никогда не перезаливал и не перевызывал NCANode.
	cmsKey := fmt.Sprintf("cms/%s.p7s", doc.ID)
	if err := h.store.UploadFile(ctx, cmsKey, cmsBytes, "application/pkcs7-signature"); err != nil {
		return nil, fmt.Errorf("upload cms archive: %w", err)
	}

	// 8. Документ юридически подписан прямо сейчас — фиксируем это и ставим
	// джобу постпроцессинга (QR-штамп + Лист подписей + upload клиенту) в
	// очередь. PostprocessWorker (тикер, internal/worker) подхватит её сам —
	// без явной публикации, тот же паттерн, что и VerificationWorker.
	if _, err := h.queries.MarkSessionDocumentSigned(ctx, repository.MarkSessionDocumentSignedParams{
		ID:       doc.ID,
		CmsS3Key: sql.NullString{String: cmsKey, Valid: true},
	}); err != nil {
		return nil, fmt.Errorf("mark signed: %w", err)
	}

	// 8b. Внутренняя async-сверка (verification_status) — отдельный, не
	// связанный с постпроцессингом механизм, оставляем как есть.
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

	// 9. Ответ: документ подписан, но артефакт (проштампованный PDF) ещё
	// готовится в фоне — s3_key намеренно не отдаём, клиент обязан поллить
	// /sign/status и ждать state=UPLOADED перед показом кнопки скачивания.
	return &completeDocResponse{
		DocID:  doc.ID.String(),
		Name:   doc.DocumentName,
		Status: "signed",
	}, nil
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
