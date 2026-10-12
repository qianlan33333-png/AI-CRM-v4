package adminops

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	effectport "github.com/qianlan33333-png/AI-CRM-v3/internal/externaleffects/port"
)

type humanFinding struct{ urgent, verify, history string }

// humanCheck describes only bounded Owner counts. No provider payload, raw
// error, identity, or customer detail is interpolated into a notification.
func humanCheck(r opsport.CheckResult) humanFinding {
	m := r.Metrics
	f := humanFinding{}
	if r.Status == "unknown" || r.Status == "uncovered" || r.Status == "stale" {
		f.verify = r.Title + "：证据不足，需核实。"
		if r.Status == "stale" {
			f.verify = r.Title + "：采集已过期，当前状态不能确认。"
		}
		if r.ID == "runtime.external_monitor" {
			f.verify = "公网与整机监控尚未接入：整体故障仍缺少独立告警证据。"
		}
		if r.ID == "retention.releases" {
			f.verify = "发布与回滚证据不完整：需核对当前版本的正式收据。"
		}
		if r.ID == "payment.order_consistency" {
			f.verify = "支付与履约证据不完整：需核实，不能据此认定付款或履约完成。"
		}
		return f
	}
	switch r.ID {
	case "archive.ingestion":
		if m["ingestion_blocked"] > 0 {
			f.urgent = fmt.Sprintf("会话存档处理失败：聊天记录可能停更，距上次成功约 %d 小时。请恢复同步并核对新增入库。", m["last_success_age_seconds"]/3600)
		}
	case "automation.runs":
		if n := m["runs_overdue"] + m["runs_partial_failed"] + m["runs_unknown"]; n > 0 {
			f.urgent = fmt.Sprintf("自动化有 %d 条异常运行：检查执行失败或未知结果，避免重复发送。", n)
		}
		if n := m["runs_awaiting_delivery"]; n > 0 {
			f.history = fmt.Sprintf("%d 条自动化发送任务已被企微接受，等待员工发送/送达证据；不计入系统执行超时。", n)
		}
	case "groupops.catalog":
		if n := m["scans_overdue"]; n > 0 {
			f.urgent = fmt.Sprintf("客户群目录有 %d 次扫描超时：需恢复同步。", n)
		}
		if n := m["groups_unreadable"]; n > 0 {
			f.verify = fmt.Sprintf("%d 个客户群详情不可读：请核对企微权限范围与群状态；系统不反复重试永久拒绝。", n)
		}
	case "jobqueue.backlog":
		if n := m["due_over_10m"]; n > 0 {
			f.urgent = fmt.Sprintf("后台任务有 %d 条到期未执行的积压：会延迟业务执行，请检查 Worker。", n)
		}
		if n := m["running_over_10m"]; n > 0 {
			f.verify = fmt.Sprintf("后台有 %d 条任务运行超过 10 分钟：需核对执行进度，运行时长本身不能证明失败。", n)
		}
		if n := m["discarded_retained"]; n > 0 {
			f.history = fmt.Sprintf("后台保留 %d 条历史终止任务，不代表当前积压。", n)
		}
	case "platform.callbacks":
		if n := m["due_over_10m"] + m["expired_processing_leases"] + m["failed_last_hour"]; n > 0 {
			f.urgent = fmt.Sprintf("回调有 %d 条近期失败或超时：请核对对应业务是否处理完成。", n)
		}
		if n := m["failed"]; n > 0 {
			f.history = fmt.Sprintf("回调累计保留 %d 条失败记录，不按累计数判断当前故障。", n)
		}
	case "effects.outcomes":
		if n := m["queued_overdue"] + m["lease_expired"] + m["retryable_last_hour"] + m["final_failed_last_hour"]; n > 0 {
			f.urgent = fmt.Sprintf("外部动作有 %d 条近期失败或超时：按原收据核对，不能盲目重试。", n)
		}
		if n := m["unknown"]; n > 0 {
			f.verify = fmt.Sprintf("%d 条外部动作结果仍未知：需 Provider 对账，尤其资金动作不能直接重放。", n)
		}
		if n := m["retryable"] + m["final_failed"]; n > 0 {
			f.history = fmt.Sprintf("外部动作累计保留 %d 条失败/重试记录；近期失败单独判断。", n)
		}
	case "payment.recovery":
		if n := m["refund_unknown"] + m["refund_failed"] + m["refund_overdue"] + m["prepay_recent_overdue"]; n > 0 {
			f.urgent = fmt.Sprintf("支付/退款有 %d 条待恢复异常：先核实 Provider 结果再处理。", n)
		}
		if n := m["prepay_overdue"] - m["prepay_recent_overdue"]; n > 0 {
			f.history = fmt.Sprintf("%d 条旧预支付记录待核对，不代表已有扣款或退款失败。", n)
		}
	case "outbound.delivery":
		if n := m["message_failed"] + m["message_overdue"] + m["message_unknown"] + m["material_current_unknown"]; n > 0 {
			f.urgent = fmt.Sprintf("消息/当前素材有 %d 条异常：请核对发送回执，避免重复发送。", n)
		}
		if n := m["material_unknown"] - m["material_current_unknown"]; n > 0 {
			f.history = fmt.Sprintf("%d 条旧素材上传未知记录，不影响当前素材的状态判断。", n)
		}
	case "wecom.sync":
		if n := m["sync_current_failed"] + m["staff_current_failed"] + m["sync_overdue"]; n > 0 {
			f.urgent = fmt.Sprintf("企微最新同步有 %d 项失败或超时：客户/员工数据可能未更新。", n)
		}
		if n := m["sync_failed"] + m["staff_refresh_failed"]; n > 0 {
			f.history = fmt.Sprintf("企微保留 %d 条历史同步失败；按最新同步结果判断健康。", n)
		}
	case "segment.refresh":
		if n := m["refresh_current_failed"] + m["refresh_overdue"]; n > 0 {
			f.urgent = fmt.Sprintf("人群快照有 %d 项当前失败或超时：运营名单可能未更新。", n)
		}
		if n := m["refresh_failed"]; n > 0 {
			f.history = fmt.Sprintf("人群保留 %d 条历史失败/版本替代记录；版本替代不算系统故障。", n)
		}
	case "identity.conflicts":
		if n := m["open_conflicts"] + m["open_merge_candidates"] + m["source_conflicts"]; n > 0 {
			f.verify = fmt.Sprintf("身份归属有 %d 项待人工核实：保留冲突，不自动合并客户。", n)
		}
	case "customer.projection":
		if n := m["projection_stale"] + m["projection_conflicts"]; n > 0 {
			f.verify = fmt.Sprintf("%d 条客户目录数据待核实：可能陈旧或存在冲突。", n)
		}
	case "diagnostics.application_errors":
		if n := m["recorded_last_hour"]; n > 0 {
			f.verify = fmt.Sprintf("近 60 分钟记录 %d 次应用错误：需查看关联详情确认业务影响。", n)
		}
	}
	if r.Status == "critical" && f.urgent == "" {
		f.urgent = r.Title + "：严重异常，请立即核查业务影响。"
	}
	if r.Status == "warning" && f.urgent == "" && f.verify == "" && f.history == "" {
		f.verify = r.Title + "：存在异常观察，需核实业务影响。"
	}
	return f
}

