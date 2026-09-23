# 四个管理端已提交搜索缺口

## 目标

已审核的 [PR #287](https://github.com/qianlan33333-png/AI-CRM-v3/pull/287) 提供 V3 `committedTextSearch` 合同：文本输入始终先保留草稿，只有普通 Enter 才转发原有读取或本地过滤事件；输入法 composition、`keyCode 229`、以及 blur 都不能提交。本 PR 将该合同补到四个仍在每次输入时执行搜索的真实入口。

## 业务与架构判断

| 项目 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及。四项只处理页面内查询草稿，不解析、关联或写入客户身份。 |
| 持久化、内部任务 | 不涉及。没有新增状态表、队列或重试。 |
| Provider 读取 / 写入 | 不新增。图片和群目录沿用调用方现有的受权读取；开放平台和字段变量搜索是本地过滤。没有命令或外部效果。 |

## 实际入口与边界

| 页面与真实装配 | 控件与既有行为 | 本次行为 |
| --- | --- | --- |
| `/admin/images`：`composition.go` → `RenderMedia` → `imageLibraryFilterHost.ts` | `[data-image-library-query]` 每次 `input` 调用 250ms `scheduleSearch`，随后用既有图片目录 GET 和 offset 分页读取。 | 输入仅保留草稿；普通 Enter 才执行原调度。已有请求中取消、错误时保留列表、重试、重置和分页不改变。 |
| Open Platform 文档：`openPlatformAdapter.ts` | 文档搜索 `applySearch` 只隐藏/显示本地操作表和章节。 | 输入只保留草稿；普通 Enter 才执行本地过滤。目录锚点清空搜索仍立即恢复文档。 |
| 商品外部推送字段映射：`productAdapter.ts` → `fieldMappingEditor.ts` | 变量 popover 的 `搜索变量` 每次 `input` 重画选择项。 | 普通 Enter 才重画候选；打开、Escape、选择候选和预览请求语义不改变。 |
| `/admin/groupops.html`：`groupOpsHostAdapter.ts` 动态加载 `groupOpsStandard.js` | 群目录列表 `input[name=keyword][data-filter]` 的 Enter 直接调用现有 `loadGroupsPage()`；select 的 `change` 也使用同一读取。 | 文本关键字的 composition/229 和 blur 不读取；普通 Enter 只转发一次现有读取。select 的 `change` 保持即时读取。 |

`groupOpsStandard.js` 的 `data-group-picker-search` 是详情中的局部候选过滤器，当前不匹配 PR #287 的冻结 group-chat picker selector；它不是本次群目录列表关键字缺口，故仅记录，不新增另一套处理器。

## 共享组件与调用规则

扩展既有 `web/v3/shared/ui/committedTextSearch.ts` 的精确注册表，不建立页面私有键盘状态机。每个实际 Host 显式安装同一组件；其 document 级幂等状态保证多个 Host 不会重复转发。保留页面已有 listener 为唯一搜索/分页实现，组件不读取业务 API、不改参数、不发送写请求。

## 验收

- 共享测试覆盖四个注册输入：输入、composition/229、普通 Enter、blur 和一次转发。
- 图片素材 Host 测试确认未提交草稿不触发目录 GET，提交后保留已有取消/乱序和分页行为。
- Open Platform Host 与字段映射测试确认 Enter 前不重画，普通 Enter 后才应用过滤。
- GroupOps Host/DOM 和 PostgreSQL + authenticated Chromium Journey 确认输入法候选 Enter 不发群目录请求，普通 Enter 只读一次，select `change` 不回归。
- 构建、typecheck、受影响前端脚本、冻结 donor consumer/stage 和真实 HTTP 资产闭合串行验证。
