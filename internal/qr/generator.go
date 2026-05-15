package qr

import (
	"fmt"

	qrcode "github.com/skip2/go-qrcode"
)

// DefaultSize is the default QR image edge length in pixels.
const DefaultSize = 256

// GenerateQR encodes url as a PNG QR code of the given square size in pixels.
// A size <= 0 falls back to DefaultSize.
func GenerateQR(url string, size int) ([]byte, error) {
	if url == "" {
		return nil, fmt.Errorf("qr: url is empty")
	}
	if size <= 0 {
		size = DefaultSize
	}
	png, err := qrcode.Encode(url, qrcode.Medium, size)
	if err != nil {
		return nil, fmt.Errorf("qr: encode: %w", err)
	}
	return png, nil
}
