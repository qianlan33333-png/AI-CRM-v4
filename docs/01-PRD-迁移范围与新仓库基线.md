# AI-CRM v3 搬迁范围与新仓库基线 PRD

> 文档状态：执行版  
> 决策日期：2026-09-02  
> 新仓库：`qianlan33333-png/AI-CRM-v3`  
> 本期固定供体基线：`AI-CRM-production@4af15e64fb7ebb311b52b17eaf5fc5ea5e8154c8`、`AI-CRM@69c5282fb38058f2cc9872b6feb3f0f54bfad64b`

## 1. 执行结论

AI-CRM v3 是后续唯一的新能力主线。它不是 `AI-CRM-production` 的删减分支，也不是 `AI-CRM` 的复制仓，而是一个从空仓建立、按白名单吸收资产的全新主仓库。

两个旧仓的定位固定如下：

| 仓库 | 后续定位 | 允许的工作 | 禁止的工作 |
|---|---|---|---|
| `AI-CRM-production` | 当前生产维护仓、OneID 行为供体 | 线上故障、安全修复、生产回滚、导出迁移证据 | 新 CRM 板块、为 v3 重复开发业务能力 |
| `AI-CRM` | 管理后台和企微侧边栏前端壳供体 | 视觉、导航、静态资源和页面结构参考 | Go 运行时、OneID 或认证逻辑 |
| `AI-CRM-v3` | 新主系统、最终生产接管仓 | 所有新能力、逐域搬迁、最终切流 | 直接依赖旧仓运行、整仓复制、双主写入 |

**不删除两个供体仓中的代码。** 原方案中“先在 production 大规模删除再继续开发”的动作取消。删除旧资产只发生在 v3 的迁移白名单之外，以及某个能力完成切流后的旧运行入口退役阶段。这样保留生产回滚和行为对照，同时避免在旧仓里进行高风险手术。

## 2. 背景与问题

现有两个仓分别积累了有价值但形态不同的资产：

- `AI-CRM-production` 已形成企微优先的 OneID 机制：`customers.id` 是稳定本地 OneID；`corp_id + external_userid` 可以建立客户；UnionID 允许后补；公众号/小程序 OpenID 按 AppID 隔离；订单客户和实际付款人身份分离。
- `AI-CRM` 提供已经验证过的后台视觉、完整菜单和企微侧边栏信息架构；v3 只白名单复用壳层资产。
- 两个仓也都包含大量历史兼容、迁移、页面、诊断、发布和一次性数据资产。整块搬运会把当前的装配复杂度和历史选择空间一起带入新仓。

v3 要解决的不是“少写代码”，而是四个结构性问题：

1. 同一能力只有一条正式实现路径，不再同时存在新接口、legacy adapter、旧页面路由和临时装配。
2. 核心链路按完整用户旅程验收，而不是按“某个包存在”验收。
3. 每个领域只有一个写入者，迁移期间也不允许两个系统同时拥有业务真相。
4. 新模块可以在不理解全部旧历史的情况下独立开发、测试和上线。

## 3. 产品目标

### 3.1 业务目标

1. 建立可持续开发的 CRM 主系统，逐步承接现有客户、身份、企微、支付及后续 CRM 能力。
2. 保留生产 OneID 的正确业务语义，同时消除旧实现中的兼容列、双写触发器和历史队列耦合。
3. 让企微侧边栏、后台登录、公众号/小程序身份、支付身份成为相互独立但可组合的状态机。
4. 每个迁移板块均可单独切流、回滚和观测，不依赖一次性全量切换。

### 3.2 工程目标

1. Go 模块化单体、单仓、单数据库为默认部署模型。
2. PostgreSQL 是业务真相与异步任务的唯一有状态基础；不把 Redis 作为首期必需组件。
3. OpenAPI 是 HTTP 契约唯一来源；生成代码不可手改。
4. 变更状态与事件/Outbox 在同一事务提交。
5. 外部调用必须有幂等、回执、`outcome_unknown` 和对账语义。
6. 关键链路的真实 PostgreSQL、HTTP、反向代理和失败路径测试必须阻断 PR。

### 3.3 成功标准

