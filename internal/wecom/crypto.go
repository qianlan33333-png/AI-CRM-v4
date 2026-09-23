package wecom

import (
	"crypto/aes"
	"crypto/cipher"
	cryptorand "crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const callbackPKCS7BlockSize = 32

// CallbackCryptoOptions exists primarily for deterministic protocol tests.
// Production callers should normally use NewCallbackCrypto, which installs
// the system clock and crypto/rand defaults.
type CallbackCryptoOptions struct {
	Now    func() time.Time
	Random func([]byte) error
	Nonce  func() string
}

// CallbackCrypto validates the WeCom signature and decrypts its callback
// envelope. The AES key is injected; it is never logged or persisted here.
type CallbackCrypto struct {
	token  string
	key    []byte
	corpID string
	now    func() time.Time
	random func([]byte) error
	nonce  func() string
}

func NewCallbackCrypto(token, encodingAESKey, corpID string) (*CallbackCrypto, error) {
	return NewCallbackCryptoWithOptions(token, encodingAESKey, corpID, CallbackCryptoOptions{})
}

func NewCallbackCryptoWithOptions(token, encodingAESKey, corpID string, options CallbackCryptoOptions) (*CallbackCrypto, error) {
	if !validToken(token) || !validCorpID(corpID) {
		return nil, ErrSignature
	}
	decoded, err := base64.StdEncoding.DecodeString(encodingAESKey + "=")
	if err != nil || len(decoded) != 32 {
		return nil, ErrSignature
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	random := options.Random
	if random == nil {
		random = func(value []byte) error {
			_, err := cryptorand.Read(value)
			return err
		}
	}
	nonce := options.Nonce
	if nonce == nil {
		nonce = func() string {
			var value [16]byte
			if _, err := cryptorand.Read(value[:]); err != nil {
				return ""
			}
			return hex.EncodeToString(value[:])
		}
	}
	return &CallbackCrypto{token: token, key: decoded, corpID: corpID, now: now, random: random, nonce: nonce}, nil
}

func (crypto *CallbackCrypto) VerifyAndDecrypt(signature, timestamp, nonce, encrypted string) ([]byte, error) {
	if crypto == nil || !crypto.validTimestamp(timestamp) {
		return nil, ErrCallbackExpired
	}
	if encrypted == "" || len(encrypted) > maxCallbackBody || len(nonce) == 0 || len(nonce) > 128 {
		return nil, ErrMalformedXML
	}
	expected := callbackSignature(crypto.token, timestamp, nonce, encrypted)
	if subtle.ConstantTimeCompare([]byte(expected), []byte(signature)) != 1 {
		return nil, ErrSignature
	}
	ciphertext, err := base64.StdEncoding.DecodeString(encrypted)
	if err != nil || len(ciphertext) == 0 || len(ciphertext)%aes.BlockSize != 0 {
		return nil, ErrMalformedXML
	}
	block, err := aes.NewCipher(crypto.key)
	if err != nil {
		return nil, ErrMalformedXML
	}
	plain := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, crypto.key[:aes.BlockSize]).CryptBlocks(plain, ciphertext)
	plain, err = unpad32(plain)
	if err != nil || len(plain) < 20 {
		return nil, ErrMalformedXML
	}
	messageLength := int(binary.BigEndian.Uint32(plain[16:20]))
	messageEnd := 20 + messageLength
	if messageLength < 1 || messageLength > maxCallbackBody || messageEnd < 20 || messageEnd > len(plain) {
		return nil, ErrMalformedXML
	}
	message := plain[20:messageEnd]
	if subtle.ConstantTimeCompare(plain[messageEnd:], []byte(crypto.corpID)) != 1 {
		return nil, ErrCorpMismatch
	}
	return append([]byte(nil), message...), nil
}

func (crypto *CallbackCrypto) validTimestamp(value string) bool {
	if value == "" || len(value) > 20 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	unix, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return false
	}
	now := time.Now
	if crypto.now != nil {
		now = crypto.now
	}
	delta := now().UTC().Sub(time.Unix(unix, 0).UTC())
	return delta <= 5*time.Minute && delta >= -60*time.Second
}

