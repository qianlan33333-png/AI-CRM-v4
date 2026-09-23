package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	hxcdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/domain"
	hxcstore "github.com/qianlan33333-png/AI-CRM-v3/internal/hxcdashboard/store"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	platformpostgres "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/postgres"
)

// OneID decision: not involved. The fixture seeds a public-safe HXC projection
// only to exercise the existing dashboard UI; it neither resolves nor changes
// an identity. Persistence decision: this is a test-only PostgreSQL fixture;
// the production layout code is stateless and never writes or invokes a
// Provider.
type adminShellLayoutFixture struct {
	*productExternalPushChromiumFixture
	screenshots          string
	radarID              int64
	aiPlanID             int64
	nativeOrderReference string
	archiveProductID     int64
	couponID             int64
}

// TestPostgreSQLAdminShellLayoutCompositionPreflight keeps the real release
// artifact and outer Composition routes under the ordinary PostgreSQL check.
// Chromium is deliberately a separate mandatory Linux step below.
func TestPostgreSQLAdminShellLayoutCompositionPreflight(t *testing.T) {
	fixture := newAdminShellLayoutFixture(t)
	session, csrf := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	refresh := httptest.NewRequest(http.MethodPost, "/api/admin/hxc-dashboard/refreshes", nil)
	refresh.Header.Set("Idempotency-Key", "admin-shell-layout-hxc-refresh")
	refresh.Header.Set("X-CSRF-Token", csrf)
	refresh.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	refresh.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	refreshResponse := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(refreshResponse, refresh)
	if refreshResponse.Code != http.StatusServiceUnavailable || !strings.Contains(refreshResponse.Body.String(), `"hxc_sync_disabled"`) {
		t.Fatalf("HXC refresh binding status=%d disabled=%t", refreshResponse.Code, strings.Contains(refreshResponse.Body.String(), `"hxc_sync_disabled"`))
	}

	radarList := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/radar-links")
	radarDetail := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/radar-links/"+strconv.FormatInt(fixture.radarID, 10))
	if radarList.Code != http.StatusOK || !strings.Contains(radarList.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)) || radarDetail.Code != http.StatusOK || !strings.Contains(radarDetail.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)) {
		t.Fatalf("admin layout radar read list_status=%d list_seeded=%t detail_status=%d detail_seeded=%t", radarList.Code, strings.Contains(radarList.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)), radarDetail.Code, strings.Contains(radarDetail.Body.String(), `"link_id":`+strconv.FormatInt(fixture.radarID, 10)))
	}
	visitorRequest := httptest.NewRequest(http.MethodGet, "/api/admin/radar-links/"+strconv.FormatInt(fixture.radarID, 10)+"/visitors?limit=100&offset=0", nil)
	visitorRequest.Header.Set("X-CSRF-Token", csrf)
	visitorRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	visitorRequest.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	visitorResponse := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(visitorResponse, visitorRequest)
	var visitors struct {
		Items []struct {
			Nickname              *string   `json:"nickname"`
			ExternalContactID     *string   `json:"external_contact_id"`
			ExternalContactStatus string    `json:"external_contact_status"`
			OneID                 *string   `json:"oneid"`
			OpenedAt              time.Time `json:"opened_at"`
			AttributionStatus     string    `json:"attribution_status"`
		} `json:"items"`
		Total int64 `json:"total"`
	}
	if err := json.Unmarshal(visitorResponse.Body.Bytes(), &visitors); err != nil || visitorResponse.Code != http.StatusOK || visitorResponse.Header().Get("Cache-Control") != "no-store" || visitors.Total != 1 || len(visitors.Items) != 1 || visitors.Items[0].Nickname == nil || *visitors.Items[0].Nickname != "雷达布局访客" || visitors.Items[0].ExternalContactID == nil || *visitors.Items[0].ExternalContactID != "external-radar-layout-001" || visitors.Items[0].ExternalContactStatus != "available" || visitors.Items[0].OneID == nil || !regexp.MustCompile(`^[1-9][0-9]{6}$`).MatchString(*visitors.Items[0].OneID) || !visitors.Items[0].OpenedAt.Equal(time.Date(2026, time.September, 7, 1, 2, 3, 0, time.UTC)) || visitors.Items[0].AttributionStatus != "resolved" {
		t.Fatalf("admin layout visitor read status=%d cache=%q total=%d items=%d decode=%v", visitorResponse.Code, visitorResponse.Header().Get("Cache-Control"), visitors.Total, len(visitors.Items), err)
	}
	aiPlan := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/ai-assistant/plans/"+strconv.FormatInt(fixture.aiPlanID, 10))
	aiRecipients := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/ai-assistant/plans/"+strconv.FormatInt(fixture.aiPlanID, 10)+"/recipients?limit=50")
	if aiPlan.Code != http.StatusOK || !strings.Contains(aiPlan.Body.String(), `"id":`+strconv.FormatInt(fixture.aiPlanID, 10)) || aiRecipients.Code != http.StatusOK || !strings.Contains(aiRecipients.Body.String(), `"items"`) {
		t.Fatalf("admin layout native AI read plan_status=%d plan_seeded=%t recipients_status=%d recipients=%t", aiPlan.Code, strings.Contains(aiPlan.Body.String(), `"id":`+strconv.FormatInt(fixture.aiPlanID, 10)), aiRecipients.Code, strings.Contains(aiRecipients.Body.String(), `"items"`))
	}
	nativeOrder := authenticatedAdminGet(t, fixture.application.handler, session, "/api/admin/orders/"+fixture.nativeOrderReference)
	if nativeOrder.Code != http.StatusOK || !strings.Contains(nativeOrder.Body.String(), `"record_origin":"native"`) || !strings.Contains(nativeOrder.Body.String(), `"refundable_amount_total":2000`) {
		t.Fatalf("admin layout native order detail status=%d native=%t refundable=%t", nativeOrder.Code, strings.Contains(nativeOrder.Body.String(), `"record_origin":"native"`), strings.Contains(nativeOrder.Body.String(), `"refundable_amount_total":2000`))
	}

	navigation := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion")
	if navigation.Code != http.StatusOK {
		t.Fatalf("admin layout navigation status=%d", navigation.Code)
	}
	for _, href := range []string{
		"/admin/automation-conversion", "/admin/operation-cycles", "/admin/automation-conversion/group-ops/ui", "/admin/channels", "/admin/cloud-orchestrator/plans", "/admin/customers", "/admin/hxc-dashboard", "/admin/questionnaires", "/admin/radar-links", "/admin/wecom-tags", "/admin/orders", "/admin/wechat-pay/products", "/admin/service-period-products", "/admin/coupons", "/admin/materials", "/admin/automation-agents", "/admin/owner-migration", "/admin/config", "/admin/api-docs",
	} {
		if !strings.Contains(navigation.Body.String(), `href="`+href+`"`) {
			t.Fatalf("admin layout navigation href=%q is absent from the actual Webshell menu", href)
		}
	}

	for _, route := range []struct {
		path            string
		canonicalPath   string
		canonicalStatus int
		marker          string
		expectTopbar    bool
	}{
		// Main navigation: every entry remains its actual UI owner rather than a
		// static fallback. The representative Chromium journey below measures the
		// three distinct layout types.
		{path: "/admin/automation-conversion", marker: `class="admin-topbar"`, expectTopbar: true},
		{path: "/admin/external-effects?view=external-effects", canonicalPath: "/admin/campaigns.html?view=external-effects", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/operation-cycles", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/automation-conversion/group-ops/ui", canonicalPath: "/admin/groupops.html", canonicalStatus: http.StatusFound, marker: `data-group-ops-standard-stage`, expectTopbar: true},
		{path: "/admin/groupops.html", marker: `data-group-ops-standard-stage`, expectTopbar: true},
		{path: "/admin/channels", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/channels/new", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/cloud-orchestrator/plans", marker: `data-cloud-plan-root`, expectTopbar: true},
		{path: "/admin/cloud-orchestrator/plans/", marker: `data-cloud-plan-root`, expectTopbar: true},
		{path: "/admin/cloud-orchestrator/plans/" + strconv.FormatInt(fixture.aiPlanID, 10), marker: `data-plan-detail-state`, expectTopbar: true},
		{path: "/admin/ai.html", canonicalPath: "/admin/cloud-orchestrator/plans", canonicalStatus: http.StatusFound, marker: `data-cloud-plan-root`, expectTopbar: true},
		{path: "/admin/aiDetail.html?id=" + strconv.FormatInt(fixture.aiPlanID, 10), canonicalPath: "/admin/cloud-orchestrator/plans/" + strconv.FormatInt(fixture.aiPlanID, 10), canonicalStatus: http.StatusFound, marker: `data-plan-detail-state`, expectTopbar: true},
		{path: "/admin/customers", marker: `class="admin-topbar"`, expectTopbar: true},
		{path: "/admin/hxc-dashboard", marker: `admin-workspace-stage--dynamic`, expectTopbar: true},
		{path: "/admin/questionnaires", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/radar-links", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/radarDetail.html?id=" + strconv.FormatInt(fixture.radarID, 10), marker: `data-page="radarDetail"`, expectTopbar: true},
		{path: "/admin/radarForm.html", marker: `data-page="radarForm"`, expectTopbar: true},
		{path: "/admin/radarForm.html?id=" + strconv.FormatInt(fixture.radarID, 10), marker: `data-page="radarForm"`, expectTopbar: true},
		{path: "/admin/wecom-tags", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/orders", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/wechat-pay/products", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/service-period-products", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/coupons", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/materials", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/image-library", canonicalPath: "/admin/materials?tab=images", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/miniprogram-library", canonicalPath: "/admin/materials?tab=miniprograms", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/attachment-library", canonicalPath: "/admin/materials?tab=attachments", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/automation-agents", marker: `admin-workspace-stage--embedded`, expectTopbar: false},
		{path: "/admin/owner-migration", marker: `admin-workspace-stage--embedded`, expectTopbar: true},
		{path: "/admin/config", marker: `data-runtime-release-host`, expectTopbar: true},
		{path: "/admin/config/releases", marker: `data-runtime-release-host`, expectTopbar: true},
		// Open Platform is an authenticated V3 Host injected into the built
		// apidocs document. The vanity route must canonicalize before that
		// document loads; do not mistake the deliberate 303 for a missing Host.
		{path: "/admin/api-docs", canonicalPath: "/admin/apidocs.html", marker: `openPlatformHost-`, expectTopbar: false},
		// Canonical detail/form aliases must keep the same owning Host and layout.
		{path: "/admin/productForm.html?id=" + strconv.FormatInt(fixture.productID, 10), marker: `data-page="productForm"`, expectTopbar: true},
		{path: "/admin/spProductForm.html?id=" + strconv.FormatInt(fixture.serviceProductID, 10), marker: `data-page="spProductForm"`, expectTopbar: true},
	} {
		response := authenticatedAdminGet(t, fixture.application.handler, session, route.path)
		if route.canonicalPath != "" {
			canonicalStatus := route.canonicalStatus
			if canonicalStatus == 0 {
				canonicalStatus = http.StatusSeeOther
			}
			if response.Code != canonicalStatus || response.Header().Get("Location") != route.canonicalPath {
				t.Fatalf("outer admin layout canonical route=%s status=%d location=%q expected_status=%d expected_location=%q", route.path, response.Code, response.Header().Get("Location"), canonicalStatus, route.canonicalPath)
			}
			response = authenticatedAdminGet(t, fixture.application.handler, session, route.canonicalPath)
		}
		body := response.Body.String()
		if response.Code != http.StatusOK || !strings.Contains(body, route.marker) || (strings.Count(body, `<header class="admin-topbar">`) == 1) != route.expectTopbar {
			t.Fatalf("outer admin layout route=%s canonical=%s status=%d marker=%t topbar_count=%d expected_topbar=%t", route.path, route.canonicalPath, response.Code, strings.Contains(body, route.marker), strings.Count(body, `<header class="admin-topbar">`), route.expectTopbar)
		}
		if strings.HasPrefix(route.path, "/admin/cloud-orchestrator/plans") || strings.HasPrefix(route.path, "/admin/ai") {
			if !strings.Contains(body, `admin-workspace-stage--dynamic`) {
				t.Fatalf("native AI Assistant route=%s must use the standard topbar content inset", route.path)
			}
		}
	}
	detail := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/cloud-orchestrator/plans/"+strconv.FormatInt(fixture.aiPlanID, 10))
	for _, marker := range []string{`data-plan-detail-state`, `data-plan-approve`, `data-plan-reject`, `href="/admin/cloud-orchestrator/plans"`} {
		if !strings.Contains(detail.Body.String(), marker) {
			t.Fatalf("native AI Assistant detail action marker=%q is absent", marker)
		}
	}
	for _, path := range []string{"/admin/cloud-orchestrator/plans/0", "/admin/cloud-orchestrator/plans/unknown", "/admin/aiDetail.html?id=0"} {
		response := authenticatedAdminGet(t, fixture.application.handler, session, path)
		if response.Code != http.StatusNotFound {
			t.Fatalf("native AI Assistant invalid detail route=%s status=%d", path, response.Code)
		}
	}
}

