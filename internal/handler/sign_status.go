package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/storage"
)

// SignStatusHandler handles GET /api/v1/sign/status/{session_id}
// and PATCH /api/v1/sign/refresh-urls.
type SignStatusHandler struct {
	queries *repository.Queries
	extS3   s3client.ExternalS3Client
	store   storage.Storage
}

func NewSignStatusHandler(
	queries *repository.Queries,
	extS3 s3client.ExternalS3Client,
	store storage.Storage,
) *SignStatusHandler {
	return &SignStatusHandler{queries: queries, extS3: extS3, store: store}
}

// --- GET /api/v1/sign/status/{session_id} ---

type sessionDocResponse struct {
	DocID      string  `json:"doc_id"`
	Name       string  `json:"name"`
	Status     string  `json:"status"`
	S3Key      string  `json:"s3_key,omitempty"`
	SignedAt   *string `json:"signed_at,omitempty"`
	UploadedAt *string `json:"uploaded_at,omitempty"`
	Error      *string `json:"error"`
}

func (h *SignStatusHandler) HandleGetStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	sessionID, err := uuid.Parse(chi.URLParam(r, "session_id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid session_id: %w", err)))
		return
	}

	ctx := r.Context()

	session, err := h.queries.GetSigningSession(ctx, repository.GetSigningSessionParams{
		ID:       sessionID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	// Auto-expire if past deadline and not already terminal.
	if time.Now().After(session.ExpiresAt) && !isTerminalStatus(session.Status) {
		updated, _ := h.queries.UpdateSigningSessionStatus(ctx, repository.UpdateSigningSessionStatusParams{
			ID:     session.ID,
			Status: "expired",
		})
		session = updated
	}

	docs, err := h.queries.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	// Compute dynamic effective status from documents if session is not terminal.
	effectiveStatus := session.Status
	if !isTerminalStatus(session.Status) {
		effectiveStatus = computeSessionStatus(docs)
	}

	var docList []sessionDocResponse
	for _, d := range docs {
		rd := sessionDocResponse{
			DocID:  d.ID.String(),
			Name:   d.DocumentName,
			Status: d.Status,
		}
		if d.TargetS3Key.Valid {
			rd.S3Key = d.TargetS3Key.String
		}
		if d.SignedAt.Valid {
			s := d.SignedAt.Time.UTC().Format(time.RFC3339)
			rd.SignedAt = &s
		}
		if d.UploadedAt.Valid {
			s := d.UploadedAt.Time.UTC().Format(time.RFC3339)
			rd.UploadedAt = &s
		}
		if d.LastError.Valid && d.LastError.String != "" {
			s := d.LastError.String
			rd.Error = &s
		}
		docList = append(docList, rd)
	}

	appID := ""
	if session.ApplicationID.Valid {
		appID = session.ApplicationID.String
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"session_id":     session.ID.String(),
		"status":         effectiveStatus,
		"expires_at":     session.ExpiresAt.UTC().Format(time.RFC3339),
		"application_id": appID,
		"documents":      docList,
	})
}

// --- PATCH /api/v1/sign/refresh-urls ---

type refreshURLDocInput struct {
	DocID     string `json:"doc_id"`
	TargetURL string `json:"target_url"`
}

type refreshURLsBody struct {
	SessionID string               `json:"session_id"`
	Documents []refreshURLDocInput `json:"documents"`
}

type refreshedDocResponse struct {
	DocID  string `json:"doc_id"`
	Name   string `json:"name"`
	Status string `json:"status"`
}

func (h *SignStatusHandler) HandleRefreshURLs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	var req refreshURLsBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	sessionID, err := uuid.Parse(req.SessionID)
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("invalid session_id: %w", err)))
		return
	}
	if len(req.Documents) == 0 {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("documents must not be empty")))
		return
	}

	ctx := r.Context()

	session, err := h.queries.GetSigningSession(ctx, repository.GetSigningSessionParams{
		ID:       sessionID,
		TenantID: tenantID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	var refreshed []refreshedDocResponse
	var skipped []map[string]string

	for _, item := range req.Documents {
		docID, err := uuid.Parse(item.DocID)
		if err != nil {
			skipped = append(skipped, map[string]string{"doc_id": item.DocID, "reason": "invalid UUID"})
			continue
		}
		if err := validateHTTPSURL(item.TargetURL); err != nil {
			skipped = append(skipped, map[string]string{"doc_id": item.DocID, "reason": fmt.Sprintf("invalid target_url: %v", err)})
			continue
		}

		doc, err := h.queries.GetSigningSessionDocument(ctx, docID)
		if err != nil || doc.SessionID != session.ID {
			skipped = append(skipped, map[string]string{"doc_id": item.DocID, "reason": "not found in this session"})
			continue
		}
		if doc.Status != "upload_failed" {
			skipped = append(skipped, map[string]string{
				"doc_id": item.DocID,
				"reason": fmt.Sprintf("status is %q, expected upload_failed", doc.Status),
			})
			continue
		}

		// Reset: new target URL + upload_attempts=0 + status='signed'.
		updated, err := h.queries.ResetSessionDocumentForRetry(ctx, repository.ResetSessionDocumentForRetryParams{
			ID:        docID,
			TargetUrl: sql.NullString{String: item.TargetURL, Valid: true},
		})
		if err != nil {
			skipped = append(skipped, map[string]string{"doc_id": item.DocID, "reason": "db update failed"})
			continue
		}

		refreshed = append(refreshed, refreshedDocResponse{
			DocID:  updated.ID.String(),
			Name:   updated.DocumentName,
			Status: updated.Status,
		})

		// Download signed PDF and retry upload asynchronously.
		if updated.SignedS3Key.Valid && updated.SignedS3Key.String != "" {
			go func(sess repository.SigningSession, ud repository.SigningSessionDocument) {
				pdfBytes, err := h.store.DownloadFile(context.Background(), ud.SignedS3Key.String)
				if err != nil {
					return
				}
				uploadSessionDocToClientS3(context.Background(), h.queries, h.extS3, h.store, sess, ud, pdfBytes)
			}(session, updated)
		}
	}

	if len(skipped) > 0 && len(refreshed) == 0 {
		// All were skipped — 400 with details.
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error": map[string]any{
				"code":    "NO_DOCUMENTS_REFRESHED",
				"message": "no documents were eligible for URL refresh",
				"skipped": skipped,
			},
		})
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"refreshed": len(refreshed),
		"documents": refreshed,
		"skipped":   skipped,
	})
}

// --- helpers ---

func isTerminalStatus(status string) bool {
	switch status {
	case "completed", "failed", "expired":
		return true
	}
	return false
}

// computeSessionStatus derives an effective session status from its documents.
func computeSessionStatus(docs []repository.SigningSessionDocument) string {
	if len(docs) == 0 {
		return "pending"
	}
	allUploaded := true
	anyUploadFailed := false
	anyActive := false
	allReady := true

	for _, d := range docs {
		switch d.Status {
		case "uploaded":
			allReady = false
		case "upload_failed":
			anyUploadFailed = true
			allUploaded = false
			allReady = false
		case "signing", "signed", "uploading":
			anyActive = true
			allUploaded = false
			allReady = false
		default:
			allUploaded = false
			if d.Status != "ready" {
				allReady = false
			}
		}
	}

	if allUploaded {
		return "completed"
	}
	if anyUploadFailed && !anyActive {
		return "failed"
	}
	if anyActive {
		return "signing"
	}
	if allReady {
		return "pending"
	}
	return "signing"
}
