# PR09 donor 前端完整闭包审计

审计对象是 v2 donor commit
`6bfbe5816bb89913c70adaca87d6a486260e016e`，不是当前 v2 工作树的任意变更。
本审计在独立 worktree/分支
`/private/tmp/aicrm-v3-adminops-audit.xoux15` /
`codex/import-adminops-audit` 上完成，基于已有准备提交 `f9cc84c`
(`codex/import-adminops`)；只增加本文件和
`scripts/check-pr09-frontend-freeze.sh`。未改 donor 工作树、既有 donor 前端字节、
Composition Root、OpenAPI、migration、lock、deploy 或 CI。

审计分类（先于实现）：

| 维度 | 结论 | 依据/边界 |
| --- | --- | --- |
| OneID / 外部身份 | **不涉及** | 配置、AdminOps 本地投影、发布证据和诊断不解析 Customer/渠道身份；任何 `external_userid`、Customer、identity-repair 只在 donor 的邻接静态/混合 schema 中出现，禁止挂载。 |
| 持久化 | **本分支不新增** | 仅保留 donor 证据、manifest、哈希/cmp check；v3 业务持久化仍由 Config/AdminOps/Release owner 和 Terra migration 负责。 |
| 内部持久任务 | **不涉及** | 审计脚本不创建 worker、队列、lease、retry 或内部 job；生成的 job operation 是 archive-only。 |
| 外部效果 | **不涉及** | 本分支不调用 Provider/企微/Feishu/LLM；任何生成的 `accepted/queued` 不能成为发送或交付成功。 |

## 结论

已有 prep commit 的前端闭包是 **16 个文件、逐字节 donor archive**，不是 active
`web/src` build，也不是完整 v2 Admin Console。16 个文件包含 3 个 HTML fragment、1
个 setup wizard、1 个共享生成 schema、10 个 generated API/health/release/runtime
模块和 1 个 transport；没有 PR09 专属 CSS、图片、字体或外部 SVG asset。

`health.schemas.ts` 虽然必须原样归档，但有 25,892 行，包含 Customer、历史、侧边栏
和 identity-adjacent 类型。它不能被当成 PR09 DTO 白名单；运行时必须从生成模块中收窄
import，不能把整个 schema 或混合 operation 暴露给页面。

真实 donor build 会把每个 registry screen 包成带 `<div class="shell">`、
`<aside class="side">`、完整导航和用户区的页面。PR10 只能保留 v3-owned
`internal/webshell/admin_base` 及其唯一一级侧边栏；本 archive 只能作为业务 fragment
与请求契约证据，不能直接部署 donor `dist/admin/*.html`，也不能复制 donor 的
`main.ts`/`legacy.ts`/`controller.ts`。

完整冻结证明：

```text
$ scripts/check-pr09-frontend-freeze.sh
PASS: PR09 frontend freeze verified (16 files; donor 6bfbe5816bb89913c70adaca87d6a486260e016e; SHA-256 + cmp; no shell/sidebar or external assets)
```

## 1. Donor build 的实际页面和入口

证据来自 donor SHA 的 `web/scripts/build.mjs`、`web/src/admin/registry.json`、
`web/src/admin/nav.json`、`web/src/admin/main.ts` 和 `web/src/admin/legacy.ts`。`build.mjs`
的 `adminPage()` 对普通 registry screen 读取同名 template，先做 `<sc-for>/<sc-if>`
转换，再插入 `adminShell()`；因此 template 本身不是完整 HTML 文档。

| 入口/页面 | registry/nav 事实 | 实际 donor 输出/URL | 入口行为和 PR09 决定 |
| --- | --- | --- | --- |
| 配置一级 `config` | `registry.json:L153-L160`，`nav.json:L117-L121`，nav key 为 `config` | `dist/admin/config.html`；静态浏览器地址是 `/admin/config.html`，不是 `/admin/config` | `body[data-page="config"]`，由 `main.ts` 默认加载 `legacy.ts`，再由 `AdminController` mount `config.html`。显示类目、开关、详情按钮，以及 setup/admin-access 两个静态扩展按钮。 |
| 配置二级 `configDetail` | `registry.json:L162-L169`，无一级 nav | `dist/admin/configDetail.html`；controller 用 `configDetail.html?cat=<categoryKey>` | v3 host 只允许活动闭集 `app-settings`、`push-capabilities`、`releases`；显示字段、开关、secret password 控件、检查/保存/返回。只允许 adapter 绑定 Config/AdminOps 闭集字段。 |
| API 文档 `apidocs` | `registry.json:L279-L285`，`nav.json:L123-L127` | `dist/admin/apidocs.html`；静态地址 `/admin/apidocs.html` | 静态 donor 展示侧边栏 API 列表，下载按钮无 id/监听器，是 evidence fragment，不是 v3 OpenAPI 入口。 |
| setup wizard modal | `config.html:L29-L35` 的 `#open-setup-wizard`，`legacy.ts:L422-L451` | 不生成独立页面；配置页的 `#config-extension-dialog` 内异步 mount `setupWizard.ts` | 只读取/保存 Corp ID、Agent ID；必须保留 `local_only=true`、`external=false`、`runtime_applied=false` 以及两审计/两事件回执检查。 |
| AdminAccess modal（邻接但排除） | `config.html:L36-L41` 的 `#open-admin-access`，`legacy.ts:L452-L456` | 同一个 config modal | 对应 `/api/admin/admin-access`，属于 `internal/access`，不在 PR09 route、叶子或挂载范围；生成文件中出现只因 donor 页面相邻按钮存在。 |
| AdminOps server page | `p4-adminops-safe.ts` 的 `getAdminOps*Page` | `/admin/config`、`/admin/config/releases`、`/admin/config/releases/new`、`/admin/config/releases/{releaseId}` | 这是后端兼容页面 operation 返回 `string`，不是上面 3 个静态 `.html`；donor 没有对应 release HTML template。当前准备提交不注册。 |
| API-docs server page | `p4-api-docs-compat.ts` | `/admin/api-docs` 返回 page；`/admin/config/mcp-tools` 返回 302 到 `/admin/api-docs` | 与静态 `apidocs.html` 分离；没有 donor 前端绑定，不能因为 generated client 存在就改 OpenAPI 或挂 route。 |
| execution runtime page | `p4-execution-runtime-compat.ts` | `/admin/execution-runtime` 返回 302 | 只有观察 API contract，没封存页面 template；不得把 redirect 当作新增 shell。 |

