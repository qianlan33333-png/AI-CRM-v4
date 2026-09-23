package app

import (
	"context"
	"testing"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/payment/domain"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestCheckoutRecoveryUsesFrozenPaymentAmount(t *testing.T) {
	for _, status := range []domain.Status{domain.StatusAwaitingPrepay, domain.StatusAwaitingPayment, domain.StatusPaid, domain.StatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			st := &storeStub{payment: domain.Payment{ID: 7, OrderID: 3, Provider: domain.ProviderWeChatPay, Channel: domain.ChannelH5Official, MerchantOrderNo: "M-7", PayerIdentityID: 4, PayerCustomerID: 11, BeneficiaryCustomerID: 11, AmountMinor: 789, Currency: "CNY", Status: status}}
			sessions := checkoutReadSessionStub{actors: map[string]paymentport.SessionActor{"authorized-payment-session": {PayerIdentityID: 4, PayerCustomerID: 11, Channel: domain.ChannelH5Official, BeneficiarySelection: paymentport.BeneficiarySelectionUnresolved}}}
			svc := NewService(uowStub{}, st, orderStub{}, sessions, &effectStub{})
			products := &checkoutProductStub{product: productport.CheckoutProduct{PriceMinor: 99999, Currency: "CNY"}}
			if err := svc.SetCheckoutProductReader(products); err != nil {
				t.Fatal(err)
			}
			for _, currentPrice := range []int64{99999, 1} {
				products.product.PriceMinor = currentPrice
				got, err := svc.GetCheckout(context.Background(), "M-7", "authorized-payment-session")
				if err != nil || got.AmountMinor != 789 || got.Currency != "CNY" || products.calls != 0 {
					t.Fatalf("recovery repriced original payment: %+v %v", got, err)
				}
			}
		})
	}
}
