package http

import (
	"embed"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
)

// memberGridDonorFiles is the byte-frozen dd8 member-grid page. It is embedded
// in the aicrm binary so the release artifact never depends on a checkout
// directory. member_grid_host.js is the separate v3-only browser seam.
//
//go:embed member_grid_donor/templates/* member_grid_donor/static/admin_console/* member_grid_donor/static/icons/* member_grid_donor/SHA256SUMS
var memberGridDonorFiles embed.FS

func memberGridDonorContent(relative string) ([]byte, error) {
	return memberGridDonorFiles.ReadFile("member_grid_donor/" + relative)
}

// MemberGridUI serves the dd8-frozen static page resources and its public
// share document. It contains no business decisions: the companion Host is
// the only v3-owned browser seam and all data still goes through Product HTTP.
type MemberGridUI struct{}

func NewMemberGridUI() http.Handler { return MemberGridUI{} }

func (MemberGridUI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	requestPath := strings.TrimSuffix(r.URL.Path, "/")
	switch {
	case strings.HasPrefix(requestPath, "/service-period-member-grid-assets/"):
		serveMemberGridAsset(w, r, strings.TrimPrefix(requestPath, "/service-period-member-grid-assets/"))
	case strings.HasPrefix(requestPath, "/static/service-period/icons/"):
		serveMemberGridIcon(w, r, strings.TrimPrefix(requestPath, "/static/service-period/icons/"))
	case requestPath == "/shared/service-period-member-grid":
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			methodNotAllowed(w, http.MethodGet+", "+http.MethodHead)
			return
		}
		writeMemberGridPublicDocument(w, r)
	default:
		http.NotFound(w, r)
	}
}

func RenderMemberGridInternal(w http.ResponseWriter, r *http.Request, productID string) error {
	if !positiveMemberGridID(productID) {
		return fs.ErrNotExist
	}
	content, err := memberGridContent("templates/service_period_member_grid.html")
	if err != nil {
		return err
	}
	content = strings.ReplaceAll(content, "{{ service_product_id | e }}", productID)
	return writeMemberGridDocument(w, r, "周期商品会员数据", "sp-compact-admin-shell", content, true)
}

func writeMemberGridPublicDocument(w http.ResponseWriter, r *http.Request) {
	raw, err := memberGridDonorContent("templates/service_period_member_grid_public.html")
	if err != nil {
		http.Error(w, "member grid unavailable", http.StatusServiceUnavailable)
		return
	}
	doc := strings.ReplaceAll(string(raw), "{{ page_title }}", "周期商品数据")
	doc = replaceMemberGridPaths(doc, false)
	w.Header().Set("Cache-Control", "no-store, private, max-age=0")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, doc)
	}
}

func writeMemberGridDocument(w http.ResponseWriter, r *http.Request, title, bodyClass, content string, internal bool) error {
	doc := "<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"utf-8\"><meta name=\"viewport\" content=\"width=device-width, initial-scale=1\"><title>" + title + "</title><link rel=\"stylesheet\" href=\"/service-period-member-grid-assets/member_grid.css\"></head><body class=\"" + bodyClass + "\"><header class=\"sp-compact-header\"><div><span class=\"sp-compact-kicker\">周期商品数据工作区</span><h1>" + title + "</h1></div></header><main class=\"sp-compact-main\">" + content + "</main><script defer src=\"/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948\"></script><script defer src=\"/service-period-member-grid-assets/member_grid_host.js\"></script><script defer src=\"/service-period-member-grid-assets/member_grid_state.js\"></script><script defer src=\"/service-period-member-grid-assets/member_grid_share.js\"></script><script defer src=\"/service-period-member-grid-assets/member_grid.js\"></script></body></html>"
	_ = internal
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if r.Method != http.MethodHead {
		_, err := io.WriteString(w, doc)
		return err
	}
	return nil
}

func memberGridContent(relative string) (string, error) {
	raw, err := memberGridDonorContent(relative)
	if err != nil {
		return "", err
	}
	start := strings.Index(string(raw), "{% block content %}")
	if start < 0 {
		return "", fs.ErrInvalid
	}
	start += len("{% block content %}")
	end := strings.Index(string(raw)[start:], "{% endblock %}")
	if end < 0 {
		return "", fs.ErrInvalid
	}
	return string(raw)[start : start+end], nil
}

func replaceMemberGridPaths(value string, internal bool) string {
	value = strings.ReplaceAll(value, "/static/service-period/admin_console/member_grid.css?v=20260716-sharing-1", "/service-period-member-grid-assets/member_grid.css")
	stateScript := "/service-period-member-grid-assets/member_grid_state.js"
	if !internal {
		// The public dd8 page also needs the V3 response seam so an unavailable
		// renewal count remains unavailable instead of being rendered as zero.
		stateScript = "/service-period-member-grid-assets/member_grid_host.js\"></script><script defer src=\"" + stateScript
	}
	value = strings.ReplaceAll(value, "/static/service-period/admin_console/member_grid_state.js?v=20260716-sharing-1", stateScript)
	value = strings.ReplaceAll(value, "/static/service-period/admin_console/member_grid.js?v=20260716-sharing-1", "/service-period-member-grid-assets/member_grid.js")
	return value
}

func serveMemberGridAsset(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet+", "+http.MethodHead)
		return
	}
	if name == "member_grid_host.js" {
		w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		if r.Method != http.MethodHead {
			_, _ = w.Write(memberGridHostJS)
		}
		return
	}
	allowed := map[string]string{"member_grid.js": "member_grid.js", "member_grid_state.js": "member_grid_state.js", "member_grid_share.js": "member_grid_share.js", "member_grid.css": "member_grid.css"}
	file, ok := allowed[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	raw, err := memberGridDonorContent("static/admin_console/" + file)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(file))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}

var memberGridIconNames = map[string]struct{}{
	"bars-3-bottom-left.svg": {}, "bars-arrow-down.svg": {}, "calendar-days.svg": {},
	"chart-bar.svg": {}, "check-circle.svg": {}, "chevron-down.svg": {},
	"ellipsis-horizontal.svg": {}, "funnel.svg": {}, "globe-alt.svg": {},
	"hashtag.svg": {}, "link.svg": {}, "plus.svg": {}, "rectangle-group.svg": {},
	"table-cells.svg": {}, "user-plus.svg": {}, "user.svg": {}, "x-mark.svg": {},
}

func serveMemberGridIcon(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		methodNotAllowed(w, http.MethodGet+", "+http.MethodHead)
		return
	}
	if _, ok := memberGridIconNames[name]; !ok {
		http.NotFound(w, r)
		return
	}
	raw, err := memberGridDonorContent("static/icons/" + name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if r.Method != http.MethodHead {
		_, _ = w.Write(raw)
	}
}

func positiveMemberGridID(value string) bool {
	id, err := strconv.ParseInt(value, 10, 64)
	return err == nil && id > 0 && strconv.FormatInt(id, 10) == value
}
