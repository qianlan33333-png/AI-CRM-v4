package paymenthttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	channelport "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	outboundport "github.com/qianlan33333-png/AI-CRM-v3/internal/outbound/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymenth5oauth "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/h5oauth"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	paymentsession "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/session"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

type appStub struct {
	createCalls                                  int
	create                                       paymentport.CreateCommand
	handoff                                      paymentport.Handoff
	refundCalls                                  int
	refund                                       paymentport.RefundCommand
	payment                                      domain.Payment
	refundRows                                   []paymentport.RefundProjection
	refundTotal                                  int64
	listProvider                                 domain.Provider
	listMerchant                                 string
	listAllCalls                                 int
	recoveryRefund                               domain.Refund
	recoveryFound                                bool
	recoveryProvider                             domain.Provider
	recoveryMerchant, recoveryActor, recoveryKey string
}

func (stub *appStub) Create(_ context.Context, command paymentport.CreateCommand) (domain.Payment, error) {
	stub.createCalls++
	stub.create = command
	return domain.Payment{ID: 7, OrderID: 3, MerchantOrderNo: "M-7", Status: domain.StatusAwaitingPrepay, EffectID: "eer_8"}, nil
}
func (*appStub) CheckoutSessionBinding(_ context.Context, token string) (string, error) {
	binding := paymentport.CheckoutSessionBinding(token)
	if binding == "" {
		return "", paymentport.ErrSessionRequired
	}
	return binding, nil
}
func (stub *appStub) GetCheckout(_ context.Context, provider domain.Provider, _ string, _ string) (paymentport.Handoff, error) {
	if stub.handoff.Status != "" {
		return stub.handoff, nil
	}
	channel := domain.ChannelH5Official
	if provider == domain.ProviderAlipay {
		channel = domain.ChannelAlipayWap
	}
	return paymentport.Handoff{PaymentID: 7, MerchantOrder: "M-7", Provider: provider, Channel: channel, Status: domain.StatusAwaitingPayment, Payload: []byte(`{"appId":"wx-test","package":"prepay_id=safe"}`), ExpiresAt: time.Now().Add(time.Minute)}, nil
}

type paidPurchaseActionReaderStub struct {
	action productport.PaidPurchaseAction
	order  int64
	err    error
}

type paidURLLinkActionReaderStub struct {
	paidPurchaseActionReaderStub
	destination string
	calls       int
}

func (stub *paidURLLinkActionReaderStub) ResolvePaidPurchaseURLLink(_ context.Context, action productport.PaidPurchaseAction) (string, error) {
	stub.calls++
	if len(action.CompletionTarget) == 0 {
		return "", errors.New("missing URL Link target")
	}
	return stub.destination, nil
}

func (stub *paidPurchaseActionReaderStub) ReadPaidPurchaseAction(_ context.Context, orderID int64) (productport.PaidPurchaseAction, error) {
	stub.order = orderID
	return stub.action, stub.err
}

type paidPurchaseLeadQRStub struct{ value channelport.PublicLeadQRCode }

func (stub paidPurchaseLeadQRStub) ReadPublicLeadQRCode(context.Context, int64) (channelport.PublicLeadQRCode, error) {
	return stub.value, nil
}
func (stub *appStub) RequestRefund(_ context.Context, command paymentport.RefundCommand) (domain.Refund, error) {
	stub.refundCalls++
	stub.refund = command
	return domain.Refund{ID: 8, Provider: domain.ProviderWeChatPay, RefundNo: "RF-8", Status: domain.RefundRequested}, nil
}
func (*appStub) ApplyVerifiedCallback(context.Context, paymentprovider.CallbackResult) error {
	return nil
}
func (*appStub) ApplyVerifiedShopCallback(context.Context, paymentport.ShopRefundCallback) error {
	return nil
}
func (*appStub) ReconcileShopRefund(context.Context, int64) (domain.Refund, error) {
	return domain.Refund{}, nil
}
func (*appStub) ReconcileWeChatPayPayment(context.Context, int64) (domain.Payment, error) {
	return domain.Payment{}, nil
}
func (*appStub) ReconcileWeChatPayRefund(context.Context, int64) (domain.Refund, error) {
	return domain.Refund{}, nil
}
func (stub *appStub) FindPayment(context.Context, domain.Provider, string) (domain.Payment, error) {
	if stub.payment.ID != 0 {
		return stub.payment, nil
	}
	return domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-9", Status: domain.StatusPaid}, nil
}
func (stub *appStub) FindRefundRecoveryReceipt(_ context.Context, provider domain.Provider, merchantOrderNo, actorScope, key string) (domain.Refund, bool, error) {
	stub.recoveryProvider, stub.recoveryMerchant, stub.recoveryActor, stub.recoveryKey = provider, merchantOrderNo, actorScope, key
	return stub.recoveryRefund, stub.recoveryFound, nil
}
func (stub *appStub) GetPayment(context.Context, int64) (domain.Payment, error) {
	return stub.FindPayment(context.Background(), domain.ProviderWeChatPay, "")
}
func (stub *appStub) ListRefunds(context.Context, int32, int32) ([]paymentport.RefundProjection, int64, error) {
	stub.listAllCalls++
	return stub.refundRows, stub.refundTotal, nil
}
func (stub *appStub) ListRefundsForPayment(_ context.Context, provider domain.Provider, merchant string, _ int32, _ int32) ([]paymentport.RefundProjection, int64, error) {
	stub.listProvider, stub.listMerchant = provider, merchant
	return stub.refundRows, stub.refundTotal, nil
}
func (*appStub) ListOrderEffects(context.Context, domain.Provider, string) ([]paymentport.EffectProjection, error) {
	return nil, nil
}

type securityStub struct{}

