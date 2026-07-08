package signer

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"mime"
	"strings"

	"github.com/mkapitanoff/pki-service/internal/reqctx"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// isAcceptablePDFContentType проверяет Content-Type ответа при скачивании
// документа. Сравнивает базовый media type без параметров и допускает
// октет-стрим/пустой заголовок (типично для S3 presigned URL). Финальную
// достоверность гарантирует проверка PDF-магии (%PDF-) у вызывающего.
func isAcceptablePDFContentType(ct string) bool {
	ct = strings.TrimSpace(ct)
	if ct == "" {
		return true
	}
	mt, _, err := mime.ParseMediaType(ct)
	if err != nil {
		// Неразбираемый заголовок — не блокируем, полагаемся на PDF-магию.
		return true
	}
	switch mt {
	case "application/pdf", "application/octet-stream", "binary/octet-stream":
		return true
	default:
		return false
	}
}

// FetchAndCacheDocument downloads a signing session document from the client's
// S3 via pre-signed URL, verifies it is a PDF, computes its SHA-256, stores it
// in our MinIO, and records the result in the DB.
//
// The function is synchronous — callers manage parallelism.
func FetchAndCacheDocument(
	ctx context.Context,
	doc repository.SigningSessionDocument,
	extS3 s3client.ExternalS3Client,
	store storage.Storage,
	queries *repository.Queries,
) error {
	rid := reqctx.RequestID(ctx)
	log.Printf("fetch.start request_id=%s session_id=%s doc_id=%s name=%q hash_source=%s",
		rid, doc.SessionID, doc.ID, doc.DocumentName, doc.HashSource)

	// Mark as fetching so concurrent workers skip it.
	if _, err := queries.UpdateSessionDocumentStatus(ctx, repository.UpdateSessionDocumentStatusParams{
		ID:        doc.ID,
		Status:    "fetching",
		LastError: sql.NullString{},
	}); err != nil {
		return fmt.Errorf("fetcher: mark fetching doc=%s: %w", doc.ID, err)
	}

	pdfBytes, contentType, err := s3client.DownloadWithRetry(ctx, extS3, doc.SourceUrl, 3)
	if err != nil {
		return markFetchFailed(ctx, queries, doc, fmt.Errorf("download: %w", err))
	}

	// Content-Type проверяем по базовому media type, игнорируя параметры
	// (W3C отдаёт "application/pdf; qs=0.001", CDN/S3 — "application/pdf;
	// charset=binary"). Также принимаем application/octet-stream — частый
	// дефолт для S3 presigned URL без явного ContentType на объекте, и пустой
	// заголовок. Достоверность проверяем ниже по PDF-магии (%PDF-).
	if !isAcceptablePDFContentType(contentType) {
		return markFetchFailed(ctx, queries, doc, fmt.Errorf("unexpected content-type %q, want application/pdf", contentType))
	}
	if len(pdfBytes) < 5 || string(pdfBytes[:5]) != "%PDF-" {
		return markFetchFailed(ctx, queries, doc, fmt.Errorf("not a PDF: missing %%PDF- header (content-type %q)", contentType))
	}

	hashHex := ComputeSHA256Hex(pdfBytes)

	cachedKey := fmt.Sprintf("cache/%s/%s.pdf", doc.SessionID, doc.ID)
	if err := store.UploadFile(ctx, cachedKey, pdfBytes, "application/pdf"); err != nil {
		return markFetchFailed(ctx, queries, doc, fmt.Errorf("store to MinIO: %w", err))
	}

	// client-mode: hash уже пришёл в /sign/initiate и авторитетен. Целостность
	// (не подменили ли объект в S3) проверяет async verification-воркер по
	// прямому S3 HEAD (x-amz-meta-sha256 ↔ content_hash). Здесь фетч только
	// кэширует PDF для /complete; расхождение recompute — WARN, но НЕ валим
	// сессию и не блокируем подписание.
	if doc.HashSource == "client" && doc.ContentHash.Valid {
		if doc.ContentHash.String != hashHex {
			log.Printf("fetcher: WARN client_hash recompute mismatch doc=%s want=%s have=%s (integrity handled by async verifier)",
				doc.ID, doc.ContentHash.String, hashHex)
		}
		if _, err := queries.UpdateSessionDocumentAfterFetchKeepHash(ctx, repository.UpdateSessionDocumentAfterFetchKeepHashParams{
			ID:          doc.ID,
			CachedS3Key: sql.NullString{String: cachedKey, Valid: true},
		}); err != nil {
			return fmt.Errorf("fetcher: update after fetch (keep hash) doc=%s: %w", doc.ID, err)
		}
		return nil
	}

	// computed-mode: фетчер сам выставляет content_hash.
	if _, err := queries.UpdateSessionDocumentAfterFetch(ctx, repository.UpdateSessionDocumentAfterFetchParams{
		ID:          doc.ID,
		ContentHash: sql.NullString{String: hashHex, Valid: true},
		CachedS3Key: sql.NullString{String: cachedKey, Valid: true},
	}); err != nil {
		return fmt.Errorf("fetcher: update after fetch doc=%s: %w", doc.ID, err)
	}

	return nil
}

