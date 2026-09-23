package domain

import (
	"encoding/json"
	"strings"
	"testing"

	groupopsport "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
)

func TestPlanTransitionsKeepArchivedTerminal(t *testing.T) {
	for _, test := range []struct {
		from, to groupopsport.PlanStatus
		want     bool
	}{
		{groupopsport.PlanDraft, groupopsport.PlanActive, true},
		{groupopsport.PlanActive, groupopsport.PlanPaused, true},
		{groupopsport.PlanPaused, groupopsport.PlanActive, true},
		{groupopsport.PlanArchived, groupopsport.PlanDraft, false},
		{groupopsport.PlanActive, groupopsport.PlanDraft, false},
	} {
		if got := CanTransitionPlanStatus(test.from, test.to); got != test.want {
			t.Fatalf("transition %s -> %s = %v, want %v", test.from, test.to, got, test.want)
		}
	}
}

func TestTypedMaterialAndScopeValidation(t *testing.T) {
	if err := ValidateMaterialPlan(groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 1}, {Kind: "attachment", ID: 2}}}); err != nil {
		t.Fatal(err)
	}
	if err := ValidateMaterialPlan(groupopsport.MaterialPlan{References: []groupopsport.MaterialReference{{Kind: "image", ID: 1}, {Kind: "image", ID: 1}}}); err == nil {
		t.Fatal("duplicate typed material reference accepted")
	}
	if !ContainsForbidden(json.RawMessage(`{"customer_id":7}`)) || ContainsForbidden(json.RawMessage(`{"message":"audience is a label"}`)) {
		t.Fatal("scope validation did not fail closed on concrete identity field")
	}
}

func TestWebhookInboundAllowsOnlyLeadingTextThenOrderedAttachments(t *testing.T) {
	valid := groupopsport.WebhookInboundCommand{
		WebhookReference:     "bound-hook",
		TargetChatReferences: []string{"bound-chat-a", "bound-chat-b"},
		Messages: []groupopsport.WebhookMessage{
			{Type: "text", Text: "今日话术"},
			{Type: "image", ImageID: 7},
			{Type: "file", AttachmentID: 8},
			{Type: "miniprogram", AppID: "wx-course", Path: "pages/course/index", Title: "课程详情"},
		},
	}
	if err := ValidateWebhookInbound(valid); err != nil {
		t.Fatal(err)
	}
	localMiniProgram := valid
	localMiniProgram.Messages = []groupopsport.WebhookMessage{{Type: "text", Text: "今日话术"}, {Type: "miniprogram", MiniProgramID: 9}}
	if err := ValidateWebhookInbound(localMiniProgram); err != nil {
		t.Fatalf("local miniprogram reference rejected: %v", err)
	}
	for _, invalid := range []groupopsport.WebhookInboundCommand{
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "image", ImageID: 7}, {Type: "text", Text: "too late"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "text", Text: "one"}, {Type: "text", Text: "two"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "text", Text: "has\x00nul"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: []string{"bound-chat-a", "bound-chat-a"}, Messages: []groupopsport.WebhookMessage{{Type: "text", Text: "duplicate target"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "image", ImageID: 7, Text: "mixed fields"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "miniprogram", AppID: "wx-course", Path: "pages/course/index", Title: strings.Repeat("中", 22)}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "miniprogram", AppID: "wx-course", Path: "pages/course/index", Title: "title\x00control"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "miniprogram", MiniProgramID: 9, AppID: "wx-course"}}},
		{WebhookReference: valid.WebhookReference, TargetChatReferences: valid.TargetChatReferences, Messages: []groupopsport.WebhookMessage{{Type: "miniprogram", MiniProgramID: -1}}},
	} {
		if err := ValidateWebhookInbound(invalid); err == nil {
			t.Fatalf("invalid command accepted: %#v", invalid)
		}
	}
}
