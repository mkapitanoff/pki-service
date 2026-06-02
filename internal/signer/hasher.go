package signer

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"fmt"
)

// ComputeSHA256Base64 returns the base64-encoded SHA-256 hash of data.
// Used in /initiate responses so that the client can pass it to NCALayer.
func ComputeSHA256Base64(data []byte) string {
	h := sha256.Sum256(data)
	return base64.StdEncoding.EncodeToString(h[:])
}

// ComputeSHA256Hex returns the hex-encoded SHA-256 hash of data.
// Used for content_hash storage in the DB.
func ComputeSHA256Hex(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

// --- ASN.1 types for PKCS#7 SignedData ---

// OID for messageDigest attribute (1.2.840.113549.1.9.4)
var oidMessageDigest = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 4}

// pkcs7ContentInfo is the outer wrapper: SEQUENCE { OID, [0] ANY }.
type pkcs7ContentInfo struct {
	ContentType asn1.ObjectIdentifier
	Content     asn1.RawValue `asn1:"explicit,tag:0,optional"`
}

// signedData is the top-level SignedData structure.
type signedData struct {
	Version          int
	DigestAlgorithms asn1.RawValue // SET OF AlgorithmIdentifier
	ContentInfo      asn1.RawValue // EncapsulatedContentInfo
	Certificates     asn1.RawValue `asn1:"optional,tag:0"`
	CRLs             asn1.RawValue `asn1:"optional,tag:1"`
	SignerInfos       asn1.RawValue // SET OF SignerInfo (raw, we parse it manually)
}

// signerInfo holds the per-signer data.
type signerInfo struct {
	Version            int
	SID                asn1.RawValue
	DigestAlgorithm    asn1.RawValue
	AuthAttributes     asn1.RawValue `asn1:"optional,tag:0"`
	DigestEncAlgorithm asn1.RawValue
	EncryptedDigest    []byte
	UnauthAttributes   asn1.RawValue `asn1:"optional,tag:1"`
}

// attribute is a single authenticated/unauthenticated attribute.
type attribute struct {
	Type   asn1.ObjectIdentifier
	Values asn1.RawValue // SET OF ANY
}

// ExtractHashFromCMS extracts the messageDigest value from the first signer's
// authenticated attributes in a CMS (PKCS#7) SignedData blob.
//
// The expected DER path is:
//
//	ContentInfo → SignedData → signerInfos[0] → authenticatedAttributes → messageDigest
func ExtractHashFromCMS(cmsBytes []byte) ([]byte, error) {
	// Step 1: unwrap ContentInfo.
	var ci pkcs7ContentInfo
	rest, err := asn1.Unmarshal(cmsBytes, &ci)
	if err != nil {
		return nil, fmt.Errorf("cms: unmarshal ContentInfo: %w", err)
	}
	if len(rest) > 0 {
		// Some implementations append trailing bytes — tolerate them.
		_ = rest
	}

	// The Content field is [0] EXPLICIT, its Bytes are the DER-encoded SignedData SEQUENCE.
	if len(ci.Content.Bytes) == 0 {
		return nil, fmt.Errorf("cms: ContentInfo has no embedded content")
	}

	// Step 2: unmarshal SignedData.
	var sd signedData
	if _, err := asn1.Unmarshal(ci.Content.Bytes, &sd); err != nil {
		return nil, fmt.Errorf("cms: unmarshal SignedData: %w", err)
	}

	// Step 3: parse SET OF SignerInfo — take the first element.
	var signerInfosRaw []asn1.RawValue
	if _, err := asn1.UnmarshalWithParams(sd.SignerInfos.Bytes, &signerInfosRaw, "set"); err != nil {
		// Fallback: try as SEQUENCE (some implementations use SEQUENCE instead of SET).
		if _, err2 := asn1.Unmarshal(sd.SignerInfos.FullBytes, &signerInfosRaw); err2 != nil {
			return nil, fmt.Errorf("cms: unmarshal SignerInfos: %w", err)
		}
	}
	if len(signerInfosRaw) == 0 {
		return nil, fmt.Errorf("cms: no signers found")
	}

	var si signerInfo
	if _, err := asn1.Unmarshal(signerInfosRaw[0].FullBytes, &si); err != nil {
		return nil, fmt.Errorf("cms: unmarshal SignerInfo: %w", err)
	}

	// Step 4: parse authenticated attributes (tag [0] IMPLICIT → treat as SET).
	if len(si.AuthAttributes.Bytes) == 0 {
		return nil, fmt.Errorf("cms: no authenticated attributes in signerInfo")
	}

	var attrs []attribute
	if _, err := asn1.UnmarshalWithParams(si.AuthAttributes.Bytes, &attrs, "set"); err != nil {
		return nil, fmt.Errorf("cms: unmarshal authenticated attributes: %w", err)
	}

	// Step 5: find the messageDigest attribute.
	for _, attr := range attrs {
		if attr.Type.Equal(oidMessageDigest) {
			// Values is a SET OF OCTET STRING; take the first element.
			var digests [][]byte
			if _, err := asn1.UnmarshalWithParams(attr.Values.Bytes, &digests, "set"); err != nil {
				// Fallback: single OCTET STRING.
				var digest []byte
				if _, err2 := asn1.Unmarshal(attr.Values.FullBytes, &digest); err2 != nil {
					return nil, fmt.Errorf("cms: unmarshal messageDigest value: %w", err)
				}
				return digest, nil
			}
			if len(digests) == 0 {
				return nil, fmt.Errorf("cms: messageDigest attribute is empty")
			}
			return digests[0], nil
		}
	}

	return nil, fmt.Errorf("cms: messageDigest attribute not found in authenticatedAttributes")
}
