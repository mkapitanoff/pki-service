package storage

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MockStorage is an in-memory Storage for tests.
type MockStorage struct {
	mu    sync.RWMutex
	files map[string][]byte
}

var _ Storage = (*MockStorage)(nil)

func NewMockStorage() *MockStorage {
	return &MockStorage{files: make(map[string][]byte)}
}

func (m *MockStorage) BuildKey(tenantID, documentID uuid.UUID, filename string) string {
	return fmt.Sprintf("%s/%s/%s", tenantID, documentID, filename)
}

func (m *MockStorage) UploadFile(ctx context.Context, key string, data []byte, contentType string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := make([]byte, len(data))
	copy(cp, data)
	m.files[key] = cp
	return nil
}

func (m *MockStorage) DownloadFile(ctx context.Context, key string) ([]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	data, ok := m.files[key]
	if !ok {
		return nil, ErrNotFound
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	return cp, nil
}

func (m *MockStorage) DeleteFile(ctx context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.files, key)
	return nil
}

func (m *MockStorage) ListObjectKeys(ctx context.Context, prefix string) ([]ObjectInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var results []ObjectInfo
	for k, v := range m.files {
		if strings.HasPrefix(k, prefix) {
			results = append(results, ObjectInfo{
				Key:          k,
				Size:         int64(len(v)),
				LastModified: time.Now(),
			})
		}
	}
	return results, nil
}
