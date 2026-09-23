package domain

import (
	"errors"
	"testing"
	"time"
)

func ptr(value int64) *int64 { return &value }

func nativeInput() NewOrderInput {
	return NewOrderInput{
		Provider: ProviderWeChatPay, SourceSystem: "aicrm-v3", SourceKey: "order-001",
		MerchantOrderNo: "M-001", PayerCustomerID: ptr(11), BeneficiaryCustomerID: ptr(22),
		Amount:       Money{AmountMinor: 3000, Currency: "CNY"},
		Items:        []ItemSnapshot{{LineNo: 1, ProductCode: "course", ProductName: "课程", UnitAmountMinor: 1500, Quantity: 2, LineAmountMinor: 3000}},
		RecordOrigin: RecordOriginNative, CreatedAt: time.Date(2026, 9, 3, 1, 2, 3, 0, time.UTC),
	}
}

func TestMoneyProviderAndActors(t *testing.T) {
	if _, err := NewMoney(0, "CNY"); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("zero money err=%v", err)
	}
	if _, err := NewMoney(1, "cny"); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("lowercase currency err=%v", err)
	}
	order, err := NewOrder(nativeInput())
	if err != nil {
		t.Fatal(err)
	}
	if *order.PayerCustomerID != 11 || *order.BeneficiaryCustomerID != 22 || order.PayerCustomerID == order.BeneficiaryCustomerID {
		t.Fatalf("payer/beneficiary collapsed: %#v", order)
	}
	missing := nativeInput()
	missing.PayerCustomerID = nil
	if _, err = NewOrder(missing); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("native order without payer err=%v", err)
	}
	alipay := nativeInput()
	alipay.Provider = ProviderAlipay
	if _, err = NewOrder(alipay); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("native Alipay write err=%v", err)
	}
}

func TestItemSnapshotsAreImmutableAndBalanceOrder(t *testing.T) {
	input := nativeInput()
	order, err := NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Items[0].ProductName = "被修改"
	items := order.Items()
	items[0].ProductName = "再次修改"
	if got := order.Items()[0].ProductName; got != "课程" {
		t.Fatalf("snapshot mutated: %q", got)
	}
	unbalanced := nativeInput()
	unbalanced.Items[0].LineAmountMinor = 2999
	if _, err = NewOrder(unbalanced); !errors.Is(err, ErrInvalidOrder) {
		t.Fatalf("unbalanced items err=%v", err)
	}
}

func TestLifecycleAndRefundBalance(t *testing.T) {
	order, err := NewOrder(nativeInput())
	if err != nil {
		t.Fatal(err)
	}
	paid, event, err := order.ApplySettlement(1, StatusPaid, 0, order.CreatedAt.Add(time.Minute))
	if err != nil || paid.Version != 2 || event.From != StatusPendingPayment || event.To != StatusPaid {
		t.Fatalf("paid=%#v event=%#v err=%v", paid, event, err)
	}
	partial, _, err := paid.ApplySettlement(2, StatusPartiallyRefunded, 1000, paid.UpdatedAt.Add(time.Minute))
	if err != nil || partial.RefundedMinor != 1000 || partial.RefundableMinor() != 2000 {
		t.Fatalf("partial=%#v err=%v", partial, err)
	}
	refunded, _, err := partial.ApplySettlement(3, StatusRefunded, 3000, partial.UpdatedAt.Add(time.Minute))
	if err != nil || refunded.RefundableMinor() != 0 {
		t.Fatalf("refunded=%#v err=%v", refunded, err)
	}
	if _, _, err = paid.ApplySettlement(2, StatusRefunded, 2999, paid.UpdatedAt.Add(time.Minute)); !errors.Is(err, ErrInvalidSettlement) {
		t.Fatalf("invalid full refund err=%v", err)
	}
	if _, _, err = order.ApplySettlement(99, StatusPaid, 0, order.UpdatedAt); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version conflict err=%v", err)
	}
	if _, _, err = refunded.ApplySettlement(4, StatusPaid, 0, refunded.UpdatedAt.Add(time.Minute)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("reverse transition err=%v", err)
	}
}

func TestHistoricalOrdersCanRemainUnresolvedButNeverEffectEligible(t *testing.T) {
	input := nativeInput()
	input.RecordOrigin = RecordOriginHistory
	input.SourceSystem = "aicrm-production"
	input.Provider = ProviderAlipay
	input.PayerCustomerID = nil
	input.BeneficiaryCustomerID = nil
	input.EffectEligible = true
	order, err := NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	if order.EffectEligible || order.PayerCustomerID != nil || order.BeneficiaryCustomerID != nil {
		t.Fatalf("unsafe history order=%#v", order)
	}
}

func TestHistoricalPayerAttributionIsScopedAndIdempotent(t *testing.T) {
	input := nativeInput()
	input.RecordOrigin = RecordOriginHistory
	input.PayerCustomerID, input.BeneficiaryCustomerID = nil, nil
	order, err := NewOrder(input)
	if err != nil {
		t.Fatal(err)
	}
	linked, changed, err := order.AttributeHistoricalPayer(order.Version, 41, order.UpdatedAt.Add(time.Second))
	if err != nil || !changed || linked.PayerCustomerID == nil || *linked.PayerCustomerID != 41 || linked.BeneficiaryCustomerID != nil || linked.EffectEligible || linked.Version != order.Version+1 {
		t.Fatalf("linked=%+v changed=%t err=%v", linked.Snapshot(), changed, err)
	}
	replayed, changed, err := linked.AttributeHistoricalPayer(linked.Version, 41, linked.UpdatedAt)
	if err != nil || changed || replayed.Version != linked.Version {
		t.Fatalf("replayed=%+v changed=%t err=%v", replayed.Snapshot(), changed, err)
	}
	if _, _, err = linked.AttributeHistoricalPayer(linked.Version, 42, linked.UpdatedAt.Add(time.Second)); err == nil {
		t.Fatal("historical payer attribution replaced an existing customer")
	}
}
