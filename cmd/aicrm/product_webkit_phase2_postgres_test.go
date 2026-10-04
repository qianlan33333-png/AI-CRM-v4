package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	paymentport "github.com/qianlan33333-png/AI-CRM-v3/internal/payment/port"
)

// TestPostgreSQLProductWebKitPhase2Journey is an opt-in local diagnostic. It
// opens the real composed public and admin pages in the already-installed
// WebKit 15.4 and 26.0 drivers, while the checkout POST alone is held by the
// browser harness so no payment/provider effect can occur.
func TestPostgreSQLProductWebKitPhase2Journey(t *testing.T) {
	if os.Getenv("AICRM_REQUIRE_PRODUCT_WEBKIT_PHASE2") != "1" {
		t.Skip("set AICRM_REQUIRE_PRODUCT_WEBKIT_PHASE2=1 to run the local WebKit journey")
	}
	if runtime.GOOS != "darwin" {
		t.Skip("the configured WebKit test binaries are macOS bundles")
	}
	node := os.Getenv("AICRM_PHASE2_NODE")
	evidenceDir := os.Getenv("AICRM_PHASE2_EVIDENCE_DIR")
	oldRoot := os.Getenv("AICRM_PHASE2_OLD_PLAYWRIGHT_ROOT")
	oldDriver := os.Getenv("AICRM_PHASE2_OLD_WEBKIT_DRIVER")
	modernRoot := os.Getenv("AICRM_PHASE2_MODERN_PLAYWRIGHT_ROOT")
	modernDriver := os.Getenv("AICRM_PHASE2_MODERN_WEBKIT_DRIVER")
	for name, value := range map[string]string{
		"AICRM_PHASE2_NODE": node, "AICRM_PHASE2_EVIDENCE_DIR": evidenceDir,
		"AICRM_PHASE2_OLD_PLAYWRIGHT_ROOT": oldRoot, "AICRM_PHASE2_OLD_WEBKIT_DRIVER": oldDriver,
		"AICRM_PHASE2_MODERN_PLAYWRIGHT_ROOT": modernRoot, "AICRM_PHASE2_MODERN_WEBKIT_DRIVER": modernDriver,
	} {
		if value == "" {
			t.Fatalf("%s is required", name)
		}
	}
	if err := os.MkdirAll(filepath.Join(evidenceDir, "phase2-webkit-screenshots"), 0o700); err != nil {
		t.Fatal(err)
	}

	fixture := newProductExternalPushChromiumFixtureWithOptions(t, 240*time.Second, productExternalPushChromiumFixtureOptions{
		enablePublicH5: true, deferEffectsWorker: true,
	})
	trusted := issuePublicCommerceTrustedH5SessionWithKey(t, fixture, "product-phase2-webkit-trusted-session")
	adminSession, adminCSRF := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	script := filepath.Join(filepath.Dir(fixture.script), "product_webkit_phase2_journey.mjs")
	type browserTarget struct {
		name, expected, packageRoot, driver string
	}
	targets := []browserTarget{
		{name: "webkit15.4", expected: "15.4", packageRoot: oldRoot, driver: oldDriver},
		{name: "webkit26.0", expected: "26.0", packageRoot: modernRoot, driver: modernDriver},
	}
	var failures []string
	for _, target := range targets {
		standardCode := "phase2-" + strings.ReplaceAll(target.name, ".", "") + "-standard"
		serviceCode := "phase2-" + strings.ReplaceAll(target.name, ".", "") + "-service"
		standardID, serviceID := seedProductWebKitPhase2Products(t, fixture, standardCode, serviceCode)
		screenshotDir := filepath.Join(evidenceDir, "phase2-webkit-screenshots", target.name)
		if err := os.MkdirAll(screenshotDir, 0o700); err != nil {
			t.Fatal(err)
		}
		command := exec.CommandContext(fixture.ctx, node, script)
		command.Dir = filepath.Dir(script)
		command.Env = append(os.Environ(),
			"AICRM_PHASE2_BASE_URL="+fixture.server.URL,
			"AICRM_PHASE2_EXPECTED_ENGINE="+target.expected,
			"AICRM_PHASE2_PLAYWRIGHT_ROOT="+target.packageRoot,
			"AICRM_PHASE2_WEBKIT_DRIVER="+target.driver,
			"AICRM_PHASE2_SCREENSHOT_DIR="+screenshotDir,
			"AICRM_PHASE2_STANDARD_ID="+fmt.Sprintf("%d", standardID),
			"AICRM_PHASE2_SERVICE_ID="+fmt.Sprintf("%d", serviceID),
			"AICRM_PHASE2_STANDARD_CODE="+standardCode,
			"AICRM_PHASE2_SERVICE_CODE="+serviceCode,
			"AICRM_PHASE2_ADMIN_SESSION="+adminSession,
			"AICRM_PHASE2_ADMIN_CSRF="+adminCSRF,
			"AICRM_PHASE2_SESSION_COOKIE_NAME="+accesshttp.SessionCookieName,
			"AICRM_PHASE2_CSRF_COOKIE_NAME="+accesshttp.CSRFCookieName,
			"AICRM_PHASE2_PAYMENT_COOKIE_NAME="+paymentport.TrustedSessionCookieName,
			"AICRM_PHASE2_PAYMENT_SESSION="+trusted.token,
		)
		output, err := command.CombinedOutput()
		logPath := filepath.Join(evidenceDir, target.name+"-browser.log")
		if writeErr := os.WriteFile(logPath, output, 0o600); writeErr != nil {
			t.Errorf("%s save browser log: %v", target.name, writeErr)
		}
		t.Logf("%s browser log: %s", target.name, logPath)
		t.Logf("%s screenshots: %s", target.name, screenshotDir)
		var receipt struct {
			Event  string `json:"event"`
			Status string `json:"status"`
			Engine string `json:"engine"`
		}
		decodeErr := json.Unmarshal(output, &receipt)
		if err != nil || decodeErr != nil || receipt.Event != "commerce_product_webkit_phase2" || receipt.Status != "PASS" || receipt.Engine != target.expected {
			failures = append(failures, fmt.Sprintf("%s err=%v output=%s", target.name, err, strings.TrimSpace(string(output))))
		}
		if checkErr := assertProductWebKitPhase2Readback(t, fixture.ctx, fixture, standardID, "edited-"+target.expected+"-standard"); checkErr != nil {
			failures = append(failures, target.name+" ordinary PG readback: "+checkErr.Error())
		}
		if checkErr := assertProductWebKitPhase2Readback(t, fixture.ctx, fixture, serviceID, "edited-"+target.expected+"-service"); checkErr != nil {
			failures = append(failures, target.name+" service-period PG readback: "+checkErr.Error())
		}
	}
	var payments int64
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, "SELECT count(*) FROM payments").Scan(&payments); err != nil {
		t.Fatal(err)
	}
	if payments != 0 {
		failures = append(failures, fmt.Sprintf("phase2 journey unexpectedly created %d payment rows", payments))
	}
	if len(failures) != 0 {
		t.Fatalf("phase2 WebKit journey failures:\n%s", strings.Join(failures, "\n"))
	}
}

