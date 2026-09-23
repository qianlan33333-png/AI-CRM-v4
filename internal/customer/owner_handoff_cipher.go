package customer

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// OwnerHandoffCipher keeps provider identifiers and the optional welcome text
// out of Customer tables in clear text.  EER receives only four digests; the
// owning Customer store decrypts a frozen snapshot immediately before the
// Outbound adapter makes the single Provider request.
type OwnerHandoffCipher struct{ aead cipher.AEAD }

func NewOwnerHandoffCipher(encoded string) (*OwnerHandoffCipher, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("owner handoff data key must be 32 raw-base64 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &OwnerHandoffCipher{aead: aead}, nil
}

func (ciphertext *OwnerHandoffCipher) Seal(batchID string, line int64, field, value string) ([]byte, error) {
	if ciphertext == nil || ciphertext.aead == nil || batchID == "" || line < 0 || field == "" || value == "" {
		return nil, errors.New("owner handoff cipher unavailable")
	}
	nonce := make([]byte, ciphertext.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return ciphertext.aead.Seal(nonce, nonce, []byte(value), ownerHandoffAAD(batchID, line, field)), nil
}

func (ciphertext *OwnerHandoffCipher) Open(batchID string, line int64, field string, value []byte) (string, error) {
	if ciphertext == nil || ciphertext.aead == nil || batchID == "" || line < 0 || field == "" || len(value) < ciphertext.aead.NonceSize()+ciphertext.aead.Overhead() {
		return "", errors.New("owner handoff snapshot unavailable")
	}
	plain, err := ciphertext.aead.Open(nil, value[:ciphertext.aead.NonceSize()], value[ciphertext.aead.NonceSize():], ownerHandoffAAD(batchID, line, field))
	if err != nil || len(plain) == 0 {
		return "", errors.New("owner handoff snapshot unavailable")
	}
	return string(plain), nil
}

func ownerHandoffAAD(batchID string, line int64, field string) []byte {
	return []byte(fmt.Sprintf("customer-owner-handoff:v1\x00%s\x00%d\x00%s", batchID, line, field))
}
