package signer

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

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

	if contentType != "application/pdf" {
		return markFetchFailed(ctx, queries, doc, fmt.Errorf("unexpected content-type %q, want application/pdf", contentType))
	}

	hashHex := ComputeSHA256Hex(pdfBytes)

	cachedKey := fmt.Sprintf("cache/%s/%s.pdf", doc.SessionID, doc.ID)
	if err := store.UploadFile(ctx, cachedKey, pdfBytes, "application/pdf"); err != nil {
		return markFetchFailed(ctx, queries, doc, fmt.Errorf("store to MinIO: %w", err))
	}

	// client-mode: hash уже пришёл в /sign/initiate. Sanity-check: реально
	// посчитанный SHA должен совпадать. Если нет — клиент мог подменить хэш
	// или объект изменился между загрузкой и фетчем; помечаем mismatch и
	// дальше не идём.
	if doc.HashSource == "client" && doc.ContentHash.Valid {
		if doc.ContentHash.String != hashHex {
			_, _ = queries.MarkSessionDocumentTampered(ctx, repository.MarkSessionDocumentTamperedParams{
				ID:                 doc.ID,
				VerificationError:  sql.NullString{String: "client_hash_mismatch", Valid: true},
			})
			return fmt.Errorf("fetcher: client_hash_mismatch doc=%s: want=%s have=%s",
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
