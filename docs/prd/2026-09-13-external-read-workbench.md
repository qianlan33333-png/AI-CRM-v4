# v1.0 外部只读 API 与工作台接入实施 PRD

状态：本轮批准实现基线；代码、合并、部署和工作台验收仍未完成。
日期：2026-09-13
CRM 基线：`db884d26`，分支 `codex/external-read`
交付范围：v3 CRM 侧只读合同、Catalog、授权、Owner Port 组合和验收用例。旧仓及旧服务器只作为行为和字段参考；工作台仓库、服务器和真实同步验收不属于当前已确认环境。

## 1. 目标与边界

为外部工作台提供一个短期令牌、细粒度授权、只读的 v3 Open Platform Client。v1.0 覆盖七项业务能力：

1. 客户身份与基础信息；
2. 订单列表；
3. 订单详情；
4. 问卷提交记录；
5. Chat 记录；
6. Radar 点击；
7. Radar 链接映射。

接口完成的定义是：真实 v3 数据可经 OneID 正确归属、所有目标能力可查询、Client 无写入权限、分页和撤销可验证、外部身份可追溯到手机号或带 scope 的 UnionID。HTTP 200、空列表、保存配置、Mock、单条测试或 CI 通过都不构成完成。

不在本期范围内：下单、支付、退款、发消息、建群、自动化、AI 写入、查询时调用 Provider、隐式建客、隐式绑定或合并、恢复旧 `/api/external/*` 路由、固定永久 API key、旧仓运行时依赖、通用同步/导出平台。

本功能涉及 OneID 和持久化读取，但不引入 Provider 读取、内部持久任务或外部效果。跨领域只使用稳定 Port；不得从 Open Platform 或新适配器直接访问其他 Owner 的表。

## 2. 当前审计结论

当前 v3 的原生 Catalog 在 `internal/openplatform/port/operations.go`，已接入本轮的
`order.list`、`order.get`、`identity.get`、`questionnaire.submissions.list`、
`customer.detail.get`、`radar.clicks.list`、`radar.links.list` 和
`chat.records.list`。`internal/openplatform/http/handler.go` 已挂载这些明确的
`/open/v1` 路径；旧 `/api/external/*` 仍是迁移审计资料，不能作为新入口。
`api/openapi.yaml` 已补齐七项能力的 path/response schema，MCP 复用同一 Catalog。

可以复用的稳定边界：

- `internal/identity/port` 的 Resolver、canonical lineage 和 verified identity 约束：负责解析，不隐式建客或合并。
- `internal/customer/port` 的 Sidebar、Owner、Tag、Survey、Timeline、Chat activity 投影：适合内部摘要和受控组合，但 Sidebar 目前只有掩码手机号等安全字段。
- `internal/order/port.Query` 的订单列表、详情、引用查询；`internal/payment/port.AdminQuery` 的支付、退款和订单 effect 只读查询；需增加外部安全组合投影。
- `internal/survey/port.ExternalSubmissionReader`：已有 native 与历史问卷读取和历史 UnionID 映射；v1 route 已在 Open Platform 层使用签名 keyset，Owner 内部位置不得泄露。
- `internal/messagearchive/port` 的 CustomerMessageReader 和 ExternalChatRecordReader：归档安全字段、员工筛选和游标已组合。
- `internal/radar/port.ExternalLinkMappingReader` 与 `ExternalClickReader`：链接和逻辑点击的 Owner Port/应用读取已组合，并已补 OpenAPI schema。

Terra 与 Root 的本地真实 PostgreSQL + HTTP journey 已覆盖全部七项能力、source
pair、订单 payer/beneficiary 分离、订单嵌套 DTO、身份 declared/verified/多值与
wrong scope、非 CN phone 空回退保护、客户详情真实 Owner/remark/follow、Chat
staff selector、Radar 和专用 10-cap read-only Client。OpenAPI 嵌入副本、Catalog
14 项、生成客户端和 composition 也已通过本地验证。GitHub 认证、合并、部署和
工作台回读仍未完成；生产仍为旧版本。旧仓行为可以帮助冻结筛选和状态含义，不能
复制旧 SQL、旧目录、旧身份主键或旧路由。

