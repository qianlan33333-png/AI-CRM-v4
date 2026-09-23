package module

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type UIAssets struct{ TokensCSS, LabsCSS, AdminJS, HostJS, StandardHostJS, SelectionDialogCSS string }
type PageRenderer func(http.ResponseWriter, *http.Request, string, UIAssets) error
type adminUI struct {
	dist   string
	render PageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render PageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &adminUI{dist: dist, render: render}
}
func (h *adminUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	page, ok := radarPage(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if _, err := os.Stat(filepath.Join(h.dist, "admin", page+".html")); err != nil {
		http.Error(w, "radar UI unavailable", 503)
		return
	}
	assets, err := radarAssets(h.dist)
	if err != nil {
		http.Error(w, "radar UI unavailable", 503)
		return
	}
	if err = h.render(w, r, page, assets); err != nil {
		http.Error(w, "radar UI unavailable", 500)
	}
}
func radarPage(r *http.Request) (string, bool) {
	switch r.URL.Path {
	case "/admin/radar-links", "/admin/radar.html":
		return "radar", len(r.URL.Query()) == 0
	case "/admin/radarDetail.html", "/admin/radarForm.html":
		q := r.URL.Query()
		if len(q) == 0 && strings.HasSuffix(r.URL.Path, "radarForm.html") {
			return "radarForm", true
		}
		if len(q) != 1 {
			return "", false
		}
		raw := q.Get("id")
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id < 1 || strconv.FormatInt(id, 10) != raw {
			return "", false
		}
		if strings.HasSuffix(r.URL.Path, "radarDetail.html") {
			return "radarDetail", true
		}
		return "radarForm", true
	}
	return "", false
}
func radarAssets(dist string) (UIAssets, error) {
	raw, err := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if err != nil {
		return UIAssets{}, err
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return UIAssets{}, err
	}
	get := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || !strings.HasPrefix(value, "assets/") || strings.Contains(value, "..") {
			return "", errors.New("radar bundle asset missing")
		}
		if _, e := os.Stat(filepath.Join(dist, value)); e != nil {
			return "", e
		}
		return "/" + value, nil
	}
	t, err := get("tokens")
	if err != nil {
		return UIAssets{}, err
	}
	l, err := get("labs")
	if err != nil {
		return UIAssets{}, err
	}
	a, err := get("admin")
	if err != nil {
		return UIAssets{}, err
	}
	h, err := get("radarHost")
	if err != nil {
		return UIAssets{}, err
	}
	standard, err := get("standardComponentsStableHost")
	if err != nil {
		return UIAssets{}, err
	}
	selectionDialog, err := get("selectionDialogStyles")
	if err != nil {
		return UIAssets{}, err
	}
	return UIAssets{TokensCSS: t, LabsCSS: l, AdminJS: a, HostJS: h, StandardHostJS: standard, SelectionDialogCSS: selectionDialog}, nil
}
