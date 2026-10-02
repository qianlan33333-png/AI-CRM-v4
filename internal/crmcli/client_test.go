package crmcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	catalog "github.com/qianlan33333-png/AI-CRM-v3/internal/clicatalog/port"
	"github.com/zalando/go-keyring"
)

func testOperation(method, path, auth string) catalog.Operation {
	return catalog.Operation{ID: "test", Binding: &catalog.Binding{Method: method, Path: path, Auth: auth, ContentType: "application/json"}}
}
func TestOriginBindingAndRegisteredPathIsolation(t *testing.T) {
	for _, v := range []string{"http://crm.test", "https://u:p@crm.test", "https://crm.test/path", "https://crm.test?token=x"} {
		if _, e := Origin(v); e == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	if _, e := NewClient("https://b.test", Credential{Origin: "https://a.test", Kind: "machine"}, time.Second); e == nil {
		t.Fatal("cross origin credential accepted")
	}
	op := testOperation("GET", "/api/admin/customers/{id}", "human")
	for _, v := range []string{"../other", "//evil", "x?token=secret", ".."} {
		if _, _, _, e := BuildRequest(op, CallInput{Parameters: map[string]string{"id": v}}); e == nil {
			t.Fatalf("path injection accepted %q", v)
		}
	}
	if _, _, _, e := BuildRequest(op, CallInput{Parameters: map[string]string{"fake": "1"}}); e == nil {
		t.Fatal("unknown path field accepted")
	}
}
func TestDuplicateJSONAndUnknownFieldsFailBeforeDispatch(t *testing.T) {
	for _, v := range []string{`{"a":1,"a":2}`, `{"x":{"scope":1,"scope":2}}`, `{"a":1} {}`} {
		var out any
		if StrictJSON([]byte(v), &out) == nil {
			t.Fatalf("accepted %s", v)
		}
	}
	op := testOperation("POST", "/registered", "machine")
	op.Binding.Body = map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{"value": map[string]any{"type": "string"}}}
	if _, _, _, e := BuildRequest(op, CallInput{Body: []byte(`{"verified":true}`)}); e == nil {
		t.Fatal("unknown field accepted")
	}
	op.Binding.IdempotencyRequired = true
	if _, _, _, e := BuildRequest(op, CallInput{Body: []byte(`{}`)}); e == nil {
		t.Fatal("missing idempotency accepted")
	}
}

func TestDeclaredQueryArraysFollowTheirSerializationAndRejectAmbiguity(t *testing.T) {
	op, ok := catalog.Find("customer.activities.list")
	if !ok {
		t.Fatal("missing native activity operation")
	}
	path, _, _, err := BuildRequest(op, CallInput{Parameters: map[string]string{"customer_id": "7"}, Query: map[string]string{"types": `["message","order"]`}})
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(path)
	values := u.Query()["types"]
	if len(values) != 2 || values[0] != "message" || values[1] != "order" {
		t.Fatalf("unexpected serialization %v", values)
	}
	for _, value := range []string{`message,order`, `["order","order"]`, `["order","unknown"]`} {
		if _, _, _, err := BuildRequest(op, CallInput{Parameters: map[string]string{"customer_id": "7"}, Query: map[string]string{"types": value}}); err == nil {
			t.Fatalf("accepted ambiguous or unsupported query array")
		}
	}
}
func TestRedirectNeverLeaksCredentialAndWrongIdentityNeverDispatches(t *testing.T) {
	leaked := 0
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked++ }))
	defer target.Close()
	calls := 0
	source := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	c, _ := NewClient(source.URL, Credential{Origin: source.URL, Kind: "machine", Token: "secret"}, time.Second)
	c.HTTP.Transport = source.Client().Transport
	_, e := c.Call(context.Background(), testOperation("GET", "/registered", "machine"), CallInput{})
	if e == nil || leaked != 0 || calls != 1 {
		t.Fatalf("redirect leaked calls=%d leaked=%d err=%v", calls, leaked, e)
	}
	_, e = c.Call(context.Background(), testOperation("POST", "/admin", "human"), CallInput{})
	if e == nil || calls != 1 {
		t.Fatal("machine entered human transport")
	}
}
func TestAmbiguousWriteIsNotRetriedAndServerErrorIsRedacted(t *testing.T) {
	calls := 0
	s := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(500)
		io.WriteString(w, `{"secret":"SENSITIVE","error":"failed"}`)
	}))
	defer s.Close()
	c, _ := NewClient(s.URL, Credential{Origin: s.URL, Kind: "machine", Token: "token"}, time.Second)
	c.HTTP.Transport = s.Client().Transport
	_, e := c.Call(context.Background(), testOperation("POST", "/registered", "machine"), CallInput{Body: []byte(`{}`)})
	var f *Failure
	if !errors.As(e, &f) || f.Code != "outcome_unknown" || calls != 1 || strings.Contains(e.Error(), "SENSITIVE") {
		t.Fatalf("calls=%d err=%v", calls, e)
	}
}

