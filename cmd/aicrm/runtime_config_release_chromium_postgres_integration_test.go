package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// TestPostgreSQLRuntimeReleaseChromiumJourney proves the complete production
// route: Access owns the browser login/session/CSRF exchange, the Config UI
// binding renders the embedded admin shell and Host, and the Host calls the
// real PostgreSQL-backed Config release handler. It deliberately uses an
// isolated Chromium profile and never substitutes a browser DOM or request
// security fixture.
//
// OneID decision: not involved; this is an internal-admin policy release.
// Persistence decision: local PostgreSQL transactions only. No Provider is
// configured or called by this journey.
func TestPostgreSQLRuntimeReleaseChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()

	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	origin := "https://" + server.Listener.Addr().String()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: origin,
		ReleaseSHA:   "runtime-config-chromium-journey",
		WorkerOwner:  "runtime-config-chromium-journey",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "runtime-config-chromium-journey-webhook-secret"},
		AutomationOperations: platformconfig.AutomationOperations{
			ProviderMode: platformconfig.AutomationProviderDisabled, MaxRecipientsPerRun: 1,
		},
		WeCom: platformconfig.WeCom{
			AgentID: "agent-preserved", ContextTokenTTL: time.Minute,
			MessageArchivePageLimit: 1, MessageArchivePageBudget: 1,
		},
		Survey: platformconfig.Survey{
			DataKey:              base64.RawStdEncoding.EncodeToString(dataKey),
			IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey),
		},
		Bootstrap: platformconfig.Bootstrap{
			Enabled: true, Username: "runtime-browser-owner", Password: "runtime-browser-owner-password", DisplayName: "Runtime Browser Owner",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{
		Enabled: true, Username: "runtime-browser-owner", Password: "runtime-browser-owner-password", DisplayName: "Runtime Browser Owner",
	}); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = application.handler
	server.StartTLS()

	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate Chromium runtime release journey")
	}
	script := filepath.Join(filepath.Dir(source), "..", "..", "internal", "webshell", "runtime_config_releases_chromium.test.mjs")
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_RUNTIME_RELEASE_TEST_URL="+server.URL,
		"AICRM_RUNTIME_RELEASE_TEST_USERNAME=runtime-browser-owner",
		"AICRM_RUNTIME_RELEASE_TEST_PASSWORD=runtime-browser-owner-password",
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("runtime release Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "runtime_config_releases_chromium: PASS") {
		t.Fatalf("runtime release Chromium journey did not report success: %q", output)
	}
	var releases, published, superseded, usage int
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*),count(*) FILTER (WHERE state='published'),count(*) FILTER (WHERE state='superseded') FROM config_runtime_releases`).Scan(&releases, &published, &superseded); err != nil {
		t.Fatal(err)
	}
	if err = application.pool.Native().QueryRow(ctx, `SELECT count(*) FROM config_runtime_usage`).Scan(&usage); err != nil {
		t.Fatal(err)
	}
	if releases != 4 || published != 1 || superseded != 2 || usage != 0 {
		t.Fatalf("Chromium release facts releases/published/superseded/usage=%d/%d/%d/%d", releases, published, superseded, usage)
	}
}