- 新增业务能力只需要理解当前领域、公共契约和相关 Journey，不需要扫描两个旧仓的历史入口。
- 关键链路故障能定位到固定阶段，例如 `oauth_exchange`、`identity_provision`、`payment_callback`，不再统一折叠为 503。
- 某领域切流后，旧系统对该领域零写入；回滚行为有明确边界。
- 任何迁移 PR 都能回答：搬了什么行为、没有搬什么、数据由谁写、如何验收、如何回退。

## 4. 非目标

本项目首轮不做以下事项：

- 不做微服务拆分、Kubernetes、多租户、跨企业数据模型。
- 不保证全部旧 URL、旧响应 envelope 和旧页面永久兼容。
- 不迁移全量历史 migration 链、历史只读页、旧审计报告或所有一次性 importer。
- 不把旧 Python 代码逐行翻译成 Go。
- 不把 V2 整个领域目录直接复制后再“慢慢清理”。
- 不在身份内核首版自动执行不可逆客户合并；跨根冲突先进入可审计的 merge candidate。
- 不在新仓库初始化阶段切生产流量或执行真实企微、支付写操作。

## 5. 已冻结的架构决策

### 5.1 技术形态

```text
Browser / WeCom / WeChat / Payment Provider
                    |
                 Edge Proxy
                    |
       AI-CRM v3 modular monolith
       +-- role=api
       +-- role=worker
       +-- role=all
                    |
              PostgreSQL 16
       +-- domain tables
       +-- outbox / audit
       +-- durable jobs
```

采用 Go 模块化单体，原因是后续大部分能力供体在 V2 中已经是 Go；若以 production 的 Python 运行时为新仓基础，再搬运 V2，会形成第二次跨语言重写和长期双栈维护。production 的 OneID 作为**业务规则和验收样本供体**，V2 作为**Go 代码与领域边界供体**。

### 5.2 包依赖规则

- 跨领域只允许依赖 `internal/<domain>/port`、稳定 value object 或领域事件。
- `cmd/aicrm` 是唯一装配具体 Store、Provider 和 Service 的位置。
- `internal/platform` 不得导入任何业务领域具体实现。
- 每张表只有一个领域 Owner；其他领域通过 Port 或事件访问。
- 任何 Provider 网络调用不得持有数据库事务。

### 5.3 OneID v3 规则

1. `customers.id` 是渠道中立、稳定、本地生成的 OneID。
2. 外部身份统一保存为：`kind + scope + normalized_value + assurance + source`。
3. `wecom_external_userid` 的 scope 为企业；OpenID 的 scope 为 AppID；UnionID 的 scope 为开放平台；中国大陆手机号统一使用 `phone:cn11`，只保存 HMAC 检索摘要与独立密文。
4. 前端 query/body 中自报的外部 ID 只能是 `declared`，不能直接建客或提升为 `verified`。
5. 只有完成 Provider 验签或凭据交换的 Adapter 才能构造 `verified` 身份。
6. `Resolve` 只解析，不隐式创建；建客使用显式的 `ProvisionCustomerFromVerifiedIdentity`。
7. UnionID 缺失不阻断企微建客、问卷、订单或支付。
8. Identity 将两个有效 Customer 根连接起来时，首期生成 merge candidate；不以较小 ID、创建时间、字段数量或调用顺序猜主根。
9. 客户合并采用别名/血缘保留，不删除来源根和审计事实。
10. 订单 Customer 与实际付款人 Identity 分离，订单号、提交 ID、消息 ID 只用于关联和幂等，不作为用户身份。

## 6. 搬迁分类规则

所有供体资产必须被分成四类，未分类资产不得进入 v3：

| 代码 | 含义 | 处理方式 |
|---|---|---|
| `BEHAVIOR` | 成熟业务规则或安全语义 | 写成 v3 契约和 Journey，再实现 |
| `PORT` | 清晰、低耦合的接口或 value object | 可重命名后迁移，并补 v3 测试 |
| `ADAPTER` | Provider 签名、验签、协议客户端等叶子代码 | 安全审查后选择性移植 |
| `DISCARD` | 页面、兼容层、历史迁移、临时脚本、重复实现 | 不进入 v3 |

**禁止用“目录复制成功”表示迁移完成。** 迁移完成的最小单位是一个用户可观察能力及其正常、边界、重放和故障路径。

## 7. 新仓库首批白名单

### 7.1 必须随仓库基线建立的能力