func TestConnectionLossAfterWriteAcceptanceDoesNotReplay(t *testing.T) {
	writes := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			io.WriteString(w, "warm-up")
			return
		}
		writes++
		io.Copy(io.Discard, r.Body)
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		connection.Close()
	}))
	defer server.Close()
	original := http.DefaultTransport
	http.DefaultTransport = server.Client().Transport
	defer func() { http.DefaultTransport = original }()
	c, _ := NewClient(server.URL, Credential{Origin: server.URL, Kind: "machine", Token: "TOKEN"}, time.Second)
	resp, err := c.Call(context.Background(), testOperation("GET", "/warm", "machine"), CallInput{})
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	_, err = c.Call(context.Background(), testOperation("POST", "/write", "machine"), CallInput{Body: []byte(`{}`), IdempotencyKey: "same-key"})
	var f *Failure
	if !errors.As(err, &f) || f.Exit != 7 || writes != 1 {
		t.Fatalf("writes=%d error=%v", writes, err)
	}
}
func TestDryRunAndBlockedCandidateNeverCallServer(t *testing.T) {
	var out, stderr bytes.Buffer
	code := Run([]string{"invoke", "candidate.wecom.moments", "--dry-run"}, strings.NewReader(""), &out, &stderr)
	if code != 4 {
		t.Fatalf("blocked candidate exit=%d output=%s", code, out.String())
	}
	out.Reset()
	code = Run([]string{"invoke", "customer.context.get", "--param", "customer_id=7", "--dry-run"}, strings.NewReader(""), &out, &stderr)
	if code != 0 || !strings.Contains(out.String(), `"executed":false`) {
		t.Fatalf("dry run exit=%d output=%s", code, out.String())
	}
}
func TestProfileCredentialStorageIsOriginBoundAndSecretsAreNotPrinted(t *testing.T) {
	keyring.MockInit()
	t.Setenv("AICRM_CLI_CONFIG_DIR", t.TempDir())
	t.Setenv("AICRM_CLI_TOKEN", "")
	cfg := Config{Current: "test", Profiles: map[string]Profile{"test": {Origin: "https://crm.test"}}}
	if e := SaveConfig(cfg); e != nil {
		t.Fatal(e)
	}
	if e := StoreCredential(Credential{Origin: "https://crm.test", Kind: "human", Session: "SESSION", CSRF: "CSRF"}); e != nil {
		t.Fatal(e)
	}
	if _, e := LoadCredential("https://other.test"); e == nil {
		t.Fatal("loaded another origin")
	}
	path, _ := configPath()
	b, _ := os.ReadFile(path)
	if strings.Contains(string(b), "SESSION") {
		t.Fatal("credential in config")
	}
	stat, _ := os.Stat(path)
	if stat.Mode().Perm() != 0600 {
		t.Fatal("profile permissions")
	}
	t.Setenv("AICRM_CLI_TOKEN", "MACHINE")
	if _, e := LoadCredential("https://crm.test"); e == nil {
		t.Fatal("unbound environment credential")
	}
}
func TestSensitiveOutputRefusesOverwriteAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out")
	if e := privateFile(path, []byte("one"), false); e != nil {
		t.Fatal(e)
	}
	if e := privateFile(path, []byte("two"), false); e == nil {
		t.Fatal("overwrote output")
	}
	link := filepath.Join(dir, "link")
	if e := os.Symlink(path, link); e != nil {
		t.Fatal(e)
	}
	if e := privateFile(link, []byte("two"), true); e == nil {
		t.Fatal("followed symlink")
	}
	b, _ := os.ReadFile(path)
	if string(b) != "one" {
		t.Fatal("modified original")
	}
}