func (securityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

func TestPaymentBusinessWriteRoleMatrix(t *testing.T) {
	for _, test := range []struct {
		name  string
		value accessdomain.Principal
		want  bool
	}{
		{name: "admin", value: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, want: true},
		{name: "super admin", value: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 2, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}, want: true},
		{name: "viewer", value: accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 3, Roles: []accessdomain.Role{accessdomain.RoleViewer}}, want: false},
		{name: "staff admin is outside payment surface", value: accessdomain.Principal{Kind: accessdomain.KindStaff, InternalID: 4, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := paymentBusinessWriteRole(test.value); got != test.want {
				t.Fatalf("paymentBusinessWriteRole(%+v)=%v want %v", test.value, got, test.want)
			}
		})
	}
}

type h5OAuthStub struct {
	completeError error
	enabled       bool
	starts        int
	completes     int
	returnPath    string
	issued        paymentsession.Issued
}

func (stub *h5OAuthStub) Enabled() bool { return stub.enabled }
func (stub *h5OAuthStub) Start(_ context.Context, returnPath string) (string, error) {
	stub.starts++
	stub.returnPath = returnPath
	if returnPath != "/pay/course-7" && returnPath != "/distribution?product_id=7&product_type=standard_product" && returnPath != "/referral" && returnPath != "/referral?campaign=7" && returnPath != "/referral?campaign=7&invite=rfi_"+strings.Repeat("A", 43) && returnPath != "/s/term-31/pay?promotion_context=dpc_"+strings.Repeat("A", 43) {
		return "", paymenth5oauth.ErrInvalid
	}
	return "https://open.weixin.qq.com/oauth", nil
}
func (stub *h5OAuthStub) Complete(_ context.Context, state, code string) (paymentsession.Issued, string, error) {
	stub.completes++
	return stub.issued, "/pay/course-7", stub.completeError
}
func (stub *h5OAuthStub) RecoverReturnPath(_ context.Context, state string) (string, error) {
	if !paymenth5oauth.ValidReturnPath(stub.returnPath) {
		return "", paymenth5oauth.ErrInvalid
	}
	return stub.returnPath, nil
}

type sessionVerifierStub struct{ fact identitydomain.VerifiedFact }

func (stub sessionVerifierStub) VerifyCode(context.Context, string) (identitydomain.VerifiedFact, error) {
	return stub.fact, nil
}

type sessionIssuerStub struct {
	command paymentsession.IssueCommand
}

func (stub *sessionIssuerStub) IssueTrusted(_ context.Context, command paymentsession.IssueCommand) (paymentsession.Issued, error) {
	stub.command = command
	return paymentsession.Issued{Token: "pays_session_token_0000000001", ExpiresAt: time.Now().Add(10 * time.Minute)}, nil
}

func TestTrustedSessionEndpointVerifiesCodeAndSetsOpaqueCookie(t *testing.T) {
	fact, err := identitydomain.NewVerifiedFact(identitydomain.ProviderVerifiedIdentityInput{Kind: identitydomain.KindMPOpenID, Scope: "wechat-app:wx-app", Value: "openid-1", Source: "wechat_miniprogram"})
	if err != nil {
		t.Fatal(err)
	}
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	issuer := &sessionIssuerStub{}
	if err = handler.SetTrustedSessionIssuer(sessionVerifierStub{fact: fact}, issuer); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/sessions", strings.NewReader(`{"code":"one-time-code"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "session-issue-key-0001")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	cookies := response.Result().Cookies()
	if response.Code != http.StatusCreated || len(cookies) != 1 || cookies[0].Name != SessionCookieName || !cookies[0].HttpOnly || !issuer.command.Fact.Valid() || strings.Contains(response.Body.String(), "openid") {
		t.Fatalf("status=%d cookies=%+v command=%+v body=%s", response.Code, cookies, issuer.command, response.Body.String())
	}
}
func (securityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return accessdomain.Principal{InternalID: 1, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}, nil
}

type recoverySecurityStub struct{ principal accessdomain.Principal }

func (stub recoverySecurityStub) Authenticate(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, nil
}
func (stub recoverySecurityStub) AuthorizeCSRF(context.Context, *http.Request) (accessdomain.Principal, error) {
	return stub.principal, nil
}

