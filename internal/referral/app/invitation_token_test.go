package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestInvitationTokenIsOpaqueAndStrictlyValidated(t *testing.T) {
	issuer, err := newInvitationTokenIssuer(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	first, err := issuer.issue("customer:101", 7, "invite-token-key-101")
	if err != nil {
		t.Fatal(err)
	}
	second, err := issuer.issue("customer:101", 7, "invite-token-key-101")
	if err != nil || first != second {
		t.Fatalf("idempotent token first=%q second=%q err=%v", first, second, err)
	}
	if !strings.HasPrefix(first, "rfi_") || len(first) != 47 || !validInvitationToken(first) {
		t.Fatalf("invalid opaque token %q", first)
	}
	if validInvitationToken(first+"x") || validInvitationToken("rfi_"+strings.Repeat("/", 43)) {
		t.Fatal("malformed token passed closed validation")
	}
}
