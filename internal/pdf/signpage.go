package pdf

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/jung-kurt/gofpdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/font"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

// SignatureInfo is one rendered entry on the "Лист подписей" page.
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

// MaskIIN masks an IIN: first 4 + "****" + last 4.
func MaskIIN(iin string) string {
	if len(iin) < 8 {
		return iin
	}
	return iin[:4] + "****" + iin[len(iin)-4:]
}

// TruncateCertSerial: first 4 + "..." + last 3.
func TruncateCertSerial(serial string) string {
	if len(serial) <= 7 {
		return serial
	}
	return serial[:4] + "..." + serial[len(serial)-3:]
}

// TruncateSHA256: first 8 + "..." + last 8.
func TruncateSHA256(hash string) string {
	if len(hash) <= 16 {
		return hash
	}
	return hash[:8] + "..." + hash[len(hash)-8:]
}

func formatTS(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.Format("02.01.2006, 15:04:05")
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("02.01.2006")
}

// ── Font management (used by stamp.go via initPDFCPUFonts / cyrillicEnabled) ──

var initFontsOnce sync.Once

// activeFontName is the pdfcpu font name used for QR stamp labels in stamp.go.
var activeFontName = "Helvetica"

// cyrillicEnabled is true when a Unicode font with Cyrillic glyphs was loaded.
var cyrillicEnabled = false

// ttfFontPath returns the path to ArialUnicodeMS.ttf for the current OS.
// Returns "" if the file is not found.
func ttfFontPath() string {
	candidates := []string{
		"/root/.config/pdfcpu/fonts/ArialUnicodeMS.ttf", // Docker (Linux)
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".config", "pdfcpu", "fonts", "ArialUnicodeMS.ttf"),
		)
	}
	candidates = append(candidates, "/Library/Fonts/Arial Unicode.ttf") // macOS system
	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// copyFile copies src to dst byte-for-byte.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// initPDFCPUFonts loads the Cyrillic font into pdfcpu for QR stamp labels
// in stamp.go. It sets activeFontName and cyrillicEnabled.
// Runs exactly once per process.
func initPDFCPUFonts() {
	initFontsOnce.Do(func() {
		fontDir := "/root/.config/pdfcpu/fonts"
		if _, err := os.Stat(fontDir); os.IsNotExist(err) {
			homeDir, _ := os.UserHomeDir()
			fontDir = filepath.Join(homeDir, ".config", "pdfcpu", "fonts")
			_ = os.MkdirAll(fontDir, 0o755)
			macFont := "/Library/Fonts/Arial Unicode.ttf"
			if _, err := os.Stat(macFont); err == nil {
				dest := filepath.Join(fontDir, "ArialUnicodeMS.ttf")
				_ = copyFile(macFont, dest)
			}
		}

		font.UserFontDir = fontDir

		gobPath := filepath.Join(fontDir, "ArialUnicodeMS.gob")
		if _, err := os.Stat(gobPath); err != nil {
			// .gob not found — generate from TTF.
			ttfPath := filepath.Join(fontDir, "ArialUnicodeMS.ttf")
			if _, err := os.Stat(ttfPath); err == nil {
				if err := font.InstallTrueTypeFont(fontDir, ttfPath); err != nil {
					fmt.Printf("[pdf] InstallTrueTypeFont error: %v\n", err)
				}
			}
		} else {
			fmt.Printf("[pdf] found pre-built gob at %s, skipping InstallTrueTypeFont\n", gobPath)
		}

		if err := font.LoadUserFonts(); err != nil {
			fmt.Printf("[pdf] LoadUserFonts error: %v\n", err)
			return
		}

		names := font.UserFontNames()
		fmt.Printf("[pdf] LoadUserFonts OK, fonts: %v\n", names)

		for _, n := range names {
			if strings.Contains(strings.ToLower(n), "arial") {
				activeFontName = n
				cyrillicEnabled = true
				return
			}
		}
		if len(names) > 0 {
			activeFontName = names[0]
			cyrillicEnabled = true
		}
	})
}

// ── GenerateSignPage (gofpdf) ─────────────────────────────────────────────────

