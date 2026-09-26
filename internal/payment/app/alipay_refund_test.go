package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	"testing"
	"time"
)

type alipayRefundReconcilerStub struct {
	query             paymentport.AlipayRefundQuery
	orderNo, refundNo string
}

func (*alipayRefundReconcilerStub) QueryPayment(context.Context, string) (paymentport.AlipayPaymentQuery, error) {
	return paymentport.AlipayPaymentQuery{}, errors.New("unexpected payment query")
}
func (s *alipayRefundReconcilerStub) QueryRefund(_ context.Context, orderNo, refundNo string) (paymentport.AlipayRefundQuery, error) {
	s.orderNo, s.refundNo = orderNo, refundNo
	return s.query, nil
}

func TestAlipayRefundReconciliationRequiresExactFactsAndSettlesOnce(t *testing.T) {
	for _, name := range []string{"success", "pending", "wrong_order", "wrong_refund", "wrong_trade", "wrong_amount", "wrong_total", "wrong_digest"} {
		t.Run(name, func(t *testing.T) {
			now := time.Date(2026, 9, 26, 4, 0, 0, 0, time.UTC)
			store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, MerchantOrderNo: "M-ALI-7", AmountMinor: 990, Currency: "CNY", Status: domain.StatusPaid, ProviderTransactionDigest: string(effectport.Hash("alipay.transaction", "ALI-TRADE-7"))}, refund: domain.Refund{ID: 9, PaymentID: 7, Provider: domain.ProviderAlipay, RefundNo: "RF-ALI-9", AmountMinor: 120, Status: domain.RefundOutcomeUnknown, Version: 4, CreatedAt: now, UpdatedAt: now}}
			orders := &recordingOrderStub{}
			adapter := &alipayRefundReconcilerStub{query: paymentport.AlipayRefundQuery{MerchantOrderNo: "M-ALI-7", TradeNo: "ALI-TRADE-7", RefundNo: "RF-ALI-9", Currency: "CNY", Status: "REFUND_SUCCESS", AmountMinor: 120, TotalMinor: 990, OccurredAt: now.Add(time.Minute), EvidenceDigest: effectport.Hash("query", name), RefundDigest: effectport.Hash("alipay.refund", "RF-ALI-9", "ALI-TRADE-7")}}
			switch name {
			case "pending":
				adapter.query.Status = ""
				adapter.query.AmountMinor = 0
				adapter.query.TotalMinor = 0
				adapter.query.RefundDigest = ""
			case "wrong_order":
				adapter.query.MerchantOrderNo = "other"
			case "wrong_refund":
				adapter.query.RefundNo = "other"
			case "wrong_trade":
				adapter.query.TradeNo = "other"
			case "wrong_amount":
				adapter.query.AmountMinor = 121
			case "wrong_total":
				adapter.query.TotalMinor = 991
			case "wrong_digest":
				adapter.query.RefundDigest = effectport.Hash("other")
			}
			service := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
			if err := service.SetAlipayReconciler(adapter); err != nil {
				t.Fatal(err)
			}
			_, err := service.ReconcileAlipayRefund(context.Background(), 9)
			if adapter.orderNo != "M-ALI-7" || adapter.refundNo != "RF-ALI-9" {
				t.Fatal("query lost original keys")
			}
			if name == "success" {
				if err != nil {
					t.Fatal(err)
				}
				if _, err = service.ReconcileAlipayRefund(context.Background(), 9); err != nil {
					t.Fatal(err)
				}
				if store.refund.Status != domain.RefundCompleted || store.refundSettlementUpdates != 1 || orders.settlementCount != 1 || orders.settlement.RefundedDelta != 120 {
					t.Fatal("refund did not settle exactly once")
				}
			} else {
				if name == "pending" && err != nil {
					t.Fatal(err)
				}
				if name != "pending" && !errors.Is(err, paymentport.ErrConflict) {
					t.Fatalf("expected conflict: %v", err)
				}
				if store.refund.Status != domain.RefundOutcomeUnknown || orders.settlementCount != 0 || store.refundSettlementUpdates != 0 {
					t.Fatal("unverified result changed business state")
				}
			}
		})
	}
}
