-- name: CreateApplication :one
INSERT INTO applications (
    tenant_id, external_id, status, signing_round, signer_role,
    callback_url, callback_secret
) VALUES (
    $1, $2, 'active', 1, $3, $4, $5
)
RETURNING *;

-- name: GetApplicationByExternalID :one
SELECT * FROM applications
WHERE tenant_id = $1 AND external_id = $2;

-- name: GetApplicationByID :one
SELECT * FROM applications
WHERE id = $1 AND tenant_id = $2;

-- name: UpdateApplicationStatus :one
UPDATE applications
SET status = $2, signing_round = $3, signer_role = $4, callback_url = COALESCE($5, callback_url),
    callback_secret = COALESCE($6, callback_secret), updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CancelApplication :one
UPDATE applications
SET status = 'cancelled', cancelled_at = now(), cancel_reason = $2, updated_at = now()
WHERE id = $1
RETURNING *;

-- name: CreateApplicationDocument :one
INSERT INTO application_documents (
    application_id, document_name, version, signing_round,
    source_url, target_url, target_s3_key, status
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, 'pending'
)
RETURNING *;

-- name: GetApplicationDocumentByID :one
SELECT * FROM application_documents
WHERE id = $1;

-- name: ListApplicationDocuments :many
SELECT * FROM application_documents
WHERE application_id = $1
ORDER BY created_at;

-- name: ListActiveApplicationDocuments :many
SELECT * FROM application_documents
WHERE application_id = $1 AND signing_round = $2 AND status != 'superseded'
ORDER BY created_at;

-- name: UpdateApplicationDocumentStatus :one
UPDATE application_documents
SET status = $2, last_error = $3
WHERE id = $1
RETURNING *;

-- name: UpdateApplicationDocumentAfterFetch :one
UPDATE application_documents
SET document_id = $2, status = 'ready'
WHERE id = $1
RETURNING *;

-- name: UpdateApplicationDocumentTargetURL :one
UPDATE application_documents
SET target_url = $2
WHERE id = $1
RETURNING *;

-- name: MarkApplicationDocumentUploaded :one
UPDATE application_documents
SET status = 'uploaded', uploaded_at = now()
WHERE id = $1
RETURNING *;

-- name: IncrementUploadAttempts :one
UPDATE application_documents
SET upload_attempts = upload_attempts + 1, last_error = $2
WHERE id = $1
RETURNING *;

-- name: SupersedeApplicationDocument :one
UPDATE application_documents
SET status = 'superseded', superseded_by = $2
WHERE id = $1
RETURNING *;

-- name: FindPreviousVersions :many
SELECT * FROM application_documents
WHERE application_id = $1 AND document_name = $2 AND status != 'superseded'
ORDER BY version;

-- name: FindLatestLinkedDocumentID :one
-- Последняя (по version) запись того же документа заявки с уже привязанным
-- document_id — НЕ фильтруем по status: предыдущий раунд к этому моменту уже
-- superseded (HandleSubmit помечает его так при создании новой версии), но
-- documents.id из него нужно переиспользовать, чтобы Sign() увидел историю
-- подписей (см. баг: каждый раунд иначе получал новый documents.id и терял
-- накопленные подписи/QR-штампы предыдущих раундов).
SELECT document_id FROM application_documents
WHERE application_id = $1 AND document_name = $2 AND document_id IS NOT NULL
ORDER BY version DESC
LIMIT 1;

-- name: GetPendingFetchDocuments :many
SELECT * FROM application_documents
WHERE status = 'pending'
ORDER BY created_at
LIMIT $1;

-- name: CreateApplicationWebhook :one
INSERT INTO application_webhooks (
    application_id, event_type, payload, hmac_signature, status, next_attempt_at
) VALUES (
    $1, $2, $3, $4, 'pending', now()
)
RETURNING *;

-- name: GetPendingWebhooks :many
SELECT aw.* FROM application_webhooks aw
WHERE (aw.status = 'pending' OR (aw.status = 'failed' AND aw.attempts < 5))
  AND (aw.next_attempt_at IS NULL OR aw.next_attempt_at <= now())
ORDER BY aw.created_at
LIMIT 50;

-- name: UpdateWebhookStatus :one
UPDATE application_webhooks
SET status = $2, attempts = $3, last_attempt_at = $4,
    delivered_at = $5, next_attempt_at = $6
WHERE id = $1
RETURNING *;
