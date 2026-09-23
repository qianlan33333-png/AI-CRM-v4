# 渠道回调：归档渠道的显式不执行收据

## 1. 结论与边界

本 PRD 只解决已经验证的企微 `add_external_contact` / `add_half_external_contact`
回调，在其可靠归属到**归档或停用**渠道时，被当作可重试故障、反复回滚的
问题。它不恢复渠道，不重放任何历史回调，不发送欢迎语，也不补打标签。

生产审计给出的脱敏事实为：渠道 `1`（`code123`）当前配置版本为 `6`、状态为
`archived`；欢迎语和入客标签已配置；12:57:46 的新增外部联系人回调已经进入
`callback_lifecycle` 自动重试七次后达到 `failed`（8/8）；已接受的 welcome intent 为
`channel_unavailable`，没有 external effect 和 Provider 发送；没有 entrant、分配或
标签命令。服务及总开关均为 active/on。

因此这次现象不是“queued 即发送成功”，而是两个独立结果：

| 能力 | 本次已审计结果 | 解释 |
| --- | --- | --- |
| 欢迎语 | `channel_unavailable`、无 effect、未发送 | 归档渠道不能取得可运行配置；已接受 intent 仍保持原样。 |
| 客服分配/入客标签 | 没有 entrant/assignment/tag command | entrant action 读取同一 active gate 后返回不可用，外层把它当异常回滚。 |

这个 PRD 不把已配置的文案或标签等同于已执行，也不把任何通用 `queued` 状态
等同于 Provider 成功回执。

## 2. 已核验的代码链路

1. 回调验签、入 Inbox 后，`internal/wecom/callback.go` 可在同一事务接受欢迎语
   intent；`AcceptCallbackWelcome` 对不可用配置写入不可发送收据
   `channel_unavailable`，不创建 effect。
2. `ExternalContactLifecycle.ProcessWithin` 经 OneID 解析/显式 provision
   `customers.id`，再写跟进关系、目录投影和 entrant receipt；这些本地事实与 Inbox
   完成同属 PostgreSQL Unit of Work。
3. 对可信 State 归属，`correlateEntrant` 当前先写 `channel_attributed` entrant receipt，
   再调用 `AcceptEntrantActions`。后者读取配置、选择客服，并在同一 UoW 内通过
   Customer TagCommand Port 接受客户标签命令。
4. 改动前，两个配置读取函数都先按不可变 runtime asset 查版本；没有 runtime row 时，已经有
   legacy fallback：仅在渠道 `active` 且存在未退休的
   `legacy_verified_active` asset 时，读取**当前** channel config。归档时该查询没有行，
   返回 `ErrEntrantActionUnavailable`。runtime-asset 分支本身当时尚未过滤
   `channels.status`，所以修复将 runtime 与 legacy 收敛为同一状态判定规则。
5. `InboxProcessor` 将这个 error 包为 `callback_lifecycle`，将整个第一个事务回滚，
   之后把 Inbox 标为 retryable。因此上一步已经写入的 OneID、关系和 entrant receipt
   都不会留下，并造成重复处理。

生产审计中的 state binding `asset_version=2000000019` 不是 runtime asset ID，也不应
与 legacy asset `1000000009` 以相似数字关联。迁移代码对非冲突 legacy scene 使用
`2_000_000_000 + scene_alias.id`；故该值编码为 scene alias `19`。发生冲突时才使用
基于 binding digest 的 `>=4_000_000_000` 编码。这个代码规则解释格式；生产审计仍须
保留 scene alias 19、binding 和 legacy asset 的关联查询结果，作为本次归因证据。

## 3. 最终根因判断

**已确认根因**：归档状态有意禁止运行欢迎语、客服分配和标签；但 entrant 的合法
“不执行”被复用了 `ErrEntrantActionUnavailable`。它在生命周期边界没有被分类，因而
被误作暂态失败并回滚/重试。

**不作为本次根因**：runtime `channel_acquisition_assets` 不存在。synthetic legacy
binding 正是 fallback 支持的输入。生产最终审计确认 channel 1 仅有一条
`contact_way_qrcode` legacy 记录，且它为 `legacy_verified_active`、`retired_at IS NULL`；
fallback 的资产条件均成立，唯一失败条件是 `channels.status='archived'`。

生产审计附件仍应保留 binding、scene alias 19、有效时间和 callback occurred_at 的
关联查询，以证明归因；但这不是修改 legacy compatibility 的理由。渠道恢复启用仍是
独立运营动作，不能成为处理历史 callback 的前置或副作用。

## 4. 最小实现

### 4.1 明确的可跳过条件

