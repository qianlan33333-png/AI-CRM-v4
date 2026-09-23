# PRD：内容雷达 UnionID 与 OneID 全能力迁移

> 文档状态：Draft for implementation
>
> 目标仓库：AI-CRM-v3
>
> 供体基线：`qianlan33333-png/AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`
>
> 范围：仅内容雷达，不扩展到自动化运营、标签动作、群发、销售跟进或 Customer 360
>
> 日期：2026-09-04

## 1. 产品结论

本项目把 v2 内容雷达的管理端、公开访问端、统计、事件、导出和历史数据承接能力完整迁入 v3，同时将 v2 的“匿名/占位归因”升级为符合 v3 OneID 规则的真实用户归因。

身份链路的硬约束是：**雷达授权链路最终只向业务域返回经 Provider 验证、带微信开放平台 scope 的 UnionID；内容雷达不直接使用 OpenID、external_userid 或手机号匹配客户，UnionID 再交给 Identity Port 与其他身份关联并解析为 `customers.id`。**

这里的“返回 UnionID”指 Provider Adapter 向可信服务端身份流程返回验证事实，不表示浏览器、管理端接口、日志、CSV 或 Radar 数据表可以获得或保存原始 UnionID。

## 2. 背景与问题

### 2.1 v2 已有能力

供体仓库已存在以下可复用的用户行为和界面：

- 内容雷达列表：搜索、类型筛选、状态筛选、新建、编辑、启停、复制链接、二维码分享。
- 内容编辑：链接、图片、PDF 三类内容；标题、描述、封面/媒体、目标链接、是否需要授权、启用状态。
- 数据详情：访问、授权用户、查看次数、转化率、事件筛选和 CSV 导出。
- 公开入口：`/r/{code}`。
- 服务端：配置 CRUD、状态切换、分享投影、访问事件、统计和导出。

### 2.2 v2 不能原样搬运的事实

在冻结基线中，事件模型不保存 `unionid`、`openid`、`external_userid` 或 `customer_id`，统计投影中的授权用户与真实查看指标仍是零值/兼容占位，CSV 的身份列也没有真实数据。公开入口主要是重定向和本地事件记录，不构成完整的微信授权、OneID 解析与媒体查看闭环。

因此，“整目录复制”只会搬来视觉能力和局部 CRUD，不会交付真实用户归因。v3 必须冻结供体行为合同，重新实现所有权、事务和身份边界。

### 2.3 v3 当前状态

v3 当前只有 `/admin/radar-links` 预留入口，没有 `internal/radar` 领域实现。供体 `radar.ts`、相关 API 客户端和生成类型处于 donor hash gate 管理范围，不能为接入 v3 而直接修改。

## 3. 两轴架构分类

```text
OneID: involved — resolves identity and may explicitly provision a customer
Persistence: local transaction + Provider read
External Effects: not involved
```

- 内容定义、启停、分享投影和匿名统计本身不需要 OneID。
- 授权访问涉及外部身份：必须由微信 Provider 读取获得经验证、带开放平台 scope 的 UnionID，然后调用 `internal/identity/port`。
- `Resolve` 只解析；只有 `not_found` 且存在合格的 verified fact 时，才允许显式调用 `ProvisionCustomerFromVerifiedIdentity`。
- 企微/微信授权换取身份是 Provider read，不是 Provider write；本范围不创建 Outbound 或 External Effects 意图。
- 不引入 Radar 私有队列、Worker、ticker、重试状态机、身份匹配器或客户主键。

## 4. 目标与非目标

### 4.1 目标

1. v2 内容雷达的可观察前端能力在 v3 中全部可用，而非仅有菜单或静态页面。
2. 链接、图片、PDF 都有完整的创建、发布、公开查看和统计闭环。
3. 需要授权的雷达访问严格取得 UnionID，并由 OneID 解析到唯一渠道中立客户根。
4. 统计口径、事件幂等、审计、CSV 和历史数据承接可验证、可回放、可追溯。
5. 供体前端业务文件保持字节冻结，v3 通过窄 Adapter 和宿主集成提供真实数据。
6. 管理端、公开端和 API 都具备安全、权限、失败态、并发控制和可观测性。

### 4.2 非目标

- 不做“看了内容后自动打标签、发消息、建任务、变更阶段”等营销自动化。
- 不做公众号菜单、群发、企微消息或其他 Provider 写入。
- 不做身份自动合并、批量客户合并或人工合并工作台。
- 不把 Radar 扩展成素材库；媒体仍由 Media 领域拥有。
- 不复制 v2 migration 历史，不把 v2 作为运行时数据库或远程依赖。
- 不把历史点击推断成已验证的 v3 OneID 归属。

