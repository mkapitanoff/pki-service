package s3client

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// MockExternalS3Client — in-memory реализация ExternalS3Client для тестов.
//
// Файл добавлен, потому что sign_session_test.go уже вызывал
// NewMockExternalS3Client, но самого мока в репозитории не было — из-за этого
// пакет internal/handler не собирался под тесты целиком (undefined:
// NewMockExternalS3Client), и ни один тест пакета не запускался.
//
// Поведение по умолчанию: скачивание отдаёт пустой PDF, загрузка принимает всё.
// Точечно переопределяется через DownloadFunc / UploadFunc.
type MockExternalS3Client struct {
	mu sync.RWMutex

	// DownloadFunc, если задан, полностью заменяет скачивание.
	DownloadFunc func(ctx context.Context, rawURL string) ([]byte, string, error)
	// UploadFunc, если задан, полностью заменяет загрузку.
	UploadFunc func(ctx context.Context, rawURL string, data []byte, contentType string, meta S3Metadata) error

	// Uploaded хранит то, что было загружено, для проверок в тестах.
	Uploaded map[string][]byte
}

var _ ExternalS3Client = (*MockExternalS3Client)(nil)

func NewMockExternalS3Client() *MockExternalS3Client {
	return &MockExternalS3Client{Uploaded: make(map[string][]byte)}
}

// WaitForUploads ждёт, пока не будет зафиксировано want загрузок, но не дольше
// timeout. Нужен для потоков, где загрузка идёт в фоновой горутине: без ожидания
// тест проверял бы состояние раньше, чем горутина успела отработать.
func (m *MockExternalS3Client) WaitForUploads(t testing.TB, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		m.mu.RLock()
		got := len(m.Uploaded)
		m.mu.RUnlock()
		if got >= want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("ожидалось загрузок: %d, получено: %d за %s", want, got, timeout)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *MockExternalS3Client) DownloadFromPresignedURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	m.mu.RLock()
	fn := m.DownloadFunc
	m.mu.RUnlock()
	if fn != nil {
		return fn(ctx, rawURL)
	}
	if rawURL == "" {
		return nil, "", fmt.Errorf("mock: empty url")
	}
	// Минимальный валидный PDF-заголовок: достаточно, чтобы поток не падал на
	// определении типа, но тест сам решает, что именно проверять.
	return []byte("%PDF-1.4\n"), "application/pdf", nil
}

func (m *MockExternalS3Client) UploadToPresignedURL(ctx context.Context, rawURL string, data []byte, contentType string, meta S3Metadata) error {
	m.mu.Lock()
	fn := m.UploadFunc
	if fn == nil {
		cp := make([]byte, len(data))
		copy(cp, data)
		m.Uploaded[rawURL] = cp
	}
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, rawURL, data, contentType, meta)
	}
	if rawURL == "" {
		return fmt.Errorf("mock: empty url")
	}
	return nil
}
