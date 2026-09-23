# Referral 活动配置扩展（配置与归因契约）

## 业务判断

本轮只扩展 Referral 活动配置和其稳定接口，不拥有商品、订单、Payment 或 Distribution 表。一个活动最多绑定一个商品目标（`product_type` + `product_id`）；产品目标是服务端配置事实，不能由浏览器在参加、下单或排行榜请求中改写。

- `team_mode=team` 时，参加记录可以保存一个活动内的 `team_id` 以便汇总；战队没有队长审批、特殊邀请或佣金权限。`team_mode=individual` 时，新的参加记录的 `team_id` 必须为空，个人榜仍可统计，团队榜及团队汇总不纳入这些记录。历史战队/战队事实保留为审计，但切到 individual 后不在活动展示或统计中使用。
- `team_mode` 只有在服务端当前时间早于活动开始且活动尚未进入过已开始生命周期时可修改；开始时间向后调整不能重新解锁。发布为 individual 前不能存在已实际参加的 team 记录。
- `qualification_mode=free_signup` 保持现有显式可信会话参加规则；`qualification_mode=product_purchase` 要求指定商品的有效本人购买证据（付款人和受益人均在同一可信 canonical lineage、支付已确认、实付大于零、无成功/在途/未知退款）。Referral 只调用稳定资格 Port，不读取 Order/Payment/Distribution 表。购买证据不通过时保持拒绝/待复核，不能当作允许。
- 商品无推广来源的购买者在满足 product_purchase 后仍可参加活动；没有来源时不写邀请关系，也不加入任何 team。此购买记录不会自动给他人计邀请分，也不改变当前全局关系。
- `leaderboard_metric=invites` 按有效直接邀请数排序；`leaderboard_metric=sales` 按活动归因且退款复核后的有效销售事实排序。销售榜返回有效销售金额和有效订单数两个字段，排序采用后台配置的一个 metric（当前默认有效销售金额，订单数作为并列/展示事实）；不从 current relationship 推断订单归属。
- 榜单周期公开值为 `all` 或 `day`。旧 `total` 作为只读兼容输入规范化为 `all`，旧周榜只保留历史读取兼容，不作为新活动配置选项。

## 两轴分类

OneID: reads canonical customer through the existing trusted session/qualification Port. Referral neither resolves external identities nor creates, merges, or reassigns customers.

Persistence: local PostgreSQL transaction. Campaign configuration, CAS version, team mode lifecycle guard, and any future immutable order attribution snapshot are Referral-owned facts; command receipt, audit, and outbox remain in the same UoW. No new durable worker is needed for configuration reads.

External Effects: not involved. No Payment/WeChat/provider write, no new queue, retry, or reconciliation state machine. Qualification is a server-side read Port and must fail closed; no provider call is made by Referral.

## 配置字段与 HTTP DTO

`Campaign` 公开字段：

- `team_mode`: `team | individual`；默认 `team`（兼容已有活动）；开始后只读。
- `qualification_mode`: `free_signup | product_purchase`；默认 `free_signup`。
- `product_id`: `product_purchase` 必填的 opaque Product ID，其他模式为 `0`。
- `product_type`: `standard_product | service_period`；与 `product_id` 成对保存。
- `leaderboard_metric`: `invites | sales`；默认 `invites`。

管理员创建/更新沿用 `/api/admin/referral/campaigns` 的组合请求和 `expected_version` CAS；缺少新字段的旧客户端创建使用以上默认值，缺少新字段的更新保留当前值。响应的 campaign/summary/view 始终返回规范化字段。更新失败时商品目标、配置和活动事实整体回滚。

`LeaderboardQuery.period` 接受 `all`、`day`；服务器返回规范化 `period`。`kind=team|in_team` 在 individual 活动返回业务冲突；`kind=personal` 可返回个人成绩与销售指标。销售榜的 `LeaderboardEntry` 增加 `sales_amount_minor`、`sales_order_count`，邀请榜仍返回 `score`。

订单归因后续由 Order 的同一 checkout UoW 传入不可变活动上下文；Referral 只提供稳定 `ReferralAttributionReader/Writer` Port，接收 `campaign_id`、产品目标、推广来源和已确认销售快照，绝不读取 current relationship 作为归因。

## 调研与复用

GitHub 上的 #384（现有 Referral 活动）和其 `docs/plans/2026-09-18-referral-campaigns.md` 已验证本仓的 OneID、PostgreSQL UoW、River 与审计边界；该实现明确是无商品/无购买活动，因此本轮只复用其 campaign/participant/leaderboard Owner 和事务模式，不复制外部项目或 Distribution 表。榜单排序参考 Nakama/go-leaderboard 的稳定窗口与排序思路，但不引入 Redis 或第二套排行榜存储。

## 验收重点

1. 新建/读取/更新四种配置组合和旧 DTO 默认值；product_purchase 缺商品、商品类型不匹配、重复活动目标均拒绝。
2. team_mode 在开始前可改、开始后 CAS/时间均拒绝；改开始时间不能解锁；individual 发布前拒绝已有 team participation；individual 新参加的 team_id 为空且不进入 team 榜。
3. free_signup 不触发资格 Port；product_purchase 使用同一 UoW 的可信资格 Port，缺证据/退款在途/未知均 fail closed。
4. 榜单 `all/day`、销售金额/订单数字段、退款后有效值和并列排序稳定；旧 `total` 规范化，旧周榜读取兼容。
5. 所有配置与归因命令使用 Referral Owner 的版本、幂等、审计、Outbox；不改 Distribution 表、不查询其他领域表、不新增身份或外部效果内核。
