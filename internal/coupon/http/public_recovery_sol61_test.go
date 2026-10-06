package http

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// The production Host renders this page. JSDOM below tests only its transport
// and DOM contract; the separately opted-in composed browser test owns HTTP/PG.
func TestPublicCouponStalledResponseRecoverySol61(t *testing.T) {
	var rendered bytes.Buffer
	if err := publicCouponPage.Execute(&rendered, frozenCouponPageState{
		Coupon:    couponPublicView{Name: "恢复验证券", DiscountAmountTotal: 100, PublicSlug: "cp-sol61-recovery"},
		Claimable: true, IsWechat: true, DisplayState: "active",
		SessionBinding: paymentport.CheckoutSessionBinding("trusted-session-token-123456"),
	}); err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(t.TempDir(), "public-coupon.html")
	if err := os.WriteFile(page, rendered.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	_, source, _, _ := runtime.Caller(0)
	if output, err := exec.Command("node", filepath.Join(filepath.Dir(source), "public_recovery_sol61.mjs"), page).CombinedOutput(); err != nil {
		t.Fatalf("public coupon stalled-response DOM contract: %v\n%s", err, output)
	}
}

func TestPublicCouponRecoveryBindingIsOptionalAndRejectsChangedSessionSol61(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		bindings []string
		want     int
	}{
		{name: "legacy absent", want: http.StatusOK},
		{name: "current opaque", bindings: []string{paymentport.CheckoutSessionBinding("trusted-session-token-123456")}, want: http.StatusOK},
		{name: "other session", bindings: []string{paymentport.CheckoutSessionBinding("other-trusted-session-token-123456")}, want: http.StatusConflict},
		{name: "empty present", bindings: []string{""}, want: http.StatusConflict},
		{name: "duplicate", bindings: []string{paymentport.CheckoutSessionBinding("trusted-session-token-123456"), paymentport.CheckoutSessionBinding("trusted-session-token-123456")}, want: http.StatusConflict},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h, _, claims := newPublicCouponHandler(t, "published")
			r := trustedCouponRequest(http.MethodPost, "/api/h5/coupons/cp-a1b2c3/claim")
			r.Header.Set("Idempotency-Key", "sol61-public-coupon-recovery-0001")
			for _, value := range scenario.bindings {
				r.Header.Add("X-AICRM-Coupon-Session-Binding", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != scenario.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if scenario.want == http.StatusOK && claims.command.HolderCustomerID != 9 {
				t.Fatalf("holder must come from trusted session: %+v", claims.command)
			}
			if scenario.want != http.StatusOK && (claims.command.HolderCustomerID != 0 || !strings.Contains(w.Body.String(), "coupon_session_binding_mismatch")) {
				t.Fatalf("mismatch reached claim: %+v body=%s", claims.command, w.Body.String())
			}
		})
	}
}

func TestPublicCouponRecoveryOnlyHeaderIsStrictAndKeepsTrustedHolderSol61(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		values []string
		want   int
	}{
		{name: "empty", values: []string{""}, want: http.StatusBadRequest},
		{name: "false", values: []string{"false"}, want: http.StatusBadRequest},
		{name: "uppercase", values: []string{"TRUE"}, want: http.StatusBadRequest},
		{name: "combined", values: []string{"true,true"}, want: http.StatusBadRequest},
		{name: "duplicate", values: []string{"true", "true"}, want: http.StatusBadRequest},
		{name: "explicit readonly", values: []string{"true"}, want: http.StatusOK},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			h, _, claims := newPublicCouponHandler(t, "published")
			r := trustedCouponRequest(http.MethodPost, "/api/h5/coupons/cp-a1b2c3/claim")
			r.Header.Set("Idempotency-Key", "sol61-public-coupon-readonly-0001")
			r.Header.Set("X-AICRM-Coupon-Session-Binding", paymentport.CheckoutSessionBinding("other-expired-trusted-token-123456"))
			for _, value := range scenario.values {
				r.Header.Add("X-AICRM-Coupon-Recovery-Only", value)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != scenario.want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			if scenario.want == http.StatusOK && (!claims.command.RecoveryOnly || claims.command.HolderCustomerID != 9) {
				t.Fatalf("readonly mode changed trusted holder or was omitted: %+v", claims.command)
			}
			if scenario.want != http.StatusOK && claims.command.HolderCustomerID != 0 {
				t.Fatalf("malformed mode reached application: %+v", claims.command)
			}
		})
	}
}
