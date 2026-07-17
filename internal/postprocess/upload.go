package postprocess

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/signer"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// attemptClientUpload does exactly one upload attempt (with s3client's own
// internal 3x retry) to the client's pre-signed PUT URL. On success it marks
// the document uploaded, dispatches the document_signed webhook, and checks
// session completion. On failure it does NOT touch document status or session
// state — that is owned by the caller, since callers differ on what a failure
// means: ProcessDocument's bounded retry ladder (job.go) treats it as one of
// several scheduled attempts, while the manual /refresh-urls retry (below)
// treats it as terminal-again.
func attemptClientUpload(
	ctx context.Context,
	queries *repository.Queries,
	extS3 s3client.ExternalS3Client,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	signedPDF []byte,
) error {
	if extS3 == nil || !doc.TargetUrl.Valid || doc.TargetUrl.String == "" {
		return nil
	}

	appID := ""
	if session.ApplicationID.Valid {
		appID = session.ApplicationID.String
	}
	signedAt := time.Now().UTC()
	if doc.SignedAt.Valid {
		signedAt = doc.SignedAt.Time
	}
	cmsKey := ""
	if doc.CmsS3Key.Valid {
		cmsKey = doc.CmsS3Key.String
	}

	meta := s3client.S3Metadata{
		ApplicationID:   appID,
		DocumentID:      doc.ID.String(),
		DocumentName:    doc.DocumentName,
		SignerRole:      session.SignerRole,
		SignedAt:        signedAt,
		SigningRound:    1,
		DocumentVersion: 1,
		CMSStorageKey:   cmsKey,
	}

	err := s3client.UploadWithRetry(ctx, extS3, doc.TargetUrl.String, signedPDF, "application/pdf", meta, 3)
	if err != nil {
		log.Printf("postprocess: client upload doc=%s: %v", doc.ID, err)
		return err
	}

	queries.MarkSessionDocumentUploaded(ctx, doc.ID) //nolint:errcheck
	log.Printf("postprocess: client upload done doc=%s", doc.ID)

	updatedDoc, gerr := queries.GetSigningSessionDocument(ctx, doc.ID)
	if gerr != nil {
		updatedDoc = doc
	}
	signer.DispatchWebhook(ctx, session, updatedDoc, "document_signed", queries)

	CheckSessionCompletion(ctx, queries, session)
	return nil
}

// RetryClientUpload re-downloads a signed PDF from internal storage and makes
// one more upload attempt. Used by HandleRefreshURLs after a fresh target_url
// has been supplied for a document stuck in upload_failed — unlike
// ProcessDocument's bounded ladder, this is a single user-triggered attempt:
// on failure it marks the document upload_failed again (terminal, matching
// the pre-existing /refresh-urls contract) rather than scheduling a retry.
func RetryClientUpload(
	ctx context.Context,
	queries *repository.Queries,
	extS3 s3client.ExternalS3Client,
	store storage.Storage,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
) {
	if !doc.SignedS3Key.Valid || doc.SignedS3Key.String == "" {
		return
	}
	pdfBytes, err := store.DownloadFile(ctx, doc.SignedS3Key.String)
	if err != nil {
		log.Printf("postprocess: retry download signed pdf doc=%s: %v", doc.ID, err)
		return
	}
	if err := attemptClientUpload(ctx, queries, extS3, session, doc, pdfBytes); err != nil {
		errMsg := err.Error()
		if errors.Is(err, s3client.ErrPresignedURLExpired) {
			errMsg = "presigned_url_expired"
		}
		queries.UpdateSessionDocumentStatus(ctx, repository.UpdateSessionDocumentStatusParams{ //nolint:errcheck
			ID:        doc.ID,
			Status:    "upload_failed",
			LastError: sql.NullString{String: errMsg, Valid: true},
		})
		CheckSessionCompletion(ctx, queries, session)
	}
}
