package query

import (
	"errors"
	"testing"
)

func TestPublicReasonFailsClosed(t *testing.T) {
	if got := publicReason("two_wecom_roots"); got != "two_wecom_roots" {
		t.Fatalf("known reason=%q", got)
	}
	if got := publicReason("external_userid=private-value"); got != "other" {
		t.Fatalf("unexpected reason leaked as %q", got)
	}
}

func TestNormalizeOptionsDefaultsAndBounds(t *testing.T) {
	allowed := map[string]struct{}{"open": {}, "resolved": {}}
	got, err := normalizeOptions(ListOptions{}, allowed)
	if err != nil || got != (ListOptions{Status: "open", Limit: DefaultLimit}) {
		t.Fatalf("default options=%#v error=%v", got, err)
	}
	for _, input := range []ListOptions{
		{Status: "unknown", Limit: 1}, {Status: "open", Limit: MaximumLimit + 1}, {Status: "open", Offset: -1},
	} {
		if _, err = normalizeOptions(input, allowed); !errors.Is(err, ErrInvalidQuery) {
			t.Fatalf("input=%#v error=%v", input, err)
		}
	}
}
