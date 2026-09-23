# PR07 自动化智能体与固定话术 donor 闭包复核

> 复核性质：只读、独立 worktree、只提交本复核文档；不 push、merge、deploy，不修改主工作树，不修改任何 donor 前端字节。
>
> 复核 worktree：`codex/pr07-closure-review`，基于 v3 `origin/main@015587164bd52ee5d24978aa07970b7c782fbb1e`（`0155871`）。
>
> 固定 donor：AI-CRM-v2 `6bfbe5816bb89913c70adaca87d6a486260e016e`。既有 prep：`2f8849a39eb25e0a63c94bf80d70d78f4d01cd22`；既有 frontend audit：`596150b48283beca9139ef9d5272404bda2113f7`。

> 实施更新：本闭包复核的“NOT CLOSED”结论仅描述当时的只读基线，已被 `codex/feature-automation-final` 的 PR07 集成取代。该集成新增 Automation-owned PostgreSQL `0013`、Store/UoW receipt/audit/outbox、认证/CSRF HTTP adapter、OpenAPI、readiness、PR10 单壳挂载与 donor 20/20 SHA 门禁；不引入 OneID、客户/受众、Worker、Provider 或执行能力。后续收口还使 `active ↔ paused` 成为真实的本地持久状态，并在固定内容保存时通过 Media stable reader 验证图片、附件、小程序和群邀请素材；该状态绝不宣称 Provider 已执行。

> 集成更新：PR23 已基于 `origin/main@507b8c9d610a44a386b77ec8f3d9693f8197abea`（PR06）重放。PR06 的 Group Ops OpenAPI、Composition Root、release migration 与 stage allowlist 均保留，PR07 仅追加 Automation 合同、`0013` 迁移和私有 donor 模板；不会替换任何既有能力。

## 结论

PR07 的 donor 证据已通过 20/20 字节冻结检查，但 v3 能力闭包尚未完成，结论为 **NOT CLOSED**。

- 前端硬门禁锁定 donor 实际运行路径 A：`build.mjs → main.ts → legacy.ts → AdminController → agents/agentEdit templates`。`web/src/admin/sections/automationAgents.ts` 是未被 donor 装配的路径 B，只能作为 characterization evidence，不能被 import、挂载或与路径 A 拼接。
- 路径 A 的列表页只有编辑、复制、校验、暂停、归档五个行操作；编辑页只有返回、校验、保存。不得增添“发布”“启用”“上传/保存固定内容”或任何其他 UI、文案、默认值、交互和请求。
- donor 的 build 产物会生成完整 `.shell/.side`；PR10 的 `admin_base` 是唯一页面壳。正式集成只能在 PR10 的单个 `admin-sidebar` 和 `#stage` 内挂载原样 donor `template#tpl`/业务 fragment，不能直接使用 donor 静态 HTML，也不能生成第二侧栏。
- 当前 v3 基线只有 `/admin/automation-agents` 的预留占位页，没有 `internal/automation` 运行时、真实 HTTP/Store/迁移或 Composition Root 注册；因此现阶段不存在可验收的真实后端闭环。

## 开发前边界分类

按仓库开发规则先分类：

| 检查项 | 结论 | 约束 |
| --- | --- | --- |
| OneID/外部身份 | 不涉及 | Agent/fixed-script 只保存本地配置、Prompt 和版本；不接收客户、external_userid、手机号，不建客、不归属、不绑定身份。 |
| 持久化/内部持久任务 | 涉及持久化；不涉及内部持久任务 | Agent 配置、版本、生命周期、幂等收据、审计和事件必须进入同一 PostgreSQL UoW；本页面不引入 scheduler、worker、ticker 或队列。 |
| External Effects | 当前不涉及 | 不调用 Provider、LLM、企微、outbound，不发送、不重试、不生成 delivery receipt；未来若产生 digest-only intent，只能经既有 outbound/EER 边界，当前 `real_external_call_executed` 必须为 `false`。 |

## 1. 20/20 前端冻结与实际 build 入口

### 1.1 实际 donor 链路

donor 的 `web/scripts/build.mjs` 为 admin 页面生成 `data-page`、`template#tpl`、donor shell 和 module script。`web/src/admin/main.ts` 对非 customers 页面动态加载 `./legacy`；`web/src/admin/legacy.ts` 对 `agents`/`agentEdit` 没有独立 section 分支，落入 `new AdminController(api, page)`，完成 `controller.init()` 后以 `mount(stage, tpl.innerHTML, controller)` 挂载模板。因此唯一有运行时证据的闭包是：

