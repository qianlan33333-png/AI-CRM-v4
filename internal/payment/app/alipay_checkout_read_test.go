package app

import (
	"context"
	"errors"
	"testing"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

func TestGetCheckoutReadsAlipayOriginalOrderThroughTrustedH5Session(t *testing.T) {
	for _, tc := range []struct {
		name, kind string
		channel    domain.Channel
	}{
		{"wap", string(effectport.KindAlipayWapPay), domain.ChannelAlipayWap},
		{"page", string(effectport.KindAlipayPagePay), domain.ChannelAlipayPage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payment := domain.Payment{ID: 48, OrderID: 48, Provider: domain.ProviderAlipay, Channel: tc.channel, MerchantOrderNo: "M-alipay-48", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 1980000, Currency: "CNY", Status: domain.StatusAwaitingPrepay, EffectID: "eer_48"}
			store := &storeStub{payment: payment}
			sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
				"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved},
				"wrong-payment-session-01":   {PayerIdentityID: 5, PayerCustomerID: 12, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved},
				"mini-payment-session-01":    {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelMiniProgram, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved},
			}}
			reader := &prepayReadStub{projection: effectport.Projection{ID: "eer_48", Owner: effectport.OwnerPayment, Kind: effectport.Kind(tc.kind), State: effectport.StateRetryable}}
			service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{}, reader)
			got, err := service.GetCheckout(context.Background(), payment.MerchantOrderNo, "authorized-payment-session")
			if err != nil || got.PaymentID != payment.ID || got.PrepayState != effectport.StateRetryable || got.AmountMinor != payment.AmountMinor || reader.calls != 1 {
				t.Fatalf("Alipay original checkout=%+v calls=%d err=%v", got, reader.calls, err)
			}
			for _, token := range []string{"wrong-payment-session-01", "mini-payment-session-01"} {
				if _, err = service.GetCheckout(context.Background(), payment.MerchantOrderNo, token); !errors.Is(err, paymentport.ErrConflict) {
					t.Fatalf("unauthorized token %s read checkout: %v", token, err)
				}
			}
			if reader.calls != 1 {
				t.Fatalf("unauthorized read reached effect owner: %d", reader.calls)
			}
			reader.projection.Kind = effectport.KindWeChatPayPrepay
			if _, err = service.GetCheckout(context.Background(), payment.MerchantOrderNo, "authorized-payment-session"); !errors.Is(err, paymentport.ErrUnavailable) {
				t.Fatalf("wrong provider effect accepted: %v", err)
			}
		})
	}
}

func TestGetCheckoutRejectsCrossProviderMerchantReferenceCollision(t *testing.T) {
	store := &storeStub{checkoutPayments: map[domain.Provider]domain.Payment{
		domain.ProviderWeChatPay: {ID: 1, Provider: domain.ProviderWeChatPay, MerchantOrderNo: "M-collision"},
		domain.ProviderAlipay:    {ID: 2, Provider: domain.ProviderAlipay, MerchantOrderNo: "M-collision"},
	}}
	sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
		"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official},
	}}
	service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{})
	if _, err := service.GetCheckout(context.Background(), "M-collision", "authorized-payment-session"); !errors.Is(err, paymentport.ErrConflict) {
		t.Fatalf("ambiguous provider reference accepted: %v", err)
	}
}

func TestGetCheckoutReturnsAlipayHandoffAfterOriginalEffectCompletes(t *testing.T) {
	store := &storeStub{payment: domain.Payment{ID: 48, OrderID: 48, Provider: domain.ProviderAlipay, Channel: domain.ChannelAlipayWap, MerchantOrderNo: "M-alipay-48", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 1980000, Currency: "CNY", Status: domain.StatusAwaitingPayment}}
	sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
		"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved},
	}}
	service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{})
	got, err := service.GetCheckout(context.Background(), "M-alipay-48", "authorized-payment-session")
	if err != nil || got.PaymentID != 48 || got.Status != domain.StatusAwaitingPayment || len(got.Payload) == 0 || store.handoffCalls != 1 {
		t.Fatalf("original Alipay handoff=%+v calls=%d err=%v", got, store.handoffCalls, err)
	}
}
