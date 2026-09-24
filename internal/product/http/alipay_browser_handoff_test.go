package http

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestAlipayBrowserHandoffPreservesSignedURLAndRejectsOpenRedirects(t *testing.T) {
	handler, err := NewPublicHandler(&testCatalog{product: enabledPublicProduct(7, "course-7")})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/pay/alipay/continue", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("bridge status=%d body=%s", response.Code, response.Body.String())
	}
	for name, expected := range map[string]string{
		"Cache-Control":          "no-store",
		"Referrer-Policy":        "no-referrer",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
	} {
		if got := response.Header().Get(name); got != expected {
			t.Errorf("%s=%q, want %q", name, got, expected)
		}
	}
	digest := sha256.Sum256([]byte(alipayBrowserHandoffScript))
	if csp := response.Header().Get("Content-Security-Policy"); !strings.Contains(csp, "script-src 'sha256-"+base64.StdEncoding.EncodeToString(digest[:])+"'") || !strings.Contains(csp, "frame-ancestors 'none'") {
		t.Fatalf("bridge CSP=%q", csp)
	}
	if !strings.Contains(response.Body.String(), "<script>"+alipayBrowserHandoffScript+"</script>") {
		t.Fatal("bridge script and CSP hash do not describe the same page")
	}
	for _, target := range []string{"/pay/alipay/continue?url=https://evil.test", "/pay/alipay/continue/", "/pay/alipay/continue"} {
		for _, method := range []string{http.MethodPost, http.MethodGet} {
			if target == "/pay/alipay/continue" && method == http.MethodGet {
				continue
			}
			result := httptest.NewRecorder()
			handler.ServeHTTP(result, httptest.NewRequest(method, target, nil))
			if result.Code != http.StatusNotFound {
				t.Fatalf("%s %s status=%d", method, target, result.Code)
			}
		}
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for Alipay browser handoff journey")
	}
	command := exec.Command("node", filepath.Join("testdata", "alipay_browser_handoff_journey.mjs"))
	command.Stdin = bytes.NewReader(response.Body.Bytes())
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("Alipay browser handoff journey: %v\n%s", err, output)
	}
}

func TestPublicCheckoutOffersAlipayBrowserHandoff(t *testing.T) {
	handler, err := NewPublicHandler(&testCatalog{product: enabledPublicProduct(7, "course-7")})
	if err != nil {
		t.Fatal(err)
	}
	handler.SetPaymentMethods(true, true)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/pay/course-7", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("payment page status=%d", response.Code)
	}
	for _, expected := range []string{"继续支付宝支付", "'/pay/alipay/continue#'+encodeURIComponent(target.href)", "复制原付款链接（备用）", "checkoutStatusURL(checkpoint,checkpoint.merchant_order_no)"} {
		if !strings.Contains(response.Body.String(), expected) {
			t.Fatalf("payment page misses %q", expected)
		}
	}
}
