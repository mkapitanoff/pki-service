package storage

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestUploadDownloadRoundtrip(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()
	payload := []byte("%PDF-1.7 fake pdf bytes")

	require.NoError(t, m.UploadFile(ctx, "t/d/v1.pdf", payload, "application/pdf"))

	got, err := m.DownloadFile(ctx, "t/d/v1.pdf")
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

func TestDownloadMissingKeyReturnsErrNotFound(t *testing.T) {
	m := NewMockStorage()
	_, err := m.DownloadFile(context.Background(), "nope/missing.pdf")
	require.ErrorIs(t, err, ErrNotFound)
}

func TestBuildKeyFormat(t *testing.T) {
	m := NewMockStorage()
	tenantID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	documentID := uuid.MustParse("22222222-2222-2222-2222-222222222222")

	key := m.BuildKey(tenantID, documentID, "original.pdf")
	require.Equal(t,
		"11111111-1111-1111-1111-111111111111/22222222-2222-2222-2222-222222222222/original.pdf",
		key)

	// S3Client must produce the identical layout.
	s := &S3Client{bucket: "eds-test"}
	require.Equal(t, key, s.BuildKey(tenantID, documentID, "original.pdf"))
}

func TestMockIsolatesStoredBytes(t *testing.T) {
	m := NewMockStorage()
	ctx := context.Background()
	src := []byte("abc")
	require.NoError(t, m.UploadFile(ctx, "k", src, ""))
	src[0] = 'X' // mutate caller's slice after upload

	got, err := m.DownloadFile(ctx, "k")
	require.NoError(t, err)
	require.Equal(t, []byte("abc"), got)
}
