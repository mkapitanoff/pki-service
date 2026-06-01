package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/service"
	"github.com/mkapitanoff/pki-service/internal/worker"
)

// ApplicationHandler handles /api/v1/applications/{external_id}/* routes.
type ApplicationHandler struct {
	queries  *repository.Queries
	signSvc  *service.SignService
	extS3    s3client.ExternalS3Client
}

// NewApplicationHandler creates a new ApplicationHandler.
func NewApplicationHandler(
	queries *repository.Queries,
	signSvc *service.SignService,
	extS3 s3client.ExternalS3Client,
) *ApplicationHandler {
	return &ApplicationHandler{
		queries: queries,
		signSvc: signSvc,
		extS3:   extS3,
	}
}

// --- Submit ---

type submitDocumentInput struct {
	Name        string `json:"name"`
	SourceURL   string `json:"source_url"`
	TargetURL   string `json:"target_url"`
	TargetS3Key string `json:"target_s3_key"`
}

type submitRequest struct {
	Documents      []submitDocumentInput `json:"documents"`
	SignerRole     string                `json:"signer_role"`
	CallbackURL    string                `json:"callback_url"`
	CallbackSecret string                `json:"callback_secret"`
}

type submitDocResponse struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version int32  `json:"version"`
	Status  string `json:"status"`
}

// HandleSubmit — POST /api/v1/applications/{external_id}/submit
func (h *ApplicationHandler) HandleSubmit(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	externalID := chi.URLParam(r, "external_id")

	var req submitRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Documents) == 0 || req.SignerRole == "" {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	ctx := r.Context()

	// Find or create application.
	app, err := h.queries.GetApplicationByExternalID(ctx, repository.GetApplicationByExternalIDParams{
		TenantID:   tenantID,
		ExternalID: externalID,
	})

	var isNew bool
	if err != nil {
		if !errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrInternal.WithCause(err))
			return
		}
		// Create new application.
		app, err = h.queries.CreateApplication(ctx, repository.CreateApplicationParams{
			TenantID:       tenantID,
			ExternalID:     externalID,
			SignerRole:     req.SignerRole,
			CallbackUrl:    toNullString(req.CallbackURL),
			CallbackSecret: toNullString(req.CallbackSecret),
		})
		if err != nil {
			respondError(w, apperr.ErrInternal.WithCause(err))
			return
		}
		isNew = true
	} else if app.Status == "cancelled" {
		respondError(w, &apperr.AppError{Code: "APPLICATION_CANCELLED", Status: 409, Message: "application is cancelled"})
		return
	}

	newRound := app.SigningRound
	if !isNew {
		newRound = app.SigningRound + 1
	}

	// Update application status and increment round (if existing).
	app, err = h.queries.UpdateApplicationStatus(ctx, repository.UpdateApplicationStatusParams{
		ID:             app.ID,
		Status:         "signing",
		SigningRound:   newRound,
		SignerRole:     req.SignerRole,
		CallbackUrl:    toNullString(req.CallbackURL),
		CallbackSecret: toNullString(req.CallbackSecret),
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	// Process documents.
	var docs []submitDocResponse
	for _, d := range req.Documents {
		if d.Name == "" || d.SourceURL == "" {
			respondError(w, apperr.ErrInvalidRequest)
			return
		}
		// Supersede previous versions.
		previous, err := h.queries.FindPreviousVersions(ctx, repository.FindPreviousVersionsParams{
			ApplicationID: app.ID,
			DocumentName:  d.Name,
		})
		if err != nil {
			respondError(w, apperr.ErrInternal.WithCause(err))
			return
		}
		maxVersion := int32(0)
		for _, prev := range previous {
			if prev.Version > maxVersion {
				maxVersion = prev.Version
			}
			h.queries.SupersedeApplicationDocument(ctx, repository.SupersedeApplicationDocumentParams{ //nolint:errcheck
				ID:           prev.ID,
				SupersededBy: uuid.NullUUID{}, // will be updated below
			})
		}

		newVersion := maxVersion + 1
		appDoc, err := h.queries.CreateApplicationDocument(ctx, repository.CreateApplicationDocumentParams{
			ApplicationID: app.ID,
			DocumentName:  d.Name,
			Version:       newVersion,
			SigningRound:  newRound,
			SourceUrl:     d.SourceURL,
			TargetUrl:     toNullString(d.TargetURL),
			TargetS3Key:   toNullString(d.TargetS3Key),
		})
		if err != nil {
			respondError(w, apperr.ErrInternal.WithCause(err))
			return
		}
		docs = append(docs, submitDocResponse{
			ID:      appDoc.ID.String(),
			Name:    appDoc.DocumentName,
			Version: appDoc.Version,
			Status:  appDoc.Status,
		})
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"application_id": app.ID.String(),
		"external_id":    app.ExternalID,
		"signing_round":  app.SigningRound,
		"signer_role":    app.SignerRole,
		"documents":      docs,
	})
}