### 1.1 页面构建的第二 shell 来源

`build.mjs:L31-L90`：

- `adminShell()` 写入 `<div class="shell">`、`<aside class="side">`、`<nav class="side-nav">`
  和 `side-user`；`navHtml()` 会把整个 `nav.json` 生成到侧栏。
- 每个普通 screen 都加载 `tokens.css`，rich screen 还加载 `labs.css`；Admin bundle
  是 `admin/main.ts`，不是页面自身的 PR09 模块。
- `registry.json` 的 `screens` 不只 3 个 PR09 页面；donor `web/README.md` 也明确是
  完整管理后台和 sidebar/H5 集合。直接把 donor build 产物接入 v3 会产生第二侧栏和
  全量跨域入口。

### 1.2 hidden query entry 必须排除

`legacy.ts` 是广域加载器，且在普通 template mount 前优先 dispatch query。`page=config`
可能进入以下隐藏历史入口：

`invalid_source_history`、`deferred_identity_history=1`、`marketing_state_history=1`、
`customer_state_history=1`、`static_history=1`、`automation_history=1`、
`wecom_contact_history=1`、`profile_catalog_history=1`。

这些入口会加载 `invalidSourceHistory`、`deferredIdentityHistory`、
`marketingStateHistory`、`customerStateHistory`、`automationHistory`、
`wecomContactHistory` 或 profile catalog 等跨域模块；不能随 config template 一起
复制。其他 page 还有 `message_history`、`outbound_task_history`、群发、问卷、
campaign、coupon、groupops 等历史 query，也不属于 PR09。

## 2. HTML、DOM、TS、CSS 和 asset 闭包

### 2.1 原样 HTML/TS 文件事实

| archive 文件（donor source path） | bytes / 行数 | DOM/行为摘要 | 外部 asset |
| --- | ---: | --- | --- |
| `web/src/admin/templates/config.html` | 7,266 / 52 | breadcrumb、配置类目表、`configPage.total/rows`、toggle/open action、setup/admin-access 静态行、config extension dialog；全部 inline style。 | 无 |
| `web/src/admin/templates/configDetail.html` | 4,382 / 43 | back inline SVG、`cfg.cat` status/toggle、blocks/fields；switch、password、text、number、readonly、textarea；检查/保存/返回；全部 inline style。 | 无（SVG 为 inline markup） |
| `web/src/admin/templates/apidocs.html` | 16,392 / 36 | 静态 API 列表和参数表；“下载 OpenAPI” button 无行为；全部 inline style。 | 无 |
| `web/src/admin/sections/setupWizard.ts` | 4,942 / 82 | exact import generated setup client/schema/transport；render 仅 Corp/Agent 和 mask 状态；submit 后 boundary + receipt + GET readback。 | 无 |
| `web/src/api/transport.ts` | 3,131 / 69 | 从 cookie 读取 CSRF，设置 `X-CSRF-Token`/credentials；`unwrapGenerated` 拒绝非 2xx；不拼业务 URL。 | 无 |

`config.html` 的 2 个静态扩展行很容易被误当成已实现能力：`企微接入基础配置`实际
打开 setup modal，而`后台访问成员`仍会触发被排除的 AdminAccess；模板字节不能为了隐藏
后者而修改。应由 v3-owned route/mount policy 在外层禁止或阻断，不要改变 donor HTML。

`apidocs.html` 的 5 个展示行是：

`GET /api/sidebar/v2/workbench`、`PUT /api/sidebar/v2/profile`、
`GET /api/sidebar/v2/periodic-orders`、`PUT /api/sidebar/v2/periodic-orders/{id}/remark`、
`POST /api/sidebar/v2/materials/send`。它们被标为 Token/侧边栏；第二张参数表还展示
`external_userid`、`source`、`industry`、`industry_description`、
`needs_blockers_followup`、`updated_by`。这组静态 donor 文案不属于 PR09 API surface，
不能直接暴露为 OneID/侧边栏能力。

### 2.2 CSS 和媒体资产审计

donor `web/src` 中发现的 CSS 文件只有：

`admin/sections/labs.css`、`admin/sections/questionnaireEditor.css`、
`admin/sections/questionnaireEditorStyles.css`、`admin/sections/wecomTagPicker.css`、
`shared/ui/tokens.css`、`sidebar/sidebar.css`。PR09 archive **没有**复制这些文件：

- 3 个 PR09 HTML 都使用 inline style；setup wizard 也使用 inline style。
- `tokens.css` 是 donor shell/shared token 样式，含 `.shell`/`.side` 相关布局，必须由
  PR10 唯一 v3 shell 决定，不能连同 donor `adminShell()` 引入。
- `labs.css`、问卷、tag picker 和 `sidebar.css` 是其他域 asset，不能作为 PR09 依赖。
- donor tree 没有 PR09 需要的 png/jpg/gif/svg/webp/woff/ttf/ico 独立文件；模板中的 SVG
  是 inline 文本。check script 对 archive 目录也做了外部 asset 断言。

