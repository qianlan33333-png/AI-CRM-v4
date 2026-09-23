# PR08 运营周期完整闭环复核

## 结论

PR08 在 PR10 的唯一 admin shell 中装配冻结 donor 前端链，并通过最小 v3 宿主
Adapter 提供真实读写。`cycles.html` 与 `cyclesDetail.html` 仍与 donor
`6bfbe5816bb89913c70adaca87d6a486260e016e` 逐字节一致；Adapter 不渲染控件、不注入
样式、不修改文案，只在 donor 主按钮触发前把现有交互绑定到 operationcycle-owned 的
typed Admin API。

OneID：**不涉及**。策略、阶段、版本、run 和 action 都是本地运营事实，不读取或写入
customer、external identity、受众、订单、会员或权益数据。

持久化：**涉及本域 PostgreSQL UoW**。策略当前投影、不可变策略版本、不可变 run 快照、
管理员幂等收据、审计与 outbox 在同一事务提交。该板块不创建异步任务、不调用 Provider，
也不虚接 External Effects；未来如新增外部写必须另行授权并通过 outbound。

## 冻结前端和宿主边界

- exact allowlist 只有 `admin/templates/cycles.html` 和
  `admin/templates/cyclesDetail.html`；CI 逐文件 SHA-256 与 `cmp` 校验。
- 继续复用 PR01 已冻结的 `main.ts -> legacy.ts -> AdminController` 链和 PR10 单侧栏；
  不复制第二套 shell、renderer、导航或样式。
- v3 `operationCyclesAdapter.ts` 在 donor 入口加载前安装 typed `loadDb`，并在捕获阶段接管
  donor 已有主按钮。它只调用后端 Adapter；donor HTML、TypeScript、CSS、图标、文案、
  布局和交互外观均不改。
- `view_progress` 保持 donor 的详情跳转外观；display ordinal 只用于解析当前已加载的本地
  strategy/run key，不作为持久化业务身份。缺少 run 时 fail closed。
- CSP 仅对 `/admin/operation-cycles` 及其 `/` 子路径放开冻结模板所需 inline style；相邻
  前缀、API 和静态资源路径不放开，`script-src` 不变。

```text
v3 session + CSRF + role gate
        -> PR10 single admin shell
        -> byte-exact cycles / cyclesDetail donor fragments
        -> minimal v3 host Adapter
        -> typed operationcycle Admin API
        -> operationcycle PostgreSQL owner tables + receipt + audit + outbox
```

## 管理员功能闭环

Admin API 提供以下 typed 操作，写请求均要求 v3 session、管理员角色、CSRF 与
`Idempotency-Key`：

- 创建策略并保存阶段、调度、指示色与 donor 已有主操作类型；
- 用 `expected_version` CAS 编辑策略与阶段定义；
- draft/paused/active/archived 状态流转与版本递增；
- 列表、详情、策略不可变版本历史和 run 不可变快照历史读取；
- donor “开始复盘”按钮提交真实本地 action request，稳定原键重试，不把 queued 冒充
  execution 或 Provider 成功。

定义 DTO 使用字段白名单并拒绝任意 JSON、未知字段、非法颜色、非法主操作和数据库不接受
的 strategy key。管理员幂等键按 actor 隔离：相同 key 与相同 payload 返回原响应，不重复
版本、审计或 outbox；相同 key 的 payload drift 返回 conflict。

## 不可变历史和并发规则

- `operation_cycle_strategies` 与 `operation_cycle_runs` 只作为最新读取投影。
- 每次管理员创建、编辑或状态流转都向
  `operation_cycle_strategy_versions(strategy_key, version)` 写入不可变行。
- runner report 向
  `operation_cycle_run_versions(run_key, snapshot_revision)` 写入不可变快照；旧 revision 不得
  覆盖 current projection，相同 revision 的语义漂移导致整事务 conflict。
- migration `0023_operation_cycle_admin_history.sql` 是 additive，并从现有 current 表回填一份
  基线历史，兼容上一 binary/static release。
- donor 数字详情 id 通过 `operation_cycle_run_ordinals` 一次性映射到不可变 run key；并发
  新增或 `updated_at` 排序变化不会让既有链接打开另一条档案。
- 所有 Store 方法只访问 operationcycle owner 表；网络调用不在事务内，且本轮没有网络调用。

## 验证门槛

- PostgreSQL Journey：创建、原键重放、payload drift、编辑、陈旧 CAS、启用、暂停、重建
  Service 后刷新、四个不可变版本，以及 receipt/audit/outbox 同数同事务。
- run Journey：revision 1/2 均保留，相同 revision 漂移被拒，current 只保留 revision 2。
- HTTP：未登录、错误 principal kind、viewer 只读、admin/superadmin 写、CSRF、
  unknown-field DTO 拒绝。
- Browser Journey：加载 byte-frozen 列表，点击 donor 原主按钮两次，验证真实 action URL、
  DTO、CSRF、稳定幂等键和受理反馈；详情页只查稳定 ordinal，在并发新增/重排后仍打开
  原 run；不出现原 blocked 提示。
- CSP contract：列表页和详情页允许冻结 inline style，近似前缀/API/assets 不允许。
- donor freeze：2/2 exact，活动集合没有新增 donor 文件。

## 明确排除

问卷、雷达、客户/OneID、客户标签、受众包、Campaign、订单支付权益会员、具体客户企微
打标、recipient 执行、实际群发/发送回执、V1 static-history import、mock/sessionStorage 和
任何 Provider 写均不在 PR08。`outcome_unknown` 只保留本地终态证据，禁止换幂等键盲重试。