func TestRefundListScopesOrderDetailToExactPayment(t *testing.T) {
	statuses := []domain.RefundStatus{
		domain.RefundCompleted,
		domain.RefundHistoryRequested,
		domain.RefundHistoryProcessing,
		domain.RefundHistoryFailed,
		domain.RefundHistoryClosed,
		domain.RefundStatus("legacy_unclassified"),
	}
	rows := make([]paymentport.RefundProjection, 0, len(statuses))
	for index, status := range statuses {
		rows = append(rows, paymentport.RefundProjection{
			Refund:  domain.Refund{ID: int64(31 + index), PaymentID: 21, Provider: domain.ProviderWeChatPay, RefundNo: "RF-" + strconv.Itoa(31+index), AmountMinor: 200, Status: status, CreatedAt: time.Date(2026, 9, 10, 4, 10, 38, 0, time.UTC)},
			OrderID: 12, MerchantOrder: "WXP2609100410381093CE4C5B0C", OrderAmount: 200000, Currency: "CNY",
		})
	}
	application := &appStub{refundRows: rows, refundTotal: int64(len(rows))}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/refunds?provider=wechat&order_no=WXP2609100410381093CE4C5B0C", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var page struct {
		Items []struct {
			RefundID            string `json:"refund_id"`
			Status              string `json:"status"`
			TransactionID       string `json:"transaction_id"`
			ExternalEffectState string `json:"external_effect_state"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	wantStatuses := []string{"completed", "history_requested", "history_processing", "history_failed", "history_closed", "unknown"}
	if response.Code != http.StatusOK || application.listProvider != domain.ProviderWeChatPay || application.listMerchant != "WXP2609100410381093CE4C5B0C" || len(page.Items) != len(wantStatuses) {
		t.Fatalf("status=%d provider=%q merchant=%q items=%+v", response.Code, application.listProvider, application.listMerchant, page.Items)
	}
	for index, want := range wantStatuses {
		item := page.Items[index]
		if item.Status != want || item.TransactionID != "" || item.ExternalEffectState != "" {
			t.Fatalf("item=%d got=%+v want status=%q and empty compatibility placeholders", index, item, want)
		}
	}
	for _, path := range []string{"/api/admin/refunds?provider=wechat", "/api/admin/refunds?order_no=WXP2609100410381093CE4C5B0C", "/api/admin/refunds?provider=v3pay&order_no=WXP2609100410381093CE4C5B0C"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/admin/refunds?provider=all", nil))
	if response.Code != http.StatusOK || application.listAllCalls != 1 {
		t.Fatalf("all-provider compatibility status=%d unscopedCalls=%d", response.Code, application.listAllCalls)
	}
}
func TestRefundRecoveryReceiptIsPaymentActorAndKeyScoped(t *testing.T) {
	application := &appStub{recoveryFound: true, recoveryRefund: domain.Refund{ID: 31, PaymentID: 21, Provider: domain.ProviderWeChatPay, RefundNo: "RF-recovery-31", Status: domain.RefundCompleted, EffectID: "eer_41"}}
	security := &recoverySecurityStub{principal: accessdomain.Principal{InternalID: 17, Kind: accessdomain.KindAdmin, Roles: []accessdomain.Role{accessdomain.RoleAdmin}}}
	handler, err := NewHandler(application, nil, security, true)
	if err != nil {
		t.Fatal(err)
	}
	key := "refund-recovery-key-0001"
	request := httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery?provider=wechat&order_no=M-recovery-21", nil)
	request.Header.Set("Idempotency-Key", key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var found map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	actorBinding, bindingOK := found["actor_binding"].(string)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" || application.recoveryProvider != domain.ProviderWeChatPay || application.recoveryMerchant != "M-recovery-21" || application.recoveryActor != "admin:17" || application.recoveryKey != key || found["found"] != true || found["receipt_id"] != float64(31) || !bindingOK || len(actorBinding) != 64 || strings.Contains(response.Body.String(), key) || strings.Contains(response.Body.String(), "admin:17") || found["external_effect_state"] != nil {
		t.Fatalf("status=%d provider=%q merchant=%q actor=%q key=%q body=%s", response.Code, application.recoveryProvider, application.recoveryMerchant, application.recoveryActor, application.recoveryKey, response.Body.String())
	}
	for _, path := range []string{
		"/api/admin/refunds/recovery?provider=wechat",
		"/api/admin/refunds/recovery?order_no=M-recovery-21",
		"/api/admin/refunds/recovery?provider=wechat&order_no=M-recovery-21&extra=1",
	} {
		response = httptest.NewRecorder()
		request = httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Idempotency-Key", key)
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path=%s status=%d", path, response.Code)
		}
	}
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery?provider=wechat&order_no=M-recovery-21", nil)
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("missing key status=%d", response.Code)
	}

	application.recoveryFound = false // A different actor/key/Payment must be indistinguishable from no receipt.
	security.principal.InternalID = 18
	response = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/admin/refunds/recovery?provider=wechat&order_no=M-other-payment", nil)
	request.Header.Set("Idempotency-Key", "other-actor-or-key-0001")
	handler.ServeHTTP(response, request)
	var noMatch map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &noMatch); err != nil {
		t.Fatal(err)
	}
	otherBinding, otherBindingOK := noMatch["actor_binding"].(string)
	if response.Code != http.StatusOK || noMatch["found"] != false || !otherBindingOK || len(otherBinding) != 64 || otherBinding == actorBinding || strings.Contains(response.Body.String(), "admin:18") || application.recoveryActor != "admin:18" {
		t.Fatalf("nonmatch status=%d actor=%q body=%s", response.Code, application.recoveryActor, response.Body.String())
	}
}

func TestCompatRefundRequiresVerifiedWeChatTransactionID(t *testing.T) {
	transactionID := "4500000365202609101828595865"
	application := &appStub{payment: domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_order", Status: domain.StatusPaid, ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transactionID))}}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/admin/wechat-pay/orders/v3pay_order/refunds"
	post := func(confirmation string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"provider":"wechat","order_no":"v3pay_order","refund_amount_total":200,"reason":"客户申请","transaction_id_confirmation":"`+confirmation+`","checked":true}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "refund-confirmation-test-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := post("v3pay_order"); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
		t.Fatalf("merchant fallback status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
	}
	if response := post(""); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
		t.Fatalf("missing transaction status=%d calls=%d", response.Code, application.refundCalls)
	}
	if response := post(transactionID); response.Code != http.StatusAccepted || application.refundCalls != 1 || application.refund.PaymentID != 9 {
		t.Fatalf("verified transaction status=%d calls=%d command=%+v body=%s", response.Code, application.refundCalls, application.refund, response.Body.String())
	}
}

func TestRefundConfirmationHonorsProviderBoundary(t *testing.T) {
	transactionID := "4500000365202609101828595865"
	payment := domain.Payment{ID: 9, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "v3pay_order", Status: domain.StatusPaid, ProviderTransactionDigest: string(effectport.Hash("wechatpay.transaction", transactionID))}

	t.Run("generic WeChat Pay endpoint requires the verified transaction", func(t *testing.T) {
		application := &appStub{payment: payment}
		handler, err := NewHandler(application, nil, securityStub{}, true)
		if err != nil {
			t.Fatal(err)
		}
		post := func(confirmation string) *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/9/refunds", strings.NewReader(`{"amount_minor":200,"refund_no":"RF-generic","reason":"客户申请","transaction_id_confirmation":"`+confirmation+`"}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "refund-generic-test-key")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			return response
		}
		if response := post("v3pay_order"); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("merchant fallback status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
		}
		if response := post(""); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("missing transaction status=%d calls=%d", response.Code, application.refundCalls)
		}
		if response := post(transactionID); response.Code != http.StatusAccepted || application.refundCalls != 1 || application.refund.PaymentID != payment.ID {
			t.Fatalf("verified transaction status=%d calls=%d command=%+v body=%s", response.Code, application.refundCalls, application.refund, response.Body.String())
		}
	})

	t.Run("WeChat Shop preserves its order confirmation contract", func(t *testing.T) {
		shopPayment := payment
		shopPayment.Provider = domain.ProviderWeChatShop
		shopPayment.ProviderTransactionDigest = ""
		application := &appStub{payment: shopPayment}
		handler, err := NewHandler(application, nil, securityStub{}, true, true)
		if err != nil {
			t.Fatal(err)
		}
		post := func(confirmation string) *httptest.ResponseRecorder {
			request := httptest.NewRequest(http.MethodPost, "/api/admin/refunds", strings.NewReader(`{"provider":"wechat_shop","order_no":"v3pay_order","product_id":"product-1","sku_id":"sku-1","refund_count":1,"refund_amount_total":200,"reason_code":"10000000","reason":"客户申请","transaction_id_confirmation":"`+confirmation+`","checked":true}`))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "refund-shop-test-key")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			return response
		}
		if response := post(transactionID); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("non-order confirmation status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
		}
		if response := post(""); response.Code != http.StatusBadRequest || application.refundCalls != 0 {
			t.Fatalf("missing order confirmation status=%d calls=%d", response.Code, application.refundCalls)
		}
		if response := post("v3pay_order"); response.Code != http.StatusAccepted || application.refundCalls != 1 || application.refund.PaymentID != shopPayment.ID {
			t.Fatalf("exact shop order status=%d calls=%d command=%+v body=%s", response.Code, application.refundCalls, application.refund, response.Body.String())
		}
	})

	t.Run("non-WeChat generic payment preserves its existing contract", func(t *testing.T) {
		application := &appStub{payment: domain.Payment{ID: 9, Provider: domain.Provider("alipay")}}
		handler, _ := NewHandler(application, nil, securityStub{}, true)
		request := httptest.NewRequest(http.MethodPost, "/api/admin/payments/9/refunds", strings.NewReader(`{"amount_minor":200,"refund_no":"RF-generic","reason":"客户申请"}`))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Idempotency-Key", "refund-generic-test-key")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusAccepted || application.refundCalls != 1 {
			t.Fatalf("non-WeChat status=%d calls=%d body=%s", response.Code, application.refundCalls, response.Body.String())
		}
	})
}
func TestCheckoutAcceptsOnlyOpaqueCookieIdentity(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	token := "pays_session_token_0000000001"
	binding := paymentport.CheckoutSessionBinding(token)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "checkout-key-0000001")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || application.createCalls != 1 || application.create.ProductID != 3 || application.create.CouponClaimID != 0 || application.create.ProductType != "standard" || application.create.BeneficiarySelection != paymentport.BeneficiarySelectionPayerSelf || application.create.SessionToken == "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("code=%d command=%+v body=%s", response.Code, application.create, response.Body.String())
	}
	for _, rawField := range []string{"customer_id", "beneficiary_customer_id", "openid", "unionid", "assurance"} {
		request = httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`","`+rawField+`":"attacker"}`))
		request.Header.Set("Content-Type", "application/json")
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("field=%s code=%d", rawField, response.Code)
		}
	}
}

