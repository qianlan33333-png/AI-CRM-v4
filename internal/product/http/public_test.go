package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	mediaport "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func promotionContextToken() string { return "dpc_" + strings.Repeat("A", 43) }

func publicRequest(method, target string, cookies map[string]string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	for name, value := range cookies {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	return request
}

func expectLegacyPromotionCookiesCleared(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	cleared := map[string]bool{}
	for _, cookie := range response.Result().Cookies() {
		if cookie.MaxAge < 0 {
			cleared[cookie.Name] = true
		}
	}
	for _, name := range []string{"aicrm_promotion_context", "aicrm_promotion_handoff", "aicrm_promotion_accepted"} {
		if !cleared[name] {
			t.Fatalf("cookie %q was not cleared: %v", name, response.Result().Cookies())
		}
	}
}

// TestPromotionContextPublicChain follows the browser-visible /d landing into
// the Product page and its payment page. GETs only carry the opaque context;
// they never create or alter a pending order snapshot.
func TestPromotionContextPublicChainRetainsCheckoutContext(t *testing.T) {
	product := enabledPublicProduct(7, "course-7")
	public, err := NewPublicHandler(&testCatalog{product: product})
	if err != nil {
		t.Fatal(err)
	}
	context := promotionContextToken()
	destination := "/p/course-7?promotion_context=" + context // Distribution's resolved /d redirect.
	landing := httptest.NewRecorder()
	public.ServeHTTP(landing, publicRequest(http.MethodGet, destination, nil))
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), `href="/pay/course-7?promotion_context=`+context+`"`) {
		t.Fatalf("handoff landing status=%d body=%s", landing.Code, landing.Body.String())
	}
	payment := httptest.NewRecorder()
	public.ServeHTTP(payment, publicRequest(http.MethodGet, "/pay/course-7?promotion_context="+context, nil))
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), "promotionContext='"+context+"'") || !strings.Contains(payment.Body.String(), "location.pathname+(promotionContext?'?promotion_context='+promotionContext:'')") {
		t.Fatalf("payment continuation status=%d body=%s", payment.Code, payment.Body.String())
	}
	if !strings.Contains(payment.Body.String(), "checkoutRecord(){let raw") || !strings.Contains(payment.Body.String(), "if(record.state==='invalid')throw requestFailure('checkout_checkpoint_invalid'") || !strings.Contains(payment.Body.String(), "if(record.state==='unavailable')return null") || !strings.Contains(payment.Body.String(), "if(promotionContext)payload.promotion_context=promotionContext") {
		t.Fatalf("checkout body does not freeze and create controlled context: %s", payment.Body.String())
	}
}

func TestServicePeriodPromotionContextReachesPaymentAndOAuth(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	context := promotionContextToken()
	landing := httptest.NewRecorder()
	handler.ServeHTTP(landing, publicRequest(http.MethodGet, "/s/term-31?promotion_context="+context, nil))
	if landing.Code != http.StatusOK || !strings.Contains(landing.Body.String(), "promotionContext='"+context+"'") {
		t.Fatalf("service promotion landing status=%d body=%s", landing.Code, landing.Body.String())
	}
	payment := httptest.NewRecorder()
	handler.ServeHTTP(payment, publicRequest(http.MethodGet, "/s/term-31/pay?promotion_context="+context, nil))
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), "promotionContext='"+context+"'") || !strings.Contains(payment.Body.String(), "location.pathname+(promotionContext?'?promotion_context='+promotionContext:'')") {
		t.Fatalf("service promotion payment status=%d body=%s", payment.Code, payment.Body.String())
	}
	if !strings.Contains(payment.Body.String(), "checkoutRecord(){let raw") || !strings.Contains(payment.Body.String(), "if(record.state==='invalid')throw requestFailure('checkout_checkpoint_invalid'") || !strings.Contains(payment.Body.String(), "if(record.state==='unavailable')return null") || !strings.Contains(payment.Body.String(), "if(promotionContext)payload.promotion_context=promotionContext") {
		t.Fatalf("service payment body does not share the controlled checkpoint runtime: %s", payment.Body.String())
	}
}

func TestPublicEntrancesClearLegacyCookiesAndDoNotCarryContext(t *testing.T) {
	standard, err := NewPublicHandler(&testCatalog{product: enabledPublicProduct(7, "course-7")})
	if err != nil {
		t.Fatal(err)
	}
	periodReader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}}
	period, err := NewServicePeriodPublicHandler(periodReader)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		handler http.Handler
		path    string
	}{
		{name: "ordinary standard entry", handler: standard, path: "/p/course-7"},
		{name: "ordinary service-period entry", handler: period, path: "/s/term-31"},
	} {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			test.handler.ServeHTTP(response, publicRequest(http.MethodGet, test.path, map[string]string{"aicrm_promotion_context": promotionContextToken(), "aicrm_promotion_handoff": "stale", "aicrm_promotion_accepted": "stale"}))
			if response.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			if strings.Contains(response.Body.String(), "promotionContext='dpc_") {
				t.Fatalf("ordinary entry retained promotion context: %s", response.Body.String())
			}
			expectLegacyPromotionCookiesCleared(t, response)
		})
	}
}

