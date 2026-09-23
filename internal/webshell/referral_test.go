package webshell

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReferralShellUsesCRMAndCompleteManifest(t *testing.T) {
	root := t.TempDir()
	entries := map[string]string{"referralStyles": "assets/referral.css", "referralAdmin": "assets/referral.js", "sharedDetailDrawerStyles": "assets/drawer.css"}
	files := map[string]any{}
	for _, name := range entries {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte("/* fixture */"), 0644); err != nil {
			t.Fatal(err)
		}
		files[name] = map[string]any{}
	}
	manifest, _ := json.Marshal(map[string]any{"entries": entries, "files": files})
	if err := os.WriteFile(filepath.Join(root, "asset-manifest.json"), manifest, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "referral"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "referral", "index.html"), []byte(`<main id="referral-root">活动</main>`), 0644); err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(HandlerOptions{DistDir: root})
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range []string{"/referral?campaign=1&invite=rfi_test", "/referral/", "/admin/referral", "/admin/referral/settings"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, route, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", route, response.Code, response.Body.String())
		}
		if strings.Contains(route, "/admin/") {
			body := response.Body.String()
			if !strings.Contains(body, `admin_search_select.js?v=search-select-v2`) || !strings.Contains(body, `id="referral-admin-root"`) || strings.Count(body, `<main`) != 1 || !strings.Contains(body, `src="/assets/referral.js"`) {
				t.Fatalf("missing single CRM shell: %s", body)
			}
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/referral", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatal(response.Code)
	}
	if err := os.Remove(filepath.Join(root, "assets/referral.js")); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/referral", nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatal(response.Code)
	}
	entries["referralAdmin"] = "assets/../referral.js"
	manifest, _ = json.Marshal(map[string]any{"entries": entries, "files": files})
	_ = os.WriteFile(filepath.Join(root, "asset-manifest.json"), manifest, 0644)
	if _, ok := DistReferralAdminAssets(root); ok {
		t.Fatal("unsafe asset path accepted")
	}
}
