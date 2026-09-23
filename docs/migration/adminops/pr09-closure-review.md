# PR09 配置、AdminOps、发布与诊断闭环复核

本文件是 PR09 的**closure review**，以准备审计
`1daff790c78a755c1da2dfb4116cdbf9a9c0247e` 和冻结 donor
`6bfbe5816bb89913c70adaca87d6a486260e016e` 为起点。它不实现、注册、部署或
开放新能力；也不改动 donor、`web/donors/adminops-v2/src` 的 16 个冻结文件、主工作树
或 PR10 webshell。

本文件取代 `pr09-frontend-audit.md` 中“不可复用
`build.mjs → main.ts → legacy.ts → AdminController`”的装配结论。该说法把
“不可部署 donor 的**第二套 shell**”误写成“不可复用冻结 runtime”。用户的最高约束是
前端 100% 原样：PR09 必须与 PR01--PR03 一样复用这条冻结原链，不能重写 loader、
controller、模板运行时、请求层或交互。

## 1. 预先分类与结论

```text
OneID: not involved。配置、本地 AdminOps 投影、发布证据和诊断不解析、关联或写入 Customer/外部身份。
Persistence: local transaction。后续 Config/AdminOps/Release 的业务状态、幂等收据、审计、事件/Outbox 必须同一 PostgreSQL UoW；本复核本身不写库。
Internal durable job: not involved。没有为 PR09 新建队列、Worker、lease/fence 或重试内核。
Provider read/write/external effect: not involved。诊断、check、release journal 和 setup 均为本地行为；Provider 默认 disabled，不能由本模块开启。
```

没有新的身份 matcher、Customer 主键、Provider writer、队列、Worker 或外部效果状态机。
`health.schemas.ts` 的 Customer/OneID/历史邻接声明及静态 API 文案不是 PR09 能力授权。

**闭环结论：** 配置页、app-settings 保存与 setup wizard 有冻结的真实浏览器请求，
因而可以在后端 Adapter 完成后作为一个完整闭环交付。`apidocs` 只有原样静态展示。
发布、广义 AdminOps 和诊断虽有生成 DTO，却没有本轮冻结页面/控制器写动作，不能借
DTO 创造页面、按钮、loader 或 route；在没有原样前端入口前，它们是 archive-only 或
显式 blocked，而不是已迁移能力。

## 2. 16/16 前端冻结与 PR10 单壳装配

`scripts/check-pr09-frontend-freeze.sh` 定义的 16 个文件是唯一 PR09 专属 donor
archive，必须继续 source SHA-256 + `cmp` 逐文件认证：三个 fragment
(`config.html`、`configDetail.html`、`apidocs.html`)、`setupWizard.ts`、`transport.ts`、
health schema 和十一个 generated contract/health/release/runtime entry。没有 PR09
专属 CSS、图片、字体或第二份 sidebar asset；文件集合也不得增加或删减。

不过“16 个 archive 文件”不等于页面运行闭包。实际 donor 页面是：

```text
web/scripts/build.mjs
  -> content-hashed admin bundle (main.ts)
     -> legacy.ts
        -> AdminController + shared runtime + HttpApi
           -> config/configDetail/apidocs fragments and setupWizard.ts
```

`build.mjs` 的作用是把原样 fragment 的 `sc-for/sc-if` 降为嵌套
`<template>`，并在完整 v2 page 外包 `.shell/.side`。PR10 只能复用已冻结的原
runtime/bundle 行为与被降级后的 fragment，**不得**把它生成的外层 `.shell`、`.side`、
`.side-nav`、nav 或 user panel 嵌入页面。

装配合同如下：

1. 由 PR10 的 `internal/webshell/templates/admin_base.html` 保留唯一一级 sidebar、登录
   session/CSRF 门禁和唯一的 `#stage` 宿主；在该 shell 内挂入完整、字节未改的 donor
   runtime 所需 assets/template。不得自建 PR09 loader、renderer、controller 或改写任何
   request/DTO 字节。
2. runtime 所需 donor assets 必须由 release manifest 中已验证的 content-hashed entry
   提供；不得用 donor repository、v2 服务、v2 数据库或远程接口作为运行依赖。和 PR02
   的 `RenderMedia` 一样，v3 只读取已验证 release asset 和外层 `<template id="tpl">`
   的完整嵌套内容，不能截断内部 template。
