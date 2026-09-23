// Package webshell contains the v3 presentation shell for the admin console
// and the WeCom customer sidebar.
//
// The package intentionally stops at HTML, CSS and local shell state.  It does
// not own authentication, customer data, business APIs, or provider calls.
// A future composition root can mount the renderer behind the corresponding
// domain handlers once those capabilities are implemented.
package webshell

import (
	_ "embed"
	"encoding/json"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	LoginPath            = "/login"
	WeComAuthStartPath   = "/auth/wecom/start"
	AdminRootPath        = "/admin"
	ComponentStatesPath  = "/admin/component-states"
	LoginAccessPath      = "/admin/config/login-access"
	OneIDResolveAPIPath  = "/api/admin/oneid/resolve"
	OneIDCustomerAPIPath = "/api/admin/oneid/customers/"
	OneIDConflictsPath   = "/api/admin/oneid/conflicts"
	OneIDCandidatesPath  = "/api/admin/oneid/merge-candidates"
	SidebarPagePath      = "/sidebar/bind-mobile"
	SidebarWorkbenchPath = "/api/sidebar/v2/workbench"
	SidebarJSSDKPath     = "/api/sidebar/jssdk-config"
	SidebarContextPath   = "/api/sidebar/context-token"
)

// AdminRoute is the stable path portion of a shell route.  Endpoint names
// retain the source shell's route vocabulary so later domain handlers can map
// the menu without copying the old application.
type AdminRoute struct {
	Endpoint string
	Path     string
}

// AdminNavItem is one link in the admin navigation.
type AdminNavItem struct {
	Key      string
	Label    string
	Endpoint string
	Href     string
	Active   bool
}

// AdminNavGroup is a labelled group of admin navigation links.
type AdminNavGroup struct {
	Title  string
	Items  []AdminNavItem
	Active bool
}

