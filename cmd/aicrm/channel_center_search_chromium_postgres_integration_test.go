package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID: not involved. This covers the authenticated channel list's local text
// filter and does not resolve or mutate an external identity. Persistence and
// External Effects: the fixture creates two ordinary inactive Channel records
// through the existing owner HTTP command; the browser only performs reads.
func TestPostgreSQLChannelCenterCommittedSearchChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newGroupOpsChromiumFixture(t)
	screenshots := channelCenterScreenshotDirectory(t)
	session, csrf := adminAccessLogin(t, fixture.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")
	for index, channel := range []struct{ name, code string }{
		{name: "中文渠道 输入法验证", code: "ime-channel-cn"},
		{name: "English control channel", code: "ime-channel-en"},
	} {
		seedChannelCenterSearchChannel(t, fixture.application.handler, session, csrf, fixture.ownerStaffID, channel.name, channel.code, index+1)
	}

	runChannelCenterChromiumJourney(t, fixture.ctx, fixture.server.URL, screenshots, false)
}

// The browser has no fixture-side DOM injection here: the separate composed
// PostgreSQL application returns an authorized, valid empty channel directory.
func TestPostgreSQLChannelCenterEmptyDirectoryChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1 to run the required Chromium journey")
	}
	fixture := newGroupOpsChromiumFixture(t)
	runChannelCenterChromiumJourney(t, fixture.ctx, fixture.server.URL, channelCenterScreenshotDirectory(t), true)
}

func channelCenterScreenshotDirectory(t *testing.T) string {
	t.Helper()
	screenshots := t.TempDir()
	if configured := platformconfig.ChannelCenterScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_CHANNEL_CENTER_SCREENSHOT_DIR must be absolute")
		}
		if err := os.MkdirAll(configured, 0o700); err != nil {
			t.Fatalf("create Channel Center screenshot directory: %v", err)
		}
		screenshots = configured
	}
	return screenshots
}

func runChannelCenterChromiumJourney(t *testing.T, ctx context.Context, serverURL, screenshots string, expectEmpty bool) {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Channel Center Chromium journey")
	}
	command := exec.CommandContext(ctx, "node", filepath.Join(filepath.Dir(source), "channel_center_search_chromium_journey.mjs"))
	command.Env = append(os.Environ(),
		"AICRM_CHANNEL_CENTER_TEST_URL="+serverURL,
		"AICRM_CHANNEL_CENTER_TEST_USERNAME=groupops-browser-owner",
		"AICRM_CHANNEL_CENTER_TEST_PASSWORD=groupops-browser-owner-password",
		"AICRM_CHANNEL_CENTER_SCREENSHOT_DIR="+screenshots,
	)
	if expectEmpty {
		command.Env = append(command.Env, "AICRM_CHANNEL_CENTER_EXPECT_EMPTY=1")
	}
	output, err := command.CombinedOutput()
	if strings.Contains(string(output), "channel_center_search_chromium: SKIP_DEVTOOLS") {
		t.Fatalf("Channel Center Chromium DevTools unexpectedly unavailable: %s", strings.TrimSpace(string(output)))
	}
	if err != nil || !strings.Contains(string(output), "channel_center_search_chromium: PASS") {
		t.Fatalf("Channel Center Chromium journey err=%v output=%s", err, strings.TrimSpace(string(output)))
	}
}

func seedChannelCenterSearchChannel(t *testing.T, handler http.Handler, session, csrf string, ownerID int64, name, code string, sequence int) {
	t.Helper()
	payload := map[string]any{
		"channel_type": "qrcode", "carrier_type": "qrcode", "channel_name": name, "channel_code": code,
		"scene_value": "", "qr_url": "", "status": "inactive", "owner_staff_id": strconv.FormatInt(ownerID, 10),
		"customer_channel": "", "link_url": "", "final_url": "", "welcome_message": "",
		"welcome_image_library_ids": []int64{}, "welcome_miniprogram_library_ids": []int64{}, "welcome_attachment_library_ids": []int64{}, "welcome_group_invite_library_ids": []int64{},
		"auto_accept_friend": false, "entry_tag_id": "", "entry_tag_name": "", "entry_tag_group_name": "",
		"assignment_mode": "single_owner", "assignment_strategy": "ratio", "overflow_policy": "", "assignment_config_json": map[string]any{},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/channels", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-CSRF-Token", csrf)
	request.Header.Set("Idempotency-Key", "channel-center-ime-fixture-"+strconv.Itoa(sequence))
	request.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
	request.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("seed channel %q status=%d body=%s", name, response.Code, response.Body.String())
	}
}
