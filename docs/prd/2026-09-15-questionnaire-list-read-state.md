# 问卷后台列表读取状态

## 背景与目标

后台 `/admin/questionnaires` 当前由冻结问卷列表模板、冻结 `AdminController` 与 V3 `surveyAdapter` 组合。目录读取失败时，冻结运行时只给出通用错误，不能区分已确认空目录、搜索／状态筛选无匹配与可恢复读取失败，也没有从列表内重试的入口。

本变更在真实 V3 问卷列表挂载点补足这三种可观察状态，同时保留当前问卷创建、编辑、发布、复制、启停、删除、下载和分享动作。它不修改问卷定义、答卷、公开提交或运营配置。

## 开发前判断

- **OneID：不涉及。** 本能力只读取问卷定义目录及当前浏览器本地筛选结果，不读取或关联客户、外部身份或归属。
- **持久化：无状态只读。** 仅呈现现有 `/api/admin/questionnaires` 目录读取结果；不写 PostgreSQL、不新增任务、Outbox 或状态机。
- **外部效果：不涉及。** 不调用 Provider，不触发发送、发布、提交、支付或授权流程。

## 已核实链路与边界

`/admin/questionnaires`、`/admin/questionnaires.html` → `internal/survey.UIBinding` → `internal/webshell.RenderSurvey` → `surveyHost` → `web/v3/surveyAdapter.ts` → 冻结 `AdminController.init()` / `api.loadDb({ page: 'questionnaires' })` → `/api/admin/questionnaires`。

冻结 donor 仅作为行为与模板供体，不修改。V3 adapter 捕获该页面的读取成功与失败，依照已渲染的真实行与当前筛选结果调用共享表格读取状态呈现；不拦截其他页面读取或任何写请求。

## GitHub 与组件参考

- [PR #340](https://github.com/qianlan33333-png/AI-CRM-v3/pull/340) 已实现并审阅同类渠道目录读取状态，提供共享 `web/v3/shared/ui/tableReadState.ts`、`surfaceFeedbackHost` 暴露和 `surfaceFeedback.css` 样式。问卷页将直接采用这套组件，不建立平行空态／错误组件。
- `skills/aicrm-v3-frontend-consistency/references/component-map.md` 已登记问卷编辑器专用企微标签选择器；它不适用于目录读取状态。当前页面由 Survey adapter 负责装配。
- Product Design catalog 在本轮不可用；未假称调用。视觉继续复用已加载的 surface feedback 与现有管理端表格语言。

## 交互与状态

1. 仅在 `/admin/questionnaires` 页面成功完成一次目录读取后判定空态；初始加载或失败绝不伪造成空目录。
2. 成功读取的当前目录为空时，表格显示“暂无问卷，可通过右上角创建新问卷”。
3. 当前目录存在但名称／ID 搜索或启停筛选不匹配时，表格显示无匹配状态；保留当前筛选输入和选择值。
4. 后续 5xx、网络或 malformed 响应失败时，保留上次成功加载的可授权行，并紧邻显示错误与“重新读取”。重试调用原 `controller.init()`，不改查询、筛选或写入任何领域数据，重复点击不并发重复读取。
5. 401／403 时清除已加载行，明确显示登录失效或无查看权限；不提供把失权数据留在页面上的重试入口。后续最新成功读取才恢复正常目录。
6. 乱序或过时读取不能覆盖最新成功／失败状态；离开页面后延迟回调不写入其他页面。
7. 既有搜索及中文输入法、创建、提交、发布和全部行操作合同保持原样；本能力不新增键盘提交或请求时机。

## 实现与验证

- 从 #340 的共享实现引入**同一份** `tableReadState`、host 暴露与样式（作为显式依赖同步，不复制页面私有实现）。
- 扩展 `surveyAdapter` 的页面限定 load/read 与 render seam；冻结模板和 donor source 不变。
- 单元／DOM 回归覆盖：初始失败非空态、成功空目录、筛选无匹配、保留行的可恢复失败与一次重试、401／403 清空、malformed 2xx、乱序回调、离页安全，以及既有归档动作。
- 真实隔离 PostgreSQL + Chromium 复用问卷 fixture，覆盖 1280／1440 成功空态、无匹配、失败重试及既有创建／行操作未回退；不启动 Provider。
- 运行受影响 Node、Go／shell contract、构建、stage 和 P5。PR 创建、合并与部署等待根代理按 #340 依赖顺序安排。
