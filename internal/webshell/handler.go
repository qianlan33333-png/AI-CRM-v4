package webshell

import (
	"net/http"
	"strings"
)

// HandlerOptions configures the standalone shell handler.  Renderer is the
// only dependency; no Composition Root, store, provider, or domain service is
// accepted here by design.
type HandlerOptions struct {
	Renderer    *Renderer
	SidebarData SidebarPageData
	// DistDir optionally points at the built frontend (web/dist).  When set,
	// built admin documents are served directly in place of the placeholder
	// shell and the sidebar serves the built workbench.
	DistDir string
}

// Handler serves the shell pages and embedded static assets.  Reserved data
// endpoints return a controlled not-implemented response until their domain
// owners are mounted by a future composition root.
type Handler struct {
	renderer    *Renderer
	sidebarData SidebarPageData
	distDir     string
}

// NewHandler builds an independent httptest-friendly shell handler.  The
// optional form keeps the common no-configuration case concise while allowing
// callers to inject a renderer or fixed sidebar data in tests.
func NewHandler(options ...HandlerOptions) (http.Handler, error) {
	if len(options) > 1 {
		return nil, errTooManyHandlerOptions
	}
	var option HandlerOptions
	if len(options) == 1 {
		option = options[0]
	}
	renderer := option.Renderer
	if renderer == nil {
		var err error
		renderer, err = NewRenderer()
		if err != nil {
			return nil, err
		}
	}
	return &Handler{renderer: renderer, sidebarData: option.SidebarData, distDir: option.DistDir}, nil
}

// MustHandler is a convenience for small local previews and tests.
func MustHandler() http.Handler {
	handler, err := NewHandler()
	if err != nil {
		panic(err)
	}
	return handler
}

// NewHandlerWithRenderer makes the dependency explicit for callers that
// already constructed a Renderer.
func NewHandlerWithRenderer(renderer *Renderer) (http.Handler, error) {
	return NewHandler(HandlerOptions{Renderer: renderer})
}

var errTooManyHandlerOptions = &handlerOptionsError{}

type handlerOptionsError struct{}

func (*handlerOptionsError) Error() string {
	return "webshell accepts at most one HandlerOptions value"
}

func (handler *Handler) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	if handler == nil || handler.renderer == nil {
		http.Error(writer, "webshell handler is not initialized", http.StatusInternalServerError)
		return
	}
	requestPath := request.URL.Path
	switch {
	case strings.HasPrefix(requestPath, "/static/"):
		handler.renderer.ServeStatic(writer, request)
	case strings.HasPrefix(requestPath, "/sidebar-assets/"):
		handler.serveSidebarAsset(writer, request)
	case requestPath == LoginPath:
		handler.serveLogin(writer, request)
	case requestPath == WeComAuthStartPath:
		handler.serveWeComAuthStart(writer, request)
	case requestPath == "/referral" || requestPath == "/referral/":
		handler.serveReferral(writer, request)
	case requestPath == "/distribution" || requestPath == "/distribution/":
		handler.serveDistribution(writer, request)
	case requestPath == AdminRootPath || strings.HasPrefix(requestPath, AdminRootPath+"/"):
		handler.serveAdmin(writer, request)
	case requestPath == SidebarPagePath:
		handler.serveSidebar(writer, request)
	default:
		http.NotFound(writer, request)
	}
}

func (handler *Handler) serveLogin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead && request.Method != http.MethodPost {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead+", "+http.MethodPost)
		return
	}
	data := DefaultLoginPage(request.URL.Query().Get("next"))
	status := http.StatusOK
	if request.Method == http.MethodPost {
		// Do not parse or log credentials.  Access owns authentication and will
		// replace this controlled response when its contract is implemented.
		data.PageError = "本地登录暂未接入，请使用企业微信登录入口。"
		status = http.StatusNotImplemented
	}
	if err := handler.renderer.RenderLoginStatus(writer, status, data); err != nil {
		http.Error(writer, "unable to render login shell", http.StatusInternalServerError)
	}
}

func (handler *Handler) serveWeComAuthStart(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	data := DefaultLoginPage(request.URL.Query().Get("next"))
	data.PageError = "企业微信登录入口已预留；实际认证能力尚未接入。"
	if err := handler.renderer.RenderLoginStatus(writer, http.StatusNotImplemented, data); err != nil {
		http.Error(writer, "unable to render authentication shell", http.StatusInternalServerError)
	}
}

