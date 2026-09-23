package source

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHistorySnapshotCanonicalPreservesAbsentAndEmptyReferences(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	empty := ""
	snap := HistorySnapshot{Plans: []HistoryPlan{{ID: 1, PlanCode: "", Name: "历史", PlanType: "standard", Status: "draft", OwnerReference: &empty, CreatedAt: now, UpdatedAt: now}}, Nodes: []HistoryNode{{ID: 2, PlanID: 1, DayIndex: 1, TriggerTime: "09:00", SortOrder: 1, Status: "active", ContentPackage: json.RawMessage(`{}`), Attachments: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now}}}
	if err := PopulateHistoryManifest(&snap, ProductionSourceSystem, strings.Repeat("a", 40), now); err != nil {
		t.Fatal(err)
	}
	raw, _, err := snap.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := ParseHistory(raw)
	if err != nil || parsed.Plans[0].OwnerReference == nil || *parsed.Plans[0].OwnerReference != "" || parsed.Plans[0].CreatedByReference != nil {
		t.Fatalf("protected source reference was normalized: parsed=%#v err=%v", parsed.Plans[0], err)
	}
}

func TestHistorySnapshotPreservesLegacyNodeTextAttachmentsAndBlankTrigger(t *testing.T) {
	now := time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC)
	snap := HistorySnapshot{Plans: []HistoryPlan{{ID: 1, Name: "历史", PlanType: "standard", Status: "disabled", CreatedAt: now, UpdatedAt: now}}, Nodes: []HistoryNode{
		{ID: 1, PlanID: 1, DayIndex: 1, TriggerTime: "", SortOrder: 1, Status: "active", ActionTitle: "标题", TextContent: "正文", ContentPackage: json.RawMessage(`{}`), Attachments: json.RawMessage(`[]`), CreatedAt: now, UpdatedAt: now},
		{ID: 2, PlanID: 1, DayIndex: 2, TriggerTime: "09:00", SortOrder: 2, Status: "active", ContentPackage: json.RawMessage(`{}`), Attachments: json.RawMessage(`[{"kind":"image","id":"m1"}]`), CreatedAt: now, UpdatedAt: now},
	}}
	if err := PopulateHistoryManifest(&snap, ProductionSourceSystem, strings.Repeat("b", 40), now); err != nil {
		t.Fatal(err)
	}
	raw, _, err := snap.Canonical()
	if err != nil {
		t.Fatal(err)
	}
	parsed, _, err := ParseHistory(raw)
	if err != nil || parsed.Nodes[0].TriggerTime != "" || parsed.Nodes[0].ActionTitle != "标题" || parsed.Nodes[0].TextContent != "正文" || string(parsed.Nodes[1].Attachments) != `[{"kind":"image","id":"m1"}]` {
		t.Fatalf("legacy node fact changed: %#v err=%v", parsed.Nodes, err)
	}
}