## 3. 七项能力矩阵

| 能力 | v1 目标 | v3 当前状态 | 可复用 Owner Port/行为 | 合同与数据边界 |
|---|---|---|---|---|
| 客户身份/基础信息 | `POST /open/v1/customers:resolve`；`GET /open/v1/customers/{id}/identities`；详情读取 | resolve/context/identity/detail 运行时已接线；本地真实 PG/HTTP 已验证 | OneID Resolver、MachineIdentityFactReader、Customer/WeCom detail | 保留 declared `phone:cn11`、带 scope UnionID、canonical lineage、多值/missing/conflict；非 CN phone 空回退保护已由真实 PG 回归覆盖；生产覆盖独立统计；保持 `customer.context` 兼容 |
| 订单列表 | `GET /open/v1/orders` | Order `List`、Open operation 和 source selectors 已接线；本地真实 PG/HTTP 已验证 | Order Query、Payment 查询、Customer identity | payer/beneficiary 选择规则、paid/refund 过滤、整数金额、无客户订单和 101 lookahead 已有证据；签名游标绑定筛选和授权 |
| 订单详情 | `GET /open/v1/orders/{order_id}` | Order `Get`、商品/退款/timeline 运行时已接线；本地真实 HTTP 已验证嵌套 DTO | Order Query、Payment `AdminQuery`、退款读取 | 当前支付摘要/回调安全投影仍不提供；不得暴露签名、密钥、原始 effect 或 Provider 私密字段 |
| 问卷提交 | `GET /open/v1/questionnaire-submissions` | ExternalSubmissionReader、Catalog/HTTP/MCP 和 signed keyset 已接线；本地真实 PG/HTTP 已验证 | Survey submission/history、历史 UnionID map | `customer_id` 必填；source selectors、native/历史来源、答案快照已验证；pending/conflict/unresolved 历史行保留在覆盖率清单；unavailable 不转空列表 |
| Chat 记录 | `GET /open/v1/chat-records` | Owner Port、Catalog/HTTP/MCP/Access/OpenAPI 已接线；本地真实 PG/HTTP 已验证 | CustomerMessageReader、ExternalChatRecordReader、V1 Archive reader | canonical lineage CustomerIDs、私聊员工/群参与者边界、固定 20 条、签名游标、媒体 unavailable 已有证据 |
| Radar 点击 | `GET /open/v1/radar/clicks` | Catalog/HTTP/MCP/Access/OpenAPI 已接线；本地真实 PG/HTTP 已验证 | OneID、Radar ExternalClickReader、可信事件判定 | logical session 去重、scope 前过滤、pending/conflict 保留、排除匿名/IP/UA/OAuth 中间事件已纳入合同 |
| Radar 链接 | `GET /open/v1/radar/links` | Catalog/HTTP/MCP/Access/OpenAPI 已接线；本地真实 PG/HTTP 已验证 | Radar link Owner Port、keyset cursor | 返回稳定 id/code/title；禁用/草稿保留；不带 customer，不返回配置和凭据 |

## 4. 硬性身份可追溯合同

### 4.1 记录、身份和统一客户的三层关系

每个跨系统业务记录必须保留以下来源链路，不能只返回一个可能变化的展示字段：

| 层 | 必填/条件字段 | 规则 |
|---|---|---|
| 来源记录 | `source_system`、`source_record_id` | 保留真实来源系统和原始记录 ID；不同来源的 ID 不得混用；即使未匹配客户也必须保留 |
| 统一客户 | `customer_id`、`identity_status` | `customer_id` 只能来自统一 Customer/Identity Port；`unresolved`、`pending`、`conflict` 时为 null，不能猜测归属 |
| 外部身份 | `kind`、`scope`、`value`、`assurance`、`source` | value 只在专用 `identity.read` capability 下返回；手机号和 UnionID 必须带身份 kind/scope，不能把 UnionID 当全局无 scope 主键 |

订单、问卷、Chat 和 Radar 点击的 DTO 必须有可审计的 `lineage` 或等价字段。对外输出可以只给授权后的身份投影，但 CRM 内部必须能从 `source_system + source_record_id` 回到原始记录；存在可信身份时再经 OneID 回到 canonical `customer_id`。Radar 链接映射是独立内容实体，只保留稳定 link ID 与 `source_system + source_record_id` 的 Owner 可追溯性，不附加 `customer_id` 或 OneID 链路。

