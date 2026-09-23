package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	productapp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/app"
	producthttp "github.com/qianlan33333-png/AI-CRM-v3/internal/product/http"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type checkoutJourneyCatalog struct{ product productport.Product }

func (catalog *checkoutJourneyCatalog) Get(_ context.Context, id productport.ID) (productport.Product, error) {
	if id != catalog.product.ID {
		return productport.Product{}, productapp.ErrNotFound
	}
	return catalog.product, nil
}
func (catalog *checkoutJourneyCatalog) GetByCode(_ context.Context, code string) (productport.Product, error) {
	if code != catalog.product.ProductCode {
		return productport.Product{}, productapp.ErrNotFound
	}
	return catalog.product, nil
}

type checkoutJourneySecurity struct{}

func (checkoutJourneySecurity) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}
func (checkoutJourneySecurity) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{}, nil
}

type checkoutJourneyRecord struct {
	command     paymentport.CreateCommand
	merchant    string
	createCalls int
	statusCalls int
}

// checkoutJourneyApplication deliberately exercises the real Payment HTTP
// decoder/cookie boundary while keeping this browser test outside Provider IO.
// Its receipt map has the same essential behavior as the Payment application:
// one opaque-session/key/payload has one payment, and another session cannot
// access or replay it.
type checkoutJourneyApplication struct {
	mu      sync.Mutex
	records map[string]*checkoutJourneyRecord
}

func (app *checkoutJourneyApplication) Create(_ context.Context, command paymentport.CreateCommand) (paymentdomain.Payment, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if existing := app.records[command.IdempotencyKey]; existing != nil {
		existing.createCalls++
		if existing.command.SessionToken != command.SessionToken || existing.command.CheckoutSessionBinding != command.CheckoutSessionBinding || existing.command.ProductID != command.ProductID || existing.command.ProductType != command.ProductType || existing.command.CouponClaimID != command.CouponClaimID || existing.command.MobileE164 != command.MobileE164 || existing.command.BeneficiarySelection != command.BeneficiarySelection || existing.command.PromotionContext != command.PromotionContext {
			return paymentdomain.Payment{}, paymentport.ErrConflict
		}
		return paymentdomain.Payment{ID: int64(len(app.records)), OrderID: int64(len(app.records)), MerchantOrderNo: existing.merchant, Status: paymentdomain.StatusAwaitingPayment}, nil
	}
	merchant := fmt.Sprintf("journey-order-%d", len(app.records)+1)
	app.records[command.IdempotencyKey] = &checkoutJourneyRecord{command: command, merchant: merchant, createCalls: 1}
	return paymentdomain.Payment{ID: int64(len(app.records)), OrderID: int64(len(app.records)), MerchantOrderNo: merchant, Status: paymentdomain.StatusAwaitingPayment}, nil
}

func (*checkoutJourneyApplication) CheckoutSessionBinding(_ context.Context, token string) (string, error) {
	binding := paymentport.CheckoutSessionBinding(token)
	if binding == "" {
		return "", paymentport.ErrSessionRequired
	}
	return binding, nil
}

// This recovery fixture keeps purchase availability independent of prior cases;
// purchase-once and consumed-session rules have dedicated application tests.
func (*checkoutJourneyApplication) CanCreateCheckout(context.Context, string) (bool, error) {
	return true, nil
}
func (*checkoutJourneyApplication) PurchaseStatus(context.Context, string, string, int64) (paymentport.PurchaseState, error) {
	return paymentport.PurchaseState{State: "available", CanPurchase: true}, nil
}

func checkoutJourneyTrustedPayer(token string) int {
	switch token {
	case "trusted-payment-session-one", "trusted-payment-session-two":
		return 1 // renewed OAuth for the same verified Payment identity
	default:
		return 0
	}
}

