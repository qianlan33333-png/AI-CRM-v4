# AI-CRM v3 AI 助手 PRD：OneID 审阅计划与持久执行闭环

> 状态：Approved for implementation
> 日期：2026-09-04
> donor：`qianlan33333-png/AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`
> 范围：AI 助手计划列表、计划详情及其真实后端；不含 Campaign、Observability、自动化运营和旧历史迁移

## 1. 开发分类

```text
OneID: involved — reads canonical customers.id and resolves verified scoped identities; no Provision/Bind/Merge
Persistence: local PostgreSQL UoW + River durable job + Provider read + Provider write/external effect
External Effects: involved — whole-plan approval atomically accepts Outbound private-message effects
```

## 2. 产品目标

- 等价迁移 donor 的一级计划列表和二级计划详情，包括统计、搜索、筛选、50 人分页、人员抽屉、消息卡、内容编辑、素材预览、逐人审阅和整单审批。
- 前端 donor 文件 byte-frozen；v3 Host Adapter 只替换模板注入、DTO、身份显示、认证和执行接口。
- 所有 recipient 使用 canonical `customers.id`。外部身份仅可由可信 Adapter 构造 scoped Reference 后调用 Identity Resolve。
- 逐人通过只保存决定；整单确认是唯一发送闸门。
- 计划、内容、审批、Outbound intent、External Effect、幂等收据、审计和 Outbox 可追溯且可恢复。
- `accepted/queued/attempted/provider_accepted/outcome_unknown/reconciled/final_failed/delivery_proven` 分层展示。

## 3. 页面需求

### 一级页 `/admin/cloud-orchestrator/plans`

- 保持 donor 页头、面包屑、统计卡、搜索框、筛选器、列表、空态、加载态和错误态。
- 搜索计划名称，不搜索或回显原始 external userid。逐人发送人只在详情安全投影中展示。
- 列表保持 donor 的计划名称、创建者、更新时间、目标数、审阅状态和执行摘要。
- 后端采用 cursor pagination，最大 50；刷新只读投影，不创建任务或效果。

### 二级页 `/admin/cloud-orchestrator/plans/{plan_id}`

- 展示计划元数据、版本、审阅和执行状态。
- recipient 每页 50 条；人员抽屉展示客户名称、OneID、内部归属、资格、消息内容和效果状态。
- pending 内容可编辑；已锁定或 attempted 后只读。
- 逐人通过/驳回不发送；整单确认对 eligible 且未驳回目标原子创建效果。
- 输入版本漂移返回 409，前端要求刷新并重新确认。
- UI 不得把 queued 或 Provider accepted 表述为已送达。

## 4. 领域和数据边界

`internal/aiassistant` 独占 plan、recipient、content version、review decision、effect binding、operation receipt、audit 和 outbox 表。AI Assistant Store 只能访问本领域表；Customer、Identity、Staff、Media、Outbound 和 External Effects 通过稳定 Port 协作。

表和结构化日志不得保存手机号、OpenID、UnionID、`external_userid`、企微 sender userid、Secret 或 Provider 原始响应。内部接入只接受 canonical Customer ID；外部接入的 not-found/conflict/invalid 只计入 disposition，不进入可发送目标。

## 5. 状态和原子性

```text
plan: draft → pending_review → partially_approved → approved → dispatching → completed
                              ↘ rejected
dispatching → needs_attention | completed_with_failures | completed

recipient: pending_review → approved | rejected | ineligible
approved → accepted → queued → attempted → provider_accepted
attempted → retryable_failed | final_failed | outcome_unknown
outcome_unknown → reconciled
provider_accepted → delivery_proven (only with trusted evidence)
```

整单审批在一个 UoW 内提交计划/recipient 锁定、冻结内容/素材/发送人政策、Outbound intents、External Effect 接受、River jobs、effect bindings、幂等收据、审计和 Outbox。任一步失败全部回滚；Provider 调用只能在提交后发生；unknown 禁止换键重试。

## 6. API 与安全

- `GET/POST /api/admin/ai-assistant/plans`
- `GET /api/admin/ai-assistant/plans/{plan_id}`
- `GET /api/admin/ai-assistant/plans/{plan_id}/recipients`
- `GET /api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}`
- `PATCH /api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}/content`
- `POST /api/admin/ai-assistant/plans/{plan_id}/recipients/{recipient_id}/review`
- `POST /api/admin/ai-assistant/plans/{plan_id}/preview-approval`
- `POST /api/admin/ai-assistant/plans/{plan_id}/approve`
- `POST /api/admin/ai-assistant/plans/{plan_id}/reject`
- `GET /api/admin/ai-assistant/plans/{plan_id}/effects`
- `POST /api/admin/ai-assistant/effects/{effect_id}/reconcile`
- `POST /api/integrations/ai-assistant/review-plans`

管理员写接口要求服务端 actor、RBAC、same-origin、CSRF、`Idempotency-Key` 和 `expected_version`。机器接入要求机器身份、HMAC、timestamp、nonce 和重放保护。

## 7. 完成标准

- 单计划上限 5,000 recipients；单 recipient 上限 20 个消息步骤。
- 1/50/51/5,000 人、Identity found/not-found/conflict/wrong-scope、CAS、重放、故障注入、worker 重启、unknown 对账和 PII 扫描通过。
- donor SHA、DOM、视觉、抽屉、分页、加载/空态/错误态合同通过。
- Provider 默认 disabled；未配置时失败关闭。
- 本期不读取或导入 donor 历史数据，不建立旧库运行依赖，不双写。
