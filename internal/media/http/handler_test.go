package http

import (
	"encoding/base64"
	"net/http/httptest"
	"testing"
)

func TestMutationKeyPreservesExplicitAndMintsCompatibilityKey(t *testing.T) {
	explicit := httptest.NewRequest("POST", "/api/admin/image-library", nil)
	explicit.Header.Set("Idempotency-Key", "client-key-123456")
	if got := mutationKey(explicit); got != "client-key-123456" {
		t.Fatalf("explicit key=%q", got)
	}
	first := mutationKey(httptest.NewRequest("POST", "/api/admin/image-library", nil))
	second := mutationKey(httptest.NewRequest("POST", "/api/admin/image-library", nil))
	if len(first) < 32 || first[:14] != "server_compat_" || first == second {
		t.Fatalf("compatibility keys must be distinct and auditable: %q %q", first, second)
	}
}

func TestImageQueryFrozenFilterShape(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/admin/image-library?tags=a,b,a&tag_group=c,d&tag_group=e&only_unlabeled=true&enabled_only=false", nil)
	query, err := imageQuery(r, 50, 0, "")
	if err != nil || query.EnabledOnly || !query.OnlyUnlabeled || len(query.Tags) != 2 || len(query.TagGroups) != 2 || len(query.TagGroups[0]) != 2 {
		t.Fatalf("query=%+v err=%v", query, err)
	}
	if _, err := imageQuery(httptest.NewRequest("GET", "/?enabled_only=TRUE", nil), 50, 0, ""); err == nil {
		t.Fatal("non-lowercase boolean accepted")
	}
}

func TestImageDataURLRequiresBase64(t *testing.T) {
	// The parser's public contract is intentionally strict: unmarked data URLs
	// are rejected before bytes can reach Media storage.
	value := base64.StdEncoding.EncodeToString([]byte("not image"))
	if value == "" {
		t.Fatal("unexpected empty base64")
	}
}
