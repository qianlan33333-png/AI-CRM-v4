package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	ordersecure "github.com/qianlan33333-png/AI-CRM-v3/internal/order/secure"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	alipaysdk "github.com/smartwalle/alipay/v3"
	"github.com/smartwalle/nsign"
)

// Source mode and installed mode share assertions. In installed mode source
// composition only prepares fixtures/readback; all writes and pages use the
// independently started package binary behind the existing TLS test server.
func TestPostgreSQLPeriodicContactCollectionHTTPJourney(t *testing.T) {
	f, client, callbackKey := periodicContactFixture(t)
	periodicContactHTTPMatrix(t, f, client, callbackKey)
}

func TestPostgreSQLPeriodicContactCollectionChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f, client, callbackKey := periodicContactFixture(t)
	sessions := map[string]string{}
	for _, kind := range []string{"spf", "pf"} {
		for _, level := range []string{"none", "mobile", "shipping_address"} {
			sessions[kind+"/"+level] = issuePeriodicContactSession(t, f, "browser-"+kind+"-"+level)
		}
	}
	sessionJSON, _ := json.Marshal(sessions)
	command := exec.CommandContext(f.ctx, "node", filepath.Join(filepath.Dir(f.script), "periodic_contact_collection_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_CONTACT_BROWSER_URL="+f.server.URL,
		"AICRM_CONTACT_BROWSER_PRODUCT_ID="+strconv.FormatInt(f.productID, 10),
		"AICRM_CONTACT_BROWSER_PERIOD_ID="+strconv.FormatInt(f.serviceProductID, 10),
		"AICRM_CONTACT_BROWSER_SESSIONS="+string(sessionJSON),
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "periodic_contact_collection_chromium: PASS") || strings.Count(string(output), "contact-order:") != 6 {
		t.Fatalf("contact browser journey: %v output=%s", err, output)
	}
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "periodic_contact_collection_chromium: PASS") {
			t.Log(line)
		}
		if strings.HasPrefix(line, "contact-order:") {
			parts := strings.Split(strings.TrimPrefix(line, "contact-order:"), ":")
			if len(parts) != 2 {
				t.Fatal("invalid browser order receipt")
			}
			assertPeriodicContactSnapshot(t, f, parts[1], parts[0])
		}
	}
	periodicContactHTTPMatrix(t, f, client, callbackKey)
}

// Each matrix cell is an independent verified synthetic subject: changing a
// session token for the same customer would correctly hit pending-order guards.
func issuePeriodicContactSession(t *testing.T, f *productExternalPushChromiumFixture, key string) string {
	t.Helper()
	openID, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindOAOpenID, Scope: "wechat-app:wx-public-commerce-h5", Value: "contact-openid-" + key, Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	unionID, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindUnionID, Scope: "wechat-open-platform:public-commerce-fixture-platform", Value: "contact-unionid-" + key, Source: "payment-h5-oauth"})
	if err != nil {
		t.Fatal(err)
	}
	issued, err := f.application.paymentSession.IssueTrusted(f.ctx, paymentsession.IssueCommand{Fact: openID, UnionID: unionID, IdempotencyKey: "contact-session-" + key})
	if err != nil || issued.Token == "" {
		t.Fatal("independent synthetic trusted session unavailable")
	}
	return issued.Token
}

func periodicContactFixture(t *testing.T) (*productExternalPushChromiumFixture, *http.Client, string) {
	t.Helper()
	f := newProductExternalPushChromiumFixtureWithOptions(t, 220*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true, deferEffectsWorker: true})
	callbackKey := useInstalledPeriodicContactFixture(t, f)
	client := f.server.Client()
	client.Timeout = 15 * time.Second
	client.Jar, _ = cookiejar.New(nil)
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Get(f.server.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	match := regexp.MustCompile(`name="login_csrf_token" value="([^"]+)"`).FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("login CSRF unavailable")
	}
	response, err = client.PostForm(f.server.URL+"/login", url.Values{"username": {"product-browser-owner"}, "password": {"product-browser-owner-password"}, "login_csrf_token": {string(match[1])}})
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("contact login status=%d", response.StatusCode)
	}
	return f, client, callbackKey
}

