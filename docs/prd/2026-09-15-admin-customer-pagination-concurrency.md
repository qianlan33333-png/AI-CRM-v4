# 管理后台客户列表：并发分页与筛选选择范围修复

**状态：** 已批准实施（窄修复）  
**基线：** `e416d0c3e11703b887c55b7caf96f89e8ca55595`（`origin/main`，PR #282）  
**用户可见入口：** `GET /admin/customers`  
**实现边界：** `internal/webshell/static/admin_console/admin_customers.js` 与它的浏览器契约测试

## 目标与业务判断

管理员在客户列表快速变更筛选、刷新或翻页时，页面必须只呈现最后一次操作对应的客户页、总数和分页按钮状态。跨页勾选客户仍是既有批量标签操作合同的一部分；只有实际筛选边界改变时才清除该选择，防止先前筛选下不可见的目标混入新的批量标签预览。

这不是权限扩大已被证实的结论。现有批量命令仍经既有预览、确认、CSRF 和服务端权限检查；风险是前端保留了用户当前不可见的历史勾选目标，容易让管理员误以为本次只针对当前筛选结果。

### 架构分类

| 检查项 | 结论 |
| --- | --- |
| OneID / 外部身份 | 不涉及身份解析、建客、关联或合并。列表继续只读既有 canonical `customers.id` / OneID 投影。 |
| 持久化 / 内部持久任务 | 不新增持久化、队列、Worker、重试或缓存。仅取消浏览器正在进行的无状态读请求。 |
| Provider 读写 / 外部效果 | 不涉及。批量标签命令的既有真实权限、预览与回读合同保持不变。 |
| 授权边界 | 不扩读取或标签权限；前端不记录手机号、OneID 或其他 PII。 |

## 已确认的源码证据

### 实际页面与调用链

`internal/webshell/renderer.go` 将 `/admin/customers` 解析到 `admin_customers` 模板；`internal/webshell/templates/admin_base.html` 加载 `admin_customers.js`。Composition Root 挂载 `/api/admin/customers`，因此本 PR 修改真实 WebShell 页面而不是 `web/src/admin/pages/customers.ts` 中另一套后台 bundle。

当前 `loadList(cursor, navigation)` 在 `admin_customers.js` 中直接发起请求，并在任意响应返回后改写：

- 列表行与勾选框；
- 总数提示；
- `nextCursor`、`pageIndex` 和 `pageCursors`；
- 上一页、下一页的可见性；
- 加载或错误状态。

该函数没有 `AbortController`、单调请求编号或请求快照。因此 A 请求先发、B 请求后发且 B 先返回时，A 的晚到响应仍会覆盖 B 的 DOM 与分页状态。该结论是可由源码和新增可控乱序浏览器契约复现的静态缺陷；尚未把它表述为生产线上已复现的网络时序。

同一脚本以全局 `selectedCustomers` 保存跨页勾选（这是既有产品合同），但筛选提交与“清空”都没有比较或清空该集合。一个新的筛选结果因而可隐藏早先选中的客户，却仍使其进入批量标签预览。

### 现有分页合同，必须保留

服务端 `internal/customer/app/directory.go` 使用签名游标，其中含固定 watermark、`updated_at/customer_id` 的 keyset 位置和筛选 hash；游标与筛选不匹配会拒绝。`internal/customer/store/postgres.go` 用稳定的 `updated_at DESC, customer_id DESC` 排序和 tuple seek 查询。对应页面索引已由 `migrations/0009_customer_activation.sql` 建立。

`refresh` 当前以 `pageCursors[pageIndex]` 重读当前页；它不应重置到首页或改变筛选。`next` 使用服务端给出的 `next_cursor`，而不是 offset 或前端一次性加载全部客户。该行为保持不变。

每页还会执行受限总数读取。这是可见的重复服务端工作，但没有线上时序或安全 `EXPLAIN` 证据证明它是当前瓶颈。本 PR 不添加索引、不改总数语义、不引入缓存；后续只在浏览器网络证据和安全环境测量确认后另立缺陷。

## 范围

### 本 PR 包含

1. 为一次列表读取建立单调 request ID，并捕获该次的筛选、cursor、导航和页状态快照。
2. 发起新列表读取时 abort 上一个列表读取以节约浏览器和网络资源。
3. 仅当 request ID 仍是最新时才修改列表 DOM、总数、cursor、页码、分页按钮和 busy 状态。旧请求的成功、失败和 `finally` 都不能影响新请求。
4. 载入期间禁用刷新、上一页、下一页，避免用即将失效的游标重复导航；筛选提交和清空保持可用，以便它们发起更晚且优先的读取。
5. 当且仅当新一次 `reset` 的规范化筛选参数与当前最新筛选边界不同，清除跨页 `selectedCustomers`。同筛选再次查询、刷新、上一页、下一页都保留选择。
6. 在既有 jsdom 浏览器契约中加入可控乱序、abort 后晚到响应、失败后重试、跨页选择、同筛选刷新和筛选变化的覆盖。

### 明确排除

