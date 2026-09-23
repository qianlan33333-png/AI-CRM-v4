package webshell

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// DistDistributionAdminAssets returns only the two named runtime files that
// the V3 build declares for the embedded admin root. It deliberately does not
// trust a whole standalone document as the page shell.
func DistDistributionAdminAssets(distRoot string) (DistributionAssets, bool) {
	if distRoot == "" {
		return DistributionAssets{}, false
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(distRoot, "asset-manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return DistributionAssets{}, false
	}
	css, drawerCSS, js := manifest.Entries["distributionStyles"], manifest.Entries["sharedDetailDrawerStyles"], manifest.Entries["distributionAdmin"]
	if !strings.HasPrefix(css, "assets/") || !strings.HasSuffix(css, ".css") || !strings.HasPrefix(drawerCSS, "assets/") || !strings.HasSuffix(drawerCSS, ".css") || !strings.HasPrefix(js, "assets/") || !strings.HasSuffix(js, ".js") || manifest.Files[css] == nil || manifest.Files[drawerCSS] == nil || manifest.Files[js] == nil {
		return DistributionAssets{}, false
	}
	return DistributionAssets{CSS: "/" + css, DetailDrawerCSS: "/" + drawerCSS, AdminJS: "/" + js}, true
}

// DistOverviewAdminAssets resolves the V3 overview stylesheet and Host module
// recorded in the release manifest. A missing or malformed pair leaves the
// generic administrator shell available rather than falling back to a frozen
// home document.
func DistOverviewAdminAssets(distRoot string) (OverviewAssets, bool) {
	if distRoot == "" {
		return OverviewAssets{}, false
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(distRoot, "asset-manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return OverviewAssets{}, false
	}
	asset := func(name, suffix string) (string, bool) {
		entry := manifest.Entries[name]
		if !strings.HasPrefix(entry, "assets/") || path.Clean(entry) != entry || !strings.HasSuffix(entry, suffix) || manifest.Files[entry] == nil {
			return "", false
		}
		if info, statErr := os.Stat(filepath.Join(distRoot, filepath.FromSlash(entry))); statErr != nil || info.IsDir() {
			return "", false
		}
		return "/" + entry, true
	}
	css, ok := asset("overviewStyles", ".css")
	if !ok {
		return OverviewAssets{}, false
	}
	drawerCSS, ok := asset("sharedDetailDrawerStyles", ".css")
	if !ok {
		return OverviewAssets{}, false
	}
	js, ok := asset("overviewAdmin", ".js")
	if !ok {
		return OverviewAssets{}, false
	}
	return OverviewAssets{CSS: css, DetailDrawerCSS: drawerCSS, AdminJS: js}, true
}

func DistGovernanceAdminAssets(distRoot string) (OverviewAssets, bool) {
	if distRoot == "" {
		return OverviewAssets{}, false
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(distRoot, "asset-manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return OverviewAssets{}, false
	}
	asset := func(name, suffix string) (string, bool) {
		entry := manifest.Entries[name]
		if !strings.HasPrefix(entry, "assets/") || path.Clean(entry) != entry || !strings.HasSuffix(entry, suffix) || manifest.Files[entry] == nil {
			return "", false
		}
		if info, statErr := os.Stat(filepath.Join(distRoot, filepath.FromSlash(entry))); statErr != nil || info.IsDir() {
			return "", false
		}
		return "/" + entry, true
	}
	css, ok := asset("governanceStyles", ".css")
	if !ok {
		return OverviewAssets{}, false
	}
	drawerCSS, ok := asset("sharedDetailDrawerStyles", ".css")
	if !ok {
		return OverviewAssets{}, false
	}
	js, ok := asset("governanceAdmin", ".js")
	if !ok {
		return OverviewAssets{}, false
	}
	return OverviewAssets{CSS: css, DetailDrawerCSS: drawerCSS, AdminJS: js}, true
}

// DistComponentStatesAssets resolves the complete V3-owned style and Host
// closure for the authenticated state-demo route. Frozen group/material paint
// is referenced only by its staged manifest paths; this function never reads
// a donor checkout or a business module.
func DistComponentStatesAssets(distRoot string) (ComponentStatesAssets, bool) {
	if distRoot == "" {
		return ComponentStatesAssets{}, false
	}
	var manifest struct {
		Entries map[string]string          `json:"entries"`
		Files   map[string]json.RawMessage `json:"files"`
	}
	raw, err := os.ReadFile(filepath.Join(distRoot, "asset-manifest.json"))
	if err != nil || json.Unmarshal(raw, &manifest) != nil {
		return ComponentStatesAssets{}, false
	}
	asset := func(name, suffix string) (string, bool) {
		entry := manifest.Entries[name]
		if !strings.HasPrefix(entry, "assets/") || path.Clean(entry) != entry || !strings.HasSuffix(entry, suffix) || manifest.Files[entry] == nil {
			return "", false
		}
		if info, statErr := os.Stat(filepath.Join(distRoot, filepath.FromSlash(entry))); statErr != nil || info.IsDir() {
			return "", false
		}
		return "/" + entry, true
	}
	staticAsset := func(entry, suffix string) (string, bool) {
		if !strings.HasPrefix(entry, "assets/") || path.Clean(entry) != entry || !strings.HasSuffix(entry, suffix) || manifest.Files[entry] == nil {
			return "", false
		}
		if info, statErr := os.Stat(filepath.Join(distRoot, filepath.FromSlash(entry))); statErr != nil || info.IsDir() {
			return "", false
		}
		return "/" + entry, true
	}
	visualTokens, ok := asset("sharedVisualTokens", ".css")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	styles, ok := asset("componentStatesStyles", ".css")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	dialog, ok := asset("selectionDialogStyles", ".css")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	groupOps, ok := asset("groupopsStyles", ".css")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	host, ok := asset("componentStatesHost", ".js")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	groupPicker, ok := staticAsset("assets/standard-components/group_chat_picker.css", ".css")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	materialPicker, ok := staticAsset("assets/standard-components/material_picker.css", ".css")
	if !ok {
		return ComponentStatesAssets{}, false
	}
	return ComponentStatesAssets{VisualTokensCSS: visualTokens, StylesCSS: styles, SelectionDialogCSS: dialog, GroupOpsCSS: groupOps, GroupPickerCSS: groupPicker, MaterialPickerCSS: materialPicker, HostJS: host}, true
}

// The new-shell frontend builds every admin screen as a standalone document
// under web/dist/admin.  When the composition root supplies a dist directory,
// the shell serves those built documents directly instead of the neutral
// placeholder, so every registry screen keeps its real API wiring with no
// donor-template re-wrapping.  Module-owned mounts (orders, coupons, survey,
// channel, …) are registered earlier in the composition mux and never reach
// this fallback.

// distAdminAliases maps vanity request paths to built admin document names.
// Only stable, nav-visible aliases belong here.  Several legacy module routes
// converge onto the built new-shell documents because the current frontend
// build no longer emits the donor-era per-module host bundles.
var distAdminAliases = map[string]string{
	"/admin/index.html":      "index.html",
	"/admin/owner-migration": "ownerMig.html",
	// The V3 Open Platform Host mounts inside this frozen admin document.
	"/admin/api-docs": "apidocs.html",
	// Operation cycles (frozen host bundle retired).
	"/admin/operation-cycles":                   "cycles.html",
	"/admin/operation-cycles/cycles.html":       "cycles.html",
	"/admin/operation-cycles/cyclesDetail.html": "cyclesDetail.html",
	// Channel center (frozen host bundle retired).
	"/admin/channels":     "channels.html",
	"/admin/channels/new": "channelForm.html",
	// WeCom tags (frozen staging document retired).
	"/admin/wecom-tags":   "wecom-tags.html",
	"/admin/distribution": "distribution.html",
	// Product and service-period product workspaces (frozen host bundle retired).
	"/admin/wechat-pay/products":                        "products.html",
	"/admin/wechat-pay/products/":                       "products.html",
	"/admin/wechat-pay/products.html":                   "products.html",
	"/admin/wechat-pay/products/new":                    "productForm.html",
	"/admin/wechat-pay/spProducts.html":                 "spProducts.html",
	"/admin/wechat-pay/spProductForm.html":              "spProductForm.html",
	"/admin/wechat-pay/spProductData.html":              "spProductData.html",
	"/admin/wechat-pay/products/spProductData.html":     "spProductData.html",
	"/admin/service-period-products":                    "spProducts.html",
	"/admin/service-period-products/":                   "spProducts.html",
	"/admin/service-period-products/new":                "spProductForm.html",
	"/admin/service-period-products/spProductData.html": "spProductData.html",
	// AI assistant (frozen host bundle retired).
	"/admin/cloud-orchestrator/plans": "ai.html",
	// Group Ops (frozen readonly bundle retired).
	"/admin/automation-conversion/group-ops/ui":        "groupops.html",
	"/admin/automation-conversion/group-ops/groups/ui": "groupops.html",
}

// DistAdminPageName resolves an /admin request path to a built standalone
// admin document name.  Only the single-segment "<name>.html" shape and the
// explicit vanity aliases are eligible; nested module paths stay with their
// domain owners.
func DistAdminPageName(requestPath string) (string, bool) {
	if name, ok := distAdminAliases[requestPath]; ok {
		return name, true
	}
	if !strings.HasPrefix(requestPath, "/admin/") || !strings.HasSuffix(requestPath, ".html") {
		return "", false
	}
	name := strings.TrimPrefix(requestPath, "/admin/")
	if name == "" || name != path.Base(name) || strings.Contains(name, "..") {
		return "", false
	}
	return name, true
}

// DistAdminPageFile resolves the built document on disk.  It returns false
// when no dist root is configured or the document is absent, so callers can
// fall back to the template shell.
func DistAdminPageFile(distRoot, requestPath string) (string, bool) {
	name, ok := DistAdminPageName(requestPath)
	if !ok || distRoot == "" {
		return "", false
	}
	file := filepath.Join(distRoot, "admin", filepath.FromSlash(name))
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return "", false
	}
	return file, true
}

// DistDistributionPageFile resolves the built current-session distributor center.
// Its data APIs remain owned by Distribution; the shell only serves its public document.
func DistDistributionPageFile(distRoot string) (string, bool) {
	if distRoot == "" {
		return "", false
	}
	file := filepath.Join(distRoot, "distribution", "index.html")
	info, err := os.Stat(file)
	if err != nil || info.IsDir() {
		return "", false
	}
	return file, true
}

// serveDistAdminPage streams one built admin document.  The documents are
// static build output; they carry no per-request data, so no-store keeps the
// shell consistent with the rest of the admin surface while the hashed
// runtime assets under /assets remain cacheable.
func serveDistAdminPage(writer http.ResponseWriter, request *http.Request, file string) {
	content, err := os.ReadFile(file)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(content)
}

// distSidebarDocument loads the built sidebar workbench and rewrites its
// hashed asset URLs onto the unauthenticated /sidebar-assets prefix: the
// sidebar runs inside the WeCom webview without an admin session, so it
// cannot share the session-gated /assets handler.
func distSidebarDocument(distRoot string) ([]byte, error) {
	if distRoot == "" {
		return nil, os.ErrNotExist
	}
	content, err := os.ReadFile(filepath.Join(distRoot, "sidebar", "index.html"))
	if err != nil {
		return nil, err
	}
	content = bytes.ReplaceAll(content, []byte(`"../assets/`), []byte(`"/sidebar-assets/`))
	return content, nil
}

// serveSidebarAsset serves one built sidebar runtime asset below dist/assets.
// Sidebar assets are static client code with content-hashed names; the WeCom
// webview fetches them without an admin session.
func (handler *Handler) serveSidebarAsset(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.distDir == "" {
		http.NotFound(writer, request)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	relative := cleanStaticPath(strings.TrimPrefix(request.URL.Path, "/sidebar-assets/"))
	if relative == "" || relative == "." || strings.HasPrefix(relative, "../") || relative == ".." {
		http.NotFound(writer, request)
		return
	}
	root := filepath.Join(handler.distDir, "assets")
	file := filepath.Join(root, filepath.FromSlash(relative))
	clean := filepath.Clean(file)
	if clean != root && !strings.HasPrefix(clean, filepath.Clean(root)+string(filepath.Separator)) {
		http.NotFound(writer, request)
		return
	}
	handle, err := os.Open(clean)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer handle.Close()
	info, err := handle.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(writer, request)
		return
	}
	content, err := io.ReadAll(handle)
	if err != nil {
		http.Error(writer, "unable to read sidebar asset", http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(info.Name())))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	http.ServeContent(writer, request, info.Name(), time.Time{}, bytes.NewReader(content))
}
