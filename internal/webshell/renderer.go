package webshell

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"time"
)

// The shell ships its presentation assets so an httptest or a future
// composition root can mount it without depending on the donor repository.
// Source commits and the verified production release are recorded in README.md.
//
//go:embed templates static
var embeddedWebAssets embed.FS

// Renderer renders only local shell templates.  It is safe for concurrent
// requests after construction because html/template execution is read-only.
type Renderer struct {
	templates *template.Template
	staticFS  fs.FS
}

// NewRenderer parses the embedded shell templates and prepares the embedded
// static filesystem, optionally reading the local presentation asset manifest.
// No network or database lookup occurs.
func NewRenderer(distDirectories ...string) (*Renderer, error) {
	distDir := ""
	if len(distDirectories) > 0 {
		distDir = distDirectories[0]
	}
	functions, err := presentationFunctions(distDir)
	if err != nil {
		return nil, err
	}
	templates, err := template.New("webshell").Funcs(functions).ParseFS(embeddedWebAssets, "templates/*.html")
	if err != nil {
		return nil, err
	}
	staticFS, err := fs.Sub(embeddedWebAssets, "static")
	if err != nil {
		return nil, err
	}
	return &Renderer{templates: templates, staticFS: staticFS}, nil
}

// AdminShellView is the fully rendered admin view returned by the renderer.
// It is exported for callers that want to prebuild a view, while the embedded
// templates remain the package's stable presentation boundary.
type AdminShellView struct {
	AdminPageData
	Content          template.HTML
	AudienceList     bool
	AudienceDetail   bool
	Customers        bool
	ExternalEffects  bool
	ExternalAssets   ExternalEffectsAssets
	HXC              bool
	HXCAssets        HXCAssets
	Media            bool
	MediaPage        string
	MediaAssets      MediaAssets
	Tags             bool
	TagsAssets       TagsAssets
	Product          bool
	ProductPage      string
	ProductAssets    ProductAssets
	Order            bool
	OrderPage        string
	OrderAssets      OrderAssets
	Coupons          bool
	CouponPage       string
	CouponAssets     CouponAssets
	Radar            bool
	RadarPage        string
	RadarAssets      RadarAssets
	GroupOps         bool
	GroupOpsPage     string
	GroupOpsStandard bool
	GroupOpsAssets   GroupOpsAssets
	Automation       bool
	AutomationPage   string
	AutomationAssets AutomationAssets
	// AutomationCreateCode is a v3 host binding for the frozen create form.
	// It is absent for existing records, whose immutable code stays donor-owned.
	AutomationCreateCode  string
	Survey                bool
	SurveyPage            string
	SurveyAssets          SurveyAssets
	OperationCycles       bool
	OperationPage         string
	OperationAssets       OperationCycleAssets
	Config                bool
	ConfigPage            string
	ConfigAssets          ConfigAssets
	RuntimeConfig         bool
	RuntimeConfigPage     string
	Channel               bool
	ChannelPage           string
	ChannelResourceID     string
	ChannelAssets         ChannelAssets
	AIAssistant           bool
	OwnerHandoff          bool
	MessageArchive        bool
	AIAssistantAssets     AIAssistantAssets
	Distribution          bool
	DistributionAssets    DistributionAssets
	ComponentStates       bool
	ComponentStatesAssets ComponentStatesAssets
	Governance            bool
	GovernanceAssets      OverviewAssets
	Overview              bool
	OverviewAssets        OverviewAssets
}

// ExternalEffectsAssets are manifest-derived URLs for the frozen donor bundle.
// They are data-only paths supplied by the composition adapter, never HTML.
type ExternalEffectsAssets struct {
	TokensCSS string
	LabsCSS   string
	AdminJS   string
}

// HXCAssets are release-manifest URLs owned by the HXC dashboard UI adapter.
// The shared shell receives URLs only and does not read dashboard data.
type HXCAssets struct{ TokensCSS, LabsCSS, AdminJS string }

// MediaAssets are manifest-derived URLs for the immutable Media donor bundle.
// They are supplied by the Media module's release-only UI adapter.
type MediaAssets struct{ TokensCSS, LabsCSS, AdminJS, MaterialSaveHostJS, ImageLibraryFilterHostJS, MaterialLibraryHostJS string }

// TagsAssets are manifest-derived frozen donor bundle paths. The tag page is
// mounted in admin_base and never publishes the donor's own shell/sidebar.
type TagsAssets struct{ TokensCSS, LabsCSS, AdminJS, PageHeaderActionHostJS string }