## 5. 用户与场景

### 5.1 CRM 管理员/运营

- 创建链接、图片或 PDF 雷达。
- 决定是否要求微信授权。
- 启停、编辑、复制公开链接、下载二维码。
- 查看总体指标和逐条访问事件。
- 按时间、事件、归因状态筛选并导出 CSV。

### 5.2 微信访问者

- 打开公开雷达链接。
- 无需授权的内容直接查看，并形成匿名访问统计。
- 需要授权的内容进入微信授权；成功后继续查看。
- 重复刷新、回退或网络重试不会制造重复授权用户或重复终态事件。

### 5.3 审计/支持人员

- 能判断一次访问处于匿名、已授权、身份冲突、Provider 失败还是内容不可用。
- 能使用 receipt、trace 和安全摘要定位问题，但看不到原始 UnionID、OpenID、手机号、OAuth code 或 token。

## 6. 核心用户旅程

### 6.1 创建并发布

1. 管理员进入“内容雷达”，选择新建。
2. 选择内容类型并填写对应字段。
3. 选择“需要微信授权”或“匿名可访问”。
4. 保存草稿，通过校验后启用。
5. 系统生成不可变的公开 code、分享 URL 和二维码。
6. 所有写入以幂等命令提交，配置、版本、收据、审计和 Outbox 同事务完成。

### 6.2 匿名访问

1. 访问 `/r/{code}`。
2. 系统校验雷达启用状态和内容版本，创建匿名 view session。
3. 链接类先记录落地事件后 302；图片/PDF 进入 v3 公开查看器。
4. 浏览器用一次性 event token 上报真实阶段；重复上报由幂等键折叠。

### 6.3 授权访问与 OneID

1. `/r/{code}` 发现 `auth_required=true`，创建一次性、限时、绑定 code/版本的 OAuth state。
2. 浏览器跳转到微信授权。
3. 回调只把 `code` 交给可信 Provider Adapter；网络调用发生在数据库事务之外。
4. Adapter 验证 Provider 响应，并且必须取得带明确 `wechat-open-platform:<platform-id>` scope 的 UnionID。
5. 缺失 UnionID、缺失 scope、验签失败、state 失效或 Provider 结果不可信时失败关闭；不得退回 OpenID。
6. Adapter 产生不可由 HTTP 构造的 verified identity fact，调用 Identity Port：
   - `resolved`：返回规范 `customers.id`；
   - `not_found`：通过显式 provision 用例建客并绑定 verified UnionID；
   - `pending/conflict`：不猜、不合并、不归属客户，记录安全失败态并展示可重试/求助页面。
7. Identity 领域负责 UnionID 与未来/已有 OpenID、external_userid、手机号等其他身份的关联；Radar 不访问 Identity 表。
8. Radar 只保存 opaque `identity_id`、规范 `customer_id` 快照、归因状态和证据摘要，随后签发用途受限的访问 session。
9. 浏览器得到的是 session/cookie 和内容结果，绝不得到原始 UnionID。

### 6.4 查看数据

1. 管理员打开详情页。
2. 页面显示 PV、已授权用户、真实查看次数和授权转化率。
3. 事件列表显示安全客户投影、归因状态、内容阶段和时间。
4. CSV 与页面使用同一查询合同；默认不导出原始外部身份值。

## 7. 功能需求

### FR-01 内容雷达列表

- 支持关键词、内容类型、状态、是否授权、创建时间筛选和分页。
- 展示标题、类型、状态、授权策略、PV、授权用户、真实查看次数、更新时间。
- 提供编辑、启停、复制链接、二维码和详情操作。
- 禁止删除已有事件的雷达；首版只支持停用，不做硬删除。

### FR-02 创建与编辑

- 类型：`link`、`image`、`pdf`。
- 公共字段：标题、描述、类型、授权策略、状态。
- 链接字段：仅允许 `https` 目标地址；域名策略可配置并审计。
- 图片/PDF 字段：使用 Media 领域稳定 Port 返回的引用，不复制媒体所有权。
- 启用前校验所有必填项和媒体可读性。
- 已发布编辑产生新版本；`public_code` 不变，访问会话绑定具体版本。
- 更新使用版本号/CAS，冲突返回 409，不静默覆盖。

### FR-03 启停与分享

- 草稿可启用；启用后可停用；停用链接返回 410。
- 分享 URL 使用 v3 正式域名和 `/r/{code}`。
- 二维码由分享 URL 确定性生成，不把身份信息编码进二维码。

### FR-04 公开查看器

