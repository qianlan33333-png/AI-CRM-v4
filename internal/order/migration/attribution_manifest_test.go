package migration

import "testing"

func TestAttributionManifestRejectsRawIdentityOnQuarantineAndDuplicates(t *testing.T) {
	valid := []byte(`{"schema_version":"aicrm-order-history-attribution-v1","run_key":"attribution-test","snapshot_at":"2026-09-04T00:00:00Z","source_system":"aicrm-production","identity_kind":"wecom_external_userid","rows":[{"source_key":"order-1","merchant_order_no":"merchant-1","external_userid":"external-1","evidence_state":"candidate","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	manifest, err := ParseAttribution(valid)
	if err != nil || manifest.Summary().Candidates != 1 {
		t.Fatalf("manifest=%+v err=%v", manifest.Summary(), err)
	}
	rawQuarantine := []byte(`{"schema_version":"aicrm-order-history-attribution-v1","run_key":"attribution-test","snapshot_at":"2026-09-04T00:00:00Z","source_system":"aicrm-production","identity_kind":"wecom_external_userid","rows":[{"source_key":"order-1","merchant_order_no":"merchant-1","external_userid":"must-not-survive","evidence_state":"source_identity_not_found","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	if _, err = ParseAttribution(rawQuarantine); err == nil {
		t.Fatal("quarantine row retained a raw external identity")
	}
	duplicate := []byte(`{"schema_version":"aicrm-order-history-attribution-v1","run_key":"attribution-test","snapshot_at":"2026-09-04T00:00:00Z","source_system":"aicrm-production","identity_kind":"wecom_external_userid","rows":[{"source_key":"order-1","merchant_order_no":"merchant-1","external_userid":"external-1","evidence_state":"candidate","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"source_key":"order-1","merchant_order_no":"merchant-2","external_userid":"external-2","evidence_state":"candidate","evidence_digest":"sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}]}`)
	if _, err = ParseAttribution(duplicate); err == nil {
		t.Fatal("duplicate source attribution accepted")
	}
}
