// Package postprocess завершает подписание документа в фоне: QR-штамп +
// Лист подписей + upload — после того как /sign/complete уже синхронно
// подтвердил CMS через NCANode и вернул пользователю "Подписан".
//
// См. план: /Users/user/.claude/plans/synthetic-launching-blanket.md
package postprocess

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/qr"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// buildSignedPDF накладывает QR-штамп и пересобирает Лист подписей.
//
// В отличие от прежней handler-локальной версии, данные о подписанте берутся
// не из транзиентного ncanode.VerifyResult (которого у воркера нет — он может
// запуститься при повторной обработке после падения процесса), а из уже
// persisted колонок signing_session_documents, записанных синхронно в
// /sign/complete через UpdateSessionDocumentSignerInfo до постановки джобы в
// очередь. Это и есть ключевое условие идемпотентности воркера.
func buildSignedPDF(pdfBytes []byte, doc repository.SigningSessionDocument, signerRole string) ([]byte, error) {
	qrURL := ""
	if doc.QrUrl.Valid {
		qrURL = doc.QrUrl.String
	}
	if qrURL == "" {
		return nil, fmt.Errorf("document has no qr_url persisted")
	}

	sum := sha256.Sum256(pdfBytes)
	docHashHex := hex.EncodeToString(sum[:])

	qrPNG, err := qr.GenerateQR(qrURL, qr.DefaultSize)
	if err != nil {
		return nil, fmt.Errorf("generate qr: %w", err)
	}

	sigInfo := pdf.SignatureInfo{
		SignerName:    nullStr(doc.SignerName),
		OrgName:       nullStr(doc.OrgName),
		BIN:           nullStr(doc.SignerBin),
		IIN:           nullStr(doc.SignerIin),
		Role:          signerRole,
		SignerType:    nullStr(doc.SignerType),
		Basis:         nullStr(doc.Basis),
		CertSerial:    nullStr(doc.CertSerial),
		CertNotBefore: nullTime(doc.CertNotBefore),
		CertNotAfter:  nullTime(doc.CertNotAfter),
		CAName:        nullStr(doc.CaName),
		SignFormat:    nullStr(doc.SignFormat),
		SHA256Hash:    docHashHex,
		Status:        "Подпись действительна",
		SignedAt:      nullTime(doc.TspTime),
		QRImagePNG:    qrPNG,
	}

	stamped, err := pdf.AddQRStamps(pdfBytes, []pdf.QRStamp{{
		SignerName: nullStr(doc.SignerName),
		Role:       signerRole,
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

func nullStr(s sql.NullString) string {
	if s.Valid {
		return s.String
	}
	return ""
}

func nullTime(t sql.NullTime) time.Time {
	if t.Valid {
		return t.Time
	}
	return time.Time{}
}
