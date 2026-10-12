package adminops

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

func inspectHumanCard(t *testing.T, raw []byte) (color, title, body string) {
	t.Helper()
	var card struct {
		MsgType string `json:"msg_type"`
		Card    struct {
			Schema string `json:"schema"`
			Header struct {
				Template string `json:"template"`
				Title    struct {
					Content string `json:"content"`
				} `json:"title"`
			} `json:"header"`
			Body struct {
				Elements []struct {
					Tag     string `json:"tag"`
					Content string `json:"content"`
				} `json:"elements"`
			} `json:"body"`
		} `json:"card"`
	}
	if err := json.Unmarshal(raw, &card); err != nil {
		t.Fatal(err)
	}
	if card.MsgType != "interactive" || card.Card.Schema != "2.0" {
		t.Fatal("not native Feishu card")
	}
	for _, element := range card.Card.Body.Elements {
		if element.Tag == "markdown" {
			body += element.Content
		}
	}
	return card.Card.Header.Template, card.Card.Header.Title.Content, body
}

func TestHumanReportActualFaultAndHistorySeparation(t *testing.T) {
	now := time.Now().UTC()
	o := opsport.InspectionOverview{Fresh: true, ObservedAt: now, Checks: []opsport.CheckResult{
		{CheckDefinition: opsport.CheckDefinition{ID: "archive.ingestion", Title: "会话存档持续入库"}, CheckObservation: opsport.CheckObservation{Status: "critical", Metrics: map[string]int64{"ingestion_blocked": 1, "last_success_age_seconds": 7200}}},
		{CheckDefinition: opsport.CheckDefinition{ID: "effects.outcomes"}, CheckObservation: opsport.CheckObservation{Status: "warning", Metrics: map[string]int64{"retryable": 6154}}},
		{CheckDefinition: opsport.CheckDefinition{ID: "automation.runs"}, CheckObservation: opsport.CheckObservation{Status: "ok", Metrics: map[string]int64{"runs_awaiting_delivery": 1264}}},
	}}
	raw, err := reportMessage(now, o, &effectport.OpsWindowCounts{Started: 7, Executed: 7}, "https://fixture.invalid/admin/ops")
	if err != nil {
		t.Fatal(err)
	}
	color, title, body := inspectHumanCard(t, raw)
	if color != "red" || !strings.Contains(title, "需要处理 · 1 项实际问题") || !strings.Contains(body, "会话存档处理失败") || !strings.Contains(body, "等待员工发送/送达证据") {
		t.Fatalf("wrong human summary: %s %s %s", color, title, body)
	}
	if strings.Contains(body, "retryable=") || strings.Contains(body, "catalog_checks") || strings.Contains(body, "1264 条异常运行") {
		t.Fatal("historical parameters treated as current fault")
	}
}

func TestHumanReportHistoricalFailuresDoNotMakeRed(t *testing.T) {
	now := time.Now().UTC()
	o := opsport.InspectionOverview{Fresh: true, ObservedAt: now, Checks: []opsport.CheckResult{
		{CheckDefinition: opsport.CheckDefinition{ID: "jobqueue.backlog"}, CheckObservation: opsport.CheckObservation{Status: "warning", Metrics: map[string]int64{"discarded_retained": 248}}},
		{CheckDefinition: opsport.CheckDefinition{ID: "segment.refresh"}, CheckObservation: opsport.CheckObservation{Status: "warning", Metrics: map[string]int64{"refresh_failed": 496}}},
	}}
	raw, err := reportMessage(now, o, &effectport.OpsWindowCounts{}, "")
	if err != nil {
		t.Fatal(err)
	}
	color, title, body := inspectHumanCard(t, raw)
	if color != "green" || !strings.Contains(title, "未发现实际故障") || !strings.Contains(body, "历史记录与等待状态") {
		t.Fatal("historical failures masked healthy execution")
	}
}

func TestHumanReportUnknownCannotTurnGreen(t *testing.T) {
	now := time.Now().UTC()
	for _, status := range []string{"unknown", "uncovered", "stale"} {
		o := opsport.InspectionOverview{Fresh: true, ObservedAt: now, Checks: []opsport.CheckResult{{CheckDefinition: opsport.CheckDefinition{ID: "runtime.external_monitor", Title: "外部监控"}, CheckObservation: opsport.CheckObservation{Status: status}}}}
		raw, err := reportMessage(now, o, &effectport.OpsWindowCounts{}, "")
		if err != nil {
			t.Fatal(err)
		}
		color, title, body := inspectHumanCard(t, raw)
		if color != "orange" || !strings.Contains(title, "需要核实") || !strings.Contains(body, "暂不能判定整体正常") {
			t.Fatal("unknown became healthy")
		}
	}
	raw, err := reportMessage(now, opsport.InspectionOverview{}, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	color, _, body := inspectHumanCard(t, raw)
	if color != "orange" || !strings.Contains(body, "不使用其他时间段替代") {
		t.Fatal("unobserved window became zero or green")
	}
}

func TestHumanTransitionCardRecoveryAndCritical(t *testing.T) {
	for _, kind := range []string{"critical", "recovery"} {
		raw, err := transitionMessage(kind, 3, time.Now(), []inspectionTransition{{ID: 7, CheckID: "archive.ingestion", Code: "critical_invariant"}}, "https://fixture.invalid/admin/ops")
		if err != nil {
			t.Fatal(err)
		}
		color, title, body := inspectHumanCard(t, raw)
		want := "red"
		if kind == "recovery" {
			want = "green"
		}
		if color != want || !strings.Contains(title, "CRM 运行巡查") || !strings.Contains(body, "会话存档持续入库") {
			t.Fatal("transition lost human title or severity")
		}
	}
}