func TestAlipayCheckoutRouteBindsProviderAndReadback(t *testing.T) {
	application := &appStub{handoff: paymentport.Handoff{
		PaymentID: 7, OrderID: 3, MerchantOrder: "M-alipay-7", Provider: domain.ProviderAlipay,
		Channel: domain.ChannelAlipayWap, Status: domain.StatusAwaitingPayment,
		Payload: []byte(`{"redirectUrl":"https://virtual-alipay.example.test/pay/7"}`), ExpiresAt: time.Now().Add(time.Minute),
	}}
	handler, err := NewHandler(application, nil, securityStub{}, true, true, true)
	if err != nil {
		t.Fatal(err)
	}
	token := "pays_session_token_0000000001"
	binding := paymentport.CheckoutSessionBinding(token)
	create := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(`{"product_id":7,"product_kind":"standard","provider":"alipay","channel":"alipay_wap","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`"}`))
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set("Idempotency-Key", "checkout-alipay-key-0001")
	create.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	created := httptest.NewRecorder()
	handler.ServeHTTP(created, create)
	if created.Code != http.StatusAccepted || application.create.Provider != string(domain.ProviderAlipay) || application.create.Channel != domain.ChannelAlipayWap {
		t.Fatalf("create code=%d command=%+v body=%s", created.Code, application.create, created.Body.String())
	}

	status := httptest.NewRequest(http.MethodGet, "/api/v1/alipay/checkouts/M-alipay-7", nil)
	status.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, status)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"provider":"alipay"`) || !strings.Contains(response.Body.String(), `"channel":"alipay_wap"`) || !strings.Contains(response.Body.String(), `"redirectUrl":"https://virtual-alipay.example.test/pay/7"`) || application.handoff.Provider != domain.ProviderAlipay {
		t.Fatalf("status code=%d handoff=%+v body=%s", response.Code, application.handoff, response.Body.String())
	}

	mismatch := httptest.NewRequest(http.MethodPost, "/api/v1/alipay/checkouts", strings.NewReader(`{"product_id":7,"product_kind":"standard","provider":"wechat_pay","beneficiary_selection":"payer_self","checkout_session_binding":"`+binding+`"}`))
	mismatch.Header.Set("Content-Type", "application/json")
	mismatch.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	rejected := httptest.NewRecorder()
	handler.ServeHTTP(rejected, mismatch)
	if rejected.Code != http.StatusConflict || !strings.Contains(rejected.Body.String(), "payment_provider_mismatch") || application.createCalls != 1 {
		t.Fatalf("mismatch code=%d calls=%d body=%s", rejected.Code, application.createCalls, rejected.Body.String())
	}
}

func TestCheckoutAcceptsOnlyWellFormedExplicitPromotionContext(t *testing.T) {
	token := "pays_session_token_0000000001"
	binding := paymentport.CheckoutSessionBinding(token)
	promotion := "dpc_" + base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))
	if len(promotion) != 47 {
		t.Fatal("test promotion token shape")
	}
	for _, test := range []struct {
		name, context string
		want          string
	}{
		{"valid explicit context", promotion, promotion},
		{"ordinary checkout", "", ""},
		{"malformed context", "dpc_" + strings.Repeat("!", 43), ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			application := &appStub{}
			handler, _ := NewHandler(application, nil, securityStub{}, true)
			body := `{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"` + binding + `"}`
			if test.context != "" {
				body = `{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"` + binding + `","promotion_context":"` + test.context + `"}`
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("Idempotency-Key", "checkout-promotion-key-0002")
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if test.name == "malformed context" {
				if response.Code != http.StatusBadRequest || application.createCalls != 0 {
					t.Fatalf("malformed context code=%d calls=%d", response.Code, application.createCalls)
				}
				return
			}
			if response.Code != http.StatusAccepted || application.create.PromotionContext != test.want {
				t.Fatalf("code=%d promotion=%q want=%q", response.Code, application.create.PromotionContext, test.want)
			}
		})
	}
}

func TestCheckoutRejectsSessionBindingBeforeCallingApplication(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/wechat-pay/checkouts", strings.NewReader(`{"product_id":3,"product_kind":"standard","beneficiary_selection":"payer_self","checkout_session_binding":"wrong"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "checkout-key-0000002")
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000002"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"session_mismatch"`) || application.createCalls != 0 {
		t.Fatalf("code=%d calls=%d body=%s", response.Code, application.createCalls, response.Body.String())
	}
}

