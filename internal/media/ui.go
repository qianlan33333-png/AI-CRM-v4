package media

import (
	"encoding/json"
	"errors"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// MediaPageRenderer is implemented by the v3 webshell adapter in composition.
// The immutable donor template is read only from the release build directory.
type MediaPageRenderer func(http.ResponseWriter, *http.Request, string, string, MediaAssets) error
type MediaAssets struct{ TokensCSS, LabsCSS, AdminJS, MaterialSaveHostJS, ImageLibraryFilterHostJS, MaterialLibraryHostJS string }
type mediaUI struct {
	dist   string
	render MediaPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render MediaPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &mediaUI{dist: dist, render: render}
}

func (h *mediaUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.render == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/media-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, canonical, ok := mediaRequest(r.URL)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if canonical != "" {
		http.Redirect(w, r, canonical, http.StatusSeeOther)
		return
	}
	templateBody := ""
	if page != "images" {
		var err error
		templateBody, err = h.template(page)
		if err != nil {
			http.Error(w, "media UI unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	assets, err := h.assets()
	if err != nil {
		http.Error(w, "media UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "media UI unavailable", http.StatusInternalServerError)
	}
}
func mediaRequest(requestURL *url.URL) (page, canonical string, ok bool) {
	switch requestURL.Path {
	case "/admin/image-library", "/admin/images.html":
		return "", canonicalMaterialPath("images"), true
	case "/admin/miniprogram-library", "/admin/mpLib.html":
		return "", canonicalMaterialPath("mpLib"), true
	case "/admin/attachment-library", "/admin/attach.html":
		return "", canonicalMaterialPath("attach"), true
	case "/admin/materials":
		query := requestURL.Query()
		if len(query) == 0 {
			return "images", "", true
		}
		valid := len(query["tab"]) == 1
		for key, values := range query {
			if (key != "tab" && key != "material_group") || len(values) != 1 {
				valid = false
			}
		}
		if !valid {
			return "", canonicalMaterialPath("images"), true
		}
		switch query.Get("tab") {
		case "images":
			return "images", "", true
		case "attachments":
			return "attach", "", true
		case "miniprograms":
			return "mpLib", "", true
		default:
			return "", canonicalMaterialPath("images"), true
		}
	default:
		return "", "", false
	}
}
func canonicalMaterialPath(page string) string {
	switch page {
	case "images":
		return "/admin/materials?tab=images"
	case "mpLib":
		return "/admin/materials?tab=miniprograms"
	default:
		return "/admin/materials?tab=attachments"
	}
}
func (h *mediaUI) template(page string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		return "", err
	}
	return extractDonorTemplate(string(raw))
}

func extractDonorTemplate(raw string) (string, error) {
	return donortemplate.Extract(raw)
}

type buildManifest struct {
	Entries map[string]string          `json:"entries"`
	Files   map[string]json.RawMessage `json:"files"`
}

func (h *mediaUI) assets() (MediaAssets, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return MediaAssets{}, err
	}
	var manifest buildManifest
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return MediaAssets{}, err
	}
	for _, name := range []string{"tokens", "labs", "admin", "materialSaveHost", "imageLibraryFilterHost", "materialLibraryHost"} {
		if manifest.Entries[name] == "" {
			return MediaAssets{}, errors.New("media bundle asset missing")
		}
	}
	return MediaAssets{TokensCSS: "/media-assets/" + manifest.Entries["tokens"], LabsCSS: "/media-assets/" + manifest.Entries["labs"], AdminJS: "/media-assets/" + manifest.Entries["admin"], MaterialSaveHostJS: "/media-assets/" + manifest.Entries["materialSaveHost"], ImageLibraryFilterHostJS: "/media-assets/" + manifest.Entries["imageLibraryFilterHost"], MaterialLibraryHostJS: "/media-assets/" + manifest.Entries["materialLibraryHost"]}, nil
}
func (h *mediaUI) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/media-assets/")
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
	if _, ok := manifest.Files[relative]; !ok {
		http.NotFound(w, r)
		return
	}
	file, err := os.Open(filepath.Join(h.dist, relative))
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