在 Channel port 定义一个窄的、可判别的 entrant-action 结果，或等价的专用 typed
sentinel；它只能表示：State 已可信归属，渠道状态已在同一事务以共享行锁读取为
`inactive` 或 `archived`，且该 State 的 runtime asset 或 verified/unretired legacy
asset、当前配置和可选择客服均已验证。状态检查覆盖 runtime asset 与 legacy fallback，
不能只给 legacy 分支加例外。

不得把以下情况归入该结果：无效 callback、未匹配/冲突身份、state 不可信、runtime
asset/verified legacy asset 缺失或已退休、配置/assignee 不完整、容量已满、tag command
冲突以外的错误、数据库错误、外部效果接受错误或 Provider 结果未知。这些保持既有
失败分类和受控重试/人工处置；绝不吞掉为 no-action。

实现应通过 Channel port 返回该已分类结果，不能让 WeCom 跨域读取 `channels` 表，
也不能以字符串匹配泛化 `ErrEntrantActionUnavailable`。

同一共享行锁也适用于 `readWelcomeConfig`：先验证 runtime/legacy 资产和配置，再对
inactive/archived 只记录 `channel_unavailable` no-send intent，而不是让 runtime asset
分支在归档渠道接受 welcome effect。锁持续到 callback 接受事务结束，因此渠道归档与
effect 接受必然前后串行。已有 callback 的 welcome intent 因 callback-key 幂等而保持
不变；该检查只影响之后首次接受的 callback。

### 4.2 同一事务中的完成语义

对于上述唯一条件，生命周期仍完成已验证的 OneID/关系/回调事实，并保留已有的
`channel_attributed` entrant receipt（它保留可信 State binding 归因）。同时在同一个
Inbox 完成收据中追加既有 `ignored` 结果码，表示“回调已完成，渠道动作按当时停用
状态跳过”。这复用现有 receipt 与 `OutcomeIgnored`，不创建队列、补偿 worker、重试
状态机或新的动态配置读取。

此结果不能创建 `channel_entrant_assignments`、`channel_entrant_actions`、Customer
TagCommand 或任何 External Effect；特别不能把欢迎语的 `channel_unavailable` intent
改成 queued/attempted/executed，也不能为它申请新的 20 秒 welcome 凭证。

若现有公开 entrant receipt 无法表达“可信归属但动作跳过”，优先保留
`channel_attributed` 作为归因事实、用 callback processing receipt 的 `ignored` 作为
执行收据。只有审核认定运营查询必须在 entrant projection 中区分该原因时，才单独
设计一个带 binding 的 immutable status；本 PRD 不预先授权这种 schema/API 扩张。

### 4.3 历史快照与后续启用

State binding、回调 occurred_at、welcome intent、已接受 effect 和 receipt 均不改写。
运营在独立流程明确恢复 `active` 后，只有**新的**回调可走 legacy fallback 并冻结当时
的当前配置。不得扫描或自动重放此前写为 `ignored` 的 callback；不得补发欢迎语或
标签。旧渠道码保持其既有可信 State 归因，配置保存对后续扫码的作用由既有 legacy
fallback 的 current config 读取实现，不能修改历史接受快照。

## 5. 架构分类

- **OneID/外部身份：涉及。** 外部企微身份必须继续由 Identity Port 解析或显式
  provision，业务主键始终是 `customers.id`；不得凭渠道、客服或 State 隐式建第二个
  Customer。
- **持久化与外部效果：涉及。** callback Inbox、receipt、OneID、关系和 audit 必须
  在同一 PostgreSQL Unit of Work；企微写只可经 outbound + external effects。本修复
  仅对停用渠道产生本地 no-action receipt，不新增效果、队列、worker 或盲重试。
- **跨域：** WeCom 只消费 Channel port 的分类结果；不读 Channel 表。Channel 不直接
  调用 Provider，标签仍通过 Customer TagCommand port。

## 6. 验收范围与测试证据

以下是本修复的验收范围。已执行的测试证据仅限本节末尾列出的命令；其余条目在
PR 审核中按结果确认，不能视作已通过。

1. 一个可信 legacy State、verified legacy asset、配置 6、`archived` 渠道的新增回调：
   Inbox 最终为 processed，不产生 `callback_lifecycle` 重试；OneID customer/identity、
   已验证关系、entrant attribution 和 processing receipt 在同一提交中存在；结果码包含
   `channel_attributed` 与 `ignored`。
2. 上述回调没有 assignment、entrant action、Customer TagCommand、External Effect，
   且没有新的 welcome intent/effect。既有 `channel_unavailable` welcome intent 的字段和
   deadline 不变。