func TestCheckoutSessionBindingIsOpaqueAndRequiresTrustedCookie(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "payment_session_required") {
		t.Fatalf("missing cookie code=%d body=%s", response.Code, response.Body.String())
	}
	token := "pays_session_token_0000000001"
	request = httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkout-session", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: token})
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || strings.Contains(response.Body.String(), token) || !strings.Contains(response.Body.String(), paymentport.CheckoutSessionBinding(token)) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

func TestH5OAuthStartRequiresWeChatAndDisabledMakesZeroCalls(t *testing.T) {
	handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
	disabled := &h5OAuthStub{}
	if err := handler.SetH5OAuth(disabled); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || disabled.starts != 0 {
		t.Fatalf("code=%d starts=%d", response.Code, disabled.starts)
	}
	enabled := &h5OAuthStub{enabled: true}
	_ = handler.SetH5OAuth(enabled)
	request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=https%3A%2F%2Fevil.test%2Fpay%2Fcourse-7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || enabled.starts != 1 {
		t.Fatalf("code=%d starts=%d", response.Code, enabled.starts)
	}
	request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://open.weixin.qq.com/oauth" {
		t.Fatalf("code=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	for _, returnPath := range []string{
		"/referral",
		"/referral?campaign=7",
		"/referral?campaign=7&invite=rfi_" + strings.Repeat("A", 43),
	} {
		request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape(returnPath), nil)
		request.Header.Set("User-Agent", "MicroMessenger")
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusFound || response.Header().Get("Location") != "https://open.weixin.qq.com/oauth" || enabled.returnPath != returnPath {
			t.Fatalf("referral return=%q oauth=%d location=%q actual=%q", returnPath, response.Code, response.Header().Get("Location"), enabled.returnPath)
		}
	}
	request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fdistribution%3Fproduct_id%3D7%26product_type%3Dstandard_product", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://open.weixin.qq.com/oauth" || enabled.returnPath != "/distribution?product_id=7&product_type=standard_product" {
		t.Fatalf("distribution oauth=%d location=%q return=%q", response.Code, response.Header().Get("Location"), enabled.returnPath)
	}
	promotion := "dpc_" + strings.Repeat("A", 43)
	request = httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url="+url.QueryEscape("/s/term-31/pay?promotion_context="+promotion), nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusFound || response.Header().Get("Location") != "https://open.weixin.qq.com/oauth" || enabled.returnPath != "/s/term-31/pay?promotion_context="+promotion {
		t.Fatalf("promotion oauth=%d location=%q return=%q", response.Code, response.Header().Get("Location"), enabled.returnPath)
	}
}