// TestPostgreSQLAdminShellLayoutChromiumJourney measures the actual composed
// admin pages after Access login. It is deliberately mandatory on Linux CI:
// DOM shape or HTTP 200 cannot prove the shell geometry or asset layout.
func TestPostgreSQLAdminShellLayoutChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newAdminShellLayoutFixture(t)
	// Hold the two reads long enough for the Host to expose its loading root.
	// The browser script must wait on rendered semantic controls, not that root,
	// before it measures the static sidebar/header geometry.
	fixture.server.Config.Handler = delayedOpenPlatformDirectoryReads(fixture.application.handler, 150*time.Millisecond)
	script := filepath.Join(filepath.Dir(fixture.script), "..", "..", "internal", "webshell", "admin_layout_geometry.mjs")
	command := exec.CommandContext(fixture.ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_ADMIN_LAYOUT_TEST_URL="+fixture.server.URL,
		"AICRM_ADMIN_LAYOUT_TEST_USERNAME=product-browser-owner",
		"AICRM_ADMIN_LAYOUT_TEST_PASSWORD=product-browser-owner-password",
		"AICRM_ADMIN_LAYOUT_TEST_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_SERVICE_PRODUCT_ID="+strconv.FormatInt(fixture.serviceProductID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_ARCHIVE_PRODUCT_ID="+strconv.FormatInt(fixture.archiveProductID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_HISTORICAL_ORDER="+fixture.historicalOrderReference,
		"AICRM_ADMIN_LAYOUT_TEST_NATIVE_ORDER="+fixture.nativeOrderReference,
		"AICRM_ADMIN_LAYOUT_TEST_RADAR_ID="+strconv.FormatInt(fixture.radarID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_AI_PLAN_ID="+strconv.FormatInt(fixture.aiPlanID, 10),
		"AICRM_ADMIN_LAYOUT_TEST_COUPON_ID="+strconv.FormatInt(fixture.couponID, 10),
		"AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR="+fixture.screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("admin shell Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "admin_shell_layout_chromium: PASS") {
		t.Fatalf("admin shell Chromium journey did not report success: %q", output)
	}
	for _, name := range []string{
		"automation.png", "cycles.png", "groupops.png", "channels.png", "ai.png", "ai-detail.png", "ai-detail-1280.png", "ai-detail-1440.png", "customers.png", "hxc.png", "questionnaires.png", "radar.png", "radar-detail.png", "radar-form.png", "tags.png", "tags-1280.png", "tags-1440.png",
		"orders.png", "products.png", "service-period-products.png", "product.png", "service-period-product.png", "coupons.png", "materials-images-1280.png", "materials-images-1440.png", "materials-miniprograms-1280.png", "materials-miniprograms-1440.png", "materials-miniprograms-page-2-1280.png", "materials-attachments-1280.png", "materials-attachments-1440.png",
		"products-actions-1440.png", "service-period-products-actions-1440.png", "products-actions-1280.png", "service-period-products-actions-1280.png", "products-actions-edge-1440.png", "products-actions-edge-1280.png", "products-delete-confirm.png",
		"automation-agents.png", "owner-migration.png", "config.png", "runtime-config.png", "api-docs.png", "order-detail-history.png", "order-detail-native.png", "order-detail-history-mobile.png", "external-effects.png",
		"coupons-desktop-1280.png", "coupons-desktop-1440.png", "coupon-form-desktop-1280.png", "coupon-form-desktop-1440.png", "coupon-data-desktop-1280.png", "coupon-data-desktop-1440.png", "service-period-products-desktop-1280.png", "service-period-products-desktop-1440.png", "member-grid-desktop-1280.png", "member-grid-desktop-1440.png", "channels-new-desktop-1280.png", "channels-new-desktop-1440.png", "external-effects-desktop-1280.png", "external-effects-desktop-1440.png", "owner-migration-desktop-1280.png", "owner-migration-desktop-1440.png", "runtime-config-desktop-1280.png", "runtime-config-desktop-1440.png", "api-docs-desktop-1280.png", "api-docs-desktop-1440.png",
	} {
		info, statErr := os.Stat(filepath.Join(fixture.screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("admin shell Chromium screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

func delayedOpenPlatformDirectoryReads(next http.Handler, duration time.Duration) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet && (request.URL.Path == "/api/admin/open-platform/clients" || request.URL.Path == "/api/admin/open-platform/routes") {
			time.Sleep(duration)
		}
		next.ServeHTTP(writer, request)
	})
}

func newAdminShellLayoutFixture(t *testing.T) *adminShellLayoutFixture {
	t.Helper()
	screenshots := t.TempDir()
	if configured := platformconfig.AdminLayoutScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatalf("AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatalf("create admin layout screenshot directory: %v", err)
		}
		screenshots = configured
	}
	// The layout journey deliberately aggregates independent failures from the
	// full navigation matrix. Its bounded three-minute context includes release
	// staging and browser startup; individual browser waits remain unchanged.
	fixture := &adminShellLayoutFixture{productExternalPushChromiumFixture: newProductExternalPushChromiumFixtureWithTimeout(t, 3*time.Minute), screenshots: screenshots}
	seedAdminShellLayoutHXC(t, fixture.ctx, fixture.application)
	fixture.radarID = seedAdminShellLayoutRadar(t, fixture.ctx, fixture.application)
	fixture.aiPlanID = seedAdminShellLayoutAIAssistantPlan(t, fixture.ctx, fixture.application)
	seedAdminShellLayoutMaterialImages(t, fixture.ctx, fixture.application)
	seedAdminShellLayoutAttachmentAndMiniProgram(t, fixture.ctx, fixture.application)
	fixture.nativeOrderReference = seedAdminShellLayoutNativeOrder(t, fixture.ctx, fixture.application, fixture.productID)
	fixture.archiveProductID = seedAdminShellLayoutArchiveProduct(t, fixture.ctx, fixture.application)
	fixture.couponID = seedAdminShellLayoutCoupon(t, fixture.ctx, fixture.application, fixture.productID)
	seedRemainingPagesGridMember(t, fixture.productExternalPushChromiumFixture)
	seedAdminShellLayoutOverflowProducts(t, fixture.ctx, fixture.application)
	return fixture
}

// seedAdminShellLayoutArchiveProduct creates a product used only to prove the
// visible list menu's existing delete confirmation. It is intentionally
// separate from productID because later geometry checks open that product form.
func seedAdminShellLayoutArchiveProduct(t *testing.T, ctx context.Context, application *composedApplication) int64 {
	t.Helper()
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	var id int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection)
VALUES('admin-layout-delete-menu','菜单删除夹具商品','仅用于后台列表可达性回归',9900,'CNY',10,1,$1::jsonb) RETURNING id`, projection).Scan(&id); err != nil {
		t.Fatalf("seed admin layout archive product: %v", err)
	}
	return id
}

// seedAdminShellLayoutCoupon supplies one current rule, target and canonical
// customer claim for the existing Coupon Owner's read-only admin pages.  It
// deliberately does not use the claim command: the browser only renders
// local fixture facts and never issues a coupon, payment or Provider request.
func seedAdminShellLayoutCoupon(t *testing.T, ctx context.Context, application *composedApplication, productID int64) int64 {
	t.Helper()
	now := time.Now().UTC()
	pool := application.pool.Native()
	var couponID, customerID int64
	if err := pool.QueryRow(ctx, `INSERT INTO coupon_rules(name,discount_amount_total,currency,status,total_issue_limit,per_user_issue_limit,issued_count,claim_starts_at,claim_ends_at,validity_mode,relative_validity_days,instructions,created_by,updated_by,created_at,updated_at)
VALUES('后台页面验收优惠券',1200,'CNY','published',100,1,1,$1,$2,'relative_days',30,'仅用于本地 Chromium 只读验收',1,1,$3,$3) RETURNING id`, now.Add(-time.Hour), now.Add(24*time.Hour), now).Scan(&couponID); err != nil {
		t.Fatalf("seed admin layout coupon rule: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO coupon_rule_targets(coupon_id,target_ref,position) VALUES($1,$2,0)`, couponID, fmt.Sprintf("standard_product:%d", productID)); err != nil {
		t.Fatalf("seed admin layout coupon target: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatalf("seed admin layout coupon customer: %v", err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,activation_status,source,source_version,last_synced_at,updated_at)
VALUES($1,'active','优惠券领取验收客户','active','admin-layout-coupon-fixture',1,$2,$2)`, customerID, now); err != nil {
		t.Fatalf("seed admin layout coupon customer projection: %v", err)
	}
	digest := sha256.Sum256([]byte("admin-layout-coupon-claim"))
	if _, err := pool.Exec(ctx, `INSERT INTO coupon_customer_claims(source_system,source_key,customer_id,coupon_id,status,claim_no_masked,claimed_at,valid_from,valid_until,source_digest,created_at,updated_at)
VALUES('admin-layout-coupon','claim-001',$1,$2,'claimed','CLM-***001',$3,$3,$4,$5,$3,$3)`, customerID, couponID, now, now.AddDate(0, 0, 30), digest[:]); err != nil {
		t.Fatalf("seed admin layout coupon claim: %v", err)
	}
	return couponID
}

// seedAdminShellLayoutOverflowProducts supplies a real long product table so
// the Chromium journey can place the shared menu at the visible viewport edge.
func seedAdminShellLayoutOverflowProducts(t *testing.T, ctx context.Context, application *composedApplication) {
	t.Helper()
	projection := `{"schema_version":1,"status":"enabled","enabled":true,"buy_button_text":"立即购买","require_mobile":false,"lead_program_id":null,"lead_channel_id":null,"lead_qr_title":"","lead_qr_subtitle":"","completion_redirect_enabled":false,"completion_redirect_url":"","completion_target":null,"wecom_tagging":{},"slices":[]}`
	for index := 1; index <= 12; index++ {
		if _, err := application.pool.Native().Exec(ctx, `INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection)
VALUES($1,$2,'仅用于后台列表边缘菜单回归',9900,'CNY',10,1,$3::jsonb)`, fmt.Sprintf("admin-layout-menu-row-%02d", index), fmt.Sprintf("菜单边缘夹具商品 %02d", index), projection); err != nil {
			t.Fatalf("seed admin layout overflow product %d: %v", index, err)
		}
	}
}

// seedAdminShellLayoutMaterialImages creates three Media-owned, local-only image
// records for the composed material-directory journey. They deliberately cover
// landscape, portrait, and small square sources while keeping Provider effects
// disabled; the browser only reads existing variants.
func seedAdminShellLayoutMaterialImages(t *testing.T, ctx context.Context, application *composedApplication) {
	t.Helper()
	type imageFixture struct {
		fileName, name string
		width, height  int
		fill           color.RGBA
	}
	for _, fixture := range []imageFixture{
		{fileName: "material-landscape.png", name: "素材工作台横向缩略图", width: 160, height: 90, fill: color.RGBA{R: 36, G: 91, B: 219, A: 255}},
		{fileName: "material-portrait.png", name: "素材工作台纵向缩略图", width: 90, height: 160, fill: color.RGBA{R: 25, G: 142, B: 118, A: 255}},
		{fileName: "material-small.png", name: "素材工作台小尺寸缩略图", width: 24, height: 24, fill: color.RGBA{R: 132, G: 94, B: 194, A: 255}},
	} {
		canvas := image.NewRGBA(image.Rect(0, 0, fixture.width, fixture.height))
		draw.Draw(canvas, canvas.Bounds(), image.NewUniform(fixture.fill), image.Point{}, draw.Src)
		var encoded bytes.Buffer
		if err := png.Encode(&encoded, canvas); err != nil {
			t.Fatal(err)
		}
		content := encoded.Bytes()
		digestValue := sha256.Sum256(content)
		digest := fmt.Sprintf("sha256:%x", digestValue)
		if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3) ON CONFLICT(digest) DO NOTHING`, digest, len(content), content); err != nil {
			t.Fatal(err)
		}
		if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,$2,$3,'素材工作台 Chromium 目录缩略图','目录验收,素材库','目录验收','image/png',$4,$5,$6,true,1,1)`, digest, fixture.fileName, fixture.name, len(content), fixture.width, fixture.height); err != nil {
			t.Fatal(err)
		}
	}
}

// seedAdminShellLayoutAttachmentAndMiniProgram keeps the existing attachment
// and miniprogram owners observable in the same browser journey. It writes
// only local PostgreSQL fixture records, with no refresh, send, or Provider
// invocation.
func seedAdminShellLayoutAttachmentAndMiniProgram(t *testing.T, ctx context.Context, application *composedApplication) {
	t.Helper()
	pdf := []byte("%PDF-1.4\\n% admin material workspace fixture\\n")
	digestValue := sha256.Sum256(pdf)
	digest := fmt.Sprintf("sha256:%x", digestValue)
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'application/pdf',$2,$3) ON CONFLICT(digest) DO NOTHING`, digest, len(pdf), pdf); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_attachments(blob_digest,file_name,name,description,tags,mime_type,byte_size,enabled,created_by,updated_by) VALUES($1,'material-workspace.pdf','素材工作台附件','Chromium 附件目录夹具','["目录验收"]'::jsonb,'application/pdf',$2,true,1,1)`, digest, len(pdf)); err != nil {
		t.Fatal(err)
	}
	// Keep a real second owner page in the browser fixture. These are local
	// PostgreSQL facts only: the journey exercises pagination/read rendering
	// and never starts a Provider or mutation flow.
	for index := 1; index <= 50; index++ {
		name := fmt.Sprintf("wx_material_page_%02d", index)
		if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_miniprograms(name,app_id,page_path,title,thumb_image_id,enabled,created_by,updated_by) VALUES($1,$2,$3,$4,NULL,true,1,1)`, name, fmt.Sprintf("wx-material-page-%02d", index), fmt.Sprintf("pages/materials/%02d", index), fmt.Sprintf("素材工作台分页 %02d", index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO media_miniprograms(name,app_id,page_path,title,thumb_image_id,enabled,created_by,updated_by) VALUES('wx_material_layout','wx-material-layout','pages/materials/index','素材工作台小程序',NULL,true,1,1)`); err != nil {
		t.Fatal(err)
	}
}

func authenticatedAdminGet(t *testing.T, handler interface {
	ServeHTTP(http.ResponseWriter, *http.Request)
}, session, path string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest("GET", path, nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	handler.ServeHTTP(response, request)
	return response
}

func seedAdminShellLayoutHXC(t *testing.T, ctx context.Context, application *composedApplication) {
	t.Helper()
	store := hxcstore.NewPostgreSQL(application.pool.Native())
	uow, err := platformpostgres.NewUnitOfWork(application.pool)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("admin-shell-layout-hxc-refresh"))
	var runID int64
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO hxc_dashboard_refresh_runs(
		run_key,request_digest,trigger,identity_mode,status,source_count,processed_count,identity_replay_verified_count
	) VALUES('admin-shell-layout-hxc',$1,'initial','inspect','publishing',30,30,0) RETURNING id`, digest[:]).Scan(&runID); err != nil {
		t.Fatal(err)
	}
	asOf := time.Date(2026, time.September, 7, 0, 0, 0, 0, time.UTC)
	rows := make([]hxcdomain.ProjectionRow, 0, 30)
	for index := 0; index < 30; index++ {
		rows = append(rows, hxcdomain.ProjectionRow{
			SubjectDigest: [32]byte{byte(index + 1)},
			UserRef:       fmt.Sprintf("HXC-%012x", index+1),
			Stage:         hxcdomain.RegisteredNoActiveMembership,
			SourceRow: hxcdomain.SourceRow{
				MembershipAttribution: "none", CapabilityUsage: []byte(`{}`), FocusTopics: []byte(`[]`), SourceUpdatedAt: asOf,
			},
			IdentityState: hxcdomain.Unmatched, MatchedBy: "none", IdentityReasonCode: "no_match",
		})
	}
	projection := hxcdomain.Projection{
		AsOf:   asOf,
		Counts: hxcdomain.Counts{Total: 30, RegisteredNoActiveMembership: 30, Unmatched: 30, PendingObservation: 30},
		Rows:   rows,
	}
	if err = uow.Within(ctx, func(tx context.Context) error {
		_, publishErr := store.Publish(tx, runID, projection)
		return publishErr
	}); err != nil {
		t.Fatal(err)
	}
}

