package domain

import (
	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	"testing"
	"time"
)

func TestInvitationThresholdBoundaries(t *testing.T) {
	for _, n := range []int{0, 1, 180, 200, 201} {
		v := p.InvitationInput{Name: "计划", Title: "入群", Mode: "sequence", Threshold: &n, ChatIDs: []string{"a", "b"}}
		err := ValidateInvitationInput(v)
		if (err == nil) != (n >= 1 && n <= 200) {
			t.Fatalf("threshold %d: %v", n, err)
		}
	}
	v := p.InvitationInput{Name: "计划", Title: "入群", Mode: "sequence", ChatIDs: []string{"a"}}
	if ValidateInvitationInput(v) == nil {
		t.Fatal("missing threshold accepted")
	}
}
func TestInvitationRotationNeverReturnsToRetiredGroup(t *testing.T) {
	now := time.Now().UTC()
	n := 180
	plan := p.InvitationPlan{Mode: "sequence", Enabled: true, Threshold: &n, Bindings: []p.InvitationBinding{{ChatID: "a", CodeState: "executed", QRCode: "a"}, {ChatID: "b", CodeState: "executed", QRCode: "b"}}}
	facts := map[string]g.CatalogGroup{"a": {MemberCount: 180, ObservedAt: &now}, "b": {MemberCount: 12, ObservedAt: &now}}
	out := EvaluateInvitation(plan, facts, now)
	if out.CurrentChatID != "b" || !out.Bindings[0].Retired {
		t.Fatalf("not advanced: %+v", out)
	}
	if plan.Bindings[0].Retired {
		t.Fatal("mutated input snapshot")
	}
	facts["a"] = g.CatalogGroup{MemberCount: 0, ObservedAt: &now}
	n = 200
	if again := EvaluateInvitation(out, facts, now); again.CurrentChatID != "b" {
		t.Fatal("returned to retired group")
	}
}
func TestInvitationFullAppendStaleAndPause(t *testing.T) {
	now := time.Now()
	n := 1
	plan := p.InvitationPlan{Mode: "sequence", Enabled: true, Threshold: &n, Bindings: []p.InvitationBinding{{ChatID: "a", CodeState: "executed", QRCode: "a"}}}
	facts := map[string]g.CatalogGroup{"a": {MemberCount: 1, ObservedAt: &now}}
	plan = EvaluateInvitation(plan, facts, now)
	if plan.State != "full" {
		t.Fatal(plan)
	}
	plan.Bindings = append(plan.Bindings, p.InvitationBinding{ChatID: "b", CodeState: "executed", QRCode: "b"})
	facts["b"] = g.CatalogGroup{ObservedAt: &now}
	plan = EvaluateInvitation(plan, facts, now)
	if plan.CurrentChatID != "b" {
		t.Fatal(plan)
	}
	plan = EvaluateInvitation(plan, facts, now.Add(11*time.Minute))
	if plan.State != "stale" || plan.CurrentChatID != "" {
		t.Fatal(plan)
	}
	plan.Enabled = false
	if EvaluateInvitation(plan, facts, now).State != "paused" {
		t.Fatal("pause ignored")
	}
}
func TestInvitationSingleDoesNotRetire(t *testing.T) {
	now := time.Now()
	plan := p.InvitationPlan{Mode: "single", Enabled: true, Bindings: []p.InvitationBinding{{ChatID: "a", CodeState: "executed", QRCode: "a"}}}
	facts := map[string]g.CatalogGroup{"a": {MemberCount: 200, ObservedAt: &now}}
	out := EvaluateInvitation(plan, facts, now)
	if out.State != "full" || out.Bindings[0].Retired {
		t.Fatal(out)
	}
}
