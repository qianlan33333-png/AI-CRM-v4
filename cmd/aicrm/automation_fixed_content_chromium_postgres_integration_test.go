package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID is not involved: this journey edits one authenticated Automation Agent
// and never reads or resolves a customer identity. Persistence stays inside the
// existing Automation Owner; the page only exercises a fixed-content command
// and readback. No Provider, Outbound intent, upload, or execution is enabled.
func TestPostgreSQLAutomationFixedContentChromiumJourney(t *testing.T) {
	runAutomationContentJourney(t, false)
}

func TestPostgreSQLAutomationLifecycleChromiumJourney(t *testing.T) {
	runAutomationContentJourney(t, true)
}

func runAutomationContentJourney(t *testing.T, lifecycle bool) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate automation fixed-content Chromium journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareAutomationFixedContentArtifacts(t, repository)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	application, err := compose(ctx, platformconfig.Runtime{
		Role:         platformconfig.RoleAPI,
		DatabaseURL:  databaseURL,
		PublicOrigin: "https://" + server.Listener.Addr().String(),
		ReleaseSHA:   "automation-fixed-content-chromium",
		WorkerOwner:  "automation-fixed-content-chromium",
		WorkerLimit:  1,
		GroupOps:     platformconfig.GroupOps{WebhookSecret: "automation-fixed-content-chromium-webhook"},
		Survey: platformconfig.Survey{
			DataKey: base64.RawStdEncoding.EncodeToString(key), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(key),
		},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "automation-browser", Password: "automation-browser-password", DisplayName: "Automation Browser"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	bootstrap := platformconfig.Bootstrap{Enabled: true, Username: "automation-browser", Password: "automation-browser-password", DisplayName: "Automation Browser"}
	if err = application.bootstrap(ctx, bootstrap); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, application.handler, bootstrap.Username, bootstrap.Password)
	imageID := createAutomationFixedContentImage(t, application.handler, session, csrf)
	agentID := createAutomationFixedContentAgent(t, application.handler, session, csrf)

	server.Config.Handler = application.handler
	server.StartTLS()
	screenshots := t.TempDir()
	if configured := platformconfig.AutomationFixedContentScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_AUTOMATION_CONTENT_SCREENSHOT_DIR must be absolute")
		}
		if err = os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "automation_fixed_content_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_AUTOMATION_CONTENT_TEST_URL="+server.URL,
		"AICRM_AUTOMATION_CONTENT_TEST_USERNAME="+bootstrap.Username,
		"AICRM_AUTOMATION_CONTENT_TEST_PASSWORD="+bootstrap.Password,
		"AICRM_AUTOMATION_CONTENT_TEST_AGENT_ID="+strconv.FormatInt(agentID, 10),
		"AICRM_AUTOMATION_CONTENT_TEST_IMAGE_ID="+strconv.FormatInt(imageID, 10),
		"AICRM_AUTOMATION_CONTENT_SCREENSHOT_DIR="+screenshots,
		"AICRM_AUTOMATION_LIFECYCLE_TEST="+strconv.FormatBool(lifecycle),
	)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "automation_fixed_content_chromium: PASS") {
		t.Fatalf("automation fixed-content Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
	for _, name := range []string{"automation-fixed-content-1280.png", "automation-fixed-content-1440.png", "automation-fixed-content-360.png", "automation-fixed-content-420.png"} {
		info, statErr := os.Stat(filepath.Join(screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("automation fixed-content screenshot=%s exists=%t size=%d", name, statErr == nil, func() int64 {
				if info == nil {
					return 0
				}
				return info.Size()
			}())
		}
	}
	readback := automationFixedContentRequest(t, application.handler, http.MethodGet, "/api/admin/automation-agents/"+strconv.FormatInt(agentID, 10), nil, session, "", "")
	if readback.Code != http.StatusOK {
		t.Fatalf("fixed-content browser readback status=%d body=%s", readback.Code, readback.Body.String())
	}
	var payload struct {
		Agent struct {
			DraftVersion        int64          `json:"draft_version"`
			PublishedVersion    int64          `json:"published_version"`
			DraftRolePrompt     string         `json:"draft_role_prompt"`
			DraftTaskPrompt     string         `json:"draft_task_prompt"`
			LegacyConfiguration map[string]any `json:"legacy_configuration"`
			FixedContentPackage struct {
				ContentText string  `json:"content_text"`
				ImageIDs    []int64 `json:"image_library_ids"`
			} `json:"fixed_content_package"`
		} `json:"agent"`
	}
	if err = json.Unmarshal(readback.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	expectedPublished := int64(1)
	if lifecycle {
		expectedPublished = 2
	}
	if payload.Agent.DraftVersion != 2 || payload.Agent.PublishedVersion != expectedPublished || payload.Agent.DraftRolePrompt != "保留角色 Prompt" || payload.Agent.DraftTaskPrompt != "保留任务 Prompt" || payload.Agent.LegacyConfiguration["keep"] != "legacy" || payload.Agent.FixedContentPackage.ContentText != "浏览器确认的中文固定话术" || len(payload.Agent.FixedContentPackage.ImageIDs) != 1 || payload.Agent.FixedContentPackage.ImageIDs[0] != imageID {
		t.Fatalf("fixed-content persisted/readback boundary is wrong: %+v", payload.Agent)
	}
	t.Logf("automation fixed-content Chromium screenshots: %s", screenshots)
}

func prepareAutomationFixedContentArtifacts(t *testing.T, repository string) {
	t.Helper()
	manifest, err := os.ReadFile(filepath.Join(repository, "web", "dist", "asset-manifest.json"))
	if err != nil {
		t.Fatalf("automation fixed-content Chromium requires built assets: %v", err)
	}
	for _, marker := range []string{`"automationContentHost"`, `"automationContentStyles"`, `"presentationStyles"`, `"selectionDialogStyles"`} {
		if !bytes.Contains(manifest, []byte(marker)) {
			t.Fatalf("automation fixed-content manifest lacks %s", marker)
		}
	}
	if _, err = os.Stat(filepath.Join(repository, "web", "dist", "assets", "standard-components", "material_picker.js")); err != nil {
		t.Fatalf("automation fixed-content frozen material bridge is missing: %v", err)
	}
}

func createAutomationFixedContentImage(t *testing.T, handler http.Handler, session, csrf string) int64 {
	t.Helper()
	png := "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII="
	body := fmt.Sprintf(`{"data_url":"data:image/png;base64,%s","file_name":"automation-cover.png","name":"自动化浏览器封面"}`, png)
	response := automationFixedContentRequest(t, handler, http.MethodPost, "/api/admin/image-library", []byte(body), session, csrf, "automation-fixed-content-image")
	if response.Code != http.StatusOK {
		t.Fatalf("seed image status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		ItemID int64 `json:"item_id"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.ItemID < 1 {
		t.Fatalf("seed image decode id=%d err=%v body=%s", payload.ItemID, err, response.Body.String())
	}
	return payload.ItemID
}

func createAutomationFixedContentAgent(t *testing.T, handler http.Handler, session, csrf string) int64 {
	t.Helper()
	body := []byte(`{"agent_name":"浏览器固定话术","agent_code":"automation_fixed_chromium","automation_type":"fixed_script","role_prompt":"保留角色 Prompt","task_prompt":"保留任务 Prompt","legacy_configuration":{"keep":"legacy"}}`)
	response := automationFixedContentRequest(t, handler, http.MethodPost, "/api/admin/automation-agents", body, session, csrf, "automation-fixed-content-create")
	if response.Code != http.StatusOK {
		t.Fatalf("seed agent status=%d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		Agent struct {
			ID int64 `json:"id"`
		} `json:"agent"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil || payload.Agent.ID < 1 {
		t.Fatalf("seed agent decode id=%d err=%v body=%s", payload.Agent.ID, err, response.Body.String())
	}
	return payload.Agent.ID
}

func automationFixedContentRequest(t *testing.T, handler http.Handler, method, target string, body []byte, session, csrf, idempotencyKey string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, target, bytes.NewReader(body))
	if len(body) > 0 {
		request.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	}
	if csrf != "" {
		request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
		request.Header.Set("X-CSRF-Token", csrf)
	}
	if idempotencyKey != "" {
		request.Header.Set("Idempotency-Key", idempotencyKey)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}
