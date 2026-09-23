package idempotency

import "testing"

func TestParse(t *testing.T) {
	if _, err := Parse("payment:12345678"); err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"short", " leading-value", "contains space", "line\nbreak"} {
		if _, err := Parse(value); err == nil {
			t.Fatalf("Parse(%q) accepted invalid key", value)
		}
	}
}