### 4.2 客户关联记录的查询路径

以下统一路径仅适用于订单、问卷、Chat 和 Radar 点击；不允许各 Owner 自建匹配逻辑。Radar 链接是无客户归属的独立内容读取，只接受其稳定业务 ID/code 和授权范围，不调用 OneID：

1. Client 以 `customer_id` 或支持的身份引用查询。支持手机号、UnionID、OpenID、WeCom external_userid 等已登记 kind；UnionID、OpenID 必须带正确 scope。
2. Open Platform 先检查 token scope、operation capability 和数据范围；身份/客户详情分别由现有 `identity.read`/`customer.detail.read` capability 保护，本轮不新增字段级权限引擎。
3. `internal/identity/port.Resolver.Resolve` 返回 `Found`、`NotFound` 或 `Conflict`；只有 `Found` 才可使用 canonical `customer_id` 查业务 Port。
4. 业务 Owner Port 以 canonical `customer_id` 查询本域记录，同时返回/组装 `source_system` 和 `source_record_id`。不得通过跨域 SQL 或旧仓 `person_id` 关联。
5. 如需向工作台回显手机号或 UnionID，Open Platform 再通过统一 Identity/Customer Port 读取授权字段，并在响应中标记 kind、scope、assurance；日志只保留脱敏值和审计引用。
6. 返回结果后，调用方可用响应中的 canonical `customer_id` 再查客户详情或其他业务列表，往返结果必须指向同一客户。

未匹配的历史记录仍可在允许的来源筛选下返回，但必须带 `identity_status=unresolved` 或 `pending`；冲突记录带 `identity_status=conflict`，不得自动选择一个客户。任何请求都不得隐式创建客户、绑定身份、自动合并或把 HTTP 空结果解释为没有历史数据。

### 4.3 手机号和 UnionID 的授权与精确查询

本轮专用 Client 通过 `identity.read` 读取原始手机号和带 scope 的 UnionID，通过
`customer.detail.read` 读取客户详情；这两个现有 capability 必须在创建时明确授予，
不是拥有 `customer.read` 就自动获得。本轮不新增 `scope_mode` 或字段级权限引擎。
身份输出仍须保留事实组：

- `identity.basic`：canonical `customer_id`、状态、匹配方式；
- `contact.phone.raw`：实际 phone fact 的值、`phone:cn11` scope、assurance、source、status；
- `identity.unionid.raw`：UnionID 及其开放平台 scope；必须显式给出 `unionid_scopes`；
- `identity.channel.raw`：OpenID/WeCom external_userid 等只有在相应 Owner 和 scope 实际登记后才能提供；
- `profile.remark`、`ownership`、`follow_employees`：属于 `customer.detail.get` 的投影，缺失时返回明确 availability/null，不伪造空字段。

既有 Client 保持原 `customer.context.get` 摘要合同，不能因共享 `customer.read`
capability 自动得到新敏感字段。未登记的身份 kind/scope 或详情 Owner 字段应返回
明确的 missing/unavailable/permission 语义，不能用空字段或空 scope 默默放宽。

精确查询规则：

- `customer_id` 是唯一跨接口主键；手机号查询必须先按统一规范化规则经 Identity Port 查询，不能直接在业务表中按字符串猜测。
- UnionID 查询必须提供且校验 `scope`；本企业当前历史 scope 为
  `wechat-open-platform:wx0ca836834b18e989`。没有正确 scope 时返回 `403` 或明确
  `scope_required`，不能跨开放平台范围匹配。
- 唯一匹配返回 `Found`；无匹配返回 `NotFound`/`unresolved`；多个可信候选返回 `409 conflict`，不返回未经授权的候选明细。
- 请求体不能自报 `verified`；verified 证据只能由内部 Provider Adapter 构造。

### 4.4 覆盖率和缺失清单

