# PRD：企微外部联系人 ID 备注补打、增量维护与侧边栏 OneID 展示

## 1. 目标与边界

用户目标是将已确认企微“员工—外部联系人”关系中的外部联系人 ID 写回旧能力所使用的企微字段：首次全量补打，之后由新客户进入事件持续补齐；企微侧边栏显示 CRM 的 **OneID**。

旧合同已冻结为：企微 `externalcontact/remark` 的请求中写 `description`，值是原始 `external_userid`；不是 `remark`，不加 marker。增量链路先读取当前 `follow_user.description`，已有目标 ID 则跳过，否则保留原文并以换行追加 ID。旧的 `identity_bridge_service.py` 直接调用 Provider 并可能覆盖字段，不能迁入 v3；v3 只能复用它的行为合同，统一走 Outbound/External Effects。

这两个标识不能互换：

| 位置 | 显示或写入的值 | 含义 |
| --- | --- | --- |
| 企微客户 `description` | 原始 `external_userid` | 企微企业 scope 下的外部联系人标识；备注 API 同时指定跟进员工 `userid`。 |
| CRM 侧边栏 | OneID（canonical `customers.id` 的稳定展示值） | CRM 客户主键；由后端在已绑定的侧边栏上下文中得出。 |

`external_userid` 的身份事实仍通过 `identity` 保留 `kind=wecom_external_userid`、`scope=wecom-corp:<corp_id>`、`assurance=verified`。它不是 CRM OneID，也不是可跨企业或跨应用复用的全局键。客服跟进关系属于 `(corp_scope, employee_id, customer_id)`；备注是该关系级资料，不能当作客户级字段写入或覆盖。

本期不做：用备注反向建客、从浏览器信任或展示 raw `external_userid`、自动合并 Customer、根据空列表推断没有历史客户、覆盖或截断人工备注。

## 2. 现有基座与结论

现仓已有可复用的全量客户读取基础：`CustomerSyncService` 依次读取跟进员工列表和 `batch/get_by_user` 分页，并为可信联系人调用 `ProvisionVerifiedIdentity`；运行记录已有 cursor、CAS、重试状态、计数、审计和完成对账。`DirectoryProvider` 明确为只读，不能承担备注写入。

现仓也已有企微回调 Inbox、异步处理和侧边栏 `BootstrapViewer`。侧边栏先以 `external_userid` 解析一个已存在的 CRM Customer，再签发 Customer-scoped token；当前安全 profile 刻意不返回 raw 外部标识。因而本期应在现有工作台响应上新增安全的 `oneid` 展示字段，不能在浏览器另行匹配身份。

需要新增的不是另一个身份或队列，而是 Outbound 所有的“企微联系人 description”外部效果。读取、写入和写后验证必须分开记录。为使 River 重试、回调重放和全量 run 恢复后仍可执行，新增 Outbound 所有的 immutable description dispatch/receipt：它只保存本地 `customer_id`、跟进员工本地引用、目标/原文/策略摘要、`effect_ref` 与完成状态，绝不把原始 `external_userid` 或人工 `description` 放进 EER、任务参数或结构化日志。执行时只经既有 Identity Port 解析带 corp scope 的 verified external ID；即时 Provider 读回必须返回相同 external ID 与计划员工的 follow 记录，callback 生命周期关系行不能充当历史目录全量资格门槛。EER 仍是唯一队列、重试和效果状态内核。

分类结论：

```text
OneID：涉及。读取 canonical customers.id，并仅通过 identity Port 解析带 corp scope 的 external_userid。
Persistence：内部可恢复任务 + Provider read + Provider write/external effect。
External Effects：涉及。企微 `description` 写入由 outbound 提交至既有 externaleffects/jobqueue；不新增业务 Worker、重试内核或第二套客户主键。
```

## 3. 备注规则

