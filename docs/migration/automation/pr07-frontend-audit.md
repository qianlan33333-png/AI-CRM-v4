# PR07 自动化智能体与固定话术：donor 前端冻结审计

## 审计结论

本审计以 v2 donor `6bfbe5816bb89913c70adaca87d6a486260e016e` 为唯一前端行为基线，以现有准备提交 `2f8849a39eb25e0a63c94bf80d70d78f4d01cd22` 为 v3 donor 快照基线。快照目录中 20 个文件逐文件 `SHA-256` 与 `cmp` 均一致；快照没有额外的页面、CSS、图片或字体文件。

PR07 的生产前端只能挂载两个 donor 页面：`agents`（自动化话术列表）和 `agentEdit`（Agent / 固定话术编辑）。PR10 已提供一级页面壳，因此 donor 的完整静态 shell、`.side`、`side-nav`、第二套导航和 donor 的页面文件路径不能进入 v3。应在 PR10 的单一 shell 中挂载 v3 自有 adapter，同时保持 donor 模板产物、字段、请求 URL、动作顺序和文案原样。

发现一个必须在正式集成前解决的装配决策：donor 同时存在“实际使用的 mini-runtime 模板 + `AdminController`”和“未被入口引用的独立 `automationAgents.ts`”两套不同 UI。它们的页签、操作按钮、发布能力、状态提示和导航路径不一致，不能拼接使用。下文把两套行为分别列出；在选择并冻结其中一套前，PR07 不能宣称前端闭环完成。

## 轴向分类（aicrm-v3-development）

```text
OneID: not involved
  Agent/fixed-script 是本地配置定义，不读取、解析、绑定或创建客户；人群包/受众/客户自动化入口全部排除。

Persistence: local transaction
  草稿、发布快照、固定内容引用、生命周期和幂等收据属于 Automation 自有 PostgreSQL 事务；业务状态、收据、审计和 Outbox（若调用方需要）必须由同一个 v3 UoW 原子提交。

Internal durable job: not involved in this frontend slice
  页面只读/编辑本地配置和执行前检查，不调度、不运行 Agent、不生成任务；不得因页面迁移新增队列、Worker 或 ticker。

External Effects: not involved
  页面不调用 Provider、企微、LLM、发送、重试或投递；precheck 的 real_external_call_executed 必须保持 false。未来若由后端提交效果意图，只能由 outbound/EER 接管，前端不直接调用。
```

固定内容页中的图片、小程序、PDF、群邀请字段只是配置引用。当前 donor schema 将四个数组声明为 `maxItems: 0`，准备阶段对非空引用 fail-closed；v3 adapter 应通过窄 Media Port 验证引用，不能读取 Media 表，也不能在本 PR 偷渡上传或 Provider 行为。

## Donor 身份与冻结证据

| 项目 | 值 |
| --- | --- |
| donor repository | `https://github.com/qianlan33333-png/AI-CRM-v2.git` |
| donor SHA | `6bfbe5816bb89913c70adaca87d6a486260e016e` |
| v3 prep commit | `2f8849a39eb25e0a63c94bf80d70d78f4d01cd22` |
| v3 branch | `codex/import-agents-audit`（审计 worktree） |
| snapshot root | `web/donors/automation-v2/src` |
| exact file count | 20 |
| SHA ledger | `docs/migration/automation/pr07-donor-sha256.txt` |

审计时 donor `HEAD` 与上述 SHA 一致，工作树和 index 无差异。可重复执行：

```sh
PR07_DONOR_DIR=/path/to/frozen/aicrm-v2 \
  bash scripts/check-pr07-frontend-freeze.sh
```

检查脚本会校验 donor SHA/洁净状态、manifest 文件列表、20 个源文件和快照文件的 `SHA-256`、`cmp`、ledger 记录、快照无增量文件，以及快照不包含第二套页面壳。任何 donor 漂移或快照字节变化都会失败，不会自动更新基线。

## 20 个逐字节冻结文件

目标路径统一为 `web/donors/automation-v2/src/${source#web/src/}`。行数和 SHA 来自当前 donor SHA，并由检查脚本再次计算；`cmp` 是最终字节边界，不以格式化、类型检查或语义等价代替。

