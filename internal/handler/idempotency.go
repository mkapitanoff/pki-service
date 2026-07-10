package handler

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"github.com/google/uuid"

	apperr "github.com/mkapitanoff/pki-service/internal/errors"
	"github.com/mkapitanoff/pki-service/internal/reqctx"
	"github.com/mkapitanoff/pki-service/internal/repository"
)

// IdempotencyHeader — стандартный header для каноничного дедуп-ключа.
const IdempotencyHeader = "Idempotency-Key"

// idempotentResult описывает кэшированный ответ.
type idempotentResult struct {
	StatusCode int
	Body       []byte
}

// ReadAndRestoreBody читает тело запроса целиком и восстанавливает r.Body,
// чтобы последующий json.Decode в handler'е отработал как обычно.
//
// Нужен для фингерпринта тела: механизм намеренно работает по СЫРЫМ байтам,
// без завязки на структуру конкретного эндпоинта — поэтому применим к любому
// будущему идемпотентному роуту, а не только к /sign/initiate.
func ReadAndRestoreBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

// HashRequestBody возвращает SHA-256 (hex) сырого тела запроса.
func HashRequestBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// CheckIdempotency читает Idempotency-Key из запроса и, если ключ есть,
// ищет ранее сохранённый ответ для (tenant, key, method, path).
//
// requestHash — SHA-256 сырого тела текущего запроса (см. HashRequestBody).
// Сверка фингерпринта отличает честный ретрай от переиспользования ключа:
//
//	request_hash IS NULL     → legacy-строка (до 000013), реплеим как раньше;
//	request_hash == текущему → реплей: это тот же самый запрос;
//	request_hash != текущему → conflict=true, caller обязан вернуть 409.
//
// Без этой сверки клиент, собравший ключ из нестабильного базиса, молча
// получал бы устаревший ответ прошлой операции (чужие doc_id и хэши).
//
// Возвращает (key, cached, conflict). Если cached != nil — caller обязан
// вернуть тот же status_code и body и НЕ выполнять основной flow. Если
// cached == nil и key != "" — после успешной обработки вызвать StoreIdempotency.
//
// Пустой ключ → ничего не делает, key == "" возвращается (клиенты, не
// присылающие заголовок, не затрагиваются вообще).
func CheckIdempotency(
	ctx context.Context,
	q *repository.Queries,
	r *http.Request,
	tenantID uuid.UUID,
	requestHash string,
) (key string, cached *idempotentResult, conflict bool, err error) {
	key = strings.TrimSpace(r.Header.Get(IdempotencyHeader))
	if key == "" {
		return "", nil, false, nil
	}
	if len(key) > 255 {
		// Защита от злоупотреблений; ключ слишком длинный — игнорируем.
		return "", nil, false, nil
	}
	row, qerr := q.GetIdempotencyKey(ctx, repository.GetIdempotencyKeyParams{
		TenantID: tenantID,
		IdemKey:  key,
		Method:   r.Method,
		Path:     r.URL.Path,
	})
	if qerr != nil {
		// sql.ErrNoRows вернётся как зашитая ошибка → cached остаётся nil.
		// Любая другая ошибка (timeout, syntax) — пробрасываем.
		if isNoRows(qerr) {
			return key, nil, false, nil
		}
		return key, nil, false, qerr
	}
	if row.RequestHash.Valid && requestHash != "" && row.RequestHash.String != requestHash {
		return key, nil, true, nil
	}
	return key, &idempotentResult{
		StatusCode: int(row.StatusCode),
		Body:       []byte(row.ResponseBody),
	}, false, nil
}

// StoreIdempotency сохраняет результат запроса (response body, status code).
// Вызывается ПОСЛЕ того как handler сформировал response, но ДО того как он
// его отправил клиенту (чтобы можно было «увидеть» ответ). Альтернатива —
// перехватить через httptest.ResponseRecorder; здесь же caller передаёт
// body как []byte явно.
func StoreIdempotency(
	ctx context.Context,
	q *repository.Queries,
	r *http.Request,
	tenantID uuid.UUID,
	key string,
	statusCode int,
	body []byte,
	sessionID uuid.NullUUID,
	requestHash string,
) {
	if key == "" {
		return
	}
	if err := q.PutIdempotencyKey(ctx, repository.PutIdempotencyKeyParams{
		TenantID:     tenantID,
		IdemKey:      key,
		Method:       r.Method,
		Path:         r.URL.Path,
		StatusCode:   int32(statusCode),
		ResponseBody: json.RawMessage(body),
		SessionID:    sessionID,
		RequestHash:  sql.NullString{String: requestHash, Valid: requestHash != ""},
	}); err != nil {
		log.Printf("idempotency.put request_id=%s tenant_id=%s key=%s err=%v",
			reqctx.RequestID(ctx), tenantID, key, err)
	}
}

// respondCached отправляет закэшированный ответ. Прозрачно — клиент не
// отличит от свежей обработки, только по X-Idempotent-Replay header'у.
func respondCached(w http.ResponseWriter, r *http.Request, cached *idempotentResult) {
	if rid := reqctx.RequestID(r.Context()); rid != "" {
		w.Header().Set(RequestIDHeader, rid)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Idempotent-Replay", "true")
	w.WriteHeader(cached.StatusCode)
	_, _ = w.Write(cached.Body)
}

// jsonBytes сериализует body в JSON для одновременной записи в response
// и сохранения в idempotency_keys. nil возвращает [].
func jsonBytes(body any) []byte {
	if body == nil {
		return []byte("null")
	}
	b, err := json.Marshal(body)
	if err != nil {
		return []byte("null")
	}
	return b
}

// isNoRows — узкая проверка, чтобы не таскать database/sql в каждый файл.
// Сравниваем строку — все error-types из стандартного драйвера дают
// одинаковое сообщение.
func isNoRows(err error) bool {
	if err == nil {
		return false
	}
	// Проверяем сначала через apperr (которая может оборачивать).
	if ae := apperr.As(err); ae != nil && ae.Code == apperr.ErrSessionNotFound.Code {
		return true
	}
	return err.Error() == "sql: no rows in result set"
}
