package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLReferralH5OAuthReturnRoundTrip exercises the composed Payment
// OAuth state store and callback rather than bypassing them with a fixture
// browser session. The Provider reads are constrained to the local test server;
// no real OAuth credential, state, code, or identity is emitted by this test.
func TestPostgreSQLReferralH5OAuthReturnRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key, cert := distributionFixturePaymentCredentials(t)
	applicationServer := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer applicationServer.Close()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	cfg := platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + applicationServer.Listener.Addr().String(), ReleaseSHA: strings.Repeat("1", 40), WorkerOwner: "referral-h5-oauth", WorkerLimit: 1,
		Referral:  platformconfig.Referral{TokenDataKey: dataKey},
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "referral-h5-oauth-webhook"},
		Survey:    platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey, OAuthEnabled: true, OAuthAppID: "wx-referral-h5", OAuthSecret: "referral-h5-oauth-secret", OAuthOpenPlatformID: "referral-h5-platform", OAuthScope: "snsapi_userinfo"},
		WeChatPay: platformconfig.WeChatPay{Enabled: true, AppID: "wx-referral-payment", AppSecret: "referral-payment-secret", AppScope: "wechat-app:referral-payment", H5OAuthEnabled: true, H5AppID: "wx-referral-h5", H5AppSecret: "referral-h5-oauth-secret", H5AppScope: "wechat-app:wx-referral-h5", OrderContactDataKey: dataKey, MerchantID: "referral-h5-mch", MerchantSerial: "referral-h5-serial", PrivateKeyPath: key, PlatformCertPath: cert, APIV3Key: strings.Repeat("k", 32)},
	}
	application, err := compose(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	applicationServer.Config.Handler = application.handler
	applicationServer.StartTLS()

	weChat := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/sns/oauth2/access_token":
			if request.Method != http.MethodGet || request.URL.Query().Get("grant_type") != "authorization_code" || request.URL.Query().Get("code") != "referral-roundtrip-code" {
				http.Error(writer, "unexpected OAuth exchange", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"access_token":"referral-roundtrip-access","openid":"referral-roundtrip-openid","scope":"snsapi_userinfo"}`))
		case "/sns/userinfo":
			if request.Method != http.MethodGet || request.URL.Query().Get("access_token") != "referral-roundtrip-access" || request.URL.Query().Get("openid") != "referral-roundtrip-openid" {
				http.Error(writer, "unexpected OAuth userinfo", http.StatusBadRequest)
				return
			}
			_, _ = writer.Write([]byte(`{"openid":"referral-roundtrip-openid","unionid":"referral-roundtrip-unionid","nickname":"授权回跳测试"}`))
		default:
			http.Error(writer, "unexpected OAuth path", http.StatusBadRequest)
		}
	}))
	defer weChat.Close()
	weChatURL, err := url.Parse(weChat.URL)
	if err != nil {
		t.Fatal(err)
	}
	originalTransport := http.DefaultTransport
	transport := &referralH5OAuthTransport{base: originalTransport, target: weChatURL}
	http.DefaultTransport = transport
	t.Cleanup(func() { http.DefaultTransport = originalTransport })

	invite := "rfi_" + strings.Repeat("A", 43)
	var consumedState string
	var paymentSession string
	for _, returnPath := range []string{
		"/referral?campaign=1",
		"/referral?campaign=1&invite=" + invite,
	} {
		start := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(returnPath), "MicroMessenger Referral OAuth")
		if start.Code != http.StatusFound {
			t.Fatalf("start return=%q status=%d body=%s", returnPath, start.Code, start.Body.String())
		}
		authorization, parseErr := url.Parse(start.Header().Get("Location"))
		if parseErr != nil || authorization.Scheme != "https" || authorization.Host != "open.weixin.qq.com" || authorization.Path != "/connect/oauth2/authorize" {
			t.Fatalf("unexpected authorization location=%q err=%v", start.Header().Get("Location"), parseErr)
		}
		state := authorization.Query().Get("state")
		if state == "" {
			t.Fatal("OAuth start omitted state")
		}
		if consumedState == "" {
			consumedState = state
		}
		callback := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/callback?state="+url.QueryEscape(state)+"&code=referral-roundtrip-code", "")
		if callback.Code != http.StatusFound || callback.Header().Get("Location") != returnPath {
			t.Fatalf("callback return=%q status=%d actual=%q", returnPath, callback.Code, callback.Header().Get("Location"))
		}
		cookies := callback.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name != "aicrm_payment_session" || !cookies[0].Secure || !cookies[0].HttpOnly || !strings.HasPrefix(cookies[0].Value, "pays_") || len(cookies[0].Value) != 48 {
			t.Fatalf("callback did not issue one valid trusted session cookie")
		}
		paymentSession = cookies[0].Value
	}
	if transport.calls != 4 {
		t.Fatalf("provider calls=%d want 4", transport.calls)
	}
	replayed := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/callback?state="+url.QueryEscape(consumedState)+"&code=referral-roundtrip-code", "")
	if replayed.Code != http.StatusUnauthorized || transport.calls != 4 {
		t.Fatalf("OAuth state replay status=%d provider_calls=%d", replayed.Code, transport.calls)
	}
	var consumed int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states WHERE return_path LIKE '/referral?campaign=1%' AND consumed_at IS NOT NULL`).Scan(&consumed); err != nil || consumed != 2 {
		t.Fatalf("stored and consumed OAuth state count=%d err=%v", consumed, err)
	}

	for _, unsafeReturn := range []string{
		"https://evil.example/referral?campaign=1",
		"/referral?campaign=1&invite=" + invite + "&next=/admin",
		"/referral?invite=" + invite + "&campaign=1",
		"/referral?campaign=0",
		"/referral?campaign=9223372036854775808",
	} {
		response := referralH5OAuthRequest(t, application.handler, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(unsafeReturn), "MicroMessenger Referral OAuth")
		if response.Code != http.StatusBadRequest || response.Header().Get("Location") != "" {
			t.Fatalf("unsafe return=%q status=%d location=%q", unsafeReturn, response.Code, response.Header().Get("Location"))
		}
	}
	var states int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM payment_h5_oauth_states`).Scan(&states); err != nil || states != 2 {
		t.Fatalf("unsafe returns persisted OAuth state count=%d err=%v", states, err)
	}
	var participations, relationshipChanges int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_participations`).Scan(&participations); err != nil || participations != 0 {
		t.Fatalf("OAuth callback unexpectedly joined a Referral campaign count=%d err=%v", participations, err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_relationship_history`).Scan(&relationshipChanges); err != nil || relationshipChanges != 0 {
		t.Fatalf("OAuth callback unexpectedly changed Referral attribution count=%d err=%v", relationshipChanges, err)
	}

	// Create only the activity fixture after the real OAuth callback. The
	// customer assigned as captain comes from the provider-verified Payment
	// session, then all Referral reads and the explicit join below use public
	// HTTP and the bridge-issued browser cookie.
	paymentDigest := sha256.Sum256([]byte(paymentSession))
	var customerID, campaignID, teamID int64
	if err = application.pool.Native().QueryRow(ctx, `SELECT payer_customer_id FROM payment_sessions WHERE token_digest=$1`, paymentDigest[:]).Scan(&customerID); err != nil || customerID < 1 {
		t.Fatalf("OAuth payment session customer=%d err=%v", customerID, err)
	}
	now := time.Now().UTC()
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO referral_campaigns(name,description,reward_rules,state,starts_at,ends_at,version,created_by,created_at,updated_at) VALUES('真实OAuth队长活动','','','active',$1,$2,1,0,$3,$3) RETURNING id`, now.Add(-time.Hour), now.Add(time.Hour), now).Scan(&campaignID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO referral_teams(campaign_id,name,captain_customer_id,version,created_at,updated_at) VALUES($1,'真实OAuth测试队',$2,1,$3,$3) RETURNING id`, campaignID, customerID, now).Scan(&teamID); err != nil {
		t.Fatal(err)
	}

	bridgeRequest := httptest.NewRequest(http.MethodPost, "/api/v1/referral/session/bridge", nil)
	bridgeRequest.Header.Set("Origin", cfg.PublicOrigin)
	bridgeRequest.AddCookie(&http.Cookie{Name: "aicrm_payment_session", Value: paymentSession})
	bridgeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(bridgeResponse, bridgeRequest)
	if bridgeResponse.Code != http.StatusCreated {
		t.Fatalf("trusted payment-to-referral bridge status=%d", bridgeResponse.Code)
	}
	var browserSession, csrf string
	for _, cookie := range bridgeResponse.Result().Cookies() {
		switch cookie.Name {
		case "aicrm_distribution_session":
			browserSession = cookie.Value
		case "aicrm_distribution_csrf":
			csrf = cookie.Value
		}
	}
	if browserSession == "" || csrf == "" {
		t.Fatal("trusted bridge did not issue Referral browser session and CSRF proof")
	}
	publicRequest := func(method, path, body string, mutate bool) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		request.AddCookie(&http.Cookie{Name: "aicrm_distribution_session", Value: browserSession})
		if mutate {
			request.Header.Set("Origin", cfg.PublicOrigin)
			request.Header.Set("X-Distribution-CSRF", csrf)
			request.Header.Set("Idempotency-Key", "real-oauth-referral-journey-key")
			request.AddCookie(&http.Cookie{Name: "aicrm_distribution_csrf", Value: csrf})
			request.Header.Set("Content-Type", "application/json")
		}
		response := httptest.NewRecorder()
		application.handler.ServeHTTP(response, request)
		return response
	}
	meResponse := publicRequest(http.MethodGet, fmt.Sprintf("/api/v1/referral/campaigns/%d/me", campaignID), "", false)
	if meResponse.Code != http.StatusOK {
		t.Fatalf("trusted OAuth referral me status=%d body=%s", meResponse.Code, meResponse.Body.String())
	}
	var me map[string]any
	if err = json.Unmarshal(meResponse.Body.Bytes(), &me); err != nil || me["is_captain"] != true || me["participation"] != nil || me["captain_team"] == nil {
		t.Fatalf("unjoined trusted OAuth captain me=%v err=%v", me, err)
	}
	beforeDetails := publicRequest(http.MethodGet, fmt.Sprintf("/api/v1/referral/campaigns/%d/invitations?limit=50", campaignID), "", false)
	if beforeDetails.Code != http.StatusOK {
		t.Fatalf("unjoined trusted OAuth invitation details status=%d body=%s", beforeDetails.Code, beforeDetails.Body.String())
	}
	var detailPage map[string]any
	if err = json.Unmarshal(beforeDetails.Body.Bytes(), &detailPage); err != nil {
		t.Fatalf("unjoined trusted OAuth invitation details=%v err=%v", detailPage, err)
	}
	items, itemsOK := detailPage["items"].([]any)
	if !itemsOK || len(items) != 0 {
		t.Fatalf("unjoined trusted OAuth invitation detail items=%v", detailPage["items"])
	}
	beforeIssue := publicRequest(http.MethodPost, fmt.Sprintf("/api/v1/referral/campaigns/%d/invite", campaignID), "", true)
	if beforeIssue.Code != http.StatusForbidden || !strings.Contains(beforeIssue.Body.String(), "referral_participation_required") {
		t.Fatalf("unjoined trusted OAuth invitation issue status=%d body=%s", beforeIssue.Code, beforeIssue.Body.String())
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_participations WHERE campaign_id=$1`, campaignID).Scan(&participations); err != nil || participations != 0 {
		t.Fatalf("OAuth and details reads unexpectedly joined a Referral campaign count=%d err=%v", participations, err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_relationship_history WHERE customer_id=$1`, customerID).Scan(&relationshipChanges); err != nil || relationshipChanges != 0 {
		t.Fatalf("OAuth and details reads unexpectedly changed attribution count=%d err=%v", relationshipChanges, err)
	}
	joinedResponse := publicRequest(http.MethodPost, fmt.Sprintf("/api/v1/referral/campaigns/%d/participations", campaignID), fmt.Sprintf(`{"team_id":%d}`, teamID), true)
	if joinedResponse.Code != http.StatusOK {
		t.Fatalf("explicit trusted OAuth captain join status=%d body=%s", joinedResponse.Code, joinedResponse.Body.String())
	}
	afterDetails := publicRequest(http.MethodGet, fmt.Sprintf("/api/v1/referral/campaigns/%d/invitations?limit=50", campaignID), "", false)
	if afterDetails.Code != http.StatusOK {
		t.Fatalf("joined trusted OAuth invitation details status=%d body=%s", afterDetails.Code, afterDetails.Body.String())
	}
	issued := publicRequest(http.MethodPost, fmt.Sprintf("/api/v1/referral/campaigns/%d/invite", campaignID), "", true)
	if issued.Code != http.StatusOK || !strings.Contains(issued.Body.String(), "/referral/invite/rfi_") {
		t.Fatalf("joined trusted OAuth invitation issue status=%d body=%s", issued.Code, issued.Body.String())
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM referral_score_events WHERE campaign_id=$1`, campaignID).Scan(&relationshipChanges); err != nil || relationshipChanges != 0 {
		t.Fatalf("direct captain join unexpectedly created score facts count=%d err=%v", relationshipChanges, err)
	}
}

type referralH5OAuthTransport struct {
	base   http.RoundTripper
	target *url.URL
	calls  int
}

func (transport *referralH5OAuthTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request.URL.Scheme != "https" || request.URL.Host != "api.weixin.qq.com" || (request.URL.Path != "/sns/oauth2/access_token" && request.URL.Path != "/sns/userinfo") || request.Method != http.MethodGet {
		return nil, fmt.Errorf("unexpected H5 OAuth provider request")
	}
	transport.calls++
	forwarded := request.Clone(request.Context())
	endpoint := *transport.target
	endpoint.Path = request.URL.Path
	endpoint.RawQuery = request.URL.RawQuery
	forwarded.URL = &endpoint
	forwarded.Host = ""
	forwarded.RequestURI = ""
	return transport.base.RoundTrip(forwarded)
}

func referralH5OAuthRequest(t *testing.T, handler http.Handler, path, userAgent string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, path, nil)
	if userAgent != "" {
		request.Header.Set("User-Agent", userAgent)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
