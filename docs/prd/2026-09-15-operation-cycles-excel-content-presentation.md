# 运营闭环 Excel 批次：内容编辑、预览与只读呈现

- 状态：实施中
- 目标页面：`/admin/operation-cycles`
- 首个依赖：Draft PR #307（共享 `ContentComposer` / `ContentPresentation` 与 V3 dialog 行为）；本 PR 不把群运营实现复制到运营闭环。
- 真实链路：`/admin/operation-cycles` → webshell handler / `RenderOperationCycles` → `OperationCyclesAssets.HostJS` → `web/v3/operationCyclesAdapter.ts` → `mountOperationExcelWorkspace` → `web/v3/excelBatches.ts`。

## 1. 业务判断与边界

运营闭环的 Excel 批次不是通用素材包。每一行由 Owner 持久化为固定顺序的两个内容块：

1. 必填文本；
2. 一个 Excel 小程序卡片（固定 AppID 和同批次统一封面，行内仅允许修改 `path`、`title`）。

批次统一封面归批次 Owner，所有行共享。没有封面时，后端已经拒绝审核通过及创建企微群发任务；前端必须保留明确禁用和原因，不能把“本地预览可见”显示为可发送。标题来自 Excel 行卡片，空标题须显示为执行时会明确失败，不能以默认标题掩盖问题。

`material_plan.references`、`ContentPackage` 的跨类型素材顺序不属于 Excel Owner 合同。本页不增加图片、附件、群邀请的添加、删除或排序控件，也不保存 UI 私有顺序。文本和 Excel 卡片按 Owner 的 `[text, excel_card]` 顺序预览、编辑和只读回显。

### 开发前分类

| 维度 | 结论 | 理由 |
| --- | --- | --- |
| OneID | 不新增涉及 | 页面沿用受权批次行中的既有不透明 UnionID 展示；不解析、匹配、建客或变更客户归属。实际执行时的身份解析仍留给既有 Owner/Port。 |
| 持久化 | 仅既有 Owner 本地事务 | V3 Composer 只维护页面草稿。确认后由既有批次行 `PATCH`、封面和审核 API 处理版本、幂等、审计与回读。 |
| 外部效果 | 本专项不新增 | 预览和只读不发送、不上传、不调用 Provider。既有“审核通过并创建企微群发任务”仍是 Owner 的意图创建，必须保持“员工仍需企微端执行，非发送成功”的事实文案。 |

## 2. 用户结果与真实流程

运营人员在长期计划下选择一个 Excel 批次，能清楚完成下面的闭环：

1. 查看计划标题、批次状态、内容版本、逐行标题异常和统一封面状态。
2. 在内容准备中打开某一行：编辑话术草稿时同步看见文本、小程序标题/路径和同批次统一封面；确认只回写该行编辑表单草稿，再由“保存并重新审核”调用既有 Owner API。
3. 修改后从当前批次回读；版本变化、冲突、无权限、读取失败或批次切换必须保留清楚的失败状态，不能成功 toast 后静默丢草稿。
4. 从历史版本打开同一只读呈现，内容顺序、标题、封面和“不可发送/执行失败”的原因与编辑预览使用同一呈现函数。
5. 进入发送效果与复盘，保留已有逐人回执、12/24/48 小时观察窗口、未知结果待对账等事实；不把预览或批准当成交付成功。

终端范围只有管理端单壳。企微侧边栏、H5、公共页和自动化话术页面不加载本专项样式、内容或 API。

## 3. 信息结构与状态

| 区域 | 显示与操作 | 权威数据与状态 |
| --- | --- | --- |
| 长期计划列表 | 计划标题、最新批次摘要、查看详情 | `strategy-summaries`；批次不可用与“无批次”分开显示。 |
| 批次头部 | 当前批次、状态、内容版本、总行数/空标题/预计任务、统一封面 | 当前批次元数据；封面未设置是阻断审核的业务状态。 |
| 内容准备 | 替换 Excel、上传或选择已有启用封面、行表、历史版本 | 仅可编辑的 pending-review / partially-approved 批次显示写操作；同一批次写操作单飞。 |
| 行编辑预览 | 共享 Composer 的文本草稿，补充 Excel 卡片与统一封面只读块 | 文本规则、卡片路径和标题由 Excel Owner 合同校验；确认仅更新行对话框草稿。 |
| 已保存/历史内容 | 共享只读呈现 + 同一 Excel 卡片/封面补充块 | 批次内容版本和行版本；历史永远只读。 |
| 效果与复盘 | 报告、逐人回执、下载 | 实际成功时间、unknown/失败原因、观察窗口；不推测送达。 |

