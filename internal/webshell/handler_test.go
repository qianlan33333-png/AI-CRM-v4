package webshell

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestAdminNavGroupsMirrorSourceMenu(t *testing.T) {
	if len(ADMIN_NAV_GROUPS) != 7 {
		t.Fatalf("group count=%d, want 7", len(ADMIN_NAV_GROUPS))
	}
	wantTitles := []string{"总览", "用户", "运营", "交易", "分销", "内容素材", "系统设置"}
	wantCounts := []int{1, 3, 7, 4, 2, 3, 4}
	for index, group := range ADMIN_NAV_GROUPS {
		if group.Title != wantTitles[index] || len(group.Items) != wantCounts[index] {
			t.Fatalf("group %d=%+v, want title=%q count=%d", index, group, wantTitles[index], wantCounts[index])
		}
		for _, item := range group.Items {
			if item.Key == "" || item.Label == "" || item.Endpoint == "" {
				t.Fatalf("incomplete nav item=%+v", item)
			}
			if got := AdminPathFor(item.Endpoint); !strings.HasPrefix(got, "/admin") {
				t.Fatalf("item %q path=%q", item.Label, got)
			}
		}
	}

	navigation := NavItems("api.admin_orders_page")
	if !navigation[3].Active || !navigation[3].Items[0].Active {
		t.Fatalf("transaction item is not active: %+v", navigation[3])
	}
	navigation[3].Items[0].Label = "mutated copy"
	if ADMIN_NAV_GROUPS[3].Items[0].Label == "mutated copy" {
		t.Fatal("NavItems returned mutable source item")
	}

	var source adminNavigationDocument
	if err := json.Unmarshal(adminNavigationJSON, &source); err != nil {
		t.Fatal(err)
	}
	prefixes := map[string]map[string]bool{}
	for _, group := range source.Groups {
		for _, item := range group.Items {
			prefixes[item.Key] = map[string]bool{}
			for _, prefix := range item.ActivePrefixes {
				prefixes[item.Key][prefix] = true
			}
		}
	}
	for key, aliases := range map[string][]string{
		"overview":                {"/admin", "/admin/index.html"},
		"customers":               {"/admin/customers", "/admin/customerDetail.html"},
		"wechat_pay_products":     {"/admin/wechat-pay/products", "/admin/wechat-pay/productForm.html", "/admin/productForm.html"},
		"group_ops":               {"/admin/automation-conversion/group-ops", "/admin/groupops.html", "/admin/groupopsDetail.html"},
		"service_period_products": {"/admin/service-period-products", "/admin/spProductForm.html"},
		"wechat_pay_transactions": {"/admin/orders", "/admin/orderDetail.html"},
		"automation_agents":       {"/admin/automation-agents", "/admin/agentEdit.html"},
		"questionnaires":          {"/admin/questionnaires", "/admin/questionnaireDetail.html"},
		"radar_links":             {"/admin/radar-links", "/admin/radarDetail.html"},
		"image_library":           {"/admin/materials", "/admin/image-library", "/admin/miniprogram-library", "/admin/attachment-library", "/admin/images.html", "/admin/mpLib.html", "/admin/attach.html"},
		"owner_migration":         {"/admin/owner-migration", "/admin/ownerMig.html"},
		"config":                  {"/admin/config", "/admin/configDetail.html"},
		"api_docs":                {"/admin/api-docs", "/admin/apidocs.html"},
	} {
		for _, alias := range aliases {
			if !prefixes[key][alias] {
				t.Fatalf("navigation %q omits active alias %q", key, alias)
			}
		}
	}
}

func TestPathForEscapesDynamicSegmentsAndNextPath(t *testing.T) {
	got := PathFor("api.admin_console_customer_detail", map[string]string{"customer_id": "42"})
	if got != "/admin/customers/42" {
		t.Fatalf("customer detail path=%q", got)
	}
	if PathFor("api.admin_console_customer_detail", map[string]string{"customer_id": "contact/a"}) != "#" {
		t.Fatal("non-numeric customer detail id accepted")
	}
	if SafeNextPath("https://attacker.example") != AdminRootPath {
		t.Fatal("absolute next path accepted")
	}
	if SafeNextPath("//attacker.example") != AdminRootPath {
		t.Fatal("protocol-relative next path accepted")
	}
	if SafeNextPath("/admin/config?tab=access") != "/admin/config?tab=access" {
		t.Fatal("local next path was changed")
	}
}

