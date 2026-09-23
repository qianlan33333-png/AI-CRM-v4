package secure

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestPhoneVaultEncryptLookupAndMask(t *testing.T) {
	key := base64.RawStdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	vault, err := NewPhoneVault(key)
	if err != nil {
		t.Fatal(err)
	}
	first, err := vault.Encrypt("13800138000")
	if err != nil {
		t.Fatal(err)
	}
	second, err := vault.Encrypt("13800138000")
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("AES-GCM nonce was not randomized")
	}
	if got, err := vault.Decrypt(first); err != nil || got != "13800138000" {
		t.Fatalf("decrypt=%q err=%v", got, err)
	}
	if vault.LookupDigest("13800138000") != vault.LookupDigest("13800138000") || vault.LookupDigest("13800138000") == vault.LookupDigest("13900138000") {
		t.Fatal("lookup digest is not deterministic and scoped")
	}
	if got := MaskPhone("13800138000"); got != "138****8000" {
		t.Fatalf("mask=%q", got)
	}
}