上线前必须对每种有客户归属的业务记录统计真实覆盖率：总记录数、可回到 `customer_id` 的数量、仅有来源 ID 的数量、`pending`、`unresolved`、`conflict` 数量，以及按 `source_system` 和时间范围划分的缺失原因。响应或验收报告应能列出缺失记录的 `source_system + source_record_id`（原始身份值按权限脱敏）。

不能因为当前样本都能查到手机号/UnionID 就宣称全部历史记录 100% 完整；缺失数据必须作为可见状态和清单，等待明确补数或人工处理。数据修复仍需走 OneID 的显式流程，不能在本只读 API 查询时补绑定。

### 4.5 原有 ID、手机号和 UnionID 的可查询性

“保留身份链路”必须落成可执行的查询合同：订单、问卷、Chat 和 Radar 点击应支持 canonical `customer_id`、授权手机号、带 scope 的 UnionID 三种客户选择器；每个来源记录还应支持按 `source_system + source_record_id` 的精确回查。来源 ID 不允许只出现在响应里而不能作为查询条件。若一个 Owner 还有稳定的业务 ID（例如订单 merchant/provider reference、问卷 submission ID、Chat message ID、Radar event/link ID），应在对应列表/详情接口提供精确过滤，并以 `source_system` 防止跨系统碰撞。Radar 链接不接收任何客户选择器：它按 link ID/code 在完整已授权链接范围内读取。

这三种客户选择器必须走同一往返路径：

1. 用手机号或 `UnionID + scope` 解析到 `customer_id`；
2. 用该 `customer_id` 查询订单、问卷、Chat、Radar 等记录；
3. 从记录的 `source_system + source_record_id` 反查原始业务记录；
4. 再从记录返回的身份投影回查同一个 `customer_id`，并验证手机号/UnionID scope 与首次解析一致。

因此，“一定能查询”表示接口和授权合同必须覆盖这三类选择器，且对实际存在并有授权的身份给出稳定结果；不表示历史源数据凭空拥有不存在的手机号或 UnionID。源记录没有该身份、身份未同步或多身份冲突时，必须返回对应缺失/`unresolved`/`pending`/`conflict` 状态并进入覆盖率清单，不能伪造字段或宣称 100% 关联。

## 5. 授权、数据范围与游标

认证使用 `client_credentials`、短期 Bearer token、audience `external_integration` 和
read scope；Secret 只进 Secret Store，不进入仓库、日志、响应或工作台页面。专用
只读 Client 必须使用 `scopes:["read"]`；本轮不修改已有 AI 工作流 Client 的
`write` grant，也不把全局 `write` 能力误判成新 Client 可用。

本轮批准的专用 Client capability 集合为已登记且实际可读的十项：
`platform.capabilities.read`、`customer.resolve`、`customer.read`、`order.read`、
`identity.read`、`questionnaire.read`、`customer.detail.read`、`radar.click.read`、
`radar.link.read`、`chat.read`。`customer.activity.read` 属于既有客户活动摘要能力，
不纳入本轮专用 profile；`ai.review_plan.create`、`operation.read` 及 legacy
`external_read/external_write` 也不属于本专用只读 Client。专用 Client 的 token
scope 固定为 `read`，既有 AI 工作流 Client 保持兼容。

当前 `OwnerScope{}` 空 map 语义是 unrestricted。为避免专用 Client 意外全量读取，
创建和激活流程必须明确提交非空 `owner_scope`：本企业全量使用
`{"corp_id":["<actual-corp-id>"]}`，客户选择使用非空 `customer_id` 列表；本轮
不新增 `scope_mode` 或另一套权限引擎。`CreateV1` 当前不会自动强制只读或非空范围，
所以管理员必须在 Client 摘要/审计中复核实际 capabilities、scope、CIDR 和
owner_scope，再激活；历史 Client 的空 map 兼容语义保持不变且不自动升级。

所有外部列表默认 opaque cursor；游标签名绑定筛选条件、effective grant、auth
version 和必要水位。失败不能推进游标，过滤条件、授权或数据水位变化不能复用旧
游标。本轮订单、问卷和 Radar 列表默认/最大 100，Chat 固定 20；既有
`customer.activities.list` 保持兼容默认 50。时间输入使用秒级 Unix 时间戳，输出
使用 RFC3339/ISO8601 UTC（通常带 `Z`）；工作台显示 Asia/Shanghai 的
`YYYY-MM-DD HH:mm:ss` 是展示层转换。金额用整数分并附格式化元。

