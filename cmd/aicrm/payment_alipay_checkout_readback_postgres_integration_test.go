package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentstore "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
	alipaysdk "github.com/smartwalle/alipay/v3"
)

type alipayCheckoutCreateResult struct {
	OrderID       int64  `json:"order_id"`
	PaymentID     int64  `json:"payment_id"`
	MerchantOrder string `json:"merchant_order_no"`
	EffectID      string `json:"effect_id"`
}

type alipayCheckoutStatusResult struct {
	Provider      paymentdomain.Provider `json:"provider"`
	Channel       paymentdomain.Channel  `json:"channel"`
	Status        paymentdomain.Status   `json:"status"`
	MerchantOrder string                 `json:"merchant_order_no"`
	Ready         bool                   `json:"ready"`
	PrepayState   string                 `json:"prepay_state"`
	Handoff       struct {
		RedirectURL string `json:"redirectUrl"`
	} `json:"handoff"`
}

// TestPostgreSQLAlipayCheckoutReadbackJourney uses the full sorted migration
// set, actual Order/Payment/EER repositories and public HTTP handler. The test
// adapter only signs a synthetic URL on the reserved .test host; it never
// submits a payment or contacts a Provider endpoint.
func TestPostgreSQLAlipayCheckoutReadbackJourney(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	requireLocalPostgreSQL16(t, ctx)
	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 90*time.Second, productExternalPushChromiumFixtureOptions{enablePublicH5: true, enableAlipay: true})

	pageProductID := seedAlipayPageCheckoutProduct(t, fixture)
	for _, test := range []struct {
		name      string
		productID int64
		channel   paymentdomain.Channel
		key       string
		provider  paymentdomain.Provider
	}{
		{name: "WAP", productID: fixture.productID, channel: paymentdomain.ChannelAlipayWap, key: "alipay-readback-wap-create-0001", provider: paymentdomain.ProviderAlipay},
		{name: "Page", productID: pageProductID, channel: paymentdomain.ChannelAlipayPage, key: "alipay-readback-page-create-0001", provider: paymentdomain.ProviderAlipay},
	} {
		t.Run(test.name, func(t *testing.T) {
			session := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "alipay-readback-"+strings.ToLower(test.name)+"-session-0001")
			created := createPublicAlipayCheckout(t, fixture, session.token, test.productID, test.channel, test.key)
			if created.OrderID < 1 || created.PaymentID < 1 || created.MerchantOrder == "" || created.EffectID == "" {
				t.Fatalf("incomplete synthetic checkout receipt: %+v", created)
			}
			status := awaitPublicAlipayHandoff(t, fixture, session.token, created.MerchantOrder, test.channel)
			parsed, err := url.Parse(status.Handoff.RedirectURL)
			var bizContent struct {
				OutTradeNo string `json:"out_trade_no"`
				Subject    string `json:"subject"`
			}
			decodeErr := json.Unmarshal([]byte(parsed.Query().Get("biz_content")), &bizContent)
			gateway, gatewayErr := url.Parse(fixture.alipayGateway)
			if err != nil || decodeErr != nil || gatewayErr != nil || parsed.Scheme != gateway.Scheme || parsed.Host != gateway.Host || bizContent.OutTradeNo != created.MerchantOrder || strings.TrimSpace(bizContent.Subject) == "" {
				t.Fatalf("synthetic %s handoff URL=%q parsed=%+v err=%v", test.name, status.Handoff.RedirectURL, parsed, err)
			}
			assertAlipayCheckoutPersistence(t, fixture, created, test.channel)
			assertLegacyAlipayIntentFallback(t, fixture, created, test.channel)

			// Provider-scoped lookup must not expose the same Alipay order through
			// the WeChat URL, even though both providers share this HTTP surface.
			wrongRoute := httptest.NewRecorder()
			wrongRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/"+created.MerchantOrder, nil)
			wrongRequest.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: session.token})
			fixture.application.handler.ServeHTTP(wrongRoute, wrongRequest)
			if wrongRoute.Code != http.StatusNotFound {
				t.Fatalf("WeChat route read Alipay order: status=%d body=%s", wrongRoute.Code, wrongRoute.Body.String())
			}
		})
	}

	// Invalid channel and provider combinations are rejected before any order,
	// payment, intent, or provider work is created.
	badSession := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "alipay-readback-invalid-session-0001")
	binding := publicAlipayCheckoutBinding(t, fixture, badSession.token)
	for _, invalid := range []struct {
		name, provider string
		channel        paymentdomain.Channel
		wantCode       string
		wantStatus     int
	}{
		{name: "unknown provider", provider: "virtual_cash", channel: paymentdomain.ChannelAlipayWap, wantCode: "payment_provider_mismatch", wantStatus: http.StatusConflict},
		{name: "WeChat channel on Alipay route", provider: string(paymentdomain.ProviderAlipay), channel: paymentdomain.ChannelH5Official, wantCode: "invalid_request", wantStatus: http.StatusBadRequest},
	} {
		t.Run(invalid.name, func(t *testing.T) {
			before := alipayCheckoutCounts(t, fixture)
			body := fmt.Sprintf(`{"product_id":%d,"product_kind":"standard","provider":%q,"channel":%q,"beneficiary_selection":"payer_self","checkout_session_binding":%q}`, fixture.productID, invalid.provider, invalid.channel, binding)
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "alipay-readback-invalid-"+strings.ReplaceAll(strings.ToLower(invalid.name), " ", "-"))
			request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: badSession.token})
			fixture.application.handler.ServeHTTP(response, request)
			if response.Code != invalid.wantStatus || !strings.Contains(response.Body.String(), invalid.wantCode) {
				t.Fatalf("invalid %s status=%d body=%s", invalid.name, response.Code, response.Body.String())
			}
			after := alipayCheckoutCounts(t, fixture)
			if after != before {
				t.Fatalf("invalid %s created checkout facts before=%+v after=%+v", invalid.name, before, after)
			}
		})
	}
}

