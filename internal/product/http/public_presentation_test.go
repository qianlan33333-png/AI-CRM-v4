package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	productport "github.com/qianlan33333-png/AI-CRM-v3/internal/product/port"
)

func TestPublicPresentationAssetsAreManifestBoundAndAnonymousSafe(t *testing.T) {
	assets, hostRelative := publicPresentationFixture(t)

	handler, err := NewPublicHandler(&testCatalog{product: enabledPublicProduct(7, "course-7")})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicPresentationAssets(assets); err != nil {
		t.Fatal(err)
	}

	page := httptest.NewRecorder()
	handler.ServeHTTP(page, httptest.NewRequest(http.MethodGet, "/pay/course-7", nil))
	if page.Code != http.StatusOK {
		t.Fatalf("page status=%d body=%s", page.Code, page.Body.String())
	}
	for _, marker := range []string{
		"data-v3-public-commerce",
		"data-public-commerce-route=\"payment\"",
		"href=\"/product-public-assets/publicCommerceStyles-ABCD1234.css\"",
		"src=\"/product-public-assets/publicCommerceHost-ABCD1234.js\"",
	} {
		if !strings.Contains(page.Body.String(), marker) {
			t.Fatalf("public page missing %q body=%s", marker, page.Body.String())
		}
	}
	for _, marker := range []string{"style-src 'self' 'unsafe-inline'", "script-src 'self' 'unsafe-inline'"} {
		if !strings.Contains(page.Header().Get("Content-Security-Policy"), marker) {
			t.Fatalf("public page CSP missing %q: %s", marker, page.Header().Get("Content-Security-Policy"))
		}
	}

	for _, path := range []string{
		"/product-public-assets/publicCommerceStyles-ABCD1234.css",
		"/product-public-assets/files/wechat-auth-ABCD1234.jpg",
		"/product-public-assets/" + strings.TrimPrefix(hostRelative, "assets/"),
		"/product-public-assets/chunks/publicCommerceRuntime-ABCD1234.js",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") || response.Header().Get("ETag") == "" {
			t.Fatalf("public asset path=%s status=%d headers=%v", path, response.Code, response.Header())
		}
		if strings.HasSuffix(path, ".jpg") && !strings.HasPrefix(response.Header().Get("Content-Type"), "image/jpeg") {
			t.Fatalf("public JPEG type=%q", response.Header().Get("Content-Type"))
		}
	}
	for _, path := range []string{
		"/assets/publicCommerceHost-ABCD1234.js",
		"/product-public-assets/admin-ABCD1234.js",
		"/product-public-assets/../asset-manifest.json",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("non-public asset path=%s status=%d", path, response.Code)
		}
	}

	if err = os.WriteFile(filepath.Join(assets.handler.dist, hostRelative), []byte("altered"), 0o600); err != nil {
		t.Fatal(err)
	}
	altered := httptest.NewRecorder()
	handler.ServeHTTP(altered, httptest.NewRequest(http.MethodGet, "/product-public-assets/"+strings.TrimPrefix(hostRelative, "assets/"), nil))
	if altered.Code != http.StatusNotFound {
		t.Fatalf("altered asset status=%d", altered.Code)
	}
}

