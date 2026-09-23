package credential

import (
	"strings"
	"testing"
)

func TestArgon2idPHCRoundTrip(t *testing.T) {
	hasher := PasswordHasher{}
	encoded, err := hasher.Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("hash = %q", encoded)
	}
	if !hasher.Verify("correct horse battery staple", encoded) || hasher.Verify("wrong password", encoded) {
		t.Fatal("password verification mismatch")
	}
}

func TestOpaqueCredentialStoresOnlyDigest(t *testing.T) {
	value, digest, err := IssueOpaque("as_")
	if err != nil {
		t.Fatal(err)
	}
	if value == "" || string(digest[:]) == value || !Matches(value, digest) || Matches(value+"x", digest) {
		t.Fatal("opaque credential contract failed")
	}
}
