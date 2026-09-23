package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	paymentapp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/app"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLPaymentActionsPublicCheckoutChromiumJourney opens actual paid
// public pages in Chromium after the composed Payment callback settles them.
// It checks H5 navigation, the same-origin URL Link resolver's 302, and two
// page refreshes with no checkout POST. The non-browser companion test retains
// the deterministic PostgreSQL assertions for the frozen action rows.
func TestPostgreSQLPaymentActionsPublicCheckoutChromiumJourney(t *testing.T) {
	if goruntime.GOOS == "darwin" && !platformconfig.ProductExternalPushDarwinChromiumDiagnosticAllowed() {
		t.Skip("Chromium CDP journey requires Linux CI; set the explicit Darwin diagnostic switch locally")
	}
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newPublicCommerceChromiumFixture(t, 110*time.Second)
	h5Session := issuePublicCommerceTrustedH5Session(t, fixture)
	service, ok := fixture.application.paymentDistribution.(*paymentapp.Service)
	if !ok || service == nil {
		t.Fatal("composition did not retain the native Payment application")
	}
	const checkoutH5URL = "https://after.example.test/frozen-h5"
	const paidH5URL = "https://after.example.test/edited-after-checkout"
	setPublicCheckoutActionProjection(t, fixture, fixture.productID, legacyRedirectProjection(false, "h5", checkoutH5URL, "", "", ""))
	h5Merchant := createPublicCheckoutForCompletionAction(t, fixture, h5Session.token, fixture.productID, "standard", "payment-actions-public-browser-h5-create")
	setPublicCheckoutActionProjection(t, fixture, fixture.productID, legacyRedirectProjection(false, "h5", paidH5URL, "", "", ""))
	settlePublicCheckoutForCompletionAction(t, fixture, service, h5Merchant, 9900, "payment-actions-public-browser-h5")
	setPublicCheckoutActionProjection(t, fixture, fixture.productID, legacyRedirectProjection(false, "h5", "https://after.example.test/edited-after-payment", "", "", ""))
	assertPublicPurchaseStatusAction(t, fixture, h5Session.token, fixture.productID, "standard", paidH5URL)

	linkSession := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "payment-actions-public-browser-link-session")
	const fallbackURL = "https://after.example.test/url-link-fallback"
	setPublicCheckoutActionProjection(t, fixture, fixture.serviceProductID, legacyRedirectProjection(true, "url_link", "", "https://source.invalid/legacy-url-link", "result.destination", fallbackURL))
	linkMerchant := createPublicCheckoutForCompletionAction(t, fixture, linkSession.token, fixture.serviceProductID, "service_period", "payment-actions-public-browser-link-create")
	settlePublicCheckoutForCompletionAction(t, fixture, service, linkMerchant, 12800, "payment-actions-public-browser-link")
	setPublicCheckoutActionProjection(t, fixture, fixture.serviceProductID, legacyRedirectProjection(true, "h5", "https://after.example.test/edited-service", "", "", ""))
	linkBinding := publicCheckoutBindingForPaymentActions(t, fixture, linkSession.token)

	screenshots := t.TempDir()
	script := filepath.Join(filepath.Dir(fixture.script), "payment_actions_public_checkout_chromium_journey.mjs")
	command := exec.CommandContext(fixture.ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_PAYMENT_ACTIONS_PUBLIC_URL="+fixture.server.URL,
		"AICRM_PAYMENT_ACTIONS_PUBLIC_STANDARD_CODE=browser-push-product",
		"AICRM_PAYMENT_ACTIONS_PUBLIC_SERVICE_CODE=browser-push-service-period",
		"AICRM_PAYMENT_ACTIONS_PUBLIC_SERVICE_ID="+fmt.Sprintf("%d", fixture.serviceProductID),
		"AICRM_PAYMENT_ACTIONS_PUBLIC_H5_TOKEN="+h5Session.token,
		"AICRM_PAYMENT_ACTIONS_PUBLIC_LINK_TOKEN="+linkSession.token,
		"AICRM_PAYMENT_ACTIONS_PUBLIC_LINK_MERCHANT="+linkMerchant,
		"AICRM_PAYMENT_ACTIONS_PUBLIC_LINK_BINDING="+linkBinding,
		"AICRM_PAYMENT_ACTIONS_PUBLIC_SCREENSHOT_DIR="+screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "payment_actions_public_checkout: PASS") {
		t.Fatalf("public paid Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	assertPublicCompletionRefreshIsReadOnly(t, fixture, h5Session.token, h5Merchant, 2)
	assertPublicCompletionRefreshIsReadOnly(t, fixture, linkSession.token, linkMerchant, 2)
}

func publicCheckoutBindingForPaymentActions(t *testing.T, fixture *productExternalPushChromiumFixture, token string) string {
	t.Helper()
	if fixture == nil || fixture.application == nil || fixture.application.paymentSession == nil {
		t.Fatal("paid public checkout fixture has no composed session service")
	}
	binding := string(paymentport.CheckoutSessionBinding(token))
	if binding == "" {
		t.Fatal("paid public checkout binding is empty")
	}
	return binding
}

func assertPublicPurchaseStatusAction(t *testing.T, fixture *productExternalPushChromiumFixture, token string, productID int64, kind, redirect string) {
	t.Helper()
	service, ok := fixture.application.paymentDistribution.(*paymentapp.Service)
	if !ok {
		t.Fatal("composition did not retain the native Payment application")
	}
	if state, err := service.PurchaseStatus(fixture.ctx, token, kind, productID); err != nil {
		t.Fatalf("Payment PurchaseStatus kind=%s direct error=%T %v state=%+v", kind, err, err, state)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/purchase-status?product_type="+kind+"&product_id="+strconv.FormatInt(productID, 10), nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"purchase_state":"owned"`) || !strings.Contains(response.Body.String(), `"redirect_url":"`+redirect+`"`) {
		t.Fatalf("public purchase status kind=%s status=%d body=%s", kind, response.Code, response.Body.String())
	}
}
