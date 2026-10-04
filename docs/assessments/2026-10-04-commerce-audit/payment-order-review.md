# Order / Payment read-only adversarial review

基线：`AI-CRM-v4` `94d4b845f9d9ddcc30e7e05432032ba1677fac5a`。这轮未改仓库文件、未调用 Provider 写接口/真实资金操作；审查期间共享工作区已出现主线的 Product 修复和审计文件，我没有触碰它们。

## 确认缺陷：Alipay 通知时间会被当作实际付款 PaidAt

**影响：P2，资格误判并偏移权益起点。** 一个验签通过、包含 `notify_time` 但没有有效 `gmt_payment` 的 Alipay 付款通知会被正常应用；Payment/Order 下游把通知送达时刻当作可信付款时间。包 #27 明确只允许 `followed_at` 秒值严格早于实际付款 `PaidAt` 的好友关系。若顾客在真实付款之后、迟到通知之前加好友，当前实现会错误入选付费好友人群，并可能触发后续外呼；服务期权益的 `start_at` 也直接使用同一个时间，因回调延迟而后移。

证据链：

1. `internal/payment/provider/alipay.go:382-395` 初始化本地时间，并按 `gmt_payment` → `notify_time` fallback 解析；缺少/无法解析 `gmt_payment` 时，只要 `notify_time` 可解析就以该值作为 `CallbackResult.OccurredAt`。这不是付款发生时间。
2. `internal/payment/provider/alipay_acceptance_test.go:83-103` 的签名验收 fixture 明确接受只有 `notify_time`、没有 `gmt_payment` 的成功付款通知；它验证规范化成功，但没有验证该时间的语义。故无需篡改签名或触及外部 Provider 即可走到此路径。
3. `internal/payment/app/service.go:1380-1396` 把 `OccurredAt` 写入 Payment `PaidConfirmedAt` 并原样交给 Order 结算。
4. `internal/order/app/service.go:448-469` 将该值写为首次付费状态事件时间；`internal/order/store/audience_read.go:22-25` 又将首次 `order_status_history.to_status='paid'` 时间投影为 audience `PaidAt`。
5. `internal/segment/adapter/legacy_templates.go:611-622` 对 opted-in 好友资格只做 `followedAt.Unix() < PaidAt.Unix()`。复现时间线：实际付款 12:00:00；好友在 12:00:02 加入；验签通知无 `gmt_payment`、`notify_time=12:00:03`。现路径给订单暴露 12:00:03，条件通过，尽管好友比真实付款晚两秒。
6. 业务不变量来自 `docs/prd/2026-09-30-package27-friend-at-payment.md:5,18-19,42`（付款时已是好友；严格秒比较、同秒排除）及 `docs/prd/2026-09-30-alipay-second-precision.md:23,26,30`（Alipay 文档区分通知时间与付款时间；`notify_time` fallback 是通知时刻，不冒称付款发生时刻；PaidConfirmedAt、订单历史/paid event 与权益使用付款事件）。
7. 权益影响：`internal/order/app/service.go:601-606` 以 Order 命令中的同一 `OccurredAt` 创建服务期授权，`internal/order/store/entitlement.go:699-705` 以 `PaidAt` 设 `start_at` / `end_at`。

**既有覆盖及缺口：** `internal/payment/provider/alipay_acceptance_test.go:83-103` 只证明缺 `gmt_payment` 的验签通知会被接受；`internal/order/store/postgres_integration_test.go:1360-1438` 覆盖 PaidAudienceOrders payer、退款状态和历史无时间，但未连真实 Alipay adapter；`internal/segment/adapter/legacy_templates_test.go:222-287` 覆盖好友与 PaidAt 的严格秒关系，但没有缺失 `gmt_payment` 的链路；`internal/payment/app/alipay_second_precision_test.go` 覆盖同秒可结算及 Provider 时间保存，却没有缺失时间 fallback。此前没有覆盖“合法缺时通知 → notification-time fallback → 错误好友资格/权益起点”的组合回归。

**建议的最小安全语义：** 保留对合法签名、金额、app、商户号和交易号匹配通知的接收与 HTTP ack，但给 Alipay 成功付款回调单独携带“无可信付款时间”（缺失或格式错误的 `gmt_payment`）状态。待支付订单在同一个 PostgreSQL UoW 中把原 callback event/body receipt 标成 `query_required` 并 enqueue 已有 Alipay payment reconciliation target，不先结算 Payment/Order、不授予带错误起点的权益；后续现有 `ReconcileAlipayPayment` 查询成功要求解析 `send_pay_date` (`internal/payment/provider/alipay.go:258-263`)，再按原有 app/商户号/金额/币种/交易号验证和原子结算 (`internal/payment/app/service.go:1221-1268`)。若 Payment 已先由权威 query 结算，通知只记 `replayed`，不要再次入队或覆盖 PaidConfirmedAt。不能只删除签名回调、也不能用 notify_time补作 PaidAt。

## 库存字段结论

未找到把商品 `StockQuantity` 作为实际库存余额的合同证据。现有资料只定义了字段 DTO/校验/编辑/复制：`docs/migration/product/pr04-frontend-audit.md:89-104` 说库存为非负整数且 donor 新建默认 0；Product CRUD 有该字段；`productport.CheckoutProduct` /公开结账路径不读取、不扣减它。未发现 0=无限或 0=售罄、下单预占/扣减、取消/失败/退款恢复等明确规则。因此本审查不把它报作购买缺陷，也不建议擅自增加库存扣减机制；要实现前需由业务提供库存语义合同。

