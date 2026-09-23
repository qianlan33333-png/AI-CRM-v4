package idempotency

import (
	"errors"
	"strings"
	"unicode"
)

var ErrInvalidKey = errors.New("invalid idempotency key")

type Key string

func Parse(raw string) (Key, error) {
	if strings.TrimSpace(raw) != raw || len(raw) < 8 || len(raw) > 200 {
		return "", ErrInvalidKey
	}
	for _, character := range raw {
		if unicode.IsControl(character) || unicode.IsSpace(character) {
			return "", ErrInvalidKey
		}
	}
	return Key(raw), nil
}
