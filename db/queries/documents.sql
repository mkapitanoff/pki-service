-- name: CreateDocument :one
INSERT INTO documents (tenant_id, title, s3_key_original, s3_key_current, metadata)
VALUES ($1, $2, $3, $4, $5)
RETURNING *;

-- name: GetDocument :one
SELECT * FROM documents
WHERE id = $1 AND tenant_id = $2;

-- name: UpdateDocumentStatus :one
UPDATE documents SET status = $1, updated_at = now()
WHERE id = $2 AND tenant_id = $3
RETURNING *;

-- name: UpdateDocumentVersion :one
UPDATE documents SET
    s3_key_current = $1,
    current_version = $2,
    status = $3,
    updated_at = now()
WHERE id = $4 AND tenant_id = $5
RETURNING *;
