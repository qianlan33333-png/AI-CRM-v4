package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// Exercises the real composed public routes and transaction rollback: denied
// Alipay does not consume the trusted session, reserve an order, or queue an
// effect. The same session can immediately pay with WeChat.
func TestPostgreSQLProductAlipayPolicyPublicCheckout(t *testing.T) {
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 120*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true})
	adminSession, adminCSRF := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	for _, product := range []struct {
		id   int64
		kind string
	}{{fixture.productID, "standard"}, {fixture.serviceProductID, "service_period"}} {
		var code string
		if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT product_code FROM products WHERE id=$1`, product.id).Scan(&code); err != nil {
			t.Fatal(err)
		}
		route := "/pay/" + code
		if product.kind == "service_period" {
			route = "/s/" + code + "/pay"
		}
		page := httptest.NewRecorder()
		fixture.application.handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, route, nil))
		if page.Code != 200 || !strings.Contains(page.Body.String(), `name="paymentMethod" value="alipay"`) {
			t.Fatalf("default both methods %s status=%d", route, page.Code)
		}
		endpoint := fmt.Sprintf("/api/v1/products/%d", product.id)
		if product.kind == "service_period" {
			endpoint = fmt.Sprintf("/api/admin/service-period-products/%d", product.id)
		}
		loaded := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodGet, endpoint, "", adminSession, adminCSRF, "")
		var stored map[string]json.RawMessage
		if loaded.Code != 200 || json.Unmarshal(loaded.Body.Bytes(), &stored) != nil {
			t.Fatalf("admin read %d %s", loaded.Code, loaded.Body.String())
		}
		if wrapped, ok := stored["product"]; ok {
			if json.Unmarshal(wrapped, &stored) != nil {
				t.Fatal("invalid product envelope")
			}
		}
		command := map[string]json.RawMessage{}
		for _, key := range []string{"name", "description", "price_minor", "currency", "stock_quantity", "images", "admin_projection"} {
			command[key] = stored[key]
		}
		command["expected_version"] = stored["version"]
		if product.kind == "service_period" {
			command["duration_days"] = stored["duration_days"]
		}
		var projection map[string]json.RawMessage
		if json.Unmarshal(command["admin_projection"], &projection) != nil {
			t.Fatal("invalid projection")
		}
		projection["alipay_enabled"] = json.RawMessage(`false`)
		command["admin_projection"], _ = json.Marshal(projection)
		encoded, _ := json.Marshal(command)
		saved := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodPut, endpoint, string(encoded), adminSession, adminCSRF, "policy-admin-save-"+product.kind)
		if saved.Code != 200 {
			t.Fatalf("admin save %s status=%d %s", product.kind, saved.Code, saved.Body.String())
		}
		readback := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodGet, endpoint, "", adminSession, adminCSRF, "")
		if readback.Code != 200 || !strings.Contains(readback.Body.String(), `"alipay_enabled":false`) {
			t.Fatalf("policy readback %d %s", readback.Code, readback.Body.String())
		}
		var err error
		page = httptest.NewRecorder()
		fixture.application.handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, route, nil))
		if page.Code != 200 || strings.Contains(page.Body.String(), `name="paymentMethod" value="alipay"`) || !strings.Contains(page.Body.String(), `name="paymentMethod" value="wechat_pay"`) {
			t.Fatalf("disabled method page %s status=%d", route, page.Code)
		}
		session := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "policy-session-"+product.kind)
		var beforeOrders, beforeEffects int
		if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM external_effects)`).Scan(&beforeOrders, &beforeEffects); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"product_id":%d,"product_kind":%q,"provider":"alipay","channel":"alipay_wap","beneficiary_selection":"payer_self","checkout_session_binding":%q}`, product.id, product.kind, paymentport.CheckoutSessionBinding(session.token))
		request := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "policy-denied-"+product.kind)
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
		response := httptest.NewRecorder()
		fixture.application.handler.ServeHTTP(response, request)
		var errorBody map[string]string
		_ = json.Unmarshal(response.Body.Bytes(), &errorBody)
		if response.Code != 409 || errorBody["code"] != "product_payment_method_disabled" {
			t.Fatalf("denied %s status=%d body=%s", product.kind, response.Code, response.Body.String())
		}
		var afterOrders, afterEffects int
		if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM external_effects)`).Scan(&afterOrders, &afterEffects); err != nil {
			t.Fatal(err)
		}
		if afterOrders != beforeOrders || afterEffects != beforeEffects {
			t.Fatal("denied Alipay created order/effect")
		}
		createPublicCheckoutForCompletionAction(t, fixture, session.token, product.id, product.kind, "policy-wechat-"+product.kind)
	}
}

// These contracts exercise the production Product Host serializer and its
// normal dimension save path, with explicit authoritative readback fixtures.
func TestProductAlipayPolicyEditorSaveContracts(t *testing.T) {
	prepareProductExternalPushChromiumArtifacts(t, "../..")
	for _, script := range []string{"web/v3/productAdapter.contact_collection.test.mjs", "web/v3/productAdapter.alipay_policy.test.mjs"} {
		command := exec.Command("node", script)
		command.Dir = "../.."
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", script, err, output)
		}
	}
}
