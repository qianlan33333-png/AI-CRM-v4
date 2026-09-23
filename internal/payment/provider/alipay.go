package provider

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	alipay "github.com/smartwalle/alipay/v3"
)

// AlipayConfig contains only deployment-owned credentials and endpoints. The
// payment domain never receives this SDK client directly.
type AlipayConfig struct {
	Enabled              bool
	Production           bool
	AppID                string
	PrivateKey           string
	Gateway              string
	ContentEncryptionKey string
	AlipayPublicKey      string
	AppCertPath          string
	AlipayCertPath       string
	AlipayRootPath       string
	NotifyURL            string
	ReturnURL            string
}

func (c AlipayConfig) valid() bool {
	if !c.Enabled {
		return true
	}
	if strings.TrimSpace(c.AppID) != c.AppID || c.AppID == "" || strings.TrimSpace(c.PrivateKey) == "" {
		return false
	}
	if !validHTTPS(c.NotifyURL) || !validHTTPS(c.ReturnURL) {
		return false
	}
	// Use exactly one verification mode. Certificate mode is the production
	// default for fund-sensitive APIs such as refund; public-key mode remains
	// useful for sandbox and is explicitly supported.
	certMode := c.AppCertPath != "" || c.AlipayCertPath != "" || c.AlipayRootPath != ""
	if certMode {
		return c.AppCertPath != "" && c.AlipayCertPath != "" && c.AlipayRootPath != "" && strings.TrimSpace(c.AlipayPublicKey) == ""
	}
	return strings.TrimSpace(c.AlipayPublicKey) != ""
}

// Alipay is the SDK adapter boundary. Callers use the returned artifacts and
// normalized notification facts; they do not import the third-party SDK.
type Alipay struct {
	config AlipayConfig
	client *alipay.Client
	loader MaterialLoader
}

func NewAlipay(config AlipayConfig) (*Alipay, error) {
	if !config.valid() {
		return nil, ErrInvalidConfig
	}
	if !config.Enabled {
		return &Alipay{config: config}, nil
	}
	var opts []alipay.OptionFunc
	if config.Gateway != "" {
		if config.Production {
			opts = append(opts, alipay.WithProductionGateway(config.Gateway))
		} else {
			opts = append(opts, alipay.WithSandboxGateway(config.Gateway))
		}
	}
	client, err := alipay.New(config.AppID, config.PrivateKey, config.Production, opts...)
	if err != nil {
		return nil, ErrInvalidConfig
	}
	if config.AppCertPath != "" {
		if err = client.LoadAppCertPublicKeyFromFile(config.AppCertPath); err != nil {
			return nil, ErrInvalidConfig
		}
		if err = client.LoadAlipayCertPublicKeyFromFile(config.AlipayCertPath); err != nil {
			return nil, ErrInvalidConfig
		}
		if err = client.LoadAliPayRootCertFromFile(config.AlipayRootPath); err != nil {
			return nil, ErrInvalidConfig
		}
	} else if err = client.LoadAliPayPublicKey(config.AlipayPublicKey); err != nil {
		return nil, ErrInvalidConfig
	}
	if key := strings.TrimSpace(config.ContentEncryptionKey); key != "" {
		if err = client.SetEncryptKey(key); err != nil {
			return nil, ErrInvalidConfig
		}
	}
	return &Alipay{config: config, client: client}, nil
}

// ContentEncryptionConfigured exposes only a boolean operational metric.
func (a *Alipay) ContentEncryptionConfigured() bool {
	return a != nil && a.client != nil && strings.TrimSpace(a.config.ContentEncryptionKey) != ""
}

// LoadContentEncryptionKey reads a deployment-owned key file without exposing
// its contents to logs, descriptors, or API responses.
func LoadContentEncryptionKey(path string) (string, error) {
	if strings.TrimSpace(path) == "" {
		return "", nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", ErrInvalidConfig
	}
	return key, nil
}

func (a *Alipay) Enabled() bool { return a != nil && a.config.Enabled && a.client != nil }

func (a *Alipay) SetMaterialLoader(loader MaterialLoader) error {
	if a == nil || loader == nil || a.loader != nil {
		return ErrInvalidConfig
	}
	a.loader = loader
	return nil
}

