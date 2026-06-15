package pdf

import (
	"bytes"
	"fmt"
	"image/png"
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

// AddQRStamps накладывает на каждую страницу PDF QR-штампы.
// Размеры и отступы — в internal/pdf/stamp_config.go (QRStampSizeMM и т.д.).
// Якорь — правый нижний угол страницы, штампы укладываются справа налево
// с зазором QRStampGapMM.
//
// TODO: белая подложка под QR (QRStampBGPadding). pdfcpu image-watermark API
// не рисует фон под изображением. Корректная реализация требует либо
// отдельной оверлей-страницы, отрендеренной через pdfcpu primitives и
// слитой со страницей, либо подгонки text-watermark с bgcolor/margins
// под размер QR (хрупко, шрифто-зависимо). Оставлено как известное
// ограничение — QR размещён в чистом нижнем-правом углу, где
// заполненной области, как правило, нет.
func AddQRStamps(pdfBytes []byte, stamps []QRStamp) ([]byte, error) {
	if len(stamps) == 0 {
		cp := make([]byte, len(pdfBytes))
		copy(cp, pdfBytes)
		return cp, nil
	}

	initPDFCPUFonts()

	conf := model.NewDefaultConfiguration()

	tmpDir, err := os.MkdirTemp("", "pdfstamp-")
	if err != nil {
		return nil, fmt.Errorf("pdf: temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	cur := make([]byte, len(pdfBytes))
	copy(cur, pdfBytes)

	const (
		qrSizePt   = QRStampSizeMM * MMToPt
		rightPt    = QRStampMarginRight * MMToPt
		bottomPt   = QRStampMarginBottom * MMToPt
		gapPt      = QRStampGapMM * MMToPt
	)

	for i, st := range stamps {
		// Натуральный размер PNG → absolute scale для нужного pt-размера.
		cfg, err := png.DecodeConfig(bytes.NewReader(st.QRImagePNG))
		if err != nil {
			return nil, fmt.Errorf("pdf: decode qr png: %w", err)
		}
		if cfg.Width <= 0 {
			return nil, fmt.Errorf("pdf: qr png has zero width")
		}
		scale := qrSizePt / float64(cfg.Width)

		// pdfcpu: BottomRight кладёт image правым-нижним углом к (W, 0);
		// off:Dx Dy сдвигает оттуда. Dx<0 — влево, Dy>0 — вверх.
		// Штампы укладываем справа налево.
		dx := -(rightPt + float64(i)*(qrSizePt+gapPt))
		dy := bottomPt

		imgPath := filepath.Join(tmpDir, fmt.Sprintf("qr-%d.png", i))
		if err := os.WriteFile(imgPath, st.QRImagePNG, 0o600); err != nil {
			return nil, fmt.Errorf("pdf: write qr image: %w", err)
		}
		imgDesc := fmt.Sprintf(
			"pos:br, off:%.2f %.2f, scale:%.4f abs, rot:0, opacity:1",
			dx, dy, scale,
		)
		imgWM, err := pdfcpu.ParseImageWatermarkDetails(imgPath, imgDesc, true, types.POINTS)
		if err != nil {
			return nil, fmt.Errorf("pdf: parse qr stamp: %w", err)
		}
		var after bytes.Buffer
		if err := api.AddWatermarks(bytes.NewReader(cur), &after, nil, imgWM, conf); err != nil {
			return nil, fmt.Errorf("pdf: apply qr stamp: %w", err)
		}
		cur = after.Bytes()
	}

	return cur, nil
}
