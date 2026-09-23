package operationcycle

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type UIAssets struct{ TokensCSS, LabsCSS, HostJS string }
type PageRenderer func(http.ResponseWriter, *http.Request, string, string, UIAssets) error
type uiHandler struct {
	dist   string
	render PageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render PageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &uiHandler{dist: dist, render: render}
}

func (h *uiHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	page, canonical := operationCyclePage(r.URL.Path)
	if page == "" {
		http.NotFound(w, r)
		return
	}
	query := r.URL.Query()
	if page == "cycles" && len(query) != 0 {
		http.Redirect(w, r, "/admin/operation-cycles", http.StatusSeeOther)
		return
	}
	if page == "cyclesDetail" && (len(query) != 1 || len(query["id"]) != 1 || !validUIOrdinal(query.Get("id"))) {
		http.Redirect(w, r, "/admin/operation-cycles", http.StatusSeeOther)
		return
	}
	if canonical {
		// The donor controller produces relative cycles.html URLs. They are
		// mounted below the one v3 route, never as an independently served v2
		// document, and the numeric value remains a display ordinal only.
		if page == "cycles" {
			http.Redirect(w, r, "/admin/operation-cycles", http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/admin/operation-cycles/cyclesDetail.html?id="+query.Get("id"), http.StatusSeeOther)
		}
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusServiceUnavailable)
		return
	}
	templateBody, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := operationCycleAssets(h.dist)
	if err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "operation cycle UI unavailable", http.StatusInternalServerError)
	}
}

func operationCyclePage(path string) (page string, canonical bool) {
	switch path {
	case "/admin/operation-cycles":
		return "cycles", false
	case "/admin/operation-cycles/cycles.html":
		return "cycles", true
	case "/admin/operation-cycles/cyclesDetail.html":
		return "cyclesDetail", false
	default:
		return "", false
	}
}

func validUIOrdinal(value string) bool {
	if value == "" || len(value) > 9 || strings.TrimSpace(value) != value {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return value != "0"
}

func operationCycleAssets(dist string) (UIAssets, error) {
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
	asset := func(name string) (string, error) {
		value := manifest.Entries[name]
		if value == "" || strings.Contains(value, "..") || !strings.HasPrefix(value, "assets/") {
			return "", errors.New("operation cycle asset missing")
		}
		if _, statErr := os.Stat(filepath.Join(dist, filepath.FromSlash(value))); statErr != nil {
			return "", statErr
		}
		return "/" + value, nil
	}
	tokens, err := asset("tokens")
	if err != nil {
		return UIAssets{}, err
	}
	labs, err := asset("labs")
	if err != nil {
		return UIAssets{}, err
	}
	host, err := asset("operationCyclesHost")
	if err != nil {
		return UIAssets{}, err
	}
	return UIAssets{TokensCSS: tokens, LabsCSS: labs, HostJS: host}, nil
}
