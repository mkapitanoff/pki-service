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
    session_id, document_name, source_url, target_url, target_s3_key,
    content_hash, status, client_index, client_ref
) VALUES (
    $1, $2, $3, $4, $5, '', 'pending', $6, $7
)
RETURNING *;

-- name: CreateSigningSessionDocumentWithHash :one
-- client-mode: хэш и метаданные пришли из /sign/initiate; статус сразу 'ready',
-- фетчер для подсчёта хэша не нужен (PDF всё равно кэшируется при первом обращении).
INSERT INTO signing_session_documents (
    session_id, document_name, source_url, target_url, target_s3_key,
    content_hash, hash_source,
    source_s3_bucket, source_s3_key, source_content_type, source_size_bytes,
    status, client_index, client_ref
) VALUES (
    $1, $2, $3, $4, $5,
    $6, 'client',
    $7, $8, $9, $10,
    'ready', $11, $12
)
RETURNING *;

-- name: GetSigningSessionDocument :one
SELECT * FROM signing_session_documents
WHERE id = $1;

-- name: ListSessionDocuments :many
-- Порядок гарантируется: сначала по client_index (если есть), затем по
-- created_at (для legacy-сессий и tie-breaker). Lovable сопоставляет
-- response.documents с request.documents по индексу.
SELECT * FROM signing_session_documents
WHERE session_id = $1
ORDER BY client_index NULLS LAST, created_at;

-- name: ListReadySessionDocuments :many
SELECT * FROM signing_session_documents
WHERE session_id = $1 AND status = 'ready'
ORDER BY client_index NULLS LAST, created_at;

-- name: UpdateSessionDocumentStatus :one
UPDATE signing_session_documents
SET status = $2, last_error = $3
WHERE id = $1
RETURNING *;

-- name: UpdateSessionDocumentAfterFetch :one
-- legacy: пишет content_hash из посчитанного фетчером значения.
UPDATE signing_session_documents
SET content_hash = $2, cached_s3_key = $3, status = 'ready'
WHERE id = $1
RETURNING *;

-- name: UpdateSessionDocumentAfterFetchKeepHash :one
-- client-mode: hash уже записан, фетчер только кэширует PDF.
UPDATE signing_session_documents
SET cached_s3_key = $2, status = 'ready'
WHERE id = $1
RETURNING *;

-- name: MarkSessionDocumentTampered :one
-- client-mode + sanity-check провалился: реально посчитанный SHA не совпал
-- с клиентским hash. Документ окончательно отбракован.
UPDATE signing_session_documents
SET status = 'fetch_failed',
    verification_status = 'mismatch',
    verification_error = $2,
    verification_checked_at = now()
WHERE id = $1
RETURNING *;

-- name: MarkSessionDocumentSigned :one
-- Быстрый синхронный путь: NCANode уже подтвердил CMS, CMS-архив уже залит.
-- Документ юридически подписан, но артефакт (QR-штамп + Лист подписей) ещё
-- не собран — это ставится в очередь постпроцессинга (postprocess_status).
UPDATE signing_session_documents
SET cms_s3_key = $2, status = 'signed', signed_at = now(),
    postprocess_status = 'queued', postprocess_attempts = 0, postprocess_next_at = now()
WHERE id = $1
RETURNING *;

-- name: ClaimSessionDocumentForPostprocess :one
-- CAS: воркер забирает джобу, только если она ещё не в работе/не завершена.
-- 0 строк = уже обработано другим воркером или дублирующей доставкой сообщения.
-- status='uploading' держится все ретраи подряд (и на этапе pdfcpu-сборки, и
-- на этапе клиентской выгрузки) — это единое "активно обрабатывается" для
-- /sign/status; success → 'uploaded', terminal fail → 'post_process_failed'.
UPDATE signing_session_documents
SET postprocess_status = 'processing', postprocess_started_at = now(), status = 'uploading'
WHERE id = $1
  AND postprocess_status IN ('queued', 'retrying')
RETURNING *;

-- name: SetSessionDocumentSignedS3Key :exec
-- Пишется сразу после успешной загрузки собранного PDF во внутренний MinIO,
-- независимо от статуса всей джобы — позволяет ретраю пропустить повторную
-- pdfcpu-сборку, если процесс упал уже после этого шага.
UPDATE signing_session_documents
SET signed_s3_key = $2
WHERE id = $1;

-- name: MarkSessionDocumentPostprocessDone :one
UPDATE signing_session_documents
SET status = 'uploaded', uploaded_at = now(), postprocess_status = 'done'
WHERE id = $1
RETURNING *;

-- name: MarkSessionDocumentPostprocessRetrying :exec
UPDATE signing_session_documents
SET postprocess_status = 'retrying',
    postprocess_attempts = postprocess_attempts + 1,
    postprocess_error = $2,
    postprocess_error_code = $3,
    postprocess_next_at = $4,
    last_error = $2
WHERE id = $1;