```text
build.mjs
  -> admin main.ts
  -> legacy.ts
  -> AdminController
  -> agents.html / agentEdit.html 的 template#tpl
```

`main.ts` 没有导入 `automationAgents.ts`，`legacy.ts` 也没有调用 `mountAutomationAgents`。在 donor 真实 build 的 `web/dist/assets` 与页面中没有路径 B 的 `mountAutomationAgents`、`当前配置可真实编辑`、`启用前检查`、`发布当前草稿` 标记。不能以 B 文件存在或导出函数存在推导它是活动页面。

### 1.2 字节门禁

冻结范围为 donor 20 个文件，目标目录是 `web/donors/automation-v2/src/${source#web/src/}`。以下 SHA-256 是固定 donor SHA 上的实际值；`automationAgents.ts` 虽列在 20 个 evidence 文件中，仍明确标记为 **runtime excluded**。

| # | donor source | SHA-256 |
| ---: | --- | --- |
| 1 | `web/src/admin/templates/agents.html` | `e2d6324f84d81d4da7c254a35a40c560a5c157a63dc0a1beb9f79a5bd767f707` |
| 2 | `web/src/admin/templates/agentEdit.html` | `6606c40c0e2dce2c2b5367684cc0005cd40d2a7bf190f4bef8626974ba119435` |
| 3 | `web/src/admin/sections/automationAgents.ts`（runtime excluded） | `b208e938eb8ae144d9831bcb145b2cce6eac4873bfe257e40f439e2ec1e3c5e4` |
| 4 | `web/src/admin/sections/util.ts` | `75e3f2b24bc5e031382f7e5c58ddf64578eb7708b06d30467edaf80464362621` |
| 5 | `web/src/admin/controller.ts` | `2c0d51283902b370c431dd04124bcc2215214eac314099fa5d6001ccdb038500` |
| 6 | `web/src/admin/main.ts` | `61bc0ef4ff883bb243af79f989813bbe29c3109168544f32d7358a7608514161` |
| 7 | `web/src/admin/nav.json` | `ee7a9a6629dcdaae4d9792ffcd757cee850bad796edcfb7ff68b6028206f1ed1` |
| 8 | `web/src/admin/registry.json` | `df5f131d9b322e435a09fccdc89c4f8269f3ef03f7856ece250d412af71bb145` |
| 9 | `web/src/shared/api/client.ts` | `2e1bfde0d36f6ab6637da66fddf6b7ee94984364a7175ad787b1da80f98695d5` |
| 10 | `web/src/shared/api/types.ts` | `6fea805d568cf91b7c43292128c2a2b0694cf6515d85c264e048726a270c5a20` |
| 11 | `web/src/shared/api/mockData.ts`（test evidence only） | `d202111695e91432879fb16a3101eae6b7f10ba53237dd493989ffd284c8264c` |
| 12 | `web/src/shared/ui/picker.ts` | `690639bde2fb605024a05fe3196f2ddf8fd5b4ae87c76ef3ff5868a7adf912c0` |
| 13 | `web/src/shared/ui/feedback.ts` | `5c16cd3b057663d2b0c5d2a01416e6330ec979513c6754f1f64f6e41f364a546` |
| 14 | `web/src/shared/ui/download.ts` | `dda7c727bbad4844749e59095dba4c450cf864efb2caaad3f96209f24348a7bb` |
| 15 | `web/src/shared/ui/runtime.ts` | `1122c0be280b1f62c1784510459471bd3ffcc6989493f103daf811900411e66a` |
| 16 | `web/src/shared/ui/tokens.css` | `0f9b719686a8516727ad86fa9475b10cbb059fd10003b3eb6ef041900c7ee3b0` |
| 17 | `web/src/api/admin.ts` | `574293ff7ab6fb0c6d1227ff879649dbc05cf454caaaec6a0fbc1d23727df9ee` |
| 18 | `web/src/api/transport.ts` | `fc5e4b447d10487f571fdafd953cb51756274bc40b019bb51b6cdd61cfbad885` |
| 19 | `web/src/api/generated/p4-automation-agents/p4-automation-agents.ts` | `f2992201652a0857614e23358d588a3a39d0237274172ab21715132318e4ac03` |
| 20 | `web/src/api/generated/health.schemas.ts` | `7f1bc1d05b3e012de46b1d53ef7b56319c0bc032a1c0389fa3fd138c7218b40d` |

