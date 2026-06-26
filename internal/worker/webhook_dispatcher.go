package worker

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
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/mkapitanoff/pki-service/internal/repository"
)

// WebhookDispatcherConfig holds tunable parameters.
type WebhookDispatcherConfig struct {
	Interval    time.Duration
	MaxAttempts int
}

// WebhookDispatcher sends pending application_webhooks to callback URLs.
type WebhookDispatcher struct {
	cfg     WebhookDispatcherConfig
	db      *sql.DB
	queries *repository.Queries
	http    *http.Client
	running chan struct{}
}

// NewWebhookDispatcher creates a WebhookDispatcher.
func NewWebhookDispatcher(
	cfg WebhookDispatcherConfig,
	db *sql.DB,
	queries *repository.Queries,
) *WebhookDispatcher {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	return &WebhookDispatcher{
		cfg:     cfg,
		db:      db,
		queries: queries,
		http:    &http.Client{Timeout: 10 * time.Second},
		running: make(chan struct{}),
	}
}

// Running returns a channel closed when the worker exits.
func (d *WebhookDispatcher) Running() <-chan struct{} { return d.running }

// Run starts the dispatch loop. Blocks until ctx is cancelled.
func (d *WebhookDispatcher) Run(ctx context.Context) {
	defer close(d.running)
	log.Println("webhook_dispatcher: started")
	ticker := time.NewTicker(d.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("webhook_dispatcher: stopped")
			return
		case <-ticker.C:
			d.tick(ctx)
		}
	}
}

func (d *WebhookDispatcher) tick(ctx context.Context) {
	hooks, err := d.queries.GetPendingWebhooks(ctx)
	if err != nil {
		log.Printf("webhook_dispatcher: query pending: %v", err)
		return
	}
	for _, hook := range hooks {
		d.dispatch(ctx, hook)
	}
}

func (d *WebhookDispatcher) dispatch(ctx context.Context, hook repository.ApplicationWebhook) {
	app, err := d.loadApplication(ctx, hook.ApplicationID)
	if err != nil {
		log.Printf("webhook_dispatcher: load app for hook=%s: %v", hook.ID, err)
		return
	}

	if !app.CallbackUrl.Valid || app.CallbackUrl.String == "" {
		// No callback URL — mark delivered silently.
		now := time.Now().UTC()
		d.queries.UpdateWebhookStatus(ctx, repository.UpdateWebhookStatusParams{ //nolint:errcheck
			ID:          hook.ID,
			Status:      "delivered",
			Attempts:    hook.Attempts + 1,
			LastAttemptAt: sql.NullTime{Time: now, Valid: true},
			DeliveredAt: sql.NullTime{Time: now, Valid: true},
		})
		return
	}

	callbackURL := app.CallbackUrl.String
	payload := hook.Payload
	now := time.Now().UTC()
	unixTS := now.Unix()

	// Build request.
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(payload))
	if err != nil {
		d.markFailed(ctx, hook, now, fmt.Errorf("build request: %w", err))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-PKI-Event", hook.EventType)
	req.Header.Set("X-PKI-Timestamp", fmt.Sprintf("%d", unixTS))

	// HMAC signature.
	if app.CallbackSecret.Valid && app.CallbackSecret.String != "" {
		mac := hmac.New(sha256.New, []byte(app.CallbackSecret.String))
		mac.Write(payload)
		sig := hex.EncodeToString(mac.Sum(nil))
		req.Header.Set("X-PKI-Signature", "sha256="+sig)
	}

	resp, err := d.http.Do(req)
	if err != nil {
		d.markFailed(ctx, hook, now, err)
		return
	}
	defer resp.Body.Close()
	// Прочтём начало тела для диагностики (Lovable Edge возвращает HTML/JSON
	// со своими ошибками — нужен снимок для расследования).
	const snippetLimit = 512
	bodySnippet, _ := io.ReadAll(io.LimitReader(resp.Body, snippetLimit))
	io.Copy(io.Discard, resp.Body)

	log.Printf("webhook_dispatcher.deliver hook=%s app=%s event=%s url=%s status=%d body=%q",
		hook.ID, hook.ApplicationID, hook.EventType, callbackURL, resp.StatusCode, string(bodySnippet))

	newAttempts := hook.Attempts + 1
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		d.queries.UpdateWebhookStatus(ctx, repository.UpdateWebhookStatusParams{ //nolint:errcheck
			ID:            hook.ID,
			Status:        "delivered",
			Attempts:      newAttempts,
			LastAttemptAt: sql.NullTime{Time: now, Valid: true},
			DeliveredAt:   sql.NullTime{Time: now, Valid: true},
		})
		return
	}
	d.markFailed(ctx, hook, now, fmt.Errorf("HTTP %d", resp.StatusCode))
}

