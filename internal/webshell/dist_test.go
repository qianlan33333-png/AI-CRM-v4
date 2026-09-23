package webshell

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newDistFixture builds a minimal built-frontend tree: one home document, one
// standalone screen, the sidebar workbench and one hashed runtime asset.
func newDistFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(relative, content string) {
		t.Helper()
		file := filepath.Join(root, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("admin/index.html", `<!doctype html><title>dist home</title><body data-page="index">新壳首页</body>`)
	write("admin/customers.html", `<!doctype html><title>dist customers</title><body data-page="customers">新壳用户列表</body>`)
	write("admin/automation.html", `<!doctype html><title>dist automation</title><body data-page="automation">新壳自动化运营</body>`)
	write("admin/cycles.html", `<!doctype html><title>dist cycles</title><body data-page="cycles">新壳运营闭环</body>`)
	write("admin/channels.html", `<!doctype html><title>dist channels</title><body data-page="channels">新壳渠道码中心</body>`)
	write("admin/wecom-tags.html", `<!doctype html><title>dist tags</title><body data-page="tags">新壳企微标签</body>`)
	write("admin/distribution.html", `<!doctype html><title>distribution admin</title><body data-page="distribution">分销管理</body>`)
	write("assets/distributionAdmin-test.js", `console.log("distribution")`)
	write("assets/distributionStyles-test.css", `.distribution-shell{}`)
	write("assets/sharedDetailDrawerStyles-test.css", `.shared-detail-drawer{}`)
	write("assets/overviewAdmin-test.js", `console.log("overview")`)
	write("assets/overviewStyles-test.css", `.overview-admin{}`)
	write("assets/sharedVisualTokens-test.css", `.visual-tokens{}`)
	write("assets/componentStatesStyles-test.css", `.component-states{}`)
	write("assets/selectionDialogStyles-test.css", `.selection-dialog{}`)
	write("assets/groupopsStyles-test.css", `.group-ops{}`)
	write("assets/componentStatesHost-test.js", `console.log("component states")`)
	write("assets/standard-components/group_chat_picker.css", `.group-picker{}`)
	write("assets/standard-components/material_picker.css", `.material-picker{}`)
	write("assets/adminDateTimeHost-test.js", `console.log("date time")`)
	write("assets/surfaceFeedbackHost-test.js", `console.log("surface feedback")`)
	write("assets/surfaceFeedbackStyles-test.css", `.surface-feedback{}`)
	write("assets/actionFeedbackStyles-test.css", `.action-feedback{}`)
	write("assets/presentationStyles-test.css", `.presentation{}`)
	write("asset-manifest.json", `{"entries":{"distributionAdmin":"assets/distributionAdmin-test.js","distributionStyles":"assets/distributionStyles-test.css","sharedDetailDrawerStyles":"assets/sharedDetailDrawerStyles-test.css","overviewAdmin":"assets/overviewAdmin-test.js","overviewStyles":"assets/overviewStyles-test.css","sharedVisualTokens":"assets/sharedVisualTokens-test.css","componentStatesStyles":"assets/componentStatesStyles-test.css","selectionDialogStyles":"assets/selectionDialogStyles-test.css","groupopsStyles":"assets/groupopsStyles-test.css","componentStatesHost":"assets/componentStatesHost-test.js","adminDateTimeHost":"assets/adminDateTimeHost-test.js","surfaceFeedbackHost":"assets/surfaceFeedbackHost-test.js","surfaceFeedbackStyles":"assets/surfaceFeedbackStyles-test.css","actionFeedbackStyles":"assets/actionFeedbackStyles-test.css","presentationStyles":"assets/presentationStyles-test.css"},"files":{"assets/distributionAdmin-test.js":{},"assets/distributionStyles-test.css":{},"assets/sharedDetailDrawerStyles-test.css":{},"assets/overviewAdmin-test.js":{},"assets/overviewStyles-test.css":{},"assets/sharedVisualTokens-test.css":{},"assets/componentStatesStyles-test.css":{},"assets/selectionDialogStyles-test.css":{},"assets/groupopsStyles-test.css":{},"assets/componentStatesHost-test.js":{},"assets/standard-components/group_chat_picker.css":{},"assets/standard-components/material_picker.css":{},"assets/adminDateTimeHost-test.js":{},"assets/surfaceFeedbackHost-test.js":{},"assets/surfaceFeedbackStyles-test.css":{},"assets/actionFeedbackStyles-test.css":{},"assets/presentationStyles-test.css":{}}}`)
	write("distribution/index.html", `<!doctype html><title>distribution</title><body data-page="distribution-center">分销中心</body>`)
	write("sidebar/index.html", `<link rel="stylesheet" href="../assets/sidebarStyles-test.css"><script type="module" src="../assets/sidebar-test.js"></script>新侧边栏`)
	write("assets/sidebar-test.js", `console.log("sidebar")`)
	write("assets/sidebarStyles-test.css", `.sidebar-shell{}`)
	return root
}

func TestDistOverviewAdminAssetsRequireManifestAndFiles(t *testing.T) {
	root := newDistFixture(t)
	assets, ok := DistOverviewAdminAssets(root)
	if !ok || assets.CSS != "/assets/overviewStyles-test.css" || assets.DetailDrawerCSS != "/assets/sharedDetailDrawerStyles-test.css" || assets.AdminJS != "/assets/overviewAdmin-test.js" {
		t.Fatalf("overview assets=%+v ok=%t", assets, ok)
	}
	if err := os.Remove(filepath.Join(root, "assets", "overviewAdmin-test.js")); err != nil {
		t.Fatal(err)
	}
	if _, ok := DistOverviewAdminAssets(root); ok {
		t.Fatal("manifest entry with a missing physical overview asset was accepted")
	}
}

func TestDistAdminPagesReplacePlaceholderShell(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{DistDir: newDistFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	for path, marker := range map[string]string{
		"/admin/index.html":      "新壳首页",
		"/admin/automation.html": "新壳自动化运营",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), marker) {
			t.Fatalf("dist admin page %s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if !strings.Contains(response.Header().Get("Cache-Control"), "no-store") {
			t.Fatalf("dist admin page %s must stay uncached: %q", path, response.Header().Get("Cache-Control"))
		}
	}

	// Vanity aliases nested deeper than /admin/<name>.html redirect onto the
	// flat canonical document: the built pages resolve "../assets/…" and
	// sibling links against the request path, which breaks at deeper mounts.
	for path, target := range map[string]string{
		"/admin/operation-cycles": "/admin/cycles.html",
		"/admin/channels":         "/admin/channels.html",
		"/admin/wecom-tags":       "/admin/wecom-tags.html",
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusSeeOther || response.Header().Get("Location") != target {
			t.Fatalf("deep alias %s status=%d location=%q, want 303 %q", path, response.Code, response.Header().Get("Location"), target)
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/operation-cycles?view=detail&id=7", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/cycles.html?view=detail&id=7" {
		t.Fatalf("deep alias must preserve the query string: status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/cycles.html", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "新壳运营闭环") {
		t.Fatalf("canonical deep-alias target must serve the document: status=%d", response.Code)
	}

	// The V3-owned root renders its own shell and manifest-verified assets; it
	// must never redirect to a frozen home document or customer page.
	for _, root := range []string{"/admin", "/admin/"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, root, nil))
		body := response.Body.String()
		if response.Code != http.StatusOK || strings.Contains(response.Header().Get("Location"), "customers") || !strings.Contains(body, `id="overview-admin-root"`) || !strings.Contains(body, `href="/assets/overviewStyles-test.css"`) || !strings.Contains(body, `href="/assets/sharedDetailDrawerStyles-test.css"`) || !strings.Contains(body, `src="/assets/overviewAdmin-test.js"`) {
			t.Fatalf("admin root %s status=%d location=%q body=%q", root, response.Code, response.Header().Get("Location"), body)
		}
	}

	// Unknown and module-owned paths keep the template shell behavior.
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/orders", nil))
	if !strings.Contains(response.Body.String(), "功能待接入") {
		t.Fatalf("module-owned placeholder regressed: %q", response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/../secret.html", nil))
	if strings.Contains(response.Body.String(), `data-page="distribution"`) && response.Code == http.StatusOK {
		t.Fatalf("traversal reached dist document: %q", response.Body.String())
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/admin/automation.html", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("dist admin page accepted POST: status=%d", response.Code)
	}
}

func TestDistSidebarReplacesLegacyWorkbench(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{DistDir: newDistFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, SidebarPagePath, nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, "新侧边栏") {
		t.Fatalf("dist sidebar status=%d body=%q", response.Code, body)
	}
	if strings.Contains(body, "../assets/") || !strings.Contains(body, `"/sidebar-assets/sidebar-test.js"`) || !strings.Contains(body, `"/sidebar-assets/sidebarStyles-test.css"`) {
		t.Fatalf("sidebar asset URLs were not rewritten to the unauthenticated prefix: %q", body)
	}

	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sidebar-assets/sidebar-test.js", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "console.log") {
		t.Fatalf("sidebar asset status=%d body=%q", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("hashed sidebar asset must be immutably cacheable: %q", response.Header().Get("Cache-Control"))
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/sidebar-assets/../sidebar/index.html", nil))
	if response.Code == http.StatusOK {
		t.Fatalf("sidebar asset traversal escaped dist root")
	}
}

func TestDistAdminPageNameMapping(t *testing.T) {
	for path, expected := range map[string]string{
		"/admin/owner-migration":                    "ownerMig.html",
		"/admin/api-docs":                           "apidocs.html",
		"/admin/funnel.html":                        "funnel.html",
		"/admin/operation-cycles":                   "cycles.html",
		"/admin/channels/new":                       "channelForm.html",
		"/admin/wecom-tags":                         "wecom-tags.html",
		"/admin/wechat-pay/products":                "products.html",
		"/admin/service-period-products":            "spProducts.html",
		"/admin/cloud-orchestrator/plans":           "ai.html",
		"/admin/automation-conversion/group-ops/ui": "groupops.html",
	} {
		name, ok := DistAdminPageName(path)
		if !ok || name != expected {
			t.Fatalf("DistAdminPageName(%q) = %q, %t", path, name, ok)
		}
	}
	for _, path := range []string{"/admin/customers/7", "/admin/nested/deep.html", "/admin/..%2fsecret.html", "/api/admin/orders", "/admin/customers"} {
		if name, ok := DistAdminPageName(path); ok {
			t.Fatalf("DistAdminPageName(%q) unexpectedly resolved %q", path, name)
		}
	}
	if _, ok := DistAdminPageFile("", "/admin"); ok {
		t.Fatal("dist lookup must be disabled without a dist root")
	}
}

func TestDistDistributionPageAndAdminAlias(t *testing.T) {
	handler, err := NewHandler(HandlerOptions{DistDir: newDistFixture(t)})
	if err != nil {
		t.Fatal(err)
	}
	for path, marker := range map[string]string{"/distribution": "分销中心", "/distribution/": "分销中心", "/admin/distribution": "distribution-admin-root"} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), marker) {
			t.Fatalf("distribution page %s status=%d body=%q", path, response.Code, response.Body.String())
		}
		if path == "/admin/distribution" && (!strings.Contains(response.Body.String(), `href="/assets/sharedDetailDrawerStyles-test.css"`) || strings.Count(response.Body.String(), `<main`) != 1) {
			t.Fatalf("distribution admin must mount shared drawer CSS in the single admin main: %q", response.Body.String())
		}
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/admin/distribution.html", nil))
	if response.Code != http.StatusSeeOther || response.Header().Get("Location") != "/admin/distribution" {
		t.Fatalf("standalone distribution alias status=%d location=%q", response.Code, response.Header().Get("Location"))
	}
}

func TestComponentStatesUsesStagedAssetClosure(t *testing.T) {
	dist := newDistFixture(t)
	handler, err := NewHandler(HandlerOptions{DistDir: dist})
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, ComponentStatesPath, nil))
	body := response.Body.String()
	if response.Code != http.StatusOK {
		t.Fatalf("component state demo status=%d body=%q", response.Code, body)
	}
	for _, required := range []string{
		`data-page="component-states"`,
		`data-component-states-root`,
		`href="/assets/sharedVisualTokens-test.css"`,
		`href="/assets/componentStatesStyles-test.css"`,
		`href="/assets/standard-components/group_chat_picker.css"`,
		`href="/assets/standard-components/material_picker.css"`,
		`src="/assets/componentStatesHost-test.js"`,
	} {
		if !strings.Contains(body, required) {
			t.Fatalf("component state demo misses staged resource %q: %q", required, body)
		}
	}
	if strings.Index(body, `href="/assets/sharedVisualTokens-test.css"`) < strings.Index(body, `href="/assets/presentationStyles-test.css"`) {
		t.Fatal("component visual token aliases must load after the presentation styles they map")
	}

	if err := os.Remove(filepath.Join(dist, "assets", "componentStatesHost-test.js")); err != nil {
		t.Fatal(err)
	}
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, ComponentStatesPath, nil))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing component host must reject the incomplete stage, status=%d", response.Code)
	}

	dist = newDistFixture(t)
	manifestPath := filepath.Join(dist, "asset-manifest.json")
	manifest, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	manifest = []byte(strings.Replace(string(manifest), "assets/sharedVisualTokens-test.css", "assets/../escape.css", -1))
	if err := os.WriteFile(manifestPath, manifest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dist, "escape.css"), []byte(".escape{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := DistComponentStatesAssets(dist); ok {
		t.Fatal("component state asset closure accepted a traversal manifest entry")
	}
}
