package main

import (
	"bytes"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID, persistence and Provider-effect decisions: not involved. This only
// uses the existing authenticated PostgreSQL Composition fixture so the
// admin-only route and its actual staged assets are exercised in a browser.
type componentStatesChromiumFixture struct {
	*groupOpsChromiumFixture
	screenshots string
}

func TestPostgreSQLComponentStatesCompositionPreflight(t *testing.T) {
	fixture := newComponentStatesChromiumFixture(t)
	session, _ := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	page := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/component-states")
	body := page.Body.Bytes()
	if page.Code != http.StatusOK {
		t.Fatalf("component state route status=%d body=%q", page.Code, page.Body.String())
	}
	for _, marker := range []string{
		`data-page="component-states"`,
		`data-component-states-root`,
		`presentationStyles-`,
		`sharedVisualTokens-`,
		`componentStatesStyles-`,
		`componentStatesHost-`,
		`selectionDialogStyles-`,
		`groupopsStyles-`,
		`standard-components/group_chat_picker.css`,
		`standard-components/material_picker.css`,
	} {
		if !bytes.Contains(body, []byte(marker)) {
			t.Fatalf("component state page lacks actual asset or mount %q", marker)
		}
	}
	if bytes.Index(body, []byte("presentationStyles-")) > bytes.Index(body, []byte("sharedVisualTokens-")) {
		t.Fatal("component visual token aliases must load after presentation styles")
	}
	unauthenticated := authenticatedAdminGet(t, fixture.application.handler, "", "/admin/component-states")
	if unauthenticated.Code == http.StatusOK {
		t.Fatal("component state route bypassed the existing admin session gate")
	}
}

func TestPostgreSQLComponentStatesChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newComponentStatesChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "component_states_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_COMPONENT_STATES_TEST_URL="+fixture.server.URL,
		"AICRM_COMPONENT_STATES_TEST_USERNAME=groupops-browser-owner",
		"AICRM_COMPONENT_STATES_TEST_PASSWORD=groupops-browser-owner-password",
		"AICRM_COMPONENT_STATES_SCREENSHOT_DIR="+fixture.screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "component_states_chromium: PASS") {
		t.Fatalf("component states Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	for _, name := range []string{
		"component-states-1280.png",
		"component-states-1440.png",
		"component-states-360.png",
		"component-states-420.png",
		"component-states-error-retry.png",
		"component-states-forbidden.png",
		"component-states-tag-420.png",
		"component-states-staff-1440.png",
		"component-states-composer-360.png",
		"component-states-composer-readonly-1440.png",
		"component-states-form.png",
	} {
		info, statErr := os.Stat(filepath.Join(fixture.screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("component state screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

func newComponentStatesChromiumFixture(t *testing.T) *componentStatesChromiumFixture {
	t.Helper()
	fixture := newGroupOpsChromiumFixture(t)
	prepareComponentStatesChromiumArtifacts(t, filepath.Clean(filepath.Join(filepath.Dir(fixture.script), "..", "..")))
	screenshots := t.TempDir()
	if configured := platformconfig.ComponentStatesScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_COMPONENT_STATES_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatalf("create component state screenshots: %v", err)
		}
		screenshots = configured
	}
	return &componentStatesChromiumFixture{groupOpsChromiumFixture: fixture, screenshots: screenshots}
}

func prepareComponentStatesChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil {
		t.Fatalf("component states Chromium requires built asset manifest: %v", err)
	}
	for _, entry := range []string{"presentationStyles", "sharedVisualTokens", "componentStatesStyles", "componentStatesHost", "selectionDialogStyles", "groupopsStyles"} {
		if !bytes.Contains(manifest, []byte("\""+entry+"\"")) {
			t.Fatalf("component states Chromium manifest lacks %s", entry)
		}
	}
	for _, asset := range []string{"assets/standard-components/group_chat_picker.css", "assets/standard-components/material_picker.css"} {
		if _, statErr := os.Stat(filepath.Join(repository, "web", "dist", filepath.FromSlash(asset))); statErr != nil {
			t.Fatalf("component states Chromium asset %s: %v", asset, statErr)
		}
	}
}
