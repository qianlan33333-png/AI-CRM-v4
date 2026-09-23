package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	accessdomain "github.com/qianlan33333-png/AI-CRM-v3/internal/access/domain"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

// OneID decision: not involved. This is Access-owned employee governance; it
// reads an already verified enterprise directory and never resolves customers.
// Persistence decision: real PostgreSQL Access transactions. Provider reads
// use the composed WeCom adapter; no Provider write or external effect occurs.
func TestPostgreSQLAccessGovernanceUIChromiumJourney(t *testing.T) {
	if !platformconfig.ChromiumJourneyRequired() {
		t.Skip("set AICRM_REQUIRE_CHROMIUM_JOURNEY=1")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Access UI browser journey")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
	t.Chdir(repository)
	prepareProductExternalPushChromiumArtifacts(t, repository)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	defer cleanup()
	provider := httptest.NewServer(accessGovernanceDirectoryFixture())
	defer provider.Close()
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	defer server.Close()
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: "https://" + server.Listener.Addr().String(), ReleaseSHA: "access-governance-ui", WorkerOwner: "access-governance-ui", WorkerLimit: 1,
		WeCom:     platformconfig.WeCom{Enabled: true, CorpID: "access-ui-corp", AgentID: "access-ui-agent", Secret: "access-ui-secret", ContactSecret: "access-ui-contact", APIBase: provider.URL, HTTPClient: provider.Client(), ContextSigningKey: strings.Repeat("x", 32)},
		GroupOps:  platformconfig.GroupOps{WebhookSecret: "access-governance-ui-webhook"},
		Survey:    platformconfig.Survey{DataKey: base64.RawStdEncoding.EncodeToString(dataKey), IdentityPhoneDataKey: base64.RawStdEncoding.EncodeToString(dataKey)},
		Bootstrap: platformconfig.Bootstrap{Enabled: true, Username: "access-ui-super", Password: "access-ui-super-password", DisplayName: "超级管理员甲"},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()
	if err = application.bootstrap(ctx, platformconfig.Bootstrap{Enabled: true, Username: "access-ui-super", Password: "access-ui-super-password", DisplayName: "超级管理员甲"}); err != nil {
		t.Fatal(err)
	}
	super := accessdomain.Principal{Kind: accessdomain.KindAdmin, InternalID: 1, Roles: []accessdomain.Role{accessdomain.RoleSuperAdmin}}
	if err = application.management.BindWeComUserID(ctx, super, 1, "SuperFixtureID"); err != nil {
		t.Fatal(err)
	}
	// Binding fences the bootstrap account once; the governed fixture uses the
	// matching fresh principal for the two production provisioning commands.
	super.SessionVersion = 2
	type governedSeed struct {
		wecomUserID string
		role        accessdomain.Role
		password    string
	}
	seeded := make([]accessdomain.User, 0, 2)
	for _, input := range []governedSeed{
		{wecomUserID: "AdminFixtureID", role: accessdomain.RoleAdmin, password: "access-ui-admin-password"},
		{wecomUserID: "ViewerFixtureID", role: accessdomain.RoleViewer, password: "access-ui-viewer-password"},
	} {
		user, seedErr := application.management.ProvisionEnterpriseEmployee(ctx, super, accessapp.ProvisionEnterpriseEmployeeInput{WeComUserID: input.wecomUserID, Role: input.role, IdempotencyKey: "seed-" + input.wecomUserID})
		if seedErr != nil {
			t.Fatal(seedErr)
		}
		if seedErr = application.management.ResetGovernancePassword(ctx, super, accessapp.ResetPasswordInput{TargetID: user.ID, Password: input.password, IdempotencyKey: "seed-password-" + input.wecomUserID}); seedErr != nil {
			t.Fatal(seedErr)
		}
		seeded = append(seeded, user)
	}
	if len(seeded) != 2 {
		t.Fatal("governed browser seed is incomplete")
	}
	if seeded[0].Username == "" || seeded[1].Username == "" {
		t.Fatal("governed browser seed must keep generated local credentials")
	}
	server.Config.Handler = application.handler
	server.StartTLS()
	screenshots := t.TempDir()
	if configured := platformconfig.AccessGovernanceScreenshotDirectory(); configured != "" {
		if !filepath.IsAbs(configured) {
			t.Fatal("AICRM_ACCESS_UI_SCREENSHOT_DIR must be absolute")
		}
		if err = os.MkdirAll(configured, 0o700); err != nil {
			t.Fatal(err)
		}
		screenshots = configured
	}
	script := filepath.Join(repository, "cmd", "aicrm", "access_governance_ui_chromium_journey.mjs")
	command := exec.CommandContext(ctx, "node", script)
	command.Env = append(os.Environ(),
		"AICRM_ACCESS_UI_TEST_URL="+server.URL,
		"AICRM_ACCESS_UI_SUPER_USERNAME=access-ui-super", "AICRM_ACCESS_UI_SUPER_PASSWORD=access-ui-super-password",
		"AICRM_ACCESS_UI_ADMIN_USERNAME="+seeded[0].Username, "AICRM_ACCESS_UI_ADMIN_PASSWORD=access-ui-admin-password",
		"AICRM_ACCESS_UI_VIEWER_USERNAME="+seeded[1].Username, "AICRM_ACCESS_UI_VIEWER_PASSWORD=access-ui-viewer-password",
		"AICRM_ACCESS_UI_SCREENSHOT_DIR="+screenshots,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Access UI Chromium journey: %v output=%s", err, strings.TrimSpace(string(output)))
	}
	if !strings.Contains(string(output), "access_governance_ui_chromium: PASS") {
		t.Fatalf("Access UI Chromium journey did not report success: %q", output)
	}
	for _, name := range []string{"access-governance-1440.png", "access-governance-1280.png", "access-governance-780.png", "access-governance-390.png", "access-governance-refresh-420.png", "access-governance-admin-drawer.png", "access-governance-transfer-confirm.png", "access-governance-transfer-complete.png"} {
		info, statErr := os.Stat(filepath.Join(screenshots, name))
		if statErr != nil || info.Size() < 512 {
			t.Fatalf("Access UI screenshot=%s exists=%t", name, statErr == nil)
		}
	}
}

func accessGovernanceDirectoryFixture() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/cgi-bin/gettoken":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "access_token": "fixture", "expires_in": 7200})
		case "/cgi-bin/agent/get":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"errcode":         0,
				"allow_userinfos": map[string]any{"user": []string{}},
				"allow_partys":    map[string]any{"partyid": []int{1}},
				"allow_tags":      nil,
			})
		case "/cgi-bin/department/simplelist":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "department_id": []map[string]int{{"id": 1, "parentid": 0}}})
		case "/cgi-bin/user/simplelist":
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userlist": []map[string]string{{"userid": "SuperFixtureID", "name": "超级管理员甲"}, {"userid": "AdminFixtureID", "name": "管理员甲"}, {"userid": "ViewerFixtureID", "name": "只读乙"}, {"userid": "CandidateCaseID", "name": "同名候选"}, {"userid": "SecondCandidateID", "name": "第二候选"}}})
		case "/cgi-bin/user/get":
			if request.URL.Query().Get("userid") == "UnavailableID" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 48002, "errmsg": "api forbidden"})
				return
			}
			if request.URL.Query().Get("userid") == "SuperFixtureID" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "SuperFixtureID", "name": "超级管理员甲"})
				return
			}
			if request.URL.Query().Get("userid") == "AdminFixtureID" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "AdminFixtureID", "name": "管理员甲"})
				return
			}
			if request.URL.Query().Get("userid") == "ViewerFixtureID" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "ViewerFixtureID", "name": "只读乙"})
				return
			}
			if request.URL.Query().Get("userid") == "SecondCandidateID" {
				_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "SecondCandidateID", "name": "第二候选"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"errcode": 0, "userid": "CandidateCaseID", "name": "同名候选"})
		default:
			http.NotFound(w, request)
		}
	})
}
