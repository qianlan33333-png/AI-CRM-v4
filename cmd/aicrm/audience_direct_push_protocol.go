package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"strconv"
	"strings"
	"time"

	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
)

type audienceDirectPushAuthenticator struct {
	key []byte
	now func() time.Time
}

func (a *audienceDirectPushAuthenticator) AuthenticateAudienceWebhook(ctx context.Context, request *http.Request, reference string, body []byte) (automationhttp.AudienceProtocolClaim, error) {
	_ = ctx
	if a == nil || request == nil || request.URL == nil || a.now == nil || len(a.key) < 32 || !validAudienceProtocolOpaque(reference) {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnavailable
	}
	client, ok := singleAudienceHeader(request, automationhttp.AudienceWebhookClientIDHeader)
	if !ok || client != automationhttp.AudienceWebhookClientID {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	timestamp, ok := singleAudienceHeader(request, automationhttp.AudienceWebhookTimestampHeader)
	if !ok {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	event, ok := singleAudienceHeader(request, automationhttp.AudienceWebhookEventIDHeader)
	if !ok || len(event) < 16 || len(event) > 256 || !validAudienceProtocolEvent(event) {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	signature, ok := singleAudienceHeader(request, automationhttp.AudienceWebhookSignatureHeader)
	if !ok {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || timestamp != strconv.FormatInt(seconds, 10) {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	now := a.now().UTC()
	signedAt := time.Unix(seconds, 0).UTC()
	if now.Sub(signedAt) > 5*time.Minute || signedAt.Sub(now) > time.Minute {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	encoded := strings.TrimPrefix(signature, "sha256=")
	decoded, err := hex.DecodeString(encoded)
	if err != nil || len(decoded) != sha256.Size || encoded != strings.ToLower(encoded) {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	mac := hmac.New(sha256.New, a.key)
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n"))
	_, _ = mac.Write(body)
	if !hmac.Equal(decoded, mac.Sum(nil)) {
		return automationhttp.AudienceProtocolClaim{}, automationhttp.ErrAudienceProtocolUnauthorized
	}
	return automationhttp.AudienceProtocolClaim{EventIDDigest: sha256.Sum256([]byte(event)), PayloadDigest: automationhttp.AudiencePayloadDigest(reference, body)}, nil
}

func singleAudienceHeader(request *http.Request, name string) (string, bool) {
	values := request.Header.Values(name)
	if len(values) != 1 || values[0] == "" || strings.TrimSpace(values[0]) != values[0] {
		return "", false
	}
	return values[0], true
}

func validAudienceProtocolEvent(value string) bool {
	for _, character := range value {
		if character < 0x21 || character > 0x7e {
			return false
		}
	}
	return true
}

func validAudienceProtocolOpaque(value string) bool {
	if value == "" || len(value) > 128 || value != strings.TrimSpace(value) {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return true
}

var _ automationhttp.AudienceProtocolAuthenticator = (*audienceDirectPushAuthenticator)(nil)
