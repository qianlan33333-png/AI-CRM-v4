# External Read v1 生产覆盖快照

状态：只读审计证据，非发布确认。快照时间：2026-09-13 11:49（Asia/Shanghai，03:49 UTC）。

本报告只记录 Root 在生产 PostgreSQL 上核实的聚合计数，不包含手机号、UnionID、客户 ID、订单号或任何其他客户字段。它描述的是当前生产数据覆盖，不能替代新 `/open/v1` 代码、部署、有效 Client、Token、HTTP 和工作台业务验收。

## 快照

| Owner / 数据集 | 聚合结果 | 解释 |
|---|---:|---|
| 订单 | 927 | 全部订单行；不等于每行都能安全绑定一个客户 |
| 订单 payer 关联 active phone | 734 | 只按 payer 关系统计；不代表 beneficiary 覆盖，也不与另一口径相加 |
| 订单 payer 关联 active UnionID | 579 | 只按 payer 关系统计；UnionID 必须保留其开放平台 scope |
| Survey resolved | 1,049 | 当前已解析问卷提交 |
| Survey unresolved | 513 | 当前未解析问卷提交，不能猜测客户 |
| Survey anonymous | 25 | 匿名问卷提交，不能补造身份 |
| Survey legacy projection | 1,562 | 历史投影行；不等于全部已完成 OneID 打通 |
| Survey resolved 且 active phone | 1,049 | 仅表示当前 active phone 存在，不表示 phone assurance 为 verified |
| Survey resolved 且 active UnionID | 1,049 | 仍须按 scope 解释，不能任取多值 |
| Identity active phone | 1,557 | 全部是 `phone:cn11`、`declared`；当前没有 verified phone |
| Identity active verified UnionID | 1,025 | 这是带 verified assurance 的当前 UnionID 聚合，不表示每个 customer 只有一个值或每个 scope 都可跨系统使用 |
| Radar native events | 4 | 技术阶段各 1 条：`identity_resolved`、`landing`、`oauth_verified`、`redirected`；均已 resolved |
| Radar legacy events | 0 | 当前没有 legacy Radar event 行 |
| Message archive group | 442 | Owner 聚合数量 |
| Message archive private | 5,215 | Owner 聚合数量；当前 legacy projection 为 0 |
| Legacy commerce history | `closed` 180、`paid` 638、`payment_failed` 4、`refunded` 101 | 历史状态分布；不能据此假设 payment/refund 明细已全部可读 |
| v3 checkout | `paid` 2、`pending` 2 | 当前 v3 checkout 行状态分布 |

本快照中的 1,025 条 active verified UnionID 使用 scope
`wechat-open-platform:wx0ca836834b18e989`；这是范围标识，不是 secret 或个人
UnionID。

## 解释边界

`phone:cn11` 是身份 scope，不是 `phone:e164`，且本快照中 assurance 为 `declared`。因此只有在最终实现明确返回真实 fact 的 `scope`、`assurance`、`source` 和 `status` 时，调用方才能知道这是一条已声明身份；不能把它硬编码为 `verified` 或把值改写成另一个 scope。

订单的 payer 与 beneficiary 是两个不同的业务关系。上表的订单手机号和 UnionID 只按 payer 统计，不能把 beneficiary 当作当前客户，也不能将任意一侧 ID 暴露给没有相应授权的调用方。两侧都为空的订单仍需保留为未解析事实。

问卷、聊天、Radar 和历史 commerce 数据之间的计数没有可加性保证。legacy projection、resolved customer、active identity 和 native event 是不同 Owner 的事实集合，不能用一个集合的计数填补另一个集合的缺失。

## 对 v1 上线的影响

1. `identity.get` 必须以 customer 的 canonical lineage 读取 Owner facts，保留每个 fact 的实际 scope、assurance、source、status；无 scope、wrong scope、多 UnionID、pending/conflict 和无客户行都必须有明确语义。
2. 不能以“只返回 verified phone”为上线条件，因为当前生产没有 verified phone；应返回经授权的真实 declared `phone:cn11` fact，并在字段中保留其 assurance。是否允许原始值由专用 Client 的显式 capability 和本轮授权决定。
3. 订单只读必须分别保留原始 `order_id`、source provenance、payer/beneficiary 事实和 canonical customer 关联规则；不能把付款、退款申请或 history 状态误标为已退款。
4. 生产当前仍为旧版本，新的 `/open/v1` API 尚未部署；GitHub 认证失效也阻止当前合并/发布。代码、CI、部署、有效 Client、Token、HTTP、真实读取和工作台同步必须分别验收。
5. 本报告不授权数据修补、隐式建客、自动合并、补发 UnionID 或把匿名/未解析记录归到某个 customer。

指南入口：[external-read-v1-ai-guide.md](external-read-v1-ai-guide.md)。
