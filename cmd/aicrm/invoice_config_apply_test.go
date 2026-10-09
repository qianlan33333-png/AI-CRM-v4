package main

import (
	"context"
	"encoding/json"
	app "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	cp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	cfg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInvoiceRuntimeConfigurationPrivateKeyAndDefaults(t *testing.T) {
	runtime := cfg.Runtime{}
	runtime.Invoice.MaterialDir = filepath.Join(t.TempDir(), "keys")
	id, e := (app.InvoiceKeyFiles{Directory: runtime.Invoice.MaterialDir}).StageInvoiceKey(context.Background(), strings.Repeat("A", 32))
	if e != nil {
		t.Fatal(e)
	}
	taxfile := filepath.Join(t.TempDir(), "tax.json")
	if e = os.WriteFile(taxfile, []byte(`{"*":{"code":"3040203000000000000","rate":100,"goods_name":"服务费"},"first":{"code":"3010101020203000000","rate":600,"goods_name":"课程"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	runtime.Invoice.TaxConfigPath = taxfile
	defaults, e := runtimeConfigDefaults(runtime)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, v := range defaults {
		if v.Key == cp.InvoiceDefaultTaxCode {
			found = string(v.Value) == `"3040203000000000000"`
		}
	}
	if !found {
		t.Fatal("existing default missing")
	}
	snapshot := cp.EffectiveSnapshot{Settings: []cp.RuntimeSetting{runtimeApplySetting(t, cp.InvoiceKeyFileID, id), runtimeApplySetting(t, cp.InvoiceDefaultGoodsName, "新服务费"), runtimeApplySetting(t, cp.InvoiceDefaultTaxRate, 100), runtimeApplySetting(t, cp.InvoiceLogoURL, "https://mmbiz.qpic.cn/mmbiz_png/fixture/0")}}
	applied, e := applyRuntimeConfig(runtime, snapshot)
	if e != nil || applied.Invoice.APIV3Key != strings.Repeat("A", 32) || applied.Invoice.DefaultGoodsName != "新服务费" {
		t.Fatal("configuration not applied")
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), applied.Invoice.APIV3Key) || strings.Contains(string(raw), runtime.Invoice.MaterialDir) {
		t.Fatal("secret leaked to snapshot")
	}
	if _, e = applyRuntimeConfig(runtime, cp.EffectiveSnapshot{Settings: []cp.RuntimeSetting{runtimeApplySetting(t, cp.InvoiceKeyFileID, strings.Repeat("f", 32))}}); e == nil {
		t.Fatal("missing private file accepted")
	}
}

func TestInvoiceRuntimeActivationRequiresFinanceAndCredentials(t *testing.T) {
	runtime := cfg.NormalizeRuntimePolicyDefaults(cfg.Runtime{})
	runtime.WeChatPay.AppID = "wx-fixture"
	runtime.WeChatPay.MerchantID = "merchant-fixture"
	runtime.WeChatPay.MerchantSerial = "serial"
	runtime.WeChatPay.H5AppID = "wx-fixture"
	runtime.WeChatPay.H5AppScope = "wechat-app:wx-fixture"
	runtime.WeChatPay.Enabled = true
	runtime.WeChatPay.H5OAuthEnabled = true
	runtime.Invoice = cfg.Invoice{Enabled: true, ServiceMerchantID: "service", MerchantSerial: "serial", SellerName: "销售方", SellerTaxID: "SELLER-TAX", BillingPersonID: "开票人", DefaultTaxCode: "3040203000000000000", DefaultTaxRate: 100, DefaultGoodsName: "服务费"}
	settings, e := runtimeConfigDefaults(runtime)
	if e != nil {
		t.Fatal(e)
	}
	guards := app.RuntimeActivationGuards{WeChatPayEnabled: true, WeChatPayH5OAuthEnabled: true, InvoiceSigningReady: true, InvoiceKeyConfigured: true}
	if e = app.ValidateEffectiveRuntimeSettings(settings, guards); e != nil {
		t.Fatal(e)
	}
	withoutDefault := append([]cp.RuntimeSetting(nil), settings...)
	for i, value := range withoutDefault {
		switch value.Key {
		case cp.InvoiceDefaultTaxCode, cp.InvoiceDefaultGoodsName:
			withoutDefault[i] = runtimeApplySetting(t, value.Key, "")
		case cp.InvoiceDefaultTaxRate:
			withoutDefault[i] = runtimeApplySetting(t, value.Key, 0)
		}
	}
	if e = app.ValidateEffectiveRuntimeSettings(withoutDefault, guards); e == nil {
		t.Fatal("enabled without any finance mapping")
	}
	guards.InvoiceTaxMappingReady = true
	if e = app.ValidateEffectiveRuntimeSettings(withoutDefault, guards); e != nil {
		t.Fatal("valid product taxes require an unnecessary default", e)
	}
	for _, partial := range []cp.RuntimeSetting{runtimeApplySetting(t, cp.InvoiceDefaultTaxCode, "3040203000000000000"), runtimeApplySetting(t, cp.InvoiceDefaultGoodsName, "服务费"), runtimeApplySetting(t, cp.InvoiceDefaultTaxRate, 100)} {
		bad := append([]cp.RuntimeSetting(nil), withoutDefault...)
		for i := range bad {
			if bad[i].Key == partial.Key {
				bad[i] = partial
			}
		}
		if e = app.ValidateEffectiveRuntimeSettings(bad, guards); e == nil {
			t.Fatal("partial default accepted despite product mapping", partial.Key)
		}
	}
	for _, tc := range []struct {
		key   cp.RuntimeSettingKey
		value any
	}{{cp.InvoiceDefaultTaxCode, "bad"}, {cp.InvoiceDefaultTaxRate, 123}, {cp.InvoiceSellerName, ""}, {cp.InvoiceDefaultGoodsName, strings.Repeat("a", 129)}, {cp.InvoiceLogoURL, "https://evil.test/logo"}, {cp.InvoiceKeyFileID, strings.Repeat("f", 32)}} {
		bad := append([]cp.RuntimeSetting(nil), settings...)
		for i := range bad {
			if bad[i].Key == tc.key {
				bad[i] = runtimeApplySetting(t, tc.key, tc.value)
			}
		}
		if e = app.ValidateEffectiveRuntimeSettings(bad, guards); e == nil {
			t.Fatalf("invalid invoice setting accepted: %s", tc.key)
		}
	}
	guards.InvoiceSigningReady = false
	if e = app.ValidateEffectiveRuntimeSettings(settings, guards); e == nil {
		t.Fatal("enabled without signing materials")
	}
}
