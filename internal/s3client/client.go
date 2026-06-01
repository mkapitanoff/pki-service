package s3client

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// ErrPresignedURLExpired is returned when the server rejects with HTTP 403,
// indicating the pre-signed URL has expired.
var ErrPresignedURLExpired = fmt.Errorf("s3client: pre-signed URL expired (HTTP 403)")

// S3Metadata holds PKI-specific metadata attached to uploaded objects.
type S3Metadata struct {
	ApplicationID   string
	DocumentID      string
	DocumentName    string
	SignerRole      string
	SignedAt        time.Time
	SigningRound    int
	DocumentVersion int
	CMSStorageKey   string
}

// ExternalS3Client interacts with a remote S3 (client-owned) via pre-signed URLs.
type ExternalS3Client interface {
	// DownloadFromPresignedURL downloads the file at url and returns (bytes, contentType, error).
	DownloadFromPresignedURL(ctx context.Context, rawURL string) ([]byte, string, error)
	// UploadToPresignedURL uploads data to the pre-signed PUT URL with PKI metadata headers.
	UploadToPresignedURL(ctx context.Context, rawURL string, data []byte, contentType string, meta S3Metadata) error
}

// HTTPExternalS3Client implements ExternalS3Client via net/http.
type HTTPExternalS3Client struct {
	http *http.Client
}

var _ ExternalS3Client = (*HTTPExternalS3Client)(nil)

// NewHTTPExternalS3Client creates a client with a 60s timeout.
func NewHTTPExternalS3Client() *HTTPExternalS3Client {
	return &HTTPExternalS3Client{
		http: &http.Client{Timeout: 60 * time.Second},
	}
}

// DownloadFromPresignedURL performs a GET and returns the body + Content-Type.
func (c *HTTPExternalS3Client) DownloadFromPresignedURL(ctx context.Context, rawURL string) ([]byte, string, error) {
	safeURL := redactQuery(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", fmt.Errorf("s3client: build GET request for %s: %w", safeURL, err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("s3client: GET %s: %w", safeURL, err)
	}
	defer resp.Body.Close()

	log.Printf("s3client: GET %s → %d", safeURL, resp.StatusCode)

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusForbidden {
			return nil, "", ErrPresignedURLExpired
		}
		return nil, "", fmt.Errorf("s3client: GET %s returned %d: %s", safeURL, resp.StatusCode, string(raw))
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 200<<20)) // 200 MB max
	if err != nil {
		return nil, "", fmt.Errorf("s3client: read body from %s: %w", safeURL, err)
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}
	log.Printf("s3client: downloaded %d bytes (content-type: %s) from %s", len(body), ct, safeURL)
	return body, ct, nil
}

// UploadToPresignedURL performs a PUT with PKI metadata headers.
func (c *HTTPExternalS3Client) UploadToPresignedURL(ctx context.Context, rawURL string, data []byte, contentType string, meta S3Metadata) error {
	safeURL := redactQuery(rawURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("s3client: build PUT request for %s: %w", safeURL, err)
	}

	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	// PKI metadata as S3 user-defined metadata headers.
	req.Header.Set("x-amz-meta-pki-signed", "true")
	req.Header.Set("x-amz-meta-pki-application-id", meta.ApplicationID)
	req.Header.Set("x-amz-meta-pki-document-id", meta.DocumentID)
	req.Header.Set("x-amz-meta-pki-document-name", url.QueryEscape(meta.DocumentName))
	req.Header.Set("x-amz-meta-pki-signer-role", meta.SignerRole)
	req.Header.Set("x-amz-meta-pki-signed-at", meta.SignedAt.UTC().Format(time.RFC3339))
	req.Header.Set("x-amz-meta-pki-signing-round", strconv.Itoa(meta.SigningRound))
	req.Header.Set("x-amz-meta-pki-document-version", strconv.Itoa(meta.DocumentVersion))
	req.Header.Set("x-amz-meta-pki-cms-key", meta.CMSStorageKey)

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("s3client: PUT %s: %w", safeURL, err)
	}
	defer resp.Body.Close()

	log.Printf("s3client: PUT %s (%d bytes) → %d", safeURL, len(data), resp.StatusCode)

	if resp.StatusCode == http.StatusForbidden {
		return ErrPresignedURLExpired
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("s3client: PUT %s returned %d: %s", safeURL, resp.StatusCode, string(raw))
	}
	return nil
}

// redactQuery removes query parameters from the URL for safe logging.
func redactQuery(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "[invalid url]"
	}
	u.RawQuery = ""
	return u.String()
}