func assertLegacyAlipayIntentFallback(t *testing.T, fixture *productExternalPushChromiumFixture, created alipayCheckoutCreateResult, channel paymentdomain.Channel) {
	t.Helper()
	kind := effectport.KindAlipayWapPay
	if channel == paymentdomain.ChannelAlipayPage {
		kind = effectport.KindAlipayPagePay
	}
	var source effectport.Digest
	var expected string
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT i.source_ref_digest,oi.product_name FROM payment_provider_intents i JOIN order_items oi ON oi.order_id=(SELECT order_id FROM payments WHERE id=i.payment_id) WHERE i.payment_id=$1`, created.PaymentID).Scan(&source, &expected); err != nil {
		t.Fatal("read synthetic Alipay legacy fallback facts")
	}
	if _, err := fixture.application.pool.Native().Exec(fixture.ctx, `UPDATE payment_provider_intents SET request_snapshot=request_snapshot-'subject' WHERE payment_id=$1`, created.PaymentID); err != nil {
		t.Fatal("remove synthetic snapshot subject to emulate pre-fix intent")
	}
	uow, err := platformpostgres.NewUnitOfWork(fixture.application.pool)
	if err != nil {
		t.Fatal("create test repository unit of work")
	}
	repository := paymentstore.NewPostgreSQL()
	read := func() (paymentport.ProviderIntent, error) {
		var intent paymentport.ProviderIntent
		err := uow.Within(fixture.ctx, func(tx context.Context) error {
			var readErr error
			intent, readErr = repository.ProviderIntent(tx, kind, source)
			return readErr
		})
		return intent, err
	}
	intent, err := read()
	if err != nil || intent.Subject != expected || intent.ProductID != "" {
		t.Fatalf("legacy subject should come from immutable order item, not ProductID: intent=%+v expected=%q err=%v", intent, expected, err)
	}

}

type alipayCheckoutFactCounts struct {
	Orders, Payments, Intents, Handoffs int
}

func requireLocalPostgreSQL16(t *testing.T, ctx context.Context) {
	t.Helper()
	raw, err := platformconfig.DatabaseURL()
	if err != nil {
		t.Skip("AICRM_DATABASE_URL is not configured; skipping isolated Alipay PostgreSQL16 journey")
	}
	config, err := pgxpool.ParseConfig(raw)
	if err != nil {
		t.Fatal("AICRM_DATABASE_URL is not a valid PostgreSQL URL")
	}
	host := config.ConnConfig.Host
	if host != "" && !strings.HasPrefix(host, "/") && !strings.EqualFold(host, "localhost") {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			t.Skip("Alipay integration requires a loopback-only test database; refusing any remote database")
		}
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Skipf("local PostgreSQL test database is not available: %v", err)
	}
	defer pool.Close()
	var version int
	if err = pool.QueryRow(ctx, `SELECT current_setting('server_version_num')::integer`).Scan(&version); err != nil {
		t.Fatal("read local PostgreSQL server version")
	}
	if version/10000 != 16 {
		t.Skipf("requires local PostgreSQL 16; detected major version %d", version/10000)
	}
}

func virtualAlipayFixtureCredentials(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	publicDER, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	privatePath := filepath.Join(t.TempDir(), "virtual-alipay-private.pem")
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})
	if err = os.WriteFile(privatePath, privatePEM, 0600); err != nil {
		t.Fatal(err)
	}
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: publicDER}))
	return privatePath, publicPEM
}

func newVirtualAlipayGateway(t *testing.T, privateKeyPath string) *httptest.Server {
	t.Helper()
	privatePEM, err := os.ReadFile(privateKeyPath)
	if err != nil {
		t.Fatal("read synthetic Alipay signing key")
	}
	signer, err := alipaysdk.New("virtual-alipay-test-app", string(privatePEM), true)
	if err != nil {
		t.Fatal("create synthetic Alipay response signer")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("method") != "alipay.trade.query" {
			t.Errorf("unexpected synthetic Alipay gateway request: method=%q err=%v", r.Form.Get("method"), err)
			http.Error(w, "unexpected synthetic request", http.StatusBadRequest)
			return
		}
		var request struct {
			OutTradeNo string `json:"out_trade_no"`
		}
		if err := json.Unmarshal([]byte(r.Form.Get("biz_content")), &request); err != nil || request.OutTradeNo == "" {
			t.Errorf("invalid synthetic Alipay query material: err=%v", err)
			http.Error(w, "invalid synthetic query", http.StatusBadRequest)
			return
		}
		body, err := json.Marshal(map[string]string{
			"code": "10000", "msg": "Success", "out_trade_no": request.OutTradeNo,
			"trade_status": "WAIT_BUYER_PAY", "total_amount": "99.00",
		})
		if err != nil {
			t.Errorf("marshal synthetic Alipay response: %v", err)
			http.Error(w, "invalid synthetic response", http.StatusInternalServerError)
			return
		}
		signature, err := signer.SignBytes(body)
		if err != nil {
			t.Errorf("sign synthetic Alipay response: %v", err)
			http.Error(w, "invalid synthetic signature", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"alipay_trade_query_response":%s,"sign":%q}`, body, base64.StdEncoding.EncodeToString(signature))
	}))
	t.Cleanup(server.Close)
	return server
}

