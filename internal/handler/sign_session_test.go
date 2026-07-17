package handler

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"

	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/pdf"
	"github.com/mkapitanoff/pki-service/internal/postprocess"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// ---- helpers ----------------------------------------------------------------

func testDBSession(t *testing.T) *sql.DB {
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

// seedTenantSession creates a tenant row and registers cleanup in FK order.
func seedTenantSession(t *testing.T, db *sql.DB) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	id := uuid.New()
	_, err := db.ExecContext(ctx,
		`INSERT INTO tenants (id, name, type) VALUES ($1, $2, 'individual')`,
		id, "Session Test Tenant "+id.String()[:8])
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec(`
			DELETE FROM signing_session_documents
			WHERE session_id IN (SELECT id FROM signing_sessions WHERE tenant_id=$1)`, id)
		_, _ = db.Exec(`DELETE FROM signing_sessions WHERE tenant_id=$1`, id)
		_, _ = db.Exec(`DELETE FROM tenants WHERE id=$1`, id)
	})
	return id
}

// makeTestPDFSession returns a minimal valid PDF using the sign-page generator.
func makeTestPDFSession(t *testing.T) []byte {
	t.Helper()
	data, err := pdf.GenerateSignPage(nil)
	require.NoError(t, err)
	return data
}

// waitDocumentStatus polls signing_session_documents until status matches or timeout.
func waitDocumentStatus(t *testing.T, db *sql.DB, docID, want string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		var status string
		err := db.QueryRow(`SELECT status FROM signing_session_documents WHERE id=$1`, docID).Scan(&status)
		if err == nil && status == want {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	var got string
	_ = db.QueryRow(`SELECT status FROM signing_session_documents WHERE id=$1`, docID).Scan(&got)
	t.Fatalf("waitDocumentStatus: timeout after %s waiting for %q; last=%q", timeout, want, got)
}

// signSessionRouter wires the four signing-session HTTP endpoints.
func signSessionRouter(
	q *repository.Queries,
	nc ncanode.NCANodeClient,
	store storage.Storage,
	extS3 s3client.ExternalS3Client,
) http.Handler {
	r := chi.NewRouter()
	r.Post("/api/v1/sign/initiate", NewSignInitiateHandler(q, extS3, store, nil).HandleInitiate)
	r.Post("/api/v1/sign/complete", NewSignCompleteHandler(q, nc, store, false, 0, "https://pki.test.local").HandleComplete)
	r.Get("/api/v1/sign/status/{session_id}", NewSignStatusHandler(q, extS3, store).HandleGetStatus)
	r.Patch("/api/v1/sign/refresh-urls", NewSignStatusHandler(q, extS3, store).HandleRefreshURLs)
	return r
}

// runPostprocess drives the background postprocessing step synchronously in
// tests, standing in for PostprocessWorker's ticker (which isn't running in
// the test process). Mirrors exactly what the worker would do on its next tick.
func runPostprocess(t *testing.T, q *repository.Queries, store storage.Storage, extS3 s3client.ExternalS3Client, docID uuid.UUID) {
	t.Helper()
	doc, err := q.GetSigningSessionDocument(context.Background(), docID)
	require.NoError(t, err)
	postprocess.ProcessDocument(context.Background(), postprocess.Deps{
		Queries:     q,
		Store:       store,
		ExtS3:       extS3,
		MaxAttempts: 5,
	}, doc)
}

// newSessionReq builds a request with a tenant injected into the context.
func newSessionReq(method, path string, body io.Reader, tenantID uuid.UUID) *http.Request {
	req := httptest.NewRequest(method, path, body)
	req = req.WithContext(WithTenant(req.Context(), tenantID))
	return req
}

// buildFakeCMSWithDigest constructs a minimal PKCS#7 SignedData DER blob whose
// messageDigest authenticated attribute contains the given digest as a plain
// OCTET STRING. This triggers the fallback path in signer.ExtractHashFromCMS.
// Returns standard base64.
func buildFakeCMSWithDigest(digest []byte) string {
	oidSignedData := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidData := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
	oidMsgDigest := asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}

	must := func(b []byte, err error) []byte {
		if err != nil {
			panic(fmt.Sprintf("asn1.Marshal: %v", err))
		}
		return b
	}
	seqOf := func(content []byte) []byte {
		return must(asn1.Marshal(asn1.RawValue{Tag: 16, Class: 0, IsCompound: true, Bytes: content}))
	}
	setOf := func(content []byte) []byte {
		return must(asn1.Marshal(asn1.RawValue{Tag: 17, Class: 0, IsCompound: true, Bytes: content}))
	}
	ctx0 := func(content []byte) []byte {
		return must(asn1.Marshal(asn1.RawValue{Tag: 0, Class: 2, IsCompound: true, Bytes: content}))
	}
	cat := func(parts ...[]byte) []byte {
		var out []byte
		for _, p := range parts {
			out = append(out, p...)
		}
		return out
	}

	// Attribute: SEQUENCE { OID(messageDigest), OCTET_STRING(digest) }
	// Values is a plain OCTET STRING to trigger the fallback unmarshal path.
	attrSEQ := seqOf(cat(
		must(asn1.Marshal(oidMsgDigest)),
		must(asn1.Marshal(digest)),
	))

	// AuthAttributes: [0] context tag wrapping the single attribute SEQUENCE.
	authAttrs := ctx0(attrSEQ)

	ver1 := must(asn1.Marshal(1))
	emptySeq := seqOf(nil)
	emptyOctet := must(asn1.Marshal([]byte{}))

	// signerInfo SEQUENCE: version, SID, digestAlg, authAttrs, encAlg, encDigest.
	signerInfoSEQ := seqOf(cat(ver1, emptySeq, emptySeq, authAttrs, emptySeq, emptyOctet))

	// SignedData SEQUENCE.
	signedDataSEQ := seqOf(cat(
		ver1,
		setOf(nil),                           // DigestAlgorithms: empty SET
		seqOf(must(asn1.Marshal(oidData))),   // EncapsulatedContentInfo
		setOf(signerInfoSEQ),                 // SignerInfos: SET { signerInfo }
	))

	// ContentInfo: SEQUENCE { OID(signedData), [0] { SignedData } }
	ciSEQ := seqOf(cat(must(asn1.Marshal(oidSignedData)), ctx0(signedDataSEQ)))

	return base64.StdEncoding.EncodeToString(ciSEQ)
}

