package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// SignatureInfo is one rendered entry on the "Лист подписей" page. Fields
// mirror the format section in CLAUDE.md. Truncation/masking helpers below
// produce the displayed values.
type SignatureInfo struct {
	SignerName    string
	OrgName       string
	BIN           string
	IIN           string // raw IIN; rendered via MaskIIN
	SignerType    string
	Basis         string
	CertSerial    string // raw serial; rendered via TruncateCertSerial
	CertNotBefore time.Time
	CertNotAfter  time.Time
	CAName        string
	SignFormat    string
	SHA256Hash    string // raw hex; rendered via TruncateSHA256
	Status        string
	SignedAt      time.Time
	QRImagePNG    []byte
}

// MaskIIN masks an IIN as first 4 + "****" + last 4 (e.g. 123456789012 ->
// 1234****9012). Strings shorter than 8 chars are returned unchanged.
func MaskIIN(iin string) string {
	if len(iin) < 8 {
		return iin
	}
	return iin[:4] + "****" + iin[len(iin)-4:]
}

// TruncateCertSerial renders a certificate serial as first 4 + "..." + last 3,
// per the CLAUDE.md format section (example: 2F:5...3:91).
func TruncateCertSerial(serial string) string {
	if len(serial) <= 7 {
		return serial
	}
	return serial[:4] + "..." + serial[len(serial)-3:]
}

// TruncateSHA256 renders a hash as first 8 + "..." + last 8.
func TruncateSHA256(hash string) string {
	if len(hash) <= 16 {
		return hash
	}
	return hash[:8] + "..." + hash[len(hash)-8:]
}

func formatTS(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("02.01.2006, 15:04:05")
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("02.01.2006")
}

func renderSignatureBlock(s SignatureInfo, idx int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "✓ ДОКУМЕНТ ПОДПИСАН ЭЦП  (%d)\n", idx)
	fmt.Fprintf(&b, "Дата подписания:  %s\n", formatTS(s.SignedAt))
	if s.OrgName != "" {
		fmt.Fprintf(&b, "Организация:      %s\n", s.OrgName)
	}
	if s.BIN != "" {
		fmt.Fprintf(&b, "БИН:              %s\n", s.BIN)
	}
	fmt.Fprintf(&b, "Подписант:        %s\n", s.SignerName)
	fmt.Fprintf(&b, "ИИН:              %s\n", MaskIIN(s.IIN))
	fmt.Fprintf(&b, "Тип:              %s\n", s.SignerType)
	if s.Basis != "" {
		fmt.Fprintf(&b, "Основание:        %s\n", s.Basis)
	}
	b.WriteString("\nСЕРТИФИКАТ\n")
	fmt.Fprintf(&b, "УЦ:               %s\n", s.CAName)
	fmt.Fprintf(&b, "№ сертификата:    %s\n", TruncateCertSerial(s.CertSerial))
	fmt.Fprintf(&b, "Действителен:     с %s по %s\n",
		formatDate(s.CertNotBefore), formatDate(s.CertNotAfter))
	b.WriteString("\nПОДПИСЬ\n")
	fmt.Fprintf(&b, "Формат:           %s\n", s.SignFormat)
	fmt.Fprintf(&b, "Хэш SHA-256:      %s\n", TruncateSHA256(s.SHA256Hash))
	fmt.Fprintf(&b, "Статус:           %s\n", s.Status)
	return b.String()
}

// blankPagesJSON returns a pdfcpu "create" JSON describing n A4 pages, each
// carrying a single near-invisible space (pdfcpu requires page content).
func blankPagesJSON(n int) string {
	if n < 1 {
		n = 1
	}
	var b strings.Builder
	b.WriteString(`{"pages":{`)
	for i := 1; i <= n; i++ {
		if i > 1 {
			b.WriteByte(',')
		}
		fmt.Fprintf(&b, `"%d":{"content":{"text":[{"value":" ","pos":[40,40],"font":{"name":"Helvetica","size":1}}]}}`, i)
	}
	b.WriteString(`}}`)
	return b.String()
}

// GenerateSignPage renders a single PDF page listing all signatures in the
// CLAUDE.md "Лист подписей" format.
func GenerateSignPage(signatures []SignatureInfo) ([]byte, error) {
	conf := model.NewDefaultConfiguration()

	var base bytes.Buffer
	if err := api.Create(nil, strings.NewReader(blankPagesJSON(1)), &base, conf); err != nil {
		return nil, fmt.Errorf("pdf: create blank sign page: %w", err)
	}

	var text strings.Builder
	text.WriteString("ЛИСТ ПОДПИСЕЙ\n\n")
	if len(signatures) == 0 {
		text.WriteString("Подписи отсутствуют\n")
	}
	for i, s := range signatures {
		if i > 0 {
			text.WriteString("\n------------------------------\n\n")
		}
		text.WriteString(renderSignatureBlock(s, i+1))
	}

	wm, err := pdfcpu.ParseTextWatermarkDetails(
		text.String(),
		"font:Courier, points:8, scale:0.9 abs, pos:tl, off:40 -40, rot:0, fillc:#000000, opacity:1",
		true,
		types.POINTS,
	)
	if err != nil {
		return nil, fmt.Errorf("pdf: parse sign-page text: %w", err)
	}

	var out bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(base.Bytes()), &out, nil, wm, conf); err != nil {
		return nil, fmt.Errorf("pdf: stamp sign-page text: %w", err)
	}
	return out.Bytes(), nil
}

// ReplaceLastPage drops the last page of pdfBytes and appends newPageBytes
// (a single-page PDF). Resulting page count = original - 1 + 1.
func ReplaceLastPage(pdfBytes []byte, newPageBytes []byte) ([]byte, error) {
	conf := model.NewDefaultConfiguration()

	count, err := api.PageCount(bytes.NewReader(pdfBytes), conf)
	if err != nil {
		return nil, fmt.Errorf("pdf: page count: %w", err)
	}

	if count <= 1 {
		cp := make([]byte, len(newPageBytes))
		copy(cp, newPageBytes)
		return cp, nil
	}

	var trimmed bytes.Buffer
	keep := fmt.Sprintf("1-%d", count-1)
	if err := api.Trim(bytes.NewReader(pdfBytes), &trimmed, []string{keep}, conf); err != nil {
		return nil, fmt.Errorf("pdf: trim last page: %w", err)
	}

	var out bytes.Buffer
	rss := []io.ReadSeeker{
		bytes.NewReader(trimmed.Bytes()),
		bytes.NewReader(newPageBytes),
	}
	if err := api.MergeRaw(rss, &out, false, conf); err != nil {
		return nil, fmt.Errorf("pdf: merge new last page: %w", err)
	}
	return out.Bytes(), nil
}
