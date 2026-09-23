# 渠道码中心列表读取状态：诊断与最小 PRD

日期：2026-09-15
状态：已实现并完成本地集成验证，待最终审阅与推送。

## 业务判断与边界

渠道码中心面向授权管理端用户展示已经由渠道领域提供的渠道资产目录。本项只改善已读列表在加载、成功空集、关键词无匹配和读取失败时的表达，不改变渠道、客户或人员归属。

- OneID/外部身份：不涉及。列表仅展示既有渠道记录；不创建、匹配或合并客户身份。
- 持久化、内部任务、Provider 写入：不涉及。只读取既有 `/api/admin/channels`；不改变归档、渠道发布、二维码、欢迎语或企微调用。
- 权限：继续由服务端读取接口校验。401、403、5xx 和网络失败必须保持为失败，绝不能显示为“暂无渠道”；已有成功页发生 401/403 时清除可能已失去授权的数据，网络/5xx 才保留已确认的当前页。
- 冻结 donor：`web/src/admin/templates/channels.html` 与 donor 控制器保持冻结。改动只能在 V3 Host 的内存模板/稳定 Adapter seam 及共享状态层完成。

## 已核实的挂载与请求路径

1. `/admin/channels` 由 `internal/webshell/renderer.go` 的 `RenderChannels` 渲染管理端 shell，页面 `data-page="channels"`。
2. donor 模板 `web/src/admin/templates/channels.html` 中保留搜索输入框 `input[aria-label="搜索渠道名称"]`、表头和 `sc-for list="{{ rows.channels }}"`。它没有空列表分支。
3. `web/v3/channelCenterAdapter.ts` 先安装 `installCommittedTextSearch()`，再调用 `prepareFrozenChannelListTemplate()`：只在内存 fragment 删除重复标题/旧创建按钮，并通过已有 `mountPageHeaderActions` 将“新建渠道”放到唯一 `.admin-topbar`。该 Adapter 继续由 `AdminController` 输出 donor 列表。
4. `web/src/admin/legacy.ts` 初始写入“正在读取页面数据…”，然后调用 `AdminController.init()`；初读异常会由 `showLoadError` 替换 `#stage` 为错误卡片。
5. `AdminController.init()` 通过 `api.loadDb(context)` 读取；成功后才替换 `this.db`。因此一次已有列表后的刷新失败保留旧 `this.db` 和已渲染行。渠道归档的 V3 seam 也明确提示“渠道已归档，但列表未更新；请刷新后核对归档状态”。
6. `web/src/api/admin.ts:readAdminRows` 对渠道调用 `listLegacyChannels({limit:50, include_archived:true})`。`unwrapGenerated` 对非 2xx（含 401/403）抛出 `ApiError`；网络异常亦会 reject。当前 `list(channels, 'channels', 'items')` 在 **2xx 但缺少两个数组字段** 时返回 `[]`，会把 DTO 不完整静默映射为成功空集。这是需要 fail-closed 的数据契约缺口。
7. donor 控制器的 `channelQuery` 仅过滤当前已加载行，不发新请求。共享 committed-search 只在非 composing 的普通 Enter 将输入提交给 donor；输入法草稿和候选 Enter 都不会筛选或发送读取请求。

## 现有共享复用结论

- `web/v3/surfaceFeedbackHost.ts` 已是所有管理 shell 的共享初始加载/资源失败反馈层，应继续承担页面级 loading 与资源失败表现。
- `web/v3/componentStatesHost.ts` 是状态示例页，不是可挂载的生产列表组件。
- `web/v3/imageLibraryFilterHost.ts` 已有“`hasSuccessfulRead && items.length === 0` 才显示空态”的正确局部判断，但它是图片库私有 Host，不能把它复制为渠道私有实现。

因此最小实现应**扩展已有 shared surface-feedback 层，提供可复用的表格读取状态 API**，由 Channel Center Adapter 在稳定 donor seam 上调用；不得新建平行页面壳或改 donor。

## 状态模型与可见行为