func TestStandaloneHandlerRendersAdminLoginSidebarAndAssets(t *testing.T) {
	handler, err := NewHandler()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		method     string
		path       string
		status     int
		contains   []string
		notContain []string
	}{
		{
			name:   "automation audience shell",
			method: http.MethodGet,
			path:   "/admin/automation-conversion",
			status: http.StatusOK,
			contains: []string{
				"AI 自动化运营",
				"class=\"aud-layout aud-workspace-panel\"",
				"id=\"audiencePackagePanel\"",
				"data-audience-workspace-tab=\"products\"",
				"data-audience-workspace-tab=\"packages\"",
				"人群包分组",
				"共 0 个自定义分组",
				"当前分组暂无人群包",
				"admin_console.js",
				"admin_audience.css",
				"admin_audience_detail.js?v=audience-direct-push-controls-v1",
			},
			notContain: []string{
				"功能待接入",
				"/api/admin/ai-audience/",
				"加载中",
			},
		},
		{
			name:   "automation audience secondary shell",
			method: http.MethodGet,
			path:   "/admin/automation-conversion/packages/42",
			status: http.StatusOK,
			contains: []string{
				"AI 自动化运营",
				"class=\"ai-page\"",
				"人群包配置维度",
				"基础配置",
				"自动化话术能力",
				"发送人白名单",
				"成员列表",
				"发送记录",
				"admin_audience_detail.css",
				"operation_member_picker_dd8d60d.js?v=audience-sender-picker-v2",
				"admin_audience_detail.js?v=audience-sender-picker-v1",
				"template_parameter_form.js?v=dd8-frozen-ab63c644",
				"admin_audience_template_host.js?v=prd05-template-empty-v2",
			},
			notContain: []string{
				"功能待接入",
				"/api/admin/ai-audience/",
				"external_userid",
				"加载中",
			},
		},
		{
			name:   "production admin console javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_console.js",
			status: http.StatusOK,
			contains: []string{
				"function bootLegacyFrames()",
				"function bootCopyButtons()",
				"window.AdminFmt",
			},
		},
		{
			name:   "audience secondary local javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_audience_detail.js",
			status: http.StatusOK,
			contains: []string{
				"/api/admin",
				"credentials: \"same-origin\"",
				"X-CSRF-Token",
				"Idempotency-Key",
				"broadcast-previews",
				"outcome_unknown",
			},
			notContain: []string{
				"sessionStorage",
				"localStorage",
				"mock.invalid",
				"external_userid",
			},
		},
		{
			name:   "admin placeholder",
			method: http.MethodGet,
			path:   "/admin/orders",
			status: http.StatusOK,
			contains: []string{
				"data-admin-shell-source=\"v3_webshell\"",
				"交易管理",
				"功能待接入",
				"用户激活 / 用户列表",
			},
			notContain: []string{"统计：0"},
		},
		{
			name:   "login",
			method: http.MethodGet,
			path:   "/login?next=/admin/orders",
			status: http.StatusOK,
			contains: []string{
				"method=\"post\"",
				"action=\"/login\"",
				"name=\"username\"",
				"name=\"password\"",
				"type=\"submit\">登录",
				"/auth/wecom/start?mode=qr&amp;next=%2Fadmin%2Forders",
				"/admin/config/login-access",
			},
			notContain: []string{"disabled"},
		},
		{
			name:   "wecom start blocked",
			method: http.MethodGet,
			path:   "/auth/wecom/start",
			status: http.StatusNotImplemented,
			contains: []string{
				"企业微信登录入口已预留",
			},
		},
		{
			name:   "sidebar shell",
			method: http.MethodGet,
			path:   SidebarPagePath,
			status: http.StatusOK,
			contains: []string{
				"核心画像",
				"问卷",
				"商品",
				"订单",
				"优惠券",
				"素材",
				"data-bind-mobile-url=\"/sidebar/bind-mobile\"",
				"data-jssdk-config-url=\"/api/sidebar/jssdk-config\"",
				"data-context-token-url=\"/api/sidebar/context-token\"",
				"data-workbench-url=\"/api/sidebar/v2/workbench\"",
				"data-profile-url=\"/api/sidebar/v2/profile\"",
				"data-questionnaires-url=\"/api/sidebar/v2/questionnaires\"",
				"data-send-intents-url=\"/api/sidebar/v2/send-intents\"",
				"https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js",
			},
			notContain: []string{"/api/v3/sidebar/", "聊天", "标签", "跟进", "运营", "自动化", "XMLHttpRequest", "sendBeacon"},
		},
		{
			name:   "sidebar shell javascript",
			method: http.MethodGet,
			path:   "/static/sidebar_workbench/sidebar_workbench.js",
			status: http.StatusOK,
			contains: []string{
				"data-tab",
				"/api/sidebar/jssdk-config",
				"/api/sidebar/context-token",
				"/api/sidebar/v2/workbench",
				"/api/sidebar/v2/questionnaires",
				"/api/sidebar/v2/products",
				"/api/sidebar/v2/orders",
				"/api/sidebar/v2/coupons",
				"/api/sidebar/v2/materials",
				"sendChatMessage",
				"/api/sidebar/oauth/start?next=/sidebar/bind-mobile",
				"Authorization: \"Bearer \"",
			},
			notContain: []string{"/api/v3/sidebar/", "chat-activity", "other-staff-messages", "/tags", "/owners", "XMLHttpRequest", "sendBeacon"},
		},
		{
			name:   "access page",
			method: http.MethodGet,
			path:   LoginAccessPath,
			status: http.StatusOK,
			contains: []string{
				"data-admin-access-root",
				"/api/admin/access/users",
				"/api/admin/access/enterprise-employees",
				"员工与权限",
				"开通权限",
				"admin_access.js",
			},
			notContain: []string{"session_version", "password_hash", "digest"},
		},
		{
			name:   "access javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_access.js",
			status: http.StatusOK,
			contains: []string{
				"last_login_at",
				"aicrm_admin_csrf",
				"X-CSRF-Token",
				"login-access",
				"enterprise-employees",
				"super-admin-transfer",
			},
			notContain: []string{"session_version", "password_hash", "digest"},
		},
		{
			name:       "customer directory page",
			method:     http.MethodGet,
			path:       "/admin/customers",
			status:     http.StatusOK,
			contains:   []string{"data-customer-directory-root", "/api/admin/customers", "/api/admin/customer-sync-runs", "用户查找", "admin-filter-bar admin-form-grid admin-form-grid--wide-filters", "手机号", "用户列表", "admin-table", "admin_customers.js"},
			notContain: []string{"type=\"password\"", "name=\"activation_status\"", "揭示理由", "临时揭示", "+8613812345678", "raw_external_userid", "unionid_value", "/api/v2/", "fixture", "data-profile-section"},
		},
		{
			name:       "customer directory javascript",
			method:     http.MethodGet,
			path:       "/static/admin_console/admin_customers.js",
			status:     http.StatusOK,
			contains:   []string{"credentials: \"same-origin\"", "cache: \"no-store\"", "X-CSRF-Token", "customer_id", "phone-reveal", "phone.startsWith(\"+86\") ? phone.slice(3)", "/360", "订单记录", "问卷记录", "最近触点", "/admin/message-archive/customers/"},
			notContain: []string{"customer-avatar", "phone_assurance", "item.activation_status", "declared", "localStorage", "sessionStorage", "console.log", "/api/v2/", "chat-activity", "survey-answers"},
		},
		{
			name:       "message archive customer entry",
			method:     http.MethodGet,
			path:       "/admin/message-archive",
			status:     http.StatusOK,
			contains:   []string{"data-message-archive-entry", `class="admin-page-title">会话存档`, "选择用户", "href=\"/admin/customers\""},
			notContain: []string{"data-message-archive-root", "name=\"q\"", "Customer ID", "<h2>会话存档</h2>"},
		},
		{
			name:       "customer profile page",
			method:     http.MethodGet,
			path:       "/admin/customers/42",
			status:     http.StatusOK,
			contains:   []string{"用户档案", "admin-module-banner", "admin-profile-grid", "admin-split-grid admin-customer-detail-layout", "admin-customer-detail-main", "admin-customer-detail-sidebar", "customer-360-sections"},
			notContain: []string{"external_userid", "UnionID", "unionid", "declared", "verified", "+8613812345678", "揭示理由", "customer-list-filters", "customer-sync-start", "跟进成员", "聊天记录", "data-profile-section"},
		},
		{
			name:   "admin shell javascript",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_shell.js",
			status: http.StatusOK,
			contains: []string{
				"X-CSRF-Token",
				"method: \"POST\"",
				"credentials: \"same-origin\"",
			},
			notContain: []string{"localStorage", "sessionStorage", "console."},
		},
		{
			name:   "oneid nav icon asset",
			method: http.MethodGet,
			path:   "/static/admin_console/nav-icons/oneid.svg",
			status: http.StatusOK,
			contains: []string{
				"<svg",
			},
		},
		{
			name:   "css asset",
			method: http.MethodGet,
			path:   "/static/admin_console/admin_console.css",
			status: http.StatusOK,
			contains: []string{
				".admin-layout",
				"--brand: #3370ff",
			},
		},
		{
			name:   "sidebar css asset",
			method: http.MethodGet,
			path:   "/static/sidebar_workbench/sidebar_workbench.css",
			status: http.StatusOK,
			contains: []string{
				".profile-card",
				".modal",
			},
		},
		{
			name:   "nav icon asset",
			method: http.MethodGet,
			path:   "/static/admin_console/nav-icons/wechat_pay_transactions.svg",
			status: http.StatusOK,
			contains: []string{
				"<svg",
			},
		},
		{
			name:   "v3 sidebar api is not registered",
			method: http.MethodGet,
			path:   "/api/v3/sidebar/workbench",
			status: http.StatusNotFound,
		},
		{
			name:   "production workbench api is not registered",
			method: http.MethodGet,
			path:   SidebarWorkbenchPath,
			status: http.StatusNotFound,
		},
		{
			name:   "questionnaire api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/questionnaires",
			status: http.StatusNotFound,
		},
		{
			name:   "product api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/products",
			status: http.StatusNotFound,
		},
		{
			name:   "order api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/orders",
			status: http.StatusNotFound,
		},
		{
			name:   "coupon api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/coupons",
			status: http.StatusNotFound,
		},
		{
			name:   "material api is not registered",
			method: http.MethodGet,
			path:   "/api/sidebar/v2/materials",
			status: http.StatusNotFound,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, nil)
			handler.ServeHTTP(response, request)
			if response.Code != test.status {
				t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
			}
			body := response.Body.String()
			for _, expected := range test.contains {
				if !strings.Contains(body, expected) {
					t.Errorf("body missing %q", expected)
				}
			}
			for _, forbidden := range test.notContain {
				if strings.Contains(body, forbidden) {
					t.Errorf("body contains forbidden %q", forbidden)
				}
			}
		})
	}
}

