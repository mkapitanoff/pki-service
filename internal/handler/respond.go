package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"log"
	"net/http"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
)

func toNullString(s string) sql.NullString {
	return sql.NullString{String: s, Valid: s != ""}
}

// respondJSON отправляет JSON-ответ. Сохранена для обратной совместимости —
// callers без access к *http.Request. Новые места должны использовать
// respondJSONReq, чтобы автоматически прокинуть X-Request-Id и/или иные
// контекстные поля в логи.
func respondJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// respondJSONReq — тот же respondJSON, но прокидывает X-Request-Id в response
// header (если он ещё не выставлен — например при прямом вызове без middleware).
func respondJSONReq(w http.ResponseWriter, r *http.Request, status int, body any) {
	if w.Header().Get(RequestIDHeader) == "" {
		if rid := RequestIDFromCtx(r.Context()); rid != "" {
			w.Header().Set(RequestIDHeader, rid)
		}
	}
	respondJSON(w, status, body)
}

// respondError — обратно-совместимая обёртка без request context. Использует
// единый envelope {error:{code,message,details?,request_id?}}.
func respondError(w http.ResponseWriter, err error) {
	respondErrorCtx(w, context.Background(), err)
}

// respondErrorReq — основная функция: знает контекст запроса, читает оттуда
// X-Request-Id, кладёт его в тело и заголовок.
func respondErrorReq(w http.ResponseWriter, r *http.Request, err error) {
	respondErrorCtx(w, r.Context(), err)
}

func respondErrorCtx(w http.ResponseWriter, ctx context.Context, err error) {
	rid := RequestIDFromCtx(ctx)

	// Распознаём http.MaxBytesError → 413 PAYLOAD_TOO_LARGE.
	var mbe *http.MaxBytesError
	if stderrors.As(err, &mbe) {
		err = apperr.ErrPayloadTooLarge.WithDetails(map[string]any{"limit_bytes": mbe.Limit})
	}

	ae := apperr.As(err)
	if ae == nil {
		ae = apperr.ErrInternal
	}

	// Логируем причину один раз. PRINTLN заменим slog'ом в шаге 1.6.
	if ae.Status >= 500 {
		log.Printf("respond_error request_id=%s code=%s status=%d cause=%v",
			rid, ae.Code, ae.Status, ae.Err)
	}

	errBody := map[string]any{
		"code":    ae.Code,
		"message": ae.Message,
	}
	if ae.Details != nil {
		errBody["details"] = ae.Details
	}
	if rid != "" {
		errBody["request_id"] = rid
		if w.Header().Get(RequestIDHeader) == "" {
			w.Header().Set(RequestIDHeader, rid)
		}
	}
	respondJSON(w, ae.Status, map[string]any{"error": errBody})
}