| 状态 | 判定来源 | 列表表现 | 操作与可访问性 |
|---|---|---|---|
| 初始读取中 | `#stage` 的既有 loading placeholder；尚无成功目录 | 由 `surfaceFeedbackHost` 显示加载；不提前生成“暂无渠道” | `role=status`、`aria-live=polite`；不抢走搜索焦点 |
| 成功但目录为空 | HTTP 2xx，且 `channels` 或 `items` 是有效数组，长度为 0，且尚无已提交关键词 | 表格一行明确“暂无渠道，可通过右上角新建渠道创建。” | 保留 topbar 新建渠道；不把空集解释成无权限 |
| 成功但关键词无匹配 | 已有有效数组，已提交的 `channelQuery` 非空，本地过滤后 0 行 | 表格一行“未找到与「关键词」匹配的渠道。” | 保留搜索输入；仅普通 Enter 后变更；IME 草稿/候选 Enter 不切换状态 |
| 已有成功数据后刷新失败（网络、5xx、DTO 不完整） | 刷新 promise reject，5xx，或 response DTO 不完整 | 保留已显示的最后成功行；在列表上方/相邻位置显示可读错误和重试/刷新入口 | 不清空、不替换为成功空态；焦点回到触发刷新处 |
| 已有成功数据后鉴权失效 | 401 或 403 | 清除旧渠道行并显示登录失效或无权限提示 | 不保留已失去授权范围的数据；不新增 OAuth 流程 |
| 首次读取失败 | 非 2xx、网络错误、或无有效数组 DTO | 现有 `showLoadError` 页面错误卡片，明确读取失败；不是空态 | 继续通过加载页的重新加载路径恢复；401/403 原样作为鉴权失败语义 |

成功定义必须同时满足：`Response.ok`/generated client 的 2xx 解包成功，且响应中 `channels` 或 `items` 为数组。缺字段、非数组、解析失败都属于失败，不能通过当前 `list()` fallback 降级成 `[]`。

## 实际最小变更范围

1. 在 `web/v3/surfaceFeedbackHost.ts` 的已有全局反馈入口中增加小型、通用的列表读取状态 renderer（或同目录受它公开 API 的共享 UI 文件），支持 loading、confirmed-empty、no-match、error-retain-rows；使用现有 `presentation.css`，不内联复制样式。
2. 在 `web/v3/channelCenterAdapter.ts` 的 `AdminController.renderVals` 稳定包装 seam 中，从已提交的 `rows.channelQuery` 和 `rows.channels` 计算显示状态；通过 in-memory template 或渲染后节点插入一行 `td[colspan="6"]`。仅在确认的空/无匹配时插入，不能根据裸 `tbody` 推断成功。
3. 在 `web/v3/channelCenterAdapter.ts` 的渠道请求适配边界对 `channels`／`items` 数组做 fail-closed 校验：2xx 但不含有效数组转换为 502，再交给既有读取错误路径；不修改冻结 donor 或其他列表的 DTO 兼容性。
4. 以当前读取 generation 绑定成功、失败与展示提交，拒绝过期响应；重试保持 singleflight。401／403 锁存并清除已缓存行，只有当前成功读取才解除。
5. 不触及 #335 的 header action、搜索的 committed Enter/IME 行为、渠道归档 CAS/幂等、渠道表单、后端 API、donor source。

## 验收与测试

- 现有 `web/v3/channelCenterAdapter.test.mjs` 扩展或紧邻的 V3 测试覆盖：
  1. 初始 loading 不显示成功空态；
  2. valid `[]` 显示目录空态；
  3. 已加载目录后普通 Enter 提交的无结果显示关键词空态；
  4. IME 草稿与候选 Enter 不过滤、不重建输入、不失焦；
  5. 401/403/5xx/网络失败不会显示为空；已有行保留；
  6. 2xx malformed DTO fail-closed。
- 扩展现有 `TestPostgreSQLChannelCenterCommittedSearchChromiumJourney` 或紧邻真实 PG Chromium journey，使用服务端实际渠道列表，覆盖至少 1280/1440 视口的成功空和 committed-query 无匹配，并保存截图。
- 复跑现有 channel archive refresh 断言，证明读取失败不把已确认归档的列表更新伪造成空成功；每行继续只保留一个归档删除入口。
- 类型检查、受影响 V3 host build/stage；不把 mock-only 作为最终交付。

本地完成记录：Node 读取状态／归档合同、真实 PostgreSQL Chromium 的已加载、有效空目录与 committed-query 无匹配（1280／1440），以及管理壳组合预检均通过；完整原始日志与截图在 `aicrm-artifacts/channel-list-read-state-20260915`。

## 参考与治理

- PR #331（已审的 committed Enter/IME 搜索约束）：https://github.com/qianlan33333-png/AI-CRM-v3/pull/331
- PR #317（已有 V3 shared Host/组件生命周期路线）：https://github.com/qianlan33333-png/AI-CRM-v3/pull/317
- 已通过 GitHub 代码搜索确认没有现成的通用 admin table empty-state；仓库检查也确认 `componentStatesHost` 仅为示例页。
- Product Design catalog 当前不可用；本诊断未假称调用插件。视觉实现应复用当前 `presentation.css` 与 surface feedback，而非建立页面私有风格。
