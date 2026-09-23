package source

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEncryptedSnapshotRoundTripAndTamperRejection(t *testing.T) {
	now := time.Date(2026, 9, 4, 1, 2, 3, 0, time.UTC)
	relative := int32(30)
	snapshot := Snapshot{
		Products:       []Product{{ID: 1, ProductCode: "p-1", Name: "商品", PriceMinor: 100, Currency: "CNY", Status: "active", Enabled: true, CreatedAt: now, UpdatedAt: now}},
		ServicePeriods: []ServicePeriod{},
		Coupons:        []Coupon{{ID: 2, Name: "券", DiscountAmountTotal: 10, Currency: "CNY", Status: "published", TotalIssueLimit: 10, PerUserIssueLimit: 1, ClaimStartsAt: now, ClaimEndsAt: now.Add(time.Hour), ValidityMode: "relative_days", RelativeValidityDays: &relative, CreatedAt: now, UpdatedAt: now}},
		CouponBindings: []CouponBinding{{ID: 3, CouponID: 2, TradeProductID: 1, CreatedAt: now}},
		GroupPlans:     []GroupPlan{{ID: 4, PlanCode: "plan-1", Name: "群计划", PlanType: "standard", Status: "disabled", CreatedAt: now, UpdatedAt: now}},
		GroupNodes:     []GroupNode{{ID: 5, PlanID: 4, DayIndex: 1, TriggerTimeLabel: "14:30", TextContent: "消息", SortOrder: 1, CreatedAt: now, UpdatedAt: now}},
		GroupAssets:    []GroupAsset{{ID: 6, PlanID: 4, ChatID: "wr-chat-1", CreatedAt: now}},
		Agents:         []Agent{{ID: 7, AgentCode: "agent-1", AgentName: "智能体", Status: "active", AutomationType: "agent", DraftVersion: 1, PublishedVersion: 1, CreatedAt: now, UpdatedAt: now}},
	}
	if err := PopulateManifest(&snapshot, ProductionSourceSystem, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	keyPath := filepath.Join(directory, "key")
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 1)
	}
	if err := os.WriteFile(keyPath, []byte(base64.RawStdEncoding.EncodeToString(key)), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(directory, "snapshot.enc")
	digest, err := SealToFile(snapshot, snapshotPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	loaded, loadedDigest, err := LoadFile(snapshotPath, keyPath)
	if err != nil || loaded.Manifest.SourceRevision != strings.Repeat("a", 40) || digest != loadedDigest {
		t.Fatalf("round trip failed: %v", err)
	}
	if err = os.Chmod(snapshotPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err = LoadFile(snapshotPath, keyPath); err == nil {
		t.Fatal("world-readable encrypted snapshot accepted")
	}
	if err = os.Chmod(snapshotPath, 0o600); err != nil {
		t.Fatal(err)
	}
	sealed, err := os.ReadFile(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 1
	if err = os.WriteFile(snapshotPath, sealed, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = LoadFile(snapshotPath, keyPath); err == nil {
		t.Fatal("tampered snapshot accepted")
	}
}

func TestExtractionQueriesExcludeForbiddenData(t *testing.T) {
	joined := strings.ToLower(strings.Join(mapValues(extractionQueries), "\n"))
	for _, forbidden := range []string{"webhook_url", "signature_secret", "owner_userid", "group_name_snapshot", "member_count_snapshot", "image_library_id", "wecom_tagging", "lead_channel_id", "completion_target", "issued_count", "commerce_coupon_claims", "redemption", "execution", "history", "message_log"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("source allowlist contains forbidden field %q", forbidden)
		}
	}
	for _, required := range []string{"wechat_pay_products", "service_period_products", "commerce_coupons", "commerce_coupon_product_bindings", "automation_group_ops_plans", "automation_group_ops_plan_nodes", "automation_group_ops_plan_groups", "automation_agent_runtime_config"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing source table %q", required)
		}
	}
}

func mapValues(values map[string]string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, value)
	}
	return result
}
