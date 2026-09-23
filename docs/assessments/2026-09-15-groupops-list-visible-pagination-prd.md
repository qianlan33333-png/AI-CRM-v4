# PRD-B：GroupOps 计划列表的 50 条可见 offset 分页

- 状态：root 已批准，实施中；未提交、未推送。
- 基线：A authority commit `a5d166603d3c3be05f4e620cb393bb849df50ab7`（功能基线 `11266eddddbe4554fbec4edf14f7ebdd482ad4e5`）；工作树 `/private/tmp/aicrm-groupops-list-visible-pagination-20260915`。
- 实施前置：PRD-A 的 `bound_group_count` 功能与 authority 提交已获 root 批准；本分支以该提交为基础。此 PRD 不改 A 的 Store/ListDTO。
- 业务分类：不涉及 OneID、持久化或持久任务、Provider 读写、外部效果和权限扩展。复用现有同领域 `GET /plans?limit=&offset=` 与既有 CAS 写入；不建 cursor、缓存、队列、API、索引或后台任务。

## 目标与已证实合同

后台运营人员可逐页查看计划，每页固定 50 条，并看到服务端权威总数和清晰的本页指标。当前 API 已支持 offset：`PlanPage` 返 `total`、`limit`、`offset`、`has_more`，Service 上限 100、固定排序 `updated_at DESC,id DESC`；HTTP 仅解析 `limit/offset`，没有 cursor。当前 UI 固定请求无 query、没有页状态或按钮；Host 的列表分支又只精确匹配无 query URL，且 `nativeRequest` 未透传 `AbortSignal`。因此现状只读取默认首窗口，不能安全翻页。

行按钮还必须以用户实际看见的行版本执行 CAS。原 PRD-B 提议“不把 list revision 缓存、写前 fresh detail”的方案会把用户看见 revision 50 的行改用服务器后来返回的 51，破坏“我对当前所见行操作”的冲突检测；翻页请求失败而旧行仍显示时尤其错误。现有 Host 通常会缓存列表的 50；本修订不把该设计反例冒充为现状缺陷，而是明确禁止以 fresh read 改写已见行的写意图。

证据：`internal/groupops/port/port.go:42-70,159-165`、`internal/groupops/app/service.go:82-102,396-429`、`internal/groupops/http/handler.go:667-715,1346-1354`、`web/v3/groupOpsStandard.js:421-424,528-561,781-858`、`web/v3/groupOpsHostAdapter.ts:195-214,262-267,375-383`。GitHub code search 未找到可直接采用的远端页面实现；复用本仓 `web/v3/radarAdapter.ts` 的 `AbortController`、generation、请求快照与 finally 守卫合同，以及 `web/v3/excelBatches.ts` 的既有 admin-pagination 呈现，不搬运外部代码。

Product Design audit 路由已用于既有 GroupOps 列表的交互和组件复用审计。本轮是 PRD 修订，未声称有登录态浏览器截图；实现后的真实 Host 浏览器验收仍单列为门槛，不在本 PRD 中冒充完成。

## 范围和交互

1. 列表 URL 只构造 `limit=50&offset=<0..1000000>`；Host 以 URL pathname 判定 `GET /plans`，保留 query 原样交给现有后端并映射返回 DTO。不得把 offset 伪装成 cursor，也不得在前端把多页拼成全量。
2. 复用现有 GroupOps 表格、`group-ops__metric-grid`、`actionButton` 和 `admin-pagination`：显示“第 x–y 项，共 total 项”、上一页、下一页；首页禁用上一页，`has_more=false` 禁用下一页。按钮含 aria-label，busy 时两个翻页按钮与会改变列表的操作禁用。
3. “运营计划”仍表示 server `total`；绑定群、通知排队和今日预估只从当前 `items` 聚合，标签明确为“本页已绑定群”“本页通知排队”“本页今日预估”。不得把本页值冒充全局值；`bound_group_count` 缺失仍显示“—”。
4. 当前没有计划列表筛选或选择/批量操作，故没有伪造筛选 reset、跨页选择或全局选择合同。未来筛选必须单列 PRD，并在实际已提交筛选改变时重置 `offset=0`。
5. “删除”在此 UI 是归档：确认文案明确“归档后仍保留在列表”，归档终态行没有可写按钮；不借此重做样式或文案体系。

## 读状态、DTO 与竞态合同

