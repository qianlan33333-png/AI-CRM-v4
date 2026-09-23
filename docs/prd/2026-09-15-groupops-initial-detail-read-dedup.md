# GroupOps 计划详情：合并首次 hydration 的重复读取

**状态：** 已审方案，实施完成后待 root 差异复审
**开发基线：** `f13f65b79659309555a0653321f7c52a02893270`（fresh `origin/main` worktree）
**用户入口：** `/admin/groupops.html` 的计划详情（例如计划 #14）

## 业务判断与源码证据

页面详情的现有标准控制器在 `loadDetailPage()` 首阶段并行发出 `apiPlan(id)`、`apiPlanGroups(id)` 和负责人目录读取；取得计划后，第二阶段独立读取当前 owner 过滤的群目录，并按计划类型读取 Webhook 描述或节点。owner 目录和 Webhook 描述各自有独立业务语义，不能与绑定群名称/摘要读取合并。

重复来自 V3 Host 的 DTO 组合：

1. `apiPlan(id)` 进入 `groupOpsHostAdapter.requestJson()` 的详情分支，先读一次 `detail(id)`，再由 `summary(id)` → `groupsForPlan(id)` 读第二次同一详情和一次无 owner 群目录。
2. 同一 hydration 的 `apiPlanGroups(id)` 进入 groups GET 分支，`groupsForPlan(id)` 又读第三次同一详情和第二次无 owner 群目录。
3. 第二阶段 `/groups?owner_userid=<owner>` 是群选择器的 owner 过滤目录，必须继续读取；Webhook 计划的 `/webhook` 继续读取既有 descriptor。

因此，初次详情的三个计划 GET 和两个无 owner 群目录 GET 都是相同的 Host 组合读取，不是三个用户动作，也不能据此断言后端执行了三次相同 SQL。只读审计的路径/状态样本与此链路一致；它不含响应体、鉴权信息或线上性能结论。

仓库与 GitHub 参考已核对。当前仓库没有可复用的 GroupOps hydration 协调器；`gh search code '"createSharedPromise" language:TypeScript'` 仅找到通用 shared-promise 模式，不能提供本项目的计划版本、写后回读或 owner 目录语义。故本 PR 使用 adapter 内部、按计划 ID 且只存活到当前两条首次读取结束的 promise lease，不引入依赖、TTL、全局缓存或后台任务。

## 架构分类

| 判断                | 结论                                                                                                                                  |
| ------------------- | ------------------------------------------------------------------------------------------------------------------------------------- |
| OneID / 外部身份    | 不涉及。只复用现有计划、群投影和 owner 过滤读取，不解析、匹配、建客或扩展身份权限。                                                   |
| 持久化              | 不新增或改变持久化、CAS、幂等收据、事务或迁移。                                                                                       |
| Provider / 外部效果 | 不新增 Provider read/write、发送、队列、worker 或 Outbox。保留现有 owner 过滤群目录、Webhook descriptor 与 `groups/sync` 的既有合同。 |
| 权限                | 不改 Admin 授权、CSRF 或现有请求路径。                                                                                                |

## 最小范围

1. 只在 V3-owned `web/v3/groupOpsHostAdapter.ts` 为**同一次** `loadDetailPage()` 的 `apiPlan(id)` 与 `apiPlanGroups(id)` 建立按计划 ID 的短生命周期 epoch lease。一个 epoch 最多接受一个 `apiPlan` claim 和一个 `apiPlanGroups` claim；同类路由第二次出现、或本轮同步配对窗口已关闭时，必须创建并发布新 epoch，不能加入旧 pending promise。
2. epoch 启动一个 `detail(id)` 与一个无 owner `directory()` promise，并惰性建立唯一的绑定群 projection promise。两个已配对的初始路由共享这三者；`apiPlan` 直接复用同一 projection，不再为摘要再读详情，也不会把 donor 的回读 view 指向另一份群数组。配对只发生在冻结 `Promise.all` 同步发起的这一轮；后续 standalone GET、刷新、换计划或重入详情不会复用该 epoch。
3. 每条 claim 在 `finally` 释放。最后一个 claim 仅当 plan-ID map 仍指向**自身 epoch**时才删除它；旧 epoch 的 finally 永远不能删掉 A→B→A 中新的 A。不得保留已解析 DTO、设置 TTL、跨详情导航复用或后台预取。
4. epoch 的 `isCurrent`/generation guard 覆盖全部 Host 发布点：`plan()` 写入 revision、`planSummaryViews`、`planGroupViews` 以及 groups/summary 合并。旧 epoch 的迟到成功或失败可以结束自己的调用，但不能改 revision、共享 view 或污染新 readback。
5. 保存、绑定、移除、启用、停用、节点/Webhook 变更以及 `groups/sync` 在写或同步前只使对应 epoch 失效并推进 generation；其后原有强制读回必须重新发起详情/无 owner 群目录读取，绝不复用先前 DTO 或已完成 promise。保留既有 409 清 revision 与后续权威回读合同，不改变重试流程。普通 `groupsForPlan()` 也自行读取。
6. 保留 owner 过滤群目录、负责人目录、Webhook descriptor/节点读取、列表摘要和 #289 的保存锁、失败反馈、读回 generation 合同。