func (d *WebhookDispatcher) markFailed(ctx context.Context, hook repository.ApplicationWebhook, now time.Time, cause error) {
	log.Printf("webhook_dispatcher: hook=%s failed: %v", hook.ID, cause)
	newAttempts := hook.Attempts + 1
	nextStatus := "failed"
	if int(newAttempts) < d.cfg.MaxAttempts {
		nextStatus = "failed" // will be retried because attempts < max
	}

	// Exponential backoff: 30s → 5m → 30m → 2h → 24h.
	delays := []time.Duration{
		30 * time.Second,
		5 * time.Minute,
		30 * time.Minute,
		2 * time.Hour,
		24 * time.Hour,
	}
	idx := int(newAttempts) - 1
	if idx >= len(delays) {
		idx = len(delays) - 1
	}
	nextAttempt := now.Add(delays[idx])

	d.queries.UpdateWebhookStatus(ctx, repository.UpdateWebhookStatusParams{ //nolint:errcheck
		ID:            hook.ID,
		Status:        nextStatus,
		Attempts:      newAttempts,
		LastAttemptAt: sql.NullTime{Time: now, Valid: true},
		NextAttemptAt: sql.NullTime{Time: nextAttempt, Valid: true},
	})
}

func (d *WebhookDispatcher) loadApplication(ctx context.Context, appID uuid.UUID) (repository.Application, error) {
	row := d.db.QueryRowContext(ctx,
		`SELECT id, tenant_id, external_id, status, signing_round, signer_role,
		        callback_url, callback_secret, cancelled_at, cancel_reason, created_at, updated_at
		 FROM applications WHERE id = $1`, appID)
	var a repository.Application
	err := row.Scan(
		&a.ID, &a.TenantID, &a.ExternalID, &a.Status, &a.SigningRound, &a.SignerRole,
		&a.CallbackUrl, &a.CallbackSecret, &a.CancelledAt, &a.CancelReason, &a.CreatedAt, &a.UpdatedAt,
	)
	return a, err
}

// CreateAndDispatchWebhook inserts a webhook record to be delivered asynchronously.
// If callbackSecret is empty the HMAC header is omitted.
func CreateAndDispatchWebhook(
	ctx context.Context,
	queries *repository.Queries,
	applicationID uuid.UUID,
	eventType string,
	payloadData any,
	callbackSecret string,
) error {
	raw, err := json.Marshal(payloadData)
	if err != nil {
		return fmt.Errorf("webhook: marshal payload: %w", err)
	}

	var hmacSig sql.NullString
	if callbackSecret != "" {
		mac := hmac.New(sha256.New, []byte(callbackSecret))
		mac.Write(raw)
		hmacSig = sql.NullString{String: "sha256=" + hex.EncodeToString(mac.Sum(nil)), Valid: true}
	}

	if _, err := queries.CreateApplicationWebhook(ctx, repository.CreateApplicationWebhookParams{
		ApplicationID: applicationID,
		EventType:     eventType,
		Payload:       raw,
		HmacSignature: hmacSig,
	}); err != nil {
		return fmt.Errorf("webhook: create record: %w", err)
	}
	log.Printf("webhook: queued event=%s for application=%s", eventType, applicationID)
	return nil
}