// seedAdminShellLayoutNativeOrder creates a fully anonymous, native Payment
// read fixture. It deliberately creates no Payment outbox, effect, or Provider
// request: the Chromium journey verifies only the composed renderer's Chinese
// facts and guarded refund form against durable order/payment records.
func seedAdminShellLayoutNativeOrder(t *testing.T, ctx context.Context, application *composedApplication, productID int64) string {
	t.Helper()
	const merchantOrderNo = "WXP260912030405A1B2C3D4E5F6"
	const transactionID = "fixture-wechat-transaction-001"
	now := time.Date(2026, time.September, 12, 3, 4, 5, 0, time.UTC)
	var customerID, orderID int64
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `
		INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,phone_masked,phone_assurance,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active','匿名订单买家','CID-ORDER-FIXTURE','130****1234','verified','active','order-detail-chromium-fixture',1,$2,$2)`, customerID, now); err != nil {
		t.Fatal(err)
	}
	if err := application.pool.Native().QueryRow(ctx, `
		INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,record_origin,effect_eligible,version,created_at,updated_at)
		VALUES('wechat_pay','order-detail-chromium-fixture','order-detail-native-001',$1,$2,$3,$3,2000,'CNY','paid','native',TRUE,1,$4,$4)
		RETURNING id`, merchantOrderNo, transactionID, customerID, now).Scan(&orderID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `
		INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
		VALUES($1,1,$2,'order-detail-fixture','匿名退款演示商品',2000,1,2000)`, orderID, productID); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte(transactionID))
	if _, err := application.pool.Native().Exec(ctx, `
		INSERT INTO payments(order_id,provider,payment_channel,merchant_order_no,payer_identity_id,payer_customer_id,beneficiary_customer_id,amount_minor,currency,status,provider_transaction_digest,version,created_at,updated_at)
		VALUES($1,'wechat_pay','mini_program',$2,1,$3,$3,2000,'CNY','paid',$4,1,$5,$5)`, orderID, merchantOrderNo, customerID, fmt.Sprintf("sha256:%x", digest), now); err != nil {
		t.Fatal(err)
	}
	return merchantOrderNo
}

