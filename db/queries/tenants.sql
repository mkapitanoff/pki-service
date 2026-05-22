-- name: CreateTenant :one
INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants
WHERE id = $1;

-- name: ListTenantsWithKeyCount :many
SELECT t.id, t.name, t.type, t.is_active, t.created_at,
       COUNT(k.id) AS api_keys_count
FROM tenants t
LEFT JOIN api_keys k ON k.tenant_id = t.id
GROUP BY t.id, t.name, t.type, t.is_active, t.created_at
ORDER BY t.created_at DESC;