// markFetchFailed sets status='fetch_failed' and last_error, then returns the original error.
func markFetchFailed(ctx context.Context, queries *repository.Queries, doc repository.SigningSessionDocument, cause error) error {
	queries.UpdateSessionDocumentStatus(ctx, repository.UpdateSessionDocumentStatusParams{ //nolint:errcheck
		ID:        doc.ID,
		Status:    "fetch_failed",
		LastError: sql.NullString{String: cause.Error(), Valid: true},
	})
	return cause
}

// CacheDocumentForComplete — фоновый путь fast-path'а. Документ с клиентским
// хэшем уже создан со status='ready' (хэш авторитетен), поэтому /sign/initiate
// НЕ качает его синхронно. Эта функция в фоне скачивает PDF по source_url и
// кладёт в кэш MinIO, проставляя cached_s3_key, — чтобы /sign/complete встроил
// подпись без повторного обращения к S3.
//
// Важно: статус документа НЕ трогает при ошибке (остаётся 'ready') — только
// WARN-лог. Если кэш не готов к моменту /complete, тот докачает on-demand.
func CacheDocumentForComplete(
	ctx context.Context,
	doc repository.SigningSessionDocument,
	extS3 s3client.ExternalS3Client,
	store storage.Storage,
	queries *repository.Queries,
) {
	rid := reqctx.RequestID(ctx)
	pdfBytes, contentType, err := s3client.DownloadWithRetry(ctx, extS3, doc.SourceUrl, 3)
	if err != nil {
		log.Printf("cache.bg WARN request_id=%s doc=%s download: %v (complete will fetch on-demand)", rid, doc.ID, err)
		return
	}
	if !isAcceptablePDFContentType(contentType) || len(pdfBytes) < 5 || string(pdfBytes[:5]) != "%PDF-" {
		log.Printf("cache.bg WARN request_id=%s doc=%s not a PDF (content-type=%q)", rid, doc.ID, contentType)
		return
	}
	cachedKey := fmt.Sprintf("cache/%s/%s.pdf", doc.SessionID, doc.ID)
	if err := store.UploadFile(ctx, cachedKey, pdfBytes, "application/pdf"); err != nil {
		log.Printf("cache.bg WARN request_id=%s doc=%s store: %v", rid, doc.ID, err)
		return
	}
	if _, err := queries.UpdateSessionDocumentAfterFetchKeepHash(ctx, repository.UpdateSessionDocumentAfterFetchKeepHashParams{
		ID:          doc.ID,
		CachedS3Key: sql.NullString{String: cachedKey, Valid: true},
	}); err != nil {
		log.Printf("cache.bg WARN request_id=%s doc=%s update: %v", rid, doc.ID, err)
		return
	}
	log.Printf("cache.bg ok request_id=%s doc=%s cached_key=%s", rid, doc.ID, cachedKey)
}
