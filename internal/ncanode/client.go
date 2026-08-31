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
	"unicode"
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
	ErrCMSInvalid       = errors.New("ncanode: CMS signature is invalid")
	ErrCertRevoked      = errors.New("ncanode: certificate is revoked")
	ErrCertInvalidUsage = errors.New("ncanode: certificate key usage does not permit signing")
	// ErrCertStatusUnknown — отозванность сертификата не подтверждена (OCSP
	// unknown). Возвращается вызывающими, которым нужен fail-closed режим.
	ErrCertStatusUnknown = errors.New("ncanode: certificate revocation status could not be determined (OCSP unknown)")
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
	VerifyCMS(ctx context.Context, cmsBase64 string, docSHA256 string) (*VerifyResult, error)
	// VerifyCMSWithRevocation дополнительно требует проверки отзыва (для входа по ЭЦП).
	VerifyCMSWithRevocation(ctx context.Context, cmsBase64 string, data string) (*VerifyResult, error)
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
	// RevocationCheck просит NCANode реально сходить за статусом отзыва.
	// БЕЗ него revocations в ответе пустой и отзыв НЕ проверяется вовсе
	// (проверено на живом NCANode 3.x 2026-07-28).
	// omitempty обязателен: signing-поток шлёт запрос без этого поля, как и раньше.
	RevocationCheck []string `json:"revocationCheck,omitempty"`
}

type tspCreateRequest struct {
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
	// NCANode 3.x кладёт результат проверки отзыва сюда, а не в OCSP:
	// [{"revoked":false,"by":"CRL"},{"revoked":false,"by":"OCSP"}].
	// Поле ocsp у этой версии всегда null.
	Revocations []ncaRevocation `json:"revocations"`
}

type ncaRevocation struct {
	Revoked bool   `json:"revoked"`
	By      string `json:"by"`
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

type tspCreateResponse struct {
	Status  int     `json:"status"`
	Message string  `json:"message"`
	TSP     *ncaTSP `json:"tsp"`
}

// normalizeCMS converts a CMS string to standard Base64 expected by NCANode:
//   - strips PEM headers/footers (-----BEGIN/END CMS-----)
//   - converts Base64URL characters: '-' → '+', '_' → '/'
//   - removes all whitespace (newlines, spaces)
func normalizeCMS(cms string) string {
	s := cms
	// Strip PEM wrapper if present.
	if idx := strings.Index(s, "-----BEGIN"); idx != -1 {
		// Extract only the Base64 body between the header and footer lines.
		lines := strings.Split(s, "\n")
		var body []string
		inBody := false
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "-----BEGIN") {
				inBody = true
				continue
			}
			if strings.HasPrefix(line, "-----END") {
				break
			}
			if inBody {
				body = append(body, line)
			}
		}
		s = strings.Join(body, "")
	}
	// Remove all whitespace.
	s = strings.Join(strings.Fields(s), "")
	// Convert Base64URL → standard Base64.
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	return s
}

// VerifyCMS posts the CMS + document hash to {url}/cms/verify and normalizes
// the response. Returns ErrCMSInvalid / ErrCertRevoked for business failures.
//
// Supports both detached and attached (encapsulated) CMS:
//   - Detached: docSHA256 is the hash of the signed document (NCALayer encapsulate:false).
//   - Attached: the PDF is embedded inside the CMS blob (NCALayer encapsulate:true).
//     NCANode detects this automatically when data="" and verifies the embedded content.
//
// Strategy: try with docSHA256 first; if NCANode rejects with "content hash found in
// signed attributes different", the CMS is attached — retry with empty data.
// VerifyCMSWithRevocation — как VerifyCMS, но дополнительно требует от NCANode
// фактической проверки отзыва (CRL+OCSP). Нужен для ВХОДА по ЭЦП: там отзыв
// сертификата обязан блокировать аутентификацию. Signing-поток намеренно
// продолжает использовать VerifyCMS без изменений.
func (c *HTTPClient) VerifyCMSWithRevocation(ctx context.Context, cmsBase64 string, data string) (*VerifyResult, error) {
	return c.verifyCMS(ctx, cmsBase64, data, []string{"CRL", "OCSP"})
}

