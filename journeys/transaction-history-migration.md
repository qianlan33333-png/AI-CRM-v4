# Journey：交易历史全量迁移

1. 不完整 source coverage 允许 inspect，但 dry-run/apply 必须失败。
2. 完整 snake_case manifest 被规范化解析，重复 identity/order/refund、退款超额和未知 Provider 必须失败。
3. 每个 verified 历史 identity 通过 OneID 显式 provision；相同 scoped identity 重放指向同一 Customer 根。
4. 历史 Order 带 `record_origin=history`、`effect_eligible=false` 和 source digest。
5. paid 微信支付/微信小店订单生成无 External Effect 的终态 Payment；支付宝只读订单不生成 Payment 写能力。
6. 历史退款生成 completed Refund，任何历史行均不得产生 EER job/provider call。
7. 同 run/digest 重放无新增；同 run 的 digest 漂移返回冲突。
8. reconcile 按 run receipts 和 OneID 精确解析验证身份、订单、支付、退款、金额恒等式，成功后标记 `reconciled`。