func TestRendererAccessContractConsumesNextAndMapsLoginErrors(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.Render(nil, response, http.StatusUnauthorized, "login", map[string]any{
		"next_path":        "/admin/config/login-access",
		"error":            "invalid_credentials",
		"login_csrf_token": "form-token",
		"password":         "must-not-render",
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", response.Code)
	}
	body := response.Body.String()
	for _, expected := range []string{"name=\"username\"", "value=\"/admin/config/login-access\"", "name=\"login_csrf_token\" value=\"form-token\"", "账号或密码不正确，请重试。"} {
		if !strings.Contains(body, expected) {
			t.Errorf("body missing %q", expected)
		}
	}
	if strings.Contains(body, "invalid_credentials") || strings.Contains(body, "must-not-render") {
		t.Fatal("renderer exposed raw error or ignored credential")
	}
}

func TestStaticAssetsUseBrowserApplicableContentType(t *testing.T) {
	handler := MustHandler()
	for _, test := range []struct {
		path        string
		contentType string
	}{
		{"/static/admin_console/admin_console.css", "text/css"},
		{"/static/admin_console/admin_audience.css", "text/css"},
		{"/static/admin_console/admin_audience_detail.css", "text/css"},
		{"/static/admin_console/automation_capability_selector.css", "text/css"},
		{"/static/admin_console/send_content_readonly_detail.css", "text/css"},
		{"/static/admin_console/ai_audience_send_records.css", "text/css"},
		{"/static/admin_console/admin_console.js", "text/javascript"},
		{"/static/admin_console/tag_sync_bridge.js", "text/javascript"},
		{"/static/admin_console/automation_create_code_adapter.js", "text/javascript"},
		{"/static/admin_console/survey_operations.js", "text/javascript"},
		{"/static/admin_console/config_adminops_bridge.js", "text/javascript"},
		{"/static/admin_console/runtime_config_releases_host.js", "text/javascript"},
		{"/static/admin_console/template_parameter_form.js", "text/javascript"},
		{"/static/admin_console/admin_audience_template_host.js", "text/javascript"},
		{"/static/admin_console/nav-icons/automation_conversion.svg", "image/svg+xml"},
		{"/static/admin_console/owner_handoff_host.js", "text/javascript"},
		{"/static/admin_console/owner_migration_dd8d60d.html", "text/html"},
		{"/static/admin_console/operation_member_picker_dd8d60d.js", "text/javascript"},
	} {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, test.path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d", test.path, response.Code)
		}
		if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, test.contentType) {
			t.Errorf("%s content-type=%q, want %s", test.path, got, test.contentType)
		}
	}
}

