package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mkapitanoff/pki-service/internal/ncanode"
)

// stubNCANode реализует ncanode.NCANodeClient — эндпоинт stateless, БД не нужна.
type stubNCANode struct {
	result *ncanode.VerifyResult
	err    error

	gotCMS  string
	gotHash string
}

func (s *stubNCANode) VerifyCMS(_ context.Context, cmsBase64, docSHA256 string) (*ncanode.VerifyResult, error) {
	s.gotCMS, s.gotHash = cmsBase64, docSHA256
	return s.result, s.err
}

func (s *stubNCANode) GetTSP(context.Context, string) (time.Time, error) {
	return time.Time{}, fmt.Errorf("GetTSP must not be called by auth verify")
}

func validResult() *ncanode.VerifyResult {
	return &ncanode.VerifyResult{
		Valid:         true,
		SignerIIN:     "123456789012",
		SignerName:    "Тест Тестов",
		SignerBIN:     "070740008101",
		OrgName:       "ТОО Тест",
		SignerType:    "legal_entity_rep",
		CertSerial:    "abc123",
		CertNotBefore: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		CertNotAfter:  time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC),
		CAName:        "НУЦ РК",
		OCSPStatus:    ncanode.OCSPStatusGood,
	}
}

func callVerify(t *testing.T, stub *stubNCANode, body any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/verify-signature", bytes.NewReader(raw))
	rec := httptest.NewRecorder()
	NewAuthVerifyHandler(stub).HandleVerifySignature(rec, req)
	return rec
}

func TestAuthVerify_Valid(t *testing.T) {
	stub := &stubNCANode{result: validResult()}
	rec := callVerify(t, stub, authVerifyRequest{CMS: "BASE64CMS", Challenge: "nonce-123"})

	require.Equal(t, http.StatusOK, rec.Code)

	var got authVerifyResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.True(t, got.Valid)
	require.Equal(t, "123456789012", got.IIN)
	require.Equal(t, "070740008101", got.BIN)
	require.Equal(t, "legal_entity_rep", got.SignerType)

	// Ключевая привязка: NCANode должен получить SHA-256 именно от challenge —
	// это и есть анти-replay на уровне параметра.
	sum := sha256.Sum256([]byte("nonce-123"))
	require.Equal(t, hex.EncodeToString(sum[:]), stub.gotHash)
	require.Equal(t, "BASE64CMS", stub.gotCMS)
}

func TestAuthVerify_InvalidSignature(t *testing.T) {
	stub := &stubNCANode{err: ncanode.ErrCMSInvalid}
	rec := callVerify(t, stub, authVerifyRequest{CMS: "x", Challenge: "n"})
	require.Equal(t, 422, rec.Code)
	require.Contains(t, rec.Body.String(), "CMS_INVALID")
}

func TestAuthVerify_CertRevoked(t *testing.T) {
	stub := &stubNCANode{err: ncanode.ErrCertRevoked}
	rec := callVerify(t, stub, authVerifyRequest{CMS: "x", Challenge: "n"})
	require.Equal(t, 422, rec.Code)
	require.Contains(t, rec.Body.String(), "CERT_REVOKED")
}

// OCSP "unknown" не должен пускать в систему — при аутентификации отсутствие
// подтверждения статуса трактуется не в пользу входа.
func TestAuthVerify_OCSPNotGood(t *testing.T) {
	for _, status := range []string{ncanode.OCSPStatusUnknown, ncanode.OCSPStatusRevoked} {
		res := validResult()
		res.OCSPStatus = status
		rec := callVerify(t, &stubNCANode{result: res}, authVerifyRequest{CMS: "x", Challenge: "n"})
		require.Equal(t, 422, rec.Code, "ocsp_status=%s must be rejected", status)
		require.Contains(t, rec.Body.String(), "CERT_REVOKED")
	}
}

func TestAuthVerify_MissingFields(t *testing.T) {
	for _, tc := range []authVerifyRequest{
		{CMS: "", Challenge: "n"},
		{CMS: "x", Challenge: ""},
		{CMS: "   ", Challenge: "   "},
	} {
		rec := callVerify(t, &stubNCANode{result: validResult()}, tc)
		require.Equal(t, 400, rec.Code)
		require.Contains(t, rec.Body.String(), "INVALID_REQUEST")
	}
}

func TestAuthVerify_NCANodeDown(t *testing.T) {
	stub := &stubNCANode{err: fmt.Errorf("ncanode: request failed: connection refused")}
	rec := callVerify(t, stub, authVerifyRequest{CMS: "x", Challenge: "n"})
	require.Equal(t, 500, rec.Code)
	require.Contains(t, rec.Body.String(), "INTERNAL")
}
