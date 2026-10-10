package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	referraldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	referralport "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
)

type activityIssuerStub struct {
	*publicStub
	calls int
}

func (s *activityIssuerStub) IssuePublicProductActivityContext(_ context.Context, campaignID int64) (referralport.ProductActivityContextCredential, error) {
	if campaignID != 7 {
		return referralport.ProductActivityContextCredential{}, referralport.ErrCampaignUnavailable
	}
	s.calls++
	return referralport.ProductActivityContextCredential{Token: "rpa_test_activity_context", ExpiresAt: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)}, nil
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
	cookies := response.Result().Cookies()
	wantExpiry := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	if len(cookies) != 1 || cookies[0].Name != "aicrm_referral_activity_context" || !cookies[0].HttpOnly || !cookies[0].Secure || !cookies[0].Expires.Equal(wantExpiry) {
		t.Fatalf("activity context cookie not protected: %+v", cookies)
	}
	bad := httptest.NewRecorder()
	tampered := strings.Replace(u.RequestURI(), "/7/", "/8/", 1)
	h.activityHandoff(bad, httptest.NewRequest(http.MethodGet, tampered, nil), strings.TrimPrefix(strings.SplitN(tampered, "?", 2)[0], "/referral/activity/"))
	if bad.Code != http.StatusNotFound || issuer.calls != 1 {
		t.Fatalf("tampered campaign accepted: status=%d calls=%d", bad.Code, issuer.calls)
	}
	badSignature := httptest.NewRecorder()
	badSignatureURL := u
	signature := badSignatureURL.Query().Get("sig")
	last := byte('0')
	if signature[len(signature)-1] == '0' {
		last = '1'
	}
	badSignatureURL.RawQuery = "sig=" + signature[:len(signature)-1] + string(last)
	h.activityHandoff(badSignature, httptest.NewRequest(http.MethodGet, badSignatureURL.RequestURI(), nil), strings.TrimPrefix(badSignatureURL.Path, "/referral/activity/"))
	if badSignature.Code != http.StatusNotFound || issuer.calls != 1 {
		t.Fatalf("tampered signature accepted: status=%d calls=%d", badSignature.Code, issuer.calls)
	}
}

type paidInvitationPreviewStub struct {
	*publicStub
	campaign referraldomain.Campaign
	err      error
}

func (s paidInvitationPreviewStub) PreviewInvitation(context.Context, string) (referralport.InvitationPreview, error) {
	return referralport.InvitationPreview{Campaign: referralport.CampaignSummary{Campaign: s.campaign}, ExpiresAt: time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)}, s.err
}

func TestPaidInvitationHandoffSetsOpaqueActivityCookieAndKeepsLegacyFreeRoute(t *testing.T) {
	token := "rfi_" + strings.Repeat("A", 43)
	for _, kind := range []struct{ productType, prefix string }{{"standard_product", "/p/"}, {"service_period", "/s/"}} {
		t.Run(kind.productType, func(t *testing.T) {
			h := &Handler{public: paidInvitationPreviewStub{publicStub: &publicStub{}, campaign: referraldomain.Campaign{ID: 7, QualificationMode: referraldomain.QualificationProductPurchase, ProductID: 42, ProductType: kind.productType}}, productTargets: productTargetStub{item: productport.ProductOption{Code: "paid-product"}}, cookieSecure: true}
			response := httptest.NewRecorder()
			h.invitationHandoff(response, httptest.NewRequest(http.MethodGet, "/referral/invite/"+token, nil), token)
			cookies := response.Result().Cookies()
			if response.Code != http.StatusSeeOther || response.Header().Get("Location") != kind.prefix+"paid-product" || len(cookies) != 1 || cookies[0].Value != token || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("handoff=%d location=%q cookies=%+v", response.Code, response.Header().Get("Location"), cookies)
			}
			h.public = paidInvitationPreviewStub{publicStub: &publicStub{}, err: referralport.ErrInvitationInvalid}
			rejected := httptest.NewRecorder()
			h.invitationHandoff(rejected, httptest.NewRequest(http.MethodGet, "/referral/invite/"+token, nil), token)
			if rejected.Code == http.StatusSeeOther || len(rejected.Result().Cookies()) != 0 {
				t.Fatal("invalid invitation created activity authority")
			}
		})
	}
	h := &Handler{public: &publicStub{}}
	response := httptest.NewRecorder()
	h.invitationHandoff(response, httptest.NewRequest(http.MethodGet, "/referral/invite/"+token, nil), token)
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/referral?campaign=7&invite="+token || len(response.Result().Cookies()) != 0 {
		t.Fatalf("free handoff=%d %s", response.Code, response.Header().Get("Location"))
	}
}
