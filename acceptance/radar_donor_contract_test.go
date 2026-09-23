package acceptance_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

const radarDonorCommit = "6bfbe5816bb89913c70adaca87d6a486260e016e"

type radarContractFixture struct {
	Name         string         `json:"name"`
	SourceCommit string         `json:"source_commit"`
	Disposition  string         `json:"disposition"`
	Capabilities []string       `json:"capabilities"`
	Assertions   map[string]any `json:"assertions"`
}

func TestRadarDonorContractFixtures(t *testing.T) {
	fixtureDir := filepath.Join("..", "internal", "radar", "contract", "fixtures")
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("fixture count=%d want=4", len(entries))
	}

	seen := make(map[string]bool, len(entries))
	fixtures := make(map[string]radarContractFixture, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			t.Fatalf("unexpected fixture entry %q", entry.Name())
		}
		payload, readErr := os.ReadFile(filepath.Join(fixtureDir, entry.Name()))
		if readErr != nil {
			t.Fatal(readErr)
		}
		var fixture radarContractFixture
		if decodeErr := json.Unmarshal(payload, &fixture); decodeErr != nil {
			t.Fatalf("decode %s: %v", entry.Name(), decodeErr)
		}
		if fixture.Name == "" || seen[fixture.Name] {
			t.Fatalf("empty or duplicate fixture name %q", fixture.Name)
		}
		seen[fixture.Name] = true
		if fixture.SourceCommit != radarDonorCommit {
			t.Fatalf("%s source commit=%q", fixture.Name, fixture.SourceCommit)
		}
		if fixture.Disposition != "donor_real" && fixture.Disposition != "donor_placeholder" && fixture.Disposition != "v3_required" {
			t.Fatalf("%s disposition=%q", fixture.Name, fixture.Disposition)
		}
		if len(fixture.Capabilities) == 0 || len(fixture.Assertions) == 0 {
			t.Fatalf("%s has an empty contract", fixture.Name)
		}
		fixtures[fixture.Name] = fixture
	}

	tracking := fixtures["public-local-tracking"]
	assertBool(t, tracking.Assertions, "identity_attributed", false)
	assertBool(t, tracking.Assertions, "real_external_call_executed", false)

	oneID := fixtures["v3-unionid-oneid-extension"]
	assertString(t, oneID.Assertions, "provider_identity_kind", "unionid")
	assertString(t, oneID.Assertions, "scope_prefix", "wechat-open-platform:")
	assertBool(t, oneID.Assertions, "provider_verified_required", true)
	assertBool(t, oneID.Assertions, "openid_fallback_allowed", false)
	assertBool(t, oneID.Assertions, "raw_unionid_exposed_to_radar", false)
}

func assertBool(t *testing.T, values map[string]any, key string, want bool) {
	t.Helper()
	got, ok := values[key].(bool)
	if !ok || got != want {
		t.Fatalf("%s=%v want=%v", key, values[key], want)
	}
}

func assertString(t *testing.T, values map[string]any, key, want string) {
	t.Helper()
	got, ok := values[key].(string)
	if !ok || got != want {
		t.Fatalf("%s=%v want=%q", key, values[key], want)
	}
}