| # | donor source | 行数 | SHA-256 | 用途/注意 |
| ---: | --- | ---: | --- | --- |
| 1 | `web/src/admin/templates/agents.html` | 47 | `e2d6324f84d81d4da7c254a35a40c560a5c157a63dc0a1beb9f79a5bd767f707` | 列表模板、创建入口、五个行操作 |
| 2 | `web/src/admin/templates/agentEdit.html` | 76 | `6606c40c0e2dce2c2b5367684cc0005cd40d2a7bf190f4bef8626974ba119435` | 编辑模板、四个页签、Prompt 和固定素材只读区 |
| 3 | `web/src/admin/sections/automationAgents.ts` | 143 | `b208e938eb8ae144d9831bcb145b2cce6eac4873bfe257e40f439e2ec1e3c5e4` | 未接入入口的独立动态实现，见双实现风险 |
| 4 | `web/src/admin/sections/util.ts` | 48 | `75e3f2b24bc5e031382f7e5c58ddf64578eb7708b06d30467edaf80464362621` | donor 页面所需转义/显示小工具 |
| 5 | `web/src/admin/controller.ts` | 4169 | `2c0d51283902b370c431dd04124bcc2215214eac314099fa5d6001ccdb038500` | 实际模板页控制器；同时含大量非 PR07 分支，不能整文件接入 |
| 6 | `web/src/admin/main.ts` | 16 | `61bc0ef4ff883bb243af79f989813bbe29c3109168544f32d7358a7608514161` | 根据 `data-page` 选择 `legacy`，不是 PR10 壳 |
| 7 | `web/src/admin/nav.json` | 128 | `ee7a9a6629dcdaae4d9792ffcd757cee850bad796edcfb7ff68b6028206f1ed1` | 仅 `agents` 的 robot inline SVG/标签/分组可作证据 |
| 8 | `web/src/admin/registry.json` | 387 | `df5f131d9b322e435a09fccdc89c4f8269f3ef03f7856ece250d412af71bb145` | 仅 `agents`/`agentEdit` 元数据可作证据 |
| 9 | `web/src/shared/api/client.ts` | 1130 | `2e1bfde0d36f6ab6637da66fddf6b7ee94984364a7175ad787b1da80f98695d5` | AdminApi agent 方法；Mock/sessionStorage 不能进生产 |
| 10 | `web/src/shared/api/types.ts` | 1089 | `6fea805d568cf91b7c43292128c2a2b0694cf6515d85c264e048726a270c5a20` | Agent/Rows 类型；文件含其他领域类型 |
| 11 | `web/src/shared/api/mockData.ts` | 704 | `d202111695e91432879fb16a3101eae6b7f10ba53237dd493989ffd284c8264c` | donor 测试种子；不得作为 v3 真实数据 |
| 12 | `web/src/shared/ui/picker.ts` | 388 | `690639bde2fb605024a05fe3196f2ddf8fd5b4ae87c76ef3ff5868a7adf912c0` | 共享选择器快照；PR07 页面不启用受众选择 |
| 13 | `web/src/shared/ui/feedback.ts` | 183 | `5c16cd3b057663d2b0c5d2a01416e6330ec979513c6754f1f64f6e41f364a546` | toast/confirm 行为 |
| 14 | `web/src/shared/ui/download.ts` | 14 | `dda7c727bbad4844749e59095dba4c450cf864efb2caaad3f96209f24348a7bb` | 共享下载小工具；PR07 没有下载控件 |
| 15 | `web/src/shared/ui/runtime.ts` | 183 | `1122c0be280b1f62c1784510459471bd3ffcc6989493f103daf811900411e66a` | `<sc-for>`/`<sc-if>` mini-runtime |
| 16 | `web/src/shared/ui/tokens.css` | 126 | `0f9b719686a8516727ad86fa9475b10cbb059fd10003b3eb6ef041900c7ee3b0` | 通用 token；没有 Agent 专属 CSS |
| 17 | `web/src/api/admin.ts` | 2130 | `574293ff7ab6fb0c6d1227ff879649dbc05cf454caaaec6a0fbc1d23727df9ee` | DTO/聚合读取；包含 audience/customer 等广泛分支 |
| 18 | `web/src/api/transport.ts` | 69 | `fc5e4b447d10487f571fdafd953cb51756274bc40b019bb51b6cdd61cfbad885` | donor authenticated transport/CSRF 封装 |
| 19 | `web/src/api/generated/p4-automation-agents/p4-automation-agents.ts` | 947 | `f2992201652a0857614e23358d588a3a39d0237274172ab21715132318e4ac03` | 12 个页面/API operation 生成物；不能手改 |
| 20 | `web/src/api/generated/health.schemas.ts` | 25892 | `7f1bc1d05b3e012de46b1d53ef7b56319c0bc032a1c0389fa3fd138c7218b40d` | `LegacyAutomationAgent*` DTO/schema；其余 schema 不属于 PR07 |

