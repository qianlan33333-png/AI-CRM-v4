package http

import (
	"bytes"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPublicVariantSelectionCouponAndRecovery(t *testing.T) {
	preparePublicBrowserDependencies(t)
	dir := t.TempDir()
	for _, mode := range []string{"multi", "single", "empty", "off"} {
		p := publicProduct{ID: 7, Name: "课程", PriceMinor: 9900, ProductKind: "standard", Currency: "CNY", ContactCollectionLevel: "none", CouponTargetRef: "standard_product:7", RegionOptionsJSON: template.JS("[]"), VariantsEnabled: mode != "off", Variants: []productport.Variant{{ID: 21, Version: 1, Name: "单人版", PriceMinor: 9900}, {ID: 22, Version: 3, Name: "三人成行版", PriceMinor: 99900, Description: "一份订单独立定价", Attributes: []productport.VariantAttribute{{Key: "版本", Value: "B"}}}}}
		if mode == "single" {
			p.Variants = p.Variants[:1]
		}
		if mode == "empty" || mode == "off" {
			p.Variants = nil
		}
		var html bytes.Buffer
		if err := publicProductPage.Execute(&html, publicProductPageView{Payment: true, Product: p, WeChatPayEnabled: true, AlipayEnabled: true}); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, mode+".html"), html.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		if target := os.Getenv("AICRM_VARIANT_VISUAL_DIR"); target != "" {
			if err := os.MkdirAll(target, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, mode+".html"), html.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	_, source, _, _ := runtime.Caller(0)
	if output, err := exec.Command("node", filepath.Join(filepath.Dir(source), "public_variants_journey.mjs"), dir).CombinedOutput(); err != nil {
		t.Fatalf("variant browser: %v\n%s", err, output)
	}
}
