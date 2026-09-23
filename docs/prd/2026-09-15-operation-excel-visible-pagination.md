# 运营闭环 Excel 批次：可见 Tab 按需分页与版本一致性

**状态：** 已批准实施（独立窄修复）  
**开发基线：** `6aa46e2c62a563bae053ecc75970da1e0d2398c7`（PR #286 的 action ownership 修复）  
**最终集成要求：** PR #286 合入 `main` 后 rebase；最终 PR 仅包含本项 diff  
**用户入口：** `/admin/operation-cycles#strategy=<strategy_key>`

## 问题与证据边界

开发基线 `web/v3/excelBatches.ts` 的 `readAllPages()` 以 `limit=50` 串行跟随 `next_cursor`，并被 `loadSelected()`、`renderDetail()`、`effects()` 与 `versionDialog()` 调用。

因此，基线中打开内容会在首个可见区绘制前读完所有内容行；切到“发送效果与复盘”也先读完隐藏的内容行，再读完所有回执，最后才读取效果报告。这个调用链是确定的源码级 O(N 页) 请求和可见区延迟根因。它不是线上性能数值；部署后的浏览器网络时序和用户体验仍须独立回读。

现有 Host API 已提供受限 cursor page：当前内容和历史版本使用 `rows + next_cursor`，回执使用 `items + next_cursor`，单页上限由服务端执行。没有证据表明需要替换 cursor 协议、改 SQL 索引或新增 API。

## 已核对的参考与取舍

