package postprocess

import (
	"context"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/signer"
)

// CheckSessionCompletion marks the session completed/failed once all documents
// are in a terminal state, and dispatches the appropriate webhook. Terminal
// states are "uploaded" (success) and "upload_failed"/"post_process_failed"
// (failure) — the latter added so a document that never reaches the upload
// step (e.g. it exhausts retries during PDF stamping) still resolves the
// session instead of leaving it stuck in "signing" forever.
func CheckSessionCompletion(ctx context.Context, queries *repository.Queries, session repository.SigningSession) {
	docs, err := queries.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		return
	}

	allDone := true
	anyFailed := false
	for _, d := range docs {
		switch d.Status {
		case "uploaded", "upload_failed", "post_process_failed":
			if d.Status != "uploaded" {
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
