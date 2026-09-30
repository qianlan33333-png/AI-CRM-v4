package app

import (
	"context"
	"encoding/json"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestAlipayPolicyDefaultAndValidation(t *testing.T) {
	for _, tc := range []struct {
		raw               string
		disabled, invalid bool
	}{
		{`{"schema_version":1}`, false, false}, {`{"schema_version":1,"alipay_enabled":true}`, false, false},
		{`{"schema_version":1,"alipay_enabled":false}`, true, false}, {`{"schema_version":1,"alipay_enabled":null}`, false, true},
		{`{"schema_version":1,"alipay_enabled":"false"}`, false, true},
	} {
		canonical, err := CanonicalLegacyAdminProjection(json.RawMessage(tc.raw))
		if (err != nil) != tc.invalid {
			t.Fatalf("%s validation=%v", tc.raw, err)
		}
		if tc.invalid {
			continue
		}
		disabled, err := ProductAlipayDisabled(canonical)
		if err != nil || disabled != tc.disabled {
			t.Fatalf("%s disabled=%v err=%v", tc.raw, disabled, err)
		}
	}
}

func TestOrdinaryAlipayPolicySaveReadAndOmittedUpdate(t *testing.T) {
	store := &productTestStore{}
	svc := NewService(&productTestUoW{}, store, &productTestEvents{})
	ctx := context.Background()
	item, err := svc.Create(ctx, productport.CreateCommand{ProductCode: "alipay-policy", Name: "支付设置", PriceMinor: 990, Currency: "CNY", Actor: 7, IdempotencyKey: "alipay-create-policy", LegacyAdminProjection: json.RawMessage(`{"schema_version":1,"alipay_enabled":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range []string{`{"schema_version":1}`, `{"schema_version":1,"alipay_enabled":true}`} {
		item, err = svc.Update(ctx, productport.UpdateCommand{ID: item.ID, ExpectedVersion: item.Version, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, Actor: 7, IdempotencyKey: []string{"alipay-other-dimension", "alipay-enable-policy"}[i], LegacyAdminProjection: json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		read, err := svc.Get(ctx, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		disabled, err := ProductAlipayDisabled(read.LegacyAdminProjection)
		if err != nil || disabled != (i == 0) {
			t.Fatalf("step %d readback disabled=%v err=%v", i, disabled, err)
		}
	}
}

func TestPeriodAlipayPolicySaveReadCheckoutAndOmittedUpdate(t *testing.T) {
	svc, _, _ := newServicePeriodFixture()
	ctx := context.Background()
	item, err := svc.CreateServicePeriodProduct(ctx, productport.CreateServicePeriodProductCommand{ProductCode: "alipay-period", Name: "周期支付设置", PriceMinor: 990, Currency: "CNY", DurationDays: 30, Actor: 7, IdempotencyKey: "alipay-period-create", AdminProjection: json.RawMessage(`{"schema_version":1,"alipay_enabled":false}`)})
	if err != nil {
		t.Fatal(err)
	}
	item, err = svc.SetServicePeriodProductEnabled(ctx, productport.SetServicePeriodProductEnabledCommand{ID: item.ServiceProductID, ExpectedVersion: item.Version, Enabled: true, Actor: 7, IdempotencyKey: "alipay-period-enable"})
	if err != nil {
		t.Fatal(err)
	}
	for i, raw := range []string{`{"schema_version":1}`, `{"schema_version":1,"alipay_enabled":true}`} {
		item, err = svc.UpdateServicePeriodProduct(ctx, productport.UpdateServicePeriodProductCommand{ID: item.ServiceProductID, ExpectedVersion: item.Version, Name: item.Name, PriceMinor: item.PriceMinor, Currency: item.Currency, DurationDays: item.DurationDays, Actor: 7, IdempotencyKey: []string{"alipay-period-other", "alipay-period-allow"}[i], AdminProjection: json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		read, err := svc.GetServicePeriodProduct(ctx, item.ServiceProductID)
		if err != nil {
			t.Fatal(err)
		}
		disabled, err := ProductAlipayDisabled(read.AdminProjection)
		if err != nil || disabled != (i == 0) {
			t.Fatalf("step %d disabled=%v err=%v", i, disabled, err)
		}
		checkout, err := svc.ReadPublicServicePeriodByCode(ctx, item.ProductCode)
		if err != nil || checkout.AlipayDisabled != disabled {
			t.Fatalf("checkout=%+v err=%v", checkout, err)
		}
	}
}
