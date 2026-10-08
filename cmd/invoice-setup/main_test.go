package main

import (
	"encoding/json"
	provider "github.com/qianlan33333-png/AI-CRM-v3/internal/invoice/provider"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInvoiceSetupPrivateConfigAndExactLocations(t *testing.T) {
	c := setupConfig{ServiceMerchantID: "123", SubMerchantID: "456", MerchantCertificatePath: "/private/cert.pem", PrivateKeyPath: "/private/key.pem", AuthMode: "public_key", PublicKeyID: "PUB_KEY_ID_fixture", PublicKeyPath: "/private/public.pem", PublicOrigin: "https://crm.test", CardAppID: "wx-fixture", CardLogoURL: "https://mmbiz.qpic.cn/mmbiz/fixture/0"}
	path := filepath.Join(t.TempDir(), "setup.json")
	raw, _ := json.Marshal(c)
	os.WriteFile(path, raw, 0600)
	if _, e := readConfig(path); e != nil {
		t.Fatal(e)
	}
	patch := c.development()
	if patch.CallbackURL != "https://crm.test/api/callbacks/wechat-invoice" || patch.ShowInvoiceCell || patch.SupportVAT {
		t.Fatal("wrong callback or enabled unsupported entry")
	}
	card, ok := c.card()
	if !ok || card.Information.CustomCell.JumpURL != "https://crm.test/my/orders" || card.AppID != "wx-fixture" {
		t.Fatal("wrong card entry")
	}
	for _, origin := range []string{"http://crm.test", "https://user:password@crm.test", "https://crm.test/other", "https://crm.test?openid=fixture"} {
		bad := c
		bad.PublicOrigin = origin
		if bad.valid() {
			t.Fatal("invalid public origin accepted")
		}
	}
	c.CardLogoURL = "https://crm.test/logo.png"
	if _, ok := c.card(); ok {
		t.Fatal("non-official permanent-material URL accepted")
	}
	os.Chmod(path, 0644)
	if _, e := readConfig(path); e == nil {
		t.Fatal("public setup config accepted")
	}
	os.Chmod(path, 0600)
	os.WriteFile(path, append(raw, []byte(`{}`)...), 0600)
	if _, e := readConfig(path); e == nil {
		t.Fatal("trailing data accepted")
	}
}
func TestInvoiceSetupCurrentPermanentMaterialLogo(t *testing.T) {
	c := setupConfig{SubMerchantID: "456", CardAppID: "wx-fixture", PublicOrigin: "https://crm.test"}
	// add_material now returns mmecoa.qpic.cn; retain legacy material URLs.
	for _, logo := range []string{"https://mmecoa.qpic.cn/mmecoa_jpg/fixture/0", "http://mmbiz.qpic.cn/mmbiz/fixture/0"} {
		c.CardLogoURL = logo
		card, ok := c.card()
		if !ok || card.Information.LogoURL != logo || card.AppID != c.CardAppID || card.SubMerchant != c.SubMerchantID || card.Information.CustomCell.JumpURL != "https://crm.test/my/orders" {
			t.Fatal("official permanent logo or card binding lost")
		}
	}
	for _, logo := range []string{"https://wx.qlogo.cn/mmhead/fixture/0", "https://mmecoa.qpic.cn.example.com/logo", "https://mmecoa.qpic.cn@evil.test/logo", "https://user:password@mmecoa.qpic.cn/logo", "https://mmecoa.qpic.cn/logo#fragment", "file:///logo.jpg"} {
		c.CardLogoURL = logo
		if _, ok := c.card(); ok {
			t.Fatal("unsupported logo accepted")
		}
	}
}

func TestInvoiceSetupIndependentDevelopmentReadback(t *testing.T) {
	c := setupConfig{PublicOrigin: "https://crm.test"}
	service := provider.SetupReceipt{CapturedAt: time.Now(), SignatureVerified: true, Outcome: "confirmed", Body: json.RawMessage(`{"callback_url":"https://crm.test/api/callbacks/wechat-invoice"}`)}
	merchant := provider.SetupReceipt{SignatureVerified: true, Outcome: "confirmed", Body: json.RawMessage(`{"show_fapiao_cell":false,"support_vat_fapiao":false}`)}
	if !developmentMatches(c, service, merchant) {
		t.Fatal("matching readback rejected")
	}
	merchant.Body = json.RawMessage(`{"show_fapiao_cell":true,"support_vat_fapiao":false}`)
	if developmentMatches(c, service, merchant) {
		t.Fatal("unrequested payment-bill entry accepted")
	}
	c.ShowInvoiceCell = true
	if !c.development().ShowInvoiceCell || !developmentMatches(c, service, merchant) {
		t.Fatal("enabled native bill entry did not read back")
	}
	c.ShowInvoiceCell = false
	merchant.Body = json.RawMessage(`{"show_fapiao_cell":false,"support_vat_fapiao":false}`)
	missing := merchant
	missing.Body = json.RawMessage(`{}`)
	if developmentMatches(c, service, missing) {
		t.Fatal("missing configuration fields accepted")
	}
	service.SignatureVerified = false
	if developmentMatches(c, service, merchant) {
		t.Fatal("unverified configuration accepted")
	}
}
