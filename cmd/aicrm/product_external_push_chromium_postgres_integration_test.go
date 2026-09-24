package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/outbound"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLProductExternalPushChromiumJourney uses the real Access
// login/CSRF flow and the frozen ordinary- and service-period-product forms
// with their V3 Host. It proves that the legacy key/value panel preserves
// raw JSON values through browser -> HTTP -> Product PostgreSQL -> immutable
// synthetic Provider payload without a JavaScript number round-trip. It then uses the normal
// EER/River worker against the reserved .invalid fixture target and manually
// refreshes the Host until the durable result is outcome_unknown. No legacy
// receiver can be reached from that target; the separate CommerceFunds journey
// covers a signed receiver response and completion artifact.
//
// OneID decision: not involved in this synthetic Product-admin action.
// Persistence decision: Product receipt/configuration, Outbound intent, EER
// acceptance and River enqueue use their normal shared PostgreSQL UoW. The
// fixture's terminal outcome is read back through Product, never retried.
type productExternalPushChromiumFixture struct {
	ctx                      context.Context
	application              *composedApplication
	server                   *httptest.Server
	script                   string
	productID                int64
	serviceProductID         int64
	materialFirstID          int64
	materialLaterID          int64
	historicalOrderReference string
	dataKey                  []byte
	alipayGateway            string
}

type productExternalPushChromiumFixtureOptions struct {
	// enablePublicH5 configures only the public-commerce journey's exact local
	// H5 identity/session boundary. Existing product-admin fixtures keep their
	// original disabled Payment/OAuth configuration.
	enablePublicH5 bool
	enableAlipay   bool
	// h5PublicOrigin separates the browser checkout origin from PublicOrigin
	// for end-to-end Origin-guard tests.
	h5PublicOrigin string
	// alipayGateway is a loopback-only synthetic server used by the full
	// Alipay journey, including post-handoff reconciliation reads.
	alipayGateway string
}

// TestPostgreSQLProductExternalPushCompositionPreflight runs in every real
// PostgreSQL check. It builds the release artifact and proves the authenticated
// outer ordinary-product, service-period and historical-order routes before the
// dedicated Linux Chromium gate opens either frozen donor page.
func TestPostgreSQLProductExternalPushCompositionPreflight(t *testing.T) {
	_ = newProductExternalPushChromiumFixture(t)
}

func TestPostgreSQLProductExternalPushChromiumJourney(t *testing.T) {
	// The independently named preflight above always covers the release artifact
	// and real Composition Root. Chromium is an explicit Linux CI gate, rather
	// than a developer-machine substitute for that contract.
	if goruntime.GOOS == "darwin" && !platformconfig.ProductExternalPushDarwinChromiumDiagnosticAllowed() {
		t.Skip("Chromium CDP journey requires Linux CI; the PostgreSQL Composition preflight runs separately")
	}
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}

	fixture := newProductExternalPushChromiumFixture(t)
	outerSession, outerCSRF := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	// Seed the old Product configuration through its owner HTTP contract. The
	// high-precision nested JSON is deliberately present before the browser
	// edits an unrelated key/value row: this catches UI round trips through a
	// JavaScript Number without reintroducing the retired JSON editor.
	for _, path := range []string{
		"/api/admin/wechat-pay/products/" + strconv.FormatInt(fixture.productID, 10) + "/external-push",
		"/api/admin/service-period-products/" + strconv.FormatInt(fixture.serviceProductID, 10) + "/external-push",
	} {
		response := productExternalPushAdminMutation(t, fixture.application.handler, http.MethodPut, path, `{"enabled":true,"webhook_url":"https://commerce-browser.invalid","configuration_reference":"browser-push-target","push_type":"paid_notify","day":null,"frequency":null,"expires_at_ts":null,"remark":"legacy fixture","custom_params":{"count":9007199254740993,"nested":[{"inner":9007199254740993}],"flag":false},"expected_revision":0}`, outerSession, outerCSRF, "browser-seed-legacy-config-"+strconv.FormatInt(int64(len(path)), 10))
		if response.Code != http.StatusOK {
			t.Fatalf("seed legacy push status=%d body=%s", response.Code, response.Body.String())
		}
	}
	command := exec.CommandContext(fixture.ctx, "node", fixture.script)
	command.Env = append(os.Environ(),
		"AICRM_PRODUCT_PUSH_TEST_URL="+fixture.server.URL,
		"AICRM_PRODUCT_PUSH_TEST_USERNAME=product-browser-owner",
		"AICRM_PRODUCT_PUSH_TEST_PASSWORD=product-browser-owner-password",
		"AICRM_PRODUCT_PUSH_TEST_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10),
		"AICRM_PRODUCT_PUSH_TEST_SERVICE_PRODUCT_ID="+strconv.FormatInt(fixture.serviceProductID, 10),
		"AICRM_PRODUCT_PUSH_TEST_MATERIAL_FIRST_ID="+strconv.FormatInt(fixture.materialFirstID, 10),
		"AICRM_PRODUCT_PUSH_TEST_MATERIAL_LATER_ID="+strconv.FormatInt(fixture.materialLaterID, 10),
		"AICRM_PRODUCT_PUSH_TEST_HISTORICAL_ORDER="+fixture.historicalOrderReference,
	)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "product_external_push_chromium: SKIP_DEVTOOLS") {
		t.Fatalf("product external push Chromium DevTools unexpectedly unavailable on required platform: %s", strings.TrimSpace(string(output)))
	}
	if err != nil {
		t.Fatalf("product external push Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "product_external_push_chromium: PASS") {
		t.Fatalf("product external push Chromium journey did not report success: %q", output)
	}

	assertProductExternalPushSyntheticDurableFacts(t, fixture.ctx, fixture.application, fixture.productID, fixture.dataKey, 3)
	var serviceRevision, serviceStoredExpiry int64
	var serviceStored json.RawMessage
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, "SELECT version,expires_at_ts,custom_params FROM product_external_push_configurations WHERE product_id=$1 AND product_kind='service_period'", fixture.serviceProductID).Scan(&serviceRevision, &serviceStoredExpiry, &serviceStored); err != nil {
		t.Fatal(err)
	}
	if serviceRevision != 2 || serviceStoredExpiry != 2147483647 || !externalPushStoredJSONHasExactBigInteger(serviceStored) {
		t.Fatalf("service-period browser configuration revision=%d params=%s", serviceRevision, serviceStored)
	}
}

