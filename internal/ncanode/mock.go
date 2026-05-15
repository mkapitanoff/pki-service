package ncanode

import (
	"context"
	"sync"
	"time"
)

// MockNCANodeClient is an in-memory NCANodeClient for tests. By default every
// CMS verifies successfully as the fixed test signer. Use RegisterRevoked /
// RegisterInvalid to override behavior for specific CMS payloads.
type MockNCANodeClient struct {
	mu      sync.RWMutex
	revoked map[string]bool
	invalid map[string]bool
}

var _ NCANodeClient = (*MockNCANodeClient)(nil)

func NewMockNCANodeClient() *MockNCANodeClient {
	return &MockNCANodeClient{
		revoked: make(map[string]bool),
		invalid: make(map[string]bool),
	}
}

// RegisterRevoked makes VerifyCMS return ErrCertRevoked for this CMS.
func (m *MockNCANodeClient) RegisterRevoked(cms string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revoked[cms] = true
}

// RegisterInvalid makes VerifyCMS return ErrCMSInvalid for this CMS.
func (m *MockNCANodeClient) RegisterInvalid(cms string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.invalid[cms] = true
}

func (m *MockNCANodeClient) VerifyCMS(ctx context.Context, cmsBase64 string, docSHA256 string) (*VerifyResult, error) {
	m.mu.RLock()
	invalid := m.invalid[cmsBase64]
	revoked := m.revoked[cmsBase64]
	m.mu.RUnlock()

	if invalid {
		return nil, ErrCMSInvalid
	}
	if revoked {
		return nil, ErrCertRevoked
	}

	now := time.Now().UTC()
	return &VerifyResult{
		Valid:         true,
		SignerIIN:     "000000000000",
		SignerName:    "ТЕСТОВ ТЕСТ ТЕСТОВИЧ",
		SignerBIN:     "",
		OrgName:       "",
		SignerType:    "individual",
		Basis:         "",
		CertSerial:    "2F5A0000000000000000000000000000000091",
		CertNotBefore: time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC),
		CertNotAfter:  time.Date(2027, 1, 8, 0, 0, 0, 0, time.UTC),
		CAName:        "ҰЛТТЫҚ КУӘЛАНДЫРУШЫ ОРТАЛЫҚ (RSA)",
		OCSPStatus:    OCSPStatusGood,
		OCSPCheckedAt: now,
		TSPTime:       now,
		SignFormat:    signFormatCAdES,
	}, nil
}

func (m *MockNCANodeClient) GetTSP(ctx context.Context, dataSHA256 string) (time.Time, error) {
	return time.Now().UTC(), nil
}
