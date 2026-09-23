# PRD：HXC 漏斗 / 数据看板

## 1. 产品目标

在 AI-CRM-v3 管理后台提供 HXC 当前用户全量投影，帮助管理员判断有效会员的真实使用情况，并识别已注册但没有当前有效会员的人群。页面路径固定为 `/admin/hxc-dashboard`。

本能力只读取 HXC 当前态，不使用 CRM 客户、订单、问卷、标签或企微跟进数据，也不迁移历史数据。OneID 只提供关联质量指标，不改变漏斗归类。

## 2. 用户与权限

- 管理员和员工的人类会话可读取汇总与分页数据。
- 只有 SuperAdmin 可手动创建刷新任务；写请求必须通过同源 CSRF 与 `Idempotency-Key`。
- API 和日志不得返回 HXC 原始用户 ID、UnionID、手机号、Customer ID 或数据源 DSN。

## 3. 统计口径

基础人群为 `new_version_users.is_deleted=0`，每个源用户恰好进入一个阶段：

1. `active_used`：会员等级不是 `free`、到期时间严格晚于统计时点，且至少发生过一次真实 HXC 使用。
2. `active_unused`：会员当前有效，但不存在真实使用时间。
3. `registered_no_active_membership`：其余已注册用户，包括 free、到期时间为空或已到期。

真实使用由用户消息、普通对话、课程对话、咨询、已完成测评或周复盘产生。订阅表优先于用户表；membership 仅在 user_id 缺失且手机号在 HXC 用户表唯一时回退归因。手机号不离开 HXC 读取适配器。

必须始终满足：

- `total = active_used + active_unused + registered_no_active_membership`
- `total = matched + unmatched + conflict`

## 4. OneID 规则

- 只解析具有 `wechat-open-platform:<platform-id>` scope 的 UnionID。
- 不用手机号、姓名或昵称匹配；不 Provision、Bind、Merge。
- 没有 UnionID或没有命中为 `unmatched`；多 Customer 根，或多个 HXC 账户命中同一个 Customer 根，为 `conflict`。
- 原始 UnionID 只存在于刷新进程内存；投影只保存可空 `customer_id` 和质量状态，API 不返回 `customer_id`。

## 5. 用户体验

页面展示总人数、三段漏斗、OneID 匹配/未匹配/冲突、统计时点、发布时间与源水位。支持：

- 漏斗阶段、会员等级、最近能力、业务阶段、用户分群、OneID 状态筛选；
- 白名单分组和排序；
- HXC 原始用户 ID 精确搜索（仅请求体传输，服务端立即 HMAC）；
- 每页 50 行、最多 100 行的签名游标分页；
- SuperAdmin 手动刷新及任务状态轮询。

筛选只保留在当前页面内，不保存视图、不写 localStorage。页面不提供 CSV、外部分享、发送人管理或群发。

## 6. 同步与容错

- 北京时间 03:15、09:15、15:15、21:15 由 systemd timer 创建持久 River 任务。
- MySQL 使用 `REPEATABLE READ READ ONLY` 快照和 1,000 行 keyset 批次；聚合仅处理当前批次用户。
- 每次刷新先做 schema 与 `EXPLAIN` preflight；失败不修改 HXC schema。
- HXC 读取和 OneID 解析发生在 PostgreSQL 事务外；最终行、汇总、刷新收据和审计在单一事务发布。
- 保留最近 8 个成功版本。失败时继续展示上一成功版本；超过 8 小时显示 stale 告警。

## 7. 验收标准

- 生产页面没有 Mock、硬编码统计或 100 行总量限制。
- HXC 源人数、投影人数、API total 一致，两条恒等式成立。
- 抽样用户的会员状态、真实使用、阶段和 OneID 质量与源/Identity 查询一致。
- `/readyz` 返回目标 release SHA；0028 migration 已应用；effects worker 与 HXC timer active。
- 首次手动刷新和随后一次定时刷新均成功，刷新失败演练不会删除上一成功版本。

## 8. 架构分类

- OneID：涉及，仅通过批量只读 Port 解析 scoped UnionID。
- 持久化：涉及，使用 PostgreSQL 不可变投影、原子发布与 River 内部持久任务。
- Provider：涉及只读 HXC MySQL，不产生外部写入。
- External Effects：不涉及，因此不创建 Outbox 或外部效果状态机。
