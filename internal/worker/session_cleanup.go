package worker

import (
	"context"
	"log"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// SessionCleanupConfig holds tunable parameters for SessionCleanupWorker.
type SessionCleanupConfig struct {
	// CleanupInterval is how often expired sessions are swept (default: 10m).
	CleanupInterval time.Duration
	// CacheTTL is the maximum age of cached PDFs in MinIO (default: 24h).
	CacheTTL time.Duration
}

// SessionCleanupWorker marks expired signing sessions and removes cached PDFs.
type SessionCleanupWorker struct {
	cfg     SessionCleanupConfig
	queries *repository.Queries
	store   storage.Storage
	running chan struct{}
}

// NewSessionCleanupWorker creates a SessionCleanupWorker.
func NewSessionCleanupWorker(
	cfg SessionCleanupConfig,
	queries *repository.Queries,
	store storage.Storage,
) *SessionCleanupWorker {
	if cfg.CleanupInterval <= 0 {
		cfg.CleanupInterval = 10 * time.Minute
	}
	if cfg.CacheTTL <= 0 {
		cfg.CacheTTL = 24 * time.Hour
	}
	return &SessionCleanupWorker{
		cfg:     cfg,
		queries: queries,
		store:   store,
		running: make(chan struct{}),
	}
}

// Running returns a channel that is closed when the worker has stopped.
func (w *SessionCleanupWorker) Running() <-chan struct{} { return w.running }

// Run starts the cleanup loop. Blocks until ctx is cancelled.
func (w *SessionCleanupWorker) Run(ctx context.Context) {
	defer close(w.running)
	log.Println("session_cleanup: started")

	cleanupTicker := time.NewTicker(w.cfg.CleanupInterval)
	defer cleanupTicker.Stop()

	// Align purge to every 24 hours from startup.
	purgeTicker := time.NewTicker(24 * time.Hour)
	defer purgeTicker.Stop()

	// Run once immediately on start so we don't wait 10m on fresh deploy.
	w.cleanupExpired(ctx)

	for {
		select {
		case <-ctx.Done():
			log.Println("session_cleanup: stopped")
			return
		case <-cleanupTicker.C:
			w.cleanupExpired(ctx)
		case <-purgeTicker.C:
			w.purgeStaleCache(ctx)
		}
	}
}

// cleanupExpired marks expired signing sessions and deletes their cached PDFs,
// плюс протирает экспайренные idempotency-ключи (TTL 24h).
func (w *SessionCleanupWorker) cleanupExpired(ctx context.Context) {
	// Idempotency keys — cheap DELETE, делаем всегда.
	if err := w.queries.CleanupExpiredIdempotencyKeys(ctx); err != nil {
		log.Printf("session_cleanup: cleanup idempotency keys: %v", err)
	}

	sessions, err := w.queries.GetExpiredSessions(ctx)
	if err != nil {
		log.Printf("session_cleanup: query expired sessions: %v", err)
		return
	}
	if len(sessions) == 0 {
		return
	}
	log.Printf("session_cleanup: processing %d expired sessions", len(sessions))
	for _, sess := range sessions {
		w.cleanupSession(ctx, sess)
	}
}

func (w *SessionCleanupWorker) cleanupSession(ctx context.Context, sess repository.SigningSession) {
	// Mark as expired in DB.
	if _, err := w.queries.UpdateSigningSessionStatus(ctx, repository.UpdateSigningSessionStatusParams{
		ID:     sess.ID,
		Status: "expired",
	}); err != nil {
		log.Printf("session_cleanup: mark expired session=%s: %v", sess.ID, err)
		return
	}

	// Load all documents to find cached S3 keys.
	docs, err := w.queries.ListSessionDocuments(ctx, sess.ID)
	if err != nil {
		log.Printf("session_cleanup: list docs session=%s: %v", sess.ID, err)
		return
	}

	deleted := 0
	var freedBytes int64
	for _, doc := range docs {
		if !doc.CachedS3Key.Valid || doc.CachedS3Key.String == "" {
			continue
		}
		key := doc.CachedS3Key.String

		// Best-effort size estimate from listing before delete.
		objs, listErr := w.store.ListObjectKeys(ctx, key)
		if listErr == nil && len(objs) > 0 {
			freedBytes += objs[0].Size
		}

		if err := w.store.DeleteFile(ctx, key); err != nil {
			log.Printf("session_cleanup: delete cached doc=%s key=%s: %v", doc.ID, key, err)
			continue
		}
		deleted++
	}

	log.Printf("session_cleanup: expired session=%s docs=%d cached_pdfs_deleted=%d freed_kb=%d",
		sess.ID, len(docs), deleted, freedBytes/1024)
}

// purgeStaleCache deletes cached PDFs under the "cache/" prefix that are older than CacheTTL.
// This is a safety net for edge cases where per-session cleanup was missed.
func (w *SessionCleanupWorker) purgeStaleCache(ctx context.Context) {
	log.Println("session_cleanup: starting stale cache purge")

	objs, err := w.store.ListObjectKeys(ctx, "cache/")
	if err != nil {
		log.Printf("session_cleanup: list cache/ objects: %v", err)
		return
	}

	cutoff := time.Now().UTC().Add(-w.cfg.CacheTTL)
	var deleted int
	var freedBytes int64

	for _, obj := range objs {
		if obj.LastModified.IsZero() || obj.LastModified.After(cutoff) {
			continue
		}
		if err := w.store.DeleteFile(ctx, obj.Key); err != nil {
			log.Printf("session_cleanup: purge delete key=%s: %v", obj.Key, err)
			continue
		}
		deleted++
		freedBytes += obj.Size
	}

	log.Printf("session_cleanup: cache_purge scanned=%d deleted=%d freed_mb=%d",
		len(objs), deleted, freedBytes/(1024*1024))
}
