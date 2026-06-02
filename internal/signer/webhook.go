package signer

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
)

var webhookHTTPClient = &http.Client{Timeout: 10 * time.Second}

// backoffs between retries: wait 5s before attempt 2, 30s before attempt 3.
var webhookBackoffs = []time.Duration{5 * time.Second, 30 * time.Second}

// DispatchWebhook fires the webhook for a signing-session event in a goroutine.
// If session.CallbackUrl is empty the event is only logged.
func DispatchWebhook(
	ctx context.Context,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	event string,
	queries *repository.Queries,
) {
	go dispatchWebhook(context.Background(), session, doc, event, queries)
}

func dispatchWebhook(
	ctx context.Context,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	event string,
	queries *repository.Queries,
) {
	if !session.CallbackUrl.Valid || session.CallbackUrl.String == "" {
		log.Printf("webhook: session=%s event=%s no callback_url, skipping HTTP", session.ID, event)
		return
	}

	payload, err := buildSessionPayload(ctx, event, session, doc, queries)
	if err != nil {
		log.Printf("webhook: session=%s event=%s build payload: %v", session.ID, event, err)
		return
	}

	callbackURL := session.CallbackUrl.String
	unixTS := fmt.Sprintf("%d", time.Now().Unix())

	var sig string
	if session.CallbackSecret.Valid && session.CallbackSecret.String != "" {
		mac := hmac.New(sha256.New, []byte(session.CallbackSecret.String))
		mac.Write(payload)
		sig = "sha256=" + hex.EncodeToString(mac.Sum(nil))
	}

	for attempt := 1; attempt <= 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, callbackURL, bytes.NewReader(payload))
		if err != nil {
			log.Printf("webhook: session=%s event=%s attempt=%d build request: %v", session.ID, event, attempt, err)
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-PKI-Event", event)
		req.Header.Set("X-PKI-Timestamp", unixTS)
		if sig != "" {
			req.Header.Set("X-PKI-Signature", sig)
		}

		resp, doErr := webhookHTTPClient.Do(req)
		if doErr == nil {
			code := resp.StatusCode
			resp.Body.Close()
			if code >= 200 && code < 300 {
				log.Printf("webhook: session=%s event=%s delivered (attempt=%d status=%d)", session.ID, event, attempt, code)
				return
			}
			log.Printf("webhook: session=%s event=%s attempt=%d non-2xx status=%d", session.ID, event, attempt, code)
		} else {
			log.Printf("webhook: session=%s event=%s attempt=%d error: %v", session.ID, event, attempt, doErr)
		}

		if attempt < 3 {
			time.Sleep(webhookBackoffs[attempt-1])
		}
	}
	log.Printf("webhook: session=%s event=%s all 3 attempts exhausted", session.ID, event)
}

func buildSessionPayload(
	ctx context.Context,
	event string,
	session repository.SigningSession,
	doc repository.SigningSessionDocument,
	queries *repository.Queries,
) ([]byte, error) {
	appID := ""
	if session.ApplicationID.Valid {
		appID = session.ApplicationID.String
	}
	now := time.Now().UTC().Format(time.RFC3339)

	var data map[string]any

	switch event {
	case "document_signed":
		signedAt := now
		if doc.SignedAt.Valid {
			signedAt = doc.SignedAt.Time.UTC().Format(time.RFC3339)
		}
		s3Key := ""
		if doc.TargetS3Key.Valid {
			s3Key = doc.TargetS3Key.String
		}
		data = map[string]any{
			"doc_id":        doc.ID.String(),
			"document_name": doc.DocumentName,
			"signed_at":     signedAt,
			"s3_key":        s3Key,
		}

	case "session_completed":
		docs, err := queries.ListSessionDocuments(ctx, session.ID)
		if err != nil {
			return nil, fmt.Errorf("list docs: %w", err)
		}
		count := 0
		for _, d := range docs {
			if d.Status == "uploaded" {
				count++
			}
		}
		data = map[string]any{
			"documents_signed": count,
			"next_action":      "submit_next_round",
		}

	case "session_failed":
		docs, err := queries.ListSessionDocuments(ctx, session.ID)
		if err != nil {
			return nil, fmt.Errorf("list docs: %w", err)
		}
		var failedIDs []string
		errMsg := "S3 upload failed"
		for _, d := range docs {
			if d.Status == "upload_failed" {
				failedIDs = append(failedIDs, d.ID.String())
				if d.LastError.Valid && d.LastError.String != "" {
					errMsg = "S3 upload failed: " + d.LastError.String
				}
			}
		}
		data = map[string]any{
			"failed_documents": failedIDs,
			"error":            errMsg,
		}

	default:
		data = map[string]any{}
	}

	return json.Marshal(map[string]any{
		"event":          event,
		"session_id":     session.ID.String(),
		"application_id": appID,
		"signer_role":    session.SignerRole,
		"timestamp":      now,
		"data":           data,
	})
}
