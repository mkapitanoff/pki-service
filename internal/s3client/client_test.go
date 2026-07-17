package s3client

import (
	"errors"
	"testing"
)

func TestClassify403_SignatureMismatchIsForbidden(t *testing.T) {
	body := []byte(`<?xml version="1.0" encoding="UTF-8"?><Error><Code>SignatureDoesNotMatch</Code><Message></Message></Error>`)
	err := classify403(body)
	if !errors.Is(err, ErrPresignedURLForbidden) {
		t.Fatalf("SignatureDoesNotMatch → want ErrPresignedURLForbidden, got %v", err)
	}
	if errors.Is(err, ErrPresignedURLExpired) {
		t.Fatalf("SignatureDoesNotMatch must NOT be classified as expired: %v", err)
	}
	// S3-код должен попасть в текст для диагностики.
	if got := err.Error(); got == "" || !containsCode(got, "SignatureDoesNotMatch") {
		t.Fatalf("expected S3 code in error text, got %q", got)
	}
}

func TestClassify403_ExpiredIsExpired(t *testing.T) {
	body := []byte(`<?xml version="1.0"?><Error><Code>AccessDenied</Code><Message>Request has expired</Message></Error>`)
	if err := classify403(body); !errors.Is(err, ErrPresignedURLExpired) {
		t.Fatalf("«Request has expired» → want ErrPresignedURLExpired, got %v", err)
	}
}

func TestClassify403_ExpiredTokenIsExpired(t *testing.T) {
	body := []byte(`<Error><Code>ExpiredToken</Code><Message>The provided token has expired.</Message></Error>`)
	if err := classify403(body); !errors.Is(err, ErrPresignedURLExpired) {
		t.Fatalf("ExpiredToken → want ErrPresignedURLExpired, got %v", err)
	}
}

func TestClassify403_EmptyBodyIsForbidden(t *testing.T) {
	if err := classify403(nil); !errors.Is(err, ErrPresignedURLForbidden) {
		t.Fatalf("empty body → want ErrPresignedURLForbidden, got %v", err)
	}
}

func TestExtractS3Code(t *testing.T) {
	cases := map[string]string{
		`<Error><Code>SignatureDoesNotMatch</Code></Error>`: "SignatureDoesNotMatch",
		`no xml here`: "",
		`<Code>AccessDenied</Code>`: "AccessDenied",
	}
	for body, want := range cases {
		if got := extractS3Code([]byte(body)); got != want {
			t.Fatalf("extractS3Code(%q) = %q, want %q", body, got, want)
		}
	}
}

func containsCode(s, code string) bool {
	for i := 0; i+len(code) <= len(s); i++ {
		if s[i:i+len(code)] == code {
			return true
		}
	}
	return false
}