func TestPublicPromotionContextRejectsForgedAndCrossProductQueries(t *testing.T) {
	standard, err := NewPublicHandler(&testCatalog{product: enabledPublicProduct(7, "course-7")})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/p/course-7?promotion_context=dpc_forged",
		"/p/course-7?promotion_context=" + promotionContextToken() + "&product_code=other-product",
		"/p/course-7?distribution_handoff=1",
	} {
		response := httptest.NewRecorder()
		standard.ServeHTTP(response, publicRequest(http.MethodGet, path, map[string]string{"aicrm_promotion_context": promotionContextToken()}))
		if response.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
		expectLegacyPromotionCookiesCleared(t, response)
	}
}

func enabledPublicProduct(id productport.ID, code string) productport.Product {
	now := time.Date(2026, 9, 14, 9, 0, 0, 0, time.UTC)
	return productport.Product{ID: id, ProductCode: code, Name: "公开商品", PriceMinor: 990, Currency: "CNY", Images: []string{"https://cdn.example.test/product.png"}, CreatedBy: 1, CreatedAt: now, UpdatedAt: now, Version: 1, LocalLifecycle: productport.LocalProductEnabled, LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`)}
}

func TestPublicProductEnabledOnlyAndSafeDTO(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	catalog := &testCatalog{product: productport.Product{
		ID: 7, ProductCode: "secret-code", Name: "公开商品", Description: "描述", PriceMinor: 990, Currency: "CNY",
		Images: []string{"https://cdn.example.test/p.png"}, CreatedBy: 99, CreatedAt: now, UpdatedAt: now, Version: 3,
		LocalLifecycle:        productport.LocalProductEnabled,
		LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"现在购买","require_mobile":true,"lead_program_id":23,"lead_channel_id":34,"lead_qr_title":"internal","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`),
	}}
	handler, err := NewPublicHandler(catalog)
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/public/products/secret-code", nil))
	if recorder.Code != http.StatusOK || catalog.getCode != "secret-code" || strings.Contains(recorder.Body.String(), "secret-code") || strings.Contains(recorder.Body.String(), "lead_program") || !strings.Contains(recorder.Body.String(), `"require_mobile":true`) {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/p/secret-code", nil))
	if page.Code != http.StatusOK || catalog.getCode != "secret-code" || !strings.Contains(page.Body.String(), "公开商品") || !strings.Contains(page.Body.String(), "checkoutStorageKey") {
		t.Fatalf("status=%d body=%s", page.Code, page.Body.String())
	}
	payment := httptest.NewRecorder()
	handler.ServeHTTP(payment, httptest.NewRequest(http.MethodGet, "/pay/secret-code", nil))
	if payment.Code != http.StatusOK || strings.Contains(payment.Body.String(), "beneficiarySelf") || !strings.Contains(payment.Body.String(), "beneficiary_selection:'payer_self'") || strings.Contains(payment.Body.String(), "beneficiary_customer_id") {
		t.Fatalf("payment page status=%d body=%s", payment.Code, payment.Body.String())
	}
	for _, required := range []string{"微信身份验证", "正在核验微信身份", "登录才能完成支付", "不会自动扣款", "授权并继续", "bootstrapCheckout", "checkoutContent", "/api/v1/wechat-pay/checkout-session", "/api/h5/wechat-pay/oauth/start?return_url="} {
		if !strings.Contains(payment.Body.String(), required) {
			t.Fatalf("payment page missing identity verification %q: %s", required, payment.Body.String())
		}
	}
	if strings.Contains(payment.Body.String(), "authAttemptKey") {
		t.Fatal("payment page must wait for a click before starting OAuth")
	}
	if strings.Contains(payment.Body.String(), `id="renew"`) {
		t.Fatalf("ordinary product must not expose renewal: %s", payment.Body.String())
	}
	for _, required := range []string{"自动选择最优优惠券", "checkoutStorageKey", "merchant_order_no", "正在恢复原订单", "Idempotency-Key':checkpoint.key", "retainPaidCheckout(orderNo)", "restorePaidCheckout", "terminal_status==='paid'", "showCompletionAction", "location.assign(action.redirect_url)", "completion-qr", "value.completion_action"} {
		if !strings.Contains(payment.Body.String(), required) {
			t.Fatalf("payment page missing stable checkout behaviour %q: %s", required, payment.Body.String())
		}
	}
	if strings.Contains(payment.Body.String(), "Idempotency-Key':crypto.randomUUID()") {
		t.Fatalf("payment page must not replace an unknown checkout key: %s", payment.Body.String())
	}
	legacyID := httptest.NewRecorder()
	handler.ServeHTTP(legacyID, httptest.NewRequest(http.MethodGet, "/p/7", nil))
	if legacyID.Code != http.StatusOK || catalog.getCode != "7" || catalog.getID != 7 {
		t.Fatalf("numeric public path must remain a historical ID alias: status=%d code=%q id=%d body=%s", legacyID.Code, catalog.getCode, catalog.getID, legacyID.Body.String())
	}
	legacyPayment := httptest.NewRecorder()
	handler.ServeHTTP(legacyPayment, httptest.NewRequest(http.MethodGet, "/pay/7", nil))
	if legacyPayment.Code != http.StatusOK || catalog.getCode != "7" || catalog.getID != 7 || !strings.Contains(legacyPayment.Body.String(), "beneficiary_selection:'payer_self'") {
		t.Fatalf("numeric payment alias must retain checkout: status=%d code=%q id=%d body=%s", legacyPayment.Code, catalog.getCode, catalog.getID, legacyPayment.Body.String())
	}

	numericCodeCatalog := &testCatalog{product: catalog.product}
	numericCodeCatalog.product.ProductCode = "7"
	numericCodeHandler, err := NewPublicHandler(numericCodeCatalog)
	if err != nil {
		t.Fatal(err)
	}
	numericCode := httptest.NewRecorder()
	numericCodeHandler.ServeHTTP(numericCode, httptest.NewRequest(http.MethodGet, "/p/7", nil))
	if numericCode.Code != http.StatusOK || numericCodeCatalog.getCode != "7" || numericCodeCatalog.getCalls != 0 {
		t.Fatalf("product code must take precedence over legacy ID: status=%d code=%q get_calls=%d", numericCode.Code, numericCodeCatalog.getCode, numericCodeCatalog.getCalls)
	}
}