| 板块 | 主要供体 | 搬入内容 | 明确不搬 | v3 目标位置 | 首个验收门 |
|---|---|---|---|---|---|
| Repository Kernel | V2 | 目录边界、构建入口、变更分类、生成物检查思想 | V2 完整 CI 历史、Nightly 仪式、旧仓证据台账 | `cmd/`、`internal/platform/`、`scripts/` | 空仓可构建、测试、启动、健康检查 |
| 配置与 Secret | 两仓 | 强类型配置、Secret 不入日志、Provider 启停与权限确认 | 散落环境变量、旧服务器路径、真实 Secret | `internal/platform/config` | 缺配置失败关闭，日志无 Secret |
| PostgreSQL/UoW | V2 | 事务上下文、禁止嵌套、Store 只在事务内写入 | production 的通用 DBAPI 兼容层、旧连接全局变量 | `internal/platform/postgres`、`internal/platform/port` | 事务提交/回滚/嵌套拒绝真库测试 |
| Idempotency/Audit/Outbox | 两仓 | 幂等键、业务回执、审计、事件同事务、结果未知 | 与具体旧表耦合的 receipt、历史 ledger 页面 | `internal/platform/idempotency`、`audit`、`events` | 重放同结果、载荷漂移 409、事务回滚 |
| Durable Jobs | V2 为主 | PostgreSQL 持久任务、重试分类、周期任务注册 | production 的全部旧任务目录、进程内定时器、旧广播队列 | `internal/platform/jobqueue` | Job 与业务事实原子提交 |
| Minimal Customer | production 行为 + V2 Port | `customer_id`、状态、创建、合并别名/血缘 | 完整画像、标签、阶段、时间线、负责人页面 | `internal/customer` | 可在 Identity 同一 UoW 中建客 |
| Identity / OneID | production 行为 + V2 typed contract | scoped identity、assurance、normalize、resolve、bind、provision、conflict、merge candidate | UnionID 兼容列、双写 trigger、直接跨域 SQL、自动破坏性合并 | `internal/identity` | 并发同身份只建一个客户；跨 scope 不串号 |
| Access Core | 两仓 | Admin/Staff Principal、Session、CSRF、Capability、Customer Context | 旧角色页面、历史 API Key 兼容、产品专属分享 Token | `internal/access` | Session 重放/过期/权限拒绝可验证 |
| WeCom Protocol | 两仓 | access token、成员 OAuth、桌面登录、回调验签解密、JSSDK 双签名、external_userid 上下文 | 侧边栏商品/素材/问卷等业务编排、旧无限轮询 | `internal/wecom` | 各授权状态机使用正确 Endpoint，互不调用客户逻辑 |
| Order Kernel | production 行为 + V2 | 商户订单号、金额/币种、Customer、Payer Identity、状态机 | 后台交易大屏、历史订单只读页、商品/优惠券/权益 | `internal/order` | 同一商户订单号幂等，金额漂移拒绝 |
| Payment Kernel | production 行为 + V2 Provider | 预支付、签名、回调验签、退款、回执、对账、outcome_unknown | 交易 UI、运营筛选、支付标签、微信小店完整履约 | `internal/payment` | 重复回调只结算一次；付款人与客户可不同 |
| HTTP/API Contract | V2 | OpenAPI、统一错误、request ID、no-store、生成客户端 | 全部 legacy URL 和万能 JSON 兼容层 | `api/`、各域 `http/` | 契约生成两次无差异，错误阶段可识别 |
| Observability/Release | 两仓 | health/readiness、结构化日志、PII 脱敏、release SHA、原子切换思路 | 旧机器 IP、旧 Nginx/Caddy 全量配置、诊断临时脚本 | `internal/platform/observability`、`deploy/` | 精确版本可观测，关键阶段有指标 |

### 7.2 production 供体的具体白名单

production 代码不直接成为 v3 依赖。以下资产仅用于提取行为、测试向量或协议边界：

