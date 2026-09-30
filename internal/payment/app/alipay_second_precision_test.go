package app

import (
	"context"
	"errors"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	paymentprovider "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/provider"
	"testing"
	"time"
)

type secondPrecisionAlipayQuery struct {
	query paymentport.AlipayPaymentQuery
}

func (q secondPrecisionAlipayQuery) QueryPayment(context.Context, string) (paymentport.AlipayPaymentQuery, error) {
	return q.query, nil
}
func (q secondPrecisionAlipayQuery) QueryRefund(context.Context, string, string) (paymentport.AlipayRefundQuery, error) {
	panic("unexpected refund")
}

func TestAlipaySecondPrecisionCallbackAndQuery(t *testing.T) {
	at := time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)
	for _, first := range []string{"callback", "query"} {
		t.Run(first, func(t *testing.T) {
			for _, offset := range []time.Duration{0, -time.Second, time.Second} {
				t.Run(offset.String(), func(t *testing.T) {
					updated := at.Add(999999 * time.Microsecond)
					store := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, Channel: domain.ChannelAlipayWap, MerchantOrderNo: "same-second-order", AmountMinor: 990, Currency: "CNY", Status: domain.StatusAwaitingPrepay, Version: 2, CreatedAt: at.Add(123456 * time.Microsecond), UpdatedAt: updated}}
					orders := &recordingOrderStub{}
					s := NewService(uowStub{}, store, orders, sessionStub{}, &effectStub{})
					if err := s.SetAlipayAppID("same-second-app"); err != nil {
						t.Fatal(err)
					}
					occurred := at.Add(offset)
					digest := effectport.Hash("alipay.transaction", "same-second-trade")
					callback := paymentprovider.CallbackResult{Provider: domain.ProviderAlipay, Kind: "payment", AppID: "same-second-app", MerchantOrderNo: store.payment.MerchantOrderNo, ProviderTransactionReference: "same-second-trade", ProviderTransactionDigest: string(digest), AmountMinor: 990, Currency: "CNY", OccurredAt: occurred, EventDigest: [32]byte{81}, BodyDigest: [32]byte{82}}
					if err := s.SetAlipayReconciler(secondPrecisionAlipayQuery{paymentport.AlipayPaymentQuery{MerchantOrderNo: store.payment.MerchantOrderNo, TradeNo: "same-second-trade", TradeStatus: "TRADE_SUCCESS", AmountMinor: 990, Currency: "CNY", OccurredAt: occurred, EvidenceDigest: effectport.Hash("query", "same-second"), TransactionDigest: digest}}); err != nil {
						t.Fatal(err)
					}
					apply := func(kind string) error {
						if kind == "callback" {
							return s.ApplyVerifiedCallback(context.Background(), callback)
						}
						_, err := s.ReconcileAlipayPayment(context.Background(), 7)
						return err
					}
					wrongDigest := callback
					wrongDigest.ProviderTransactionDigest = string(effectport.Hash("alipay.transaction", "different-transaction"))
					if err := s.ApplyVerifiedCallback(context.Background(), wrongDigest); !errors.Is(err, paymentport.ErrInvalid) {
						t.Fatalf("transaction digest mismatch accepted: %v", err)
					}
					err := apply(first)
					if offset < 0 {
						if !errors.Is(err, paymentport.ErrConflict) || store.payment.Status == domain.StatusPaid || orders.settlementCount != 0 {
							t.Fatalf("older second accepted: %v", err)
						}
						return
					}
					if err != nil {
						t.Fatalf("verified same-second settlement: %v", err)
					}
					if store.payment.PaidConfirmedAt == nil || !store.payment.PaidConfirmedAt.Equal(occurred) || store.payment.UpdatedAt.Before(updated) || !orders.settlement.OccurredAt.Equal(occurred) {
						t.Fatalf("event/bookkeeping times changed: payment=%+v order=%+v", store.payment, orders.settlement)
					}
					for _, kind := range []string{"callback", "query", "callback"} {
						if err := apply(kind); err != nil {
							t.Fatal(err)
						}
					}
					if store.paymentSettlementUpdates != 1 || orders.settlementCount != 1 {
						t.Fatalf("settled twice: payment=%d order=%d", store.paymentSettlementUpdates, orders.settlementCount)
					}
				})
			}
		})
	}
}
