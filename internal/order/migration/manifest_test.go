package migration

import (
	"crypto/sha256"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadUsesCanonicalSnakeCaseAndRejectsIncompleteFinancialShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "manifest.json")
	raw := `{
  "schema_version":"aicrm-commerce-history-v3",
  "run_key":"production-commerce-20260903",
  "coverage":{"identities":true,"wechat_pay_orders":true,"wechat_pay_refunds":true,"wechat_shop_orders":true,"wechat_shop_refunds":true,"alipay_orders":true},
  "subjects":[{"source_key":"person-1","identity_keys":["identity-1"]}],
  "identities":[{"source_key":"identity-1","kind":"mp_openid","scope":"wechat-app:app","value":"opaque","source":"provider-history:wechat-pay"}],
  "identity_quarantines":[{"source_key":"identity-no-scope","reason_code":"missing_unionid_scope","evidence_digest":"sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}],
  "orders":[{"provider":"wechat_pay","source_key":"order-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"identity-1","payer_subject_key":"person-1","beneficiary_subject_key":"person-1","amount_minor":100,"currency":"CNY","status":"partially_refunded","items":[{"line_no":1,"product_code":"legacy","product_name":"历史交易","unit_amount_minor":50,"quantity":2,"line_amount_minor":100}],"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-02T00:00:00Z"}],
  "refunds":[{"provider":"wechat_pay","source_key":"refund-1","merchant_order_no":"merchant-1","refund_no":"refund-1","provider_refund_no":"provider-refund-1","amount_minor":40,"reason":"历史退款","occurred_at":"2026-09-02T00:00:00Z"}]
}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Orders[0].SourceKey != "order-1" || manifest.Identities[0].SourceKey != "identity-1" || manifest.Refunds[0].ProviderRefundNo != "provider-refund-1" {
		t.Fatalf("snake_case fields were not decoded: %#v", manifest)
	}
	if summary := manifest.Summary(); summary.SubjectRows != 1 || summary.IdentityQuarantineRows != 1 || summary.PaymentRows != 1 || summary.AmountMinor != 100 || summary.RefundMinor != 40 || !summary.Complete {
		t.Fatalf("unexpected summary: %#v", summary)
	}

	manifest.Refunds[0].AmountMinor = 101
	if err := manifest.Validate(true); err == nil {
		t.Fatal("refunds exceeding the source order were accepted")
	}
}

func TestValidateRejectsIdentityAssignedToTwoSubjectsAndFloatingRefund(t *testing.T) {
	manifest := Manifest{
		SchemaVersion: SchemaVersion,
		RunKey:        "run",
		Coverage:      Coverage{Identities: true, WeChatPayOrders: true, WeChatPayRefunds: true, WeChatShopOrders: true, WeChatShopRefunds: true, AlipayOrders: true},
		Subjects:      []SubjectRow{{SourceKey: "a", IdentityKeys: []string{"i"}}, {SourceKey: "b", IdentityKeys: []string{"i"}}},
		Identities:    []IdentityRow{{SourceKey: "i", Kind: "mp_openid", Scope: "wechat-app:app", Value: "value", Source: "history"}},
	}
	if err := manifest.Validate(true); err == nil {
		t.Fatal("identity assigned to two source subjects")
	}
}

func TestValidateRejectsQuarantineKeyCollidingWithCanonicalSubjectReceipt(t *testing.T) {
	manifest := Manifest{
		SchemaVersion:       SchemaVersion,
		RunKey:              "run",
		Coverage:            Coverage{Identities: true, WeChatPayOrders: true, WeChatPayRefunds: true, WeChatShopOrders: true, WeChatShopRefunds: true, AlipayOrders: true},
		Subjects:            []SubjectRow{{SourceKey: "person-1", IdentityKeys: []string{"identity-1"}}},
		Identities:          []IdentityRow{{SourceKey: "identity-1", Kind: "mp_openid", Scope: "wechat-app:app", Value: "value", Source: "provider-history:test"}},
		IdentityQuarantines: []IdentityQuarantineRow{{SourceKey: "person-1", ReasonCode: "missing_scope", EvidenceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}
	if err := manifest.Validate(true); err == nil {
		t.Fatal("quarantine key colliding with canonical subject receipt was accepted")
	}
}

func TestValidateRejectsCrossProviderPaymentAndRefundReceiptKeyCollisions(t *testing.T) {
	manifest, err := Parse([]byte(`{"schema_version":"aicrm-commerce-history-v3","run_key":"receipt-collision-test","coverage":{"identities":true,"wechat_pay_orders":true,"wechat_pay_refunds":true,"wechat_shop_orders":true,"wechat_shop_refunds":true,"alipay_orders":true},"subjects":[{"source_key":"subject-1","identity_keys":["identity-1"]}],"identities":[{"source_key":"identity-1","kind":"mp_openid","scope":"wechat-app:app","value":"opaque","source":"provider-history"}],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"order-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"identity-1","payer_subject_key":"subject-1","beneficiary_subject_key":"subject-1","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"product-1","product_name":"Product 1","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}],"refunds":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	second := manifest.Orders[0]
	second.Provider, second.SourceKey, second.ProviderTransactionNo = "wechat_shop", "order-2", "transaction-2"
	manifest.Orders = append(manifest.Orders, second)
	if err = manifest.Validate(true); err == nil {
		t.Fatal("cross-provider merchant receipt collision was accepted")
	}
	manifest.Orders[1].MerchantOrderNo = "merchant-2"
	manifest.Orders[0].Status, manifest.Orders[1].Status = "partially_refunded", "partially_refunded"
	manifest.Refunds = []RefundRow{
		{Provider: "wechat_pay", SourceKey: "refund-1", MerchantOrderNo: "merchant-1", RefundNo: "refund-1", AmountMinor: 40, Reason: "历史退款", OccurredAt: manifest.Orders[0].UpdatedAt},
		{Provider: "wechat_shop", SourceKey: "refund-2", MerchantOrderNo: "merchant-2", RefundNo: "refund-1", AmountMinor: 40, Reason: "历史退款", OccurredAt: manifest.Orders[1].UpdatedAt},
	}
	if err = manifest.Validate(true); err == nil {
		t.Fatal("cross-provider refund receipt collision was accepted")
	}
}

