package provider

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/profitsharing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// ProfitSharingMaterial is released only inside Payment's provider adapter.
// Account and original transaction reference never appear in an EER envelope,
// Distribution response, audit payload, or log.
type ProfitSharingMaterial paymentport.ProfitSharingProviderMaterial

func (v ProfitSharingMaterial) valid(kind effectport.Kind) bool {
	if !effectport.ValidDigest(v.PayloadDigest) || strings.TrimSpace(v.AppID) != v.AppID || v.AppID == "" {
		return false
	}
	switch kind {
	case effectport.KindWeChatPayReceiverAdd:
		return v.ReceiverAccount != "" && strings.TrimSpace(v.ReceiverAccount) == v.ReceiverAccount
	case effectport.KindWeChatPayProfitSharing:
		return v.ReceiverAccount != "" && v.TransactionReference != "" && validProfitSharingOrderNo(v.ProviderOrderNo) && v.AmountMinor > 0
	case effectport.KindWeChatPayProfitUnfreeze:
		return v.TransactionReference != "" && validProfitSharingUnfreezeNo(v.ProviderOrderNo) && strings.TrimSpace(v.Reason) == v.Reason && v.Reason != ""
	default:
		return false
	}
}

// ProfitSharingMaterialLoader is an optional narrow provider seam. Payment's
// implementation reads its own instructions and asks Identity for the exact,
// scoped OpenID transiently. It cannot be implemented by Distribution.
type ProfitSharingMaterialLoader interface {
	LoadProfitSharing(context.Context, effectport.Kind, effectport.Digest) (ProfitSharingMaterial, error)
	LoadProfitSharingReference(context.Context, string) (effectport.Kind, ProfitSharingMaterial, error)
}

type ProfitSharingSDK interface {
	AddReceiver(context.Context, ProfitSharingMaterial) error
	CreateOrder(context.Context, ProfitSharingMaterial) error
	QueryOrder(context.Context, ProfitSharingMaterial) (ProfitSharingQuery, error)
	UnfreezeOrder(context.Context, ProfitSharingMaterial) error
}

// ProfitSharingQuery is safe to persist as a receipt digest after Payment
// verifies it. ReceiverConfirmedSuccess/Failure are never inferred from an
// HTTP 2xx or an order-level FINISHED state: either requires the exact receiver
// account, amount and a terminal detail from the official query response.
type ProfitSharingQuery struct {
	State                                              string
	ReceiverConfirmedSuccess, ReceiverConfirmedFailure bool
	// FailureClass is a bounded projection of an exact PERSONAL_OPENID CLOSED
	// detail. Raw Provider response text and identifiers never leave this SDK
	// adapter.
	FailureClass string
	OutcomeKnown bool
	OccurredAt   time.Time
}

type OfficialProfitSharingSDK struct {
	receivers profitsharing.ReceiversApiService
	orders    profitsharing.OrdersApiService
}

// ProfitSharingProviderRejection intentionally preserves only a bounded,
// documented provider class. It must not wrap the SDK APIError because that
// object contains raw response data and headers.
type ProfitSharingProviderRejection struct{ class string }

func (value *ProfitSharingProviderRejection) Error() string {
	return "profit sharing provider rejected request"
}

func (value *ProfitSharingProviderRejection) FailureClass() string {
	if value == nil {
		return ""
	}
	return value.class
}

func profitSharingProviderRejectionClass(err error) string {
	var value *ProfitSharingProviderRejection
	if errors.As(err, &value) {
		return value.FailureClass()
	}
	return ""
}

func classifyOfficialProfitSharingError(err error) error {
	var api *core.APIError
	if errors.As(err, &api) && api.StatusCode == http.StatusForbidden && api.Code == "NO_AUTH" {
		return &ProfitSharingProviderRejection{class: paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied}
	}
	return ErrInvalidResponse
}

// NewOfficialProfitSharingSDK builds the official WeChat Pay Go SDK client.
// It is intentionally separate from NewWeChatPay: unless Composition injects
// this client and a Payment-only material loader, split effects fail closed and
// make no provider call.
func NewOfficialProfitSharingSDK(ctx context.Context, merchantID, serial, apiV3Key string, signer *rsa.PrivateKey) (*OfficialProfitSharingSDK, error) {
	return newOfficialProfitSharingSDKWithAutoCertificate(ctx, merchantID, serial, apiV3Key, signer, newProfitSharingAutoClient)
}