func (a *Alipay) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if a == nil || envelope.Owner != effectport.OwnerPayment || (envelope.Kind != effectport.KindAlipayWapPay && envelope.Kind != effectport.KindAlipayPagePay && envelope.Kind != effectport.KindAlipayRefund) {
		return final("alipay.unsupported", envelope, attempt), nil
	}
	if !a.Enabled() {
		return final("alipay.disabled", envelope, attempt), nil
	}
	if a.loader == nil {
		return final("alipay.material-loader", envelope, attempt), nil
	}
	material, err := a.loader.Load(ctx, envelope.Kind, envelope.SourceRefDigest)
	if err != nil || material.Intent.PayloadDigest != envelope.PayloadDigest {
		return final("alipay.material", envelope, attempt), nil
	}
	if envelope.Kind == effectport.KindAlipayRefund {
		_, callErr := a.Refund(ctx, RefundRequest{MerchantOrderNo: material.Intent.MerchantOrderNo, RefundAmount: minorToAmount(material.Intent.AmountMinor), RefundReason: material.Intent.RefundReason, RefundRequestNo: material.Intent.RefundNo})
		if callErr != nil {
			return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("alipay.refund.unknown", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, callErr
		}
		return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: receipt("alipay.refund.executed", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
	}
	request := WebPayRequest{MerchantOrderNo: material.Intent.MerchantOrderNo, Subject: material.Intent.ProductID, TotalAmount: minorToAmount(material.Intent.AmountMinor)}
	var payURL string
	if envelope.Kind == effectport.KindAlipayWapPay {
		payURL, err = a.BuildWapPay(ctx, request)
	} else {
		payURL, err = a.BuildPagePay(ctx, request)
	}
	if err != nil {
		return effectport.AdapterResult{Completion: effectport.StateRetryable, ReceiptDigest: receipt("alipay.pay.retryable", envelope, attempt)}, err
	}
	artifactPayload, marshalErr := json.Marshal(map[string]string{"redirectUrl": payURL, "expiresAt": time.Now().UTC().Add(2 * time.Hour).Format(time.RFC3339Nano)})
	if marshalErr != nil {
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("alipay.pay.artifact", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, marshalErr
	}
	artifact := effectport.ResultArtifact{Kind: "alipay_web_pay_url_v1", Payload: artifactPayload}
	artifact.Digest = effectport.Hash("external-effect.artifact.v1", artifact.Kind, string(artifact.Payload))
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: receipt("alipay.pay.executed", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true, Artifact: artifact}, nil
}

type WebPayRequest struct {
	MerchantOrderNo string
	Subject         string
	Body            string
	TotalAmount     string
	TimeoutExpress  string
}

func (a *Alipay) BuildWapPay(_ context.Context, request WebPayRequest) (string, error) {
	if !a.Enabled() || !validWebPayRequest(request) {
		return "", ErrInvalidMaterial
	}
	value := alipay.TradeWapPay{Trade: alipay.Trade{
		NotifyURL: a.config.NotifyURL, ReturnURL: a.config.ReturnURL,
		OutTradeNo: request.MerchantOrderNo, Subject: request.Subject,
		Body: request.Body, TotalAmount: request.TotalAmount,
		ProductCode: "QUICK_WAP_WAY", TimeoutExpress: request.TimeoutExpress,
	}}
	result, err := a.client.TradeWapPay(value)
	if err != nil || result == nil || result.Scheme == "" || result.Host == "" {
		return "", ErrInvalidResponse
	}
	return result.String(), nil
}

func (a *Alipay) BuildPagePay(_ context.Context, request WebPayRequest) (string, error) {
	if !a.Enabled() || !validWebPayRequest(request) {
		return "", ErrInvalidMaterial
	}
	value := alipay.TradePagePay{Trade: alipay.Trade{
		NotifyURL: a.config.NotifyURL, ReturnURL: a.config.ReturnURL,
		OutTradeNo: request.MerchantOrderNo, Subject: request.Subject,
		Body: request.Body, TotalAmount: request.TotalAmount,
		ProductCode: "FAST_INSTANT_TRADE_PAY", TimeoutExpress: request.TimeoutExpress,
	}}
	result, err := a.client.TradePagePay(value)
	if err != nil || result == nil || result.Scheme == "" || result.Host == "" {
		return "", ErrInvalidResponse
	}
	return result.String(), nil
}

type PaymentQuery struct {
	MerchantOrderNo string
	TradeNo         string
}

type PaymentQueryResult struct {
	MerchantOrderNo string
	TradeNo         string
	TradeStatus     string
	TotalAmount     string
	BuyerID         string
}

func (a *Alipay) Query(ctx context.Context, request PaymentQuery) (PaymentQueryResult, error) {
	if !a.Enabled() || (strings.TrimSpace(request.MerchantOrderNo) == "" && strings.TrimSpace(request.TradeNo) == "") {
		return PaymentQueryResult{}, ErrInvalidMaterial
	}
	result, err := a.client.TradeQuery(ctx, alipay.TradeQuery{OutTradeNo: request.MerchantOrderNo, TradeNo: request.TradeNo})
	if err != nil || result == nil {
		return PaymentQueryResult{}, ErrInvalidResponse
	}
	return PaymentQueryResult{MerchantOrderNo: result.OutTradeNo, TradeNo: result.TradeNo, TradeStatus: string(result.TradeStatus), TotalAmount: result.TotalAmount, BuyerID: result.BuyerUserId}, nil
}

func (a *Alipay) QueryPayment(ctx context.Context, merchantOrderNo string) (paymentport.AlipayPaymentQuery, error) {
	result, err := a.Query(ctx, PaymentQuery{MerchantOrderNo: merchantOrderNo})
	if err != nil {
		return paymentport.AlipayPaymentQuery{}, err
	}
	minor, err := amountToMinor(result.TotalAmount)
	if err != nil {
		return paymentport.AlipayPaymentQuery{}, err
	}
	return paymentport.AlipayPaymentQuery{MerchantOrderNo: result.MerchantOrderNo, TradeNo: result.TradeNo, TradeStatus: result.TradeStatus, Currency: "CNY", AmountMinor: minor, OccurredAt: time.Now().UTC(), EvidenceDigest: effectport.Hash("alipay.query", result.MerchantOrderNo, result.TradeNo, result.TradeStatus, result.TotalAmount), TransactionDigest: effectport.Hash("alipay.transaction", result.TradeNo)}, nil
}

type RefundRequest struct {
	MerchantOrderNo string
	TradeNo         string
	RefundAmount    string
	RefundReason    string
	RefundRequestNo string
}

type RefundResult struct {
	MerchantOrderNo string
	TradeNo         string
	RefundAmount    string
	FundChanged     bool
}

func (a *Alipay) Refund(ctx context.Context, request RefundRequest) (RefundResult, error) {
	if !a.Enabled() || strings.TrimSpace(request.RefundAmount) == "" || strings.TrimSpace(request.RefundRequestNo) == "" || (strings.TrimSpace(request.MerchantOrderNo) == "" && strings.TrimSpace(request.TradeNo) == "") {
		return RefundResult{}, ErrInvalidMaterial
	}
	result, err := a.client.TradeRefund(ctx, alipay.TradeRefund{OutTradeNo: request.MerchantOrderNo, TradeNo: request.TradeNo, RefundAmount: request.RefundAmount, RefundReason: request.RefundReason, OutRequestNo: request.RefundRequestNo})
	if err != nil || result == nil {
		return RefundResult{}, ErrInvalidResponse
	}
	return RefundResult{MerchantOrderNo: result.OutTradeNo, TradeNo: result.TradeNo, RefundAmount: result.RefundFee, FundChanged: result.FundChange == "Y"}, nil
}

func (a *Alipay) QueryRefund(ctx context.Context, refundNo string) (paymentport.AlipayRefundQuery, error) {
	if !a.Enabled() || strings.TrimSpace(refundNo) == "" {
		return paymentport.AlipayRefundQuery{}, ErrInvalidMaterial
	}
	result, err := a.client.TradeFastPayRefundQuery(ctx, alipay.TradeFastPayRefundQuery{OutRequestNo: refundNo})
	if err != nil || result == nil {
		return paymentport.AlipayRefundQuery{}, ErrInvalidResponse
	}
	amount, err := amountToMinor(result.RefundAmount)
	if err != nil {
		return paymentport.AlipayRefundQuery{}, err
	}
	total, err := amountToMinor(result.TotalAmount)
	if err != nil {
		return paymentport.AlipayRefundQuery{}, err
	}
	return paymentport.AlipayRefundQuery{RefundNo: result.OutRequestNo, Currency: "CNY", Status: result.RefundStatus, AmountMinor: amount, TotalMinor: total, OccurredAt: time.Now().UTC(), EvidenceDigest: effectport.Hash("alipay.refund.query", result.OutRequestNo, result.RefundStatus, result.RefundAmount), RefundDigest: effectport.Hash("alipay.refund", result.OutRequestNo, result.TradeNo)}, nil
}

type Notification struct {
	NotifyID        string
	AppID           string
	MerchantOrderNo string
	TradeNo         string
	TradeStatus     string
	TotalAmount     string
	BuyerID         string
	RefundRequestNo string
	RefundAmount    string
}

func (a *Alipay) DecodeNotification(ctx context.Context, values url.Values) (Notification, error) {
	if !a.Enabled() || values == nil {
		return Notification{}, ErrInvalidMaterial
	}
	notification, err := a.client.DecodeNotification(ctx, values)
	if err != nil || notification == nil || notification.AppId != a.config.AppID || notification.OutTradeNo == "" {
		return Notification{}, ErrInvalidResponse
	}
	return Notification{NotifyID: notification.NotifyId, AppID: notification.AppId, MerchantOrderNo: notification.OutTradeNo, TradeNo: notification.TradeNo, TradeStatus: string(notification.TradeStatus), TotalAmount: notification.TotalAmount, BuyerID: notification.BuyerId, RefundRequestNo: notification.OutRequestNo, RefundAmount: notification.RefundAmount}, nil
}

func (a *Alipay) VerifyValues(ctx context.Context, values url.Values) (CallbackResult, error) {
	notification, err := a.DecodeNotification(ctx, values)
	if err != nil || notification.NotifyID == "" || notification.TradeNo == "" {
		return CallbackResult{}, ErrInvalidCallback
	}
	if notification.TradeStatus != "TRADE_SUCCESS" && notification.TradeStatus != "TRADE_FINISHED" && notification.RefundRequestNo == "" {
		return CallbackResult{}, ErrInvalidCallback
	}
	kind, amount := "payment", notification.TotalAmount
	if notification.RefundRequestNo != "" || notification.RefundAmount != "" {
		kind, amount = "refund", notification.RefundAmount
	}
	minor, err := amountToMinor(amount)
	if err != nil {
		return CallbackResult{}, ErrInvalidCallback
	}
	occurred := time.Now().UTC()
	for _, key := range []string{"gmt_payment", "gmt_refund", "notify_time"} {
		if raw := values.Get(key); raw != "" {
			if parsed, parseErr := time.ParseInLocation("2006-01-02 15:04:05", raw, time.FixedZone("CST", 8*60*60)); parseErr == nil {
				occurred = parsed.UTC()
				break
			}
		}
	}
	transactionDigest := effectport.Hash("alipay.transaction", notification.TradeNo)
	return CallbackResult{
		EventDigest: sha256.Sum256([]byte(notification.NotifyID)), BodyDigest: sha256.Sum256([]byte(values.Encode())),
		Kind: kind, MerchantOrderNo: notification.MerchantOrderNo, RefundNo: notification.RefundRequestNo,
		AppID: notification.AppID, ProviderTransactionReference: notification.TradeNo, ProviderTransactionDigest: string(transactionDigest),
		ProviderRefundDigest: string(effectport.Hash("alipay.refund", notification.RefundRequestNo, notification.TradeNo)),
		AmountMinor:          minor, Currency: "CNY", OccurredAt: occurred, Provider: paymentdomain.ProviderAlipay,
	}, nil
}

func amountToMinor(value string) (int64, error) {
	parts := strings.Split(value, ".")
	if len(parts) > 2 || len(parts) == 0 || parts[0] == "" || len(parts) == 2 && len(parts[1]) > 2 {
		return 0, ErrInvalidMaterial
	}
	yuan, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || yuan < 0 {
		return 0, ErrInvalidMaterial
	}
	fraction := "00"
	if len(parts) == 2 {
		fraction = parts[1] + strings.Repeat("0", 2-len(parts[1]))
	}
	fen, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil || yuan > (1<<63-1-fen)/100 {
		return 0, ErrInvalidMaterial
	}
	minor := yuan*100 + fen
	if minor < 1 {
		return 0, ErrInvalidMaterial
	}
	return minor, nil
}

func validWebPayRequest(request WebPayRequest) bool {
	return strings.TrimSpace(request.MerchantOrderNo) == request.MerchantOrderNo && request.MerchantOrderNo != "" && len(request.MerchantOrderNo) <= 64 && strings.TrimSpace(request.Subject) != "" && strings.TrimSpace(request.TotalAmount) != ""
}

func minorToAmount(value int64) string {
	return fmt.Sprintf("%d.%02d", value/100, value%100)
}

var _ effectport.ProviderAdapter = (*Alipay)(nil)