-- name: MarkSessionDocumentPostprocessFailed :one
-- Терминальный провал: попытки исчерпаны. status='post_process_failed' —
-- отдельное значение от upload_failed/fetch_failed, чтобы отличать провал на
-- этапе фонового постпроцессинга от старых причин отказа.
UPDATE signing_session_documents
SET postprocess_status = 'failed',
    postprocess_attempts = postprocess_attempts + 1,
    postprocess_error = $2,
    postprocess_error_code = $3,
    postprocess_next_at = NULL,
    status = 'post_process_failed',
    last_error = $2
WHERE id = $1
RETURNING *;

-- name: ListDocumentsDueForPostprocess :many
-- Поллер (тот же паттерн, что ListDocumentsForVerification): подхватывает
-- джобы, у которых наступило время следующей попытки — страхует publish-сбой
-- и отсутствие бэкоффа у Nack(requeue=true) в RabbitMQ-консьюмере.
SELECT * FROM signing_session_documents
WHERE postprocess_status IN ('queued', 'retrying')
  AND postprocess_next_at <= now()
ORDER BY postprocess_next_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: UpdateSessionDocumentSignerInfo :one
-- Персистит ncanode.VerifyResult + реальный verify QR-URL. Non-fatal при
-- ошибке (вызывающий код логирует и продолжает — PDF уже собран корректно).
UPDATE signing_session_documents
SET signer_iin = $2, signer_name = $3, signer_bin = $4, org_name = $5,
    signer_type = $6, basis = $7, cert_serial = $8, cert_not_before = $9,
    cert_not_after = $10, ca_name = $11, ocsp_status = $12, tsp_time = $13,
    sign_format = $14, qr_url = $15
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

-- name: MarkSessionDocumentVerificationPending :exec
-- Вызывается из /sign/complete после успешной sync-сверки messageDigest.
-- Первая проверка асинхронным воркером откладывается на InitialDelay секунд.
UPDATE signing_session_documents
SET verification_status = 'pending',
    verification_next_at = $2
WHERE id = $1;

-- name: ListDocumentsForVerification :many
-- Воркер берёт пачку документов, готовых к проверке. SKIP LOCKED — для безопасной
-- работы нескольких инстансов воркера одновременно.
SELECT * FROM signing_session_documents
WHERE verification_status IN ('pending', 'retrying')
  AND verification_next_at <= now()
ORDER BY verification_next_at
LIMIT $1
FOR UPDATE SKIP LOCKED;

-- name: UpdateDocumentVerification :exec
-- Финальная или промежуточная запись результата проверки. attempts инкрементируется
-- атомарно. error/source_meta_hash могут быть NULL.
UPDATE signing_session_documents
SET verification_status = $2,
    verification_checked_at = now(),
    verification_attempts = verification_attempts + 1,
    verification_error = $3,
    verification_next_at = $4,
    source_meta_hash = COALESCE($5, source_meta_hash)
WHERE id = $1;

-- name: RecalcSessionVerification :exec
-- Worst-of агрегат: любой не-'verified' документ перетягивает статус сессии.
-- Приоритет: mismatch > unavailable > pending > verified. Сессии без verification-
-- атрибутов у документов не трогаем.
UPDATE signing_sessions s
SET verification_status = (
    SELECT CASE
        WHEN bool_or(ssd.verification_status = 'mismatch')       THEN 'mismatch'
        WHEN bool_or(ssd.verification_status = 'unavailable')    THEN 'unavailable'
        WHEN bool_or(ssd.verification_status IN ('pending','retrying')) THEN 'pending'
        WHEN bool_and(ssd.verification_status = 'verified')      THEN 'verified'
        ELSE NULL
    END
    FROM signing_session_documents ssd
    WHERE ssd.session_id = s.id
)
WHERE s.id = $1;

-- name: ListSigningSessionDocumentsForRegistry :many
-- Реестр подписаний (admin-only): по документам, не по сессиям — оператор
-- должен видеть каждый документ отдельно. tenant_id/status — nullable
-- фильтры, NULL = без фильтра.
SELECT ssd.*, ss.tenant_id AS tenant_id, ss.application_id AS application_id,
       ss.status AS session_status, ss.expires_at AS session_expires_at
FROM signing_session_documents ssd
JOIN signing_sessions ss ON ss.id = ssd.session_id
WHERE (sqlc.narg('tenant_id')::uuid IS NULL OR ss.tenant_id = sqlc.narg('tenant_id')::uuid)
  AND (sqlc.narg('doc_status')::text IS NULL OR ssd.status = sqlc.narg('doc_status')::text)
ORDER BY ssd.created_at DESC
LIMIT sqlc.arg('limit_count') OFFSET sqlc.arg('offset_count');

-- name: CountSigningSessionDocumentsForRegistry :one
SELECT count(*) FROM signing_session_documents ssd
JOIN signing_sessions ss ON ss.id = ssd.session_id
WHERE (sqlc.narg('tenant_id')::uuid IS NULL OR ss.tenant_id = sqlc.narg('tenant_id')::uuid)
  AND (sqlc.narg('doc_status')::text IS NULL OR ssd.status = sqlc.narg('doc_status')::text);
