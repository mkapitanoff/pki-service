package worker

import (
	"context"
	"log"
	"time"

	"github.com/mkapitanoff/pki-service/internal/postprocess"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// PostprocessConfig — параметры воркера постпроцессинга подписания.
type PostprocessConfig struct {
	TickInterval time.Duration // период тика (default 3s)
	BatchSize    int32         // макс. кол-во документов за тик (default 20)
	MaxAttempts  int           // потолок попыток перед терминальным provал'ом (default 5)
}

// PostprocessWorker завершает подписание документа в фоне: QR-штамп + Лист
// подписей + upload клиенту, после того как /sign/complete уже синхронно
// подтвердил CMS и вернул пользователю "Подписан". Тот же паттерн, что и
// VerificationWorker — тикер + прямой DB-поллинг, без очереди: сообщение в
// очереди не даёт ничего, чего не даёт короткий тик, а тикер проще и не
// требует RabbitMQ-инфраструктуры для внутреннего, не межсервисного, шага.
type PostprocessWorker struct {
	cfg     PostprocessConfig
	queries *repository.Queries
	store   storage.Storage
	extS3   s3client.ExternalS3Client
	running chan struct{}
}

func NewPostprocessWorker(
	cfg PostprocessConfig,
	queries *repository.Queries,
	store storage.Storage,
	extS3 s3client.ExternalS3Client,
) *PostprocessWorker {
	if cfg.TickInterval <= 0 {
		cfg.TickInterval = 3 * time.Second
	}
	if cfg.BatchSize <= 0 {
		cfg.BatchSize = 20
	}
	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 5
	}
	return &PostprocessWorker{
		cfg:     cfg,
		queries: queries,
		store:   store,
		extS3:   extS3,
		running: make(chan struct{}),
	}
}

func (w *PostprocessWorker) Running() <-chan struct{} { return w.running }

func (w *PostprocessWorker) Run(ctx context.Context) {
	defer close(w.running)
	log.Println("postprocess_worker: started")
	t := time.NewTicker(w.cfg.TickInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("postprocess_worker: stopped")
			return
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *PostprocessWorker) tick(ctx context.Context) {
	docs, err := w.queries.ListDocumentsDueForPostprocess(ctx, w.cfg.BatchSize)
	if err != nil {
		log.Printf("postprocess_worker: list: %v", err)
		return
	}
	deps := postprocess.Deps{
		Queries:     w.queries,
		Store:       w.store,
		ExtS3:       w.extS3,
		MaxAttempts: w.cfg.MaxAttempts,
	}
	for _, doc := range docs {
		postprocess.ProcessDocument(ctx, deps, doc)
	}
}