以上 20 个文件在快照中各有且仅有一个对应文件。快照不是 v3 build 输入，后续 adapter 只能放在快照目录之外；对快照文件做格式化、改路径、改文案、加按钮、删字段都会使审计失败。

## 页面行为与 DOM 合同

### A. 实际 legacy 模板页（建议作为默认冻结行为）

证据链是 donor `web/scripts/build.mjs` → 生成 `<body data-page="agents|agentEdit">`、`<template id="tpl">` 和 donor shell → `web/src/admin/main.ts`（非 customers 时动态加载 `./legacy`）→ `web/src/admin/legacy.ts`（`agents`/`agentEdit` 没有独立 case，落到 `new AdminController(api, page)`）→ `mount(stage, tpl.innerHTML, controller)`。

`legacy.ts` 与 `build.mjs` 都不在 20 个快照文件中，因为它们带来完整 v2 shell 和大量客户/受众/历史页面。v3 必须实现窄 adapter，保留本页 mini-runtime 的输出和控制器语义，不把整个 legacy bootstrap 搬进来。

#### `agents.html` 列表

| DOM/行为 | donor 合同 |
| --- | --- |
| 页面标题 | 面包屑“客户管理后台 / 配置及后台”，标题“自动化话术” |
| 创建 | `data-agent-create="agent"` / `agentsPage.createAgent` → `agentEdit?type=agent`；`data-agent-create="fixed_script"` / `createFixedScript` → `agentEdit?type=fixed_script` |
| 数据源 | `<sc-for list="{{ rows.agents }}" as="r" hint-placeholder-count="3">`；列表列为自动化名称、自动化类型、固定素材、状态、操作 |
| 行字段 | `r.name`、`r.code`、`r.type` + `r.typeCs`、`r.material` + `r.matCs`、`r.status` + `r.cs` |
| 行动作 | `data-agent-action="edit|copy|precheck|pause|archive"`，顺序固定为编辑、复制、校验、暂停、归档；没有激活按钮 |
| 反馈 | copy/pause/archive 成功后刷新 `controller.init()`；precheck 只 toast 诊断结果，不执行启用 |
| 空态 | 由 mini-runtime 的 donor `<sc-for>` 占位行为决定；不能换成自定义列表/分页组件 |

控制器 `renderVals` 将每行动作绑定到 `goto('agentEdit', '?id=...')`、`copyAutomationAgent`、`precheckAutomationAgent`、`pauseAutomationAgent`、`archiveAutomationAgent`。ID 必须是正的 safe integer；非法 ID 在发请求前 toast 并停止。

#### `agentEdit.html` 编辑

| DOM/行为 | donor 合同 |
| --- | --- |
| 顶栏 | 返回自动化列表、`data-agent-precheck` 校验、`data-agent-save` 保存；左侧为 inline SVG 返回箭头 |
| 摘要卡 | 自动化类型、状态、固定素材、编码四张卡 |
| 页签 | `ago.1..4` / `an.1..4` / `ap.1..4`：基本信息、当前绑定人群包、Prompt 配置、固定素材；`astep` 控制当前页签和标题 `aTitle` |
| 基本信息 | `#agentName` 名称；`#agentCode` 编码（已有记录 readonly，新建可写）；`#agentType` 为 `agent`/`fixed_script`；状态为只读显示 |
| Prompt | `#agentRolePrompt` 对应 `role_prompt`；`#agentTaskPrompt` 对应 `task_prompt`；保存时读取并 trim 输入，按 donor DTO 名称提交 |
| 人群包 | 只显示 `agentEditItem.bound` 和“绑定关系请在人群包详情的「自动化绑定」中管理。”；本页不得增加绑定、挑选、解除或客户选择控件 |
| 固定素材 | `data-agent-materials-readonly`；固定话术正文只读；图片/小程序/PDF/客户群 ID 只读；警告明确普通保存不发 fixed-content 请求 |
| 保存 | 新建 POST、已有 PATCH；成功后回到 `agentEdit?id=...`；不运行、不发送、不调用 Provider |
| 校验 | 已保存记录才 GET precheck；未保存时不请求；显示配置、物料、执行、可激活和真实外部调用结果 |

