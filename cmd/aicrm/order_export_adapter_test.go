package main

import (
	distributionport "github.com/qianlan33333-png/AI-CRM-v3/internal/distribution/port"
	"testing"
)

func TestOrderExportCommissionUsesCurrentPayableAndRecordedRecovery(t *testing.T) {
	lines := []distributionport.OrderDistributionLine{
		{HasCommission: true, CurrentPayableMinor: 300, PaidMinor: 100, Status: "pending", Adjustments: []distributionport.OrderDistributionAdjustment{{Kind: "manual_recovery", DeltaMinor: 50}, {Kind: "merchant_liability", DeltaMinor: 70}}},
		{HasCommission: true, CurrentPayableMinor: 100, PaidMinor: 100, Status: "paid"},
		{HasCommission: true, CurrentPayableMinor: 0, PaidMinor: 100, Status: "cancelled", Exceptions: []distributionport.OrderDistributionException{{Kind: "buyer_refund_after_paid", Status: "open"}}},
		{HasCommission: false, CurrentPayableMinor: 999, Status: "pending"},
	}
	v := exportCommissionFacts(lines)
	if v.PayableMinor != 400 || v.PendingMinor != 200 || v.RecoveredMinor != 50 || !v.Exception {
		t.Fatalf("incorrect aggregate: %+v", v)
	}
	if v = exportCommissionFacts([]distributionport.OrderDistributionLine{{HasCommission: true, Status: "pending", Exceptions: []distributionport.OrderDistributionException{{Kind: "settlement_deadline_imminent", Status: "open"}}}}); v.Exception {
		t.Fatal("scheduling warning became financial exception")
	}
}
