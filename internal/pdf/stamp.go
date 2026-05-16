package pdf

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

// QRStamp is a single QR stamp to apply to every page of a document.
type QRStamp struct {
	SignerName string
	Role       string
	QRImagePNG []byte
	PageCount  int
}

const (
	stampSizePt = 80
	stampGapPt  = 10
	stampMargin = 24
)

// AddQRStamps overlays each stamp's QR (80×80pt) on the bottom-left of every
// page, laid out left-to-right with a 10pt gap.
// Under each QR the label "Proverit ECP" is printed (Latin, Helvetica-safe).
func AddQRStamps(pdfBytes []byte, stamps []QRStamp) ([]byte, error) {
	if len(stamps) == 0 {
		cp := make([]byte, len(pdfBytes))
		copy(cp, pdfBytes)
		return cp, nil
	}

	conf := model.NewDefaultConfiguration()

	tmpDir, err := os.MkdirTemp("", "pdfstamp-")
	if err != nil {
		return nil, fmt.Errorf("pdf: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cur := make([]byte, len(pdfBytes))
	copy(cur, pdfBytes)

	for i, st := range stamps {
		xOff := stampMargin + i*(stampSizePt+stampGapPt)

		// --- QR image ---
		imgPath := filepath.Join(tmpDir, fmt.Sprintf("qr-%d.png", i))
		if err := os.WriteFile(imgPath, st.QRImagePNG, 0o600); err != nil {
			return nil, fmt.Errorf("pdf: write qr image: %w", err)
		}
		imgDesc := fmt.Sprintf(
			"pos:bl, off:%d %d, scale:0.12 rel, rot:0, opacity:1",
			xOff, stampMargin+12,
		)
		imgWM, err := pdfcpu.ParseImageWatermarkDetails(imgPath, imgDesc, true, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("pdf: parse qr stamp: %w", err)
		}
		var afterImg bytes.Buffer
		if err := api.AddWatermarks(bytes.NewReader(cur), &afterImg, nil, imgWM, conf); err != nil {
			return nil, fmt.Errorf("pdf: apply qr stamp: %w", err)
		}

		// --- "Proverit ECP" label (Latin only — Helvetica is Latin-1) ---
		txtDesc := fmt.Sprintf(
			"font:Helvetica, points:6, scale:1 abs, pos:bl, off:%d %d, rot:0, fillc:#000000, opacity:1",
			xOff, stampMargin,
		)
		txtWM, err := pdfcpu.ParseTextWatermarkDetails("Proverit ECP", txtDesc, true, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("pdf: parse stamp label: %w", err)
		}
		var afterTxt bytes.Buffer
		if err := api.AddWatermarks(bytes.NewReader(afterImg.Bytes()), &afterTxt, nil, txtWM, conf); err != nil {
			return nil, fmt.Errorf("pdf: apply stamp label: %w", err)
		}
		cur = afterTxt.Bytes()
	}

	return cur, nil
}