func useInstalledPeriodicContactFixture(t *testing.T, f *productExternalPushChromiumFixture) string {
	t.Helper()
	input := testconfig.InstalledProduct()
	if input.Binary == "" {
		return f.alipayPrivateKeyPath
	}
	content, err := os.ReadFile(input.Binary)
	if err != nil {
		t.Fatal("read installed product binary")
	}
	digest := sha256.Sum256(content)
	manifestBytes, manifestErr := os.ReadFile(filepath.Join(input.Root, "web", "dist", "asset-manifest.json"))
	var manifest struct {
		SourceSHA string `json:"source_sha"`
	}
	if !filepath.IsAbs(input.Binary) || !filepath.IsAbs(input.Root) || !validDomesticReleaseSHA(input.SHA) || hex.EncodeToString(digest[:]) != input.BinarySHA256 || manifestErr != nil || json.Unmarshal(manifestBytes, &manifest) != nil || manifest.SourceSHA != input.SHA {
		t.Fatal("installed product binary/Host binding mismatch")
	}
	databaseURL := f.application.pool.Native().Config().ConnString()
	db, err := url.Parse(databaseURL)
	if err != nil || db.Hostname() != "localhost" && db.Hostname() != "127.0.0.1" && db.Hostname() != "::1" {
		t.Fatal("installed product needs loopback synthetic database")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	dataKey := base64.RawStdEncoding.EncodeToString(f.dataKey)
	wechatKey, wechatCert := distributionFixturePaymentCredentials(t)
	alipayKey, alipayPublic := f.alipayPrivateKeyPath, f.alipayPublicKey
	// No effects worker is started. Checkout URLs are signed locally; callback
	// commits freeze the push payload, with a reserved invalid receiver target.
	targets, _ := json.Marshal(map[string]any{"browser-push-target": map[string]any{
		"slot": "browser-product-slot", "endpoint": "https://commerce-browser.invalid", "version": "legacy-v1", "tenant_id": "aicrm-browser",
		"signing_key":   base64.RawStdEncoding.EncodeToString([]byte("browser-fixture-signing-key")),
		"buyer_id":      map[string]any{"kind": "wecom_external_userid", "scope": "wecom-corp:browser"},
		"buyer_openid":  map[string]any{"kind": "mp_openid", "scope": "wechat-app:browser"},
		"buyer_unionid": map[string]any{"kind": "unionid", "scope": "wechat-open-platform:browser"},
		"buyer_phone":   map[string]any{"kind": "phone", "scope": "phone:cn11"}, "beneficiary_phone": map[string]any{"kind": "phone", "scope": "phone:cn11"},
	}})
	environment := []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + input.SHA,
		"AICRM_PUBLIC_ORIGIN=" + f.server.URL, "AICRM_H5_PUBLIC_ORIGIN=" + f.server.URL,
		"AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey, "AICRM_ORDER_CONTACT_DATA_KEY=" + dataKey,
		"AICRM_SURVEY_OAUTH_ENABLED=true", "AICRM_SURVEY_OAUTH_APP_ID=wx-public-commerce-h5", "AICRM_SURVEY_OAUTH_SECRET=public-commerce-h5-fixture-secret", "AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID=public-commerce-fixture-platform", "AICRM_SURVEY_OAUTH_SCOPE=snsapi_userinfo",
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED=true", "AICRM_WECHAT_PAY_APP_ID=wx-public-commerce-mini", "AICRM_WECHAT_PAY_APP_SECRET=public-commerce-mini-fixture-secret", "AICRM_WECHAT_PAY_APP_SCOPE=wechat-app:wx-public-commerce-mini",
		"AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true", "AICRM_WECHAT_PAY_H5_APP_ID=wx-public-commerce-h5", "AICRM_WECHAT_PAY_H5_APP_SECRET=public-commerce-h5-fixture-secret", "AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:wx-public-commerce-h5",
		"AICRM_WECHAT_PAY_MERCHANT_ID=public-commerce-fixture-mch", "AICRM_WECHAT_PAY_MERCHANT_SERIAL=public-commerce-fixture-serial", "AICRM_WECHAT_PAY_PRIVATE_KEY_PATH=" + wechatKey, "AICRM_WECHAT_PAY_PLATFORM_CERT_PATH=" + wechatCert, "AICRM_WECHAT_PAY_API_V3_KEY=0123456789abcdef0123456789abcdef",
		"AICRM_ALIPAY_PROVIDER_ENABLED=true", "AICRM_ALIPAY_PRODUCTION=false", "AICRM_ALIPAY_APP_ID=virtual-alipay-test-app", "AICRM_ALIPAY_PRIVATE_KEY_PATH=" + alipayKey, "AICRM_ALIPAY_PUBLIC_KEY=" + alipayPublic, "AICRM_ALIPAY_GATEWAY=https://127.0.0.1:1/gateway.do", "AICRM_ALIPAY_NOTIFY_URL=" + f.server.URL + "/api/public/alipay/callback", "AICRM_ALIPAY_RETURN_URL=" + f.server.URL + "/pay/result",
		"AICRM_OUTBOUND_PROVIDER_ENABLED=true", "AICRM_COMMERCE_PUSH_PROVIDER_ENABLED=true", "AICRM_COMMERCE_PUSH_TARGETS_JSON=" + string(targets), "AICRM_COMMERCE_PUSH_PAYLOAD_DATA_KEY=" + dataKey,
	}
	process := startPeriodicContactInstalledProcess(t, input.Binary, input.Root, environment)
	waitInstalledSurveyReady(t, process, "http://"+address, input.SHA)
	target, _ := url.Parse("http://" + address)
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.Transport = &http.Transport{Proxy: nil}
	f.server.Config.Handler = proxy
	t.Logf("installed_product_contact source_sha=%s binary_sha256=%s HTTPS=true", input.SHA, input.BinarySHA256)
	return alipayKey
}

