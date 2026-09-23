# 自动化固定话术：编辑、素材选择与保存后只读回显

- 状态：实施中
- 目标页面：管理端 `/admin/agentEdit.html?id={agent_id}`，从 `/admin/automation-agents` 进入。
- 复用：当前主线已接入的 `SelectionSession`、`SelectionDialog`、`ContentComposer`、`ContentPresentation` 与素材选择器。
- 首批范围：已创建或刚创建并取得 ID 的 `fixed_script` Agent。欢迎语、AI 助手、公共页、企微侧边栏和动态 Agent 内容不在本 PR 范围。

## 1. 业务判断和事实边界

| 维度 | 结论 | 原因 |
| --- | --- | --- |
| OneID | 不涉及 | 编辑的是 Automation Owner 的 Agent 配置；本页不读取、解析、关联或创建客户身份。 |
| 持久化 | 既有 Automation 本地事务 | `SaveFixedContent` 锁定 Agent、验证 Media 引用、更新固定内容并按既有规则增加 `DraftVersion`；V3 组件只保存浏览器草稿。 |
| 内部任务 | 不新增 | 不创建队列、Worker 或定时任务。 |
| Provider / 外部效果 | 不涉及 | 编辑、预览、素材目录读取和服务端读回都不上传、不生成、不排队、不发送，也不调用 Provider。实际运行、发布和外部效果仍由既有 Automation/Outbound 领域负责。 |

固定话术不是 Prompt，也不是即时发送内容。`fixed_script` 的固定内容由 `content_text` 与四类 Media 引用组成；角色和任务 Prompt 是 Agent 的独立领域字段。该专项不会改 Prompt、`LegacyConfiguration`、发布、启停、Agent 类型或任何执行规则。

## 2. 已核实页面与保存链

真实链路为：

`/admin/agentEdit.html?id=` → `automation.UIBinding` → `RenderAutomation` → `admin_base` manifest assets → 冻结运行时的 `[data-agent-materials-readonly]` 素材只读容器 → V3 `automationContentHost`。

静态 `agentEdit.html` 只是 donor 模板证据；实际页面由冻结运行时重绘。现有普通表单只保存 Agent 基本信息、Prompt 与 `LegacyConfiguration`，不能当作固定内容写入口。V3 Host 只在其运行时 `[data-agent-materials-readonly]` 已出现且 URL 具有合法 ID 后装配；不会修改冻结文件或复制整页。

固定内容唯一写入路径是：

`PUT /api/admin/automation-agents/{id}/fixed-content` → `Automation AgentService.SaveFixedContent` → 同一 Owner 事务中的 Media 引用校验、版本更新与既有事件 → `GET /api/admin/automation-agents/{id}` 服务端读回。

连续确认使用每次一次既有命令幂等键；按钮 pending 时单飞。失败不自动重试、不自动暂停 Agent，也不假报成功。成功后必须以 GET 的返回内容重新构建只读区，不能以本地草稿冒充已保存。若 PUT 已 2xx 而 GET 失败，页面明确显示“已保存，暂时无法刷新当前展示”，只提供重试读取入口；不得把它降级为保存失败并让确认再次提交 PUT。关闭或 donor 重绘也不能声称撤销已经接受的保存。

## 3. 固定内容、Prompt 与版本规则

| 状态 / 类型 | V3 行为 | 权威规则 |
| --- | --- | --- |
| `fixed_script` + `paused` | 显示“编辑固定话术”，可编辑文本和受权 Media 引用。确认仅保存固定内容草稿。 | 服务端把内容 trim，要求合法 Unicode rune 文本不超过 4000；引用每类及总数由 Owner 再校验。 |
| `fixed_script` + `active` | 显示“当前 Agent 已启用，不能修改固定话术”；保留只读内容与既有暂停入口。 | `SaveFixedContent` 返回冲突；UI 不替用户执行暂停。 |
| `archived` / 不存在 | 只显示对应不可用或未找到状态，不打开编辑器。 | Owner 返回 not found / conflict。 |
| `agent` | 不显示固定话术编辑器；说明内容由 Prompt 管理，Prompt 继续使用原页面领域流程。 | 非 `fixed_script` 的 `ContentText` 非法。 |
| 尚未创建的 `fixed_script` | 普通创建成功并跳转取得 ID 后，再显示固定话术编辑区。 | 首次创建可带现有创建载荷；后续修改固定内容只能由固定内容端点完成。 |

`DraftVersion` 和 `PublishedVersion` 保持可见且分开：保存固定内容只改变草稿版本，不发布、不启用、不执行。页面要说明“已保存为草稿，仍需使用既有发布流程”，不能把保存、发布和发送混为一个成功状态。

固定内容不声明变量能力：当前 `fixed_script` 没有调用方授权的变量语法或上下文白名单，页面不提供变量插入器；由于当前 `fixed_script` 没有调用方授权的变量语法或上下文白名单，编辑器会在提交前拒绝包含 `{{` 或 `}}` 的新草稿；这不是变量解析或替换能力。其他历史原文只按文本显示，不伪造替换值。Prompt 不由共享编辑器规范化或解析。

## 4. 内容与素材合同

内容包保持既有字段：`content_text`、`image_library_ids`、`miniprogram_library_ids`、`attachment_library_ids`、`group_invite_library_ids`。不写 UI 私有字段，也不开放 `DynamicMiniprogramCard`：尽管 Port 有该字段，当前 `normalizeContent` 明确拒绝。

允许的上限由 Owner 再次把关：图片最多 3，小程序 1，附件 9，群邀请 1，合计最多 9。编辑器按相同限额前置校验；任何预填、目录返回或调用方异常导致的越限都会阻止确认，绝不静默截断。

素材选择复用 V3 `SelectionSession → SelectionDialog → MaterialPickerAdapter`：

