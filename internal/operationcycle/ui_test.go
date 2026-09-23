package operationcycle

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUIBindingMountsFrozenTemplateWithHostAdapter(t *testing.T) {
	dist := t.TempDir()
	for _, name := range []string{"tokens.css", "labs.css", "host.js"} {
		path := filepath.Join(dist, "assets", name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	carrier := `<!doctype html><template id="tpl"><section data-proof="frozen">原版页面</section></template>`
	for _, page := range []string{"cycles", "cyclesDetail"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(carrier), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	manifest := `{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","operationCyclesHost":"assets/host.js"}}`
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	module := NewModuleRegistration()
	handler := module.UIBinding(dist, func(w http.ResponseWriter, _ *http.Request, page, donor string, assets UIAssets) error {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(page + "|" + donor + "|" + assets.HostJS))
		return nil
	})
	for _, target := range []string{"/admin/operation-cycles", "/admin/operation-cycles/cyclesDetail.html?id=1"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `data-proof="frozen"`) || !strings.Contains(response.Body.String(), "/assets/host.js") {
			t.Fatalf("unexpected UI response for %s: %d %s", target, response.Code, response.Body.String())
		}
	}
}

func TestUIBindingRejectsNonDonorRouteAndQueryShapes(t *testing.T) {
	handler := NewModuleRegistration().UIBinding(t.TempDir(), func(http.ResponseWriter, *http.Request, string, string, UIAssets) error { return nil })
	for _, target := range []string{"/admin/operation-cycles?view=detail&id=run_1", "/admin/operation-cycles/cyclesDetail.html?id=../secret"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/operation-cycles" {
			t.Fatalf("unexpected response for %s: %d %s", target, response.Code, response.Header().Get("Location"))
		}
	}
}
