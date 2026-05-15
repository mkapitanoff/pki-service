package service

import (
	"context"
	"database/sql"
	stderrors "errors"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// testDB opens the DB from DATABASE_URL or skips the test.
func testDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping DB-backed test")
	}
	db, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	if err := db.Ping(); err != nil {
		t.Skipf("cannot reach DATABASE_URL: %v", err)
	}
	return db
}

// seed creates a tenant + a draft document with a valid original PDF in mock
// storage. Returns tenantID, documentID and the storage instance.
func seed(t *testing.T, db *sql.DB, st *storage.MockStorage, metadata string) (uuid.UUID, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	q := repository.New(db)

	tenantID := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO tenants (id, name, type) VALUES ($1, $2, 'individual')`,
		tenantID, "Test Tenant")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM audit_log WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM signatures WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM document_versions WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM documents WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	origPDF, err := pdf.GenerateSignPage(nil)
	require.NoError(t, err)

	key := st.BuildKey(tenantID, uuid.Nil, "original.pdf")
	require.NoError(t, st.UploadFile(ctx, key, origPDF, "application/pdf"))

	doc, err := q.CreateDocument(ctx, repository.CreateDocumentParams{
		TenantID:       tenantID,
		Title:          sql.NullString{String: "Doc", Valid: true},
		S3KeyOriginal:  key,
		S3KeyCurrent:   key,
		CurrentVersion: 0,
		Status:         repository.DocStatusDraft,
	})
	require.NoError(t, err)

	if metadata != "" {
		_, err = db.ExecContext(ctx,
			`UPDATE documents SET metadata=$2::jsonb WHERE id=$1`, doc.ID, metadata)
		require.NoError(t, err)
	}
	return tenantID, doc.ID
}

func newService(db *sql.DB, nc ncanode.NCANodeClient, st storage.Storage) *SignService {
	return NewSignService(db, nc, st, repository.New(db), nil, "https://test.sign.example.kz")
}

func TestSign_FullFlow(t *testing.T) {
	db := testDB(t)
	st := storage.NewMockStorage()
	tenantID, docID := seed(t, db, st, "")

	svc := newService(db, ncanode.NewMockNCANodeClient(), st)

	res, err := svc.Sign(context.Background(), SignInput{
		DocumentID: docID,
		TenantID:   tenantID,
		CMS:        "VALIDCMS",
		Role:       "client",
	})
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, res.SignatureID)
	require.NotEmpty(t, res.SignedDocumentURL)
	require.Equal(t, "ТЕСТОВ ТЕСТ ТЕСТОВИЧ", res.Signature.SignerName)
	require.Equal(t, int32(1), res.Signature.SequenceNum)

	// New version PDF must exist in storage.
	_, derr := st.DownloadFile(context.Background(), res.SignedDocumentURL)
	require.NoError(t, derr)

	// Document advanced to partially_signed (no expected_signatures set).
	q := repository.New(db)
	doc, err := q.GetDocument(context.Background(), repository.GetDocumentParams{ID: docID, TenantID: tenantID})
	require.NoError(t, err)
	require.Equal(t, repository.DocStatusPartiallySigned, doc.Status)
	require.Equal(t, int32(1), doc.CurrentVersion)
}

func TestSign_CMSInvalid(t *testing.T) {
	db := testDB(t)
	st := storage.NewMockStorage()
	tenantID, docID := seed(t, db, st, "")

	mock := ncanode.NewMockNCANodeClient()
	mock.RegisterInvalid("BADCMS")
	svc := newService(db, mock, st)

	_, err := svc.Sign(context.Background(), SignInput{
		DocumentID: docID, TenantID: tenantID, CMS: "BADCMS", Role: "client",
	})
	require.Error(t, err)
	require.True(t, stderrors.Is(err, apperr.ErrCMSInvalid))
}

func TestSign_CertRevoked(t *testing.T) {
	db := testDB(t)
	st := storage.NewMockStorage()
	tenantID, docID := seed(t, db, st, "")

	mock := ncanode.NewMockNCANodeClient()
	mock.RegisterRevoked("REVOKEDCMS")
	svc := newService(db, mock, st)

	_, err := svc.Sign(context.Background(), SignInput{
		DocumentID: docID, TenantID: tenantID, CMS: "REVOKEDCMS", Role: "client",
	})
	require.Error(t, err)
	require.True(t, stderrors.Is(err, apperr.ErrCertRevoked))
}

func TestSign_StatusTransitions(t *testing.T) {
	db := testDB(t)
	st := storage.NewMockStorage()
	// expected_signatures=2: draft -> partially_signed -> signed.
	tenantID, docID := seed(t, db, st, `{"expected_signatures":2}`)

	svc := newService(db, ncanode.NewMockNCANodeClient(), st)
	q := repository.New(db)

	doc0, _ := q.GetDocument(context.Background(), repository.GetDocumentParams{ID: docID, TenantID: tenantID})
	require.Equal(t, repository.DocStatusDraft, doc0.Status)

	_, err := svc.Sign(context.Background(), SignInput{DocumentID: docID, TenantID: tenantID, CMS: "C1", Role: "client"})
	require.NoError(t, err)
	doc1, _ := q.GetDocument(context.Background(), repository.GetDocumentParams{ID: docID, TenantID: tenantID})
	require.Equal(t, repository.DocStatusPartiallySigned, doc1.Status)

	_, err = svc.Sign(context.Background(), SignInput{DocumentID: docID, TenantID: tenantID, CMS: "C2", Role: "factor"})
	require.NoError(t, err)
	doc2, _ := q.GetDocument(context.Background(), repository.GetDocumentParams{ID: docID, TenantID: tenantID})
	require.Equal(t, repository.DocStatusSigned, doc2.Status)
}