func startPeriodicContactInstalledProcess(t *testing.T, binary, root string, environment []string) *domesticSmokeProcess {
	t.Helper()
	command := exec.Command(binary)
	command.Dir, command.Env = root, environment
	var diagnostics bytes.Buffer
	command.Stdout, command.Stderr = io.Discard, &diagnostics
	if err := command.Start(); err != nil {
		t.Fatal("start installed contact service")
	}
	process := &domesticSmokeProcess{command: command, done: make(chan error, 1)}
	go func() { process.done <- command.Wait() }()
	t.Cleanup(func() {
		if !process.finished {
			_ = command.Process.Signal(os.Interrupt)
			select {
			case err := <-process.done:
				process.finished = true
				if err != nil {
					t.Errorf("installed contact service stopped unexpectedly: %T", err)
				}
			case <-time.After(8 * time.Second):
				_ = command.Process.Kill()
				<-process.done
				process.finished = true
				t.Error("installed contact service shutdown deadline")
			}
		}
		// The process receives only synthetic credentials. Report only the
		// startup error, never request/identity fields or its environment.
		if t.Failed() {
			for _, line := range strings.Split(diagnostics.String(), "\n") {
				if strings.Contains(line, "aicrm stopped") {
					t.Log(line)
				}
			}
		}
	})
	return process
}

func periodicContactRequest(t *testing.T, f *productExternalPushChromiumFixture, client *http.Client, method, path string, payload any, key string) (int, map[string]any) {
	t.Helper()
	raw, _ := json.Marshal(payload)
	request, err := http.NewRequestWithContext(f.ctx, method, f.server.URL+path, bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", f.server.URL)
	for _, cookie := range client.Jar.Cookies(request.URL) {
		if cookie.Name == "aicrm_csrf" || cookie.Name == "aicrm_admin_csrf" {
			request.Header.Set("X-CSRF-Token", cookie.Value)
		}
	}
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var result map[string]any
	_ = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result)
	return response.StatusCode, result
}

