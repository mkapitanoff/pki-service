package service

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/mkapitanoff/pki-service/internal/repository"
)

// FanOutWebhook creates a webhook_delivery row for every active webhook
// of tenantID that subscribes to eventName.
// Best-effort: errors are logged, not returned.
func FanOutWebhook(
	ctx context.Context,
	queries *repository.Queries,
	tenantID uuid.UUID,
	eventName string,
	payload any,
) {
	hooks, err := queries.GetWebhooksByTenantAndEvent(ctx, repository.GetWebhooksByTenantAndEventParams{
		TenantID: tenantID,
		Column2:  eventName,
	})
	if err != nil {
		log.Printf("webhook_fanout: list webhooks tenant=%s event=%s: %v", tenantID, eventName, err)
		return
	}
	if len(hooks) == 0 {
		return
	}

	body, err := json.Marshal(payload)
	if err != nil {
		log.Printf("webhook_fanout: marshal payload: %v", err)
		return
	}

	for _, wh := range hooks {
		_, err := queries.CreateWebhookDelivery(ctx, repository.CreateWebhookDeliveryParams{
			WebhookID:   wh.ID,
			Event:       eventName,
			Payload:     body,
			Attempt:     1,
			Status:      "pending",
			ScheduledAt: time.Now(),
		})
		if err != nil {
			log.Printf("webhook_fanout: create delivery webhook=%s: %v", wh.ID, err)
		}
	}
}