加载、空、无权限、失败分别呈现：读取中不显示旧批次内容；空计划不显示伪数据；403 说明无访问权限并保留原选择；失败提供重试，不以“后端能力未就绪”等工程文案代替业务说明。批次/节点切换、关闭对话框和旧请求返回均以代次与 AbortController 防止覆盖当前视图。

## 4. 组件与装配方案

### 复用

- 管理端单壳和 `operationCyclesHost` manifest entry；不新增二级壳或修改冻结 donor。
- PR #307 的 `openContentComposer`：本页配置为仅文本、零可选素材、无变量插入；调用方传入 Excel 行真实的 UTF-8/长度/空白策略。
- PR #307 的 `renderContentPresentation`：编辑预览和已保存只读共用文本呈现。扩展时只增加被调用方明确传入的 Excel 卡片补充呈现（标题、路径、同批次封面受控 URL、不可用原因），不使共享层读取、保存或发送。
- 现有 `actionFeedback`、批次单飞、版本回读、分页和效果报告。

### 不复用或不采用

- 不将 GroupOps 的 `material_plan.references`、目录 loader、`selectedRecords`、群聊选择或素材多选注入 Excel 批次；这些不是 Excel Owner 的持久合同。
- 不创建独立操作周期页面、React/Ant Design/Vant 依赖、第二套 dialog 或独立内容持久化。
- 不把自动化固定话术与 Prompt 入口混入此页；它们以后从 `RenderAutomation` 的真实 Host 独立接入。

### GitHub 与设计参考

| 参考 | 采用 | 不采用 |
| --- | --- | --- |
| [Ant Design DESIGN.md](https://github.com/ant-design/ant-design/blob/master/DESIGN.md) | 14px 高密度后台、语义状态、单一主要操作、4px 间距和有文本的错误反馈。 | 不引入 Ant Design/React、运行时 token 或新视觉壳。 |
| [Ant Design Pro](https://github.com/ant-design/ant-design-pro) | 长期计划列表 → 批次详情 → 内容/效果双维度的信息层级。 | 不复制模板、路由、mock 或应用脚手架。 |
| [Vant](https://github.com/youzan/vant) | 窄视口中固定可访问的确认区与可滚动正文原则。 | 不把移动组件或管理端 CSS 泄漏到 H5/侧边栏。 |

## 5. 真实验收

1. 有统一封面的可编辑批次：打开行编辑，中文组合输入不重建 textarea；确认只改变行对话框草稿；“保存并重新审核”一次 PATCH 后回读内容版本和行文本。
2. 无封面批次：预览可见明确“未上传统一封面”，审核按钮禁用，后端 `cover_required` 仍被实际验证；不可通过 UI 绕过。
3. 空标题行：显示“执行时明确失败”，不自动补标题；后端不创建可发送任务。
4. 历史内容：选中历史版本后打开只读内容，文本、卡片标题/路径、同批次封面与当前行不混淆；同时保留分层、是否排除、审核状态、执行状态、发送时间、行版本和内容版本。历史没有编辑/保存操作。
5. 批次切换、关闭和慢响应：旧行或旧版本不打开新对话框、不写回新批次；一次交互期间没有重复 PATCH。
6. 效果页：实际发送时间、失败原因和 `outcome_unknown` 分开展示；预览/批准没有被显示为发送成功。
7. 真实 PostgreSQL + 独立 Chromium：1280/1440 管理端和 360/420 窄视口；截图只证明本地 UI 与业务回读，不作为发布或 Provider 送达证据。

## 6. 实施与 PR 划分

1. 本 PR：共享文本/Excel 卡片呈现扩展、真实 Operation Excel Workspace 行编辑与历史只读接入、Host/Journey/PG Chromium 验证。依赖 PR #307，Draft PR 需明确堆叠关系。
2. 后续独立 PR：自动化固定话术与 Prompt，从 `RenderAutomation` 的真实资产链接入；保留服务端 fixed-script / Prompt / 素材限额规则。
3. 不在本轮：变更 Excel Owner 的内容块模型、跨类型素材排序、自动批准、真实群发、Provider 读取/写入、OneID 规则或数据迁移。
