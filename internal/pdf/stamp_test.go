package pdf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/mkapitanoff/pki-service/internal/qr"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/stretchr/testify/require"
)

func TestAddQRStamps_NoStampsReturnsCopy(t *testing.T) {
	conf := model.NewDefaultConfiguration()
	var base bytes.Buffer
	require.NoError(t, api.Create(nil, strings.NewReader(blankPagesJSON(2)), &base, conf))

	out, err := AddQRStamps(base.Bytes(), nil)
	require.NoError(t, err)
	require.Equal(t, base.Bytes(), out)
}

func TestAddQRStamps_AppliesToEveryPage(t *testing.T) {
	conf := model.NewDefaultConfiguration()
	var base bytes.Buffer
	require.NoError(t, api.Create(nil, strings.NewReader(blankPagesJSON(2)), &base, conf))

	png, err := qr.GenerateQR("https://test.sign.example.kz/verify/abc", 128)
	require.NoError(t, err)

	stamps := []QRStamp{
		{SignerName: "ТЕСТОВ ТЕСТ", Role: "client", QRImagePNG: png, PageCount: 2},
		{SignerName: "ИВАНОВ ИВАН", Role: "factor", QRImagePNG: png, PageCount: 2},
	}

	out, err := AddQRStamps(base.Bytes(), stamps)
	require.NoError(t, err)
	require.True(t, bytes.Equal(out[:5], []byte("%PDF-")))

	n, err := api.PageCount(bytes.NewReader(out), conf)
	require.NoError(t, err)
	require.Equal(t, 2, n) // stamping must not change page count
}