func periodicContactHTTPMatrix(t *testing.T, f *productExternalPushChromiumFixture, client *http.Client, callbackKey string) {
	t.Helper()
	for _, kind := range []string{"standard", "period"} {
		id, endpoint, productKind := f.productID, "/api/v1/products/", "standard"
		if kind == "period" {
			id, endpoint, productKind = f.serviceProductID, "/api/admin/service-period-products/", "service_period"
		}
		path := endpoint + strconv.FormatInt(id, 10)
		for _, level := range []string{"none", "mobile", "shipping_address"} {
			t.Run(kind+"/"+level, func(t *testing.T) {
				status, read := periodicContactRequest(t, f, client, "GET", path, nil, "")
				if status != 200 {
					t.Fatalf("product read status=%d", status)
				}
				product := read
				if nested, ok := read["product"].(map[string]any); ok {
					product = nested
				}
				version := product["version"]
				projection := product["admin_projection"].(map[string]any)
				projection["contact_collection_level"] = level
				projection["require_mobile"] = level != "none"
				update := map[string]any{"name": product["name"], "description": product["description"], "price_minor": product["price_minor"], "currency": product["currency"], "stock_quantity": product["stock_quantity"], "images": product["images"], "admin_projection": projection, "expected_version": version}
				if kind == "period" {
					update["duration_days"] = product["duration_days"]
				}
				status, _ = periodicContactRequest(t, f, client, "PUT", path, update, "contact-update-"+kind+"-"+level)
				if status != 200 {
					t.Fatalf("contact save status=%d", status)
				}
				status, read = periodicContactRequest(t, f, client, "GET", path, nil, "")
				product = read
				if nested, ok := read["product"].(map[string]any); ok {
					product = nested
				}
				if status != 200 || product["admin_projection"].(map[string]any)["contact_collection_level"] != level || product["version"].(float64) <= version.(float64) {
					t.Fatal("contact persisted readback mismatch")
				}
				status, _ = periodicContactRequest(t, f, client, "PUT", path, update, "contact-stale-"+kind+"-"+level)
				if status != 409 {
					t.Fatalf("stale version accepted: %d", status)
				}
				session := issuePeriodicContactSession(t, f, "http-"+kind+"-"+level)
				origin, _ := url.Parse(f.server.URL)
				client.Jar.SetCookies(origin, []*http.Cookie{{Name: paymentport.TrustedSessionCookieName, Value: session, Secure: true}})
				status, binding := periodicContactRequest(t, f, client, "GET", "/api/v1/wechat-pay/checkout-session", nil, "")
				if status != 200 || binding["can_create_checkout"] != true {
					t.Fatal("trusted checkout binding unavailable")
				}
				body := map[string]any{"product_id": id, "product_kind": productKind, "beneficiary_selection": "payer_self", "coupon_claim_id": 0, "contact_collection_level": level, "checkout_session_binding": binding["checkout_session_binding"], "provider": "alipay", "channel": "alipay_page"}
				beforeRejected := periodicContactRowCounts(t, f)
				if level != "none" {
					status, rejected := periodicContactRequest(t, f, client, "POST", "/api/v1/alipay/checkouts", body, "contact-missing-"+kind+"-"+level)
					if status != 409 {
						t.Fatalf("missing contact status=%d code=%v", status, rejected["code"])
					}
					body["mobile"] = "123"
					status, _ = periodicContactRequest(t, f, client, "POST", "/api/v1/alipay/checkouts", body, "contact-invalid-"+kind+"-"+level)
					if status != 409 {
						t.Fatalf("invalid mobile accepted status=%d", status)
					}
					body["mobile"] = "+8613800138000"
				}
				if level == "shipping_address" {
					status, _ = periodicContactRequest(t, f, client, "POST", "/api/v1/alipay/checkouts", body, "contact-no-address-"+kind)
					if status != 409 {
						t.Fatalf("missing shipping accepted status=%d", status)
					}
					for k, v := range map[string]string{"recipient_name": "synthetic", "province_code": "11", "province_name": "北京市", "city_code": "1101", "city_name": "北京市", "district_code": "110101", "district_name": "东城区", "detail_address": "synthetic street"} {
						body[k] = v
					}
					delete(body, "district_name")
					status, _ = periodicContactRequest(t, f, client, "POST", "/api/v1/alipay/checkouts", body, "contact-no-region-name-"+kind)
					if status != 409 {
						t.Fatalf("missing region name accepted status=%d", status)
					}
					body["district_name"] = "东城区"
				}
				if periodicContactRowCounts(t, f) != beforeRejected {
					t.Fatal("rejected contact created an order/payment")
				}
				status, created := periodicContactRequest(t, f, client, "POST", "/api/v1/alipay/checkouts", body, "contact-valid-"+kind+"-"+level)
				if status != 202 || created["merchant_order_no"] == "" {
					t.Fatalf("valid contact checkout status=%d error=%v", status, created["error"])
				}
				merchant := created["merchant_order_no"].(string)
				status, replay := periodicContactRequest(t, f, client, "POST", "/api/v1/alipay/checkouts", body, "contact-valid-"+kind+"-"+level)
				if status != 202 || replay["merchant_order_no"] != merchant {
					t.Fatal("checkout replay created another intent")
				}
				assertPeriodicContactSnapshot(t, f, merchant, level)
				if kind == "period" && level == "shipping_address" {
					periodicContactPushSnapshot(t, f, client, merchant, callbackKey)
				}
			})
		}
	}
}

