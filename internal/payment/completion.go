package payment

import (
	"context"
	"errors"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type EffectProjector interface {
	CompleteEffectWithin(context.Context, string, effectport.Envelope, effectport.Attempt, effectport.AdapterResult) error
	ReconciliationTargetWithin(context.Context, effectport.Envelope) (paymentport.ReconciliationTarget, error)
}

type CompletionSink struct {
	projector EffectProjector
	enqueuer  paymentport.ReconciliationEnqueuer
	orders    orderport.PaymentCoordinator
}

func NewCompletionSink(projector EffectProjector, enqueuer paymentport.ReconciliationEnqueuer, orders orderport.PaymentCoordinator) (*CompletionSink, error) {
	if projector == nil || enqueuer == nil || orders == nil {
		return nil, errors.New("payment completion projector is required")
	}
	return &CompletionSink{projector: projector, enqueuer: enqueuer, orders: orders}, nil
}

func (sink *CompletionSink) CompleteEffect(ctx context.Context, effectRef string, envelope effectport.Envelope, attempt effectport.Attempt, result effectport.AdapterResult) error {
	if sink == nil || envelope.Owner != effectport.OwnerPayment {
		return errors.New("unsupported payment completion")
	}
	if err := sink.projector.CompleteEffectWithin(ctx, effectRef, envelope, attempt, result); err != nil {
		return err
	}
	if (envelope.Kind == effectport.KindWeChatPayPrepay || envelope.Kind == effectport.KindAlipayWapPay || envelope.Kind == effectport.KindAlipayPagePay) && result.Completion == effectport.StateFinalFailed {
		target, err := sink.projector.ReconciliationTargetWithin(ctx, envelope)
		if err != nil {
			return err
		}
		_, err = sink.orders.SettlePaymentWithin(ctx, orderport.PaymentSettlementCommand{
			OrderID: target.OrderID, Failed: true, OccurredAt: time.Now().UTC(), ReceiptKey: "effect-final:" + effectRef,
		})
		return err
	}
	shouldReconcile := envelope.Kind == effectport.KindWeChatShopRefund && result.Completion == effectport.StateExecuted ||
		envelope.Kind == effectport.KindWeChatPayRefund && (result.Completion == effectport.StateExecuted || result.Completion == effectport.StateUnknown) ||
		envelope.Kind == effectport.KindAlipayRefund && (result.Completion == effectport.StateExecuted || result.Completion == effectport.StateUnknown) ||
		envelope.Kind == effectport.KindWeChatPayPrepay && (result.Completion == effectport.StateExecuted || result.Completion == effectport.StateUnknown) ||
		(envelope.Kind == effectport.KindAlipayWapPay || envelope.Kind == effectport.KindAlipayPagePay) && (result.Completion == effectport.StateExecuted || result.Completion == effectport.StateUnknown)
	if !shouldReconcile {
		return nil
	}
	target, err := sink.projector.ReconciliationTargetWithin(ctx, envelope)
	if err != nil {
		return err
	}
	return sink.enqueuer.EnqueueWithin(ctx, target)
}

var _ effectport.CompletionSink = (*CompletionSink)(nil)
