package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMountReferralSeparatesPublicAndAdminPrefixes(t *testing.T) {
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusAccepted) })
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := mountReferral(fallback, public, admin)

	for path, expected := range map[string]int{
		"/referral/invite/rfi_" + "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA": http.StatusNoContent,
		"/referral/activity/7/dpc_ABCDEFGHIJKLMNOPQRSTUV":                       http.StatusNoContent,
		"/api/v1/referral/campaigns/7/posters/1":                                http.StatusNoContent,
		"/api/admin/referral/campaigns/7/posters":                               http.StatusAccepted,
		"/api/v1/referral/campaigns":                                            http.StatusNoContent,
		"/api/admin/referral/campaigns":                                         http.StatusAccepted,
		"/referral":                                                             http.StatusTeapot,
		"/api/v1/referrals":                                                     http.StatusTeapot,
		"/r/rd_remaining_pages123":                                              http.StatusTeapot,
		"/r/not-a-referral-token":                                               http.StatusTeapot,
		"/referral/invite/rfi_not-a-valid-token":                                http.StatusNoContent,
		"/r/rfi_not-a-valid-token":                                              http.StatusTeapot,
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != expected {
			t.Fatalf("path=%s status=%d want=%d", path, response.Code, expected)
		}
	}
}

func TestMountReferralFailsClosedWhenAHandlerIsAbsent(t *testing.T) {
	response := httptest.NewRecorder()
	mountReferral(http.NotFoundHandler(), nil, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns", nil))
	if response.Code != http.StatusServiceUnavailable || response.Body.String() != "{\"error\":\"referral_unavailable\"}" {
		t.Fatalf("status=%d body=%q", response.Code, response.Body.String())
	}
}

func TestMountSecuredReferralAddsHostSecurityHeaders(t *testing.T) {
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := mountSecuredReferral(http.NotFoundHandler(), public, admin, "https://crm.example.test", "https://h5.example.test")

	for _, path := range []string{
		"/referral/invite/rfi_" + strings.Repeat("A", 43),
		"/referral/activity/7/dpc_ABCDEFGHIJKLMNOPQRSTUV",
		"/api/v1/referral/campaigns",
		"/api/admin/referral/campaigns",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNoContent {
			t.Fatalf("path=%s status=%d want=%d", path, response.Code, http.StatusNoContent)
		}
		if got := response.Header().Get("Strict-Transport-Security"); got != "max-age=31536000; includeSubDomains" {
			t.Fatalf("path=%s HSTS=%q", path, got)
		}
		if got := response.Header().Get("X-Content-Type-Options"); got != "nosniff" {
			t.Fatalf("path=%s X-Content-Type-Options=%q", path, got)
		}
		if got := response.Header().Get("Referrer-Policy"); got != "no-referrer" {
			t.Fatalf("path=%s Referrer-Policy=%q", path, got)
		}
		if got := response.Header().Get("Content-Security-Policy"); !strings.Contains(got, "form-action 'self'") {
			t.Fatalf("path=%s CSP=%q", path, got)
		}
	}
}

func TestMountSecuredReferralRejectsCrossSiteWritesWithoutChangingFallback(t *testing.T) {
	var publicWrites, adminWrites int
	public := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		publicWrites++
		w.WriteHeader(http.StatusNoContent)
	})
	admin := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		adminWrites++
		w.WriteHeader(http.StatusNoContent)
	})
	fallback := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	handler := mountSecuredReferral(fallback, public, admin, "https://crm.example.test", "https://h5.example.test")

	for _, path := range []string{"/api/v1/referral/campaigns/1/join", "/api/admin/referral/campaigns"} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.Header.Set("Origin", "https://attacker.example.test")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden || response.Body.String() != `{"ok":false,"error":"cross_site_request"}` {
			t.Fatalf("path=%s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("path=%s cross-site rejection lost security headers", path)
		}
	}
	if publicWrites != 0 || adminWrites != 0 {
		t.Fatalf("cross-site writes reached referral handlers: public=%d admin=%d", publicWrites, adminWrites)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/1/participations", nil)
	request.Header.Set("Origin", "https://crm.example.test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || publicWrites != 1 {
		t.Fatalf("same-origin public write status=%d public=%d", response.Code, publicWrites)
	}
	request = httptest.NewRequest(http.MethodPost, "/api/v1/referral/campaigns/1/participations", nil)
	request.Header.Set("Origin", "https://h5.example.test")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent || publicWrites != 2 {
		t.Fatalf("configured H5 public write status=%d public=%d", response.Code, publicWrites)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/r/existing-radar-code", nil))
	if response.Code != http.StatusTeapot {
		t.Fatalf("fallback status=%d want=%d", response.Code, http.StatusTeapot)
	}
	if response.Header().Get("X-Content-Type-Options") != "" {
		t.Fatal("Referral security wrapper changed a non-Referral fallback route")
	}
}

func TestMountSecuredReferralFailsClosedWhenAHandlerIsAbsent(t *testing.T) {
	handler := mountSecuredReferral(http.NotFoundHandler(), nil, nil, "https://crm.example.test")
	for _, path := range []string{"/api/v1/referral/campaigns", "/api/admin/referral/campaigns"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || response.Body.String() != `{"error":"referral_unavailable"}` {
			t.Fatalf("path=%s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if response.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("path=%s unavailable route lost security headers", path)
		}
	}
}