- 已接受的微信生态 OneID ADR：企微优先建客、scoped OpenID/UnionID、冲突、身份补全和合并血缘行为。
- OneID 相关的数据库约束、高风险测试和真实用户旅程：转写为 v3 schema 与 Journey，不复制兼容列、双写触发器和跨模块实现。
- 企微授权、侧边栏授权、回调、JSSDK、员工会话相关的协议测试、状态机边界和安全失败分类。
- 微信支付与支付宝的请求签名、通知验签、订单关联、退款和对账行为；具体实现以 v3 Go Port/Adapter 重建。
- 平台层中有价值的 Session、Webhook HMAC、审计、错误分类、外部效果回执和精确版本发布思想。

每项资产迁入前必须在 production 基线中找到可执行测试或明确文档证据；找不到证据的代码默认归为 `DISCARD`，不能因目录名称看似相关而迁入。

以下 production 资产首批全部排除：

- 后台页面、Agent、Automation、模板、静态页面和前端 Bundle。
- AI、Archive、Forms、Growth、HXC、Radar 等非首批业务模块。
- Commerce 中的优惠券、商品页面、交易后台、微信小店完整业务、周期权益和运营导出。
- 生产诊断、临时部署、历史数据修复、旧服务器路径和环境配置。
- 旧数据库兼容包装、跨模块全局状态、为了旧行为存在的双写与自动重定向代码。

### 7.3 V2 供体的具体白名单

优先选择以下类型的 Go 资产进行二次实现或选择性移植：

- `internal/platform/port` 的 UoW 契约及 PostgreSQL 事务绑定实现。
- `internal/identity/port` 的 Kind、Scope、Assurance、Resolve/Bind/Ingest 语义和 Normalizer。
- 已验证企微身份建客的应用编排，但需改成 v3 明确 Command，不复制 Sidebar 耦合。
- `internal/wecom/client` 中低耦合的 OAuth、JSSDK、回调、Token 和 Provider Client。
- `internal/order/provider` 与微信支付运行时中的签名、验签、预支付、退款和回调材料化逻辑。
- OpenAPI 生成、sqlc 生成、架构 import 检查、Secret scan 和 changed-path CI 思想。

以下 V2 资产首批全部排除：

- `cmd/aicrm` 的大型 Composition Root 和所有 `legacy_*` 文件。
- 完整历史 migration 链、V1 archive、DM01、白名单 importer、cleanup 执行器。
- 历史观察表、只读历史页面、旧 Feature Matrix 和大批 evidence 文档。
- 全量前端、旧兼容 URL、发布到现有 id-dev/aa 的环境脚本。
- 在同一包中混合多个用户旅程的聚合 Adapter。

## 8. 后续从 V2 分批搬迁的板块

### Wave 0：运行底座

范围：Repository Kernel、配置、PostgreSQL/UoW、事件、幂等、审计、Job、HTTP 错误、Observability、Migration、CI。

退出条件：新仓空业务状态可启动；健康和就绪可区分；真库测试可重复；没有旧仓运行依赖。

### Wave 1：Customer + OneID

范围：Minimal Customer、Identity、身份冲突、merge candidate、迁移映射。

退出条件：

- 同一个 verified external_userid 并发请求只创建一个 Customer 和一个活动 Identity。
- 相同 OpenID 在不同 AppID 不互通。
- declared 身份不能触发建客。
- UnionID 缺失时企微建客仍成功。
- 两个 Customer 根被同一 verified 身份连接时零静默合并。

### Wave 2：Access + 企微授权

范围：后台员工登录、企微侧边栏员工 OAuth、JSSDK、当前外部联系人上下文、Customer Context Token。

必须拆成四个独立状态机：

1. 桌面后台员工登录；
2. 侧边栏员工 OAuth；
3. JSSDK 外部联系人上下文；
4. 公众号/小程序用户身份交换。

退出条件：OAuth callback 只建立员工 Session；客户解析只发生在可信 external_userid 到达后；员工、当前客服、CRM 负责人和外部联系人四种概念不再混用。

### Wave 3：Order + Payment

范围：订单、微信预支付、支付回调、退款、对账、支付身份；支付宝按同一 Payment Port 单独实现。

退出条件：

- 支付请求不信任前端 OpenID。
- 从 Order Customer 的 verified scope 身份或显式 verified Payer Identity 获取付款人。
- 重复回调、乱序回调和载荷漂移有稳定结果。
- Provider 超时进入 `outcome_unknown`，不得自动换幂等键盲重试。
- 退款有独立状态、回执和对账，不用支付成功状态反推。

