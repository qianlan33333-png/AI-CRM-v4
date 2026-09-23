package main

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	wecomadapter "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/adapter"
)

// TestPostgreSQLSurveyCompletionChromiumJourney exercises the real Access
// login, frozen questionnaire operations page, V3 Host selector, PostgreSQL
// save/reload and synthetic completion effect. The receiver is a local TLS
// server trusted only by the test-injected client; production composition
// retains its default transport and cannot use this certificate.
func TestPostgreSQLSurveyCompletionChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the Chromium journey")
	}
	fixture := newSurveyCompletionChromiumFixture(t)
	command := exec.CommandContext(fixture.ctx, "node", filepath.Join(filepath.Dir(fixture.script), "survey_completion_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_SURVEY_BROWSER_URL="+fixture.server.URL, "AICRM_SURVEY_BROWSER_USERNAME=survey-browser-owner", "AICRM_SURVEY_BROWSER_PASSWORD=survey-browser-owner-password", "AICRM_SURVEY_BROWSER_QUESTIONNAIRE_ID="+strconv.FormatInt(fixture.questionnaireID, 10), "AICRM_SURVEY_BROWSER_WEBHOOK="+fixture.receiverURL)
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "survey_completion_chromium: SKIP_DEVTOOLS") && runtime.GOOS == "darwin" {
		t.Skip("local Chromium DevTools is unavailable; Linux CI runs the required journey")
	}
	if err != nil || !strings.Contains(string(output), "survey_completion_chromium: PASS") {
		t.Fatalf("survey Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	deadline := time.Now().Add(12 * time.Second)
	for fixture.receiverCalls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if fixture.receiverCalls.Load() != 1 {
		t.Fatalf("controlled receiver calls=%d", fixture.receiverCalls.Load())
	}
	fixture.receiverMu.Lock()
	receiverFailure := fixture.receiverFailure
	fixture.receiverMu.Unlock()
	if receiverFailure != "" {
		t.Fatalf("controlled receiver protocol=%s", receiverFailure)
	}
	var ref string
	wantRef := "survey-endpoint:" + strconv.FormatInt(fixture.questionnaireID, 10)
	if err = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT external_push_configuration_ref FROM survey_operation_configurations WHERE questionnaire_id=$1`, fixture.questionnaireID).Scan(&ref); err != nil || ref != wantRef {
		t.Fatalf("saved target ref=%q err=%v", ref, err)
	}
	var status string
	var attempted, realCall, resultReceived bool
	terminal := false
	var receiptErr error
	for time.Now().Before(deadline) {
		receiptErr = fixture.application.pool.Native().QueryRow(fixture.ctx, `SELECT status,provider_call_attempted,provider_real_call_executed,provider_result_received FROM survey_external_operation_receipts WHERE questionnaire_id=$1 ORDER BY created_at DESC LIMIT 1`, fixture.questionnaireID).Scan(&status, &attempted, &realCall, &resultReceived)
		if receiptErr == nil && status == "executed" && attempted && realCall && resultReceived {
			terminal = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !terminal || fixture.receiverCalls.Load() != 1 {
		t.Fatalf("effect receipt terminal=%t calls=%d status=%q attempted=%t real=%t received=%t err=%v", terminal, fixture.receiverCalls.Load(), status, attempted, realCall, resultReceived, receiptErr)
	}
	t.Log("survey Chromium journey: PASS")
}

type surveyCompletionChromiumFixture struct {
	ctx             context.Context
	application     *composedApplication
	server          *httptest.Server
	questionnaireID int64
	receiverCalls   atomic.Int64
	receiverMu      sync.Mutex
	receiverFailure string
	receiverURL     string
	script          string
}

func newSurveyCompletionChromiumFixture(t *testing.T) *surveyCompletionChromiumFixture {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)
	_, source, _, _ := runtime.Caller(0)
	root := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(root)
	prepareProductExternalPushChromiumArtifacts(t, root)
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	var key [32]byte
	if _, err := rand.Read(key[:]); err != nil {
		t.Fatal(err)
	}
	fixture := &surveyCompletionChromiumFixture{ctx: ctx, script: source}
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, readErr := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		failure := ""
		if readErr != nil || r.Method != http.MethodPost || r.Header.Get("X-AICRM-Timestamp") == "" || r.Header.Get("X-AICRM-Event-Id") == "" {
			failure = "request"
		}
		mac := hmac.New(sha256.New, key[:])
		_, _ = mac.Write([]byte(r.Header.Get("X-AICRM-Timestamp") + "\n" + r.Header.Get("X-AICRM-Event-Id") + "\n"))
		_, _ = mac.Write(raw)
		if failure == "" && r.Header.Get("X-AICRM-Signature") != "sha256="+hex.EncodeToString(mac.Sum(nil)) {
			failure = "signature"
		}
		var payload struct {
			UserID      string `json:"user_id"`
			Type        string `json:"type"`
			Day         int64  `json:"day"`
			Frequency   int64  `json:"frequency"`
			ExpiresAtTS int64  `json:"expires_at_ts"`
			Remark      string `json:"remark"`
			Campaign    string `json:"campaign"`
		}
		if failure == "" && (json.Unmarshal(raw, &payload) != nil || payload.UserID != "questionnaire_test" || payload.Type != "subscription" || payload.Day != 45 || payload.Frequency != 2 || payload.ExpiresAtTS != 2147483000 || payload.Remark != "browser parity" || payload.Campaign != "survey-browser") {
			failure = "payload"
		}
		fixture.receiverMu.Lock()
		fixture.receiverFailure = failure
		fixture.receiverMu.Unlock()
		if failure != "" {
			http.Error(w, failure, http.StatusBadRequest)
			return
		}
		fixture.receiverCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(receiver.Close)
	receiverEndpoint, receiverNetwork := surveyCompletionTLSEndpoint(t, receiver, "example.com")
	fixture.receiverURL = receiverEndpoint
	targets, err := json.Marshal(map[string]any{"survey.browser.target": map[string]any{"endpoint": receiverEndpoint, "signing_key": base64.RawStdEncoding.EncodeToString(key[:]), "client_id": "survey-browser", "version": "v1", "identity_kind": "unionid", "identity_scope": "wechat-open-platform:browser", "day": 30, "frequency": 1, "expires_at_ts": 2147483647}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	origin := "https://" + server.Listener.Addr().String()
	application, err := composeWithWeComClientFactoryAndSurveyCompletionHTTPClient(ctx, platformconfig.Runtime{Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: "survey-completion-chromium", WorkerOwner: "survey-completion-chromium", WorkerLimit: 1, GroupOps: platformconfig.GroupOps{WebhookSecret: "survey-browser-webhook-secret"}, Survey: platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(key[:]), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key[:]), CompletionProviderEnabled: true, CompletionTargetsJSON: string(targets)}, Effects: platformconfig.Effects{ProviderEnabled: true}, Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "survey-browser-owner", Password: "survey-browser-owner-password", DisplayName: "Survey Browser Owner"}}, wecomadapter.New, receiver.Client(), receiverNetwork)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "survey-browser-owner", Password: "survey-browser-owner-password", DisplayName: "Survey Browser Owner"}); err != nil {
		t.Fatal(err)
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	t.Cleanup(server.Close)
	workerCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- application.effectsRuntime.Run(workerCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("worker: %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("worker did not stop")
		}
	})
	session, csrf := adminAccessLogin(t, application.handler, "survey-browser-owner", "survey-browser-owner-password")
	body := `{"name":"Survey browser","title":"Survey browser","description":"","answer_display_mode":"all_in_one","assessment_enabled":false,"assessment_config":{},"slug":"survey-browser","questions":[{"type":"single_choice","title":"Ready?","required":true,"sort_order":0,"validation":{"max_selections":1},"options":[{"option_text":"Yes","score":0,"tag_codes":[],"is_other":false,"sort_order":0},{"option_text":"No","score":0,"tag_codes":[],"is_other":false,"sort_order":1}]}],"score_rules":[]}`
	req := httptest.NewRequest(http.MethodPost, "/api/admin/questionnaires", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", "survey-browser-create-0001")
	req.Header.Set("X-CSRF-Token", csrf)
	req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	application.handler.ServeHTTP(response, req)
	if response.Code != http.StatusOK {
		t.Fatalf("create questionnaire status=%d body=%s", response.Code, response.Body.String())
	}
	var created struct {
		Questionnaire struct {
			ID int64 `json:"id"`
		} `json:"questionnaire"`
	}
	if err = json.Unmarshal(response.Body.Bytes(), &created); err != nil || created.Questionnaire.ID < 1 {
		t.Fatalf("create response id=%d err=%v", created.Questionnaire.ID, err)
	}
	fixture.application, fixture.server, fixture.questionnaireID = application, server, created.Questionnaire.ID
	return fixture
}
