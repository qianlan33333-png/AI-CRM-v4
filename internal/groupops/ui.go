package groupops

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

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

// GroupOpsPageRenderer is the v3-owned shell adapter. The donor template and
// manifest-derived asset URLs are read-only values from the built donor
// release; this module never edits or recompiles donor business files.
type GroupOpsPageRenderer func(http.ResponseWriter, *http.Request, string, string, GroupOpsAssets) error

type GroupOpsAssets struct {
	TokensCSS, LabsCSS, AdminJS, ReadonlyCSS, ReadonlyJS string
	StandardCSS, HostJS, SelectionDialogCSS              string
	OperationPickerJS                                    string
	GroupPickerCSS, GroupPickerJS                        string
	MaterialPickerCSS, MaterialPickerJS                  string
	ComposerCSS, ComposerJS                              string
}

type groupOpsUI struct {
	dist   string
	render GroupOpsPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render GroupOpsPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &groupOpsUI{dist: dist, render: render}
}

func (h *groupOpsUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h == nil || h.render == nil {
		http.NotFound(w, r)
		return
	}
	if strings.HasPrefix(r.URL.Path, "/groupops-assets/") {
		h.asset(w, r)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// The admin menu keeps this historical canonical target. Serve the V3
	// standard Host only from that single document route so the legacy alias
	// cannot create a second independently mounted workspace.
	if r.URL.Path == "/admin/automation-conversion/group-ops/ui" {
		target := "/admin/groupops.html"
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
		return
	}
	page, ok := groupOpsPage(r.URL.Path)
	if !ok || !validGroupOpsQuery(r, page) {
		http.NotFound(w, r)
		return
	}
	// History remains a read-only frozen donor view. The active plan pages use
	// the standard Group Ops DOM directly and receive only this V3 transport
	// host; neither path uses request-controlled markup.
	if r.URL.Query().Get("history") != "1" {
		assets, err := h.assets(true)
		if err != nil {
			http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
			return
		}
		mode, planID := standardPageMode(r, page)
		host := `<div id="group-ops-app" class="group-ops" data-group-ops-standard-host="true" data-page-mode="` + mode + `"`
		if planID != "" {
			host += ` data-plan-id="` + planID + `"`
		}
		host += `></div>`
		if err = h.render(w, r, page, host, assets); err != nil {
			http.Error(w, "group ops UI unavailable", http.StatusInternalServerError)
		}
		return
	}
	filename := page + ".html"
	raw, err := os.ReadFile(filepath.Join(h.dist, "admin", filename))
	if err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
		return
	}
	templateBody, err := donortemplate.Extract(string(raw))
	if err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
		return
	}
	assets, err := h.assets(false)
	if err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusServiceUnavailable)
		return
	}
	if err = h.render(w, r, page, templateBody, assets); err != nil {
		http.Error(w, "group ops UI unavailable", http.StatusInternalServerError)
	}
}

func standardPageMode(r *http.Request, page string) (string, string) {
	if r.URL.Path == "/admin/automation-conversion/group-ops/groups/ui" {
		return "groups", ""
	}
	if page != "groupopsDetail" {
		return "list", ""
	}
	if id := r.URL.Query().Get("id"); id != "" {
		return "detail", id
	}
	const prefix = "/admin/automation-conversion/group-ops/plans/"
	return "detail", strings.TrimPrefix(r.URL.Path, prefix)
}

func groupOpsPage(path string) (string, bool) {
	switch path {
	case "/admin/automation-conversion/group-ops/ui", "/admin/automation-conversion/group-ops/groups/ui", "/admin/groupops.html":
		return "groupops", true
	case "/admin/groupopsDetail.html":
		return "groupopsDetail", true
	}
	const prefix = "/admin/automation-conversion/group-ops/plans/"
	if strings.HasPrefix(path, prefix) {
		id := strings.TrimPrefix(path, prefix)
		if _, err := strconv.ParseInt(id, 10, 64); err == nil && positiveCanonicalID(id) {
			return "groupopsDetail", true
		}
	}
	return "", false
}

