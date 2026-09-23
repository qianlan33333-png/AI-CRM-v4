package adminops

import (
	opsport "github.com/qianlan33333-png/AI-CRM-v3/internal/adminops/port"
	"regexp"
	"time"
)

var safeInspectionCode = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,95}$`)

// InspectionCatalog describes coverage, not an assertion that every source is
// connected. Missing collectors always produce uncovered findings.
func InspectionCatalog() []opsport.CheckDefinition {
	defs := []opsport.CheckDefinition{
		{ID: "inspection.self", Owner: "adminops", Title: "巡查覆盖与小时通知自检"},
		{ID: "runtime.endpoints", Owner: "runtime", Title: "关键入口与鉴权"},
		{ID: "runtime.resources", Owner: "runtime", Title: "数据库容量与等待"},
		{ID: "runtime.host", Owner: "runtime", Title: "主机磁盘与进程资源"},
		{ID: "runtime.external_monitor", Owner: "runtime", Title: "整机与公网外部监控"},
		{ID: "legacy.payment_callbacks", Owner: "payment", Title: "旧机未退役资金回调"},
		{ID: "jobqueue.backlog", Owner: "platform", Title: "持久任务积压与失败"},
		{ID: "jobqueue.workers", Owner: "platform", Title: "持久任务执行活性"},
		{ID: "platform.outbox", Owner: "platform", Title: "事件消费积压"},
		{ID: "platform.callbacks", Owner: "platform", Title: "回调处理与重放"},
		{ID: "identity.conflicts", Owner: "identity", Title: "身份冲突与合并候选"},
		{ID: "customer.projection", Owner: "customer", Title: "客户目录投影异常"},
		{ID: "config.drift", Owner: "config", Title: "发布配置与角色应用版本"},
		{ID: "effects.outcomes", Owner: "externaleffects", Title: "外部效果失败与未知结果"},
		{ID: "payment.recovery", Owner: "payment", Title: "支付退款待恢复状态"},
		{ID: "payment.order_consistency", Owner: "payment", Title: "支付订单与履约一致性"},
		{ID: "outbound.delivery", Owner: "outbound", Title: "消息发送与回执"},
		{ID: "wecom.sync", Owner: "wecom", Title: "企业微信同步任务"},
		{ID: "segment.refresh", Owner: "segment", Title: "人群快照刷新"},
		{ID: "automation.runs", Owner: "automation", Title: "自动化运行与未知结果"},
		{ID: "survey.submissions", Owner: "survey", Title: "问卷提交业务不变量"},
		{ID: "media.storage", Owner: "media", Title: "素材与上传存储"},
		{ID: "operationcycle.actions", Owner: "operationcycle", Title: "运营动作待处理"},
		{ID: "diagnostics.application_errors", Owner: "adminops", Title: "关联应用错误"},
		{ID: "retention.host", Owner: "runtime", Title: "宿主日志和过程文件清理", MaxAge: 75 * time.Minute},
		{ID: "retention.releases", Owner: "runtime", Title: "发布包清理与回滚保护"},
		{ID: "retention.lifecycle", Owner: "adminops", Title: "数据生命周期清理"},
	}
	scopes := map[string]string{
		"inspection.self":                "当前目录与最近完成检查数量、75分钟采集新鲜度；每小时:05执行一次持久扫描并为上一完整小时生成唯一报告，生成宽限2分钟；未决发送超过10分钟、unknown及近24小时最终失败；禁用通知为未覆盖，executed不等于送达证明",
		"runtime.endpoints":              "本机API健康、准备状态、版本与未登录鉴权边界；登录后业务操作由完整浏览器旅程验证，不据此声称公网可达",
		"runtime.host":                   "根文件系统容量与当前Worker进程堆和协程；不代表整机内存和公网可达",
		"runtime.external_monitor":       "腾讯云已有免费外部监控的配置和送达证据；未接入不得声称覆盖整机故障",
		"legacy.payment_callbacks":       "旧机仅残留资金回调；需旧端点只读可信事实，未接入显示未覆盖",
		"runtime.resources":              "接入的主机与数据库资源阈值；具体采样项见metrics",
		"jobqueue.backlog":               "已到期任务的状态、最老年龄与失败；未来预约不算积压",
		"jobqueue.workers":               "Worker活性与最近执行证据；进程启动记录不等于持续存活",
		"platform.outbox":                "仅声明消费义务的事件积压；审计性事件不算未消费",
		"platform.callbacks":             "回调本地持久状态；不据零回调推断Provider可达",
		"identity.conflicts":             "现存open冲突、merge candidate、source conflict计数；不自动合并",
		"customer.projection":            "目录自身conflict/stale状态计数；不证明与全部业务源一致",
		"config.drift":                   "API与常驻effects-worker最后应用revision和发布SHA一致性；一次性worker不当作常驻进程，活性由队列另查",
		"effects.outcomes":               "EER本地状态及年龄；executed不代表业务送达",
		"payment.recovery":               "退款unknown/final_failed、超过一小时待退款和待预支付",
		"payment.order_consistency":      "同一只读快照比对原生已付Payment与Order金额、身份、退款及服务期收据；不含历史、普通商品外部交付和反向孤立订单；证据不足为unknown",
		"outbound.delivery":              "私信unknown/final_failed、旧attempted与素材unknown；不覆盖所有消息类型",
		"wecom.sync":                     "客户同步失败/旧运行与员工目录失败；不验证当前Provider网络",
		"segment.refresh":                "人群刷新failed和超过一小时未完成；不验证DSL业务口径",
		"automation.runs":                "unknown/partial_failed及超过一小时executing；ready审批不报积压",
		"survey.submissions":             "提交claim缺失、claim归属和定义版本不一致、24小时身份冲突",
		"media.storage":                  "过期30天上传分片数量/字节；不证明正式素材可读或外部存储健康",
		"operationcycle.actions":         "failed和超过一小时未结束动作；不验证策略业务效果",
		"diagnostics.application_errors": "仅已接入诊断写口的分类错误和关联摘要；不代表全量异常捕获",
		"retention.host":                 "固定宿主清理执行的结果与新鲜度；业务文件与备份受保护",
		"retention.releases":             "校验当前安装SHA对应发布清理证据；缺少两个兼容回滚版本或引用不明时保护并报告，不当作清理成功",
		"retention.lifecycle":            "Owner清理任务结果与新鲜度；业务永久数据不参与清除",
	}
	for i := range defs {
		defs[i].Scope = scopes[defs[i].ID]
		if defs[i].MaxAge == 0 {
			defs[i].MaxAge = 75 * time.Minute
		}
	}
	return defs
}

func normalizeObservation(o opsport.CheckObservation, def opsport.CheckDefinition, now time.Time) opsport.CheckObservation {
	if !safeInspectionCode.MatchString(o.Code) {
		o.Code = "invalid_observation"
		o.Status = "unknown"
	}
	switch o.Status {
	case "ok", "warning", "critical", "unknown", "uncovered", "stale":
	default:
		o.Status = "unknown"
		o.Code = "invalid_status"
	}
	if o.ObservedAt.IsZero() || o.ObservedAt.After(now.Add(time.Minute)) {
		o.Status = "unknown"
		o.Code = "invalid_observed_at"
	} else if now.Sub(o.ObservedAt) > def.MaxAge && o.Status != "uncovered" {
		o.Status = "stale"
		o.Code = "stale_observation"
	}
	safe := map[string]int64{}
	if len(o.Metrics) > 32 {
		o.Status = "unknown"
		o.Code = "invalid_metrics"
	} else {
		for k, v := range o.Metrics {
			if !safeInspectionCode.MatchString(k) || v < 0 {
				o.Status = "unknown"
				o.Code = "invalid_metrics"
				safe = map[string]int64{}
				break
			}
			safe[k] = v
		}
	}
	o.Metrics = safe
	return o
}
