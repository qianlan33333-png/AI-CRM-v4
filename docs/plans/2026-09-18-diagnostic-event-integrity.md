# 请求扇出诊断完整性修复

业务判断：同一次页面操作可以接受多个后台任务。同一个请求关联号不能成为所有失败事件的唯一键；管理员应看到各任务及各次真实尝试的失败，同时原事件重放不能重复增加次数。已批准运行治理 PRD 的排查链要求适用于本次修复。

OneID：不涉及客户匹配、归属或身份写入。Persistence：AdminOps 拥有有期限的诊断事件；只扩展既有过程记录，不新增任务或 Provider 效果。继续使用原 River 尝试号，既不创建重试状态机，也不改变执行结果。

参考 [OpenTelemetry 日志模型](https://opentelemetry.io/docs/specs/otel/logs/data-model/) 中关联上下文与单条日志事件的区分，以及 [River v0.24.0 JobRow](https://github.com/riverqueue/river/blob/v0.24.0/rivertype/river_type.go) 的原生 Attempt：首次工作为1，真正错误重试递增，snooze不增加尝试。只复用模型，不安装额外平台。

## 固定规则

- 保留原请求关联号。事件键为组件、分类码、关联摘要、任务引用、效果引用、River尝试号；不使用时间或随机数制造新事件。
- `job_attempt=0` 表示无任务或旧记录未采集尝试号，正数来自当前 River JobRow；不得称为 Provider/EER 尝试号。禁止负数、超出32位整数，或没有任务引用却设置正数。
- 同一事件再次记录为无操作；不同任务、不同效果或不同真实任务尝试均保留。该过程明细仍仅保留720小时；UI/API暂不新增尝试号字段。
- 既有记录原值保留，由新迁移0192补默认0和新的明确唯一键；不改0186历史迁移。诊断按720小时照常清理，业务事实/收据不变。
- HTTP仅从已注册的`Request.Pattern`取静态模板，去除受支持的HTTP方法前缀，交给原Owner校验；非法或不存在的注册模板回退`/request`。绝不从URL、query或客户路径推导模板。
- 记录器不可改变HTTP响应、River重试或panic处理语义；不记录错误原文、身份、Token、Cookie或请求正文。

不选“每个失败生成随机ID”，因为同一事件重放会膨胀；不选“每个任务换关联号”，因为会断开原请求链。首期只保存固定元数据，完整操作/收据链仍由后续Owner读Port补齐。

## 验收

真实PostgreSQL验证：同一关联号两个失败任务均存在；相同job/attempt重放不增；下一真实attempt保留；效果不同保留；旧记录迁移不丢；负数/越界/无job尝试号拒绝；29/30/31天在线可见性及清理，业务接受证据保持。真实HTTP middleware→AdminOps记录路径验证方法模板规范化与Secret不落库。平台/River专项验证attempt透传、snooze不记失败、原请求关联和其他metadata保留。最后依次fast、compile、受影响包PG/race专项；完整CI与生产验收由集成轮执行。
