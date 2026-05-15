package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	stderrors "errors"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/sqlc-dev/pqtype"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/qr"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// EventPublisher publishes domain events (RabbitMQ in prod). Nil-safe: a nil
// publisher skips step 19.
type EventPublisher interface {
	Publish(ctx context.Context, event string, payload any) error
}

type SignService struct {
	db            *sql.DB
	ncanode       ncanode.NCANodeClient
	storage       storage.Storage
	queries       *repository.Queries
	publisher     EventPublisher
	verifyBaseURL string
}

func NewSignService(
	db *sql.DB,
	nc ncanode.NCANodeClient,
	st storage.Storage,
	q *repository.Queries,
	publisher EventPublisher,
	verifyBaseURL string,
) *SignService {
	return &SignService{
		db:            db,
		ncanode:       nc,
		storage:       st,
		queries:       q,
		publisher:     publisher,
		verifyBaseURL: verifyBaseURL,
	}
}

type SignInput struct {
	DocumentID uuid.UUID
	TenantID   uuid.UUID
	CMS        string
	Role       string
}

type SignResult struct {
	SignatureID       uuid.UUID
	SignedDocumentURL string
	Signature         repository.Signature
}

func nullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// Sign implements the 20-step POST /api/v1/documents/:id/sign flow.
func (s *SignService) Sign(ctx context.Context, input SignInput) (*SignResult, error) {
	// 1. tenant_id is resolved by auth middleware and passed in input.

	// 2. SELECT document WHERE id AND tenant_id.
	doc, err := s.queries.GetDocument(ctx, repository.GetDocumentParams{
		ID:       input.DocumentID,
		TenantID: input.TenantID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apperr.ErrDocumentNotFound
		}
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 3. Download current PDF (step 14: original when v0 — s3_key_current
	//    already points at original for v0).
	pdfBytes, err := s.storage.DownloadFile(ctx, doc.S3KeyCurrent)
	if err != nil {
		if stderrors.Is(err, storage.ErrNotFound) {
			return nil, apperr.ErrDocumentNotFound.WithCause(err)
		}
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 4. SHA-256 of the PDF.
	sum := sha256.Sum256(pdfBytes)
	docSHA256 := hex.EncodeToString(sum[:])

	// 5. Verify CMS. 6. Extract cert data. 7. Revoked -> 422.
	vr, err := s.ncanode.VerifyCMS(ctx, input.CMS, docSHA256)
	if err != nil {
		switch {
		case stderrors.Is(err, ncanode.ErrCMSInvalid):
			return nil, apperr.ErrCMSInvalid.WithCause(err)
		case stderrors.Is(err, ncanode.ErrCertRevoked):
			return nil, apperr.ErrCertRevoked.WithCause(err)
		default:
			return nil, apperr.ErrInternal.WithCause(err)
		}
	}
	if !vr.Valid {
		return nil, apperr.ErrCMSInvalid
	}
	if vr.OCSPStatus == ncanode.OCSPStatusRevoked {
		return nil, apperr.ErrCertRevoked
	}

	// 8. Timestamp.
	tspTime, err := s.ncanode.GetTSP(ctx, docSHA256)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 9. sequence_num = count(existing) + 1.
	existing, err := s.queries.GetSignaturesByDocument(ctx, repository.GetSignaturesByDocumentParams{
		DocumentID: input.DocumentID,
		TenantID:   input.TenantID,
	})
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	sequenceNum := int32(len(existing) + 1)
	newVersion := doc.CurrentVersion + 1

	// 10. New signature id. 11. qr_url.
	newSignatureID := uuid.New()
	qrURL := fmt.Sprintf("%s/verify/%s", s.verifyBaseURL, newSignatureID)

	// 12. QR image.
	qrPNG, err := qr.GenerateQR(qrURL, qr.DefaultSize)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 13. All signatures (1..N-1) + new, for the sign page.
	sigInfos := make([]pdf.SignatureInfo, 0, len(existing)+1)
	for _, e := range existing {
		info := signatureToInfo(e)
		// Regenerate the QR for previously-stored signatures so step 15a
		// (QR stamps of all signers) has an image to place.
		prevQR, qerr := qr.GenerateQR(e.QrUrl, qr.DefaultSize)
		if qerr != nil {
			return nil, apperr.ErrInternal.WithCause(qerr)
		}
		info.QRImagePNG = prevQR
		sigInfos = append(sigInfos, info)
	}
	newInfo := pdf.SignatureInfo{
		SignerName:    vr.SignerName,
		OrgName:       vr.OrgName,
		BIN:           vr.SignerBIN,
		IIN:           vr.SignerIIN,
		SignerType:    vr.SignerType,
		Basis:         vr.Basis,
		CertSerial:    vr.CertSerial,
		CertNotBefore: vr.CertNotBefore,
		CertNotAfter:  vr.CertNotAfter,
		CAName:        vr.CAName,
		SignFormat:    vr.SignFormat,
		SHA256Hash:    docSHA256,
		Status:        "Подпись действительна",
		SignedAt:      tspTime,
		QRImagePNG:    qrPNG,
	}
	sigInfos = append(sigInfos, newInfo)

	// 15. Regenerate PDF: QR stamps on every page + replace sign page.
	stamps := make([]pdf.QRStamp, 0, len(sigInfos))
	for i := range sigInfos {
		stamps = append(stamps, pdf.QRStamp{
			SignerName: sigInfos[i].SignerName,
			Role:       input.Role,
			QRImagePNG: sigInfos[i].QRImagePNG,
		})
	}
	stamped, err := pdf.AddQRStamps(pdfBytes, stamps)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	signPage, err := pdf.GenerateSignPage(sigInfos)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	finalPDF, err := pdf.ReplaceLastPage(stamped, signPage)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 16. New s3 key.
	newKey := s.storage.BuildKey(input.TenantID, input.DocumentID,
		fmt.Sprintf("v%d.pdf", newVersion))

	// 17. Upload new PDF.
	if err := s.storage.UploadFile(ctx, newKey, finalPDF, "application/pdf"); err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	newStatus := nextStatus(doc, sequenceNum)

	// 18. Transaction: signature + version + document + audit.
	var createdSig repository.Signature
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	qtx := s.queries.WithTx(tx)

	// 18a. INSERT signature (sqlc, explicit precomputed id).
	createdSig, err = qtx.CreateSignatureWithID(ctx, repository.CreateSignatureWithIDParams{
		ID:            newSignatureID,
		DocumentID:    input.DocumentID,
		TenantID:      input.TenantID,
		VersionNumber: newVersion,
		SequenceNum:   sequenceNum,
		CmsB64:        input.CMS,
		Role:          input.Role,
		SignerIin:     nullString(vr.SignerIIN),
		SignerName:    vr.SignerName,
		SignerBin:     nullString(vr.SignerBIN),
		OrgName:       nullString(vr.OrgName),
		SignerType:    vr.SignerType,
		Basis:         nullString(vr.Basis),
		CertSerial:    vr.CertSerial,
		CertNotBefore: vr.CertNotBefore,
		CertNotAfter:  vr.CertNotAfter,
		CaName:        vr.CAName,
		OcspStatus:    repository.OcspStatusType(vr.OCSPStatus),
		OcspCheckedAt: vr.OCSPCheckedAt,
		TspTime:       sql.NullTime{Time: tspTime, Valid: !tspTime.IsZero()},
		Sha256Hash:    docSHA256,
		SignFormat:    vr.SignFormat,
		QrUrl:         qrURL,
	})
	if err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 18b. INSERT document_versions (sqlc).
	if _, err = qtx.CreateDocumentVersion(ctx, repository.CreateDocumentVersionParams{
		DocumentID: input.DocumentID,
		TenantID:   input.TenantID,
		Version:    newVersion,
		S3Key:      newKey,
	}); err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 18c. UPDATE document (sqlc).
	if _, err = qtx.UpdateDocumentVersion(ctx, repository.UpdateDocumentVersionParams{
		ID:             input.DocumentID,
		TenantID:       input.TenantID,
		S3KeyCurrent:   newKey,
		CurrentVersion: newVersion,
		Status:         newStatus,
	}); err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	// 18d. INSERT audit_log (sqlc).
	auditMeta, _ := json.Marshal(map[string]any{
		"signature_id": newSignatureID,
		"version":      newVersion,
		"role":         input.Role,
	})
	if err = qtx.CreateAuditLog(ctx, repository.CreateAuditLogParams{
		TenantID:   input.TenantID,
		Action:     "signature.added",
		EntityType: "document",
		EntityID:   uuid.NullUUID{UUID: input.DocumentID, Valid: true},
		Meta:       pqtype.NullRawMessage{RawMessage: auditMeta, Valid: len(auditMeta) > 0},
	}); err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}

	if err = tx.Commit(); err != nil {
		return nil, apperr.ErrInternal.WithCause(err)
	}
	committed = true

	// 19. Publish event (best-effort).
	if s.publisher != nil {
		_ = s.publisher.Publish(ctx, "signature.added", map[string]any{
			"document_id":  input.DocumentID,
			"signature_id": createdSig.ID,
			"tenant_id":    input.TenantID,
			"signer_name":  createdSig.SignerName,
			"signed_at":    createdSig.SignedAt,
		})
	}

	// 20. Respond.
	return &SignResult{
		SignatureID:       createdSig.ID,
		SignedDocumentURL: newKey,
		Signature:         createdSig,
	}, nil
}

func signatureToInfo(s repository.Signature) pdf.SignatureInfo {
	tsp := s.TspTime.Time
	return pdf.SignatureInfo{
		SignerName:    s.SignerName,
		OrgName:       s.OrgName.String,
		BIN:           s.SignerBin.String,
		IIN:           s.SignerIin.String,
		SignerType:    s.SignerType,
		Basis:         s.Basis.String,
		CertSerial:    s.CertSerial,
		CertNotBefore: s.CertNotBefore,
		CertNotAfter:  s.CertNotAfter,
		CAName:        s.CaName,
		SignFormat:    s.SignFormat,
		SHA256Hash:    s.Sha256Hash,
		Status:        "Подпись действительна",
		SignedAt:      tsp,
	}
}

// nextStatus implements DRAFT/PENDING -> PARTIALLY_SIGNED -> SIGNED. When the
// document metadata carries {"expected_signatures": N}, the document becomes
// SIGNED once sequenceNum >= N; otherwise it stays PARTIALLY_SIGNED.
func nextStatus(doc repository.Document, sequenceNum int32) repository.DocStatus {
	expected := 0
	if doc.Metadata.Valid {
		var m struct {
			ExpectedSignatures int `json:"expected_signatures"`
		}
		if err := json.Unmarshal(doc.Metadata.RawMessage, &m); err == nil {
			expected = m.ExpectedSignatures
		}
	}
	if expected > 0 && int(sequenceNum) >= expected {
		return repository.DocStatusSigned
	}
	return repository.DocStatusPartiallySigned
}
