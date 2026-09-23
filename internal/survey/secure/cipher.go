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

type Cipher struct {
	aead     cipher.AEAD
	tokenKey [32]byte
}

func NewCipher(encoded string) (*Cipher, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("survey data key must be base64url encoded 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, errors.New("invalid survey data key")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, errors.New("invalid survey data cipher")
	}
	return &Cipher{aead: aead, tokenKey: sha256.Sum256(append(append([]byte(nil), key...), []byte("survey-result-token-v1")...))}, nil
}
func (c *Cipher) Encrypt(value string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errors.New("survey cipher unavailable")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(value), nil), nil
}
func (c *Cipher) Decrypt(value []byte) (string, error) {
	if c == nil || c.aead == nil || len(value) < c.aead.NonceSize() {
		return "", errors.New("survey ciphertext invalid")
	}
	plain, err := c.aead.Open(nil, value[:c.aead.NonceSize()], value[c.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("survey ciphertext invalid")
	}
	return string(plain), nil
}
func Digest(value string) [32]byte { return sha256.Sum256([]byte(value)) }
func (c *Cipher) Token(parts ...string) (string, error) {
	if c == nil || c.aead == nil {
		return "", errors.New("survey cipher unavailable")
	}
	mac := hmac.New(sha256.New, c.tokenKey[:])
	for _, part := range parts {
		mac.Write([]byte{0})
		mac.Write([]byte(part))
	}
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}
