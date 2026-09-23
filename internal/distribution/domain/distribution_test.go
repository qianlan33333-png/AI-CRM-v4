package domain

import (
	"testing"
	"time"
)

func TestCalculateCommissionRoundsDownAndGuardsBounds(t *testing.T) {
	for _, tt := range []struct {
		name string
		paid int64
		rate int32
		want int64
	}{
		{"zero percent", 101, 0, 0},
		{"thirty percent", 101, 3000, 30},
		{"fractional cent rounds down", 2, 333, 0},
		{"normal fractional calculation", 1001, 333, 33},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := CalculateCommission(tt.paid, tt.rate)
			if err != nil || got != tt.want {
				t.Fatalf("CalculateCommission(%d,%d)=(%d,%v), want %d,nil", tt.paid, tt.rate, got, err, tt.want)
			}
		})
	}
	if _, err := CalculateCommission(1, 3001); err == nil {
		t.Fatal("rate above 30 percent was accepted")
	}
}

func TestCommissionRefundUsesRemainingPaidAmount(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	attribution := Attribution{ID: 1, OrderID: 2, OrderItemLine: 1, ProductCode: "p-1", ProductName: "Product 1", DistributorID: 3, PromotionCredentialID: 4, QualificationEvidenceRef: "order:1:item:1", QualificationState: QualificationEligible, PolicyVersion: 2, CommissionRateBasisPoints: 333, WaitDays: 7, AttributedAt: now}
	commission, err := NewCommission(attribution, 1001, now)
	if err != nil || commission.InitialMinor != 33 || commission.CurrentPayableMinor != 33 {
		t.Fatalf("new commission=%+v err=%v", commission, err)
	}
	repriced, err := commission.RepriceFromPaidMinor(commission.Version, 1001, 100, now.Add(time.Minute))
	if err != nil || repriced.CurrentPayableMinor != 30 || repriced.Status != CommissionPending {
		t.Fatalf("repriced=%+v err=%v", repriced, err)
	}
	fullyRefunded, err := repriced.RepriceFromPaidMinor(repriced.Version, 1001, 1001, now.Add(2*time.Minute))
	if err != nil || fullyRefunded.CurrentPayableMinor != 0 || fullyRefunded.Status != CommissionCancelled {
		t.Fatalf("full refund=%+v err=%v", fullyRefunded, err)
	}
}

func TestRefundCannotRewriteSettlingOrPaidCommission(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	attribution := Attribution{ID: 1, OrderID: 2, OrderItemLine: 1, ProductCode: "p-1", ProductName: "Product 1", DistributorID: 3, PromotionCredentialID: 4, QualificationEvidenceRef: "order:1:item:1", QualificationState: QualificationEligible, PolicyVersion: 2, CommissionRateBasisPoints: 1000, WaitDays: 0, AttributedAt: now}
	commission, err := NewCommission(attribution, 1000, now)
	if err != nil {
		t.Fatal(err)
	}
	settling, err := commission.BeginSettlement(commission.Version, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = settling.RepriceFromPaidMinor(settling.Version, 1000, 1000, now.Add(time.Minute)); err == nil {
		t.Fatal("in-flight settlement was repriced/cancelled")
	}
	paid, err := settling.ConfirmReceiverPaid(settling.Version, 100, now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	exception, err := paid.MarkException(paid.Version, "buyer_refund_after_payout", now.Add(2*time.Minute))
	if err != nil || exception.PaidMinor != 100 || exception.Status != CommissionException {
		t.Fatalf("paid exception=%+v err=%v", exception, err)
	}
	if _, err = exception.RepriceFromPaidMinor(exception.Version, 1000, 1000, now.Add(3*time.Minute)); err == nil {
		t.Fatal("post-paid exception was repriced/cancelled")
	}
}

func TestPendingCommissionCanBecomeDeadlineExceptionWithoutPretendingPayment(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	attribution := Attribution{ID: 1, OrderID: 2, OrderItemLine: 1, ProductCode: "p-1", ProductName: "Product 1", DistributorID: 3, PromotionCredentialID: 4, QualificationEvidenceRef: "order:1:item:1", QualificationState: QualificationEligible, PolicyVersion: 2, CommissionRateBasisPoints: 1000, WaitDays: 0, AttributedAt: now}
	commission, err := NewCommission(attribution, 1000, now)
	if err != nil {
		t.Fatal(err)
	}
	exception, err := commission.MarkException(commission.Version, "split_deadline_unavailable", now.Add(time.Minute))
	if err != nil || exception.Status != CommissionException || exception.PaidMinor != 0 || exception.CurrentPayableMinor != 100 {
		t.Fatalf("pending exception=%+v err=%v", exception, err)
	}
}

func TestPromotionCredentialSeparatesInsertAndPersistedValidity(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	credential := PromotionCredential{
		DistributorID: 1,
		ProductID:     2,
		ProductType:   ProductTypeStandard,
		Status:        CredentialActive,
		CreatedAt:     now,
		ExpiresAt:     now.Add(time.Hour),
	}
	if !credential.ValidForInsert() || credential.Valid() {
		t.Fatalf("new credential validity insert=%t persisted=%t", credential.ValidForInsert(), credential.Valid())
	}
	credential.ID = 3
	if credential.ValidForInsert() || !credential.Valid() {
		t.Fatalf("persisted credential validity insert=%t persisted=%t", credential.ValidForInsert(), credential.Valid())
	}
}

func TestAttributionSeparatesInsertAndPersistedValidity(t *testing.T) {
	now := time.Date(2026, 9, 14, 8, 0, 0, 0, time.UTC)
	attribution := Attribution{OrderID: 2, OrderItemLine: 1, ProductCode: "p-1", ProductName: "Product 1", DistributorID: 3, PromotionCredentialID: 4, QualificationEvidenceRef: "order:1:item:1", QualificationState: QualificationEligible, PolicyVersion: 2, CommissionRateBasisPoints: 333, WaitDays: 7, AttributedAt: now}
	if !attribution.ValidForInsert() || attribution.Valid() {
		t.Fatalf("new attribution validity insert=%t persisted=%t", attribution.ValidForInsert(), attribution.Valid())
	}
	attribution.ID = 5
	if attribution.ValidForInsert() || !attribution.Valid() {
		t.Fatalf("persisted attribution validity insert=%t persisted=%t", attribution.ValidForInsert(), attribution.Valid())
	}
}
