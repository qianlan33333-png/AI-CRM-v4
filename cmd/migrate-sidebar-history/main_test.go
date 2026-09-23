package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExtractStreamCreatesProtectedDeterministicSnapshot(t *testing.T) {
	directory := t.TempDir()
	stream := filepath.Join(directory, "source.stream")
	snapshot := filepath.Join(directory, "snapshot.json")
	captured := "2026-09-04T01:02:03.123456Z"
	entitlement := `{"source_id":7,"unionid":"union-7","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"续费","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`
	coupon := `{"source_id":8,"unionid":"union-7","coupon_id":12,"status":"consumed","claimed_at":"2026-02-01T00:00:00Z","valid_from":"2026-02-01T00:00:00Z","valid_until":"2026-03-01T00:00:00Z","redeemed_at":"2026-02-03T00:00:00Z","created_at":"2026-02-01T00:00:00Z","updated_at":"2026-02-03T00:00:00Z"}`
	raw := strings.Join([]string{
		"psql banner that must be ignored",
		snapshotMarker + captured,
		entitlementMarker + hex.EncodeToString([]byte(entitlement)),
		couponMarker + hex.EncodeToString([]byte(coupon)),
	}, "\n")
	if err := os.WriteFile(stream, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := extractStream(stream, "wechat-open-platform:primary")
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != currentSchemaVersion || len(m.Entitlements) != 1 || len(m.Coupons) != 1 || m.SourceSystem != productionSourceSystem || m.CapturedAt.Format(time.RFC3339Nano) != captured {
		t.Fatalf("unexpected manifest: %+v", m)
	}
	if err = save(snapshot, m); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(snapshot)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("snapshot mode=%v err=%v", info.Mode().Perm(), err)
	}
	loaded, err := load(snapshot)
	if err != nil || loaded.rawDigest == ([32]byte{}) || loaded.RunKey != m.RunKey {
		t.Fatalf("loaded=%+v err=%v", loaded, err)
	}
	if err = run(context.Background(), []string{"--mode=apply", "--snapshot=" + snapshot, "--manifest-sha256=" + strings.Repeat("0", 64), "--confirm-apply"}); err == nil || !strings.Contains(err.Error(), "digest confirmation") {
		t.Fatalf("tampered digest err=%v", err)
	}
}