- 页面读取持有不可变快照 `{limit:50, offset}`、`AbortController`、单调 `generation` 和 `retrySnapshot`。新页面、重试或写后回读先 abort 旧读并递增 generation；Abort 仅减负，success、error、finally 都必须以 generation 匹配才更新 rows、total、offset、has_more、busy、错误或 retry。
- 扩展 V3 Host 的 `nativeRequest`，将 `options.signal` 作为 Fetch `RequestInit.signal` 传递，不序列化进 body。列表映射在 abort 或 generation 已过期后不得发布结果。
- 列表页响应必须验证 `items` 数组、`total` 非负安全整数、`limit=50`、`offset` 与快照相等、`has_more` 为布尔、items 不超过 limit，以及 A 提供的每项绑定数合同。每个可渲染行还必须有正安全整数 `id` 与 `revision`；缺失或非法 revision 使该页成为 DTO 错误，不能给行按钮猜测/补取版本。缺字段、非法页或 HTTP/权限错误走可见 `role=alert` 错误，不清零成空列表。
- 失败只保存这一次的 snapshot；“重新读取当前页”严格复发原 `limit/offset`，不读取任何临时表单。若已经有成功页，保留该页并明确提示目标页读取失败；禁用翻页直到重试成功，避免把旧 rows 当成新页。被 abort 或已过期的失败不提示。读取为 401/403 时清除现有 rows、total、page/retry 和可写状态，渲染无权错误并禁用行写按钮，不能继续展示或操作已经失权的数据。
- 列表映射必须是 revision 纯函数：列表成功仅把已验证 revision 随 `state.plans` 的该次成功快照保存，绝不写 Host 的全局 `revisions`。迟到、abort 或已过期页既不能覆盖 rows，也不能污染任何可写版本缓存。详情读取和详情内既有写操作仍可沿用当前 `revision(id)` 路径。

## 已见行 CAS、写后回读与归档

1. `enable`、`disable`、`archive` 的每个列表按钮都从该次成功渲染的不可变行快照/闭包绑定 ID 与 revision（HTML 表达可为 `data-plan-id` + `data-plan-revision`，事件处理时先构成不可变 local action）。确认弹窗期间即使列表重绘，也必须继续使用该 local action 的 ID + revision，不能回查 `state.plans` 取得新行对象或新版 revision。事件处理器只接受正安全整数，并将它原样作为本地 action 的 `expected_revision` 传给 `requestJson`。行不存在、revision 缺失/非法或列表 busy 时不发写，提示重新读取当前页。
2. Host 仅对这三个列表 action 支持显式 `expected_revision`：显式字段存在但不是正安全整数时，立刻报错且零写；显式字段合法时原样转发给现有 HTTP 字段，绝不调用 `revision(id)`、绝不从 `revisions` 取值或用响应覆盖该值。仅在调用方完全缺省该字段的既有详情入口，才沿用当前 `revision(id)` 路径，避免扩大本 PR 的详情行为。
3. 因此反例必须成立：A 行以 revision 50 成功渲染；服务端变为 51；用户去第二页但读取失败，页面保留 A；点击 A 必须发送 50 并得到既有 409，而不是先读详情并写 51。冲突后允许只读详情/当前页以展示最新状态，但不得自动重发原写。
4. 写入与列表读是两个结果阶段。启用/停用/归档返回状态确认后写已执行；随后以当前 offset 做权威 GET。该 GET 成功才更新列表并显示“已启用/已停用/已归档”。若该 GET 失败，保留最近成功页，显示“操作已执行，但当前页未更新；重新读取当前页”，且 retry 只重发该 GET，绝不重发 POST/DELETE；不得把它改报成写失败。该 readback 未成功前，锁定这次写对应旧行的所有写按钮，避免用户再基于旧快照发送同类写；权威 GET 成功替换该行或 401/403 清理它后才解除。
5. 写本身失败（包括 409）维持既有详情+当前页只读回读用于解释冲突；没有自动写重试。只读回读失败时保留写失败信息和可见 GET retry。
6. `DELETE → application.Archive` 不物理删除计划或绑定，List 未按状态过滤，归档行通常仍出现且 `total` 通常不减。绝不先乐观移除行或假定总数减少。每次归档后同 offset 权威回读；若服务端因并发物理删除、数据变化或未来过滤返回 `items=[]`、`offset>0` 且 `total<=offset`，仅自动退一页到 `max(0, offset-50)` 并读取一次；第二次仍空显示服务端空态，不循环。
7. 创建继续既有“创建后进入详情”行为，不在列表乐观插入。详情页启动时的 `GET /plans/{id}` 是创建的权威 readback；详情读取失败必须显示真实错误且不把 POST 回包冒充已展示详情；不为此额外发一次列表读或改变创建幂等合同。返回列表时从 `offset=0` 做权威读取。

## 前端复用、错误与测试

GroupOps 活动 UI 由 `internal/groupops/ui.go` 的 V3 Host 在既有管理壳装配；`groupOpsHostAdapter`、`groupOpsStandard`、`groupOpsStandard.css` 是 `build-v3-host-adapters` 的 V3 entries，PR06 donor archive 继续冻结。只增加现有列表表格下方的 `admin-pagination`、状态/alert 和现有按钮 data 属性，不改 donor 模板、全局 guard 或壳。

定向验证设计：