活动 generic 页 `AdminApi` 只暴露 `saveAutomationAgent`、`copyAutomationAgent`、`pauseAutomationAgent`、`archiveAutomationAgent`、`precheckAutomationAgent`。它没有 publish 或 fixed-content 写方法；编辑页固定素材明确只读。因此不能自行在该页加“发布”“上传素材”按钮来补足想象中的功能，这会违反 donor 100% 原样。草稿/发布版本和固定内容的持久化字段仍需由后端实现并在详情 DTO 中正确返回；是否有可见发布入口必须先完成下面的双实现决策。

### B. 未接入入口的独立 `automationAgents.ts`

`web/src/admin/sections/automationAgents.ts` 导出了 `mountAutomationAgents(root, page)`，但在 donor `main.ts`/`legacy.ts` 中搜索不到 import 或调用；不能把“导出函数存在”当成活动 v2 页面证据。

该实现与实际模板页不等价：

| 维度 | generic 模板 + controller | 独立 `automationAgents.ts` |
| --- | --- | --- |
| 列表列 | 名称、类型、固定素材、状态、操作 | 另加更新时间 |
| 列表动作 | 编辑、复制、校验、暂停、归档 | 编辑、启用前检查、复制、归档；没有暂停 |
| 编辑页签 | 4 个，含只读“绑定人群包” | 3 个：基本信息、Prompt、固定素材 |
| 发布 | active 页面没有发布按钮/调用 | 已有记录显示“发布当前草稿”，POST `/publish` |
| 固定内容 | 只读且不发请求 | 固定文本 disabled、素材只读，不发 fixed-content 请求；另有 legacy JSON 编辑 |
| 导航 | `goto('agentEdit', ...)`，由 AdminController 处理 | 硬编码相对路径 `agents.html` / `agentEdit.html` |
| 反馈/提示 | toast/confirm，通过共享 controller | 自建 `data-agent-result` 状态栏和动态 `innerHTML` |

建议默认选择 A，因为 build/legacy 装配证明它才是 donor 注册页面的实际行为；B 保留为 donor characterization evidence，不得半接入。若产品明确选择 B，则必须把 B 作为完整冻结页面重新记录并做独立 DOM/API replay，不能从 A 借页签/按钮/文案，也不能同时暴露两个入口。当前 prep manifest 中把“publish”列入页面 allowlist，但 A 的实际 UI 不提供 publish，这是正式集成前的文档/实现缺口。

## 请求 URL、方法与 DTO 合同

以下是生成文件 `p4-automation-agents.ts` 的全部 12 个 operation（页面 handoff 另列）。URL、HTTP 方法、字段名和错误语义必须保持 donor；v3 只可在后端增加兼容 adapter。写操作带 donor transport 的 `credentials: include`、CSRF 和 `Idempotency-Key`，不允许用 HTTP 200/排队成功伪造完成。

| donor operation | 方法与 URL | A active UI | B 独立模块 | PR07 处理 |
| --- | --- | ---: | ---: | --- |
| page handoff | `GET /admin/automation-agents` | route | route | v3 shell adapter；不复制 donor shell |
| list | `GET /api/admin/automation-agents` | ✓ | ✓ | 本地摘要列表；页面无 customer/audience filter |
| create | `POST /api/admin/automation-agents` | ✓ | ✓ | 本地 paused 配置 |
| detail | `GET /api/admin/automation-agents/{agentId}` | ✓（`agentEdit`） | ✓（编辑） | 本地详情、Prompt、版本、固定内容 |
| update | `PATCH /api/admin/automation-agents/{agentId}` | ✓ | ✓ | 本地草稿；code 不可变 |
| archive | `DELETE /api/admin/automation-agents/{agentId}` | ✓ | ✓ | 本地归档、幂等 |
| fixed content | `PUT /api/admin/automation-agents/{agentId}/fixed-content` | ✗ | ✗ | 后端保留合同；当前 donor UI 不发送，媒体非空 fail-closed |
| precheck | `GET /api/admin/automation-agents/{agentId}/precheck` | ✓ | ✓ | 只读诊断；`real_external_call_executed=false` |
| activate | `POST /api/admin/automation-agents/{agentId}/activate` | ✗ | ✗ | 生成形状仅 characterization；必须拒绝，不得提供成功 2xx |
| copy | `POST /api/admin/automation-agents/{agentId}/copy` | ✓ | ✓ | 本地复制；无执行/绑定/provider effect |
| pause | `POST /api/admin/automation-agents/{agentId}/pause` | ✓ | ✗ | 本地暂停；无执行 |
| publish | `POST /api/admin/automation-agents/{agentId}/publish` | ✗ | ✓ | 本地发布快照；是否可见取决于双实现决策 |