func (handler *Handler) serveDistribution(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	file, ok := DistDistributionPageFile(handler.distDir)
	if !ok {
		http.NotFound(writer, request)
		return
	}
	serveDistAdminPage(writer, request, file)
}

func (handler *Handler) serveAdmin(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	if request.URL.Path == "/admin/ops" {
		assets, ok := DistGovernanceAdminAssets(handler.distDir)
		if !ok {
			http.Error(writer, "运行治理页面资源不可用", http.StatusServiceUnavailable)
			return
		}
		if err := handler.renderer.RenderGovernance(writer, AdminPageForRequest(request, "运行治理", "", "api.admin_ops_governance"), assets); err != nil {
			http.Error(writer, "unable to render governance", http.StatusInternalServerError)
		}
		return
	}
	// The operating overview is a V3-owned server shell. It takes the admin
	// root before generic frozen-document fallback; its Host reads the
	// authenticated overview API and never relies on the donor home redirect.
	if request.URL.Path == AdminRootPath || request.URL.Path == AdminRootPath+"/" {
		spec := adminSpecForPath(AdminRootPath)
		if assets, ok := DistOverviewAdminAssets(handler.distDir); ok {
			if err := handler.renderer.RenderOverview(writer, AdminPageForRequest(request, spec.title, spec.summary, spec.activeEndpoint), assets); err != nil {
				http.Error(writer, "unable to render overview shell", http.StatusInternalServerError)
			}
			return
		}
		// Do not redirect the root into a generated donor document when the
		// current release has not yet supplied the Overview Host. The generic
		// shell is explicit and does not manufacture operating data.
		data := AdminPageForRequest(request, spec.title, spec.summary, spec.activeEndpoint)
		data.PageNotice = "经营数据页面资源正在准备，请稍后刷新。"
		if err := handler.renderer.RenderAdmin(writer, data); err != nil {
			http.Error(writer, "unable to render overview fallback shell", http.StatusInternalServerError)
		}
		return
	}
	// Distribution has employee-only facts but used to be served as a separate
	// built document. Capture both old and canonical paths before the generic
	// dist document fallback so it always retains the single admin_base shell.
	if request.URL.Path == "/admin/referral.html" {
		http.Redirect(writer, request, "/admin/referral", http.StatusSeeOther)
		return
	}
	if request.URL.Path == "/admin/referral" || request.URL.Path == "/admin/referral/settings" {
		assets, ok := DistReferralAdminAssets(handler.distDir)
		if !ok {
			http.Error(writer, "referral page unavailable", http.StatusServiceUnavailable)
			return
		}
		spec := adminSpecForPath(request.URL.Path)
		if err := handler.renderer.RenderReferral(writer, AdminPageForRequest(request, spec.title, spec.summary, spec.activeEndpoint), assets); err != nil {
			http.Error(writer, "unable to render referral shell", http.StatusInternalServerError)
		}
		return
	}
	if request.URL.Path == "/admin/distribution.html" {
		http.Redirect(writer, request, "/admin/distribution", http.StatusSeeOther)
		return
	}
	if request.URL.Path == "/admin/distribution" {
		assets, ok := DistDistributionAdminAssets(handler.distDir)
		if !ok {
			http.NotFound(writer, request)
			return
		}
		spec := adminSpecForPath(request.URL.Path)
		if err := handler.renderer.RenderDistribution(writer, AdminPageForRequest(request, spec.title, spec.summary, spec.activeEndpoint), assets); err != nil {
			http.Error(writer, "unable to render distribution shell", http.StatusInternalServerError)
		}
		return
	}
	if request.URL.Path == ComponentStatesPath {
		assets, ok := DistComponentStatesAssets(handler.distDir)
		if !ok {
			http.Error(writer, "component state demo unavailable", http.StatusServiceUnavailable)
			return
		}
		data := AdminPageForRequest(request, "共享组件状态示例", "仅展示本地示例状态；不会发送、保存或调用 Provider。", "")
		if err := handler.renderer.RenderComponentStates(writer, data, assets); err != nil {
			http.Error(writer, "component state demo unavailable", http.StatusInternalServerError)
		}
		return
	}
	// Built documents reference their runtime assets and sibling pages with
	// root-relative depth-1 URLs ("../assets/…", "customers.html").  Vanity
	// aliases nested deeper than /admin/<name>.html would resolve those
	// against the wrong base, so canonicalize onto the flat document path
	// before serving; the query string carries any detail-page parameters.
	if file, ok := DistAdminPageFile(handler.distDir, request.URL.Path); ok {
		name, _ := DistAdminPageName(request.URL.Path)
		if canonical := "/admin/" + name; request.URL.Path != canonical {
			target := canonical
			if request.URL.RawQuery != "" {
				target += "?" + request.URL.RawQuery
			}
			http.Redirect(writer, request, target, http.StatusSeeOther)
			return
		}
		serveDistAdminPage(writer, request, file)
		return
	}
	spec := adminSpecForPath(request.URL.Path)
	data := AdminPageForRequest(request, spec.title, spec.summary, spec.activeEndpoint)
	if request.URL.Path == "/admin/message-archive" || strings.HasPrefix(request.URL.Path, "/admin/message-archive/customers/") {
		// The shared topbar owns the one page title and this navigation action.
		// The archive body keeps only its route-specific guidance and filters.
		data.PageActions = []PageAction{{Label: "选择用户", Href: "/admin/customers", Variant: "primary"}}
	}
	if err := handler.renderer.RenderAdmin(writer, data); err != nil {
		http.Error(writer, "unable to render admin shell", http.StatusInternalServerError)
	}
}

