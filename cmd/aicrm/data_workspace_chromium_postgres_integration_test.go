package main

import (
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

func TestPostgreSQLDataWorkspaceChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	fixture := newAdminShellLayoutFixture(t)
	session, csrf := adminAccessLogin(t, fixture.application.handler, "product-browser-owner", "product-browser-owner-password")
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "data_workspace_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_DATA_WORKSPACE_URL="+fixture.server.URL, "AICRM_DATA_WORKSPACE_SESSION="+session, "AICRM_DATA_WORKSPACE_CSRF="+csrf, "AICRM_DATA_WORKSPACE_PRODUCT="+strconv.FormatInt(fixture.serviceProductID, 10), "AICRM_DATA_WORKSPACE_SCREENSHOTS="+fixture.screenshots)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("data workspace Chromium: %v\n%s", err, output)
	} else {
		t.Log(string(output))
	}
}