func seedAlipayPageCheckoutProduct(t *testing.T, fixture *productExternalPushChromiumFixture) int64 {
	t.Helper()
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	var id int64
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('alipay-page-readback','支付宝 Page 回读夹具','仅用于合成支付链接测试',9900,'CNY',10,1,$1::jsonb) RETURNING id`, projection).Scan(&id)
	if err != nil {
		t.Fatal("seed synthetic Alipay Page product")
	}
	return id
}

func publicAlipayCheckoutBinding(t *testing.T, fixture *productExternalPushChromiumFixture, token string) string {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(response, request)
	var body struct {
		Binding string `json:"checkout_session_binding"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || body.Binding == "" {
		t.Fatalf("checkout session binding status=%d body=%s", response.Code, response.Body.String())
	}
	return body.Binding
}

func createPublicAlipayCheckout(t *testing.T, fixture *productExternalPushChromiumFixture, token string, productID int64, channel paymentdomain.Channel, key string) alipayCheckoutCreateResult {
	t.Helper()
	binding := publicAlipayCheckoutBinding(t, fixture, token)
	body := fmt.Sprintf(`{"product_id":%d,"product_kind":"standard","provider":"alipay","channel":%q,"beneficiary_selection":"payer_self","checkout_session_binding":%q}`, productID, channel, binding)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
	fixture.application.handler.ServeHTTP(response, request)
	var result alipayCheckoutCreateResult
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &result) != nil || result.MerchantOrder == "" {
		t.Fatalf("create Alipay %s checkout status=%d body=%s", channel, response.Code, response.Body.String())
	}
	return result
}