func TestPublicProductWithoutPageMaterialRedirectsDirectlyToPayment(t *testing.T) {
	product := enabledPublicProduct(7, "course-7")
	product.Images = []string{}
	handler, err := NewPublicHandler(&testCatalog{product: product})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/p/course-7?promotion_context="+promotionContextToken(), nil))
	if response.Code != http.StatusFound || response.Header().Get("Location") != "/pay/course-7?promotion_context="+promotionContextToken() || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("status=%d location=%q headers=%v body=%s", response.Code, response.Header().Get("Location"), response.Header(), response.Body.String())
	}
	payment := httptest.NewRecorder()
	handler.ServeHTTP(payment, httptest.NewRequest(http.MethodGet, response.Header().Get("Location"), nil))
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), `data-public-commerce-route="payment"`) || strings.Contains(payment.Body.String(), `id="detailContent"`) {
		t.Fatalf("payment status=%d body=%s", payment.Code, payment.Body.String())
	}
}

func TestPublicProductDetailIsImmersiveAndPrioritizesOnlyTheFirstImage(t *testing.T) {
	product := enabledPublicProduct(7, "course-7")
	product.Name = "不应出现在详情素材上方"
	product.Description = "不应渲染摘要卡片"
	product.Images = []string{"https://cdn.example.test/first.png", "https://cdn.example.test/second.png"}
	handler, err := NewPublicHandler(&testCatalog{product: product})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/p/course-7", nil))
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, body)
	}
	for _, forbidden := range []string{
		`id="detailContent" hidden><div class="panel">`,
		`<p class="desc">不应渲染摘要卡片</p>`,
	} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("immersive detail retained summary chrome %q: %s", forbidden, body)
		}
	}
	for _, required := range []string{
		`id="detailContent" hidden><img class="detail-image"`,
		`data-src="https://cdn.example.test/first.png" data-detail-index="0" alt="商品详情" decoding="async" loading="eager" fetchpriority="high"`,
		`data-src="https://cdn.example.test/second.png" data-detail-index="1" alt="商品详情" decoding="async" loading="lazy"`,
		`new IntersectionObserver`,
		`rootMargin:'900px 0px'`,
		`controller.abort(),12000`,
		`网络连接超时，请刷新重试`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("immersive detail missing %q: %s", required, body)
		}
	}
}

