package s3client

import (
	"context"
	"sync"
	"testing"
	"time"
)

// MockExternalS3Client is a thread-safe ExternalS3Client for tests.
type MockExternalS3Client struct {
	mu sync.Mutex

	// DownloadFunc controls DownloadFromPresignedURL. If nil, returns ErrPresignedURLExpired.
	DownloadFunc func(ctx context.Context, rawURL string) ([]byte, string, error)

	uploadErr error
	uploads   []MockUpload
}

// MockUpload records one call to UploadToPresignedURL.
type MockUpload struct {
	URL  string
	Data []byte
	Meta S3Metadata
}

var _ ExternalS3Client = (*MockExternalS3Client)(nil)

func NewMockExternalS3Client() *MockExternalS3Client {
	return &MockExternalS3Client{}
}

// SetUploadError changes the error returned by UploadToPresignedURL (thread-safe).
func (m *MockExternalS3Client) SetUploadError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploadErr = err
}

// GetUploads returns a copy of recorded uploads.
func (m *MockExternalS3Client) GetUploads() []MockUpload {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]MockUpload, len(m.uploads))
	copy(cp, m.uploads)
	return cp
}

// WaitForUploads blocks until at least `want` uploads are recorded or timeout elapses.
func (m *MockExternalS3Client) WaitForUploads(t *testing.T, want int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		n := len(m.uploads)
		m.mu.Unlock()
		if n >= want {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	m.mu.Lock()
	got := len(m.uploads)
	m.mu.Unlock()
	t.Fatalf("timed out waiting for %d upload(s), got %d", want, got)
}

func (m *MockExternalS3Client) DownloadFromPresignedURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	m.mu.Lock()
	fn := m.DownloadFunc
	m.mu.Unlock()
	if fn != nil {
		return fn(ctx, rawURL)
	}
	return nil, "", ErrPresignedURLExpired
}

func (m *MockExternalS3Client) UploadToPresignedURL(ctx context.Context, rawURL string, data []byte, contentType string, meta S3Metadata) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.uploads = append(m.uploads, MockUpload{URL: rawURL, Data: cp, Meta: meta})
	return m.uploadErr
}
