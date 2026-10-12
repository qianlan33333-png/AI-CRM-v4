package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublicContactPrefillThreeModes(t *testing.T) {
	pages := map[string]string{}
	for _, level := range []string{"none", "mobile", "shipping_address"} {
		p := enabledPublicProduct(7, "contact-course")
		p.LegacyAdminProjection = json.RawMessage(fmt.Sprintf(`{"schema_version":1,"status":"active","enabled":true,"contact_collection_level":%q,"slices":[]}`, level))
		h, e := NewPublicHandler(&testCatalog{product: p})
		if e != nil {
			t.Fatal(e)
		}
		h.SetPaymentMethods(true, true)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/pay/contact-course", nil))
		if w.Code != 200 {
			t.Fatal(w.Code)
		}
		pages[level] = w.Body.String()
		if dir := os.Getenv("AICRM_CONTACT_QA_DIR"); dir != "" {
			if e = os.MkdirAll(dir, 0700); e != nil {
				t.Fatal(e)
			}
			visual := bytes.Buffer{}
			visualProduct := publicProduct{ID: 7, Name: "课程", PriceMinor: 99900, ProductKind: "standard", Currency: "CNY", ContactCollectionLevel: level, RequireMobile: level != "none", CouponTargetRef: "standard_product:7", RegionOptionsJSON: template.JS(`[{"c":"11","n":"北京市","ch":[{"c":"1101","n":"北京市","ch":[{"c":"110101","n":"东城区"}]}]}]`), VariantsEnabled: true, Variants: []productport.Variant{{ID: 21, Version: 1, Name: "单人版", PriceMinor: 9900}, {ID: 22, Version: 1, Name: "三人成行版", PriceMinor: 99900, Description: "一份订单独立定价", Attributes: []productport.VariantAttribute{{Key: "版本", Value: "B"}}}}}
			if e = publicProductPage.Execute(&visual, publicProductPageView{Payment: true, Product: visualProduct, WeChatPayEnabled: true, AlipayEnabled: true}); e != nil {
				t.Fatal(e)
			}
			if e = os.WriteFile(filepath.Join(dir, level+".html"), visual.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	raw, _ := json.Marshal(pages)
	cmd := exec.Command("node", "testdata/contact_prefill.mjs")
	cmd.Stdin = bytes.NewReader(raw)
	if output, e := cmd.CombinedOutput(); e != nil {
		t.Fatalf("prefill journey: %v\n%s", e, output)
	}
}