DTO 关键字段：

- 列表项：`id`（正整数）、`automation_type`（`agent`/`fixed_script`）、`agent_code`（小写 ASCII `[a-z0-9_-]+`）、`agent_name`、`fixed_material_summary`、`status`、`execution_enabled`、`materials_configured`、`updated_at`。准备阶段可见状态仅 `paused`，`active` 不能写入。
- 详情：列表字段再加 `automation_type_label`、`draft_role_prompt`、`draft_task_prompt`、`published_role_prompt`、`published_task_prompt`、`draft_version`、`published_version`、`has_unpublished_changes`、`fixed_content_package`、`fixed_content_package_preview`、`legacy_configuration`。
- 新建：`agent_name`、`agent_code`、`automation_type`、`role_prompt`、`task_prompt`；`status` 默认/限制为 `paused`。更新：`agent_name`、`automation_type`、`role_prompt`、`task_prompt`，至少一个字段，编码不可更新。详情/写响应包在 `agent` 下。
- 固定内容：`content_text` 最多 4000 rune；`image_library_ids`、`miniprogram_library_ids`、`attachment_library_ids`、`group_invite_library_ids` 按 donor schema `maxItems: 0`；动态小程序卡片字段作为 opaque contract 保存但准备阶段非空拒绝。
- Prompt：草稿和发布快照各最多 20000 rune，不能用 HTTP body 自报发布或 verified 身份；发布只复制快照和版本，不执行 Agent。
- precheck：`agent_id`、`configuration_ready`、`materials_configured`、`execution_enabled`、`can_activate`、`reasons`、`real_external_call_executed=false`；常见原因是 `prompt_unconfigured`、`material_unconfigured`、`execution_disabled`。
- 错误：保留 donor `invalid_agent_payload`、`agent_not_found`、`automation_agent_conflict`、`automation_execution_disabled`、`authentication_required`、`permission_denied`、`automation_agent_unavailable`、`webhook_configuration_retired` 等响应码/错误结构；不能把失败映射成成功 toast。

已发现的契约细节：后端/OpenAPI 允许 `automation_type` list query filter，但生成的 `listLegacyAutomationAgents(options?)` 没有 query 参数序列化，当前 donor 页面也不传 filter。v3 可兼容读取 query，但不要为此改前端。`api/admin.ts` 里的 `automationAgentMutationOptions` 为写请求生成 donor 风格 idempotency key；v3 后端仍需按稳定业务命令实现重放/冲突，而不能依赖随机 key 实现业务幂等。

## 相关文件全量审计与排除

### 实际生产页面装配依赖（不是可整目录复制清单）

| donor 路径 | 结论 |
| --- | --- |
| `web/src/admin/legacy.ts` | 必须阅读以确认 A 的活动装配，但整文件排除；它导入客户、问卷、受众、历史等大量页面。实现窄 adapter。 |
| `web/scripts/build.mjs` | 仅作 build/壳证据；它会生成 `<aside class="side">`、完整 nav 和静态 `agents.html`/`agentEdit.html`，不能作为 v3 shell。 |
| `web/src/admin/nav.json` | 仅保留 `agents` 条目（robot inline SVG、自动化话术、配置及后台）作为 metadata；不能复制全侧边栏。 |
| `web/src/admin/registry.json` | 仅保留 `agents`（一级）与 `agentEdit`（二级）记录；不能复制全 registry。 |
| `web/src/admin/templates/agents.html`, `agentEdit.html` | 20 文件中唯一的两个生产模板，字节冻结。 |
| `web/src/admin/sections/automationAgents.ts` | 20 文件中保留作 B 的 donor evidence；未接入事实必须保留。 |
| `web/src/admin/controller.ts`, `web/src/api/admin.ts`, `web/src/shared/api/client.ts` | 20 文件中保留字节证据；正式 build 只能通过窄 adapter 选择 agent 方法，不能让 customer/audience/provider 分支随 bundle 进入。 |
| `web/src/shared/ui/runtime.ts`, `feedback.ts`, `tokens.css` 等 | 仅复用 donor 所需 mini-runtime、反馈和通用 token；不能自行重写 CSS 或交互。 |
| `web/src/api/generated/p4-automation-agents/p4-automation-agents.ts`, `health.schemas.ts` | 生成契约快照；不得手工改生成物，v3 通过后端兼容 DTO。 |

