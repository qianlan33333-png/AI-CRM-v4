# PRD：企微外部联系人回调与 OneID 闭环

## 1. 文档信息

- 状态：已确认，开发中；真实企微回调切换与渠道码实测延期
- 日期：2026-09-03
- 目标仓库：`AI-CRM-v3`
- v3 基线：`6ac3aba3bd3cfee598b7d3a44d9840a348466f7c`
- 行为供体：`AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
- 部署目标：`https://id-dev.youcangogogo.com`
- 架构：Go 模块化单体、PostgreSQL 16、单企业、API/oneshot worker 两种运行角色

本文中的“企微回调”仅指企业微信外部联系人事件回调：

- `GET /wecom/external-contact/callback`
- `POST /wecom/external-contact/callback`

企微扫码/OAuth callback、侧边栏 OAuth/JSSDK 是已经存在的基础能力，本期只做回归，不在本 PRD 重复开发。

## 2. 本期结论

本期只完成一条闭环：

```text
企微外部联系人事件
  → 验签解密
  → 持久 Inbox
  → 异步处理
  → 创建或解析 OneID
  → 建立/终止员工跟进关系
  → 按渠道 State 归因
  → 写回调结果与审计
```

本期不主动从任何环境拉取数据，不做企微全量同步，不读取联系人详情，不读取旧生产手机号，不导入历史客户数据。

## 3. 明确范围

### 3.1 本期开发

1. 回调 URL 验证。
2. 回调签名、时间窗、AES 解密和 CorpID 校验。
3. POST 加密 success ACK。
4. 回调 XML 类型化、字段校验和敏感字段摘要化。
5. 持久 Inbox、幂等去重、租约、重试、失败终态。
6. 外部联系人新增、半联系人新增、资料变更、删除和跟进终止事件处理。
7. 可信回调触发的 OneID 创建、解析、冲突和审计。
8. 企微员工 UserID 与客户 OneID 的跟进关系维护。
9. 渠道码 State 的本地精确匹配和新增客户归因收据。
10. 未匹配、歧义、冲突事件的隔离与人工校正接口。
11. 回调状态、积压、错误和处理结果的安全运维接口。
12. 生产环境变量接入、部署、健康检查和 fixture 验证。

### 3.2 本期明确不做

- 企微员工目录拉取。
- 外部联系人列表、详情、标签、备注或跟进人主动拉取。
- 首次全量、定时全量、增量补拉、对账拉取或 Provider readback。
- 从 `150.158.82.186` 拉取手机号或其他客户数据。
- 手机号导入、历史数据迁移或跨环境数据绑定。
- 客户激活/客户列表前端、客户列表 API、客户档案。
- 主动创建、更新、停用或删除企微渠道码。
- 自动发送欢迎语、改备注、打标签或发送消息。
- 支付、支付宝、AI audience、群运营等其他 webhook。
- 自动合并两个 Customer 根。

渠道码的创建和 Provider 管理由另一专项能力负责。本期回调只消费渠道码已经登记的 `State`，完成本地归因。

## 4. 企微回调模块的明确能力

### 4.1 Provider 边界

- 验证 `msg_signature`。
- 校验 timestamp：允许过去 5 分钟、未来 60 秒。
- AES-256-CBC 解密和企微 32-byte PKCS#7 padding。
- 同时校验外层 CorpID、解密帧 receiveid、内层 CorpID。
- GET 校验成功返回解密后的 `echostr`。
- POST 持久化成功后返回可验签、可解密为 `success` 的加密 ACK。
- 请求体最大 1 MiB；拒绝畸形 XML、尾随 XML、重复 query 参数和非法字段。
- 数据库写入失败时返回 503，不提前 ACK，让企微后续重投。

### 4.2 类型化事件

允许从已验签明文提取：

- CorpID
- Event
- ChangeType
- ExternalUserID
- UserID
- State
- CreateTime
- MsgID（存在时）
- WelcomeCode 是否存在及摘要
- Source 摘要
- FailReason 摘要

WelcomeCode、Source、FailReason、原始 XML、external_userid 和 State 原值不得进入普通日志。业务处理只使用必要字段；查询 API 返回安全摘要。

### 4.3 可靠处理

- 已认证完整明文的 SHA-256 加 CorpID 和协议版本形成幂等键。
- 同一回调重复投递只生成一个 Inbox 主记录和一个业务结果。
- API 只负责验证、落库和 ACK，不在请求内执行 OneID 或其他业务。
- oneshot worker 用 lease + CAS 领取任务。
- worker 崩溃后任务可接管；旧 worker 不能覆盖新结果。
- 临时错误进入 retryable，稳定错误进入 failed_terminal。
- 每次处理产生不可变回调收据和审计。