### 2.3 setup wizard 的实际 import/interaction 闭包

`setupWizard.ts:L1-L9` 只导入：

1. `p4-setup-wizard` 的 `getSetupWizard`/`saveSetupWizard`；
2. `health.schemas` 的 `SetupWizardReadResponse`/`SetupWizardSaveResponse`；
3. `api/transport` 的 `apiRequestOptions`/`unwrapGenerated`。

`L11-L15` 的 `assertLocalBoundary` 必须同时满足 `ok`、`local_only===true`、
`external===false`、`runtime_applied===false`。`L54-L68` 的 POST payload 固定发送：

```text
wecom.corp_id, wecom.agent_id,
wecom.secret="", wecom.callback_token="", wecom.callback_aes_key="", ai.api_key="",
expected_digest, admin_action_token
```

随后必须检查 `receipt.audits.length === 2`、`receipt.events.length === 2`，再 GET 并
重复 boundary 检查。`L64` 使用 `crypto.randomUUID()`（fallback `web-${Date.now()}`）
生成 `Idempotency-Key`；这是 donor 原样行为，不能改 byte，但 v3 adapter 必须另行定义
稳定的逻辑幂等键/replay 语义，不能把每次随机 UI key 当作同一命令的重试身份。

## 3. 16 个 byte-exact archive 文件与哈希

下表的 source hash 来自 donor git object `6bfbe5816bb89913c70adaca87d6a486260e016e`，
target hash 来自 `web/donors/adminops-v2/src`；`source == target` 且逐文件 `cmp`。
完整双向行也保存在 `pr09-donor-sha256.txt`（前端兼容视图为
`pr09-frontend.sha256`）。

| # | source / archive target | bytes / 行数 | SHA-256（source = target） |
| ---: | --- | ---: | --- |
| 1 | `admin/templates/config.html` | 7,266 / 52 | `1b6a6d05e3300e3a59fc00068daa987b4e586c57a122f726c1c9abbbc1f34eb3` |
| 2 | `admin/templates/configDetail.html` | 4,382 / 43 | `235dcf711ce9965a6862fa5f0b9cc0d967f2fc25c13cd5a19d17ec40e446df14` |
| 3 | `admin/templates/apidocs.html` | 16,392 / 36 | `63b1ddcd4a49d70a9c3cd8351138b64efa07a166f3e66f7dbd09277767633a4a` |
| 4 | `admin/sections/setupWizard.ts` | 4,942 / 82 | `4903070814159e3ddd703aa649dd2be9428b6829fcc4164918359fe4ad1b70e6` |
| 5 | `api/generated/health.schemas.ts` | 692,834 / 25,892 | `7f1bc1d05b3e012de46b1d53ef7b56319c0bc032a1c0389fa3fd138c7218b40d` |
| 6 | `api/generated/health/health.ts` | 1,139 / 43 | `f86374997bbd6b16e660906222a1b0d5793ce28f30b3eed696eb484611692b04` |
| 7 | `api/generated/p4-config-settings-compat/p4-config-settings-compat.ts` | 11,984 / 421 | `d5fdac4633a01d2f175af83f1e9de666db8472f4cb905de7ca46de18dd0f3f80` |
| 8 | `api/generated/p4-api-docs-compat/p4-api-docs-compat.ts` | 3,513 / 140 | `8fca747b7a749c1122c15de15c6f53c8abf8dde4d69ad944c2d5734301c6d7cc` |
| 9 | `api/generated/p4-setup-wizard/p4-setup-wizard.ts` | 7,359 / 299 | `47443ffbf72dfbc1744b434e8e4f913d1c2c6f9a3bc7ec11371edb255593ccd3` |
| 10 | `api/generated/p4-adminops-safe/p4-adminops-safe.ts` | 66,960 / 2,546 | `3efc2b02a6ae0b7953da74bb89d1636f3792aeb7707e4f0e4be5627cde0fe44e` |
| 11 | `api/generated/p4-data-health/p4-data-health.ts` | 5,612 / 212 | `b302e46a388250485694557c65db0406abef0d68370f6f4069726fb6dc51763f` |
| 12 | `api/generated/p4-legacy-health/p4-legacy-health.ts` | 1,535 / 56 | `d1827f386c993e929ac37ad5bcdda68aa8bb5fa948e634435418341b1869f182` |
| 13 | `api/generated/p4-system-health/p4-system-health.ts` | 1,486 / 53 | `00ab5702d1f2d3aed9440010ac5642669634be822726036e95892a155f7b31cd` |
| 14 | `api/generated/p4-execution-runtime-compat/p4-execution-runtime-compat.ts` | 5,544 / 208 | `362be88fb99d33a236ce9b288453e7d1eed8e640179c49a1cb9ddfeec6a186ca` |
| 15 | `api/generated/p4-release-plane/p4-release-plane.ts` | 26,187 / 1,016 | `2685d45c3ab155bfba8f51250e601ab82bd52652758f9595c14e21824149e43c` |
| 16 | `api/transport.ts` | 3,131 / 69 | `fc5e4b447d10487f571fdafd953cb51756274bc40b019bb51b6cdd61cfbad885` |

表中路径相对于 `web/src`；archive 前缀是
`web/donors/adminops-v2/src/`。check script 还断言 archive 文件集合恰好 16 个、donor
HEAD 干净且等于指定 SHA、没有 shell/sidebar markup 和外部 asset。

### 3.1 查阅但不应封存/导入的 donor 文件

以下文件用于解释实际入口和风险，但不在 16 文件闭包中：

