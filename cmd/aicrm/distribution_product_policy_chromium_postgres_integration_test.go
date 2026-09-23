package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLDistributionProductPolicyChromiumJourney exercises the two
// real product editor Hosts.  It saves their policy as part of the normal
// product command, reloads the editor, and verifies the persisted CAS version
// through Distribution's owned table.  The companion composition test covers
// a stale concurrent CAS write; this browser test proves that the UI sends the
// version it read rather than inventing a follow-up policy request.
func TestPostgreSQLDistributionProductPolicyChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	fixture := newProductExternalPushChromiumFixture(t)
	journey := filepath.Join(filepath.Dir(fixture.script), "distribution_product_policy_chromium_journey.mjs")
	command := exec.CommandContext(fixture.ctx, "node", journey)
	command.Env = append(os.Environ(),
		"AICRM_DISTRIBUTION_POLICY_BROWSER_URL="+fixture.server.URL,
		"AICRM_DISTRIBUTION_POLICY_BROWSER_USERNAME=product-browser-owner",
		"AICRM_DISTRIBUTION_POLICY_BROWSER_PASSWORD=product-browser-owner-password",
		"AICRM_DISTRIBUTION_POLICY_BROWSER_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10),
		"AICRM_DISTRIBUTION_POLICY_BROWSER_SERVICE_PRODUCT_ID="+strconv.FormatInt(fixture.serviceProductID, 10),
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "distribution_product_policy_chromium: PASS") {
		t.Fatalf("Distribution Product policy Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	for _, expected := range []struct {
		id       int64
		kind     string
		enabled  bool
		rate     int
		waitDays int
	}{
		{fixture.productID, "standard_product", false, 2345, 8},
		{fixture.serviceProductID, "service_period", false, 2345, 0},
	} {
		var enabled bool
		var rate, waitDays int
		var version int64
		err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT enabled,commission_rate_basis_points,wait_days,version FROM distribution_product_policies WHERE product_id=$1 AND product_type=$2`, expected.id, expected.kind).Scan(&enabled, &rate, &waitDays, &version)
		if err != nil || enabled != expected.enabled || rate != expected.rate || waitDays != expected.waitDays || version != 3 {
			t.Fatalf("persisted browser policy product=%d kind=%s enabled=%t rate=%d wait_days=%d version=%d err=%v", expected.id, expected.kind, enabled, rate, waitDays, version, err)
		}
	}
}
