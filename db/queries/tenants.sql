-- name: GetTenant :one
SELECT * FROM tenants
WHERE id = $1;
