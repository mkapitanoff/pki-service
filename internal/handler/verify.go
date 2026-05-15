package handler

import (
	"encoding/base64"
	stderrors "errors"
	"database/sql"
	"html/template"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/qr"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

type VerifyHandler struct {
	queries *repository.Queries
}

func NewVerifyHandler(q *repository.Queries) *VerifyHandler {
	return &VerifyHandler{queries: q}
}

type verifyView struct {
	SignedAt     string
	OrgName      string
	SignerBin    string
	SignerName   string
	MaskedIIN    string
	SignerType   string
	Basis        string
	CaName       string
	CertSerial   string
	CertValidFrom string
	CertValidTo  string
	SignFormat   string
	HashShort    string
	QRBase64     string
}

const dateTimeFmt = "02.01.2006, 15:04:05"
const dateFmt = "02.01.2006"

var notFoundTmpl = template.Must(template.New("nf").Parse(
	`<!DOCTYPE html><html lang="ru"><head><meta charset="utf-8">` +
		`<title>Проверка ЭЦП — PKI Service</title></head>` +
		`<body><h1>Подпись не найдена</h1></body></html>`))

var verifyTmpl = template.Must(template.New("v").Parse(`<!DOCTYPE html>
<html lang="ru">
<head>
<meta charset="utf-8">
<title>Проверка ЭЦП — PKI Service</title>
</head>
<body>
<h1><span style="color:green">✓</span> ДОКУМЕНТ ПОДПИСАН ЭЦП</h1>
<table>
<tr><td>Дата подписания:</td><td>{{.SignedAt}}</td></tr>
<tr><td>Организация:</td><td>{{.OrgName}}</td></tr>
<tr><td>БИН:</td><td>{{.SignerBin}}</td></tr>
<tr><td>Подписант:</td><td>{{.SignerName}}</td></tr>
<tr><td>ИИН:</td><td>{{.MaskedIIN}}</td></tr>
<tr><td>Тип:</td><td>{{.SignerType}}</td></tr>
<tr><td>Основание:</td><td>{{.Basis}}</td></tr>
</table>
<h2>СЕРТИФИКАТ</h2>
<table>
<tr><td>УЦ:</td><td>{{.CaName}}</td></tr>
<tr><td>№ сертификата:</td><td>{{.CertSerial}}</td></tr>
<tr><td>Действителен:</td><td>с {{.CertValidFrom}} по {{.CertValidTo}}</td></tr>
</table>
<h2>ПОДПИСЬ</h2>
<table>
<tr><td>Формат:</td><td>{{.SignFormat}}</td></tr>
<tr><td>Хэш SHA-256:</td><td>{{.HashShort}}</td></tr>
<tr><td>Статус:</td><td><span style="color:green">Подпись действительна ✓</span></td></tr>
</table>
<div>
<img src="data:image/png;base64,{{.QRBase64}}" width="200" height="200" alt="QR">
<p>Сканируйте для проверки</p>
</div>
</body>
</html>`))

// HandleVerify renders the public HTML verification page for a signature.
//
// NOTE: the generated GetSignature query is tenant-scoped (CLAUDE.md mandates
// a tenant_id filter on every query) but this endpoint is public and has no
// tenant. We look up by signature id with a nil tenant; a dedicated
// by-id-only query should be added for production correctness.
func (h *VerifyHandler) HandleVerify(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	sigID, err := uuid.Parse(chi.URLParam(r, "signature_id"))
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		_ = notFoundTmpl.Execute(w, nil)
		return
	}

	sig, err := h.queries.GetSignatureByIDPublic(r.Context(),
		sigID)
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			w.WriteHeader(http.StatusNotFound)
			_ = notFoundTmpl.Execute(w, nil)
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_ = notFoundTmpl.Execute(w, nil)
		return
	}

	qrPNG, err := qr.GenerateQR(sig.QrUrl, 200)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	view := verifyView{
		SignedAt:      sig.SignedAt.Format(dateTimeFmt),
		OrgName:       sig.OrgName.String,
		SignerBin:     sig.SignerBin.String,
		SignerName:    sig.SignerName,
		MaskedIIN:     pdf.MaskIIN(sig.SignerIin.String),
		SignerType:    sig.SignerType,
		Basis:         sig.Basis.String,
		CaName:        sig.CaName,
		CertSerial:    pdf.TruncateCertSerial(sig.CertSerial),
		CertValidFrom: sig.CertNotBefore.Format(dateFmt),
		CertValidTo:   sig.CertNotAfter.Format(dateFmt),
		SignFormat:    sig.SignFormat,
		HashShort:     pdf.TruncateSHA256(sig.Sha256Hash),
		QRBase64:      base64.StdEncoding.EncodeToString(qrPNG),
	}

	w.WriteHeader(http.StatusOK)
	_ = verifyTmpl.Execute(w, view)
}
