package http

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIHandlerRendersDashboardAndServesOnlyReleaseAssets(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets", "chunks"), 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"assets/tokens-HASH.css":        "tokens",
		"assets/labs-HASH.css":          "labs",
		"assets/admin-HASH.js":          "admin",
		"assets/chunks/funnel-HASH.js":  "funnel",
		"assets/not-in-release-HASH.js": "private",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dist, filepath.FromSlash(name)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"entries":{"tokens":"assets/tokens-HASH.css","labs":"assets/labs-HASH.css","admin":"assets/admin-HASH.js"},"release_files":{"assets/tokens-HASH.css":{},"assets/labs-HASH.css":{},"assets/admin-HASH.js":{},"assets/chunks/funnel-HASH.js":{}}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewUIHandler(dist, func(writer http.ResponseWriter, _ *http.Request, assets PageAssets) error {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(strings.Join([]string{assets.TokensCSS, assets.LabsCSS, assets.AdminJS}, "|")))
		return nil
	})

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, dashboardPagePath, nil))
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "private, no-store" || response.Body.String() != "/hxc-dashboard-assets/tokens-HASH.css|/hxc-dashboard-assets/labs-HASH.css|/hxc-dashboard-assets/admin-HASH.js" {
		t.Fatalf("page status=%d cache=%q body=%q", response.Code, response.Header().Get("Cache-Control"), response.Body.String())
	}

	for _, tab := range []string{"overview", "details"} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, dashboardPagePath+"?tab="+tab, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("tab %s status=%d", tab, response.Code)
		}
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hxc-dashboard-assets/chunks/funnel-HASH.js", nil))
	if response.Code != http.StatusOK || response.Body.String() != "funnel" || !strings.Contains(response.Header().Get("Content-Type"), "javascript") || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("asset status=%d type=%q cache=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Header().Get("Cache-Control"), response.Body.String())
	}

	for _, target := range []string{
		"/admin/hxc-dashboard?persisted=1",
		"/admin/hxc-dashboard?tab=other",
		"/admin/hxc-dashboard?tab=details&tab=overview",
		"/admin/hxc-dashboard?tab=details&user=1",
		"/admin/hxc-dashboard?tab=%ZZ",
		"/hxc-dashboard-assets/not-in-release-HASH.js",
		"/hxc-dashboard-assets/chunks/../admin-HASH.js",
	} {
		response = httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("target=%q status=%d", target, response.Code)
		}
	}
}

func TestUIHandlerFailsClosedWithoutVerifiedReleaseManifest(t *testing.T) {
	handler := NewUIHandler(t.TempDir(), func(http.ResponseWriter, *http.Request, PageAssets) error { return nil })
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, dashboardPagePath, nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d", response.Code)
	}
}
