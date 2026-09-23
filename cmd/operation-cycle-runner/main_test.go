package main

import (
	"reflect"
	"testing"
	"time"
)

func TestParseConfigRequiresExplicitSafeLocalBindings(t *testing.T) {
	value, err := parseConfig([]string{"--crm-url", "https://crm.example.test", "--runner-id", "local-runner", "--codex-binary", "/opt/codex", "--codex-socket", "/tmp/codex.sock", "--codex-version", "codex 1", "--control-socket", "/tmp/runner.sock", "--renewal-interval", "25s", "--binding", "excel_workspace=/private/tmp/workspace", "--binding", "reports=/private/tmp/reports"})
	if err != nil || value.renewalInterval != 25*time.Second || !reflect.DeepEqual(value.bindings, map[string]string{"excel_workspace": "/private/tmp/workspace", "reports": "/private/tmp/reports"}) {
		t.Fatalf("config=%#v err=%v", value, err)
	}
	for _, args := range [][]string{
		{"--crm-url", "http://crm.example.test"},
		{"--crm-url", "https://crm.example.test", "--runner-id", "x", "--codex-binary", "/x", "--codex-socket", "/x", "--codex-version", "v", "--control-socket", "/x", "--binding", "bad=relative"},
		{"--crm-url", "https://crm.example.test", "--runner-id", "x", "--codex-binary", "/x", "--codex-socket", "/x", "--codex-version", "v", "--control-socket", "/x", "--renewal-interval", "26s", "--binding", "safe=/tmp/x"},
		{"--crm-url", "https://crm.example.test", "--runner-id", "x", "--codex-binary", "/x", "--codex-socket", "/x", "--codex-version", "v", "--control-socket", "/x", "--renewal-interval", "60s", "--binding", "safe=/tmp/x"},
	} {
		if _, err := parseConfig(args); err == nil {
			t.Fatalf("unsafe config accepted: %q", args)
		}
	}
}
func TestLookupEnvDoesNotNeedProcessEnvironment(t *testing.T) {
	if value, ok := lookupEnv([]string{"AICRM_OPERATION_RUNNER_SERVICE_TOKEN=secret", "OTHER=x"}, "AICRM_OPERATION_RUNNER_SERVICE_TOKEN"); !ok || value != "secret" {
		t.Fatalf("lookup=%q/%v", value, ok)
	}
}
