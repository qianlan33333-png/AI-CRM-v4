package provider

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	identitydomain "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/domain"
	identityport "github.com/qianlan33333-png/AI-CRM-v3/internal/identity/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	platformport "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/port"
)

var ErrInvalidConfig = errors.New("invalid payment provider configuration")
var ErrInvalidMaterial = errors.New("invalid payment provider material")
var ErrInvalidResponse = errors.New("invalid payment provider response")

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type Credential struct {
	MerchantID, Serial string
	Signer             crypto.Signer
	PlatformKeys       map[string]*rsa.PublicKey
}

func (Credential) String() string   { return "[REDACTED]" }
func (Credential) GoString() string { return "[REDACTED]" }

func (c Credential) valid() bool {
	key, ok := c.Signer.Public().(*rsa.PublicKey)
	return strings.TrimSpace(c.MerchantID) == c.MerchantID && c.MerchantID != "" &&
		strings.TrimSpace(c.Serial) == c.Serial && c.Serial != "" && ok && key.Size() >= 256 && len(c.PlatformKeys) > 0
}

func ParseMerchantPrivateKey(raw []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, ErrInvalidConfig
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, ErrInvalidConfig
}

func ParsePlatformCertificate(raw []byte) (string, *rsa.PublicKey, error) {
	block, _ := pem.Decode(raw)
	if block == nil {
		return "", nil, ErrInvalidConfig
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", nil, ErrInvalidConfig
	}
	key, ok := certificate.PublicKey.(*rsa.PublicKey)
	if !ok || key.Size() < 256 {
		return "", nil, ErrInvalidConfig
	}
	return strings.ToUpper(certificate.SerialNumber.Text(16)), key, nil
}

type Config struct {
	Enabled                           bool
	AppID, AppScope                   string
	H5AppID, H5AppScope               string
	APIBaseURL                        string
	PaymentNotifyURL, RefundNotifyURL string
	Credential                        Credential
}

func (Config) String() string   { return "WeChatPayConfig{credentials:[REDACTED]}" }
func (Config) GoString() string { return "WeChatPayConfig{credentials:[REDACTED]}" }

type Material struct {
	Intent        paymentport.ProviderIntent
	PayerOpenID   string
	AlipaySubject string
}

type MaterialLoader interface {
	Load(context.Context, effectport.Kind, effectport.Digest) (Material, error)
}

type DBMaterialLoader struct {
	UOW           platformport.UnitOfWork
	Intents       paymentport.ProviderIntentReader
	Identities    identityport.PaymentIdentityReader
	Checkouts     orderport.CheckoutSnapshotReader
	AppScope      string
	H5AppScope    string
	ProfitSharing paymentport.ProfitSharingMaterialReader
}

func (loader DBMaterialLoader) LoadProfitSharing(ctx context.Context, kind effectport.Kind, source effectport.Digest) (ProfitSharingMaterial, error) {
	if loader.ProfitSharing == nil {
		return ProfitSharingMaterial{}, ErrInvalidMaterial
	}
	value, err := loader.ProfitSharing.LoadProfitSharingEffectMaterial(ctx, kind, source)
	return ProfitSharingMaterial(value), err
}

func (loader DBMaterialLoader) LoadProfitSharingReference(ctx context.Context, reference string) (effectport.Kind, ProfitSharingMaterial, error) {
	if loader.ProfitSharing == nil {
		return "", ProfitSharingMaterial{}, ErrInvalidMaterial
	}
	kind, value, err := loader.ProfitSharing.LoadProfitSharingReferenceMaterial(ctx, reference)
	return kind, ProfitSharingMaterial(value), err
}