func reportMessage(hour time.Time, o opsport.InspectionOverview, window *effectport.OpsWindowCounts, detailURL string) ([]byte, error) {
	urgent, verify, history := []string{}, []string{}, []string{}
	for _, r := range o.Checks {
		f := humanCheck(r)
		if f.urgent != "" {
			urgent = append(urgent, f.urgent)
		}
		if f.verify != "" {
			verify = append(verify, f.verify)
		}
		if f.history != "" {
			history = append(history, f.history)
		}
	}
	if !o.Fresh || len(o.Checks) == 0 {
		verify = append(verify, "巡查数据尚未取得或已过期，不能据此判定系统正常。")
	}
	if window == nil {
		verify = append(verify, "本报告窗口的外部执行统计未取得；不使用其他时间段替代。")
	}
	title, color := "当前未发现实际故障", "green"
	if len(verify) > 0 {
		title, color = fmt.Sprintf("需要核实 · %d 项待确认", len(verify)), "orange"
	}
	if len(urgent) > 0 {
		title, color = fmt.Sprintf("需要处理 · %d 项实际问题", len(urgent)), "red"
	}
	zone := time.FixedZone("CST", 8*3600)
	var b strings.Builder
	fmt.Fprintf(&b, "**实际问题 %d 项 · 待核实 %d 项**\n", len(urgent), len(verify))
	if len(urgent) == 0 && len(verify) == 0 {
		b.WriteString("已采集范围未见系统执行阻塞，无需立即处理。\n")
	} else if len(urgent) == 0 {
		b.WriteString("下列事项仍待核实，暂不能判定整体正常。\n")
	}
	if len(urgent) > 0 {
		b.WriteString("\n**优先处理**\n")
		writeFindings(&b, urgent)
	}
	if len(verify) > 0 {
		b.WriteString("\n**需要核实**\n")
		writeFindings(&b, verify)
	}
	if len(history) > 0 {
		b.WriteString("\n**历史记录与等待状态**\n")
		writeFindings(&b, history)
	}
	fmt.Fprintf(&b, "\n统计窗口：%s—%s（北京时间）\n", hour.In(zone).Format("01/02 15:04"), hour.Add(time.Hour).In(zone).Format("15:04"))
	if window != nil {
		fmt.Fprintf(&b, "该窗口外部动作：开始 %d 次，执行完成 %d 次（分别按开始/完成时间计数；不等同送达）。\n", window.Started, window.Executed)
	}
	fmt.Fprintf(&b, "当前采集：%s；详细参数与收据见治理看板。", o.ObservedAt.In(zone).Format("01/02 15:04"))
	return humanCard("CRM 运行巡查 · "+title, color, b.String(), detailURL)
}

func writeFindings(b *strings.Builder, items []string) {
	for _, item := range items {
		b.WriteString("- " + item + "\n")
	}
}

func humanCard(title, color, body, detailURL string) ([]byte, error) {
	elements := []any{map[string]any{"tag": "markdown", "content": body}}
	if detailURL != "" {
		elements = append(elements, map[string]any{"tag": "button", "text": map[string]string{"tag": "plain_text", "content": "查看治理详情"}, "type": "primary", "behaviors": []any{map[string]string{"type": "open_url", "default_url": detailURL}}})
	}
	raw, err := json.Marshal(map[string]any{"msg_type": "interactive", "card": map[string]any{"schema": "2.0", "header": map[string]any{"template": color, "title": map[string]string{"tag": "plain_text", "content": title}}, "body": map[string]any{"elements": elements}}})
	if err != nil {
		return nil, err
	}
	if len(raw) > 24000 {
		return nil, ErrInspectionInvalid
	}
	return raw, nil
}
