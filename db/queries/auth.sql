-- name: CreateUser :one
INSERT INTO users (email, password_hash, name, tenant_id, role)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetUserByEmail :one
SELECT * FROM users WHERE email = $1;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = $1;

-- name: UpdateUserLastLogin :exec
UPDATE users SET last_login_at = now() WHERE id = $1;

-- name: CreateAuthToken :one
INSERT INTO auth_tokens (user_id, token_hash, expires_at)
VALUES ($1, $2, $3)
RETURNING *;

-- name: GetAuthTokenByHash :one
SELECT * FROM auth_tokens WHERE token_hash = $1;

-- name: DeleteAuthToken :exec
DELETE FROM auth_tokens WHERE token_hash = $1;

-- name: DeleteExpiredTokens :exec
DELETE FROM auth_tokens WHERE expires_at < now();