- 链接：服务端记录落地后重定向。
- 图片：使用 v3 H5 viewer 展示，资源经受控媒体读取接口返回。
- PDF：使用 v3 H5 viewer，支持移动端查看和受控 Range 请求。
- 不向浏览器暴露私有对象存储地址。
- 内容不存在返回 404，停用/已撤回返回 410，Provider/身份失败使用明确错误页。

### FR-05 事件采集

- 基础阶段：`landing`、`oauth_started`、`oauth_verified`、`identity_resolved`、`content_opened`、`redirected`、`image_loaded`、`pdf_opened`、`failed`。
- 每个 view session + 内容版本 + 事件阶段只有一个确定性幂等键。
- 服务端事件不可由客户端自报 `customer_id`、`identity_id`、verified 或 UnionID。
- 客户端只提交服务端签发的短期 event token 和允许的阶段。
- 不保存完整 IP、User-Agent、referrer query 或 OAuth 参数；如需反滥用只保存带轮换盐的短期摘要。

### FR-06 指标口径

- `PV`：有效 `landing` 事件数。
- `授权用户`：成功取得 scoped verified UnionID 后的 distinct `identity_id` 数；用 identity 计数避免客户根合并导致历史波动。
- `真实查看次数`：链接为 `redirected`，图片为 `image_loaded`，PDF 为 `pdf_opened` 的有效事件数。
- `授权转化率`：授权用户数 / PV；PV 为零时显示 `0%`。
- 管理端所有指标使用同一时区和半开区间 `[from,to)`。
- 不把 OAuth 开始、排队或页面 HTTP 200 当作已授权/已查看。

### FR-07 事件查询与 CSV

- 支持按时间、阶段、归因状态、客户安全搜索键和分页查询。
- 页面客户展示通过 Customer stable Port 投影，不跨域查表。
- CSV 字段：时间、雷达、版本、事件阶段、归因状态、客户公开编号/显示名、安全 UnionID 掩码（可选）、receipt。
- 默认 CSV 不含原始 UnionID、OpenID、external_userid、手机号、IP、Cookie 或 token。
- 若未来确需敏感导出，必须独立 PR、独立权限、用途审计和下载过期策略。

### FR-08 权限与审计

- 读权限：`radar.read`；写权限：`radar.write`；导出权限：`radar.export`。
- 创建、编辑、启停、导出和历史导入都写入审计记录。
- 未认证管理端继续使用 v3 统一登录和 CSRF 机制。

### FR-09 历史数据承接

- 提供一次性 `cmd/migrate-radar-v2`，只读读取经过授权的冻结快照。
- 导入内容定义、状态、媒体映射和可核验的历史统计事实；所有来源均保存 source key 和 digest。
- 历史内容默认以 `disabled` 或 `draft` 导入，完成 URL/媒体/授权策略复核后再启用。
- 旧点击保存在 legacy history 投影中，不混入 v3 实时指标。
- 不根据相同整数 ID、姓名、手机号、旧 digest 或不完整 external ID 推断 `customers.id`。
- 只有能转化为 scoped provider-verified UnionID 的证据才可进入 Identity 流程；否则保持 unattributed。
- 真实迁移前必须重新核实源 schema、记录数、媒体可访问性和授权窗口；文档中的旧盘点数字不是执行依据。

## 8. 信息架构与前端页面

| 页面 | 路由 | 交付内容 |
|---|---|---|
| 雷达列表 | `/admin/radar-links` | 搜索、筛选、分页、启停、分享、二维码、指标 |
| 新建 | `/admin/radar-links/new` | 三种类型、媒体选择、授权策略、校验 |
| 编辑 | `/admin/radar-links/{id}/edit` | 版本/CAS、发布校验、冲突反馈 |
| 详情 | `/admin/radar-links/{id}` | 指标、事件筛选、客户投影、CSV |
| 公开入口 | `/r/{code}` | 路由、授权、内容查看、失败态 |

供体 `web/src/admin/sections/radar.ts` 等业务文件继续受 hash gate 保护。v3 新增独立 Radar Adapter：负责调用 v3 API、把真实统计/事件投影为供体 UI 合同、修正路由和错误反馈；任何必须改变供体视觉行为的工作先更新 Behavior Contract，再通过受控例外实现，不能直接解冻整片供体代码。

## 9. API 合同

### 9.1 管理端