1. 目标值固定为当前关系的原始 `external_userid`，绝不包含 OneID 或 marker；写入字段固定为 `description`。
2. 旧链路和当前新仓读取的字段名不同：旧合同的 Provider detail 为 `follow_user.description`，新仓现有只读投影为 `follow_user.remark`。迁移时必须为 `description` 补齐独立、经 Provider 契约测试的读取/写入字段，不能把新仓 `remark` 误作旧 `description`。
3. 若当前 `description` 已包含目标 `external_userid`，记录 `already_present`，不写 Provider。
4. 否则保留原 `description` 并追加 `\\n<external_userid>`；原值为 nil 或空时只写 ID。不可改写、归一化、删除或截断人工内容。每次写入前必须即时重读目标员工关系，将原文摘要与计划快照比较；变化则标记 `description_changed` 并保守跳过，不能把非空且缺 ID 默认为成功。若 Provider 仅支持覆盖式 set 且没有并发版本/CAS，这只能缩小、不能消除企微人工编辑在最终读写窗口内丢失的风险：本地 relationship lock 仅串行本系统操作，不能锁住企微人工编辑。这一剩余风险必须以目标应用实测和产品策略明确接受后才可上线。
5. 目标文本超过经证实的 Provider 限制，或当前员工无权限，记录可审计的安全错误码 `too_long` 或 `not_authorized`，跳过该关系。不得截断人工内容，也不得把跳过计为成功。
6. Provider 接受写入只将外部效果记为 `executed`。写后单联系人读回另记为 `readback_pending`、`readback_confirmed` 或 `readback_failed`；读回失败不会倒改一个已经执行的写为 `outcome_unknown`。仅当写调用结果本身无法判断时，效果状态才是 `outcome_unknown`，并且只能使用原 key 对账。

所有审计、外部效果表和日志只保存 HMAC/哈希摘要、run/effect/receipt ID、员工与客户关系的安全引用以及状态码；不记录 `external_userid`、备注全文、token 或 Provider 原响应。

## 4. 全量补打

### 4.1 入口与范围

管理员以有权限、带幂等 operation key 的“全量补打”创建一次 run。它调用现有持久化同步链路：

```text
get_follow_user_list
  → batch/get_by_user（每员工游标分页）
  → verified identity / canonical Customer
  → 每条有效跟进关系创建或重放同一备注效果
  → 备注前读、写入、读回
  → run reconciliation
```

不能假设企微存在“全企业客户”单接口；结果受应用可见范围、成员授权和 Provider 历史窗口限制。接口无数据、无权限和网络失败分别计为 `empty_observed`、`not_authorized`、`retryable_failed`，不能汇总为“没有历史客户”。

### 4.2 可恢复性与计数

run 采用已有 PostgreSQL/River 任务基础、游标和 CAS；同一 run key 可重入并 replay。全量和增量必须共用同一关系级 receipt key：`H(corp_scope, employee_id, external_userid_digest, description-contract-v1, target_value_digest)`；run ID 不得进入该 key。由此二者对同一关系/同一版本收敛到一个 immutable intent。单一活动全量 run 防止并发扫描；失败在原 run 上恢复。

旧批处理是安全子集，而不是本期完成定义：它仅扫描 `relation_status='active'` 且本地 `description` 为空的关系，先发 detail effect 再发 update effect，默认上限为 24,999 个联系人、每批 500，并要求显式授权。这证明全量过程应有硬上限、预览和恢复，但不能替代用户要求的“全量用户补打”。v3 全量 run 必须枚举全部 active 关系、对每条真实读 `description` 后执行上述“已含则跳过、否则追加”规则，并分别报告空描述写入、人工描述追加、`description_changed` 跳过和超长跳过；非空且缺 ID 不得静默计为成功。

Run 至少展示：发现关系数、identity 未解析/冲突数、已含目标 ID 数、已排队数、写入已执行数、读回确认数、超长跳过数、无权限跳过数、可重试失败数、终态失败数和未决 `outcome_unknown` 数。详情还必须给出当前 run 的 distinct `(customer, corp_scope, employee)` Provider 来源分母：观察到的关系数、description 字段明确投影数、字段省略数、已提交 Outbound 数和已投影但未提交数。字段省略是 source-to-projected gap，已投影但未提交是 projected-to-Outbound-intent gap；二者独立、不得推断为空，也不得计入完成。完成门槛为输入关系数等于各终态和未决状态之和；只有 `readback_confirmed + already_present` 是已完成补打。

限速由 Outbound Provider adapter 按企微响应和明确配置执行，保持可续跑；不在 callback handler 或浏览器中批量请求。

## 5. 新增客户的持续维护

1. 企微 webhook HTTP handler 仍只做验签、持久 Inbox 与 ACK；绝不在请求内读详情或写备注。
2. 已有 callback worker 在可信新用户事件完成 OneID 解析或 provision、关系更新之后，原子地登记一条关系 `description` 意图（或以同一 callback 业务 transaction 登记 Outbox event）。这样回调 ACK 不被 Provider 写延迟影响。
3. 本期连续触发只接入旧合同和回调 schema 均已证实的“新增完整外部联系人”事件。`add_half_external_contact`、`edit_external_contact`、`del_external_contact` 与 `del_follow_user` 维持既有回调合同和审计，不因本期备注需求扩大为自动读写或取消逻辑；后续变更必须单列行为合同。
4. 回调重复、乱序和全量补打重叠时，关系级幂等键收敛为同一 immutable intent。已 `outcome_unknown` 的写仅可用原 key 查证、可信回调或人工对账关闭。
5. Provider 读取/写入不可用时，Customer/关系的主回调处理仍可在 Inbox 中完成；备注意图单独处于 retryable/unknown，绝不把已接受的回调降级为“未建客”。

