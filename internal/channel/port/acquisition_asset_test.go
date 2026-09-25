package port

import (
	"strings"
	"testing"
)

func TestEffectiveAcquisitionState(t *testing.T) {
	digest := "sha256:" + strings.Repeat("a", 64)
	if got := EffectiveAcquisitionState("", digest); got != "ca-"+strings.Repeat("a", 27) || len(got) != 30 {
		t.Fatalf("generated state=%q", got)
	}
	if got := EffectiveAcquisitionState("configured", digest); got != "configured" {
		t.Fatalf("configured state=%q", got)
	}
}
