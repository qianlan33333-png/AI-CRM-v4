package http

import (
	"bytes"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPublicZeroAmountCheckoutRecoveryJourney(t *testing.T) {
	preparePublicBrowserDependencies(t)
	_, source, _, _ := runtime.Caller(0)
	pages := []string{}
	for _, amount := range []int64{0, 990} {
		var html bytes.Buffer
		if err := publicProductPage.Execute(&html, map[string]any{"Payment": true, "Detail": false, "ResumeOrderID": 0, "WeChatPayEnabled": false, "AlipayEnabled": false, "Product": publicProduct{ID: 7, Name: "领取商品", PriceMinor: amount, ProductKind: "standard", CouponTargetRef: "standard_product:7", ContactCollectionLevel: "none", RegionOptionsJSON: template.JS("[]")}}); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "page.html")
		if err := os.WriteFile(path, html.Bytes(), 0600); err != nil {
			t.Fatal(err)
		}
		pages = append(pages, path)
	}
	command := exec.Command("node", append([]string{filepath.Join(filepath.Dir(source), "zero_amount_journey.mjs")}, pages...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("zero browser journey: %v\n%s", err, output)
	}
}