独立冻结脚本在固定 donor 上的结果为：

```text
PR07 frontend freeze PASS: donor 6bfbe5816bb89913c70adaca87d6a486260e016e; 20/20 files cmp+SHA verified; no shell or automation-specific external assets
```

这只证明快照字节、文件数、无额外自动化专属外部 asset；不证明 v3 已经挂载页面或拥有后端闭环。图标和返回箭头是 donor HTML/JSON 内联 SVG；不能替换成新图标、字体或自绘 CSS。

### 1.3 build 与壳事实

在固定 donor 的完整 Node 工作副本执行了 `npm run build` 和 `npm run typecheck`，均通过；产物在 `web/dist`，不是仓库根 `dist`。`web/dist/admin/agents.html`、`web/dist/admin/agentEdit.html` 均含 donor `<div class="shell">`、`<aside class="side">`、`<main id="stage" class="stage">` 和 `template#tpl`。这证明直接返回 donor HTML 会产生第二套页面壳。

当前环境的 `npm run version:check` 未通过：Node 为 `v25.9.0`，项目要求精确的 Node `v24.18.0`（npm `11.12.1` 已满足）。该环境差异不能被记作 donor 功能通过；最终 build 验收必须在项目锁定的 Node 版本下重跑。

## 2. PR10 单壳挂载硬门禁

v3 `internal/webshell/templates/admin_base.html` 目前提供一个且仅一个 `<aside class="admin-sidebar">`。`/admin/automation-agents` 当前只是 webshell 占位页；它尚未渲染 Agent 页面。

正式集成必须满足：

1. PR10 `admin_base` 保留唯一壳、登录态、CSRF、导航和 `#stage`；donor build 的 `.shell`、`.side`、完整 `navHtml`、`agents.html`/`agentEdit.html` 外层 document 均不再输出。
2. 浏览器仍以冻结 donor 路径 A 的 `main.ts → legacy.ts → AdminController` bundle 解释 `data-page`、`template#tpl` 和原始业务 fragment；v3 只提供宿主挂载、后端兼容和页面/资源 allowlist，不自建 product-only loader，不把 B 的独立 `innerHTML` 渲染器接入。
3. 只能暴露 Agent/fixed-script 两个页面的业务模板和其必要共享 runtime；`controller.ts`、`api/admin.ts`、`client.ts`、`types.ts`、generated schema 都是混合大文件，不能整文件开放 customer、audience、history、Provider 或其他 API 分支。
4. 禁止出现第二个 `.shell`、`.side`、`side-nav` 或完整 donor document；验收必须检查 DOM 中 `admin-sidebar` 恰好 1 个、`<main>` 恰好 1 个，并确认 donor shell 类为 0。

donor 的相对导航事实必须保留：`AdminController.goto` 生成 `agents.html` 与 `agentEdit.html`，查询参数为 `?type=agent`、`?type=fixed_script` 或 `?id=<positive-safe-int>`。v3 page handoff 的已登记 canonical route 是 `GET /admin/automation-agents`；`/admin/agents.html`、`/admin/agentEdit.html?...` 是 donor 页面别名证据。适配器必须把这些别名映射到同一个 PR10 壳，不能因相对路径误解析成 `/admin/automation-agents/agents.html` 的第二层页面，也不能借此改写 URL 或文案。

## 3. 路径 A 页面与动作闭包

### 3.1 列表 `agents`

原样模板标题为“自动化话术”，创建按钮只有“新增 Agent”和“新增固定话术”。表格列为自动化名称、自动化类型、固定素材、状态、操作。每行操作和顺序固定为：编辑、复制、校验、暂停、归档；没有激活或发布按钮。

