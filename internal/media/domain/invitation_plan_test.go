package domain

import (
	g "github.com/qianlan33333-png/AI-CRM-v3/internal/groupops/port"
	p "github.com/qianlan33333-png/AI-CRM-v3/internal/media/port"
	w "github.com/qianlan33333-png/AI-CRM-v3/internal/wecom/port"
	"strings"
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

func TestNativeInvitationDelegatesFullAndStaleGroupsToWeCom(t *testing.T) {
	now := time.Now()
	plan := p.InvitationPlan{Mode: "native", Enabled: true, ProviderState: "executed", ProviderQRCode: "https://wework.qpic.cn/code", Bindings: []p.InvitationBinding{{ChatID: "a"}}}
	old := now.Add(-24 * time.Hour)
	for _, facts := range []map[string]g.CatalogGroup{nil, {"a": {MemberCount: 200, ObservedAt: &now}}, {"a": {MemberCount: 500, ObservedAt: &old}}} {
		out := EvaluateInvitation(plan, facts, now)
		if out.State != "active" || out.CurrentChatID != "" || out.Bindings[0].Retired {
			t.Fatalf("native allocation overridden: %+v", out)
		}
	}
	plan.ProviderState = "outcome_unknown"
	if out := EvaluateInvitation(plan, nil, now); out.State != "preparing" {
		t.Fatal(out)
	}
	plan.Enabled = false
	if out := EvaluateInvitation(plan, nil, now); out.State != "paused" {
		t.Fatal(out)
	}
}

func TestNativeInvitationOfficialParameterBoundaries(t *testing.T) {
	good := p.InvitationInput{Name: "原生", Title: "入群", Mode: "native", ChatIDs: []string{"a", "b", "c", "d", "e"}, NativeOptions: &w.InvitationJoinWayOptions{AutoCreateRoom: true, RoomBaseName: strings.Repeat("群", 40), Remark: strings.Repeat("备", 30), State: strings.Repeat("渠", 30)}}
	if err := ValidateInvitationInput(good); err != nil {
		t.Fatal("valid UTF8 params", err)
	}
	tests := []p.InvitationInput{good, good, good, good, good}
	tests[0].ChatIDs = append(append([]string(nil), good.ChatIDs...), "f")
	tests[1].NativeOptions = nil
	for i := 2; i < 5; i++ {
		o := *good.NativeOptions
		tests[i].NativeOptions = &o
	}
	tests[2].NativeOptions.RoomBaseName += "群"
	tests[3].NativeOptions.Remark += "备"
	tests[4].NativeOptions.State += "渠"
	for i, v := range tests {
		if ValidateInvitationInput(v) == nil {
			t.Fatalf("invalid native case %d accepted", i)
		}
	}
}
