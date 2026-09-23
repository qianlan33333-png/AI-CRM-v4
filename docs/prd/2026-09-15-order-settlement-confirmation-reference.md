# PRD：订单分销顶部最近确认时间只采纳真实结算单

日期：2026-09-15
范围：修复订单只读分销投影顶部 `SettlementConfirmedAt`。它只能取同一 commission 下、audit payload 的 `settlement_reference` 与真实 `distribution_settlements.settlement_reference` 匹配的最近 `distribution.settlement_paid.v1` 记录。

## 业务判断

顶部“最近分账确认时间”表示系统确认过某一张真实结算单的最近时刻，不是任意 commission audit 的最新时间。当前逐结算单 JSON 已验证 `payload.settlement_reference=s.settlement_reference`，但顶层子查询只按 commission 与 event type 取 `MAX`；晚到的错误 reference audit 会污染顶部事实。

本 PR 只收紧 Distribution Owner 的只读 SQL。它不修改结算、付款、金额、`due_at`、audit、幂等键、Provider 调用或写入路径；不读取 Payment 表，也不把 `updated_at` 或金额当确认时间。

```text
OneID：不涉及。
持久化/外部效果：仅既有 Distribution 表的只读查询；无写入、任务或 Provider 调用。
```

## 参考与实现

- GitHub code search 没有发现另一个可复用的 `SettlementConfirmedAt` 实现。
- 仓库当前同一 `ReadOrderDistribution` 的逐 settlement JSON 已采用正确匹配谓词；本修复让顶层投影使用同一事实边界。
- 顶层子查询改为：仅当 audit 的 `payload->>'settlement_reference'` 存在于同一 `commission_id` 的 `distribution_settlements` 时，才参与 `MAX(ae.occurred_at)`。
- 当前完整 `ReadOrderDistribution` 保持一条 SQL statement，因此 commission、嵌套 settlement 和顶层时间仍来自同一 MVCC 快照。

## 验收

1. 同一 commission 的真实 settlement reference audit 使顶层与该 settlement 行都显示确认时间。
2. 在其后追加相同 commission、错误/不存在 settlement reference 的 audit，顶层仍保留匹配 audit 的时间，不能被污染。
3. 另一 commission 的 audit、无 audit 的真实 settlement、多个真实 settlement 的多条确认 audit 均不串线；顶层取所有有效匹配记录中最近时间。
4. PostgreSQL 集成测试仍断言整个订单批量投影只有一条 fact statement，且原有订单/结算/调整/异常投影与 `due_at` 不变。

本项完成并验证后，才继续运营首页的 canonical payer 计数实现及更后的同口径下钻。
