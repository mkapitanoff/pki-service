package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/font"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

const (
	cyrillicFont = "ArialUnicodeMS"
	fallbackFont = "Helvetica"
)

var (
	fontOnce       sync.Once
	activeFontName = fallbackFont
)

// ensureFont attempts to install Arial Unicode MS once per process.
// Uses pdfcpu's platform-default font directory (e.g. ~/Library/Application Support/pdfcpu/fonts on macOS).
// Falls back to Helvetica silently if the font file is not present.
func ensureFont() {
	fontOnce.Do(func() {
		// Ensure the font directory exists (pdfcpu may not create it automatically).
		if font.UserFontDir != "" {
			_ = os.MkdirAll(font.UserFontDir, 0o755)
		}

		const ttfPath = "/Library/Fonts/Arial Unicode.ttf"
		if _, err := os.Stat(ttfPath); err != nil {
			return // font not installed on this machine
		}
		if err := api.InstallFonts([]string{ttfPath}); err != nil {
			return
		}
		font.LoadUserFonts()
		for _, n := range font.UserFontNames() {
			if n == cyrillicFont {
				activeFontName = cyrillicFont
				return
			}
		}
	})
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

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// blankPagesJSON returns a pdfcpu "create" JSON describing n A4 pages.
func blankPagesJSON(n int) string {
	if n < 1 {
		n = 1
	}
	s := `{"pages":{`
	for i := 1; i <= n; i++ {
		if i > 1 {
			s += ","
		}
		s += fmt.Sprintf(`"%d":{"content":{"text":[{"value":" ","pos":[40,40],"font":{"name":"Helvetica","size":1}}]}}`, i)
	}
	s += `}}`
	return s
}

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

// line is a text line to stamp on the page with a Y offset.
type line struct {
	text string
	bold bool
}

// buildSignPageLines converts all signature infos into an ordered slice of
// text lines following the CLAUDE.md reference format.
func buildSignPageLines(sigs []SignatureInfo) []line {
	var lines []line

	add := func(text string, bold bool) {
		lines = append(lines, line{text, bold})
	}
	sep := func() { add("", false) }

	add("ЛИСТ ПОДПИСЕЙ", true)
	add("Электронные цифровые подписи документа", false)
	sep()

	for i, s := range sigs {
		if i > 0 {
			add("------------------------------------------------", false)
			sep()
		}
		add(fmt.Sprintf("✓ ДОКУМЕНТ ПОДПИСАН ЭЦП (%d)", i+1), true)
		add(fmt.Sprintf("Дата подписания:  %s", formatTS(s.SignedAt)), false)
		if s.OrgName != "" {
			add(fmt.Sprintf("Организация:      %s", s.OrgName), false)
		}
		if s.BIN != "" {
			add(fmt.Sprintf("БИН:              %s", s.BIN), false)
		}
		add(fmt.Sprintf("Подписант:        %s", s.SignerName), false)
		if s.IIN != "" {
			add(fmt.Sprintf("ИИН:              %s", MaskIIN(s.IIN)), false)
		}
		add(fmt.Sprintf("Тип:              %s", orDash(s.SignerType)), false)
		if s.Basis != "" {
			add(fmt.Sprintf("Основание:        %s", s.Basis), false)
		}
		sep()
		add("СЕРТИФИКАТ", true)
		add(fmt.Sprintf("УЦ:               %s", orDash(s.CAName)), false)
		add(fmt.Sprintf("№ сертификата:    %s", TruncateCertSerial(s.CertSerial)), false)
		add(fmt.Sprintf("Действителен:     с %s по %s", formatDate(s.CertNotBefore), formatDate(s.CertNotAfter)), false)
		sep()
		add("ПОДПИСЬ", true)
		add(fmt.Sprintf("Формат:           %s", orDash(s.SignFormat)), false)
		add(fmt.Sprintf("Хэш SHA-256:      %s", TruncateSHA256(s.SHA256Hash)), false)
		add("Статус:           Подпись действительна ✓", false)
		sep()
	}

	return lines
}

// GenerateSignPage renders a single PDF page listing all signatures.
// Text is stamped line-by-line via pdfcpu watermarks; QR images are placed
// at the right margin alongside each signature block.
func GenerateSignPage(signatures []SignatureInfo) ([]byte, error) {
	ensureFont()

	conf := model.NewDefaultConfiguration()

	// Create a blank A4 page.
	blankJSON := `{"pages":{"1":{"mediaBox":[0,0,595,842],"content":{"text":[{"value":" ","pos":[40,40],"font":{"name":"Helvetica","size":1}}]}}}}`
	var base bytes.Buffer
	if err := api.Create(nil, bytes.NewReader([]byte(blankJSON)), &base, conf); err != nil {
		return nil, fmt.Errorf("pdf: create blank sign page: %w", err)
	}

	lines := buildSignPageLines(signatures)

	const (
		fontPt    = 9
		lineH     = 14  // pt between lines
		topMargin = 800 // pt from bottom of A4 (842pt tall)
		leftMargin = 40
	)

	fn := activeFontName
	cur := base.Bytes()

	for i, l := range lines {
		if l.text == "" {
			continue
		}
		yFromBottom := topMargin - i*lineH
		if yFromBottom < 20 {
			break // off page
		}

		pts := fontPt
		if l.bold {
			pts = fontPt + 1
		}
		desc := fmt.Sprintf(
			"font:%s, points:%d, scale:1 abs, pos:bl, off:%d %d, rot:0, fillc:#000000, opacity:1",
			fn, pts, leftMargin, yFromBottom,
		)
		wm, err := pdfcpu.ParseTextWatermarkDetails(l.text, desc, true, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("pdf: parse sign-page line %d: %w", i, err)
		}
		var out bytes.Buffer
		if err := api.AddWatermarks(bytes.NewReader(cur), &out, nil, wm, conf); err != nil {
			return nil, fmt.Errorf("pdf: stamp sign-page line %d: %w", i, err)
		}
		cur = out.Bytes()
	}

	// Overlay QR images at the right margin, one per signature.
	// Each QR is placed at the same vertical position as its block header.
	// Block header for sig i is at line offset: 3 (header) + i*(linesPerBlock).
	// We estimate ~12 lines per signature block; place QR near the top of each.
	const (
		qrSize     = 80 // pt
		qrRightOff = 520
		linesPerSig = 12
	)
	tmpDir, err := os.MkdirTemp("", "signpage-")
	if err != nil {
		return nil, fmt.Errorf("pdf: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	for i, s := range signatures {
		if len(s.QRImagePNG) == 0 {
			continue
		}
		imgPath := fmt.Sprintf("%s/qr-%d.png", tmpDir, i)
		if err := os.WriteFile(imgPath, s.QRImagePNG, 0o600); err != nil {
			return nil, fmt.Errorf("pdf: write qr: %w", err)
		}
		// Y position aligns with the signature block header.
		yFromBottom := topMargin - (3+i*(linesPerSig+1))*lineH
		if yFromBottom < qrSize+20 {
			yFromBottom = qrSize + 20
		}
		imgDesc := fmt.Sprintf(
			"pos:bl, off:%d %d, scale:%d abs, rot:0, opacity:1",
			qrRightOff, yFromBottom-qrSize, qrSize,
		)
		imgWM, err := pdfcpu.ParseImageWatermarkDetails(imgPath, imgDesc, true, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("pdf: parse qr wm: %w", err)
		}
		var out bytes.Buffer
		if err := api.AddWatermarks(bytes.NewReader(cur), &out, nil, imgWM, conf); err != nil {
			return nil, fmt.Errorf("pdf: apply qr wm: %w", err)
		}
		cur = out.Bytes()
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
