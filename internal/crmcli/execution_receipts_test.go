package crmcli

import (
	"bytes"
	"github.com/zalando/go-keyring"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIExecutionReceiptsReadJourney(t *testing.T) {
	keyring.MockInit()
	reads := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/admin/access/cli/login-challenge" {
			http.SetCookie(w, &http.Cookie{Name: "aicrm_login_csrf", Value: "nonce", Secure: true, Path: "/"})
			io.WriteString(w, `{"login_csrf_token":"nonce"}`)
			return
		}
		if r.URL.Path == "/login" {
			http.SetCookie(w, &http.Cookie{Name: "aicrm_admin_session", Value: "PRIVATE_SESSION", Secure: true, Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "aicrm_admin_csrf", Value: "PRIVATE_CSRF", Secure: true, Path: "/"})
			io.WriteString(w, `{"ok":true}`)
			return
		}
		if r.Method != "GET" || r.URL.Path != "/api/admin/ai-assistant/plans/5/execution-receipts" || r.URL.Query().Get("live") != "true" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		cookie, e := r.Cookie("aicrm_admin_session")
		if e != nil || cookie.Value != "PRIVATE_SESSION" {
			w.WriteHeader(401)
			return
		}
		reads++
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true,"read_only":true,"next_cursor":"","items":[{"recipient_id":17,"receipt":{"msgid":"msg-original","sender_userid":"HuangYouCan","creation_errcode":null,"creation_errcode_evidence":"not_recorded","query_state":"observed","query_http_status":200,"query_errcode":0,"provider_delivery_status":0}}]}`)
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_CREDENTIAL_ORIGIN", server.URL)
	var out, stderr bytes.Buffer
	if code := Run([]string{"profile", "add", "test", server.URL}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := Run([]string{"auth", "login", "--username", "staff", "--secret-stdin"}, strings.NewReader("PASSWORD"), &out, &stderr); code != 0 {
		t.Fatal(out.String())
	}
	out.Reset()
	if code := Run([]string{"invoke", "admin.listAIAssistantExecutionReceipts", "--param", "plan_id=5", "--query", "live=true"}, strings.NewReader(""), &out, &stderr); code != 0 || reads != 1 {
		t.Fatalf("code=%d reads=%d out=%s", code, reads, out.String())
	}
	if !strings.Contains(out.String(), "msg-original") || strings.Contains(out.String(), "PRIVATE_SESSION") {
		t.Fatal("CLI receipt or credential boundary failed")
	}
}
