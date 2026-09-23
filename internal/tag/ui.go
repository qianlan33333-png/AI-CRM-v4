package tag

import (
	"encoding/json"
	"errors"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type TagsAssets struct{ TokensCSS, LabsCSS, AdminJS, PageHeaderActionHostJS string }
type TagsPageRenderer func(http.ResponseWriter, *http.Request, string, TagsAssets) error
type tagsUI struct {
	dist   string
	render TagsPageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render TagsPageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &tagsUI{dist: dist, render: render}
}
func (h *tagsUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", http.MethodGet+", "+http.MethodHead)
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.URL.Path != "/admin/wecom-tags" {
		http.NotFound(w, r)
		return
	}
	if !validTagsQuery(r) {
		http.Redirect(w, r, "/admin/wecom-tags", http.StatusSeeOther)
		return
	}
	raw, e := os.ReadFile(filepath.Join(h.dist, "admin", "tags.html"))
	if e != nil {
		http.Error(w, "tag UI unavailable", 503)
		return
	}
	body, e := donortemplate.Extract(string(raw))
	if e != nil {
		http.Error(w, "tag UI unavailable", 503)
		return
	}
	assets, e := tagAssets(h.dist)
	if e != nil {
		http.Error(w, "tag UI unavailable", 503)
		return
	}
	if e = h.render(w, r, body, assets); e != nil {
		http.Error(w, "tag UI unavailable", 500)
	}
}
func tagAssets(dist string) (TagsAssets, error) {
	raw, e := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if e != nil {
		return TagsAssets{}, e
	}
	var manifest struct {
		Entries map[string]string `json:"entries"`
	}
	if e = json.Unmarshal(raw, &manifest); e != nil {
		return TagsAssets{}, e
	}
	get := func(name string) (string, error) {
		v := manifest.Entries[name]
		if v == "" || strings.Contains(v, "..") || !strings.HasPrefix(v, "assets/") {
			return "", errors.New("tag bundle asset missing")
		}
		if _, e = os.Stat(filepath.Join(dist, v)); e != nil {
			return "", e
		}
		return "/" + v, nil
	}
	tokens, e := get("tokens")
	if e != nil {
		return TagsAssets{}, e
	}
	labs, e := get("labs")
	if e != nil {
		return TagsAssets{}, e
	}
	admin, e := get("admin")
	if e != nil {
		return TagsAssets{}, e
	}
	pageHeaderActions, e := get("pageHeaderActionHost")
	if e != nil {
		return TagsAssets{}, e
	}
	return TagsAssets{tokens, labs, admin, pageHeaderActions}, nil
}

// The frozen donor opens a tag detail through ?id=<positive>. Preserve that
// query exactly so its controller can issue the original detail/gate reads;
// all other query shapes are normalized away.
func validTagsQuery(r *http.Request) bool {
	values := r.URL.Query()
	if len(values) == 0 {
		return true
	}
	ids, ok := values["id"]
	if !ok || len(ids) != 1 || len(values) != 1 {
		return false
	}
	id, err := strconv.ParseInt(ids[0], 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == ids[0]
}