// ---- tests ------------------------------------------------------------------

func TestSignInitiate_Success(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	body, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "test.pdf",
				"source_url": "https://mock.test.example.invalid/source.pdf",
				"target_url": "https://mock.test.example.invalid/target.pdf",
			},
		},
		"signer_role": "client",
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(body), tenantID))

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotEmpty(t, resp["session_id"])
	require.NotEmpty(t, resp["expires_at"])

	docs := resp["documents"].([]interface{})
	require.Len(t, docs, 1)
	doc := docs[0].(map[string]any)
	require.Equal(t, "ready", doc["status"])
	require.NotEmpty(t, doc["hash"])
	require.Equal(t, "SHA256", doc["hash_algorithm"])
}

func TestSignInitiate_FetchFailed(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return nil, "", s3client.ErrPresignedURLExpired
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	body, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "fail.pdf",
				"source_url": "https://mock.test.example.invalid/fail.pdf",
				"target_url": "https://mock.test.example.invalid/fail-target.pdf",
			},
		},
		"signer_role": "manager",
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(body), tenantID))

	require.Equal(t, http.StatusBadGateway, rec.Code, rec.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	errObj := resp["error"].(map[string]any)
	require.Equal(t, "FETCH_FAILED", errObj["code"])
}

func TestSignInitiate_HashConsistency(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	body, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "hash-check.pdf",
				"source_url": "https://mock.test.example.invalid/hash-check.pdf",
				"target_url": "https://mock.test.example.invalid/hash-check-target.pdf",
			},
		},
		"signer_role": "director",
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(body), tenantID))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	doc := resp["documents"].([]interface{})[0].(map[string]any)

	// The hash in the response is base64(SHA256(pdf)).
	expectedSum := sha256.Sum256(testPDF)
	expectedB64 := base64.StdEncoding.EncodeToString(expectedSum[:])
	require.Equal(t, expectedB64, doc["hash"])
}

