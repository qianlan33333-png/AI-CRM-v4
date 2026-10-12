package domain

import (
	"testing"
	"time"
)

func TestNoPaymentRequiresLocalZeroAndRejectsRefundEquality(t *testing.T) {
	input := nativeInput()
	input.Provider = ProviderNoPayment
	input.Amount.AmountMinor = 0
	for i := range input.Items {
		input.Items[i].UnitAmountMinor = 0
		input.Items[i].LineAmountMinor = 0
	}
	order, err := NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	paid, _, err := order.ApplySettlement(order.Version, StatusPaid, 0, order.UpdatedAt.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []Status{StatusRefunded, StatusPartiallyRefunded, StatusPaymentFailed} {
		if _, _, err := paid.ApplySettlement(paid.Version, state, 0, paid.UpdatedAt.Add(time.Second)); err == nil {
			t.Fatalf("zero settled as %s", state)
		}
	}
	for _, provider := range []Provider{ProviderWeChatPay, ProviderAlipay, ProviderExternalHistory} {
		bad := input
		bad.Provider = provider
		if _, err := NewOrder(bad); err == nil {
			t.Fatalf("zero native %s accepted", provider)
		}
	}
	positive := input
	positive.Amount.AmountMinor = 1
	positive.Items[0].UnitAmountMinor = 1
	positive.Items[0].LineAmountMinor = 1
	if _, err := NewOrder(positive); err == nil {
		t.Fatal("no_payment with positive amount accepted")
	}
	if _, err := NewMoney(0, "CNY"); err == nil {
		t.Fatal("general Money zero guard relaxed")
	}
	snapshot := paid.Snapshot()
	snapshot.Status = StatusRefunded
	if _, err := Restore(snapshot); err == nil {
		t.Fatal("zero refund snapshot accepted")
	}
	snapshot = paid.Snapshot()
	snapshot.ProviderTransactionNo = "fabricated"
	if _, err := Restore(snapshot); err == nil {
		t.Fatal("no_payment transaction accepted")
	}
}