### 4.4 业务结果

回调处理结果固定为：

- `customer_created`
- `customer_resolved`
- `relationship_activated`
- `relationship_deactivated`
- `channel_attributed`
- `channel_unmatched`
- `channel_ambiguous`
- `identity_conflict`
- `ignored`
- `failed_terminal`

一个回调可以同时具有多个结果维度，例如：已有 OneID、关系激活、渠道归因成功。

## 5. 回调与 OneID 的关系

### 5.1 身份键

外部联系人的 OneID 身份键固定为：

```text
kind             = wecom_external_userid
scope            = wecom-corp:<corp_id>
normalized_value = <企微回调中的 ExternalUserID 原值>
assurance        = verified
source           = wecom.callback
```

`customers.id` 才是 CRM 内部唯一 OneID。ExternalUserID 只是这个 OneID 的一个有企业 scope 的外部身份。

### 5.2 为什么回调可以产生 verified identity

只有完成以下全部校验的内部 Adapter 才能构造 verified fact：

1. 企微签名正确。
2. timestamp 在允许时间窗内。
3. AES 解密成功。
4. 外层、内层和解密帧 CorpID 全部一致。
5. XML 和业务字段合法。

浏览器、管理员 API、普通 HTTP JSON 和导入文件都不能提交 `assurance=verified`。

### 5.3 创建和解析规则

- 新增类回调携带的身份不存在：显式调用 `ProvisionCustomerFromVerifiedIdentity`，原子创建一个 Customer 和一个 active identity。
- 身份已经存在：返回它当前的 canonical customer_id，不创建新 Customer。
- 并发收到多个相同新增回调：数据库唯一约束保证只产生一个 identity 和一个 Customer。
- 身份当前挂在已归并 Customer：返回最终 canonical root。
- 强身份出现不一致归属：记录 conflict/merge candidate，不自动合并。
- 删除类回调找不到身份：结束为 ignored，绝不因为删除事件创建 Customer。

### 5.4 事件类型与 OneID 行为

| ChangeType | OneID | 跟进关系 | 渠道 |
|---|---|---|---|
| `add_external_contact` | 未命中则建 Customer；命中则复用 | `UserID + customer_id` active | 有 State 时尝试归因 |
| `add_half_external_contact` | 同上 | active | 有 State 时尝试归因 |
| `edit_external_contact` | 命中则记录生命周期；未命中可用已验证事实自修复建客 | 事件包含有效 UserID 时保持 active | 不创建新的渠道 entrant |
| `del_follow_user` | 只解析，不建客 | 对应关系 inactive | 不改变历史渠道归因 |
| `del_external_contact` | 只解析，不建客 | 对应关系 inactive | 不改变历史渠道归因 |
| 其他合法 ChangeType | 不建客 | 不改变 | ignored，仅留审计 |

`edit_external_contact` 的自修复建客是 v3 对 v2 的明确增强：它解决新增回调曾丢失、但后来收到可信编辑事件的情况。它仍然只依赖经过验证的企微回调，不是猜测式建客。

## 6. 新客户通过渠道码加企微的完整流程

### 6.1 渠道码准备阶段

渠道码专项能力事先完成：

1. 创建渠道及渠道码素材。
2. 生成随机、不可猜、带版本的 State。
3. 本地登记唯一映射：

```text
(corp_id, state_digest)
  → channel_id
  → asset_kind
  → asset_version
```

本回调模块不创建渠道码，只通过只读 Channel port 查询这个映射。

### 6.2 用户扫码添加员工

用户扫描渠道码并添加企微员工后，企微发送：

```text
Event          = change_external_contact
ChangeType     = add_external_contact
ExternalUserID = 该外部联系人的企微身份
UserID         = 被添加的企业员工
State          = 渠道码携带的关联值
WelcomeCode    = 可选的一次性欢迎语凭据
```

本期只记录 WelcomeCode 是否存在及摘要，不发送欢迎语。

### 6.3 OneID 生效顺序

worker 在一个业务事务内执行：

1. 用 verified `wecom_external_userid` 查 OneID。
2. 不存在则创建 `customers.id` 和 `customer_identities`。
3. 已存在则取 canonical customer_id。
4. 建立 `(corp_id, UserID, customer_id)` active 跟进关系。
5. 使用 CorpID + State 查询唯一渠道素材。
6. 写 channel entrant receipt 和客户事件。
7. 写审计并完成 Inbox。