1. 真实 Host + GroupOps Standard DOM：首/次页请求精确为 `GET /plans?limit=50&offset=0/50` 与既有 operation-members 读；上一页、下一页和页边界；range、每页指标标签和服务器 total。一次列表加载不得有 `/plans/{id}` 或 `/groups`。
2. 503 后重试仍为原 `offset=50`；初始读取失败、权限错误、空页、缺/非法 revision 和其他 DTO 不合法都有可见错误且不显示假 0。旧 `bound_group_count` 缺失为“—”，不回退 N+1。
3. 受控乱序/abort fixture：A 被 abort 后晚 success、late reject、late finally 均不覆盖 B 的 rows/total/busy/retry，也不写 Host `revisions`。A 已见 revision 50、服务端为 51、B 页失败时点击 A 请求必须带 50 并返回 409；预置或详情缓存的 51 不得改变它。
4. 写成功后列表 GET 失败：断言 POST/DELETE 只执行一次，保留旧页，错误明确“已执行但当前页未更新”，该旧行所有写按钮保持锁定；重试仅发原 offset GET，随后 GET 成功才更新行并解除锁。另覆盖写失败/409 没有自动重发写，以及 401/403 读取清理旧数据并禁写。
5. 归档后同页回读保留 archived 行；模拟尾页并发变空时只退一次；创建后详情 GET 成功/失败路径不在列表伪造插入。
6. A 的 PG16 DTO/分页验证、Host DOM、`build-v3-host-adapters`、required donor-consumer 链和 GitHub CI 各自记录。无本地 PG、冻结 donor 或基线失败时仅记录限制，不能称完整验证或线上提速。

## 非目标

不修改 List SQL、PlanPage 后端分页语义、排序、总数计算或生命周期/CAS 规则；不引入筛选、全局统计、跨页选择、无限滚动、cursor、预取、成员目录缓存或 Provider 行为。线上请求数、渲染时点和实际提速仍须在部署后的自然浏览器回读中独立确认。

## 实施补充：写回包确认

现有 Group Ops enable、disable 和 archive handler 都返回 `groupopsport.Detail`，其中含 `plan`。列表操作只有在回包的 `plan_id` 等于被点击行、`revision` 为正安全整数，且状态分别为 `active`、`paused`、`archived` 时才进入权威列表回读。HTTP 200 的空对象、错误计划 ID、非法 revision 或错误状态都显示“结果未确认”，仅做既有只读恢复，绝不把它报告为成功或自动重写。

实现中的 Host 将 `signal` 传给 Fetch，并把非 2xx 的 status/payload 保留给 V3 控制器；列表的 Host 映射严格校验数值 page DTO，但允许 OpenAPI 所定义的 string `plan_id`。行级显式 `expected_revision` 必须是 numeric positive safe integer；`true`、字符串、`undefined` 等显式值在发请求前拒绝。`plan(value, publishRevision = true)` 保留详情路径的 revision 行为，列表使用 `false`，让未渲染或过期页不能污染 CAS 缓存。

截至本地实施阶段，使用固定 Node 24.18.0 运行 `scripts/groupops-host-adapter-e2e.mjs` 与 `npm run typecheck` 均通过。真实 Host + Standard fixture 覆盖 50-offset、准确 retry、初始未知与 403 清理路径（401/403 共享同一状态分支）、页面失败后的 rendered-revision 409、空/错 ID/错状态 200 写回包、归档尾页单次回退、abort 后晚 success/reject/finally，以及初次加载只读取 plans page 和 operation-members。该结果不构成线上请求次数或实际提速的证明；部署后的浏览器回读仍是独立验收门槛。

## 实施收敛：未完成回读锁与既有创建缺口

写入返回成功而当前页权威 GET 失败时，页面保留最近成功页并进入“操作已执行，但当前页未更新”的 GET-only retry。实现将此状态作为全部列表写入的短暂锁：在该 retry 成功、或权限错误清理页面前，不允许另一行 enable、disable 或 archive 覆盖先前未完成的 readback 锁；创建入口也受列表 busy/无权状态约束。fixture 用两行验证 A 的 readback 失败后 B 不会发出第二个写，重试只发 GET，成功刷新后才解锁。

本轮不修复创建动作本身的双击单飞或失败后已填写 name/type 的保留：当前 `createPlan` 缺少自己的 in-flight 锁，且失败重渲染仍使用默认表单值。这是既有创建合同缺口，已登记为 B 后的独立“群运营创建防重复与草稿保留”PRD；不得把本轮 busy/无权入口保护表述为已解决重复创建。

分页 DOM fixture 现为真实 51 项：首页 50 行、末页 1 行，断言 `1–50`、`51–51` 与边界按钮。CAS 反例在保留的 revision 50 行上先执行真实 Host detail 读取，将 Host revision cache 更新为 51，再断言列表显式请求仍携 50。竞态 fixture 分别验证旧请求的 page/member 都晚成功时不改变新 readback 的 rows/busy/finally，以及独立晚拒绝不覆盖已完成的新页。
