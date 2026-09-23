package order

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type PageAssets struct{ TokensCSS, LabsCSS, AdminJS, HostJS string }
type PageRenderer func(http.ResponseWriter, *http.Request, string, string, PageAssets) error

type uiBinding struct {
	dist   string
	render PageRenderer
}

func NewUIBinding(dist string, render PageRenderer) http.Handler {
	if strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &uiBinding{dist: dist, render: render}
}

func (h *uiBinding) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(path, "/order-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page := ""
	switch path {
	case "/admin/orders", "/admin/orders.html":
		page = "orders"
	case "/admin/orderDetail.html":
		page = "orderDetail"
	}
	if page == "" || !validUIQuery(r, page) {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "order UI unavailable", http.StatusServiceUnavailable)
		return
	}
	templateBody, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "order UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := h.assets()
	if err != nil {
		http.Error(w, "order UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "order UI unavailable", http.StatusInternalServerError)
	}
}

func validUIQuery(r *http.Request, page string) bool {
	values := r.URL.Query()
	if page == "orders" {
		return len(values) == 0
	}
	ids, ok := values["id"]
	if !ok || len(ids) != 1 || ids[0] == "" || len(ids[0]) > 200 || strings.TrimSpace(ids[0]) != ids[0] {
		return false
	}
	if len(values) > 2 {
		return false
	}
	if providers, exists := values["provider"]; exists {
		if len(providers) != 1 || !validDetailProvider(providers[0]) {
			return false
		}
	}
	if len(values) == 2 {
		if _, exists := values["provider"]; !exists {
			return false
		}
	}
	for _, value := range ids[0] {
		if unicode.IsControl(value) {
			return false
		}
	}
	return true
}

func validDetailProvider(provider string) bool {
	return provider == "wechat" || provider == "wechat_pay" || provider == "wechat_shop" || provider == "alipay"
}

type buildManifest struct {
	Entries map[string]string          `json:"entries"`
	Files   map[string]json.RawMessage `json:"files"`
}

func (h *uiBinding) manifest() (buildManifest, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return buildManifest{}, err
	}
	var manifest buildManifest
	if json.Unmarshal(raw, &manifest) != nil {
		return buildManifest{}, errors.New("invalid order asset manifest")
	}
	return manifest, nil
}

func (h *uiBinding) assets() (PageAssets, error) {
	manifest, err := h.manifest()
	if err != nil {
		return PageAssets{}, err
	}
	get := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("order bundle asset missing")
		}
		if _, statErr := os.Stat(filepath.Join(h.dist, value)); statErr != nil {
			return "", statErr
		}
		return "/order-assets/" + strings.TrimPrefix(value, "assets/"), nil
	}
	tokens, err := get("tokens")
	if err != nil {
		return PageAssets{}, err
	}
	labs, err := get("labs")
	if err != nil {
		return PageAssets{}, err
	}
	admin, err := get("admin")
	if err != nil {
		return PageAssets{}, err
	}
	host, err := get("orderHost")
	if err != nil {
		return PageAssets{}, err
	}
	return PageAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin, HostJS: host}, nil
}

func (h *uiBinding) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/order-assets/")
	if relative == "" || strings.Contains(relative, "..") || strings.HasPrefix(relative, "/") {
		http.NotFound(w, r)
		return
	}
	manifest, err := h.manifest()
	assetPath := "assets/" + relative
	if err != nil || manifest.Files[assetPath] == nil {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(h.dist, assetPath))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(filepath.Ext(info.Name()))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, file)
}
