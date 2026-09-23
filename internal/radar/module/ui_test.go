package module

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestUIBindingMountsOnlyRadarPages(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"radar", "radarDetail", "radarForm"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"tokens.css", "labs.css", "admin.js", "radarHost.js", "standardHost.js", "selectionDialog.css"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", file), []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"entries": map[string]string{"tokens": "assets/tokens.css", "labs": "assets/labs.css", "admin": "assets/admin.js", "radarHost": "assets/radarHost.js", "standardComponentsStableHost": "assets/standardHost.js", "selectionDialogStyles": "assets/selectionDialog.css"}})
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	handler := NewModuleRegistration().UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page string, assets UIAssets) error {
		if assets.HostJS != "/assets/radarHost.js" || assets.StandardHostJS != "/assets/standardHost.js" || assets.SelectionDialogCSS != "/assets/selectionDialog.css" {
			t.Fatalf("picker hosts missing: %+v", assets)
		}
		w.WriteHeader(200)
		_, _ = w.Write([]byte(page))
		return nil
	})
	for _, path := range []string{"/admin/radar-links", "/admin/radarDetail.html?id=1", "/admin/radarForm.html", "/admin/radarForm.html?id=2"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/radarDetail.html?id=0", nil))
	if response.Code != 404 {
		t.Fatalf("invalid id status=%d", response.Code)
	}
	if err := os.Remove(filepath.Join(dist, "assets", "standardHost.js")); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/radar-links", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing standard picker host status=%d", response.Code)
	}
}
