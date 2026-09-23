package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID decision: this control-plane journey does not resolve or provision a
// customer. Persistence decision: every control-plane write is Access-owned
// and uses its ordinary PostgreSQL UoW; no Provider is called.
type openPlatformChromiumFixture struct {
	ctx         context.Context
	application *composedApplication
	server      *httptest.Server
	script      string
}

// TestPostgreSQLOpenPlatformV1CompositionPreflight is intentionally separate
// from the CDP gate. It runs in every PostgreSQL check and proves that the
// release artifact, authenticated outer route and management-detail wire
// contract are real before the Linux-only browser step runs the lifecycle.
func TestPostgreSQLOpenPlatformV1CompositionPreflight(t *testing.T) {
	_ = newOpenPlatformChromiumFixture(t)
}

// TestPostgreSQLOpenPlatformV1ChromiumJourney uses the real administrator
// session, CSRF bridge, V3-owned Host and Access-owned machine control plane.
// The browser creates a disabled V1 client, receives its one-time credential,
// activates it, changes a grant, rotates, activates again and disables it.
// It then proves OAuth, the REST catalog and MCP catalog at each applicable
// lifecycle boundary without ever writing a credential to test output.
func TestPostgreSQLOpenPlatformV1ChromiumJourney(t *testing.T) {
	// Chromium itself is a dedicated Linux CI gate. The independently named
	// preflight above remains part of ordinary PostgreSQL checks, so this guard
	// never turns the release-artifact or Composition contract into a local skip.
	if goruntime.GOOS == "darwin" {
		t.Skip("Chromium CDP journey requires Linux CI; the PostgreSQL Composition preflight runs separately")
	}
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}

	fixture := newOpenPlatformChromiumFixture(t)
	t.Log("open platform Chromium: Linux CDP script launched")
	command := exec.CommandContext(fixture.ctx, "node", fixture.script)
	command.Env = append(os.Environ(),
		"AICRM_OPEN_PLATFORM_TEST_URL="+fixture.server.URL,
		"AICRM_OPEN_PLATFORM_TEST_USERNAME=open-platform-browser-owner",
		"AICRM_OPEN_PLATFORM_TEST_PASSWORD=open-platform-browser-owner-password",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Open Platform Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "open_platform_chromium: PASS") {
		t.Fatalf("Open Platform Chromium journey did not report success: %q", output)
	}

	var enabled bool
	var authVersion, audits int
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT enabled,auth_version FROM access_machine_clients WHERE client_id='browser-open-agent'`).Scan(&enabled, &authVersion); err != nil {
		t.Fatal(err)
	}
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT count(*) FROM access_machine_audit a JOIN access_machine_clients c ON c.id=a.machine_client_id WHERE c.client_id='browser-open-agent'`).Scan(&audits); err != nil {
		t.Fatal(err)
	}
	if enabled || authVersion < 5 || audits < 5 {
		t.Fatalf("Open Platform Chromium durable lifecycle enabled=%t auth_version=%d audits=%d", enabled, authVersion, audits)
	}
}

