package secure

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestObservationVaultScopesCiphertextAndDigest(t *testing.T) {
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	vault, err := NewObservationVault(key)
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := vault.Encrypt("unionid", "wechat-open-platform:main", "union-secret")
	if err != nil {
		t.Fatal(err)
	}
	plain, err := vault.Decrypt("unionid", "wechat-open-platform:main", ciphertext)
	if err != nil || plain != "union-secret" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
	if _, err = vault.Decrypt("unionid", "wechat-open-platform:other", ciphertext); err == nil {
		t.Fatal("cross-scope decrypt unexpectedly succeeded")
	}
	first := vault.LookupDigest("unionid", "wechat-open-platform:main", "union-secret")
	second := vault.LookupDigest("unionid", "wechat-open-platform:main", "union-secret")
	other := vault.LookupDigest("phone", "phone:cn11", "union-secret")
	if first != second || first == other {
		t.Fatal("lookup digest is not deterministic and scope separated")
	}
}

func TestObservationVaultRejectsInvalidKey(t *testing.T) {
	if _, err := NewObservationVault("short"); err == nil {
		t.Fatal("invalid key accepted")
	}
}
