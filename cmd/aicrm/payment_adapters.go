package main

import (
	"context"
	"errors"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func (router composedProviderRouter) Preflight(ctx context.Context, envelope effectport.Envelope, effectID string) (bool, time.Duration, error) {
	if envelope.Owner == effectport.OwnerOutbound {
		if p, ok := router.outbound.(effectport.ProviderPreflighter); ok {
			return p.Preflight(ctx, envelope, effectID)
		}
	}
	return true, 0, nil
}

type composedProviderRouter struct {
	outbound   effectport.ProviderAdapter
	payment    effectport.ProviderAdapter
	automation effectport.ProviderAdapter
	adminops   effectport.ProviderAdapter
	segment    effectport.ProviderAdapter
}

type paymentProviderRouter struct {
	wechatPay  effectport.ProviderAdapter
	wechatShop effectport.ProviderAdapter
	alipay     effectport.ProviderAdapter
}

func (router paymentProviderRouter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	switch envelope.Kind {
	case effectport.KindWeChatPayPrepay, effectport.KindWeChatPayRefund, effectport.KindWeChatPayReceiverAdd, effectport.KindWeChatPayProfitSharing, effectport.KindWeChatPayProfitUnfreeze:
		if router.wechatPay == nil {
			return effectport.AdapterResult{}, errors.New("wechat pay provider unavailable")
		}
		return router.wechatPay.Execute(ctx, envelope, attempt)
	case effectport.KindWeChatShopRefund:
		if router.wechatShop == nil {
			return effectport.AdapterResult{}, errors.New("wechat shop provider unavailable")
		}
		return router.wechatShop.Execute(ctx, envelope, attempt)
	case effectport.KindAlipayWapPay, effectport.KindAlipayPagePay, effectport.KindAlipayRefund:
		if router.alipay == nil {
			return effectport.AdapterResult{}, errors.New("alipay provider unavailable")
		}
		return router.alipay.Execute(ctx, envelope, attempt)
	default:
		return effectport.AdapterResult{}, errors.New("payment effect kind unavailable")
	}
}

func (router composedProviderRouter) Execute(ctx context.Context, envelope effectport.Envelope, attempt effectport.Attempt) (effectport.AdapterResult, error) {
	if envelope.Owner == effectport.OwnerAdminOps {
		if router.adminops == nil {
			return effectport.AdapterResult{}, errors.New("ops provider unavailable")
		}
		return router.adminops.Execute(ctx, envelope, attempt)
	}
	if envelope.Owner == effectport.OwnerSegment {
		if router.segment == nil {
			return effectport.AdapterResult{}, errors.New("segment provider unavailable")
		}
		return router.segment.Execute(ctx, envelope, attempt)
	}
	if envelope.Owner == effectport.OwnerPayment {
		if router.payment == nil {
			return effectport.AdapterResult{}, errors.New("payment provider unavailable")
		}
		return router.payment.Execute(ctx, envelope, attempt)
	}
	if envelope.Owner == effectport.OwnerAutomation {
		if router.automation == nil {
			return effectport.AdapterResult{}, errors.New("automation provider unavailable")
		}
		return router.automation.Execute(ctx, envelope, attempt)
	}
	if router.outbound == nil {
		return effectport.AdapterResult{}, errors.New("outbound provider unavailable")
	}
	return router.outbound.Execute(ctx, envelope, attempt)
}

type composedCompletionRouter struct {
	outbound            effectport.CompletionSink
	payment             effectport.CompletionSink
	paymentDistribution effectport.CompletionSink
	automation          effectport.CompletionSink
	adminops            effectport.CompletionSink
	segment             effectport.CompletionSink
}

func (router composedCompletionRouter) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if envelope.Owner == effectport.OwnerAdminOps {
		if router.adminops == nil {
			return errors.New("ops completion unavailable")
		}
		return router.adminops.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	}
	if envelope.Owner == effectport.OwnerSegment {
		if router.segment == nil {
			return errors.New("segment completion unavailable")
		}
		return router.segment.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	}
	if envelope.Owner == effectport.OwnerPayment {
		switch envelope.Kind {
		case effectport.KindWeChatPayReceiverAdd, effectport.KindWeChatPayProfitSharing, effectport.KindWeChatPayProfitUnfreeze:
			if router.paymentDistribution == nil {
				return errors.New("payment distribution completion unavailable")
			}
			return router.paymentDistribution.CompleteEffect(ctx, effectRef, envelope, attempt, result)
		}
		if router.payment == nil {
			return errors.New("payment completion unavailable")
		}
		return router.payment.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	}
	if envelope.Owner == effectport.OwnerAutomation {
		if router.automation == nil {
			return errors.New("automation completion unavailable")
		}
		return router.automation.CompleteEffect(ctx, effectRef, envelope, attempt, result)
	}
	if router.outbound == nil {
		return errors.New("outbound completion unavailable")
	}
	return router.outbound.CompleteEffect(ctx, effectRef, envelope, attempt, result)
}

var _ effectport.ProviderAdapter = composedProviderRouter{}
var _ effectport.ProviderAdapter = paymentProviderRouter{}
var _ effectport.CompletionSink = composedCompletionRouter{}
