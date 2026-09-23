package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	automationhttp "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/http"
)

func TestAudienceDirectPushAuthenticatorChecksSignatureAndBody(t *testing.T) {
	now := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	key := []byte("01234567890123456789012345678901")
	body := []byte(`{"items":[]}`)
	event := "event-0123456789abcdef"
	timestamp := strconv.FormatInt(now.Unix(), 10)
	request := httptest.NewRequest(http.MethodPost, "/api/automation/audience/webhooks/awh_fixture", nil)
	request.Header.Set(automationhttp.AudienceWebhookClientIDHeader, automationhttp.AudienceWebhookClientID)
	request.Header.Set(automationhttp.AudienceWebhookTimestampHeader, timestamp)
	request.Header.Set(automationhttp.AudienceWebhookEventIDHeader, event)
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n"))
	_, _ = mac.Write(body)
	request.Header.Set(automationhttp.AudienceWebhookSignatureHeader, "sha256="+hex.EncodeToString(mac.Sum(nil)))
	authenticator := &audienceDirectPushAuthenticator{key: key, now: func() time.Time { return now }}
	claim, err := authenticator.AuthenticateAudienceWebhook(context.Background(), request, "awh_fixture", body)
	if err != nil || claim.EventIDDigest == ([32]byte{}) || claim.PayloadDigest == ([32]byte{}) {
		t.Fatalf("valid claim=%+v err=%v", claim, err)
	}
	if _, err = authenticator.AuthenticateAudienceWebhook(context.Background(), request, "awh_fixture", []byte(`{"items":[1]}`)); err == nil {
		t.Fatal("modified body accepted")
	}
}