- `GET /api/admin/radar-links`
- `POST /api/admin/radar-links`
- `GET /api/admin/radar-links/{id}`
- `PATCH /api/admin/radar-links/{id}`
- `POST /api/admin/radar-links/{id}:enable`
- `POST /api/admin/radar-links/{id}:disable`
- `GET /api/admin/radar-links/{id}/share`
- `GET /api/admin/radar-links/{id}/stats`
- `GET /api/admin/radar-links/{id}/events`
- `GET /api/admin/radar-links/{id}/events.csv`

为兼容冻结供体，可由宿主 Adapter 将 v2 旧路径转换为上述 canonical 路径；服务端兼容别名必须有 owner、删除条件和测试。

所有写接口要求：

- `Idempotency-Key`；
- 版本/CAS；
- RFC 7807 风格错误；
- 配置、收据、审计、Outbox 同一 PostgreSQL UoW。

### 9.2 公开端

- `GET /r/{code}`：入口与跳转。
- `GET /api/public/radar/{code}/content`：受 session 保护的内容投影。
- `POST /api/public/radar/{code}/events`：受签名 event token 约束的事件提交。
- `GET /api/public/radar/oauth/callback`：OAuth 回调，不返回身份原值。
- `GET /api/public/radar/{code}/media`：受控媒体流/Range。

## 10. 数据模型与所有权

Radar 领域拥有：

- `radar_links`：当前配置、public code、状态、当前版本、CAS。
- `radar_link_versions`：不可变版本快照。
- `radar_operation_receipts`：管理写入幂等收据。
- `radar_oauth_states`：一次性 state 摘要、过期时间、消费状态。
- `radar_view_sessions`：匿名/已归因访问会话，不含原始外部身份值。
- `radar_events`：不可变事件、幂等键、`identity_id`/`customer_id` 快照、归因状态。
- `radar_audit_events`：管理和安全审计。
- `radar_outbox`：Radar 自有版本化领域事件。
- `radar_legacy_imports` / `radar_legacy_events`：来源映射和只读历史事实。

Identity 领域独占 UnionID 原值和其与其他 ID 的绑定；Customer 领域独占客户主数据；Media 领域独占媒体对象。Radar Store 只访问 Radar 表。

## 11. 事务边界

### 11.1 管理命令

`link/version + operation receipt + audit + outbox` 在同一 PostgreSQL 事务提交或回滚。

### 11.2 OAuth 回调

1. 事务外调用 Provider 换取用户信息。
2. Provider Adapter 验证并生成 scoped UnionID verified fact。
3. 在同一 UoW 中消费 state、调用支持该 UoW 的 Identity Resolver/Provisioner、创建 Radar session、写授权事件、收据、审计和 Outbox。
4. 若现有 Identity Port 不能参与调用方 UoW，先补齐共享 Port，禁止用两个独立事务伪装原子性。

### 11.3 事件提交

`event + idempotency receipt + audit/outbox` 同事务；重复键返回原结果。任何 Provider 网络调用不得持有事务。

## 12. 失败模型

| 场景 | 行为 |
|---|---|
| 雷达不存在 | 404，不创建事件 |
| 雷达停用 | 410，保留安全访问审计 |
| OAuth state 过期/重放 | 400/409，不调用 Identity |
| Provider 未返回 UnionID | 失败关闭，绝不 fallback 到 OpenID |
| UnionID 缺开放平台 scope | 失败关闭并审计配置问题 |
| Identity not found | 仅 verified fact 可显式 provision |
| Identity conflict/pending | 不归属、不合并，展示明确失败态 |
| 媒体缺失 | 不计入真实查看；页面可重试/求助 |
| 重复事件 | 返回原 receipt，不重复计数 |
| 并发编辑 | 409，要求刷新，不覆盖新版本 |
| CSV 超时 | 本期使用有上限的流式导出；超限明确拒绝，不新建私有 Worker |

## 13. 安全、隐私与合规

- 原始 UnionID、OpenID、external_userid、手机号、OAuth code、access token、Cookie、Secret 不进入 Radar 表、结构化日志、错误文本、前端状态或 CSV。
- OAuth state 一次性、短 TTL、绑定 radar code/版本和回跳路径，保存摘要而非明文。
- 会话 Cookie 使用 `HttpOnly`、`Secure`、`SameSite` 和用途绑定。
- 公共事件接口启用 body 上限、速率限制、事件白名单、重放保护和 CSP。
- 目标链接只允许 HTTPS，防止开放重定向；媒体响应禁止嗅探并设置正确 MIME。
- 所有 Identity/Customer 查询通过稳定 Port，不跨领域读写表。

## 14. 非功能指标