- Host 提供 Automation 页面当前权限范围内的 Media 目录 loader，组件没有默认 URL 或全局请求劫持；
- 按 `media-library + kind + id` 区分稳定键，同号跨类型不碰撞；
- 初选采用完整 `selectedRecords`。打开、重开和保存后的只读均按授权详情读取名称、缩略图、停用、删除、无权限或读取失败原因；未出现在当前分页不视为失效；
- 选择仅是临时集合。取消、关闭、选择失败或目录失败都不写 Agent；`onCommit` 只更新当前编辑器本地草稿；
- 由于 Owner 内容包只有按类型 ID 数组，首批不承诺跨类型排序。预览和只读使用包的稳定类型顺序及各数组顺序；编辑器不显示跨类型拖拽或“已保存跨类型顺序”的暗示。

`renderContentPresentation` 是编辑预览和服务端读回只读的唯一内容呈现函数。它只显示调用方提供的文本、受控缩略图和不可用原因；不会调用保存、上传、生成、Provider 预览或发送。

## 5. 用户旅程和状态

1. 运营人员创建或打开一个暂停的 `fixed_script` Agent；固定话术区先读取 Agent 详情，再按需要受权补全已有素材详情。
2. 只读区显示草稿内容、素材与“草稿 / 已发布版本”事实；点击编辑后打开共享 Composer。中文组合输入、预览、素材临时添加/删除、取消、失败与键盘焦点沿用共享合同。
3. 点击确认时，V3 Host 只发一次固定内容 PUT。加载中不能重复确认；同步或异步失败保留 Composer 本地草稿并给出可理解原因。
4. PUT 成功后 Host 重新 GET 当前 Agent，关闭 Composer，并将 GET 内容作为保存后只读展示；若读回失败，明确“已保存，暂时无法刷新当前展示”，仅提供重试 GET，不将本地草稿标为已读回或重新提交 PUT。版本显示只来自 GET 或已确认的 PUT 响应。
5. 当 Agent 为 active、archived、无权限、读取失败、素材删除/停用、目录无权限或请求过期时，状态区分别说明原因；不显示“后端能力未就绪”等工程文案，不静默隐藏。

页面切换、donor 重绘、关闭编辑器及慢请求都以当前 Agent ID/Host 代次和 AbortController 保护：旧详情、旧素材或旧 PUT 后读回不能覆盖新 Agent 的只读区。

## 6. 组件、样式与装配

- 管理端继续使用 `admin_base` 单壳、现有 `RenderAutomation` 和 manifest 缺 asset 即失败的门禁；不新增壳、依赖或 React/Ant Design。
- 新增 V3 `automationContentHost`，等待真实 donor `[data-agent-materials-readonly]` 后挂载固定话术卡，不接管 donor 普通保存表单。
- 复用 `ContentComposer`、`ContentPresentation`、`MaterialPickerAdapter`、`SelectionSession`、`SelectionDialog` 与已加载的 `presentation.css` 作用域。自动化场景传入准确的顶部说明，不能复用“实际发送由计划执行触发”的群运营文案。
- Host 负责页面范围内 GET、PUT、Media 详情/目录调用和状态翻译；共享组件不含 Automation URL、权限猜测或持久化命令。

本次会话的 Skills catalog 没有可用的 Product Design 路由，因此该环节未完成。本页继续沿用已审核的管理端视觉 token 与共享组件基线；不安装、替代或伪称已使用该插件。

## 7. 参考与取舍

| 参考 | 采用 | 不采用 |
| --- | --- | --- |
| [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) | 语义化加载/错误/只读状态、单一主要确认动作、token 约束的组件目录。 | 不引入 Ant Design、React 或新的组件运行时。 |
| [Tiptap Mention extension](https://github.com/ueberdosis/tiptap/blob/main/packages/extension-mention/src/mention.ts) | 稳定身份与显示标签分离的原则，适用于 Media record key。 | 不引入 Tiptap、ProseMirror，且本场景不虚构变量插入能力。 |
| 冻结 automation Agent 页面 | 实际页面结构、Prompt 分区和原有发布流程。 | 不修改 donor、不过载普通保存接口，也不保留其“后端不支持”的过时工程提示。 |

## 8. 真实验收与 PR 边界

1. 新建 `fixed_script` 成功跳转取得 ID 后，固定话术区出现；普通保存不会暗中覆盖固定内容。
2. 暂停 Agent：中文组合输入不重建 textarea；多类素材临时选择、取消、重新打开回显和显式移除正确；目录失败保留历史引用与事实原因。
3. 确认一次只发送固定内容 PUT；重复点击不重复命令；失败、409 active、403、404 均保留恰当草稿或只读事实。PUT 2xx 后 GET 失败时关闭已接受的草稿、显示明确读回失败，并且“重试读取”不发送第二次 PUT。
4. 成功 PUT 后验证 Agent `DraftVersion` 增加、`PublishedVersion` 不变，Prompt 和 `LegacyConfiguration` 与保存前一致；GET 读回内容被同一 renderer 呈现。
5. 非 `fixed_script`、active 与 archived 状态不能通过 UI 绕过 Owner 边界。
6. 定向 Node/TypeScript/manifest、冻结 Agent runtime Journey、真实 PostgreSQL + 独立 Chromium 页面验收；1280/1440 检查后台完整页面，360/420 只检查共享编辑器与固定内容局部容器。后台移动壳属于后续统一壳专项，不能以本页截图宣称完成。截图只证明本地页面/服务端读回，不证明发布或 Provider 送达。

本 PR 不改变 Automation Owner、OpenAPI、Media 写入、发布、执行、外部效果、OneID、Prompt 语义或数据迁移；它沿用当前主线共享组件，只声明本页真实接入。