func seedProductWebKitPhase2Products(t *testing.T, fixture *productExternalPushChromiumFixture, standardCode, serviceCode string) (int64, int64) {
	t.Helper()
	pool := fixture.application.pool.Native()
	standardProjection, err := json.Marshal(map[string]any{
		"schema_version": 1, "status": "enabled", "enabled": true, "buy_button_text": "立即购买",
		"require_mobile": true, "contact_collection_level": "shipping_address",
		"lead_program_id": nil, "lead_channel_id": nil, "lead_qr_title": "", "lead_qr_subtitle": "",
		"completion_redirect_enabled": false, "completion_redirect_url": "", "completion_target": nil,
		"wecom_tagging": map[string]any{}, "slices": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	serviceProjection, err := json.Marshal(map[string]any{
		"schema_version": 1, "status": "service_period_enabled", "enabled": true, "buy_button_text": "立即报名",
		"require_mobile": true, "contact_collection_level": "shipping_address",
		"lead_program_id": nil, "lead_channel_id": nil, "lead_qr_title": "", "lead_qr_subtitle": "",
		"completion_redirect_enabled": false, "completion_redirect_url": "", "completion_target": nil,
		"wecom_tagging": map[string]any{}, "slices": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var standardID, serviceID int64
	if err = pool.QueryRow(fixture.ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES($1,'phase2普通商品','WebKit真实页面fixture',1234,'CNY',10,1,$2::jsonb) RETURNING id", standardCode, string(standardProjection)).Scan(&standardID); err != nil {
		t.Fatal(err)
	}
	if err = pool.QueryRow(fixture.ctx, "INSERT INTO products(product_code,name,description,price_minor,currency,stock_quantity,created_by,legacy_admin_projection) VALUES($1,'phase2周期商品','WebKit真实页面fixture',1234,'CNY',10,1,$2::jsonb) RETURNING id", serviceCode, string(serviceProjection)).Scan(&serviceID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(fixture.ctx, "INSERT INTO product_imported_service_period_definitions(product_id,duration_days) VALUES($1,30)", serviceID); err != nil {
		t.Fatal(err)
	}
	var images []byte
	// The shared composition fixture already seeds a synthetic Product-owned
	// image on its ordinary product. Reuse that image reference so this journey
	// does not insert the fixture's deterministic media digest a second time.
	if err = pool.QueryRow(fixture.ctx, "SELECT images FROM products WHERE id=$1", fixture.productID).Scan(&images); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(fixture.ctx, "UPDATE products SET images=$1::jsonb WHERE id=$2", string(images), standardID); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(fixture.ctx, "UPDATE products SET images=$1::jsonb WHERE id=$2", string(images), serviceID); err != nil {
		t.Fatal(err)
	}
	return standardID, serviceID
}

func assertProductWebKitPhase2Readback(t *testing.T, ctx context.Context, fixture *productExternalPushChromiumFixture, id int64, expectedName string) error {
	t.Helper()
	var price int64
	var images, name string
	if err := fixture.application.pool.Native().QueryRow(ctx, "SELECT price_minor,images::text,name FROM products WHERE id=$1", id).Scan(&price, &images, &name); err != nil {
		return err
	}
	if price != 1234 || images != "[]" || name != expectedName {
		return fmt.Errorf("id=%d price_minor=%d images=%s name=%q expected name=%q", id, price, images, name, expectedName)
	}
	return nil
}
