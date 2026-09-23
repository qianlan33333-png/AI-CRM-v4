package provider

import (
	"context"
	"strconv"
	"strings"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func (provider *WeChatPay) executeProfitSharing(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	loader, ok := provider.loader.(ProfitSharingMaterialLoader)
	if !ok || provider.profitSharing == nil {
		return final("wechatpay.profit-sharing.unavailable", envelope, attempt), nil
	}
	material, err := loader.LoadProfitSharing(ctx, envelope.Kind, envelope.SourceRefDigest)
	if err != nil || material.PayloadDigest != envelope.PayloadDigest || !material.valid(envelope.Kind) {
		return final("wechatpay.profit-sharing.material", envelope, attempt), nil
	}
	// A network error can have reached WeChat. EER must preserve this original
	// effect/merchant order no and let the reconciliation path query it; no new
	// money instruction or idempotency key is ever minted here.
	var callErr error
	switch envelope.Kind {
	case effectport.KindWeChatPayReceiverAdd:
		callErr = provider.profitSharing.AddReceiver(ctx, material)
	case effectport.KindWeChatPayProfitSharing:
		callErr = provider.profitSharing.CreateOrder(ctx, material)
	case effectport.KindWeChatPayProfitUnfreeze:
		callErr = provider.profitSharing.UnfreezeOrder(ctx, material)
	default:
		return final("wechatpay.profit-sharing.unsupported", envelope, attempt), nil
	}
	if callErr != nil {
		if envelope.Kind == effectport.KindWeChatPayReceiverAdd && profitSharingProviderRejectionClass(callErr) == paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied {
			return effectport.AdapterResult{Completion: effectport.StateFinalFailed, ReceiptDigest: receipt("wechatpay.profit-sharing.receiver-rejected", envelope, attempt), FailureCode: paymentdomain.ProfitSharingReceiverFailureProviderPermissionDenied, CallAttempted: true, RealExternalCallExecuted: true}, nil
		}
		return effectport.AdapterResult{Completion: effectport.StateUnknown, ReceiptDigest: receipt("wechatpay.profit-sharing.outcome-unknown", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, callErr
	}
	return effectport.AdapterResult{Completion: effectport.StateExecuted, ReceiptDigest: receipt("wechatpay.profit-sharing.accepted", envelope, attempt), CallAttempted: true, RealExternalCallExecuted: true}, nil
}

func (provider *WeChatPay) QueryProfitSharing(ctx context.Context, reference string) (paymentport.ProfitSharingProviderResult, error) {
	loader, ok := provider.loader.(ProfitSharingMaterialLoader)
	if provider == nil || !provider.config.Enabled || provider.profitSharing == nil || !ok {
		return paymentport.ProfitSharingProviderResult{}, ErrInvalidMaterial
	}
	kind, material, err := loader.LoadProfitSharingReference(ctx, reference)
	if err != nil || kind != effectport.KindWeChatPayProfitSharing || !material.valid(kind) {
		return paymentport.ProfitSharingProviderResult{}, ErrInvalidMaterial
	}
	query, err := provider.profitSharing.QueryOrder(ctx, material)
	if err != nil {
		return paymentport.ProfitSharingProviderResult{}, err
	}
	known := query.ReceiverConfirmedSuccess || query.ReceiverConfirmedFailure
	return paymentport.ProfitSharingProviderResult{State: query.State, ReceiverConfirmedSuccess: query.ReceiverConfirmedSuccess, ReceiverConfirmedFailure: query.ReceiverConfirmedFailure, FailureClass: query.FailureClass, OutcomeKnown: known, OccurredAt: query.OccurredAt.UTC(), EvidenceDigest: effectport.Hash("wechatpay.profit-sharing.query", reference, query.State, query.FailureClass, strconv.FormatBool(query.ReceiverConfirmedSuccess), strconv.FormatBool(query.ReceiverConfirmedFailure), strconv.FormatBool(known), query.OccurredAt.UTC().Format(time.RFC3339Nano))}, nil
}

func (provider *WeChatPay) QueryProfitSharingUnfreeze(ctx context.Context, reference string) (paymentport.ProfitSharingProviderResult, error) {
	loader, ok := provider.loader.(ProfitSharingMaterialLoader)
	if provider == nil || !provider.config.Enabled || provider.profitSharing == nil || !ok {
		return paymentport.ProfitSharingProviderResult{}, ErrInvalidMaterial
	}
	kind, material, err := loader.LoadProfitSharingReference(ctx, reference)
	if err != nil || kind != effectport.KindWeChatPayProfitUnfreeze || !material.valid(kind) {
		return paymentport.ProfitSharingProviderResult{}, ErrInvalidMaterial
	}
	// WeChat documents QueryOrder as the result query after an unfreeze. It
	// never reports a receiver success for this path; the aggregate terminal
	// state is preserved as reconciliation evidence only.
	query, err := provider.profitSharing.QueryOrder(ctx, material)
	if err != nil {
		return paymentport.ProfitSharingProviderResult{}, err
	}
	known := query.OutcomeKnown || strings.EqualFold(query.State, "FINISHED")
	return paymentport.ProfitSharingProviderResult{State: query.State, OutcomeKnown: known, OccurredAt: query.OccurredAt.UTC(), EvidenceDigest: effectport.Hash("wechatpay.profit-sharing.unfreeze.query", reference, query.State, strconv.FormatBool(known), query.OccurredAt.UTC().Format(time.RFC3339Nano))}, nil
}

var _ paymentport.ProfitSharingReconciler = (*WeChatPay)(nil)