// NewOfficialProfitSharingSDKWithPublicKey preserves deployments that already
// manage the WeChat platform verification/public encryption key themselves.
// It does not register the SDK certificate downloader or perform setup I/O.
// publicKeyID must be the configured WeChat Pay public-key id, never an
// unscoped key guessed from a response.
func NewOfficialProfitSharingSDKWithPublicKey(ctx context.Context, merchantID, serial, publicKeyID string, signer *rsa.PrivateKey, publicKey *rsa.PublicKey) (*OfficialProfitSharingSDK, error) {
	return NewOfficialProfitSharingSDKWithAuthentication(ctx, merchantID, serial, signer, ProfitSharingAuthentication{Mode: ProfitSharingAuthenticationPublicKey, PlatformPublicKeyID: publicKeyID, PlatformPublicKey: publicKey})
}

// NewOfficialProfitSharingSDKWithCertificate uses a configured, trusted
// platform certificate and performs no certificate download at construction.
func NewOfficialProfitSharingSDKWithCertificate(ctx context.Context, merchantID, serial string, signer *rsa.PrivateKey, certificate *x509.Certificate) (*OfficialProfitSharingSDK, error) {
	return NewOfficialProfitSharingSDKWithAuthentication(ctx, merchantID, serial, signer, ProfitSharingAuthentication{Mode: ProfitSharingAuthenticationCertificate, PlatformCertificate: certificate})
}

func (sdk *OfficialProfitSharingSDK) AddReceiver(ctx context.Context, value ProfitSharingMaterial) error {
	if sdk == nil || !value.valid(effectport.KindWeChatPayReceiverAdd) {
		return ErrInvalidMaterial
	}
	relation := profitsharing.RECEIVERRELATIONTYPE_DISTRIBUTOR
	kind := profitsharing.RECEIVERTYPE_PERSONAL_OPENID
	response, _, err := sdk.receivers.AddReceiver(ctx, profitsharing.AddReceiverRequest{Account: &value.ReceiverAccount, Appid: &value.AppID, RelationType: &relation, Type: &kind})
	if err != nil {
		return classifyOfficialProfitSharingError(err)
	}
	if response == nil || response.Account == nil || *response.Account != value.ReceiverAccount || response.Type == nil || *response.Type != kind {
		return ErrInvalidResponse
	}
	return nil
}

func (sdk *OfficialProfitSharingSDK) CreateOrder(ctx context.Context, value ProfitSharingMaterial) error {
	if sdk == nil || !value.valid(effectport.KindWeChatPayProfitSharing) {
		return ErrInvalidMaterial
	}
	personal := "PERSONAL_OPENID"
	description := "distribution commission"
	freezeRemaining := false
	response, _, err := sdk.orders.CreateOrder(ctx, profitsharing.CreateOrderRequest{Appid: &value.AppID, OutOrderNo: &value.ProviderOrderNo, TransactionId: &value.TransactionReference, UnfreezeUnsplit: &freezeRemaining, Receivers: []profitsharing.CreateOrderReceiver{{Account: &value.ReceiverAccount, Amount: &value.AmountMinor, Description: &description, Type: &personal}}})
	if err != nil || response == nil || response.OutOrderNo == nil || *response.OutOrderNo != value.ProviderOrderNo || response.TransactionId == nil || *response.TransactionId != value.TransactionReference {
		return ErrInvalidResponse
	}
	return nil
}

func (sdk *OfficialProfitSharingSDK) QueryOrder(ctx context.Context, value ProfitSharingMaterial) (ProfitSharingQuery, error) {
	if sdk == nil || !value.validForQuery() {
		return ProfitSharingQuery{}, ErrInvalidMaterial
	}
	response, _, err := sdk.orders.QueryOrder(ctx, profitsharing.QueryOrderRequest{OutOrderNo: &value.ProviderOrderNo, TransactionId: &value.TransactionReference})
	if err != nil || response == nil || response.OutOrderNo == nil || *response.OutOrderNo != value.ProviderOrderNo || response.TransactionId == nil || *response.TransactionId != value.TransactionReference || response.State == nil {
		return ProfitSharingQuery{}, ErrInvalidResponse
	}
	query := ProfitSharingQuery{State: string(*response.State), OccurredAt: time.Now().UTC()}
	for _, detail := range response.Receivers {
		if profitSharingReceiverDetailMatches(detail, value) {
			if *detail.Result == profitsharing.DETAILSTATUS_SUCCESS {
				query.ReceiverConfirmedSuccess, query.OutcomeKnown = true, true
				if detail.FinishTime != nil {
					query.OccurredAt = detail.FinishTime.UTC()
				}
				return query, nil
			}
			if *detail.Result == profitsharing.DETAILSTATUS_CLOSED {
				query.ReceiverConfirmedFailure, query.OutcomeKnown = true, true
				query.FailureClass = profitSharingClosedDetailFailureClass(detail.FailReason)
				if detail.FinishTime != nil {
					query.OccurredAt = detail.FinishTime.UTC()
				}
				return query, nil
			}
		}
	}
	// FINISHED only terminates the aggregate order. If the exact receiver/amount
	// is absent, money may have reached another detail: preserve the reserve and
	// reconcile it as an exceptional unknown rather than infer non-payment.
	return query, nil
}

