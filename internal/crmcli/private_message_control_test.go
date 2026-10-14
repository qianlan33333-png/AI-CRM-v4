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

func TestCLIPrivateMessageStopAndResult(t *testing.T) {
	keyring.MockInit()
	stopCalls, queryCalls := 0, 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("aicrm_admin_session")
		if err != nil || cookie.Value != "PRIVATE_SESSION" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "POST" && r.URL.Path == "/api/admin/ai-assistant/plans/5/recipients/17/stop" {
			stopCalls++
			var body map[string]any
			if json.NewDecoder(r.Body).Decode(&body) != nil || body["expected_version"] != float64(1) || body["reason"] != "discard old task" || r.Header.Get("Idempotency-Key") != "original-stop-key" || r.Header.Get("X-CSRF-Token") != "PRIVATE_CSRF" {
				t.Error("original control command or CSRF missing")
			}
			w.WriteHeader(202)
			io.WriteString(w, `{"ok":true,"control":{"effect_id":"eer_123","action":"stop","state":"queued","receipt_digest":"","replayed":false},"send_triggered":false}`)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/api/admin/ai-assistant/plans/5/recipients/17/control" && r.URL.Query().Get("action") == "stop" {
			queryCalls++
			io.WriteString(w, `{"ok":true,"plan_id":5,"recipient_id":17,"control":{"effect_id":"eer_123","action":"stop","state":"stopped","receipt_digest":"sha256:fixture","replayed":false},"read_only":true}`)
			return
		}
		t.Errorf("unexpected operation: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	var out, stderr bytes.Buffer
	if code := Run([]string{"profile", "add", "control", server.URL}, strings.NewReader(""), &out, &stderr); code != 0 {
		t.Fatal(out.String())
	}
	if err := StoreCredential(Credential{Origin: server.URL, Kind: "human", Session: "PRIVATE_SESSION", CSRF: "PRIVATE_CSRF"}); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	args := []string{"invoke", "admin.stopAIAssistantRecipient", "--param", "plan_id=5", "--param", "recipient_id=17", "--idempotency-key", "original-stop-key", "--input", "-"}
	if code := Run(args, strings.NewReader(`{"expected_version":1,"reason":"discard old task"}`), &out, &stderr); code != 0 || stopCalls != 1 {
		t.Fatalf("stop code=%d calls=%d out=%s", code, stopCalls, out.String())
	}
	if !strings.Contains(out.String(), `"send_triggered":false`) {
		t.Fatal("task acceptance claimed delivery")
	}
	out.Reset()
	if code := Run([]string{"invoke", "admin.getAIAssistantRecipientControl", "--param", "plan_id=5", "--param", "recipient_id=17", "--query", "action=stop"}, strings.NewReader(""), &out, &stderr); code != 0 || queryCalls != 1 || !strings.Contains(out.String(), `"state":"stopped"`) {
		t.Fatalf("query code=%d calls=%d out=%s", code, queryCalls, out.String())
	}
	if strings.Contains(out.String(), "PRIVATE_SESSION") || strings.Contains(out.String(), "PRIVATE_CSRF") {
		t.Fatal("credentials appeared in external CLI result")
	}
}