关键设计：OneID 创建不依赖渠道 State 是否匹配。

- State 匹配成功：客户已创建/解析，同时标记来源渠道。
- State 缺失或零匹配：客户仍是可信企微客户，但渠道结果为 `channel_unmatched`。
- State 多匹配：客户仍正常存在，但渠道结果为 `channel_ambiguous`，不猜来源。
- OneID 冲突：不写渠道客户归属，结果为 `identity_conflict`，等待人工处理。

这能避免渠道配置错误导致真实新增客户完全丢失，同时保持渠道归因严格、不可猜测。

### 6.4 “创建用户”的准确含义

回调创建的是 CRM 客户 OneID，不是员工登录账号：

- 创建：`customers` + `customer_identities`
- 不创建：`admin_users`

回调中的 `UserID` 是负责跟进该客户的企微员工标识。它只用于跟进关系：

- 如果已有 CRM 员工账号绑定该企微 UserID，后台可以把关系展示给该员工。
- 如果暂时没有绑定账号，关系仍以企微 UserID 保存，状态标记为待员工绑定。
- 回调绝不自动创建员工登录账号、授予角色或开放后台权限。

### 6.5 同一客户的后续行为

- 同一人重复扫码：复用原 OneID，回调和渠道收据幂等。
- 同一人添加第二名员工：仍是同一个 OneID，新增第二条 active 跟进关系。
- 用户删除其中一名员工：只将对应关系 inactive；其他关系和 OneID 保留。
- 用户删除全部员工：所有关系 inactive，但 Customer 和身份历史不删除。
- 后续再次添加：恢复或新增 active 关系，继续复用原 OneID。

## 7. 无主动拉取后的数据边界

外部联系人回调本身不包含完整客户资料，因此本期只能可靠得到：

- external_userid
- 关联员工 UserID
- 新增/编辑/删除事件类型和时间
- 可选渠道 State
- 可选 WelcomeCode presence/digest

本期不能从回调得到并承诺：

- 手机号
- 完整姓名
- 头像
- UnionID/OpenID
- 完整标签
- 完整备注/描述
- 其他员工的全部跟进关系

因此新 Customer 可以立即拥有可用 OneID，但个人资料保持最小状态。后续由独立“企微数据同步/资料补齐”专项能力丰富，不属于本 PRD。

## 8. 模块与数据归属

| 模块 | 职责 |
|---|---|
| `internal/platform/webhook` | 通用 Inbox、租约、重试状态 |
| `internal/wecom` | 回调密码学、typed fact、回调收据、跟进关系 |
| `internal/identity` | Customer、identity、conflict、canonical root |
| `internal/channel` | State correlation 和 entrant receipt |
| `cmd/aicrm` | 依赖装配、HTTP 路由、API/worker 角色 |

计划新增或演进：

- `wecom_callback_receipts`
- `wecom_follow_relationships`
- `channel_acquisition_state_bindings`
- `channel_acquisition_entrant_receipts`

不新增全量同步 run/cursor/profile 表，不新增手机号导入表，不新增 Provider reader。

## 9. HTTP 与 worker 契约

### 9.1 公共回调

- `GET /wecom/external-contact/callback`
- `POST /wecom/external-contact/callback`
- `GET /api/wecom/events`（v2 兼容别名，共用同一 Handler）
- `POST /api/wecom/events`（v2 兼容别名，共用同一 Handler）

两组路由共享同一幂等命名空间。

### 9.2 安全运维 API

- `GET /api/admin/wecom/callback-receipts`
- `GET /api/admin/wecom/callback-receipts/{receipt_id}`
- `POST /api/admin/wecom/callback-receipts/{receipt_id}/retry`
- `GET /api/admin/channel-acquisition-entrant-receipts/unassigned`
- `POST /api/admin/channel-acquisition-entrant-receipts/{receipt_id}/reconcile`

运维写操作仅 SuperAdmin，要求数据库 Session、同源校验、CSRF、理由和 `Idempotency-Key`。API 不返回 external_userid、State 原值或原始 XML。
`identity_conflict` 仅在未归属列表展示，不能通过渠道 reconcile 指定任意 Customer；必须先走 OneID 冲突处置/归并审核，再由后续专项流程重新处理渠道归属。

### 9.3 运行角色

- `AICRM_ROLE=api`：接收回调和提供管理 API。
- `AICRM_ROLE=worker`：一次有界处理 Inbox 后退出。
- systemd timer 周期唤起 worker；不引入进程内 ticker。