| donor 文件 | 不封存/不导入原因 |
| --- | --- |
| `web/scripts/build.mjs` | 会生成完整 v2 shell、nav、tokens/labs asset 和全量 registry 页面。 |
| `web/src/admin/main.ts` | 将 config/configDetail/apidocs 全部转发到广域 `legacy.ts`；仅 customers 非历史页走独立模块。 |
| `web/src/admin/legacy.ts` | 动态引用 customer、campaign、问卷、群发、identity/history 等几十个 section；包含 hidden query dispatch。 |
| `web/src/admin/controller.ts` | `AdminController` 同时管理 customers、commerce、audience、groupops、config 等全域页面；`goto()` 生成 `.html` 旁路。 |
| `web/src/api/admin.ts` | 巨型 DTO glue；config 读取和保存与 customer、audience、groupops 等 imports 共存，不能作为 PR09 API adapter。 |
| `web/src/shared/api/client.ts` | `AdminApi`/Mock/Http 实现跨域；Mock 的 secret 更新语义尤其不能成为生产行为。 |
| `web/src/admin/registry.json` / `nav.json` | registry/nav 包含 34 屏、sidebar 外部身份邻接项；只作为入口证据。 |
| `web/src/shared/ui/tokens.css`、`web/src/sidebar/*` | donor shell/sidebar 样式/实现，复制会违反 PR10 单侧栏。 |
| `web/src/admin/sections/adminAccess.ts` | `internal/access` 访问控制，不属于 Config/AdminOps/Release owner。 |

## 4. 完整 URL / DTO / response inventory（67 operations）

以下是 archive 中 generated/API `.ts` 文件实际导出的全部 HTTP operation；`—` 表示无成功
response，仅保留错误/blocked union。状态集合来自对应 generated response type，不能将
`202 accepted` 或本地 `queued` 解释为 Provider 成功。所有路径在本准备分支都没有注册。

### 4.1 Config settings、API docs、setup wizard（10 operations）

| operation | 方法 / URL | request DTO / params | success response（状态） | PR09 处置 |
| --- | --- | --- | --- | --- |
| `getLegacyAppSettingsPage` | GET `/admin/config/app-settings?q&scope` | `GetLegacyAppSettingsPageParams` | `string`（200；错误 400/401/403/503） | page compatibility evidence；不绕过 JSON adapter |
| `saveLegacyAppSettingsPage` | POST `/admin/config/app-settings/save` | `LegacyAppSettingsSaveForm` | 302（错误 401/403） | legacy form evidence；不注册 |
| `getLegacyAppSettingsResource` | GET `/api/admin/config/app-settings?q&scope` | `GetLegacyAppSettingsResourceParams` | `LegacyAppSettingsResponse`（200；400/401/403/503） | 候选 Config read |
| `saveLegacyAppSettingsResource` | PUT `/api/admin/config/app-settings` | `LegacyAppSettingsResourceSaveRequest` | `LegacyAppSettingsResourceSaveResponse`（200；400/401/403/409/503） | 候选 Config write，需 action proof/UoW |
| `getLegacyApiDocsPage` | GET `/admin/api-docs` | 无 | `string`（200；401/403/500/503） | server API-doc page evidence |
| `getLegacyMcpToolsRedirect` | GET `/admin/config/mcp-tools` | 无 | 302（错误 401/403/503） | redirect evidence，不能扩展 OpenAPI |
| `getSetupWizard` | GET `/api/admin/setup-wizard` | 无 | `SetupWizardReadResponse`（200；400/401/403/503） | setup local read |
| `saveSetupWizard` | POST `/api/admin/setup-wizard` | `SetupWizardSaveRequest` | `SetupWizardSaveResponse`（200；400/401/403/409/503） | setup two-key local CAS |
| `listAdminAccessMembers` | GET `/api/admin/admin-access` | 无 | `AdminAccessReadResponse`（200；400/401/403/503） | **排除，internal/access** |
| `saveAdminAccessMembers` | PUT `/api/admin/admin-access` | `AdminAccessSaveRequest` | `AdminAccessSaveResponse`（200；400/401/403/503） | **排除，internal/access** |

`LegacyAppSettingsResponse` 是最多 12 行、metadata map、3 summary cards、最多 10 条
audit 的 projection；masked row 只有 `configured`，不返回 value。
`LegacyAppSettingsResourceSaveRequest.settings` 虽是 string/number map 且有 index
signature，后端必须继续用闭集 key validator；`operator` 被忽略，actor 从 authenticated
Principal 派生。错误包括 `secret_input_forbidden`、`settings_idempotency_conflict`。

### 4.2 `p4-adminops-safe.ts`（36 operations）

