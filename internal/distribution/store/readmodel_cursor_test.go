package store

import (
	"errors"
	"testing"
)

func TestReadModelCursorRejectsMalformedInputInsteadOfRepeatingFirstPage(t *testing.T) {
	for _, value := range []string{"abc", "0", "01", "+1", "-1", "9223372036854775808"} {
		if _, err := readModelCursor(value); !errors.Is(err, ErrInvalid) {
			t.Fatalf("cursor %q error=%v, want invalid", value, err)
		}
	}
	if got, err := readModelCursor("17"); err != nil || got != 17 {
		t.Fatalf("valid cursor=%d err=%v", got, err)
	}
}
