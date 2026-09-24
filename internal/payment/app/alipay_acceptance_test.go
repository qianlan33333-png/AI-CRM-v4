package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
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

func TestAlipayCheckoutReadbackUsesPaymentProviderChannelAndTrustedWechatSession(t *testing.T) {
	for _, channel := range []domain.Channel{domain.ChannelAlipayWap, domain.ChannelAlipayPage} {
		t.Run(string(channel), func(t *testing.T) {
			store := &storeStub{payment: domain.Payment{
				ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, Channel: channel,
				MerchantOrderNo: "merchant-test-1", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11,
				AmountMinor: 990, Currency: "CNY", Status: domain.StatusAwaitingPayment,
			}}
			sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
				"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiaryCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf},
			}}
			service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{})
			got, err := service.GetCheckout(context.Background(), domain.ProviderAlipay, "merchant-test-1", "authorized-payment-session")
			if err != nil || got.Provider != domain.ProviderAlipay || got.Channel != channel || got.Status != domain.StatusAwaitingPayment || string(got.Payload) == "" {
				t.Fatalf("Alipay checkout readback=%+v err=%v", got, err)
			}
			if _, err = service.GetCheckout(context.Background(), domain.ProviderWeChatPay, "merchant-test-1", "authorized-payment-session"); !errors.Is(err, paymentport.ErrNotFound) {
				t.Fatalf("WeChat route read an Alipay order: %v", err)
			}
		})
	}
}

func TestAlipayCheckoutReadbackValidatesChannelSpecificPrepayEffect(t *testing.T) {
	for _, test := range []struct {
		channel domain.Channel
		kind    effectport.Kind
	}{{domain.ChannelAlipayWap, effectport.KindAlipayWapPay}, {domain.ChannelAlipayPage, effectport.KindAlipayPagePay}} {
		t.Run(string(test.channel), func(t *testing.T) {
			store := &storeStub{payment: domain.Payment{
				ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, Channel: test.channel,
				MerchantOrderNo: "merchant-test-1", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11,
				AmountMinor: 990, Currency: "CNY", Status: domain.StatusAwaitingPrepay, EffectID: "eer_alipay_7",
			}}
			sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{
				"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiaryCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf},
			}}
			reader := &prepayReadStub{projection: effectport.Projection{ID: "eer_alipay_7", Owner: effectport.OwnerPayment, Kind: test.kind, State: effectport.StateQueued}}
			service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{}, reader)
			got, err := service.GetCheckout(context.Background(), domain.ProviderAlipay, "merchant-test-1", "authorized-payment-session")
			if err != nil || got.Provider != domain.ProviderAlipay || got.Channel != test.channel || got.PrepayState != effectport.StateQueued {
				t.Fatalf("Alipay prepay readback=%+v err=%v", got, err)
			}
			reader.projection.Kind = effectport.KindWeChatPayPrepay
			if _, err = service.GetCheckout(context.Background(), domain.ProviderAlipay, "merchant-test-1", "authorized-payment-session"); !errors.Is(err, paymentport.ErrUnavailable) {
				t.Fatalf("mismatched WeChat effect was exposed for Alipay: %v", err)
			}
		})
	}
}

func TestAlipayCheckoutReadbackDoesNotBroadenPayerBeneficiaryOrSessionScope(t *testing.T) {
	store := &storeStub{payment: domain.Payment{
		ID: 7, OrderID: 3, Provider: domain.ProviderAlipay, Channel: domain.ChannelAlipayWap,
		MerchantOrderNo: "merchant-test-1", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 22,
		AmountMinor: 990, Currency: "CNY", Status: domain.StatusPaid,
	}}
	actors := map[string]paymentport.SessionActor{
		"same-admin-beneficiary":    {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelMiniProgram, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted},
		"same-payer-self-mismatch":  {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiaryCustomerID: 11, BeneficiarySelection: paymentport.BeneficiarySelectionPayerSelf},
		"other-payer-identity":      {PayerIdentityID: 5, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted},
		"other-payer-customer":      {PayerIdentityID: 4, PayerCustomerID: 12, Channel: domain.ChannelH5Official, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted},
		"unrelated-session-channel": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelAlipayWap, BeneficiaryCustomerID: 22, BeneficiarySelection: paymentport.BeneficiarySelectionAdminAssisted},
	}
	sessions := checkoutReadSessionStub{actors: actors}
	service := NewService(uowStub{}, store, orderStub{}, sessions, &effectStub{})
	for _, name := range []string{"same-admin-beneficiary"} {
		if _, err := service.GetCheckout(context.Background(), domain.ProviderAlipay, "merchant-test-1", name); err != nil {
			t.Fatalf("same payer and selected beneficiary should read checkout: actor=%s err=%v", name, err)
		}
	}
	for _, name := range []string{"same-payer-self-mismatch", "other-payer-identity", "other-payer-customer", "unrelated-session-channel"} {
		if _, err := service.GetCheckout(context.Background(), domain.ProviderAlipay, "merchant-test-1", name); !errors.Is(err, paymentport.ErrConflict) {
			t.Fatalf("Alipay readback crossed payer, beneficiary, or session scope: actor=%s err=%v", name, err)
		}
	}
}

func TestAlipaySubjectComesFromSingleImmutableOrderItemAndFitsProviderBudget(t *testing.T) {
	tests := []struct {
		name  string
		items []orderdomain.ItemSnapshot
		want  string
		ok    bool
	}{
		{name: "order item name", items: []orderdomain.ItemSnapshot{{ProductName: "产品标题", ProductCode: "immutable-code"}}, want: "产品标题", ok: true},
		{name: "item code fallback", items: []orderdomain.ItemSnapshot{{ProductCode: "immutable-code"}}, want: "immutable-code", ok: true},
		{name: "unicode title limit", items: []orderdomain.ItemSnapshot{{ProductName: strings.Repeat("课", paymentport.AlipayMaxSubjectRunes+3)}}, want: strings.Repeat("课", paymentport.AlipayMaxSubjectRunes), ok: true},
		{name: "missing item", ok: false},
		{name: "ambiguous items", items: []orderdomain.ItemSnapshot{{ProductName: "one"}, {ProductName: "two"}}, ok: false},
		{name: "missing name and code", items: []orderdomain.ItemSnapshot{{ProductName: "  ", ProductCode: " "}}, ok: false},
		{name: "control character", items: []orderdomain.ItemSnapshot{{ProductName: "bad\ntitle"}}, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := alipaySubjectFromOrder(orderdomain.Snapshot{Items: test.items})
			if ok != test.ok || got != test.want {
				t.Fatalf("subject=%q ok=%t want=%q ok=%t", got, ok, test.want, test.ok)
			}
			if ok && len([]rune(got)) > paymentport.AlipayMaxSubjectRunes {
				t.Fatalf("subject exceeds Alipay rune budget: %d", len([]rune(got)))
			}
		})
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
