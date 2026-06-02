package handler

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/mkapitanoff/pki-service/internal/repository"
	"github.com/mkapitanoff/pki-service/internal/s3client"
	"github.com/mkapitanoff/pki-service/internal/signer"
	"github.com/mkapitanoff/pki-service/internal/storage"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
)

// SignInitiateHandler handles POST /api/v1/sign/initiate.
type SignInitiateHandler struct {
	queries *repository.Queries
	extS3   s3client.ExternalS3Client
	store   storage.Storage
}

func NewSignInitiateHandler(
	queries *repository.Queries,
	extS3 s3client.ExternalS3Client,
	store storage.Storage,
) *SignInitiateHandler {
	return &SignInitiateHandler{queries: queries, extS3: extS3, store: store}
}

// --- request / response types ---

type initiateDocInput struct {
	Name        string `json:"name"`
	SourceURL   string `json:"source_url"`
	TargetURL   string `json:"target_url"`
	TargetS3Key string `json:"target_s3_key"`
}

type initiateRequest struct {
	Documents      []initiateDocInput `json:"documents"`
	SignerRole     string             `json:"signer_role"`
	ApplicationID  string             `json:"application_id"`
	CallbackURL    string             `json:"callback_url"`
	CallbackSecret string             `json:"callback_secret"`
}

type initiateDocResponse struct {
	DocID         string `json:"doc_id"`
	Name          string `json:"name"`
	Hash          string `json:"hash,omitempty"`
	HashAlgorithm string `json:"hash_algorithm,omitempty"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
}

var validSignerRoles = map[string]bool{
	"client":   true,
	"manager":  true,
	"director": true,
}

// HandleInitiate — POST /api/v1/sign/initiate
func (h *SignInitiateHandler) HandleInitiate(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	var req initiateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	// --- validation ---
	if len(req.Documents) == 0 || len(req.Documents) > 20 {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("documents must have 1–20 items")))
		return
	}
	for i, d := range req.Documents {
		if d.Name == "" {
			respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("documents[%d].name is required", i)))
			return
		}
		if err := validateHTTPSURL(d.SourceURL); err != nil {
			respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("documents[%d].source_url: %w", i, err)))
			return
		}
		if err := validateHTTPSURL(d.TargetURL); err != nil {
			respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("documents[%d].target_url: %w", i, err)))
			return
		}
	}
	if !validSignerRoles[req.SignerRole] {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("signer_role must be one of: client, manager, director")))
		return
	}
	if req.CallbackURL != "" && req.CallbackSecret == "" {
		respondError(w, apperr.ErrInvalidRequest.WithCause(fmt.Errorf("callback_secret is required when callback_url is set")))
		return
	}

	ctx := r.Context()

	// 1. Create signing session (expires in 2 hours, default from DB).
	session, err := h.queries.CreateSigningSession(ctx, repository.CreateSigningSessionParams{
		TenantID:       tenantID,
		ApplicationID:  toNullString(req.ApplicationID),
		SignerRole:     req.SignerRole,
		CallbackUrl:    toNullString(req.CallbackURL),
		CallbackSecret: toNullString(req.CallbackSecret),
		Column6:        nil, // use DB default: now() + 2h
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	// 2. Create document records and collect IDs for fetching.
	type docEntry struct {
		appDoc repository.SigningSessionDocument
		input  initiateDocInput
	}
	entries := make([]docEntry, 0, len(req.Documents))
	for _, d := range req.Documents {
		appDoc, err := h.queries.CreateSigningSessionDocument(ctx, repository.CreateSigningSessionDocumentParams{
			SessionID:    session.ID,
			DocumentName: d.Name,
			SourceUrl:    d.SourceURL,
			TargetUrl:    toNullString(d.TargetURL),
			TargetS3Key:  toNullString(d.TargetS3Key),
		})
		if err != nil {
			respondError(w, apperr.ErrInternal.WithCause(err))
			return
		}
		entries = append(entries, docEntry{appDoc: appDoc, input: d})
	}

	// 3. Fetch all documents concurrently, timeout 55s.
	fetchCtx, cancel := context.WithTimeout(ctx, 55*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	for _, e := range entries {
		wg.Add(1)
		go func(doc repository.SigningSessionDocument) {
			defer wg.Done()
			signer.FetchAndCacheDocument(fetchCtx, doc, h.extS3, h.store, h.queries) //nolint:errcheck
		}(e.appDoc)
	}
	wg.Wait()

	// 4. Re-read documents from DB (statuses updated by FetchAndCacheDocument).
	freshDocs, err := h.queries.ListSessionDocuments(ctx, session.ID)
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	// 5. Build response documents; check if all failed.
	allFailed := true
	var respDocs []initiateDocResponse
	for _, doc := range freshDocs {
		rd := initiateDocResponse{
			DocID:  doc.ID.String(),
			Name:   doc.DocumentName,
			Status: doc.Status,
		}
		if doc.Status == "ready" {
			allFailed = false
			// Convert stored hex hash → base64 for NCALayer.
			if hashBytes, err := hex.DecodeString(doc.ContentHash); err == nil {
				rd.Hash = base64.StdEncoding.EncodeToString(hashBytes)
				rd.HashAlgorithm = "SHA256"
			}
		} else if doc.Status == "fetch_failed" {
			if doc.LastError.Valid {
				rd.Error = doc.LastError.String
			}
		}
		respDocs = append(respDocs, rd)
	}

	// 6. All documents failed → 502.
	if allFailed {
		respondJSON(w, http.StatusBadGateway, map[string]any{
			"error": map[string]any{
				"code":      "FETCH_FAILED",
				"message":   "all documents failed to fetch from provided URLs",
				"documents": respDocs,
			},
		})
		return
	}

	// 7. Success response.
	respondJSON(w, http.StatusOK, map[string]any{
		"session_id": session.ID.String(),
		"expires_at": session.ExpiresAt.UTC().Format(time.RFC3339),
		"documents":  respDocs,
	})
}

// validateHTTPSURL checks that s is a non-empty, parseable HTTPS URL.
func validateHTTPSURL(s string) error {
	if s == "" {
		return fmt.Errorf("URL is required")
	}
	u, err := url.ParseRequestURI(s)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("URL must use https scheme, got %q", u.Scheme)
	}
	return nil
}
