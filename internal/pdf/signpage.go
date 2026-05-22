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

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/font"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
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

// ── Font management ──────────────────────────────────────────────────────────

var (
	fontOnce        sync.Once
	activeFontName  = "Helvetica" // built-in pdfcpu fallback (Latin-only, won't crash)
	cyrillicEnabled = false       // true when a Unicode font with Cyrillic was loaded
)

// ensureFont copies the first available Unicode/Cyrillic TTF into pdfcpu's
// font dir (~/.config/pdfcpu/fonts) under the name ArialUnicodeMS.ttf,
// installs it, and activates it. Falls back to Helvetica if nothing found.
func ensureFont() {
	fontOnce.Do(func() {
		homeDir, _ := os.UserHomeDir()
		fontDir := filepath.Join(homeDir, ".config", "pdfcpu", "fonts")
		_ = os.MkdirAll(fontDir, 0o755)
		font.UserFontDir = fontDir

		// Already installed by Dockerfile or previous run.
		destPath := filepath.Join(fontDir, "ArialUnicodeMS.ttf")

		candidates := []string{
			destPath, // pre-installed in Docker image
			"/usr/share/fonts/noto/NotoSans-Regular.ttf",
			"/usr/share/fonts/truetype/noto/NotoSans-Regular.ttf",
			"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/dejavu/DejaVuSans.ttf",
			"/usr/share/fonts/TTF/DejaVuSans.ttf",
			"/Library/Fonts/Arial Unicode.ttf", // macOS
		}

		for _, src := range candidates {
			if _, err := os.Stat(src); err != nil {
				continue
			}
			// Copy to destPath under the name ArialUnicodeMS.ttf if needed.
			if src != destPath {
				data, err := os.ReadFile(src)
				if err != nil {
					continue
				}
				if err := os.WriteFile(destPath, data, 0o644); err != nil {
					continue
				}
			}
			// InstallFonts expects a directory path.
			if err := api.InstallFonts([]string{fontDir}); err != nil {
				continue
			}
			font.LoadUserFonts()
			if names := font.UserFontNames(); len(names) > 0 {
				activeFontName = "ArialUnicodeMS"
				cyrillicEnabled = true
				return
			}
		}
		// No TTF found — Helvetica built-in, no Cyrillic.
	})
}

// ── Blank page helper ────────────────────────────────────────────────────────

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

// ── Page stamper ─────────────────────────────────────────────────────────────

type stamper struct {
	cur    []byte
	conf   *model.Configuration
	err    error
	tmpDir string
	imgIdx int
}

func newStamper(base []byte, conf *model.Configuration, tmpDir string) *stamper {
	return &stamper{cur: base, conf: conf, tmpDir: tmpDir}
}

// txt stamps a single text watermark at (x, yFromTop) in pt from page top.
// centered=true uses pos:tc (horizontally centered; x ignored).
func (s *stamper) txt(text string, x, yFromTop, pts int, colorHex string, centered bool) {
	if s.err != nil || text == "" {
		return
	}
	var pos string
	if centered {
		pos = fmt.Sprintf("pos:tc, off:0 -%d", yFromTop)
	} else {
		pos = fmt.Sprintf("pos:tl, off:%d -%d", x, yFromTop)
	}
	desc := fmt.Sprintf(
		"font:%s, points:%d, scale:1 abs, %s, rot:0, fillc:%s, opacity:1",
		activeFontName, pts, pos, colorHex,
	)
	wm, err := pdfcpu.ParseTextWatermarkDetails(text, desc, true, types.POINTS)
	if err != nil {
		s.err = fmt.Errorf("pdf: parse text wm %q: %w", text, err)
		return
	}
	var out bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(s.cur), &out, nil, wm, s.conf); err != nil {
		s.err = fmt.Errorf("pdf: stamp text %q: %w", text, err)
		return
	}
	s.cur = out.Bytes()
}

// img stamps a PNG at (xFromLeft, yFromTop); sizePt is the desired square side.
func (s *stamper) img(png []byte, xFromLeft, yFromTop, sizePt int) {
	if s.err != nil || len(png) == 0 {
		return
	}
	path := fmt.Sprintf("%s/qr%d.png", s.tmpDir, s.imgIdx)
	s.imgIdx++
	if err := os.WriteFile(path, png, 0o600); err != nil {
		s.err = fmt.Errorf("pdf: write img: %w", err)
		return
	}
	// pdfcpu image scale is relative to page height (842pt on A4).
	scale := float64(sizePt) / 842.0
	desc := fmt.Sprintf("pos:tl, off:%d -%d, scale:%.4f rel, rot:0, opacity:1",
		xFromLeft, yFromTop, scale)
	wm, err := pdfcpu.ParseImageWatermarkDetails(path, desc, true, types.POINTS)
	if err != nil {
		s.err = fmt.Errorf("pdf: parse img wm: %w", err)
		return
	}
	var out bytes.Buffer
	if err := api.AddWatermarks(bytes.NewReader(s.cur), &out, nil, wm, s.conf); err != nil {
		s.err = fmt.Errorf("pdf: stamp img: %w", err)
		return
	}
	s.cur = out.Bytes()
}

func (s *stamper) result() ([]byte, error) {
	return s.cur, s.err
}

// ── Layout constants ──────────────────────────────────────────────────────────

