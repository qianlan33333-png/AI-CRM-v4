package adapter

import "encoding/json"

// normalizeArchiveJSON escapes only literal C0 characters inside JSON strings.
// This preserves the decoded text, including tabs and line breaks, instead of
// dropping a message or moving the cursor past it. All other malformed syntax
// stays rejected. The caller retains the exact pre-repair private bytes.
func normalizeArchiveJSON(raw []byte) ([]byte, bool, error) {
	if json.Valid(raw) {
		return append([]byte(nil), raw...), false, nil
	}
	const hex = "0123456789abcdef"
	out := make([]byte, 0, len(raw))
	inString, escaped, repaired := false, false, false
	for _, b := range raw {
		if inString && escaped {
			// A broken escape sequence is not the observed SDK defect.
			if b < 0x20 {
				return nil, false, ErrArchiveUnavailable
			}
			escaped = false
			out = append(out, b)
			continue
		}
		if inString && b < 0x20 {
			out = append(out, '\\', 'u', '0', '0', hex[b>>4], hex[b&15])
			repaired = true
			continue
		}
		if b == '"' {
			inString = !inString
		} else if inString && b == '\\' {
			escaped = true
		}
		out = append(out, b)
	}
	if !repaired || !json.Valid(out) {
		return nil, false, ErrArchiveUnavailable
	}
	return out, true, nil
}