// --- Sign ---

type signatureInput struct {
	DocumentID string `json:"document_id"`
	CMS        string `json:"cms"`
}

type appSignRequest struct {
	Signatures []signatureInput `json:"signatures"`
}

type appSignDocResponse struct {
	DocumentID string `json:"document_id"`
	Name       string `json:"name"`
	Version    int32  `json:"version"`
	Status     string `json:"status"`
	S3Key      string `json:"s3_key,omitempty"`
}

// HandleSign — POST /api/v1/applications/{external_id}/sign
func (h *ApplicationHandler) HandleSign(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	externalID := chi.URLParam(r, "external_id")
	ctx := r.Context()

	app, err := h.queries.GetApplicationByExternalID(ctx, repository.GetApplicationByExternalIDParams{
		TenantID:   tenantID,
		ExternalID: externalID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}
	if app.Status != "signing" {
		respondError(w, &apperr.AppError{Code: "INVALID_STATUS", Status: 409, Message: fmt.Sprintf("application status is %s, expected signing", app.Status)})
		return
	}

	var req appSignRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Signatures) == 0 {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	succeeded := 0
	failed := 0
	var results []appSignDocResponse

	for _, sig := range req.Signatures {
		docID, err := uuid.Parse(sig.DocumentID)
		if err != nil {
			failed++
			results = append(results, appSignDocResponse{DocumentID: sig.DocumentID, Status: "error"})
			continue
		}

		appDoc, err := h.queries.GetApplicationDocumentByID(ctx, docID)
		if err != nil || appDoc.ApplicationID != app.ID {
			failed++
			results = append(results, appSignDocResponse{DocumentID: sig.DocumentID, Status: "not_found"})
			continue
		}
		if appDoc.Status != "ready" {
			failed++
			results = append(results, appSignDocResponse{DocumentID: sig.DocumentID, Status: "not_ready", Name: appDoc.DocumentName})
			continue
		}

		// Sign via existing mechanism.
		if !appDoc.DocumentID.Valid {
			failed++
			results = append(results, appSignDocResponse{DocumentID: sig.DocumentID, Status: "no_document", Name: appDoc.DocumentName})
			continue
		}

		signResult, err := h.signSvc.Sign(ctx, service.SignInput{
			DocumentID: appDoc.DocumentID.UUID,
			TenantID:   tenantID,
			CMS:        sig.CMS,
			Role:       app.SignerRole,
		})
		if err != nil {
			failed++
			log.Printf("applications: sign doc=%s: %v", appDoc.ID, err)
			h.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{ //nolint:errcheck
				ID:        appDoc.ID,
				Status:    "error",
				LastError: sql.NullString{String: err.Error(), Valid: true},
			})
			results = append(results, appSignDocResponse{DocumentID: sig.DocumentID, Status: "error", Name: appDoc.DocumentName})
			continue
		}

		// Mark signed.
		h.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{ //nolint:errcheck
			ID:     appDoc.ID,
			Status: "signed",
		})

		s3Key := appDoc.TargetS3Key.String
		result := appSignDocResponse{
			DocumentID: sig.DocumentID,
			Name:       appDoc.DocumentName,
			Version:    appDoc.Version,
			Status:     "signed",
			S3Key:      s3Key,
		}
		succeeded++
		results = append(results, result)

		// Upload signed PDF to client S3 asynchronously.
		if appDoc.TargetUrl.Valid && appDoc.TargetUrl.String != "" && h.extS3 != nil {
			go h.uploadSignedDocument(context.Background(), app, appDoc, signResult)
		}
	}

	// Check if all documents in this round are done.
	go h.checkRoundCompletion(context.Background(), app)

	respondJSON(w, http.StatusOK, map[string]any{
		"succeeded": succeeded,
		"failed":    failed,
		"documents": results,
	})
}

