package domain

import (
	"testing"
	"time"
)

func TestCommissionDueHonorsProviderMinuteAndLongerPolicy(t *testing.T) {
	paid := time.Date(2026, 10, 10, 10, 0, 0, 0, time.UTC)
	a := Attribution{ID: 1, OrderID: 2, OrderItemLine: 1, ProductCode: "p-1", ProductName: "Product", DistributorID: 3, PromotionCredentialID: 4, QualificationEvidenceRef: "order:1:item:1", QualificationState: QualificationEligible, PolicyVersion: 1, CommissionRateBasisPoints: 3000, AttributedAt: paid}
	for _, row := range []struct {
		days  int32
		delay time.Duration
	}{{0, time.Minute}, {7, 7 * 24 * time.Hour}, {29, 29 * 24 * time.Hour}} {
		a.WaitDays = row.days
		c, err := NewCommission(a, 680, paid)
		if err != nil || !c.DueAt.Equal(paid.Add(row.delay)) || c.InitialMinor != 204 {
			t.Fatalf("days=%d due=%v err=%v", row.days, c.DueAt, err)
		}
	}
}
