# 商品裂变销售成绩支付闭环

## 目标

商品裂变活动以订单冻结的活动上下文和商品推广链接归因为唯一销售依据。订单支付成功后，Referral 记录可解释的销售事实；确认退款后追加冲正事实。活动成员关系、战队和销售归属互不推导：当前关系不会产生销售分或佣金。

## 业务判断

| 订单事实 | Referral 行为 |
| --- | --- |
| 有效活动上下文、无推广人 | 受益人自动参加活动，`team_id=NULL`；记录个人销售金额和订单数，不进入战队统计。 |
| 有效活动上下文、有效推广人 | 冻结推广人、推广凭证、政策和商品快照；记录销售事实。 |
| 推广人等于付款人或受益人 | 受益人仍自动参加活动；不记录销售分。 |
| 无活动上下文或活动无效 | 普通购买，不创建活动参与或销售事实。 |
| 重复支付确认 | 用 Order paid-event ID 和所有冻结字段幂等返回，字段不一致冲突。 |
| 确认退款 | 以 Payment/Order receipt key 追加不可变冲正；部分退款冲金额，累计全额退款才冲订单数。 |
| 退款处理中、未知或失败 | 不向 Referral 发送冲正。 |

活动入口只写 HttpOnly `aicrm_referral_activity_context`。Payment checkout 只从该 cookie 读取 `rpa_` 不透明令牌；JSON 不接收活动 ID、推广人、销售金额或排序规则。Distribution 仍独立完成协议、注册、本人有效购买资格和 receiver readiness；活动入口不会使未准备收款的用户成为可推广分销员。

## 领域与事务分类

- **OneID：涉及。** 订单的付款人、受益人和推广人只使用现有 canonical Customer ID；不建身份表，也不使用浏览器提交的身份。
- **持久化：涉及。** Order 冻结两个上下文摘要；Referral 拥有活动上下文、checkout 归因、参与及销售/冲正事实。支付状态、Order paid/refund 事实、Referral 写入、审计和 outbox 均在已有 PostgreSQL Unit of Work 中提交。
- **外部效果：不新增。** 本闭环不直接调用 Provider、不建队列、不重试。Payment 只在已有确认支付或确认退款流程后调用 Order consumer；未知结果继续由 Payment 对账。

## 跨域契约

1. Payment `CreateCommand.ReferralActivityContext` 只承载 cookie 读取的不透明令牌，透传到 Order。
2. Order hash 并冻结活动与推广上下文；`PaidEvent` 追加商品类型、实付、两个摘要。
3. Order 在 checkout UoW 中先调用既有 Distribution attribution，再调用 `ProductSaleCheckoutCoordinator`，将 PromotionCustomer、credential ref、policy snapshot 和商品 snapshot 交给 Referral。
4. Referral 的 `ConsumePaidEventWithin` 和 `ConsumeRefundSettlementWithin` 实现 Order 的稳定 consumer Port，并在当前 UoW 内写本领域事实。Composition 只做 fanout，不访问 Referral 表。

## 排名口径

每个销售事实同时保存 `amount_delta_minor` 和 `order_count_delta`。活动配置可选择按有效销售金额或有效订单数排序；切换排序只改变读取投影，不能重写销售事实。个人榜包含无队成员；战队榜仅聚合冻结 `team_id` 非空的事实。

## 验收

- 同一 paid event 重放不重复入会或记销售；变更推广人、活动或金额的重放冲突。
- 无来源订单自动入会但 team 为空；当前 relationship 不影响销售。
- 自购不产生销售事实。
- 两笔并发支付为同一客户自动参加时，仅产生一个 participation。
- 部分退款和全额退款分别正确冲金额和订单数；同 receipt 重放不重复冲正。
- Provider 未确认退款、无效 activity cookie、无 Distribution receiver readiness 均不伪造有效推广或销售。

## 参考

GitHub 的 [Microsoft event-sourcing pattern](https://github.com/microsoftdocs/architecture-center/blob/main/docs/patterns/event-sourcing.md) 明确要求至少一次投递的消费者保持幂等，并以补偿事件保留撤销历史。本实现以稳定 paid-event ID 和 refund receipt key 去重，以 append-only 冲正保留退款前后的可审计事实。
