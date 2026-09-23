package paymenthttp

import (
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func TestReferralActivityContextCookieRequiresOpaqueTokenShape(t *testing.T) {
	digest := sha256.Sum256([]byte("activity"))
	token := "rpa_" + base64.RawURLEncoding.EncodeToString(digest[:])
	if !validReferralActivityContext(token) {
		t.Fatal("server-issued activity token should be accepted")
	}
	for _, invalid := range []string{"", "campaign:3", "rpa_" + base64.RawURLEncoding.EncodeToString([]byte("short"))} {
		if validReferralActivityContext(invalid) {
			t.Fatalf("invalid activity context accepted: %q", invalid)
		}
	}
}
