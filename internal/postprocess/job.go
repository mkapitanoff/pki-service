package postprocess

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// Deps bundles the dependencies ProcessDocument needs.
type Deps struct {
	Queries     *repository.Queries
	Store       storage.Storage // internal MinIO (signed PDFs, CMS archive)
	ExtS3       s3client.ExternalS3Client
	MaxAttempts int // после этого числа попыток — терминальный provал
}

// ProcessDocument finishes signing doc in the background: builds the stamped
// PDF (or reuses one from a prior partial attempt), uploads it internally and
// to the client's S3, and updates postprocess bookkeeping. Safe to call for
// the same doc more than once (at-least-once delivery from the poller tick) —
// see the CAS claim and check-before-rebuild guards below.
func ProcessDocument(ctx context.Context, deps Deps, doc repository.SigningSessionDocument) {
	claimed, err := deps.Queries.ClaimSessionDocumentForPostprocess(ctx, doc.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			// Already claimed by another tick/instance, or already terminal — no-op.
			return
		}
		log.Printf("postprocess: claim doc=%s: %v", doc.ID, err)
		return
	}
	doc = claimed

	if int(doc.PostprocessAttempts) >= deps.MaxAttempts {
		failDoc(ctx, deps, doc, fmt.Errorf("max attempts (%d) exhausted", deps.MaxAttempts))
		return
	}

	session, err := deps.Queries.GetSigningSessionByID(ctx, doc.SessionID)
	if err != nil {
		retryDoc(ctx, deps, doc, fmt.Errorf("load session: %w", err))
		return
	}

	signedPDF, err := ensureSignedPDF(ctx, deps, doc, session.SignerRole)
	if err != nil {
		retryDoc(ctx, deps, doc, err)
		return
	}

	if err := attemptClientUpload(ctx, deps.Queries, deps.ExtS3, session, doc, signedPDF); err != nil {
		retryDoc(ctx, deps, doc, fmt.Errorf("upload to client s3: %w", err))
		return
	}

	if _, err := deps.Queries.MarkSessionDocumentPostprocessDone(ctx, doc.ID); err != nil {
		log.Printf("postprocess: mark done doc=%s: %v", doc.ID, err)
	}
	log.Printf("postprocess: doc=%s done", doc.ID)
}

// ensureSignedPDF returns the stamped PDF bytes, reusing the internally
// stored one if a prior attempt already got that far (signed_s3_key set) —
// avoids re-running the 3 pdfcpu passes on retry once they've succeeded once.
func ensureSignedPDF(ctx context.Context, deps Deps, doc repository.SigningSessionDocument, signerRole string) ([]byte, error) {
	if doc.SignedS3Key.Valid && doc.SignedS3Key.String != "" {
		b, err := deps.Store.DownloadFile(ctx, doc.SignedS3Key.String)
		if err != nil {
			return nil, fmt.Errorf("download already-stamped pdf: %w", err)
		}
		return b, nil
	}

	pdfBytes, err := downloadOriginal(ctx, deps, doc)
	if err != nil {
		return nil, err
	}

	built, err := buildSignedPDF(pdfBytes, doc, signerRole)
	if err != nil {
		return nil, fmt.Errorf("build signed pdf: %w", err)
	}

	signedKey := fmt.Sprintf("signed/%s/%s.pdf", doc.SessionID, doc.ID)
	if err := deps.Store.UploadFile(ctx, signedKey, built, "application/pdf"); err != nil {
		return nil, fmt.Errorf("upload signed pdf: %w", err)
	}
	if err := deps.Queries.SetSessionDocumentSignedS3Key(ctx, repository.SetSessionDocumentSignedS3KeyParams{
		ID:          doc.ID,
		SignedS3Key: sql.NullString{String: signedKey, Valid: true},
	}); err != nil {
		// Non-fatal: PDF is uploaded and correct; worst case a retry re-stamps it.
		log.Printf("postprocess: persist signed_s3_key doc=%s: %v (non-fatal)", doc.ID, err)
	}
	return built, nil
}

// downloadOriginal fetches the original PDF — usually already cached in
// internal MinIO by the fetcher; falls back to an on-demand download from
// source_url for the client-hash fast path if the cache wasn't warm yet.
func downloadOriginal(ctx context.Context, deps Deps, doc repository.SigningSessionDocument) ([]byte, error) {
	if doc.CachedS3Key.Valid && doc.CachedS3Key.String != "" {
		b, err := deps.Store.DownloadFile(ctx, doc.CachedS3Key.String)
		if err != nil {
			return nil, fmt.Errorf("download cached pdf: %w", err)
		}
		return b, nil
	}
	if doc.SourceUrl == "" {
		return nil, fmt.Errorf("document has no cached_s3_key and no source_url")
	}
	b, _, err := s3client.DownloadWithRetry(ctx, deps.ExtS3, doc.SourceUrl, 3)
	if err != nil {
		return nil, fmt.Errorf("on-demand download source pdf: %w", err)
	}
	return b, nil
}

// retryDoc schedules a backoff retry, or escalates to a terminal failure once
// MaxAttempts is reached — bounds retries so a document can never hang forever.
func retryDoc(ctx context.Context, deps Deps, doc repository.SigningSessionDocument, cause error) {
	attempts := doc.PostprocessAttempts + 1
	if int(attempts) >= deps.MaxAttempts {
		failDoc(ctx, deps, doc, cause)
		return
	}
	code := classifyErrorCode(cause)
	nextAt := time.Now().Add(backoff(attempts))
	if err := deps.Queries.MarkSessionDocumentPostprocessRetrying(ctx, repository.MarkSessionDocumentPostprocessRetryingParams{
		ID:                   doc.ID,
		PostprocessError:     sql.NullString{String: cause.Error(), Valid: true},
		PostprocessErrorCode: sql.NullString{String: code, Valid: true},
		PostprocessNextAt:    sql.NullTime{Time: nextAt, Valid: true},
	}); err != nil {
		log.Printf("postprocess: mark retrying doc=%s: %v", doc.ID, err)
	}
	log.Printf("postprocess: doc=%s retry %d/%d in %s: %v", doc.ID, attempts, deps.MaxAttempts, backoff(attempts), cause)
}

// failDoc marks the document as terminally failed and resolves the session
// (a document can become terminal here without ever reaching the client-upload
// step, e.g. if pdfcpu stamping itself exhausts retries — session_failed must
// still fire in that case, not just on upload_failed).
func failDoc(ctx context.Context, deps Deps, doc repository.SigningSessionDocument, cause error) {
	code := classifyErrorCode(cause)
	if _, err := deps.Queries.MarkSessionDocumentPostprocessFailed(ctx, repository.MarkSessionDocumentPostprocessFailedParams{
		ID:                   doc.ID,
		PostprocessError:     sql.NullString{String: cause.Error(), Valid: true},
		PostprocessErrorCode: sql.NullString{String: code, Valid: true},
	}); err != nil {
		log.Printf("postprocess: mark failed doc=%s: %v", doc.ID, err)
	}
	log.Printf("postprocess: doc=%s TERMINAL FAILURE: %v", doc.ID, cause)

	if session, err := deps.Queries.GetSigningSessionByID(ctx, doc.SessionID); err == nil {
		CheckSessionCompletion(ctx, deps.Queries, session)
	}
}
