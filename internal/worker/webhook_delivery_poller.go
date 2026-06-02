package worker

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/webhook"
)

// WebhookDeliveryPollerConfig holds tunable parameters.
type WebhookDeliveryPollerConfig struct {
	Interval  time.Duration // how often to poll (default: 5s)
	BatchSize int32         // rows per tick (default: 50)
}

// WebhookDeliveryPoller polls webhook_deliveries and delivers pending rows.
type WebhookDeliveryPoller struct {
	cfg     WebhookDeliveryPollerConfig
	queries *repository.Queries
	worker  *webhook.DeliveryWorker
	running chan struct{}
}

// NewWebhookDeliveryPoller creates a poller using the provided queries and HTTP client.
func NewWebhookDeliveryPoller(cfg WebhookDeliveryPollerConfig, queries *repository.Queries, httpClient *http.Client) *WebhookDeliveryPoller {
	if cfg.Interval <= 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 50
	}
	return &WebhookDeliveryPoller{
		cfg:     cfg,
		queries: queries,
		worker:  webhook.NewDeliveryWorker(queries, httpClient),
		running: make(chan struct{}),
	}
}

// Running returns a channel closed when the worker exits.
func (p *WebhookDeliveryPoller) Running() <-chan struct{} { return p.running }

// Run starts the polling loop. Blocks until ctx is cancelled.
func (p *WebhookDeliveryPoller) Run(ctx context.Context) {
	defer close(p.running)
	log.Println("webhook_delivery_poller: started")
	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("webhook_delivery_poller: stopped")
			return
		case <-ticker.C:
			p.tick(ctx)
		}
	}
}

func (p *WebhookDeliveryPoller) tick(ctx context.Context) {
	rows, err := p.queries.ListPendingWebhookDeliveries(ctx, p.cfg.BatchSize)
	if err != nil {
		log.Printf("webhook_delivery_poller: list pending: %v", err)
		return
	}
	for _, row := range rows {
		if err := p.worker.ProcessWebhookDelivery(ctx, row.ID); err != nil {
			log.Printf("webhook_delivery_poller: deliver id=%s: %v", row.ID, err)
		}
	}
}
