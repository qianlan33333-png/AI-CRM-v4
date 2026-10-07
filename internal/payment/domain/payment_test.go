package domain

import (
	"errors"
	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	"testing"
	"time"
)

func orderSnapshot() orderdomain.Snapshot {
	payer, beneficiary := int64(1), int64(2)
	return orderdomain.Snapshot{ID: 7, Provider: orderdomain.ProviderWeChatPay, MerchantOrderNo: "M-7", PayerCustomerID: &payer, BeneficiaryCustomerID: &beneficiary, Amount: orderdomain.Money{AmountMinor: 1000, Currency: "CNY"}, RecordOrigin: orderdomain.RecordOriginNative, EffectEligible: true}
}
func TestPaymentLifecycleAndPayerBeneficiary(t *testing.T) {
	now := time.Now().UTC()
	p, err := NewPayment(orderSnapshot(), 9, now)
	if err != nil || p.PayerCustomerID == p.BeneficiaryCustomerID {
		t.Fatalf("p=%+v err=%v", p, err)
	}
	p, err = p.BindEffect(1, "eer_9", now)
	if err != nil || p.Version != 2 {
		t.Fatal(err)
	}
	p, err = p.Settle(2, StatusPaid, now)
	if err != nil || p.Status != StatusPaid {
		t.Fatal(err)
	}
	if _, err = p.Settle(2, StatusPaid, now); !errors.Is(err, ErrVersion) {
		t.Fatalf("err=%v", err)
	}
}
func TestRefundPartialFullAndUnknown(t *testing.T) {
	now := time.Now().UTC()
	p, _ := NewPayment(orderSnapshot(), 9, now)
	p.ID = 3
	p.Status = StatusPaid
	r, err := NewRefund(p, "R-1", 400, "客户申请", now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.BindEffect(1, "eer_10", now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Complete(2, RefundOutcomeUnknown, now)
	if err != nil {
		t.Fatal(err)
	}
	r, err = r.Complete(3, RefundCompleted, now)
	if err != nil || r.Status != RefundCompleted {
		t.Fatal(err)
	}
	if _, err = NewRefund(p, "R-2", 1001, "bad", now); !errors.Is(err, ErrInvalid) {
		t.Fatalf("err=%v", err)
	}
}

func TestVerifiedRefundFactSeparatesOccurrenceFromMonotonicProcessingTime(t *testing.T) {
	createdAt := time.Date(2026, 10, 7, 8, 0, 0, 500_000_000, time.UTC)
	unknownAt := createdAt.Add(5 * time.Minute)
	refund := Refund{Status: RefundOutcomeUnknown, Version: 4, CreatedAt: createdAt, UpdatedAt: unknownAt}
	providerOccurredAt := createdAt.Truncate(time.Second) // Provider fact has second precision.

	completed, err := refund.CompleteVerifiedFact(refund.Version, RefundCompleted, providerOccurredAt, unknownAt.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != RefundCompleted || completed.Version != refund.Version+1 || !completed.UpdatedAt.Equal(unknownAt) {
		t.Fatalf("completed refund=%+v", completed)
	}
	if _, err = refund.Complete(refund.Version, RefundCompleted, providerOccurredAt); !errors.Is(err, ErrTransition) {
		t.Fatalf("legacy strict completion moved UpdatedAt backward: %v", err)
	}

	if _, err = refund.CompleteVerifiedFact(refund.Version, RefundCompleted, time.Time{}, unknownAt.Add(time.Minute)); !errors.Is(err, ErrTransition) {
		t.Fatalf("missing Provider occurrence accepted: %v", err)
	}
	if _, err = refund.CompleteVerifiedFact(refund.Version, RefundCompleted, createdAt.Add(-time.Second), unknownAt.Add(time.Minute)); !errors.Is(err, ErrTransition) {
		t.Fatalf("Provider fact before refund creation accepted: %v", err)
	}
	if _, err = refund.CompleteVerifiedFact(refund.Version, RefundCompleted, providerOccurredAt, time.Time{}); !errors.Is(err, ErrTransition) {
		t.Fatalf("missing processing clock accepted: %v", err)
	}
}

func TestVerifiedRefundFactUsesMicrosecondPrecisionWhenProviderSuppliesFraction(t *testing.T) {
	createdAt := time.Date(2026, 10, 7, 8, 0, 0, 123_456_000, time.UTC)
	refund := Refund{Status: RefundEffectAccepted, Version: 2, CreatedAt: createdAt, UpdatedAt: createdAt}
	processingAt := createdAt.Add(time.Minute)
	occurredAt := createdAt.Add(500 * time.Nanosecond)

	completed, err := refund.CompleteVerifiedFact(refund.Version, RefundCompleted, occurredAt, processingAt)
	if err != nil || !completed.UpdatedAt.Equal(processingAt) {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if _, err = refund.CompleteVerifiedFact(refund.Version, RefundCompleted, createdAt.Add(-time.Microsecond), processingAt); !errors.Is(err, ErrTransition) {
		t.Fatalf("earlier microsecond fact accepted: %v", err)
	}
}

func TestVerifiedAlipaySecondPrecisionIsNarrow(t *testing.T) {
	at := time.Date(2026, 9, 30, 1, 2, 3, 0, time.UTC)
	p := Payment{Provider: ProviderAlipay, Status: StatusAwaitingPayment, Version: 2, CreatedAt: at.Add(time.Microsecond), UpdatedAt: at.Add(999999 * time.Microsecond)}
	settled, err := p.SettleVerifiedAlipayPayment(2, at)
	if err != nil || settled.PaidConfirmedAt == nil || !settled.PaidConfirmedAt.Equal(at) || !settled.UpdatedAt.Equal(p.UpdatedAt) {
		t.Fatalf("settled=%+v err=%v", settled, err)
	}
	if _, err = p.Settle(2, StatusPaid, at); !errors.Is(err, ErrTransition) {
		t.Fatalf("generic settlement broadened: %v", err)
	}
	if _, err = p.SettleVerifiedAlipayPayment(1, at); !errors.Is(err, ErrVersion) {
		t.Fatalf("CAS bypass: %v", err)
	}
	if _, err = p.SettleVerifiedAlipayPayment(2, at.Add(-time.Second)); !errors.Is(err, ErrTransition) {
		t.Fatalf("older second accepted: %v", err)
	}
	p.Provider = ProviderWeChatPay
	if _, err = p.SettleVerifiedAlipayPayment(2, at); !errors.Is(err, ErrTransition) {
		t.Fatalf("other provider accepted: %v", err)
	}
	p.Provider, p.Status = ProviderAlipay, StatusPaid
	if _, err = p.SettleVerifiedAlipayPayment(2, at); !errors.Is(err, ErrTransition) {
		t.Fatalf("terminal paid resettled: %v", err)
	}
}