- 管理查询 p95 ≤ 300ms（常规分页、数据库热态）。
- 公开入口本地处理 p95 ≤ 200ms，不含 Provider 网络时间。
- Provider 读取超时上限 10s，超时不持有数据库事务。
- 已提交事件与幂等收据 RPO=0；服务级 RTO ≤ 4h。
- PostgreSQL 16 为唯一业务存储；不引入 Redis、Kafka 或新微服务。
- 关键指标具备 trace、receipt 和安全错误码，但无 PII 标签。

## 15. 方案比较与决策

### 方案 A：整模块复制 v2

优点是目录迁移快；缺点是复制了占位统计、无身份归因、旧事务边界和供体依赖，违反 v3 所有权规则。拒绝。

### 方案 B：只做 API 兼容，让旧页面继续工作

能快速显示页面，但仍不能补齐 UnionID、公开媒体查看、真实 UV、审计和历史隔离；会把“HTTP 200”误当完成。拒绝。

### 方案 C：冻结 UI 行为 + v3 Radar 领域 + 窄宿主 Adapter

保留供体交互和验收基线，由 v3 重建领域、公开端、事务与 OneID 集成；Adapter 只做数据/路由/错误映射。该方案工作量较大，但边界清晰、可长期维护，采用。

## 16. 发布范围与分 PR 计划

每个 PR 只交付一个可观察能力：

1. **R0 行为冻结**：供体 manifest、Behavior Contract、journey fixtures、差异矩阵。
2. **R1 领域与管理 CRUD**：迁移、Port、Store、App、OpenAPI、权限、事务收据。
3. **R2 冻结 UI 接入**：Radar Adapter、webshell 路由、列表/表单/启停/分享真实可用。
4. **R3 公开匿名链路**：`/r/{code}`、链接跳转、图片/PDF viewer、匿名事件。
5. **R4 UnionID + OneID**：OAuth Provider read、严格 UnionID、Resolve/显式 Provision、冲突失败态。
6. **R5 统计、事件与 CSV**：真实口径、客户安全投影、筛选和导出。
7. **R6 历史导入工具**：冻结快照、dry-run、隔离历史指标、回滚/复核报告。
8. **R7 端到端与发布门禁**：真实浏览器、PG16、Provider staging、隐私扫描、运维手册。

详细测试驱动步骤见 `docs/plans/2026-09-04-content-radar-oneid-full-migration.md`。

## 17. 验收标准

### 产品验收

- 三种雷达可创建、编辑、启停、分享并在移动端真实访问。
- 要求授权时，Provider 缺 UnionID 必须失败；有 scoped verified UnionID 时能解析或显式建客。
- 已存在的其他 ID 关联由 Identity 展现为同一 `customers.id`，Radar 不参与匹配。
- 列表、详情、事件和 CSV 指标一致，重复刷新不重复计算授权用户。
- 停用、媒体缺失、身份冲突、Provider 超时和并发编辑都有可理解的失败反馈。

### 架构验收

- v2 只作为只读供体，不是 Go module、submodule、远程运行依赖或正常数据源。
- donor hash gate 通过；供体业务文件未改。
- 无 Radar 自建身份匹配、队列、Worker、Provider writer 或重试内核。
- Radar Store 只访问 Radar 表；Identity、Customer、Media 通过稳定 Port。
- 需要原子的状态、收据、审计和 Outbox 在同一 UoW。

### 证据验收

- Go 单元/集成/契约/旅程测试通过。
- PostgreSQL 16 migration up/down 与并发/幂等测试通过。
- 浏览器录屏或截图覆盖列表、新建、分享、三种公开查看、授权、详情和 CSV。
- Provider staging 证明最终身份事实为 scoped UnionID，且缺 UnionID 不 fallback。
- 日志、DB、CSV 和浏览器网络响应的 PII 扫描无原始 UnionID 等敏感值。
- 发布后 `/healthz`、`/readyz`、正式路由、release SHA 和实际 API/页面结果均已验证。

## 18. Definition of Done

只有当上述产品、架构和证据验收全部通过，内容雷达才算“完成”。以下状态均不能单独视为完成：目录已创建、接口骨架、Mock 成功、页面可打开、HTTP 200、任务已排队、PR 已合并或菜单已出现。

## 19. 待实施前确认项

- 微信开放平台 scope/config 的正式值及 staging 凭据所有者。
- Media stable Port 是否支持受控读取、MIME 与 Range；若不支持，先补 Port，不跨表。
- Identity Resolver/Provisioner 是否能加入 Radar 调用方 UoW。
- 生产正式域名和 OAuth 回调白名单。
- v2 历史快照的授权来源、schema、数据量与媒体映射。
- 当前主线 migration 最大编号；本计划中的建议编号在开工时重新编号。