// Encrypt wraps plaintext in the WeCom AES-256-CBC callback frame and returns
// the base64 ciphertext. It is intentionally kept on the same crypto object
// as VerifyAndDecrypt so the receiveid and key cannot drift between paths.
func (crypto *CallbackCrypto) Encrypt(message []byte) (string, error) {
	if crypto == nil || len(crypto.key) != 32 || !validCorpID(crypto.corpID) || len(message) == 0 || len(message) > maxCallbackBody {
		return "", ErrMalformedXML
	}
	if uint64(len(message))+uint64(len(crypto.corpID))+20 > uint64(^uint32(0)) {
		return "", ErrMalformedXML
	}
	prefix := make([]byte, 16)
	if crypto.random == nil {
		_, err := cryptorand.Read(prefix)
		if err != nil {
			return "", fmt.Errorf("encrypt callback prefix: %w", err)
		}
	} else if err := crypto.random(prefix); err != nil {
		return "", fmt.Errorf("encrypt callback prefix: %w", err)
	}
	payload := make([]byte, 20+len(message)+len(crypto.corpID))
	copy(payload[:16], prefix)
	binary.BigEndian.PutUint32(payload[16:20], uint32(len(message)))
	copy(payload[20:], message)
	copy(payload[20+len(message):], crypto.corpID)
	padding := callbackPKCS7BlockSize - len(payload)%callbackPKCS7BlockSize
	payload = append(payload, bytesOf(padding, byte(padding))...)
	block, err := aes.NewCipher(crypto.key)
	if err != nil {
		return "", fmt.Errorf("encrypt callback frame: %w", err)
	}
	ciphertext := make([]byte, len(payload))
	cipher.NewCBCEncrypter(block, crypto.key[:aes.BlockSize]).CryptBlocks(ciphertext, payload)
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// EncryptedSuccessReply creates the provider-facing POST acknowledgement.
// The returned XML contains a separately signed ciphertext that decrypts to
// exactly the ASCII string "success".
func (crypto *CallbackCrypto) EncryptedSuccessReply() ([]byte, error) {
	if crypto == nil {
		return nil, ErrMalformedXML
	}
	encrypted, err := crypto.Encrypt([]byte("success"))
	if err != nil {
		return nil, err
	}
	now := time.Now
	if crypto.now != nil {
		now = crypto.now
	}
	timestamp := strconv.FormatInt(now().UTC().Unix(), 10)
	nonce := ""
	if crypto.nonce != nil {
		nonce = crypto.nonce()
	}
	if !validReplyNonce(nonce) {
		return nil, ErrMalformedXML
	}
	signature := callbackSignature(crypto.token, timestamp, nonce, encrypted)
	// All values except the caller-injected nonce are generated base64/hex.
	// Escape the nonce anyway so a deterministic test callback cannot turn the
	// acknowledgement into malformed XML.
	var escapedNonce strings.Builder
	if err := writeXMLEscaped(&escapedNonce, nonce); err != nil {
		return nil, ErrMalformedXML
	}
	return []byte(fmt.Sprintf("<xml><Encrypt><![CDATA[%s]]></Encrypt><MsgSignature><![CDATA[%s]]></MsgSignature><TimeStamp>%s</TimeStamp><Nonce>%s</Nonce></xml>", encrypted, signature, timestamp, escapedNonce.String())), nil
}

func validReplyNonce(value string) bool {
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 && character != '\t' {
			return false
		}
	}
	return true
}

func writeXMLEscaped(builder *strings.Builder, value string) error {
	for _, character := range value {
		switch character {
		case '&':
			builder.WriteString("&amp;")
		case '<':
			builder.WriteString("&lt;")
		case '>':
			builder.WriteString("&gt;")
		case '\'':
			builder.WriteString("&#39;")
		case '"':
			builder.WriteString("&#34;")
		default:
			builder.WriteRune(character)
		}
	}
	return nil
}

func callbackSignature(token, timestamp, nonce, encrypted string) string {
	parts := []string{token, timestamp, nonce, encrypted}
	sort.Strings(parts)
	sum := sha1.Sum([]byte(strings.Join(parts, "")))
	return hex.EncodeToString(sum[:])
}

func unpad32(value []byte) ([]byte, error) {
	if len(value) == 0 || len(value)%aes.BlockSize != 0 {
		return nil, errors.New("empty padded input")
	}
	padding := int(value[len(value)-1])
	if padding < 1 || padding > callbackPKCS7BlockSize || padding > len(value) {
		return nil, errors.New("invalid padding")
	}
	var invalid byte
	for _, item := range value[len(value)-padding:] {
		invalid |= item ^ byte(padding)
	}
	if invalid != 0 {
		return nil, errors.New("invalid padding")
	}
	return value[:len(value)-padding], nil
}

func bytesOf(length int, value byte) []byte {
	result := make([]byte, length)
	for index := range result {
		result[index] = value
	}
	return result
}
