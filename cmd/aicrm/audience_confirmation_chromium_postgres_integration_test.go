package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID is not involved: this Journey only authenticates the existing local
// administrator and displays a Segment-owned package. Persistence stays with
// the existing Audience Owner command; the shared dialog returns only a local
// confirmation result and never owns the HTTP request.
type audienceConfirmationChromiumFixture struct {
	*groupOpsChromiumFixture
	packageID   int64
	screenshots string
}

func TestPostgreSQLAudienceConfirmationCompositionPreflight(t *testing.T) {
	fixture := newAudienceConfirmationChromiumFixture(t)
	session, _ := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	page := authenticatedAdminGet(t, fixture.application.handler, session, "/admin/automation-conversion")
	if page.Code != http.StatusOK {
		t.Fatalf("audience shell status=%d body=%q", page.Code, page.Body.String())
	}
	for _, marker := range []string{
		`confirmationDialogHost-`,
		`confirmationDialogStyles-`,
		`admin_audience_detail.js?v=audience-direct-push-controls-v1`,
	} {
		if !bytes.Contains(page.Body.Bytes(), []byte(marker)) {
			t.Fatalf("audience confirmation shell lacks real asset %q", marker)
		}
	}
	unauthenticated := authenticatedAdminGet(t, fixture.application.handler, "", "/admin/automation-conversion")
	if unauthenticated.Code == http.StatusOK {
		t.Fatal("audience confirmation route bypassed the existing admin session gate")
	}
}

func TestPostgreSQLAudienceConfirmationChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newAudienceConfirmationChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "audience_confirmation_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_AUDIENCE_CONFIRMATION_TEST_URL="+fixture.server.URL,
		"AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME=groupops-browser-owner",
		"AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD=groupops-browser-owner-password",
		"AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID="+strconv.FormatInt(fixture.packageID, 10),
		"AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR="+fixture.screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "audience_confirmation_chromium: PASS") {
		t.Fatalf("audience confirmation Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	for _, name := range []string{"audience-confirmation-1440.png", "audience-confirmation-1280.png", "audience-confirmation-390.png"} {
		info, statErr := os.Stat(filepath.Join(fixture.screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("audience confirmation screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
	var lifecycle string
	var version int64
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT lifecycle,version FROM segment_audience_packages WHERE id=$1`, fixture.packageID).Scan(&lifecycle, &version); err != nil || lifecycle != "archived" || version < 2 {
		t.Fatalf("browser archive Owner readback lifecycle=%q version=%d err=%v", lifecycle, version, err)
	}
}

func newAudienceConfirmationChromiumFixture(t *testing.T) *audienceConfirmationChromiumFixture {
	t.Helper()
	fixture := newGroupOpsChromiumFixture(t)
	repository := filepath.Clean(filepath.Join(filepath.Dir(fixture.script), "..", ".."))
	prepareAudienceConfirmationChromiumArtifacts(t, repository)
	screenshots := t.TempDir()
	if configured := platformconfig.AudienceConfirmationScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatalf("create audience confirmation screenshots: %v", err)
		}
		screenshots = configured
	}
	session, csrf := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	payload, err := json.Marshal(map[string]any{"name": "Chromium 确认人群包", "template_key": "active_contacts"})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/ai-audience/packages", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "audience-confirmation-browser-create-0001")
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	fixture.application.handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("create local audience package status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Package struct {
			ID int64 `json:"id"`
		} `json:"package"`
	}
	if err = json.NewDecoder(response.Body).Decode(&created); err != nil || created.Package.ID < 1 {
		t.Fatalf("decode local audience package err=%v payload=%s", err, response.Body.String())
	}
	return &audienceConfirmationChromiumFixture{groupOpsChromiumFixture: fixture, packageID: created.Package.ID, screenshots: screenshots}
}

func prepareAudienceConfirmationChromiumArtifacts(t *testing.T, repository string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil {
		t.Fatalf("audience confirmation Chromium requires built asset manifest: %v", err)
	}
	for _, entry := range []string{"confirmationDialogHost", "confirmationDialogStyles"} {
		if !bytes.Contains(manifest, []byte(`"`+entry+`"`)) {
			t.Fatalf("audience confirmation manifest lacks %s", entry)
		}
	}
}
