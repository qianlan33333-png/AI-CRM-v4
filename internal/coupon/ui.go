package coupon

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/qianlan33333-png/AI-CRM-v3/internal/platform/donortemplate"
)

type Assets struct{ TokensCSS, LabsCSS, AdminJS, HostJS string }

var couponEditPath = regexp.MustCompile(`^/admin/coupons/([1-9][0-9]*)/edit$`)

type PageRenderer func(http.ResponseWriter, *http.Request, string, string, Assets) error
type ui struct {
	dist   string
	render PageRenderer
}

func (m *ModuleRegistration) UIBinding(dist string, render PageRenderer) http.Handler {
	if m == nil || strings.TrimSpace(dist) == "" || render == nil {
		return http.NotFoundHandler()
	}
	return &ui{dist, render}
}
func (h *ui) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", 405)
		return
	}
	page, ok := pageFor(r)
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, e := os.ReadFile(filepath.Join(h.dist, "admin", page+".html"))
	if e != nil {
		http.Error(w, "coupon UI unavailable", 503)
		return
	}
	body, e := donortemplate.Extract(string(raw))
	if e != nil {
		http.Error(w, "coupon UI unavailable", 503)
		return
	}
	assets, e := couponAssets(h.dist)
	if e != nil {
		http.Error(w, "coupon UI unavailable", 503)
		return
	}
	if e = h.render(w, r, page, body, assets); e != nil {
		http.Error(w, "coupon UI unavailable", 500)
	}
}
func pageFor(r *http.Request) (string, bool) {
	if match := couponEditPath.FindStringSubmatch(r.URL.Path); len(match) == 2 {
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err == nil && id > 0 && strconv.FormatInt(id, 10) == match[1] && len(r.URL.Query()) == 0 {
			return "couponForm", true
		}
		return "", false
	}
	switch r.URL.Path {
	case "/admin/coupons", "/admin/coupons.html":
		return "coupons", len(r.URL.Query()) == 0
	case "/admin/couponForm.html":
		values := r.URL.Query()
		if len(values) == 0 {
			return "couponForm", true
		}
		ids, ok := values["id"]
		if !ok || len(values) != 1 || len(ids) != 1 {
			return "", false
		}
		id, e := strconv.ParseInt(ids[0], 10, 64)
		return "couponForm", e == nil && id > 0 && strconv.FormatInt(id, 10) == ids[0]
	case "/admin/couponData.html":
		values := r.URL.Query()
		ids, ok := values["id"]
		if !ok || len(values) != 1 || len(ids) != 1 {
			return "", false
		}
		id, e := strconv.ParseInt(ids[0], 10, 64)
		return "couponData", e == nil && id > 0 && strconv.FormatInt(id, 10) == ids[0]
	default:
		return "", false
	}
}
func couponAssets(dist string) (Assets, error) {
	raw, e := os.ReadFile(filepath.Join(dist, "asset-manifest.json"))
	if e != nil {
		return Assets{}, e
	}
	var m struct {
		Entries map[string]string `json:"entries"`
	}
	if e = json.Unmarshal(raw, &m); e != nil {
		return Assets{}, e
	}
	get := func(k string) (string, error) {
		v := m.Entries[k]
		if v == "" || !strings.HasPrefix(v, "assets/") || strings.Contains(v, "..") {
			return "", errors.New("coupon bundle asset missing")
		}
		if _, e = os.Stat(filepath.Join(dist, v)); e != nil {
			return "", e
		}
		return "/" + v, nil
	}
	t, e := get("tokens")
	if e != nil {
		return Assets{}, e
	}
	l, e := get("labs")
	if e != nil {
		return Assets{}, e
	}
	a, e := get("admin")
	if e != nil {
		return Assets{}, e
	}
	h, e := get("couponHost")
	if e != nil {
		return Assets{}, e
	}
	return Assets{TokensCSS: t, LabsCSS: l, AdminJS: a, HostJS: h}, nil
}