### donor 中名字相近但必须排除的文件

下列文件是通过 donor `git ls-tree` 对自动化/Agent/话术关键词的完整搜索得到的候选，但不属于 PR07 Agent/fixed-script 配置页面：

| 路径 | 排除理由 |
| --- | --- |
| `web/src/admin/templates/automation.html` | AI 自动化运营/人群包、受众包、群发和客户选择；属于 customer automation/audience。 |
| `web/src/admin/sections/automationHistory.ts` | V1 自动化历史只读；本轮排除 history/import。 |
| `web/src/api/automationHistory.ts`, `web/src/api/automationHistory.test.ts` | 历史读取和来源身份/摘要合同；不属于当前 Agent 配置页面。 |
| `web/src/api/generated/p4-automation-compat/p4-automation-compat.ts` | Automation trigger/agent run 回执；属于运行时/客户自动化效果，不是配置页。 |
| `web/src/api/generated/p4-automation-runtime/p4-automation-runtime.ts` | 规则、触发、执行和 reconcile；明确排除 customer automation/runtime。 |
| `web/scripts/automation-history-e2e.mjs` | 历史页面 E2E；不能随 PR07 页面上线。 |
| `web/scripts/automation-agents-e2e.mjs` | 相关 donor characterization test，作为测试证据读取；应由 Terra 在 v3 后端/PR10 壳上适配，不复制 donor build 或 Mock 生产逻辑。 |

关键词搜索还发现以下 CSS 文件，但没有 Agent/fixed-script 专属 CSS/asset：`web/src/admin/sections/labs.css`、`questionnaireEditor.css`、`questionnaireEditorStyles.css`、`wecomTagPicker.css`、`web/src/sidebar/sidebar.css` 均属其他领域或完整 donor/sidebar；只有通用 `web/src/shared/ui/tokens.css` 在 20 个快照中。Agent 图标和编辑返回箭头都是 HTML/JSON 内联 SVG，没有外部图片、SVG、字体或 favicon 依赖。

## 明确边界

### 本 PR07 页面/后端闭环应包含

- Agent 与 fixed_script 本地定义、名称、不可变编码、类型和 paused/archived 生命周期。
- 角色/任务 Prompt 草稿、版本号、发布快照和 `has_unpublished_changes` 详情字段；发布若开放，必须是本地快照操作，不得触发执行。
- 固定话术正文及固定内容包引用的读取/校验语义；当前 donor UI 只读，非空媒体引用准备阶段 fail-closed。
- 列表、详情、创建、编辑、复制、暂停、归档、precheck 的真实 HTTP + PostgreSQL + 权限 + CSRF + 幂等 + 审计闭环；不能用 Mock、sessionStorage、假 200 或只排队。
- PR10 单 shell 下 1440×900、1280×800 的 donor DOM/文案/交互 replay；没有第二侧边栏。

### 明确不在 PR07

- Customer、OneID、Segment/Audience、人群包绑定、受众包、Campaign、recipient selection，以及 `agentEdit` 绑定页签中的任何写控件。
- Agent 激活、运行、生成、审批、触发、调度、内部执行 Worker、历史/import。
- outbound、企微、Provider、LLM、token、credential、发送、投递、重试和 delivery receipt。
- 固定素材上传、生成、预览外呼；本 PR 只保留 donor 返回的本地引用字段。
- donor `legacy.ts`/`build.mjs` 全壳、完整 `nav.json`、完整 registry、未相关页面和任何第二套 `.side`。

## 集成缺口与 adapter 要求