const (
	colorBlack = "#000000"
	colorGreen = "#2D7D1F"
	colorGray  = "#888888"
	colorLine  = "#BBBBBB"

	spMargin  = 20  // left/right margin (pt)
	spQRSize  = 100 // QR image side (pt)
	spQRGap   = 14  // gap between QR right edge and text column
	spTextX   = spMargin + spQRSize + spQRGap // 134

	spTitlePt  = 13
	spSubtitPt = 10
	spHeaderPt = 11 // "✓ ДОКУМЕНТ ПОДПИСАН ЭЦП"
	spBodyPt   = 9
	spSmallPt  = 7

	spLineH  = 13 // body line height (pt)
	spHdrLH  = 16 // block-header line height
	spBlankH = 8  // blank-line advance
)

// ── GenerateSignPage ──────────────────────────────────────────────────────────

// GenerateSignPage renders one PDF page with all signatures matching the
// CLAUDE.md reference layout: QR on the left, text fields on the right,
// coloured headers, Cyrillic via ArialUnicodeMS (or Courier fallback).
func GenerateSignPage(signatures []SignatureInfo) ([]byte, error) {
	ensureFont()

	conf := model.NewDefaultConfiguration()

	var base bytes.Buffer
	if err := api.Create(nil, strings.NewReader(blankPagesJSON(1)), &base, conf); err != nil {
		return nil, fmt.Errorf("pdf: create blank page: %w", err)
	}

	tmpDir, err := os.MkdirTemp("", "signpage-")
	if err != nil {
		return nil, fmt.Errorf("pdf: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	st := newStamper(base.Bytes(), conf, tmpDir)

	// ── Page header ──────────────────────────────────────────
	st.txt("ЛИСТ ПОДПИСЕЙ", 0, 36, spTitlePt, colorBlack, true)
	st.txt("Электронные цифровые подписи документа", 0, 54, spSubtitPt, colorGray, true)
	st.txt(strings.Repeat("─", 100), 0, 67, spSmallPt, colorLine, true)

	y := 82 // current Y from top of page

	for i, s := range signatures {
		// ── Separator between blocks ──────────────────────────
		if i > 0 {
			st.txt(strings.Repeat("─", 95), 0, y+4, spSmallPt, colorLine, true)
			y += 16
		}

		blockTop := y

		// ── QR image ─────────────────────────────────────────
		st.img(s.QRImagePNG, spMargin, blockTop, spQRSize)

		// "Сканируйте для проверки" под QR
		st.txt("Сканируйте для", spMargin, blockTop+spQRSize+4, spSmallPt, colorGray, false)
		st.txt("проверки", spMargin+14, blockTop+spQRSize+4+spSmallPt+2, spSmallPt, colorGray, false)

		// ── Block header ──────────────────────────────────────
		st.txt("✓ ДОКУМЕНТ ПОДПИСАН ЭЦП", spTextX, y, spHeaderPt, colorGreen, false)
		y += spHdrLH

		st.txt(fmt.Sprintf("Дата подписания:  %s", formatTS(s.SignedAt)), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		y += spBlankH

		// ── Signer fields ─────────────────────────────────────
		if s.OrgName != "" && s.OrgName != "—" {
			st.txt(fmt.Sprintf("Организация:      %s", s.OrgName), spTextX, y, spBodyPt, colorBlack, false)
			y += spLineH
		}
		if s.BIN != "" && s.BIN != "—" {
			st.txt(fmt.Sprintf("БИН:              %s", s.BIN), spTextX, y, spBodyPt, colorBlack, false)
			y += spLineH
		}
		st.txt(fmt.Sprintf("Подписант:        %s", s.SignerName), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		if s.IIN != "" && s.IIN != "—" {
			st.txt(fmt.Sprintf("ИИН:              %s", MaskIIN(s.IIN)), spTextX, y, spBodyPt, colorBlack, false)
			y += spLineH
		}
		st.txt(fmt.Sprintf("Тип:              %s", s.SignerType), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		if s.Basis != "" && s.Basis != "—" {
			st.txt(fmt.Sprintf("Основание:        %s", s.Basis), spTextX, y, spBodyPt, colorBlack, false)
			y += spLineH
		}

		y += 10

		// ── Certificate section ───────────────────────────────
		st.txt("СЕРТИФИКАТ", spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		st.txt(fmt.Sprintf("УЦ:               %s", s.CAName), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		st.txt(fmt.Sprintf("№ сертификата:    %s", TruncateCertSerial(s.CertSerial)), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		st.txt(fmt.Sprintf("Действителен:     с %s по %s",
			formatDate(s.CertNotBefore), formatDate(s.CertNotAfter)), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH

		y += 10

		// ── Signature section ─────────────────────────────────
		st.txt("ПОДПИСЬ", spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		st.txt(fmt.Sprintf("Формат:           %s", s.SignFormat), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		st.txt(fmt.Sprintf("Хэш SHA-256:      %s", TruncateSHA256(s.SHA256Hash)), spTextX, y, spBodyPt, colorBlack, false)
		y += spLineH
		st.txt(fmt.Sprintf("Статус:           %s ✓", s.Status), spTextX, y, spBodyPt, colorGreen, false)
		y += spLineH

		// Y не должен быть выше нижнего края QR
		qrBottom := blockTop + spQRSize + 20
		if y < qrBottom {
			y = qrBottom
		}
		y += 12
	}

	return st.result()
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
