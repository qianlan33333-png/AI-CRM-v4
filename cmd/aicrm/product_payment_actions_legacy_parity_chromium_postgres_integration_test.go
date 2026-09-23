package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLProductPaymentActionsLegacyParityChromiumJourney drives the
// composed V3 Host, rather than a JSDOM adapter: both ordinary and
// service-period product pages save and reload the exact legacy-parity
// purchase-action and external-push panels. The script writes four 1440x1100
// screenshots for review. It has no OneID work; Product configuration and its
// external-effect test acceptance continue through their existing PostgreSQL
// Unit of Work.
func TestPostgreSQLProductPaymentActionsLegacyParityChromiumJourney(t *testing.T) {
	if goruntime.GOOS == "darwin" && !platformconfig.ProductExternalPushDarwinChromiumDiagnosticAllowed() {
		t.Skip("Chromium CDP journey requires Linux CI; set the explicit Darwin diagnostic switch locally")
	}
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}

	fixture := newProductExternalPushChromiumFixture(t)
	screenshots, err := os.MkdirTemp("", "aicrm-payment-actions-legacy-parity-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("payment-action Host screenshots: %s", screenshots)
	script := filepath.Join(filepath.Dir(fixture.script), "product_payment_actions_legacy_parity_chromium_journey.mjs")
	command := exec.CommandContext(fixture.ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_PAYMENT_ACTIONS_TEST_URL="+fixture.server.URL,
		"AICRM_PAYMENT_ACTIONS_TEST_USERNAME=product-browser-owner",
		"AICRM_PAYMENT_ACTIONS_TEST_PASSWORD=product-browser-owner-password",
		"AICRM_PAYMENT_ACTIONS_TEST_PRODUCT_ID="+fmt.Sprintf("%d", fixture.productID),
		"AICRM_PAYMENT_ACTIONS_TEST_SERVICE_PRODUCT_ID="+fmt.Sprintf("%d", fixture.serviceProductID),
		"AICRM_PAYMENT_ACTIONS_SCREENSHOT_DIR="+screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("payment-action legacy parity Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "product_payment_actions_legacy_parity: PASS") {
		t.Fatalf("payment-action legacy parity Chromium journey did not report success: %q", output)
	}
	for _, name := range []string{
		"ordinary-after-redirect-1440x1100.png",
		"ordinary-external-push-1440x1100.png",
		"service-period-after-url-link-1440x1100.png",
		"service-external-push-1440x1100.png",
	} {
		info, readErr := os.Stat(filepath.Join(screenshots, name))
		if readErr != nil || info.Size() == 0 {
			t.Fatalf("payment-action screenshot=%s err=%v size=%d", name, readErr, sizeOf(info))
		}
	}
	assertLegacyParityHostPushConfiguration(t, fixture, fixture.productID, "wechat_pay", "ordinary_paid", "ordinary parity remark", "ordinary")
	assertLegacyParityHostPushConfiguration(t, fixture, fixture.serviceProductID, "service_period", "service_paid", "service parity remark", "service")
	assertLegacyParityHostAction(t, fixture, fixture.productID, "h5", "https://after.example.test/ordinary", "")
	assertLegacyParityHostAction(t, fixture, fixture.serviceProductID, "url_link", "", "https://link.example.test/service-period")
}

func sizeOf(info os.FileInfo) int64 {
	if info == nil {
		return 0
	}
	return info.Size()
}

func assertLegacyParityHostPushConfiguration(t *testing.T, fixture *productExternalPushChromiumFixture, productID int64, kind, pushType, remark, campaign string) {
	t.Helper()
	var enabled bool
	var actualType, actualRemark string
	var expires, day, frequency int64
	var params json.RawMessage
	var version int64
	err := fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT enabled,push_type,expires_at_ts,day,frequency,remark,custom_params,version
FROM product_external_push_configurations WHERE product_id=$1 AND product_kind=$2`, productID, kind).Scan(&enabled, &actualType, &expires, &day, &frequency, &actualRemark, &params, &version)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if json.Unmarshal(params, &decoded) != nil || !enabled || actualType != pushType || expires != 2147483647 || day != 30 || frequency != 1 || actualRemark != remark || decoded["campaign"] != campaign || version != 1 {
		t.Fatalf("legacy parity push kind=%s enabled=%t type=%q expires=%d day=%d frequency=%d remark=%q params=%s version=%d", kind, enabled, actualType, expires, day, frequency, actualRemark, params, version)
	}
}

func assertLegacyParityHostAction(t *testing.T, fixture *productExternalPushChromiumFixture, productID int64, targetType, h5URL, sourceURL string) {
	t.Helper()
	var projection json.RawMessage
	if err := fixture.application.pool.Native().QueryRow(fixture.ctx, "SELECT legacy_admin_projection FROM products WHERE id=$1", productID).Scan(&projection); err != nil {
		t.Fatal(err)
	}
	var value struct {
		Enabled bool   `json:"purchase_action_enabled"`
		Mode    string `json:"purchase_action_mode"`
		Target  struct {
			Enabled    bool   `json:"enabled"`
			TargetType string `json:"target_type"`
			H5URL      string `json:"h5_url"`
			URLLink    struct {
				Enabled   bool   `json:"enabled"`
				SourceURL string `json:"source_url"`
			} `json:"url_link"`
		} `json:"completion_target"`
	}
	if json.Unmarshal(projection, &value) != nil || !value.Enabled || value.Mode != "redirect" || !value.Target.Enabled || value.Target.TargetType != targetType || value.Target.H5URL != h5URL || value.Target.URLLink.SourceURL != sourceURL || targetType == "url_link" && !value.Target.URLLink.Enabled {
		t.Fatalf("legacy parity action target=%s projection=%s", targetType, projection)
	}
}
