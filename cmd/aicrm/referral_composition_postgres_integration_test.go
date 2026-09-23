package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestPostgreSQLReferralCompositionDoesNotRequireDistributionPaymentCapability(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg := platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://referral-composition.example.test",
		ReleaseSHA:   "referral-without-distribution-payment",
		WorkerOwner:  "referral-without-distribution-payment",
		WorkerLimit:  1,
		Referral:     platformconfig.Referral{TokenDataKey: dataKey},
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "referral-without-distribution-payment-webhook"},
		Survey:       platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey},
		// The scoped identity metadata is sufficient to validate an existing
		// trusted Payment session. Payment itself remains disabled, so no
		// Distribution registration, checkout, or settlement capability is
		// composed.
		WeChatPay: platformconfig.WeChatPay{AppID: "wx-referral-scope", AppScope: "wechat-app:referral-scope"},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "referral-owner", Password: "referral-owner-password", DisplayName: "活动管理员"},
	}
	application, err := compose(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, cfg.Bootstrap); err != nil {
		t.Fatal(err)
	}

	public := httptest.NewRecorder()
	application.handler.ServeHTTP(public, httptest.NewRequest(http.MethodGet, "/api/v1/referral/campaigns", nil))
	if public.Code != http.StatusOK {
		t.Fatalf("Referral public read status=%d body=%s", public.Code, public.Body.String())
	}

	session, csrf := adminAccessLogin(t, application.handler, cfg.Bootstrap.Username, cfg.Bootstrap.Password)
	startsAt := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	endsAt := time.Now().UTC().Add(25 * time.Hour).Format(time.RFC3339)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/referral/campaigns", strings.NewReader(`{"name":"独立裂变活动","starts_at":"`+startsAt+`","ends_at":"`+endsAt+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", cfg.PublicOrigin)
	request.Header.Set("Idempotency-Key", "referral-without-distribution-payment-create")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	created := httptest.NewRecorder()
	application.handler.ServeHTTP(created, request)
	if created.Code != http.StatusCreated {
		t.Fatalf("Referral admin create status=%d body=%s", created.Code, created.Body.String())
	}

	distribution := httptest.NewRecorder()
	application.handler.ServeHTTP(distribution, httptest.NewRequest(http.MethodGet, "/api/v1/distribution/agreement", nil))
	if distribution.Code != http.StatusServiceUnavailable {
		t.Fatalf("Distribution unexpectedly available with payment disabled: status=%d body=%s", distribution.Code, distribution.Body.String())
	}

	bridge := httptest.NewRequest(http.MethodPost, "/api/v1/referral/session/bridge", nil)
	bridge.Header.Set("Origin", cfg.PublicOrigin)
	bridge.AddCookie(&http.Cookie{Name: "aicrm_payment_session", Value: "payment_" + strings.Repeat("x", 32)})
	bridgeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(bridgeResponse, bridge)
	if bridgeResponse.Code != http.StatusUnauthorized {
		t.Fatalf("untrusted Payment cookie bridged with payment disabled: status=%d body=%s", bridgeResponse.Code, bridgeResponse.Body.String())
	}
}
