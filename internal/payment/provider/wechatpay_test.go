package provider

import (
	"context"
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type doerFunc func(*http.Request) (*http.Response, error)

func (fn doerFunc) Do(request *http.Request) (*http.Response, error) { return fn(request) }

type loaderStub struct{ material Material }

func (stub loaderStub) Load(context.Context, effectport.Kind, effectport.Digest) (Material, error) {
	return stub.material, nil
}

type profitSharingLoaderStub struct {
	material      ProfitSharingMaterial
	referenceKind effectport.Kind
}

func (stub profitSharingLoaderStub) Load(context.Context, effectport.Kind, effectport.Digest) (Material, error) {
	return Material{}, errors.New("ordinary material must not load for profit sharing")
}
func (stub profitSharingLoaderStub) LoadProfitSharing(context.Context, effectport.Kind, effectport.Digest) (ProfitSharingMaterial, error) {
	return stub.material, nil
}
func (stub profitSharingLoaderStub) LoadProfitSharingReference(context.Context, string) (effectport.Kind, ProfitSharingMaterial, error) {
	kind := stub.referenceKind
	if kind == "" {
		kind = effectport.KindWeChatPayProfitSharing
	}
	return kind, stub.material, nil
}

type profitSharingSDKStub struct {
	add, create, unfreeze int
	query                 ProfitSharingQuery
	queryMaterial         ProfitSharingMaterial
}

func (stub *profitSharingSDKStub) AddReceiver(context.Context, ProfitSharingMaterial) error {
	stub.add++
	return nil
}
func (stub *profitSharingSDKStub) CreateOrder(context.Context, ProfitSharingMaterial) error {
	stub.create++
	return nil
}
func (stub *profitSharingSDKStub) QueryOrder(_ context.Context, material ProfitSharingMaterial) (ProfitSharingQuery, error) {
	stub.queryMaterial = material
	return stub.query, nil
}

func TestProfitSharingReconciliationUsesOriginalInstructionMaterial(t *testing.T) {
	payload := effectport.Hash("profit-sharing-query-payload")
	when := time.Date(2026, 9, 14, 5, 0, 0, 0, time.UTC)
	material := ProfitSharingMaterial{PayloadDigest: payload, AppID: "wx-app", ReceiverAccount: "open-id", TransactionReference: "4200000001", ProviderOrderNo: "v3ps_ABCDEFGHIJKLMNOPQRST", AmountMinor: 12}
	sdk := &profitSharingSDKStub{query: ProfitSharingQuery{State: "FINISHED", ReceiverConfirmedSuccess: true, OutcomeKnown: true, OccurredAt: when}}
	provider := &WeChatPay{config: Config{Enabled: true}, loader: profitSharingLoaderStub{material: material}, profitSharing: sdk}
	result, err := provider.QueryProfitSharing(context.Background(), "psinst_7")
	if err != nil || !result.ReceiverConfirmedSuccess || !result.OutcomeKnown || result.State != "FINISHED" || result.OccurredAt != when || !effectport.ValidDigest(result.EvidenceDigest) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	sdk.query = ProfitSharingQuery{State: "FINISHED", OutcomeKnown: true, OccurredAt: when}
	result, err = provider.QueryProfitSharing(context.Background(), "psinst_7")
	if err != nil || result.ReceiverConfirmedSuccess || result.ReceiverConfirmedFailure || result.OutcomeKnown {
		t.Fatalf("aggregate completion without exact receiver outcome became terminal: %+v err=%v", result, err)
	}
	sdk.query = ProfitSharingQuery{State: "FINISHED", ReceiverConfirmedFailure: true, OutcomeKnown: true, OccurredAt: when}
	result, err = provider.QueryProfitSharing(context.Background(), "psinst_7")
	if err != nil || result.ReceiverConfirmedSuccess || !result.ReceiverConfirmedFailure || !result.OutcomeKnown {
		t.Fatalf("exact receiver failure was not preserved: %+v err=%v", result, err)
	}
}

func TestProfitSharingUnfreezeReconciliationUsesOriginalUnfreezeOrderNumber(t *testing.T) {
	payload := effectport.Hash("profit-sharing-unfreeze-query-payload")
	when := time.Date(2026, 9, 14, 6, 0, 0, 0, time.UTC)
	material := ProfitSharingMaterial{PayloadDigest: payload, AppID: "wx-app", TransactionReference: "4200000002", ProviderOrderNo: "v3psu_ABCDEFGHIJKLMNOPQRST", Reason: "release unused reserve"}
	sdk := &profitSharingSDKStub{query: ProfitSharingQuery{State: "FINISHED", OccurredAt: when}}
	provider := &WeChatPay{config: Config{Enabled: true}, loader: profitSharingLoaderStub{material: material, referenceKind: effectport.KindWeChatPayProfitUnfreeze}, profitSharing: sdk}
	result, err := provider.QueryProfitSharingUnfreeze(context.Background(), "psunfreeze_7")
	if err != nil || !result.OutcomeKnown || result.ReceiverConfirmedSuccess || result.State != "FINISHED" || sdk.queryMaterial.ProviderOrderNo != material.ProviderOrderNo || !strings.HasPrefix(sdk.queryMaterial.ProviderOrderNo, "v3psu_") {
		t.Fatalf("unfreeze result=%+v queried=%+v err=%v", result, sdk.queryMaterial, err)
	}
}
func (stub *profitSharingSDKStub) UnfreezeOrder(context.Context, ProfitSharingMaterial) error {
	stub.unfreeze++
	return nil
}

func testEnvelope(kind effectport.Kind, payload effectport.Digest) effectport.Envelope {
	return effectport.Envelope{Owner: effectport.OwnerPayment, Kind: kind, SourceRefDigest: effectport.Hash("source"), TargetRefDigest: effectport.Hash("target"), PayloadDigest: payload, PolicyVersionHash: effectport.Hash("policy")}
}

func TestDisabledProviderMakesZeroCalls(t *testing.T) {
	calls := 0
	provider, err := NewWeChatPay(Config{}, nil, doerFunc(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not call")
	}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayPrepay, effectport.Hash("payload")), effectport.Attempt{Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || calls != 0 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
}

func TestProfitSharingEffectsUseInjectedSDKAndFailClosedWithoutIt(t *testing.T) {
	payload := effectport.Hash("profit-sharing-payload")
	envelope := testEnvelope(effectport.KindWeChatPayProfitSharing, payload)
	material := ProfitSharingMaterial{PayloadDigest: payload, AppID: "wx-app", ReceiverAccount: "open-id", TransactionReference: "4200000001", ProviderOrderNo: "v3ps_ABCDEFGHIJKLMNOPQRST", AmountMinor: 12}
	provider := &WeChatPay{config: Config{Enabled: true}, loader: profitSharingLoaderStub{material: material}}
	result, err := provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted {
		t.Fatalf("missing SDK result=%+v err=%v", result, err)
	}
	sdk := &profitSharingSDKStub{}
	if err := provider.SetProfitSharingSDK(sdk); err != nil {
		t.Fatal(err)
	}
	result, err = provider.Execute(context.Background(), envelope, effectport.Attempt{Number: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || sdk.create != 1 || sdk.add != 0 || sdk.unfreeze != 0 {
		t.Fatalf("split result=%+v calls=%+v err=%v", result, sdk, err)
	}

	for _, test := range []struct {
		kind effectport.Kind
		want func() int
	}{
		{effectport.KindWeChatPayReceiverAdd, func() int { return sdk.add }},
		{effectport.KindWeChatPayProfitUnfreeze, func() int { return sdk.unfreeze }},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			copy := material
			if test.kind == effectport.KindWeChatPayReceiverAdd {
				copy.ProviderOrderNo, copy.TransactionReference, copy.AmountMinor = "", "", 0
			}
			if test.kind == effectport.KindWeChatPayProfitUnfreeze {
				copy.ProviderOrderNo, copy.Reason = "v3psu_ABCDEFGHIJKLMNOPQRST", "release unused reserve"
			}
			provider.loader = profitSharingLoaderStub{material: copy}
			before := test.want()
			result, callErr := provider.Execute(context.Background(), testEnvelope(test.kind, payload), effectport.Attempt{Number: 1})
			if callErr != nil || result.Completion != effectport.StateExecuted || test.want() != before+1 {
				t.Fatalf("result=%+v before=%d after=%d err=%v", result, before, test.want(), callErr)
			}
		})
	}
}

func TestSignedPrepayAndAmbiguousTransport(t *testing.T) {
	merchant, _ := rsa.GenerateKey(rand.Reader, 2048)
	platform, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC)
	payloadDigest := effectport.Hash("payload")
	material := Material{PayerOpenID: "openid", Intent: paymentport.ProviderIntent{Kind: effectport.KindWeChatPayPrepay, MerchantOrderNo: "order-1", AmountMinor: 99, TotalMinor: 99, Currency: "CNY", PayloadDigest: payloadDigest}}
	calls := 0
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		calls++
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/v3/pay/transactions/jsapi" || !strings.HasPrefix(request.Header.Get("Authorization"), "WECHATPAY2-SHA256-RSA2048 ") || !strings.Contains(string(body), `"openid":"openid"`) {
			t.Fatalf("request=%s body=%s", request.URL, body)
		}
		responseBody := []byte(`{"prepay_id":"prepay-1"}`)
		timestamp, nonce := fmt.Sprint(now.Unix()), "response-nonce"
		signature := signTest(t, platform, timestamp+"\n"+nonce+"\n"+string(responseBody)+"\n")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(responseBody))), Header: http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {"platform"}, "Wechatpay-Signature": {signature}}}, nil
	})
	provider, err := NewWeChatPay(Config{Enabled: true, AppID: "app", AppScope: "wechat-app:app", APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: "https://crm.example/pay", RefundNotifyURL: "https://crm.example/refund", Credential: Credential{MerchantID: "mch", Serial: "merchant", Signer: merchant, PlatformKeys: map[string]*rsa.PublicKey{"platform": &platform.PublicKey}}}, loaderStub{material}, doer)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	provider.nonce = func() (string, error) { return "nonce", nil }
	result, err := provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayPrepay, payloadDigest), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted || !result.Artifact.Valid() || calls != 1 {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls, err)
	}
	provider.client = doerFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("timeout") })
	result, err = provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayPrepay, payloadDigest), effectport.Attempt{Number: 2, Generation: 1, Fence: 2})
	if err == nil || result.Completion != effectport.StateUnknown || !result.CallAttempted {
		t.Fatalf("ambiguous=%+v err=%v", result, err)
	}
}

