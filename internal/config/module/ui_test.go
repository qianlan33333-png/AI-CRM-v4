package module

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestConfigUIOnlyMountsClosedDonorPagesAndPreservesDetailQuery(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "admin"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, page := range []string{"config", "configDetail", "apidocs"} {
		if err := os.WriteFile(filepath.Join(dist, "admin", page+".html"), []byte(`<html><template id="tpl"><section data-page="`+page+`">donor</section></template></html>`), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tokens.css", "labs.css", "admin.js"} {
		if err := os.WriteFile(filepath.Join(dist, "assets", name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), []byte(`{"entries":{"tokens":"assets/tokens.css","labs":"assets/labs.css","admin":"assets/admin.js"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	h := NewRegistration().UIBinding(dist, func(w http.ResponseWriter, r *http.Request, page, body string, assets UIAssets) error {
		_, _ = w.Write([]byte(page + ":" + body + ":" + assets.AdminJS))
		return nil
	})
	for _, path := range []string{"/admin/config", "/admin/config/", "/admin/configDetail.html?cat=wecom_base", "/admin/configDetail.html?cat=admin_access", "/admin/configDetail.html?cat=sidebar_identity", "/admin/configDetail.html?cat=ai_automation", "/admin/configDetail.html?cat=open_api_key", "/admin/configDetail.html?cat=api_token", "/admin/configDetail.html?cat=webhooks_push", "/admin/configDetail.html?cat=reliability", "/admin/configDetail.html?cat=wechat_pay", "/admin/configDetail.html?cat=alipay", "/admin/configDetail.html?cat=wechat_shop", "/admin/configDetail.html?cat=wechat_oauth", "/admin/api-docs", "/admin/config/releases", "/admin/config/releases/new", "/admin/config/releases/17"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 200 {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
	for _, path := range []string{"/admin/config?history=x", "/admin/configDetail.html?cat=customer_state_history", "/admin/configDetail.html?cat=wecom_base&extra=1", "/admin/apidocs.html?download=1", "/admin/config/releases/0", "/admin/config/releases/not-a-number", "/admin/config/releases/7/usage"} {
		response := httptest.NewRecorder()
		h.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != 404 {
			t.Fatalf("%s status=%d", path, response.Code)
		}
	}
}
