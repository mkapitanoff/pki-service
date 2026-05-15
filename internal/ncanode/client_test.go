package ncanode

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func newTestClient(handler http.HandlerFunc) (*HTTPClient, *httptest.Server) {
	srv := httptest.NewServer(handler)
	c := NewHTTPClient(Options{URL: srv.URL, Timeout: 5 * time.Second})
	return c, srv
}

func TestVerifyCMS_Success(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/cms/verify", r.URL.Path)

		var req cmsVerifyRequest
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		require.Equal(t, "CMSBASE64", req.CMS)
		require.Equal(t, "deadbeef", req.Data)

		resp := cmsVerifyResponse{
			Status: 0,
			Valid:  true,
			Signers: []ncaSigner{{
				Certificates: []ncaCertificate{{
					Valid:        true,
					SerialNumber: "2F5A91",
					NotBefore:    time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC),
					NotAfter:     time.Date(2027, 1, 8, 0, 0, 0, 0, time.UTC),
					Subject: ncaSubject{
						CommonName:   "БАХЫТЖАНОВА ТОЖАН",
						IIN:          "890400001782",
						BIN:          "230240030302",
						Organization: "ТОО МеталлОптТорг KZ",
					},
					Issuer: ncaIssuer{CommonName: "ҰЛТТЫҚ КУӘЛАНДЫРУШЫ ОРТАЛЫҚ"},
					OCSP:   &ncaOCSP{Status: "GOOD", CheckedTime: time.Date(2026, 5, 15, 10, 0, 0, 0, time.UTC)},
				}},
				TSP: &ncaTSP{GenTime: time.Date(2026, 5, 15, 10, 0, 1, 0, time.UTC)},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(resp))
	})
	defer srv.Close()

	res, err := c.VerifyCMS(context.Background(), "CMSBASE64", "deadbeef")
	require.NoError(t, err)
	require.True(t, res.Valid)
	require.Equal(t, "890400001782", res.SignerIIN)
	require.Equal(t, "БАХЫТЖАНОВА ТОЖАН", res.SignerName)
	require.Equal(t, "230240030302", res.SignerBIN)
	require.Equal(t, "legal_entity_rep", res.SignerType)
	require.Equal(t, "2F5A91", res.CertSerial)
	require.Equal(t, OCSPStatusGood, res.OCSPStatus)
	require.Equal(t, signFormatCAdES, res.SignFormat)
	require.Equal(t, time.Date(2026, 5, 15, 10, 0, 1, 0, time.UTC), res.TSPTime)
}

func TestVerifyCMS_RevokedReturnsError(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		resp := cmsVerifyResponse{
			Valid: true,
			Signers: []ncaSigner{{
				Certificates: []ncaCertificate{{
					Valid:        true,
					SerialNumber: "ABC",
					Subject:      ncaSubject{CommonName: "X", IIN: "000000000000"},
					Issuer:       ncaIssuer{CommonName: "CA"},
					OCSP:         &ncaOCSP{Status: "REVOKED"},
				}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer srv.Close()

	res, err := c.VerifyCMS(context.Background(), "CMS", "hash")
	require.Nil(t, res)
	require.ErrorIs(t, err, ErrCertRevoked)
}

func TestVerifyCMS_InvalidReturnsError(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		resp := cmsVerifyResponse{Valid: false, Message: "signature does not match"}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer srv.Close()

	res, err := c.VerifyCMS(context.Background(), "CMS", "hash")
	require.Nil(t, res)
	require.ErrorIs(t, err, ErrCMSInvalid)
}

func TestVerifyCMS_InvalidCertFlag(t *testing.T) {
	c, srv := newTestClient(func(w http.ResponseWriter, r *http.Request) {
		resp := cmsVerifyResponse{
			Valid: true,
			Signers: []ncaSigner{{
				Certificates: []ncaCertificate{{Valid: false, Subject: ncaSubject{CommonName: "X"}}},
			}},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	defer srv.Close()

	_, err := c.VerifyCMS(context.Background(), "CMS", "hash")
	require.ErrorIs(t, err, ErrCMSInvalid)
}

// GetTSP is a no-op in NCANode v3 — TSP time comes from VerifyCMS response.
func TestGetTSP_NoOp(t *testing.T) {
	c := NewHTTPClient(Options{URL: "http://localhost:0", Timeout: 5 * time.Second})
	got, err := c.GetTSP(context.Background(), "abc123")
	require.NoError(t, err)
	require.False(t, got.IsZero())
}

func TestMockClient(t *testing.T) {
	m := NewMockNCANodeClient()

	res, err := m.VerifyCMS(context.Background(), "anything", "hash")
	require.NoError(t, err)
	require.Equal(t, "ТЕСТОВ ТЕСТ ТЕСТОВИЧ", res.SignerName)
	require.Equal(t, "000000000000", res.SignerIIN)
	require.Equal(t, OCSPStatusGood, res.OCSPStatus)

	m.RegisterRevoked("badcms")
	_, err = m.VerifyCMS(context.Background(), "badcms", "hash")
	require.ErrorIs(t, err, ErrCertRevoked)

	m.RegisterInvalid("junk")
	_, err = m.VerifyCMS(context.Background(), "junk", "hash")
	require.ErrorIs(t, err, ErrCMSInvalid)

	ts, err := m.GetTSP(context.Background(), "hash")
	require.NoError(t, err)
	require.False(t, ts.IsZero())
}

// guard: sentinel errors are distinct
func TestSentinelsDistinct(t *testing.T) {
	require.False(t, errors.Is(ErrCMSInvalid, ErrCertRevoked))
}
