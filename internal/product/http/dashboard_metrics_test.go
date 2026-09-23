package http

import (
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	"testing"
	"time"
)

func TestDashboardMetricsExpiryBoundariesAndUnknownStates(t *testing.T) {
	at := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	rows := []memberGridRow{{entitlement: orderport.Entitlement{Status: "active", EndAt: at.Add(7 * 24 * time.Hour)}}, {entitlement: orderport.Entitlement{Status: "active", EndAt: at}}, {entitlement: orderport.Entitlement{Status: "refunded", EndAt: at.Add(time.Hour)}}}
	got := summarizeMemberGrid(rows, at)
	if got.Total != 3 || got.Active != 1 || got.Expired != 1 || got.Expiring7D != 1 || got.Other != 1 {
		t.Fatalf("wrong state distribution: %+v", got)
	}
	empty := summarizeMemberGrid(nil, at)
	if empty.Total != 0 || empty.Active != 0 {
		t.Fatal("empty result must be factual zero")
	}
}
func TestPublicGridFieldAllowlistExcludesIdentityAndNotes(t *testing.T) {
	for _, field := range []string{"member", "remark", "alliance", "customer_id", "unionid", "phone", "external_userid"} {
		if publicGridFields[field] {
			t.Fatalf("unsafe public field %s", field)
		}
	}
}
