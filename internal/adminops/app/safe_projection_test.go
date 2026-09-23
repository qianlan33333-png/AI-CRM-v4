package app

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	adminopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
)

func TestControlPlaneWritesUseClosedAllowlistsAndRejectNormalizedSensitiveKeysAtAnyDepth(t *testing.T) {
	if got, err := canonicalCategorySettings(map[string]any{"enabled": true}); err != nil || string(got) != `{"enabled":true}` {
		t.Fatalf("category canonical=%s err=%v", got, err)
	}
	if got, err := canonicalReleaseChanges(map[string]any{"wecom.corp_id": "corp", "wecom.agent_id": 7, "outbound.rate_per_second": "3", "outbound.max_attempts": 4, "wecom.webhook_ref": "secretref:wecom/alerts"}); err != nil || !json.Valid(got) {
		t.Fatalf("release canonical=%s err=%v", got, err)
	}
	if _, err := canonicalCategorySettings(map[string]any{"unknown": true}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("unknown category field error=%v", err)
	}
	if _, err := canonicalReleaseChanges(map[string]any{"unknown": true}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("unknown release field error=%v", err)
	}
	if _, err := canonicalReleaseChanges(map[string]any{"wecom.webhook_ref": "masked"}); !errors.Is(err, ErrInvalidCommand) {
		t.Fatalf("masked display value must not be accepted as a write reference: %v", err)
	}

	for _, key := range []string{"api_key", "Client-Secret", "Access.Token", "authorization", "cookie", "private_key", "credential", "password", "secret", "webhook_url"} {
		nested := map[string]any{"outer": []any{map[string]any{"inner": map[string]any{key: "must-not-store"}}}}
		if _, err := canonicalCategorySettings(nested); !errors.Is(err, ErrSecretMaterial) {
			t.Errorf("category sensitive key %q error=%v", key, err)
		}
		if _, err := canonicalReleaseChanges(nested); !errors.Is(err, ErrSecretMaterial) {
			t.Errorf("release sensitive key %q error=%v", key, err)
		}
	}
}

func TestReleaseCorpIDAndIntegersUseAuthoritativeCanonicalValues(t *testing.T) {
	canonical, err := canonicalReleaseChanges(map[string]any{
		"wecom.corp_id":            "corp-authoritative",
		"wecom.agent_id":           "17",
		"outbound.rate_per_second": json.Number("17.0"),
		"outbound.max_attempts":    17.0,
	})
	if err != nil || string(canonical) != `{"outbound.max_attempts":17,"outbound.rate_per_second":17,"wecom.agent_id":17,"wecom.corp_id":"corp-authoritative"}` {
		t.Fatalf("canonical release changes=%s err=%v", canonical, err)
	}

	for name, corpID := range map[string]string{
		"bearer":       "Bearer provider-token",
		"newline":      "corp-line\nbreak",
		"over-256":     strings.Repeat("c", 257),
		"invalid-utf8": string([]byte{'c', 'o', 'r', 'p', 0xff}),
	} {
		t.Run("corp-id-"+name, func(t *testing.T) {
			if _, err := canonicalReleaseChanges(map[string]any{"wecom.corp_id": corpID}); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("unsafe corp ID error=%v", err)
			}
		})
	}

	for name, value := range map[string]any{
		"int64-overflow-number":  json.Number("9223372036854775808"),
		"int64-overflow-decimal": json.Number("9223372036854775808.0"),
		"int64-overflow-string":  "9223372036854775808",
		"fraction":               17.5,
		"fraction-expression":    "34/2",
	} {
		t.Run("integer-"+name, func(t *testing.T) {
			if _, err := canonicalReleaseChanges(map[string]any{"wecom.agent_id": value}); !errors.Is(err, ErrInvalidCommand) {
				t.Fatalf("unsafe integer error=%v", err)
			}
		})
	}
}

