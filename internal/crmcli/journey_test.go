package crmcli

import (
	"bytes"
	"encoding/json"
	"github.com/zalando/go-keyring"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCLIHumanLoginReadAndGovernanceJourney(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	t.Setenv("AICRM_CLI_PASSWORD", "")
	writes := 0
	reads := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/admin/access/cli/login-challenge":
			http.SetCookie(w, &http.Cookie{Name: "aicrm_login_csrf", Value: "nonce", Secure: true, HttpOnly: true, Path: "/"})
			io.WriteString(w, `{"login_csrf_token":"nonce"}`)
		case "/login":
			var v map[string]string
			json.NewDecoder(r.Body).Decode(&v)
			cookie, e := r.Cookie("aicrm_login_csrf")
			if e != nil || cookie.Value != "nonce" || v["password"] != "PASSWORD" || v["login_csrf_token"] != "nonce" {
				w.WriteHeader(403)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "aicrm_admin_session", Value: "SESSION", Secure: true, Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "aicrm_admin_csrf", Value: "CSRF", Secure: true, Path: "/"})
			io.WriteString(w, `{"ok":true}`)
		case "/api/admin/access/users":
			cookie, e := r.Cookie("aicrm_admin_session")
			if e != nil || cookie.Value != "SESSION" || r.Header.Get("Authorization") != "" {
				w.WriteHeader(401)
				return
			}
			if r.Method == "POST" {
				if r.Header.Get("X-CSRF-Token") != "CSRF" || r.Header.Get("Idempotency-Key") == "" {
					w.WriteHeader(403)
					return
				}
				writes++
				io.WriteString(w, `{"ok":true}`)
			} else {
				reads++
				io.WriteString(w, `{"items":[{"id":7,"role":"viewer"}]}`)
			}
		default:
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	var out, stderr bytes.Buffer
	run := func(args []string, input string) int {
		out.Reset()
		stderr.Reset()
		return Run(args, strings.NewReader(input), &out, &stderr)
	}
	if code := run([]string{"profile", "add", "test", server.URL}, ""); code != 0 {
		t.Fatal(out.String())
	}
	if code := run([]string{"auth", "login", "--username", "operator", "--secret-stdin"}, "PASSWORD"); code != 0 {
		t.Fatal(out.String())
	}
	if strings.Contains(out.String(), "PASSWORD") || strings.Contains(out.String(), "SESSION") {
		t.Fatal("credential output")
	}
	if code := run([]string{"access", "users", "list"}, ""); code != 0 || reads != 1 {
		t.Fatalf("read=%d code=%d %s", reads, code, out.String())
	}
	payload := `{"wecom_userid":"employee","role":"viewer"}`
	if code := run([]string{"invoke", "admin.provisionAccessEnterpriseEmployee", "--input", "-", "--idempotency-key", "provision-employee-1"}, payload); code != 0 || writes != 1 {
		t.Fatalf("writes=%d code=%d %s", writes, code, out.String())
	}
	if code := run([]string{"invoke", "admin.provisionAccessEnterpriseEmployee", "--input", "-"}, payload); code != 2 || writes != 1 {
		t.Fatal("governance dispatched without idempotency")
	}
	if code := run([]string{"invoke", "admin.provisionAccessEnterpriseEmployee", "--input", "-", "--idempotency-key", "another-key", "--format", "invalid"}, payload); code != 2 || writes != 1 {
		t.Fatal("invalid output format dispatched a write")
	}
	if code := run([]string{"invoke", "customer.context.get", "--param", "customer_id=7"}, ""); code != 4 {
		t.Fatal("human was converted to machine")
	}
	if code := run([]string{"invoke", "admin.createLegacyAIAssistantReviewPlan", "--dry-run"}, ""); code == 0 || writes != 1 {
		t.Fatal("independent integration signature route must not bind a human session")
	}
}
func TestPaginationPartialAndUnknownResultsHaveExplicitOutcomes(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	calls := 0
	mode := "pages"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		switch mode {
		case "pages":
			if r.URL.Query().Get("cursor") == "" {
				io.WriteString(w, `{"data":{"items":[1],"next_cursor":"next"}}`)
			} else {
				io.WriteString(w, `{"data":{"items":[2],"next_cursor":""}}`)
			}
		case "partial":
			io.WriteString(w, `{"data":{"status":"partial"}}`)
		case "unknown":
			io.WriteString(w, `{"data":{"status":"outcome_unknown"}}`)
		}
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	SaveConfig(Config{Current: "test", Profiles: map[string]Profile{"test": {Origin: server.URL}}})
	StoreCredential(Credential{Origin: server.URL, Kind: "machine", Token: "TOKEN"})
	var out, stderr bytes.Buffer
	if code := Run([]string{"invoke", "customer.list", "--all"}, strings.NewReader(""), &out, &stderr); code != 0 || calls != 2 {
		t.Fatalf("pagination %d calls %d output %s", code, calls, out.String())
	}
	if strings.Count(strings.TrimSpace(out.String()), "\n") != 1 {
		t.Fatal("pagination not NDJSON")
	}
	for _, test := range []struct {
		mode string
		code int
	}{{"partial", 8}, {"unknown", 7}} {
		mode = test.mode
		before := calls
		out.Reset()
		if code := Run([]string{"invoke", "customer.list"}, strings.NewReader(""), &out, &stderr); code != test.code || calls != before+1 {
			t.Fatalf("mode=%s code=%d calls=%d", mode, code, calls)
		}
		var envelope map[string]any
		if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope["result"] == nil || envelope["error"] == nil {
			t.Fatalf("failure must be one JSON document retaining per-item results: %s", out.String())
		}
	}
}
