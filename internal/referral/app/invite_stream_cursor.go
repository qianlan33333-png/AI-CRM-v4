package app

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

const inviteStreamPrefix = "ris1_"

type inviteStreamCursor struct {
	Watermark int64
	Total     int64
	At        time.Time
	ID        int64
}

func (s *Service) inviteStreamCipher() (cipher.AEAD, error) {
	key := sha256.Sum256(append(append([]byte(nil), s.tokens.key[:]...), []byte("referral/invite-stream/v1")...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func inviteStreamScope(actorID, campaignID int64) []byte {
	return []byte(fmt.Sprintf("referral/invite-stream/v1/%d/%d", actorID, campaignID))
}
func (s *Service) readInviteStreamCursor(value string, actorID, campaignID int64) (*inviteStreamCursor, error) {
	if value == "" {
		return nil, nil
	}
	if !strings.HasPrefix(value, inviteStreamPrefix) {
		return nil, referralport.ErrConflict
	}
	body, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, inviteStreamPrefix))
	if err != nil {
		return nil, referralport.ErrConflict
	}
	aead, err := s.inviteStreamCipher()
	if err != nil {
		return nil, referralport.ErrUnavailable
	}
	if len(body) < aead.NonceSize()+aead.Overhead() {
		return nil, referralport.ErrConflict
	}
	plain, err := aead.Open(nil, body[:aead.NonceSize()], body[aead.NonceSize():], inviteStreamScope(actorID, campaignID))
	if err != nil {
		return nil, referralport.ErrConflict
	}
	var position inviteStreamCursor
	if json.Unmarshal(plain, &position) != nil || position.Watermark < 1 || position.Total < 1 || position.ID < 1 || position.At.IsZero() {
		return nil, referralport.ErrConflict
	}
	return &position, nil
}
func (s *Service) writeInviteStreamCursor(value inviteStreamCursor, actorID, campaignID int64) (string, error) {
	aead, err := s.inviteStreamCipher()
	if err != nil {
		return "", referralport.ErrUnavailable
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return "", referralport.ErrUnavailable
	}
	plain, err := json.Marshal(value)
	if err != nil {
		return "", referralport.ErrUnavailable
	}
	body := aead.Seal(nonce, nonce, plain, inviteStreamScope(actorID, campaignID))
	return inviteStreamPrefix + base64.RawURLEncoding.EncodeToString(body), nil
}
func (s *Service) inviteEntryKey(actorID, campaignID, entryID int64) string {
	mac := hmac.New(sha256.New, s.tokens.key[:])
	_, _ = mac.Write([]byte(fmt.Sprintf("referral/invite-entry/v1/%d/%d/%d", actorID, campaignID, entryID)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
