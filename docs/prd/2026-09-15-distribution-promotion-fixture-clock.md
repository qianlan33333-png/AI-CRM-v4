# PRD — 分销结算测试夹具时钟一致性

## 事实与业务判断

`TestPostgreSQLDistributionPromotionCheckout*` 需要证明真实订单创建后，支付结算只在不早于该订单不可变快照的时间发生。当前夹具把 seed/actor 时间固定为 `2026-09-14T18:00:00Z`，但真实 `CreatePaymentOrderWithin` 记录当前 UTC 时间；结算却固定使用 `fixture.now + 1m`。测试时间越过 18:01 UTC 后，结算时间早于新订单，生产领域的 `ApplySettlement` 正确拒绝为 `order conflict`。

本修复不改变生产订单、CAS、结算校验、OneID 或任何 Provider 行为；只让测试命令由真实创建结果派生结算回调时间。它涉及本地测试 schema 持久化，但不新增持久任务、外部效果或身份匹配。

## 参考

仓库 GitHub 已有同一合同的写法：`internal/order/app/service_test.go` 从 `created.CreatedAt.Add(time.Minute)` 派生 `paidAt`，并明确说明结算事实不能早于不可变订单快照：
https://github.com/qianlan33333-png/AI-CRM-v3/blob/f13f65b79659309555a0653321f7c52a02893270/internal/order/app/service_test.go#L341-L346

## 最小变更

1. 保留 fixture seed 的单次 UTC 时间，避免把固定日期仅向未来移动。
2. 在 `createOrder` 返回的真实 `orderdomain.Snapshot` 基础上，令 `settle` 接收该 snapshot 或其 `CreatedAt`，并以 `CreatedAt.Add(time.Minute)` 构造 `OccurredAt`。
3. 三个 PostgreSQL 测试继续走真实 Order/Distribution/PostgreSQL composition；不 mock `ApplySettlement`，不放松“结算不得早于订单”的生产约束。
4. 在既有正向真实订单旅程中先提交早于 `CreatedAt` 的结算并断言 `ErrConflict`、订单仍待支付、佣金为零；再走正常结算并断言佣金创建。

## 验收

- 在全新 PostgreSQL 16 数据库、Provider disabled 下，以下三项全部通过：
  - `TestPostgreSQLDistributionPromotionCheckoutCreatesCommissionFromARealOrder`
  - `TestPostgreSQLDistributionPromotionCheckoutRejectsPromoterAsEitherBuyerParty`
  - `TestPostgreSQLDistributionPromotionCredentialReceiptsSerializeAndScopeKeys`
- 在同一真实订单旅程中，早于 `CreatedAt` 的结算返回冲突、订单仍待支付且无佣金；随后从 `CreatedAt.Add(time.Minute)` 派生的正常结算创建预期佣金。
- 生产结算时间/CAS/支付回执规则无差异，diff 仅测试夹具与本 PRD。
- 之后由独立 CI 完整运行验证；本 PR 不修改 #292 的 CI 门禁或预算变更。