func TestAudienceTemplateControllerIsFrozenDD8Asset(t *testing.T) {
	contents, err := os.ReadFile("static/admin_console/template_parameter_form.js")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(contents)
	if got := hex.EncodeToString(digest[:]); got != "ab63c6446f37fd94c3fc07439a89cf90e6114b1c4e962d60cfe96bd72b8bdc1c" {
		t.Fatalf("frozen template controller digest=%s", got)
	}
}

func TestRenderTagsKeepsPR10AsTheOnlyAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderTags(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/wecom-tags", nil), "企微标签管理", "", "api.admin_wecom_tags_page"), `<section data-page="tags">frozen donor fragment</section>`, TagsAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", PageHeaderActionHostJS: "/assets/page-header-action-host.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Count(body, `<h1 class="admin-page-title">企微标签管理</h1>`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="tags">frozen donor fragment</section></template>`) || !strings.Contains(body, `data-admin-shell-source="v3_webshell"`) || !strings.Contains(body, `/static/admin_console/tag_sync_bridge.js`) || !strings.Contains(body, `/assets/page-header-action-host.js`) {
		t.Fatalf("tags shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderImageLibraryUsesTheSourceOwnedHostAndKeepsMaterialSave(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	assets := MediaAssets{
		TokensCSS:                "/media-assets/tokens.css",
		LabsCSS:                  "/media-assets/labs.css",
		AdminJS:                  "/media-assets/admin.js",
		MaterialSaveHostJS:       "/media-assets/material-save-host.js",
		ImageLibraryFilterHostJS: "/media-assets/image-library-filter-host.js",
		MaterialLibraryHostJS:    "/media-assets/material-library-host.js",
	}
	response := httptest.NewRecorder()
	err = renderer.RenderMedia(
		response,
		AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/image-library", nil), "图片素材库", "", "api.admin_image_library_workspace"),
		"images",
		"",
		assets,
	)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	hostAt := strings.Index(body, `src="/media-assets/image-library-filter-host.js"`)
	materialAt := strings.Index(body, `src="/media-assets/material-save-host.js"`)
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `data-page="images"`) || !strings.Contains(body, `data-image-library-v3-root`) || strings.Contains(body, `<template id="tpl">`) || strings.Contains(body, `src="/media-assets/admin.js"`) || hostAt < 0 || materialAt < 0 || !(materialAt < hostAt) {
		t.Fatalf("image library shell mismatch host=%d material=%d body=%q", hostAt, materialAt, body)
	}
	if err = renderer.RenderMedia(httptest.NewRecorder(), AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/image-library", nil), "图片素材库", "", "api.admin_image_library_workspace"), "images", "", MediaAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, MaterialSaveHostJS: assets.MaterialSaveHostJS, MaterialLibraryHostJS: assets.MaterialLibraryHostJS}); err == nil {
		t.Fatal("image library shell accepted a missing filter Host asset")
	}
	attachmentResponse := httptest.NewRecorder()
	attachmentTemplate := `<template data-sc-for="{{ rows.attachItems }}" data-as="a"><tr style="{{ a.rowStyle }}"><td>{{ a.name }}</td></tr></template>`
	if err = renderer.RenderMedia(attachmentResponse, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/attachment-library", nil), "附件素材库", "", "api.admin_attachment_library_workspace"), "attach", attachmentTemplate, MediaAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, MaterialSaveHostJS: assets.MaterialSaveHostJS, MaterialLibraryHostJS: assets.MaterialLibraryHostJS}); err != nil {
		t.Fatal(err)
	}
	attachmentBody := attachmentResponse.Body.String()
	if strings.Contains(attachmentBody, "image-library-filter-host.js") || !strings.Contains(attachmentBody, `src="/media-assets/admin.js"`) || !strings.Contains(attachmentBody, `src="/media-assets/material-save-host.js"`) || !strings.Contains(attachmentBody, `data-material-library-id="{{ a.resourceId }}"`) {
		t.Fatal("source-owned image Host leaked or frozen attachment runtime was removed")
	}
}

func TestMaterialTemplateIdentitySeamsUseControllerResourceIDs(t *testing.T) {
	attachment := `<template data-sc-for="{{ rows.attachItems }}" data-as="a"><tr style="{{ a.rowStyle }}"><td>{{ a.name }}</td></tr></template>`
	withAttachmentID, err := materialTemplateIdentitySeams("attach", attachment)
	if err != nil || !strings.Contains(withAttachmentID, `data-material-library-id="{{ a.resourceId }}"`) || strings.Count(withAttachmentID, `data-material-library-id=`) != 1 {
		t.Fatalf("attachment identity seam err=%v template=%q", err, withAttachmentID)
	}
	mini := `<template data-sc-for="{{ rows.mpItems }}" data-as="m"><div style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden">{{ m.name }}</div></template>`
	withMiniID, err := materialTemplateIdentitySeams("mpLib", mini)
	if err != nil || !strings.Contains(withMiniID, `data-material-library-id="{{ m.resourceId }}"`) || strings.Count(withMiniID, `data-material-library-id=`) != 1 {
		t.Fatalf("mini-program identity seam err=%v template=%q", err, withMiniID)
	}
	if _, err = materialTemplateIdentitySeams("attach", strings.Replace(attachment, `</template>`, `<tr style="{{ a.rowStyle }}"></tr></template>`, 1)); err == nil {
		t.Fatal("ambiguous attachment donor row must fail closed")
	}
}

