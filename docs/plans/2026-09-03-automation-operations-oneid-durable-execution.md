# Automation Operations OneID and Durable Execution Implementation Plan

## 分类结论

本能力涉及客户、外部身份、内部持久任务和 Provider 写。读取 canonical Customer/Identity Port；River 承担刷新与成员事件；企微写只经 Outbound/EER；相关本地事实同一 PostgreSQL UoW。禁止隐式建客、跨域表访问、自建队列和 Provider writer。

## 实施基线

- 分支：从实施时最新 main 建 `codex/automation-operations-v3` 独立工作区。
- 供体：`AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`，只读。
- 迁移编号：实施时扫描最大编号顺延。
- Provider：AO08 探针前始终 disabled。

## AO00 冻结供体与契约

- 固化页面、控制器、API、按钮状态和 donor SHA；记录 v2 群发按钮受阻与 Provider disabled。
- 冻结 Segment/Automation/Identity/Access/Outbound Ports、版本化事件、OpenAPI 和黄金 Journey。
- 门：donor manifest hash、契约测试和 OpenAPI 镜像一致。

## AO01 人群本地配置

- Segment 拥有 group/package/config version/receipt/audit/outbox。
- 实现创建、复制、暂停、激活预检、归档、固定模板和闭集 AST。
- 所有写入使用 Session/RBAC/CSRF/idempotency/CAS，同 UoW 提交事实。

## AO02 OneID 快照

- Compiler 只经 Port 读取事实并输出 canonical IDs。
- Preview 返回 count/digests/watermark/identity buckets。
- River 分批 staging，完整性校验后原子切换 published snapshot。
- 签名 Webhook 验签、nonce 防重放并走 Identity Resolve。
- 门：重复、并发、重启、损坏批次、旧快照和 100,000 容量测试。

## AO03 内容与发送人

- 复用 Automation published agent/version/digests，不复制内容模型。
- 只保存内部 staff ID 与 eligibility version；执行时解析 provider userid。
- 首版只执行 active fixed_script，所有不支持材料 fail closed。

## AO04 策略、Enrollment 与运行

- 建 policy version、unique enrollment、action snapshot、manual preview/run/recipient。
- `audience.member_entered.v1` 开启；tag trigger disabled。
- 人工群发 preview/confirm 双重校验 snapshot/content/sender/package digests。
- Quiet hours 和 single-run limit 是版本化策略。

## AO05 Outbound/EER

- 同一 UoW 冻结 run/recipient/intent/effect binding。
- 事务外执行 Provider；状态区分 accepted/queued/attempted/provider_accepted/delivery_proven/unknown/reconciled。
- unknown 禁止换键重试，只能原 effect、可信回调或 generation/fence/evidence 对账。

## AO06 前端

- 单 webshell 挂载列表和详情；donor 字节冻结，v3 Adapter 调真实 API。
- 删除 Mock/sessionStorage/hardcoded member。
- 显示 loading/empty/stale/conflict/forbidden/not-ready/unknown/reconciliation。

## AO07 v2 数据迁移

- read-only inspect 后生成 0600 AES-256-GCM snapshot。
- dry-run 后按 group/config/agent/sender/snapshot/policy/history 导入。
- 成员逐行进入 OneID disposition；active 以 paused 导入。
- 历史只读不可重放；完整 replay 零新增、零 River/EER。
- reconcile 证明计数等式和副作用计数不变。

## AO08 Shadow、探针与切流

- 使用同一加密快照和 reference time 比较配置、映射数、canonical member count/digest。
- 自动化运营 Provider 使用独立 disabled/probe/limited 门；probe 强制一人。
- 探针记录 run/recipient/effect/receipt/final reconciliation；未知立即停止。
- 经审批设置 limited cap，逐步激活导入包；禁止 v2/v3 双主写。

## 验证命令

```bash
make check
bash scripts/check-automationops-donor-manifest.sh
node scripts/validate-openapi.mjs
node --test internal/webshell/static/admin_console/automation_create_code_adapter.test.mjs
go test -race ./internal/segment/... ./internal/automation/... ./internal/outbound/... ./internal/externaleffects/... ./cmd/migrate-automation-operations ./cmd/aicrm
```

生产迁移依次执行 `inspect → extract → validate → dry-run → apply → replay-check → reconcile → shadow`。任一步有可写源凭据、schema drift、身份错配、计数漂移、EER/River 副作用或 unknown，立即停止。

## 完成证据

必须记录：donor SHA、branch/base、migration 编号、测试结果、PR/merge SHA、部署 run、live release SHA、真实 route、capability readiness、迁移 batch/shadow 报告，以及经授权探针的 Provider receipt 和对账结果。HTTP 200、queued、CI green 或 merged PR 不单独构成完成。
