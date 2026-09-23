package main

import (
	"encoding/json"
	accesshttp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/http"
	config "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgreSQLInvitationHostChromiumJourney(t *testing.T) {
	if !config.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	f := newGroupOpsChromiumFixture(t)
	f.application.invitationService.WriteEnabled = true
	_, err := f.application.pool.Native().Exec(f.ctx, `UPDATE group_ops_directory_groups SET display_name='测试群',observed_at=clock_timestamp(),checked_at=clock_timestamp(),sync_state='ready',member_count=0,external_member_count=NULL WHERE chat_reference IN ('chromium-group-1','chromium-group-2')`)
	if err != nil {
		t.Fatal(err)
	}
	// Provider writes remain disabled. This test proves real Host persistence and
	// EER acceptance, and must not be reported as an actual WeChat scan.
	screenshots := config.InvitationScreenshotDirectory()
	if screenshots == "" {
		screenshots = t.TempDir()
	}
	if err = os.MkdirAll(screenshots, 0700); err != nil {
		t.Fatal(err)
	}
	session, csrf := adminAccessLogin(t, f.application.handler, "groupops-browser-owner", "groupops-browser-owner-password")

	for _, threshold := range []string{"null", "0", "201", "1.5"} {
		body := `{"name":"invalid","title":"invalid","mode":"sequence","threshold":` + threshold + `,"chat_ids":["chromium-group-1"],"enabled":true}`
		req := httptest.NewRequest(http.MethodPost, "/api/admin/group-invitations", strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: accesshttp.SessionCookieName, Value: session})
		req.AddCookie(&http.Cookie{Name: accesshttp.CSRFCookieName, Value: csrf})
		req.Header.Set("X-CSRF-Token", csrf)
		req.Header.Set("Idempotency-Key", "invalid-threshold-"+threshold)
		res := httptest.NewRecorder()
		f.application.handler.ServeHTTP(res, req)
		if res.Code != 400 {
			t.Fatalf("threshold %s accepted: %d %s", threshold, res.Code, res.Body.String())
		}
	}
	for _, route := range []string{"/api/admin/group-invitations", "/api/admin/group-directory"} {
		res := httptest.NewRecorder()
		f.application.handler.ServeHTTP(res, httptest.NewRequest(http.MethodGet, route, nil))
		if res.Code != 401 {
			t.Fatalf("unauthenticated %s status %d", route, res.Code)
		}
	}
	page := authenticatedAdminGet(t, f.application.handler, session, "/admin/group-invitations")
	if page.Code != http.StatusOK {
		t.Fatal(page.Code)
	}
	if err = os.WriteFile(filepath.Join(screenshots, "index.html"), page.Body.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	catalog, err := f.application.catalogService.ListCatalog(f.ctx, "测试群", 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(catalog)
	if err = os.WriteFile(filepath.Join(screenshots, "directory.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(f.ctx, "node", filepath.Join("cmd", "aicrm", "invitation_chromium_journey.mjs"))
	command.Env = append(os.Environ(), "AICRM_INVITATION_TEST_URL="+f.server.URL, "AICRM_INVITATION_SCREENSHOTS="+screenshots)
	output, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "invitation_chromium: PASS") {
		t.Fatalf("journey: %v %s", err, output)
	}
	var id int64
	if err = f.application.pool.Native().QueryRow(f.ctx, `SELECT id FROM media_group_invites WHERE name='浏览器邀请计划'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	plan, err := f.application.invitationService.Store.ReadInvitationPlan(f.ctx, id)
	if err != nil || plan.Threshold == nil || *plan.Threshold != 1 || len(plan.Bindings) != 2 {
		t.Fatalf("saved plan: %+v %v", plan, err)
	}
	// Complete the test fixtures locally: no Provider call is implied.
	if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE media_invitation_join_ways SET state='executed',config_id='test-config',qr_code='https://example.test/code' WHERE invite_id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = f.application.invitationService.Refresh(f.ctx); err != nil {
		t.Fatal(err)
	}
	plan, err = f.application.invitationService.Public(f.ctx, plan.Token)
	if err != nil || plan.CurrentChatID != "chromium-group-1" {
		t.Fatalf("active: %+v %v", plan, err)
	}
	if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE group_ops_directory_groups SET member_count=1 WHERE chat_reference='chromium-group-1'`); err != nil {
		t.Fatal(err)
	}
	plan, err = f.application.invitationService.Public(f.ctx, plan.Token)
	if err != nil || plan.State != "preparing" || plan.CurrentChatID != "" || plan.ProviderQRCode != "https://example.test/code" {
		t.Fatalf("rotation must wait for Provider confirmation: %+v %v", plan, err)
	}
	// Complete the queued virtual update using the same config and QR, then
	// confirm that the public projection advances to the second group.
	if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE media_invitation_join_ways SET state='executed' WHERE invite_id=$1 AND config_id='test-config' AND qr_code='https://example.test/code'`, id); err != nil {
		t.Fatal(err)
	}
	plan, err = f.application.invitationService.Public(f.ctx, plan.Token)
	if err != nil || plan.CurrentChatID != "chromium-group-2" || plan.ProviderQRCode != "https://example.test/code" {
		t.Fatalf("confirmed rotation: %+v %v", plan, err)
	}
	if _, err = f.application.pool.Native().Exec(f.ctx, `UPDATE group_ops_directory_groups SET member_count=0 WHERE chat_reference='chromium-group-1'`); err != nil {
		t.Fatal(err)
	}
	plan, err = f.application.invitationService.Public(f.ctx, plan.Token)
	if err != nil || plan.CurrentChatID != "chromium-group-2" {
		t.Fatalf("no return: %+v %v", plan, err)
	}
	response, err := f.server.Client().Get(f.server.URL + "/gi/" + plan.Token + "?format=json")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatal(response.Status)
	}
}