func TestPublicPaymentCompletionRefreshJourney(t *testing.T) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	journey := filepath.Join(filepath.Dir(source), "public_payment_completion_journey.mjs")
	pages := []string{}
	for _, kind := range []string{"standard", "service_period"} {
		var html bytes.Buffer
		if err := publicProductPage.Execute(&html, map[string]any{"Payment": true, "Detail": false, "Product": publicProduct{ID: 7, Name: "已购商品", PriceMinor: 990, ProductKind: kind, CouponTargetRef: "standard_product:7", RequireMobile: true, ContactCollectionLevel: "mobile", RegionOptionsJSON: template.JS("[]")}}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), kind+".html")
		if err := os.WriteFile(path, html.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, path)
	}
	command := exec.Command("node", append([]string{journey}, pages...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("public payment completion refresh journey: %v\n%s", err, output)
	}
}

func TestPublicAlipayOriginalOrderBrowserJourney(t *testing.T) {
	var html bytes.Buffer
	view := publicProductPageView{Payment: true, WeChatPayEnabled: true, AlipayEnabled: true, Product: publicProduct{ID: 7, Name: "测试商品", PriceMinor: 990, ProductKind: "standard", CouponTargetRef: "standard_product:7", ContactCollectionLevel: "none", RegionOptionsJSON: template.JS("[]")}}
	if err := publicProductPage.Execute(&html, view); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "alipay-checkout.html")
	if err := os.WriteFile(path, html.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	_, source, _, _ := runtime.Caller(0)
	journey := filepath.Join(filepath.Dir(source), "public_alipay_journey.mjs")
	if output, err := exec.Command("node", journey, path).CombinedOutput(); err != nil {
		t.Fatalf("Alipay original order journey: %v\n%s", err, output)
	}
}

func TestPublicPaymentMethodsRespectAvailability(t *testing.T) {
	for _, tc := range []struct {
		wechat, alipay bool
	}{{true, false}, {false, true}, {true, true}} {
		var html bytes.Buffer
		view := publicProductPageView{Payment: true, WeChatPayEnabled: tc.wechat, AlipayEnabled: tc.alipay, Product: publicProduct{ID: 7, Name: "测试商品", PriceMinor: 990, ProductKind: "standard", ContactCollectionLevel: "none", RegionOptionsJSON: template.JS("[]")}}
		if err := publicProductPage.Execute(&html, view); err != nil {
			t.Fatal(err)
		}
		body := html.String()
		if strings.Contains(body, `name="paymentMethod" value="wechat_pay"`) != tc.wechat || strings.Contains(body, `name="paymentMethod" value="alipay"`) != tc.alipay {
			t.Fatalf("visible payment methods differ from availability: wechat=%v alipay=%v", tc.wechat, tc.alipay)
		}
	}
}

func TestPublicPaymentEmbedsRegionOptionsAsArray(t *testing.T) {
	var html bytes.Buffer
	if err := publicProductPage.Execute(&html, map[string]any{
		"Payment": true,
		"Detail":  false,
		"Product": publicProduct{ContactCollectionLevel: "shipping_address", RegionOptionsJSON: template.JS(`[{"c":11,"n":"北京市","ch":[]}]`)},
	}); err != nil {
		t.Fatal(err)
	}
	body := html.String()
	if !strings.Contains(body, `const regionOptions=[{"c":11,"n":"北京市","ch":[]}];`) {
		t.Fatalf("region options were not embedded as an array: %s", body)
	}
	if strings.Contains(body, `const regionOptions="`) {
		t.Fatalf("region options were incorrectly quoted as a string: %s", body)
	}
}

