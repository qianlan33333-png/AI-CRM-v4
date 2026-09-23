package wecom

import (
	"bytes"
	"testing"
)

func TestWelcomeGrantCipherAuthenticatesAndDoesNotExposePlaintext(t *testing.T) {
	ciphertext, err := NewWelcomeGrantCipher(string(bytes.Repeat([]byte("k"), 32)))
	if err != nil {
		t.Fatal(err)
	}
	associatedData := []byte("callback-digest")
	sealed, err := ciphertext.encrypt("opaque-welcome-code", associatedData)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("opaque-welcome-code")) {
		t.Fatal("ciphertext exposed welcome code")
	}
	plain, err := ciphertext.decrypt(sealed, associatedData)
	if err != nil || plain != "opaque-welcome-code" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
	sealed[len(sealed)-1] ^= 0xff
	if _, err = ciphertext.decrypt(sealed, associatedData); err == nil {
		t.Fatal("tampered welcome grant was accepted")
	}
	sealed, err = ciphertext.encrypt("opaque-welcome-code", associatedData)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ciphertext.decrypt(sealed, []byte("another-callback")); err == nil {
		t.Fatal("welcome grant was accepted for another callback")
	}
}

func TestWelcomeGrantReferenceRoundTrip(t *testing.T) {
	for _, id := range []int64{1, 10, 255, 256, 1<<62 - 1} {
		reference := "wgrant_" + formatGrantID(id)
		parsed, ok := parseGrantRef(reference)
		if !ok || parsed != id {
			t.Fatalf("reference=%q parsed=%d ok=%v", reference, parsed, ok)
		}
	}
	for _, value := range []string{"", "wgrant_", "wgrant_0", "wgrant_zz", "grant_1"} {
		if _, ok := parseGrantRef(value); ok {
			t.Fatalf("invalid reference accepted: %q", value)
		}
	}
}