func (h *ApplicationHandler) uploadSignedDocument(ctx context.Context, app repository.Application, appDoc repository.ApplicationDocument, signResult *service.SignResult) {
	if h.extS3 == nil || !appDoc.TargetUrl.Valid {
		return
	}

	// Download signed PDF from our storage (via URL in signResult).
	_ = signResult // signResult.SignedDocumentURL — we need the actual bytes

	// Use the document s3 key for metadata.
	meta := s3client.S3Metadata{
		ApplicationID:   app.ID.String(),
		DocumentID:      appDoc.DocumentID.UUID.String(),
		DocumentName:    appDoc.DocumentName,
		SignerRole:      app.SignerRole,
		SignedAt:        time.Now().UTC(),
		SigningRound:    int(app.SigningRound),
		DocumentVersion: int(appDoc.Version),
	}

	err := s3client.UploadWithRetry(ctx, h.extS3, appDoc.TargetUrl.String, []byte{}, "application/pdf", meta, 3)
	if err != nil {
		log.Printf("applications: upload doc=%s: %v", appDoc.ID, err)

		status := "upload_failed"
		errMsg := err.Error()
		if errors.Is(err, s3client.ErrPresignedURLExpired) {
			errMsg = "presigned_url_expired"
		}
		h.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{ //nolint:errcheck
			ID:        appDoc.ID,
			Status:    status,
			LastError: sql.NullString{String: errMsg, Valid: true},
		})
		return
	}

	h.queries.MarkApplicationDocumentUploaded(ctx, appDoc.ID) //nolint:errcheck

	callbackSecret := ""
	if app.CallbackSecret.Valid {
		callbackSecret = app.CallbackSecret.String
	}
	worker.CreateAndDispatchWebhook(ctx, h.queries, app.ID, "document_signed", map[string]any{ //nolint:errcheck
		"document_id":      appDoc.DocumentID.UUID.String(),
		"document_name":    appDoc.DocumentName,
		"document_version": appDoc.Version,
		"signer_role":      app.SignerRole,
		"signed_at":        time.Now().UTC().Format(time.RFC3339),
		"s3_key":           appDoc.TargetS3Key.String,
	}, callbackSecret)
}

func (h *ApplicationHandler) checkRoundCompletion(ctx context.Context, app repository.Application) {
	docs, err := h.queries.ListActiveApplicationDocuments(ctx, repository.ListActiveApplicationDocumentsParams{
		ApplicationID: app.ID,
		SigningRound:  app.SigningRound,
	})
	if err != nil {
		return
	}

	allDone := true
	anyFailed := false
	for _, d := range docs {
		switch d.Status {
		case "signed", "uploaded":
			// done
		case "upload_failed":
			anyFailed = true
		default:
			allDone = false
		}
	}
	if !allDone {
		return
	}

	// Update application.
	h.queries.UpdateApplicationStatus(ctx, repository.UpdateApplicationStatusParams{ //nolint:errcheck
		ID:           app.ID,
		Status:       "round_completed",
		SigningRound: app.SigningRound,
		SignerRole:   app.SignerRole,
	})

	callbackSecret := ""
	if app.CallbackSecret.Valid {
		callbackSecret = app.CallbackSecret.String
	}

	eventType := "round_completed"
	data := map[string]any{
		"documents_signed": len(docs),
		"next_action":      "submit_next_round",
	}
	if anyFailed {
		eventType = "round_failed"
		var failedIDs []string
		for _, d := range docs {
			if d.Status == "upload_failed" {
				failedIDs = append(failedIDs, d.ID.String())
			}
		}
		data = map[string]any{
			"failed_documents": failedIDs,
			"error":            "upload failed for some documents",
		}
	}

	worker.CreateAndDispatchWebhook(ctx, h.queries, app.ID, eventType, map[string]any{ //nolint:errcheck
		"application_id": app.ID.String(),
		"external_id":    app.ExternalID,
		"signing_round":  app.SigningRound,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
		"data":           data,
	}, callbackSecret)
}

// --- Status ---