启用 v3 连续链路前，必须确认旧仓的 identity/profile-description worker 对同一企微企业与关系没有并行真实写权限；运行时只允许一个 owner 负责该字段。不能以两个“同样追加”工作者并行运行替代迁移，这会形成双主写并破坏快照校验。

## 6. 侧边栏 OneID

后端在已通过 `BootstrapViewer` 解析并签发 context token 后，将 canonical Customer 的稳定 OneID 展示值添加到 bootstrap/workbench profile。必须直接复用 `customerdomain.CanonicalOneIDLabel(customerID)` 的既有 `CID-<id>` 契约，而不是另行拼接或引入新格式。值必须后端生成，不能由 `external_userid`、姓名、手机号或前端缓存拼接。

前端只在 `state=ready` 且响应含有效 `oneid` 时展示“OneID”；`viewer_session_required`、`customer_not_bound`、缺失字段或失败态展示空态，不伪造 `CID-*`，更不回显 `external_userid`。切换企微会话时复用现有 bootstrap request-version/AbortSignal 机制：旧客户的响应不能覆盖新客户的 OneID。

验收覆盖：同名不同客户、一人多员工、切换 A→B 的慢旧响应、未绑定外部联系人、身份冲突和 SDK 获取失败。所有情况下不得串号或输出 raw `external_userid`。

## 7. 接口、数据与测试建议

- 新增管理员 run 的创建、列表、详情和恢复 API；详情返回计数、状态、safe error code、effect/receipt 引用，不返回 PII。
- 在 Outbound/External Effects 增加唯一的 `description` 更新 kind 和 Provider adapter；目标、源、payload、policy 四个 digest 与关系级 receipt key 固定，业务意图到 `effect_id` 的绑定归所属模块保存。
- 新增备注意图/run/line/receipt 的 Owner 表及迁移，业务行、审计、Outbox/效果受理同一 PostgreSQL UoW。不要让 wecom 或 customer 直接写 external-effects 表。
- Provider adapter 实现备注写和单联系人读回；读取接口保留在 wecom 的已验证 connector 边界，Composition Root 注入稳定 Port。
- 侧边栏 OpenAPI 和 generated client 同步增加 `oneid`，并让 DOM 显示非可编辑字段。

最低验证：全量 run 的分页、断点续跑、重复提交、限速、各计数对账；同一客户多员工分别备注；已有 ID 跳过；人工备注保留；超长/无权限跳过；写后读回；外部调用 ambiguous 后原 key 对账；回调 ACK 与备注延迟隔离；OneID 空态、切换抗串号以及 raw `external_userid` 不泄漏。

## 8. 公开参考与待确认项

