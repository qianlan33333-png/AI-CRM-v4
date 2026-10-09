package http

import (
	"bytes"
	"html/template"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestPublicOriginalCheckoutBrowserJourney(t *testing.T) {
	preparePublicBrowserDependencies(t)
	for _, kind := range []string{"standard", "service_period"} {
		t.Run(kind, func(t *testing.T) {
			var html bytes.Buffer
			view := publicProductPageView{ResumeOrderID: 7, Payment: true, WeChatPayEnabled: true, AlipayEnabled: true, Product: publicProduct{ID: 7, Name: "目录新名称", PriceMinor: 19900, ProductKind: kind, ContactCollectionLevel: "none", RegionOptionsJSON: template.JS("[]")}}
			if e := publicProductPage.Execute(&html, view); e != nil {
				t.Fatal(e)
			}
			if dir := os.Getenv("AICRM_TEST_UI_ARTIFACT_DIR"); dir != "" {
				if e := os.WriteFile(filepath.Join(dir, "resume-"+kind+".html"), html.Bytes(), 0600); e != nil {
					t.Fatal(e)
				}
			}
			path := filepath.Join(t.TempDir(), "resume.html")
			if e := os.WriteFile(path, html.Bytes(), 0600); e != nil {
				t.Fatal(e)
			}
			out, e := exec.Command("node", "public_resume_checkout_journey.mjs", path, kind).CombinedOutput()
			if e != nil {
				t.Fatalf("%v\n%s", e, out)
			}
			t.Log(string(out))
		})
	}
}

func TestPublicOriginalCheckoutQueryCannotBecomeAttribution(t *testing.T) {
	for _, v := range []string{"resume_order=7", "resume_order=9223372036854775807"} {
		_, id, ok := publicPaymentContext(httptest.NewRequest("GET", "/pay/course?"+v, nil), true)
		if !ok || id < 1 {
			t.Fatal(v)
		}
	}
	for _, v := range []string{"resume_order=0", "resume_order=01", "resume_order=%37", "resume_order=7&resume_order=8", "resume_order=7&promotion_context=dpc_test", "resume_order=9223372036854775808", "resume_order=7&next=/admin"} {
		_, _, ok := publicPaymentContext(httptest.NewRequest("GET", "/pay/course?"+v, nil), true)
		if ok {
			t.Fatal("unsafe query", v)
		}
	}
	if _, _, ok := publicPaymentContext(httptest.NewRequest("GET", "/p/course?resume_order=7", nil), false); ok {
		t.Fatal("resume on introduction page")
	}
}