func TestPublicPaymentRegionCascadeBrowserJourney(t *testing.T) {
	product := enabledPublicProduct(7, "book")
	product.LegacyAdminProjection = json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":true,"contact_collection_level":"shipping_address","slices":[]}`)
	handler, err := NewPublicHandler(&testCatalog{product: product})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/pay/book", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("payment page status=%d", response.Code)
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	path := filepath.Join(t.TempDir(), "pay-book.html")
	if err := os.WriteFile(path, response.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	journey := filepath.Join(filepath.Dir(source), "public_shipping_region_journey.mjs")
	if output, err := exec.Command("node", journey, path).CombinedOutput(); err != nil {
		t.Fatalf("shipping region browser journey: %v\n%s", err, output)
	}
}

func TestPublicCheckoutCollectionProviderMatrix(t *testing.T) {
	dir := t.TempDir()
	for _, kind := range []string{"standard", "service_period"} {
		for _, level := range []string{"none", "mobile", "shipping_address"} {
			var html bytes.Buffer
			product := publicProduct{ID: 7, Name: "测试商品", Description: "文字描述", PriceMinor: 990, ProductKind: kind, CouponTargetRef: "standard_product:7", RequireMobile: level != "none", ContactCollectionLevel: level, RegionOptionsJSON: template.JS(`[{"c":11,"n":"北京市","ch":[{"c":1101,"n":"北京市","ch":[{"c":110101,"n":"东城区"}]}]}]`)}
			if kind == "service_period" {
				product.CouponTargetRef = "service_period:7"
				product.ServicePeriodDurationDays = 30
			}
			if err := publicProductPage.Execute(&html, publicProductPageView{Payment: true, Product: product, WeChatPayEnabled: true, AlipayEnabled: true}); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, kind+"-"+level+".html"), html.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime caller unavailable")
	}
	journey := filepath.Join(filepath.Dir(source), "public_checkout_collection_matrix.mjs")
	if output, err := exec.Command("node", journey, dir).CombinedOutput(); err != nil {
		t.Fatalf("checkout collection/provider matrix: %v\n%s", err, output)
	}
}

func TestPublicProductDraftDisabledAndMalformedAre404(t *testing.T) {
	for _, projection := range []json.RawMessage{
		json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`),
		json.RawMessage(`{"schema_version":1,"status":"disabled","enabled":false,"buy_button_text":"","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`),
		json.RawMessage(`{"status":"unknown"}`),
	} {
		handler, err := NewPublicHandler(&testCatalog{product: productport.Product{ID: 8, ProductCode: "p-8", Name: "hidden", PriceMinor: 100, Currency: "CNY", Version: 1, LegacyAdminProjection: projection}})
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/public/products/p-8", "/p/p-8", "/pay/p-8"} {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
			}
		}
	}
}

func TestPublicProductRejectsMalformedCodePaths(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 0, 0, 0, time.UTC)
	catalog := &testCatalog{product: productport.Product{ID: 7, ProductCode: "course-7", Name: "公开商品", PriceMinor: 990, Currency: "CNY", CreatedBy: 99, CreatedAt: now, UpdatedAt: now, Version: 1, LocalLifecycle: productport.LocalProductEnabled, LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`)}}
	handler, err := NewPublicHandler(catalog)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/p/course-7/extra", "/pay/course-7/extra", "/p/%2F", "/api/public/products/course-7?x=1"} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))
		if recorder.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d body=%s", path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestPublicProductMediaUsesOnlyEnabledProductImageBindings(t *testing.T) {
	now := time.Date(2026, 9, 8, 9, 0, 0, 0, time.UTC)
	catalog := &testCatalog{product: productport.Product{
		ID: 8, ProductCode: "course-9", Name: "公开商品", Description: "说明", PriceMinor: 990, Currency: "CNY", StockQuantity: 1,
		Images: []string{"/api/admin/image-library/88/variants/original"}, CreatedBy: 9, CreatedAt: now, UpdatedAt: now, Version: 1, LocalLifecycle: productport.LocalProductEnabled,
		LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`),
	}}
	handler, err := NewPublicHandler(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicMediaReader(servicePeriodMediaStub{}); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/public/products/course-9", nil))
	if api.Code != http.StatusOK || !strings.Contains(api.Body.String(), "/api/h5/product-images/course-9/88/variants/original") || strings.Contains(api.Body.String(), "/api/admin/image-library/") {
		t.Fatalf("public product status=%d body=%s", api.Code, api.Body.String())
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/p/course-9", nil))
	if page.Code != http.StatusOK || strings.Contains(page.Body.String(), `class="hero"`) || strings.Contains(page.Body.String(), `class="cover"`) || strings.Contains(page.Body.String(), "/api/admin/image-library/") {
		t.Fatalf("public page status=%d body=%s", page.Code, page.Body.String())
	}
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, httptest.NewRequest(http.MethodGet, "/api/h5/product-images/course-9/88/variants/original", nil))
	if allowed.Code != http.StatusOK || allowed.Body.String() != "image-88" || allowed.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("allowed status=%d body=%q headers=%v", allowed.Code, allowed.Body.String(), allowed.Header())
	}
	for _, path := range []string{
		"/api/h5/product-images/course-9/89/variants/original",
		"/api/h5/product-images/course-9/88/variants/thumb_320",
		"/api/h5/product-images/course-9/88/variants/original?download=1",
	} {
		denied := httptest.NewRecorder()
		handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, path, nil))
		if denied.Code != http.StatusNotFound {
			t.Fatalf("unbound path=%s status=%d", path, denied.Code)
		}
	}

	catalog.product.LocalLifecycle = productport.LocalProductDraft
	catalog.product.LegacyAdminProjection = json.RawMessage(`{"schema_version":1,"status":"draft","enabled":false,"buy_button_text":"购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"purchase_action_enabled":false,"purchase_action_mode":"","wecom_tagging":{},"slices":[]}`)
	draft := httptest.NewRecorder()
	handler.ServeHTTP(draft, httptest.NewRequest(http.MethodGet, "/api/h5/product-images/course-9/88/variants/original", nil))
	if draft.Code != http.StatusNotFound {
		t.Fatalf("draft media status=%d", draft.Code)
	}
}

