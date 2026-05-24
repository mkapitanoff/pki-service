package pdf

import (
	"encoding/base64"
	"encoding/hex"
	"regexp"
)

// ExistingSignature holds a CMS signature found inside a PDF file.
type ExistingSignature struct {
	CMS string // base64-encoded DER CMS
}

// reContents matches /Contents <hexdata> in a PDF byte stream.
// The PDF spec stores CMS as a hex string enclosed in angle brackets.
var reContents = regexp.MustCompile(`/Contents\s*<([0-9A-Fa-f]+)>`)

// reByteRange is used as a proximity check — we only accept /Contents entries
// that appear near a /ByteRange array (i.e. they belong to a /Sig dictionary).
var reByteRange = regexp.MustCompile(`/ByteRange\s*\[`)

// ExtractSignatures scans raw PDF bytes for CAdES/CMS signatures embedded as
// /Sig form fields (/ByteRange + /Contents). It returns a best-effort list;
// any parse error causes an empty (nil) result rather than a hard error.
func ExtractSignatures(pdfBytes []byte) []ExistingSignature {
	// Locate all /ByteRange markers so we know which regions of the file
	// contain signature dictionaries.
	brLocs := reByteRange.FindAllIndex(pdfBytes, -1)
	if len(brLocs) == 0 {
		return nil
	}

	// For each /ByteRange, search for a /Contents entry in a window around it.
	// Window size: 64 KB in each direction should cover any realistic /Sig dict.
	const window = 64 * 1024

	seen := map[string]struct{}{} // dedup by hex prefix
	var sigs []ExistingSignature

	for _, loc := range brLocs {
		start := loc[0] - window
		if start < 0 {
			start = 0
		}
		end := loc[1] + window
		if end > len(pdfBytes) {
			end = len(pdfBytes)
		}

		chunk := pdfBytes[start:end]
		matches := reContents.FindAllSubmatch(chunk, -1)
		for _, m := range matches {
			hexData := m[1]
			key := string(hexData[:min(32, len(hexData))])
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}

			raw, err := hex.DecodeString(string(hexData))
			if err != nil || len(raw) < 8 {
				continue
			}
			// Sanity-check: DER sequence tag 0x30 expected at the start of CMS.
			if raw[0] != 0x30 {
				continue
			}
			cms := base64.StdEncoding.EncodeToString(raw)
			sigs = append(sigs, ExistingSignature{CMS: cms})
		}
	}
	return sigs
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
