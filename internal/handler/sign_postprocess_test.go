package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"bytes"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/mkapitanoff/pki-service/internal/ncanode"
	"github.com/mkapitanoff/pki-service/internal/postprocess"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// TestPostprocess_RetryThenSucceed verifies: client-upload fails once (simulated
// transient error), doc goes to postprocess_status=retrying with a scheduled
// next_at, then a second ProcessDocument call (as the poller would do once
// next_at is due) succeeds without re-running the expensive pdfcpu stamping
// (signed_s3_key must not change between attempts).
func TestPostprocess_RetryThenSucceed(t *testing.T) {
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

	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{"name": "retry.pdf", "source_url": "https://mock.test.example.invalid/retry.pdf",
				"target_url": "https://mock.test.example.invalid/retry-target.pdf"},
		},
		"signer_role": "client",
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())
	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	docID := initResp["documents"].([]interface{})[0].(map[string]any)["doc_id"].(string)

	hashSum := sha256.Sum256(testPDF)
	cmsB64 := buildFakeCMSWithDigest(hashSum[:])
	completeBody, _ := json.Marshal(map[string]any{
		"session_id": initResp["session_id"],
		"signatures": []map[string]any{{"doc_id": docID, "cms": cmsB64}},
	})
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, newSessionReq(http.MethodPost, "/api/v1/sign/complete", bytes.NewReader(completeBody), tenantID))
	require.Equal(t, http.StatusOK, completeRec.Code, completeRec.Body.String())

	docUUID, err := uuid.Parse(docID)
	require.NoError(t, err)

	// First attempt: force the client-S3 upload to fail transiently.
	extS3.SetUploadError(context.DeadlineExceeded)

	deps := postprocess.Deps{Queries: q, Store: store, ExtS3: extS3, MaxAttempts: 5}
	doc1, err := q.GetSigningSessionDocument(context.Background(), docUUID)
	require.NoError(t, err)
	postprocess.ProcessDocument(context.Background(), deps, doc1)

	afterFail, err := q.GetSigningSessionDocument(context.Background(), docUUID)
	require.NoError(t, err)
	require.Equal(t, "retrying", afterFail.PostprocessStatus.String)
	require.Equal(t, int32(1), afterFail.PostprocessAttempts)
	require.True(t, afterFail.PostprocessNextAt.Valid)
	require.True(t, afterFail.SignedS3Key.Valid, "internal artifact must already be stored after the first attempt")
	firstSignedKey := afterFail.SignedS3Key.String

	// Force next_at into the past so ListDocumentsDueForPostprocess would pick
	// it up (simulating the poller tick firing later), then run a second
	// attempt — this time it must succeed.
	_, err = db.Exec(`UPDATE signing_session_documents SET postprocess_next_at = now() - interval '1 second' WHERE id=$1`, docID)
	require.NoError(t, err)

	due, err := q.ListDocumentsDueForPostprocess(context.Background(), 10)
	require.NoError(t, err)
	require.Len(t, due, 1)
	require.Equal(t, docUUID, due[0].ID)

	extS3.SetUploadError(nil)
	postprocess.ProcessDocument(context.Background(), deps, due[0])

	final, err := q.GetSigningSessionDocument(context.Background(), docUUID)
	require.NoError(t, err)
	require.Equal(t, "done", final.PostprocessStatus.String)
	require.Equal(t, "uploaded", final.Status)
	require.Equal(t, firstSignedKey, final.SignedS3Key.String, "retry must not re-run pdfcpu stamping")
	require.Equal(t, int32(1), final.PostprocessAttempts, "success does not increment attempts, only failures do")

	extS3.WaitForUploads(t, 1, 5*time.Second)
}

// TestPostprocess_TerminalFailureAfterMaxAttempts verifies bounded retries:
// a permanently-failing upload must land in post_process_failed after
// MaxAttempts, not loop forever, and the session must resolve to "failed".
func TestPostprocess_TerminalFailureAfterMaxAttempts(t *testing.T) {
	db := testDBSession(t)
	tenantID := seedTenantSession(t, db)
	q := repository.New(db)

	testPDF := makeTestPDFSession(t)
	store := storage.NewMockStorage()
	extS3 := s3client.NewMockExternalS3Client()
	extS3.DownloadFunc = func(_ context.Context, _ string) ([]byte, string, error) {
		return testPDF, "application/pdf", nil
	}
	extS3.SetUploadError(context.DeadlineExceeded)
	nc := ncanode.NewMockNCANodeClient()
	router := signSessionRouter(q, nc, store, extS3)

	initBody, _ := json.Marshal(map[string]any{
		"documents": []map[string]any{
			{"name": "fail-forever.pdf", "source_url": "https://mock.test.example.invalid/ff.pdf",
				"target_url": "https://mock.test.example.invalid/ff-target.pdf"},
		},
		"signer_role": "client",
	})
	initRec := httptest.NewRecorder()
	router.ServeHTTP(initRec, newSessionReq(http.MethodPost, "/api/v1/sign/initiate", bytes.NewReader(initBody), tenantID))
	require.Equal(t, http.StatusOK, initRec.Code, initRec.Body.String())
	var initResp map[string]any
	require.NoError(t, json.Unmarshal(initRec.Body.Bytes(), &initResp))
	docID := initResp["documents"].([]interface{})[0].(map[string]any)["doc_id"].(string)

	hashSum := sha256.Sum256(testPDF)
	cmsB64 := buildFakeCMSWithDigest(hashSum[:])
	completeBody, _ := json.Marshal(map[string]any{
		"session_id": initResp["session_id"],
		"signatures": []map[string]any{{"doc_id": docID, "cms": cmsB64}},
	})
	completeRec := httptest.NewRecorder()
	router.ServeHTTP(completeRec, newSessionReq(http.MethodPost, "/api/v1/sign/complete", bytes.NewReader(completeBody), tenantID))
	require.Equal(t, http.StatusOK, completeRec.Code, completeRec.Body.String())

	docUUID, err := uuid.Parse(docID)
	require.NoError(t, err)
	deps := postprocess.Deps{Queries: q, Store: store, ExtS3: extS3, MaxAttempts: 3}

	for i := 0; i < 3; i++ {
		_, err = db.Exec(`UPDATE signing_session_documents SET postprocess_next_at = now() - interval '1 second' WHERE id=$1 AND postprocess_status IN ('queued','retrying')`, docID)
		require.NoError(t, err)
		doc, err := q.GetSigningSessionDocument(context.Background(), docUUID)
		require.NoError(t, err)
		postprocess.ProcessDocument(context.Background(), deps, doc)
	}

	final, err := q.GetSigningSessionDocument(context.Background(), docUUID)
	require.NoError(t, err)
	require.Equal(t, "post_process_failed", final.Status)
	require.Equal(t, "failed", final.PostprocessStatus.String)
	require.False(t, final.PostprocessNextAt.Valid, "terminal failure must not be scheduled for another retry")

	var sessionStatus string
	require.NoError(t, db.QueryRow(`SELECT status FROM signing_sessions WHERE id=$1`, initResp["session_id"]).Scan(&sessionStatus))
	require.Equal(t, "failed", sessionStatus, "session must resolve, not hang, once its only doc terminally fails")

	_ = sql.ErrNoRows
}
