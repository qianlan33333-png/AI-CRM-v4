package main

import (
	"context"
	od "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	op "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"testing"
	"time"
)

type noPaymentConsumerFunc func(context.Context, op.PaidEvent) error

func (f noPaymentConsumerFunc) ConsumePaidEventWithin(ctx context.Context, event op.PaidEvent) error {
	return f(ctx, event)
}
func TestNoPaymentCompletionReusesFulfillmentAndSkipsFinancialConsumers(t *testing.T) {
	commerce, purchase, distribution, referral := 0, 0, 0, 0
	counter := func(count *int) op.PaidEventConsumer {
		return noPaymentConsumerFunc(func(context.Context, op.PaidEvent) error { *count++; return nil })
	}
	fanout := orderPaidEventFanout{commerce: counter(&commerce), purchase: counter(&purchase), distribution: counter(&distribution), referral: counter(&referral)}
	event := op.PaidEvent{ID: 1, OrderID: 1, OrderVersion: 2, DomainEventOutboxID: 1, CheckoutProductID: 3, CheckoutProductType: "standard_product", OccurredAt: time.Now(), SourceDigest: op.NewPaidEventSourceDigest(1, 2), Order: od.Snapshot{ID: 1, Version: 2, Provider: od.ProviderNoPayment, RecordOrigin: od.RecordOriginNative, EffectEligible: true, Status: od.StatusPaid, Amount: od.Money{Currency: "CNY"}}}
	if !event.Valid() {
		t.Fatal("local completion event rejected")
	}
	if err := fanout.ConsumePaidEventWithin(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if commerce != 1 || purchase != 1 || distribution != 0 || referral != 0 {
		t.Fatalf("free fanout=%d,%d,%d,%d", commerce, purchase, distribution, referral)
	}
	event.Order.Provider = od.ProviderWeChatPay
	event.Order.Amount.AmountMinor = 100
	event.CheckoutGrossAmountMinor = 100
	event.CheckoutPayableAmountMinor = 100
	if err := fanout.ConsumePaidEventWithin(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if commerce != 2 || purchase != 2 || distribution != 1 || referral != 1 {
		t.Fatal("positive fanout changed")
	}
}