func TestHistoricalCategoryAndReleaseProjectionNeverEchoesRawOrBase64SensitiveData(t *testing.T) {
	now := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC)
	categoryRaw := []byte(`{"enabled":true,"nested":{"access_token":"category-secret"},"unknown":"drop"}`)
	category := projectCategory(adminopsport.Category{Key: "push_capabilities", Enabled: true, Settings: categoryRaw, Version: 2, UpdatedBy: "admin:1", UpdatedAt: now})
	assertSafeProjectionJSON(t, category, categoryRaw, "category-secret", "access_token", "unknown")
	if category.Settings["enabled"] != true || !category.SettingsRedacted || len(category.Settings) != 1 {
		t.Fatalf("category projection=%#v", category)
	}

	releaseRaw := []byte(`{"outbound.rate_per_second":3,"wecom.webhook_ref":"secretref:wecom/sensitive-locator","nested":{"authorization":"Bearer release-secret"}}`)
	release := projectRelease(adminopsport.Release{ID: 9, State: "draft", Changes: releaseRaw, Checksum: strings.Repeat("a", 64), CreatedBy: "admin:1", CreatedAt: now})
	assertSafeProjectionJSON(t, release, releaseRaw, "sensitive-locator", "release-secret", "authorization", "nested")
	if release.Changes["outbound.rate_per_second"] == nil || release.Changes["wecom.webhook_ref"] != "masked" || !release.ChangesRedacted {
		t.Fatalf("release projection=%#v", release)
	}
}

func TestHistoricalReleaseChangesMustMatchClosedContractBeforeLifecycleMutation(t *testing.T) {
	for name, raw := range map[string][]byte{
		"sensitive": []byte(`{"wecom.corp_id":"corp","nested":{"access_token":"secret"}}`),
		"unknown":   []byte(`{"wecom.corp_id":"corp","legacy_flag":true}`),
		"invalid":   []byte(`{"wecom.agent_id":0}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateStoredReleaseChanges(raw); err == nil {
				t.Fatal("unsafe historical release changes must fail closed")
			}
		})
	}
	if err := validateStoredReleaseChanges([]byte(`{"wecom.corp_id":"corp","outbound.max_attempts":3}`)); err != nil {
		t.Fatalf("safe historical release changes rejected: %v", err)
	}
}

func TestLegacyReceiptReplayIsProjectedThroughTheSameClosedDTO(t *testing.T) {
	now := time.Date(2026, 8, 23, 8, 0, 0, 0, time.UTC)
	categoryRaw := []byte(`{"enabled":true,"api_key":"receipt-secret"}`)
	legacyCategory, _ := json.Marshal(adminopsport.Category{Key: "push_capabilities", Enabled: true, Settings: categoryRaw, Version: 1, UpdatedBy: "admin:1", UpdatedAt: now})
	category, err := decodeCategoryResult(legacyCategory, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSafeProjectionJSON(t, category, categoryRaw, "receipt-secret", "api_key")

	releaseRaw := []byte(`{"wecom.corp_id":"corp","private_key":"receipt-private"}`)
	legacyRelease, _ := json.Marshal(adminopsport.Release{ID: 7, State: "draft", Changes: releaseRaw, Checksum: strings.Repeat("b", 64), CreatedBy: "admin:1", CreatedAt: now})
	release, err := decodeReleaseResult(legacyRelease, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSafeProjectionJSON(t, release, releaseRaw, "receipt-private", "private_key")

	replayed, _ := json.Marshal(release)
	again, err := decodeReleaseResult(replayed, nil)
	if err != nil || !again.ChangesRedacted || again.Changes["wecom.corp_id"] != "corp" {
		t.Fatalf("safe replay=%#v err=%v", again, err)
	}
}

func assertSafeProjectionJSON(t *testing.T, value any, raw []byte, forbidden ...string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	forbidden = append(forbidden, base64.StdEncoding.EncodeToString(raw))
	for _, item := range forbidden {
		if strings.Contains(string(encoded), item) {
			t.Fatalf("projection leaked %q: %s", item, encoded)
		}
	}
}