type servicePeriodPublicStub struct {
	product productport.CheckoutProduct
	code    string
}

func (stub *servicePeriodPublicStub) ReadPublicServicePeriodByCode(_ context.Context, code string) (productport.CheckoutProduct, error) {
	stub.code = code
	if code != stub.product.Code {
		return productport.CheckoutProduct{}, errors.New("not found")
	}
	return stub.product, nil
}

type servicePeriodTestUOW struct{}

func (servicePeriodTestUOW) Within(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}

type servicePeriodSessionStub struct{}

func (servicePeriodSessionStub) LookupWithin(_ context.Context, token string, _ time.Time) (paymentport.SessionActor, error) {
	if token != "service-period-trusted" {
		return paymentport.SessionActor{}, errors.New("bad session")
	}
	return paymentport.SessionActor{PayerCustomerID: 11, BeneficiaryCustomerID: 11}, nil
}

// servicePeriodExpiredSessionStub deliberately returns a populated actor with
// ErrSessionRequired. The public Host must disregard every actor when session
// validation fails rather than relying on a particular adapter's zero value.
type servicePeriodExpiredSessionStub struct{}

func (servicePeriodExpiredSessionStub) LookupWithin(_ context.Context, _ string, _ time.Time) (paymentport.SessionActor, error) {
	return paymentport.SessionActor{PayerCustomerID: 99, BeneficiaryCustomerID: 99}, paymentport.ErrSessionRequired
}

type servicePeriodEntitlementStub struct{ page orderport.EntitlementPage }

func (stub servicePeriodEntitlementStub) ListCustomerEntitlements(_ context.Context, customerID int64, _ int32) (orderport.EntitlementPage, error) {
	if customerID != 11 {
		return orderport.EntitlementPage{}, errors.New("wrong customer")
	}
	return stub.page, nil
}
func (servicePeriodEntitlementStub) ListServicePeriodMembers(context.Context, orderport.ServicePeriodMemberQuery) (orderport.ServicePeriodMemberPage, error) {
	return orderport.ServicePeriodMemberPage{}, errors.New("unused")
}
func (stub servicePeriodEntitlementStub) GetCustomerServicePeriodEntitlement(_ context.Context, customerID, productID int64) (orderport.Entitlement, bool, error) {
	if customerID != 11 {
		return orderport.Entitlement{}, false, errors.New("wrong customer")
	}
	for _, item := range stub.page.Items {
		if item.ServiceProductID == productID {
			return item, true, nil
		}
	}
	return orderport.Entitlement{}, false, nil
}
func (servicePeriodEntitlementStub) UpdateEntitlementRemark(context.Context, orderport.RemarkCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, errors.New("unused")
}
func (servicePeriodEntitlementStub) UpdateEntitlementAlliance(context.Context, orderport.AllianceCommand) (orderport.Entitlement, error) {
	return orderport.Entitlement{}, errors.New("unused")
}

type servicePeriodEntitlementProbe struct {
	servicePeriodEntitlementStub
	called bool
}

func (probe *servicePeriodEntitlementProbe) GetCustomerServicePeriodEntitlement(_ context.Context, _, _ int64) (orderport.Entitlement, bool, error) {
	probe.called = true
	return orderport.Entitlement{}, false, errors.New("expired session must not read entitlement")
}

type servicePeriodLeadQRStub struct{ value channelport.PublicLeadQRCode }

func (stub servicePeriodLeadQRStub) ReadPublicLeadQRCode(context.Context, int64) (channelport.PublicLeadQRCode, error) {
	return stub.value, nil
}

func TestServicePeriodPublicHostRetainsFrozenDonorStateDOM(t *testing.T) {
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(frozenServicePeriodPublicRenderer))); got != frozenServicePeriodPublicRendererSHA256 {
		t.Fatalf("service-period donor hash=%s", got)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256([]byte(frozenPublicProductService))); got != frozenPublicProductServiceSHA256 {
		t.Fatalf("public-product service donor hash=%s", got)
	}
	var page bytes.Buffer
	if err := renderServicePeriodPublicPage(&page, servicePeriodPublicState{Available: true, Product: publicProduct{ID: 71, Name: "31 天服务期", PriceMinor: 12800, PaymentPath: "/s/term-31/pay", ServicePeriodDurationDays: 31}, Status: "active", CTA: "立即续费", EndAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC), RemainingDays: 15}); err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{`service-period-page`, `servicePeriodStateCard`, `servicePeriodPayButton`, `data-route-owner="ai_crm_next"`, `service-period-wecom-action`, `detail-media`, `width:48%`, `fetch(window.location.pathname.replace(`} {
		if !strings.Contains(page.String(), marker) {
			t.Fatalf("adapted donor state DOM missing %q", marker)
		}
	}
}