func (app *checkoutJourneyApplication) GetCheckout(_ context.Context, merchantOrderNo, sessionToken string) (paymentport.Handoff, error) {
	app.mu.Lock()
	defer app.mu.Unlock()
	for _, record := range app.records {
		if record.merchant != merchantOrderNo {
			continue
		}
		if checkoutJourneyTrustedPayer(record.command.SessionToken) == 0 || checkoutJourneyTrustedPayer(record.command.SessionToken) != checkoutJourneyTrustedPayer(sessionToken) {
			return paymentport.Handoff{}, paymentport.ErrConflict
		}
		record.statusCalls++
		switch record.command.CouponClaimID {
		case 11:
			return paymentport.Handoff{MerchantOrder: merchantOrderNo, Status: paymentdomain.StatusPaid}, nil
		case 12:
			if record.statusCalls >= 3 {
				return paymentport.Handoff{MerchantOrder: merchantOrderNo, Status: paymentdomain.StatusPaid}, nil
			}
			return paymentport.Handoff{MerchantOrder: merchantOrderNo, Status: paymentdomain.StatusAwaitingPayment, Payload: []byte(`{"appId":"wx-test","package":"prepay_id=checkout"}`), ExpiresAt: time.Now().Add(time.Minute)}, nil
		case 13:
			// A known merchant order can be recovered by a renewed trusted session
			// for the same payer. It must never call Create or mint another key.
			if sessionToken == "trusted-payment-session-two" {
				return paymentport.Handoff{MerchantOrder: merchantOrderNo, Status: paymentdomain.StatusPaid}, nil
			}
			return paymentport.Handoff{MerchantOrder: merchantOrderNo, Status: paymentdomain.StatusAwaitingPrepay}, nil
		default:
			return paymentport.Handoff{MerchantOrder: merchantOrderNo, Status: paymentdomain.StatusAwaitingPrepay}, nil
		}
	}
	return paymentport.Handoff{}, paymentport.ErrNotFound
}

func (*checkoutJourneyApplication) RequestRefund(context.Context, paymentport.RefundCommand) (paymentdomain.Refund, error) {
	return paymentdomain.Refund{}, paymentport.ErrUnavailable
}
func (*checkoutJourneyApplication) ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error {
	return paymentport.ErrUnavailable
}
func (*checkoutJourneyApplication) ApplyVerifiedShopCallback(context.Context, paymentport.ShopRefundCallback) error {
	return paymentport.ErrUnavailable
}
func (*checkoutJourneyApplication) ReconcileShopRefund(context.Context, int64) (paymentdomain.Refund, error) {
	return paymentdomain.Refund{}, paymentport.ErrUnavailable
}
func (*checkoutJourneyApplication) ReconcileWeChatPayPayment(context.Context, int64) (paymentdomain.Payment, error) {
	return paymentdomain.Payment{}, paymentport.ErrUnavailable
}
func (*checkoutJourneyApplication) ReconcileWeChatPayRefund(context.Context, int64) (paymentdomain.Refund, error) {
	return paymentdomain.Refund{}, paymentport.ErrUnavailable
}
func (*checkoutJourneyApplication) FindPayment(context.Context, paymentdomain.Provider, string) (paymentdomain.Payment, error) {
	return paymentdomain.Payment{}, paymentport.ErrNotFound
}
func (*checkoutJourneyApplication) GetPayment(context.Context, int64) (paymentdomain.Payment, error) {
	return paymentdomain.Payment{}, paymentport.ErrNotFound
}
func (*checkoutJourneyApplication) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}
func (*checkoutJourneyApplication) ListRefundsForPayment(context.Context, paymentdomain.Provider, string, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	return nil, 0, nil
}
func (*checkoutJourneyApplication) ListOrderEffects(context.Context, paymentdomain.Provider, string) ([]paymentport.EffectProjection, error) {
	return nil, nil
}

func (app *checkoutJourneyApplication) snapshot() map[string]checkoutJourneyRecord {
	app.mu.Lock()
	defer app.mu.Unlock()
	out := make(map[string]checkoutJourneyRecord, len(app.records))
	for key, record := range app.records {
		out[key] = *record
	}
	return out
}

type checkoutJourneyResponseLoss struct {
	next http.Handler
	mu   sync.Mutex
	lost bool
}

