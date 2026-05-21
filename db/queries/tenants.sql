-- name: CreateTenant :one
INSERT INTO tenants (name, type) VALUES ($1, $2) RETURNING *;

-- name: GetTenant :one
SELECT * FROM tenants
WHERE id = $1;