### Wave 4：CRM 核心工作台

迁移顺序：

1. 完整 Customer Profile / Customer 360；
2. Staff Directory、角色和数据范围；
3. Customer Tags / Stage / Timeline；
4. Channel / Acquisition；
5. Sidebar Workbench。

注意：Sidebar 只聚合已迁移领域的读模型。不得为了“页面先能打开”直查 V2 数据表或跨域 Store。

### Wave 5：内容与交易周边

迁移顺序：

1. Survey；
2. Product；
3. Media；
4. Radar；
5. Coupon；
6. Service Period / Member Grid；
7. 微信小店订单投影与履约。

依赖关系：Product 依赖 Media；Coupon 和 Service Period 依赖 Product/Order；公开购买依赖 Identity/Order/Payment 已稳定。

### Wave 6：运营执行

迁移顺序：

1. Segment / Audience；
2. Campaign；
3. GroupOps；
4. Automation；
5. Outbound；
6. Push Center / 外部效果观察。

所有发送必须经过 Outbound；AI、Campaign、Automation、GroupOps 均不得直接调用企微写 API。

### Wave 7：智能与平台尾部

范围：AI 内容生成/审批、Stats、Ops、Admin Config、Gateway/MCP、客户定制 Extension。

AI 只产生建议或内容，不决定收件人、发送时机或 Provider 执行结果。

### Wave 8：数据切换与旧系统退役

逐域执行：只读快照 → 导入 → 对账 → shadow read → 新仓唯一写入 → 路由切换 → 观察 → 旧写入口封闭 → 旧代码归档。

## 9. 第一组黄金 Journey

### GJ-01：企微侧边栏首次进入

```text
员工进入侧边栏
→ 侧边栏员工 OAuth 建立 Staff Session
→ JSSDK 取得可信 external_userid
→ Identity Resolve
→ 仅在已存在唯一 canonical Customer 时签发 Context Token
→ 返回最小客户档案
```

本侧边栏不隐式建客；identity not_found/conflict 均失败关闭。必须覆盖：重复进入、并发进入、员工会话过期、JSSDK 失败、外部联系人切换、身份冲突、事务回滚、反向代理误路由、Provider 超时。

### GJ-02：已存在客户再次进入

不创建新客户、不重复写绑定、不依赖 owner_staff_id、不建立隐式员工–客户关系；只刷新可验证的访问上下文。

### GJ-03：公众号/小程序身份补全

同一 AppID 的 verified OpenID 绑定；UnionID 可后补；跨 AppID OpenID 不桥接；跨 Customer 根产生 merge candidate。

### GJ-04：微信支付闭环

```text
创建订单
→ 冻结金额与商品摘要
→ 解析 verified payer identity
→ 创建预支付请求与回执
→ 接收并验签支付回调
→ 同一事务结算订单、支付和事件
→ 重复回调返回同一结果
→ 对账修正 outcome_unknown
```

### GJ-05：退款闭环

退款申请、Provider 调用、回调/查询、最终状态和客户事件彼此独立可审计；任何未知结果均不得显示“退款成功”。

## 10. 数据搬迁规则

### 10.1 新旧数据库边界

- v3 使用独立数据库或完全独立且不可被旧系统写入的 Schema。
- 不允许 v3 直接查询 production/V2 的业务表作为正常运行路径。
- 迁移工具放在 `cmd/migrate-*` 或独立工具仓，运行时包不得 import 迁移器。

### 10.2 一域一写原则

每次切换都必须指定写入 Owner：

| 状态 | 旧系统 | v3 |
|---|---|---|
| 搬迁前 | 唯一写入 | 只读对照/空 |
| Shadow 阶段 | 唯一写入 | 复制或只读比较，不对外生效 |
| Cutover | 写入口关闭 | 唯一写入 |
| 回滚窗口 | 只允许显式回滚流程 | 保留新事实，不做无审计逆向覆盖 |

禁止长期双写。确需短期同步时必须有：唯一 Source of Truth、消息 ID、幂等键、漂移对账、终止日期和删除任务。

### 10.3 ID 映射

- 业务表不保存旧仓数字主键作为 v3 主键。
- 使用独立 `migration_references(source_system, source_kind, source_key, target_kind, target_id, digest)` 记录迁移映射。
- 外部身份值只进入 Identity 域；其他领域只保存 `customer_id`/`identity_id`。
- 任何无法证明唯一关系的记录进入 quarantine，不猜测归属。

