package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"io"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
)

// ChannelWelcomeMessageCipher derives a separate content key from the
// callback AES input. It uses a domain-separated derivation and deliberately
// does not reuse the welcome-grant key or unrelated Commerce configuration key.
type ChannelWelcomeMessageCipher struct{ aead cipher.AEAD }

func NewChannelWelcomeMessageCipher(secret string) (*ChannelWelcomeMessageCipher, error) {
	if len(secret) < 32 {
		return nil, channelport.ErrWelcomeMessageUnavailable
	}
	key := sha256.Sum256([]byte("aicrm.wecom.channel-welcome-message.v1\x00" + secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, channelport.ErrWelcomeMessageUnavailable
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, channelport.ErrWelcomeMessageUnavailable
	}
	return &ChannelWelcomeMessageCipher{aead: aead}, nil
}

func (ciphertext *ChannelWelcomeMessageCipher) EncryptChannelWelcomeMessage(value, associatedData []byte) ([]byte, int16, error) {
	if ciphertext == nil || ciphertext.aead == nil || len(value) > channelport.MaxWelcomeMessageUTF8Bytes || len(associatedData) == 0 {
		return nil, 0, channelport.ErrWelcomeMessageUnavailable
	}
	nonce := make([]byte, ciphertext.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, 0, err
	}
	return ciphertext.aead.Seal(nonce, nonce, value, associatedData), 1, nil
}

func (ciphertext *ChannelWelcomeMessageCipher) DecryptChannelWelcomeMessage(value []byte, version int16, associatedData []byte) ([]byte, error) {
	if ciphertext == nil || ciphertext.aead == nil || version != 1 || len(associatedData) == 0 || len(value) <= ciphertext.aead.NonceSize() {
		return nil, channelport.ErrWelcomeMessageUnavailable
	}
	plain, err := ciphertext.aead.Open(nil, value[:ciphertext.aead.NonceSize()], value[ciphertext.aead.NonceSize():], associatedData)
	if err != nil {
		return nil, channelport.ErrWelcomeMessageUnavailable
	}
	return plain, nil
}

var _ channelport.WelcomeMessageCipher = (*ChannelWelcomeMessageCipher)(nil)