func TestSignedRefundAcceptsExactProviderResponseFields(t *testing.T) {
	merchant, _ := rsa.GenerateKey(rand.Reader, 2048)
	platform, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Date(2026, 9, 3, 3, 30, 0, 0, time.UTC)
	payloadDigest := effectport.Hash("refund-payload")
	material := Material{Intent: paymentport.ProviderIntent{Kind: effectport.KindWeChatPayRefund, MerchantOrderNo: "order-1", RefundNo: "refund-1", RefundReason: "customer request", AmountMinor: 20, TotalMinor: 99, Currency: "CNY", PayloadDigest: payloadDigest}}
	doer := doerFunc(func(request *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(request.Body)
		if request.URL.Path != "/v3/refund/domestic/refunds" || !strings.Contains(string(body), `"out_refund_no":"refund-1"`) {
			t.Fatalf("request=%s body=%s", request.URL, body)
		}
		responseBody := []byte(`{"refund_id":"provider-refund-1","out_refund_no":"refund-1","status":"PROCESSING"}`)
		timestamp, nonce := fmt.Sprint(now.Unix()), "response-nonce"
		signature := signTest(t, platform, timestamp+"\n"+nonce+"\n"+string(responseBody)+"\n")
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(responseBody))), Header: http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {"platform"}, "Wechatpay-Signature": {signature}}}, nil
	})
	provider, err := NewWeChatPay(Config{Enabled: true, AppID: "app", AppScope: "wechat-app:app", APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: "https://crm.example/pay", RefundNotifyURL: "https://crm.example/refund", Credential: Credential{MerchantID: "mch", Serial: "merchant", Signer: merchant, PlatformKeys: map[string]*rsa.PublicKey{"platform": &platform.PublicKey}}}, loaderStub{material}, doer)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	provider.nonce = func() (string, error) { return "nonce", nil }
	result, err := provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayRefund, payloadDigest), effectport.Attempt{Number: 1, Generation: 1, Fence: 1})
	if err != nil || result.Completion != effectport.StateExecuted || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCallbackVerifiesSignatureAndDecrypts(t *testing.T) {
	platform, _ := rsa.GenerateKey(rand.Reader, 2048)
	apiKey := []byte("0123456789abcdef0123456789abcdef")
	now := time.Date(2026, 9, 3, 3, 0, 0, 0, time.UTC)
	// A production transaction notification can report payer_total separately
	// when a WeChat coupon is used.  Settlement must continue to compare the
	// merchant's original amount.total while accepting the additive fields.
	plain := []byte(`{"appid":"app","mchid":"mch","out_trade_no":"order-1","transaction_id":"tx-1","trade_state":"SUCCESS","success_time":"2026-09-03T02:59:00Z","trade_type":"JSAPI","payer":{"openid":"opaque"},"amount":{"total":99,"payer_total":1,"currency":"CNY","payer_currency":"CNY"}}`)
	block, _ := aes.NewCipher(apiKey)
	gcm, _ := cipher.NewGCM(block)
	nonce, associated := "123456789012", "transaction"
	ciphertext := base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), plain, []byte(associated)))
	body, _ := json.Marshal(map[string]any{"id": "event-1", "event_type": "TRANSACTION.SUCCESS", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ciphertext, "nonce": nonce, "associated_data": associated}})
	timestamp := fmt.Sprint(now.Unix())
	headers := http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {"cb-nonce"}, "Wechatpay-Serial": {"platform"}, "Wechatpay-Signature": {signTest(t, platform, timestamp+"\ncb-nonce\n"+string(body)+"\n")}}
	verifier, err := NewCallbackVerifier(map[string]*rsa.PublicKey{"platform": &platform.PublicKey}, apiKey, "app", "mch")
	if err != nil {
		t.Fatal(err)
	}
	verifier.now = func() time.Time { return now }
	result, err := verifier.Verify(context.Background(), body, headers)
	if err != nil || result.Kind != "payment" || result.MerchantOrderNo != "order-1" || result.AmountMinor != 99 || result.ProviderTransactionDigest == "" || result.ProviderTransactionReference != "tx-1" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	headers.Set("Wechatpay-Signature", "bad")
	if _, err = verifier.Verify(context.Background(), body, headers); !errors.Is(err, ErrInvalidCallback) {
		t.Fatalf("signature err=%v", err)
	}
	withoutSuccessTime := []byte(`{"appid":"app","mchid":"mch","out_trade_no":"order-1","transaction_id":"tx-1","trade_state":"SUCCESS","amount":{"total":99,"currency":"CNY"}}`)
	ciphertext = base64.StdEncoding.EncodeToString(gcm.Seal(nil, []byte(nonce), withoutSuccessTime, []byte(associated)))
	body, _ = json.Marshal(map[string]any{"id": "event-without-success-time", "event_type": "TRANSACTION.SUCCESS", "resource": map[string]string{"algorithm": "AEAD_AES_256_GCM", "ciphertext": ciphertext, "nonce": nonce, "associated_data": associated}})
	headers.Set("Wechatpay-Signature", signTest(t, platform, timestamp+"\ncb-nonce\n"+string(body)+"\n"))
	if _, err = verifier.Verify(context.Background(), body, headers); !errors.Is(err, ErrInvalidCallback) || CallbackFailureStage(err) != "occurred_at" {
		t.Fatalf("missing success_time err=%v stage=%q", err, CallbackFailureStage(err))
	}
}