func validGroupOpsQuery(r *http.Request, page string) bool {
	values := r.URL.Query()
	if len(values) == 0 {
		return true
	}
	if page == "groupops" {
		history, ok := values["history"]
		return ok && len(values) == 1 && len(history) == 1 && history[0] == "1"
	}
	allowed := map[string]bool{"id": true, "history": true}
	for key := range values {
		if !allowed[key] || len(values[key]) != 1 {
			return false
		}
	}
	if history, ok := values["history"]; ok && history[0] != "1" {
		return false
	}
	if raw, ok := values["id"]; ok {
		return positiveCanonicalID(raw[0])
	}
	return values.Get("history") == "1"
}

func positiveCanonicalID(value string) bool {
	if value == "" || len(value) > 19 || (len(value) > 1 && value[0] == '0') {
		return false
	}
	number, err := strconv.ParseInt(value, 10, 64)
	return err == nil && number > 0
}

func (h *groupOpsUI) assets(standard bool) (GroupOpsAssets, error) {
	raw, err := os.ReadFile(filepath.Join(h.dist, "asset-manifest.json"))
	if err != nil {
		return GroupOpsAssets{}, err
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
		Files   map[string]struct {
			SHA256 string `json:"sha256"`
		} `json:"files"`
	}
	if err = json.Unmarshal(raw, &manifest); err != nil {
		return GroupOpsAssets{}, err
	}
	get := func(value string) (string, error) {
		if value == "" || strings.Contains(value, "..") || strings.HasPrefix(value, "/") {
			return "", errors.New("group ops bundle asset missing")
		}
		if _, ok := manifest.Files[value]; !ok {
			return "", errors.New("group ops bundle asset is not in manifest")
		}
		if _, err := os.Stat(filepath.Join(h.dist, value)); err != nil {
			return "", err
		}
		return "/groupops-assets/" + value, nil
	}
	getVersioned := func(value string) (string, error) {
		asset, getErr := get(value)
		if getErr != nil {
			return "", getErr
		}
		sum := manifest.Files[value].SHA256
		if len(sum) != 64 {
			return asset, nil
		}
		return asset + "?v=" + sum[:16], nil
	}
	entry := func(name string) (string, error) {
		value := manifest.Entries[name]
		if !strings.HasPrefix(value, "assets/") {
			return "", errors.New("group ops bundle asset missing")
		}
		return get(value)
	}
	tokens, err := entry("tokens")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	labs, err := entry("labs")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	admin, err := entry("admin")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	readonlyCSS, err := get("aiassistant/send_content_readonly_detail.css")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	readonlyJS, err := get("aiassistant/send_content_readonly_detail.js")
	if err != nil {
		return GroupOpsAssets{}, err
	}
	assets := GroupOpsAssets{TokensCSS: tokens, LabsCSS: labs, AdminJS: admin, ReadonlyCSS: readonlyCSS, ReadonlyJS: readonlyJS}
	if !standard {
		return assets, nil
	}
	if assets.StandardCSS, err = entry("groupopsStyles"); err != nil {
		return GroupOpsAssets{}, err
	}
	if assets.HostJS, err = entry("groupopsHost"); err != nil {
		return GroupOpsAssets{}, err
	}
	if assets.SelectionDialogCSS, err = entry("selectionDialogStyles"); err != nil {
		return GroupOpsAssets{}, err
	}
	for name, target := range map[string]*string{
		"assets/standard-components/operation_member_picker.js": &assets.OperationPickerJS,
		"assets/standard-components/group_chat_picker.css":      &assets.GroupPickerCSS, "assets/standard-components/group_chat_picker.js": &assets.GroupPickerJS,
		"assets/standard-components/material_picker.css": &assets.MaterialPickerCSS, "assets/standard-components/material_picker.js": &assets.MaterialPickerJS,
		"assets/standard-components/send_content_composer.css": &assets.ComposerCSS, "assets/standard-components/send_content_composer.js": &assets.ComposerJS,
	} {
		if name == "assets/standard-components/operation_member_picker.js" {
			*target, err = getVersioned(name)
		} else {
			*target, err = get(name)
		}
		if err != nil {
			return GroupOpsAssets{}, err
		}
	}
	return assets, nil
}

func (h *groupOpsUI) asset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	relative := strings.TrimPrefix(r.URL.Path, "/groupops-assets/")
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
