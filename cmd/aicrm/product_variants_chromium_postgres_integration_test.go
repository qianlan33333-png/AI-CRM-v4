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

func TestPostgreSQLProductVariantsChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	fixture := newProductExternalPushChromiumFixture(t)
	cmd := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "product_variants_chromium_journey.mjs"))
	cmd.Env = append(os.Environ(), "AICRM_VARIANT_BROWSER_URL="+fixture.server.URL, "AICRM_VARIANT_BROWSER_PRODUCT_ID="+strconv.FormatInt(fixture.productID, 10))
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "product_variants_chromium: PASS") {
		t.Fatalf("variant browser: %v\n%s", err, out)
	}
	var count int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM product_variants WHERE product_id=$1 AND removed_at IS NULL`, fixture.productID).Scan(&count); err != nil || count != 2 {
		t.Fatal("stored variants", count, err)
	}
}