func TestExtractStreamNormalizesAllianceWithFrozenPythonStripContract(t *testing.T) {
	directory := t.TempDir()
	stream := filepath.Join(directory, "source.stream")
	rows := []string{
		`{"source_id":7,"unionid":"union-7","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"source_id":8,"unionid":"union-8","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":" 联盟甲 ","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"source_id":9,"unionid":"union-9","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":"\t联盟乙\t","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"source_id":10,"unionid":"union-10","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":"\u00a0联盟丙\u00a0","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"source_id":11,"unionid":"union-11","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":"\u001c联盟丁\u001f","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"source_id":12,"unionid":"union-12","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":"","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
		`{"source_id":13,"unionid":"union-13","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`,
	}
	parts := []string{snapshotMarker + "2026-09-04T01:02:03Z"}
	for _, row := range rows {
		parts = append(parts, entitlementMarker+hex.EncodeToString([]byte(row)))
	}
	if err := os.WriteFile(stream, []byte(strings.Join(parts, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	m, err := extractStream(stream, "wechat-open-platform:primary")
	if err != nil {
		t.Fatal(err)
	}
	if m.SchemaVersion != currentSchemaVersion || m.Entitlements[0].Alliance != nil || !m.Entitlements[0].alliancePresent || m.Entitlements[1].Alliance == nil || *m.Entitlements[1].Alliance != "联盟甲" || !m.Entitlements[1].alliancePresent || m.Entitlements[2].Alliance == nil || *m.Entitlements[2].Alliance != "联盟乙" || !m.Entitlements[2].alliancePresent || m.Entitlements[3].Alliance == nil || *m.Entitlements[3].Alliance != "联盟丙" || !m.Entitlements[3].alliancePresent || m.Entitlements[4].Alliance == nil || *m.Entitlements[4].Alliance != "联盟丁" || !m.Entitlements[4].alliancePresent || m.Entitlements[5].Alliance == nil || *m.Entitlements[5].Alliance != "" || !m.Entitlements[5].alliancePresent || m.Entitlements[6].Alliance != nil || m.Entitlements[6].alliancePresent {
		t.Fatalf("alliance source presence lost: %#v", m.Entitlements)
	}
}

func TestExtractStreamRejectsNonStringAllianceInsteadOfInventingDisplayText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.stream")
	row := `{"source_id":7,"unionid":"union-7","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":42,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`
	if err := os.WriteFile(path, []byte(snapshotMarker+"2026-09-04T01:02:03Z\n"+entitlementMarker+hex.EncodeToString([]byte(row))), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := extractStream(path, "wechat-open-platform:primary"); err == nil || !strings.Contains(err.Error(), "invalid sidebar source row") {
		t.Fatalf("non-string alliance err=%v", err)
	}
}

func TestSourceEntitlementLegacyDigestOmitsNewAllianceKey(t *testing.T) {
	legacy := []byte(`{"source_id":7,"unionid":"union-7","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`)
	var row sourceEntitlement
	if err := json.Unmarshal(legacy, &row); err != nil {
		t.Fatal(err)
	}
	if row.alliancePresent || row.Alliance != nil {
		t.Fatalf("old snapshot acquired a new alliance fact: %#v", row)
	}
	canonical, err := json.Marshal(row)
	if err != nil || string(canonical) != string(legacy) {
		t.Fatalf("legacy canonical row=%s err=%v", canonical, err)
	}
	newNull := []byte(`{"source_id":7,"unionid":"union-7","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":null,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`)
	if err = json.Unmarshal(newNull, &row); err != nil {
		t.Fatal(err)
	}
	if !row.alliancePresent || row.Alliance != nil {
		t.Fatalf("new null alliance presence lost: %#v", row)
	}
	canonical, err = json.Marshal(row)
	if err != nil || string(canonical) != string(newNull) {
		t.Fatalf("new null canonical row=%s err=%v", canonical, err)
	}
	// A protected v1 file is read, not recaptured: its existing receipt must
	// retain whitespace even though v2 capture uses the frozen Python strip.
	legacyString := []byte(`{"source_id":7,"unionid":"union-7","service_product_id":11,"product_name":"年度服务","status":"active","start_at":"2026-01-01T00:00:00Z","end_at":"2027-01-01T00:00:00Z","remark":"","alliance":" \t联盟 ","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}`)
	if err = json.Unmarshal(legacyString, &row); err != nil {
		t.Fatal(err)
	}
	canonical, err = json.Marshal(row)
	if err != nil || string(canonical) != string(legacyString) {
		t.Fatalf("protected v1 alliance canonical row=%s err=%v", canonical, err)
	}
}

func TestExtractStreamRejectsMissingOrDuplicateMarkerAndInvalidRows(t *testing.T) {
	for name, raw := range map[string]string{
		"missing":   entitlementMarker + hex.EncodeToString([]byte(`{"source_id":1}`)),
		"duplicate": snapshotMarker + "2026-09-04T00:00:00Z\n" + snapshotMarker + "2026-09-04T00:00:01Z",
		"bad-row":   snapshotMarker + "2026-09-04T00:00:00Z\n" + couponMarker + "not-hex",
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.stream")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := extractStream(path, "wechat-open-platform:primary"); err == nil {
				t.Fatal("invalid source stream accepted")
			}
		})
	}
}

func TestCouponStatusMappingPreservesSupportedLifecycle(t *testing.T) {
	for input, want := range map[string]string{"available": "claimed", "reserved": "reserved", "consumed": "redeemed", "expired": "expired"} {
		if got := mapCouponStatus(input); got != want {
			t.Fatalf("mapCouponStatus(%q)=%q want %q", input, got, want)
		}
	}
}
