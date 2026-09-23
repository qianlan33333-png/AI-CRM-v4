# PR-TX01 交易管理 donor 契约审计

## 冻结基线

- v3 基线：`origin/main@723b90914c20fe12bf07507e3683112816cf4fe3`。
- donor：`qianlan33333-png/AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`，仅作只读行为、测试和叶子协议供体。
- v3 不依赖 donor 的 Go module、数据库、迁移、运行时或远程服务。
- 本契约只把 `order` 与 `payment` 状态推进到 `contracted`；它不代表 API、页面、Provider 或生产迁移已经就绪。

## 开发前分类

```text
OneID: reads canonical customer（后续归因只经 internal/identity/port；本 PR 不解析、建客或合并）
Persistence: local transaction（本 PR 只冻结契约；后续 Order 写入、幂等收据、审计和 Outbox 必须同事务）
External Effects: 本 PR 不执行；后续支付/退款 Provider 写必须走 versioned payment contract + EER
```

Order 是订单、条目快照、状态历史、导出收据和历史订单导入的唯一 Owner。Payment 是支付、退款、Provider callback、effect binding 与对账的唯一 Owner。两者不得读取 Identity、External Effects 或对方的表。

## 管理端行为契约

### 页面与动作

- 保留 donor `orders.html` 和 `orderDetail.html` 的视觉层级、筛选、分页、详情时间线、退款确认和历史退款展示。
- 订单列表支持订单/交易号、付款人、商品、状态和创建时间筛选；跨字段模糊检索必须由服务端实现，不得只筛当前页。
- 订单详情展示付款人与受益客户为不同角色；不存在可信证据时不得把两者猜成同一 OneID。
- 导出仅支持经服务端过滤的 CSV，最多 10,000 行、5 MiB，防公式注入，并以稳定幂等键记录收据。
- 退款必须完整复输订单号、勾选核对，并校验剩余可退金额；`accepted/queued` 不等于 Provider 成功。
- 历史订单和历史退款只读，必须显式显示 history 标记，且永远不能触发 Provider 效果。

### 管理端读写路由

| 能力 | donor 路由 | v3 语义 |
|---|---|---|
| 订单列表 | `GET /api/admin/orders` | 只读统一投影；稳定游标、服务端筛选 |
| 订单详情 | `GET /api/admin/orders/{order_ref}` | 多 Provider 引用歧义时 fail closed |
| 条目快照 | `GET /api/admin/orders/{order_ref}/items` | 只读不可变购买快照 |
| 支付宝列表/详情 | `GET /api/admin/alipay/transactions[/{order_no}]` | 仅历史/本地投影只读 |
| 退款列表 | `GET /api/admin/refunds` | 只读 Payment 投影 |
| 导出预览/创建/结果 | `/api/admin/exports...` | 本地、收据化、无 Provider 调用 |
| 微信订单导出 | `POST /api/admin/wechat-pay/order-exports` | PII-safe CSV；旧 job/download 路由 fail closed |
| 微信支付退款 | `POST /api/admin/wechat-pay/orders/{order_id}/refunds` | Payment intent + EER；默认 disabled |
| 微信小店退款 | `POST /api/admin/refunds` | Payment intent + EER；默认 disabled |
| 小店退款对账 | `POST /api/admin/wechat-shop/refunds/{refund_id}/reconcile` | 原始 after-sale key 精确查询，不盲重试 |

所有管理端写要求已认证管理员、RBAC、CSRF 和幂等键。所有敏感读响应必须 `Cache-Control: no-store`。公开支付 callback 只依赖 Provider 签名/验签和回放收据，不使用管理员会话。

## Provider 与失败语义

- WeChat Pay：后续支持 checkout、签名 callback、退款、原始键查询对账；开关默认 false。
- WeChat Shop：后续支持订单物料读取、退款、加密 callback 和精确 after-sale 查询；开关默认 false。
- Alipay：donor 只有持久化交易列表与详情，没有可证明的 checkout、退款、callback 或 reconciliation 实现，因此本期严格只读，不伪造写能力。
- 超时必须区分未发送、已发送但结果未知；`outcome_unknown` 只能原键查询、可信 callback 或显式 reconciliation，禁止换幂等键重试。
- 业务状态、幂等收据、审计、Outbox 和 effect acceptance 需要原子提交时，必须参与同一个 PostgreSQL Unit of Work；Provider 网络调用不得持有事务。

## OneID 归因规则

- `customers.id` 是唯一渠道中立客户主键；订单可分别保存 `payer_customer_id` 与 `beneficiary_customer_id`。
- 外部身份只经 `internal/identity/port` 按 `kind/scope/value/assurance/source` 解析；Order/Payment Store 不访问 Identity 表。
- OpenID 无 App scope、UnionID 无开放平台 scope、手机号声明、多根冲突或证据不足时保持 unresolved/pending/conflict；不隐式建客、不自动合并。
- 原生 checkout 必须来自可信 Payment Session 的 verified scoped identity；浏览器不能自报 customer、openid、unionid 或 assurance。

## 历史导入与红线

- 所有历史用户/identity 行由 OneID 迁移路径处理；交易范围含 orders、items、payments、refunds 和状态/回调事件。
- 历史行使用稳定 `source_system + source_key` 幂等，保留 `record_origin=history`，且 `effect_eligible=false`。
- 无法唯一归因的历史交易进入可审计隔离桶，不能猜测归属或阻塞其余安全数据导入。
- 生产发现、快照、apply、delta、reconcile 和切流是后续独立门禁；当前 PR 不连接或写入生产。
- 双主写、身份错绑、重复支付/退款、鉴权绕过、PII/Secret 泄漏、跨领域写表或静默丢数立即停止。

## 完成边界

TX01 完成的证据只有：donor commit、选定文件 SHA-256、两张活跃模板逐字节一致、禁止 v2 runtime import 的自动检查，以及本契约。Order/Payment 仍不能对外声称可用，直到各自 migration、API、UI journey 和生产 readiness 全部通过。
