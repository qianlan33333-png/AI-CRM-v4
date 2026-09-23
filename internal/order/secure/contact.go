package secure

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
)

const KeyVersion int16 = 1

type ContactCipher struct{ aead cipher.AEAD }

func NewContactCipher(encoded string) (*ContactCipher, error) {
	key, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("order contact data key must be 32 raw-base64 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &ContactCipher{aead: aead}, nil
}

func (c *ContactCipher) Encrypt(value string) ([]byte, error) {
	if c == nil || c.aead == nil {
		return nil, errors.New("order contact cipher unavailable")
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.aead.Seal(nonce, nonce, []byte(value), []byte("order-contact-phone:v1")), nil
}

func (c *ContactCipher) KeyVersion() int16 { return KeyVersion }

func (c *ContactCipher) Decrypt(ciphertext []byte, version int16) (string, error) {
	if c == nil || c.aead == nil || version != KeyVersion || len(ciphertext) < c.aead.NonceSize()+c.aead.Overhead() {
		return "", errors.New("order contact cipher unavailable")
	}
	nonce := ciphertext[:c.aead.NonceSize()]
	raw, err := c.aead.Open(nil, nonce, ciphertext[c.aead.NonceSize():], []byte("order-contact-phone:v1"))
	if err != nil {
		return "", errors.New("order contact verification failed")
	}
	if len(raw) != 14 || string(raw[:4]) != "+861" {
		return "", errors.New("order contact invalid")
	}
	for _, b := range raw[1:] {
		if b < '0' || b > '9' {
			return "", errors.New("order contact invalid")
		}
	}
	return string(raw), nil
}
