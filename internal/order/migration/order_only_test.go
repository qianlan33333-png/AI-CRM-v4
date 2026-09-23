package migration

import (
	"context"
	"testing"

	orderdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/order/domain"
	orderport "github.com/qianlan33333-png/AI-CRM-v3/internal/order/port"
)

const orderOnlyJSON = `{"schema_version":"aicrm-commerce-history-v3","run_key":"order-snapshot-test-0001","coverage":{"identities":false,"wechat_pay_orders":true,"wechat_pay_refunds":false,"wechat_shop_orders":false,"wechat_shop_refunds":false,"alipay_orders":false},"subjects":[],"identities":[],"identity_quarantines":[],"orders":[{"provider":"wechat_pay","source_key":"legacy-1","merchant_order_no":"merchant-1","provider_transaction_no":"transaction-1","payer_identity_key":"","payer_subject_key":"","beneficiary_subject_key":"","amount_minor":100,"currency":"CNY","status":"paid","items":[{"line_no":1,"product_code":"legacy","product_name":"历史交易","unit_amount_minor":100,"quantity":1,"line_amount_minor":100}],"created_at":"2026-09-01T00:00:00Z","updated_at":"2026-09-01T00:00:00Z"}],"refunds":[]}`

type orderOnlyImporterStub struct {
	command orderport.HistoricalImportCommand
}

func (stub *orderOnlyImporterStub) ImportHistorical(_ context.Context, command orderport.HistoricalImportCommand) (orderdomain.Snapshot, error) {
	stub.command = command
	return command.Order, nil
}

type runStoreStub struct{ began, completed int64 }

func (stub *runStoreStub) Begin(_ context.Context, _ string, _ [32]byte, input int64) error {
	stub.began = input
	return nil
}
func (stub *runStoreStub) CompleteOrders(_ context.Context, _ string, imported int64) error {
	stub.completed = imported
	return nil
}

func TestOrderOnlyRunnerKeepsFloatingHistoryIneligibleForEffects(t *testing.T) {
	manifest, err := Parse([]byte(orderOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	orders, runs := &orderOnlyImporterStub{}, &runStoreStub{}
	result, err := (OrderOnlyRunner{Orders: orders, Runs: runs}).Apply(context.Background(), manifest)
	if err != nil {
		t.Fatal(err)
	}
	got := orders.command.Order
	if result.Orders != 1 || runs.began != 1 || runs.completed != 1 || got.RecordOrigin != orderdomain.RecordOriginHistory || got.EffectEligible || got.PayerCustomerID != nil || got.BeneficiaryCustomerID != nil {
		t.Fatalf("unsafe order-only result=%+v run=%+v order=%+v", result, runs, got)
	}
}

func TestOrderOnlyRejectsIdentityAndProviderExpansion(t *testing.T) {
	manifest, err := Parse([]byte(orderOnlyJSON))
	if err != nil {
		t.Fatal(err)
	}
	manifest.Coverage.Identities = true
	if err = ValidateOrderOnly(manifest); err == nil {
		t.Fatal("identity coverage was accepted by order-only migration")
	}
	manifest.Coverage.Identities = false
	manifest.Orders[0].Provider = orderdomain.ProviderAlipay
	if err = ValidateOrderOnly(manifest); err == nil {
		t.Fatal("non-WeChat provider was accepted by order-only migration")
	}
}

func TestParseRejectsUnknownOrTrailingJSON(t *testing.T) {
	if _, err := Parse([]byte(orderOnlyJSON + `{}`)); err == nil {
		t.Fatal("trailing JSON was accepted")
	}
}
