# 话术与素材编辑器：共享编辑、预览和只读呈现

## 目标与边界

为管理端内容编排提供一套 V3 共享组件：文字话术、受调用方允许的变量、素材选择与排序，以及同一内容包的编辑预览和保存后只读呈现。它先在**群运营计划**节点编辑中真实接入；自动化话术、运营闭环和渠道欢迎语分别以各自业务合同接入，不能把相邻页面当作同一能力。

| 判断 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及。编辑器不读取、解析、创建或关联客户身份。允许变量是调用方声明的展示令牌，不是身份解析。 |
| 持久化 | 编辑器不持久化。它只维护会话草稿；调用方的 `onCommit` 才把确认值交给既有保存路径。 |
| 外部效果 | 不涉及。编辑器不上传、不发送、不执行 Provider 预览或校验；预览只在浏览器根据当前草稿和调用方已授权的素材记录渲染。它可以展示调用方提供的受控缩略图 URL，缩略图失败时回退为文本状态。调用方自己决定何时读取素材、保存或发送。 |

本专项不改冻结 donor，也不复制其业务逻辑。它不改变运营节点保存、自动化 Agent 保存/发布、运营周期运行或渠道保存的权限、版本、幂等与外部效果边界。

## 已核实的调用路径

| 页面 | 当前真实入口 | 当前写入边界 | 首批处理 |
| --- | --- | --- | --- |
| 群运营计划 | `web/v3/groupOpsStandard.js` 的 `openNodeContentComposer` | 确认后写 `node_content_package_json`，随后既有 `saveNode` 保存 | **首个真实接入**：替代对冻结 `AICRMSendContentComposer` 的依赖，保持节点命令不变。 |
| AI 助手草稿审阅 | 冻结 `cloud_plan_review.js` 的 `openTaskComposer`；`web/v3/aiAssistantAdapter.ts` 转换为接收人内容 PATCH | 调用方 `updateAIAssistantRecipientContent` | 后续独立 PR；V3 桥接只把确认内容交回既有 PATCH，绝不将编辑器视为发送。 |
| 自动化话术 | `/admin/automation-agents`，`RenderAutomation` + Agent 编辑页 | Agent 的固定内容与 Prompt 由 automation 领域保存/发布 | 后续独立 PR。固定话术内容和 Prompt 为两项独立字段；编辑固定内容不得修改 role/task Prompt，也不能把 Prompt 当发送文案。 |
| 运营闭环 | `/admin/operation-cycles`，`operationCyclesAdapter` / Excel Batches | 当前为策略、运行档案与复盘交互 | 后续独立 PR。在确认存在可编辑内容包及其领域保存合同前，不注入伪编辑器。 |
| 渠道欢迎语 | 当前发布链由 manifest 的 `channelAdmissionHost.ts` 桥接；不得在未加载的 `channelAdmissionStandard.js` 实现 | 调用方同步既有隐藏字段后保存渠道 | 已登记，后续独立接入；先以实际 Host/manifest 核验后再接入，不与群运营计划首批混合。 |

## 共享合同

### 内容包和素材记录

内容包保持现有字段：`content_text`、`image_library_ids`、`miniprogram_library_ids`、`attachment_library_ids`、`group_invite_library_ids`。这些引用都属于既有 Media 素材库，V3 组件以 `media-library + kind + id` 的稳定键管理选择，拒绝把别的来源中同号 ID 当作同一素材。内容包本身不夹带 UI 排序字段。

调用方必须显式传入：

- 场景标题、可用素材类型、每类和总数上限、是否允许文本；
- 当前内容包和 `selectedRecords`。已有但失效、无权限或暂时未知的素材须保留回显并标明原因；只有用户显式移除才会删除；`selectedRecords` 的跨类型顺序只能来自调用方已持久化的权威合同；
- 仅本场景授权的只读 `loadMaterials(query, cursor, signal)`，包括 scope/source；组件没有默认 URL、全局 `fetch`/`AdminApi` 劫持或跨页面目录；
- `onCommit(package, selectedRecords)`。取消、关闭、加载失败或仅预览均不得调用它。

共享选择会话复用已审核的 `SelectionSession → selectionDialog` 栈：搜索草稿和已提交 query 分离，加载按 epoch/abort 防旧响应覆盖；翻页只沿用已提交 query；取消/关闭使在途请求失效；只读状态保留已有草稿与不可用原因。多选、单选、上限、重新打开回显、显式移除由同一会话状态处理。

### 编辑、预览与只读