// ProductAssets are manifest-derived URLs for the frozen donor Product
// bundle. They are passed by the Product UI adapter and contain no markup.
type ProductAssets struct {
	TokensCSS, LabsCSS, ProductCSS, HostJS, StandardHostJS string
	StandardCSS                                            []string
}

// OrderAssets are release-manifest URLs for the frozen transaction UI.
type OrderAssets struct{ TokensCSS, LabsCSS, AdminJS, HostJS string }

// CouponAssets are verified manifest paths for the frozen coupon workspaces.
type CouponAssets struct{ TokensCSS, LabsCSS, AdminJS, HostJS string }

type RadarAssets struct{ TokensCSS, LabsCSS, AdminJS, HostJS, StandardHostJS, SelectionDialogCSS string }

// GroupOpsAssets are manifest-derived URLs for the immutable donor Group Ops
// bundle. The v3 shell owns the sidebar; the donor supplies only its stage
// template and runtime assets.
type GroupOpsAssets struct {
	TokensCSS, LabsCSS, AdminJS, ReadonlyCSS, ReadonlyJS string
	StandardCSS, HostJS, SelectionDialogCSS              string
	OperationPickerJS                                    string
	GroupPickerCSS, GroupPickerJS                        string
	MaterialPickerCSS, MaterialPickerJS                  string
	ComposerCSS, ComposerJS                              string
}

// AutomationAssets are manifest-derived frozen Agent bundle paths. The v3
// shell supplies only URLs; donor markup remains the extracted template.
type AutomationAssets struct {
	TokensCSS, LabsCSS, AdminJS                                        string
	PresentationCSS, ContentCSS, SelectionDialogCSS, MaterialPickerCSS string
	MaterialPickerJS, ContentHostJS                                    string
}

type SurveyAssets struct {
	TokensCSS, LabsCSS, AdminJS, EditorJS, EditorCSS, StandardHostJS, SurveyHostJS string
	OperationsHostJS, OperationsCSS                                                string
	StandardCSS                                                                    []string
}

// OperationCycleAssets keep the immutable donor presentation separate from
// the minimal v3 host binding that supplies real data and commands.
type OperationCycleAssets struct{ TokensCSS, LabsCSS, HostJS string }

// ConfigAssets are immutable donor runtime URLs supplied by the v3 config UI
// adapter. The v3 shell owns authentication and only mounts template#tpl.
type ConfigAssets struct{ TokensCSS, LabsCSS, AdminJS string }

type ChannelAssets struct {
	TokensCSS, LabsCSS, AdminJS, StandardHostJS string
	StandardCSS                                 []string
}
type AIAssistantAssets struct{ TokensCSS, LabsCSS, GroupCSS, MaterialCSS, ComposerCSS, ReadonlyCSS, HostJS, PageHeaderActionHostJS string }

// DistributionAssets is the small manifest-derived closure mounted inside the
// admin shell. It never contains a donor document or business data.
type DistributionAssets struct{ CSS, DetailDrawerCSS, AdminJS string }

// ComponentStatesAssets are the V3-owned presentation resources for the
// authenticated component-state demo. They contain no business data or API
// endpoint; the Host uses explicit in-memory fixture records only.
type ComponentStatesAssets struct {
	VisualTokensCSS, StylesCSS, SelectionDialogCSS string
	GroupOpsCSS, GroupPickerCSS, MaterialPickerCSS string
	HostJS                                         string
}

// OverviewAssets is the V3-owned stylesheet and Host module for the read-only
// operating overview. The shell receives only manifest-derived URLs; all
// business facts remain in the authorized overview HTTP endpoint.
type OverviewAssets struct{ CSS, DetailDrawerCSS, AdminJS string }

// Render implements the small presentation contract consumed by the Access
// HTTP handler. Keeping this adapter in webshell avoids a concrete import
// from Access into the UI package while still letting Access own login
// authentication, cookies, redirects, and error status codes.
//
// Only the reserved "login" view is accepted here. In particular, this
// method does not turn arbitrary names or request data into business pages.
// Values unrelated to the login shell (including credentials) are ignored.
func (renderer *Renderer) Render(_ context.Context, writer http.ResponseWriter, status int, name string, values map[string]any) error {
	if name != "login" {
		return errors.New("webshell renderer does not support view " + name)
	}

	data := DefaultLoginPage(stringValue(values, "next_path"))
	data.PageNotice = stringValue(values, "notice")
	data.PageError = friendlyLoginError(stringValue(values, "error"))
	data.LoginCSRFToken = stringValue(values, "login_csrf_token")
	if title := stringValue(values, "page_title"); title != "" {
		data.PageTitle = title
	}
	if summary := stringValue(values, "page_summary"); summary != "" {
		data.PageSummary = summary
	}
	return renderer.RenderLoginStatus(writer, status, data)
}