## 6. API 和字段附表骨架

以下是当前工作树与批准合同的最小字段骨架。`api/openapi.yaml` 以及运行时嵌入副本
已收口订单、身份、问卷、Chat 和 Radar 的路径与响应 schema；缺失值必须区分 null、
空字符串、未授权和 unavailable。

### 6.1 客户

请求：`customer.resolve` 接受 `{references:[{kind,scope,value}]}`；身份/详情读取用
canonical `customer_id`。响应保留 `kind/scope/value/assurance/source/status` 和
canonical 状态；`identity.get` 默认 phone，UnionID 需显式 `unionid_scopes`。不返回
旧 `person_id` 或 map row。

### 6.2 订单列表/详情

请求：provider、created/paid 时间、product、`is_paid`、`is_refunded`、merchant/payment
reference、`source_system/source_record_id`、customer_id、cursor、limit。列表响应：
`order_id`、payer/beneficiary 两种关系、按最终规则计算的 `customer_id`、
`identity_status`、source provenance、provider/reference、产品、created/paid 时间、
状态、整数 `amount_minor`/格式化元、退款状态。详情追加 `items`、`refund_records`、
`timeline`；当前支付摘要/回调摘要尚未提供；禁止 callback body、签名、密钥和内部
effect 细节。

### 6.3 问卷

请求：`customer_id`、问卷标识、`source_system/source_record_id`、提交时间、cursor、
limit。响应：`submission_id`、`questionnaire_id/title`、definition version、
submitted_at、answers snapshot（含 `score_contribution`）、labels、assessment
snapshot、source provenance、`customer_id/identity_status`。不在查询时重新评分；
当前已解析 item 的 `identity_status` 固定为 `resolved`；pending/conflict/unresolved
历史行保留在覆盖率清单，不得转成 resolved 或用空列表掩盖。

### 6.4 Chat

请求：`customer_id`、`chat_type`、时间范围、私聊的 `staff_user_id` 或
`staff_wecom_userid`、`source_system/source_record_id`、`message_id`、cursor。响应：
`message_id`、`customer_id`、`identity_status`、`chat_type`、`message_type`、
`content`、`render_type`、`direction`、`occurred_at`、`conversation_id`、
`group_name`、`media_archive_status`、`media_availability`、`staff[]` 以及来源字段。
固定 20 条；媒体未归档时明确 unavailable。Chat 已进入 Catalog/HTTP/MCP/Access，
并在 OpenAPI 中有响应 schema；`staff_wecom_userid` 使用本地员工投影。

### 6.5 Radar 点击/链接

点击请求的 `customer_id` 可选：受限 Client 指定客户时只读取该 canonical Customer 的
resolved 点击；具备 corp 范围的 Radar click Client 可跨客户读取 resolved、pending
和 conflict 点击。一个访问会话的多个技术阶段按第一次成功打开归并为一次 click；
pending/conflict 必须有可审计身份链路，匿名和 failed、IP、UA、OAuth 中间加载一律
排除。点击响应：`click_id/event_id/session_id`、radar id/code、click time、
`source_system/source_record_id`、`customer_id/identity_status/attribution_status`。
链接请求不带 `customer_id` 或身份选择器。链接响应：稳定 link id、code、title、
enabled/deleted 状态和 cursor；非删除禁用/草稿链接保留供历史查询，不返回凭据或配置。

### 6.6 共用错误

`400` 参数/游标错误，`401` token 无效，`403` scope/capability/field/scope 不足，`404` canonical 或记录不存在，`409` identity/order conflict，`429` 限流，`503` 依赖未就绪。`409` 不得被转为空列表。

## 7. Owner 边界和具体文件

