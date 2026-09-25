package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type activityIssuerStub struct {
	*publicStub
	calls int
}

func (s *activityIssuerStub) IssuePublicProductActivityContext(_ context.Context, campaignID int64) (string, error) {
	if campaignID != 7 {
		return "", referralport.ErrCampaignUnavailable
	}
	s.calls++
	return "rpa_test_activity_context", nil
}

func TestSignedActivityHandoffPreservesCampaignContext(t *testing.T) {
	issuer := &activityIssuerStub{publicStub: &publicStub{}}
	h := &Handler{public: issuer, activityHandoffKey: []byte(strings.Repeat("k", 32)), allowedOrigins: map[string]struct{}{"https://crm.example.test": {}}, cookieSecure: true}
	raw := "https://crm.example.test/d/dpc_ABCDEFGHIJKLMNOPQRSTUV"
	wrapped, err := h.signedActivityURL(7, raw)
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(u.Path, "/referral/activity/7/dpc_") || len(u.Query().Get("sig")) != 64 {
		t.Fatalf("invalid wrapped link: %s", wrapped)
	}
	response := httptest.NewRecorder()
	h.activityHandoff(response, httptest.NewRequest(http.MethodGet, u.RequestURI(), nil), strings.TrimPrefix(u.Path, "/referral/activity/"))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/d/dpc_ABCDEFGHIJKLMNOPQRSTUV" || issuer.calls != 1 {
		t.Fatalf("handoff status=%d location=%q calls=%d", response.Code, response.Header().Get("Location"), issuer.calls)
	}
	if cookies := response.Result().Cookies(); len(cookies) != 1 || cookies[0].Name != "aicrm_referral_activity_context" || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("activity context cookie not protected: %+v", cookies)
	}
	bad := httptest.NewRecorder()
	tampered := strings.Replace(u.RequestURI(), "/7/", "/8/", 1)
	h.activityHandoff(bad, httptest.NewRequest(http.MethodGet, tampered, nil), strings.TrimPrefix(strings.SplitN(tampered, "?", 2)[0], "/referral/activity/"))
	if bad.Code != http.StatusNotFound || issuer.calls != 1 {
		t.Fatalf("tampered campaign accepted: status=%d calls=%d", bad.Code, issuer.calls)
	}
}
