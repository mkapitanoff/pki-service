-- name: GetIdempotencyKey :one
-- Возвращает закэшированный response для (tenant, key, method, path) если
-- он ещё не истёк. NULL → нужно обработать запрос обычным flow.
SELECT * FROM idempotency_keys
WHERE tenant_id = $1
  AND idem_key  = $2
  AND method    = $3
  AND path      = $4
  AND expires_at > now();

-- name: PutIdempotencyKey :exec
-- Сохраняет результат обработки. ON CONFLICT DO NOTHING — если две
-- параллельные транзакции дошли сюда, первая выигрывает; вторая запросом
-- GetIdempotencyKey увидит её результат при ретрае.
INSERT INTO idempotency_keys (
    tenant_id, idem_key, method, path, status_code, response_body, session_id
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
ON CONFLICT (tenant_id, idem_key, method, path) DO NOTHING;

-- name: CleanupExpiredIdempotencyKeys :exec
DELETE FROM idempotency_keys WHERE expires_at < now();
