-- name: CreateAuditLog :exec
INSERT INTO audit_log (tenant_id, action, entity_type, entity_id, actor_id, meta)
VALUES ($1, $2, $3, $4, $5, $6);