func TestFrozenServicePeriodFStringDecodesOnlyStaticLiteralSegments(t *testing.T) {
	dynamic := `客户 {{state_json}} \\ 保持`
	rendered, err := renderFrozenPythonFString(`const route = /^\\/s\\//; const literal = {{ok: true}}; const title = {title};`, []frozenFStringReplacement{{expression: "{title}", value: dynamic}})
	if err != nil {
		t.Fatal(err)
	}
	if rendered != `const route = /^\/s\//; const literal = {ok: true}; const title = 客户 {{state_json}} \\ 保持;` {
		t.Fatalf("f-string render=%q", rendered)
	}
}

// This exercises the shared authorized checkout for periodic products.
func TestServicePeriodPublicBrowserJourney(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for frozen service-period browser journey")
	}
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{
		ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: `服务 {{state_json}} \ 标题`, PriceMinor: 12800, Currency: "CNY", Version: 4,
		LeadChannelID: 44, LeadQRTitle: `二维码 {{keep}} \ 标题`, LeadQRSubtitle: `扫码 {{keep}} \ 领取资料`, ServicePeriodDurationDays: 31,
	}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	endAt := time.Date(2026, 9, 20, 16, 0, 0, 0, time.UTC) // 2026-09-21 in Shanghai.
	if err = handler.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodEntitlementStub{page: orderport.EntitlementPage{Items: []orderport.Entitlement{{CustomerID: 11, ServiceProductID: 71, Status: "active", EndAt: endAt}}}}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicLeadQRCodeReader(servicePeriodLeadQRStub{value: channelport.PublicLeadQRCode{URL: "https://work.weixin.qq.com/q/term"}}); err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC) }
	server := httptest.NewServer(handler)
	defer server.Close()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate service-period journey")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	journey := filepath.Join(root, "internal", "product", "http", "service_period_public_journey.mjs")
	pages := []string{}
	for _, kind := range []string{"standard", "service_period"} {
		var html bytes.Buffer
		if err := publicProductPage.Execute(&html, map[string]any{"Payment": true, "Detail": false, "Product": publicProduct{ID: 7, Name: "已购商品", PriceMinor: 990, ProductKind: kind, CouponTargetRef: "standard_product:7", RequireMobile: true, ContactCollectionLevel: "mobile", RegionOptionsJSON: template.JS("[]")}}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), kind+".html")
		if err := os.WriteFile(path, html.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, path)
	}
	command := exec.Command("node", append([]string{journey}, pages...)...)
	command.Dir = root
	command.Env = append(os.Environ(), "AICRM_SERVICE_PERIOD_JOURNEY_BASE_URL="+server.URL, "AICRM_SERVICE_PERIOD_JOURNEY_COOKIE="+paymentport.TrustedSessionCookieName+"=service-period-trusted")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("frozen service-period browser journey: %v\n%s", err, output)
	}
}

func TestPublicServicePeriodRendersTrustedEntitlementWithoutIdentityFallback(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, DetailMedia: []productport.PublicDetailMedia{{ImageID: 88}}, LeadChannelID: 44, LeadQRTitle: "加企微", LeadQRSubtitle: "领取资料", ServicePeriodDurationDays: 31}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	activeEnd := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	if err = handler.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodEntitlementStub{page: orderport.EntitlementPage{Items: []orderport.Entitlement{{CustomerID: 11, ServiceProductID: 71, Status: "active", EndAt: activeEnd}}}}); err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicLeadQRCodeReader(servicePeriodLeadQRStub{value: channelport.PublicLeadQRCode{URL: "https://work.weixin.qq.com/q/term"}}); err != nil {
		t.Fatal(err)
	}
	handler.now = func() time.Time { return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC) }
	request := httptest.NewRequest(http.MethodGet, "/s/term-31", nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "service-period-trusted"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `id="detailContent" hidden`) || !strings.Contains(response.Body.String(), "/images/88/variants/original") || !strings.Contains(response.Body.String(), `href="/s/term-31/pay"`) {
		t.Fatalf("active page status=%d body=%s", response.Code, response.Body.String())
	}
	untrusted := httptest.NewRecorder()
	handler.ServeHTTP(untrusted, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	if untrusted.Code != http.StatusOK || !strings.Contains(untrusted.Body.String(), `id="identityGate"`) {
		t.Fatalf("untrusted page status=%d body=%s", untrusted.Code, untrusted.Body.String())
	}
}

