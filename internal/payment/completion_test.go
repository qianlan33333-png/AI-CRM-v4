package payment

import (
	"context"
	"testing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	paymentdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

type completionProjectorStub struct {
	target    paymentport.ReconciliationTarget
	completed int
}

func (stub *completionProjectorStub) CompleteEffectWithin(context.Context, string, effectport.Envelope, effectport.Attempt, effectport.AdapterResult) error {
	stub.completed++
	return nil
}
func (stub *completionProjectorStub) ReconciliationTargetWithin(context.Context, effectport.Envelope) (paymentport.ReconciliationTarget, error) {
	return stub.target, nil
}

type completionEnqueuerStub struct {
	target paymentport.ReconciliationTarget
	calls  int
}

func (stub *completionEnqueuerStub) EnqueueWithin(_ context.Context, target paymentport.ReconciliationTarget) error {
	stub.target, stub.calls = target, stub.calls+1
	return nil
}

type completionOrderStub struct {
	settlement orderport.PaymentSettlementCommand
}

func (*completionOrderStub) ReservePaymentWithin(context.Context, int64) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (*completionOrderStub) CreatePaymentOrderWithin(context.Context, orderport.PaymentOrderCommand) (orderdomain.Snapshot, error) {
	return orderdomain.Snapshot{}, nil
}
func (stub *completionOrderStub) SettlePaymentWithin(_ context.Context, command orderport.PaymentSettlementCommand) (orderdomain.Snapshot, error) {
	stub.settlement = command
	return orderdomain.Snapshot{}, nil
}

func TestCompletionSinkDurablyQueuesPrepayAndAcceptedShopOutcomes(t *testing.T) {
	tests := []struct {
		name       string
		kind       effectport.Kind
		completion effectport.State
		target     paymentport.ReconciliationTarget
		wantQueue  bool
		wantFailed bool
	}{
		{name: "pay prepay unknown", kind: effectport.KindWeChatPayPrepay, completion: effectport.StateUnknown, target: paymentport.ReconciliationTarget{Provider: paymentdomain.ProviderWeChatPay, PaymentID: 1}, wantQueue: true},
		{name: "pay prepay final failure", kind: effectport.KindWeChatPayPrepay, completion: effectport.StateFinalFailed, target: paymentport.ReconciliationTarget{Provider: paymentdomain.ProviderWeChatPay, OrderID: 4, PaymentID: 1}, wantFailed: true},
		{name: "pay refund accepted", kind: effectport.KindWeChatPayRefund, completion: effectport.StateExecuted, target: paymentport.ReconciliationTarget{Provider: paymentdomain.ProviderWeChatPay, RefundID: 2}, wantQueue: true},
		{name: "pay refund unknown", kind: effectport.KindWeChatPayRefund, completion: effectport.StateUnknown, target: paymentport.ReconciliationTarget{Provider: paymentdomain.ProviderWeChatPay, RefundID: 2}, wantQueue: true},
		{name: "shop refund accepted", kind: effectport.KindWeChatShopRefund, completion: effectport.StateExecuted, target: paymentport.ReconciliationTarget{Provider: paymentdomain.ProviderWeChatShop, RefundID: 3}, wantQueue: true},
		{name: "pay prepay handoff", kind: effectport.KindWeChatPayPrepay, completion: effectport.StateExecuted, target: paymentport.ReconciliationTarget{Provider: paymentdomain.ProviderWeChatPay, PaymentID: 1}, wantQueue: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			projector := &completionProjectorStub{target: test.target}
			enqueuer := &completionEnqueuerStub{}
			orders := &completionOrderStub{}
			sink, err := NewCompletionSink(projector, enqueuer, orders)
			if err != nil {
				t.Fatal(err)
			}
			err = sink.CompleteEffect(context.Background(), "eer_1", effectport.Envelope{Owner: effectport.OwnerPayment, Kind: test.kind}, effectport.Attempt{Number: 1}, effectport.AdapterResult{Completion: test.completion})
			if err != nil || projector.completed != 1 || (enqueuer.calls == 1) != test.wantQueue {
				t.Fatalf("completed=%d queue=%d err=%v", projector.completed, enqueuer.calls, err)
			}
			if test.wantQueue && enqueuer.target != test.target {
				t.Fatalf("target=%+v want=%+v", enqueuer.target, test.target)
			}
			if orders.settlement.Failed != test.wantFailed {
				t.Fatalf("failed settlement=%+v want=%v", orders.settlement, test.wantFailed)
			}
			if test.wantFailed && orders.settlement.OrderID != test.target.OrderID {
				t.Fatalf("order settlement=%+v", orders.settlement)
			}
		})
	}
}
