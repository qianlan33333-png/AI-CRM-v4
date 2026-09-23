# 微信支付回调可靠性修复 PRD（2026-09-16）

## 业务判断

用户完成微信支付后，微信支付通知是支付成功的低延迟信号，不是唯一事实来源。一次已成功创建的 JSAPI 支付单可能因为通知投递、入口验签、应用事务或后续同步投影失败而没有落库；此时，订单不能永久停留在 `pending_payment`。

本次以生产订单 `v3pay_3YVO3VRLMBCI3TDMWNPOLSU5TU` 的已证实 Provider `SUCCESS` 与本地待支付状态不一致为触发条件。既有商户、AppID、平台证书和 APIv3 Key 已核验匹配；现有证据不能将“没有回调收据”解释为“微信没有投递”。

进一步的生产只读证据已确定一个可达的应用内失败路径：该商品使用 legacy paid payload（`field_mapping` 为空，且未被到期配置提前短路），启用的出站目标同时选择付款人与受益人的 `phone:cn11`。Composition 当时向该路径注入通用 Identity query，而该 query 按安全设计拒绝 phone，因此已付观察器在事务中快速失败并使 callback 返回 503。这解释此订单条件下的失败路径，但不把来源尚未归属的其他 HTTP POST 一概归因于该订单。

## 边界与分类

```text
OneID: involved as a read only — 回调/对账本身只迁移既有 Payment/Order；其已有的已付商品扇出会读取 canonical Customer 根上的、精确 `phone:cn11` scope、active + verified 的 Phone Vault 身份。不会创建客户、创建身份、匹配、升级 assurance 或合并客户。
Persistence: local transaction + Provider read + internal durable job — 支付、订单、回调收据、审计、Outbox/已付事件与对账任务在同一 PostgreSQL Unit of Work；微信查单在事务外。
External Effects: no new Provider write — 只复用既有 payment reconciliation jobqueue、签名 Provider read 与既有商品出站意图；不创建新的支付、退款或消息 Provider 写入。已验证手机号仍复用原有加密外部效果接受；不满足身份条件时只记录 planned intent。
```

## 已查参考

微信支付官方 Go SDK `wechatpay-apiv3/wechatpay-go` 的 `notify.Handler.ParseNotifyRequest` 以原始 HTTP body、`Wechatpay-*` 签名头、平台证书和 APIv3 Key 完成验签与解密；它要求调用方在成功处理业务后返回 200。SDK 同时说明通知解密结果可按结构化对象或 `map[string]interface{}` 解析，新增非关键字段不应造成拒绝。

## 需求

1. 每次已成功返回 JSAPI 支付参数的微信支付预支付效果，都必须在相同的效果完成事务中加入既有 `payment-reconciliation` 耐久任务。查询尚未付款状态可重试；不得产生新的预支付或改变原始幂等键。
2. 签名校验和解密继续使用原始 body、平台证书、APIv3 Key 和 5 分钟时钟窗口。解析须接受真实 `TRANSACTION.SUCCESS` 中的 `amount.payer_total`、`payer_currency` 等非关键扩展字段，并继续以 `amount.total` 与本地应付金额严格比对。
3. 对账已用同一笔、同一金额、同一交易号将 Payment/Order 标为已付时，迟到的已验签回调必须写入自己的幂等回调收据并返回成功；不得再次结算订单、再次消费优惠券、重复产生已付事件或外部效果。
4. 回调入口必须输出脱敏、分阶段的诊断：请求大小、路由/事件种类校验、签名/解密/业务处理阶段及 body SHA-256 关联摘要。不得记录原始 body、签名、nonce、openid、商户单号、交易号、密钥或证书。
5. 对账、回调并发以及已付业务观察器均继续保持原子：观察器失败时全部回滚并返回非 2xx 以促使微信重试，诊断必须标明业务处理阶段。
6. 没有合法管理员会话时，恢复已存在的生产 Payment 只能通过 `AICRM_ROLE=payment-reconcile` 的单用途受控运行：必须显式提供正整数 `AICRM_PAYMENT_RECONCILE_ID`，完成完整 Composition 后调用同一 `ReconcileWeChatPayPayment` 服务。该角色不监听 HTTP、不接受商户单号输入、不写微信 Provider；Provider 查单、订单已付扇出、收据、审计和 Outbox 均复用既有路径。
7. 已付商品扇出对于配置的 phone selector 只能通过新的窄化 OneID Port 读取 canonical root 的唯一 `active + verified + phone:cn11` Vault 事实。通用 external-identity reader 继续拒绝 phone；不得使用管理员 reveal path，也不得将 declared phone 当作 verified 外发。身份缺失、歧义或缺密文时，Payment/Order/receipt 仍原子入账并记录 `planned_identity_unavailable`，不接受外部效果；数据库、解密或规范化故障继续使事务失败以便安全重试。

## 验收

- 对 `StateExecuted` 的预支付完成，记录一个可恢复的支付对账任务；旧的 `StateUnknown` 行为保持。
- 使用含 `payer_total` 的加密签名交易成功通知可验签、解密并取 `amount.total`。
- 对账先支付成功、回调后到，以及回调先完成、对账后到，均只产生一次 Payment/Order 结算及一次 Order paid event；每个唯一通知事件都有独立回调收据，HTTP 返回 200。
- 不匹配的交易号、金额、AppID 或重复事件 body 仍拒绝，且无状态写入。
- `payment-reconcile` 缺少、非数字、非正数 Payment ID 时拒绝启动；成功运行仅记录 Payment ID、最终状态和发布 SHA，且能以真实 PostgreSQL 回调/对账旅程证明其复用同一已付业务扇出。
- 真实 PostgreSQL + Phone Vault 的 callback 旅程证明：declared、歧义或缺密文 phone 仍只生成 `planned_identity_unavailable` 并完成 Payment/Order/receipt；唯一 verified phone 才进入既有加密 EER 排队路径；损坏密文、错误 scope 与 generic phone read 不能降级为可外发值。