func TestGovernanceScopeIntentPreservesOmissionAndRequiresExplicitBroadScope(t *testing.T) {
	for _, tc := range []struct {
		id, body            string
		unrestricted, valid bool
	}{
		{"admin.createOpenPlatformClient", `{}`, false, false},
		{"admin.createOpenPlatformClient", `{"owner_scope":null}`, true, false},
		{"admin.createOpenPlatformClient", `{"owner_scope":{}}`, false, false},
		{"admin.createOpenPlatformClient", `{"owner_scope":{}}`, true, true},
		{"admin.createOpenPlatformClient", `{"owner_scope":{"customer_id":["7"]}}`, false, true},
		{"admin.patchOpenPlatformClient", `{"display_name":"new"}`, false, true},
		{"admin.patchOpenPlatformClient", `{"owner_scope":null}`, false, false},
		{"admin.patchOpenPlatformClient", `{"owner_scope":null}`, true, true},
		{"admin.patchOpenPlatformClient", `{"owner_scope":{}}`, false, false},
		{"admin.patchOpenPlatformClient", `{"owner_scope":{"customer_id":[]}}`, true, false},
	} {
		if e := validateScopeIntent(catalog.Operation{ID: tc.id}, []byte(tc.body), tc.unrestricted); (e == nil) != tc.valid {
			t.Fatalf("id=%s valid=%v err=%v", tc.id, tc.valid, e)
		}
	}
}

func TestMultipartStreamsAndValidationReadersCanBeClosedWithoutSending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "asset.txt")
	if e := os.WriteFile(path, []byte("asset-content"), 0600); e != nil {
		t.Fatal(e)
	}
	op := testOperation("POST", "/upload", "human")
	op.Binding.ContentType = "multipart/form-data"
	input := CallInput{Uploads: map[string]string{"file": path}, Body: []byte(`{"name":"asset"}`)}
	_, _, reader, err := BuildRequest(op, input)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.(io.Closer).Close(); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1024); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		defer r.MultipartForm.RemoveAll()
		f, _, err := r.FormFile("file")
		if err != nil {
			t.Error(err)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		if string(b) != "asset-content" || r.FormValue("name") != "asset" || r.Header.Get("X-CSRF-Token") != "CSRF" {
			t.Error("multipart or CSRF mismatch")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()
	c, _ := NewClient(server.URL, Credential{Origin: server.URL, Kind: "human", Session: "SESSION", CSRF: "CSRF"}, time.Second)
	c.HTTP.Transport = server.Client().Transport
	resp, err := c.Call(context.Background(), op, input)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}
func TestCatalogRetainsV1AndCoversSidebarWithoutExecutableGaps(t *testing.T) {
	c := catalog.Load()
	if len(c.Modules) != 24 {
		t.Fatalf("modules %d", len(c.Modules))
	}
	machine := 0
	ids := map[string]bool{}
	groups := map[string]bool{"schema": true} // Built-in schema get owns the API documentation entry.
	for _, op := range c.Operations {
		groups[op.Group] = true
		if ids[op.ID] {
			t.Fatal("duplicate operation ID")
		}
		ids[op.ID] = true
		if op.Binding != nil && op.Binding.Auth == "machine" {
			machine++
			if !strings.HasPrefix(op.Binding.Path, "/open/v1/") {
				t.Fatal("unexpected machine business route")
			}
		}
		if strings.HasPrefix(op.ID, "candidate.") && op.Binding != nil {
			t.Fatal("candidate became executable")
		}
	}
	if machine != 20 {
		t.Fatalf("machine V1 count %d", machine)
	}
	for _, module := range c.Modules {
		if !groups[module.Group] {
			t.Fatalf("sidebar module has no command mapping: %s", module.Name)
		}
	}
	var out bytes.Buffer
	if e := json.NewEncoder(&out).Encode(c); e != nil {
		t.Fatal(e)
	}
}