func newProductExternalPushChromiumFixture(t *testing.T) *productExternalPushChromiumFixture {
	return newProductExternalPushChromiumFixtureWithTimeout(t, 90*time.Second)
}

// newProductExternalPushChromiumFixtureWithTimeout keeps single-Host browser
// journeys bounded while allowing a composed caller to budget for its own
// larger route matrix. It does not change any per-page browser waits.
func newProductExternalPushChromiumFixtureWithTimeout(t *testing.T, timeout time.Duration) *productExternalPushChromiumFixture {
	return newProductExternalPushChromiumFixtureWithOptions(t, timeout, productExternalPushChromiumFixtureOptions{})
}

func newPublicCommerceChromiumFixture(t *testing.T, timeout time.Duration) *productExternalPushChromiumFixture {
	return newProductExternalPushChromiumFixtureWithOptions(t, timeout, productExternalPushChromiumFixtureOptions{enablePublicH5: true})
}

func newProductExternalPushChromiumFixtureWithOptions(t *testing.T, timeout time.Duration, options productExternalPushChromiumFixtureOptions) *productExternalPushChromiumFixture {
	t.Helper()
	if timeout < time.Second {
		t.Fatal("Chromium fixture timeout must be positive")
	}
	// Go executes this package with cmd/aicrm as its working directory, while
	// composition deliberately resolves the release artifact at web/dist. Use
	// the repository root just as the release binary does, so this fixture
	// exercises the full outer route with its built Product Host rather than a
	// package-local missing-artifact 503.
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Chromium product journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	targetsJSON, err := json.Marshal(map[string]any{
		"browser-push-target": map[string]any{
			"slot": "browser-product-slot", "endpoint": "https://commerce-browser.invalid",
			"signing_key": base64.RawStdEncoding.EncodeToString([]byte("browser-fixture-signing-key")),
			"version":     "legacy-v1", "tenant_id": "aicrm-browser",
			"buyer_id":          map[string]any{"kind": "wecom_external_userid", "scope": "wecom-corp:browser"},
			"buyer_openid":      map[string]any{"kind": "mp_openid", "scope": "wechat-app:browser"},
			"buyer_unionid":     map[string]any{"kind": "unionid", "scope": "wechat-open-platform:browser"},
			"buyer_phone":       map[string]any{"kind": "phone", "scope": "phone:cn11"},
			"beneficiary_phone": map[string]any{"kind": "phone", "scope": "phone:cn11"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	runtime := platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, H5PublicOrigin: options.h5PublicOrigin,
		ReleaseSHA: "product-external-push-chromium-journey", WorkerOwner: "product-external-push-chromium-journey", WorkerLimit: 1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "product-external-push-chromium-webhook-secret"},
		WeCom:        platformconfig.WeCom{CorpID: "admin-layout-fixture-corp"},
		Survey:       platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Effects:      platformconfig.Effects{ProviderEnabled: true},
		AIAssistant:  platformconfig.AIAssistant{UIEnabled: true},
		CommercePush: platformconfig.CommercePush{ProviderEnabled: true, TargetsJSON: string(targetsJSON), PayloadDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Bootstrap:    platformconfig.Bootstrap{Enabled: true, Username: "product-browser-owner", Password: "product-browser-owner-password", DisplayName: "Product Browser Owner"},
	}
	if options.enablePublicH5 {
		// The public journey issues a synthetic, provider-verified test session
		// through the composed OneID/Payment service. These temporary local
		// credentials make that existing read boundary available; browser steps
		// stop before checkout, OAuth, or any Provider call.
		paymentKey, paymentCertificate := distributionFixturePaymentCredentials(t)
		runtime.Survey = platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey), OAuthEnabled: true, OAuthAppID: "wx-public-commerce-h5", OAuthSecret: "public-commerce-h5-fixture-secret", OAuthOpenPlatformID: "public-commerce-fixture-platform", OAuthScope: "snsapi_userinfo"}
		runtime.WeChatPay = platformconfig.WeChatPay{Enabled: true, AppID: "wx-public-commerce-mini", AppSecret: "public-commerce-mini-fixture-secret", AppScope: "wechat-app:wx-public-commerce-mini", H5OAuthEnabled: true, H5AppID: "wx-public-commerce-h5", H5AppSecret: "public-commerce-h5-fixture-secret", H5AppScope: "wechat-app:wx-public-commerce-h5", OrderContactDataKey: base64.RawStdEncoding.EncodeToString(dataKey), MerchantID: "public-commerce-fixture-mch", MerchantSerial: "public-commerce-fixture-serial", PrivateKeyPath: paymentKey, PlatformCertPath: paymentCertificate, APIV3Key: "0123456789abcdef0123456789abcdef"}
	}
	alipayGateway := options.alipayGateway
	if options.enableAlipay {
		privateKeyPath, publicKey := virtualAlipayFixtureCredentials(t)
		if alipayGateway == "" {
			alipayGateway = newVirtualAlipayGateway(t, privateKeyPath).URL
		}
		runtime.Alipay = platformconfig.Alipay{
			Enabled: true, AppID: "virtual-alipay-test-app", PrivateKeyPath: privateKeyPath,
			AlipayPublicKey: publicKey, Gateway: alipayGateway,
			NotifyURL: "https://crm.example.test/api/public/alipay/callback", ReturnURL: "https://crm.example.test/pay/result",
		}
	}
	application, err := compose(ctx, runtime)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "product-browser-owner", Password: "product-browser-owner-password", DisplayName: "Product Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	productID, serviceProductID, historicalOrderReference, err := seedProductExternalPushChromiumJourney(ctx, application)
	if err != nil {
		t.Fatal(err)
	}
	if options.enablePublicH5 {
		seedPublicCommerceDetailImage(t, ctx, application, productID)
	}
	materialFirstID, materialLaterID, err := seedProductMaterialChromiumImages(ctx, application)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- application.effectsRuntime.Run(workerCtx) }()
	t.Cleanup(func() {
		stopWorker()
		select {
		case runErr := <-workerDone:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Errorf("effects runtime: %v", runErr)
			}
		case <-time.After(20 * time.Second):
			t.Error("effects runtime did not stop")
		}
	})
	server.Config.Handler = application.handler
	server.StartTLS()

	// This direct outer-handler preflight catches route shadows before Chrome
	// opens the frozen pages. It runs in ordinary PostgreSQL CI as well, so a
	// missing release artifact or Host binding cannot be reclassified as a
	// browser-only timing failure.
	outerSession, _ := adminAccessLogin(t, application.handler, "product-browser-owner", "product-browser-owner-password")
	outerProduct := httptest.NewRecorder()
	outerProductRequest := httptest.NewRequest(http.MethodGet, "/admin/productForm.html?id="+strconv.FormatInt(productID, 10), nil)
	outerProductRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	application.handler.ServeHTTP(outerProduct, outerProductRequest)
	if outerProduct.Code != http.StatusOK || !bytes.Contains(outerProduct.Body.Bytes(), []byte(`/product-assets/`)) || !bytes.Contains(outerProduct.Body.Bytes(), []byte(`data-page="productForm"`)) || !bytes.Contains(outerProduct.Body.Bytes(), []byte(`<header class="admin-topbar">`)) || !bytes.Contains(outerProduct.Body.Bytes(), []byte(`<h1 class="admin-page-title">编辑普通商品</h1>`)) || !bytes.Contains(outerProduct.Body.Bytes(), []byte(`/static/admin_console/admin_console.css`)) {
		t.Fatalf("outer composed product Host status=%d product_assets=%t product_form=%t topbar=%t title=%t console_css=%t", outerProduct.Code, bytes.Contains(outerProduct.Body.Bytes(), []byte(`/product-assets/`)), bytes.Contains(outerProduct.Body.Bytes(), []byte(`data-page="productForm"`)), bytes.Contains(outerProduct.Body.Bytes(), []byte(`<header class="admin-topbar">`)), bytes.Contains(outerProduct.Body.Bytes(), []byte(`<h1 class="admin-page-title">编辑普通商品</h1>`)), bytes.Contains(outerProduct.Body.Bytes(), []byte(`/static/admin_console/admin_console.css`)))
	}
	outerServiceProduct := httptest.NewRecorder()
	outerServiceProductRequest := httptest.NewRequest(http.MethodGet, "/admin/spProductForm.html?id="+strconv.FormatInt(serviceProductID, 10), nil)
	outerServiceProductRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	application.handler.ServeHTTP(outerServiceProduct, outerServiceProductRequest)
	if outerServiceProduct.Code != http.StatusOK || !bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`/product-assets/`)) || !bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`data-page="spProductForm"`)) || !bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`id="sp-push"`)) || !bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`<header class="admin-topbar">`)) || !bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`<h1 class="admin-page-title">编辑周期商品</h1>`)) || !bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`/static/admin_console/admin_console.css`)) {
		t.Fatalf("outer composed service-period product Host status=%d product_assets=%t service_form=%t service_anchor=%t topbar=%t title=%t console_css=%t", outerServiceProduct.Code, bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`/product-assets/`)), bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`data-page="spProductForm"`)), bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`id="sp-push"`)), bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`<header class="admin-topbar">`)), bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`<h1 class="admin-page-title">编辑周期商品</h1>`)), bytes.Contains(outerServiceProduct.Body.Bytes(), []byte(`/static/admin_console/admin_console.css`)))
	}
	outerMaterials := httptest.NewRecorder()
	outerMaterialsRequest := httptest.NewRequest(http.MethodGet, "/api/admin/image-library?limit=50&offset=50&enabled_only=true", nil)
	outerMaterialsRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	application.handler.ServeHTTP(outerMaterials, outerMaterialsRequest)
	if outerMaterials.Code != http.StatusOK || !bytes.Contains(outerMaterials.Body.Bytes(), []byte(`Chromium 商品后续页素材`)) || !bytes.Contains(outerMaterials.Body.Bytes(), []byte(`"has_more":false`)) {
		t.Fatalf("outer composed product material later page status=%d later=%t terminal_page=%t", outerMaterials.Code, bytes.Contains(outerMaterials.Body.Bytes(), []byte(`Chromium 商品后续页素材`)), bytes.Contains(outerMaterials.Body.Bytes(), []byte(`"has_more":false`)))
	}
	for _, read := range []struct {
		path   string
		marker string
	}{
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10), marker: `"product"`},
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10) + "/members?limit=50", marker: `"items"`},
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10) + "/member-grid/access", marker: `"can_view"`},
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10) + "/member-grid/schema", marker: `"schema"`},
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10) + "/member-views", marker: `"items"`},
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10) + "/member-grid/share-settings", marker: `"external_share_supported"`},
		{path: "/api/admin/service-period-products/" + strconv.FormatInt(serviceProductID, 10) + "/external-push", marker: `"custom_params_json"`},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, read.path, nil)
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
		application.handler.ServeHTTP(response, request)
		body := response.Body.Bytes()
		if response.Code != http.StatusOK || !json.Valid(body) || !bytes.Contains(body, []byte(read.marker)) {
			failure := "other"
			for _, candidate := range []string{"permission_denied", "unauthorized", "unavailable", "not_found", "invalid_request"} {
				if bytes.Contains(body, []byte(candidate)) {
					failure = candidate
					break
				}
			}
			t.Fatalf("outer composed service-period loadDb resource path=%s status=%d json=%t contract=%t failure=%s", read.path, response.Code, json.Valid(body), bytes.Contains(body, []byte(read.marker)), failure)
		}
	}
	outerDelivery := httptest.NewRecorder()
	outerRequest := httptest.NewRequest(http.MethodGet, "/api/admin/wechat-pay/orders/"+historicalOrderReference+"/external-push-deliveries", nil)
	outerRequest.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	application.handler.ServeHTTP(outerDelivery, outerRequest)
	if outerDelivery.Code != http.StatusOK || !bytes.Contains(outerDelivery.Body.Bytes(), []byte(`"source":"history"`)) || !bytes.Contains(outerDelivery.Body.Bytes(), []byte(`"legacy_delivery_id":"browser-history-delivery-1"`)) {
		// Keep the failure non-sensitive: this only reports route and fixture
		// contract booleans, not the protected historical result payload.
		t.Fatalf("outer composed historical delivery route status=%d history=%t legacy_id=%t", outerDelivery.Code, bytes.Contains(outerDelivery.Body.Bytes(), []byte(`"source":"history"`)), bytes.Contains(outerDelivery.Body.Bytes(), []byte(`"legacy_delivery_id":"browser-history-delivery-1"`)))
	}

	return &productExternalPushChromiumFixture{
		ctx: ctx, application: application, server: server,
		script:    filepath.Join(filepath.Dir(source), "product_external_push_chromium_journey.mjs"),
		productID: productID, serviceProductID: serviceProductID,
		materialFirstID: materialFirstID, materialLaterID: materialLaterID,
		historicalOrderReference: historicalOrderReference, dataKey: dataKey,
		alipayGateway: alipayGateway,
	}
}