// profitSharingReceiverDetailMatches is intentionally shared by SUCCESS and
// CLOSED handling. An order-level result, a matching account/amount with a
// different receiver type, or any incomplete detail is never proof of this
// instruction's outcome.
func profitSharingReceiverDetailMatches(detail profitsharing.OrderReceiverDetail, value ProfitSharingMaterial) bool {
	return detail.Account != nil && detail.Amount != nil && detail.Type != nil && detail.Result != nil && *detail.Account == value.ReceiverAccount && *detail.Amount == value.AmountMinor && *detail.Type == profitsharing.RECEIVERTYPE_PERSONAL_OPENID
}

// profitSharingClosedDetailFailureClass maps only the official, finite CLOSED
// detail reasons that Payment has approved for durable diagnostics. The SDK's
// older typed enum deliberately cannot constrain newer documented strings;
// anything absent or outside this list remains unknown.
func profitSharingClosedDetailFailureClass(value *profitsharing.DetailFailReason) string {
	if value == nil {
		return ""
	}
	switch string(*value) {
	case "ACCOUNT_ABNORMAL":
		return paymentdomain.ProfitSharingInstructionFailureReceiverAccountAbnormal
	case "NO_RELATION":
		return paymentdomain.ProfitSharingInstructionFailureReceiverRelationRemoved
	case "RECEIVER_HIGH_RISK":
		return paymentdomain.ProfitSharingInstructionFailureReceiverHighRisk
	case "RECEIVER_REAL_NAME_NOT_VERIFIED":
		return paymentdomain.ProfitSharingInstructionFailureReceiverRealNameMissing
	case "NO_AUTH":
		return paymentdomain.ProfitSharingInstructionFailureMerchantPermissionLost
	case "RECEIVER_RECEIPT_LIMIT":
		return paymentdomain.ProfitSharingInstructionFailureReceiverReceiptLimit
	case "PAYER_ACCOUNT_ABNORMAL":
		return paymentdomain.ProfitSharingInstructionFailurePayerAccountAbnormal
	case "INVALID_REQUEST":
		return paymentdomain.ProfitSharingInstructionFailureInvalidRequest
	default:
		return ""
	}
}

func (sdk *OfficialProfitSharingSDK) UnfreezeOrder(ctx context.Context, value ProfitSharingMaterial) error {
	if sdk == nil || !value.valid(effectport.KindWeChatPayProfitUnfreeze) {
		return ErrInvalidMaterial
	}
	response, _, err := sdk.orders.UnfreezeOrder(ctx, profitsharing.UnfreezeOrderRequest{Description: &value.Reason, OutOrderNo: &value.ProviderOrderNo, TransactionId: &value.TransactionReference})
	if err != nil || response == nil || response.OutOrderNo == nil || *response.OutOrderNo != value.ProviderOrderNo || response.TransactionId == nil || *response.TransactionId != value.TransactionReference {
		return ErrInvalidResponse
	}
	return nil
}

func validProfitSharingOrderNo(value string) bool {
	return validProfitSharingProviderNo(value, "v3ps_", 20)
}

func validProfitSharingUnfreezeNo(value string) bool {
	return validProfitSharingProviderNo(value, "v3psu_", 20)
}

func validProfitSharingProviderNo(value, prefix string, minimumSuffix int) bool {
	if !strings.HasPrefix(value, prefix) || len(value) < len(prefix)+minimumSuffix || len(value) > len(prefix)+40 {
		return false
	}
	for _, r := range value[len(prefix):] {
		if r < 'A' || r > 'Z' {
			if r < '2' || r > '7' {
				return false
			}
		}
	}
	return true
}

func (v ProfitSharingMaterial) validForQuery() bool {
	return v.valid(effectport.KindWeChatPayProfitSharing) || v.valid(effectport.KindWeChatPayProfitUnfreeze)
}
