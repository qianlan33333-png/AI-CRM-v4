package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
)

// TestPostgreSQLOrdersH5OAuthStartAndCompleteRoundTrip exercises the composed
// Payment OAuth state store, verified OneID session issuance, and orders read
// using PostgreSQL. When AICRM_INSTALLED_PRODUCT_* is set, every HTTP request
// is routed to that immutable installed binary; only the synthetic Provider
// boundary and fixture readback run in the test process.
func TestPostgreSQLOrdersH5OAuthStartAndCompleteRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	readPool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal("open isolated OAuth journey database")
	}
	t.Cleanup(readPool.Close)

	installed := testconfig.InstalledProduct()
	installedMode := installed.Binary != ""
	if installedMode {
		validateOrdersOAuthInstalledProduct(t, installed)
		parsed, parseErr := url.Parse(databaseURL)
		if parseErr != nil || (parsed.Hostname() != "localhost" && parsed.Hostname() != "127.0.0.1" && parsed.Hostname() != "::1") || !strings.HasPrefix(strings.TrimPrefix(parsed.Path, "/"), "aicrm_test_") {
			t.Fatal("installed OAuth journey requires an isolated loopback PostgreSQL database")
		}
	}

	keyPath, certPath := distributionFixturePaymentCredentials(t)
	applicationServer := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(applicationServer.Close)
	origin := "https://" + applicationServer.Listener.Addr().String()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	const (
		h5AppID       = "public-survey-browser-app"
		h5AppSecret   = "public-survey-browser-secret"
		platformID    = "public-survey-browser-platform"
		syntheticCode = "public-survey-browser-code"
	)

	var providerReads func() int32
	var unexpectedProviderCalls func() int32
	if installedMode {
		providerProxy, trust, calls, unexpected := installedSurveyProviderBoundary(t)
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			t.Fatal("reserve installed OAuth API listener")
		}
		address := listener.Addr().String()
		_ = listener.Close()
		environment := []string{
			"AICRM_DATABASE_URL=" + databaseURL,
			"AICRM_ROLE=api",
			"AICRM_LISTEN_ADDR=" + address,
			"AICRM_RELEASE_SHA=" + installed.SHA,
			"AICRM_PUBLIC_ORIGIN=" + origin,
			"AICRM_H5_PUBLIC_ORIGIN=" + origin,
			"AICRM_SURVEY_DATA_KEY=" + dataKey,
			"AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey,
			"AICRM_ORDER_CONTACT_DATA_KEY=" + dataKey,
			"AICRM_REFERRAL_TOKEN_DATA_KEY=" + dataKey,
			"AICRM_GROUP_OPS_WEBHOOK_SECRET=orders-oauth-installed-composition-webhook-secret",
			"AICRM_SURVEY_OAUTH_ENABLED=true",
			"AICRM_SURVEY_OAUTH_APP_ID=" + h5AppID,
			"AICRM_SURVEY_OAUTH_SECRET=" + h5AppSecret,
			"AICRM_SURVEY_OAUTH_OPEN_PLATFORM_ID=" + platformID,
			"AICRM_SURVEY_OAUTH_SCOPE=snsapi_userinfo",
			"AICRM_WECHAT_PAY_PROVIDER_ENABLED=true",
			"AICRM_WECHAT_PAY_APP_ID=orders-oauth-payment-app",
			"AICRM_WECHAT_PAY_APP_SECRET=orders-oauth-payment-secret",
			"AICRM_WECHAT_PAY_APP_SCOPE=wechat-app:orders-oauth-payment-app",
			"AICRM_WECHAT_PAY_H5_OAUTH_ENABLED=true",
			"AICRM_WECHAT_PAY_H5_APP_ID=" + h5AppID,
			"AICRM_WECHAT_PAY_H5_APP_SECRET=" + h5AppSecret,
			"AICRM_WECHAT_PAY_H5_APP_SCOPE=wechat-app:" + h5AppID,
			"AICRM_WECHAT_PAY_MERCHANT_ID=orders-oauth-test-mch",
			"AICRM_WECHAT_PAY_MERCHANT_SERIAL=orders-oauth-test-serial",
			"AICRM_WECHAT_PAY_PRIVATE_KEY_PATH=" + keyPath,
			"AICRM_WECHAT_PAY_PLATFORM_CERT_PATH=" + certPath,
			"AICRM_WECHAT_PAY_API_V3_KEY=0123456789abcdef0123456789abcdef",
			"AICRM_EFFECTS_PROVIDER_ENABLED=false",
			"AICRM_OUTBOUND_PROVIDER_ENABLED=false",
			"AICRM_SURVEY_COMPLETION_PROVIDER_ENABLED=false",
			"HTTPS_PROXY=" + providerProxy,
			"NO_PROXY=127.0.0.1,localhost,::1",
			"SSL_CERT_FILE=" + trust,
		}
		process := startDomesticSmokeProcess(t, installed.Binary, installed.Root, environment)
		endpoint := "http://" + address
		waitDomesticReleaseReady(t, process, endpoint, installed.SHA)
		target, _ := url.Parse(endpoint)
		proxy := httputil.NewSingleHostReverseProxy(target)
		proxy.Transport = &http.Transport{Proxy: nil}
		applicationServer.Config.Handler = proxy
		providerReads = calls.Load
		unexpectedProviderCalls = unexpected.Load
	} else {
		application, composeErr := compose(ctx, platformconfig.Runtime{
			Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, H5PublicOrigin: origin,
			ReleaseSHA: strings.Repeat("1", 40), WorkerOwner: "orders-h5-oauth-journey", WorkerLimit: 1,
			Survey: platformconfig.Survey{
				DataKey: dataKey, IdentityPhoneDataKey: dataKey, OAuthEnabled: true,
				OAuthAppID: h5AppID, OAuthSecret: h5AppSecret, OAuthOpenPlatformID: platformID, OAuthScope: "snsapi_userinfo",
			},
			Referral: platformconfig.Referral{TokenDataKey: dataKey},
			GroupOps: platformconfig.GroupOps{WebhookSecret: "orders-oauth-source-composition-webhook-secret"},
			WeChatPay: platformconfig.WeChatPay{
				Enabled: true, AppID: "orders-oauth-payment-app", AppSecret: "orders-oauth-payment-secret", AppScope: "wechat-app:orders-oauth-payment-app",
				H5OAuthEnabled: true, H5AppID: h5AppID, H5AppSecret: h5AppSecret, H5AppScope: "wechat-app:" + h5AppID,
				OrderContactDataKey: dataKey, MerchantID: "orders-oauth-test-mch", MerchantSerial: "orders-oauth-test-serial",
				PrivateKeyPath: keyPath, PlatformCertPath: certPath, APIV3Key: "0123456789abcdef0123456789abcdef",
			},
		})
		if composeErr != nil {
			t.Fatal("compose source OAuth journey")
		}
		t.Cleanup(application.Close)
		applicationServer.Config.Handler = application.handler

		var sourceCalls, sourceUnexpected atomic.Int32
		provider := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			switch request.URL.Path {
			case "/sns/oauth2/access_token":
				if request.Method != http.MethodGet || request.URL.Query().Get("code") != syntheticCode || request.URL.Query().Get("grant_type") != "authorization_code" {
					sourceUnexpected.Add(1)
					http.Error(writer, "synthetic OAuth request rejected", http.StatusBadRequest)
					return
				}
				sourceCalls.Add(1)
				_, _ = writer.Write([]byte(`{"access_token":"public-survey-browser-token","openid":"public-survey-browser-openid","scope":"snsapi_userinfo"}`))
			case "/sns/userinfo":
				if request.Method != http.MethodGet || request.URL.Query().Get("access_token") != "public-survey-browser-token" || request.URL.Query().Get("openid") != "public-survey-browser-openid" {
					sourceUnexpected.Add(1)
					http.Error(writer, "synthetic OAuth request rejected", http.StatusBadRequest)
					return
				}
				sourceCalls.Add(1)
				_, _ = writer.Write([]byte(`{"openid":"public-survey-browser-openid","unionid":"public-survey-browser-unionid","nickname":"OAuth 测试"}`))
			default:
				sourceUnexpected.Add(1)
				http.Error(writer, "synthetic OAuth route rejected", http.StatusBadRequest)
			}
		}))
		t.Cleanup(provider.Close)
		providerURL, parseErr := url.Parse(provider.URL)
		if parseErr != nil {
			t.Fatal("parse synthetic OAuth provider URL")
		}
		originalTransport := http.DefaultTransport
		transport := &referralH5OAuthTransport{base: originalTransport, target: providerURL}
		http.DefaultTransport = transport
		t.Cleanup(func() { http.DefaultTransport = originalTransport })
		providerReads = func() int32 { return int32(transport.calls) }
		unexpectedProviderCalls = sourceUnexpected.Load
	}
	applicationServer.StartTLS()

	clientTransport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	client := &http.Client{
		Transport: clientTransport,
		Timeout:   10 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	t.Cleanup(clientTransport.CloseIdleConnections)
	get := func(path string, userAgent string, cookies ...*http.Cookie) *http.Response {
		t.Helper()
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, origin+path, nil)
		if requestErr != nil {
			t.Fatal("build OAuth journey request")
		}
		if userAgent != "" {
			request.Header.Set("User-Agent", userAgent)
		}
		for _, cookie := range cookies {
			request.AddCookie(cookie)
		}
		response, requestErr := client.Do(request)
		if requestErr != nil {
			t.Fatal("execute OAuth journey request")
		}
		return response
	}

	var before int
	if err = readPool.QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states`).Scan(&before); err != nil || before != 0 {
		t.Fatalf("isolated OAuth state fixture was not empty: rows=%d", before)
	}
	start := get("/api/h5/wechat-pay/oauth/start?return_url=%2Fmy%2Forders", "MicroMessenger orders OAuth")
	_ = start.Body.Close()
	if start.StatusCode != http.StatusFound {
		t.Fatalf("fixed orders OAuth start status=%d", start.StatusCode)
	}
	authorization, parseErr := url.Parse(start.Header.Get("Location"))
	if parseErr != nil || authorization.Scheme != "https" || authorization.Host != "open.weixin.qq.com" || authorization.Path != "/connect/oauth2/authorize" {
		t.Fatal("OAuth start did not redirect to the official authorization endpoint")
	}
	query := authorization.Query()
	stateToken := query.Get("state")
	if stateToken == "" || query.Get("appid") != h5AppID || query.Get("scope") != "snsapi_userinfo" || query.Get("redirect_uri") != origin+"/api/h5/wechat-pay/oauth/callback" {
		t.Fatal("OAuth start did not preserve its fixed callback contract")
	}
	var returnCookie *http.Cookie
	for _, cookie := range start.Cookies() {
		if cookie.Name == "__Secure-aicrm_payment_oauth_return" && cookie.Path == "/api/h5/wechat-pay/oauth/callback" && cookie.Value != "" && cookie.Secure && cookie.HttpOnly && cookie.SameSite == http.SameSiteLaxMode {
			returnCookie = cookie
		}
	}
	if returnCookie == nil {
		t.Fatal("OAuth start did not return the callback recovery cookie")
	}
	returnCookiePath, cookieDecodeErr := base64.RawURLEncoding.DecodeString(returnCookie.Value)
	if cookieDecodeErr != nil || string(returnCookiePath) != "/my/orders" {
		t.Fatal("OAuth recovery cookie was not bound to the validated orders path")
	}
	stateDigest := sha256.Sum256([]byte(stateToken))
	var persistedPath string
	var consumed bool
	if err = readPool.QueryRow(ctx, `SELECT return_path,consumed_at IS NOT NULL FROM payment_h5_oauth_states WHERE state_digest=$1`, stateDigest[:]).Scan(&persistedPath, &consumed); err != nil || persistedPath != "/my/orders" || consumed {
		t.Fatal("OAuth start state was not durably bound to the fixed orders path")
	}

	for _, path := range []string{
		"/my/orders?customer_id=7",
		"/my/orders?openid=untrusted",
		"/my/orders?next=https://evil.example/",
		"//evil.example/my/orders",
		"/my/orders/",
	} {
		response := get("/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(path), "MicroMessenger orders OAuth")
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest || response.Header.Get("Location") != "" {
			t.Fatal("unsafe orders OAuth return path was accepted")
		}
	}
	var stateCount int
	if err = readPool.QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states`).Scan(&stateCount); err != nil || stateCount != 1 {
		t.Fatalf("invalid OAuth return paths persisted state: rows=%d", stateCount)
	}

	callbackPath := "/api/h5/wechat-pay/oauth/callback?state=" + url.QueryEscape(stateToken) + "&code=" + url.QueryEscape(syntheticCode)
	tampered := get(callbackPath+"&customer_id=7", "", returnCookie)
	_ = tampered.Body.Close()
	if tampered.StatusCode != http.StatusBadRequest || providerReads() != 0 {
		t.Fatal("callback identity parameter was not rejected before Provider reads")
	}

	callback := get(callbackPath, "", returnCookie)
	_ = callback.Body.Close()
	if callback.StatusCode != http.StatusFound || callback.Header.Get("Location") != "/my/orders" {
		t.Fatal("successful OAuth callback did not return to the fixed orders path")
	}
	var paymentCookie *http.Cookie
	for _, cookie := range callback.Cookies() {
		if cookie.Name == paymenthttp.SessionCookieName && cookie.Value != "" && cookie.Secure && cookie.HttpOnly {
			paymentCookie = cookie
		}
	}
	if paymentCookie == nil || providerReads() != 2 || unexpectedProviderCalls() != 0 {
		t.Fatal("OAuth callback did not issue a trusted session from the synthetic Provider read")
	}
	if err = readPool.QueryRow(ctx, `SELECT consumed_at IS NOT NULL FROM payment_h5_oauth_states WHERE state_digest=$1`, stateDigest[:]).Scan(&consumed); err != nil || !consumed {
		t.Fatal("successful OAuth callback did not consume its persisted state")
	}

	orders := get("/api/h5/me/orders", "", paymentCookie)
	_ = orders.Body.Close()
	if orders.StatusCode != http.StatusOK {
		t.Fatalf("trusted OAuth session could not read its orders endpoint: status=%d", orders.StatusCode)
	}

	var customerID int64
	tokenDigest := sha256.Sum256([]byte(paymentCookie.Value))
	if err = readPool.QueryRow(ctx, `SELECT payer_customer_id FROM payment_sessions WHERE token_digest=$1`, tokenDigest[:]).Scan(&customerID); err != nil || customerID < 1 {
		t.Fatal("callback session was not stored with its canonical customer")
	}
	var projectedActive bool
	if err = readPool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM customer_directory_projection WHERE customer_id=$1 AND customer_status='active')`, customerID).Scan(&projectedActive); err != nil || !projectedActive {
		t.Fatal("verified OAuth customer was not projected into the readiness-required directory")
	}
	var openIDCustomerID, unionIDCustomerID int64
	if err = readPool.QueryRow(ctx, `SELECT customer_id FROM customer_identities WHERE kind=$1 AND scope_key=$2 AND normalized_value=$3 AND assurance='verified' AND source='wechat.payment.h5_oauth.userinfo' AND status='active'`, identitydomain.KindOAOpenID, "wechat-app:"+h5AppID, "public-survey-browser-openid").Scan(&openIDCustomerID); err != nil {
		t.Fatal("provider-verified OA identity was not persisted")
	}
	if err = readPool.QueryRow(ctx, `SELECT customer_id FROM customer_identities WHERE kind=$1 AND scope_key=$2 AND normalized_value=$3 AND assurance='verified' AND source='wechat.payment.h5_oauth.userinfo' AND status='active'`, identitydomain.KindUnionID, "wechat-open-platform:"+platformID, "public-survey-browser-unionid").Scan(&unionIDCustomerID); err != nil || customerID != openIDCustomerID || customerID != unionIDCustomerID {
		t.Fatal("trusted Payment Session and verified OneID facts did not resolve to one customer")
	}

	providerReadCount := providerReads()
	replay := get(callbackPath, "", returnCookie)
	_ = replay.Body.Close()
	if replay.StatusCode != http.StatusSeeOther || replay.Header.Get("Location") != "/my/orders?oauth_error=denied" || providerReads() != providerReadCount {
		t.Fatal("OAuth state replay was not rejected before another Provider read")
	}
	for _, cookie := range replay.Cookies() {
		if cookie.Name == paymenthttp.SessionCookieName && cookie.Value != "" {
			t.Fatal("OAuth state replay issued a second trusted session")
		}
	}
	var sessions, consumedStates int
	if err = readPool.QueryRow(ctx, `SELECT count(*) FROM payment_sessions`).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("OAuth replay changed trusted session count: %d", sessions)
	}
	if err = readPool.QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states WHERE return_path='/my/orders' AND consumed_at IS NOT NULL`).Scan(&consumedStates); err != nil || consumedStates != 1 {
		t.Fatalf("orders OAuth state persistence mismatch: consumed=%d", consumedStates)
	}

	if installedMode {
		t.Logf("orders-oauth-installed-roundtrip: PASS source_sha=%s binary_sha256=%s actual_installed=true synthetic_provider=true unexpected_provider_calls=0", installed.SHA, installed.BinarySHA256)
	} else {
		t.Logf("orders-oauth-source-roundtrip: PASS synthetic_provider=true provider_reads=%d unexpected_provider_calls=%d", providerReads(), unexpectedProviderCalls())
	}
}

