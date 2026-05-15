-- name: GetAPIKeyByHash :one
SELECT ak.*, t.name as tenant_name, t.type as tenant_type, t.is_active as tenant_is_active
FROM api_keys ak
JOIN tenants t ON t.id = ak.tenant_id
WHERE ak.key_hash = $1 AND ak.is_active = true;

-- name: UpdateAPIKeyLastUsed :exec
UPDATE api_keys SET last_used_at = now()
WHERE id = $1;
