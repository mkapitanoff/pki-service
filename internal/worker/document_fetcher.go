package worker

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// DocumentFetcherConfig holds tunable parameters for DocumentFetcher.
type DocumentFetcherConfig struct {
	FetchInterval   time.Duration
	FetchBatchSize  int
	MaxFetchRetries int
}

// DocumentFetcher polls application_documents for pending records,
// downloads them from the client's S3, and stores them in our MinIO.
type DocumentFetcher struct {
	cfg      DocumentFetcherConfig
	db       *sql.DB
	queries  *repository.Queries
	store    storage.Storage
	extS3    s3client.ExternalS3Client
	running  chan struct{}
}

// NewDocumentFetcher creates a DocumentFetcher. running is closed when the worker exits.
func NewDocumentFetcher(
	cfg DocumentFetcherConfig,
	db *sql.DB,
	queries *repository.Queries,
	store storage.Storage,
	extS3 s3client.ExternalS3Client,
) *DocumentFetcher {
	if cfg.FetchInterval <= 0 {
		cfg.FetchInterval = 10 * time.Second
	}
	if cfg.FetchBatchSize <= 0 {
		cfg.FetchBatchSize = 20
	}
	if cfg.MaxFetchRetries <= 0 {
		cfg.MaxFetchRetries = 3
	}
	return &DocumentFetcher{
		cfg:     cfg,
		db:      db,
		queries: queries,
		store:   store,
		extS3:   extS3,
		running: make(chan struct{}),
	}
}

// Running returns a channel that is closed when the worker has stopped.
func (f *DocumentFetcher) Running() <-chan struct{} { return f.running }

// Run starts the fetch loop. It blocks until ctx is cancelled.
func (f *DocumentFetcher) Run(ctx context.Context) {
	defer close(f.running)
	log.Println("document_fetcher: started")
	ticker := time.NewTicker(f.cfg.FetchInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("document_fetcher: stopped")
			return
		case <-ticker.C:
			f.tick(ctx)
		}
	}
}

func (f *DocumentFetcher) tick(ctx context.Context) {
	docs, err := f.queries.GetPendingFetchDocuments(ctx, int32(f.cfg.FetchBatchSize))
	if err != nil {
		log.Printf("document_fetcher: query pending: %v", err)
		return
	}
	for _, doc := range docs {
		f.fetchOne(ctx, doc)
	}
}

func (f *DocumentFetcher) fetchOne(ctx context.Context, doc repository.ApplicationDocument) {
	log.Printf("document_fetcher: fetching doc=%s name=%s", doc.ID, doc.DocumentName)

	// Mark as fetching.
	if _, err := f.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{
		ID:        doc.ID,
		Status:    "fetching",
		LastError: sql.NullString{},
	}); err != nil {
		log.Printf("document_fetcher: mark fetching doc=%s: %v", doc.ID, err)
		return
	}

	// Download from client S3.
	data, contentType, err := f.extS3.DownloadFromPresignedURL(ctx, doc.SourceUrl)
	if err != nil {
		f.markFetchError(ctx, doc, err)
		return
	}

	// Get application to find tenant_id.
	app, err := f.queryApplicationByID(ctx, doc.ApplicationID)
	if err != nil {
		f.markFetchError(ctx, doc, fmt.Errorf("load application: %w", err))
		return
	}

	// Store in our MinIO.
	s3Key := fmt.Sprintf("%s/applications/%s/%s_v%d", app.TenantID, doc.ApplicationID, doc.DocumentName, doc.Version)
	if err := f.store.UploadFile(ctx, s3Key, data, contentType); err != nil {
		f.markFetchError(ctx, doc, fmt.Errorf("upload to MinIO: %w", err))
		return
	}

	// Create document record in our DB.
	docID, err := f.createDocument(ctx, app.TenantID, doc, s3Key)
	if err != nil {
		f.markFetchError(ctx, doc, fmt.Errorf("create document: %w", err))
		return
	}

	// Link document_id and mark ready.
	if _, err := f.queries.UpdateApplicationDocumentAfterFetch(ctx, repository.UpdateApplicationDocumentAfterFetchParams{
		ID:         doc.ID,
		DocumentID: uuid.NullUUID{UUID: docID, Valid: true},
	}); err != nil {
		log.Printf("document_fetcher: mark ready doc=%s: %v", doc.ID, err)
	}
	log.Printf("document_fetcher: doc=%s ready (document_id=%s)", doc.ID, docID)
}

func (f *DocumentFetcher) markFetchError(ctx context.Context, doc repository.ApplicationDocument, err error) {
	log.Printf("document_fetcher: error doc=%s: %v", doc.ID, err)

	// Increment attempts.
	updated, incErr := f.queries.IncrementUploadAttempts(ctx, repository.IncrementUploadAttemptsParams{
		ID:        doc.ID,
		LastError: sql.NullString{String: err.Error(), Valid: true},
	})
	if incErr != nil {
		log.Printf("document_fetcher: increment attempts doc=%s: %v", doc.ID, incErr)
		return
	}

	nextStatus := "pending"
	if int(updated.UploadAttempts) >= f.cfg.MaxFetchRetries {
		nextStatus = "fetch_failed"
	}
	if _, statusErr := f.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{
		ID:        doc.ID,
		Status:    nextStatus,
		LastError: sql.NullString{String: err.Error(), Valid: true},
	}); statusErr != nil {
		log.Printf("document_fetcher: set status doc=%s: %v", doc.ID, statusErr)
	}
}

func (f *DocumentFetcher) queryApplicationByID(ctx context.Context, appID uuid.UUID) (repository.Application, error) {
	// Use raw DB query since we don't have tenant context here.
	row := f.db.QueryRowContext(ctx, `SELECT id, tenant_id, external_id, status, signing_round, signer_role,
		callback_url, callback_secret, cancelled_at, cancel_reason, created_at, updated_at
		FROM applications WHERE id = $1`, appID)
	var a repository.Application
	err := row.Scan(
		&a.ID, &a.TenantID, &a.ExternalID, &a.Status, &a.SigningRound, &a.SignerRole,
		&a.CallbackUrl, &a.CallbackSecret, &a.CancelledAt, &a.CancelReason, &a.CreatedAt, &a.UpdatedAt,
	)
	return a, err
}

func (f *DocumentFetcher) createDocument(ctx context.Context, tenantID uuid.UUID, doc repository.ApplicationDocument, s3Key string) (uuid.UUID, error) {
	row := f.db.QueryRowContext(ctx,
		`INSERT INTO documents (tenant_id, title, s3_key_original, s3_key_current, current_version, status, sha256_hash, sha256_hash_current)
		 VALUES ($1, $2, $3, $4, 0, 'pending', '', '')
		 RETURNING id`,
		tenantID,
		sql.NullString{String: doc.DocumentName, Valid: true},
		s3Key, s3Key,
	)
	var id uuid.UUID
	return id, row.Scan(&id)
}
