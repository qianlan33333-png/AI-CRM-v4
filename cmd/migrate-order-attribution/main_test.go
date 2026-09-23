package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInspectAcceptsProtectedAttributionManifest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "attribution.json")
	raw := `{"schema_version":"aicrm-order-history-attribution-v1","run_key":"attribution-test","snapshot_at":"2026-09-04T00:00:00Z","source_system":"aicrm-production","identity_kind":"wecom_external_userid","rows":[{"source_key":"order-1","merchant_order_no":"merchant-1","external_userid":"external-1","evidence_state":"candidate","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=inspect", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=apply", "--snapshot=" + path, "--wecom-corp-id=corp"}); err == nil {
		t.Fatal("apply accepted a missing digest and confirmation")
	}
}
