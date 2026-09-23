package target

import (
	"crypto/sha256"
	"testing"
)

func TestDigestHexIsStable(t *testing.T) {
	digest := sha256.Sum256([]byte("config-definition-import"))
	if got, want := DigestHex(digest), "db1fbe8f5117059900b8cd72440edf75f34854a897ec5c062ae0ba93132379dd"; got != want {
		t.Fatalf("digest=%s want=%s", got, want)
	}
}