1. **双实现必须先裁决。** 默认冻结 A（实际 generic route），将 B 标为未接入 donor evidence；若选择 B，需要重新冻结 B 的 DOM/API journey。绝不能把 A 的四页签与 B 的发布按钮混在一起。
2. **窄装配而不是整文件 import。** `controller.ts`、`api/admin.ts`、`client.ts`、`types.ts`、`health.schemas.ts` 都是混合大文件。v3 adapter 只能选择 Agent 相关 handler/DTO；编译产物不得加载 customer/OneID/audience/provider 分支。冻结文件本身不改，适配写在快照目录外。
3. **静态壳要由 PR10 提供。** 不能生成 donor `agents.html` 作为另一 HTML 壳，也不能把 donor `.side`/全量 `navHtml` 再放进 stage。页面相对导航需要映射到 v3 shell 路由，但用户可见布局、文字、动作顺序不变；API URL/DTO 不能因 shell 改造而改名。
4. **发布/固定内容字段与当前 UI 不一致。** 后端必须能保存/返回 draft/published/fixed-content 合同，然而 A 的 donor UI 没有 publish/fixed-content 写入；不能为了“补齐闭环”新增前端按钮。应在产品决策中明确可见入口，再做一次完整 donor 选择，不得猜测。
5. **audience 残留必须只读/不暴露。** A 的第四页签之外还保留“绑定人群包”文字，`controller.ts` 的 `readAdminRows` 也把 `audienceEdit` 放在同一 `needs` 条件。v3 adapter 必须删除 audience 分支的运行时暴露，保留 donor 的只读显示文案时不得带任何绑定 API。
6. **安全边界要在后端重建。** 管理页面必须经过 v3 Session/权限/CSRF 门禁；没有 verified 客户身份输入；固定引用通过 Media Port；所有本地 mutation 与收据/审计在同一 UoW。`activate` 必须 fail-closed，不能因 donor 生成函数存在而提供 2xx。

## 验证矩阵与完成门槛

### 本审计已完成

- donor HEAD/clean tree 与冻结 SHA 校验。
- exact manifest 20 项枚举，目标目录 20/20 一一对应。
- 每项 source/target SHA-256 与 `cmp -s`；ledger source/target 记录校验。
- donor 关键词候选扫描，确认无 Agent 专属 CSS/图片/SVG/字体 asset。
- active legacy 装配链、B 独立实现和二者差异核对。
- 页面 DOM、输入 ID、事件顺序、请求 URL/方法/DTO/错误边界记录。

### Terra/Web 正式集成前必须完成

- [ ] 裁决 A/B 单一页面行为，并在 PR 描述记录 donor 选择；不混用实现。
- [ ] v3 PR10 shell 下编译/路由装配，证明没有 donor 第二 `.side`、目录浏览或额外导航。
- [ ] `bash scripts/check-pr07-frontend-freeze.sh` 在 clean donor 和 clean release 上通过；CI 对 20 项逐字节门禁。
- [ ] donor `automation-agents-e2e.mjs` 改为 v3 Session/真实 PostgreSQL/确定性后端 adapter 的 DOM/API replay；不把 Mock/sessionStorage 当生产后端。
- [ ] typecheck、API contract、OpenAPI 生成两次无 diff；生成 DTO/错误响应与 donor 字段完全一致。
- [ ] 未登录、无权限、CSRF、幂等键重复/改 payload、code 不可变、并发 CAS、归档隐藏、刷新后持久化和审计 Journey。
- [ ] Agent 与 fixed_script 创建/编辑；Prompt 草稿 → 发布快照（若选择的 donor UI 有发布入口）；固定内容只读/引用 fail-closed；precheck 不产生外部调用。
- [ ] 1440×900 与 1280×800 的列表/空态/编辑/四页签截图，与选定 donor 页面逐项对比；禁止自主视觉发挥。
- [ ] `activate`、audience binding、customer/OneID、Provider/LLM、上传/发送/重试均有明确“不暴露/不调用”断言。

## 结论与交接

本分支只新增本审计文档和检查脚本，没有集成 PR07、没有修改 donor、没有修改主工作树或中央文件。可交接给 Terra 的最小输入是：冻结 SHA、20 个快照、A/B 装配风险、单一页面决策、以及脚本通过结果。正式 PR 必须同时交付后端真实闭环和 donor 前端原样产物；仅把 20 个文件复制到 v3 或仅挂出占位页面都不满足完成标准。
