# ADR-0004：渠道码中心使用 OneID、Outbound 外部效果与一次性历史导入

## 状态

Accepted

## 背景

v3 已有 Channel State 摘要绑定和归因收据的基础实现，也包含与 `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e` 一致的渠道管理页面；但 `/api/admin/channels` 仍是只读空兼容投影，页面尚未连接完整 Channel PostgreSQL、资产发布、扫码归因和历史导入闭环。

donor 的渠道中心提供了列表、编辑、客服分配、欢迎语、素材、标签、二维码、企微获客链接和历史用户行为参考。直接搬运 donor 后端会同时引入旧客户主键、外部身份字段、领域私有 Provider 调用、队列和 migration 历史，与 v3 的 OneID 和 External Effects 边界冲突。

本项目还要求把 `150.158.82.186` 在正式快照时点之前的渠道相关历史数据一次性导入 v3。当前没有该主机的受限只读数据库凭据，因此本地设计和实现可以继续，生产 schema discovery、快照和正式导入必须保持阻塞。

## 开发前分类

```text
OneID: 涉及。扫码 external_userid 只接受 provider-verified corp scope，并只经 Identity Port Resolve/显式 Provision；历史导入只 Resolve。
Persistence: 涉及本地 PostgreSQL UoW、River 内部持久任务、Provider read 和 Provider write。
External Effects: 涉及。企微二维码、获客链接、欢迎语和标签写全部由 outbound 拥有并通过 External Effects 接受、执行和对账。
```

## 决策

1. Channel 在 v3 中重建为独立领域。v2 只作为行为、前端和叶子协议供体，不成为 module、submodule、运行依赖、正常数据源或远程服务。
2. `channels.html` 和 `channelForm.html` 保持供体字节不变；v3 Adapter、版本化 OpenAPI 和真实后端实现页面依赖。共享 donor 文件不得为 Channel 私自分叉。
3. Channel 拥有渠道定义、不可变配置版本、客服分配、渠道资产本地引用、幂等操作收据和归因收据。Channel Store 只能访问本领域表。
4. `customers.id` 是唯一渠道中立客户主键。Channel 不保存或匹配 raw OpenID、UnionID、external_userid、手机号或 State；外部身份只经 `internal/identity/port`。
5. raw State 只在 WeCom callback 边界计算 HMAC。Channel Port 只接收摘要；0、1、N 个匹配分别形成 unmatched、attributed、ambiguous，不按时间或资产猜测。
6. 只有带 corp scope 的 provider-verified external_userid 可以进入 Identity `Resolve`。只有 verified not-found 才调用显式 Provision；pending/conflict 保持收据，不自动 Attach、Link 或 Merge。
7. 客服候选通过 WeCom follow-user Provider read 与本地 active staff 交集产生。Provider read 失败时失败关闭，不回退用户自报或过期候选。
8. Channel 每次发布先冻结配置版本，再通过稳定 `OutboundAccepter` 在调用方 PostgreSQL 事务中接受 effect。Channel 只保存 opaque `effect_id` 和本地摘要，不跨域读写 Outbound 或 External Effects 表。
9. Outbound 是所有企微业务写的唯一 Owner。二维码、获客链接、欢迎语和入渠标签的 Provider 调用、结果载荷和 Provider 引用都由 Outbound 持有；External Effects 只保存 digest-only 状态机证据。
10. 状态严格区分 `accepted`、`queued`、`attempted`、`executed`、`outcome_unknown`、`final_failed` 和 `reconciled`。`outcome_unknown` 只允许原 effect/idempotency scope 的 Provider readback 或人工对账，禁止更换 key 重试。
11. Callback Inbox 在接收事务中把 WelcomeCode 加密为短时一次性 grant。Channel 只传递 opaque grant reference；Outbound 使用 WeCom `WelcomeGrantRedeemer` 以 CAS 一次性兑换，明文不进入日志、Channel、Outbound 或 EER 持久化。
12. 近期用户、历史联系人、历史客服和归因收据只提供 canonical customer、安全时间、统计和必要 source reference；不返回 raw 外部身份、手机号或 State。
13. 历史导入由 `cmd/migrate-channel-history` 独立完成，运行时包不得 import 迁移器。源数据来自 PostgreSQL 一致性只读快照，快照使用独立密钥 AES-256-GCM 加密并记录逐表 canonical digest。
14. 历史身份只调用 Identity Resolver。唯一可信命中关联 `customer_id`；缺 scope、未命中和冲突进入 unresolved/quarantined，不阻塞其他安全行，也不隐式建客。
15. 历史二维码、链接和效果日志仅作为 `effect_eligible=false` 的不可执行事实导入，不创建 River Job、External Effect 或 Provider 请求。
16. 正式导入是一次性 bounded snapshot。签字确认的 snapshot timestamp 是历史截止点；不暂停、不切换、不退役旧系统，不建设双写、CDC、delta catch-up 或持续同步。快照之后的旧系统数据不属于本项目。
17. 生产导入前必须完成受限只读 schema discovery、隔离 PG16 全量 rehearsal、目标备份/恢复点和 replay/rollback 演练。凭据缺失只阻塞这些生产动作，不阻塞 PR1–PR6 本地开发。

