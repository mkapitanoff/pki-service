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
	"github.com/mkapitanoff/pki-service/internal/reqctx"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// tenantRecordKey holds the full authenticated repository.Tenant. The
// uuid-only TenantIDKey (see sign.go) is also set so existing handlers that
// read tenantFromCtx keep working.
const tenantRecordKey ctxKey = "tenant_record"

// RequestIDHeader is the HTTP header used for inbound/outbound correlation.
const RequestIDHeader = "X-Request-Id"

// RequestIDMiddleware accepts an inbound X-Request-Id or generates one,
// stores it in the request context (см. reqctx), mirrors it back in the
// response header, чтобы Lovable Edge мог сопоставлять логи между системами.
func RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := strings.TrimSpace(r.Header.Get(RequestIDHeader))
		if rid == "" {
			rid = uuid.NewString()
		}
		w.Header().Set(RequestIDHeader, rid)
		next.ServeHTTP(w, r.WithContext(reqctx.WithRequestID(r.Context(), rid)))
	})
}

// RequestIDFromCtx returns the request ID set by RequestIDMiddleware, or "".
func RequestIDFromCtx(ctx context.Context) string {
	return reqctx.RequestID(ctx)
}

// JSONRecover catches panics inside handlers and returns a structured JSON
// 500 instead of chi's default plain-text. Includes request_id for the caller.
func JSONRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				rid := RequestIDFromCtx(r.Context())
				// Log the panic with stack-equivalent context (request_id, path, method).
				logPanic(rid, r.Method, r.URL.Path, rec)
				// Best-effort: if headers already written, can't recover the body.
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"INTERNAL","message":"Internal server error","request_id":"` + rid + `"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func logPanic(rid, method, path string, rec any) {
	// Use stdlib log via fmt; structured slog migration tracked in 1.6.
	// nolint: forbidigo
	println("PANIC request_id=" + rid + " " + method + " " + path + ": " + sprintAny(rec))
}

func sprintAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return "<panic value not stringifiable>"
}

// MaxBytes wraps the handler and enforces a per-request body size limit.
// Превышение приведёт к ошибке при попытке прочитать тело — handler сам
// вернёт 413/400; чтобы быть гарантированным, decoder-у нужен MaxBytesReader
// для отдачи правильного http.MaxBytesError.
func MaxBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Body = http.MaxBytesReader(w, r.Body, limit)
			next.ServeHTTP(w, r)
		})
	}
}

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

// DualAuth routes to jwtMw when the token looks like a JWT (two dots in the
// base64 parts), otherwise falls through to apiKeyMw.
func DualAuth(jwtMw, apiKeyMw func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		jwtH := jwtMw(next)
		apiH := apiKeyMw(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authz := r.Header.Get("Authorization")
			token := strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
			if strings.Count(token, ".") == 2 {
				jwtH.ServeHTTP(w, r)
			} else {
				apiH.ServeHTTP(w, r)
			}
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
