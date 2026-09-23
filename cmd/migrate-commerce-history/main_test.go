package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	ordermigration "github.com/qianlan33333-png/AI-CRM-v3/internal/order/migration"
)

func TestInspectAllowsIncompleteButDryRunFailsClosed(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "snapshot.json")
	raw := `{"schema_version":"aicrm-commerce-history-v3","run_key":"test-run","coverage":{"identities":true},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=inspect", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=dry-run", "--snapshot=" + path}); err == nil {
		t.Fatal("dry-run accepted incomplete source coverage")
	}
}

func TestOrderOnlyDryRunAcceptsOnlyFloatingWeChatPaySnapshot(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "orders.json")
	raw := `{"schema_version":"aicrm-commerce-history-v3","run_key":"orders-only-test","coverage":{"wechat_pay_orders":true},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"source-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"","payer_subject_key":"","beneficiary_subject_key":"","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"product-1","product_name":"Product 1","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--order-only", "--mode=dry-run", "--snapshot=" + path}); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--mode=dry-run", "--snapshot=" + path}); err == nil {
		t.Fatal("full-commerce dry-run accepted order-only source coverage")
	}
}

func TestOrderOnlyRejectsIdentityCoverage(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "orders.json")
	raw := `{"schema_version":"aicrm-commerce-history-v3","run_key":"orders-only-test","coverage":{"identities":true,"wechat_pay_orders":true},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"source-1","merchant_order_no":"merchant-1","provider_transaction_no":"","payer_identity_key":"","payer_subject_key":"","beneficiary_subject_key":"","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"product-1","product_name":"Product 1","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}],"refunds":[]}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"--order-only", "--mode=inspect", "--snapshot=" + path}); err == nil {
		t.Fatal("order-only mode accepted identity coverage")
	}
}

func TestPaymentHistoryFactsComposeOnlyVerifiedOwnerResults(t *testing.T) {
	manifest := ordermigration.Manifest{
		Orders: []ordermigration.OrderRow{
			{Provider: "wechat_pay", SourceKey: "paid", MerchantOrderNo: "merchant-paid", ProviderTransactionNo: "transaction-paid", PayerIdentityKey: "identity-1", PayerSubjectKey: "subject-1", BeneficiarySubjectKey: "subject-1", AmountMinor: 100, Currency: "CNY", Status: "paid", Items: []ordermigration.ItemRow{{LineNo: 1, ProductCode: "p", ProductName: "P", UnitAmountMinor: 100, Quantity: 1, LineAmountMinor: 100}}, CreatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)},
			{Provider: "wechat_pay", SourceKey: "pending", MerchantOrderNo: "merchant-pending", PayerIdentityKey: "identity-1", PayerSubjectKey: "subject-1", BeneficiarySubjectKey: "subject-1", AmountMinor: 200, Currency: "CNY", Status: "pending_payment", Items: []ordermigration.ItemRow{{LineNo: 1, ProductCode: "p2", ProductName: "P2", UnitAmountMinor: 200, Quantity: 1, LineAmountMinor: 200}}, CreatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)},
			{Provider: "alipay", SourceKey: "alipay", MerchantOrderNo: "merchant-alipay", PayerIdentityKey: "identity-1", PayerSubjectKey: "subject-1", BeneficiarySubjectKey: "subject-1", AmountMinor: 300, Currency: "CNY", Status: "paid", Items: []ordermigration.ItemRow{{LineNo: 1, ProductCode: "p3", ProductName: "P3", UnitAmountMinor: 300, Quantity: 1, LineAmountMinor: 300}}, CreatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)},
		},
		Refunds: []ordermigration.RefundRow{{Provider: "wechat_pay", SourceKey: "refund", MerchantOrderNo: "merchant-paid", RefundNo: "refund-paid", AmountMinor: 40, Reason: "历史退款", OccurredAt: time.Date(2026, 9, 5, 1, 0, 0, 0, time.UTC)}},
	}
	orderIDs := map[string]int64{
		ordermigration.HistoricalMerchantKey("wechat_pay", "merchant-paid"):    11,
		ordermigration.HistoricalMerchantKey("wechat_pay", "merchant-pending"): 12,
		ordermigration.HistoricalMerchantKey("alipay", "merchant-alipay"):      13,
	}
	all, payments, refunds, err := paymentHistoryFacts(manifest, orderIDs, map[string]int64{"subject-1": 7}, map[string]historyIdentityResolution{"identity-1": {CustomerID: 7, IdentityID: 9}})
	if err != nil || len(all) != 3 || len(payments) != 1 || len(refunds) != 1 || payments[0].OrderID != 11 || payments[0].PayerIdentityID != 9 || refunds[0].OrderID != 11 {
		t.Fatalf("all=%v payments=%+v refunds=%+v err=%v", all, payments, refunds, err)
	}
	if _, _, _, err = paymentHistoryFacts(manifest, orderIDs, map[string]int64{"subject-1": 7}, map[string]historyIdentityResolution{}); !errors.Is(err, ordermigration.ErrReconciliationMismatch) {
		t.Fatalf("unresolved payment identity err=%v", err)
	}
}

func TestIdentityReceiptExpectationsBindFrozenSubjectRowsAndResolvedRoots(t *testing.T) {
	manifest := ordermigration.Manifest{
		Identities:          []ordermigration.IdentityRow{{SourceKey: "identity-1", Kind: "mp_openid", Scope: "wechat-app:app", Value: "opaque", Source: "provider-history:commerce"}},
		Subjects:            []ordermigration.SubjectRow{{SourceKey: "subject-1", IdentityKeys: []string{"identity-1"}}},
		IdentityQuarantines: []ordermigration.IdentityQuarantineRow{{SourceKey: "quarantine-1", ReasonCode: "scope_missing", EvidenceDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}},
	}
	subjects, quarantines, err := identityReceiptExpectations(manifest, map[string]int64{"subject-1": 9})
	if err != nil || len(subjects) != 1 || len(quarantines) != 1 || subjects[0].CustomerID != 9 || subjects[0].IdentityCount != 1 {
		t.Fatalf("subjects=%+v quarantines=%+v err=%v", subjects, quarantines, err)
	}
	raw, err := json.Marshal(struct {
		Subject ordermigration.SubjectRow    `json:"subject"`
		Rows    []ordermigration.IdentityRow `json:"identities"`
	}{Subject: manifest.Subjects[0], Rows: manifest.Identities})
	if err != nil || subjects[0].SourceDigest != sha256.Sum256(raw) {
		t.Fatalf("subject digest=%x expected=%x err=%v", subjects[0].SourceDigest, sha256.Sum256(raw), err)
	}
	if _, _, err = identityReceiptExpectations(manifest, map[string]int64{}); !errors.Is(err, ordermigration.ErrReconciliationMismatch) {
		t.Fatalf("missing resolved root err=%v", err)
	}
}
