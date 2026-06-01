package s3client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// UploadWithRetry calls UploadToPresignedURL with exponential backoff.
// Backoff schedule: 1s → 3s → 9s.
// Returns immediately on ErrPresignedURLExpired (do not retry 403).
func UploadWithRetry(
	ctx context.Context,
	client ExternalS3Client,
	rawURL string,
	data []byte,
	contentType string,
	meta S3Metadata,
	maxAttempts int,
) error {
	if maxAttempts <= 0 {
		maxAttempts = 3
	}
	delays := []time.Duration{1 * time.Second, 3 * time.Second, 9 * time.Second}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		err := client.UploadToPresignedURL(ctx, rawURL, data, contentType, meta)
		if err == nil {
			return nil
		}
		if errors.Is(err, ErrPresignedURLExpired) {
			return ErrPresignedURLExpired
		}
		lastErr = err
		if attempt < maxAttempts {
			delay := delays[attempt-1]
			log.Printf("s3client: upload attempt %d/%d failed: %v; retrying in %s", attempt, maxAttempts, err, delay)
			select {
			case <-ctx.Done():
				return fmt.Errorf("s3client: upload cancelled after %d attempts: %w", attempt, ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("s3client: upload failed after %d attempts: %w", maxAttempts, lastErr)
}
