-- name: GetAPIKeyByHash :one
SELECT * FROM api_keys
WHERE key_hash = $1 AND is_active = true;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys
SET last_used_at = now()
WHERE id = $1;
