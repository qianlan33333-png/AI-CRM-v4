# Order

Order 是交易事实的唯一 Owner，当前主干包含：

- `domain`：金额、Provider、订单生命周期、不可变商品快照、可退余额，以及付款人与受益客户分离。
- `port`：创建、查询、结算写入和历史导入四个窄接口；跨领域不得 import `app` 或 `store`。
- `app`：幂等创建/结算、稳定 `(created_at,id)` 游标和历史导入门禁。
- `store`：只接受共享 PostgreSQL Unit of Work 的事务上下文；原子写订单、条目、状态历史、收据、审计与 Outbox。
- `migrations/0020_order.sql`：Order 独占表、约束、索引和 append-only 财务事实。

## OneID 与持久化分类

```text
OneID: reads canonical customer
Persistence: local PostgreSQL transaction
External Effects: not involved in this slice
```

`payer_customer_id` 和 `beneficiary_customer_id` 是两个独立的 canonical `customers.id` 引用。当前模块不解析外部身份、不访问 Identity 表、不隐式建客或合并；归因将在后续切片中只通过 `internal/identity/port` 完成。

创建与结算把业务状态、幂等收据、状态历史、审计和 Outbox 放在同一共享 UoW。Store 没有 autocommit fallback。Payment 只能通过 `port.PaymentCoordinator` 在调用方已有 UoW 内创建订单或推进结算，不能访问 Order 表。

## 历史与效果边界

历史记录固定为 `record_origin=history`、`effect_eligible=false`，并用 `source_system + source_key + source_row_digest` 跨 run 重放。未解析客户可以为空；这不是允许猜测 OneID。任何历史行都不能发起支付、退款或其他 Provider 效果。

Order 的列表、详情、条目、导出和历史导入已经通过 v3 HTTP 与冻结 donor 页面挂载；Payment 只经 `PaymentCoordinator` 在调用方已有的 PostgreSQL UoW 内推进支付/退款结算，不直接访问 Order 表。模块状态为 `implemented`；生产历史数据完成全量 reconcile 之前不标记 `cutover`。

## 不允许

- Order Store 读取或写入 Identity、Payment、External Effects 表。
- 在 Order 内创建队列、Worker、Provider 重试或对账状态机。
- 把手机号、openid、unionid、external_userid 当作 Customer 主键或写入结构化日志。
- 修改/删除条目快照、状态历史、审计和历史导入收据。