func (handler *Handler) serveSidebar(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	if body, err := distSidebarDocument(handler.distDir); err == nil {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		writer.Header().Set("Cache-Control", "private, no-store")
		writer.WriteHeader(http.StatusOK)
		_, _ = writer.Write(body)
		return
	}
	if err := handler.renderer.RenderSidebar(writer, handler.sidebarData); err != nil {
		http.Error(writer, "unable to render sidebar shell", http.StatusInternalServerError)
	}
}

func methodNotAllowed(writer http.ResponseWriter, allow string) {
	writer.Header().Set("Allow", allow)
	writer.WriteHeader(http.StatusMethodNotAllowed)
}

type adminSpec struct {
	title          string
	summary        string
	activeEndpoint string
}

var adminSpecs = map[string]adminSpec{
	"/admin": {
		title:          "经营总览",
		summary:        "查看已确认的经营数据与需要处理的事项。",
		activeEndpoint: "api.admin_operating_overview",
	},
	"/admin/automation-conversion": {
		title:          "AI 自动化运营",
		summary:        "",
		activeEndpoint: "api.admin_automation_conversion",
	},
	"/admin/operation-cycles": {
		title:          "运营闭环",
		summary:        "运营闭环入口已预留。",
		activeEndpoint: "api.admin_operation_cycles_page",
	},
	"/admin/automation-conversion/group-ops/ui": {
		title:          "群运营计划",
		summary:        "群运营计划入口已预留。",
		activeEndpoint: "api.admin_group_ops_ui",
	},
	"/admin/channels": {
		title:          "渠道码中心",
		summary:        "渠道码中心入口已预留。",
		activeEndpoint: "api.admin_channels_page",
	},
	"/admin/group-invitations": {title: "群邀请", summary: "统一管理邀请计划与群聊目录。", activeEndpoint: "api.admin_group_invitations"},
	"/admin/cloud-orchestrator/plans": {
		title:          "AI 助手",
		summary:        "AI 助手入口已预留。",
		activeEndpoint: "api.admin_cloud_orchestrator_workspace",
	},
	"/admin/cloud-orchestrator/campaigns": {
		title:          "AI 助手 · 方案",
		summary:        "AI 助手方案工作区入口已预留。",
		activeEndpoint: "api.admin_cloud_orchestrator_workspace",
	},
	"/admin/cloud-orchestrator/observability": {
		title:          "AI 助手 · 观测",
		summary:        "AI 助手观测入口已预留。",
		activeEndpoint: "api.admin_cloud_orchestrator_workspace",
	},
	"/admin/message-archive": {
		title:          "会话存档",
		summary:        "从用户列表选择用户，查看已入库会话存档。",
		activeEndpoint: "",
	},
	"/admin/customers": {
		title:          "用户激活 / 用户列表",
		summary:        "从 v3 OneID 与企微同步投影查看用户，手机号默认脱敏。",
		activeEndpoint: "api.admin_console_customers",
	},
	"/admin/hxc-dashboard": {
		title:          "漏斗 / 数据看板",
		summary:        "数据看板入口已预留。",
		activeEndpoint: "api.admin_hxc_dashboard_workspace",
	},
	"/admin/questionnaires": {
		title:          "问卷",
		summary:        "问卷管理入口已预留。",
		activeEndpoint: "api.admin_questionnaires",
	},
	"/admin/radar-links": {
		title:          "内容雷达",
		summary:        "内容雷达入口已预留。",
		activeEndpoint: "api.admin_radar_links",
	},
	"/admin/wecom-tags": {
		title:          "企微标签管理",
		summary:        "企微标签管理入口已预留。",
		activeEndpoint: "api.admin_wecom_tags_page",
	},
	"/admin/orders": {
		title:          "交易管理",
		summary:        "交易后端尚未就绪；当前仅保留导航与明确不可用态，不读取订单或发起退款。",
		activeEndpoint: "api.admin_orders_page",
	},
	"/admin/wechat-pay/transactions": {
		title:          "交易管理",
		summary:        "交易后端尚未就绪；当前仅保留导航与明确不可用态，不读取订单或发起退款。",
		activeEndpoint: "api.admin_orders_page",
	},
	"/admin/wechat-pay/products": {
		title:          "商品管理",
		summary:        "商品管理入口已预留。",
		activeEndpoint: "api.admin_wechat_pay_products_page",
	},
	"/admin/service-period-products": {
		title:          "周期商品管理",
		summary:        "周期商品管理入口已预留。",
		activeEndpoint: "api.admin_service_period_products_page",
	},
	"/admin/referral": {
		title:          "裂变活动",
		summary:        "管理邀请活动、战队、排行榜和奖励。",
		activeEndpoint: "api.admin_referral_page",
	},
	"/admin/distribution": {
		title:          "分销管理",
		summary:        "查看分销员、归因订单与异常处理。",
		activeEndpoint: "api.admin_distribution_page",
	},
	"/admin/coupons": {
		title:          "优惠券",
		summary:        "优惠券入口已预留。",
		activeEndpoint: "api.admin_coupons_page",
	},
	"/admin/materials": {
		title:          "素材库",
		summary:        "图片、附件和小程序素材使用各自受权读写契约。",
		activeEndpoint: "api.admin_materials_workspace",
	},
	"/admin/image-library": {
		title:          "图片素材库",
		summary:        "图片素材库入口已预留。",
		activeEndpoint: "api.admin_image_library_workspace",
	},
	"/admin/miniprogram-library": {
		title:          "小程序素材库",
		summary:        "小程序素材库入口已预留。",
		activeEndpoint: "api.admin_miniprogram_library_workspace",
	},
	"/admin/attachment-library": {
		title:          "附件素材库",
		summary:        "附件素材库入口已预留。",
		activeEndpoint: "api.admin_attachment_library_workspace",
	},
	"/admin/automation-agents": {
		title:          "自动化话术",
		summary:        "自动化话术入口已预留。",
		activeEndpoint: "api.admin_automation_agents_page",
	},
	"/admin/owner-migration": {
		title:          "负责人迁移",
		summary:        "负责人迁移入口已预留。",
		activeEndpoint: "api.admin_owner_migration_page",
	},
	"/admin/config": {
		title:          "配置",
		summary:        "配置入口已预留。",
		activeEndpoint: "api.admin_config",
	},
	LoginAccessPath: {
		title:          "员工登录权限",
		summary:        "管理后台员工账号、角色与企微绑定。",
		activeEndpoint: "api.admin_config",
	},
	"/admin/api-docs": {
		title:          "API 文档",
		summary:        "API 文档入口已预留。",
		activeEndpoint: "api.admin_api_docs",
	},
}

func adminSpecForPath(requestPath string) adminSpec {
	if spec, ok := adminSpecs[requestPath]; ok {
		return spec
	}
	if strings.HasPrefix(requestPath, "/admin/customers/") {
		return adminSpec{title: "用户档案", summary: "按 Customer ID 查看分区式安全用户档案。", activeEndpoint: "api.admin_console_customers"}
	}
	if strings.HasPrefix(requestPath, "/admin/message-archive/customers/") {
		return adminSpec{title: "会话存档", summary: "仅显示已入库的本地会话存档。", activeEndpoint: ""}
	}
	for route, spec := range adminSpecs {
		if route != AdminRootPath && strings.HasPrefix(requestPath, route+"/") {
			return spec
		}
	}
	return adminSpec{
		title:          "管理后台",
		summary:        "v3 管理后台壳已就绪，业务能力按模块逐项接入。",
		activeEndpoint: "",
	}
}