// GenerateSignPage renders one PDF page ("Лист подписей") using gofpdf,
// which handles Unicode/Cyrillic natively via embedded TTF.
func GenerateSignPage(signatures []SignatureInfo) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(10, 10, 10)
	pdf.SetAutoPageBreak(true, 10)

	const fontName = "Arial"
	ttf := ttfFontPath()
	if ttf != "" {
		pdf.AddUTF8Font(fontName, "", ttf)
	}
	if pdf.Error() != nil || ttf == "" {
		// No Unicode font available — fall back to built-in Helvetica.
		fmt.Printf("[pdf] gofpdf: no TTF found, using Helvetica fallback\n")
	}

	useCyrillic := ttf != "" && pdf.Error() == nil

	setFont := func(size float64, bold bool) {
		style := ""
		if bold {
			style = "B"
		}
		if useCyrillic {
			pdf.SetFont(fontName, style, size)
		} else {
			pdf.SetFont("Helvetica", style, size)
		}
	}

	pdf.AddPage()
	pageW, _ := pdf.GetPageSize()
	contentW := pageW - 20 // 10mm margins each side

	// ── Page header ────────────────────────────────────────────
	setFont(16, true)
	pdf.SetTextColor(0, 0, 0)
	pdf.CellFormat(contentW, 10, "ЛИСТ ПОДПИСЕЙ", "", 1, "C", false, 0, "")

	setFont(9, false)
	pdf.SetTextColor(136, 136, 136)
	pdf.CellFormat(contentW, 6, "Электронные цифровые подписи документа", "", 1, "C", false, 0, "")
	pdf.SetTextColor(0, 0, 0)

	pdf.SetDrawColor(187, 187, 187)
	pdf.Line(10, pdf.GetY()+2, pageW-10, pdf.GetY()+2)
	pdf.Ln(6)

	for i, s := range signatures {
		if i > 0 {
			pdf.SetDrawColor(187, 187, 187)
			pdf.Line(10, pdf.GetY()+1, pageW-10, pdf.GetY()+1)
			pdf.Ln(5)
		}

		blockStartY := pdf.GetY()
		qrW := 30.0 // mm
		textX := 10 + qrW + 5
		textW := contentW - qrW - 5

		// ── QR image ───────────────────────────────────────────
		if len(s.QRImagePNG) > 0 {
			pdf.RegisterImageOptionsReader(
				fmt.Sprintf("qr_%d", i),
				gofpdf.ImageOptions{ImageType: "PNG"},
				bytes.NewReader(s.QRImagePNG),
			)
			pdf.ImageOptions(
				fmt.Sprintf("qr_%d", i),
				10, blockStartY, qrW, qrW, false,
				gofpdf.ImageOptions{ImageType: "PNG"},
				0, "",
			)
			setFont(6, false)
			pdf.SetTextColor(100, 100, 100)
			pdf.SetXY(10, blockStartY+qrW+1)
			pdf.CellFormat(qrW, 3, "Сканируйте для проверки", "", 1, "C", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
		}

		// ── Block header ────────────────────────────────────────
		pdf.SetXY(textX, blockStartY)
		setFont(11, true)
		pdf.SetTextColor(45, 125, 31)
		pdf.CellFormat(textW, 7, "ДОКУМЕНТ ПОДПИСАН ЭЦП", "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)

		pdf.SetX(textX)
		setFont(9, false)
		pdf.CellFormat(textW, 5, fmt.Sprintf("Дата подписания:  %s", formatTS(s.SignedAt)), "", 1, "L", false, 0, "")
		pdf.Ln(2)

		// ── Signer fields ───────────────────────────────────────
		type row struct{ label, value string }
		var rows []row
		if s.OrgName != "" && s.OrgName != "—" {
			rows = append(rows, row{"Организация:", s.OrgName})
		}
		if s.BIN != "" && s.BIN != "—" {
			rows = append(rows, row{"БИН:", s.BIN})
		}
		rows = append(rows, row{"Подписант:", s.SignerName})
		if s.IIN != "" && s.IIN != "—" {
			rows = append(rows, row{"ИИН:", MaskIIN(s.IIN)})
		}
		if s.SignerType != "" {
			rows = append(rows, row{"Тип:", s.SignerType})
		}
		if s.Basis != "" && s.Basis != "—" {
			rows = append(rows, row{"Основание:", s.Basis})
		}

		labelW := 38.0
		for _, r := range rows {
			pdf.SetX(textX)
			setFont(9, false)
			pdf.SetTextColor(100, 100, 100)
			pdf.CellFormat(labelW, 5, r.label, "", 0, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
			pdf.CellFormat(textW-labelW, 5, r.value, "", 1, "L", false, 0, "")
		}
		pdf.Ln(2)

		// ── Certificate ─────────────────────────────────────────
		pdf.SetX(textX)
		setFont(9, true)
		pdf.CellFormat(textW, 5, "СЕРТИФИКАТ", "", 1, "L", false, 0, "")

		certRows := []row{
			{"УЦ:", s.CAName},
			{"№ сертификата:", TruncateCertSerial(s.CertSerial)},
			{"Действителен:", fmt.Sprintf("с %s по %s", formatDate(s.CertNotBefore), formatDate(s.CertNotAfter))},
		}
		setFont(9, false)
		for _, r := range certRows {
			pdf.SetX(textX)
			pdf.SetTextColor(100, 100, 100)
			pdf.CellFormat(labelW, 5, r.label, "", 0, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
			pdf.CellFormat(textW-labelW, 5, r.value, "", 1, "L", false, 0, "")
		}
		pdf.Ln(2)

		// ── Signature ───────────────────────────────────────────
		pdf.SetX(textX)
		setFont(9, true)
		pdf.CellFormat(textW, 5, "ПОДПИСЬ", "", 1, "L", false, 0, "")

		setFont(9, false)
		sigRows := []row{
			{"Формат:", s.SignFormat},
			{"Хэш SHA-256:", TruncateSHA256(s.SHA256Hash)},
		}
		for _, r := range sigRows {
			pdf.SetX(textX)
			pdf.SetTextColor(100, 100, 100)
			pdf.CellFormat(labelW, 5, r.label, "", 0, "L", false, 0, "")
			pdf.SetTextColor(0, 0, 0)
			pdf.CellFormat(textW-labelW, 5, r.value, "", 1, "L", false, 0, "")
		}
		// Статус зелёным
		pdf.SetX(textX)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(labelW, 5, "Статус:", "", 0, "L", false, 0, "")
		pdf.SetTextColor(45, 125, 31)
		pdf.CellFormat(textW-labelW, 5, "Подпись действительна", "", 1, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)

		// Убедимся что Y не перекрывает QR
		qrBottom := blockStartY + qrW + 8
		if pdf.GetY() < qrBottom {
			pdf.SetY(qrBottom)
		}
		pdf.Ln(4)
	}

	var buf bytes.Buffer
	if err := pdf.Output(&buf); err != nil {
		return nil, fmt.Errorf("gofpdf output: %w", err)
	}
	return buf.Bytes(), nil
}

// ── ReplaceLastPage ───────────────────────────────────────────────────────────

// ReplaceLastPage drops the last page of pdfBytes and appends newPageBytes.
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