func (loader DBMaterialLoader) Load(ctx context.Context, kind effectport.Kind, source effectport.Digest) (Material, error) {
	if loader.UOW == nil || loader.Intents == nil {
		return Material{}, ErrInvalidMaterial
	}
	if kind == effectport.KindWeChatPayPrepay && (strings.TrimSpace(loader.AppScope) != loader.AppScope || loader.AppScope == "") {
		return Material{}, ErrInvalidMaterial
	}
	var material Material
	err := loader.UOW.Within(ctx, func(tx context.Context) error {
		intent, err := loader.Intents.ProviderIntent(tx, kind, source)
		if err != nil {
			return err
		}
		material.Intent = intent
		if kind == effectport.KindAlipayWapPay || kind == effectport.KindAlipayPagePay {
			if loader.Checkouts == nil || intent.OrderID < 1 || intent.PaymentID < 1 || intent.RefundID != 0 || intent.Currency != "CNY" {
				return ErrInvalidMaterial
			}
			snapshot, readErr := loader.Checkouts.ReadCheckoutSnapshotWithin(tx, intent.OrderID)
			if readErr != nil || snapshot.OrderID != intent.OrderID || snapshot.PayableAmountMinor != intent.AmountMinor || snapshot.Currency != intent.Currency || strings.TrimSpace(snapshot.ProductName) == "" {
				return ErrInvalidMaterial
			}
			material.AlipaySubject = snapshot.ProductName
		}
		if kind == effectport.KindWeChatPayPrepay {
			if loader.Identities == nil {
				return ErrInvalidMaterial
			}
			identityKind, scope := identitydomain.KindMPOpenID, loader.AppScope
			if intent.Channel == paymentdomain.ChannelH5Official {
				identityKind, scope = identitydomain.KindOAOpenID, loader.H5AppScope
			}
			if scope == "" {
				return ErrInvalidMaterial
			}
			identity, ok, err := loader.Identities.VerifiedPaymentIdentity(tx, intent.PayerIdentityID, identityKind, scope)
			if err != nil || !ok || identity.IdentityID != intent.PayerIdentityID || identity.Scope != scope {
				return ErrInvalidMaterial
			}
			material.PayerOpenID = identity.Value
		}
		return nil
	})
	return material, err
}

type WeChatPay struct {
	config Config
	base   *url.URL
	loader MaterialLoader
	client HTTPDoer
	// profitSharing is deliberately nil until Composition has supplied the
	// official SDK and a Payment-only material loader. Nil is fail-closed.
	profitSharing ProfitSharingSDK
	now           func() time.Time
	nonce         func() (string, error)
}

func (provider *WeChatPay) SetProfitSharingSDK(sdk ProfitSharingSDK) error {
	if provider == nil || sdk == nil || provider.profitSharing != nil {
		return ErrInvalidConfig
	}
	provider.profitSharing = sdk
	return nil
}

func NewWeChatPay(config Config, loader MaterialLoader, client HTTPDoer) (*WeChatPay, error) {
	if !config.Enabled {
		return &WeChatPay{config: config}, nil
	}
	base, err := url.Parse(config.APIBaseURL)
	if err != nil || base.Scheme != "https" || base.Host == "" || base.Path != "" || loader == nil || client == nil || !config.Credential.valid() || !validHTTPS(config.PaymentNotifyURL) || !validHTTPS(config.RefundNotifyURL) || config.AppID == "" || config.AppScope == "" || (config.H5AppID == "") != (config.H5AppScope == "") {
		return nil, ErrInvalidConfig
	}
	return &WeChatPay{config: config, base: base, loader: loader, client: client, now: time.Now, nonce: randomNonce}, nil
}

