package secure

import (
	"encoding/base64"
	"testing"
)

func TestContactDecryptAuthenticatesVersionAndFrozenValue(t *testing.T) {
	c, err := NewContactCipher(base64.RawStdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := c.Encrypt("+8613800000000")
	if err != nil {
		t.Fatal(err)
	}
	value, err := c.Decrypt(ciphertext, 1)
	if err != nil || value != "+8613800000000" {
		t.Fatal("roundtrip failed")
	}
	if _, err = c.Decrypt(ciphertext, 2); err == nil {
		t.Fatal("invalid version accepted")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err = c.Decrypt(ciphertext, 1); err == nil {
		t.Fatal("tampered contact accepted")
	}
}