func validateOrdersOAuthInstalledProduct(t *testing.T, input testconfig.InstalledSurveyInputs) {
	t.Helper()
	if _, err := validateInstalledSurveyPackage(input, installedSurveyWebTree); err != nil {
		t.Fatalf("installed Product provenance: %v", err)
	}
	if !filepath.IsAbs(input.Binary) || !filepath.IsAbs(input.Root) || !validDomesticReleaseSHA(input.SHA) || len(input.BinarySHA256) != 64 {
		t.Fatal("invalid installed OAuth package inputs")
	}
	relativeBinary, err := filepath.Rel(input.Root, input.Binary)
	if err != nil || relativeBinary == ".." || strings.HasPrefix(relativeBinary, ".."+string(filepath.Separator)) {
		t.Fatal("installed OAuth binary is outside its immutable package root")
	}
	if _, err := hex.DecodeString(input.BinarySHA256); err != nil {
		t.Fatal("invalid installed OAuth binary digest")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(input.Root, "web", "dist", "asset-manifest.json"))
	var manifest struct {
		SourceSHA string `json:"source_sha"`
	}
	if err != nil || json.Unmarshal(manifestBytes, &manifest) != nil || !validDomesticReleaseSHA(manifest.SourceSHA) {
		t.Fatal("installed OAuth package UI manifest has an invalid source SHA")
	}
	info, err := os.Stat(input.Binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&0111 == 0 {
		t.Fatal("installed OAuth binary is unavailable")
	}
	content, err := os.ReadFile(input.Binary)
	if err != nil {
		t.Fatal("read installed OAuth binary")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != input.BinarySHA256 {
		t.Fatal("installed OAuth binary digest mismatch")
	}
}