## 10. 配置与部署

- `AICRM_WECOM_CALLBACK_ENABLED`
- `AICRM_WECOM_CORP_ID`
- `AICRM_WECOM_CALLBACK_TOKEN`
- `AICRM_WECOM_CALLBACK_AES_KEY`

用户提供的 Token 与 EncodingAESKey 只注入服务器受限环境文件和 GitHub Secret，不写入本文、Git、日志、数据库或发布包。

部署时完成：

1. migration 和新二进制发布。
2. callback 环境变量注入。
3. readiness 显示 callback_config ready，但不显示值。
4. 使用本地/fixture 验证回调密码学、Inbox 和 OneID 流程。
5. 正式企微回调 URL 仍不切换；真实新增和渠道码验收延期。

## 11. 安全与隐私

- 不记录 Token、AESKey、原始 XML、密文、external_userid、State、WelcomeCode、手机号、UnionID 或 OpenID。
- 回调查询只暴露 receipt ID、事件类型、结果、时间、重试次数和错误码。
- State 只保存 HMAC/digest，按 CorpID scope 查询。
- 回调 receipt 和审计 append-only。
- retry/reconcile 必须记录 actor、理由、幂等键、前后状态。
- HTTP 请求体无法伪造 verified identity。

## 12. 验收标准

### 12.1 合并前自动验收

- 官方格式 fixture 完成 GET 验证和 POST 加密 ACK 再次验签/解密。
- 错签名、时间窗、CorpID、畸形/尾随/超大 XML、重复参数全部拒绝且不落 Inbox。
- 同一回调并发重放只产生一个 Inbox、一个 Customer/identity 和一个结果收据。
- 新增回调创建 OneID；已有身份复用 OneID；删除事件不建客。
- 无 State/错 State/重复 State 不阻止可信 OneID 建立，但不会错误归因。
- 同一个 external_userid 添加多名员工仍只有一个 OneID，并有多条跟进关系。
- 删除单个跟进关系不删除 OneID。
- 浏览器 JSON 无法提交 verified fact。
- Provider disabled 时无可用回调处理路径。
- `make check`、相关包 `go test -race`、`govulncheck ./...` 和 secret scan 通过。

### 12.2 本期生产验收

- 新 release、migration、health/readiness 和现有 OAuth/JSSDK 回归正常。
- 回调环境变量已配置且无泄漏。
- HTTPS 回调路由可达。
- fixture 在生产应用边界完成验签、入库、worker、OneID 和 receipt 测试；fixture 结果明确标记为 synthetic。

### 12.3 延期真实验收

以下保持 `NOT_EXECUTED`，不阻塞代码部署：

- 把企微正式回调 URL 从旧生产切换到 v3。
- 企微真实产生新增、半联系人、编辑、删除和跟进终止事件。
- 用真实渠道码验证 State、WelcomeCode 和渠道归因。
- 回调切换后的双环境观察和旧入口退役。

## 13. 失败模式

| 失败 | 行为 |
|---|---|
| ACK 前数据库失败 | 返回 503，让企微重投 |
| ACK 后 worker 崩溃 | lease 过期后接管，CAS 防止旧 worker 覆盖 |
| 相同回调重复投递 | 返回相同收据，不重复执行业务 |
| OneID 并发首次创建 | 唯一索引 + 事务确保一个 Customer |
| State 缺失或零匹配 | OneID 正常，渠道 unmatched |
| State 多匹配 | OneID 正常，渠道 ambiguous |
| 删除事件找不到身份 | ignored，不创建 Customer |
| 员工 UserID 未绑定登录账号 | 保存待绑定关系，不创建账号或授予权限 |
| Secret 出现在 diff/log | 阻断发布并轮换 Secret |

## 14. 实施 PR

确认后拆成四个集中 PR：

1. 回调协议、类型化字段、加密 ACK 和 v2 fixture。
2. Inbox worker、回调收据、生命周期与 OneID。
3. 跟进关系、State correlation 和 entrant receipt。
4. 运维 API、配置拆分、CI、部署和 synthetic 生产验收。

不创建全量同步、数据抓取、历史迁移或客户列表 PR。

## 15. 开发启动门

用户确认本 PRD 后才开始开发。固定口径：

- 只开发企微外部联系人回调及其本地 OneID/渠道归因闭环。
- 不从企微、旧生产或其他数据源主动拉取任何数据。
- 新增可信客户时创建 CRM Customer OneID，不自动创建员工账号。
- 完整代码和环境配置先上线，真实企微回调与渠道码测试另约窗口。
