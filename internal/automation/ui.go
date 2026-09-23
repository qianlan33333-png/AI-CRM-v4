package automation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type AgentAssets struct {
	TokensCSS, LabsCSS, AdminJS                                                          string
	PresentationCSS, ContentCSS, SelectionDialogCSS, MaterialPickerCSS, MaterialPickerJS string
	ContentHostJS                                                                        string
}

// AgentPageBootstrap is v3-owned host data. It deliberately carries only a
// locally generated create-code suggestion; the frozen donor template and
// controller remain unchanged.
type AgentPageBootstrap struct{ CreateCode string }

type AgentPageRenderer func(http.ResponseWriter, *http.Request, string, string, AgentAssets, AgentPageBootstrap) error
type agentUI struct {
	dist   string
	render AgentPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render AgentPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &agentUI{dist, render}
}
func (h *agentUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	page, ok := agentPage(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	if !validAgentQuery(page, r) {
		http.Redirect(w, r, "/admin/automation-agents", http.StatusSeeOther)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if err != nil {
		http.Error(w, "automation UI unavailable", 503)
		return
	}
	tpl, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "automation UI unavailable", 503)
		return
	}
	assets, err := agentAssets(h.dist, page == "agentEdit")
	if err != nil {
		http.Error(w, "automation UI unavailable", 503)
		return
	}
	bootstrap := AgentPageBootstrap{}
	if page == "agentEdit" && r.URL.Query().Get("id") == "" {
		bootstrap.CreateCode, err = newCreateCode()
		if err != nil {
			http.Error(w, "automation UI unavailable", http.StatusServiceUnavailable)
			return
		}
	}
	if err = h.render(w, r, page, tpl, assets, bootstrap); err != nil {
		http.Error(w, "automation UI unavailable", 500)
	}
}

// newCreateCode supplies a legal, high-entropy default for the frozen create
// form. It reserves nothing: the Automation-owned PostgreSQL unique index is
// still the concurrency authority when the unchanged donor controller POSTs.
func newCreateCode() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "agent_" + hex.EncodeToString(raw), nil
}
func agentPage(path string) (string, bool) {
	switch path {
	case "/admin/automation-agents", "/admin/agents.html":
		return "agents", true
	case "/admin/agentEdit.html":
		return "agentEdit", true
	default:
		return "", false
	}
}
func validAgentQuery(page string, r *http.Request) bool {
	q := r.URL.Query()
	if page == "agents" {
		if len(q) == 0 {
			return true
		}
		v, ok := q["type"]
		return ok && len(q) == 1 && len(v) == 1 && (v[0] == "agent" || v[0] == "fixed_script")
	}
	if len(q) == 0 {
		return true
	}
	if types, ok := q["type"]; ok {
		return len(q) == 1 && len(types) == 1 && (types[0] == "agent" || types[0] == "fixed_script")
	}
	v, ok := q["id"]
	if !ok || len(v) != 1 || len(q) > 2 {
		return false
	}
	id, e := strconv.ParseInt(v[0], 10, 64)
	if e != nil || id < 1 || strconv.FormatInt(id, 10) != v[0] {
		return false
	}
	if len(q) == 1 {
		return true
	}
	saved, ok := q["saved"]
	return ok && len(saved) == 1 && saved[0] == "1"
}
func agentAssets(dist string, editor bool) (AgentAssets, error) {
	raw, e := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if e != nil {
		return AgentAssets{}, e
	}
	var m struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return AgentAssets{}, e
	}
	get := func(n string) (string, error) {
		v := m.Entries[n]
		if v == "" || !strings.HasPrefix(v, "assets/") || strings.Contains(v, "..") || m.Files[v] == nil {
			return "", errors.New("automation bundle asset missing")
		}
		if _, e = os.Stat(filepath.Join(dist, v)); e != nil {
			return "", e
		}
		return "/" + v, nil
	}
	t, e := get("tokens")
	if e != nil {
		return AgentAssets{}, e
	}
	l, e := get("labs")
	if e != nil {
		return AgentAssets{}, e
	}
	a, e := get("admin")
	if e != nil {
		return AgentAssets{}, e
	}
	assets := AgentAssets{TokensCSS: t, LabsCSS: l, AdminJS: a}
	if !editor {
		assets.AdminJS, e = get("automationLifecycleHost")
		return assets, e
	}
	for name, target := range map[string]*string{
		"presentationStyles":      &assets.PresentationCSS,
		"automationContentStyles": &assets.ContentCSS,
		"selectionDialogStyles":   &assets.SelectionDialogCSS,
		"automationContentHost":   &assets.ContentHostJS,
	} {
		value, err := get(name)
		if err != nil {
			return AgentAssets{}, err
		}
		*target = value
	}
	materialCSS, err := staticAutomationAsset(dist, m.Files, "assets/standard-components/material_picker.css")
	if err != nil {
		return AgentAssets{}, err
	}
	materialJS, err := staticAutomationAsset(dist, m.Files, "assets/standard-components/material_picker.js")
	if err != nil {
		return AgentAssets{}, err
	}
	assets.MaterialPickerCSS, assets.MaterialPickerJS = materialCSS, materialJS
	if assets.PresentationCSS == "" || assets.ContentCSS == "" || assets.SelectionDialogCSS == "" || assets.ContentHostJS == "" {
		return AgentAssets{}, errors.New("automation content assets missing")
	}
	return assets, nil
}

func staticAutomationAsset(dist string, files map[string]json.RawMessage, relative string) (string, error) {
	if !strings.HasPrefix(relative, "assets/") || strings.Contains(relative, "..") || files[relative] == nil {
		return "", errors.New("automation bundle asset missing")
	}
	if _, err := os.Stat(filepath.Join(dist, relative)); err != nil {
		return "", err
	}
	return "/" + relative, nil
}