func TestPublicServicePeriodExpiredTrustedSessionDoesNotReuseActor(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	probe := &servicePeriodEntitlementProbe{}
	if err = handler.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodExpiredSessionStub{}, probe); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/s/term-31", nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "expired-service-period-trusted"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || probe.called {
		t.Fatalf("expired session status=%d entitlement_called=%t", response.Code, probe.called)
	}
}

func TestPublicServicePeriodUsesExactCodeAndSeparateCheckoutRoute(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, DetailMedia: []productport.PublicDetailMedia{{ImageID: 88}}, ServicePeriodDurationDays: 31}}
	handler, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	if page.Code != http.StatusOK || reader.code != "term-31" || !strings.Contains(page.Body.String(), `id="detailContent" hidden`) || !strings.Contains(page.Body.String(), "服务周期 31 天") || !strings.Contains(page.Body.String(), `href="/s/term-31/pay"`) {
		t.Fatalf("page status=%d code=%q body=%s", page.Code, reader.code, page.Body.String())
	}
	payment := httptest.NewRecorder()
	handler.ServeHTTP(payment, httptest.NewRequest(http.MethodGet, "/s/term-31/pay", nil))
	if payment.Code != http.StatusOK || !strings.Contains(payment.Body.String(), "product_kind:'service_period'") || !strings.Contains(payment.Body.String(), "beneficiary_selection:'payer_self'") || !strings.Contains(payment.Body.String(), "restorePaidCheckout") || !strings.Contains(payment.Body.String(), "showCompletionAction") {
		t.Fatalf("payment status=%d body=%s", payment.Code, payment.Body.String())
	}
	for _, path := range []string{"/s/71", "/s/term-31/extra", "/s/term-31/pay/extra", "/s/%2F", "/s/term-31?x=1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
}

func TestPublicServicePeriodStateEndpointUsesTheFrozenStateContract(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}}
	h, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.SetTrustedPublicState(servicePeriodTestUOW{}, servicePeriodSessionStub{}, servicePeriodEntitlementStub{page: orderport.EntitlementPage{Items: []orderport.Entitlement{{ServiceProductID: 71, Status: "active", EndAt: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)}}}}); err != nil {
		t.Fatal(err)
	}
	h.now = func() time.Time { return time.Date(2026, 9, 5, 9, 0, 0, 0, time.UTC) }
	request := httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/term-31", nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: "service-period-trusted"})
	response := httptest.NewRecorder()
	h.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"available":true`) || !strings.Contains(response.Body.String(), `"status":"active"`) || !strings.Contains(response.Body.String(), `"remaining_days":15`) || !strings.Contains(response.Body.String(), `"checkout_url":"/s/term-31/pay"`) {
		t.Fatalf("state endpoint status=%d body=%s", response.Code, response.Body.String())
	}
}

type servicePeriodMediaStub struct{}

func (servicePeriodMediaStub) LocalImageExists(_ context.Context, id int64) (bool, error) {
	return id == 88, nil
}
func (servicePeriodMediaStub) GetImageVariant(_ context.Context, id int64, key string) (mediaport.ImageVariant, error) {
	if id != 88 || key != "original" {
		return mediaport.ImageVariant{}, errors.New("not found")
	}
	return mediaport.ImageVariant{Content: []byte("image-88"), MediaType: "image/png", ETag: `"image-88"`}, nil
}
func TestPublicServicePeriodMediaIsRestrictedToProductSlices(t *testing.T) {
	reader := &servicePeriodPublicStub{product: productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "期", PriceMinor: 100, Currency: "CNY", Version: 1, ServicePeriodDurationDays: 31, DetailMedia: []productport.PublicDetailMedia{{ImageID: 88}}}}
	h, err := NewServicePeriodPublicHandler(reader)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.SetPublicMediaReader(servicePeriodMediaStub{}); err != nil {
		t.Fatal(err)
	}
	ok := httptest.NewRecorder()
	h.ServeHTTP(ok, httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/term-31/images/88/variants/original", nil))
	if ok.Code != http.StatusOK || ok.Body.String() != "image-88" || ok.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("ok=%d body=%q headers=%v", ok.Code, ok.Body.String(), ok.Header())
	}
	denied := httptest.NewRecorder()
	h.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/h5/service-period-products/term-31/images/89/variants/original", nil))
	if denied.Code != http.StatusNotFound {
		t.Fatalf("denied=%d", denied.Code)
	}
}