| Owner | 现有位置 | 本期允许 | 禁止 |
|---|---|---|---|
| Open Platform | `internal/openplatform/{port,http}`、`cmd/aicrm/open_platform_v1.go`、`open_platform_adapters.go` | operation/catalog、授权、DTO、游标、统一错误、Port 组合 | 直接查其他领域表；恢复旧路由 |
| Identity/Customer | `internal/identity/port`、`internal/customer/port` | 稳定身份解析和 `identity.read`/`customer.detail.read` 授权投影；canonical lineage、实际 assurance/source | 各业务 Owner 自建匹配；隐式建客/合并；把 raw identity 写入日志 |
| Order | `internal/order/{port,app,store}` | 列表过滤、支付时间/退款安全投影、详情 Port | 直接查 payment/radar 表；订单写入 |
| Payment | `internal/payment/port` | 提供 machine-safe payment/refund projection | 暴露回调签名、密钥、原始 effect/provider 私密字段 |
| Survey | `internal/survey/port` | 外部提交投影、历史身份关联、游标适配 | 查询时重评分；直接读别域表 |
| Message archive | `internal/messagearchive/port` | 归档消息安全投影和去重 | Provider 实时读取、发送、旧硬编码员工 |
| Radar | `internal/radar/port` | 新建 click read Port；复用已有 link reader | 复制旧 SQL；暴露 tracking/IP/UA 或配置凭据 |
| Access | `internal/access/{domain,app,http}` | 复用 `external_agent`、精确 read-only capability、`scopes:["read"]` 和非空 `owner_scope`；管理员自检 CreateV1 宽松校验结果 | 本期订单 PR 修改员工登录、治理、readiness、0152 migration；新增 scope_mode/字段级权限引擎 |

## 8. 实施边界和当前责任

Terra `api_order_finish` 已把订单、身份和共享 API 收口；backend fixes 已完成
问卷、Chat、Radar click/link 的 Owner Port 与独立 executor。所有能力都经同一个
Operation Catalog、HTTP/MCP executor 和 Access profile 暴露，最终发布仍受合并、
部署和工作台回读门槛约束。

订单与已接线外部读取实现最小范围如下：

1. `order.list`、`order.get`、`identity.get`、`questionnaire.submissions.list`、`customer.detail.get`、`radar.clicks.list`、`radar.links.list`、`chat.records.list` 使用同一个 Catalog、REST/MCP executor 和 capability allowlist。
2. 在 Order Owner Port 之上组合 Payment 只读数据，补齐 paid/refund 筛选、可信 `paid_at`、部分/全额退款和安全详情投影。
3. 每条订单输出来源系统/来源记录 ID、canonical `customer_id`、`identity_status`；支持以 customer_id、已授权手机号和带 scope UnionID 做精确查询，所有解析经 Identity/Customer Port。
4. 保留未绑定订单并明确状态；unknown、pending、conflict 不猜测、不建客、不自动合并；身份歧义返回 409。
5. 签名 opaque cursor 绑定 filters、effective grant、auth version 和必要水位；当前订单、问卷、Radar、Chat 均有运行时 cursor，并覆盖篡改、撤销、筛选变化和失败重试。
6. 测试至少覆盖多 provider、未支付/已支付/部分退款/全额退款、无客户订单、手机号往返、UnionID+scope 往返、来源 ID 回溯、conflict 409 和敏感字段未授权。

订单实现不要修改 Access0152 分支中的员工权限、migration、readiness、安装脚本；若 `internal/access/app/machine.go` 或 `domain/machine.go` 发生冲突，保留本项目最小 capability 增量，并通过现有 `external_agent` CreateV1 创建精确 read-only Client。`cmd/aicrm/composition.go` 只做必要 wiring；不要把 CreateV1 当前不强制的 read-only/非空 owner scope 写成服务端自动保证。

所有 Owner 批次必须复用上述身份链路和覆盖率报告，不接受“各接口自行按手机号/UnionID 查表”。

## 9. 本轮已冻结的实现决策

以下决策已经批准，开发和文档不再反复要求 Root 选择：

1. 专用 Client 复用现有 `external_agent` CreateV1 路径
   `POST /api/admin/open-platform/clients`，使用 `purpose=external_agent`、
   `audiences=["external_integration"]`、`scopes:["read"]`、下列十项 capability
   和非空 `owner_scope`。CreateV1 当前仍兼容 `read/write` 和历史空范围语义，管理
   自检必须确认专用配置；不为本轮新增 `scope_mode` 或字段级权限引擎，也不自动
   升级已有 Client。