func TestH5OAuthFailureReturnsToOriginalLoginGateWithoutPayment(t *testing.T) {
	handler, err := NewHandler(&appStub{}, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	oauth := &h5OAuthStub{enabled: true, completeError: paymenth5oauth.ErrInvalid}
	if err := handler.SetH5OAuth(oauth); err != nil {
		t.Fatal(err)
	}
	start := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7", nil)
	start.Header.Set("User-Agent", "MicroMessenger")
	started := httptest.NewRecorder()
	handler.ServeHTTP(started, start)
	if started.Code != http.StatusFound || len(started.Result().Cookies()) != 1 {
		t.Fatalf("start status=%d cookies=%v", started.Code, started.Result().Cookies())
	}
	returnCookie := started.Result().Cookies()[0]
	if !returnCookie.Secure || !returnCookie.HttpOnly || returnCookie.SameSite != http.SameSiteLaxMode || returnCookie.Path != "/api/h5/wechat-pay/oauth/callback" {
		t.Fatalf("insecure recovery cookie: %+v", returnCookie)
	}
	callback := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=used&code=denied", nil)
	callback.AddCookie(returnCookie)
	failed := httptest.NewRecorder()
	handler.ServeHTTP(failed, callback)
	if failed.Code != http.StatusSeeOther || failed.Header().Get("Location") != "/pay/course-7" || oauth.completes != 1 {
		t.Fatalf("failed callback status=%d location=%q completes=%d", failed.Code, failed.Header().Get("Location"), oauth.completes)
	}
	// The consumed state still identifies the original gate if WeChat drops
	// the short-lived cookie on the provider callback.
	withoutCookie := httptest.NewRecorder()
	handler.ServeHTTP(withoutCookie, httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=used&code=denied", nil))
	if withoutCookie.Code != http.StatusSeeOther || withoutCookie.Header().Get("Location") != "/pay/course-7" {
		t.Fatalf("cookie-less callback status=%d location=%q", withoutCookie.Code, withoutCookie.Header().Get("Location"))
	}
	oauth.returnPath = ""
	for _, malicious := range []string{"https://evil.test/pay/course-7", "//evil.test", "/pay/course-7?next=evil"} {
		callback := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=used&code=denied", nil)
		callback.AddCookie(&http.Cookie{Name: h5OAuthReturnCookieName, Value: base64.RawURLEncoding.EncodeToString([]byte(malicious))})
		failed := httptest.NewRecorder()
		handler.ServeHTTP(failed, callback)
		if failed.Code != http.StatusUnauthorized || failed.Header().Get("Location") != "" {
			t.Fatalf("unsafe return %q status=%d location=%q", malicious, failed.Code, failed.Header().Get("Location"))
		}
	}
}

func TestH5OAuthExplicitDenialReturnsToGateWithoutExchangingCode(t *testing.T) {
	handler, err := NewHandler(&appStub{}, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	oauth := &h5OAuthStub{enabled: true, returnPath: "/pay/course-7"}
	if err := handler.SetH5OAuth(oauth); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{
		"state=used&error=access_denied",
		"state=used&error=authdeny",
		"state=used&code=authdeny",
		"state=used",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?"+suffix, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/pay/course-7" || oauth.completes != 0 {
			t.Fatalf("denial %q status=%d location=%q exchanges=%d", suffix, response.Code, response.Header().Get("Location"), oauth.completes)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=used&error=unrecognized", nil))
	if response.Code != http.StatusBadRequest || oauth.completes != 0 {
		t.Fatalf("unknown callback status=%d exchanges=%d", response.Code, oauth.completes)
	}
}

func TestH5OAuthStartMapsUnavailableStateReservationToServiceUnavailable(t *testing.T) {
	handler, err := NewHandler(&appStub{}, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	oauth := &h5OAuthStub{enabled: true}
	if err = handler.SetH5OAuth(h5OAuthStartErrorStub{oauth: oauth, err: paymenth5oauth.ErrUnavailable}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?return_url=%2Freferral%3Fcampaign%3D7", nil)
	request.Header.Set("User-Agent", "MicroMessenger")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"payment_h5_oauth_unavailable"`) || strings.Contains(response.Body.String(), "unavailable") == false {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
}

type h5OAuthStartErrorStub struct {
	oauth *h5OAuthStub
	err   error
}

func (stub h5OAuthStartErrorStub) Enabled() bool { return stub.oauth.Enabled() }
func (stub h5OAuthStartErrorStub) Start(ctx context.Context, returnPath string) (string, error) {
	_, _ = stub.oauth.Start(ctx, returnPath)
	return "", stub.err
}
func (stub h5OAuthStartErrorStub) Complete(ctx context.Context, state, code string) (paymentsession.Issued, string, error) {
	return stub.oauth.Complete(ctx, state, code)
}
func (stub h5OAuthStartErrorStub) RecoverReturnPath(ctx context.Context, state string) (string, error) {
	return stub.oauth.RecoverReturnPath(ctx, state)
}

func TestH5OAuthRejectsDuplicateAndUnknownQueryBeforeApplication(t *testing.T) {
	t.Run("start", func(t *testing.T) {
		for _, rawQuery := range []string{
			"return_url=%2Fpay%2Fcourse-7&return_url=%2Fpay%2Fcourse-7",
			"return_url=%2Fpay%2Fcourse-7&return_url=%2Fdistribution%3Fproduct_id%3D7%26product_type%3Dstandard_product",
			"return_url=%2Fpay%2Fcourse-7&unexpected=1",
		} {
			t.Run(rawQuery, func(t *testing.T) {
				handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
				oauth := &h5OAuthStub{enabled: true}
				if err := handler.SetH5OAuth(oauth); err != nil {
					t.Fatal(err)
				}
				request := httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/start?"+rawQuery, nil)
				request.Header.Set("User-Agent", "MicroMessenger")
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusBadRequest || oauth.starts != 0 || oauth.completes != 0 {
					t.Fatalf("code=%d starts=%d completes=%d", response.Code, oauth.starts, oauth.completes)
				}
			})
		}
	})

	t.Run("callback", func(t *testing.T) {
		for _, rawQuery := range []string{
			"state=opaque&state=opaque&code=opaque",
			"state=opaque&state=other&code=opaque",
			"state=opaque&code=opaque&code=opaque",
			"state=opaque&code=other&code=opaque",
			"state=opaque&code=opaque&unexpected=1",
		} {
			t.Run(rawQuery, func(t *testing.T) {
				handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
				oauth := &h5OAuthStub{enabled: true}
				if err := handler.SetH5OAuth(oauth); err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?"+rawQuery, nil))
				if response.Code != http.StatusBadRequest || oauth.starts != 0 || oauth.completes != 0 {
					t.Fatalf("code=%d starts=%d completes=%d", response.Code, oauth.starts, oauth.completes)
				}
			})
		}
	})
}

func TestTrustedCookieSecurityAttributes(t *testing.T) {
	response := httptest.NewRecorder()
	err := WriteTrustedSessionCookie(response, paymentsession.Issued{Token: "pays_session_token_0000000001", ExpiresAt: time.Now().Add(time.Minute)})
	cookies := response.Result().Cookies()
	if err != nil || len(cookies) != 1 || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != "/" {
		t.Fatalf("cookies=%+v err=%v", cookies, err)
	}
}

func TestCheckoutHandoffPollingKeepsIdentityOpaqueAndSessionUntilTerminalStatus(t *testing.T) {
	application := &appStub{}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-7", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ready":true`) {
		t.Fatalf("code=%d body=%s", response.Code, response.Body.String())
	}
	if cookies := response.Result().Cookies(); len(cookies) != 0 {
		t.Fatalf("unexpected terminal cookie clear=%+v", cookies)
	}
}

func TestCheckoutStatusExposesFrozenPurchaseActionOnlyAfterAuthorizedPaidCheckout(t *testing.T) {
	application := &appStub{handoff: paymentport.Handoff{PaymentID: 7, OrderID: 31, MerchantOrder: "M-paid-7", Status: domain.StatusPaid}}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	actions := &paidPurchaseActionReaderStub{action: productport.PaidPurchaseAction{OrderPaidEventID: 9, OrderID: 31, ProductID: 4, ProductVersion: 2, Enabled: true, Mode: productport.PaidPurchaseActionRedirect, RedirectURL: "/after-paid", TagState: "not_configured", CreatedAt: time.Now()}}
	if err = handler.SetPaidPurchaseActionReader(actions, paidPurchaseLeadQRStub{}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-paid-7", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusAccepted || actions.order != 31 || !strings.Contains(body, `"completion_action":{"mode":"redirect","redirect_url":"/after-paid","state":"available"}`) || strings.Contains(body, `"tag_state"`) || len(response.Result().Cookies()) != 0 {
		t.Fatalf("code=%d order=%d body=%s cookies=%+v", response.Code, actions.order, body, response.Result().Cookies())
	}
	// A reload is still bound to the same trusted payer session and order. It
	// can recover the immutable action but cannot mint a second checkout.
	retry := httptest.NewRecorder()
	handler.ServeHTTP(retry, request)
	if retry.Code != http.StatusAccepted || !strings.Contains(retry.Body.String(), `"completion_action":{"mode":"redirect","redirect_url":"/after-paid","state":"available"}`) || len(retry.Result().Cookies()) != 0 {
		t.Fatalf("reload code=%d body=%s cookies=%+v", retry.Code, retry.Body.String(), retry.Result().Cookies())
	}
}

func TestCheckoutURLLinkResolvesOnlyThroughTheAuthorizedPaidOrder(t *testing.T) {
	application := &appStub{handoff: paymentport.Handoff{PaymentID: 7, OrderID: 31, MerchantOrder: "M-url-link-7", Status: domain.StatusPaid}}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	actions := &paidURLLinkActionReaderStub{paidPurchaseActionReaderStub: paidPurchaseActionReaderStub{action: productport.PaidPurchaseAction{OrderPaidEventID: 9, OrderID: 31, ProductID: 4, ProductVersion: 2, Enabled: true, Mode: productport.PaidPurchaseActionRedirect, CompletionTarget: []byte(`{"enabled":true,"type":"url_link","source_url":"https://source.example.test/secret","response_key":"url_link"}`), TagState: "not_configured", CreatedAt: time.Now()}}, destination: "https://destination.example.test/after-paid"}
	if err = handler.SetPaidPurchaseActionReader(actions, paidPurchaseLeadQRStub{}); err != nil {
		t.Fatal(err)
	}
	statusRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-url-link-7", nil)
	statusRequest.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	statusResponse := httptest.NewRecorder()
	handler.ServeHTTP(statusResponse, statusRequest)
	if statusResponse.Code != http.StatusAccepted || !strings.Contains(statusResponse.Body.String(), `"redirect_url":"/api/v1/wechat-pay/checkouts/M-url-link-7/completion-target"`) || strings.Contains(statusResponse.Body.String(), "source.example.test") {
		t.Fatalf("status code=%d body=%s", statusResponse.Code, statusResponse.Body.String())
	}
	resolveRequest := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-url-link-7/completion-target", nil)
	resolveRequest.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	resolveResponse := httptest.NewRecorder()
	handler.ServeHTTP(resolveResponse, resolveRequest)
	if resolveResponse.Code != http.StatusFound || resolveResponse.Header().Get("Location") != actions.destination || actions.calls != 1 {
		t.Fatalf("resolve code=%d location=%q calls=%d", resolveResponse.Code, resolveResponse.Header().Get("Location"), actions.calls)
	}
	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-url-link-7/completion-target", nil))
	if denied.Code != http.StatusUnauthorized || actions.calls != 1 {
		t.Fatalf("unauthorized code=%d calls=%d", denied.Code, actions.calls)
	}
}

type commerceOrderReaderStub struct {
	value orderport.CommercePushDeliveryReference
	err   error
}

func (s commerceOrderReaderStub) CommercePushDeliveryReference(_ context.Context, provider orderdomain.Provider, reference string) (orderport.CommercePushDeliveryReference, error) {
	if provider != orderdomain.ProviderWeChatPay || reference != "legacy-order-1" {
		return orderport.CommercePushDeliveryReference{}, orderport.ErrNotFound
	}
	return s.value, s.err
}

type commerceDeliveryReaderStub struct {
	query outboundport.CommercePushDeliveryQuery
	rows  []outboundport.CommercePushDelivery
	err   error
}

func (s *commerceDeliveryReaderStub) ListCommercePushDeliveries(_ context.Context, query outboundport.CommercePushDeliveryQuery) ([]outboundport.CommercePushDelivery, error) {
	s.query = query
	return s.rows, s.err
}

func TestOrderExternalPushDeliveriesUsesOrderAndOutboundPorts(t *testing.T) {
	application := &appStub{}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	attempted, executed := true, true
	reader := &commerceDeliveryReaderStub{rows: []outboundport.CommercePushDelivery{{ID: "current:19", Source: "current", EffectID: "eer_19", State: "outcome_unknown", AttemptCount: 2, ProviderCallAttempted: &attempted, RealExternalCallExecuted: &executed, UpdatedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)}}}
	if err = handler.SetCommercePushDeliveryReaders(commerceOrderReaderStub{value: orderport.CommercePushDeliveryReference{OrderID: 7, PaidEventID: 11, HistoricalMappingState: "current"}}, reader); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/legacy-order-1/external-push-deliveries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.query.PaidEventID != 11 || reader.query.HistoricalSourceKey != "" || !strings.Contains(response.Body.String(), `"outcome_unknown"`) || !strings.Contains(response.Body.String(), `"real_external_call_executed":true`) || strings.Contains(response.Body.String(), "payment") {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, reader.query, response.Body.String())
	}
}

func TestOrderExternalPushDeliveriesLeavesUnpaidNativeOrderEmpty(t *testing.T) {
	application := &appStub{}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	reader := &commerceDeliveryReaderStub{rows: []outboundport.CommercePushDelivery{{ID: "must-not-read"}}}
	if err = handler.SetCommercePushDeliveryReaders(commerceOrderReaderStub{value: orderport.CommercePushDeliveryReference{OrderID: 99, HistoricalMappingState: "current"}}, reader); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/legacy-order-1/external-push-deliveries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.query.PaidEventID != 0 || reader.query.HistoricalSourceKey != "" || !strings.Contains(response.Body.String(), `"history_mapping_state":"current"`) || !strings.Contains(response.Body.String(), `"total":0`) || strings.Contains(response.Body.String(), "must-not-read") {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, reader.query, response.Body.String())
	}
}

func TestOrderExternalPushDeliveriesLeavesUnmappedHistoryPending(t *testing.T) {
	application := &appStub{}
	handler, err := NewHandler(application, nil, securityStub{}, true)
	if err != nil {
		t.Fatal(err)
	}
	reader := &commerceDeliveryReaderStub{rows: []outboundport.CommercePushDelivery{{ID: "must-not-read"}}}
	if err = handler.SetCommercePushDeliveryReaders(commerceOrderReaderStub{value: orderport.CommercePushDeliveryReference{OrderID: 99, HistoricalMappingState: "pending"}}, reader); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/legacy-order-1/external-push-deliveries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || reader.query.PaidEventID != 0 || reader.query.HistoricalSourceKey != "" || !strings.Contains(response.Body.String(), `"history_mapping_state":"pending"`) || strings.Contains(response.Body.String(), "must-not-read") {
		t.Fatalf("code=%d query=%+v body=%s", response.Code, reader.query, response.Body.String())
	}
}

func TestCheckoutStatusReportsUnknownWithoutHandoffOrClearingSession(t *testing.T) {
	application := &appStub{handoff: paymentport.Handoff{PaymentID: 7, MerchantOrder: "M-7", Status: domain.StatusAwaitingPrepay, PrepayState: "outcome_unknown"}}
	handler, _ := NewHandler(application, nil, securityStub{}, true)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wechat-pay/checkouts/M-7", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "pays_session_token_0000000001"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusAccepted || !strings.Contains(body, `"prepay_state":"outcome_unknown"`) || !strings.Contains(body, `"ready":false`) || strings.Contains(body, `"handoff"`) || len(response.Result().Cookies()) != 0 {
		t.Fatalf("unexpected checkout: code=%d body=%s", response.Code, body)
	}
}

func TestCallbackApplicationFailureStageIsFixedAndSafe(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: paymentport.ErrInvalid, want: "application_invalid"},
		{err: fmt.Errorf("wrapped: %w", paymentport.ErrNotFound), want: "application_not_found"},
		{err: paymentport.ErrConflict, want: "application_conflict"},
		{err: errors.New("transaction 4200000000000000 customer 7"), want: "application_unavailable"},
	}
	for _, test := range tests {
		if got := callbackApplicationFailureStage(test.err); got != test.want {
			t.Fatalf("error=%v stage=%q want=%q", test.err, got, test.want)
		}
	}
}

func TestOAuthIdentityConflictExplainsReviewWithoutRedirect(t *testing.T) {
	handler, _ := NewHandler(&appStub{}, nil, securityStub{}, true)
	_ = handler.SetH5OAuth(&h5OAuthStub{enabled: true, completeError: paymenth5oauth.ErrIdentityConflict})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/h5/wechat-pay/oauth/callback?state=opaque&code=opaque", nil))
	if response.Code != http.StatusUnauthorized || !strings.Contains(response.Body.String(), "历史账号资料需要核对") || response.Header().Get("Location") != "" || !strings.Contains(response.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("unexpected OAuth response: %d", response.Code)
	}
}
