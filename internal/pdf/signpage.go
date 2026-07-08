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

// blankPagesJSON возвращает pdfcpu "create" JSON с n пустыми A4-страницами.
// pdfcpu требует, чтобы у каждой страницы было content — кладём один
// почти-невидимый пробел (size 1pt). Используется тестами этого пакета.
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

// SignatureInfo is one rendered entry on the "Лист подписей" page.
type SignatureInfo struct {
	SignerName    string
	OrgName       string
	BIN           string
	IIN           string // raw IIN; rendered via MaskIIN
	Role          string // signing role: client, factor, director, etc.
	SignerType    string // from NCANode: individual, legal_entity_rep
	Basis         string
	CertSerial    string // raw serial; rendered via TruncateCertSerial
	CertNotBefore time.Time
	CertNotAfter  time.Time
	CAName        string
	SignFormat    string
	SHA256Hash    string // raw hex (64); на Листе подписей рендерится полностью
	Status        string
	SignedAt      time.Time
	QRImagePNG    []byte
}

// RoleLabel maps internal role codes to Russian labels.
func RoleLabel(role string) string {
	switch role {
	case "client":
		return "Клиент"
	case "factor":
		return "Фактор"
	case "director":
		return "Директор"
	case "accountant":
		return "Бухгалтер"
	case "signatory":
		return "Уполномоченное лицо"
	case "external":
		return "Внешняя подпись"
	default:
		if role != "" {
			return role
		}
		return ""
	}
}

