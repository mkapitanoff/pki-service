package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"go.uber.org/zap"

	"github.com/mkapitanoff/pki-service/internal/config"
	"github.com/mkapitanoff/pki-service/internal/queue"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/webhook"
)

func main() {
	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "test"
	}

	cfg, err := config.Load(env)
	if err != nil {
		panic("worker: load config: " + err.Error())
	}

	logger, err := buildLogger(cfg.Log.Level)
	if err != nil {
		panic("worker: build logger: " + err.Error())
	}
	defer logger.Sync() //nolint:errcheck

	// Connect to PostgreSQL.
	db, err := sql.Open("postgres", cfg.Database.DSN)
	if err != nil {
		logger.Fatal("worker: open db", zap.Error(err))
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		logger.Fatal("worker: ping db", zap.Error(err))
	}

	queries := repository.New(db)
	deliveryWorker := webhook.NewDeliveryWorker(queries, &http.Client{Timeout: 15 * time.Second})

	// Connect to RabbitMQ.
	consumer, err := queue.NewRabbitMQConsumer(
		cfg.RabbitMQ.URL,
		cfg.RabbitMQ.WebhookQueue,
		"", // webhook.delivery is a direct work queue, no exchange binding needed
	)
	if err != nil {
		logger.Fatal("worker: connect rabbitmq", zap.Error(err))
	}
	defer consumer.Close()

	logger.Info("worker: started",
		zap.String("env", env),
		zap.String("queue", cfg.RabbitMQ.WebhookQueue),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Graceful shutdown on SIGINT/SIGTERM.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-quit
		logger.Info("worker: shutting down", zap.String("signal", sig.String()))
		cancel()
	}()

	handler := func(event string, body []byte) error {
		var msg struct {
			DeliveryID uuid.UUID `json:"delivery_id"`
		}
		if err := json.Unmarshal(body, &msg); err != nil {
			logger.Error("worker: invalid message", zap.Error(err), zap.ByteString("body", body))
			return err
		}
		logger.Info("worker: processing delivery", zap.String("delivery_id", msg.DeliveryID.String()))
		if err := deliveryWorker.ProcessWebhookDelivery(ctx, msg.DeliveryID); err != nil {
			logger.Error("worker: delivery failed",
				zap.String("delivery_id", msg.DeliveryID.String()),
				zap.Error(err),
			)
			return err
		}
		logger.Info("worker: delivery done", zap.String("delivery_id", msg.DeliveryID.String()))
		return nil
	}

	if err := consumer.Consume(ctx, handler); err != nil && ctx.Err() == nil {
		logger.Error("worker: consumer error", zap.Error(err))
	}

	logger.Info("worker: stopped")
}

func buildLogger(level string) (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	if level == "debug" {
		cfg = zap.NewDevelopmentConfig()
	}
	return cfg.Build()
}