func TestRenderHXCMountsLiveDashboardInTheV3Shell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderHXC(
		response,
		AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/hxc-dashboard", nil), "漏斗 / 数据看板", "HXC 当前全量投影", "api.admin_hxc_dashboard_workspace"),
		HXCAssets{TokensCSS: "/hxc-dashboard-assets/tokens.css", LabsCSS: "/hxc-dashboard-assets/labs.css", AdminJS: "/hxc-dashboard-assets/admin.js"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Count(body, `<header class="admin-topbar">`) != 1 || !strings.Contains(body, `data-admin-shell-source="v3_webshell" data-page="funnel"`) || !strings.Contains(body, `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--dynamic"></main>`) || !strings.Contains(body, `/hxc-dashboard-assets/admin.js`) || strings.Contains(body, "功能待接入") {
		t.Fatalf("HXC shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderProductsKeepsPR10AsTheOnlyAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderProducts(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/wechat-pay/products", nil), "普通商品", "", "api.admin_products_page"), "products", `<section data-page="products">frozen donor product fragment</section>`, ProductAssets{TokensCSS: "/product-assets/tokens.css", LabsCSS: "/product-assets/labs.css", ProductCSS: "/product-assets/product-distribution.css", HostJS: "/product-assets/product-host.js", StandardHostJS: "/product-assets/standard-components-host.js", StandardCSS: []string{"/product-assets/standard-components/material_picker.css", "/product-assets/standard-components/send_content_composer.css", "/product-assets/standard-components/wecom_tag_picker.css", "/product-assets/selection-dialog.css", "/product-assets/shared-detail-drawer.css"}})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || !strings.Contains(body, `<template id="tpl"><section data-page="products">frozen donor product fragment</section></template>`) || !strings.Contains(body, `data-admin-shell-source="v3_webshell"`) || !strings.Contains(body, `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>`) || strings.Count(body, `<header class="admin-topbar">`) != 1 || !strings.Contains(body, `<h1 class="admin-page-title">普通商品</h1>`) || !strings.Contains(body, `href="/product-assets/product-distribution.css"`) || !strings.Contains(body, `src="/product-assets/product-host.js"`) {
		t.Fatalf("product list shell mismatch status=%d body=%q", response.Code, body)
	}
	for page, wantTopbar := range map[string]int{"productForm": 1, "spProductForm": 1, "spProductData": 1} {
		response = httptest.NewRecorder()
		err = renderer.RenderProducts(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/"+page+".html", nil), "产品编辑", "", "api.admin_products_page"), page, `<section data-page="`+page+`">frozen donor product fragment</section>`, ProductAssets{TokensCSS: "/product-assets/tokens.css", LabsCSS: "/product-assets/labs.css", ProductCSS: "/product-assets/product-distribution.css", HostJS: "/product-assets/product-host.js", StandardHostJS: "/product-assets/standard-components-host.js", StandardCSS: []string{"/product-assets/standard-components/material_picker.css", "/product-assets/standard-components/send_content_composer.css", "/product-assets/standard-components/wecom_tag_picker.css", "/product-assets/selection-dialog.css", "/product-assets/shared-detail-drawer.css"}})
		if err != nil {
			t.Fatal(err)
		}
		body := response.Body.String()
		if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || strings.Count(body, `<header class="admin-topbar">`) != wantTopbar || !strings.Contains(body, `data-page="`+page+`"`) {
			t.Fatalf("product embedded page=%s topbar=%d want=%d body=%q", page, strings.Count(body, `<header class="admin-topbar">`), wantTopbar, body)
		}
		if wantTopbar == 1 && strings.Count(body, `class="admin-page-title"`) != 1 {
			t.Fatalf("product editor page=%s must have one shared page title body=%q", page, body)
		}
	}
}

func TestRenderOrdersMountsFrozenTransactionPageAndHostImportControl(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderOrders(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/orders", nil), "交易管理", "", "api.admin_orders_page"), "orders", `<section data-page="orders">frozen donor orders</section>`, OrderAssets{TokensCSS: "/order-assets/tokens.css", LabsCSS: "/order-assets/labs.css", AdminJS: "/order-assets/admin.js", HostJS: "/order-assets/order-host.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	hostAt := strings.Index(body, `<script type="module" src="/order-assets/order-host.js"></script>`)
	donorAt := strings.Index(body, `<script type="module" src="/order-assets/admin.js"></script>`)
	if hostAt < 0 || donorAt >= 0 {
		t.Fatal("Order Host must be the sole bootstrap on the real /admin/orders route")
	}
	stageAt := strings.Index(body, `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>`)
	panelAt := strings.Index(body, `data-order-import`)
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="orders">frozen donor orders</section></template>`) || panelAt < 0 || !strings.Contains(body, `/static/admin_console/order_import.js`) || stageAt < 0 || stageAt > panelAt {
		t.Fatalf("order shell mismatch status=%d stage_at=%d panel_at=%d body=%q", response.Code, stageAt, panelAt, body)
	}
}

func TestRenderCouponsKeepsPR10AsTheOnlyAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderCoupons(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/coupons", nil), "优惠券", "", "api.admin_coupons_page"), "coupons", `<section data-page="coupons">frozen donor coupon fragment</section>`, CouponAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", HostJS: "/assets/coupon-host.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	hostAt := strings.Index(body, `<script type="module" src="/assets/coupon-host.js"></script>`)
	donorAt := strings.Index(body, `<script type="module" src="/assets/admin.js"></script>`)
	if response.Code != http.StatusOK || hostAt < 0 || donorAt >= 0 || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || !strings.Contains(body, `<template id="tpl"><section data-page="coupons">frozen donor coupon fragment</section></template>`) || !strings.Contains(body, `data-page="coupons"`) {
		t.Fatalf("coupon shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRenderOwnerHandoffUsesV3StaticHostOnly(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderOwnerHandoff(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/owner-migration", nil), "负责人迁移", "", "api.admin_owner_migration_page"))
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<header class="admin-topbar">`) != 1 || strings.Count(body, `<main`) != 1 || strings.Contains(body, `<main class="admin-page">`) || !strings.Contains(body, `data-owner-handoff-host`) || !strings.Contains(body, `data-page="owner-handoff"`) || !strings.Contains(body, `/static/admin_console/owner_handoff_host.js?v=owner-handoff-host-v3`) || strings.Contains(body, `owner-handoff-host-v2`) || strings.Contains(body, `web/src/admin/`) || strings.Contains(body, `<<<<<<<`) || strings.Contains(body, `=======`) || strings.Contains(body, `>>>>>>>`) {
		t.Fatalf("owner handoff shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestOwnerHandoffFrozenDonorAssetAndHostBinding(t *testing.T) {
	frozen, err := os.ReadFile("static/admin_console/owner_migration_dd8d60d.html")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(frozen)
	if got := hex.EncodeToString(sum[:]); got != "7491be60ed89b84fcbe4b9b37f27f5a95dd270cd4915d459449a74349f74138f" {
		t.Fatalf("owner migration frozen asset hash=%s", got)
	}
	if !bytes.Contains(frozen, []byte("data-owner-migration-page")) || !bytes.Contains(frozen, []byte("OperationMemberPicker.open")) || !bytes.Contains(frozen, []byte("/api/admin/owner-migration/template.xlsx")) {
		t.Fatal("frozen owner migration asset no longer contains the verified page/controller contract")
	}
	host, err := os.ReadFile("static/admin_console/owner_handoff_host.js")
	if err != nil {
		t.Fatal(err)
	}
	picker, err := os.ReadFile("static/admin_console/operation_member_picker_dd8d60d.js")
	if err != nil {
		t.Fatal(err)
	}
	pickerSum := sha256.Sum256(picker)
	if got := hex.EncodeToString(pickerSum[:]); got != "c51e565cac86ca99186e96028a8d9683b3692a2de5203dfd85cdf5cab7ecab49" || !bytes.Contains(picker, []byte("OperationMemberPicker")) || !bytes.Contains(picker, []byte("/api/admin/common/operation-members")) {
		t.Fatalf("shared frozen picker contract changed hash=%s", got)
	}
	if !bytes.Contains(host, []byte("owner_migration_dd8d60d.html")) || !bytes.Contains(host, []byte("AICRMStaffPicker")) || bytes.Contains(host, []byte("operation_member_picker_dd8d60d.js")) || bytes.Contains(host, []byte("OperationMemberPicker")) || bytes.Contains(host, []byte("data-owner-picker-options")) {
		t.Fatal("Host did not mount the frozen donor with the V3 scoped staff picker contract")
	}
}

func TestRenderAutomationUsesOnlyV3CreateCodeHostBinding(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	assets := AutomationAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", PresentationCSS: "/assets/presentation.css", ContentCSS: "/assets/automation-content.css", SelectionDialogCSS: "/assets/selection-dialog.css", MaterialPickerCSS: "/assets/material-picker.css", MaterialPickerJS: "/assets/material-picker.js", ContentHostJS: "/assets/automation-content.js"}
	response := httptest.NewRecorder()
	err = renderer.RenderAutomation(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?type=agent", nil), "自动化话术", "", "api.admin_automation_agents"), "agentEdit", `<section data-page="agentEdit">frozen donor fragment</section>`, assets, "agent_0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<main`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="agentEdit">frozen donor fragment</section></template>`) || !strings.Contains(body, `data-automation-create-code="agent_0123456789abcdef0123456789abcdef"`) || !strings.Contains(body, `<script defer src="/static/admin_console/automation_create_code_adapter.js?v=automation-create-code-v2"></script>`) || !strings.Contains(body, `href="/assets/automation-content.css"`) || !strings.Contains(body, `src="/assets/automation-content.js"`) {
		t.Fatalf("automation create shell mismatch status=%d body=%q", response.Code, body)
	}

	response = httptest.NewRecorder()
	err = renderer.RenderAutomation(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/agentEdit.html?id=7", nil), "自动化话术", "", "api.admin_automation_agents"), "agentEdit", `<section data-page="agentEdit">frozen donor fragment</section>`, assets, "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(response.Body.String(), `data-automation-create-code=`) {
		t.Fatal("existing automation editor received a create-code binding")
	}
}

func TestAutomationCreateCodeAdapterBrowserTiming(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(repo, "web", "dist", "admin", "agentEdit.html")); err != nil {
		t.Skip("browser bundle is not staged")
	}
	command := exec.Command("node", "internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("browser timing contract failed: %v\n%s", err, output.String())
	}

	if !strings.Contains(output.String(), "automation-create-code-adapter-browser: PASS") {
		t.Fatalf("browser timing contract did not report success: %q", output.String())
	}
}

func TestOwnerMigrationFileRetainsDonorXLSNameContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/owner_migration_file_contract.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("owner migration import contract failed: %v\\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "owner-migration-file-contract: PASS") {
		t.Fatalf("owner migration import contract did not report success: %q", output.String())
	}
}

func TestAudienceActivationReadinessBrowserContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_audience_detail.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("audience readiness browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-audience-activation-readiness-browser: PASS") {
		t.Fatalf("audience readiness browser contract did not report success: %q", output.String())
	}
}

func TestAudienceFrozenTemplateHostBrowserContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate webshell test")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_audience_template_host.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("audience template Host browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-audience-template-host-browser: PASS") {
		t.Fatalf("audience template Host browser contract did not report success: %q", output.String())
	}
}