2. 专用十项 capability 为
   `platform.capabilities.read`、`customer.resolve`、`customer.read`、`order.read`、
   `identity.read`、`questionnaire.read`、`customer.detail.read`、`radar.click.read`、
   `radar.link.read`、`chat.read`。`customer.activity.read`、`ai.review_plan.create`、
   `operation.read`、legacy `external_read/external_write` 不属于本专用 profile；
   既有 AI 工作流 Client 的 write grant 保持兼容。
3. raw phone 默认保留其真实 `scope`、`assurance`、`source`、`status`；本轮专用
   Client 允许读取生产已有的 `phone:cn11`、`declared` 事实。UnionID 必须按
   显式 `unionid_scope` 请求和校验；多值返回列表与冲突状态，不能任取一个。
4. 每次敏感读取都先解析并授权请求 customer，再以 canonical lineage 结果重新
   做 scope 检查；无客户、无事实、pending、inactive 或 conflict 保留明确状态，
   不转成成功空数组。
5. 订单 payer 与 beneficiary 永远是两种关系。`customer_id` 只能按最终冻结的
   业务规则输出，不能用任意一侧 ID 替代另一侧；两侧为空保留 unresolved。
   `paid_at` 只来自 verified paid event；退款申请、处理中、outcome unknown
   不计入 `is_refunded`。
6. 金额事实为整数 `amount_minor`，`amount_yuan` 只是格式化字符串；所有业务
   记录保留原始 `source_system + source_record_id`。
7. 所有外部列表使用绑定 operation、过滤、effective grant、auth version 和必要
   水位的签名 opaque cursor；limit+1 lookahead 不泄露第 101 条；cursor 错误、授权
   变化和 Owner 失败均不得被转为空列表。
8. API 查询时间使用秒级 Unix 时间戳，响应时间使用 RFC3339/ISO8601 UTC（通常带
   `Z`）；工作台统一显示 Asia/Shanghai `YYYY-MM-DD HH:mm:ss`，属于展示层转换。
9. Radar link 是无客户归属的内容实体；Radar click 只有可信事件才可归属，
   pending/conflict 必须显式保留，匿名、IP、UA 和 OAuth 中间阶段不作为客户点击。

## 10. 验收与发布门槛

本轮本地真实 PostgreSQL/HTTP 验收已覆盖七项能力、source pair、订单
payer/beneficiary 分离、订单嵌套 DTO、身份 declared/verified/多值/wrong scope 与
非 CN phone 空回退保护、客户详情真实 Owner/remark/follow、Chat staff selector、
Radar、专用 10-cap read-only Client 的 PG 200/写入 403/停用后 401，以及 OpenAPI
嵌入副本、Catalog 14 项、生成客户端和 composition。发布前仍需完成以下门槛：

- token scope/capability 组合、拒绝 write、撤销立即生效；
- canonical customer_id、手机号、UnionID+scope 三种方向的查询往返；
- 唯一、未知、pending、conflict 身份及 409 行为；
- 七项能力均保留来源系统和原始记录 ID；
- 真实历史覆盖率和缺失清单，不以空结果代替缺失数据；
- 订单支付/退款、问卷快照、Chat 私聊/群聊/重复/媒体、Radar 事件去重/禁用链接；
- 游标篡改、过滤变化、授权变化、失败重试；
- 审计、脱敏、Secret/PII 不进日志；
- CRM/API 真实读路径和外部工作台同步回读。工作台仓库或服务器未提供前，只能标记 CRM 侧完成，不能宣称端到端完成；当前代码尚未合并或部署，GitHub SSH/HTTPS 认证无效，生产仍运行旧版本。

## 11. 证据与参考

- PR#173 已通过 Connector 核验为 merged，merge commit 为 `f1ea84caa0070bbd4cc06d6d0628999d31567dc7`；本 PR 只作为 v3 Open Platform 基线证据。
- 旧仓只读 donor 的 HEAD 为 `dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`；仅用于行为、字段和历史数据缺口对照，不复制目录或运行时依赖。
- 订单脱敏参考文件仅用于字段行为核对；禁止打开或传播含真实 Secret 的原始文件。
