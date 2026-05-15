-- name: CreateDocument :one
INSERT INTO documents (
    tenant_id, title, s3_key_original, s3_key_current, current_version, status, metadata
) VALUES (
    $1, $2, $3, $4, $5, $6, $7
)
RETURNING *;

-- name: GetDocument :one
SELECT * FROM documents
WHERE id = $1 AND tenant_id = $2;

-- name: UpdateDocumentStatus :one
UPDATE documents
SET status = $3, updated_at = now()
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: UpdateDocumentVersion :one
UPDATE documents
SET s3_key_current = $3, current_version = $4, status = $5, updated_at = now()
WHERE id = $1 AND tenant_id = $2
RETURNING *;

-- name: CreateDocumentVersion :one
INSERT INTO document_versions (document_id, tenant_id, version, s3_key)
VALUES ($1, $2, $3, $4) RETURNING *;