func TestDeferredPublicPresentationDefersCompositionButFailsClosedOnPublicRoute(t *testing.T) {
	assets, err := NewDeferredPublicPresentationAssets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewPublicHandler(&testCatalog{product: enabledPublicProduct(7, "course-7")})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicPresentationAssets(assets); err != nil {
		t.Fatal(err)
	}

	// Composition can bind the explicit release directory before a browser
	// artifact exists, but an actual public HTML or declared asset request must
	// never silently fall back to the donor-only page.
	for _, path := range []string{"/pay/course-7", "/product-public-assets/publicCommerceHost.js"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "public presentation unavailable") {
			t.Fatalf("bound deferred path=%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	// The browser closure is unrelated to the stable public catalog API, which
	// remains readable by worker and non-UI composition fixtures.
	api := httptest.NewRecorder()
	handler.ServeHTTP(api, httptest.NewRequest(http.MethodGet, "/api/public/products/course-7", nil))
	if api.Code != http.StatusOK {
		t.Fatalf("public API status=%d body=%s", api.Code, api.Body.String())
	}
}

func TestServicePeriodPresentationMountsForAvailableAndUnavailablePages(t *testing.T) {
	assets, _ := publicPresentationFixture(t)
	product := productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}

	available, err := NewServicePeriodPublicHandler(&servicePeriodPublicStub{product: product})
	if err != nil {
		t.Fatal(err)
	}
	if err = available.SetPublicPresentationAssets(assets); err != nil {
		t.Fatal(err)
	}
	availablePage := httptest.NewRecorder()
	available.ServeHTTP(availablePage, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	assertServicePeriodPresentation(t, availablePage, "available")

	unavailable, err := NewServicePeriodPublicHandler(servicePeriodUnavailablePresentationStub{product: product})
	if err != nil {
		t.Fatal(err)
	}
	if err = unavailable.SetPublicPresentationAssets(assets); err != nil {
		t.Fatal(err)
	}
	unavailablePage := httptest.NewRecorder()
	unavailable.ServeHTTP(unavailablePage, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	assertServicePeriodPresentation(t, unavailablePage, "unavailable")
}

func TestDeferredServicePeriodPresentationFailsClosedWhenRouteIsRequested(t *testing.T) {
	assets, err := NewDeferredPublicPresentationAssets(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	product := productport.CheckoutProduct{ID: 71, ProductType: productport.ProductOptionServicePeriod, Code: "term-31", Name: "31 天服务期", PriceMinor: 12800, Currency: "CNY", Version: 4, ServicePeriodDurationDays: 31}
	handler, err := NewServicePeriodPublicHandler(&servicePeriodPublicStub{product: product})
	if err != nil {
		t.Fatal(err)
	}
	if err = handler.SetPublicPresentationAssets(assets); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/s/term-31", nil))
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "public presentation unavailable") {
		t.Fatalf("deferred service-period status=%d body=%s", response.Code, response.Body.String())
	}
}

func assertServicePeriodPresentation(t *testing.T, response *httptest.ResponseRecorder, branch string) {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("%s page status=%d body=%s", branch, response.Code, response.Body.String())
	}
	for _, marker := range []string{
		"data-v3-public-commerce",
		"data-product-kind=\"service_period\"",
		"href=\"/product-public-assets/publicCommerceStyles-ABCD1234.css\"",
		"src=\"/product-public-assets/publicCommerceHost-ABCD1234.js\"",
	} {
		if !strings.Contains(response.Body.String(), marker) {
			t.Fatalf("%s page missing %q: %s", branch, marker, response.Body.String())
		}
	}
	if !strings.Contains(response.Header().Get("Content-Security-Policy"), "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("%s page has no same-origin public script CSP: %s", branch, response.Header().Get("Content-Security-Policy"))
	}
}

type servicePeriodUnavailablePresentationStub struct {
	product productport.CheckoutProduct
}

func (stub servicePeriodUnavailablePresentationStub) ReadPublicServicePeriodByCode(context.Context, string) (productport.CheckoutProduct, error) {
	return productport.CheckoutProduct{}, errors.New("not currently available")
}

func (stub servicePeriodUnavailablePresentationStub) ReadServicePeriodPublicPresentationByCode(_ context.Context, code string) (productport.CheckoutProduct, bool, error) {
	if code != stub.product.Code {
		return productport.CheckoutProduct{}, false, errors.New("not found")
	}
	return stub.product, false, nil
}

func TestFrozenServicePeriodPresentationDecoratesOutputNotDonor(t *testing.T) {
	assets, _ := publicPresentationFixture(t)
	page, err := decorateFrozenServicePeriodPresentation("<!doctype html><html><head><title>服务期</title></head><body><main class=\"service-period-page is-none\">事实</main></body></html>", assets)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{
		"href=\"/product-public-assets/publicCommerceStyles-ABCD1234.css\"",
		"src=\"/product-public-assets/publicCommerceHost-ABCD1234.js\"",
		"<main data-v3-public-commerce data-public-commerce-route=\"service-period-state\" data-product-kind=\"service_period\" class=\"service-period-page ",
	} {
		if !strings.Contains(page, marker) {
			t.Fatalf("frozen output decoration missing %q: %s", marker, page)
		}
	}
	if got := sha256.Sum256([]byte(frozenServicePeriodPublicRenderer)); hex.EncodeToString(got[:]) != frozenServicePeriodPublicRendererSHA256 {
		t.Fatalf("frozen donor changed: %x", got)
	}
}

func publicPresentationFixture(t *testing.T) (PublicPresentationAssets, string) {
	t.Helper()
	dist := t.TempDir()
	cssRelative := "assets/publicCommerceStyles-ABCD1234.css"
	imageRelative := "assets/files/wechat-auth-ABCD1234.jpg"
	hostRelative := "assets/publicCommerceHost-ABCD1234.js"
	chunkRelative := "assets/chunks/publicCommerceRuntime-ABCD1234.js"
	adminRelative := "assets/admin-ABCD1234.js"
	files := map[string][]byte{
		cssRelative:   []byte("main[data-v3-public-commerce]{background:url('./files/wechat-auth-ABCD1234.jpg')}"),
		imageRelative: []byte("\xff\xd8\xff\xe0fixture"),
		hostRelative:  []byte("import './chunks/publicCommerceRuntime-ABCD1234.js';"),
		chunkRelative: []byte("export const publicCommerceRuntime = true;"),
		adminRelative: []byte("private admin asset"),
	}
	manifest := map[string]any{
		"entries":       map[string]string{"publicCommerceStyles": cssRelative, "publicCommerceHost": hostRelative},
		"files":         map[string]any{},
		"release_files": map[string]any{},
	}
	manifestFiles := manifest["files"].(map[string]any)
	releaseFiles := manifest["release_files"].(map[string]any)
	for relative, contents := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dist, relative)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dist, relative), contents, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(contents)
		value := map[string]any{"sha256": hex.EncodeToString(sum[:])}
		if relative == hostRelative {
			value["imports"] = []map[string]string{{"path": chunkRelative}}
		}
		if relative == cssRelative {
			value["imports"] = []map[string]string{{"path": imageRelative}}
		}
		manifestFiles[relative] = value
		releaseFiles[relative] = map[string]string{"sha256": hex.EncodeToString(sum[:])}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dist, "asset-manifest.json"), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	assets, err := NewPublicPresentationAssets(dist)
	if err != nil {
		t.Fatal(err)
	}
	return assets, hostRelative
}