func TestHistoricalRefundStatusesPreserveLegacyDigestAndCompletedTotals(t *testing.T) {
	m, err := Parse([]byte(orderOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	m.Subjects = []SubjectRow{{SourceKey: "s", IdentityKeys: []string{"i"}}}
	m.Identities = []IdentityRow{{SourceKey: "i", Kind: "mp_openid", Scope: "wechat-app:app", Value: "opaque", Source: "provider-history"}}
	m.Orders[0].PayerIdentityKey = "i"
	m.Orders[0].PayerSubjectKey = "s"
	m.Orders[0].BeneficiarySubjectKey = "s"
	row := RefundRow{Provider: "wechat_pay", SourceKey: "r", MerchantOrderNo: "merchant-1", RefundNo: "r", AmountMinor: 40, Reason: "history", OccurredAt: m.Orders[0].UpdatedAt}
	oldBytes, _ := json.Marshal(row)
	if strings.Contains(string(oldBytes), `"status"`) {
		t.Fatal("legacy empty-status digest changed")
	}
	for _, state := range []string{"failed", "closed", "PROCESSING", "requested"} {
		row.Status = state
		m.Refunds = []RefundRow{row}
		if err := m.Validate(false); err != nil {
			t.Fatalf("%s: %v", state, err)
		}
		if m.Summary().RefundMinor != 0 || row.HistoricalStatus() == "completed" {
			t.Fatalf("%s counted completed", state)
		}
		if HistoricalRefundDigest(row) == sha256.Sum256(oldBytes) {
			t.Fatal("source state omitted from frozen digest")
		}
	}
	row.Status = "SUCCESS"
	m.Refunds = []RefundRow{row}
	if err := m.Validate(false); err == nil {
		t.Fatal("paid order accepted completed refund without status update")
	}
	m.Orders[0].Status = "partially_refunded"
	if err := m.Validate(false); err != nil || m.Summary().RefundMinor != 40 {
		t.Fatalf("completed totals: %v", err)
	}
	row.Status = "unexpected"
	m.Refunds = []RefundRow{row}
	if err := m.Validate(false); err == nil {
		t.Fatal("unknown refund source status accepted")
	}
}

func TestHistoricalUnassignedRefundAndPayerOnlyHaveNoInventedBeneficiary(t *testing.T) {
	m, err := Parse([]byte(orderOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	m.Refunds = []RefundRow{{Provider: "wechat_pay", SourceKey: "refund-failed", MerchantOrderNo: m.Orders[0].MerchantOrderNo, RefundNo: "failed", AmountMinor: 40, Reason: "historical", Status: "failed", OccurredAt: m.Orders[0].UpdatedAt}}
	if err = m.Validate(false); err != nil {
		t.Fatal("unassigned failed refund rejected", err)
	}
	if m.Summary().PaymentRows != 1 || m.Summary().RefundMinor != 0 {
		t.Fatal("money evidence miscounted")
	}
	m.Subjects = []SubjectRow{{SourceKey: "s", IdentityKeys: []string{"i"}}}
	m.Identities = []IdentityRow{{SourceKey: "i", Kind: "mp_openid", Scope: "wechat-app:a", Value: "opaque", Source: "provider-history"}}
	m.Orders[0].PayerSubjectKey = "s"
	m.Orders[0].PayerIdentityKey = "i"
	if err = m.Validate(false); err != nil {
		t.Fatal("payer-only rejected", err)
	}
	m.Orders[0].BeneficiarySubjectKey = "unknown"
	if err = m.Validate(false); err == nil {
		t.Fatal("unknown beneficiary accepted")
	}
}