## 原子性边界

同一业务命令需要同时改变渠道配置、幂等收据、审计、Outbox 和 effect acceptance 时，所有记录必须加入同一个 PostgreSQL Unit of Work；两个独立事务不视为原子。Provider 网络调用不得持有数据库事务。

扫码命令涉及 callback receipt、relationship、渠道归因、负责人、入渠动作接受、审计和 Outbox 时，由 Composition Root 组合 transaction-aware Ports；任何模块不得跨领域写表来“补齐”原子性。

## 后果

### 正面

- 渠道用户归属复用唯一 OneID，不产生第二套客户主键或静默合并。
- 企微写入复用 Outbound、External Effects 和 River 的 lease/fence、重试和对账语义，不产生第二套执行内核。
- donor 页面体验可保留，同时后端、数据和权限遵循 v3 的 Owner/Port 边界。
- 历史资产不会被误当成可执行配置，正式导入可以重放、对账和受控回滚。

### 负面

- 需要 transaction-aware Channel、Identity、WeCom、Outbound、Media 和 Tag Adapter，组合复杂度高于直接复制 donor。
- 无法唯一归属的历史用户会保留为 unresolved，需要后续可信证据和显式流程处理。
- snapshot timestamp 之后的旧系统渠道数据不会进入 v3。

### 中性

- `source_system + snapshot_id + source_table + source_key` 仅用于迁移幂等和审计，不成为业务主键。
- 历史 Provider success 只表示旧系统记录的历史事实，不自动等价为 v3 的 executed 或 reconciled。
- Provider 写默认关闭；完成本地代码和 CI 不意味着生产资产发布可用。

## 考虑过的替代方案

### 复制 v2 Channel 后端和 migration

拒绝。它会引入旧身份模型、跨领域表访问、独立队列/Provider 调用和迁移历史依赖。

### Channel 保存 external_userid 或 State 并自行匹配

拒绝。这会形成第二套 OneID，并导致 scope 缺失、错绑和自动合并风险。

### Channel 直接调用企微 Provider

拒绝。这会绕过 Outbound/EER 的原子接受、幂等、outcome_unknown 和对账边界，存在重复外部效果风险。

### 在线双写、CDC 或切换旧系统

拒绝。用户明确要求全新 v3 重构和一次性历史导入；不切流、不持续同步。双写会扩大双主和身份漂移风险。

## 生产验收不变量

```text
wrong_oneid_bindings = 0
duplicate_source_maps = 0
provider_effects_created_by_import = 0
provider_calls_during_import = 0
silent_loss = 0
```

## 参考

- `docs/migration/channel/contract-audit.md`
- `docs/migration/channel/behavior-contract.md`
- `docs/01-PRD-迁移范围与新仓库基线.md`
- `skills/aicrm-v3-development/SKILL.md`
- `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
