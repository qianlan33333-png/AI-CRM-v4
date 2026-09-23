// Package donortemplate safely extracts the frozen outer template#tpl from a
// release page. It is platform-only and imports no business domain.
package donortemplate

import (
	"errors"
	"strings"
)

func Extract(raw string) (string, error) {
	found, depth, startContent := false, 0, 0
	for cursor := 0; cursor < len(raw); {
		tag, next, ok := nextTag(raw, cursor)
		if !ok {
			break
		}
		cursor = next
		if tag.name != "template" {
			continue
		}
		if !found {
			if !tag.closing && templateID(tag.raw, "tpl") {
				found, depth, startContent = true, 1, tag.end
			}
			continue
		}
		if tag.closing {
			depth--
			if depth == 0 {
				return raw[startContent:tag.start], nil
			}
		} else {
			depth++
		}
	}
	if !found {
		return "", errors.New("donor template missing")
	}
	return "", errors.New("donor template incomplete")
}

type tag struct {
	raw, name  string
	closing    bool
	start, end int
}

func nextTag(raw string, cursor int) (tag, int, bool) {
	for cursor < len(raw) {
		rel := strings.IndexByte(raw[cursor:], '<')
		if rel < 0 {
			return tag{}, len(raw), false
		}
		start := cursor + rel
		if strings.HasPrefix(raw[start:], "<!--") {
			end := strings.Index(raw[start+4:], "-->")
			if end < 0 {
				return tag{}, len(raw), false
			}
			cursor = start + end + 7
			continue
		}
		cursor = start + 1
		closing := false
		for cursor < len(raw) && space(raw[cursor]) {
			cursor++
		}
		if cursor < len(raw) && raw[cursor] == '/' {
			closing = true
			cursor++
			for cursor < len(raw) && space(raw[cursor]) {
				cursor++
			}
		}
		nameStart := cursor
		for cursor < len(raw) && name(raw[cursor]) {
			cursor++
		}
		if nameStart == cursor {
			cursor = start + 1
			continue
		}
		n := strings.ToLower(raw[nameStart:cursor])
		quote := byte(0)
		for cursor < len(raw) {
			ch := raw[cursor]
			if quote != 0 {
				if ch == quote {
					quote = 0
				}
				cursor++
				continue
			}
			if ch == '\'' || ch == '"' {
				quote = ch
				cursor++
				continue
			}
			if ch == '>' {
				end := cursor + 1
				return tag{raw: raw[start:end], name: n, closing: closing, start: start, end: end}, end, true
			}
			cursor++
		}
		return tag{}, len(raw), false
	}
	return tag{}, len(raw), false
}
func space(ch byte) bool { return ch == ' ' || ch == '\t' || ch == '\n' || ch == '\r' || ch == '\f' }
func name(ch byte) bool {
	return ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '-' || ch == ':'
}
func templateID(raw, expected string) bool {
	lower := strings.ToLower(raw)
	for offset := 0; ; {
		index := strings.Index(lower[offset:], "id")
		if index < 0 {
			return false
		}
		index += offset
		before := index == 0 || !name(lower[index-1])
		after := index + 2
		afterOK := after >= len(lower) || !name(lower[after])
		if !before || !afterOK {
			offset = after
			continue
		}
		for after < len(lower) && space(lower[after]) {
			after++
		}
		if after >= len(lower) || lower[after] != '=' {
			offset = after
			continue
		}
		after++
		for after < len(lower) && space(lower[after]) {
			after++
		}
		if after >= len(lower) {
			return false
		}
		start, end := after, after
		if lower[after] == '\'' || lower[after] == '"' {
			q := lower[after]
			start++
			end = strings.IndexByte(lower[start:], q)
			if end < 0 {
				return false
			}
			end += start
		} else {
			for end < len(lower) && !space(lower[end]) && lower[end] != '>' && lower[end] != '/' {
				end++
			}
		}
		return lower[start:end] == strings.ToLower(expected)
	}
}