| # | operation | 方法 / URL | request DTO | success response（状态） | 处置 |
| ---: | --- | --- | --- | --- | --- |
| 1 | `listAdminOpsBroadcastJobs` | GET `/api/admin/broadcast-jobs` | 无 | —（401/403/409/503） | archive-only/blocked |
| 2 | `runAdminOpsFeishuHourlyReportPlan` | POST `/api/admin/broadcast-jobs/feishu-hourly-report/run` | `AdminOpsActionRequest` | `AdminOpsLocalAcceptedResponse`（202；400/401/403/409/503） | archive-only；不调用 Feishu |
| 3 | `getAdminOpsFeishuNotificationSetting` | GET `/api/admin/broadcast-jobs/notification-settings/feishu` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | archive-only |
| 4 | `saveAdminOpsFeishuNotificationSetting` | PUT `/api/admin/broadcast-jobs/notification-settings/feishu` | `AdminOpsNotificationSettingRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/409/503） | archive-only；仅 secret ref 形状 |
| 5 | `validateAdminOpsFeishuNotificationPlan` | POST `/api/admin/broadcast-jobs/notification-settings/feishu/validate` | `AdminOpsActionRequest` | `AdminOpsLocalAcceptedResponse`（202；400/401/403/409/503） | archive-only |
| 6 | `getAdminOpsBroadcastJob` | GET `/api/admin/broadcast-jobs/{jobId}` | path `jobId` | —（400/401/403/404/409/503） | blocked/unmounted |
| 7 | `approveAdminOpsBroadcastJob` | POST `/api/admin/broadcast-jobs/{jobId}/approve` | path + `AdminOpsConfirmedActionRequest` | —（400/401/403/409/503） | blocked/unmounted |
| 8 | `cancelAdminOpsBroadcastJob` | POST `/api/admin/broadcast-jobs/{jobId}/cancel` | path + `AdminOpsCancelJobRequest` | —（400/401/403/409/503） | blocked/unmounted |
| 9 | `getAdminOpsJobsSummary` | GET `/api/admin/jobs/summary` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | read candidate，仍需 owner 复核 |
| 10 | `listAdminOpsArchiveSyncJobs` | GET `/api/admin/jobs/archive-sync` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | archive-only |
| 11 | `runAdminOpsArchiveSyncPlan` | POST `/api/admin/jobs/archive-sync/run` | `AdminOpsConfirmedActionRequest` | `AdminOpsLocalAcceptedResponse`（202；400/401/403/409/503） | archive-only；不执行 provider |
| 12 | `listAdminOpsCallbackJobs` | GET `/api/admin/jobs/callbacks` | 无 | —（401/403/409/503） | callback branch unmounted |
| 13 | `listAdminOpsDeferredJobs` | GET `/api/admin/jobs/deferred-jobs` | 无 | —（401/403/409/503） | deferred branch unmounted |
| 14 | `listAdminOpsWebhookDeliveryJobs` | GET `/api/admin/jobs/webhook-deliveries` | 无 | —（401/403/409/503） | delivery branch unmounted |
| 15 | `listAdminOpsMessageBatchJobs` | GET `/api/admin/jobs/message-batches` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | message-batch branch unmounted |
| 16 | `getAdminOpsMessageBatch` | GET `/api/admin/jobs/message-batches/{batchId}` | path `batchId` | —（401/403/409/503） | blocked/unmounted |
| 17 | `acknowledgeAdminOpsMessageBatch` | POST `/api/admin/jobs/message-batches/{batchId}/ack` | path + `AdminOpsBatchAckRequest` | `AdminOpsLocalAcceptedResponse`（202；400/401/403/409/503） | unmounted；ack != delivery |
| 18 | `getAdminOpsPushCapabilities` | GET `/api/admin/config/push-capabilities` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | local read candidate |
| 19 | `setAdminOpsPushScheduler` | PATCH `/api/admin/config/push-capabilities/scheduler` | `AdminOpsEnabledRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/409/503） | local gate only |
| 20 | `setAdminOpsPushCapability` | PATCH `/api/admin/config/push-capabilities/{capabilityKey}` | path + `AdminOpsEnabledRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/409/503） | closed capability only |
| 21 | `listAdminOpsReleases` | GET `/api/admin/config/releases` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | local release read |
| 22 | `createAdminOpsRelease` | POST `/api/admin/config/releases` | `AdminOpsCreateReleaseRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/409/503） | local record only |
| 23 | `getAdminOpsRelease` | GET `/api/admin/config/releases/{releaseId}` | path `releaseId` | `AdminOpsLocalOKResponse`（200；400/401/403/404/503） | masked read |
| 24 | `compareAdminOpsReleaseShadow` | GET `/api/admin/config/releases/{releaseId}/shadow-compare` | path `releaseId` | `AdminOpsLocalOKResponse`（200；400/401/403/404/503） | local compare only |
| 25 | `validateAdminOpsRelease` | POST `/api/admin/config/releases/{releaseId}/validate` | path + `AdminOpsActionRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/404/409/503） | local validation |
| 26 | `publishAdminOpsRelease` | POST `/api/admin/config/releases/{releaseId}/publish` | path + `AdminOpsPublishReleaseRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/404/409/503） | published fact only |
| 27 | `rollbackAdminOpsRelease` | POST `/api/admin/config/releases/{releaseId}/rollback` | path + `AdminOpsConfirmedActionRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/404/409/503） | local fact only |
| 28 | `getAdminOpsConfigPage` | GET `/admin/config` | 无 | `string`（200；401/403/503） | server page != static config.html |
| 29 | `getAdminOpsReleasesPage` | GET `/admin/config/releases` | 无 | `string`（200；401/403/503） | no donor release template |
| 30 | `getAdminOpsNewReleasePage` | GET `/admin/config/releases/new` | 无 | `string`（200；401/403/503） | no donor release template |
| 31 | `getAdminOpsReleasePage` | GET `/admin/config/releases/{releaseId}` | path `releaseId` | `string`（200；401/403/404/503） | no donor release template |
| 32 | `listAdminOpsCategories` | GET `/api/admin/config/categories` | 无 | `AdminOpsLocalOKResponse`（200；401/403/503） | local category read |
| 33 | `getAdminOpsCategory` | GET `/api/admin/config/categories/{categoryKey}` | path `categoryKey` | `AdminOpsLocalOKResponse`（200；401/403/503） | closed category read |
| 34 | `setAdminOpsCategoryEnabled` | PUT `/api/admin/config/categories/{categoryKey}/enabled` | path + `AdminOpsEnabledRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/409/503） | CAS/local only |
| 35 | `setAdminOpsCategorySettings` | PUT `/api/admin/config/categories/{categoryKey}/settings` | path + `AdminOpsCategorySettingsRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/409/503） | no secret material |
| 36 | `checkAdminOpsCategory` | POST `/api/admin/config/categories/{categoryKey}/check` | path + `AdminOpsActionRequest` | `AdminOpsLocalOKResponse`（200；400/401/403/503） | diagnostic only; no Provider |

`p4-adminops-safe.ts` 的 “safe” 命名不能替代逐 operation review。generated schema
同时包含 `AdminOpsJobKind.order_identity_repair`、`target_kind=order_identity`、
`notification`、`message_archive` 等邻接值；它们不应获得 route。job status
`queued/running/completed/failed/cancelled/outcome_unknown/retired` 和
`real_external_call_executed` 必须原样区分；summary 的
`outcome_unknown_auto_retry` 不能触发盲重试。

### 4.3 health、runtime、data-health（9 operations）

| operation | 方法 / URL | request DTO | success response（状态） | 边界 |
| --- | --- | --- | --- | --- |
| `listLegacyDataHealthChecks` | GET `/api/admin/data-health/checks` | 无 | `LegacyDataHealthChecksResponse`（200；401/403/503） | 4 项 registry observation |
| `getLegacyDataHealthCheck` | GET `/api/admin/data-health/checks/{checkId}` | path `checkId` | `LegacyDataHealthCheckResponse`（200；401/403/404/503） | bounded check detail |
| `getLegacyDataHealthSummary` | GET `/api/admin/data-health/summary` | 无 | `LegacyDataHealthSummaryResponse`（200；401/403/503） | 不调用外部系统 |
| `getLegacyHealth` | GET `/health` | 无 | `LegacyRuntimeHealthSnapshot`（200；405） | 只读 readiness/mode flags |
| `getSystemHealth` | GET `/api/system/health` | 无 | `SystemHealthResponse`（200；503） | 组件/PII/secret guard flags |
| `getLegacyExecutionRuntimePage` | GET `/admin/execution-runtime` | 无 | 302 page redirect（302；401/403/405） | 不是新 shell |
| `getLegacyExecutionRuntime` | GET `/api/admin/execution-runtime` | 无 | `LegacyExecutionRuntimeResponse`（200；401/403/503） | observed snapshot；不启动 worker |
| `getLegacyExecutionTimeline` | GET `/api/admin/executions/{executionId}` | path `executionId` | `LegacyExecutionTimelineResponse`（200；401/403/404/503） | graph/timeline only |
| `getHealthz` | GET `/healthz` | 无 | `HealthResponse`（200） | liveness only |

`LegacyRuntimeHealthSnapshot` 只给 `secret_key_present`、callback token present、DB/
fixture/readiness 等布尔/枚举，不给 secret。`SystemHealthResponse` 的 components 固定
包含 wecom/release/runtime_units/database/migration/queues，并带
`pii_in_output`、`secrets_in_output`；这些是观察事实，不是启用开关。

### 4.4 release candidate plane（12 operations）

| operation | 方法 / URL | request DTO / path | success response（状态） |
| --- | --- | --- | --- |
| `listReleaseCandidates` | GET `/api/v1/admin/release-candidates?limit` | `ListReleaseCandidatesParams` | `ReleaseCandidateList`（200；400/401/403/503） |
| `registerReleaseCandidate` | POST `/api/v1/admin/release-candidates` | `RegisterReleaseCandidateRequest` | `ReleaseCandidate`（201；400/401/403/409/503） |
| `getReleaseCandidate` | GET `/api/v1/admin/release-candidates/{candidateId}` | path `candidateId` | `ReleaseCandidateDetail`（200；400/401/403/404/503） |
| `recordReleasePrerequisite` | POST `/api/v1/admin/release-candidates/{candidateId}/prerequisites` | path + `RecordReleasePrerequisiteRequest` | `ReleasePrerequisiteReceipt`（201；400/401/403/404/409/503） |
| `prepareReleaseCandidate` | POST `/api/v1/admin/release-candidates/{candidateId}/prepare` | path | `ReleaseCandidate`（200；400/401/403/404/409/503） |
| `startReleaseCutover` | POST `/api/v1/admin/release-candidates/{candidateId}/cutover/start` | path | `ReleaseWorkerLease`（200；400/401/403/404/409/503） |
| `restartReleaseCutover` | POST `/api/v1/admin/release-candidates/{candidateId}/cutover/restart` | path + `ReleaseWorkerCommand` | `ReleaseWorkerLease`（200；400/401/403/404/409/503） |
| `completeReleaseCutoverStep` | POST `/api/v1/admin/release-candidates/{candidateId}/cutover/steps/{step}/complete` | path `candidateId`,`step` + `ReleaseWorkerCommand` | `ReleaseCutoverProgress`（200；400/401/403/404/409/503） |
| `activateReleaseCandidate` | POST `/api/v1/admin/release-candidates/{candidateId}/activate` | path + `ReleaseWorkerCommand` | `ReleaseCandidate`（200；400/401/403/404/409/503） |
| `recordReleaseRollbackCheck` | POST `/api/v1/admin/release-candidates/{candidateId}/rollback-checks` | path + `RecordReleaseRollbackCheckRequest` | `ReleaseRollbackCheck`（201；400/401/403/404/409/503） |
| `requestReleaseRollback` | POST `/api/v1/admin/release-candidates/{candidateId}/rollback/request` | path | `ReleaseCandidate`（200；400/401/403/404/409/503） |
| `completeReleaseRollback` | POST `/api/v1/admin/release-candidates/{candidateId}/rollback/complete` | path | `ReleaseCandidate`（200；400/401/403/404/409/503） |

Release DTO 只应携带 commit/artifact/manifest/config digest、prerequisite evidence、
generation/fence、固定步骤和 rollback reconciliation。`activate`、`cutover`、`switch`、
`rollback` 是本地 journal/evidence 状态，不授权 deploy、traffic switch、backup restore
或 Provider 写入；跨域 prerequisite 通过稳定 port/版本化事件提供事实。

## 5. Journey 闭包与状态语义

### 5.1 Config list/detail/save/check

1. donor controller 从 `cat` 选当前 category；`readAdminRows()` 在 `config`/
   `configDetail` 分支会读取 categories、app-settings、push-capabilities、releases，
   并映射到一个 mixed `ConfigCategory[]`。
2. list template 显示 `status`、toggle 和 open；open 通过 `location.href =
   "configDetail.html?cat=" + key` 离开当前页面。
3. detail 收集所有 `[data-cfg]` input 和 switch，调用 `saveConfigCategory`；读写 adapter
   必须只允许闭集非敏感 key，先验证 CSRF/action proof/幂等，再在一个 UoW 中写 setting、
   audit、event，返回不含 old/new secret value 的 receipt。
4. check 只能产生本地诊断，不触发 Provider；保存成功的用户可见判断必须以 receipt 和
   再读 projection 为边界，而不是 HTTP 200 或 toast。

donor `MockApi.saveConfigCategory` 的测试实现会在非空 secret 时写入本地 mock value；
这不是生产契约，不能从广域 `shared/api/client.ts` 带入 v3。production `HttpApi` 的
app-settings mapper 会跳过 secret field，并要求 route-bound action token；新 adapter
必须继续 fail closed。

### 5.2 Setup wizard

GET projection → 编辑两键 → POST 带 expected digest、action proof 和 Idempotency-Key →
同一 UoW 锁两键并产出两 audit/两 `setting.updated` event → 检查 local boundary → GET
读回。非空 secret、digest 冲突、回执数量不对、readback 不一致或
`runtime_applied=true` 都必须进入错误态；不能展示“已接入企微”或“已应用运行时”。

### 5.3 AdminOps safe / job observation

category、push capability、release、check 都只能返回 `AdminOpsLocalOKResponse` 的
本地投影；`AdminOpsLocalAcceptedResponse` 仅表示本地计划被接收。任何
`queued/running/completed` 都不能被页面文案升格成外部发送成功。broadcast、Feishu、
archive-sync、callback、message-batch、delivery 和 order-identity 分支保持
archive-only/unmounted，除非 Terra 逐项建立 owner、UoW、回执和 outbound 边界。

### 5.4 Release evidence

candidate 注册 → 精确主体 prerequisite receipt → readiness → prepare → generation/fence
cutover journal → 固定顺序步骤 → local activated fact。rollback 需先记录 schema/data/
outbound reconciliation check，再 request/complete local journal；没有任何 Provider、
部署或流量切换副作用。

### 5.5 Diagnostics / health / API docs

diagnostics 只读有界、去敏后的 snapshot/timeline/check；query、details、graph depth、
PII 和 secret 必须由 adapter 截断/隐藏。API docs donor 页面是静态 showcase，按钮当前无
行为；不能把它当 authoritative OpenAPI，也不能将其列出的 sidebar profile/external
identity routes 归入 PR09。

## 6. Secret/config 安全边界

### 6.1 允许的配置投影

生成 schema 的 app-settings metadata 中出现以下 key：

`wecom.corp_id`、`wecom.agent_id`、`outbound.rate_per_second`、
`outbound.max_attempts`、`database.url`、`wecom.secret`、`wecom.callback_token`、
`wecom.callback_aes_key`、`ai.api_key`、`auth.jwt_secret`、
`extension.api_key_pepper`、`gateway.webhook_master_key`。

其中敏感/连接类 key 只能返回 `masked/configured`。`LegacyAppSettingsSaveForm` 对
`database.url`、WeCom secret/callback/AES、AI key、JWT、pepper、webhook master key 均是
`@maxLength 0`；`SetupWizardSaveRequest` 对四个敏感键也是 `@maxLength 0`。schema 约束
不是唯一防线，后端还必须在解析 body 后、进入事务前执行 `secret_input_forbidden`。

### 6.2 action proof、CSRF、actor、幂等

- `admin_action_token` 是 43 字符 route-bound token；不能进日志、URL、DOM 文案或审计
  message。form 还要求 43 字符 CSRF token；JSON transport 从 cookie 取 CSRF，设置
  `X-CSRF-Token` 并使用 `credentials: include`。
- `operator` 字段被忽略；actor 必须由 authenticated Principal 派生。不要相信 HTTP
  body 自报 operator。
- Config/setup/AdminOps 的业务状态、audit、event、outbox、idempotency receipt 必须同一
  PostgreSQL UoW 原子提交；Provider 网络调用不能持有该事务。
- setup wizard 原样 UI 每次 submit 默认随机 UUID key，v3 adapter 必须把业务 command
  identity 与 transport key 分离，支持相同 payload replay、不同 payload conflict，不能
  用新 key 绕过冲突。
- `transport.ts` 的 `unwrapGenerated` 只有 2xx 返回 data；401/403/409/503 必须错误态，
  不能因为 fetch 完成或 response 有 body 就渲染成功。

### 6.3 AdminOps/release secret reference

AdminOps notification/release write DTO 只能接受形如 `secret://...` 或 `secretref:...`
的 opaque reference；read DTO 返回 `masked`/configured，不返回 locator 的可推断明文。
release changes read 的 `wecom.webhook_ref` 只能是 `"masked"`；write 需格式校验且不应
被页面回显。任何 raw secret、Cookie、OAuth code、private key、openid、external_userid
都不得进入结构化日志、toast、URL、audit/event payload。

