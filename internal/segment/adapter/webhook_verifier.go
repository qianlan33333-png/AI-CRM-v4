package adapter

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"strings"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
)

var ErrInvalidWebhookProof = errors.New("invalid audience webhook proof")
var ErrInvalidWebhookFact = errors.New("invalid audience webhook fact")

type WebhookVerifier struct{ secret []byte }

func NewWebhookVerifier(secret string) (WebhookVerifier, error) {
	if secret != "" && (len(secret) < 32 || len(secret) > 4096 || strings.TrimSpace(secret) != secret) {
		return WebhookVerifier{}, ErrInvalidWebhookProof
	}
	return WebhookVerifier{[]byte(secret)}, nil
}
func (v WebhookVerifier) Configured() bool { return len(v.secret) >= 32 }
func (v WebhookVerifier) Verify(packageKey, eventID, timestampText, signatureText string, body []byte, now time.Time) (segmentapp.VerifiedInboundFact, error) {
	if !v.Configured() || len(eventID) < 1 || len(eventID) > 200 || strings.TrimSpace(eventID) != eventID {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookProof
	}
	seconds, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookProof
	}
	occurred := time.Unix(seconds, 0).UTC()
	delta := now.UTC().Sub(occurred)
	if delta < -5*time.Minute || delta > 5*time.Minute {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookProof
	}
	signature, err := hex.DecodeString(signatureText)
	if err != nil || len(signature) != sha256.Size {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookProof
	}
	mac := hmac.New(sha256.New, v.secret)
	_, _ = mac.Write([]byte(timestampText + "\n" + eventID + "\n" + packageKey + "\n"))
	_, _ = mac.Write(body)
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookProof
	}
	var input struct {
		Kind   identitydomain.Kind `json:"kind"`
		Scope  string              `json:"scope"`
		Value  string              `json:"value"`
		Source string              `json:"source"`
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookFact
	}
	var trailing any
	if err = decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookFact
	}
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: input.Kind, Scope: input.Scope, Value: input.Value, Source: input.Source})
	if err != nil {
		return segmentapp.VerifiedInboundFact{}, ErrInvalidWebhookFact
	}
	return segmentapp.VerifiedInboundFact{PackageKey: packageKey, EventID: eventID, PayloadDigest: sha256.Sum256(body), Identity: fact, OccurredAt: occurred}, nil
}
