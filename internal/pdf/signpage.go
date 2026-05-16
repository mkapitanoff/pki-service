package pdf

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

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
		return "-"
	}
	return t.Format("02.01.2006, 15:04:05")
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.Format("02.01.2006")
}

// cyrTranslit maps Cyrillic runes to Latin equivalents for PDF output.
// pdfcpu standard fonts (Helvetica, Courier) are Latin-1 only.
var cyrTranslit = map[rune]string{
	'А': "A", 'а': "a", 'Б': "B", 'б': "b", 'В': "V", 'в': "v",
	'Г': "G", 'г': "g", 'Д': "D", 'д': "d", 'Е': "E", 'е': "e",
	'Ё': "YO", 'ё': "yo", 'Ж': "ZH", 'ж': "zh", 'З': "Z", 'з': "z",
	'И': "I", 'и': "i", 'Й': "J", 'й': "j", 'К': "K", 'к': "k",
	'Л': "L", 'л': "l", 'М': "M", 'м': "m", 'Н': "N", 'н': "n",
	'О': "O", 'о': "o", 'П': "P", 'п': "p", 'Р': "R", 'р': "r",
	'С': "S", 'с': "s", 'Т': "T", 'т': "t", 'У': "U", 'у': "u",
	'Ф': "F", 'ф': "f", 'Х': "KH", 'х': "kh", 'Ц': "TS", 'ц': "ts",
	'Ч': "CH", 'ч': "ch", 'Ш': "SH", 'ш': "sh", 'Щ': "SHCH", 'щ': "shch",
	'Ъ': "", 'ъ': "", 'Ы': "Y", 'ы': "y", 'Ь': "", 'ь': "",
	'Э': "E", 'э': "e", 'Ю': "YU", 'ю': "yu", 'Я': "YA", 'я': "ya",
	// Kazakh specific
	'Ә': "A", 'ә': "a", 'Ғ': "G", 'ғ': "g", 'Қ': "K", 'қ': "k",
	'Ң': "N", 'ң': "n", 'Ө': "O", 'ө': "o", 'Ұ': "U", 'ұ': "u",
	'Ү': "U", 'ү': "u", 'Һ': "H", 'һ': "h", 'І': "I", 'і': "i",
}

// translit converts a string: replaces Cyrillic with Latin equivalents,
// passes ASCII and other printable characters through unchanged.
func translit(s string) string {
	var b strings.Builder
	for _, r := range s {
		if lat, ok := cyrTranslit[r]; ok {
			b.WriteString(lat)
		} else if r > unicode.MaxASCII && !unicode.IsSpace(r) && !unicode.IsPunct(r) && !unicode.IsNumber(r) {
			// unknown non-ASCII, non-space — skip to avoid PDF encoding issues
		} else {
			b.WriteRune(r)
		}
	}
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

// signPageLines returns text lines for the sign page, all transliterated to Latin.
func signPageLines(signatures []SignatureInfo) []string {
	var lines []string
	lines = append(lines, "LYST PODPISEJ / LIST OF SIGNATURES")
	lines = append(lines, "")
	for i, s := range signatures {
		if i > 0 {
			lines = append(lines, "------------------------------------------------")
			lines = append(lines, "")
		}
		lines = append(lines, fmt.Sprintf("DOKUMENT PODPISAN ECP (%d)", i+1))
		lines = append(lines, fmt.Sprintf("Data podpisanija:  %s", formatTS(s.SignedAt)))
		if s.OrgName != "" {
			lines = append(lines, fmt.Sprintf("Organizacija:      %s", translit(s.OrgName)))
		}
		if s.BIN != "" {
			lines = append(lines, fmt.Sprintf("BIN:               %s", s.BIN))
		}
		lines = append(lines, fmt.Sprintf("Podpisant:         %s", translit(s.SignerName)))
		if s.IIN != "" {
			lines = append(lines, fmt.Sprintf("IIN:               %s", MaskIIN(s.IIN)))
		}
		lines = append(lines, fmt.Sprintf("Tip:               %s", translit(s.SignerType)))
		if s.Basis != "" {
			lines = append(lines, fmt.Sprintf("Osnovanie:         %s", translit(s.Basis)))
		}
		lines = append(lines, "")
		lines = append(lines, "SERTIFIKAT / CERTIFICATE")
		lines = append(lines, fmt.Sprintf("UC:                %s", translit(s.CAName)))
		lines = append(lines, fmt.Sprintf("Nomer:             %s", TruncateCertSerial(s.CertSerial)))
		lines = append(lines, fmt.Sprintf("Dejstvitelen:      %s - %s",
			formatDate(s.CertNotBefore), formatDate(s.CertNotAfter)))
		lines = append(lines, "")
		lines = append(lines, "PODPIS / SIGNATURE")
		lines = append(lines, fmt.Sprintf("Format:            %s", s.SignFormat))
		lines = append(lines, fmt.Sprintf("SHA-256:           %s", TruncateSHA256(s.SHA256Hash)))
		lines = append(lines, fmt.Sprintf("Status:            %s", translit(s.Status)))
	}
	return lines
}

// GenerateSignPage renders a single PDF page listing all signatures.
// Each line is stamped as a separate watermark for reliable positioning.
// All text is transliterated to Latin because pdfcpu built-in fonts are Latin-1 only.
func GenerateSignPage(signatures []SignatureInfo) ([]byte, error) {
	conf := model.NewDefaultConfiguration()

	var base bytes.Buffer
	if err := api.Create(nil, strings.NewReader(blankPagesJSON(1)), &base, conf); err != nil {
		return nil, fmt.Errorf("pdf: create blank sign page: %w", err)
	}

	lines := signPageLines(signatures)

	const (
		fontPt     = 9
		lineH      = 13 // pt between lines
		topMargin  = 45
		leftMargin = 40
	)

	cur := base.Bytes()
	lineIdx := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			lineIdx++
			continue
		}
		yOff := topMargin + lineIdx*lineH
		desc := fmt.Sprintf(
			"font:Courier, points:%d, scale:1 abs, pos:tl, off:%d -%d, rot:0, fillc:#000000, opacity:1",
			fontPt, leftMargin, yOff,
		)
		wm, err := pdfcpu.ParseTextWatermarkDetails(line, desc, true, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("pdf: parse sign-page line: %w", err)
		}
		var out bytes.Buffer
		if err := api.AddWatermarks(bytes.NewReader(cur), &out, nil, wm, conf); err != nil {
			return nil, fmt.Errorf("pdf: stamp sign-page line: %w", err)
		}
		cur = out.Bytes()
		lineIdx++
	}

	return cur, nil
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
