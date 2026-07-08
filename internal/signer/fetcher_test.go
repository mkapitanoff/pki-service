package signer

import "testing"

func TestIsAcceptablePDFContentType(t *testing.T) {
	cases := []struct {
		name string
		ct   string
		want bool
	}{
		{"clean pdf", "application/pdf", true},
		{"pdf with qs param (W3C)", "application/pdf; qs=0.001", true},
		{"pdf with charset param", "application/pdf; charset=binary", true},
		{"pdf uppercase", "Application/PDF", true},
		{"octet-stream (S3 presigned default)", "application/octet-stream", true},
		{"binary octet-stream", "binary/octet-stream", true},
		{"empty header", "", true},
		{"whitespace only", "   ", true},
		{"unparseable header", "not a media type ;;;", true},
		{"html rejected", "text/html", false},
		{"html with charset rejected", "text/html; charset=UTF-8", false},
		{"json rejected", "application/json", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAcceptablePDFContentType(c.ct); got != c.want {
				t.Errorf("isAcceptablePDFContentType(%q) = %v, want %v", c.ct, got, c.want)
			}
		})
	}
}