// SignerTypeLabel maps NCANode signer type codes to Russian labels.
func SignerTypeLabel(signerType string) string {
	switch signerType {
	case "individual":
		return "Физическое лицо"
	case "legal_entity_rep":
		return "Представитель юр. лица"
	default:
		return signerType
	}
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

// kzLocation — Казахстан круглогодично UTC+5 (Asia/Almaty; DST отменён с 2024).
// Используем FixedZone, чтобы не зависеть от наличия tzdata в alpine-контейнере.
// Время в БД хранится в UTC (TIMESTAMPTZ) — на Листе подписей показываем по КЗ.
var kzLocation = time.FixedZone("Asia/Almaty", 5*60*60)

func formatTS(t time.Time) string {
	if t.IsZero() {
		t = time.Now()
	}
	return t.In(kzLocation).Format("02.01.2006, 15:04:05")
}

func formatDate(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Format("02.01.2006")
}

// ── Font bytes cache ──────────────────────────────────────────────────────────

var (
	cachedFontBytes     []byte
	cachedFontBytesOnce sync.Once
	cachedFontBytesErr  error
)

func loadFontBytes() ([]byte, error) {
	cachedFontBytesOnce.Do(func() {
		path := ttfFontPath()
		if path == "" {
			cachedFontBytesErr = fmt.Errorf("font not found")
			return
		}
		cachedFontBytes, cachedFontBytesErr = os.ReadFile(path)
		if cachedFontBytesErr == nil {
			fmt.Printf("[pdf] font loaded into memory: %d bytes from %s\n", len(cachedFontBytes), path)
		}
	})
	return cachedFontBytes, cachedFontBytesErr
}

// ── Font management (used by stamp.go via initPDFCPUFonts / cyrillicEnabled) ──

var initFontsOnce sync.Once

// activeFontName is the pdfcpu font name used for QR stamp labels in stamp.go.
var activeFontName = "Helvetica"

// cyrillicEnabled is true when a Unicode font with Cyrillic glyphs was loaded.
var cyrillicEnabled = false

// ourFontDir is the absolute font directory we resolved — separate from font.UserFontDir
// because pdfcpu may mutate that variable internally after LoadUserFonts().
var ourFontDir = ""

// ttfFontPath returns the absolute path to ArialUnicodeMS.ttf.
// Returns "" if the file is not found.
func ttfFontPath() string {
	candidates := []string{
		"/root/.config/pdfcpu/fonts/ArialUnicodeMS.ttf", // Docker (Linux) — hardcoded absolute
		"/Library/Fonts/Arial Unicode.ttf",              // macOS system
	}
	// Prefer the dir we resolved in initPDFCPUFonts (always absolute).
	if ourFontDir != "" {
		candidates = append([]string{filepath.Join(ourFontDir, "ArialUnicodeMS.ttf")}, candidates...)
	}
	for _, p := range candidates {
		if !filepath.IsAbs(p) {
			continue // skip any relative path — would silently fail
		}
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
		ourFontDir = fontDir // save absolute path before pdfcpu can mutate font.UserFontDir

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
	// Ensure font.UserFontDir is set before calling ttfFontPath().
	initPDFCPUFonts()

	fpdf := gofpdf.New("P", "mm", "A4", "")
	fpdf.SetMargins(10, 10, 10)
	fpdf.SetAutoPageBreak(true, 10)

	const fontName = "Arial"
	useCyrillic := false

	if fb, err := loadFontBytes(); err == nil {
		fpdf.AddUTF8FontFromBytes(fontName, "", fb)
		if fpdf.Error() == nil {
			useCyrillic = true
		} else {
			fmt.Printf("[pdf] gofpdf AddUTF8FontFromBytes error: %v, falling back to Helvetica\n", fpdf.Error())
			fpdf = gofpdf.New("P", "mm", "A4", "")
			fpdf.SetMargins(10, 10, 10)
			fpdf.SetAutoPageBreak(true, 10)
		}
	} else {
		fmt.Printf("[pdf] gofpdf: font not available (%v), using Helvetica fallback\n", err)
	}

	// gofpdf UTF8 fonts don't support bold style unless registered separately.
	// We use size variation instead: headers get larger size, not bold.
	setFont := func(size float64, _ bool) {
		if useCyrillic {
			fpdf.SetFont(fontName, "", size)
		} else {
			fpdf.SetFont("Helvetica", "", size)
		}
	}
	pdf := fpdf

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
		if rl := RoleLabel(s.Role); rl != "" {
			rows = append(rows, row{"Роль:", rl})
		}
		if st := SignerTypeLabel(s.SignerType); st != "" {
			rows = append(rows, row{"Тип:", st})
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
		// Формат — в одну строку.
		pdf.SetX(textX)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(labelW, 5, "Формат:", "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		pdf.CellFormat(textW-labelW, 5, s.SignFormat, "", 1, "L", false, 0, "")

		// Хэш SHA-256 — ПОЛНОСТЬЮ (64 hex), чтобы получатель мог проверить
		// подпись по хэшу на публичной странице верификации. Значение — тем же
		// рядом, но меньшим кеглем (7), чтобы 64 символа влезли в одну строку
		// без прироста высоты блока.
		pdf.SetX(textX)
		pdf.SetTextColor(100, 100, 100)
		pdf.CellFormat(labelW, 5, "Хэш SHA-256:", "", 0, "L", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
		setFont(7, false)
		pdf.CellFormat(textW-labelW, 5, s.SHA256Hash, "", 1, "L", false, 0, "")
		setFont(9, false)
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

// StripLastPage removes the last page from a PDF.
// Returns pdfBytes unchanged if the document has only one page.
func StripLastPage(pdfBytes []byte) ([]byte, error) {
	conf := model.NewDefaultConfiguration()
	count, err := api.PageCount(bytes.NewReader(pdfBytes), conf)
	if err != nil {
		return nil, fmt.Errorf("pdf: page count: %w", err)
	}
	if count <= 1 {
		return pdfBytes, nil
	}
	var trimmed bytes.Buffer
	keep := fmt.Sprintf("1-%d", count-1)
	if err := api.Trim(bytes.NewReader(pdfBytes), &trimmed, []string{keep}, conf); err != nil {
		return nil, fmt.Errorf("pdf: strip last page: %w", err)
	}
	return trimmed.Bytes(), nil
}

// ReplaceLastPage appends signPageBytes to docBytes (always merges as last page).
// The caller is responsible for stripping any existing sign page before calling
// this function (use StripLastPage for subsequent signings).
func ReplaceLastPage(docBytes []byte, signPageBytes []byte) ([]byte, error) {
	conf := model.NewDefaultConfiguration()
	conf.ValidationMode = model.ValidationRelaxed

	var result bytes.Buffer
	err := api.MergeRaw(
		[]io.ReadSeeker{
			bytes.NewReader(docBytes),
			bytes.NewReader(signPageBytes),
		},
		&result,
		false,
		conf,
	)
	if err != nil {
		return nil, fmt.Errorf("merge sign page: %w", err)
	}
	return result.Bytes(), nil
}
