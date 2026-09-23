package channel

import (
	"testing"

	channeldomain "github.com/qianlan33333-png/AI-CRM-v3/internal/channel/domain"
)

func TestAcquisitionPreviewProjectsConfiguredProviderReadiness(t *testing.T) {
	channel := channeldomain.Channel{
		ID: 3, Code: "campaign", Status: channeldomain.StatusActive,
		Config: channeldomain.Config{
			Name:       "Campaign",
			Assignment: channeldomain.Assignment{Assignees: []channeldomain.Assignee{{StaffID: 7, Priority: 1, Ratio: 100}}},
		},
	}
	result := previewJSON(channel, []AcquisitionCandidate{{ID: 7, WeComUserID: "owner", DisplayName: "Owner"}}, true, true)
	lifecycle := result["lifecycle"].(map[string]any)
	blockers := lifecycle["readiness_blockers"].([]string)
	if len(blockers) != 0 || lifecycle["entrant_ready"] != true || result["provider_execution_eligible"] != true || result["real_external_call_executed"] != false {
		t.Fatalf("preview=%#v", result)
	}
}

func TestAcquisitionPreviewFailsClosedWhenProviderCapabilityIsDisabled(t *testing.T) {
	channel := channeldomain.Channel{ID: 3, Code: "campaign", Status: channeldomain.StatusInactive, Config: channeldomain.Config{Name: "Campaign"}}
	result := previewJSON(channel, nil, false, false)
	lifecycle := result["lifecycle"].(map[string]any)
	blockers := lifecycle["readiness_blockers"].([]string)
	want := []string{"provider_read_disabled", "provider_write_disabled", "channel_not_active", "assignees_missing"}
	if len(blockers) != len(want) {
		t.Fatalf("blockers=%v", blockers)
	}
	for index := range want {
		if blockers[index] != want[index] {
			t.Fatalf("blockers=%v", blockers)
		}
	}
	if lifecycle["entrant_ready"] != false || result["provider_execution_eligible"] != false {
		t.Fatalf("preview=%#v", result)
	}
}
