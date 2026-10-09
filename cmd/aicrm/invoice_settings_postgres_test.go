package main

import (
	"context"
	"encoding/json"
	ca "github.com/qianlan33333-png/AI-CRM-v3/internal/config/app"
	cp "github.com/qianlan33333-png/AI-CRM-v3/internal/config/port"
	cs "github.com/qianlan33333-png/AI-CRM-v3/internal/config/store"
	invoice "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice"
	cfg "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInvoiceSettingsPostgreSQLPublishAndCompositionReadback(t *testing.T) {
	f := newInvoiceFixture(t)
	ctx := context.Background()
	runtime := cfg.NormalizeRuntimePolicyDefaults(cfg.Runtime{})
	runtime.Invoice = cfg.Invoice{Enabled: true, ServiceMerchantID: "service", MerchantSerial: "serial", SellerName: "销售方", SellerTaxID: "SELLER-TAX", BillingPersonID: "开票人", DefaultTaxCode: "3040203000000000000", DefaultTaxRate: 100, DefaultGoodsName: "服务费", MaterialDir: filepath.Join(t.TempDir(), "keys"), LogoURL: "https://mmbiz.qpic.cn/mmbiz_png/fixture/0", CardConfirmed: true}
	runtime.PublicOrigin = "https://crm.test"
	runtime.WeChatPay.Enabled = true
	runtime.WeChatPay.H5OAuthEnabled = true
	runtime.WeChatPay.AppID = "wx-fixture"
	runtime.WeChatPay.H5AppID = "wx-fixture"
	runtime.WeChatPay.H5AppScope = "wechat-app:wx-fixture"
	runtime.WeChatPay.MerchantID = f.s.Config.MerchantID
	runtime.WeChatPay.MerchantSerial = "serial"
	runtime.Invoice.TaxConfigPath = filepath.Join(t.TempDir(), "tax.json")
	if e := os.WriteFile(runtime.Invoice.TaxConfigPath, []byte(`{"course":{"code":"3010101020203000000","rate":600,"goods_name":"课程"}}`), 0600); e != nil {
		t.Fatal(e)
	}
	id, e := (ca.InvoiceKeyFiles{Directory: runtime.Invoice.MaterialDir}).StageInvoiceKey(ctx, strings.Repeat("A", 32))
	if e != nil {
		t.Fatal(e)
	}
	defaults, e := runtimeConfigDefaults(runtime)
	if e != nil {
		t.Fatal(e)
	}
	repo, e := cs.NewPostgreSQL(f.pool, f.s.UoW)
	if e != nil {
		t.Fatal(e)
	}
	guards := ca.RuntimeActivationGuards{InvoiceSigningReady: true, InvoiceKeyConfigured: true, WeChatPayEnabled: true, WeChatPayH5OAuthEnabled: true, InvoiceKeyReferenceExists: func(ref string) bool {
		_, e := (ca.InvoiceKeyFiles{Directory: runtime.Invoice.MaterialDir}).Read(ref)
		return e == nil
	}}
	releases, e := ca.NewRuntimeReleaseService(f.s.UoW, repo, repo, 1, ca.WithRuntimeDefaults(defaults), ca.WithRuntimeActivationGuards(guards))
	if e != nil {
		t.Fatal(e)
	}
	draft, e := releases.CreateRuntimeReleaseDraft(ctx, cp.RuntimeReleaseDraftCommand{Actor: "7", IdempotencyKey: "invoice-settings-save-1", Settings: []cp.RuntimeSetting{runtimeApplySetting(t, cp.InvoiceKeyFileID, id), runtimeApplySetting(t, cp.InvoiceDefaultGoodsName, "已发布服务费")}})
	if e != nil {
		t.Fatal(e)
	}
	validated, e := releases.ValidateRuntimeRelease(ctx, cp.RuntimeReleaseMutationCommand{ReleaseID: draft.ID, Actor: "7", IdempotencyKey: "invoice-settings-validate-1"})
	if e != nil || validated.State != cp.RuntimeReleaseValidated {
		t.Fatalf("finance config invalid: %v %+v", e, validated.ValidationErrors)
	}
	if _, e = releases.PublishRuntimeRelease(ctx, cp.RuntimeReleasePublishCommand{ReleaseID: draft.ID, ExpectedChecksum: validated.Checksum, Actor: "7", IdempotencyKey: "invoice-settings-publish-1"}); e != nil {
		t.Fatal(e)
	}
	snapshot, e := releases.EffectiveSnapshot(ctx)
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(snapshot)
	if strings.Contains(string(raw), strings.Repeat("A", 32)) || strings.Contains(string(raw), runtime.Invoice.MaterialDir) {
		t.Fatal("private material persisted in public snapshot")
	}
	applied, e := applyRuntimeConfig(runtime, snapshot)
	if e != nil || applied.Invoice.APIV3Key != strings.Repeat("A", 32) {
		t.Fatal("new process did not read published private reference")
	}
	consumer, _, e := composeInvoice(f.s.UoW, f.s.Sessions, f.s.Lineage.(invoiceIdentity), f.s.Orders, f.s.Refunds, f.effects, f.s.Jobs.(invoice.QueryEnqueuer).Client, applied)
	if e != nil {
		t.Fatal(e)
	}
	if consumer.Config.Taxes["*"].GoodsName != "已发布服务费" || consumer.Config.Taxes["course"].Rate != 600 {
		t.Fatal("default did not apply or product override was overwritten")
	}
	if e = releases.RecordRuntimeApplication(ctx, cp.RuntimeApplication{Revision: snapshot.Revision, Source: snapshot.Source, Role: "api", ReleaseSHA: "invoice-settings-fixture", SnapshotChecksum: snapshot.Checksum, AppliedAt: time.Now().UTC()}); e != nil {
		t.Fatal(e)
	}
	facts, e := releases.ListRuntimeApplications(ctx, 10)
	if e != nil || len(facts) != 1 || facts[0].SnapshotChecksum != snapshot.Checksum {
		t.Fatal("actual application fact missing")
	}
}