本仓已合并的 [PR #223](https://github.com/qianlan33333-png/AI-CRM-v3/pull/223) 建立了 Excel 运营批次及其 revision/cursor 读取合同。本修复复用该现有 bounded page，而不是另造 offset、metadata 或全量导出 API。现有 `/report.csv` 已是服务端整批导出，因而保留它来满足跨页报表需求；UI table 只负责当前可见页。

`AbortController` 只减少被替换读取的浏览器负载，不能作为正确性边界；实现仍使用每个区域 request ID 和 workspace generation 拒绝晚到的成功、失败及清理结果。这一取舍避免引入共享缓存、后台预取、持久任务或新的取消协议。

## 架构分类

| 检查项 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不新增解析、匹配、建客或权限扩展。页面仅呈现既有冻结收件人字段。 |
| 持久化 / 内部持久任务 | 内容页读取不写入。回执页沿用既有 `RowsPage(..., poll=true)`：它可能复用既有 `pollReceipt` Provider 读取，并通过既有 `SavePrivateMessageDelivery` / `RecordExcelDelivery` 更新本地回执。此 PR 不改变该合同，不新建表、UoW、持久任务、队列、重试或对账状态机。 |
| Provider / 外部效果 | 不新增 Provider 写入、发送或重试。回执页的既有可信 Provider read 保留；`outcome_unknown` 继续只显示待核对，绝不换 key 盲重发。 |
| 权限 | 保持既有管理员读、审核、导出和服务端 CSRF / CAS / 幂等检查。 |

## 范围

### 包含

1. 内容 Tab 只读取当前批次的首个 50 行；行表提供同一 cursor 契约下的上一页/下一页。批次 `summary` 由当前 detail/strategy 响应的服务端全量摘要提供，不以当前页行数伪装总数。
2. effects Tab 不读取内容行。它在获得当前批次 metadata 后并行启动回执首页和 `/report`：两块区域独立先后渲染、独立错误和重试；`report` 继续是全局 observations 指标来源。
3. 逐人回执改为一个 cursor 页，不再隐式预取后页；保留 `/report.csv` 的服务端全批导出。
4. 历史版本“只读查看”改为该固定 revision 的单页 cursor 阅读和翻页，保留只读属性。Host 的 `GET /versions/{revision}` 响应中 `content_version` 是 `ExcelBatchVersion` 对象，revision 取其 `content_version.content_version` 字段；必须同时校验顶层 `batch_id` 和 `read_only: true`，不能把对象数值化或仅凭 selector 相信响应。
5. 对换批次、换 Tab、换历史版本、翻页与请求晚返回使用 AbortController 减负加 request generation/key 最终门控。历史 viewer 在发出版本列表 GET 前绑定 batch/workspace generation，且为每一次版本选择维护单调 viewer generation 和 page object identity；任何旧响应不能写入 DOM、page cursor、loading 或错误区，也不能继续追旧 cursor。
6. current content version 以本次 metadata/页响应为准。跨页响应与当前批次 `current_content_version`（回执为 `content_version`）不一致时，丢弃该页和 cursor 栈，并且仅自动从当前可见目标的首页重新读取一次；连续版本漂移显示可重试错误，禁止无限刷新循环。该版本失效错误的“重新读取当前页”必须重新请求 metadata、从首页开始并重置一次恢复预算；普通网络失败仍精确重试原 cursor。
7. 成功替换 Excel、上传/选择封面、编辑/排除行或审核后，失效全部当前 page cursor，并从可见 Tab 的第一页重新读取；不将旧 cursor 与新版本组合。写命令的 busy 直到这次回读成功或失败才解除；GET 失败展示回读错误，不能显示写入成功。内容页响应即使 content version 相同，若返回 batch state/CAS version/cover/summary 已变，也重新构建顶部摘要和动作，使审核按钮闭包使用最新 metadata。

### 明确排除

- 不改后端 cursor API、最大页数、数据库索引、持久化模型、Provider adapter、outbound、队列或 WebShell/donor 组件。
- 不截断 `readAllPages()` 后把剩余数据移到隐式后台预取；本变更消除上述四个 UI 全量读取调用点。
- 不以当前 content/receipt 页的长度推导批次总行数、发送数、成功率、观察窗口或整批审核资格。
- 不改变全批 `preview-approval` 后以相同 `expected_version` 和 `preview_digest` 执行 approve 的服务端审核语义。

## 状态、刷新与数据完整性

策略详情 endpoint 已为每个批次返回最新 `batchJSON`：包括状态、plan version、`current_content_version`、封面与全局 `summary`。进入/切换 Tab 或批次时先以它刷新页头 metadata，不借用可能过期的列表对象，也不另造 metadata endpoint。

每个可分页区域保存不可变请求快照：`workspaceGeneration`、批次 ID、Tab、可选历史版本、cursor 和该区域 request ID。新请求会 abort 该区域旧读；Abort 仅降低负载，响应修改前仍必须按快照验证 request ID、workspace generation、批次与 Tab/version。历史查看还验证本次 page object identity 和单调 viewer generation，以阻断 A→B→A 中第一次 A 的迟到响应。回执翻页不会中止同时在途的 report；report 和回执没有相互等待关系。

服务端 detail/receipts 已在读取前后比较 meta revision/file digest；前端还要拒绝与当前 metadata version 不同的页面，确保不会在同一表格拼接不同内容版本。失败状态保留其精确快照，重试同一 cursor；空页只表示该页无记录，不能改写整批 summary 或伪称无效果。

## 不可破坏的业务合同

- 审核仍先由服务端对**整批**当前 version 生成 preview digest，再以该 digest 和 expected version 原子批准；当前显示的 50 行不能缩小审核范围。
- 没有统一封面时，审核按钮继续禁用，服务端封面门禁不变。
- 行编辑/排除继续走现有版本化 PATCH，成功后回读最新首屏和服务端摘要。
- 历史版本始终只读；它的 page cursor 与当前版本分开，切换历史版本后不复用旧 cursor。
- CSV 保持整批 server export。Provider 回执、真实发送时刻和 `outcome_unknown` 的现有中文呈现与保护不改变。

## 前端一致性

复用现有 Excel workspace、WebShell、`action()`、表格、`admin-pagination` 与现有 API client。只在 `web/v3/excelBatches.ts` 增加局部 cursor 状态和现有样式类的分页控件；不重设计页面、不新建平行组件、不修改冻结 donor。PR #286 的 action ownership 声明应随开发基线保留，但本 PR 最终 diff 不重复它。

## 验收与回归白名单

隔离 jsdom fixture 用服务端全局 `summary.total_rows = 151`、首个内容可见页和 51 条回执的两页 cursor 场景控制响应先后；它验证请求数量、cursor 和渲染时点，不把该 fixture 表述为 151 行或 151 回执的四页吞吐测量：

| 场景 | 预期 |
| --- | --- |
| 打开 content | 只发一次 `?limit=50` 内容页并先渲染首屏；不请求 cursor 第二页或 receipts/report。 |
| 切到 effects | 不请求内容 rows；同时发起回执首页和 report；任一先返回即独立显示。 |
| 内容、回执、历史版本翻页 | 每次只使用上一次响应的正确 cursor；上一页回到原 cursor；不跨版本拼页。 |
| 乱序/Abort 后晚返回 | 旧 response/reject/finally 不改当前 DOM、cursor、错误或 busy；不继续追旧 cursor。历史列表在换批次后晚返回不挂 dialog；A→B→A 的旧 A success 与旧 B error 都不能覆盖当前 A。 |
| 版本漂移 | 一次明确的首页刷新；仍漂移时显示可重试错误，不循环请求。直接点击该版本错误的重试会刷新 metadata 并从首页恢复；普通网络错误仍重试原 cursor。 |
| 失败、重试、空态 | 精确重试原 batch/tab/version/cursor；真实错误与空态可见。 |
| 审核、封面、行编辑、历史只读、CSV、outcome unknown | 延续现有全批预览/批准、封面门禁、版本化回读、只读和 server export 合同；写后 GET 回读未完成时保持 mutation lock，失败显示回读错误。 |

定向验证：

```text
node scripts/excel-batches-dom-test.mjs
node scripts/excel-batches-pagination-dom-test.mjs
npm run typecheck
```

两套 Excel DOM fixture 均注册到 canonical `quality_lanes.py frontend`，因此 required frontend lane 会执行它们；分页用例直接以 `esbuild` 打包 V3 workspace，不读取未物化 donor view 或 `dist`。

若环境没有 PostgreSQL，Go 测试结果只能说明编译/非数据库路径，不应表述为 PostgreSQL 执行通过。定向 DOM 的请求数和首个可见区渲染时点是源码级性能证据；合并、部署与真实浏览器网络回读分别记录，不能相互替代。
