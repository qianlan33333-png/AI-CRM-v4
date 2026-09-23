package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLPublicCommerceChromiumJourney uses the real Composition Root,
// PostgreSQL Product facts, anonymous manifest closure, and Chromium. It
// deliberately stops before the checkout action: payments, OAuth, identity
// resolution, and Provider effects belong to their existing owners. The
// separate PostgreSQL response-loss journey remains the payment-recovery
// evidence for an accepted order that loses its initial response.
func TestPostgreSQLPublicCommerceChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newPublicCommerceChromiumFixture(t, 110*time.Second)
	unavailable := seedPublicCommerceUnavailableServicePeriod(t, fixture.ctx, fixture.application)
	trustedSession := issuePublicCommerceTrustedH5Session(t, fixture)
	assertPublicCommerceUnavailableState(t, fixture, unavailable.code, trustedSession.token, "no_entitlement")
	seedPublicCommerceEntitlement(t, fixture, unavailable, trustedSession)
	assertPublicCommerceUnavailableState(t, fixture, unavailable.code, trustedSession.token, "active_entitlement")

	screenshots := t.TempDir()
	if configured := platformconfig.PublicCommerceScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "public_commerce_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_PUBLIC_COMMERCE_TEST_URL="+fixture.server.URL,
		"AICRM_PUBLIC_COMMERCE_STANDARD_CODE=browser-push-product",
		"AICRM_PUBLIC_COMMERCE_SERVICE_CODE=browser-push-service-period",
		"AICRM_PUBLIC_COMMERCE_UNAVAILABLE_SERVICE_CODE="+unavailable.code,
		"AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR="+screenshots,
		"AICRM_PUBLIC_COMMERCE_TRUSTED_SESSION="+paymentport.TrustedSessionCookieName+"="+trustedSession.token,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "public_commerce_chromium: PASS") {
		t.Fatalf("public commerce Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	expirePublicCommerceTrustedSession(t, fixture, trustedSession.token)
	assertPublicCommerceUnavailableState(t, fixture, unavailable.code, trustedSession.token, "expired_session")

	for _, name := range []string{
		"public-standard-detail-375.png",
		"public-standard-detail-375-bottom.png",
		"public-standard-payment-390.png",
		"public-service-available-430.png",
		"public-service-available-payment-390.png",
		"public-service-unavailable-detail-375.png",
		"public-service-unavailable-payment-430.png",
	} {
		info, statErr := os.Stat(filepath.Join(screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("public commerce screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

type publicCommerceUnavailableServicePeriod struct {
	code string
	id   int64
}

type publicCommerceTrustedH5Session struct {
	token           string
	payerCustomerID int64
}

func issuePublicCommerceTrustedH5Session(t *testing.T, fixture *productExternalPushChromiumFixture) publicCommerceTrustedH5Session {
	return issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "public-commerce-browser-h5-session-0001")
}

// issuePublicCommerceTrustedH5SessionWithKey represents a renewed H5 OAuth
// session for the same verified OneID subject. A terminal checkout remains
// readable to the original session while a subsequent service-period purchase
// must use a new, not-yet-consumed session.
func issuePublicCommerceTrustedH5SessionWithKey(t *testing.T, fixture *productExternalPushChromiumFixture, idempotencyKey string) publicCommerceTrustedH5Session {
	t.Helper()
	if fixture == nil || fixture.application == nil || fixture.application.paymentSession == nil {
		t.Fatal("public commerce fixture has no composed Payment session service")
	}
	openID, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-public-commerce-h5",
		Value: "public-commerce-browser-openid", Source: "payment-h5-oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	unionID, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{
		Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:public-commerce-fixture-platform",
		Value: "public-commerce-browser-unionid", Source: "payment-h5-oauth",
	})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := fixture.application.paymentSession.IssueTrusted(fixture.ctx, paymentsession.IssueCommand{
		Fact: openID, UnionID: unionID, IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		t.Fatal(err)
	}
	if issued.Token == "" || issued.PayerCustomerID < 1 || issued.PayerIdentityID < 1 || issued.Channel != paymentdomain.ChannelH5Official {
		t.Fatalf("trusted public commerce H5 session is incomplete: tokenPresent=%t payerCustomerPresent=%t payerIdentityPresent=%t channel=%q", issued.Token != "", issued.PayerCustomerID > 0, issued.PayerIdentityID > 0, issued.Channel)
	}
	return publicCommerceTrustedH5Session{token: issued.Token, payerCustomerID: int64(issued.PayerCustomerID)}
}

func seedPublicCommerceUnavailableServicePeriod(t *testing.T, ctx context.Context, application *composedApplication) publicCommerceUnavailableServicePeriod {
	t.Helper()
	const code = "browser-public-service-unavailable"
	projection := `{"schema_version":1,"status":"service_period_disabled","enabled":false,"buy_button_text":"暂未开放","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	var id int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection)
		VALUES($1,'周期商品暂未开放','真实周期不可用公开页夹具',12800,'CNY',0,1,$2::jsonb) RETURNING id`, code, projection).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,30)`, id); err != nil {
		t.Fatal(err)
	}
	return publicCommerceUnavailableServicePeriod{code: code, id: id}
}

func seedPublicCommerceEntitlement(t *testing.T, fixture *productExternalPushChromiumFixture, unavailable publicCommerceUnavailableServicePeriod, session publicCommerceTrustedH5Session) {
	t.Helper()
	if session.payerCustomerID < 1 || unavailable.id < 1 {
		t.Fatal("public commerce entitlement fixture requires a trusted customer and service product")
	}
	now := time.Now().UTC()
	digest := sha256.Sum256([]byte("public-commerce-unavailable-entitlement"))
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `INSERT INTO order_service_entitlements(source_system,source_key,customer_id,service_product_id,product_name,status,start_at,end_at,remark,source_digest,created_at,updated_at) VALUES('public-commerce-chromium','unavailable-entitlement',$1,$2,'周期商品暂未开放','active',$3,$4,'',$5,$3,$3)`, session.payerCustomerID, unavailable.id, now.Add(-time.Hour), now.AddDate(0, 0, 30), digest[:]); err != nil {
		t.Fatal(err)
	}
}

func expirePublicCommerceTrustedSession(t *testing.T, fixture *productExternalPushChromiumFixture, token string) {
	t.Helper()
	digest := sha256.Sum256([]byte(token))
	if command, err := fixture.application.pool.Native().Exec(fixture.ctx, `UPDATE payment_sessions SET expires_at=created_at + interval '1 microsecond' WHERE token_digest=$1`, digest[:]); err != nil || command.RowsAffected() != 1 {
		t.Fatalf("expire trusted public session changed=%d err=%v", command.RowsAffected(), err)
	}
}

func assertPublicCommerceUnavailableState(t *testing.T, fixture *productExternalPushChromiumFixture, code, token, state string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/s/"+code, nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(recorder, request)
	body := recorder.Body.Bytes()
	if recorder.Code != http.StatusOK || !bytes.Contains(body, []byte(`data-v3-public-commerce`)) || !bytes.Contains(body, []byte(`未上架`)) || bytes.Contains(body, []byte(`"status":"active"`)) {
		t.Fatalf("unavailable public service state=%s status=%d v3=%t unavailable=%t entitlement_leaked=%t", state, recorder.Code, bytes.Contains(body, []byte(`data-v3-public-commerce`)), bytes.Contains(body, []byte(`未上架`)), bytes.Contains(body, []byte(`"status":"active"`)))
	}
}
