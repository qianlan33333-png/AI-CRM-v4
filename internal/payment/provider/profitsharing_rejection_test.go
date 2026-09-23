package provider

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/services/profitsharing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
)

func TestProfitSharingQueryDetailRequiresExactPersonalOpenIDReceiver(t *testing.T) {
	account := "trusted-openid"
	amount := int64(990)
	personal := profitsharing.RECEIVERTYPE_PERSONAL_OPENID
	otherType := profitsharing.ReceiverType("PERSONAL_SUB_OPENID")
	material := ProfitSharingMaterial{ReceiverAccount: account, AmountMinor: amount}
	for _, value := range []struct {
		name   string
		result profitsharing.DetailStatus
		type_  *profitsharing.ReceiverType
		want   bool
	}{
		{name: "success personal", result: profitsharing.DETAILSTATUS_SUCCESS, type_: &personal, want: true},
		{name: "closed personal", result: profitsharing.DETAILSTATUS_CLOSED, type_: &personal, want: true},
		{name: "success wrong type", result: profitsharing.DETAILSTATUS_SUCCESS, type_: &otherType},
		{name: "closed wrong type", result: profitsharing.DETAILSTATUS_CLOSED, type_: &otherType},
		{name: "closed missing type", result: profitsharing.DETAILSTATUS_CLOSED},
	} {
		t.Run(value.name, func(t *testing.T) {
			result := value.result
			detail := profitsharing.OrderReceiverDetail{Account: &account, Amount: &amount, Type: value.type_, Result: &result}
			if got := profitSharingReceiverDetailMatches(detail, material); got != value.want {
				t.Fatalf("exact receiver match=%v want=%v detail=%+v", got, value.want, detail)
			}
		})
	}
}

func TestProfitSharingClosedDetailFailureClassUsesOnlyOfficialFiniteReasons(t *testing.T) {
	for _, value := range []struct{ raw, want string }{
		{"ACCOUNT_ABNORMAL", paymentdomain.ProfitSharingInstructionFailureReceiverAccountAbnormal},
		{"NO_RELATION", paymentdomain.ProfitSharingInstructionFailureReceiverRelationRemoved},
		{"RECEIVER_HIGH_RISK", paymentdomain.ProfitSharingInstructionFailureReceiverHighRisk},
		{"RECEIVER_REAL_NAME_NOT_VERIFIED", paymentdomain.ProfitSharingInstructionFailureReceiverRealNameMissing},
		{"NO_AUTH", paymentdomain.ProfitSharingInstructionFailureMerchantPermissionLost},
		{"RECEIVER_RECEIPT_LIMIT", paymentdomain.ProfitSharingInstructionFailureReceiverReceiptLimit},
		{"PAYER_ACCOUNT_ABNORMAL", paymentdomain.ProfitSharingInstructionFailurePayerAccountAbnormal},
		{"INVALID_REQUEST", paymentdomain.ProfitSharingInstructionFailureInvalidRequest},
		{"UNRECOGNIZED_PROVIDER_REASON", ""},
	} {
		t.Run(value.raw, func(t *testing.T) {
			reason := profitsharing.DetailFailReason(value.raw)
			if got := profitSharingClosedDetailFailureClass(&reason); got != value.want {
				t.Fatalf("failure class=%q want=%q", got, value.want)
			}
		})
	}
	if got := profitSharingClosedDetailFailureClass(nil); got != "" {
		t.Fatalf("nil failure class=%q", got)
	}
}

type profitSharingRejectionSDK struct{ err error }

func (value profitSharingRejectionSDK) AddReceiver(context.Context, ProfitSharingMaterial) error {
	return value.err
}
func (profitSharingRejectionSDK) CreateOrder(context.Context, ProfitSharingMaterial) error {
	return nil
}
func (profitSharingRejectionSDK) QueryOrder(context.Context, ProfitSharingMaterial) (ProfitSharingQuery, error) {
	return ProfitSharingQuery{}, nil
}
func (profitSharingRejectionSDK) UnfreezeOrder(context.Context, ProfitSharingMaterial) error {
	return nil
}

func TestOfficialProfitSharingErrorKeepsOnlyDocumentedSafeClass(t *testing.T) {
	accepted := classifyOfficialProfitSharingError(&core.APIError{StatusCode: 403, Code: "NO_AUTH", Body: `{"message":"openid must never escape"}`, Message: "merchant details"})
	if class := profitSharingProviderRejectionClass(accepted); class != paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied || strings.Contains(accepted.Error(), "openid") || strings.Contains(accepted.Error(), "merchant details") {
		t.Fatalf("accepted class/error=%q/%q", class, accepted)
	}
	var raw *core.APIError
	if errors.As(accepted, &raw) {
		t.Fatal("safe classification must not retain the raw SDK error")
	}
	for _, value := range []*core.APIError{
		{StatusCode: 401, Code: "NO_AUTH"},
		{StatusCode: 403, Code: "PARAM_ERROR"},
		{StatusCode: 500, Code: "NO_AUTH"},
	} {
		if class := profitSharingProviderRejectionClass(classifyOfficialProfitSharingError(value)); class != "" {
			t.Fatalf("unapproved status/code became safe rejection: status=%d code=%s class=%s", value.StatusCode, value.Code, class)
		}
	}
}

func TestReceiverAddUsesOnlySafeProviderRejectionAsFinalFailure(t *testing.T) {
	payload := effectport.Hash("receiver-rejection-payload")
	material := ProfitSharingMaterial{PayloadDigest: payload, AppID: "wx-test", ReceiverAccount: "opaque-openid"}
	provider := &WeChatPay{config: Config{Enabled: true}, loader: profitSharingLoaderStub{material: material}, profitSharing: profitSharingRejectionSDK{err: &ProfitSharingProviderRejection{class: paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied}}}
	result, err := provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayReceiverAdd, payload), effectport.Attempt{Number: 1})
	if err != nil || result.Completion != effectport.StateFinalFailed || result.FailureCode != paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("safe receiver rejection result=%+v err=%v", result, err)
	}
	provider.profitSharing = profitSharingRejectionSDK{err: errors.New("transport response unknown")}
	result, err = provider.Execute(context.Background(), testEnvelope(effectport.KindWeChatPayReceiverAdd, payload), effectport.Attempt{Number: 2})
	if err == nil || result.Completion != effectport.StateUnknown || result.FailureCode != "" || !result.CallAttempted || !result.RealExternalCallExecuted {
		t.Fatalf("unknown receiver failure result=%+v err=%v", result, err)
	}
}
