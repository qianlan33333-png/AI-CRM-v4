# 渠道码中心 Behavior Contract

Baseline: `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`。

V1 semantic reference: `AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`。V2 仍是两张活动模板的字节冻结供体；V1 只用于用户数、二维码选择、场景别名、客服、欢迎内容和入渠动作的行为取证，不是运行依赖或代码供体。

本文件在 v3 后端重写前冻结用户可观察行为。兼容表示保留交互意图、校验结果和安全响应语义；不允许复制 donor 的身份匹配、数据库所有权、Provider 执行、重试状态机或 migration 历史。

## 路由清单

| 领域 | 方法 | 路径 |
|---|---|---|
| 页面 | GET | `/admin/channels` |
| 渠道 | GET, POST | `/api/admin/channels` |
| 渠道 | GET, PATCH | `/api/admin/channels/{channel_id}` |
| 历史 | GET | `/api/admin/channels/{channel_id}/history` |
| 近期用户 | GET | `/api/admin/channels/{channel_id}/contacts` |
| 发布预览 | GET | `/api/admin/channels/{channel_id}/acquisition-preview` |
| 客服分配 | PUT | `/api/admin/channels/{channel_id}/assignees` |
| 客服候选 | GET | `/api/admin/channels/{channel_id}/acquisition-staff` |
| 渠道资产 | GET, POST | `/api/admin/channels/{channel_id}/acquisition-assets` |
| 资产详情与二维码维护 | GET, PATCH, DELETE | `/api/admin/channels/{channel_id}/acquisition-assets/{effect_id}` |
| 资产对账 | POST | `/api/admin/channels/{channel_id}/acquisition-assets/{effect_id}/reconcile` |
| 二维码兼容 | POST | `/api/admin/channels/{channel_id}/qrcode/generate` |
| 二维码下载 | GET | `/api/admin/channels/{channel_id}/qrcode/download` |
| 获客链接 | GET, POST | `/api/admin/wecom-customer-acquisition-links` |
| 获客链接 | GET, PATCH, DELETE | `/api/admin/wecom-customer-acquisition-links/{link_id}` |
| 获客链接对账 | POST | `/api/admin/wecom-customer-acquisition-links/{link_id}/reconcile` |
| 归因收据 | GET | `/api/admin/channels/{channel_id}/acquisition-entrant-receipts` |
| 归因收据 | GET | `/api/admin/channels/{channel_id}/acquisition-entrant-receipts/{receipt_id}` |
| 归因修正 | POST | `/api/admin/channels/{channel_id}/acquisition-entrant-receipts/{receipt_id}/reconcile` |
| 未归属收据 | GET | `/api/admin/channel-acquisition-entrant-receipts/unassigned` |
| 未归属收据 | GET | `/api/admin/channel-acquisition-entrant-receipts/unassigned/{receipt_id}` |
| 未归属修正 | POST | `/api/admin/channel-acquisition-entrant-receipts/unassigned/{receipt_id}/reconcile` |

## Journey 1：列表、创建和编辑

- 列表支持关键字、状态、渠道类型、稳定游标和准确总数；失败不得回退 Mock 或伪造空成功。
- 新建要求唯一不可变 `channel_code`、名称、类型和有效配置；成功返回真实持久化 ID 与版本。
- 编辑使用 CAS；版本冲突返回稳定冲突错误，不允许 last-write-wins。
- 启用、停用和归档保留历史。有渠道用户、资产或归因引用时禁止硬删除。
- Viewer 只读；Admin 可修改本地配置；Provider 写和人工对账只允许 SuperAdmin。

## Journey 2：客服分配

- 支持单负责人，或 1–5 名多客服。
- 多客服策略为按比例或 24 小时满额切换；比例、顺序、上限和溢出策略由服务端校验。
- 本地已保存客服始终可读；实时可发布候选是本地 active staff 与企微 follow-user 的交集。Provider read 失败时读取返回明确降级状态，保存客服和发布资产严格失败关闭，不沿用过期候选或用户自报 userid。
- 发布预览必须明确本地配置、阻塞项和 Provider 开关；本地保存成功不等于企微生效。

