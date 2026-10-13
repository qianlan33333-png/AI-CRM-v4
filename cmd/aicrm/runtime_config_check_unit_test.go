package main

import (
	"bytes"
	"strings"
	"testing"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestRuntimeConfigCheckUsesStartupValidation(t *testing.T) {
	t.Setenv("AICRM_DATABASE_URL", "postgres://synthetic:unused@127.0.0.1:1/check-only")
	t.Setenv("AICRM_OUTBOUND_PROVIDER_ENABLED", "false")
	t.Setenv("AICRM_ROLE", "effects-worker")
	t.Setenv("AICRM_GROUP_OPS_WEBHOOK_SECRET", "")
	if _, err := platformconfig.Load(); err != nil {
		t.Fatal("valid startup configuration fixture rejected")
	}
	var output bytes.Buffer
	if checkRuntimeConfig(&output) != 0 || !strings.Contains(output.String(), `"code":"runtime_config_valid"`) {
		t.Fatal("configuration check disagrees with startup validation")
	}
	t.Setenv("AICRM_GROUP_OPS_WEBHOOK_SECRET", "private-short-sentinel")
	if _, err := platformconfig.Load(); err == nil {
		t.Fatal("startup validator did not reject the known invalid secret")
	}
	output.Reset()
	if checkRuntimeConfig(&output) != 1 || output.String() != "{\"ok\":false,\"code\":\"runtime_config_invalid\"}\n" {
		t.Fatal("configuration check must reject without revealing a value")
	}
}