- 旧仓冻结合同（审计 worktree `codex/classify-profile-backfill-terminal-20260825`，提交 `36869201403d416d6f0db54ab27b78545994a0fe`；**不是**旧仓 `origin/main` 的 `dd8d60d`）：`/Users/qianlan/Documents/New project/.worktrees/classify-profile-backfill-terminal-20260825/aicrm_next/channels/channel_entry/identity_external_effect.py`
- 同一冻结 ref 的全量子集实现：`/Users/qianlan/Documents/New project/.worktrees/classify-profile-backfill-terminal-20260825/aicrm_next/channels/channel_entry/profile_description_backfill.py`
- [企微客户联系概述镜像](https://github.com/wxkingstar/doc-hub-mcp/blob/main/wecom/001-%E4%BC%81%E4%B8%9A%E5%86%85%E9%83%A8%E5%BC%80%E5%8F%91/002-%E6%9C%8D%E5%8A%A1%E7%AB%AFAPI/015-%E5%AE%A2%E6%88%B7%E8%81%94%E7%B3%BB/001-%E6%A6%82%E8%BF%B0.md)
- [客户联系 API 封装，含 remark](https://github.com/fastwego/wxwork/blob/master/corporation/apis/external_contact/customer/customer.go)
- [外部联系人事件字段参考](https://github.com/xen0n/go-workwx/blob/v2/rx_msg.md.go)
- [企微侧边栏模板](https://github.com/wecom-sidebar/wecom-sidebar-qiankun-tpl/blob/main/apps/sidebar-app/README.md)

旧仓已提供字段、格式、active 关系与补打限额证据；官方实现参考确认 `description` 最大 150 个字符，因此 v3 以 150 Unicode rune 作本地硬拒绝上限，绝不截断人工内容。开发前仍必须在目标企微应用环境确认调用主体权限、同一联系人多跟进员工语义、写入覆盖式风险和 readback 可见性。上述任一证据缺失，不能发起生产全量补打或自动写入。

## 9. 本次交付契约

- 管理员 API 为 `POST /api/admin/wecom/contact-description-backfills`、`GET /api/admin/wecom/contact-description-backfills/{run_id}`，以及同一路径的 `GET`/`POST .../{run_id}/readback`。创建入口复用既有 `CustomerSync` manual full run，不另建扫描或队列；四个入口均要求管理员权限，写入口还要求 CSRF。GET 详情同时返回 Outbound 状态和安全的 `observed/projected/omitted/submitted/not_submitted` 来源覆盖，不返回 Provider 原值。`AICRM_WECOM_CONTACT_DESCRIPTION_PROVIDER_ENABLED=false` 时，已认证管理员收到明确的 disabled 状态，不能据此执行任何 Provider 调用。
- 回调 HTTP 链路只持久 Inbox 并 ACK。已完成客户/关系生命周期的完整新增事件通过既有 River 队列登记单联系人读取任务；任务校验 Provider 返回的 `external_userid` 与事件目标一致后，才在同一个 PostgreSQL UoW 接受 Outbound intent 与效果收据。半联系人、未知、在途或结果未知的既有计划不触发盲目重写。
- `readback_failed` 只能创建 `readback` 操作：它再次读取并登记 confirmed/failed，不会写 description。`outcome_unknown` 保留原 effect/receipt key，只能对账或等待可信新观测；显式 replan 创建新的 immutable revision，不能改写旧快照。
- 0170 的完成行仅允许状态、结果、readback、安全的 Provider 数值拒绝码和更新时间变化；删除和 TRUNCATE 均被数据库触发器拒绝。安装包显式要求带有 0170 migration，运行期开关维持环境变量所有权且默认关闭。
- 本地 PostgreSQL 16.0.13 验证覆盖 0170、同 UoW 回滚、no-op 终态完成、run 真实统计、Provider 数值拒绝码及 TRUNCATE guard。实际命令为 `go test ./internal/outbound ./internal/wecom ./internal/externaleffects ./cmd/aicrm -count=1`、在独立临时本地库上运行的 `AICRM_DATABASE_URL=... go test ./internal/outbound -run '^TestPostgreSQLContactDescriptionIntentMigrationUOWNoopAndRunStats$' -count=1` 和 `DATABASE_URL=... go test ./internal/externaleffects -run '^TestPostgreSQLContactDescriptionNoopCompletionIsTerminal$' -count=1`，以及 `python3 scripts/dev_preflight.py compile` 与 `node scripts/validate-openapi.mjs`。临时库已删除。
- 此证据不代表生产部署、企微实写、目标应用权限验证或真实全量补打已经执行。发布 Actions 的 deploy 变量尚不可用，生产 SSH host key 已变更且未绕过；因此本期不执行生产发布或补打。

## 10. 2026-09-16 目标 description 读取缺陷修正

单联系人 detail 同时包含全部跟进关系及其 tags。全量补打和回调的 description
效果只需要一个已经确定的 `(external_userid, employee_userid)`；若复用全量严格
投影，任一无关关系的 tag 格式异常会使目标读取失败，即使 Provider 成功且目标关系
与 description 均有效。新增 WeCom 专用目标读取 Port：沿用同一 contact token、HTTP
超时、响应大小上限、HTTP/errcode 分类和一次 token 刷新，只把成功响应投影为精确的
外部联系人 ID、唯一且有效的目标员工关系和 description。无关关系及其 tags 不参与该
投影；通用 `ExternalContactReader` 继续保持完整 tags 校验，供客户标签和切换流程使用。

`description` 只有显式 JSON string（包括 `""`）才是已投影值。字段缺失或 JSON
`null` 都是未投影，不能当作空字符串生成快照、来源台账或 Outbound intent；非 string、
目标 ID 不一致、目标关系不存在或重复均保守拒绝。回调和效果执行均传入明确的目标员工，
由该 Port 在写前和读回时执行相同验证。