// ADMIN_ROUTE_REGISTRY is the source shell's route registry plus the v3
// reserved authentication, access and sidebar paths.  Registry values are
// paths only; they do not imply that the corresponding business capability is
// implemented.
var ADMIN_ROUTE_REGISTRY = map[string]AdminRoute{
	"api.admin_ops_governance":                         {"api.admin_ops_governance", "/admin/ops"},
	"api.admin_console_dashboard":                      {"api.admin_console_dashboard", "/admin"},
	"api.admin_operating_overview":                     {"api.admin_operating_overview", AdminRootPath},
	"api.admin_console_customers":                      {"api.admin_console_customers", "/admin/customers"},
	"api.admin_owner_migration_page":                   {"api.admin_owner_migration_page", "/admin/owner-migration"},
	"api.admin_owner_migration_action":                 {"api.admin_owner_migration_action", "/admin/owner-migration"},
	"api.admin_user_ops_ui":                            {"api.admin_user_ops_ui", "/admin/user-ops/ui"},
	"api.admin_hxc_dashboard_workspace":                {"api.admin_hxc_dashboard_workspace", "/admin/hxc-dashboard"},
	"api.admin_hxc_send_config_page":                   {"api.admin_hxc_send_config_page", "/admin/hxc-send-config"},
	"api.admin_cloud_orchestrator_workspace":           {"api.admin_cloud_orchestrator_workspace", "/admin/cloud-orchestrator/plans"},
	"api.admin_external_effects_page":                  {"api.admin_external_effects_page", "/admin/external-effects"},
	"api.admin_cloud_orchestrator_plans_workspace":     {"api.admin_cloud_orchestrator_plans_workspace", "/admin/cloud-orchestrator/plans"},
	"api.admin_cloud_orchestrator_campaigns_workspace": {"api.admin_cloud_orchestrator_campaigns_workspace", "/admin/cloud-orchestrator/campaigns"},
	"api.admin_cloud_orchestrator_observability":       {"api.admin_cloud_orchestrator_observability", "/admin/cloud-orchestrator/observability"},
	"api.admin_wecom_tags_page":                        {"api.admin_wecom_tags_page", "/admin/wecom-tags"},
	"api.admin_channels_page":                          {"api.admin_channels_page", "/admin/channels"},
	"api.admin_channel_new_page":                       {"api.admin_channel_new_page", "/admin/channels/new"},
	"api.admin_questionnaires":                         {"api.admin_questionnaires", "/admin/questionnaires"},
	"api.admin_console_questionnaires":                 {"api.admin_console_questionnaires", "/admin/questionnaires"},
	"api.admin_console_questionnaire_new":              {"api.admin_console_questionnaire_new", "/admin/questionnaires/new"},
	"api.admin_radar_links":                            {"api.admin_radar_links", "/admin/radar-links"},
	"api.admin_radar_link_new":                         {"api.admin_radar_link_new", "/admin/radar-links/new"},
	"api.admin_automation_conversion":                  {"api.admin_automation_conversion", "/admin/automation-conversion"},
	"api.admin_automation_agents_page":                 {"api.admin_automation_agents_page", "/admin/automation-agents"},
	"api.admin_group_invitations":                      {"api.admin_group_invitations", "/admin/group-invitations"},
	"api.admin_group_ops_ui":                           {"api.admin_group_ops_ui", "/admin/automation-conversion/group-ops/ui"},
	"api.admin_group_ops_groups_ui":                    {"api.admin_group_ops_groups_ui", "/admin/automation-conversion/group-ops/groups/ui"},
	"api.admin_wechat_pay_transactions_page":           {"api.admin_wechat_pay_transactions_page", "/admin/wechat-pay/transactions"},
	"api.admin_orders_page":                            {"api.admin_orders_page", "/admin/orders"},
	"api.admin_wechat_pay_products_page":               {"api.admin_wechat_pay_products_page", "/admin/wechat-pay/products"},
	"api.admin_service_period_products_page":           {"api.admin_service_period_products_page", "/admin/service-period-products"},
	"api.admin_referral_page":                          {"api.admin_referral_page", "/admin/referral"},
	"api.admin_distribution_page":                      {"api.admin_distribution_page", "/admin/distribution"},
	"api.admin_coupons_page":                           {"api.admin_coupons_page", "/admin/coupons"},
	"api.admin_alipay_transactions_page":               {"api.admin_alipay_transactions_page", "/admin/alipay/transactions"},
	"api.admin_materials_workspace":                    {"api.admin_materials_workspace", "/admin/materials"},
	"api.admin_image_library_workspace":                {"api.admin_image_library_workspace", "/admin/image-library"},
	"api.admin_miniprogram_library_workspace":          {"api.admin_miniprogram_library_workspace", "/admin/miniprogram-library"},
	"api.admin_attachment_library_workspace":           {"api.admin_attachment_library_workspace", "/admin/attachment-library"},
	"api.admin_config":                                 {"api.admin_config", "/admin/config"},
	"api.admin_config_app_settings":                    {"api.admin_config_app_settings", "/admin/config/app-settings"},
	"api.admin_config_login_access":                    {"api.admin_config_login_access", LoginAccessPath},
	"api.admin_api_docs":                               {"api.admin_api_docs", "/admin/api-docs"},
	"api.admin_console_api_docs":                       {"api.admin_console_api_docs", "/admin/api-docs"},
	"api.admin_operation_cycles_page":                  {"api.admin_operation_cycles_page", "/admin/operation-cycles"},
	"api.admin_login":                                  {"api.admin_login", LoginPath},
	"api.admin_login_submit":                           {"api.admin_login_submit", LoginPath},
	"api.auth_wecom_start":                             {"api.auth_wecom_start", WeComAuthStartPath},
	"api.admin_logout":                                 {"api.admin_logout", "/logout"},
	"api.sidebar_bind_mobile_page":                     {"api.sidebar_bind_mobile_page", SidebarPagePath},
	"api.sidebar_workbench":                            {"api.sidebar_workbench", SidebarWorkbenchPath},
	"api.sidebar_jssdk_config":                         {"api.sidebar_jssdk_config", SidebarJSSDKPath},
	"api.sidebar_context_token":                        {"api.sidebar_context_token", SidebarContextPath},
}

