package main

import (
	"testing"
	"time"

	platformconfig "github.com/qianlan33333-png/AI-CRM-v3/internal/platform/config"
)

func TestCompositionPassesIndependentMaterialUploadTimeout(t *testing.T) {
	cfg := platformconfig.NormalizeRuntimePolicyDefaults(platformconfig.Runtime{})
	provider := weComProviderConfig(cfg)
	if provider.UploadTimeout != 120*time.Second {
		t.Fatalf("upload timeout=%s", provider.UploadTimeout)
	}
	cfg.WeCom.MaterialUploadTimeout = 45 * time.Second
	if got := weComProviderConfig(cfg).UploadTimeout; got != 45*time.Second {
		t.Fatalf("configured upload timeout=%s", got)
	}
}