3. v3 route adapter 必须兼容 controller 实际的导航：`/admin/config.html`、
   `/admin/configDetail.html?cat=<closed-key>`、`/admin/apidocs.html`，并可由 PR10 侧边栏
   的 `/admin/config`、`/admin/api-docs` 入口进入相同的单壳 runtime。不能为方便而改
   donor 的 `.html?cat=` 跳转，亦不能把 release page、redirect 或另一份 donor page
   当作新 UI。
4. `config` 仅允许无 query；`configDetail` 仅允许一个已知、非空 `cat`；`apidocs` 仅
   允许无 query。`invalid_source_history`、`deferred_identity_history`、
   `customer_state_history`、`marketing_state_history`、`static_history`、
   `automation_history`、`wecom_contact_history` 和 profile-catalog 等 hidden query 必须
   在 runtime 前由 v3 route policy 拒绝，不能让 legacy dispatcher 进入跨域页面。
5. `data-page` 只能是 `config`、`configDetail` 或 `apidocs`；所有其它 legacy page 均
   不可由本模块挂载。页面、DOM、文字、inline CSS、图标、事件顺序和请求代码必须来自
   donor，v3 不得额外加按钮、提示、Mock 或二级 shell。

因此“单壳”是外层壳的约束，不是缩减/改写 donor 前端的授权。未实现后端闭环的页面
不挂载；不能先露出只有 frontend 的半成品。

## 3. 冻结 runtime 实际会调用什么

不能把生成 client 的 67 个 operation 当作页面实际可用性。以 donor 的
`AdminController`、`HttpApi`、`api/admin.ts` 和 `setupWizard.ts` 为准，HTTP mode 的
可达动作如下。

| 冻结入口 | 浏览器实际行为 | 后端 Adapter 必须提供的真实请求 | 前端闭环结论 |
| --- | --- | --- | --- |
| config / configDetail 初始化 | 并发读取 category、app settings、push capability、release projection；普通类目 detail 额外读取一条 category。 | `GET /api/admin/config/categories`; `GET /api/admin/config/app-settings`; `GET /api/admin/config/push-capabilities`; `GET /api/admin/config/releases`; `GET /api/admin/config/categories/{categoryKey}`。 | 可用，返回严格适配 donor mapper 所需的闭集 JSON。 |
| app-settings 保存 | `HttpApi.saveConfigCategory` 只在 key 为 `app-settings`、且读 projection 含 `admin_action_token` 时调用 `saveLegacyAppSettingsResource`。secret inputs 被 mapper 跳过。 | `PUT /api/admin/config/app-settings`，冻结 `LegacyAppSettingsResourceSaveRequest`。 | 可用候选；必须成为一次原样前端可完成的写闭环。 |
| setup wizard | 原样 module GET；POST Corp ID/Agent ID、四个空 secret 字段、expected digest/action token 和 `Idempotency-Key`；验证两 audits、两 events，随后 GET readback。 | `GET`/`POST /api/admin/setup-wizard`，冻结 `SetupWizardReadResponse`/`SetupWizardSaveResponse`。 | 可用候选；必须完整交付，不能把 local save 描述为已接入企微。 |
| 配置类目 toggle | controller 调 `HttpApi.toggleConfigCategory`；冻结 HttpApi 直接 reject“DTO 未提供 route-bound token”，**没有 HTTP 请求**。 | 无。不得借 generated `PUT .../enabled` 自创可写 UI。 | 明确 blocked（原样 toast/error）。 |
| 普通类目 save / category check | `saveConfigCategory` 对非 `app-settings` 直接 reject；`checkConfigCategory` 直接 reject，**没有 HTTP 请求**。 | 无。`PUT .../settings`、`POST .../check` 不可因 generated DTO 而提前暴露。 | 明确 blocked（原样 toast/error）。 |
| push capability / release rows | config read mapper 把返回数据渲染为 read-only config rows；没有 frozen controller 写入口。 | `GET /api/admin/config/push-capabilities` 必须返回顶层 `capabilities`；`GET /api/admin/config/releases` 必须返回顶层 `releases:[{id,state,checksum}]`，而不是仅有 `items`。两者均为 local/redacted read。 | 可展示只读投影；所有 PATCH/POST publish/rollback 仍 unmounted。 |
| apidocs | 原样静态 fragment；“下载 OpenAPI” button 没有 id、handler 或 fetch。 | 无。 | 可原样静态渲染；不是动态 OpenAPI 下载页。 |
| AdminAccess 相邻按钮 | `legacy.ts` 原样动态 import `adminAccess.ts`，但 access HTTP 是另一个领域。 | PR09 无。 | 必须在 v3 外层 capability policy block；不改模板文字/DOM。 |

