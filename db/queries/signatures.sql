-- name: CreateSignature :one
INSERT INTO signatures (
    document_id, tenant_id, version_number, sequence_num,
    cms_b64, role, signer_iin, signer_name, signer_bin, org_name,
    signer_type, basis,
    cert_serial, cert_not_before, cert_not_after, ca_name,
    ocsp_status, ocsp_checked_at, tsp_time, sha256_hash, sign_format,
    qr_url
) VALUES (
    $1, $2, $3, $4,
    $5, $6, $7, $8, $9, $10,
    $11, $12,
    $13, $14, $15, $16,
    $17, $18, $19, $20, $21,
    $22
)
RETURNING *;

-- name: CreateSignatureWithID :one
INSERT INTO signatures (
    id, document_id, tenant_id, version_number, sequence_num,
    cms_b64, role, signer_iin, signer_name, signer_bin,
    org_name, signer_type, basis, cert_serial,
    cert_not_before, cert_not_after, ca_name,
    ocsp_status, ocsp_checked_at, tsp_time,
    sha256_hash, sign_format, qr_url
) VALUES (
    $1, $2, $3, $4, $5, $6, $7, $8, $9, $10,
    $11, $12, $13, $14, $15, $16, $17,
    $18, $19, $20, $21, $22, $23
) RETURNING *;

-- name: GetSignature :one
SELECT * FROM signatures
WHERE id = $1 AND tenant_id = $2;

-- name: GetSignaturesByDocument :many
SELECT * FROM signatures
WHERE document_id = $1 AND tenant_id = $2
ORDER BY sequence_num ASC;

-- name: GetSignatureByIDPublic :one
SELECT * FROM signatures
WHERE id = $1;
