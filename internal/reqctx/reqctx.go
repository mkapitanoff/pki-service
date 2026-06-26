// Package reqctx — общий per-request context для request_id и других
// корреляционных полей. Используется и handler-ами, и signer/worker'ами,
// поэтому вынесен из internal/handler, чтобы избежать import cycle.
package reqctx

import "context"

type ctxKey string

const requestIDKey ctxKey = "request_id"

// WithRequestID кладёт идентификатор запроса в контекст.
func WithRequestID(ctx context.Context, id string) context.Context {
	if id == "" {
		return ctx
	}
	return context.WithValue(ctx, requestIDKey, id)
}

// RequestID возвращает идентификатор запроса или "" если он не выставлен.
func RequestID(ctx context.Context) string {
	if v, ok := ctx.Value(requestIDKey).(string); ok {
		return v
	}
	return ""
}