// seedAdminShellLayoutRadar provides an existing read-only radar record so the
// composed detail alias, as well as the empty new-form alias, are measured by
// the same Chromium geometry contract. It never submits a browser mutation.
func seedAdminShellLayoutRadar(t *testing.T, ctx context.Context, application *composedApplication) int64 {
	t.Helper()
	now := time.Date(2026, time.September, 7, 1, 2, 3, 0, time.UTC)
	var radarID, customerID, identityID int64
	err := application.pool.Native().QueryRow(ctx, `
		INSERT INTO radar_links(
			public_code,name,title,description,content_type,destination_url,
			auth_policy,status,created_by,updated_by,created_at,updated_at
		) VALUES(
			'rd_adminlayoutradar','Admin layout radar','Admin layout radar','layout fixture',
			'link','https://example.com/admin-layout-radar','unionid_required','enabled',1,1,$1,$1
		) RETURNING id`, now).Scan(&radarID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `
		INSERT INTO radar_link_versions(radar_id,version,snapshot,actor_id,created_at)
		VALUES($1,1,'{}'::jsonb,1,$2)`, radarID, now); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `INSERT INTO customer_identities(customer_id,kind,scope_key,normalized_value,assurance,source,normalizer_version,verified_at)
		VALUES($1,'wecom_external_userid','wecom-corp:admin-layout-fixture-corp','external-radar-layout-001','verified','admin_layout_fixture',1,$2)
		RETURNING id`, customerID, now).Scan(&identityID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active','雷达布局访客',$2,'active','admin_layout_fixture',1,$3,$3)`, customerID, fmt.Sprintf("CID-%d", customerID), now); err != nil {
		t.Fatal(err)
	}
	var sessionID int64
	if err = application.pool.Native().QueryRow(ctx, `
		INSERT INTO radar_view_sessions(
			session_digest,radar_id,radar_version,identity_id,customer_id,attribution_status,evidence_digest,expires_at,created_at
		) VALUES(
			decode(repeat('1a',32),'hex'),$1,1,$2,$3,'resolved',decode(repeat('4d',32),'hex'),$4::timestamptz + interval '1 hour',$4
		) RETURNING id`, radarID, identityID, customerID, now).Scan(&sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err = application.pool.Native().Exec(ctx, `
		INSERT INTO radar_events(
			receipt_id,radar_id,radar_version,session_id,stage,attribution_status,
			identity_id,customer_id,key_digest,payload_digest,occurred_at,created_at
		) VALUES
			('rre_00000000000000000000000000000001',$1,1,$2,'content_opened','resolved',$3,$4,decode(repeat('2b',32),'hex'),decode(repeat('3c',32),'hex'),$5,$5),
			('rre_00000000000000000000000000000002',$1,1,$2,'image_loaded','resolved',$3,$4,decode(repeat('5e',32),'hex'),decode(repeat('6f',32),'hex'),$5::timestamptz + interval '30 seconds',$5::timestamptz + interval '30 seconds')`, radarID, sessionID, identityID, customerID, now); err != nil {
		t.Fatal(err)
	}
	return radarID
}