// ADMIN_NAV_GROUPS is loaded from the one V3-owned navigation data source.
// Both the server-rendered shell and the release-document adapter use this
// embedded JSON; endpoint paths and access enforcement remain in their
// existing server-owned route registration.
//
//go:embed static/admin_console/admin-navigation.v3.json
var adminNavigationJSON []byte

type adminNavigationDocument struct {
	Version int                    `json:"version"`
	Groups  []adminNavigationGroup `json:"groups"`
}

type adminNavigationGroup struct {
	Key   string                `json:"key"`
	Label string                `json:"label"`
	Items []adminNavigationItem `json:"items"`
}

type adminNavigationItem struct {
	Key                string   `json:"key"`
	Label              string   `json:"label"`
	Endpoint           string   `json:"endpoint"`
	Href               string   `json:"href"`
	ActivePrefixes     []string `json:"active_prefixes"`
	RequiredPermission string   `json:"required_permission"`
}

var ADMIN_NAV_GROUPS = loadAdminNavigationGroups()

func loadAdminNavigationGroups() []AdminNavGroup {
	var document adminNavigationDocument
	if err := json.Unmarshal(adminNavigationJSON, &document); err != nil || document.Version != 1 || len(document.Groups) == 0 {
		panic("webshell admin navigation configuration is invalid")
	}
	groups := make([]AdminNavGroup, 0, len(document.Groups))
	keys := map[string]struct{}{}
	endpoints := map[string]struct{}{}
	for _, sourceGroup := range document.Groups {
		if strings.TrimSpace(sourceGroup.Key) == "" || strings.TrimSpace(sourceGroup.Label) == "" || len(sourceGroup.Items) == 0 {
			panic("webshell admin navigation group is invalid")
		}
		group := AdminNavGroup{Title: sourceGroup.Label, Items: make([]AdminNavItem, 0, len(sourceGroup.Items))}
		for _, sourceItem := range sourceGroup.Items {
			if strings.TrimSpace(sourceItem.Key) == "" || strings.TrimSpace(sourceItem.Label) == "" || strings.TrimSpace(sourceItem.Endpoint) == "" || strings.TrimSpace(sourceItem.Href) == "" || len(sourceItem.ActivePrefixes) == 0 {
				panic("webshell admin navigation item is invalid")
			}
			if _, exists := keys[sourceItem.Key]; exists {
				panic("webshell admin navigation repeats a key")
			}
			if _, exists := endpoints[sourceItem.Endpoint]; exists {
				panic("webshell admin navigation repeats an endpoint")
			}
			route, exists := ADMIN_ROUTE_REGISTRY[sourceItem.Endpoint]
			if !exists || !strings.HasPrefix(route.Path, AdminRootPath) || route.Path != sourceItem.Href {
				panic("webshell admin navigation names an unavailable endpoint")
			}
			for _, prefix := range sourceItem.ActivePrefixes {
				if prefix != AdminRootPath && !strings.HasPrefix(prefix, AdminRootPath+"/") {
					panic("webshell admin navigation active prefix is invalid")
				}
			}
			if sourceItem.RequiredPermission != "" {
				if _, exists := ADMIN_ROUTE_REGISTRY[sourceItem.RequiredPermission]; !exists {
					panic("webshell admin navigation permission mapping is invalid")
				}
			}
			keys[sourceItem.Key] = struct{}{}
			endpoints[sourceItem.Endpoint] = struct{}{}
			group.Items = append(group.Items, AdminNavItem{Key: sourceItem.Key, Label: sourceItem.Label, Endpoint: sourceItem.Endpoint})
		}
		groups = append(groups, group)
	}
	return groups
}

// AdminNavGroups is the idiomatic Go alias for callers that do not need to
// mirror the source Python constant name.
var AdminNavGroups = ADMIN_NAV_GROUPS

// Breadcrumb is one item in the admin shell breadcrumb trail.
type Breadcrumb struct {
	Label string
	Href  string
}

// PageAction is an optional topbar link.  It is intentionally link-only; a
// write action must be implemented by a domain command handler first.
type PageAction struct {
	Label   string
	Href    string
	Variant string
}

// HeaderTab is an optional topbar tab.
type HeaderTab struct {
	Label  string
	Href   string
	Active bool
}