func TestSignComplete_Success(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	// Step 1: initiate.
	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "complete.pdf",
				"source_url": "https://mock.test.example.invalid/complete.pdf",
				"target_url": "https://mock.test.example.invalid/complete-target.pdf",
			},
		},
		"signer_role": "client",
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())

	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	sessionID := initResp["session_id"].(string)
	docID := initResp["documents"].([]interface{})[0].(map[string]any)["doc_id"].(string)

	// Build CMS with the correct hash of testPDF.
	hashSum := sha256.Sum256(testPDF)
	cmsB64 := buildFakeCMSWithDigest(hashSum[:])

	// Step 2: complete.
	completeBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"signatures": []map[string]any{
			{"doc_id": docID, "cms": cmsB64},
		},
	})
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, newSessionReq(http.MethodPost, "/api/v1/sign/complete", bytes.NewReader(completeBody), tenantID))
	require.Equal(t, http.StatusOK, completeRec.Code, completeRec.Body.String())

	var completeResp map[string]any
	require.NoError(t, json.Unmarshal(completeRec.Body.Bytes(), &completeResp))
	require.Equal(t, float64(1), completeResp["succeeded"])
	require.Equal(t, float64(0), completeResp["failed"])

	respDocs := completeResp["documents"].([]interface{})
	require.Len(t, respDocs, 1)
	require.Equal(t, "signed", respDocs[0].(map[string]any)["status"])
	require.Empty(t, respDocs[0].(map[string]any)["s3_key"],
		"s3_key must not be present before background postprocessing finishes")

	// Drive the background postprocessing step (QR stamp + sign page + client
	// upload) that PostprocessWorker's ticker would otherwise do.
	docUUID, err := uuid.Parse(docID)
	require.NoError(t, err)
	runPostprocess(t, q, store, extS3, docUUID)

	extS3.WaitForUploads(t, 1, 5*time.Second)
}

func TestSignComplete_HashMismatch(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	// Initiate.
	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "mismatch.pdf",
				"source_url": "https://mock.test.example.invalid/mismatch.pdf",
				"target_url": "https://mock.test.example.invalid/mismatch-target.pdf",
			},
		},
		"signer_role": "client",
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())

	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	sessionID := initResp["session_id"].(string)
	docID := initResp["documents"].([]interface{})[0].(map[string]any)["doc_id"].(string)

	// CMS with wrong hash (all zeros ≠ SHA256(testPDF)).
	cmsB64 := buildFakeCMSWithDigest(make([]byte, 32))

	completeBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"signatures": []map[string]any{
			{"doc_id": docID, "cms": cmsB64},
		},
	})
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, newSessionReq(http.MethodPost, "/api/v1/sign/complete", bytes.NewReader(completeBody), tenantID))
	require.Equal(t, http.StatusOK, completeRec.Code, completeRec.Body.String())

	var completeResp map[string]any
	require.NoError(t, json.Unmarshal(completeRec.Body.Bytes(), &completeResp))
	require.Equal(t, float64(0), completeResp["succeeded"])
	require.Equal(t, float64(1), completeResp["failed"])

	failedDoc := completeResp["documents"].([]interface{})[0].(map[string]any)
	require.Equal(t, "error", failedDoc["status"])
	require.Contains(t, failedDoc["error"].(string), "hash_mismatch")
}

func TestSignComplete_SessionExpired(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	// Insert an already-expired session directly.
	var sessionID string
	err := db.QueryRow(`
		INSERT INTO signing_sessions (tenant_id, signer_role, expires_at)
		VALUES ($1, 'client', now() - interval '1 hour')
		RETURNING id`, tenantID).Scan(&sessionID)
	require.NoError(t, err)

	completeBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"signatures": []map[string]any{
			{"doc_id": "00000000-0000-0000-0000-000000000001", "cms": buildFakeCMSWithDigest(make([]byte, 32))},
		},
	})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, newSessionReq(http.MethodPost, "/api/v1/sign/complete", bytes.NewReader(completeBody), tenantID))

	require.Equal(t, http.StatusGone, rec.Code)
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "SESSION_EXPIRED", resp["error"].(map[string]any)["code"])
}

func TestSignStatus_Polling(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	// Create a session via initiate.
	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "status.pdf",
				"source_url": "https://mock.test.example.invalid/status.pdf",
				"target_url": "https://mock.test.example.invalid/status-target.pdf",
			},
		},
		"signer_role": "client",
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())

	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	sessionID := initResp["session_id"].(string)

	// Poll status.
	statusRec := httptest.NewRecorder()
	router.ServeHTTP(statusRec, newSessionReq(http.MethodGet, "/api/v1/sign/status/"+sessionID, nil, tenantID))
	require.Equal(t, http.StatusOK, statusRec.Code, statusRec.Body.String())

	var statusResp map[string]any
	require.NoError(t, json.Unmarshal(statusRec.Body.Bytes(), &statusResp))
	require.Equal(t, sessionID, statusResp["session_id"])
	require.NotEmpty(t, statusResp["expires_at"])
	require.NotNil(t, statusResp["documents"])
	require.NotNil(t, statusResp["status"])
	// After a successful initiate the document should be in "ready" state.
	docs := statusResp["documents"].([]interface{})
	require.Len(t, docs, 1)
	require.Equal(t, "ready", docs[0].(map[string]any)["status"])
}