- 不改变 `/api/admin/customers` 的 keyset cursor、固定 watermark、排序、limit、总数或错误合同。
- 不以 offset、全量加载、客户端切片替换现有分页。
- 不新增数据库索引、缓存、队列、后台任务或 Provider 调用。
- 不改批量标签权限、预览/确认/回读合同，也不隐藏“能力未就绪”或 Provider 不可用提示来冒充功能完成。
- 不修改冻结 donor、`web/src/admin` 的非实际页面 bundle，或 HXC 仪表盘。

## 交互与状态规则

### 筛选边界

`queryFromForm()` 已按固定字段顺序产生 `keyword`、`phone`、`status` 和 `limit=50`。`reset` 的候选 query 不含 cursor，并与最近一次已发起的列表筛选边界比较：

- 参数不同：立即清除跨页选择；后续响应只会呈现新筛选的行。
- 参数相同：保留选择，包括“查询”重复提交、清空已经为空的表单、刷新和翻页。

此比较在请求发起时完成，不能等待响应，否则两个连续筛选请求乱序返回时会错误恢复旧选择。

### 请求顺序与分页

每次 `loadList` 先计算 query/cursor/navigation 的本地快照，再递增 request ID。客户端同时区分“最近发起的读取目标”和“最后成功渲染页”：响应成功前不提交页码、页游标或该成功页的 query。收到最新成功响应时才提交对应导航的页状态、渲染列表、更新总数、`nextCursor` 和 committed query。

新请求调用 `AbortController.abort()` 取消前一个请求；但 abort 只减少资源消耗，不能作为正确性前提。测试中的 fetch 会在 abort 后故意晚到返回，仍必须被 request ID 忽略。

最新请求失败时保留现有中文错误分类。客户端把最近一次已发起读取的完整规范化 filter、cursor 和导航意图作为一个原子重试目标；刷新不会读取表单中尚未提交的值，也不会把新 filter 与旧页 cursor 组合。若失败的 reset 改变了 filter，则最后成功页属于旧 query，刷新保留可用以重试新目标，而上一页、下一页保持禁用直到新 query 有成功页。被新请求取代或被 abort 的旧请求既不显示错误，也不解除新请求的 busy 状态。

### 保留的批量选择合同

选择仍以 canonical customer ID 存入现有集合，可跨稳定筛选范围的多页积累。批量提交仍发送该集合至既有预览接口，之后才由服务端执行现有权限和外部效果流程。本 PR 不假设客户端选择是授权依据。

## 前端一致性

此处使用既有 WebShell `admin_base`、`admin_customers` 模板、标准 `admin-button` 和 `admin-state`，只为已有按钮增加原生 busy/disabled 状态。没有新页面、组件、样式或设计变更；不修改冻结 donor。页面路径和实际模板已先于实现确认。

## 参考与取舍

- 仓库 PR #248 以有界页面、总数与明确不可用状态替代全量/N+1 读取；本页已具备服务端 keyset 分页，故只借鉴“保留诚实状态与有界读取”的取舍，不移植其 offset 实现。
- 仓库 PR #237 是私有静态资源的 content-hash 缓存优化，不能解决本页 API 响应竞态，故不纳入。
- [MDN AbortController](https://developer.mozilla.org/en-US/docs/Web/API/AbortController) 说明 fetch 可被取消；本设计另加 request ID，处理取消后仍返回的竞态。
- [GitHub REST pagination guidance](https://docs.github.com/en/rest/using-the-rest-api/using-pagination-in-the-rest-api) 支持逐页跟随下一游标/链接而非取回全量；本页维持既有服务端 cursor。

## 验收与回归白名单

| 场景 | 预期 |
| --- | --- |
| A 筛选后 B 筛选，B 先返回 | 只显示 B 的行、总数与翻页按钮；A 晚到不得覆盖。 |
| A 被 abort 后仍晚到成功或失败 | A 不改变 DOM、页码、cursor、错误或 busy。 |
| 当前请求失败再刷新 | 显示原有可重试中文错误；刷新以同一已发起 filter/cursor/导航意图重试，不读取未提交表单字段或混用旧 cursor。 |
| 第 2 页提交新筛选但 reset 失败 | 不展示旧筛选为新结果；上一页、下一页保持禁用；修改未提交字段后刷新仍以新筛选首页重试。 |
| 同筛选重复查询、刷新、翻页 | 跨页勾选保持；刷新仍重读当前 cursor。 |
| 实际筛选变化或从非空筛选清空 | 历史勾选清除，新的批量预览不含隐藏旧目标。 |
| 列表为空 | 保持既有空态文案与无下一页状态。 |
| 权限 / 批量标签 | 既有 preview、CSRF、权限错误和结果回读契约通过。 |

定向验证：

```text
node --check internal/webshell/static/admin_console/admin_customers.js
node internal/webshell/static/admin_console/admin_customers.test.mjs
GOWORK=off go test ./internal/webshell -run TestAdminCustomersBrowserMessageArchiveEntry -count=1
```

浏览器线上验收仍待独立 UI 审计提供实际路由、请求时序和截图；该证据与本源码缺陷、定向契约测试分开记录。源码修复、合并、部署和线上回读分别记录，定向测试通过不等于线上验收完成。
