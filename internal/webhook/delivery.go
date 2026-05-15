package webhook

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

const maxRetries = 3

// backoff returns the retry delay for a given attempt number (1-based).
// attempt 1 → 60s, attempt 2 → 300s, attempt 3 → 1800s.
func backoff(attempt int32) time.Duration {
	switch attempt {
	case 1:
		return 60 * time.Second
	case 2:
		return 300 * time.Second
	default:
		return 1800 * time.Second
	}
}

// DeliveryWorker processes webhook_delivery rows: signs the payload, POSTs
// to the target URL, and updates the delivery record in DB.
type DeliveryWorker struct {
	queries    *repository.Queries
	httpClient *http.Client
}

// NewDeliveryWorker creates a DeliveryWorker with the given repository and HTTP client.
func NewDeliveryWorker(queries *repository.Queries, httpClient *http.Client) *DeliveryWorker {
	return &DeliveryWorker{queries: queries, httpClient: httpClient}
}

// ProcessWebhookDelivery executes one delivery attempt for deliveryID.
func (w *DeliveryWorker) ProcessWebhookDelivery(ctx context.Context, deliveryID uuid.UUID) error {
	// 1. Load delivery + webhook.
	delivery, err := w.queries.GetWebhookDeliveryByID(ctx, deliveryID)
	if err != nil {
		return fmt.Errorf("webhook delivery: load delivery %s: %w", deliveryID, err)
	}
	webhook, err := w.queries.GetWebhookByID(ctx, delivery.WebhookID)
	if err != nil {
		return fmt.Errorf("webhook delivery: load webhook %s: %w", delivery.WebhookID, err)
	}

	// 2. Marshal payload.
	body, err := json.Marshal(delivery.Payload)
	if err != nil {
		return fmt.Errorf("webhook delivery: marshal payload: %w", err)
	}

	// 3. HMAC-SHA256 signature.
	mac := hmac.New(sha256.New, []byte(webhook.Secret))
	mac.Write(body)
	sig := "sha256=" + hex.EncodeToString(mac.Sum(nil))

	// 4. POST with 10s timeout.
	reqCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, webhook.Url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook delivery: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-EDS-Signature", sig)

	resp, err := w.httpClient.Do(req)
	if err != nil {
		// Network error — treat as failure.
		return w.handleFailure(ctx, delivery, 0, err.Error())
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); resp.Body.Close() }()

	// 5. Success (2xx).
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return w.queries.UpdateWebhookDeliverySuccess(ctx, deliveryID)
	}

	// 6. Non-2xx failure.
	return w.handleFailure(ctx, delivery, resp.StatusCode, fmt.Sprintf("HTTP %d", resp.StatusCode))
}

// handleFailure updates the delivery to failed and schedules a retry when
// attempt < maxRetries by inserting a new delivery row with future scheduled_at.
func (w *DeliveryWorker) handleFailure(
	ctx context.Context,
	delivery repository.WebhookDelivery,
	statusCode int,
	errMsg string,
) error {
	var code sql.NullInt32
	if statusCode != 0 {
		code = sql.NullInt32{Int32: int32(statusCode), Valid: true}
	}
	if err := w.queries.UpdateWebhookDeliveryFailed(ctx, repository.UpdateWebhookDeliveryFailedParams{
		ID:           delivery.ID,
		Status:       "failed",
		ErrorMsg:     sql.NullString{String: errMsg, Valid: true},
		ResponseCode: code,
	}); err != nil {
		return fmt.Errorf("webhook delivery: mark failed: %w", err)
	}

	// 7. Schedule retry if retries remain.
	if delivery.Attempt < maxRetries {
		nextAttempt := delivery.Attempt + 1
		scheduledAt := time.Now().Add(backoff(delivery.Attempt))
		_, err := w.queries.CreateWebhookDelivery(ctx, repository.CreateWebhookDeliveryParams{
			WebhookID:   delivery.WebhookID,
			Event:       delivery.Event,
			Payload:     delivery.Payload,
			Attempt:     nextAttempt,
			Status:      "pending",
			ScheduledAt: scheduledAt,
		})
		if err != nil {
			return fmt.Errorf("webhook delivery: schedule retry: %w", err)
		}
	}
	return nil
}
