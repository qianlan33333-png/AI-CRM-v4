package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestApplicationRouterSeparatesH5AndAdminOrigins(t *testing.T) {
	const admin = "https://admin.example"
	const h5 = "https://h5.example"
	marker := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler, err := routeApplicationWithProductsCouponsGroupOpsAutomationAndCycles(
		marker, marker, marker, marker, marker, marker, marker, marker,
		marker, marker, marker, marker, marker, marker, marker, marker,
		marker, marker, marker, marker, marker, marker, marker, marker,
		&fakeAccessAuthentication{}, admin, h5,
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path, origin string
		want         int
	}{
		{"/api/public/questionnaires/example/submissions", h5, http.StatusNoContent},
		{"/api/public/survey-submission-results/query", h5, http.StatusNoContent},
		{"/api/v1/wechat-pay/checkouts", h5, http.StatusNoContent},
		{"/api/v1/wechat-pay/checkouts/", h5, http.StatusNoContent},
		{"/api/public/questionnaires/example/submissions", admin, http.StatusForbidden},
		{"/api/v1/wechat-pay/checkouts", "https://evil.example", http.StatusForbidden},
		{"/api/public/questionnaires/example/submissions", "null", http.StatusForbidden},
		{"/api/admin/questionnaires", h5, http.StatusForbidden},
		{"/api/admin/refunds", h5, http.StatusForbidden},
		{"/api/admin/wechat-pay/payments/7/abandon-checkout", h5, http.StatusForbidden},
		{"/api/admin/wechat-pay/payments/7/abandon-checkout", admin, http.StatusNoContent},
		{"/api/admin/wechat-pay/payments/7/allow-checkout-restart", admin, http.StatusNoContent},
		{"/api/admin/wechat-pay/payments/7/allow-checkout-restart", h5, http.StatusForbidden},
		{"/api/v1/wechat-pay/sessions", h5, http.StatusForbidden},
		{"/api/public/questionnaires/example/admin/submissions", h5, http.StatusForbidden},
		{"/api/admin/questionnaires", admin, http.StatusNoContent},
	} {
		t.Run(test.path+test.origin, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, h5+test.path, nil)
			r.Header.Set("Origin", test.origin)
			r.Header.Set("Sec-Fetch-Site", "same-origin")
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, r)
			if w.Code != test.want {
				t.Fatalf("status=%d want=%d body=%s", w.Code, test.want, w.Body.String())
			}
		})
	}
}

func TestH5EntryRedirectPreservesPathAndQueryOnlyOnConfiguredOldHost(t *testing.T) {
	marker := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := redirectH5EntryOrigin(marker, "https://admin.example", "https://h5.example")
	for _, path := range []string{"/h5/auth.html?slug=launch0908-basic-survey", "/q/example", "/p/7", "/pay/example?coupon=demo", "/s/example", "/s/example/pay", "/c/demo"} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://admin.example"+path, nil))
		if w.Code != http.StatusTemporaryRedirect || w.Header().Get("Location") != "https://h5.example"+path {
			t.Fatalf("path=%s status=%d redirect=%q", path, w.Code, w.Header().Get("Location"))
		}
	}
	for _, target := range []string{
		"https://h5.example/h5/auth.html?slug=example",
		"https://evil.example/h5/auth.html?slug=example",
		"https://admin.example/admin/orders",
		"https://admin.example/api/h5/surveys/oauth/callback?code=example&state=example",
		"https://admin.example/api/h5/wechat-pay/oauth/callback?code=example&state=example",
		"https://admin.example/api/h5/surveys/oauth/start?slug=example",
		"https://admin.example/p/example/extra",
		"https://admin.example/p/example/pay",
		"https://admin.example/s/example/extra",
		"https://admin.example/s/example/pay/extra",
		"https://admin.example/s/../pay",
		"https://admin.example/s//pay",
	} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, target, nil))
		if w.Code != http.StatusNoContent || w.Header().Get("Location") != "" {
			t.Fatalf("unexpected redirect for %s: %d %q", target, w.Code, w.Header().Get("Location"))
		}
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "https://admin.example/pay/example", nil))
	if w.Code != http.StatusNoContent {
		t.Fatalf("POST redirected: %d", w.Code)
	}
}