- 文本编辑器只接受调用方给出的实际令牌扫描规则和允许列表，不以内置 ASCII 双花括号推断发送能力。变量令牌携带稳定 key 和显示 label；未知、已禁用或不在场景允许列表的令牌在预览和只读态明确标为“变量当前不可用”，不伪造值。调用方可把这类提示设为阻止确认或仅保留历史原文。
- 添加、删除与获授权的排序都只更新本地草稿。确认后将有序素材记录和内容包一次性交回调用方。未声明持久顺序能力的场景不显示跨类型排序，预览与只读使用其既有稳定顺序。
- `renderContentPresentation` 是唯一内容呈现函数，编辑预览和保存后只读都调用它；它只生成安全 DOM/text，不使用原始 `innerHTML`，不调用保存、发送、上传或 Provider 预览。它仅可渲染调用方提供的受控缩略图 URL，并在图片加载失败时显示素材文字与失败状态。文本是否 trim、保留换行或使用其他规范化由调用方显式声明；共享层的兼容默认仅用于既有内容包，不能据此推断未来 Prompt 的保存规则。
- 预览明确标识“预览不会发送”。保存成功后调用方重新读取的内容以只读呈现；加载失败、无权限、未配置和未知素材必须分别显示，不能静默消失或显示成功提示。

### 具体场景能力

| 场景 | 文本 / 变量 | 素材 | 保留的领域规则 |
| --- | --- | --- | --- |
| 群运营计划节点 | 文本开启；当前领域没有变量解析或上下文白名单，所以不提供变量插入。历史令牌按原文显示，并提示当前场景未声明可用变量。匹配现有 `validText`：有效 UTF-8、无首尾空白、非空时最多 1000 rune；空文本仅在存在素材时可确认 | 图片、小程序、附件、群邀请；上限沿用现有节点合同；可排序 | 节点表单和 `saveNode` 仍负责版本、保存和执行。Host 将调用方已确认的有序 `selectedRecords` 投影到既有 `material_plan.references`；读回也从该权威顺序重建编辑预览与只读呈现，随后由 Media 快照和 outbound 序列校验保留。 |
| AI 助手草稿审阅 | 文本开启；不能把审阅页当成 Agent Prompt 编辑器 | 沿用接收人消息内容允许范围 | PATCH 仍由 AI Assistant adapter 调用；不可编辑状态不开放确认。 |
| 自动化固定话术 | 仅 `fixed_script` 允许固定 `ContentText`；`agent` 类型必须为空，且 generation 当前拒绝 `{{` / `}}`。role/task Prompt 与固定内容独立分区，按既有字段和发布规则保存 | 图片最多 3、小程序 1、附件 9、群邀请 1、总数 9；`DynamicMiniprogramCard` 虽出现在 Port 但当前 normalize 明确拒绝，不开放 UI | 编辑固定内容绝不清空 draft/published role/task Prompt 或 `LegacyConfiguration`；Agent 类型、保存、发布和执行开关仍由 automation 领域。 |
| 运营闭环 | 待领域提供可编辑内容包后决定 | 待确认 | 不把策略/复盘说明误当发送话术。 |

## 参考与取舍

- 采用 [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) 的语义化状态和可预测反馈原则：正常、加载、无权限、失效和错误使用明确的语义标记与 tokens；不引入 Ant Design 或 React。
- 采用 [Tiptap Mention](https://github.com/ueberdosis/tiptap/blob/main/packages/extension-mention/src/mention.ts) 的“稳定标识与显示标签分离”理念，变量使用 key/label 令牌；不引入 Tiptap、ProseMirror 或编辑器依赖。
- 冻结 `send_content_composer.js` 只作现有视觉、字段与上限参考。**不采用**其 `preview`/`validate` POST、即时确认素材选择和静默失败降级，因为这些会越过本专项的纯呈现与调用方写入边界。群运营页只在 V3 宿主将其既有 `open` 调用适配到本组件，不改变冻结文件或把此桥接推广为其他页面的全局行为。

## 交付与验收

### PR A：共享合同与群运营计划

1. 引入 V3 内容编辑器、单一呈现函数和 SelectionSession/Dialog 适配，不加载或修改冻结 donor。
2. 将群运营计划的节点编辑接入。验证：多素材临时选择、显式删除、按既有 `material_plan.references` 排序、取消不写 hidden field、确认后仅更新节点草稿、重新打开回显、失效/无权限素材保留说明、变量与预览不触发请求。
3. 运行定向 Node/TypeScript、构建和冻结 renderer journey；用本地 PostgreSQL + 独立 Chromium 验收实际群运营页，截图 1280/1440 及 360/420 布局。构建与浏览器串行。

### PR B：AI 助手草稿审阅

适配冻结审阅页面的确认桥接和只读呈现，使确认仍走既有受控 PATCH；验证预览零 Provider 调用、保存失败保留草稿、不可编辑状态不提交。

### PR C：自动化话术与运营闭环

分别在真实 `/admin/automation-agents` 与 `/admin/operation-cycles` 发现并核对领域内容保存合同后接入。自动化固定话术与 Prompt 分离；运营闭环不在无内容包合同前虚构编辑能力。

每个 PR 只声明已实际接入的页面和状态，不把组件样例或相邻页面计为业务验收。不得合并、部署或发送外部内容；由 root 审核后再推进。
