-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys
WHERE key_hash = $1 AND is_active = true;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys
SET last_used_at = now()
WHERE id = $1;

-- name: ListAPIKeysByTenant :many
SELECT * FROM api_keys
WHERE tenant_id = $1
ORDER BY created_at DESC;

-- name: CreateAPIKey :one
INSERT INTO api_keys (tenant_id, key_hash, label, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: DeactivateAPIKey :exec
UPDATE api_keys SET is_active = false
WHERE id = $1 AND tenant_id = $2;
