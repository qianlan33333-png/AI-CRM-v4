package http

import (
	"github.com/qianlan33333-png/AI-CRM-v3/internal/tag/domain"
	"testing"
)

func TestLegacyTagSeparatesProviderBindingFromLocalCommandID(t *testing.T) {
	got := legacyTag(domain.Tag{ID: 22, ProviderTagID: "provider-real-id"})
	if got["tag_id"] != int64(22) || got["id"] != int64(22) || got["provider_tag_id"] != "provider-real-id" {
		t.Fatalf("ID projections changed: %+v", got)
	}
	if legacyTag(domain.Tag{ID: 23})["provider_tag_id"] != "" {
		t.Fatal("unbound tag must not fabricate a provider ID")
	}
}
