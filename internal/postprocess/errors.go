package postprocess

import (
	"errors"
	"time"

	"github.com/mkapitanoff/pki-service/internal/s3client"
)

// backoff — тот же график, что и internal/webhook.backoff (60с/300с/1800с),
// продублирован здесь: webhook.backoff неэкспортирован, а тянуть постпроцессинг
// в зависимость от пакета webhook ради одной чистой функции того не стоит.
func backoff(attempt int32) time.Duration {
	switch attempt {
	case 1:
		return 60 * time.Second
	case 2:
		return 300 * time.Second
	default:
		return 1800 * time.Second
	}
}

// classifyErrorCode возвращает машинно-читаемый код для postprocess_error_code.
// TARGET_URL_EXPIRED — особый случай: повторная попытка тем же presigned URL
// заведомо не поможет, восстановление — через PATCH /sign/refresh-urls.
func classifyErrorCode(err error) string {
	switch {
	case errors.Is(err, s3client.ErrPresignedURLExpired):
		return "TARGET_URL_EXPIRED"
	case errors.Is(err, s3client.ErrPresignedURLForbidden):
		// НЕ истечение (обычно SignatureDoesNotMatch): refresh-urls не поможет,
		// проблема в контракте presigned URL на стороне клиента.
		return "TARGET_URL_REJECTED"
	default:
		return "POST_PROCESSING_FAILED"
	}
}