// seedPublicCommerceDetailImage creates one synthetic, Product-owned detail
// image for the authorized public browser journey. It does not upload or call
// a Provider; Media serves the saved bytes through Product's existing narrow
// anonymous image route.
func seedPublicCommerceDetailImage(t *testing.T, ctx context.Context, application *composedApplication, productID int64) {
	t.Helper()
	// Use a tall Product-owned image so the public browser journey verifies the
	// real fixed purchase bar at the end of a scrollable detail page instead of
	// only a one-viewport media card.
	canvas := image.NewRGBA(image.Rect(0, 0, 640, 2200))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(color.RGBA{51, 112, 255, 255}), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 734, 640, 1467), image.NewUniform(color.RGBA{15, 118, 110, 255}), image.Point{}, draw.Src)
	draw.Draw(canvas, image.Rect(0, 1467, 640, 2200), image.NewUniform(color.RGBA{70, 83, 185, 255}), image.Point{}, draw.Src)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, canvas); err != nil {
		t.Fatal(err)
	}
	content := encoded.Bytes()
	digestValue := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(digestValue[:])
	pool := application.pool.Native()
	if _, err := pool.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3)`, digest, len(content), content); err != nil {
		t.Fatal(err)
	}
	var imageID int64
	if err := pool.QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,'public-commerce-detail-long.png','公开商品验收长图','公开商品浏览器验收用合成长图','chromium,public-commerce','public-commerce','image/png',$2,640,2200,true,1,1) RETURNING id`, digest, len(content)).Scan(&imageID); err != nil {
		t.Fatal(err)
	}
	images, err := json.Marshal([]string{"/api/admin/image-library/" + strconv.FormatInt(imageID, 10) + "/variants/original"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE products SET images=$2::jsonb WHERE id=$1`, productID, images); err != nil {
		t.Fatal(err)
	}
}

// assertProductExternalPushSyntheticDurableFacts deliberately uses only the
// durable Product/Outbound PostgreSQL rows.  The Chromium journey invokes it
// after the real browser flow, and the HTTP-only companion test below invokes
// the same assertion so a browser-launch failure cannot conceal a bad column,
// encrypted payload binding, or JSON precision regression.
func assertProductExternalPushSyntheticDurableFacts(t *testing.T, ctx context.Context, application *composedApplication, productID int64, dataKey []byte, expectedVersions ...int64) {
	expectedVersion := int64(1)
	if len(expectedVersions) > 0 {
		expectedVersion = expectedVersions[0]
	}
	t.Helper()
	var revision, storedExpiry int64
	var stored json.RawMessage
	var ciphertext []byte
	var keyVersion int16
	var sourceReference, targetSlot, effectID, state string
	err := application.pool.Native().QueryRow(ctx, "SELECT version,expires_at_ts,custom_params FROM product_external_push_configurations WHERE product_id=$1 AND product_kind='wechat_pay'", productID).Scan(&revision, &storedExpiry, &stored)
	if err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, "SELECT payload_ciphertext,payload_key_version,source_reference,target_slot,effect_id,state FROM outbound_commerce_push_intents WHERE product_id=$1 AND source_kind='synthetic_test'", productID).Scan(&ciphertext, &keyVersion, &sourceReference, &targetSlot, &effectID, &state); err != nil {
		t.Fatal(err)
	}
	if !externalPushStoredJSONHasExactBigInteger(stored) {
		t.Fatalf("browser configuration stored JSON lost required typed facts: %s", stored)
	}
	if revision != expectedVersion || storedExpiry != 2147483647 || !strings.HasPrefix(sourceReference, "synthetic:") || targetSlot != "product:"+strconv.FormatInt(productID, 10) || effectID == "" || state != "outcome_unknown" {
		t.Fatalf("browser configuration/intent revision=%d params=%s source=%q slot=%q effect=%q state=%q", revision, stored, sourceReference, targetSlot, effectID, state)
	}
	cipher, err := outbound.NewCommercePayloadAESGCM(base64.RawStdEncoding.EncodeToString(dataKey))
	if err != nil {
		t.Fatal(err)
	}
	body, err := cipher.DecryptCommercePayload(ciphertext, keyVersion, []byte("commerce-push.payload.v1\x00"+sourceReference+"\x00"+targetSlot))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(body, []byte("9007199254740993")) || bytes.Contains(body, []byte("9007199254740992")) {
		t.Fatalf("browser synthetic payload rounded JSON integer: %s", body)
	}
	var payload map[string]any
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err = decoder.Decode(&payload); err != nil {
		t.Fatal(err)
	}
	params, ok := payload["custom_params"].(map[string]any)
	if !ok || payload["event"] != "external_push.test" || params["count"] != json.Number("9007199254740993") {
		t.Fatalf("browser synthetic payload facts=%#v", payload)
	}
}

func externalPushStoredJSONHasExactBigInteger(raw json.RawMessage) bool {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value map[string]any
	if decoder.Decode(&value) != nil {
		return false
	}
	count, countOK := value["count"].(json.Number)
	flag, flagOK := value["flag"].(bool)
	nested, nestedOK := value["nested"].([]any)
	if !countOK || count.String() != "9007199254740993" || !flagOK || flag || !nestedOK || len(nested) != 1 {
		return false
	}
	first, firstOK := nested[0].(map[string]any)
	inner, innerOK := first["inner"].(json.Number)
	return firstOK && innerOK && inner.String() == "9007199254740993"
}

// TestPostgreSQLProductExternalPushDurableHTTPFacts executes the same save and
// controlled synthetic action through the authenticated Product HTTP handler,
// then runs the durable-row assertion without launching Chromium. It keeps the
// browser journey focused on DOM/Host behavior while making later PostgreSQL
// columns and envelope failures local, deterministic regression failures.
func TestPostgreSQLProductExternalPushDurableHTTPFacts(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	targetsJSON, err := json.Marshal(map[string]any{
		"browser-push-target": map[string]any{
			"slot": "browser-product-slot", "endpoint": "https://commerce-browser.invalid",
			"signing_key": base64.RawStdEncoding.EncodeToString([]byte("browser-fixture-signing-key")),
			"version":     "legacy-v1", "tenant_id": "aicrm-browser",
			"buyer_id":          map[string]any{"kind": "wecom_external_userid", "scope": "wecom-corp:browser"},
			"buyer_openid":      map[string]any{"kind": "mp_openid", "scope": "wechat-app:browser"},
			"buyer_unionid":     map[string]any{"kind": "unionid", "scope": "wechat-open-platform:browser"},
			"buyer_phone":       map[string]any{"kind": "phone", "scope": "phone:cn11"},
			"beneficiary_phone": map[string]any{"kind": "phone", "scope": "phone:cn11"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://push-durable.test",
		ReleaseSHA: "product-external-push-durable-http", WorkerOwner: "product-external-push-durable-http", WorkerLimit: 1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "product-external-push-durable-webhook-secret"},
		Survey:       platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Effects:      platformconfig.Effects{ProviderEnabled: true},
		CommercePush: platformconfig.CommercePush{ProviderEnabled: true, TargetsJSON: string(targetsJSON), PayloadDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Bootstrap:    platformconfig.Bootstrap{Enabled: true, Username: "product-durable-owner", Password: "product-durable-owner-password", DisplayName: "Product Durable Owner"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "product-durable-owner", Password: "product-durable-owner-password", DisplayName: "Product Durable Owner"}); err != nil {
		t.Fatal(err)
	}
	productID, _, _, err := seedProductExternalPushChromiumJourney(ctx, application)
	if err != nil {
		t.Fatal(err)
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerDone := make(chan error, 1)
	go func() { workerDone <- application.effectsRuntime.Run(workerCtx) }()
	defer func() {
		stopWorker()
		select {
		case runErr := <-workerDone:
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				t.Errorf("effects runtime: %v", runErr)
			}
		case <-time.After(20 * time.Second):
			t.Error("effects runtime did not stop")
		}
	}()
	session, csrf := adminAccessLogin(t, application.handler, "product-durable-owner", "product-durable-owner-password")
	configuration := `{"enabled":true,"configuration_reference":"browser-push-target","type":"member_open","day":30,"frequency":1,"expires_at_ts":2147483647,"remark":"HTTP durable contract","custom_params":{"count":9007199254740993,"nested":[{"inner":9007199254740993}],"flag":false},"expected_revision":0}`
	configurationResponse := productExternalPushAdminMutation(t, application.handler, http.MethodPut, "/api/admin/wechat-pay/products/"+strconv.FormatInt(productID, 10)+"/external-push", configuration, session, csrf, "product-durable-configuration-0001")
	if configurationResponse.Code != http.StatusOK {
		t.Fatalf("save product configuration status=%d", configurationResponse.Code)
	}
	testResponse := productExternalPushAdminMutation(t, application.handler, http.MethodPost, "/api/admin/wechat-pay/products/"+strconv.FormatInt(productID, 10)+"/external-push/test", `{}`, session, csrf, "product-durable-synthetic-0001")
	if testResponse.Code != http.StatusAccepted {
		t.Fatalf("queue synthetic product push status=%d", testResponse.Code)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		var state string
		err = application.pool.Native().QueryRow(ctx, "SELECT state FROM outbound_commerce_push_intents WHERE product_id=$1 AND source_kind='synthetic_test'", productID).Scan(&state)
		if err == nil && state == "outcome_unknown" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("synthetic Product push did not reach durable unknown state: %v", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	assertProductExternalPushSyntheticDurableFacts(t, ctx, application, productID, dataKey)
}

func productExternalPushAdminMutation(t *testing.T, handler http.Handler, method, path, body, session, csrf, key string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Idempotency-Key", key)
	request.Header.Set("X-CSRF-Token", csrf)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

// prepareProductExternalPushChromiumArtifacts constructs the same hashed browser
// artifact and V3 Host bundle required by the release before composing the
// server. The Go package test runs before CI's later release-artifact stage,
// so relying on a developer's pre-existing web/dist would make the outer-route
// journey non-reproducible and turn an absent Host into a misleading 503.
func prepareProductExternalPushChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	for _, invocation := range [][]string{
		{"npm", "run", "build", "--silent"},
		{"node", "scripts/build-v3-host-adapters.mjs"},
	} {
		command := exec.Command(invocation[0], invocation[1:]...)
		command.Dir = repository
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("prepare Product Chromium build artifact %s: %v output=%s", strings.Join(invocation, " "), err, strings.TrimSpace(string(output)))
		}
	}

	// Composition reads web/dist just as the installed binary does. Build the
	// complete release closure into an isolated stage first, then use that exact
	// staged artifact for this fixture. In particular, tags.html is a private
	// PR03 carrier generated only during staging; a raw frontend build has only
	// wecom-tags.html and would make the real tag Host fail closed with 503.
	stage := filepath.Join(t.TempDir(), "release", "web", "dist")
	for _, invocation := range [][]string{
		{"node", "scripts/stage-pr01-effects-ui.mjs", "web/dist", stage},
		{"node", "scripts/stage-survey-ui.mjs", "web/dist", stage},
		{"node", "scripts/stage-new-shell-ui.mjs", "web/dist", stage},
	} {
		command := exec.Command(invocation[0], invocation[1:]...)
		command.Dir = repository
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("prepare Product Chromium staged release artifact %s: %v output=%s", strings.Join(invocation[:2], " "), err, strings.TrimSpace(string(output)))
		}
	}
	dist := filepath.Join(repository, "web", "dist")
	if err := os.RemoveAll(dist); err != nil {
		t.Fatalf("replace Product Chromium build artifact: %v", err)
	}
	if err := os.MkdirAll(dist, 0o755); err != nil {
		t.Fatalf("create Product Chromium staged artifact root: %v", err)
	}
	if err := os.CopyFS(dist, os.DirFS(stage)); err != nil {
		t.Fatalf("install Product Chromium staged artifact: %v", err)
	}
}

func seedProductExternalPushChromiumJourney(ctx context.Context, application *composedApplication) (int64, int64, string, error) {
	projection := "{\"schema_version\":1,\"status\":\"enabled\",\"enabled\":true,\"buy_button_text\":\"立即购买\",\"require_mobile\":false,\"lead_program_id\":null,\"lead_channel_id\":null,\"lead_qr_title\":\"\",\"lead_qr_subtitle\":\"\",\"completion_redirect_enabled\":false,\"completion_redirect_url\":\"\",\"completion_target\":null,\"wecom_tagging\":{},\"slices\":[]}"
	pool := application.pool.Native()
	var productID int64
	if err := pool.QueryRow(ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('browser-push-product','浏览器外推商品','真实 Host 浏览器夹具',9900,'CNY',10,1,$1::jsonb) RETURNING id", projection).Scan(&productID); err != nil {
		return 0, 0, "", err
	}
	serviceProjection := "{\"schema_version\":1,\"status\":\"service_period_enabled\",\"enabled\":true,\"buy_button_text\":\"立即续费\",\"require_mobile\":false,\"lead_program_id\":null,\"lead_channel_id\":null,\"lead_qr_title\":\"\",\"lead_qr_subtitle\":\"\",\"completion_redirect_enabled\":false,\"completion_redirect_url\":\"\",\"completion_target\":null,\"wecom_tagging\":{},\"slices\":[]}"
	var serviceProductID int64
	if err := pool.QueryRow(ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES('browser-push-service-period','浏览器周期外推商品','真实周期 Host 浏览器夹具',12800,'CNY',10,1,$1::jsonb) RETURNING id", serviceProjection).Scan(&serviceProductID); err != nil {
		return 0, 0, "", err
	}
	if _, err := pool.Exec(ctx, "INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,30)", serviceProductID); err != nil {
		return 0, 0, "", err
	}
	// The frozen order-detail Host consumes the compatibility delivery URL.
	// Seed a mapped historical Order with its source kind/scope/key triple, not
	// a numerically coincident V3 order ID, so this page exercises the actual
	// Order -> Outbound Port bridge through the outer composition route.
	const orderReference = "WXP260911235959F6E5D4C3B2A1"
	const sourceKey = "browser-history-source-order-1"
	now := time.Now().UTC().Add(-time.Minute)
	orderDigest := sha256.Sum256([]byte("browser-history-order-1"))
	var orderID int64
	if err := pool.QueryRow(ctx, `INSERT INTO orders(provider,source_system,source_key,merchant_order_no,provider_transaction_no,payer_customer_id,beneficiary_customer_id,amount_minor,refunded_minor,currency,status,record_origin,effect_eligible,source_row_digest,version,created_at,updated_at)
VALUES('wechat_pay','commerce-history',$1,$2,'',NULL,NULL,9900,0,'CNY','paid','history',FALSE,$3,1,$4,$4) RETURNING id`, sourceKey, orderReference, orderDigest[:], now).Scan(&orderID); err != nil {
		return 0, 0, "", err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO order_items(order_id,line_no,product_id,product_code,product_name,unit_amount_minor,quantity,line_amount_minor)
VALUES($1,1,$2,'browser-push-product','浏览器外推商品',9900,1,9900)`, orderID, productID); err != nil {
		return 0, 0, "", err
	}
	deliveryDigest := sha256.Sum256([]byte("browser-history-delivery-1"))
	var historyRowID int64
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_rows(source_system,source_kind,source_id,source_digest,source_delivery_id,source_event_type,source_target_type,source_target_id,source_order_kind,source_order_scope,source_order_key,source_order_id,source_product_id,source_state,source_attempt_count,source_effect_job_id,source_effect_state,source_response_status,source_error_message,source_response_body_protected,source_created_at,source_updated_at,target_product_id,outcome,reason_code,read_only)
VALUES('browser-commerce-history','delivery',1,$1,'browser-history-delivery-1','transaction.paid','product',$2,'wechat_pay_order','commerce-history',$4,1,$3,'succeeded',1,1,'succeeded',204,'',FALSE,$5,$5,$3,'imported','product_mapping_imported',TRUE) RETURNING id`, deliveryDigest[:], strconv.FormatInt(productID, 10), productID, sourceKey, now).Scan(&historyRowID); err != nil {
		return 0, 0, "", err
	}
	batchDigest := sha256.Sum256([]byte("browser-history-delivery-batch-1"))
	var batchID int64
	if err := pool.QueryRow(ctx, `INSERT INTO outbound_commerce_push_history_batches(source_system,source_revision,manifest_digest,snapshot_at,status,input_count,imported_count,pending_count,excluded_count,applied_at)
VALUES('browser-commerce-history',$1,$2,$3,'applied',1,1,0,0,$3) RETURNING id`, strings.Repeat("b", 40), batchDigest[:], now).Scan(&batchID); err != nil {
		return 0, 0, "", err
	}
	if _, err := pool.Exec(ctx, `INSERT INTO outbound_commerce_push_history_batch_rows(batch_id,source_row_id,source_digest) VALUES($1,$2,$3)`, batchID, historyRowID, deliveryDigest[:]); err != nil {
		return 0, 0, "", err
	}
	return productID, serviceProductID, orderReference, nil
}
func seedProductMaterialChromiumImages(ctx context.Context, application *composedApplication) (int64, int64, error) {
	content, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADElEQVR42mP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC")
	if err != nil {
		return 0, 0, err
	}
	digestValue := sha256.Sum256(content)
	digest := "sha256:" + hex.EncodeToString(digestValue[:])
	pool := application.pool.Native()
	if _, err = pool.Exec(ctx, `INSERT INTO media_blobs(digest,mime_type,byte_size,content) VALUES($1,'image/png',$2,$3)`, digest, len(content), content); err != nil {
		return 0, 0, err
	}
	var laterID int64
	if err = pool.QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,'browser-product-later.png','Chromium 商品后续页素材','真实商品素材分页验收','chromium,product','chromium-product','image/png',$2,1,1,true,1,1) RETURNING id`, digest, len(content)).Scan(&laterID); err != nil {
		return 0, 0, err
	}
	var firstID int64
	for index := 1; index <= 50; index++ {
		name := "Chromium 商品目录填充 " + strconv.Itoa(index)
		if index == 50 {
			name = "Chromium 商品首页素材"
		}
		var imageID int64
		if err = pool.QueryRow(ctx, `INSERT INTO media_images(blob_digest,file_name,name,description,tags,category,mime_type,byte_size,width,height,enabled,created_by,updated_by) VALUES($1,$2,$3,'真实商品素材分页验收','chromium,product','chromium-product','image/png',$4,1,1,true,1,1) RETURNING id`, digest, "browser-product-page-"+strconv.Itoa(index)+".png", name, len(content)).Scan(&imageID); err != nil {
			return 0, 0, err
		}
		if index == 50 {
			firstID = imageID
		}
	}
	return firstID, laterID, nil
}