func (c *HTTPClient) VerifyCMS(ctx context.Context, cmsBase64 string, docSHA256 string) (*VerifyResult, error) {
	return c.verifyCMS(ctx, cmsBase64, docSHA256, nil)
}

func (c *HTTPClient) verifyCMS(ctx context.Context, cmsBase64 string, docSHA256 string, revocationCheck []string) (*VerifyResult, error) {
	normalized := normalizeCMS(cmsBase64)

	var resp cmsVerifyResponse
	err := c.postJSON(ctx, "/cms/verify", cmsVerifyRequest{CMS: normalized, Data: docSHA256, RevocationCheck: revocationCheck}, &resp)
	if err != nil {
		// Attached CMS: the signed attributes contain the hash of the embedded content,
		// not of our stored document. Retry with empty data so NCANode verifies internally.
		if strings.Contains(err.Error(), "content hash found in signed attributes different") {
			var resp2 cmsVerifyResponse
			if err2 := c.postJSON(ctx, "/cms/verify", cmsVerifyRequest{CMS: normalized, Data: "", RevocationCheck: revocationCheck}, &resp2); err2 != nil {
				return nil, err2
			}
			resp = resp2
		} else {
			return nil, err
		}
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

	// п.6.3 Правил проверки подлинности ЭЦП (приказ №1187): сертификат для
	// подписи обязан иметь nonRepudiation (Неотрекаемость). Сертификат
	// аутентификации (digitalSignature+keyEncipherment) для ЭЦП недопустим.
	if !keyUsagePermitsSigning(cert.KeyUsage) {
		return nil, ErrCertInvalidUsage
	}

	ocspStatus := revocationStatus(cert.Revocations, cert.OCSP)
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

// GetTSP posts the data hash to {url}/tsp/create and returns the timestamp.
func (c *HTTPClient) GetTSP(ctx context.Context, dataSHA256 string) (time.Time, error) {
	var resp tspCreateResponse
	if err := c.postJSON(ctx, "/tsp/create", tspCreateRequest{Data: dataSHA256}, &resp); err != nil {
		return time.Time{}, err
	}
	if resp.TSP == nil || resp.TSP.GenTime.IsZero() {
		return time.Time{}, fmt.Errorf("ncanode: tsp response missing genTime")
	}
	return resp.TSP.GenTime, nil
}

// revocationStatus выводит статус из revocations[]; при пустом списке —
// падает обратно на поле ocsp (сохраняет прежнее поведение signing-потока).
func revocationStatus(revs []ncaRevocation, o *ncaOCSP) string {
	if len(revs) == 0 {
		return normalizeOCSP(o)
	}
	for _, r := range revs {
		if r.Revoked {
			return OCSPStatusRevoked
		}
	}
	return OCSPStatusGood
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

// keyUsagePermitsSigning проверяет, что KeyUsage сертификата допускает ЭЦП.
// NCANode 3.x возвращает упрощённый enum ("SIGN" / "AUTH"), а не X.509-строку
// расширения KeyUsage — сертификат аутентификации ("AUTH") для ЭЦП недопустим
// (п.6.3 Правил проверки подлинности ЭЦП). На случай, если конфигурация
// NCANode вернёт классические X.509-имена (nonRepudiation/contentCommitment),
// они тоже не содержат "auth" и будут допущены. Пустой KeyUsage (поле не
// вернулось) не блокируем — определить нельзя.
func keyUsagePermitsSigning(ku string) bool {
	n := normalizeKeyUsage(ku)
	if n == "" {
		return true
	}
	return !strings.Contains(n, "auth")
}

// normalizeKeyUsage приводит строку KeyUsage к нижнему регистру и оставляет
// только буквы/цифры — формат от NCANode может отличаться разделителями
// (пробелы, запятые, скобки).
func normalizeKeyUsage(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
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