| donor 可见动作 | donor 行为/请求 | v3 闭环与 fail-closed 要求 |
| --- | --- | --- |
| 首次加载/刷新 | `GET /api/admin/automation-agents`；`agentEdit` 还读取列表并读取详情 | 真实认证请求；401/403/5xx、DTO 越界或范围不匹配直接显示错误并停止，不用 Mock/sessionStorage/假 200。 |
| 新增 Agent | `goto('agentEdit','?type=agent')`，保存时 POST | 新记录固定为 `paused`、`execution_enabled=false`；服务端重新校验 type/name/code/Prompt，不能由请求体自报 active。 |
| 新增固定话术 | `goto('agentEdit','?type=fixed_script')`，保存时 POST | 与 Agent 共享 donor 表单行为；不自动上传、发布、发送或调用 Provider。 |
| 编辑 | `goto('agentEdit','?id=...')`，加载详情 | 正整数 safe integer 且详情 ID 必须匹配；归档记录不可见/不可编辑。 |
| 复制 | `POST /api/admin/automation-agents/{agentId}/copy` | 只复制本地配置和 Prompt 快照；服务端生成不冲突的本地 code/name（含“（副本）”语义），不带受众、客户或外部效果。 |
| 校验 | `GET /api/admin/automation-agents/{agentId}/precheck`；无已保存 ID 时不发请求 | 只读诊断；响应必须 `real_external_call_executed=false`，否则 fail closed，不显示可启用/可执行的成功结论。 |
| 暂停 | `POST /api/admin/automation-agents/{agentId}/pause` | 仅本地状态变更；不得启动或停止外部任务，不把排队/accepted 当完成。 |
| 归档 | `DELETE /api/admin/automation-agents/{agentId}` | 本地归档、从 list/get 隐藏、重复调用幂等；后续编辑/复制/暂停拒绝。 |

### 3.2 编辑 `agentEdit`

顶栏只有“返回自动化列表”“校验”“保存”。四个页签和顺序固定为“基本信息”“绑定人群包”“Prompt 配置”“固定素材”。

- 基本信息只有名称、编码、类型和只读状态；已有编码 readonly，类型仅 `agent`/`fixed_script`。
- Prompt 输入为 `rolePrompt`/`taskPrompt`，保存走 donor `POST`（新建）或 `PATCH`（已有）；成功后回到原 `agentEdit?id=...` 语义。
- “绑定人群包”是 display-only 说明；不得增加绑定、挑选、解除、客户选择或跳转动作。页面文案提到在人群包详情管理，不等于本页可以调用 Audience/Customer API。
- “固定素材”是 display-only；正文、图片、小程序、PDF、客户群引用只读，普通保存不发 `fixed-content` 请求。
- 页签切换、返回列表和返回编辑页只改变 donor 页面导航/本地显示，不产生额外 API；它们同样不得被改成受众、客户或发布流程。
- 不存在“发布当前草稿”“启用前检查”“固定内容保存/上传”按钮；不允许通过后端能力缺口反向改 donor HTML/TS/CSS。

编辑保存和列表 mutation 的失败必须保留真实错误；不能用 MockApi 的 sessionStorage 行为替代 v3 数据库。所有写操作由后端认证、权限、CSRF、幂等和审计闭环负责，前端随机 Idempotency-Key 不能成为唯一幂等依据。

### 3.3 路径 B 必须排除

`web/src/admin/sections/automationAgents.ts` 未被 donor `main.ts`/`legacy.ts` 装配，且与 A 不等价：它增加更新时间列，删除暂停动作，改为“启用前检查”，只显示三个页签，另加“发布当前草稿”和自建结果栏，使用硬编码相对链接及独立 `innerHTML`。这些差异不是可选增强，而是另一套产品行为。B 不能被部分接入，也不能从 B 借按钮、页签、文案或 DTO，再从 A 借控制器。

## 4. 页面、API 路由与 DTO 合同

### 4.1 12 个 donor operation 的处理

PR07 donor route contract 是页面 handoff 加生成文件 `p4-automation-agents.ts` 中的 11 个 API operation，共 12 个 operation；A 的实际页面只使用其中 8 个 API，页面 handoff 另计。`publish`、`fixed-content` 与 `activate` 均由 v3 后端兼容 Adapter 提供真实本地闭环，但不因此改动或扩展 A 的可见控件。