所有上述读写由原样 `transport.ts` 继续带 credentials 和 CSRF header。后端只能适配 URL、
DTO、错误/状态码、session 和 CSRF；不能改写 frozen fetch 实现。

## 4. 后端闭环设计（仅真实请求）

### 4.1 Config 与 setup

Config 是自身表 Owner。推荐拆分为 Config-owned setting/version/receipt/audit/event-outbox
表族；AdminOps 只通过 `internal/config/port` 读取安全 projection 或提交闭集本地意图，
不能访问 Config 表。每次 app-settings 或 setup 写入在同一 PostgreSQL UoW 内完成：
state/version CAS、idempotency receipt、audit、versioned event 和 Outbox 一起 commit 或
rollback。

`PUT /api/admin/config/app-settings` 只接受 registry 明确列出的非敏感 key；未知 key、
JSON 类型漂移、非数字限值、secret/connection key、body 自报 `operator` 一律 fail closed。
`operator` 从 session Principal 取得。返回 projection 只含允许的值，敏感项仅
`configured/masked`，而且不回显 old/new value。

setup POST 同时锁 `wecom.corp_id` 与 `wecom.agent_id`，校验 `expected_digest`，拒绝四个
非空 secret 字段和任何 runtime/provider command。成功 response 必须保持
`ok=true`、`local_only=true`、`external=false`、`runtime_applied=false`，并恰有两条安全
audit 与两条 `setting.updated` event receipt，供冻结 module 的 exact checks 使用。

传输 `Idempotency-Key` 是 donor 的随机 UUID/fallback timestamp；后端仍需记录 transport
key、canonical payload digest 和业务 command identity：同一 key/同 payload 回放相同
receipt，同一 key/payload drift 为 409；不得让随机新 key 覆盖 concurrent expected digest
冲突，亦不得把受理/HTTP 200 误当作运行时已应用。

### 4.2 AdminOps local projections、release 与 diagnostics

`GET categories/push-capabilities/releases` 只能是 AdminOps-owned safe projection：闭集
category、enabled 本地事实、release checksum/state 和 masked capability。若 Config 已
拥有设置，AdminOps 通过 `config/port` 读取，不跨表。`GET category/{key}` 只接受 registry
key；不存在返回 donor-compatible 404/4xx，不用 fallback/demo data。

release candidate、prerequisite、cutover generation/fence、rollback check 可以由独立
Release owner 在自己的表族持久化，且 state/receipt/audit/outbox 也要同 UoW；跨领域
prerequisite 仅经稳定 port/版本化事件取事实。但它目前没有冻结 PR09 写 screen，故不能
注册 create/publish/activate/rollback web mutation 为“配置页能力”。即使以后单独获得
前端授权，`published/activated/rollback` 也只是本地 evidence/journal：不执行 deploy、
binary/static switch、traffic switch、backup restore、Provider call 或消息发送。

diagnostics/health 若由别的页面或运维调用挂载，只能是有界、去敏只读快照：query length、
timeline depth、node count 和 detail 均受限；URL query、secret、cookie、OAuth code、private
key、openid、external_userid、手机号和 credential locator 不得写入日志、response、toast、
audit 或 event。`/health`、`/healthz`、`/api/system/health` 和 execution/runtime generated
contracts不自动成为 PR09 页面；它们需单独页面/owner Journey 后才可开放。

### 4.3 权限与错误合同

所有 PR09 管理 HTML 先经 v3 session/role 门禁；未登录 401/login flow，无相应 admin role
403。每个写请求要求 v3 CSRF 及 route-bound 43-char `admin_action_token`，验证 token 的
path、principal、expiry 和一次性/版本绑定，且不把 token 记录到 URL、DOM、日志或 audit。
写 JSON 的 error 必须保留 donor 400/401/403/409/503 可辨语义；不能以 200 + error field
冒充成功。GET 无权、CSRF、token/digest/idempotency 冲突也必须有 contract test。

## 5. Included、excluded 与明确阻断