// TestOperationCyclesHostShellJourney is the release-facing host Journey:
// an authenticated v3 route mounts one frozen fragment in the sole sidebar
// shell and exposes only the v3 binding that starts the donor runtime.
func TestOperationCyclesHostShellJourney(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderOperationCycles(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/operation-cycles/cyclesDetail.html?id=1", nil), "运营闭环", "", "api.admin_operation_cycles_page"), "cyclesDetail", `<section data-proof="frozen">原版页面</section>`, OperationCycleAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", HostJS: "/assets/operationCyclesHost.js"})
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<aside`) != 1 || strings.Contains(body, `class="side"`) || strings.Contains(body, `class="shell"`) || !strings.Contains(body, `<base href="/admin/operation-cycles/">`) || !strings.Contains(body, `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>`) || strings.Contains(body, `<header class="admin-topbar">`) || !strings.Contains(body, `data-page="cyclesDetail"`) || !strings.Contains(body, `src="/assets/operationCyclesHost.js"`) || !strings.Contains(body, `<template id="tpl"><section data-proof="frozen">原版页面</section></template>`) {
		t.Fatalf("operation-cycle host shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestRuntimeConfigHostShellJourney(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderRuntimeConfig(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/config/releases/7", nil), "配置发布详情", "", "api.admin_runtime_config_releases"), "runtimeReleaseDetail", `<section data-proof="runtime-config">加载</section>`)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Count(body, `<header class="admin-topbar">`) != 1 || !strings.Contains(body, `data-runtime-config-page="runtimeReleaseDetail"`) || !strings.Contains(body, `data-runtime-release-host`) || !strings.Contains(body, `src="/static/admin_console/runtime_config_releases_host.js`) || strings.Contains(body, `config_adminops_bridge.js`) || strings.Contains(body, `type="module" src="/assets/admin.js"`) {
		t.Fatalf("runtime config host shell mismatch status=%d body=%q", response.Code, body)
	}
}