func TestSignedExactPaymentAndRefundReconciliationQueries(t *testing.T) {
	merchant, _ := rsa.GenerateKey(rand.Reader, 2048)
	platform, _ := rsa.GenerateKey(rand.Reader, 2048)
	now := time.Date(2026, 9, 3, 5, 0, 0, 0, time.UTC)
	responses := []string{
		`{"appid":"app","mchid":"mch","payer":{"openid":"synthetic-openid"},"out_trade_no":"order-1","transaction_id":"tx-1","trade_state":"SUCCESS","success_time":"2026-09-03T04:59:00Z","amount":{"total":99,"currency":"CNY"}}`,
		`{"refund_id":"provider-refund-1","out_refund_no":"refund-1","status":"SUCCESS","success_time":"2026-09-03T04:59:30Z","amount":{"refund":20,"total":99,"currency":"CNY"}}`,
		`{"appid":"app","mchid":"other-merchant","out_trade_no":"order-1","transaction_id":"tx-1","trade_state":"SUCCESS","success_time":"2026-09-03T04:59:00Z","amount":{"total":99,"currency":"CNY"}}`,
	}
	calls := 0
	client := doerFunc(func(request *http.Request) (*http.Response, error) {
		if request.Method != http.MethodGet || !strings.HasPrefix(request.Header.Get("Authorization"), "WECHATPAY2-SHA256-RSA2048 ") {
			t.Fatalf("request=%s method=%s", request.URL, request.Method)
		}
		body := []byte(responses[calls])
		calls++
		timestamp, nonce := fmt.Sprint(now.Unix()), fmt.Sprintf("nonce-%d", calls)
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body))), Header: http.Header{"Wechatpay-Timestamp": {timestamp}, "Wechatpay-Nonce": {nonce}, "Wechatpay-Serial": {"platform"}, "Wechatpay-Signature": {signTest(t, platform, timestamp+"\n"+nonce+"\n"+string(body)+"\n")}}}, nil
	})
	provider, err := NewWeChatPay(Config{Enabled: true, AppID: "app", AppScope: "wechat-app:app", APIBaseURL: "https://api.mch.weixin.qq.com", PaymentNotifyURL: "https://crm.example/pay", RefundNotifyURL: "https://crm.example/refund", Credential: Credential{MerchantID: "mch", Serial: "merchant", Signer: merchant, PlatformKeys: map[string]*rsa.PublicKey{"platform": &platform.PublicKey}}}, loaderStub{}, client)
	if err != nil {
		t.Fatal(err)
	}
	provider.now = func() time.Time { return now }
	provider.nonce = func() (string, error) { return "request-nonce", nil }
	payment, err := provider.QueryPayment(context.Background(), "order-1")
	if err != nil || payment.Status != "SUCCESS" || payment.AmountMinor != 99 || !effectport.ValidDigest(payment.TransactionDigest) || payment.TransactionReference != "tx-1" || payment.AppID != "app" || payment.PayerOpenID != "synthetic-openid" {
		t.Fatalf("payment=%+v err=%v", payment, err)
	}
	refund, err := provider.QueryRefund(context.Background(), "refund-1")
	if err != nil || refund.Status != "SUCCESS" || refund.AmountMinor != 20 || refund.TotalMinor != 99 || !effectport.ValidDigest(refund.RefundDigest) || calls != 2 {
		t.Fatalf("refund=%+v calls=%d err=%v", refund, calls, err)
	}
	if _, err = provider.QueryPayment(context.Background(), "order-1"); !errors.Is(err, ErrInvalidResponse) || calls != 3 {
		t.Fatalf("different merchant response err=%v calls=%d", err, calls)
	}
}

func signTest(t *testing.T, key *rsa.PrivateKey, message string) string {
	t.Helper()
	digest := sha256.Sum256([]byte(message))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return base64.StdEncoding.EncodeToString(signature)
}

func TestInvalidMerchantNumberNeverDispatches(t *testing.T) {
	for _, number := range []string{"v3pay_" + strings.Repeat("a", 32), "short", "order/unsafe", "订单123456"} {
		digest := effectport.Hash("payload")
		p := &WeChatPay{config: Config{Enabled: true}, loader: loaderStub{Material{PayerOpenID: "openid", Intent: paymentport.ProviderIntent{MerchantOrderNo: number, PayloadDigest: digest}}}, client: doerFunc(func(*http.Request) (*http.Response, error) { t.Fatal("invalid order dispatched"); return nil, nil })}
		result, err := p.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayPrepay, digest), effectport.Attempt{Number: 1})
		if err != nil || result.Completion != effectport.StateFinalFailed || result.CallAttempted || result.RealExternalCallExecuted {
			t.Fatalf("invalid result: %+v %v", result, err)
		}
	}
}