## 其余审查结论

在本次实际读到的 Order/Payment/Distribution 路径中，未找到第二项可证实的金额累计、回调重复结算、退款超额、跨域部分提交或 commission funding reserve 缺陷：

- Order付款/累计退款在一个UoW中行锁订单、验证累计额不溢出且不超过订单金额，随后原子更新 Order history/audit/outbox、权益/Coupon及付费/退款消费者：`internal/order/app/service.go:417-489`。
- Payment退款创建先做幂等 replay + payload scope/hash 检查，随后锁 Payment，计入所有仍占用余额的 refund 和 settlement reserve，校验可退款余量；WeChat/Alipay 活跃 refund 约束有真实PG并发用例。已有测试：`internal/payment/app/service_refund_postgres_integration_test.go:94-196`；query-before-callback 只结算一次：`:198-316`。
- Callback先校验 provider事实，再锁并核验 Payment/Refund 与金额/app/digest，callback receipt 的 EventDigest绑定 BodyDigest和目标行；Payment+Order+refund/paid event consumers 在一个 UoW，且已结算事实只记新回调收据不重复应用：`internal/payment/app/service.go:1334-1437`, `internal/payment/store/postgres.go:676-706`。WeChat/Alipay query-before-callback现有PG测试在上列退款 suite及 `internal/payment/app/alipay_acceptance_test.go`。
- 商品结账对 Product 行 `FOR UPDATE` 读取当前 status、lifecycle、价格/version；商品下架后新建 checkout 被拒绝，已创建的订单保存 immutable checkout snapshot。未发现旧的订单价格被当前商品覆盖；购买防重复状态投影与 advisory lock见 `internal/order/store/purchase.go:10-47` 和 `internal/order/store/purchase_test.go:13-121`。新 checkout 忽略旧 pending 状态是 Payment app 现有明确业务注释/测试，不作为本轮缺陷。
- 服务期源单 grant/revoke 使用确定性 fulfillment receipt、source-order 锁与同UoW；部分退款首次撤权/后续重复退款等已有PG覆盖：`internal/order/store/postgres_integration_test.go` 中 `TestPostgreSQL...ServicePeriod...` 家族（见搜索结果中的 `:658`, `:861`, `:977`, `:1128`, `:1220`, `:1263`）。
- Distribution settlement 前在 Payment UoW 重做 funding/refund-exposure/reserve核验，Payment 行锁保证与退款创建序列化；settlement instruction/reference immutable，reconciliation success要求精确 reference/amount/currency；refund callback只原子持久化Order退款和内部recheck job，具体commission调整在全局锁序的内部job中做。组件测试在 `internal/distribution/app/settlement_test.go` / `refund_test.go`；PG覆盖锁序 `internal/distribution/store/settlement_warning_integration_test.go:58`、结算确认引用 `internal/distribution/store/readmodel_paid_at_integration_test.go:15`、`admin_detail_integration_test.go:140`。主线另外已有真实PG/domain组合测试。

## 已读文件和规范

- 仓库规范：`AI-CRM-v4/AGENTS.md`、`AI-CRM-v4/skills/aicrm-v3-development-frontdoor/SKILL.md`、`AI-CRM-v4/skills/aicrm-v3-development/SKILL.md`。
- Order：`internal/order/app/service.go`、`app/entitlement.go`、`app/distribution.go`、`store/purchase.go`、`store/postgres.go`、`store/entitlement.go`、`store/audience_read.go`、`store/purchase_test.go`、`store/postgres_integration_test.go`、`port/purchase.go`、`domain/order.go`、`domain/order_test.go`。
- Payment：`internal/payment/app/service.go`、`app/purchase.go`、`app/distribution_settlement.go`、`app/service_test.go`、`app/service_refund_postgres_integration_test.go`、`app/alipay_acceptance_test.go`、`app/alipay_second_precision_test.go`、`store/postgres.go`、`provider/callback.go`、`provider/alipay.go`、`provider/alipay_acceptance_test.go`、`domain/payment.go`、`domain/profit_sharing.go`。
- Product：`internal/product/app/target_reader.go`、相关 `internal/product/port` checkout定义、`internal/product/README.md`、`docs/migration/product/pr04-frontend-audit.md`。
- Distribution：`internal/distribution/app/commission.go`、`app/refund.go`、`app/refund_test.go`、`app/settlement.go`、`app/settlement_test.go`、`domain/distribution.go`、`store/settlement.go`、`store/settlement_warning_integration_test.go`、`store/readmodel_paid_at_integration_test.go`、`store/admin_detail_integration_test.go`。
- 关联合同：`docs/prd/2026-09-30-alipay-second-precision.md`、`docs/prd/2026-09-30-package27-friend-at-payment.md`、`docs/prd/2026-09-30-alipay-callback-openapi-contract.md`、`docs/prd/2026-09-14-distribution-receiver-readiness.md`。
- 相关資格消费：`internal/segment/adapter/legacy_templates.go`、`legacy_templates_test.go`。

## 待更深审

这轮没有逐文件检查所有 Payment provider/退款HTTP分支、所有 Product生命周期 store 竞争案例、所有 Distribution reconciliation-worker 数据库适配器和全部归因读路径，也没有独立启动/执行新的测试。尤其需由主线回归新增 Alipay missing/invalid `gmt_payment` → query-required job → query `send_pay_date` → 正确 PaidAt/权益，以及 query-first/重复通知/queue失败事务回滚的端到端PG组合。以上为审查边界，不代表这些未逐项检查处无缺陷。
