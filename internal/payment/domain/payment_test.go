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
