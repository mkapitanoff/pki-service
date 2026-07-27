package worker

import (
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/mkapitanoff/pki-service/internal/repository"
)

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

// TestLinkDocument_ReusesDocumentIDAcrossRounds is the regression test for the
// bug reported by an integrator: signing the same application document across
// two rounds produced two separate one-signature "Лист подписей" pages and
// only one visible QR stamp instead of one cumulative page listing both
// signers in order. Root cause: linkDocument's predecessor (createDocument)
// always inserted a fresh documents row per round, so service.Sign()'s
// signature-history lookup (by document_id) always came back empty for round
// 2+. This asserts round 2 reuses round 1's documents.id.
func TestLinkDocument_ReusesDocumentIDAcrossRounds(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	q := repository.New(db)
	f := &DocumentFetcher{db: db, queries: q}

	tenantID := uuid.New()
	_, err := db.ExecContext(ctx, `INSERT INTO tenants (id, name, type) VALUES ($1, $2, 'individual')`, tenantID, "Test Tenant")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM application_documents WHERE application_id IN (SELECT id FROM applications WHERE tenant_id=$1)`, tenantID)
		_, _ = db.Exec(`DELETE FROM applications WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM documents WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	app, err := q.CreateApplication(ctx, repository.CreateApplicationParams{
		TenantID:   tenantID,
		ExternalID: "app-1",
		SignerRole: "client",
	})
	require.NoError(t, err)

	const docName = "agreement.pdf"

	// Round 1: no application_documents row yet with this name → creates a
	// fresh documents row.
	round1Doc := repository.ApplicationDocument{ApplicationID: app.ID, DocumentName: docName, Version: 1}
	docID1, err := f.linkDocument(ctx, tenantID, round1Doc, "tenant/app/round1.pdf")
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, docID1)

	appDoc1, err := q.CreateApplicationDocument(ctx, repository.CreateApplicationDocumentParams{
		ApplicationID: app.ID,
		DocumentName:  docName,
		Version:       1,
		SigningRound:  1,
		SourceUrl:     "https://client/round1.pdf",
	})
	require.NoError(t, err)
	_, err = q.UpdateApplicationDocumentAfterFetch(ctx, repository.UpdateApplicationDocumentAfterFetchParams{
		ID:         appDoc1.ID,
		DocumentID: uuid.NullUUID{UUID: docID1, Valid: true},
	})
	require.NoError(t, err)
	_, err = q.SupersedeApplicationDocument(ctx, repository.SupersedeApplicationDocumentParams{ID: appDoc1.ID})
	require.NoError(t, err)

	// Round 2: a previous linked application_documents row exists (now
	// superseded) → must reuse docID1, not mint a new documents.id.
	round2Doc := repository.ApplicationDocument{ApplicationID: app.ID, DocumentName: docName, Version: 2}
	docID2, err := f.linkDocument(ctx, tenantID, round2Doc, "tenant/app/round2.pdf")
	require.NoError(t, err)
	require.Equal(t, docID1, docID2, "round 2 must reuse round 1's documents.id so Sign() sees prior-round signatures")

	// s3_key_current must reflect round 2's freshly fetched content.
	updatedDoc, err := q.GetDocument(ctx, repository.GetDocumentParams{ID: docID1, TenantID: tenantID})
	require.NoError(t, err)
	require.Equal(t, "tenant/app/round2.pdf", updatedDoc.S3KeyCurrent)
}

// TestLinkDocument_DifferentNameCreatesNewDocument guards against
// over-matching: a document with a different name in the same application
// must NOT reuse another document's documents.id.
func TestLinkDocument_DifferentNameCreatesNewDocument(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	q := repository.New(db)
	f := &DocumentFetcher{db: db, queries: q}

	tenantID := uuid.New()
	_, err := db.ExecContext(ctx, `INSERT INTO tenants (id, name, type) VALUES ($1, $2, 'individual')`, tenantID, "Test Tenant")
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`DELETE FROM application_documents WHERE application_id IN (SELECT id FROM applications WHERE tenant_id=$1)`, tenantID)
		_, _ = db.Exec(`DELETE FROM applications WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM documents WHERE tenant_id=$1`, tenantID)
		_, _ = db.Exec(`DELETE FROM tenants WHERE id=$1`, tenantID)
	})

	app, err := q.CreateApplication(ctx, repository.CreateApplicationParams{
		TenantID:   tenantID,
		ExternalID: "app-2",
		SignerRole: "client",
	})
	require.NoError(t, err)

	docA := repository.ApplicationDocument{ApplicationID: app.ID, DocumentName: "a.pdf", Version: 1}
	docIDA, err := f.linkDocument(ctx, tenantID, docA, "tenant/app/a.pdf")
	require.NoError(t, err)

	docB := repository.ApplicationDocument{ApplicationID: app.ID, DocumentName: "b.pdf", Version: 1}
	docIDB, err := f.linkDocument(ctx, tenantID, docB, "tenant/app/b.pdf")
	require.NoError(t, err)

	require.NotEqual(t, docIDA, docIDB)
}
