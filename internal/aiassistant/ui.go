package aiassistant

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type PageRenderer func(http.ResponseWriter, *http.Request, string, string, Assets) error
type Assets struct {
	TokensCSS, LabsCSS, GroupCSS, MaterialCSS, ComposerCSS, ReadonlyCSS, HostJS, PageHeaderActionHostJS string
	DonorScripts                                                                                        []string
}
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
	if strings.HasPrefix(r.URL.Path, "/ai-assistant-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path == "/admin/ai.html" {
		http.Redirect(w, r, "/admin/cloud-orchestrator/plans", http.StatusFound)
		return
	}
	if r.URL.Path == "/admin/aiDetail.html" {
		id := r.URL.Query().Get("id")
		if validID(id) {
			http.Redirect(w, r, "/admin/cloud-orchestrator/plans/"+id, http.StatusFound)
			return
		}
		http.NotFound(w, r)
		return
	}
	mode, id, ok := page(r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "aiassistant", mode+".html"))
	if err != nil {
		http.Error(w, "AI Assistant UI unavailable", http.StatusServiceUnavailable)
		return
	}
	fragment := strings.ReplaceAll(string(raw), "__PLAN_ID__", id)
	assets, err := h.assets()
	if err != nil {
		http.Error(w, "AI Assistant UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, mode, fragment, assets); err != nil {
		http.Error(w, "AI Assistant UI unavailable", http.StatusInternalServerError)
	}
}

func page(path string) (string, string, bool) {
	if path == "/admin/cloud-orchestrator/plans" || path == "/admin/cloud-orchestrator/plans/" {
		return "list", "", true
	}
	prefix := "/admin/cloud-orchestrator/plans/"
	if !strings.HasPrefix(path, prefix) {
		return "", "", false
	}
	id := strings.TrimPrefix(path, prefix)
	if !validID(id) {
		return "", "", false
	}
	return "detail", id, true
}
func validID(value string) bool {
	if value == "" || (len(value) > 1 && value[0] == '0') {
		return false
	}
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0
}

func (h *uiHandler) assets() (Assets, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return Assets{}, err
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	if json.Unmarshal(raw, &manifest) != nil {
		return Assets{}, errors.New("invalid manifest")
	}
	get := func(relative string) (string, error) {
		if relative == "" || strings.Contains(relative, "..") {
			return "", errors.New("asset missing")
		}
		if _, ok := manifest.Files[relative]; !ok {
			return "", errors.New("asset not manifested")
		}
		if _, err := os.Stat(filepath.Join(h.dist, relative)); err != nil {
			return "", err
		}
		return "/ai-assistant-assets/" + relative, nil
	}
	tokens, err := get(manifest.Entries["tokens"])
	if err != nil {
		return Assets{}, err
	}
	labs, err := get(manifest.Entries["labs"])
	if err != nil {
		return Assets{}, err
	}
	host, err := get(manifest.Entries["aiAssistantHost"])
	if err != nil {
		return Assets{}, err
	}
	pageHeaderActions, err := get(manifest.Entries["pageHeaderActionHost"])
	if err != nil {
		return Assets{}, err
	}
	group, err := get("assets/standard-components/group_chat_picker.css")
	if err != nil {
		return Assets{}, err
	}
	material, err := get("assets/standard-components/material_picker.css")
	if err != nil {
		return Assets{}, err
	}
	composer, err := get("assets/standard-components/send_content_composer.css")
	if err != nil {
		return Assets{}, err
	}
	readonly, _ := get("aiassistant/send_content_readonly_detail.css")
	scripts := []string{}
	for _, name := range []string{"assets/standard-components/group_chat_picker.js", "assets/standard-components/material_picker.js", "assets/standard-components/send_content_composer.js", "aiassistant/send_content_readonly_detail.js", "aiassistant/cloud_plan_review.js"} {
		value, e := get(name)
		if e != nil {
			return Assets{}, e
		}
		scripts = append(scripts, value)
	}
	return Assets{tokens, labs, group, material, composer, readonly, host, pageHeaderActions, scripts}, nil
}
func (h *uiHandler) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", 405)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/ai-assistant-assets/")
	if relative == "" || strings.Contains(relative, "..") || strings.HasPrefix(relative, "/") {
		http.NotFound(w, r)
		return
	}
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var manifest struct {
		Files map[string]json.RawMessage `json:"files"`
	}
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
	kind := mime.TypeByExtension(filepath.Ext(relative))
	if kind == "" {
		kind = "application/octet-stream"
	}
	w.Header().Set("Content-Type", kind)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	if r.Method == http.MethodGet {
		_, _ = io.Copy(w, file)
	}
}