func TestSurveyEditorUsesFullWidthDonorWorkspaceInsideAdminShell(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderSurvey(
		response,
		AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/questionnaireDetail.html?id=11", nil), "问卷编辑", "", "api.admin_questionnaires"),
		"questionnaireDetail",
		`<div class="shell"><header class="topbar">问卷工具栏</header><div class="workspace">编辑区</div></div><div id="questionnaire-editor-config" hidden>{}</div>`,
		SurveyAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", EditorJS: "/assets/editor.js", EditorCSS: "/assets/editor.css", OperationsHostJS: "/assets/survey-operations.js", OperationsCSS: "/assets/survey-operations.css"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Contains(body, `<main class="admin-page">`) || strings.Contains(body, `<template id="tpl">`) || strings.Contains(body, `href="/assets/tokens.css"`) || strings.Contains(body, `href="/assets/labs.css"`) || strings.Contains(body, `src="/assets/admin.js"`) || !strings.Contains(body, `<div class="admin-main-wrap">`) || !strings.Contains(body, `<div class="shell"><header class="topbar">问卷工具栏</header><div class="workspace">编辑区</div></div>`) || !strings.Contains(body, `data-page="questionnaireDetail"`) || !strings.Contains(body, `href="/assets/editor.css"`) || !strings.Contains(body, `src="/assets/editor.js"`) || !strings.Contains(body, `survey_operations.js?v=survey-bridge-v2`) {
		t.Fatalf("survey editor host layout mismatch status=%d body=%q", response.Code, body)
	}
}

func TestSurveyListUsesV3HostAsItsOnlyRuntimeEntrypoint(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	err = renderer.RenderSurvey(
		response,
		AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/questionnaires.html", nil), "问卷管理", "", "api.admin_questionnaires"),
		"questionnaires",
		`<section>问卷列表</section>`,
		SurveyAssets{TokensCSS: "/assets/tokens.css", LabsCSS: "/assets/labs.css", AdminJS: "/assets/admin.js", SurveyHostJS: "/assets/survey-host.js", EditorJS: "/assets/editor.js", EditorCSS: "/assets/editor.css", OperationsHostJS: "/assets/survey-operations.js", OperationsCSS: "/assets/survey-operations.css"},
	)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `src="/assets/survey-host.js"`) || strings.Contains(body, `src="/assets/admin.js"`) {
		t.Fatalf("survey list must use only the V3 Host runtime: status=%d body=%q", response.Code, body)
	}
}

func TestSurveyQRBridgeBrowserFallback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/survey_qr_bridge.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("survey QR bridge browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "survey-qr-bridge-browser: PASS") {
		t.Fatalf("survey QR bridge browser contract did not report success: %q", output.String())
	}
}

func TestAdminConsoleDateTimeReadiness(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_console_datetime.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("admin console date/time readiness failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-console date-time readiness: PASS") {
		t.Fatalf("admin console date/time readiness did not report success: %q", output.String())
	}
}

func TestMessageArchiveBrowserPrivateImageContract(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/message_archive.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("message archive browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "message-archive-browser: PASS") {
		t.Fatalf("message archive browser contract did not report success: %q", output.String())
	}
}

func TestAdminCustomersBrowserMessageArchiveEntry(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository")
	}
	repo := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	command := exec.Command("node", "internal/webshell/static/admin_console/admin_customers.test.mjs")
	command.Dir = repo
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Run(); err != nil {
		t.Fatalf("admin customers browser contract failed: %v\n%s", err, output.String())
	}
	if !strings.Contains(output.String(), "admin-customers-browser: PASS") {
		t.Fatalf("admin customers browser contract did not report success: %q", output.String())
	}
}

func TestLoginPostNeverIssuesSession(t *testing.T) {
	handler := MustHandler()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, LoginPath, strings.NewReader("account=admin&password=secret"))
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotImplemented {
		t.Fatalf("status=%d", response.Code)
	}
	if response.Header().Get("Set-Cookie") != "" {
		t.Fatal("login shell issued a session cookie")
	}
	if strings.Contains(response.Body.String(), "secret") {
		t.Fatal("login shell echoed credential input")
	}
}
func TestMessageArchiveRendersAsSeparateArchiveHost(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/message-archive/customers/7", nil)
	if err = renderer.RenderAdminStatus(response, http.StatusOK, AdminPageForRequest(request, "会话存档", "", "")); err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `data-message-archive-root`) || !strings.Contains(body, `admin-profile-message-list`) || strings.Contains(body, `customer-profile-root`) || strings.Contains(body, `customer-chat-activity`) || strings.Count(body, `class="admin-page-title">会话存档`) != 1 || strings.Count(body, `<h2>会话存档</h2>`) != 0 {
		t.Fatalf("archive host boundary mismatch: %s", body)
	}
}