// seedAdminShellLayoutAIAssistantPlan uses the composed authenticated admin
// API to persist a pending-review plan and recipient. The fixture deliberately
// stops before approval, dispatch, or any Provider invocation; it proves only
// the native UI's read path against its normal AI Service and Unit of Work.
func seedAdminShellLayoutAIAssistantPlan(t *testing.T, ctx context.Context, application *composedApplication) int64 {
	t.Helper()
	now := time.Date(2026, time.September, 7, 1, 3, 4, 0, time.UTC)
	var actorID, customerID int64
	if err := application.pool.Native().QueryRow(ctx, `SELECT id FROM admin_users WHERE username='product-browser-owner'`).Scan(&actorID); err != nil {
		t.Fatal(err)
	}
	if err := application.pool.Native().QueryRow(ctx, `INSERT INTO customers(status) VALUES('active') RETURNING id`).Scan(&customerID); err != nil {
		t.Fatal(err)
	}
	if _, err := application.pool.Native().Exec(ctx, `INSERT INTO customer_directory_projection(customer_id,customer_status,display_name,oneid_label,activation_status,source,source_version,last_synced_at,updated_at)
		VALUES($1,'active','AI layout customer','CID-AI-LAYOUT','active','admin-layout-fixture',1,$2,$2)`, customerID, now); err != nil {
		t.Fatal(err)
	}
	input := struct {
		Name         string            `json:"name"`
		SourceKind   string            `json:"source_kind"`
		SourceDigest effectport.Digest `json:"source_digest"`
		Recipients   []struct {
			CustomerID int64 `json:"customer_id"`
			StaffID    int64 `json:"staff_id"`
			Content    []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"recipients"`
	}{
		Name: "AI layout detail fixture", SourceKind: "admin_shell_layout.fixture.v1", SourceDigest: effectport.Hash("admin-shell-layout-ai-plan"),
		Recipients: []struct {
			CustomerID int64 `json:"customer_id"`
			StaffID    int64 `json:"staff_id"`
			Content    []struct {
				Kind string `json:"kind"`
				Text string `json:"text"`
			} `json:"content"`
		}{{CustomerID: customerID, StaffID: actorID, Content: []struct {
			Kind string `json:"kind"`
			Text string `json:"text"`
		}{{Kind: "text", Text: "AI layout detail fixture"}}}},
	}
	payload, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, "product-browser-owner", "product-browser-owner-password")
	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-assistant/plans", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", "admin-shell-layout-ai-plan-0001")
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, request)
	var created struct {
		OK   bool `json:"ok"`
		Plan struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		} `json:"plan"`
	}
	if response.Code != http.StatusCreated || json.Unmarshal(response.Body.Bytes(), &created) != nil || !created.OK || created.Plan.ID < 1 || created.Plan.State != "pending_review" {
		t.Fatalf("admin layout AI fixture create status=%d response_valid=%t plan_id=%d state=%q", response.Code, json.Unmarshal(response.Body.Bytes(), &created) == nil, created.Plan.ID, created.Plan.State)
	}
	return created.Plan.ID
}