## Journey 3：欢迎内容、素材和标签

- 欢迎语文本和图片、小程序、附件、群邀请引用组成不可变配置版本。
- Media、Tag 的稳定 Port 校验引用存在、可用和类型匹配；Channel 不读写它们的表。
- 页面预览只显示受控素材投影，不接收 URL 代替素材引用。
- 入渠标签只保存稳定本地引用；企微联系人由 WeCom relationship Port 在执行时解析。

## Journey 4：二维码与获客链接

- 发布先冻结配置版本，再在同一 PostgreSQL UoW 中接受 External Effect；返回 `accepted/queued` 只表示已受理。
- 状态序列为 `accepted → queued → attempted → executed/outcome_unknown/final_failed → reconciled`。
- `accepted`、`queued`、`attempted`、`outcome_unknown` 和 `final_failed` 均不可打开、复制或下载。
- 只有具有受控结果的 `executed`，或已确认 provider applied 并投影为可用结果的 reconciliation，才可使用资产。
- 二维码只经 same-origin 下载端点；服务端限制目标域、重定向、MIME、大小和超时。
- `outcome_unknown` 只允许原 effect/idempotency scope 的 readback 或人工对账，禁止换 key 重试。

## Journey 5：扫码、OneID 和入渠动作

- raw State 仅在 WeCom callback 边界计算 HMAC；Channel Port 只接收摘要。
- State 0/1/N 匹配分别形成 `unmatched/attributed/ambiguous`，不得以最新资产或最新渠道猜测。
- verified external_userid 必须含 corp scope，并只经 Identity `Resolve`。只有 verified not-found 才显式 `ProvisionCustomerFromVerifiedIdentity`。
- OneID conflict 生成冲突收据，不在渠道页面强绑或合并。
- WelcomeCode 在 callback inbox 事务中密封为加密、短时、一次性 grant；Channel 只保存 opaque reference，Outbound 负责兑换。
- callback receipt、关系、归因、负责人、效果接受、审计和 Outbox 按命令需要在同一 PostgreSQL UoW。

## Journey 6：近期用户、历史与收据

- “渠道用户”是历史与实时事实合并后的唯一用户数；已解析事实按 canonical `customer_id` 去重，未解析历史事实按稳定 source contact 去重。累计进入次数单独求和，不能用事件数冒充用户数。
- 近期用户同时覆盖已导入历史联系人和 v3 回调；只返回 canonical `customer_id`、安全展示名、进入时间和安全统计，未解析历史用户显示安全占位。
- 历史联系人和客服允许展示 source row reference、canonical customer 关联、名称快照和时间，但不返回 raw UnionID、OpenID、external_userid、手机号或 State。
- 归因修正追加新收据，不改写旧证据；多候选、缺 scope、OneID conflict 都保持可审计 unresolved。
- 所有敏感管理端读取返回 `Cache-Control: no-store`；游标不可推断原始身份。

## 历史导入合同

- 只导入正式 snapshot timestamp 之前的渠道、客服、渠道联系人、分配事件和历史效果事实。
- 每个源行必须进入 `imported/already_imported/unresolved/quarantined/invalid` 之一；`silent_loss = 0`。
- 历史身份只使用 Identity Resolver；不调用 Provision、Attach、Link、Merge 或 identity store。
- 历史资产和效果永远 `effect_eligible=false`，不得创建 Job、External Effect 或 Provider 请求。
- 同快照重放不增量；有新运行时引用的回滚失败关闭。
- 本项目不切换旧系统、不停写、不双写、不持续同步；snapshot 之后的 source 数据不在范围内。

## 稳定错误族

`authentication_required`、`permission_denied`、`csrf_invalid`、`invalid_request`、`channel_not_found`、`channel_code_conflict`、`version_conflict`、`channel_referenced`、`staff_directory_unavailable`、`provider_disabled`、`effect_not_ready`、`outcome_unknown`、`identity_pending`、`identity_conflict`、`state_unmatched`、`state_ambiguous`、`migration_source_drift` 和 `migration_reconciliation_failed`。
