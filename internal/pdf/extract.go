package pdf

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// ExistingSignature holds a CMS signature found inside a PDF file.
type ExistingSignature struct {
	CMS string // base64-encoded DER CMS
}

// reContents matches /Contents <hexdata> where hexdata may contain whitespace
// (PDF spec §7.3.4.3: whitespace in hex strings is ignored).
var reContents = regexp.MustCompile(`/Contents\s*<([0-9A-Fa-f\s]+)>`)

// reByteRange detects signature dictionaries.
var reByteRange = regexp.MustCompile(`/ByteRange\s*[\[<]`)

// ExtractSignatures scans raw PDF bytes for CAdES/CMS signatures embedded as
// /Sig form fields (/ByteRange + /Contents). Best-effort; returns nil on failure.
func ExtractSignatures(pdfBytes []byte) []ExistingSignature {
	text := string(pdfBytes)

	// Find all /ByteRange positions.
	brLocs := reByteRange.FindAllStringIndex(text, -1)
	fmt.Printf("[extract] found %d /ByteRange markers in PDF (%d bytes)\n", len(brLocs), len(pdfBytes))
	if len(brLocs) == 0 {
		return nil
	}

	// Search a generous window around each /ByteRange for a /Contents entry.
	// Use 512 KB — large PDFs can have big gap between ByteRange and Contents.
	const window = 512 * 1024

	seen := map[string]struct{}{}
	var sigs []ExistingSignature

	for idx, loc := range brLocs {
		start := loc[0] - window
		if start < 0 {
			start = 0
		}
		end := loc[1] + window
		if end > len(text) {
			end = len(text)
		}

		chunk := text[start:end]
		matches := reContents.FindAllStringSubmatch(chunk, -1)
		fmt.Printf("[extract] ByteRange[%d]: window [%d..%d], /Contents matches: %d\n", idx, start, end, len(matches))

		for _, m := range matches {
			// Strip whitespace from hex string (PDF spec allows it).
			hexRaw := strings.Join(strings.Fields(m[1]), "")
			if len(hexRaw) < 16 {
				fmt.Printf("[extract] skip short hex (%d chars)\n", len(hexRaw))
				continue
			}

			// Dedup by first 32 hex chars.
			key := hexRaw[:min(32, len(hexRaw))]
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			raw, err := hex.DecodeString(hexRaw)
			if err != nil {
				fmt.Printf("[extract] hex decode error: %v (hex len=%d)\n", err, len(hexRaw))
				continue
			}
			if len(raw) < 8 {
				fmt.Printf("[extract] DER too short: %d bytes\n", len(raw))
				continue
			}

			// Strip trailing zero-padding (PDF reserves space by padding with 0x00).
			trimmed := strings.TrimRight(string(raw), "\x00")
			raw = []byte(trimmed)

			// Sanity-check: DER SEQUENCE tag.
			if len(raw) == 0 || raw[0] != 0x30 {
				fmt.Printf("[extract] not a DER SEQUENCE (first byte=0x%02x), skip\n", raw[0])
				continue
			}

			fmt.Printf("[extract] found CMS: DER %d bytes\n", len(raw))
			cms := base64.StdEncoding.EncodeToString(raw)
			sigs = append(sigs, ExistingSignature{CMS: cms})
		}
	}

	fmt.Printf("[extract] total extracted signatures: %d\n", len(sigs))
	return sigs
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
