# ADR-0003：交易聚合、OneID、外部效果与历史导入边界

- 状态：Accepted（2026-09-03；生产 apply/cutover 仍需独立授权）
- 日期：2026-09-03

## 背景

v3 已有完整交易前端壳，但 Order/Payment 后端仍是占位。v2 的交易能力集中在 `internal/order`，同时存在浏览器直传 `customer_id`、Order 同时承担 Payment 事实、历史导入只覆盖微信等不适合原样照搬到 v3 的边界。生产历史用户和交易数据需要一次性迁入，但生产 schema 尚未完成只读核验。

## 决策

1. 复用 v2 的用户可见页面、请求语义、Provider 协议和测试向量，不复制 v2 module/runtime/store 作为依赖。
2. Order 拥有订单、订单项、金额/币种、受益 Customer 引用、付款 identity 引用、订单状态、导出和历史导入。
3. Payment 拥有支付命令、尝试、回调、退款、Provider receipt、对账和 `effect_id` 绑定；Payment 只能通过 `internal/order/port` 改变订单金融状态。
4. 付款人身份与受益 Customer 分开。Checkout 不接受浏览器自报 raw identity 或任意 Customer；可信 OAuth/支付会话通过 Identity Port 解析或显式建客。
5. 为支付新增版本化 `internal/externaleffects/port/payment_v1.go` 契约，复用同一 External Effects lease/fence/attempt/reconciliation 内核；不得把支付伪装成 `OwnerOutbound`，也不得在 Payment 自建队列/Worker/重试状态机。
6. 跨 Order、Payment、审计、Outbox、job 和 effect 接受的单一命令使用同一个 PostgreSQL Unit of Work；Provider 网络调用永远在事务外。
7. 历史数据通过一次性加密快照迁移，不让 v3 运行时连接旧库。每个源行必须有 receipt，无法安全归属的事实进入 quarantine，不猜 OneID。
8. 历史订单/支付/退款是只读终态，`effect_eligible=false`；迁移不得重放 callback、任务、权益或外部效果。
9. 微信支付与微信小店保持 Provider 默认 disabled；支付宝只迁移 v2 已证明的读取能力，不新增支付宝写能力。

## 备选方案

### A. 整目录复制 v2 `internal/order`

拒绝。它会把 Order/Payment 所有权混在一起，带入 v2 的旧迁移历史与执行内核，并让 v3 失去清晰的 Port 边界。

### B. v3 长期查询旧生产库，只迁页面

拒绝。它制造第二数据源、部署耦合和不可控的一致性窗口，也无法满足 v3 成为唯一主线和禁止长期双主写的约束。

### C. 只迁历史只读，不实现新支付/退款

拒绝作为最终方案。风险最低，但不满足“前后端能力全部搬过来”；可作为 PR-TX03/04 的中间可发布阶段。

### D. 本 ADR 方案：选择性重写 + 一次性可对账迁移

采用。它保留供体 UX 和已验证协议，同时适配当前 OneID、External Effects、UoW 和数据所有权。

## 后果

### 正向

- 一个订单只有一个 Order 真相，一个外部支付动作只有一个 External Effects 执行内核。
- 历史数据完整性可通过结果桶和金额恒等式证明。
- 付款人与受益人不同不会造成客户错误合并。
- 可分阶段先上线历史只读，再灰度开启真实支付/退款。

### 代价

- 需要同时改 Order、Payment、Identity read Port、External Effects versioned Port 和 Composition Root。
- 生产迁移前必须获得受限只读入口并完成两轮快照/增量演练。
- 历史冲突不会被“自动修复”，会形成显式 quarantine/merge candidate 运营工作量。

### 中性

- 本期不实现权益，因此支付成功只发布版本化结算事件；不能在交易页面宣称服务权益已发放。
- 前端模板可原样复用，但 capabilities/readiness 和 OneID 搜索文案需要跟真实后端状态同步。

## 失败模式与控制

| 失败 | 控制 |
|---|---|
| 相同外部身份归到多个 Customer | scoped Identity Resolve + conflict/merge candidate，禁止自动合并 |
| 订单、退款、effect 半提交 | 同一 transaction-bound context；集成测试注入每个写点失败 |
| Provider 超时后重复扣款/退款 | `outcome_unknown` + 原键查询/回调/对账，禁止新键重试 |
| 历史导入触发外部动作 | `record_origin`/`effect_eligible` 约束 + 无 Worker 依赖的 migrator |
| 源 schema 或快照漂移 | schema/manifest digest 不匹配立即停止整个 run |
| 导出泄露或公式注入 | 最小列、RBAC、no-store、行/字节上限、公式转义、审计 |

## 复核条件

- 生产源 schema 盘点后若不存在稳定用户或交易 source key，重新评审迁移策略。
- 若未来引入支付宝写能力，必须新建独立 ADR 和 Provider 合同，不修改历史数据语义来“顺便支持”。
- 若权益要求与支付回调同事务生效，必须先冻结 Product/Entitlement 的 transaction-bound Port，再进入实现。
