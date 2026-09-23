package provider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

var ErrInvalidCallback = errors.New("invalid wechat pay callback")

// CallbackFailureStage is safe to emit in operational diagnostics.  It never
// contains request headers, encrypted content, payment identifiers, or keys.
func CallbackFailureStage(err error) string {
	var failure *callbackVerificationFailure
	if errors.As(err, &failure) {
		return failure.stage
	}
	return "unknown"
}

type callbackVerificationFailure struct{ stage string }

func (failure *callbackVerificationFailure) Error() string { return ErrInvalidCallback.Error() }
func (failure *callbackVerificationFailure) Unwrap() error { return ErrInvalidCallback }

func invalidCallback(stage string) error { return &callbackVerificationFailure{stage: stage} }

type CallbackVerifier struct {
	PlatformKeys map[string]*rsa.PublicKey
	APIV3Key     [32]byte
	AppID        string
	AppIDs       map[string]struct{}
	MerchantID   string
	now          func() time.Time
}

type CallbackResult struct {
	EventDigest, BodyDigest      [32]byte
	Kind                         string
	MerchantOrderNo, RefundNo    string
	AppID                        string
	ProviderTransactionDigest    string
	ProviderTransactionReference string // decrypted in memory; passed only to Order's same-UoW paid fact
	ProviderRefundDigest         string
	AmountMinor                  int64
	Currency                     string
	OccurredAt                   time.Time
	Provider                     paymentdomain.Provider
}

func NewCallbackVerifier(keys map[string]*rsa.PublicKey, apiV3Key []byte, appID, merchantID string, additionalAppIDs ...string) (*CallbackVerifier, error) {
	if len(keys) == 0 || len(apiV3Key) != 32 || appID == "" || merchantID == "" {
		return nil, ErrInvalidConfig
	}
	appIDs := map[string]struct{}{appID: {}}
	for _, additional := range additionalAppIDs {
		if additional == "" {
			continue
		}
		appIDs[additional] = struct{}{}
	}
	verifier := &CallbackVerifier{PlatformKeys: keys, AppID: appID, AppIDs: appIDs, MerchantID: merchantID, now: time.Now}
	copy(verifier.APIV3Key[:], apiV3Key)
	return verifier, nil
}

func (verifier *CallbackVerifier) Verify(_ context.Context, body []byte, headers http.Header) (CallbackResult, error) {
	if verifier == nil {
		return CallbackResult{}, invalidCallback("configuration")
	}
	if !json.Valid(body) {
		return CallbackResult{}, invalidCallback("body")
	}
	if verifyResponse(verifier.PlatformKeys, verifier.now().UTC(), headers, body) != nil {
		return CallbackResult{}, invalidCallback("signature")
	}
	var envelope struct {
		ID        string `json:"id"`
		EventType string `json:"event_type"`
		Resource  struct {
			Algorithm      string `json:"algorithm"`
			Ciphertext     string `json:"ciphertext"`
			Nonce          string `json:"nonce"`
			AssociatedData string `json:"associated_data"`
		} `json:"resource"`
	}
	if json.Unmarshal(body, &envelope) != nil || envelope.ID == "" || envelope.Resource.Algorithm != "AEAD_AES_256_GCM" {
		return CallbackResult{}, invalidCallback("envelope")
	}
	plain, err := verifier.decrypt(envelope.Resource.Ciphertext, envelope.Resource.Nonce, envelope.Resource.AssociatedData)
	if err != nil {
		return CallbackResult{}, invalidCallback("decrypt")
	}
	var value struct {
		AppID         string `json:"appid"`
		MerchantID    string `json:"mchid"`
		OutTradeNo    string `json:"out_trade_no"`
		TransactionID string `json:"transaction_id"`
		TradeState    string `json:"trade_state"`
		OutRefundNo   string `json:"out_refund_no"`
		RefundID      string `json:"refund_id"`
		RefundStatus  string `json:"refund_status"`
		SuccessTime   string `json:"success_time"`
		Amount        struct {
			Total    int64  `json:"total"`
			Refund   int64  `json:"refund"`
			Currency string `json:"currency"`
		} `json:"amount"`
	}
	if json.Unmarshal(plain, &value) != nil {
		return CallbackResult{}, invalidCallback("resource")
	}
	_, acceptedAppID := verifier.AppIDs[value.AppID]
	if !acceptedAppID || value.MerchantID != verifier.MerchantID || value.Amount.Currency != "CNY" {
		return CallbackResult{}, invalidCallback("identity_or_currency")
	}
	result := CallbackResult{EventDigest: sha256.Sum256([]byte(envelope.ID)), BodyDigest: sha256.Sum256(body), AppID: value.AppID, AmountMinor: value.Amount.Total, Currency: value.Amount.Currency, MerchantOrderNo: value.OutTradeNo, Provider: paymentdomain.ProviderWeChatPay}
	if value.SuccessTime == "" {
		// The Provider's immutable success timestamp is required for the
		// Payment/Order transaction fact. Never manufacture it from local time.
		return CallbackResult{}, invalidCallback("occurred_at")
	}
	result.OccurredAt, err = time.Parse(time.RFC3339Nano, value.SuccessTime)
	if err != nil {
		return CallbackResult{}, invalidCallback("occurred_at")
	}
	switch envelope.EventType {
	case "TRANSACTION.SUCCESS":
		if value.TradeState != "SUCCESS" || value.OutTradeNo == "" || value.TransactionID == "" || value.Amount.Total < 1 {
			return CallbackResult{}, invalidCallback("payment_resource")
		}
		result.Kind = "payment"
		result.ProviderTransactionReference = value.TransactionID
		result.ProviderTransactionDigest = string(effectport.Hash("wechatpay.transaction", value.TransactionID))
	case "REFUND.SUCCESS":
		if value.RefundStatus != "SUCCESS" || value.OutRefundNo == "" || value.RefundID == "" || value.Amount.Refund < 1 {
			return CallbackResult{}, invalidCallback("refund_resource")
		}
		result.Kind, result.RefundNo, result.AmountMinor = "refund", value.OutRefundNo, value.Amount.Refund
		result.ProviderRefundDigest = string(effectport.Hash("wechatpay.refund", value.RefundID))
	default:
		return CallbackResult{}, invalidCallback("event_type")
	}
	return result, nil
}

func (verifier *CallbackVerifier) decrypt(ciphertext, nonce, associated string) ([]byte, error) {
	if len(nonce) != 12 {
		return nil, ErrInvalidCallback
	}
	decoded, err := base64.StdEncoding.Strict().DecodeString(ciphertext)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(verifier.APIV3Key[:])
	if err != nil {
		return nil, err
	}
	var gcm cipher.AEAD
	gcm, err = cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return gcm.Open(nil, []byte(nonce), decoded, []byte(associated))
}

func CallbackHeaders(request *http.Request) (http.Header, error) {
	headers := make(http.Header, 4)
	for _, name := range []string{"Wechatpay-Timestamp", "Wechatpay-Nonce", "Wechatpay-Serial", "Wechatpay-Signature"} {
		if request == nil || len(request.Header.Values(name)) != 1 || request.Header.Get(name) == "" {
			return nil, ErrInvalidCallback
		}
		headers.Set(name, request.Header.Get(name))
	}
	if _, err := strconv.ParseInt(headers.Get("Wechatpay-Timestamp"), 10, 64); err != nil {
		return nil, ErrInvalidCallback
	}
	return headers, nil
}
