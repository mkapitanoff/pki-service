package handler

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/mkapitanoff/pki-service/internal/repository"
)

// --- minimal database/sql driver that returns zero rows for every query ---

type emptyDriver struct{}

type emptyConn struct{}

type emptyRows struct{}

func (emptyDriver) Open(string) (driver.Conn, error) { return emptyConn{}, nil }

func (emptyConn) Prepare(string) (driver.Stmt, error) { return nil, driver.ErrSkip }
func (emptyConn) Close() error                        { return nil }
func (emptyConn) Begin() (driver.Tx, error)           { return nil, driver.ErrSkip }

func (emptyConn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return emptyRows{}, nil
}

func (emptyConn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(0), nil
}

func (emptyRows) Columns() []string              { return []string{} }
func (emptyRows) Close() error                   { return nil }
func (emptyRows) Next([]driver.Value) error      { return io.EOF }

func init() {
	sql.Register("emptyfake", emptyDriver{})
}

func emptyQueries(t *testing.T) *repository.Queries {
	t.Helper()
	db, err := sql.Open("emptyfake", "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return repository.New(db)
}

func newRouter(q *repository.Queries) http.Handler {
	r := chi.NewRouter()
	r.Route("/api/v1", func(api chi.Router) {
		api.Use(APIKeyAuth(q))
		api.Post("/documents/{id}/sign", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})
	})
	return r
}

func TestHandleSign_NoAuthHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/documents/00000000-0000-0000-0000-000000000001/sign", nil)

	newRouter(emptyQueries(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "UNAUTHORIZED")
}

func TestHandleSign_InvalidKey(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/documents/00000000-0000-0000-0000-000000000001/sign", nil)
	req.Header.Set("Authorization", "Bearer totally-invalid-key")

	newRouter(emptyQueries(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Contains(t, rec.Body.String(), "UNAUTHORIZED")
}

func TestHandleSign_MalformedAuthHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost,
		"/api/v1/documents/00000000-0000-0000-0000-000000000001/sign", nil)
	req.Header.Set("Authorization", "Basic abc123")

	newRouter(emptyQueries(t)).ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
}
