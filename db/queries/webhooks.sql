-- name: CreateWebhook :one
INSERT INTO webhooks (tenant_id, url, events, secret)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListWebhooksByTenant :many
SELECT * FROM webhooks
WHERE tenant_id = $1
ORDER BY created_at DESC;

-- name: DeactivateWebhook :exec
UPDATE webhooks SET is_active = false
WHERE id = $1 AND tenant_id = $2;

-- name: ListPendingWebhookDeliveries :many
SELECT * FROM webhook_deliveries
WHERE status = 'pending'
  AND scheduled_at <= now()
ORDER BY scheduled_at
LIMIT $1;

-- name: GetWebhooksByTenantAndEvent :many
SELECT * FROM webhooks
WHERE tenant_id = $1
  AND $2::text = ANY(events)
  AND is_active = true;

-- name: GetWebhookByID :one
SELECT * FROM webhooks
WHERE id = $1;

-- name: GetWebhookDeliveryByID :one
SELECT * FROM webhook_deliveries
WHERE id = $1;

-- name: CreateWebhookDelivery :one
INSERT INTO webhook_deliveries (webhook_id, event, payload, attempt, status, scheduled_at)
VALUES ($1, $2, $3, $4, $5, $6)
RETURNING *;

-- name: UpdateWebhookDeliverySuccess :exec
UPDATE webhook_deliveries
SET status = 'success', delivered_at = now()
WHERE id = $1;

-- name: UpdateWebhookDeliveryFailed :exec
UPDATE webhook_deliveries
SET status = $2, error_msg = $3, response_code = $4
WHERE id = $1;