| operation | 方法与 URL | A 页面 | PR07 处理 |
| --- | --- | ---: | --- |
| page handoff | `GET /admin/automation-agents` | route | PR10 单壳适配；不返回 donor shell。 |
| list | `GET /api/admin/automation-agents` | ✓ | Agent/fixed-script 本地摘要；不引入 customer/audience filter。 |
| create | `POST /api/admin/automation-agents` | ✓ | paused 配置创建；同一 UoW 写收据/审计/事件。 |
| detail | `GET /api/admin/automation-agents/{agentId}` | ✓ | 详情、Prompt、版本和固定内容读取；校验 ID 范围。 |
| update | `PATCH /api/admin/automation-agents/{agentId}` | ✓ | 本地草稿更新；编码不可变；无执行。 |
| archive | `DELETE /api/admin/automation-agents/{agentId}` | ✓ | 归档并隐藏；重复请求幂等。 |
| fixed content | `PUT /api/admin/automation-agents/{agentId}/fixed-content` | ✗ | 保存本地正文及已启用的 Media 图片、附件、小程序和群邀请引用；A 不发送，不新增按钮。 |
| precheck | `GET /api/admin/automation-agents/{agentId}/precheck` | ✓ | 只读本地发布/物料/状态诊断，始终 `real_external_call_executed=false`。 |
| activate | `POST /api/admin/automation-agents/{agentId}/activate` | ✗ | 已发布且物料完整时持久化为 active；不启动 Worker 或 Provider。 |
| copy | `POST /api/admin/automation-agents/{agentId}/copy` | ✓ | 本地复制；不复制外部绑定或执行计划。 |
| pause | `POST /api/admin/automation-agents/{agentId}/pause` | ✓ | 本地暂停；不得执行任务。 |
| publish | `POST /api/admin/automation-agents/{agentId}/publish` | ✗ | 后端可保留本地快照合同；A 无发布按钮、无调用，不能借此补 UI。 |

上表中的“后端合同”不代表允许开放路由给任意页面；必须有管理员认证、权限、CSRF、feature/状态门禁和审计。生成 DTO 不得手工改写，v3 通过后端兼容适配，不得为解决 query 或类型问题修改 donor 前端。

### 4.2 DTO 与校验

- 列表项至少保留 `id`、`automation_type`、`code`、`name`、`fixed_material_summary`、`status`、`execution_enabled`、`materials_configured`、`updated_at` 和 donor 的绑定包显示字段。详情再保留 draft/published Prompt、版本、`has_unpublished_changes`、fixed content package/preview、opaque `legacy_configuration`。
- ID、版本和引用 ID 必须是正的 safe integer；拒绝 NaN、浮点、溢出、负数和响应对象错位。donor JS `Number(...)` 的宽松转换不构成 v3 的安全校验。
- `automation_type` 只允许 `agent`/`fixed_script`；创建默认且强制 `paused`；仅专用 activate/pause Adapter 能变更 active，并与 `execution_enabled` 一致。
- name/code 最多 120 rune；code 只能是小写 ASCII `[a-z0-9_-]`，创建必填且更新不可变。role/task Prompt 各最多 20,000 rune，且不能带首尾空白；空 Prompt 代表未配置，由 precheck 返回相应原因，不得被误判为可执行。
- fixed content `content_text` 最多 4,000 rune，非空正文只允许 `fixed_script`；图片最多 3 个、附件最多 9 个、小程序与群邀请各最多 1 个，均通过 Media stable reader 锁定校验；动态小程序卡片仍因无对应稳定 reader 而 fail closed；不得擅自拉取 blob、URL、Provider 或 tag 数据。
- `legacy_configuration` 只允许 object，默认 `{}`，最多 100,000 bytes；不得解释为客户、人群、Provider 或凭据配置。
- precheck 必须返回配置/物料/执行状态、`can_activate`、原因数组和 `real_external_call_executed=false`。没有可信证据时显示不可用/拒绝，不猜测已配置或已发送。
- 错误必须保留生成 DTO 的结构和状态边界（400 invalid payload、401 authentication、403 permission、404 not found、409 conflict、410 retired/unsupported、503 unavailable 等）；不能把错误映射成成功 toast 或空列表。
- donor/OpenAPI 允许 `automation_type` list query filter，但生成的 `listLegacyAutomationAgents` 不序列化该 query，且 A 页面不传 filter。后端可兼容读取，不能因此增加前端筛选 UI 或修改生成文件。

## 5. included / excluded

### Included

- Agent 与 `fixed_script` 的本地定义、name/code/type、active/paused/archived 本地生命周期。
- role/task Prompt 草稿、draft/published version 和本地发布快照字段；发布若由后端保留，只是本地快照，不执行 Agent。
- 列表、详情、创建、编辑、复制、暂停、归档、precheck 的 donor A 行为及其真实 v3 HTTP/数据库适配。
- fixed content 正文、Media 图片/附件/小程序/群邀请引用的本地读取/校验事实；当前页面不新增写控件，动态卡片仍 fail closed。
- 20 个 donor 文件的字节 evidence、A 的两个 templates、必要共享 runtime/feedback/token，以及 `nav/registry` 中 Agent 元数据的证据性使用。
- 本地幂等收据、审计、配置事件；未来仅携带 Agent ID、published version 和 payload digest 的 effect intent 合同，不执行 effect。