func periodicContactRowCounts(t *testing.T, f *productExternalPushChromiumFixture) [2]int {
	t.Helper()
	var counts [2]int
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT (SELECT count(*) FROM orders),(SELECT count(*) FROM payments)`).Scan(&counts[0], &counts[1]); err != nil {
		t.Fatal(err)
	}
	return counts
}

func assertPeriodicContactSnapshot(t *testing.T, f *productExternalPushChromiumFixture, merchant, level string) {
	t.Helper()
	var id int64
	var contacts, addresses int
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT id,(SELECT count(*) FROM order_contact_snapshots c WHERE c.order_id=o.id),(SELECT count(*) FROM order_shipping_address_snapshots s WHERE s.order_id=o.id) FROM orders o WHERE merchant_order_no=$1`, merchant).Scan(&id, &contacts, &addresses); err != nil {
		t.Fatal(err)
	}
	wantContact, wantAddress := 0, 0
	if level != "none" {
		wantContact = 1
	}
	if level == "shipping_address" {
		wantAddress = 1
	}
	if contacts != wantContact || addresses != wantAddress {
		t.Fatal("contact/shipping snapshot cardinality mismatch")
	}
	if contacts == 1 {
		var ciphertext []byte
		var version int16
		if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT phone_ciphertext,key_version FROM order_contact_snapshots WHERE order_id=$1`, id).Scan(&ciphertext, &version); err != nil {
			t.Fatal(err)
		}
		cipher, err := ordersecure.NewContactCipher(base64.RawStdEncoding.EncodeToString(f.dataKey))
		if err != nil {
			t.Fatal(err)
		}
		phone, err := cipher.Decrypt(ciphertext, version)
		if err != nil || phone != "+8613800138000" {
			t.Fatal("immutable order contact mismatch")
		}
	}
	if addresses == 1 {
		var valid bool
		if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT recipient_name='synthetic' AND province_code='11' AND city_code='1101' AND district_code='110101' AND detail_address='synthetic street' FROM order_shipping_address_snapshots WHERE order_id=$1`, id).Scan(&valid); err != nil || !valid {
			t.Fatal("shipping snapshot mismatch")
		}
	}
}

func periodicContactPushSnapshot(t *testing.T, f *productExternalPushChromiumFixture, client *http.Client, merchant, callbackKey string) {
	t.Helper()
	status, _ := periodicContactRequest(t, f, client, "PUT", fmt.Sprintf("/api/admin/service-period-products/%d/external-push", f.serviceProductID), map[string]any{"enabled": true, "configuration_reference": "browser-push-target", "expected_revision": 0, "type": "member_open", "day": 30, "frequency": 2, "remark": "contact fixture", "expires_at_ts": 2147483647, "custom_params": map[string]any{}}, "contact-push-config")
	if status != 200 {
		t.Fatalf("synthetic push configuration status=%d", status)
	}
	{
		key, err := os.ReadFile(callbackKey)
		if err != nil {
			t.Fatal(err)
		}
		signer, err := alipaysdk.New("virtual-alipay-test-app", string(key), false)
		if err != nil {
			t.Fatal(err)
		}
		form := url.Values{"app_id": {"virtual-alipay-test-app"}, "notify_id": {"contact-notify"}, "out_trade_no": {merchant}, "trade_no": {"contact-synthetic-trade"}, "trade_status": {"TRADE_SUCCESS"}, "total_amount": {"128.00"}, "sign_type": {"RSA2"}, "gmt_payment": {time.Now().In(time.FixedZone("CST", 8*3600)).Format("2006-01-02 15:04:05")}}
		sig, err := signer.SignValues(form, nsign.WithIgnore("sign", "sign_type", "alipay_cert_sn"))
		if err != nil {
			t.Fatal(err)
		}
		form.Set("sign", base64.StdEncoding.EncodeToString(sig))
		response, err := client.PostForm(f.server.URL+"/api/public/alipay/callback", form)
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 200 || strings.TrimSpace(string(raw)) != "success" {
			t.Fatalf("installed signed callback status=%d", response.StatusCode)
		}
	}
	var ciphertext []byte
	var version int16
	var reference, slot string
	if err := f.application.pool.Native().QueryRow(f.ctx, `SELECT payload_ciphertext,payload_key_version,source_reference,target_slot FROM outbound_commerce_push_intents WHERE product_id=$1 AND source_kind='order_paid'`, f.serviceProductID).Scan(&ciphertext, &version, &reference, &slot); err != nil {
		t.Fatal(err)
	}
	cipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString(f.dataKey))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := cipher.DecryptCommercePayload(ciphertext, version, []byte("commerce-push.payload.v1\x00"+reference+"\x00"+slot))
	if err != nil {
		t.Fatal(err)
	}
	var body struct {
		Phone          string `json:"phone_number"`
		Type           string `json:"type"`
		Day, Frequency int64
		Remark         string
		Buyer          struct {
			Phone string `json:"phone"`
		} `json:"buyer"`
	}
	if json.Unmarshal(raw, &body) != nil || body.Phone != "13800138000" || body.Buyer.Phone != "13800138000" || body.Type != "member_open" || body.Day != 30 || body.Frequency != 2 || body.Remark != "contact fixture" {
		t.Fatal("frozen paid push lost contact/fixed params")
	}
	t.Log("contact snapshot and synthetic paid push fixed fields: PASS; receiver execution not claimed")
}