## 7. 无 OneID / 无客户板块边界

本 PR09 前端没有 Customer 列表、客户详情、渠道身份解析、OneID Resolve、Provision 或
merge candidate 入口；没有 `customers.id`、openid、unionid、external_userid 的业务写入
或匹配。Config/AdminOps/Release adapter 不能 import OneID/Customer 表、app、store、http
或 worker。

必须显式防止以下误导：

- `health.schemas.ts` 是共享 generated schema，包含大量 `Customer*`、`customer_id`、
  history、sidebar 等声明；setup 只可 import 其两个 setup type，不能把整个文件变成页面
  DTO。
- `apidocs.html:L31` 的 `external_userid` 只是 donor 静态 API showcase；它不是 PR09
  identity evidence，不能由 API docs 页发起 OneID 解析或外部用户更新。
- `legacy.ts` 的 `deferred_identity_history`、`customer_state_history`、
  `wecom_contact_history` 等 query branch 必须不随 config 入口进入 v3；任何
  `customer_id` query 都直接拒绝/不挂载。
- `AdminOpsJobKind.order_identity_repair` 与 `AdminOpsJobTargetKind.order_identity`
  是混合 schema 邻接值；即使 generated enum 存在，也不得成为 route、按钮或默认 job。
- `AdminAccessMember.staff_wecom_userid` 属本地后台访问控制邻接 DTO；AdminAccess 由
  `internal/access` owner 管理，不等于客户/OneID，也不能借 setup button 进入本 PR。

