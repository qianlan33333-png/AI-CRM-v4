package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
)

const ObservationKeyVersion int16 = 1

type ObservationVault struct {
	aead cipher.AEAD
	key  []byte
}

func NewObservationVault(encoded string) (*ObservationVault, error) {
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("identity observation key must be base64 encoded 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &ObservationVault{aead: aead, key: append([]byte(nil), key...)}, nil
}

func (vault *ObservationVault) Encrypt(kind, scope, value string) ([]byte, error) {
	if vault == nil || vault.aead == nil || kind == "" || scope == "" || value == "" {
		return nil, errors.New("identity observation vault unavailable")
	}
	nonce := make([]byte, vault.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return vault.aead.Seal(nonce, nonce, []byte(value), observationAAD(kind, scope)), nil
}

func (vault *ObservationVault) Decrypt(kind, scope string, ciphertext []byte) (string, error) {
	if vault == nil || vault.aead == nil || len(ciphertext) < vault.aead.NonceSize() {
		return "", errors.New("identity observation vault unavailable")
	}
	plain, err := vault.aead.Open(nil, ciphertext[:vault.aead.NonceSize()], ciphertext[vault.aead.NonceSize():], observationAAD(kind, scope))
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func (vault *ObservationVault) LookupDigest(kind, scope, value string) [32]byte {
	mac := hmac.New(sha256.New, vault.key)
	_, _ = mac.Write(observationAAD(kind, scope))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(value))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

func observationAAD(kind, scope string) []byte {
	return []byte("identity-source-observation:v1\x00" + kind + "\x00" + scope)
}