func (handler *checkoutJourneyResponseLoss) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost || request.URL.Path != "/api/v1/wechat-pay/checkouts" {
		handler.next.ServeHTTP(writer, request)
		return
	}
	recorder := httptest.NewRecorder()
	handler.next.ServeHTTP(recorder, request)
	handler.mu.Lock()
	loss := !handler.lost && recorder.Code == http.StatusAccepted
	if loss {
		handler.lost = true
	}
	handler.mu.Unlock()
	if loss {
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"code":"response_lost"}`))
		return
	}
	for key, values := range recorder.Header() {
		writer.Header()[key] = append([]string(nil), values...)
	}
	writer.WriteHeader(recorder.Code)
	_, _ = writer.Write(recorder.Body.Bytes())
}

// TestPublicCheckoutBrowserJourney runs the rendered page script against the
// actual public and Payment HTTP handlers. Its JSDOM journey covers lost
// create responses, frozen request replay, WeChat cancellation, unknown
// results, unavailable local storage, and opaque-session switching.
func TestPublicCheckoutBrowserJourney(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Fatal("node is required for public checkout browser journey")
	}
	now := time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)
	catalog := &checkoutJourneyCatalog{product: productport.Product{
		ID: 7, ProductCode: "course-7", Name: "恢复测试商品", Description: "浏览器付款恢复", PriceMinor: 990, Currency: "CNY", CreatedBy: 99, CreatedAt: now, UpdatedAt: now, Version: 3,
		LocalLifecycle:        productport.LocalProductEnabled,
		LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"status":"active","enabled":true,"buy_button_text":"立即购买","require_mobile":true,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`),
	}}
	public, err := producthttp.NewPublicHandler(catalog)
	if err != nil {
		t.Fatal(err)
	}
	application := &checkoutJourneyApplication{records: make(map[string]*checkoutJourneyRecord)}
	server := newPublicCheckoutJourneyServer(t, public, application)
	defer server.Close()

	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate public checkout journey")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	journey := filepath.Join(root, "cmd", "aicrm", "public_checkout_journey.mjs")
	command := exec.Command("node", journey)
	command.Dir = root
	command.Env = append(os.Environ(),
		"AICRM_PUBLIC_CHECKOUT_JOURNEY_BASE_URL="+server.URL,
		"AICRM_PUBLIC_CHECKOUT_JOURNEY_FIRST_COOKIE="+paymentport.TrustedSessionCookieName+"=trusted-payment-session-one",
		"AICRM_PUBLIC_CHECKOUT_JOURNEY_SECOND_COOKIE="+paymentport.TrustedSessionCookieName+"=trusted-payment-session-two",
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("public checkout browser journey: %v\n%s", err, output)
	}

	records := application.snapshot()
	if len(records) != 4 {
		t.Fatalf("records=%+v", records)
	}
	lost := records["checkout-journey-1"]
	if lost.createCalls != 2 || lost.statusCalls != 2 || lost.command.CouponClaimID != 11 || lost.command.MobileE164 != "+8613800138000" || lost.command.PromotionContext != "dpc_"+strings.Repeat("A", 43) {
		t.Fatalf("lost-response replay=%+v", lost)
	}
	cancelled := records["checkout-journey-2"]
	if cancelled.createCalls != 1 || cancelled.statusCalls != 3 {
		t.Fatalf("cancelled resumed record=%+v", cancelled)
	}
	if switched := records["checkout-journey-4"]; switched.command.SessionToken != "trusted-payment-session-one" || switched.createCalls != 1 {
		t.Fatalf("session switch must not create under the second session: %+v", switched)
	}
	if _, exists := records["checkout-journey-5"]; exists {
		t.Fatalf("unavailable browser storage must block checkout request: %+v", records["checkout-journey-5"])
	}
	if platformconfig.ChromiumJourneyRequired() {
		runPublicCheckoutChromiumJourney(t, public, root)
	}
}

func newPublicCheckoutJourneyServer(t *testing.T, public http.Handler, application *checkoutJourneyApplication) *httptest.Server {
	t.Helper()
	payment, err := paymenthttp.NewHandler(application, nil, checkoutJourneySecurity{}, true)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/pay/", public)
	mux.Handle("/api/v1/wechat-pay/", &checkoutJourneyResponseLoss{next: payment})
	mux.HandleFunc("/api/h5/coupons/available", func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Query().Get("target_ref") != "standard_product:7" {
			http.NotFound(writer, request)
			return
		}
		writer.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = writer.Write([]byte(`{"items":[]}`))
	})
	return httptest.NewServer(mux)
}

func runPublicCheckoutChromiumJourney(t *testing.T, public http.Handler, root string) {
	t.Helper()
	application := &checkoutJourneyApplication{records: make(map[string]*checkoutJourneyRecord)}
	server := newPublicCheckoutJourneyServer(t, public, application)
	defer server.Close()
	command := exec.Command("node", filepath.Join(root, "cmd", "aicrm", "public_checkout_chromium_journey.mjs"))
	command.Dir = root
	command.Env = append(os.Environ(),
		"AICRM_PUBLIC_CHECKOUT_CHROMIUM_BASE_URL="+server.URL,
		"AICRM_PUBLIC_CHECKOUT_CHROMIUM_SESSION="+paymentport.TrustedSessionCookieName+"=trusted-payment-session-one",
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "public_checkout_chromium: PASS") {
		t.Fatalf("public checkout Chromium journey: %v\n%s", err, output)
	}
	records := application.snapshot()
	if len(records) != 1 {
		t.Fatalf("Chromium records=%+v", records)
	}
	for key, record := range records {
		if record.createCalls != 2 || record.command.PromotionContext != "dpc_"+strings.Repeat("A", 43) || record.command.CouponClaimID != 11 {
			t.Fatalf("Chromium recovered record %q=%+v", key, record)
		}
	}
}
