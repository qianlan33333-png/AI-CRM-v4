# 运营闭环 Excel 审批：浏览器夹具读回就绪 PRD

## 业务判断

管理员在为批次上传封面、编辑或排除行后，只有权威内容页读回完成、当前批次仍可编辑时，才可点击“审核通过并创建企微群发任务”。本修复不改变该产品合同；它让 Chromium journey 以确定性方式等待真实可交互状态，避免把一次 disabled click 误判成审批功能失败。

## 根因与范围

`web/v3/excelBatches.ts` 的 `withBatchWrite` 从写入开始保持 `batchWritePending=true`，直到 `loadSelected(..., requireReadback=true)` 完整等待 `loadContentPage`。内容页请求前，`renderDetail()` 已会显示最新摘要，例如“已排除 1”。原 journey 只等摘要变化就点击审批按钮，因此在内容页仍 pending 时会对 disabled button 调用 `.click()`；不会发 preview 或 approve 请求，页面保留待审核和空状态。该结论来自源码和失败 DOM，随后由本 PR 的受控请求闸门验证；不是线上复现结论。

只改 `cmd/aicrm/excel_batches_chromium_test.go` 与 `cmd/aicrm/excel_batches_chromium_journey.mjs`：

1. 仅在 `httptest.NewUnstartedServer` 的 browser fixture 增加一次性、测试私有的内容 GET 闸门。journey 从已渲染的“当前批次 #ID”读取正整数并传给 arm；fixture 再查询该 `plan_id` 是否是当前合成批次，拒绝无效或不存在 ID。它先放行所有正常请求，只有 journey 显式 arm 后的下一次该批次内容读取会等待测试私有 release；不进入 production handler、路由或发布资产。
2. journey 通过 CDP `Network.requestWillBeSent` 记录当前批次内容 GET、preview 和 approve。它先证明“摘要已刷新、审批 disabled、内容 GET 已开始”，disabled click 后断言 preview/approve 均为零。
3. release 后，journey 只在审批按钮 enabled 时点击，并要求 `[data-excel-feedback][role=status]` 的既有成功提示、按钮消失、一次 preview、一次 approve，以及现有 PostgreSQL `queued=1`/`excluded=1` 收据断言。
4. 反馈选择器限制在 workspace 所有者的 `data-excel-feedback`，不再依赖页面第一个全局 `role=status`，避免分页或 busy 子元素改变选择目标。

这是一项测试可靠性修复，既不改审批 API、批次状态、CAS、幂等键、Provider 调用、队列、数据库 schema，也不修改冻结 donor 或 UI 设计。

## 前置分类

```text
OneID：不涉及新增解析、归属、Provision 或合并；测试继续使用既有批次行身份数据。
持久化/任务/Provider：不新增任何产品持久化或 Provider 调用。fixture 只回归已有审批路径的 PostgreSQL 收据及既有受控 Provider 读写；不把测试 HTTP 200 表述为外部发送结果。
```

## 组件与参考

前端只复用 Excel V3 workspace 的既有 `data-excel-feedback`、审批按钮和 API 行为；不改 `web/donors/*`。GitHub 参考：Go 的 [`httptest.NewUnstartedServer`](https://github.com/golang/go/blob/master/src/net/http/httptest/server.go) 允许在 `StartTLS` 前配置 handler；Chrome DevTools Protocol 的 [`Network.requestWillBeSent`](https://github.com/ChromeDevTools/devtools-protocol/blob/master/types/protocol-mapping.d.ts) 是本仓现有 raw-CDP journey 已采用的请求开始事件。两者仅用于可控 fixture 闸门和请求计数，不引入新测试框架或依赖。

## 验收

- 延迟内容页时，摘要显示“已排除 1”，审批按钮仍 disabled，且 preview/approve 请求都是零。
- release 后，审批变为 enabled；一次 click 产生恰好一次 preview 和 approve，成功反馈来自 workspace 所有者，数据库仍验证一个任务意图和一条排除行。
- Chromium journey、现有 Excel DOM pagination suite、Go test/race/vet 的实际 exit 分开记录；真实 PostgreSQL fixture 使用独立 localhost 数据目录、空数据库与显式 DSN。未执行或 skip 不算执行证据。
- 源码修复、合并、部署和线上回读分别记录；定向测试通过不等于线上验收完成。
