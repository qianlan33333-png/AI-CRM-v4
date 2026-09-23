package coupon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCouponUIBindingExtractsFrozenTemplateAndVerifiedAssets(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tokens.css", "labs.css", "admin.js", "coupon-host.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js","couponHost":"assets/coupon-host.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"coupons", "couponForm", "couponData"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(`<html><template id="tpl"><section data-page="`+page+`">frozen</section></template></html>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var gotPage, gotBody string
	h := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, body string, assets Assets) error {
		gotPage, gotBody = page, body
		if assets.AdminJS != "/assets/admin.js" || assets.HostJS != "/assets/coupon-host.js" {
			t.Fatalf("admin asset=%q", assets.AdminJS)
		}
		w.WriteHeader(200)
		return nil
	})
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/admin/coupons", nil))
	if r.Code != 200 || gotPage != "coupons" || gotBody != `<section data-page="coupons">frozen</section>` {
		t.Fatalf("code=%d page=%q body=%q", r.Code, gotPage, gotBody)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/admin/couponForm.html?id=12", nil))
	if r.Code != 200 || gotPage != "couponForm" {
		t.Fatalf("form code=%d page=%q", r.Code, gotPage)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/admin/coupons/12/edit", nil))
	if r.Code != 200 || gotPage != "couponForm" {
		t.Fatalf("standard edit redirect destination code=%d page=%q", r.Code, gotPage)
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/admin/couponData.html?id=12", nil))
	if r.Code != 200 || gotPage != "couponData" {
		t.Fatalf("data code=%d page=%q", r.Code, gotPage)
	}
}

func TestCouponUIBindingFailsClosedOnNonDonorRoutesAndQueries(t *testing.T) {
	h := NewModuleRegistration().UIBinding(t.TempDir(), func(http.ResponseWriter, *http.Request, string, string, Assets) error { return nil })
	for _, path := range []string{"/admin/coupons/new", "/admin/coupons/01/edit", "/admin/coupons/1/edit?x=1", "/admin/coupons?id=1", "/admin/couponForm.html?id=01", "/admin/couponForm.html?id=1&x=1", "/admin/couponData.html", "/admin/couponData.html?id=01", "/admin/couponData.html?id=1&x=1"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, path, nil))
		if r.Code != 404 {
			t.Fatalf("%s: got %d", path, r.Code)
		}
	}
}