// AdminUser is the non-sensitive display-only portion of an authenticated
// operator.  The shell never resolves or logs identity values.
type AdminUser struct {
	DisplayName string
}

// AdminPageData is the data contract for the base admin shell and placeholder
// pages.  The zero value is safe and is normalized by the renderer.
type AdminPageData struct {
	PageTitle         string
	PageSummary       string
	ActiveEndpoint    string
	RequestPath       string
	Breadcrumbs       []Breadcrumb
	NavItems          []AdminNavGroup
	CurrentAdminUser  *AdminUser
	PageNotice        string
	PageError         string
	PageActions       []PageAction
	HeaderTabs        []HeaderTab
	ShowPageHeader    bool
	AdminActionTokens map[string]string
}

// LoginLinks contains the two reserved WeCom entry modes.
type LoginLinks struct {
	QR    string
	OAuth string
}

// LoginPageData is deliberately capability-neutral. It renders the login form
// but does not authenticate credentials or create a session.
type LoginPageData struct {
	PageTitle      string
	PageSummary    string
	PageNotice     string
	PageError      string
	NextPath       string
	FormAction     string
	LoginCSRFToken string
	LoginLinks     LoginLinks
	AuthModeLabel  string
}

// SidebarPageData supplies data attributes and initial shell state. The
// sidebar JavaScript consumes only the frozen bootstrap URLs; domain-owned
// handlers remain responsible for authentication, context, and workbench
// data.
type SidebarPageData struct {
	DebugEnabled    bool
	WorkbenchURL    string
	BindMobileURL   string
	JSSDKConfigURL  string
	ContextTokenURL string
}

// AdminPathFor resolves a known route without parameters.  Unknown routes
// return # so a missing capability cannot accidentally become an external URL.
func AdminPathFor(endpoint string) string {
	return PathFor(endpoint, nil)
}

// PathFor resolves a route and appends optional query parameters.  Dynamic
// route segments can be added by a later domain adapter; the shell itself does
// not interpolate untrusted identifiers into paths.
func PathFor(endpoint string, query map[string]string) string {
	if endpoint == "static" {
		filename := strings.TrimLeft(query["filename"], "/")
		if filename == "" {
			return "/static/"
		}
		return "/static/" + filename
	}
	switch endpoint {
	case "api.admin_console_customer_detail":
		customerID := query["customer_id"]
		value, err := strconv.ParseInt(customerID, 10, 64)
		if err != nil || value < 1 || strconv.FormatInt(value, 10) != customerID {
			return "#"
		}
		return "/admin/customers/" + customerID
	case "api.admin_cloud_orchestrator_plan_detail":
		return "/admin/cloud-orchestrator/plans/" + escapedSegment(query["plan_id"])
	case "api.admin_channel_edit_page":
		return "/admin/channels/" + escapedSegment(query["channel_id"]) + "/edit"
	case "api.admin_radar_link_edit":
		return "/admin/radar-links/" + escapedSegment(query["link_id"]) + "/edit"
	case "api.admin_radar_link_detail":
		return "/admin/radar-links/" + escapedSegment(query["link_id"]) + "/detail"
	case "api.admin_group_ops_plan_detail":
		return "/admin/automation-conversion/group-ops/plans/" + escapedSegment(query["plan_id"])
	case "api.admin_wechat_pay_transaction_detail_page":
		return "/admin/wechat-pay/transactions/" + escapedSegment(query["order_id"])
	case "api.admin_wechat_shop_transaction_detail_page":
		return "/admin/wechat-shop/transactions/" + escapedSegment(query["order_id"])
	case "api.admin_console_questionnaire_detail":
		return "/admin/questionnaires/" + escapedSegment(query["questionnaire_id"])
	case "api.admin_operation_cycle_strategy_page":
		return "/admin/operation-cycles/" + escapedSegment(query["strategy_key"])
	case "api.admin_operation_cycle_run_page":
		return "/admin/operation-cycles/" + escapedSegment(query["strategy_key"]) + "/runs/" + escapedSegment(query["run_key"])
	}
	route, ok := ADMIN_ROUTE_REGISTRY[endpoint]
	if !ok {
		return "#"
	}
	if len(query) == 0 {
		return route.Path
	}
	values := url.Values{}
	keys := make([]string, 0, len(query))
	for key, value := range query {
		if key == "" || value == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		values.Set(key, query[key])
	}
	if encoded := values.Encode(); encoded != "" {
		return route.Path + "?" + encoded
	}
	return route.Path
}

