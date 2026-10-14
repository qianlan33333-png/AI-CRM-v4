package http

import (
	"context"
	"encoding/json"
	"errors"
	d "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/domain"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/referral/port"
	"net/http"
	"net/http/httptest"
	"testing"
)

type activityLinkAdminStub struct{ adminStub }

func (activityLinkAdminStub) ListAdminCampaigns(context.Context, string, int32) (p.AdminCampaignPage, error) {
	return p.AdminCampaignPage{Items: []p.CampaignSummary{{Campaign: d.Campaign{ID: 5, Name: "活动A", State: d.CampaignActive}}, {Campaign: d.Campaign{ID: 8, Name: "活动B", State: d.CampaignDraft}}}}, nil
}
func (activityLinkAdminStub) ReadAdminCampaign(_ context.Context, id int64) (p.CampaignView, error) {
	return p.CampaignView{CampaignSummary: p.CampaignSummary{Campaign: d.Campaign{ID: id, Name: "活动A", State: d.CampaignActive}}}, nil
}
func activityLinkConfig(origin string) Config {
	return Config{Public: &publicStub{}, Admin: activityLinkAdminStub{}, Sessions: sessionStub{}, Bridge: bridgeStub{}, Names: namesStub{}, Security: securityStub{}, CookieSecure: true, AllowedOrigins: []string{"https://admin.example.test", "https://h5.example.test"}, PublicActivityOrigin: origin, SessionCookieName: "dist", CSRFCookieName: "csrf", CSRFHeader: "X-CSRF"}
}
func TestAdminActivityLinksUseH5OriginAndRetainPublicContract(t *testing.T) {
	h, err := NewHandler(activityLinkConfig("https://h5.example.test"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/admin/referral/campaigns", "/api/admin/referral/campaigns/5"} {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Host = "spoof.invalid"
		r.Header.Set("X-Forwarded-Host", "spoof.invalid")
		w := httptest.NewRecorder()
		h.ServeAdminHTTP(w, r)
		if w.Code != 200 {
			t.Fatal(w.Code, w.Body.String())
		}
		var value map[string]any
		if err = json.Unmarshal(w.Body.Bytes(), &value); err != nil {
			t.Fatal(err)
		}
		if path == "/api/admin/referral/campaigns" {
			items := value["items"].([]any)
			for i, id := range []string{"5", "8"} {
				if items[i].(map[string]any)["activity_url"] != "https://h5.example.test/referral?campaign="+id {
					t.Fatal(items)
				}
			}
		} else if value["activity_url"] != "https://h5.example.test/referral?campaign=5" {
			t.Fatal(value)
		}
	}
	public := map[string]any{}
	h.addPublicCampaignLinks(context.Background(), public, d.Campaign{ID: 5})
	if public["activity_url"] != "/referral?campaign=5" {
		t.Fatal("public relative contract changed", public)
	}
	h.security = securityStub{err: errors.New("not signed in")}
	w := httptest.NewRecorder()
	h.ServeAdminHTTP(w, httptest.NewRequest(http.MethodGet, "/api/admin/referral/campaigns/5", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatal(w.Code)
	}
}
func TestActivityOriginMustBeConfiguredAndAllowed(t *testing.T) {
	for _, origin := range []string{"https://spoof.invalid", "javascript:alert(1)", "https://h5.example.test/referral", "https://h5.example.test?campaign=5"} {
		if _, err := NewHandler(activityLinkConfig(origin)); err == nil {
			t.Fatal("untrusted origin accepted", origin)
		}
	}
	h, err := NewHandler(activityLinkConfig(""))
	if err != nil || h.publicActivityOrigin != "https://admin.example.test" {
		t.Fatal("legacy constructor origin changed", err)
	}
}