func TestSignRefreshURLs(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	// Initiate to create session + doc.
	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "refresh.pdf",
				"source_url": "https://mock.test.example.invalid/refresh.pdf",
				"target_url": "https://mock.test.example.invalid/refresh-target.pdf",
			},
		},
		"signer_role": "client",
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())

	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	sessionID := initResp["session_id"].(string)
	docID := initResp["documents"].([]interface{})[0].(map[string]any)["doc_id"].(string)

	// Force the doc into upload_failed so refresh-urls will accept it.
	_, err := db.Exec(`
		UPDATE signing_session_documents
		SET status='upload_failed', last_error='presigned_url_expired'
		WHERE id=$1`, docID)
	require.NoError(t, err)

	refreshBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"documents": []map[string]any{
			{"doc_id": docID, "target_url": "https://mock.test.example.invalid/refresh-new-target.pdf"},
		},
	})
	refreshRec := httptest.NewRecorder()
	router.ServeHTTP(refreshRec, newSessionReq(http.MethodPatch, "/api/v1/sign/refresh-urls", bytes.NewReader(refreshBody), tenantID))
	require.Equal(t, http.StatusOK, refreshRec.Code, refreshRec.Body.String())

	var refreshResp map[string]any
	require.NoError(t, json.Unmarshal(refreshRec.Body.Bytes(), &refreshResp))
	require.Equal(t, float64(1), refreshResp["refreshed"])
	refreshedDocs := refreshResp["documents"].([]interface{})
	require.Len(t, refreshedDocs, 1)
	require.Equal(t, "signed", refreshedDocs[0].(map[string]any)["status"])
}

func TestWebhookHMAC(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	nc := ncanode.NewMockNCANodeClient()

	// Plain HTTP server — signer.webhookHTTPClient uses default transport.
	type capturedWebhook struct {
		body      []byte
		signature string
		event     string
	}
	webhookCh := make(chan capturedWebhook, 5)

	webhookSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		webhookCh <- capturedWebhook{
			body:      body,
			signature: r.Header.Get("X-PKI-Signature"),
			event:     r.Header.Get("X-PKI-Event"),
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(webhookSrv.Close)

	router := signSessionRouter(q, nc, store, extS3)
	secret := "test-webhook-secret-abc"

	// Initiate with callback_url pointing to the test server.
	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{
				"name":       "webhook.pdf",
				"source_url": "https://mock.test.example.invalid/webhook.pdf",
				"target_url": "https://mock.test.example.invalid/webhook-target.pdf",
			},
		},
		"signer_role":     "client",
		"callback_url":    webhookSrv.URL,
		"callback_secret": secret,
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())

	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	sessionID := initResp["session_id"].(string)
	docID := initResp["documents"].([]interface{})[0].(map[string]any)["doc_id"].(string)

	// Complete with correct hash → triggers async upload → webhook.
	hashSum := sha256.Sum256(testPDF)
	cmsB64 := buildFakeCMSWithDigest(hashSum[:])

	completeBody, _ := json.Marshal(map[string]any{
		"session_id": sessionID,
		"signatures": []map[string]any{
			{"doc_id": docID, "cms": cmsB64},
		},
	})
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, newSessionReq(http.MethodPost, "/api/v1/sign/complete", bytes.NewReader(completeBody), tenantID))
	require.Equal(t, http.StatusOK, completeRec.Code, completeRec.Body.String())

	// Drive background postprocessing — webhook fires from here, not from
	// /sign/complete itself.
	docUUID, err := uuid.Parse(docID)
	require.NoError(t, err)
	runPostprocess(t, q, store, extS3, docUUID)

	// Wait for the webhook delivery.
	select {
	case wh := <-webhookCh:
		// Verify X-PKI-Signature: "sha256=<hex(HMAC-SHA256(body, secret))>"
		require.NotEmpty(t, wh.signature)
		require.True(t, len(wh.signature) > 7, "signature too short: %q", wh.signature)
		require.Equal(t, "sha256=", wh.signature[:7])

		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write(wh.body)
		expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
		require.Equal(t, expected, wh.signature)
		require.NotEmpty(t, wh.event)

	case <-time.After(15 * time.Second):
		t.Fatal("timed out waiting for webhook delivery (15s)")
	}
}