## 8. PR10 单侧栏装配风险与适配规则

1. v2 `build.mjs` 的 `adminShell()` 是完整页面，不是可复用的 PR09 fragment；任何
   `dist/admin/config.html` 直接上线都会带 v2 `.shell/.side/.side-nav`、全量 nav 和用户区。
2. `tokens.css` 既是样式又是 donor shell 的布局依赖；将其连同 donor page 引入 v3 会
   出现两个 shell 或互相覆盖。PR10 只允许 v3-owned `admin_base` 和 primary sidebar。
3. Terra/Web 应把 donor `config.html`/`configDetail.html`/`apidocs.html` 的 body fragment
   挂到唯一 `#stage`，将 setup wizard 作为受控 modal hook；不可复制 `legacy.ts` 或
   `AdminController` 的 page router。
4. `.html` 静态入口与 `/admin/config`、`/admin/config/releases*` server pages 是两个
   契约面；适配时必须明确 route map，不能让同一路径同时落到 v2 static page 和 v3
   server page。
5. donor nav 的 `config`/`apidocs` 只是 adjacent labels；不能把 agents、ownerMig、
   sidebar profile、message/marketing/customer history 一并挂入 PR10 sidebar。
6. 业务文字、DOM、inline CSS、setup interaction 和 generated request bytes 由 archive
   freeze 保护；要隐藏 excluded operation，应在 v3 adapter/route policy 做 capability
   gate，不改 archive 文件本身。