func escapedSegment(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

// SafeNextPath accepts only a local absolute path and prevents protocol
// relative redirects.  The value is used in a hidden form field and in the
// WeCom start link, but never causes a redirect in this shell package.
func SafeNextPath(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return AdminRootPath
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.IsAbs() || parsed.Host != "" {
		return AdminRootPath
	}
	return value
}

// NavItems returns a deep copy of the complete menu with stable hrefs and
// active flags.  Callers may safely customize the returned slice for one
// request without mutating the package contract.
func NavItems(activeEndpoint string) []AdminNavGroup {
	groups := make([]AdminNavGroup, 0, len(ADMIN_NAV_GROUPS))
	for _, sourceGroup := range ADMIN_NAV_GROUPS {
		group := AdminNavGroup{Title: sourceGroup.Title, Items: make([]AdminNavItem, 0, len(sourceGroup.Items))}
		for _, sourceItem := range sourceGroup.Items {
			item := sourceItem
			item.Active = item.Endpoint == activeEndpoint
			item.Href = AdminPathFor(item.Endpoint)
			group.Items = append(group.Items, item)
			group.Active = group.Active || item.Active
		}
		groups = append(groups, group)
	}
	return groups
}

// AdminPageForRequest constructs the standard shell context for a request.
// It contains no business data and no sample statistics.
func AdminPageForRequest(request *http.Request, title, summary, activeEndpoint string) AdminPageData {
	requestPath := ""
	if request != nil && request.URL != nil {
		requestPath = request.URL.Path
	}
	if title == "" {
		title = "管理后台"
	}
	if summary == "" {
		summary = "从导航选择需要处理的业务模块。"
	}
	return AdminPageData{
		PageTitle:      title,
		PageSummary:    summary,
		ActiveEndpoint: activeEndpoint,
		RequestPath:    requestPath,
		Breadcrumbs: []Breadcrumb{
			{Label: "用户管理后台", Href: AdminPathFor("api.admin_console_dashboard")},
		},
		NavItems:          NavItems(activeEndpoint),
		ShowPageHeader:    true,
		AdminActionTokens: map[string]string{},
	}
}

// DefaultLoginPage returns the neutral login shell context for a local next
// path.  The path is sanitized before being included in links or form fields.
func DefaultLoginPage(nextPath string) LoginPageData {
	nextPath = SafeNextPath(nextPath)
	return LoginPageData{
		PageTitle:   "后台登录",
		PageSummary: "企业微信负责“你是谁”，用户管理后台负责“你能做什么”。",
		NextPath:    nextPath,
		FormAction:  LoginPath,
		LoginLinks: LoginLinks{
			QR:    PathFor("api.auth_wecom_start", map[string]string{"mode": "qr", "next": nextPath}),
			OAuth: PathFor("api.auth_wecom_start", map[string]string{"mode": "oauth", "next": nextPath}),
		},
		AuthModeLabel: "企业微信认证",
	}
}

// DefaultSidebarPage returns the frozen bootstrap data URL contract. It is
// safe to render in any environment; the shell only attempts these URLs and
// leaves all unfinished business tabs as local empty states.
func DefaultSidebarPage() SidebarPageData {
	return SidebarPageData{
		WorkbenchURL:    SidebarWorkbenchPath,
		BindMobileURL:   SidebarPagePath,
		JSSDKConfigURL:  SidebarJSSDKPath,
		ContextTokenURL: SidebarContextPath,
	}
}

// cleanStaticPath keeps the static file server below /static and prevents a
// path traversal from reaching the embedded filesystem.
func cleanStaticPath(raw string) string {
	cleaned := path.Clean("/" + strings.TrimPrefix(raw, "/"))
	return strings.TrimPrefix(cleaned, "/")
}
