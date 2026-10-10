package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	accessapp "github.com/qianlan33333-png/AI-CRM-v3/internal/access/app"
	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
	testconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config/tests"
	"github.com/qianlan33333-png/AI-CRM-v3/internal/wecom"
)

// Source composition seeds only a disposable scoped customer and staff session.
// All HTTP assertions run through the immutable installed API and assets.
// Reader/generator remain disabled; no real credentials or worker are used.
func TestPostgreSQLSidebarAIOperationsInstalled(t *testing.T) {
	input := testconfig.InstalledProduct()
	if input.Binary == "" {
		t.Skip("explicit installed package inputs required")
	}
	content, err := os.ReadFile(input.Binary)
	digest := sha256.Sum256(content)
	if err != nil || !filepath.IsAbs(input.Binary) || !filepath.IsAbs(input.Root) || !validDomesticReleaseSHA(input.SHA) || hex.EncodeToString(digest[:]) != input.BinarySHA256 {
		t.Fatal("installed sidebar binding mismatch")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	databaseURL, cleanup := adminAccessCompositionDatabase(t, ctx)
	t.Cleanup(cleanup)
	db, err := url.Parse(databaseURL)
	if err != nil || (db.Hostname() != "localhost" && db.Hostname() != "127.0.0.1" && db.Hostname() != "::1") || !strings.HasPrefix(strings.TrimPrefix(db.Path, "/"), "aicrm_test_") {
		t.Fatal("installed sidebar requires isolated loopback PostgreSQL")
	}
	server := httptest.NewUnstartedServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	origin := "https://" + server.Listener.Addr().String()
	dataKey := base64.RawStdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	bootstrap := platformconfig.Bootstrap{Enabled: true, Username: "sidebar-browser-owner", Password: "sidebar-browser-owner-password", DisplayName: "Sidebar Browser Owner"}
	application, err := compose(ctx, platformconfig.Runtime{
		Role: platformconfig.RoleAPI, DatabaseURL: databaseURL, PublicOrigin: origin, ReleaseSHA: input.SHA,
		GroupOps: platformconfig.GroupOps{WebhookSecret: "sidebar-installed-fixture-webhook-secret"},
		Survey:   platformconfig.Survey{DataKey: dataKey, IdentityPhoneDataKey: dataKey}, Bootstrap: bootstrap,
		WeCom: platformconfig.WeCom{Enabled: true, CorpID: "fixture-corp", AgentID: "fixture-agent", Secret: "fixture-secret", ContextSigningKey: "sidebar-thumbnail-context-key-32"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(application.Close)
	if err = application.bootstrap(ctx, bootstrap); err != nil {
		t.Fatal(err)
	}
	if err = seedSidebarThumbnailChromiumJourney(ctx, application); err != nil {
		t.Fatal(err)
	}
	issued, err := application.authentication.LoginWithWeComUserID(ctx, accessapp.WeComLoginCommand{WeComUserID: "fixture-staff", Remote: "installed-sidebar-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	_ = listener.Close()
	process := startPeriodicContactInstalledProcess(t, input.Binary, input.Root, []string{
		"AICRM_DATABASE_URL=" + databaseURL, "AICRM_ROLE=api", "AICRM_LISTEN_ADDR=" + address, "AICRM_RELEASE_SHA=" + input.SHA,
		"AICRM_PUBLIC_ORIGIN=" + origin, "AICRM_H5_PUBLIC_ORIGIN=" + origin,
		"AICRM_SURVEY_DATA_KEY=" + dataKey, "AICRM_IDENTITY_PHONE_DATA_KEY=" + dataKey,
		"AICRM_GROUP_OPS_WEBHOOK_SECRET=sidebar-installed-fixture-webhook-secret",
		"AICRM_WECOM_ENABLED=true", "AICRM_WECOM_CORP_ID=fixture-corp", "AICRM_WECOM_AGENT_ID=fixture-agent",
		"AICRM_WECOM_SECRET=fixture-secret", "AICRM_WECOM_CONTEXT_SIGNING_KEY=sidebar-thumbnail-context-key-32",
		"AICRM_SIDEBAR_WAREHOUSE_ENABLED=false", "AICRM_SIDEBAR_GENERATION_ENABLED=false",
		"AICRM_EFFECTS_PROVIDER_ENABLED=false", "AICRM_OUTBOUND_PROVIDER_ENABLED=false", "AICRM_GROUP_OPS_PROVIDER_ENABLED=false",
		"AICRM_WECHAT_PAY_PROVIDER_ENABLED=false", "AICRM_ALIPAY_PROVIDER_ENABLED=false",
		"HTTP_PROXY=http://127.0.0.1:9", "HTTPS_PROXY=http://127.0.0.1:9", "NO_PROXY=127.0.0.1,localhost,::1",
	})
	endpoint := "http://" + address
	waitInstalledSurveyReady(t, process, endpoint, input.SHA)
	target, _ := url.Parse(endpoint)
	proxy := httputil.NewSingleHostReverseProxy(target)
	transport := &http.Transport{Proxy: nil}
	proxy.Transport = transport
	t.Cleanup(transport.CloseIdleConnections)
	server.Config.Handler = proxy
	server.StartTLS()
	call := func(method, path, body, token string, session bool) (int, map[string]any) {
		t.Helper()
		r, _ := http.NewRequestWithContext(ctx, method, server.URL+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("X-Sidebar-Context-Token", token)
		if session {
			r.AddCookie(&http.Cookie{Name: "aicrm_sidebar_session", Value: issued.SessionToken})
		}
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		var result map[string]any
		if err = json.NewDecoder(io.LimitReader(response.Body, 32768)).Decode(&result); err != nil {
			t.Fatal("installed sidebar response is not JSON")
		}
		if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
			t.Fatal("installed sidebar response lacks no-store")
		}
		return response.StatusCode, result
	}
	if code, _ := call("GET", "/api/sidebar/v2/ai-operations", "", "", false); code != 401 {
		t.Fatalf("anonymous AI read status=%d", code)
	}
	if code, _ := call("POST", "/api/sidebar/v2/ai-operations/suggestions", `{}`, "", false); code != 401 {
		t.Fatalf("anonymous suggestion status=%d", code)
	}
	code, bound := call("POST", "/api/sidebar/v2/bootstrap", `{"external_userid":"sidebar-thumbnail-external"}`, "", true)
	token, _ := bound["context_token"].(string)
	if code != 200 || token == "" || bound["state"] != "ready" {
		t.Fatalf("installed staff bootstrap status=%d state=%v token_present=%t", code, bound["state"], token != "")
	}
	code, body := call("GET", "/api/sidebar/v2/ai-operations", "", token, true)
	if code != 200 || body["state"] != "unconfigured" || body["generation_ready"] != false || body["synced_at"] != nil || body["snapshot_token"] != nil || body["data"] != nil {
		t.Fatal("disabled installed AI read leaked data or enabled generation")
	}
	for _, check := range []struct {
		method, path, body, token string
		session                   bool
		want                      int
	}{
		{"GET", "/api/sidebar/v2/ai-operations", "", token, false, 401},
		{"GET", "/api/sidebar/v2/ai-operations?customer_id=2", "", token, true, 400},
		{"POST", "/api/sidebar/v2/ai-operations/suggestions", `{"confirmed":false}`, token, true, 400},
		{"POST", "/api/sidebar/v2/ai-operations/suggestions", `{"confirmed":true,"snapshot_token":"forged"}`, token, true, 409},
		{"POST", "/api/sidebar/v2/ai-operations/suggestions", `{"confirmed":true,"messages":[]}`, token, true, 400},
	} {
		if code, _ := call(check.method, check.path, check.body, check.token, check.session); code != check.want {
			t.Fatalf("installed sidebar refusal %s status=%d want=%d", check.path, code, check.want)
		}
	}
	otherToken, err := (wecom.ContextTokenService{CorpID: "fixture-corp", SigningKey: []byte("sidebar-thumbnail-context-key-32")}).Issue(ctx, wecom.SidebarPrincipal{CorpID: "fixture-corp", EmployeeID: "other-staff"}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if code, _ := call("GET", "/api/sidebar/v2/ai-operations", "", otherToken, true); code != 401 {
		t.Fatalf("cross employee status=%d", code)
	}
	if _, err = application.pool.Native().Exec(ctx, `UPDATE admin_users SET is_active=false WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if code, _ := call("GET", "/api/sidebar/v2/ai-operations", "", token, true); code != 401 {
		t.Fatalf("revoked staff status=%d", code)
	}
	var effects, intents int
	if err = application.pool.Native().QueryRow(ctx, `SELECT (SELECT count(*) FROM external_effects),(SELECT count(*) FROM outbound_sidebar_send_intents)`).Scan(&effects, &intents); err != nil {
		t.Fatal(err)
	}
	if effects != 0 || intents != 0 {
		t.Fatal("AI disabled/refusal paths created an effect")
	}
	t.Logf("sidebar-ai-operations-installed PASS source_sha=%s binary_sha256=%s scoped_read=unconfigured refusals=passed effects=0 workers=0 real_provider=false", input.SHA, input.BinarySHA256)
}
