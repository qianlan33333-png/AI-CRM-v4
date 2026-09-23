package app

import (
	"context"
	"errors"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
)

func TestAlipayAcceptanceVirtualPaymentSettlementAndReplay(t *testing.T) {
	now := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	store := &storeStub{payment: domain.Payment{
		ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, Channel: domain.ChannelAlipayWap,
		MerchantOrderNo: "merchant-test-1", AmountMinor: 990, Currency: "CNY",
		Status: domain.StatusAwaitingPayment, Version: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
	}}
	orders := &recordingOrderStub{}
	service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
	if err := service.SetAlipayAppID("test-alipay-app"); err != nil {
		t.Fatal(err)
	}
	callback := paymentprovider.CallbackResult{
		Provider: domain.ProviderAlipay, Kind: "payment", AppID: "test-alipay-app",
		MerchantOrderNo: "merchant-test-1", ProviderTransactionReference: "trade-test-1",
		ProviderTransactionDigest: string(effectport.Hash("alipay.transaction", "trade-test-1")),
		AmountMinor:               990, Currency: "CNY", OccurredAt: now.Add(time.Minute),
		EventDigest: [32]byte{1}, BodyDigest: [32]byte{2},
	}
	if err := service.ApplyVerifiedCallback(context.Background(), callback); err != nil {
		t.Fatal(err)
	}
	if store.payment.Status != domain.StatusPaid || store.payment.PaidConfirmedAt == nil || store.paymentSettlementUpdates != 1 || orders.settlementCount != 1 || orders.settlement.ProviderTransactionNo != "trade-test-1" || store.callbackOutcome != "settled" {
		t.Fatalf("first settlement not applied exactly once: payment=%s updates=%d order_settlements=%d callback=%q", store.payment.Status, store.paymentSettlementUpdates, orders.settlementCount, store.callbackOutcome)
	}
	if err := service.ApplyVerifiedCallback(context.Background(), callback); err != nil {
		t.Fatal(err)
	}
	if store.paymentSettlementUpdates != 1 || orders.settlementCount != 1 || store.callbackClaims != 2 || store.callbackOutcome != "replayed" {
		t.Fatalf("duplicate callback changed settlement: updates=%d order_settlements=%d claims=%d callback=%q", store.paymentSettlementUpdates, orders.settlementCount, store.callbackClaims, store.callbackOutcome)
	}
	wrongAmount := callback
	wrongAmount.AmountMinor++
	if err := service.ApplyVerifiedCallback(context.Background(), wrongAmount); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("mismatched amount accepted: %v", err)
	}
	wrongApp := callback
	wrongApp.AppID = "other-app"
	if err := service.ApplyVerifiedCallback(context.Background(), wrongApp); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("mismatched app accepted: %v", err)
	}
}

func TestAlipayAcceptanceVirtualRefundSettlementAndReplay(t *testing.T) {
	now := time.Date(2026, 9, 23, 4, 0, 0, 0, time.UTC)
	store := &storeStub{
		callbackReplay: true,
		payment:        domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, Channel: domain.ChannelAlipayPage, MerchantOrderNo: "merchant-test-1", AmountMinor: 990, Currency: "CNY", Status: domain.StatusPaid, Version: 3, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
		refund:         domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderAlipay, RefundNo: "refund-test-1", AmountMinor: 120, Status: domain.RefundOutcomeUnknown, Version: 4, CreatedAt: now.Add(-time.Hour), UpdatedAt: now},
	}
	orders := &recordingOrderStub{}
	service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
	if err := service.SetAlipayAppID("test-alipay-app"); err != nil {
		t.Fatal(err)
	}
	callback := paymentprovider.CallbackResult{
		Provider: domain.ProviderAlipay, Kind: "refund", AppID: "test-alipay-app",
		RefundNo: "refund-test-1", AmountMinor: 120, Currency: "CNY", OccurredAt: now.Add(time.Minute),
		ProviderRefundDigest: string(effectport.Hash("alipay.refund", "refund-test-1", "trade-test-1")),
		EventDigest:          [32]byte{3}, BodyDigest: [32]byte{4},
	}
	for i := 0; i < 2; i++ {
		if err := service.ApplyVerifiedCallback(context.Background(), callback); err != nil {
			t.Fatal(err)
		}
	}
	if store.refund.Status != domain.RefundCompleted || store.refundSettlementUpdates != 1 || orders.settlementCount != 1 || orders.settlement.RefundedDelta != 120 || store.callbackClaims != 2 {
		t.Fatalf("refund replay changed settlement: status=%s updates=%d order_settlements=%d claims=%d", store.refund.Status, store.refundSettlementUpdates, orders.settlementCount, store.callbackClaims)
	}
}
