-- name: CreateSigningSession :one
INSERT INTO signing_sessions (
    tenant_id, application_id, signer_role, callback_url, callback_secret, status, expires_at
) VALUES (
    $1, $2, $3, $4, $5, 'pending', COALESCE($6, now() + INTERVAL '2 hours')
)
RETURNING *;

-- name: GetSigningSession :one
SELECT * FROM signing_sessions
WHERE id = $1 AND tenant_id = $2;

-- name: GetSigningSessionByID :one
SELECT * FROM signing_sessions
WHERE id = $1;

-- name: UpdateSigningSessionStatus :one
UPDATE signing_sessions
SET status = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateSigningSessionDocument :one
INSERT INTO signing_session_documents (
    session_id, document_name, source_url, target_url, target_s3_key, content_hash, status
) VALUES (
    $1, $2, $3, $4, $5, '', 'pending'
)
RETURNING *;

-- name: GetSigningSessionDocument :one
SELECT * FROM signing_session_documents
WHERE id = $1;

-- name: ListSessionDocuments :many
SELECT * FROM signing_session_documents
WHERE session_id = $1
ORDER BY created_at;

-- name: ListReadySessionDocuments :many
SELECT * FROM signing_session_documents
WHERE session_id = $1 AND status = 'ready'
ORDER BY created_at;

-- name: UpdateSessionDocumentStatus :one
UPDATE signing_session_documents
SET status = $2, last_error = $3
WHERE id = $1
RETURNING *;

-- name: UpdateSessionDocumentAfterFetch :one
UPDATE signing_session_documents
SET content_hash = $2, cached_s3_key = $3, status = 'ready'
WHERE id = $1
RETURNING *;

-- name: UpdateSessionDocumentAfterSign :one
UPDATE signing_session_documents
SET cms_s3_key = $2, signed_s3_key = $3, status = 'signed', signed_at = now()
WHERE id = $1
RETURNING *;

-- name: UpdateSessionDocumentTargetURL :one
UPDATE signing_session_documents
SET target_url = $2
WHERE id = $1
RETURNING *;

-- name: MarkSessionDocumentUploaded :one
UPDATE signing_session_documents
SET status = 'uploaded', uploaded_at = now()
WHERE id = $1
RETURNING *;

-- name: IncrementSessionDocumentUploadAttempts :one
UPDATE signing_session_documents
SET upload_attempts = upload_attempts + 1, last_error = $2
WHERE id = $1
RETURNING *;

-- name: ResetSessionDocumentForRetry :one
UPDATE signing_session_documents
SET status = 'signed', upload_attempts = 0, last_error = NULL, target_url = $2
WHERE id = $1
RETURNING *;

-- name: GetExpiredSessions :many
SELECT * FROM signing_sessions
WHERE expires_at < now()
  AND status NOT IN ('completed', 'failed', 'expired')
ORDER BY expires_at
LIMIT 100;

-- name: GetPendingFetchSessionDocuments :many
SELECT ssd.* FROM signing_session_documents ssd
JOIN signing_sessions ss ON ss.id = ssd.session_id
WHERE ssd.status = 'pending'
  AND ss.status NOT IN ('expired', 'failed', 'completed')
  AND ss.expires_at > now()
ORDER BY ssd.created_at
LIMIT 20;
