package handler

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

// uploadSessionDocToClientS3 uploads a signed PDF to the client's pre-signed PUT URL.
// It handles retries, marks the document uploaded/upload_failed, dispatches webhooks,
// and triggers session completion check. Intended to be called in a goroutine.
func uploadSessionDocToClientS3(
	ctx context.Context,
	queries *repository.Queries,
	extS3 s3client.ExternalS3Client,
	store storage.Storage,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	signedPDF []byte,
) {
	if extS3 == nil || !doc.TargetUrl.Valid || doc.TargetUrl.String == "" {
		return
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
		errMsg := err.Error()
		if errors.Is(err, s3client.ErrPresignedURLExpired) {
			errMsg = "presigned_url_expired"
		}
		log.Printf("session_upload: upload doc=%s: %v", doc.ID, err)
		queries.UpdateSessionDocumentStatus(ctx, repository.UpdateSessionDocumentStatusParams{ //nolint:errcheck
			ID:        doc.ID,
			Status:    "upload_failed",
			LastError: sql.NullString{String: errMsg, Valid: true},
		})
		checkSessionCompletion(ctx, queries, session)
		return
	}

	queries.MarkSessionDocumentUploaded(ctx, doc.ID) //nolint:errcheck
	log.Printf("session_upload: uploaded doc=%s", doc.ID)

	// Re-load the updated doc so signed_at / target_s3_key are populated.
	updatedDoc, err := queries.GetSigningSessionDocument(ctx, doc.ID)
	if err != nil {
		updatedDoc = doc
	}

	signer.DispatchWebhook(ctx, session, updatedDoc, "document_signed", queries)

	checkSessionCompletion(ctx, queries, session)
}

// checkSessionCompletion marks the session completed/failed once all documents
// are in a terminal upload state, and dispatches the appropriate webhook.
func checkSessionCompletion(ctx context.Context, queries *repository.Queries, session repository.SigningSession) {
	docs, err := queries.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		return
	}

	allDone := true
	anyFailed := false
	for _, d := range docs {
		switch d.Status {
		case "uploaded", "upload_failed":
			if d.Status == "upload_failed" {
				anyFailed = true
			}
		default:
			allDone = false
		}
	}
	if !allDone {
		return
	}

	newStatus := "completed"
	eventType := "session_completed"
	if anyFailed {
		newStatus = "failed"
		eventType = "session_failed"
	}

	queries.UpdateSigningSessionStatus(ctx, repository.UpdateSigningSessionStatusParams{ //nolint:errcheck
		ID:     session.ID,
		Status: newStatus,
	})

	// Pass an empty doc — payload builder will query all docs from DB.
	signer.DispatchWebhook(ctx, session, repository.SigningSessionDocument{}, eventType, queries)
}
