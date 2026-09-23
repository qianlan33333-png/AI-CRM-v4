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
	"sync/atomic"
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID: not involved. This test reads and creates a disposable questionnaire
// through the authenticated admin owner only. Persistence/external effects:
// PostgreSQL fixture state only; Provider and effects are disabled.
func TestPostgreSQLQuestionnaireListReadStateChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newQuestionnaireListReadStateChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "questionnaire_list_read_state_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_QUESTIONNAIRE_LIST_TEST_URL="+fixture.server.URL,
		"AICRM_QUESTIONNAIRE_LIST_TEST_USERNAME=questionnaire-browser",
		"AICRM_QUESTIONNAIRE_LIST_TEST_PASSWORD=questionnaire-browser-password",
		"AICRM_QUESTIONNAIRE_LIST_SCREENSHOT_DIR="+fixture.screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "questionnaire_list_read_state_chromium: PASS") {
		t.Fatalf("questionnaire list read-state Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	for _, name := range []string{"questionnaire-empty-1280.png", "questionnaire-no-match-1440.png", "questionnaire-retry-1280.png", "questionnaire-forbidden-1440.png"} {
		info, statErr := os.Stat(filepath.Join(fixture.screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("questionnaire read-state screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
}

type questionnaireListReadStateChromiumFixture struct {
	ctx         context.Context
	application *composedApplication
	server      *httptest.Server
	screenshots string
	script      string
	listMode    atomic.Int32
}

const (
	questionnaireListNormal int32 = iota
	questionnaireListUnavailable
	questionnaireListForbidden
)

func newQuestionnaireListReadStateChromiumFixture(t *testing.T) *questionnaireListReadStateChromiumFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	t.Cleanup(cancel)
	_, source, _, ok := goruntime.Caller(0)
	if !ok {
		t.Fatal("locate questionnaire read-state Chromium script")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://" + server.Listener.Addr().String(),
		ReleaseSHA:   "questionnaire-list-read-state-chromium",
		WorkerOwner:  "questionnaire-list-read-state-chromium",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "questionnaire-list-read-state-chromium"},
		Survey:       platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key)},
		Bootstrap:    platformconfig.Bootstrap{Enabled: true, Username: "questionnaire-browser", Password: "questionnaire-browser-password", DisplayName: "Questionnaire Browser"},
		// Effects.ProviderEnabled remains false. This journey never starts a worker
		// or invokes an external completion, OAuth, payment, or WeCom boundary.
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "questionnaire-browser", Password: "questionnaire-browser-password", DisplayName: "Questionnaire Browser"}); err != nil {
		t.Fatal(err)
	}
	screenshots := t.TempDir()
	if configured := platformconfig.QuestionnaireListScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_QUESTIONNAIRE_LIST_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	fixture := &questionnaireListReadStateChromiumFixture{ctx: ctx, application: application, server: server, screenshots: screenshots, script: source}
	fixture.listMode.Store(questionnaireListUnavailable) // prove cold first navigation, before any list mount.
	server.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/__test/questionnaire-list-mode" {
			if request.Header.Get("X-AICRM-Questionnaire-Test-Control") != "1" {
				http.NotFound(writer, request)
				return
			}
			switch request.URL.Query().Get("mode") {
			case "normal":
				fixture.listMode.Store(questionnaireListNormal)
			case "503":
				fixture.listMode.Store(questionnaireListUnavailable)
			case "403":
				fixture.listMode.Store(questionnaireListForbidden)
			default:
				http.Error(writer, "invalid fixture mode", http.StatusBadRequest)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		if request.Method == http.MethodGet && request.URL.Path == "/api/admin/questionnaires" {
			switch fixture.listMode.Load() {
			case questionnaireListUnavailable:
				http.Error(writer, `{"code":"temporary_failure"}`, http.StatusServiceUnavailable)
				return
			case questionnaireListForbidden:
				http.Error(writer, `{"code":"forbidden"}`, http.StatusForbidden)
				return
			}
		}
		application.handler.ServeHTTP(writer, request)
	})
	server.StartTLS()
	return fixture
}