// RenderAdmin renders the standard admin base shell around the neutral
// placeholder body.  The body contains no business data.
func (renderer *Renderer) RenderAdmin(writer http.ResponseWriter, data AdminPageData) error {
	return renderer.RenderAdminStatus(writer, http.StatusOK, data)
}

// RenderAdminStatus is the status-aware variant used by controlled blocked
// routes.  The body is rendered before headers are written so template errors
// cannot produce a partial document.
func (renderer *Renderer) RenderAdminStatus(writer http.ResponseWriter, status int, data AdminPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("webshell renderer is not initialized")
	}
	normalizeAdminPage(&data)
	contentTemplate := "admin_placeholder"
	audienceList := data.RequestPath == "/admin/automation-conversion"
	audienceDetail := strings.HasPrefix(data.RequestPath, "/admin/automation-conversion/packages/")
	customers := data.RequestPath == "/admin/customers" || strings.HasPrefix(data.RequestPath, "/admin/customers/")
	archive := data.RequestPath == "/admin/message-archive" || strings.HasPrefix(data.RequestPath, "/admin/message-archive/customers/")
	if data.RequestPath == "/admin/group-invitations" {
		contentTemplate = "admin_invitations"
	} else if audienceList {
		contentTemplate = "admin_audience"
	} else if audienceDetail {
		contentTemplate = "admin_audience_detail"
	} else if data.RequestPath == LoginAccessPath {
		contentTemplate = "admin_access"
	} else if customers {
		contentTemplate = "admin_customers"
	} else if archive {
		contentTemplate = "admin_message_archive"
	}
	content, err := executeTemplate(renderer.templates, contentTemplate, data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData:  data,
		Content:        template.HTML(content), // child template already escaped all data
		AudienceList:   audienceList,
		AudienceDetail: audienceDetail,
		Customers:      customers,
		MessageArchive: archive,
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// RenderDistribution mounts the staff-only Distribution root in the one V3
// admin shell. The browser then reads the separately authorized API; Webshell
// does not receive or resolve any distribution facts.
func (renderer *Renderer) RenderDistribution(writer http.ResponseWriter, data AdminPageData, assets DistributionAssets) error {
	if renderer == nil || renderer.templates == nil || assets.CSS == "" || assets.AdminJS == "" {
		return errors.New("distribution shell assets are required")
	}
	normalizeAdminPage(&data)
	content := template.HTML(`<section id="distribution-admin-root" aria-live="polite"></section>`)
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: content, Distribution: true, DistributionAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderComponentStates mounts the V3 component-state demo in the existing
// admin shell. The generic /admin route remains protected by Access in the
// composition root; this renderer never receives an identity or domain data.
func (renderer *Renderer) RenderComponentStates(writer http.ResponseWriter, data AdminPageData, assets ComponentStatesAssets) error {
	if renderer == nil || renderer.templates == nil || assets.VisualTokensCSS == "" || assets.StylesCSS == "" || assets.SelectionDialogCSS == "" || assets.GroupOpsCSS == "" || assets.GroupPickerCSS == "" || assets.MaterialPickerCSS == "" || assets.HostJS == "" {
		return errors.New("component state shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = true
	content := template.HTML(`<section id="component-states-root" data-component-states-root aria-live="polite"></section>`)
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: content, ComponentStates: true, ComponentStatesAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderOverview mounts the V3 operating overview root in the standard admin
// shell. The Host independently fetches the already-authorized read-only API;
// this package never receives or resolves operating facts.
func (renderer *Renderer) RenderOverview(writer http.ResponseWriter, data AdminPageData, assets OverviewAssets) error {
	if renderer == nil || renderer.templates == nil || assets.CSS == "" || assets.DetailDrawerCSS == "" || assets.AdminJS == "" {
		return errors.New("overview shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = true
	content, err := executeTemplate(renderer.templates, "admin_overview", data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData:  data,
		Content:        template.HTML(content),
		Overview:       true,
		OverviewAssets: assets,
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderGovernance reuses the existing single admin shell and shared controls.
func (renderer *Renderer) RenderGovernance(writer http.ResponseWriter, data AdminPageData, assets OverviewAssets) error {
	if renderer == nil || renderer.templates == nil || assets.CSS == "" || assets.AdminJS == "" || assets.DetailDrawerCSS == "" {
		return errors.New("governance assets required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = true
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(`<section id="governance-admin-root" aria-live="polite"><p>正在读取治理数据…</p></section>`), Governance: true, GovernanceAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderExternalEffects mounts the immutable donor runtime inside the one v3
// admin shell. It deliberately renders only the original stage mount point;
// donor navigation and HTML are never embedded.
func (renderer *Renderer) RenderExternalEffects(writer http.ResponseWriter, data AdminPageData, assets ExternalEffectsAssets) error {
	if renderer == nil || renderer.templates == nil || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" {
		return errors.New("external effects shell assets are required")
	}
	normalizeAdminPage(&data)
	// The External Effects runtime is a V3-owned dynamic workspace. Its
	// controller renders a local heading for its content, but it does not own
	// the surrounding shell bar.
	data.ShowPageHeader = true
	content, err := executeTemplate(renderer.templates, "admin_external_effects", data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData:   data,
		Content:         template.HTML(content),
		ExternalEffects: true,
		ExternalAssets:  assets,
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderHXC mounts the live HXC dashboard controller inside the one v3 admin
// shell. The controller reads only the authenticated HXC dashboard API.
func (renderer *Renderer) RenderHXC(writer http.ResponseWriter, data AdminPageData, assets HXCAssets) error {
	if renderer == nil || renderer.templates == nil || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" {
		return errors.New("HXC dashboard shell assets are required")
	}
	normalizeAdminPage(&data)
	// HXC is a V3 dynamic content page: it has no donor-owned first toolbar,
	// so the shell owns the single standard topbar.
	data.ShowPageHeader = true
	content, err := executeTemplate(renderer.templates, "admin_hxc", data)
	if err != nil {
		return err
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{
		AdminPageData: data,
		Content:       template.HTML(content),
		HXC:           true,
		HXCAssets:     assets,
	})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderMedia mounts immutable Media donor templates in the v3 shell. The
// image library is source-owned and deliberately receives its own stable host;
// the other Media workspaces receive only verified release templates.
//
// The frozen templates expose display names but the running controller carries
// a stable resourceId for every attachment and mini-program. Add that ID at the
// v3 render seam so presentation code can join read-only metadata without
// guessing from a name or row position. This changes the rendered copy only;
// the byte-frozen release template remains untouched. A changed loop shape
// fails closed instead of silently reintroducing a name-based join.
func materialTemplateIdentitySeams(page, donorTemplate string) (string, error) {
	type seam struct {
		loop   string
		needle string
		withID string
	}
	var expected seam
	switch page {
	case "attach":
		expected = seam{
			loop:   `data-sc-for="{{ rows.attachItems }}"`,
			needle: `<tr style="{{ a.rowStyle }}">`,
			withID: `<tr data-material-library-id="{{ a.resourceId }}" style="{{ a.rowStyle }}">`,
		}
	case "mpLib":
		expected = seam{
			loop:   `data-sc-for="{{ rows.mpItems }}"`,
			needle: `<div style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden">`,
			withID: `<div data-material-library-id="{{ m.resourceId }}" style="background:#fff;border:1px solid #DEE0E3;border-radius:8px;overflow:hidden">`,
		}
	default:
		return donorTemplate, nil
	}
	if strings.Count(donorTemplate, expected.loop) != 1 || strings.Count(donorTemplate, expected.needle) != 1 {
		return "", errors.New("media donor identity seam is missing or ambiguous")
	}
	return strings.Replace(donorTemplate, expected.needle, expected.withID, 1), nil
}

func (renderer *Renderer) RenderMedia(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets MediaAssets) error {
	if renderer == nil || renderer.templates == nil || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.MaterialSaveHostJS == "" || assets.MaterialLibraryHostJS == "" || (page == "images" && assets.ImageLibraryFilterHostJS == "") || (page != "images" && page != "attach" && page != "mpLib") || (page != "images" && donorTemplate == "") {
		return errors.New("media shell assets are required")
	}
	normalizeAdminPage(&data)
	unified := data.RequestPath == "/admin/materials"
	data.ShowPageHeader = unified
	workspaceAttribute := ""
	if unified {
		workspaceAttribute = ` data-material-library-workspace="true"`
	}
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"` + workspaceAttribute + `></main>`
	if page == "images" {
		content = `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded" data-image-library-v3-root` + workspaceAttribute + `></main>`
	} else {
		var seamErr error
		donorTemplate, seamErr = materialTemplateIdentitySeams(page, donorTemplate)
		if seamErr != nil {
			return seamErr
		}
		content += `<template id="tpl">` + donorTemplate + `</template>`
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Media: true, MediaPage: page, MediaAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderTags mounts the complete, byte-frozen tags workspace into the one v3
// sidebar shell.  The supplied template was extracted from a verified release
// asset by the tag module, not from request input.
func (renderer *Renderer) RenderTags(writer http.ResponseWriter, data AdminPageData, donorTemplate string, assets TagsAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.PageHeaderActionHostJS == "" {
		return errors.New("tags shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = true
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Tags: true, TagsAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderProducts mounts one allowlisted Product template into the existing
// PR10 shell. The donor template is the release-built template#tpl fragment;
// this method never renders the donor document or a second sidebar.
func (renderer *Renderer) RenderProducts(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets ProductAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.ProductCSS == "" || assets.HostJS == "" || assets.StandardHostJS == "" || len(assets.StandardCSS) != 5 || (page != "products" && page != "productForm" && page != "spProducts" && page != "spProductForm" && page != "spProductData") {
		return errors.New("product shell assets are required")
	}
	normalizeAdminPage(&data)
	// Product list pages use the shared V3 shell title and action slot; form
	// pages use the same shell header for their existing editor actions. The
	// Product adapter retains original controls while removing duplicate donor headings.
	data.ShowPageHeader = page == "products" || page == "spProducts" || page == "productForm" || page == "spProductForm" || page == "spProductData"
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Product: true, ProductPage: page, ProductAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderOrders mounts the frozen list/detail transaction templates into the
// v3 shell. The host-owned import panel is deliberately separate from donor
// markup and can only call the narrow order-only migration API.
func (renderer *Renderer) RenderOrders(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets OrderAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.HostJS == "" || (page != "orders" && page != "orderDetail") {
		return errors.New("order shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	panel := ""
	if page == "orders" {
		panel = `<details class="order-import-panel" data-order-import><summary>生产订单快照迁移（超级管理员）</summary><div class="order-import-panel__body"><input type="file" accept="application/json,.json" data-order-import-file><label><input type="checkbox" data-order-import-confirm> 已核对快照摘要与 SHA-256，确认仅导入历史订单</label><div class="order-import-panel__actions"><button type="button" data-order-import-action="inspect">检查快照</button><button type="button" data-order-import-action="apply" disabled>导入生产</button><button type="button" data-order-import-action="reconcile" disabled>对账</button></div><pre data-order-import-status>请选择订单快照文件。</pre></div></details>`
	}
	// The frozen order workspace owns the first row of the business page. Keep
	// the V3-only history import control in the same Host, after that workspace,
	// so it cannot inset or displace the donor toolbar at the shell boundary.
	content := `<div class="order-host-layout"><main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>` + panel + `</div><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Order: true, OrderPage: page, OrderAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderCoupons mounts a verified coupons/couponForm donor template inside
// the only v3 admin shell; it never serves the donor's outer HTML document.
func (renderer *Renderer) RenderCoupons(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets CouponAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.HostJS == "" || (page != "coupons" && page != "couponForm" && page != "couponData") {
		return errors.New("coupon shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Coupons: true, CouponPage: page, CouponAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderRadar mounts the byte-frozen Radar runtime in the v3 shell. Its host
// bridge is additive v3 code and therefore remains outside the donor hash set.
func (renderer *Renderer) RenderRadar(writer http.ResponseWriter, data AdminPageData, page string, assets RadarAssets) error {
	if renderer == nil || renderer.templates == nil || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.HostJS == "" || assets.StandardHostJS == "" || (page != "radar" && page != "radarDetail" && page != "radarForm") {
		return errors.New("radar shell assets are required")
	}
	normalizeAdminPage(&data)
	// Radar replaces the stage class while it mounts. Keep the V3 shell bar
	// outside that mutable workspace so the page has one stable topbar.
	data.ShowPageHeader = true
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Radar: true, RadarPage: page, RadarAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderChannels mounts the two byte-frozen channel templates. Resource IDs
// are supplied as inert body data for the v3 host adapter; they are never
// interpolated into donor markup.
func (renderer *Renderer) RenderChannels(writer http.ResponseWriter, data AdminPageData, page, resourceID, donorTemplate string, assets ChannelAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.StandardHostJS == "" || len(assets.StandardCSS) != 6 || (page != "channels" && page != "channelForm") {
		return errors.New("channel shell assets are required")
	}
	if resourceID != "" {
		if id, err := strconv.ParseInt(resourceID, 10, 64); err != nil || id < 1 || strconv.FormatInt(id, 10) != resourceID {
			return errors.New("channel resource ID is invalid")
		}
	}
	normalizeAdminPage(&data)
	// The channel list uses the shared V3 topbar for its one title and create
	// action. The form keeps its existing embedded layout and controls.
	data.ShowPageHeader = page == "channels"
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Channel: true, ChannelPage: page, ChannelResourceID: resourceID, ChannelAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderGroupOps mounts the active donor plan list/detail templates into the
// single v3 admin_base sidebar. It accepts only the page names selected by
// the Group Ops UI adapter and never receives request-controlled HTML.
func (renderer *Renderer) RenderGroupOps(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets GroupOpsAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || assets.ReadonlyCSS == "" || assets.ReadonlyJS == "" || (page != "groupops" && page != "groupopsDetail") {
		return errors.New("Group Ops shell assets are required")
	}
	standard := strings.Contains(donorTemplate, `data-group-ops-standard-host="true"`)
	if standard && (assets.StandardCSS == "" || assets.HostJS == "" || assets.SelectionDialogCSS == "" || assets.OperationPickerJS == "" || assets.GroupPickerCSS == "" || assets.GroupPickerJS == "" || assets.MaterialPickerCSS == "" || assets.MaterialPickerJS == "" || assets.ComposerCSS == "" || assets.ComposerJS == "") {
		return errors.New("Group Ops standard host assets are required")
	}
	normalizeAdminPage(&data)
	content := ""
	if standard {
		// The standard Group Ops page was extracted from the source admin base,
		// where the shell owns its one breadcrumb/title bar and main.admin-page
		// supplies the business-content inset. Its V3 host begins with actions,
		// not a second page heading, so keep that same native shell boundary.
		data.ShowPageHeader = true
		content = `<main id="stage" class="admin-page" data-group-ops-standard-stage>` + donorTemplate + `</main>`
	} else {
		data.ShowPageHeader = false
		content += `<template id="tpl">` + donorTemplate + `</template>`
		content = `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main>` + `<template id="tpl">` + donorTemplate + `</template>`
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), GroupOps: true, GroupOpsPage: page, GroupOpsStandard: standard, GroupOpsAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

func (renderer *Renderer) RenderAIAssistant(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets AIAssistantAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.HostJS == "" || assets.PageHeaderActionHostJS == "" || (page != "list" && page != "detail") {
		return errors.New("AI Assistant shell assets are required")
	}
	normalizeAdminPage(&data)
	// The native cloud-plan fragment begins with filtering controls rather than
	// its own page title. It therefore uses the one Webshell topbar and the
	// dynamic content inset; the business toolbar and review actions remain in
	// the existing fragment below it.
	data.ShowPageHeader = true
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(`<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--dynamic">` + donorTemplate + `</main>`), AIAssistant: true, AIAssistantAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderOwnerHandoff mounts the V3-owned Host for the byte-frozen owner-migration
// route. The Host owns only HTTP adaptation; it never calls a Provider itself.
func (renderer *Renderer) RenderOwnerHandoff(writer http.ResponseWriter, data AdminPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("owner handoff shell is unavailable")
	}
	normalizeAdminPage(&data)
	// The frozen donor has a local title/status row. The V3 host keeps its
	// actions but renders the sole shell topbar around it.
	data.ShowPageHeader = true
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded" data-owner-handoff-host></main>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), OwnerHandoff: true})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderAutomation mounts one verified Agent template into the existing v3
// admin shell. The outer donor document is never independently served.
func (renderer *Renderer) RenderAutomation(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets AutomationAssets, createCode string) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "agents" && page != "agentEdit") {
		return errors.New("automation shell assets are required")
	}
	if page == "agentEdit" && (assets.PresentationCSS == "" || assets.ContentCSS == "" || assets.SelectionDialogCSS == "" || assets.MaterialPickerCSS == "" || assets.MaterialPickerJS == "" || assets.ContentHostJS == "") {
		return errors.New("automation content host assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Automation: true, AutomationPage: page, AutomationAssets: assets, AutomationCreateCode: createCode})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderSurvey mounts only the frozen question workspace fragment into the
// v3 admin shell. The editor bootstrap contains no record data; it directs the
// frozen controller to the v3 API adapter.
func (renderer *Renderer) RenderSurvey(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets SurveyAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || (assets.AdminJS == "" && assets.SurveyHostJS == "") || assets.EditorJS == "" || assets.EditorCSS == "" || assets.OperationsHostJS == "" || assets.OperationsCSS == "" || (page != "questionnaires" && page != "questionnaireDetail" && page != "questionnaireOps") {
		return errors.New("survey shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	if page == "questionnaireDetail" {
		content = donorTemplate
	}
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Survey: true, SurveyPage: page, SurveyAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderOperationCycles mounts one byte-frozen donor template inside the one
// v3 sidebar. Its host binding starts the frozen donor main -> legacy ->
// AdminController runtime after supplying only the operation-cycle read DTO.
func (renderer *Renderer) RenderOperationCycles(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets OperationCycleAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.HostJS == "" || (page != "cycles" && page != "cyclesDetail") {
		return errors.New("operation-cycle shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<base href="/admin/operation-cycles/"><main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), OperationCycles: true, OperationPage: page, OperationAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderRuntimeConfig mounts the Config-owned host for the proven draft →
// validate → publish → rollback interaction. It deliberately does not run the
// frozen AdminOps JavaScript: that DTO neither owns nor understands the closed
// runtime-release catalog.
func (renderer *Renderer) RenderRuntimeConfig(writer http.ResponseWriter, data AdminPageData, page, hostTemplate string) error {
	if renderer == nil || renderer.templates == nil || hostTemplate == "" || (page != "runtimeConfigCenter" && page != "runtimeConfigCategory" && page != "runtimeReleaseList" && page != "runtimeReleaseNew" && page != "runtimeReleaseDetail") {
		return errors.New("runtime config shell is required")
	}
	normalizeAdminPage(&data)
	// Runtime configuration is a V3 host rather than a donor-toolbar page, so
	// it must retain the shell's standard topbar above its release cards.
	data.ShowPageHeader = true
	content := `<main id="runtime-release-host" class="admin-page" data-runtime-release-host>` + hostTemplate + `</main>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), RuntimeConfig: true, RuntimeConfigPage: page})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderConfig mounts the frozen Config/AdminOps template in the authenticated
// v3 sidebar shell. All runtime compatibility belongs to the config module;
// donor document files are never independently exposed.
func (renderer *Renderer) RenderConfig(writer http.ResponseWriter, data AdminPageData, page, donorTemplate string, assets ConfigAssets) error {
	if renderer == nil || renderer.templates == nil || donorTemplate == "" || assets.TokensCSS == "" || assets.LabsCSS == "" || assets.AdminJS == "" || (page != "config" && page != "configDetail" && page != "apidocs") {
		return errors.New("config shell assets are required")
	}
	normalizeAdminPage(&data)
	data.ShowPageHeader = false
	content := `<main id="stage" class="stage rich admin-workspace-stage admin-workspace-stage--embedded"></main><template id="tpl">` + donorTemplate + `</template>`
	body, err := executeTemplate(renderer.templates, "admin_base", AdminShellView{AdminPageData: data, Content: template.HTML(content), Config: true, ConfigPage: page, ConfigAssets: assets})
	if err != nil {
		return err
	}
	return writeHTML(writer, http.StatusOK, body)
}

// RenderLogin renders the login shell.  It does not authenticate or issue a
// session; POST handling is intentionally owned by Access in a later slice.
func (renderer *Renderer) RenderLogin(writer http.ResponseWriter, data LoginPageData) error {
	return renderer.RenderLoginStatus(writer, http.StatusOK, data)
}

// RenderLoginStatus renders a login page with a controlled HTTP status, useful
// for the reserved WeCom start and local POST routes.
func (renderer *Renderer) RenderLoginStatus(writer http.ResponseWriter, status int, data LoginPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("webshell renderer is not initialized")
	}
	normalizeLoginPage(&data)
	body, err := executeTemplate(renderer.templates, "login_page", data)
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// RenderSidebar renders the WeCom sidebar shell with reserved bootstrap URLs.
// The renderer itself never resolves a customer or contacts a provider; its
// browser asset may invoke only the explicitly reserved domain endpoints.
func (renderer *Renderer) RenderSidebar(writer http.ResponseWriter, data SidebarPageData) error {
	return renderer.RenderSidebarStatus(writer, http.StatusOK, data)
}

// RenderSidebarStatus is the status-aware variant for a future adapter to use
// when the shell is mounted behind a capability gate.
func (renderer *Renderer) RenderSidebarStatus(writer http.ResponseWriter, status int, data SidebarPageData) error {
	if renderer == nil || renderer.templates == nil {
		return errors.New("webshell renderer is not initialized")
	}
	normalizeSidebarPage(&data)
	body, err := executeTemplate(renderer.templates, "sidebar_page", data)
	if err != nil {
		return err
	}
	return writeHTML(writer, status, body)
}

// ServeStatic serves one embedded shell asset under /static.  It intentionally
// avoids a directory listing and rejects traversal outside the embedded tree.
func (renderer *Renderer) ServeStatic(writer http.ResponseWriter, request *http.Request) {
	if renderer == nil || renderer.staticFS == nil {
		http.Error(writer, "webshell assets are not initialized", http.StatusInternalServerError)
		return
	}
	if request.Method != http.MethodGet && request.Method != http.MethodHead {
		methodNotAllowed(writer, http.MethodGet+", "+http.MethodHead)
		return
	}
	relative := strings.TrimPrefix(request.URL.Path, "/static/")
	relative = cleanStaticPath(relative)
	if relative == "" || relative == "." {
		http.NotFound(writer, request)
		return
	}
	file, err := renderer.staticFS.Open(relative)
	if err != nil {
		http.NotFound(writer, request)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		http.NotFound(writer, request)
		return
	}
	content, err := io.ReadAll(file)
	if err != nil {
		http.Error(writer, "unable to read webshell asset", http.StatusInternalServerError)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(strings.ToLower(info.Name())))
	if contentType == "" {
		contentType = http.DetectContentType(content)
	}
	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("Cache-Control", "public, max-age=3600")
	http.ServeContent(writer, request, info.Name(), time.Time{}, bytes.NewReader(content))
}

func executeTemplate(templates *template.Template, name string, data any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := templates.ExecuteTemplate(&buffer, name, data); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func writeHTML(writer http.ResponseWriter, status int, body []byte) error {
	writer.Header().Set("Content-Type", "text/html; charset=utf-8")
	writer.Header().Set("Cache-Control", "private, no-store")
	writer.WriteHeader(status)
	_, err := writer.Write(body)
	return err
}

func normalizeAdminPage(data *AdminPageData) {
	if data.PageTitle == "" {
		data.PageTitle = "管理后台"
	}
	if data.PageSummary == "" {
		data.PageSummary = "v3 管理后台壳已就绪，业务能力按模块逐项接入。"
	}
	if data.Breadcrumbs == nil {
		data.Breadcrumbs = []Breadcrumb{{Label: "用户管理后台", Href: AdminRootPath}}
	}
	if data.NavItems == nil {
		data.NavItems = NavItems(data.ActiveEndpoint)
	}
	if data.AdminActionTokens == nil {
		data.AdminActionTokens = map[string]string{}
	}
	// The shell has a single page-level title/header.  A future caller may
	// introduce a richer layout only by adding an explicit template contract.
	data.ShowPageHeader = true
}

func normalizeLoginPage(data *LoginPageData) {
	if data.PageTitle == "" {
		data.PageTitle = "后台登录"
	}
	if data.PageSummary == "" {
		data.PageSummary = "企业微信负责“你是谁”，用户管理后台负责“你能做什么”。"
	}
	data.NextPath = SafeNextPath(data.NextPath)
	if data.FormAction == "" {
		data.FormAction = LoginPath
	}
	if data.LoginLinks.QR == "" || data.LoginLinks.OAuth == "" {
		defaults := DefaultLoginPage(data.NextPath)
		if data.LoginLinks.QR == "" {
			data.LoginLinks.QR = defaults.LoginLinks.QR
		}
		if data.LoginLinks.OAuth == "" {
			data.LoginLinks.OAuth = defaults.LoginLinks.OAuth
		}
	}
	if data.AuthModeLabel == "" {
		data.AuthModeLabel = "企业微信登录（待接入）"
	}
}

func normalizeSidebarPage(data *SidebarPageData) {
	defaults := DefaultSidebarPage()
	if data.WorkbenchURL == "" {
		data.WorkbenchURL = defaults.WorkbenchURL
	}
	if data.BindMobileURL == "" {
		data.BindMobileURL = defaults.BindMobileURL
	}
	if data.JSSDKConfigURL == "" {
		data.JSSDKConfigURL = defaults.JSSDKConfigURL
	}
	if data.ContextTokenURL == "" {
		data.ContextTokenURL = defaults.ContextTokenURL
	}
}

func stringValue(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(text)
}

func friendlyLoginError(code string) string {
	switch strings.TrimSpace(code) {
	case "":
		return ""
	case "invalid_credentials", "authentication_required":
		return "账号或密码不正确，请重试。"
	case "csrf_required":
		return "页面安全令牌已失效，请刷新后重试。"
	case "invalid_request":
		return "请输入有效的账号和密码。"
	case "rate_limited":
		return "尝试次数过多，请稍后再试。"
	case "permission_denied":
		return "当前账号没有登录权限，请联系管理员。"
	case "not_found", "conflict", "internal_error":
		return "登录服务暂时不可用，请稍后重试。"
	default:
		return "登录失败，请稍后重试。"
	}
}
