package externaleffects

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// PageRenderer is supplied by the composition root's v3 webshell adapter. It
// receives only manifest-derived asset paths and never donor HTML.
type PageRenderer func(http.ResponseWriter, *http.Request, string, string, string) error

// UIHandler serves only the frozen V2 hidden External Effects workspace. The
// donor bundle is not given a campaign URL: every request is normalized to
// view=external-effects and only its hashed assets are reachable. The page
// itself remains the one v3 webshell, supplied through PageRenderer.
type UIHandler struct {
	dist     string
	renderer PageRenderer
}

func NewUIHandler(dist string, renderers ...PageRenderer) *UIHandler {
	var renderer PageRenderer
	if len(renderers) == 1 {
		renderer = renderers[0]
	}
	return &UIHandler{dist: dist, renderer: renderer}
}
func (h *UIHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.dist == "" {
		http.NotFound(w, r)
		return
	}
	if r.URL.Path == "/admin/external-effects" {
		if !validExternalEffectsQuery(r.URL.Query()) {
			http.Redirect(w, r, "/admin/campaigns.html?view=external-effects", http.StatusSeeOther)
			return
		}
		http.Redirect(w, r, "/admin/campaigns.html?"+r.URL.Query().Encode(), http.StatusSeeOther)
		return
	}
	if r.URL.Path == "/admin/campaigns.html" {
		if !validExternalEffectsQuery(r.URL.Query()) {
			http.NotFound(w, r)
			return
		}
		if h.renderer == nil {
			http.Error(w, "external effects UI unavailable", http.StatusServiceUnavailable)
			return
		}
		tokens, labs, admin, err := h.pageAssets()
		if err != nil {
			http.Error(w, "external effects UI unavailable", http.StatusServiceUnavailable)
			return
		}
		if err = h.renderer(w, r, tokens, labs, admin); err != nil {
			http.Error(w, "external effects UI unavailable", http.StatusServiceUnavailable)
		}
		return
	}
	if !strings.HasPrefix(r.URL.Path, "/assets/") {
		http.NotFound(w, r)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/")
	if strings.Contains(relative, "..") {
		http.NotFound(w, r)
		return
	}
	h.serve(w, r, relative, staticMIME(relative))
}

// ValidUIQuery freezes the only donor campaigns.html compatibility shape that
// v3 exposes. It is exported for the outer security-header adapter, not as a
// business API contract.
func ValidUIQuery(query map[string][]string) bool { return validExternalEffectsQuery(query) }

func validExternalEffectsQuery(query map[string][]string) bool {
	if len(query) < 1 || len(query) > 2 || len(query["view"]) != 1 || query["view"][0] != "external-effects" {
		return false
	}
	jobs, hasJob := query["job"]
	if !hasJob {
		return len(query) == 1
	}
	if len(query) != 2 || len(jobs) != 1 {
		return false
	}
	value, err := strconv.ParseInt(jobs[0], 10, 64)
	return err == nil && value > 0
}

func (h *UIHandler) pageAssets() (string, string, string, error) {
	data, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return "", "", "", err
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if err = json.Unmarshal(data, &manifest); err != nil {
		return "", "", "", err
	}
	asset := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("external effects asset is unavailable")
		}
		if _, statErr := os.Stat(filepath.Join(h.dist, filepath.FromSlash(value))); statErr != nil {
			return "", statErr
		}
		return "/" + value, nil
	}
	tokens, err := asset("tokens")
	if err != nil {
		return "", "", "", err
	}
	labs, err := asset("labs")
	if err != nil {
		return "", "", "", err
	}
	admin, err := asset("admin")
	if err != nil {
		return "", "", "", err
	}
	return tokens, labs, admin, nil
}

func staticMIME(relative string) string {
	switch strings.ToLower(filepath.Ext(relative)) {
	case ".css":
		return "text/css; charset=utf-8"
	case ".js", ".mjs":
		return "text/javascript; charset=utf-8"
	case ".json":
		return "application/json; charset=utf-8"
	default:
		return ""
	}
}

func (h *UIHandler) serve(w http.ResponseWriter, r *http.Request, relative, mime string) {
	file := filepath.Join(h.dist, filepath.FromSlash(relative))
	clean := filepath.Clean(file)
	root := filepath.Clean(h.dist) + string(filepath.Separator)
	if !strings.HasPrefix(clean, root) {
		http.NotFound(w, r)
		return
	}
	if mime != "" {
		w.Header().Set("Content-Type", mime)
	}
	w.Header().Set("Cache-Control", "no-store")
	data, err := os.ReadFile(clean)
	if err != nil {
		http.Error(w, "external effects UI unavailable", http.StatusServiceUnavailable)
		return
	}
	_, _ = w.Write(data)
}
