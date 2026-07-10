package signer

import (
	"crypto/sha256"
	"encoding/asn1"
	"encoding/base64"
	"encoding/hex"
	"testing"
)

var (
	oidSignedData = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 2}
	oidPKCS7Data  = asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 7, 1}
)

// buildTestCMS строит минимальный синтетический CMS (без ПДн), несущий eContent.
// attached=false → encapContentInfo без eContent (detached).
func buildTestCMS(t *testing.T, eContent []byte, attached bool) []byte {
	t.Helper()

	// explicit0 оборачивает inner DER в [0] EXPLICIT (context-specific, constructed).
	explicit0 := func(inner []byte) asn1.RawValue {
		return asn1.RawValue{Class: asn1.ClassContextSpecific, Tag: 0, IsCompound: true, Bytes: inner}
	}

	var eciDER []byte
	var err error
	if attached {
		octetDER, merr := asn1.Marshal(eContent) // OCTET STRING
		if merr != nil {
			t.Fatalf("marshal eContent: %v", merr)
		}
		var e struct {
			OID     asn1.ObjectIdentifier
			Content asn1.RawValue // [0] EXPLICIT OCTET STRING, построен вручную
		}
		e.OID = oidPKCS7Data
		e.Content = explicit0(octetDER)
		eciDER, err = asn1.Marshal(e)
	} else {
		var e struct {
			OID asn1.ObjectIdentifier
		}
		e.OID = oidPKCS7Data
		eciDER, err = asn1.Marshal(e)
	}
	if err != nil {
		t.Fatalf("marshal encapContentInfo: %v", err)
	}

	var sd struct {
		Version          int
		DigestAlgorithms []asn1.RawValue `asn1:"set"`
		EncapContentInfo asn1.RawValue
		SignerInfos      []asn1.RawValue `asn1:"set"`
	}
	sd.Version = 1
	sd.DigestAlgorithms = []asn1.RawValue{}
	sd.EncapContentInfo = asn1.RawValue{FullBytes: eciDER}
	sd.SignerInfos = []asn1.RawValue{}
	sdDER, err := asn1.Marshal(sd)
	if err != nil {
		t.Fatalf("marshal SignedData: %v", err)
	}

	var ci struct {
		OID     asn1.ObjectIdentifier
		Content asn1.RawValue // [0] EXPLICIT SignedData, построен вручную
	}
	ci.OID = oidSignedData
	ci.Content = explicit0(sdDER)
	ciDER, err := asn1.Marshal(ci)
	if err != nil {
		t.Fatalf("marshal ContentInfo: %v", err)
	}
	return ciDER
}

func TestExtractSignedContentHash_AttachedBase64(t *testing.T) {
	// Наблюдаемый формат NCALayer: eContent = ASCII base64 от 32-байтного SHA-256.
	var want [32]byte
	for i := range want {
		want[i] = byte(i)
	}
	wantHex := hex.EncodeToString(want[:])
	eContent := []byte(base64.StdEncoding.EncodeToString(want[:])) // 44 ASCII bytes

	cms := buildTestCMS(t, eContent, true)
	gotHex, attached, err := ExtractSignedContentHash(cms)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if !attached {
		t.Fatalf("expected attached=true")
	}
	if gotHex != wantHex {
		t.Fatalf("hash mismatch:\n got=%s\nwant=%s", gotHex, wantHex)
	}
}

func TestExtractSignedContentHash_AttachedRaw(t *testing.T) {
	// eContent = сырые 32 байта.
	var raw [32]byte
	for i := range raw {
		raw[i] = byte(255 - i)
	}
	cms := buildTestCMS(t, raw[:], true)
	gotHex, attached, err := ExtractSignedContentHash(cms)
	if err != nil || !attached {
		t.Fatalf("attached raw: err=%v attached=%v", err, attached)
	}
	if gotHex != hex.EncodeToString(raw[:]) {
		t.Fatalf("raw hash mismatch: got=%s", gotHex)
	}
}

func TestExtractSignedContentHash_Detached(t *testing.T) {
	cms := buildTestCMS(t, nil, false)
	gotHex, attached, err := ExtractSignedContentHash(cms)
	if err != nil {
		t.Fatalf("detached should not error: %v", err)
	}
	if attached {
		t.Fatalf("expected attached=false for detached CMS")
	}
	if gotHex != "" {
		t.Fatalf("expected empty hash for detached, got %q", gotHex)
	}
}

func TestExtractSignedContentHash_GarbageEContent(t *testing.T) {
	// eContent не похож на SHA-256 (не 32 байта / не base64 32B / не hex 64).
	cms := buildTestCMS(t, []byte("not-a-hash"), true)
	_, attached, err := ExtractSignedContentHash(cms)
	if err == nil {
		t.Fatalf("expected error for unrecognizable eContent")
	}
	if !attached {
		t.Fatalf("attached should still be true (eContent present)")
	}
}

func TestNormalizeToSHA256Hex(t *testing.T) {
	var h [32]byte
	for i := range h {
		h[i] = byte(i * 7)
	}
	wantHex := hex.EncodeToString(h[:])

	cases := map[string][]byte{
		"raw32":  h[:],
		"base64": []byte(base64.StdEncoding.EncodeToString(h[:])),
		"hex64":  []byte(hex.EncodeToString(h[:])),
	}
	for name, in := range cases {
		got, err := normalizeToSHA256Hex(in)
		if err != nil {
			t.Fatalf("%s: err %v", name, err)
		}
		if got != wantHex {
			t.Fatalf("%s: got=%s want=%s", name, got, wantHex)
		}
	}
	if _, err := normalizeToSHA256Hex([]byte("short")); err == nil {
		t.Fatalf("expected error for garbage")
	}
	_ = sha256.Size
}
