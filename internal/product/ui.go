package product

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

// ProductPageRenderer is implemented by the v3 webshell adapter. Only the
// already extracted template fragment is passed to it; the adapter never
// receives a request-controlled HTML string.
type ProductPageRenderer func(http.ResponseWriter, *http.Request, string, string, ProductAssets) error

type ProductAssets struct {
	TokensCSS, LabsCSS, ProductCSS, HostJS, StandardHostJS string
	StandardCSS                                            []string
}

type productUI struct {
	dist   string
	render ProductPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render ProductPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &productUI{dist: dist, render: render}
}

func (h *productUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.render == nil {
		http.NotFound(w, r)
		return
	}
	path := strings.TrimSuffix(r.URL.Path, "/")
	if strings.HasPrefix(path, "/product-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, id, ok := productPage(path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !validProductUIQuery(r, page, id) {
		http.NotFound(w, r)
		return
	}
	if id != "" && r.URL.Query().Get("id") == "" {
		// AdminController reads the original query string. Keep canonical
		// resource URLs while handing the frozen donor its expected ?id form.
		alias := productAliasForPage(page)
		query := "?id=" + urlQueryEscape(id)
		http.Redirect(w, r, alias+query, http.StatusSeeOther)
		return
	}
	templateBody, err := h.template(page)
	if err != nil {
		http.Error(w, "product UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := h.assets()
	if err == nil && page == "spProductData" {
		var manifest buildManifest
		raw, e := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
		if e == nil {
			e = json.Unmarshal(raw, &manifest)
		}
		if e != nil {
			err = e
		} else {
			value := manifest.Entries["productWorkspace"]
			if _, exists := manifest.Files[value]; value == "" || !exists || !strings.HasPrefix(value, "assets/") || strings.Contains(value, "..") {
				err = errors.New("product workspace asset missing")
			} else {
				assets.HostJS = "/product-assets/" + strings.TrimPrefix(value, "assets/")
			}
		}
	}
	if err != nil {
		http.Error(w, "product UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "product UI unavailable", http.StatusInternalServerError)
	}
}

func productPage(path string) (page, id string, ok bool) {
	switch path {
	case "/admin/wechat-pay/products", "/admin/wechat-pay/products.html", "/admin/products.html":
		return "products", "", true
	case "/admin/wechat-pay/productForm.html", "/admin/productForm.html":
		return "productForm", "", true
	case "/admin/wechat-pay/spProducts.html", "/admin/spProducts.html":
		return "spProducts", "", true
	case "/admin/wechat-pay/spProductForm.html", "/admin/spProductForm.html":
		return "spProductForm", "", true
	case "/admin/spProductData.html", "/admin/wechat-pay/spProductData.html", "/admin/wechat-pay/products/spProductData.html", "/admin/service-period-products/spProductData.html":
		return "spProductData", "", true
	case "/admin/wechat-pay/products/new":
		return "productForm", "", true
	case "/admin/service-period-products":
		return "spProducts", "", true
	case "/admin/service-period-products/new":
		return "spProductForm", "", true
	}
	if strings.HasPrefix(path, "/admin/wechat-pay/products/") {
		value := strings.TrimPrefix(path, "/admin/wechat-pay/products/")
		if strings.HasSuffix(value, "/edit") {
			return "productForm", strings.TrimSuffix(value, "/edit"), true
		}
	}
	if strings.HasPrefix(path, "/admin/service-period-products/") {
		value := strings.TrimPrefix(path, "/admin/service-period-products/")
		if strings.HasSuffix(value, "/edit") {
			return "spProductForm", strings.TrimSuffix(value, "/edit"), true
		}
	}
	return "", "", false
}

func productAliasForPage(page string) string {
	switch page {
	case "productForm":
		return "/admin/wechat-pay/productForm.html"
	case "spProductForm":
		return "/admin/spProductForm.html"
	case "spProductData":
		return "/admin/spProductData.html"
	default:
		return "/admin/wechat-pay/products.html"
	}
}

func validProductUIQuery(r *http.Request, page, canonicalID string) bool {
	if canonicalID != "" && !positiveDecimal(canonicalID) {
		return false
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return false
	}
	if tabs, ok := values["tab"]; ok {
		if page != "spProductData" || len(tabs) != 1 || (tabs[0] != "overview" && tabs[0] != "details") {
			return false
		}
		delete(values, "tab")
	}
	if len(values) == 0 {
		return true
	}
	if page != "productForm" && page != "spProductForm" && page != "spProductData" {
		return false
	}
	ids, ok := values["id"]
	if !ok || len(ids) != 1 || len(values) != 1 || !positiveDecimal(ids[0]) {
		return false
	}
	if canonicalID != "" && ids[0] != canonicalID {
		return false
	}
	return true
}

func positiveDecimal(value string) bool {
	if value == "" || strings.HasPrefix(value, "0") {
		return false
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatInt(parsed, 10) == value
}

func urlQueryEscape(value string) string {
	// IDs are validated decimal strings. Avoid importing a broad URL builder
	// merely to escape a value that has already passed this invariant.
	return value
}

func (h *productUI) template(page string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		return "", err
	}
	return donortemplate.Extract(string(raw))
}

type buildManifest struct {
	Entries map[string]string          `json:"entries"`
	Files   map[string]json.RawMessage `json:"files"`
}

func (h *productUI) assets() (ProductAssets, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return ProductAssets{}, err
	}
	var manifest buildManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return ProductAssets{}, err
	}
	get := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("product bundle asset missing")
		}
		if _, err = os.Stat(filepath.Join(h.dist, value)); err != nil {
			return "", err
		}
		return "/product-assets/" + strings.TrimPrefix(value, "assets/"), nil
	}
	tokens, err := get("tokens")
	if err != nil {
		return ProductAssets{}, err
	}
	labs, err := get("labs")
	if err != nil {
		return ProductAssets{}, err
	}
	productCSS, err := get("productDistributionStyles")
	if err != nil {
		return ProductAssets{}, err
	}
	host, err := get("productHost")
	if err != nil {
		return ProductAssets{}, err
	}
	standardHost, err := get("standardComponentsHost")
	if err != nil {
		return ProductAssets{}, err
	}
	selectionDialogCSS, err := get("selectionDialogStyles")
	if err != nil {
		return ProductAssets{}, err
	}
	detailDrawerCSS, err := get("sharedDetailDrawerStyles")
	if err != nil {
		return ProductAssets{}, err
	}
	standardCSS := make([]string, 0, 5)
	for _, name := range []string{"material_picker.css", "send_content_composer.css", "wecom_tag_picker.css"} {
		relative := "assets/standard-components/" + name
		if _, ok := manifest.Files[relative]; !ok {
			return ProductAssets{}, errors.New("product standard component asset missing")
		}
		if _, err = os.Stat(filepath.Join(h.dist, relative)); err != nil {
			return ProductAssets{}, err
		}
		standardCSS = append(standardCSS, "/product-assets/standard-components/"+name)
	}
	standardCSS = append(standardCSS, selectionDialogCSS, detailDrawerCSS)
	return ProductAssets{TokensCSS: tokens, LabsCSS: labs, ProductCSS: productCSS, HostJS: host, StandardHostJS: standardHost, StandardCSS: standardCSS}, nil
}

func (h *productUI) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/product-assets/")
	if relative == "" || strings.Contains(relative, "..") || strings.HasPrefix(relative, "/") {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var manifest buildManifest
	if json.Unmarshal(raw, &manifest) != nil {
		http.NotFound(w, r)
		return
	}
	assetPath := "assets/" + relative
	if _, ok := manifest.Files[assetPath]; !ok {
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
