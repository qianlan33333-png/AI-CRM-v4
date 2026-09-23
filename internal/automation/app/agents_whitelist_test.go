package app

import (
	"encoding/json"
	"errors"
	"testing"

	automationport "github.com/qianlan33333-png/AI-CRM-v3/internal/automation/port"
)

func TestWhitelistAgentConfigurationFailsClosed(t *testing.T) {
	active := automationport.Agent{AgentName: "agent", AgentCode: "agent_1", AutomationType: automationport.AutomationTypeAgent, Status: automationport.AgentStatusActive}
	if _, err := normalizeCreate(active, 1); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("active create err=%v", err)
	}
	paused := active
	paused.Status = automationport.AgentStatusPaused
	paused.ExecutionEnabled = true
	if _, err := normalizeCreate(paused, 1); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("execution-enabled create err=%v", err)
	}
	if content, err := normalizeContent(automationport.FixedContentPackage{ImageLibraryIDs: []int64{7}, AttachmentLibraryIDs: []int64{8}, MiniprogramLibraryIDs: []int64{9}, GroupInviteLibraryIDs: []int64{10}}, automationport.AutomationTypeFixedScript); err != nil || len(content.ImageLibraryIDs) != 1 || len(content.AttachmentLibraryIDs) != 1 || len(content.MiniprogramLibraryIDs) != 1 || len(content.GroupInviteLibraryIDs) != 1 {
		t.Fatalf("media content=%+v err=%v", content, err)
	}
	if _, err := normalizeContent(automationport.FixedContentPackage{DynamicMiniprogramCard: []byte(`{"appid":"wx"}`)}, automationport.AutomationTypeFixedScript); !errors.Is(err, ErrInvalidAgent) {
		t.Fatalf("dynamic card without a stable reader err=%v", err)
	}
}

func TestLegacyConfigurationRejectsSensitiveKeysRecursively(t *testing.T) {
	for _, raw := range []string{
		`{"provider":{"api_key":"value"}}`,
		`{"nested":[{"private_key":"value"}]}`,
		`{"Cookie-Jar":{"value":"value"}}`,
		`{"safe":{"authorization_token":"value"}}`,
	} {
		if _, err := normalizeLegacyConfiguration(json.RawMessage(raw)); !errors.Is(err, ErrInvalidAgent) {
			t.Fatalf("raw=%s err=%v, want sensitive-key rejection", raw, err)
		}
	}
}

func TestLegacyConfigurationAllowsNonSensitiveNestedKeys(t *testing.T) {
	raw := json.RawMessage(`{"features":[{"name":"draft"}],"provider":{"region":"cn","timeout_ms":5000}}`)
	canonical, err := normalizeLegacyConfiguration(raw)
	if err != nil || string(canonical) != string(raw) {
		t.Fatalf("canonical=%s err=%v", canonical, err)
	}
}
