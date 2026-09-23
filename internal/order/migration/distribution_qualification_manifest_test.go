package migration

import (
	"errors"
	"testing"
)

func TestQualificationManifestBindsMappingRowToProtectedSourceOrder(t *testing.T) {
	raw := []byte(`{"schema_version":"aicrm-order-distribution-qualification-v1","run_key":"qualification-audit-20260914","source_system":"aicrm-production","snapshot_at":"2026-09-14T12:00:00Z","rows":[{"order_id":71,"order_item_line":1,"product_id":9,"product_type":"standard_product","source_product_code":"course-9","payer_customer_id":41,"beneficiary_customer_id":42,"item_paid_minor":100,"payment_confirmed_at":"2026-09-14T10:00:00Z","order_source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	manifest, err := ParseQualificationManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	evidence, err := manifest.Evidence(manifest.Rows[0])
	if err != nil || !evidence.Valid() || evidence.OrderID != 71 || evidence.ProductID != 9 || evidence.SourceProductCode != "course-9" || evidence.SourceOrderDigest == ([32]byte{}) || evidence.SourceDigest == ([32]byte{}) || manifest.DigestHex() == "" {
		t.Fatalf("evidence=%+v err=%v", evidence, err)
	}
	changed := manifest.Rows[0]
	changed.ProductType = "service_period"
	changedEvidence, err := manifest.Evidence(changed)
	if err != nil || changedEvidence.SourceDigest == evidence.SourceDigest {
		t.Fatalf("changed=%+v err=%v", changedEvidence, err)
	}
}

func TestQualificationManifestRejectsDuplicateOrUnboundRows(t *testing.T) {
	duplicate := []byte(`{"schema_version":"aicrm-order-distribution-qualification-v1","run_key":"qualification-audit-20260914","source_system":"aicrm-production","snapshot_at":"2026-09-14T12:00:00Z","rows":[{"order_id":71,"order_item_line":1,"product_id":9,"product_type":"standard_product","source_product_code":"course-9","payer_customer_id":41,"beneficiary_customer_id":42,"item_paid_minor":100,"payment_confirmed_at":"2026-09-14T10:00:00Z","order_source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},{"order_id":71,"order_item_line":1,"product_id":9,"product_type":"standard_product","source_product_code":"course-9","payer_customer_id":41,"beneficiary_customer_id":42,"item_paid_minor":100,"payment_confirmed_at":"2026-09-14T10:00:00Z","order_source_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}`)
	if _, err := ParseQualificationManifest(duplicate); !errors.Is(err, ErrInvalidQualificationManifest) {
		t.Fatalf("duplicate err=%v", err)
	}
	missingDigest := []byte(`{"schema_version":"aicrm-order-distribution-qualification-v1","run_key":"qualification-audit-20260914","source_system":"aicrm-production","snapshot_at":"2026-09-14T12:00:00Z","rows":[{"order_id":71,"order_item_line":1,"product_id":9,"product_type":"standard_product","source_product_code":"course-9","payer_customer_id":41,"beneficiary_customer_id":42,"item_paid_minor":100,"payment_confirmed_at":"2026-09-14T10:00:00Z","order_source_digest":""}]}`)
	if _, err := ParseQualificationManifest(missingDigest); !errors.Is(err, ErrInvalidQualificationManifest) {
		t.Fatalf("missing digest err=%v", err)
	}
}
