package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mkapitanoff/pki-service/internal/repository"
)

// --- fake driver returning one canned signature row ---

type sigRowDriver struct{}
type sigRowConn struct{}
type sigRows struct{ done bool }

func (sigRowDriver) Open(string) (driver.Conn, error) { return sigRowConn{}, nil }

func (sigRowConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (sigRowConn) Close() error                        { return nil }
func (sigRowConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (sigRowConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &sigRows{}, nil
}

var sigCols = []string{
	"id", "document_id", "tenant_id", "version_number", "sequence_num",
	"cms_b64", "role", "signer_iin", "signer_name", "signer_bin",
	"org_name", "signer_type", "basis", "cert_serial",
	"cert_not_before", "cert_not_after", "ca_name",
	"ocsp_status", "ocsp_checked_at", "tsp_time",
	"sha256_hash", "sign_format", "qr_url", "signed_at",
}

func (r *sigRows) Columns() []string { return sigCols }
func (r *sigRows) Close() error      { return nil }
func (r *sigRows) Next(dest []driver.Value) error {
	if r.done {
		return io.EOF
	}
	r.done = true
	now := time.Date(2026, 5, 14, 14, 10, 4, 0, time.UTC)
	vals := []driver.Value{
		"11111111-1111-1111-1111-111111111111", // id
		"22222222-2222-2222-2222-222222222222", // document_id
		"33333333-3333-3333-3333-333333333333", // tenant_id
		int64(1),                               // version_number
		int64(1),                               // sequence_num
		"BASE64CMS",                            // cms_b64
		"client",                               // role
		"890400001782",                         // signer_iin
		"БАХЫТЖАНОВА ТОЖАН БАХЫТЖАНОВНА",        // signer_name
		"230240030302",                         // signer_bin
		"ТОО МеталлОптТорг KZ",                  // org_name
		"Представитель юридического лица",       // signer_type
		"Устав",                                 // basis
		"2F5391ABCDEF0011223391",               // cert_serial
		now,                                     // cert_not_before
		now,                                     // cert_not_after
		"ҰЛТТЫҚ КУӘЛАНДЫРУШЫ ОРТАЛЫҚ",            // ca_name
		"good",                                  // ocsp_status
		now,                                     // ocsp_checked_at
		now,                                     // tsp_time
		"125939f400000000000000000000000000000000000000000000000071ece070", // sha256_hash
		"CAdES (CMS, PKCS#7)",                                               // sign_format
		"https://test.sign.example.kz/verify/11111111-1111-1111-1111-111111111111", // qr_url
		now, // signed_at
	}
	copy(dest, vals)
	return nil
}

func init() {
	sql.Register("sigrowfake", sigRowDriver{})
}

func sigRowQueries(t *testing.T) *repository.Queries {
	t.Helper()
	db, err := sql.Open("sigrowfake", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return repository.New(db)
}

func verifyRouter(q *repository.Queries) http.Handler {
	r := chi.NewRouter()
	h := NewVerifyHandler(q)
	r.Get("/verify/{signature_id}", h.HandleVerify)
	return r
}

func TestHandleVerify_UnknownReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/verify/"+uuid.NewString(), nil)

	verifyRouter(emptyQueries(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "не найдена")
}

func TestHandleVerify_InvalidUUIDReturns404(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/verify/not-a-uuid", nil)

	verifyRouter(emptyQueries(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	require.Contains(t, rec.Body.String(), "не найдена")
}

func TestHandleVerify_ValidReturns200WithSigner(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet,
		"/verify/11111111-1111-1111-1111-111111111111", nil)

	verifyRouter(sigRowQueries(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	body := rec.Body.String()
	require.Contains(t, body, "БАХЫТЖАНОВА ТОЖАН БАХЫТЖАНОВНА")
	require.Contains(t, body, "ДОКУМЕНТ ПОДПИСАН ЭЦП")
	require.Contains(t, body, "8904****1782")               // masked IIN
	require.Contains(t, body, "data:image/png;base64,")     // QR embedded
	require.Contains(t, body, "Сканируйте для проверки")
}