### 10.4 历史数据分级

| 等级 | 数据类型 | 处理 |
|---|---|---|
| L0 | 当前运行必需事实 | 在领域切流前迁移并逐条/聚合对账 |
| L1 | 用户可见近期历史 | 领域能力稳定后迁移或建立只读归档服务 |
| L2 | 合规/审计历史 | 以不可变归档保留，不必进入在线核心表 |
| L3 | 临时任务、缓存、旧会话、失败队列 | 不迁移；按政策封存或删除 |

## 11. 路由切换策略

采用 Strangler Pattern，按能力路由，不按整站一次切换：

```text
/auth/v3/*             → v3
/api/v3/identity/*     → v3
/api/v3/sidebar/*      → v3
/api/v3/orders/*       → v3
/api/v3/payments/*     → v3
其余旧路径             → 原系统
```

一个领域达到 Cutover Gate 后，再把旧公开路径映射到 v3。兼容 Adapter 只允许放在 `internal/compat/<source>`，必须记录 Owner、替代路径和删除条件。

## 12. Cutover Gate

一个板块只有同时满足以下条件才能切流：

1. 行为清单已冻结，正常/边界/错误/重放均有测试。
2. 数据 Schema、索引、迁移、回滚或 forward-fix 策略已审查。
3. 关键 Journey 在真实 PostgreSQL 和完整 HTTP/Edge 链路通过。
4. 安全检查覆盖权限、CSRF、签名、PII 日志和 Secret。
5. Shadow 对账无未解释漂移；Quarantine 有明确处置。
6. 新仓为唯一写入者，旧写入口已经机械关闭。
7. Dashboard 能观察吞吐、错误阶段、回调重放、Job、Outbox 和 Provider 结果。
8. 回退仅切路由时不会丢失 v3 已产生事实；需要数据补偿的场景有脚本和审批。

## 13. 风险与控制

| 风险 | 表现 | 控制措施 |
|---|---|---|
| 新仓变成第三个旧仓 | 整目录复制、保留所有兼容路径 | 白名单搬迁；禁止 donor runtime import；Journey 验收 |
| 双系统身份分叉 | 两边都能建客/合并 | Identity 最先切为唯一写；旧仓只通过 API 读取 |
| Python/Go 双栈延长 | production 代码逐行翻译 | production 只提供行为和测试向量；实现以 Go 为准 |
| 企微授权再次混用 | 不同 OAuth Endpoint 共用 Service | 四个独立状态机和独立 Port；端点合同测试 |
| 支付重复效果 | 超时后换键重试、重复回调 | 商户单号、Provider 请求 ID、回执、outcome_unknown、对账 |
| 大模块搬迁后难定位 | 单 PR 改全部 API/UI/DB | 按用户能力切片；关键 Journey 每 PR 阻断 |
| 历史数据拖慢主线 | 先迁所有历史表 | L0-L3 分级；在线核心只迁当前事实 |
| Composition Root 再膨胀 | 所有 wiring 进入一个巨型文件 | 每域导出 Module Registrar；根只注册模块 |

## 14. PRD 交付物

本 PRD 执行后，新仓库基线必须包含：

- 两份执行文档；
- 可启动的最小 Go 服务及 `/healthz`、`/readyz`；
- Customer、Identity、Access、WeCom、Order、Payment 的目录边界；
- Identity scoped reference 的最小 value object 和测试；
- 模块注册清单、供体基线清单和发布脚本；
- 初始 Git commit；
- 不包含任何真实 Secret、旧业务代码或生产数据。

## 15. 最终验收

新仓库基线通过以下验收即视为 PRD 初始化完成：

- 代码树中不存在对 `AI-CRM-production` 或 `AI-CRM` 包的运行时 import。
- `/healthz` 表示进程存活，`/readyz` 表示当前最小依赖已就绪，两者语义分离。
- Identity Ref 拒绝空 scope、跨命名空间、控制字符和无效手机号。
- 两份文档能直接指导下一批 PR，不需要再决定“以哪个旧仓为主”。
- Git 历史从干净根提交开始，旧仓历史仅记录在 source baseline 中。