func (provider *WeChatPay) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if provider == nil || envelope.Owner != effectport.OwnerPayment || (envelope.Kind != effectport.KindWeChatPayPrepay && envelope.Kind != effectport.KindWeChatPayRefund && envelope.Kind != effectport.KindWeChatPayReceiverAdd && envelope.Kind != effectport.KindWeChatPayProfitSharing && envelope.Kind != effectport.KindWeChatPayProfitUnfreeze) {
		return final("wechatpay.unsupported", envelope, attempt), nil
	}
	if !provider.config.Enabled {
		return final("wechatpay.disabled", envelope, attempt), nil
	}
	if envelope.Kind == effectport.KindWeChatPayReceiverAdd || envelope.Kind == effectport.KindWeChatPayProfitSharing || envelope.Kind == effectport.KindWeChatPayProfitUnfreeze {
		return provider.executeProfitSharing(ctx, envelope, attempt)
	}
	material, err := provider.loader.Load(ctx, envelope.Kind, envelope.SourceRefDigest)
	if err != nil || material.Intent.PayloadDigest != envelope.PayloadDigest {
		return final("wechatpay.material", envelope, attempt), nil
	}
	if !validMerchantOrderNo(material.Intent.MerchantOrderNo) {
		return final("wechatpay.order-number.invalid", envelope, attempt), nil
	}
	var path string
	var payload any
	if envelope.Kind == effectport.KindWeChatPayPrepay {
		if material.PayerOpenID == "" || material.Intent.AmountMinor < 1 || material.Intent.Currency != "CNY" {
			return final("wechatpay.prepay.invalid", envelope, attempt), nil
		}
		appID := provider.config.AppID
		if material.Intent.Channel == paymentdomain.ChannelH5Official {
			appID = provider.config.H5AppID
		}
		if appID == "" {
			return final("wechatpay.prepay.channel", envelope, attempt), nil
		}
		path = "/v3/pay/transactions/jsapi"
		payload = map[string]any{
			"appid": appID, "mchid": provider.config.Credential.MerchantID,
			"description":  "CRM order " + material.Intent.MerchantOrderNo,
			"out_trade_no": material.Intent.MerchantOrderNo, "notify_url": provider.config.PaymentNotifyURL,
			"amount": map[string]any{"total": material.Intent.AmountMinor, "currency": material.Intent.Currency},
			"payer":  map[string]any{"openid": material.PayerOpenID},
		}
		if material.Intent.ProfitSharingMarked {
			payload.(map[string]any)["settle_info"] = map[string]any{"profit_sharing": true}
		}
	} else {
		if material.Intent.RefundNo == "" || material.Intent.AmountMinor < 1 || material.Intent.AmountMinor > material.Intent.TotalMinor || material.Intent.Currency != "CNY" {
			return final("wechatpay.refund.invalid", envelope, attempt), nil
		}
		path = "/v3/refund/domestic/refunds"
		payload = map[string]any{
			"out_trade_no": material.Intent.MerchantOrderNo, "out_refund_no": material.Intent.RefundNo,
			"reason": material.Intent.RefundReason, "notify_url": provider.config.RefundNotifyURL,
			"amount": map[string]any{"refund": material.Intent.AmountMinor, "total": material.Intent.TotalMinor, "currency": material.Intent.Currency},
		}
	}
	body, _ := json.Marshal(payload)
	response, dispatched, err := provider.signedJSON(ctx, http.MethodPost, path, body)
	if err != nil {
		result := effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: receipt("wechatpay.pre-dispatch", envelope, attempt), CallAttempted: false}
		if dispatched {
			result.Completion, result.CallAttempted, result.RealExternalCallExecuted = effectport.StateUnknown, true, true
			result.ReceiptDigest = receipt("wechatpay.outcome-unknown", envelope, attempt)
		}
		return result, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: receipt("wechatpay.provider-rejected", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	if envelope.Kind == effectport.KindWeChatPayPrepay {
		var decoded struct {
			PrepayID string `json:"prepay_id"`
		}
		if json.Unmarshal(response.Body, &decoded) != nil || decoded.PrepayID == "" {
			return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("wechatpay.invalid-response", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
		}
		appID := provider.config.AppID
		if material.Intent.Channel == paymentdomain.ChannelH5Official {
			appID = provider.config.H5AppID
		}
		artifactPayload, err := provider.handoff(decoded.PrepayID, appID)
		if err != nil {
			return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("wechatpay.invalid-handoff", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
		}
		artifact := effectport.ResultArtifact{Kind: "wechat_pay_jsapi_handoff_v1", Payload: artifactPayload}
		artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
		return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("wechatpay.executed", string(envelope.Fingerprint()), hashBytes(response.Body)), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
	}
	var decoded struct {
		RefundID    string `json:"refund_id"`
		OutRefundNo string `json:"out_refund_no"`
		Status      string `json:"status"`
	}
	if json.Unmarshal(response.Body, &decoded) != nil || decoded.RefundID == "" || decoded.OutRefundNo != material.Intent.RefundNo || decoded.Status == "" {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("wechatpay.invalid-refund-response", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: effectport.Hash("wechatpay.refund.executed", string(envelope.Fingerprint()), hashBytes(response.Body)), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (provider *WeChatPay) QueryPayment(ctx context.Context, merchantOrderNo string) (paymentport.WeChatPayPaymentQuery, error) {
	if provider == nil || !provider.config.Enabled || merchantOrderNo == "" || len(merchantOrderNo) > 200 || strings.TrimSpace(merchantOrderNo) != merchantOrderNo {
		return paymentport.WeChatPayPaymentQuery{}, ErrInvalidMaterial
	}
	path := "/v3/pay/transactions/out-trade-no/" + url.PathEscape(merchantOrderNo) + "?mchid=" + url.QueryEscape(provider.config.Credential.MerchantID)
	response, _, err := provider.signedJSON(ctx, http.MethodGet, path, nil)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		if err != nil {
			return paymentport.WeChatPayPaymentQuery{}, err
		}
		return paymentport.WeChatPayPaymentQuery{}, ErrInvalidResponse
	}
	var decoded struct {
		AppID      string `json:"appid"`
		MerchantID string `json:"mchid"`
		Payer      struct {
			OpenID string `json:"openid"`
		} `json:"payer"`
		MerchantOrderNo string `json:"out_trade_no"`
		TransactionID   string `json:"transaction_id"`
		TradeState      string `json:"trade_state"`
		SuccessTime     string `json:"success_time"`
		Amount          struct {
			Total    int64  `json:"total"`
			Currency string `json:"currency"`
		} `json:"amount"`
	}
	if json.Unmarshal(response.Body, &decoded) != nil || decoded.MerchantID != provider.config.Credential.MerchantID || decoded.MerchantOrderNo != merchantOrderNo || decoded.Amount.Total < 1 || decoded.Amount.Currency != "CNY" || !validPayQueryStatus(decoded.TradeState) {
		return paymentport.WeChatPayPaymentQuery{}, ErrInvalidResponse
	}
	occurred := provider.now().UTC()
	transactionDigest := effectport.Digest("")
	if decoded.TradeState == "SUCCESS" {
		var parseErr error
		occurred, parseErr = time.Parse(time.RFC3339, decoded.SuccessTime)
		if parseErr != nil || decoded.TransactionID == "" {
			return paymentport.WeChatPayPaymentQuery{}, ErrInvalidResponse
		}
		transactionDigest = effectport.Hash("wechatpay.transaction", decoded.TransactionID)
	}
	return paymentport.WeChatPayPaymentQuery{AppID: decoded.AppID, PayerOpenID: decoded.Payer.OpenID, MerchantOrderNo: merchantOrderNo, Currency: decoded.Amount.Currency, Status: decoded.TradeState, TransactionReference: decoded.TransactionID, AmountMinor: decoded.Amount.Total, OccurredAt: occurred.UTC(), EvidenceDigest: effectport.Hash("wechatpay.payment.query", merchantOrderNo, hashBytes(response.Body)), TransactionDigest: transactionDigest}, nil
}

func (provider *WeChatPay) QueryRefund(ctx context.Context, refundNo string) (paymentport.WeChatPayRefundQuery, error) {
	if provider == nil || !provider.config.Enabled || refundNo == "" || len(refundNo) > 200 || strings.TrimSpace(refundNo) != refundNo {
		return paymentport.WeChatPayRefundQuery{}, ErrInvalidMaterial
	}
	path := "/v3/refund/domestic/refunds/" + url.PathEscape(refundNo)
	response, _, err := provider.signedJSON(ctx, http.MethodGet, path, nil)
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		if err != nil {
			return paymentport.WeChatPayRefundQuery{}, err
		}
		return paymentport.WeChatPayRefundQuery{}, ErrInvalidResponse
	}
	var decoded struct {
		RefundID    string `json:"refund_id"`
		RefundNo    string `json:"out_refund_no"`
		Status      string `json:"status"`
		SuccessTime string `json:"success_time"`
		Amount      struct {
			Refund   int64  `json:"refund"`
			Total    int64  `json:"total"`
			Currency string `json:"currency"`
		} `json:"amount"`
	}
	if json.Unmarshal(response.Body, &decoded) != nil || decoded.RefundNo != refundNo || decoded.Amount.Refund < 1 || decoded.Amount.Total < decoded.Amount.Refund || decoded.Amount.Currency != "CNY" || !validRefundQueryStatus(decoded.Status) {
		return paymentport.WeChatPayRefundQuery{}, ErrInvalidResponse
	}
	occurred := provider.now().UTC()
	refundDigest := effectport.Digest("")
	if decoded.Status == "SUCCESS" {
		var parseErr error
		occurred, parseErr = time.Parse(time.RFC3339, decoded.SuccessTime)
		if parseErr != nil || decoded.RefundID == "" {
			return paymentport.WeChatPayRefundQuery{}, ErrInvalidResponse
		}
		refundDigest = effectport.Hash("wechatpay.refund", decoded.RefundID)
	}
	return paymentport.WeChatPayRefundQuery{RefundNo: refundNo, Currency: decoded.Amount.Currency, Status: decoded.Status, AmountMinor: decoded.Amount.Refund, TotalMinor: decoded.Amount.Total, OccurredAt: occurred.UTC(), EvidenceDigest: effectport.Hash("wechatpay.refund.query", refundNo, hashBytes(response.Body)), RefundDigest: refundDigest}, nil
}

func validPayQueryStatus(value string) bool {
	switch value {
	case "SUCCESS", "REFUND", "NOTPAY", "CLOSED", "REVOKED", "USERPAYING", "PAYERROR":
		return true
	default:
		return false
	}
}

func validRefundQueryStatus(value string) bool {
	return value == "SUCCESS" || value == "CLOSED" || value == "PROCESSING" || value == "ABNORMAL"
}

type signedResponse struct {
	StatusCode int
	Body       []byte
}

func (provider *WeChatPay) signedJSON(ctx context.Context, method, path string, body []byte) (signedResponse, bool, error) {
	nonce, err := provider.nonce()
	if err != nil {
		return signedResponse{}, false, err
	}
	timestamp := strconv.FormatInt(provider.now().UTC().Unix(), 10)
	message := method + "\n" + path + "\n" + timestamp + "\n" + nonce + "\n" + string(body) + "\n"
	signature, err := sign(provider.config.Credential.Signer, message)
	if err != nil {
		return signedResponse{}, false, err
	}
	request, err := http.NewRequestWithContext(ctx, method, provider.base.String()+path, strings.NewReader(string(body)))
	if err != nil {
		return signedResponse{}, false, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", fmt.Sprintf(`WECHATPAY2-SHA256-RSA2048 mchid="%s",nonce_str="%s",timestamp="%s",serial_no="%s",signature="%s"`, provider.config.Credential.MerchantID, nonce, timestamp, provider.config.Credential.Serial, signature))
	response, err := provider.client.Do(request)
	if err != nil {
		return signedResponse{}, true, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return signedResponse{}, true, err
	}
	if err = verifyResponse(provider.config.Credential.PlatformKeys, provider.now().UTC(), response.Header, responseBody); err != nil {
		return signedResponse{}, true, err
	}
	return signedResponse{StatusCode: response.StatusCode, Body: responseBody}, true, nil
}

func (provider *WeChatPay) handoff(prepayID, appID string) ([]byte, error) {
	nonce, err := provider.nonce()
	if err != nil {
		return nil, err
	}
	timestamp := strconv.FormatInt(provider.now().UTC().Unix(), 10)
	pkg := "prepay_id=" + prepayID
	signature, err := sign(provider.config.Credential.Signer, appID+"\n"+timestamp+"\n"+nonce+"\n"+pkg+"\n")
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]string{"appId": appID, "timeStamp": timestamp, "nonceStr": nonce, "package": pkg, "signType": "RSA", "paySign": signature, "expiresAt": provider.now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano)})
}

func sign(signer crypto.Signer, message string) (string, error) {
	digest := sha256.Sum256([]byte(message))
	signature, err := signer.Sign(rand.Reader, digest[:], crypto.SHA256)
	return base64.StdEncoding.EncodeToString(signature), err
}

func verifyResponse(keys map[string]*rsa.PublicKey, now time.Time, headers http.Header, body []byte) error {
	timestamp, nonce, serial, encoded := headers.Get("Wechatpay-Timestamp"), headers.Get("Wechatpay-Nonce"), headers.Get("Wechatpay-Serial"), headers.Get("Wechatpay-Signature")
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || now.Sub(time.Unix(seconds, 0)).Abs() > 5*time.Minute || nonce == "" || serial == "" || encoded == "" {
		return ErrInvalidResponse
	}
	key := keys[serial]
	signature, err := base64.StdEncoding.Strict().DecodeString(encoded)
	digest := sha256.Sum256([]byte(timestamp + "\n" + nonce + "\n" + string(body) + "\n"))
	if err != nil || key == nil || rsa.VerifyPKCS1v15(key, crypto.SHA256, digest[:], signature) != nil {
		return ErrInvalidResponse
	}
	return nil
}

func randomNonce() (string, error) {
	raw := make([]byte, 18)
	_, err := rand.Read(raw)
	return base64.RawURLEncoding.EncodeToString(raw), err
}

func validHTTPS(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme == "https" && parsed.Host != ""
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return fmt.Sprintf("%x", sum[:]) }
func receipt(stage string, envelope effectport.Envelope, attempt effectport.Attempt) effectport.Digest {
	return effectport.Hash(stage, string(envelope.Fingerprint()), strconv.Itoa(int(attempt.Number)), strconv.FormatInt(attempt.Generation, 10), strconv.FormatInt(attempt.Fence, 10))
}
func final(stage string, envelope effectport.Envelope, attempt effectport.Attempt) effectport.AdapterResult {
	return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: receipt(stage, envelope, attempt)}
}

var _ effectport.ProviderAdapter = (*WeChatPay)(nil)
var _ paymentport.WeChatPayReconciler = (*WeChatPay)(nil)

// WeChat Pay accepts at most 32 ASCII letters, digits, underscores or hyphens.
func validMerchantOrderNo(value string) bool {
	if len(value) < 6 || len(value) > 32 {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '*' || c == '|') {
			return false
		}
	}
	return true
}
