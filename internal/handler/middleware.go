package handler

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	stderrors "errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// tenantRecordKey holds the full authenticated repository.Tenant. The
// uuid-only TenantIDKey (see sign.go) is also set so existing handlers that
// read tenantFromCtx keep working.
const tenantRecordKey ctxKey = "tenant_record"

func withTenantRecord(ctx context.Context, t repository.Tenant) context.Context {
	return context.WithValue(ctx, tenantRecordKey, t)
}

// TenantFromCtx extracts the authenticated tenant from the request context.
func TenantFromCtx(r *http.Request) (repository.Tenant, error) {
	t, ok := r.Context().Value(tenantRecordKey).(repository.Tenant)
	if !ok {
		return repository.Tenant{}, apperr.ErrUnauthorized
	}
	return t, nil
}

// APIKeyAuth authenticates requests via "Authorization: Bearer <key>".
func APIKeyAuth(queries *repository.Queries) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			const prefix = "Bearer "
			if !strings.HasPrefix(authz, prefix) {
				respondError(w, apperr.ErrUnauthorized)
				return
			}
			rawKey := strings.TrimSpace(authz[len(prefix):])
			if rawKey == "" {
				respondError(w, apperr.ErrUnauthorized)
				return
			}

			sum := sha256.Sum256([]byte(rawKey))
			keyHash := hex.EncodeToString(sum[:])

			ctx := r.Context()
			apiKey, err := queries.GetAPIKeyByHash(ctx, keyHash)
			if err != nil {
				// Not found or not active (query filters is_active=true).
				respondError(w, apperr.ErrUnauthorized)
				return
			}
			if !apiKey.IsActive {
				respondError(w, apperr.ErrUnauthorized)
				return
			}
			if apiKey.ExpiresAt.Valid && apiKey.ExpiresAt.Time.Before(time.Now()) {
				respondError(w, apperr.ErrUnauthorized)
				return
			}

			tenant, err := queries.GetTenant(ctx, apiKey.TenantID)
			if err != nil {
				if stderrors.Is(err, sql.ErrNoRows) {
					respondError(w, apperr.ErrUnauthorized)
					return
				}
				respondError(w, apperr.ErrInternal.WithCause(err))
				return
			}
			if !tenant.IsActive {
				respondError(w, apperr.ErrForbidden)
				return
			}

			// Best-effort; a failed touch must not block the request.
			_ = queries.UpdateAPIKeyLastUsed(ctx, apiKey.ID)

			ctx = WithTenant(ctx, tenant.ID)
			ctx = withTenantRecord(ctx, tenant)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RateLimiter is a fixed-window in-memory limiter keyed by client IP. Counters
// reset every minute via a background goroutine.
func RateLimiter(requestsPerMinute int) func(http.Handler) http.Handler {
	var counters sync.Map // ip -> *int64

	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			counters.Range(func(k, _ any) bool {
				counters.Delete(k)
				return true
			})
		}
	}()

	tooMany := &apperr.AppError{
		Code:    "RATE_LIMITED",
		Status:  http.StatusTooManyRequests,
		Message: "Too many requests",
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if requestsPerMinute <= 0 {
				next.ServeHTTP(w, r)
				return
			}
			ip := clientIP(r)
			var zero int64
			actual, _ := counters.LoadOrStore(ip, &zero)
			cnt := actual.(*int64)
			if atomic.AddInt64(cnt, 1) > int64(requestsPerMinute) {
				respondError(w, tooMany)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.IndexByte(xff, ','); i >= 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// HandleGetDocument implements GET /api/v1/documents/:id.
func (h *SignHandler) HandleGetDocument(w http.ResponseWriter, r *http.Request) {
	tenantID, ok := tenantFromCtx(r)
	if !ok {
		respondError(w, apperr.ErrUnauthorized)
		return
	}

	docID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, apperr.ErrInvalidRequest)
		return
	}

	doc, err := h.queries.GetDocument(r.Context(), repository.GetDocumentParams{
		ID:       docID,
		TenantID: tenantID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			respondError(w, apperr.ErrDocumentNotFound)
			return
		}
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	sigs, err := h.queries.GetSignaturesByDocument(r.Context(), repository.GetSignaturesByDocumentParams{
		DocumentID: docID,
		TenantID:   tenantID,
	})
	if err != nil {
		respondError(w, apperr.ErrInternal.WithCause(err))
		return
	}

	respondJSON(w, http.StatusOK, map[string]any{
		"data": map[string]any{
			"id":              doc.ID,
			"title":           doc.Title.String,
			"status":          doc.Status,
			"current_version": doc.CurrentVersion,
			"s3_key_current":  doc.S3KeyCurrent,
			"signatures":      sigs,
		},
	})
}
