# ADR-0002：问卷使用 OneID、不可变提交快照和 Outbound 外部效果

## 状态

Proposed

## 背景

v3 已包含与 `AI-CRM-v2@6bfbe58` 逐字节一致的问卷前端，但没有 Survey 后端、数据库表或 OpenAPI 路由。v2 的 Survey 领域提供了有价值的 CRUD、公开提交、导出、历史导入和外部效果测试合同，但它仍保存多种 opaque 身份字段，assessment 后端未完成，真实 Provider adapter 默认 disabled。

生产源库现有 1,585 次提交和 6,649 个答案。其中至少 53 次提交不能安全映射到当前 production OneID；4,327 个答案引用的 question id 已不在当前定义中。旧外推日志还包含 URL、请求载荷和响应体。

v3 的固定架构要求 `customers.id` 是唯一业务主键，外部身份归 Identity，业务状态/回执/Outbox/效果接受在同一 PostgreSQL Unit of Work，企微 Provider 写入统一经 Outbound 和 External Effects。

## 决策

1. Survey 在 v3 中重建为独立领域；v2 仅作为行为、前端和测试供体，不成为 module、submodule、运行依赖或数据源。
2. 已验证的问卷前端资产保持供体交互和视觉；后端 DTO 可适配 v3 模型。只有真实 Journey 通过后，能力矩阵才标记为 `real`。
3. 发布定义和提交答案均使用不可变快照。历史答案不要求当前 question/option FK 存在，因此定义编辑不会破坏历史可读性。
4. Survey 提交只保存可空 canonical `customer_id` 和身份解析状态。外部身份通过 `internal/identity/port` 解析；HTTP 自报和手机号答案不能建客或自动合并。
5. production 历史身份只能通过签名 source OneID→v3 Customer 映射关联。未匹配或冲突的提交完整导入并进入 digest-only unresolved case。
6. Survey 冻结后续动作意图，通过稳定 Outbound Port 交给 Outbound；External Effects 保存 digest-only 状态机。Survey 保存 opaque `effect_id` 绑定，不跨域查询效果表。
7. 提交、幂等收据、审计、Outbox、Outbound intent 与 effect acceptance 在同一 caller transaction；Provider 网络调用在事务外由共享 River runtime 执行。
8. 历史 Provider 原始材料不进入 live 业务表、日志或冷归档。只迁移状态、次数、时间、安全失败分类和业务关联。
9. 生产迁移采用一次性一致性快照。snapshot 建立后 source 新增数据不在本期范围；不切流、不停写、不增量同步、不双写也不退役 source。

## 后果

### 正面

- 问卷与 Customer 的归属遵循唯一 OneID，不产生第二套映射。
- 定义变更不会再让历史答案不可见或被级联删除。
- Provider 超时、重试和对账复用现有可靠内核，不新增 Survey queue/worker。
- 所有历史行都有可审计处置；无法归属身份不等于丢数据。
- 前端可保持已验证的用户习惯，同时后端能逐步替换 legacy 命名。

### 负面

- 需要补充 Survey→Identity、Survey→Outbound 的稳定 Port 和 transaction-aware adapter。
- 历史导入必须维护 source map、digest 和 unresolved 工作台，代码量高于直接 `INSERT ... SELECT`。
- 旧结果 token 和敏感导出需要额外密钥及访问控制。
- 一次性快照之后 source 新增问卷数据不会自动进入 v3。

### 中性

- 旧 source ID 只作为迁移元数据，不成为 v3 主键。
- 旧外推 success 只是历史 provider 接受/响应事实，不自动等价为最终业务交付。
- unresolved identity 可以在后续获得 verified evidence 后显式 reconcile，但不会自动消失。

## 考虑过的替代方案

### 直接复制 v2 Survey 目录和 migration

拒绝。它会引入旧 module、旧身份字段、独立效果 owner 和历史 migration 依赖，也不能补齐 assessment 与真实前后端闭环。

### Survey 自己保存 openid/unionid/external_userid 并匹配客户

拒绝。这会形成第二套 OneID，存在跨 scope 串号和身份错误归属风险。

### Survey 自建 webhook queue/worker/retry 表

拒绝。它会复制 River、lease/fence、outcome_unknown 和 reconciliation 内核，也可能造成重复 Provider 效果。

### 在线双写或 CDC 零停机迁移

拒绝。本期数据量小，双写收益低，却显著增加双主、顺序漂移和身份映射不一致风险。

### 无法映射的历史提交不导入

拒绝。当前至少 53 次提交身份未解析，且大量答案无法关联当前定义。丢弃会违反“全部历史数据”目标。

## 参考

- `docs/06-PRD-问卷全能力与历史数据迁移.md`
- `docs/01-PRD-迁移范围与新仓库基线.md`
- `skills/aicrm-v3-development/SKILL.md`
- `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
