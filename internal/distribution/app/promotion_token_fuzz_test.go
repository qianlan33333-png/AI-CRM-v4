package app

import (
	"encoding/base64"
	"strings"
	"testing"
)

func FuzzPromotionTokenFailsClosedForMalformedBearer(f *testing.F) {
	for _, token := range []string{"", "dpc_" + strings.Repeat("A", 43), "dpc_" + strings.Repeat("A", 42) + "\n", "dpc_" + strings.Repeat("/", 43)} {
		f.Add(token)
	}
	f.Fuzz(func(t *testing.T, token string) {
		if !validPromotionToken(token) {
			return
		}
		if len(token) != 47 || !strings.HasPrefix(token, "dpc_") {
			t.Fatal("invalid promotion bearer shape accepted")
		}
		raw, err := base64.RawURLEncoding.DecodeString(token[4:])
		if err != nil || len(raw) != 32 {
			t.Fatal("malformed promotion bearer accepted")
		}
	})
}