## 9. 完成门禁与复核命令

### 9.1 已执行的只读门禁

```text
git -C /tmp/aicrm-v2-audit.yN3jmr rev-parse HEAD
=> 6bfbe5816bb89913c70adaca87d6a486260e016e

git -C /tmp/aicrm-v2-audit.yN3jmr status --porcelain=v1
=> (empty)

bash -n scripts/check-pr09-frontend-freeze.sh
=> (pass)

scripts/check-pr09-frontend-freeze.sh
=> PASS: ... (16 files; ... SHA-256 + cmp; no shell/sidebar or external assets)
```

脚本读取 manifest `frontend.exact_files`，从 donor git object 计算 source SHA，从
archive 文件计算 target SHA，并对每个文件执行 `git show <SHA>:<source> | cmp - target`。
它不会读取 donor 未提交内容，不会改 donor 或 v3 文件；临时目录只存排序后的 expected
file set，退出时清理。

等价的手工单文件证明（示例）：

```sh
# 任一 archive 文件都可用 donor git object 与目标执行 cmp：
git -C /tmp/aicrm-v2-audit.yN3jmr show \
  6bfbe5816bb89913c70adaca87d6a486260e016e:web/src/admin/templates/config.html \
  | cmp - web/donors/adminops-v2/src/admin/templates/config.html
```

### 9.2 Terra/Web 后续必须补齐的非本审计工作

- 以 v3-owned adapter 收窄 generated imports，并为每个要挂载的 operation 建立
  authenticated HTTP/OpenAPI、owner table、UoW、CAS、audit/idempotency 和 redacted DTO；
- 对 Config/setup 的 read-after-write、digest conflict、secret rejection、两审计/两事件
  receipt、random UI key replay 等做 journey/E2E；
- 对 AdminOps job/notification/release 逐项给出 mounted/unmounted decision，确认
  `accepted/queued/outcome_unknown/executed/reconciled` 不被 UI 混淆；
- 在 PR10 shell 上做唯一 sidebar 的浏览器 smoke test，确认无 `.side` duplicate、无
  hidden history query、无 Customer/OneID panel、无静态 API docs 外泄；
- 在真正需要外部写入时经 `outbound`/External Effects Port 和 provider receipt/reconcile
  闭环；本 archive 不能授权该效果。

本审计没有把目录存在、TypeScript 可解析、HTTP 200、Mock、queued 或 donor build 成功
当作完成标准；唯一已闭环的是 donor 前端文件归档、入口/依赖/URL/DTO/Journey 的边界
审计，以及 SHA-256 + `cmp` 的字节证明。
