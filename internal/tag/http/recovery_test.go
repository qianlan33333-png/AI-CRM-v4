package http

import (
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
	"testing"
)

func TestRecoveryReplayReportsCurrentTerminalState(t *testing.T) {
	for state, want := range map[effectport.State]string{
		effectport.StateQueued:      "原企微任务等待执行",
		effectport.StateExecuted:    "原企微任务已完成",
		effectport.StateUnknown:     "原企微任务结果待核对，请勿重复创建",
		effectport.StateFinalFailed: "原企微任务尚未完成，请查看当前失败状态",
		effectport.StateRetryable:   "原企微任务尚未完成，请查看当前失败状态",
	} {
		if got := recoveryStateMessage(state); got != want {
			t.Errorf("state=%s message=%q", state, got)
		}
	}
}