3. `inactive` 与 `archived` 都满足相同 no-action 语义，且分别覆盖 runtime asset 与
   verified legacy synthetic binding；两者都不得创建 welcome effect。缺失或退休 legacy
   asset 即使渠道停用也必须留在 retryable 路径。`active` + verified legacy
   asset 与 `active` + runtime asset 都仍可读取各自受保护的配置路径，正常创建分配并
   独立接受标签命令。欢迎语与标签分别断言各自的 accepted/无 effect/Provider receipt
   状态，不以 queued 代表发送成功。
4. 同一 callback 的重复投递保持 receipt/relationship/identity 幂等；先收到归档
   no-action 后再把渠道恢复 active，重投历史 callback 仍不执行；一个新的 callback
   才可按 active 路径执行。
5. active 渠道的 asset 缺失、非法 assignee、数据库失败、tag command 非冲突错误、
   外部效果接受失败和 outcome unknown 不会被转为 ignored；保持现有失败/重试或明确
   拒绝路径。
6. 跑与改动相关的 Go package/integration tests、`go test ./internal/wecom ./internal/channel`
   及对应 composition integration test；不把 mock、HTTP 200、queued 当作真实 Provider
   发送验证。

本地隔离 PostgreSQL 已执行并通过：

- archived runtime callback Processor 回归：processed、`channel_attributed` + `ignored`、
  无 retry/effect/assignment/action/tag command；恢复后重复投递仍为 no-op。
- archived verified-legacy synthetic binding Processor 回归：无 action/effect。
- active runtime 与 verified-legacy synthetic binding 的 Processor 正向回归：可信归因、
  分配和 entry-tag external-effect 接受均存在；其 `queued` 只表示 effect 已被接受和
  入队，不表示 Provider 已发送。
- inactive/archived 下 verified legacy asset 有效、但合法 ratio 配置对该 callback 无可选
  客服时，仍是 `callback_lifecycle` retryable，未被 ignored 吞掉。
- 缺失 runtime/legacy asset 在 active/inactive/archived 三种状态下均为
  `callback_lifecycle` retryable，未被 ignored 吞掉。
- `go test ./internal/wecom -count=1` 与 `go test ./internal/channel -count=1`。
- 现有 Customer TagCommand composition integration：Channel entry tag 和通用标签的
  capability routing 各自独立。

## 7. 非目标与发布前提

- 不恢复生产渠道、不重放当前 callback、不发送欢迎语、不打标签、不修改任何生产
  receipt 或配置。
- 不修复 runtime/legacy asset 版本关联，也不同步 asset version；已有 legacy fallback
  是本次方案的保护前提。
- 不新增诊断 UI、版本面板或历史补偿功能。
- 发布后仍要用**新的、授权的** active 渠道真实回调分别核验欢迎语 Provider receipt
  与标签 Provider receipt。

## 8. 本次生产的操作顺序

已审计的 Inbox 22 已在自动重试 8/8 后为 `failed`；它和欢迎语 no-send intent 都是
不可改写的历史证据。本次不通过 SQL 改状态、不调用管理 retry API，也不因启用渠道而
处理它。修复发布并经隔离测试后，运营若决定恢复渠道，只能先显式恢复 `active`，再用
**新的扫码**进行欢迎语和标签的独立真实回执验收。已失败的历史 callback 不会自动 claim；
任何未来人工 retry 都必须先由授权审核，并在 archived 状态下完成 no-action 后才可能
考虑启用，绝不能在 active 状态重放。

## 9. 外部协议参考（只读）

- 企业微信官方：联系人联系我管理、State 与回调字段：
  <https://developer.work.weixin.qq.com/document/path/92228>
- 企业微信官方：客户联系事件：
  <https://developer.work.weixin.qq.com/document/path/92130>
- 企业微信官方：欢迎语，WelcomeCode 20 秒且仅一次：
  <https://developer.work.weixin.qq.com/document/path/92137>
- 企业微信官方：客户标签标记：
  <https://developer.work.weixin.qq.com/document/path/92118>
- GitHub 只读参考：go-workwx 的 State/欢迎语模型：
  <https://github.com/xen0n/go-workwx/blob/v2/external_contact.md.go#L1729-L1760>
  与 <https://github.com/xen0n/go-workwx/blob/v2/external_contact.md.go#L2050-L2088>
- GitHub 只读参考：fastwego 的客户标签请求：
  <https://github.com/fastwego/wxwork/blob/master/corporation/apis/external_contact/customer_tag/customer_tag.go#L512-L526>
