package pdf

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/pdfcpu/pdfcpu/pkg/api"
	"github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/stretchr/testify/require"
)

func TestMaskIIN(t *testing.T) {
	require.Equal(t, "1234****9012", MaskIIN("123456789012"))
	require.Equal(t, "short", MaskIIN("short")) // too short, unchanged
}

func TestTruncateCertSerial(t *testing.T) {
	// CLAUDE.md rule: first 4 + "..." + last 3 (example 2F:5...3:91).
	require.Equal(t, "2F:5...:CD", TruncateCertSerial("2F:53:91:AB:CD"))
	require.Equal(t, "short", TruncateCertSerial("short"))
}

func TestTruncateSHA256(t *testing.T) {
	hash := strings.Repeat("a", 8) + strings.Repeat("0", 48) + strings.Repeat("b", 8)
	require.Len(t, hash, 64)
	require.Equal(t, "aaaaaaaa...bbbbbbbb", TruncateSHA256(hash))
}

func sampleSignature() SignatureInfo {
	return SignatureInfo{
		SignerName:    "БАХЫТЖАНОВА ТОЖАН БАХЫТЖАНОВНА",
		OrgName:       "ТОО МеталлОптТорг KZ",
		BIN:           "230240030302",
		IIN:           "890400001782",
		SignerType:    "Представитель юридического лица",
		Basis:         "Устав",
		CertSerial:    "2F5391ABCDEF0011223391",
		CertNotBefore: time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC),
		CertNotAfter:  time.Date(2027, 1, 8, 0, 0, 0, 0, time.UTC),
		CAName:        "ҰЛТТЫҚ КУӘЛАНДЫРУШЫ ОРТАЛЫҚ",
		SignFormat:    "CAdES (CMS, PKCS#7)",
		SHA256Hash:    strings.Repeat("1", 64),
		Status:        "Подпись действительна",
		SignedAt:      time.Date(2026, 5, 14, 14, 10, 4, 0, time.UTC),
	}
}

func TestGenerateSignPage_NonEmptyPDF(t *testing.T) {
	out, err := GenerateSignPage([]SignatureInfo{sampleSignature(), sampleSignature()})
	require.NoError(t, err)
	require.NotEmpty(t, out)
	require.True(t, bytes.HasPrefix(out, []byte("%PDF-")), "must be a PDF")

	n, err := api.PageCount(bytes.NewReader(out), model.NewDefaultConfiguration())
	require.NoError(t, err)
	require.Equal(t, 1, n)
}

func TestReplaceLastPage_PageCount(t *testing.T) {
	conf := model.NewDefaultConfiguration()

	// Build a 3-page original.
	var orig bytes.Buffer
	err := api.Create(nil, strings.NewReader(blankPagesJSON(3)), &orig, conf)
	require.NoError(t, err)
	origCount, err := api.PageCount(bytes.NewReader(orig.Bytes()), conf)
	require.NoError(t, err)
	require.Equal(t, 3, origCount)

	// Первая подпись: ReplaceLastPage дописывает Лист подписей (стрипать нечего).
	// 3 контентных страницы + 1 Лист = 4.
	signPage, err := GenerateSignPage([]SignatureInfo{sampleSignature()})
	require.NoError(t, err)
	firstSigned, err := ReplaceLastPage(orig.Bytes(), signPage)
	require.NoError(t, err)
	firstCount, err := api.PageCount(bytes.NewReader(firstSigned), conf)
	require.NoError(t, err)
	require.Equal(t, 4, firstCount)

	// Повторная подпись (как в service.Sign): сначала StripLastPage убирает
	// старый Лист, затем ReplaceLastPage дописывает новый. Число страниц
	// должно оставаться стабильным (4), а не расти.
	stripped, err := StripLastPage(firstSigned)
	require.NoError(t, err)
	strippedCount, err := api.PageCount(bytes.NewReader(stripped), conf)
	require.NoError(t, err)
	require.Equal(t, 3, strippedCount)

	signPage2, err := GenerateSignPage([]SignatureInfo{sampleSignature(), sampleSignature()})
	require.NoError(t, err)
	secondSigned, err := ReplaceLastPage(stripped, signPage2)
	require.NoError(t, err)
	secondCount, err := api.PageCount(bytes.NewReader(secondSigned), conf)
	require.NoError(t, err)
	require.Equal(t, 4, secondCount)
}
