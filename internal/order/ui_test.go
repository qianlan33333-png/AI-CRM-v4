package order

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestOrderUIRequiresAndServesHostBundle(t *testing.T) {
	dist := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dist, "assets"), 0755); err != nil {
		t.Fatal(err)
	}
	entries := map[string]string{"tokens": "assets/tokens.css", "labs": "assets/labs.css", "admin": "assets/admin.js", "orderHost": "assets/order-host.js"}
	files := map[string]any{}
	for _, path := range entries {
		files[path] = map[string]any{}
		if err := os.WriteFile(filepath.Join(dist, path), []byte("/* fixture */"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	raw, _ := json.Marshal(map[string]any{"entries": entries, "files": files})
	if err := os.WriteFile(filepath.Join(dist, "asset-manifest.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	binding := &uiBinding{dist: dist}
	assets, err := binding.assets()
	if err != nil || assets.HostJS != "/order-assets/order-host.js" {
		t.Fatalf("host asset missing: %#v %v", assets, err)
	}
	response := httptest.NewRecorder()
	binding.ServeHTTP(response, httptest.NewRequest(http.MethodGet, assets.HostJS, nil))
	if response.Code != http.StatusOK {
		t.Fatalf("host asset status %d", response.Code)
	}
	if err := os.Remove(filepath.Join(dist, entries["orderHost"])); err != nil {
		t.Fatal(err)
	}
	if _, err := binding.assets(); err == nil {
		t.Fatal("missing Host must fail rather than render the old query UI")
	}
}

func TestOrderDetailUIAcceptsOnlyCanonicalProviderQuery(t *testing.T) {
	for _, test := range []struct {
		name, query string
		want        bool
	}{
		{"legacy id", "?id=M-1", true},
		{"wechat alias", "?id=M-1&provider=wechat", true},
		{"wechat pay", "?id=M-1&provider=wechat_pay", true},
		{"alipay", "?id=M-1&provider=alipay", true},
		{"unknown provider", "?id=M-1&provider=unknown", false},
		{"duplicate provider", "?id=M-1&provider=wechat&provider=alipay", false},
		{"unrelated query", "?id=M-1&source=dom", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/admin/orderDetail.html"+test.query, nil)
			if got := validUIQuery(request, "orderDetail"); got != test.want {
				t.Fatalf("validUIQuery(%s)=%v want=%v", test.query, got, test.want)
			}
		})
	}
}