func TestRenderGroupOpsInjectsManifestVerifiedReadonlyContentRenderer(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	assets := GroupOpsAssets{TokensCSS: "/groupops-assets/assets/tokens.css", LabsCSS: "/groupops-assets/assets/labs.css", AdminJS: "/groupops-assets/assets/admin.js", ReadonlyCSS: "/groupops-assets/aiassistant/send_content_readonly_detail.css", ReadonlyJS: "/groupops-assets/aiassistant/send_content_readonly_detail.js"}
	response := httptest.NewRecorder()
	err = renderer.RenderGroupOps(response, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/groupopsDetail.html?history=1&id=9", nil), "群运营计划", "", "api.admin_group_ops_plan_detail"), "groupopsDetail", `<section data-page="groupopsDetail">frozen donor fragment</section>`, assets)
	if err != nil {
		t.Fatal(err)
	}
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `href="/groupops-assets/aiassistant/send_content_readonly_detail.css"`) || !strings.Contains(body, `<script defer src="/groupops-assets/aiassistant/send_content_readonly_detail.js"></script>`) || !strings.Contains(body, `<script defer src="/static/admin_console/groupops_history_readonly_bridge.js"></script>`) || !strings.Contains(body, `<template id="tpl"><section data-page="groupopsDetail">frozen donor fragment</section></template>`) {
		t.Fatalf("group ops read-only shell mismatch status=%d body=%q", response.Code, body)
	}
	if err := renderer.RenderGroupOps(httptest.NewRecorder(), AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/groupops.html", nil), "群运营计划", "", "api.admin_group_ops_ui"), "groupops", `<section></section>`, GroupOpsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS}); err == nil {
		t.Fatal("group ops shell accepted missing read-only content assets")
	}
	standardAssets := GroupOpsAssets{TokensCSS: assets.TokensCSS, LabsCSS: assets.LabsCSS, AdminJS: assets.AdminJS, ReadonlyCSS: assets.ReadonlyCSS, ReadonlyJS: assets.ReadonlyJS, StandardCSS: "/groupops-assets/assets/groupops.css", HostJS: "/groupops-assets/assets/groupops.js", SelectionDialogCSS: "/groupops-assets/assets/selection-dialog.css", OperationPickerJS: "/groupops-assets/assets/standard-components/operation_member_picker.js", GroupPickerCSS: "/groupops-assets/assets/standard-components/group_chat_picker.css", GroupPickerJS: "/groupops-assets/assets/standard-components/group_chat_picker.js", MaterialPickerCSS: "/groupops-assets/assets/standard-components/material_picker.css", MaterialPickerJS: "/groupops-assets/assets/standard-components/material_picker.js", ComposerCSS: "/groupops-assets/assets/standard-components/send_content_composer.css", ComposerJS: "/groupops-assets/assets/standard-components/send_content_composer.js"}
	standardResponse := httptest.NewRecorder()
	if err = renderer.RenderGroupOps(standardResponse, AdminPageForRequest(httptest.NewRequest(http.MethodGet, "/admin/groupops.html", nil), "群运营计划", "管理本地群计划。", "api.admin_group_ops_ui"), "groupops", `<div id="group-ops-app" data-group-ops-standard-host="true"></div>`, standardAssets); err != nil {
		t.Fatal(err)
	}
	standardBody := standardResponse.Body.String()
	if standardResponse.Code != http.StatusOK || !strings.Contains(standardBody, `<header class="admin-topbar">`) || !strings.Contains(standardBody, `<h1 class="admin-page-title">群运营计划</h1>`) || !strings.Contains(standardBody, `<main id="stage" class="admin-page" data-group-ops-standard-stage>`) || strings.Contains(standardBody, `admin-workspace-stage--embedded`) {
		t.Fatalf("standard Group Ops native shell mismatch status=%d body=%q", standardResponse.Code, standardBody)
	}
}

func TestRenderProductFormsUseTheSharedSSRHeader(t *testing.T) {
	renderer, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	assets := ProductAssets{TokensCSS: "/product-assets/tokens.css", LabsCSS: "/product-assets/labs.css", ProductCSS: "/product-assets/product-distribution.css", HostJS: "/product-assets/product-host.js", StandardHostJS: "/product-assets/standard-components-host.js", StandardCSS: []string{"/product-assets/standard-components/material_picker.css", "/product-assets/standard-components/send_content_composer.css", "/product-assets/standard-components/wecom_tag_picker.css", "/product-assets/selection-dialog.css", "/product-assets/shared-detail-drawer.css"}}
	for _, page := range []string{"productForm", "spProductForm"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/admin/wechat-pay/"+page+".html?id=9", nil)
		if err := renderer.RenderProducts(response, AdminPageForRequest(request, "编辑商品", "", "api.admin_products_page"), page, `<section data-page="`+page+`">frozen donor product fragment</section>`, assets); err != nil {
			t.Fatalf("render %s: %v", page, err)
		}
		body := response.Body.String()
		if response.Code != http.StatusOK || strings.Count(body, `<header class="admin-topbar">`) != 1 || strings.Count(body, `class="admin-page-title"`) != 1 || strings.Count(body, `class="admin-sidebar"`) != 1 || strings.Contains(body, `class="side"`) || !strings.Contains(body, `<template id="tpl"><section data-page="`+page+`">frozen donor product fragment</section></template>`) {
			t.Fatalf("%s must render one SSR header and one V3 shell: %q", page, body)
		}
	}
}