func awaitPublicAlipayHandoff(t *testing.T, fixture *productExternalPushChromiumFixture, token, merchant string, channel paymentdomain.Channel) alipayCheckoutStatusResult {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	lastStatus, lastBody := 0, ""
	for time.Now().Before(deadline) {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/alipay/checkouts/"+merchant, nil)
		request.AddCookie(&http.Cookie{Name: paymentport.TrustedSessionCookieName, Value: token})
		fixture.application.handler.ServeHTTP(response, request)
		lastStatus, lastBody = response.Code, response.Body.String()
		var status alipayCheckoutStatusResult
		if (response.Code == http.StatusOK || response.Code == http.StatusAccepted) && json.Unmarshal(response.Body.Bytes(), &status) == nil {
			if status.Provider != paymentdomain.ProviderAlipay || status.Channel != channel || status.MerchantOrder != merchant {
				t.Fatalf("provider/channel/order mismatch: %+v", status)
			}
			if status.Ready && status.Handoff.RedirectURL != "" {
				return status
			}
		} else {
			t.Fatalf("Alipay status readback status=%d body=%s", response.Code, response.Body.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	var paymentStatus, effectState, effectKind string
	var attempts int
	queryErr := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT p.status,e.state,e.kind,e.attempt_count FROM payments p JOIN external_effects e ON e.id=p.external_effect_id WHERE p.merchant_order_no=$1`, merchant).Scan(&paymentStatus, &effectState, &effectKind, &attempts)
	t.Fatalf("timed out waiting for synthetic Alipay %s link for %s: last status=%d body=%s payment=%q effect=%q/%q attempts=%d query_err=%v", channel, merchant, lastStatus, lastBody, paymentStatus, effectState, effectKind, attempts, queryErr)
	return alipayCheckoutStatusResult{}
}

func assertAlipayCheckoutPersistence(t *testing.T, fixture *productExternalPushChromiumFixture, created alipayCheckoutCreateResult, channel paymentdomain.Channel) {
	t.Helper()
	wantKind := "alipay_wap_pay_v1"
	if channel == paymentdomain.ChannelAlipayPage {
		wantKind = "alipay_page_pay_v1"
	}
	var provider, storedChannel, paymentStatus, effectKind, intentKind, effectState, redirectURL string
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `
SELECT p.provider,p.payment_channel,p.status,e.kind,i.effect_kind,e.state,h.payload->>'redirectUrl'
FROM payments p
JOIN orders o ON o.id=p.order_id
JOIN external_effects e ON e.id=p.external_effect_id
JOIN payment_provider_intents i ON i.payment_id=p.id
JOIN payment_handoffs h ON h.payment_id=p.id
WHERE p.id=$1 AND o.id=$2 AND o.merchant_order_no=$3`, created.PaymentID, created.OrderID, created.MerchantOrder).Scan(&provider, &storedChannel, &paymentStatus, &effectKind, &intentKind, &effectState, &redirectURL)
	if err != nil || provider != string(paymentdomain.ProviderAlipay) || storedChannel != string(channel) || paymentStatus != string(paymentdomain.StatusAwaitingPayment) || effectKind != wantKind || intentKind != wantKind || effectState != "executed" || redirectURL == "" {
		t.Fatalf("order/payment/intent/handoff persistence provider=%q channel=%q status=%q effect=%q intent=%q state=%q urlPresent=%t err=%v", provider, storedChannel, paymentStatus, effectKind, intentKind, effectState, redirectURL != "", err)
	}
}

func alipayCheckoutCounts(t *testing.T, fixture *productExternalPushChromiumFixture) alipayCheckoutFactCounts {
	t.Helper()
	var counts alipayCheckoutFactCounts
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT
(SELECT count(*) FROM orders WHERE record_origin='native' AND provider='alipay'),
(SELECT count(*) FROM payments WHERE provider='alipay'),
(SELECT count(*) FROM payment_provider_intents WHERE effect_kind IN ('alipay_wap_pay_v1','alipay_page_pay_v1')),
(SELECT count(*) FROM payment_handoffs)`).Scan(&counts.Orders, &counts.Payments, &counts.Intents, &counts.Handoffs)
	if err != nil {
		t.Fatal("read synthetic Alipay checkout counts")
	}
	return counts
}