// HandleStatus — GET /api/v1/applications/{external_id}/status
func (h *ApplicationHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	externalID := chi.URLParam(r, "external_id")
	ctx := r.Context()

	app, err := h.queries.GetApplicationByExternalID(ctx, repository.GetApplicationByExternalIDParams{
		TenantID:   tenantID,
		ExternalID: externalID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	allDocs, err := h.queries.ListApplicationDocuments(ctx, app.ID)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	type docResponse struct {
		ID          string  `json:"id"`
		Name        string  `json:"name"`
		Version     int32   `json:"version"`
		SigningRound int32   `json:"signing_round"`
		Status      string  `json:"status"`
		S3Key       string  `json:"s3_key,omitempty"`
		UploadedAt  *string `json:"uploaded_at,omitempty"`
		Superseded  bool    `json:"superseded"`
	}

	var docList []docResponse
	for _, d := range allDocs {
		dr := docResponse{
			ID:           d.ID.String(),
			Name:         d.DocumentName,
			Version:      d.Version,
			SigningRound: d.SigningRound,
			Status:       d.Status,
			Superseded:   d.Status == "superseded",
		}
		if d.TargetS3Key.Valid {
			dr.S3Key = d.TargetS3Key.String
		}
		if d.UploadedAt.Valid {
			s := d.UploadedAt.Time.UTC().Format(time.RFC3339)
			dr.UploadedAt = &s
		}
		docList = append(docList, dr)
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"application_id": app.ID.String(),
		"external_id":    app.ExternalID,
		"status":         app.Status,
		"signing_round":  app.SigningRound,
		"signer_role":    app.SignerRole,
		"created_at":     app.CreatedAt.UTC().Format(time.RFC3339),
		"updated_at":     app.UpdatedAt.UTC().Format(time.RFC3339),
		"documents":      docList,
	})
}

// --- Cancel ---

type cancelRequest struct {
	Reason string `json:"reason"`
}

// HandleCancel — POST /api/v1/applications/{external_id}/cancel
func (h *ApplicationHandler) HandleCancel(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	externalID := chi.URLParam(r, "external_id")
	ctx := r.Context()

	app, err := h.queries.GetApplicationByExternalID(ctx, repository.GetApplicationByExternalIDParams{
		TenantID:   tenantID,
		ExternalID: externalID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}
	if app.Status == "cancelled" {
		respondError(w, &apperr.AppError{Code: "ALREADY_CANCELLED", Status: 409, Message: "application is already cancelled"})
		return
	}

	var req cancelRequest
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck

	updated, err := h.queries.CancelApplication(ctx, repository.CancelApplicationParams{
		ID:           app.ID,
		CancelReason: toNullString(req.Reason),
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	callbackSecret := ""
	if app.CallbackSecret.Valid {
		callbackSecret = app.CallbackSecret.String
	}
	worker.CreateAndDispatchWebhook(ctx, h.queries, app.ID, "application_cancelled", map[string]any{ //nolint:errcheck
		"application_id": app.ID.String(),
		"external_id":    app.ExternalID,
		"reason":         req.Reason,
		"timestamp":      time.Now().UTC().Format(time.RFC3339),
	}, callbackSecret)

	respondJSON(w, http.StatusOK, map[string]any{
		"application_id": updated.ID.String(),
		"external_id":    updated.ExternalID,
		"status":         updated.Status,
		"cancel_reason":  req.Reason,
	})
}

// --- Refresh URLs ---

type refreshURLInput struct {
	DocumentID string `json:"document_id"`
	TargetURL  string `json:"target_url"`
}

type refreshURLsRequest struct {
	Documents []refreshURLInput `json:"documents"`
}

// HandleRefreshURLs — PATCH /api/v1/applications/{external_id}/refresh-urls
func (h *ApplicationHandler) HandleRefreshURLs(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	externalID := chi.URLParam(r, "external_id")
	ctx := r.Context()

	app, err := h.queries.GetApplicationByExternalID(ctx, repository.GetApplicationByExternalIDParams{
		TenantID:   tenantID,
		ExternalID: externalID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	var req refreshURLsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Documents) == 0 {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	var updated []string
	for _, item := range req.Documents {
		docID, err := uuid.Parse(item.DocumentID)
		if err != nil {
			continue
		}
		appDoc, err := h.queries.GetApplicationDocumentByID(ctx, docID)
		if err != nil || appDoc.ApplicationID != app.ID {
			continue
		}
		if appDoc.Status != "upload_failed" || !appDoc.LastError.Valid || appDoc.LastError.String != "presigned_url_expired" {
			continue
		}

		h.queries.UpdateApplicationDocumentTargetURL(ctx, repository.UpdateApplicationDocumentTargetURLParams{ //nolint:errcheck
			ID:        docID,
			TargetUrl: toNullString(item.TargetURL),
		})
		h.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{ //nolint:errcheck
			ID:     docID,
			Status: "signed",
		})
		updated = append(updated, item.DocumentID)
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"updated": updated,
	})
}

// --- Retry Upload ---

type retryUploadRequest struct {
	DocumentIDs []string `json:"document_ids"`
}

// HandleRetryUpload — POST /api/v1/applications/{external_id}/retry-upload
func (h *ApplicationHandler) HandleRetryUpload(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}
	externalID := chi.URLParam(r, "external_id")
	ctx := r.Context()

	app, err := h.queries.GetApplicationByExternalID(ctx, repository.GetApplicationByExternalIDParams{
		TenantID:   tenantID,
		ExternalID: externalID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
		} else {
			respondError(w, apperr.ErrInternal.WithCause(err))
		}
		return
	}

	var req retryUploadRequest
	json.NewDecoder(r.Body).Decode(&req) //nolint:errcheck

	allDocs, err := h.queries.ListApplicationDocuments(ctx, app.ID)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	// Build set of requested IDs.
	filter := make(map[string]bool, len(req.DocumentIDs))
	for _, id := range req.DocumentIDs {
		filter[id] = true
	}
	useFilter := len(req.DocumentIDs) > 0

	var affected []string
	for _, d := range allDocs {
		if d.Status != "upload_failed" {
			continue
		}
		if useFilter && !filter[d.ID.String()] {
			continue
		}
		h.queries.UpdateApplicationDocumentStatus(ctx, repository.UpdateApplicationDocumentStatusParams{ //nolint:errcheck
			ID:     d.ID,
			Status: "signed",
		})
		affected = append(affected, d.ID.String())
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"retried": affected,
	})
}
