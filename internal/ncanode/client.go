package ncanode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OCSP status values surfaced in VerifyResult.OCSPStatus.
const (
	OCSPStatusGood    = "good"
	OCSPStatusRevoked = "revoked"
	OCSPStatusUnknown = "unknown"
)

const signFormatCAdES = "CAdES (CMS, PKCS#7)"

// Sentinel errors returned by the client. Callers (sign service) map these to
// typed AppErrors at the boundary.
var (
	ErrCMSInvalid  = errors.New("ncanode: CMS signature is invalid")
	ErrCertRevoked = errors.New("ncanode: certificate is revoked")
)

// VerifyResult is the normalized result of a CMS verification.
type VerifyResult struct {
	Valid         bool
	SignerIIN     string
	SignerName    string
	SignerBIN     string
	OrgName       string
	SignerType    string // "individual" | "legal_entity_rep"
	Basis         string // "Устав" | "Доверенность" | ""
	CertSerial    string
	CertNotBefore time.Time
	CertNotAfter  time.Time
	CAName        string
	OCSPStatus    string // "good" | "revoked" | "unknown"
	OCSPCheckedAt time.Time
	TSPTime       time.Time
	SignFormat    string
}

// NCANodeClient is the crypto boundary. No PKCS#7 / x509 logic lives outside
// this package.
type NCANodeClient interface {
	VerifyCMS(ctx context.Context, cmsBase64 string, docBase64 string) (*VerifyResult, error)
	GetTSP(ctx context.Context, dataSHA256 string) (time.Time, error)
}

// Options configures the HTTP client. Populated from config.Config by the
// caller; this package does not read config or env directly.
type Options struct {
	URL     string
	Timeout time.Duration
}

// HTTPClient talks to a NCANode 3.x REST sidecar.
type HTTPClient struct {
	baseURL string
	timeout time.Duration
	http    *http.Client
}

var _ NCANodeClient = (*HTTPClient)(nil)

func NewHTTPClient(opts Options) *HTTPClient {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &HTTPClient{
		baseURL: strings.TrimRight(opts.URL, "/"),
		timeout: timeout,
		http:    &http.Client{Timeout: timeout},
	}
}

// --- wire types (NCANode 3.x JSON shapes) ---

type cmsVerifyRequest struct {
	CMS  string `json:"cms"`
	Data string `json:"data"`
}

type ncaSubject struct {
	CommonName   string `json:"commonName"`
	IIN          string `json:"iin"`
	BIN          string `json:"bin"`
	Organization string `json:"organization"`
}

type ncaIssuer struct {
	CommonName string `json:"commonName"`
}

type ncaOCSP struct {
	Status      string    `json:"status"`
	RevokedAt   time.Time `json:"revokedAt"`
	CheckedTime time.Time `json:"genTime"`
}

type ncaTSP struct {
	GenTime time.Time `json:"genTime"`
}

type ncaCertificate struct {
	Valid        bool       `json:"valid"`
	Subject      ncaSubject `json:"subject"`
	Issuer       ncaIssuer  `json:"issuer"`
	SerialNumber string     `json:"serialNumber"`
	NotBefore    time.Time  `json:"notBefore"`
	NotAfter     time.Time  `json:"notAfter"`
	KeyUsage     string     `json:"keyUsage"`
	OCSP         *ncaOCSP   `json:"ocsp"`
}

type ncaSigner struct {
	Certificates []ncaCertificate `json:"certificates"`
	TSP          *ncaTSP          `json:"tsp"`
}

type cmsVerifyResponse struct {
	Status  int         `json:"status"`
	Message string      `json:"message"`
	Valid   bool        `json:"valid"`
	Signers []ncaSigner `json:"signers"`
}

// VerifyCMS posts the CMS + full document base64 to {url}/cms/verify and
// normalizes the response. Returns ErrCMSInvalid / ErrCertRevoked for business
// failures. TSP time is read from signer.tsp.genTime in the response.
func (c *HTTPClient) VerifyCMS(ctx context.Context, cmsBase64 string, docBase64 string) (*VerifyResult, error) {
	var resp cmsVerifyResponse
	if err := c.postJSON(ctx, "/cms/verify", cmsVerifyRequest{CMS: cmsBase64, Data: docBase64}, &resp); err != nil {
		return nil, err
	}

	if !resp.Valid || len(resp.Signers) == 0 {
		return nil, ErrCMSInvalid
	}

	signer := resp.Signers[0]
	if len(signer.Certificates) == 0 {
		return nil, ErrCMSInvalid
	}
	cert := signer.Certificates[0]
	if !cert.Valid {
		return nil, ErrCMSInvalid
	}

	ocspStatus := normalizeOCSP(cert.OCSP)
	if ocspStatus == OCSPStatusRevoked {
		return nil, ErrCertRevoked
	}

	ocspCheckedAt := time.Now().UTC()
	if cert.OCSP != nil && !cert.OCSP.CheckedTime.IsZero() {
		ocspCheckedAt = cert.OCSP.CheckedTime
	}

	var tspTime time.Time
	if signer.TSP != nil {
		tspTime = signer.TSP.GenTime
	}

	signerType := "individual"
	if cert.Subject.BIN != "" || cert.Subject.Organization != "" {
		signerType = "legal_entity_rep"
	}

	return &VerifyResult{
		Valid:         true,
		SignerIIN:     cert.Subject.IIN,
		SignerName:    cert.Subject.CommonName,
		SignerBIN:     cert.Subject.BIN,
		OrgName:       cert.Subject.Organization,
		SignerType:    signerType,
		Basis:         "",
		CertSerial:    cert.SerialNumber,
		CertNotBefore: cert.NotBefore,
		CertNotAfter:  cert.NotAfter,
		CAName:        cert.Issuer.CommonName,
		OCSPStatus:    ocspStatus,
		OCSPCheckedAt: ocspCheckedAt,
		TSPTime:       tspTime,
		SignFormat:    signFormatCAdES,
	}, nil
}

// GetTSP is a no-op in NCANode v3: TSP time is returned as part of the
// VerifyCMS response (signer.tsp.genTime). Callers should use
// VerifyResult.TSPTime directly.
func (c *HTTPClient) GetTSP(_ context.Context, _ string) (time.Time, error) {
	return time.Now().UTC(), nil
}

func normalizeOCSP(o *ncaOCSP) string {
	if o == nil {
		return OCSPStatusUnknown
	}
	switch strings.ToLower(strings.TrimSpace(o.Status)) {
	case "good", "active":
		return OCSPStatusGood
	case "revoked":
		return OCSPStatusRevoked
	default:
		return OCSPStatusUnknown
	}
}

func (c *HTTPClient) postJSON(ctx context.Context, path string, body any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("ncanode: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("ncanode: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("ncanode: request to %s failed: %w", path, err)
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("ncanode: read response: %w", err)
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("ncanode: %s returned HTTP %d: %s", path, res.StatusCode, strings.TrimSpace(string(raw)))
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("ncanode: decode response: %w", err)
	}
	return nil
}