### Excluded

- 路径 B `automationAgents.ts` 的任何 runtime import、UI、发布按钮、结果栏、三页签或独立 loader。
- 运行/生成/审批/触发/调度、内部执行 worker、history/import 和任何自动化运行时；active 仅为本地生命周期事实。
- Customer、OneID、Segment/Audience、人群包绑定、recipient selection、Campaign、客户归属或隐式建客。
- fixed material 上传、生成、外呼、企微/Provider/LLM/token/credential/send/retry/delivery/outbound 写入。
- donor `build.mjs` 生成的完整 HTML shell、`.side`、完整 nav、无关页面、完整 legacy 页面暴露、MockApi/sessionStorage 生产数据源。
- donor runtime/store SQL、旧 migration 历史、v2 HTTP/OpenAPI、Composition Root、deploy/CI、go.mod/sum 和共享端口改动；这些只能作为证据读取。

## 6. 后端 donor 能力与 v3 闭包检查

### 6.1 已有 prep 能力（不是已部署闭环）

既有 prep 在 `internal/automation/port` 与 `internal/automation/app` 形成了本地 Agent service 语义：创建、读取、更新、copy、publish、状态变更、fixed content 保存、precheck，以及 Media metadata reader 接口。服务层约束了 name/code/Prompt/content/config 长度，创建为 paused，active/execution-enabled 只经已发布的专用状态变更进入，归档从可见查询排除。

Media port 只有窄的 transaction-bound `Exists(ctx, id)` 事实读取；不暴露 blob、URL、上传、Provider 或跨领域表。fixed content 的图片、附件、小程序和群邀请可保存并被真实验证；动态卡片没有稳定 port 而按非空拒绝，且没有 tag/受众 port。

mutation 语义包含 reservation、payload digest、完成收据和 changed-payload conflict；同一成功 key/payload 可以重放保存快照，未知外部工作不在本 PR 盲目重试。事件 appender 是调用方 UoW 内的本地 seam；未来 effect intent 只包含 Agent/version/digest facts。

这些是 donor/模块行为证据，不等于 v3 已有 PostgreSQL Store、HTTP handler、OpenAPI、迁移和 Composition Root。当前 v3 基线没有 `internal/automation`，因此仍缺少可真实验收的运行链。

### 6.2 必须补齐的持久化、审计和幂等

正式 v3 实现必须由 Automation 单一 owner 建立自己的表和独立 migration 序列；不得复制旧仓 migration 或跨领域写 Customer/Audience/OneID/outbound 表。Agent 业务状态、幂等 receipt、审计记录、configuration event/outbox 必须同一次 PostgreSQL UoW 原子提交，Provider 网络调用不得持有事务。

每个 mutation 需绑定 actor、operation、object、payload digest 和稳定逻辑幂等 key：相同 key + 相同 payload 重放原结果，相同 key + 不同 payload 返回 conflict；并发更新使用显式锁/CAS/version，不能只依赖 donor 浏览器生成的随机 UUID/时间戳 Idempotency-Key。create/update/copy/publish/pause/archive/fixed-content 的审计应保存状态/版本前后变化和拒绝原因，不把原始 Prompt、凭据、openid、external_userid 等写入结构化日志。

precheck、页面读取和所有失败都必须 fail closed。尤其不能将 HTTP 200、queued、accepted、Mock 或 toast 当作状态已持久化；当前 PR 没有外部写效果，所以不产生真实 provider receipt。

## 7. 对既有 prep/audit 的纠正

