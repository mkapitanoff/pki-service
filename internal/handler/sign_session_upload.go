package handler

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
	"github.com/mkapitanoff/pki-service/internal/worker"
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

	callbackSecret := ""
	if session.CallbackSecret.Valid {
		callbackSecret = session.CallbackSecret.String
	}
	if session.CallbackUrl.Valid && session.CallbackUrl.String != "" {
		worker.CreateAndDispatchWebhook(ctx, queries, session.ID, "document_signed", map[string]any{ //nolint:errcheck
			"application_id": appID,
			"session_id":     session.ID.String(),
			"document_id":    doc.ID.String(),
			"document_name":  doc.DocumentName,
			"signer_role":    session.SignerRole,
			"signed_at":      signedAt.Format(time.RFC3339),
			"s3_key":         doc.TargetS3Key.String,
		}, callbackSecret)
	}

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

	if !session.CallbackUrl.Valid || session.CallbackUrl.String == "" {
		return
	}
	callbackSecret := ""
	if session.CallbackSecret.Valid {
		callbackSecret = session.CallbackSecret.String
	}
	appID := ""
	if session.ApplicationID.Valid {
		appID = session.ApplicationID.String
	}
	worker.CreateAndDispatchWebhook(ctx, queries, session.ID, eventType, map[string]any{ //nolint:errcheck
		"application_id": appID,
		"session_id":     session.ID.String(),
		"status":         newStatus,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	}, callbackSecret)
}
