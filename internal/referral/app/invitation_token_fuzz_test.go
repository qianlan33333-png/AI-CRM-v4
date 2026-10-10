package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

func FuzzInvitationTokenFailsClosedForMalformedBearer(f *testing.F) {
	f.Add("rfi_" + strings.Repeat("A", 43))
	f.Add("")
	f.Add("rfi_" + strings.Repeat("/", 43))
	f.Add("rfi_" + strings.Repeat("A", 42) + "\n")
	f.Fuzz(func(t *testing.T, token string) {
		if !validInvitationToken(token) {
			return
		}
		if len(token) != 47 || !strings.HasPrefix(token, "rfi_") {
			t.Fatal("invalid bearer shape accepted")
		}
		raw, err := base64.RawURLEncoding.DecodeString(token[4:])
		if err != nil || len(raw) != 32 {
			t.Fatal("malformed bearer payload accepted")
		}
	})
}
