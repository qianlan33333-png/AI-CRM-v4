package source

import (
	"bytes"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseRejectsUnknownFieldsAndTrailingJSON(t *testing.T) {
	snapshot := testSnapshot(t)
	raw, _, err := snapshot.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) < 2 || raw[len(raw)-1] != '}' {
		t.Fatal("canonical snapshot is not an object")
	}
	unknown := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"unexpected":true}`)...)
	if _, _, err = Parse(unknown); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("unknown field accepted: %v", err)
	}
	trailing := append(append([]byte{}, raw...), []byte(` {}`)...)
	if _, _, err = Parse(trailing); !errors.Is(err, ErrInvalidSnapshot) {
		t.Fatalf("trailing JSON accepted: %v", err)
	}
}

func TestReadKeyRequiresExact0600Mode(t *testing.T) {
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "key")
	key := bytes.Repeat([]byte{0x42}, 32)
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyPath, 0o400); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadKey(keyPath); err == nil {
		t.Fatal("0400 key accepted")
	}
	if err := os.Chmod(keyPath, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ReadKey(keyPath)
	if err != nil || !bytes.Equal(got, key) {
		t.Fatalf("0600 key rejected: %v", err)
	}
}

func TestValidateExpectedBaselineRejectsCountDrift(t *testing.T) {
	snapshot := testSnapshot(t)
	if err := ValidateExpectedBaseline(snapshot); err == nil {
		t.Fatal("small fixture unexpectedly satisfied production baseline")
	}
	snapshot.Manifest.Counts = map[string]int{
		"products": 31, "service_periods": 2, "coupons": 15, "coupon_bindings": 15,
		"group_plans": 12, "group_nodes": 3, "group_assets": 14, "agents": 10,
	}
	if err := ValidateExpectedBaseline(snapshot); err != nil {
		t.Fatalf("baseline counts rejected: %v", err)
	}
	snapshot.Manifest.Counts["agents"] = 9
	if err := ValidateExpectedBaseline(snapshot); err == nil {
		t.Fatal("agent count drift accepted")
	}
}

func TestValidateRejectsOrphanForeignKeys(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*Snapshot)
	}{
		{name: "service period product", mutate: func(snapshot *Snapshot) {
			snapshot.ServicePeriods[0].TradeProductID = 999
		}},
		{name: "coupon binding coupon", mutate: func(snapshot *Snapshot) {
			snapshot.CouponBindings[0].CouponID = 999
		}},
		{name: "coupon binding product", mutate: func(snapshot *Snapshot) {
			snapshot.CouponBindings[0].TradeProductID = 999
		}},
		{name: "group node plan", mutate: func(snapshot *Snapshot) {
			snapshot.GroupNodes[0].PlanID = 999
		}},
		{name: "group asset plan", mutate: func(snapshot *Snapshot) {
			snapshot.GroupAssets[0].PlanID = 999
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			snapshot := testSnapshot(t)
			test.mutate(&snapshot)
			if err := snapshot.Validate(); err == nil {
				t.Fatal("orphan row accepted")
			}
		})
	}
}

func TestCanonicalDigestNormalizesEmptySlicesAndRowOrder(t *testing.T) {
	left := testSnapshot(t)
	right := left
	right.Products = append([]Product(nil), left.Products...)
	right.ServicePeriods = append([]ServicePeriod(nil), left.ServicePeriods...)
	right.Coupons = append([]Coupon(nil), left.Coupons...)
	right.CouponBindings = append([]CouponBinding(nil), left.CouponBindings...)
	right.GroupPlans = append([]GroupPlan(nil), left.GroupPlans...)
	right.GroupNodes = append([]GroupNode(nil), left.GroupNodes...)
	right.GroupAssets = append([]GroupAsset(nil), left.GroupAssets...)
	right.Agents = append([]Agent(nil), left.Agents...)
	// A second row with a later ID makes ordering observable while keeping all
	// foreign keys valid.  PopulateManifest is intentionally rerun on each
	// representation so the canonical digest is the only compared value.
	later := left.Products[0]
	later.ID = 2
	later.ProductCode = "p-2"
	right.Products = append(right.Products, later)
	if err := PopulateManifest(&right, ProductionSourceSystem, strings.Repeat("b", 40), later.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	// Build the same semantic snapshot in reverse source order.
	left.Products = append(left.Products, later)
	if err := PopulateManifest(&left, ProductionSourceSystem, strings.Repeat("b", 40), later.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	left.Products[0], left.Products[1] = left.Products[1], left.Products[0]
	if _, _, err := left.Canonical(); err != nil {
		t.Fatalf("reordered snapshot rejected: %v", err)
	}
	left.Manifest.Digests["products"] = right.Manifest.Digests["products"]
	left.Manifest.Counts["products"] = right.Manifest.Counts["products"]
	ld, err := left.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	rd, err := right.CanonicalDigest()
	if err != nil {
		t.Fatal(err)
	}
	if ld != rd {
		t.Fatalf("semantic snapshots have different canonical digest: %x != %x", ld, rd)
	}
}

func TestExtractionQueriesAreReadOnlyAndAllowlisted(t *testing.T) {
	if len(extractionQueries) != len(tableOrder) {
		t.Fatalf("query count=%d, tables=%d", len(extractionQueries), len(tableOrder))
	}
	joined := strings.ToLower(strings.Join(mapValues(extractionQueries), "\n"))
	for _, forbidden := range []string{
		"select *", "insert ", "update ", "delete ", "for update", "webhook_url", "webhook_secret",
		"signature_secret", "inbound_webhook_token", "send_webhook_url", "image_library", "miniprogram",
		"attachment", "group_invite", "wecom_tag", "claim_no", "first_claim", "redemption", "execution",
		"history", "message_log", "customer", "unionid", "openid", "external_userid",
	} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("source query contains forbidden token %q", forbidden)
		}
	}
	for _, name := range tableOrder {
		query, ok := extractionQueries[name]
		if !ok || !strings.HasPrefix(strings.TrimSpace(strings.ToLower(query)), "select ") {
			t.Fatalf("%s is not a fixed SELECT", name)
		}
		if !strings.Contains(strings.ToLower(query), "jsonb_agg") {
			t.Fatalf("%s is not an aggregate snapshot query", name)
		}
	}
	if !strings.Contains(strings.ToLower(extractionQueries["agents"]), "need_human_review") {
		t.Fatal("agent safety guard is not explicitly snapshotted")
	}
	if !strings.Contains(strings.ToLower(extractionQueries["products"]), "metadata_json->>'description'") {
		t.Fatal("product description projection is not explicit")
	}
}

func TestReaderRejectsMissingRevisionBeforeOpeningDatabase(t *testing.T) {
	reader := NewReader(nil, "")
	if _, err := reader.Extract(nil); err == nil {
		t.Fatal("reader accepted missing context/revision")
	}
}

func testSnapshot(t *testing.T) Snapshot {
	t.Helper()
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	relative := int32(30)
	snapshot := Snapshot{
		Products:       []Product{{ID: 1, ProductCode: "p-1", Name: "商品", PriceMinor: 100, Currency: "CNY", Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}},
		ServicePeriods: []ServicePeriod{{ID: 2, TradeProductID: 1, DurationDays: 30, CreatedAt: now, UpdatedAt: now}},
		Coupons:        []Coupon{{ID: 3, Name: "券", DiscountAmountTotal: 10, Currency: "CNY", Status: "published", TotalIssueLimit: 10, PerUserIssueLimit: 1, ClaimStartsAt: now, ClaimEndsAt: now.Add(time.Hour), ValidityMode: "relative_days", RelativeValidityDays: &relative, CreatedAt: now, UpdatedAt: now}},
		CouponBindings: []CouponBinding{{ID: 4, CouponID: 3, TradeProductID: 1, CreatedAt: now}},
		GroupPlans:     []GroupPlan{{ID: 5, PlanCode: "plan-1", Name: "群计划", PlanType: "standard", Status: "disabled", CreatedAt: now, UpdatedAt: now}},
		GroupNodes:     []GroupNode{{ID: 6, PlanID: 5, DayIndex: 1, TriggerTimeLabel: "14:30", TextContent: "消息", SortOrder: 1, CreatedAt: now, UpdatedAt: now}},
		GroupAssets:    []GroupAsset{{ID: 7, PlanID: 5, ChatID: "wr-chat-1", CreatedAt: now}},
		Agents:         []Agent{{ID: 8, AgentCode: "agent-1", AgentName: "智能体", Status: "active", AutomationType: "agent", DraftVersion: 1, PublishedVersion: 1, CreatedAt: now, UpdatedAt: now}},
	}
	if err := PopulateManifest(&snapshot, ProductionSourceSystem, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	return snapshot
}