1. 既有 audit 把 A/B 写成可选择/推荐关系；本复核将其纠正为硬门禁：实际 donor 运行链只能是 A，B 永不挂载。不能混用 A 的四页签/暂停与 B 的发布/三页签。
2. 既有 prep/audit 的页面 operation 表述把 `publish` 与活动页面能力并列；A 实际没有 publish 按钮，也没有 publish 调用。本复核将 publish 降为后端本地快照兼容合同，禁止进入页面 allowlist，禁止新增发布 UI。
3. “窄 adapter”只能指 PR10 壳挂载和后端兼容/allowlist；不能被实现成自研 product-only loader，也不能跳过冻结的 `main.ts → legacy.ts → AdminController` bundle。宽 donor 文件只能按 allowlist 取 Agent 行为，不能开放其 customer/audience/provider 分支。
4. `automationAgents.ts` 虽是 20/20 evidence 文件，必须在 manifest 的 runtime 结论中单独写 `excluded`；不能因冻结文件数包含它而把它当活动页面。
5. 当前 v3 的 `/admin/automation-agents` 仍是 placeholder，未有真实 Automation module、Store、HTTP、迁移或 root 注册；这是实现缺口，不得以 donor build 通过或 Go port tests 通过宣称闭包。
6. donor `api/admin.ts`、`client.ts`、`controller.ts` 是混合大文件，且 `readAdminRows` 有 `audienceEdit` 等广泛分支；正式 adapter 必须证明未暴露 excluded API。donor 生成 DTO 的 Number 转换和随机幂等 key 也不能直接作为 v3 的边界实现。
7. 当前固定 donor build/typecheck 通过，但锁定版本检查在 Node `v25.9.0` 环境失败；最终验收要在 Node `v24.18.0` 重跑，不能把版本差异隐藏为通过。

## 8. 完成标准（全部满足才可标记 CLOSED）

- 固定 donor SHA 不变；20/20 文件逐字节 `cmp + SHA-256` 通过，无额外页面壳、CSS、图标、字体或 automation-specific asset；任何 donor 漂移直接失败。
- 浏览器实际运行追踪明确为 `build.mjs → main.ts → legacy.ts → AdminController → template#tpl`；无 `automationAgents.ts` import、无 B marker、无自建 loader、无 bundle 重写。
- `/admin/automation-agents` 及 donor `agents.html`/`agentEdit.html` 查询别名均在 PR10 单壳内工作；DOM 中 `admin-sidebar` 恰好一个，`.shell/.side/side-nav` 为零，只有一个 `main#stage`。
- 列表/编辑页所有文案、默认值、字段、页签、按钮、顺序、URL、DTO 和反馈与路径 A donor replay 一致；无发布、激活、固定内容写、绑定或受众控件。
- A 的 list/detail/create/update/copy/pause/archive/precheck 逐项命中真实 v3 HTTP + PostgreSQL + 权限/CSRF + 审计/幂等；错误、越权、范围不匹配、active/archived、外部事实不完整时均 fail closed；不使用 Mock/sessionStorage。
- Agent-owned migration、Store、HTTP adapter、事件/Outbox/receipt 和 root registration 完成；业务状态、receipt、audit/event 同一 UoW，独立序列，不修改旧迁移、不跨 owner 写表。
- 状态只允许 donor 合同的 paused/archived 生命周期；`List/Get` 对 archived 隐藏，active/execute/send/Provider/OneID/customer/audience/outbound 均有明确拒绝或不可达证明；precheck 始终报告 `real_external_call_executed=false`，activate 无成功 2xx。
- media/reference 只使用窄 metadata port；四类非空引用和动态卡片在当前范围明确拒绝；无 blob、URL、tag、客户群或 Provider 读取旁路。
- 在项目锁定 Node 版本下完成 build/typecheck/version check，并完成 Automation 窄测试、race/vet、页面 DOM/API journey 和单壳回归；测试记录固定 donor SHA、v3 base SHA 和本次 adapter 版本。

## 9. 本次复核证据

本次只读执行的关键结果：

```text
PR07_DONOR_DIR=/path/to/frozen/aicrm-v2 \
  bash scripts/check-pr07-frontend-freeze.sh
# PASS: donor 6bfbe5816bb89913c70adaca87d6a486260e016e; 20/20 files cmp+SHA verified

go test -count=1 ./internal/automation/...       # prep: PASS
go vet ./internal/automation/...                 # prep: PASS
go test -race -count=1 ./internal/automation/... # prep: PASS
go test -count=1 ./internal/automation/...       # fixed donor: PASS
npm run build                                    # fixed donor: PASS, 56 pages + 9 hashed entries
npm run typecheck                                # fixed donor: PASS
npm run version:check                            # env FAIL: Node v25.9.0 != required v24.18.0
```

测试通过只支持上面的证据边界，不改变本复核的 **NOT CLOSED** 结论。下一步若实现，只能在独立 PR/明确授权下补齐 v3 module、单壳 adapter、真实 Store/HTTP/migration 与 journey；本复核没有进行这些写入。