## 明确排除

- 不修改 `web/v3/groupOpsStandard.js`、后端 GroupOps API、数据库、分页、SQL、Provider adapter、队列、Outbox 或权限。
- 不移除 owner 过滤 `/groups?owner_userid=...`、负责人目录或 Webhook/节点请求。
- 不新增长期缓存、TTL、跨路由 snapshot、轮询、预取、取消协议或第二套任务。
- 不处理客户目录四个 picker 的首屏资源问题；那是独立领域和后续 PRD。

## 兼容性与状态边界

PR #289 的 `groupOpsStandard.js` 保存可靠性修复仍保持单写锁和“写后权威详情回读”的合同。本 PR 不触碰该控制器；若 #289 先合入，最终分支必须 rebase 并保留其全部行为与测试。

首次 hydration 只能共享同一同步启动轮中的一个 `apiPlan` 和一个 `apiPlanGroups` claim。仅按计划 ID 或 pending 数量不足以证明调用属于同一轮：A 的旧加载尚未完成时，刷新 A、切 B、再切回 A 都必须得到新 epoch；同类第二个请求不得接入旧 epoch。每个旧 finally 先比对 map identity，避免删除新 A；旧 response/reject 还须通过当前 epoch/generation gate，不能改 revision、`planSummaryViews` 或 `planGroupViews`。

审计目标 #14 是 Webhook 计划，因此第二阶段维持独立 descriptor GET，首轮可验证为一条详情和一条无 owner 群目录。标准计划第二阶段的 `apiPlanNodes` 仍按既有节点 DTO 路径读取详情；该节点语义不进入本 PR 的两 claim lease，也不把已解析详情留作跨阶段 snapshot。故本 PR 对标准计划不宣称将节点读取合并为同一请求。

任何后续详情加载、用户刷新、失败重试、写后重新打开详情或 standalone GET 都必须建立新 lease。共享读取出错时，两条当前 hydration 请求按既有错误路径失败；lease 必须清理，下一次重试才可重新请求。没有已完成数据缓存，所以强制回读不会被旧结果伪装为最新配置。

## 验收

扩展已注册到 canonical frontend consumer chain 的 `scripts/groupops-host-adapter-e2e.mjs`，用真实冻结 GroupOps 标准 DOM 与 V3 Host 共同挂载：

| 场景                                   | 预期                                                                                                                                                      |
| -------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Webhook 首次详情 hydration（审计 #14） | `apiPlan` 与 `apiPlanGroups` 合计只请求一次 `/plans/{id}` 和一次无 owner `/groups?limit=200&offset=0`；计划、绑定群和摘要仍正确渲染。                     |
| 语义独立读取                           | owner 过滤群目录、负责人目录和 Webhook descriptor 各按原路径读取，未被共享 lease 吞掉；标准计划 nodes 保持既有独立详情 DTO 读取。                         |
| 出错与重试                             | 共享详情或无 owner 目录失败后不留下已完成/失败 snapshot；下一次详情重试发起新 GET，错误与现有重试展示保持真实。                                           |
| A→刷新 A→B→A 乱序                      | 至多一对同轮 route 能共享 epoch；同类重入创建新 epoch。旧 A success/reject/finally 不能删除新 A、覆盖其 revision/summary/groups view 或污染随后强制读回。 |
| CAS 与写后读回                          | 保存、绑定、移除、启停和群同步后，强制读回使用新 GET，不复用初始 hydration 的 detail/directory；保留既有 409 清 revision 与后续权威回读合同，不改变重试流程；#289 的单写、失败草稿保留和读回失败提示继续通过。 |
| 非初始路径                             | 列表摘要、普通群读取、归档只读和无 owner/无 Webhook 分支不被误合并为详情 snapshot。                                                                       |

定向验证计划：`node scripts/groupops-host-adapter-e2e.mjs`、`npm run typecheck`、`node scripts/verify-materialized-donor-source-views.mjs`、`npm run ui:shell:contract` 与 `git diff --check`。隔离 fixture 的请求计数仅证明源码组合读取已收敛；线上自然导航时序、数据库耗时和 Provider 事实必须在合并/部署后分别回读。
