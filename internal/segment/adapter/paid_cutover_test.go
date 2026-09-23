package adapter

import (
	"context"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
	segmentdsl "github.com/qianlan33333-png/AI-CRM-v3/internal/segment/dsl"
	wecomport "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"testing"
	"time"
)

func TestPaidCutoverRule37UsesPaymentTimeAndAnyActiveRelationship(t *testing.T) {
	from := time.Date(2026, 8, 12, 11, 30, 0, 0, time.UTC)
	before := from.Add(-time.Second)
	to := from.Add(time.Hour)
	facts := legacyFacts{orders: []orderport.PaidAudienceOrder{
		{CustomerID: 1, ProductCode: "202608121337", PaidAt: &from},
		{CustomerID: 1, ProductCode: "202608121337", PaidAt: &from},
		{CustomerID: 2, ProductCode: "202608121337", PaidAt: &before},
		{CustomerID: 3, ProductCode: "202608121337"},
		{CustomerID: 4, ProductCode: "202608121337", PaidAt: &from},
		{CustomerID: 5, ProductCode: "different", PaidAt: &from},
		{CustomerID: 6, ProductCode: "202608121337", PaidAt: &to},
	}, contacts: []wecomport.AudienceContact{
		{CustomerID: 1, OwnerUserID: "any-owner", Status: "active"},
		{CustomerID: 2, Status: "active"}, {CustomerID: 3, Status: "active"},
		{CustomerID: 4, Status: "deleted"}, {CustomerID: 5, Status: "active"}, {CustomerID: 6, Status: "active"},
	}}
	source := LegacyTemplateSource{Orders: facts, Contacts: facts}
	definition := legacyDefinition(t, segmentdsl.PaidOrder, `{"product_codes":["202608121337"],"paid_at_from":"2026-08-12T19:30:00+08:00","paid_at_to":"2026-08-12T20:30:00+08:00","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true}`)
	result, err := source.Evaluate(context.Background(), definition, to.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, result, 1)
}

// The reviewed package-30 rule is intentionally a full native business-state
// recomputation. It does not reuse old mobile-hash joins or incremental clocks.
func TestPaidCutoverRule30UnboundedCurrentBuyers(t *testing.T) {
	at := time.Date(2026, 9, 11, 5, 0, 0, 0, time.UTC)
	facts := legacyFacts{orders: []orderport.PaidAudienceOrder{
		{CustomerID: 1, ProductCode: "subscription_trial_month"},
		{CustomerID: 2, ProductCode: "prd_20260518095708_9f77db"},
		{CustomerID: 3, ProductCode: "prd_20260707050545_291025"},
		{CustomerID: 4, ProductCode: "another-product"},
		{CustomerID: 5, ProductCode: "subscription_trial_month"},
	}, contacts: []wecomport.AudienceContact{
		{CustomerID: 1, Status: "active"}, {CustomerID: 2, Status: "active"}, {CustomerID: 3, Status: "active"},
		{CustomerID: 4, Status: "active"}, {CustomerID: 5, Status: "deleted"},
	}}
	source := LegacyTemplateSource{Orders: facts, Contacts: facts}
	definition := legacyDefinition(t, segmentdsl.PaidOrder, `{"product_codes":["subscription_trial_month","prd_20260518095708_9f77db","prd_20260707050545_291025"],"paid_at_from":"","paid_at_to":"","owner_scope":"all","owner_staff_ids":[],"require_active_wecom_contact":true}`)
	result, err := source.Evaluate(context.Background(), definition, at)
	if err != nil {
		t.Fatal(err)
	}
	assertAudienceIDs(t, result, 1, 2, 3)
}
