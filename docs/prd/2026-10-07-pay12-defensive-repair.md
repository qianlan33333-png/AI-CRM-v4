# PAY1/PAY2 退款防御性修复

## 父 PRD 与业务判断

修复两个退款事实边界：成功时刻早于本地 `outcome_unknown` 更新时间时仍可正确结算；只有与原 Payment 商户单号和原交易精确绑定的退款事实，才能写回调/对账收据或结算退款。

```mermaid
flowchart TD
    A[验签回调或 Provider 查询结果] --> B[定位 Refund 并读取其原 Payment]
    B --> C{原商户单号与原交易引用/摘要匹配?}
    C -->|否或缺失| X[返回冲突；不写 callback/reconciliation/settlement receipt]
    C -->|是| D{同一退款事实已完成?}
    D -->|是| E[只记录匹配回调的 replay 收据；不重复结算 Order]
    D -->|否| F[校验 Provider OccurredAt 非零且不早于 Refund.CreatedAt]
    F --> G[同一 UoW 写 callback 或 reconciliation 收据]
    G --> H[Refund UpdatedAt = max(处理时钟, 既有 UpdatedAt)]
    H --> I[settled audit/outbox 与 Order 保留 Provider OccurredAt]
    I --> J[提交同一 PostgreSQL UoW]
```

复用 payment domain/app/store、现有 Provider 查询 Adapter、`payment.Store`/`orderport` 与共享 PostgreSQL UoW；外部查询仍在事务外，回调事实仍由现有 Provider Adapter 验签。Shop 的 AfterSaleID、ProviderOrderID、商品/SKU/数量/金额继续绑定冻结退款 intent material。微信支付、支付宝退款查询与回调需额外核对其响应给出的原商户单号和原交易号，再写 receipt。

参考：Stripe Webhooks 的事件乱序和重复处理说明仅作通用可靠性参考（父任务已核实）：[Handle duplicate events](https://docs.stripe.com/webhooks#handle-duplicate-events)、[Event ordering](https://docs.stripe.com/webhooks#event-ordering)。微信支付 V3 商户退款查询响应将 `transaction_id` 与 `out_trade_no` 列为必填原交易字段：[查询单笔退款（Native 支付）](https://pay.wechatpay.cn/doc/v3/merchant/4012791884)。支付宝退款查询响应提供 `gmt_refund_pay` 退款支付时间：[统一收单交易退款查询](https://aipay.alipay.com/docs/vibe-pay/ai-web-app-payment-qianyi/api-list/alipay-trade-fastpay-refund-query.html)。锁定 SDK `v3.2.31` 将其映射为 `GMTRefundPay`；官方查询请求示例中的 `query_options` 用于 `refund_detail_item_list`，响应示例含 `gmt_refund_pay`，因此不发送未文档化的时间查询选项。对应成功结果缺少退款时间时，Adapter 拒绝构造可结算事实；支付宝退款回调只接受签名字段 `gmt_refund` 或 `notify_time`，不以本地处理时间补位。Provider 文档只用于核对对应 Provider 字段，不跨 Provider 推断。

## 分类与影响

- **OneID**：不涉及。只读取 Refund 所属的冻结 Payment 记录，不解析外部身份、不建客、不更改客户归属。
- **Persistence / execution**：Payment 与 Order 共用本地 PostgreSQL UoW；退款状态、Payment settlement receipt/audit/outbox 与 Order 退款结算在同一 UoW。Provider 查询在事务外。沿用现有 `internal/platform/jobqueue` reconciliation job，不新增持久任务。
- **External Effects**：不新增 Provider 写效果或效果状态机。只消费已验签回调及现有 WeChat Pay、Alipay、WeChat Shop 只读查询事实；退款发起继续走现有 payment/External Effects 端口。
- **对外合同**：不增改 HTTP 路由或 Provider 操作。缺少/不匹配原交易绑定时沿用冲突失败，不落回调、对账或退款结算收据。
- **业务机制**：Refund `UpdatedAt` 使用不倒退的本地处理时钟；原 `OccurredAt` 保留给 `payment.refund_settled` audit/outbox 与 Order 结算。退款事件的精确交易绑定在任何收据写入前检查。
- **关联模块**：`internal/payment/domain`、`app`、`provider`、`store`；Order 仅通过既有 `orderport.SettlePaymentWithin` 同一 UoW 接收退款事实。直接消费者是三种退款 reconcile 路径与已验签 callback。证据来自当前 V4 调用点及 Payment/Order Ports。
- **页面影响**：无；不改侧边栏、后台页面或客户展示页。
- **验证证据**：仅运行具名 domain/app 纯单元合同；精确 SHA/tree 与命令收据单独交付。Provider 真实结果、HTTP/PG 环境对抗验证和业务资金验收不在本候选。

## 必要约束依据

`原商户订单号 + 原 Provider 交易引用/摘要`｜不核验会把另一笔真实退款归入本 Payment、推进 Order 退款金额并留下不可变收据｜现有 Payment 中冻结的商户订单与已验证交易事实；微信支付官方 V3 查询要求返回原交易字段｜只接受 Adapter 交付且与原 Payment 完全匹配的事实；缺字段或不符时在 callback/reconciliation receipt 前返回冲突。其他输入限制不变。

## 验收与不覆盖项

1. `CompleteVerifiedFact` 在已验证 `OccurredAt` 早于已有 `UpdatedAt` 时仍推进有效状态和版本，并保留不倒退的 `UpdatedAt`；原 `Refund.Complete` 保持严格时间合同。
2. 正常原交易绑定可结算；商户订单号或交易引用/摘要不符、缺失时，在 `ClaimCallback`、`RecordReconciliation`、Refund settlement receipt/audit/outbox 和 Order 结算之前拒绝。
3. 已完成退款的 Provider `OccurredAt` 必须非零，且不能早于 Refund `CreatedAt` 的来源精度桶：整秒事实同秒可比较，带子秒事实按现有 PostgreSQL `timestamptz` 微秒精度比较。支付宝成功查询必须提供 `gmt_refund_pay`；退款回调必须有签名退款时间或签名通知时间。不得用本地处理时钟替代缺失或早于该精度桶的 Provider 事实。
4. 原 Provider `OccurredAt` 保留给 Order 和 settled audit/outbox；处理时间仅用于 Refund `UpdatedAt` 和 operation receipt 创建时间。
5. 完成后的匹配重放保持既有 once 语义，不重复推进 Refund 或 Order。
6. 旧 `Refund.Complete` 继续拒绝倒退时间；只有 `CompleteVerifiedFact` 将已验证 Provider 发生时间与单调处理时间分开。

不覆盖旧对抗任务或其脚本/测试；不读写历史财务数据、不做退款恢复、不进行 HTTP/PG 对抗验证、不调用外网 Provider、不做实际支付/退款或任何资金操作；不改 UI，不扩大到其他支付行为。