**同一 PR 能完整交付的 UI 闭环（仅在后端同时完成时挂载）：**

- config/configDetail 的原样列表、详情、四个 GET projection、app-settings PUT 的读取、
  保存、刷新持久化和安全审计；
- 原样 setup wizard 的 GET → 两键 local CAS POST → 两 audit/两 event receipt → GET
  readback；
- 原样 apidocs 静态展示（无下载/动态 API 行为）；
- config 内 push-capabilities/release 的原样只读 row（不开放写动作）。

**archive-only / blocked / unmounted：**

- `AdminAccess`、客户/OneID、identity repair、customer/history hidden queries、订单/支付、
  audience/campaign/问卷/雷达/群运营、recipient/mark/unmark；
- broadcast、Feishu notification、archive-sync、callback、deferred job、webhook delivery、
  message batch、order-identity repair，以及任何 Provider read/write、发送、重试或 delivery
  claim；
- config category enabled/settings/check、push scheduler/capability PATCH、所有 release
  create/validate/publish/rollback/candidate/cutover API：它们没有本冻结 controller 的 HTTP
  write contract，不能自造 front-end action；
- `/admin/config/app-settings` server HTML、`/admin/config/app-settings/save` 302、
  `/admin/config/releases*` server page、`/admin/api-docs` server page、
  `/admin/config/mcp-tools` redirect、`/admin/execution-runtime` redirect：生成 operation
  是 archive evidence，不是可挂载 donor fragment；
- apidocs 内展示的 sidebar/profile/external-userid 文案和无 handler 的 download button。

“blocked”是冻结 frontend 的实际行为，而非临时 placeholder：对于上述 controller
mutations，应原样发送现有 error/toast、**不发送 HTTP**；不得以 v3 自定义界面把它们
偷偷变成可执行功能。

## 6. 完成 Journey 与验收门禁

PR09 最终实现前后，至少需要以下证据；没有全部闭合前不挂载页面。

1. fresh PostgreSQL 16 migration 及前一版本 upgrade；Config/AdminOps/Release 各自表 Owner、
   CAS、并发写、same-key replay、payload drift、secret rejection 和原子 rollback 集成测试。
2. 管理员登录 → `/admin/config` 原样 runtime 读取四个 projection → 打开 donor
   `configDetail.html?cat=app-settings` → 保存非敏感字段 → refresh 后仍存在 → audit/event
   可查；未登录、无 role、CSRF/action proof/digest conflict 均失败且不写入。
3. 同一管理员打开原样 setup modal → GET → 两键 POST → response 正好两 audits/两 events
   → GET readback；空/非空 secret、provider disabled、runtime_applied、receipt count、
   idempotency/payload drift 都覆盖。全程不产生企微或其他外呼。
4. `/admin/api-docs` 和 donor `.html` compatibility alias 在 PR10 单壳中视觉/DOM replay；
   1440×900、1280×800 对比冻结页面，检查恰一个 v3 sidebar、无 donor `.side`、无第二
   user area、无 hidden query dispatch。对 16/16 执行 SHA-256 + `cmp`，对 runtime entry
   使用 release asset manifest/固定 Node npm 构建与 DOM 请求 replay。
5. generated OpenAPI 两次生成无 diff；只列第 3 节真实可达 API。OpenAPI/diagnostics 不能
   暗示 blocked 或 archive-only route 已可用。`go test -race`、domain/store/HTTP、CSRF/role、
   DOM/Journey、release manifest 与 readiness 都通过后，才可部署到 id-dev；Provider 保持
   disabled。

## 7. 当前阻断与移交

本 review 没有发现 OneID 或 External Effects 必需依赖；也没有理由为配置页面伪造它们。
真正阻断是实现尚未完成：需要 Terra 完成 Config/AdminOps 的 Postgres Owner、session/CSRF/
action-token compatibility、冻结 release runtime 的单壳 binding、exact DTO adapters、
migration/OpenAPI/worker/readiness 与上节 Journey。尤其不能以保留的 67 operation 或已归档
16 files 宣称这些后端已经可用。

PR09 可先实现并验收第 5 节的完整 config/setup/static-docs 闭环；release 与 diagnostics
若要成为用户可操作能力，必须等到有同样 100% donor 的真实页面控制/请求入口，或等待
用户明确授权新的前端版本。此前它们保持未挂载，不以“内部 API 存在”替代完整前后端闭环。
