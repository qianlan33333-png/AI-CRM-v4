package http

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	segmentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/app"
	segmentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/store"
)

type webhookApplicationStub struct {
	calls int
	fact  segmentapp.VerifiedInboundFact
}

func (s *webhookApplicationStub) Ingest(_ context.Context, f segmentapp.VerifiedInboundFact) (segmentapp.InboundResult, error) {
	s.calls++
	s.fact = f
	return segmentapp.InboundResult{Receipt: segmentstore.WebhookReceipt{ID: 4, Disposition: "resolved", RefreshRunID: 7}}, nil
}
func webhookRequest(secret string, at time.Time, body string) *http.Request {
	event := "event-0001"
	key := "new-users"
	timestamp := at.Format("150405")
	timestamp = strconv.FormatInt(at.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(timestamp + "\n" + event + "\n" + key + "\n" + body))
	r := httptest.NewRequest(http.MethodPost, webhookPathPrefix+key+webhookPathSuffix, strings.NewReader(body))
	r.Header.Set(webhookTimestampHeader, timestamp)
	r.Header.Set(webhookEventIDHeader, event)
	r.Header.Set(webhookSignatureHeader, hex.EncodeToString(mac.Sum(nil)))
	return r
}
func TestAudienceWebhookVerifiesBeforeMintingFact(t *testing.T) {
	secret := strings.Repeat("s", 32)
	app := &webhookApplicationStub{}
	handler, _ := NewWebhookHandler(app, secret)
	now := time.Unix(1800000000, 0).UTC()
	handler.now = func() time.Time { return now }
	body := `{"kind":"wecom_external_userid","scope":"wecom-corp:corp","value":"external-secret","source":"trusted-feed"}`
	request := webhookRequest(secret, now, body)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 202 || app.calls != 1 || !app.fact.Identity.Valid() || strings.Contains(response.Body.String(), "external-secret") {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, app.calls, response.Body.String())
	}
}
func TestAudienceWebhookRejectsBadProofOldTimestampAndOversize(t *testing.T) {
	secret := strings.Repeat("s", 32)
	now := time.Unix(1800000000, 0).UTC()
	for _, tc := range []struct {
		name    string
		request *http.Request
		want    int
	}{{"bad signature", webhookRequest("different-secret-different-secret-00", now, `{}`), 401}, {"old", webhookRequest(secret, now.Add(-6*time.Minute), `{}`), 401}, {"oversize", webhookRequest(secret, now, strings.Repeat("x", webhookMaxBody+1)), 413}} {
		t.Run(tc.name, func(t *testing.T) {
			app := &webhookApplicationStub{}
			handler, _ := NewWebhookHandler(app, secret)
			handler.now = func() time.Time { return now }
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, tc.request)
			if response.Code != tc.want || app.calls != 0 {
				t.Fatalf("status=%d calls=%d", response.Code, app.calls)
			}
		})
	}
}
