// Package secure owns cryptographic handling for declared phone identities.
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

	"golang.org/x/crypto/hkdf"
)

const PhoneKeyVersion int16 = 1

type PhoneVault struct {
	aead      cipher.AEAD
	lookupKey [32]byte
}

func NewPhoneVault(encoded string) (*PhoneVault, error) {
	master, err := base64.RawStdEncoding.DecodeString(encoded)
	if err != nil || len(master) != 32 {
		return nil, errors.New("identity phone data key must be 32 raw-base64 bytes")
	}
	derive := func(info string) ([]byte, error) {
		value := make([]byte, 32)
		_, readErr := io.ReadFull(hkdf.New(sha256.New, master, nil, []byte(info)), value)
		return value, readErr
	}
	encryptionKey, err := derive("aicrm/identity/phone/aes-gcm/v1")
	if err != nil {
		return nil, err
	}
	lookupKey, err := derive("aicrm/identity/phone/hmac-sha256/v1")
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(encryptionKey)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	vault := &PhoneVault{aead: aead}
	copy(vault.lookupKey[:], lookupKey)
	return vault, nil
}

func (v *PhoneVault) Encrypt(phone string) ([]byte, error) {
	if v == nil || v.aead == nil {
		return nil, errors.New("phone vault unavailable")
	}
	nonce := make([]byte, v.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return v.aead.Seal(nonce, nonce, []byte(phone), []byte("phone:cn11:v1")), nil
}

func (v *PhoneVault) Decrypt(ciphertext []byte) (string, error) {
	if v == nil || v.aead == nil || len(ciphertext) < v.aead.NonceSize()+v.aead.Overhead() {
		return "", errors.New("invalid phone ciphertext")
	}
	nonce := ciphertext[:v.aead.NonceSize()]
	plain, err := v.aead.Open(nil, nonce, ciphertext[v.aead.NonceSize():], []byte("phone:cn11:v1"))
	if err != nil {
		return "", errors.New("invalid phone ciphertext")
	}
	return string(plain), nil
}

func (v *PhoneVault) LookupDigest(phone string) [32]byte {
	mac := hmac.New(sha256.New, v.lookupKey[:])
	_, _ = mac.Write([]byte("phone:cn11\x00" + phone))
	var digest [32]byte
	copy(digest[:], mac.Sum(nil))
	return digest
}

func MaskPhone(phone string) string {
	if len(phone) != 11 {
		return "***"
	}
	return phone[:3] + "****" + phone[7:]
}