func newOpenPlatformChromiumFixture(t *testing.T) *openPlatformChromiumFixture {
	t.Helper()
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Open Platform Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	t.Log("open platform Chromium: release artifact prepared")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)

	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: origin,
		ReleaseSHA:   "open-platform-v1-chromium-journey",
		WorkerOwner:  "open-platform-v1-chromium-journey",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "open-platform-v1-chromium-journey-webhook-secret"},
		WeCom:        platformconfig.WeCom{CorpID: "open-platform-browser"},
		OpenPlatform: platformconfig.OpenPlatform{JWTSigningKey: "01234567890123456789012345678901"},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
			OAuthOpenPlatformID:  "open-platform-browser",
		},
		Bootstrap: platformconfig.Bootstrap{
			Enabled: true, Username: "open-platform-browser-owner", Password: "open-platform-browser-owner-password", DisplayName: "Open Platform Browser Owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{
		Enabled: true, Username: "open-platform-browser-owner", Password: "open-platform-browser-owner-password", DisplayName: "Open Platform Browser Owner",
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("open platform Chromium: PostgreSQL composition ready")

	outerSession, outerCSRF := adminAccessLogin(t, application.handler, "open-platform-browser-owner", "open-platform-browser-owner-password")
	if _, authenticateErr := application.authentication.Authenticate(ctx, outerSession); authenticateErr != nil {
		t.Fatal("test login did not issue a usable administrator session")
	}
	// The native V1 DTO guarantees [] rather than null for an empty CIDR list.
	// The Host remains defensive, but this preflight catches a composed contract
	// regression before Chromium needs to render the detail panel.
	probe := httptest.NewRequest(http.MethodPost, "/api/admin/open-platform/clients", strings.NewReader(`{"client_id":"browser-open-empty-cidr-probe","display_name":"Browser Empty CIDR Probe","purpose":"external_agent","audiences":["external_integration"],"scopes":["read"],"capabilities":["platform.capabilities.read"],"allowed_cidrs":[],"token_ttl_seconds":1800}`))
	probe.Header.Set("Content-Type", "application/json")
	probe.Header.Set("X-CSRF-Token", outerCSRF)
	probe.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	probe.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: outerCSRF})
	probeResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(probeResponse, probe)
	if probeResponse.Code != http.StatusCreated {
		t.Fatalf("Open Platform empty-CIDR probe create status=%d", probeResponse.Code)
	}
	probeRead := httptest.NewRequest(http.MethodGet, "/api/admin/open-platform/clients/browser-open-empty-cidr-probe", nil)
	probeRead.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	probeReadResponse := httptest.NewRecorder()
	application.handler.ServeHTTP(probeReadResponse, probeRead)
	var probeDetail struct {
		Client struct {
			AllowedCIDRs json.RawMessage `json:"allowed_cidrs"`
		} `json:"client"`
	}
	if probeReadResponse.Code != http.StatusOK || json.Unmarshal(probeReadResponse.Body.Bytes(), &probeDetail) != nil {
		t.Fatalf("Open Platform empty-CIDR detail status=%d response_valid=%t", probeReadResponse.Code, probeReadResponse.Code == http.StatusOK)
	}
	if string(probeDetail.Client.AllowedCIDRs) != "[]" {
		t.Fatal("Open Platform empty-CIDR detail did not use an empty array")
	}
	outer := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin/apidocs.html", nil)
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: outerSession})
	application.handler.ServeHTTP(outer, request)
	if outer.Code != http.StatusOK || !bytes.Contains(outer.Body.Bytes(), []byte(`data-page="apidocs"`)) || !bytes.Contains(outer.Body.Bytes(), []byte("openPlatformHost-")) {
		t.Fatalf("outer composed Open Platform Host status=%d api_docs=%t host_asset=%t", outer.Code, bytes.Contains(outer.Body.Bytes(), []byte(`data-page="apidocs"`)), bytes.Contains(outer.Body.Bytes(), []byte("openPlatformHost-")))
	}
	t.Log("open platform Chromium: outer route and management detail preflight passed")
	// The Host must not expose a writable create form while this selected detail
	// request is outstanding. Delay only the browser server's probe detail read;
	// the direct preflight above remains the ordinary Composition contract.
	const selectedDetailPath = "/api/admin/open-platform/clients/browser-open-empty-cidr-probe"
	const releaseFirstDetailPath = "/__test__/release-open-platform-first-detail"
	var selectedDetailReads atomic.Int32
	firstDetailRelease := make(chan struct{})
	var releaseFirstDetail sync.Once
	// This must run before the earlier server.Close cleanup: an interrupted
	// browser journey may leave the first delayed request waiting on the test
	// control channel, and httptest.Server.Close waits for that handler.
	t.Cleanup(func() { releaseFirstDetail.Do(func() { close(firstDetailRelease) }) })
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == releaseFirstDetailPath {
			releaseFirstDetail.Do(func() { close(firstDetailRelease) })
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == selectedDetailPath && selectedDetailReads.Add(1) == 1 {
			// The browser deliberately refreshes this same selected caller.
			// Keep only the first detail request pending until the script has
			// filled the settled second form, then explicitly release it. This
			// makes the stale response ordering deterministic without delays.
			select {
			case <-firstDetailRelease:
			case <-request.Context().Done():
				return
			}
		}
		application.handler.ServeHTTP(writer, request)
	})
	server.StartTLS()
	return &openPlatformChromiumFixture{
		ctx:         ctx,
		application: application,
		server:      server,
		script:      filepath.Join(filepath.Dir(source), "open_platform_chromium_journey.mjs"),
	}
}
