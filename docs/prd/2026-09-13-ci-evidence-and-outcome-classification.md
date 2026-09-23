# CI 证据绑定与结果分类 PRD

## 1. 业务判断

### 目标

开发者在本地完成快速检查或编译后，不能把该结果说成完整回归通过；PR
的首轮、重跑和最终状态要能按同一 `head_sha`、`run_id`、`run_attempt`
复核。失败记录要把已执行断言/测试失败、可定位的执行环境准备失败、
取消或未完成分开，但不从日志文本猜测“代码错误”或“网络故障”。

### 用户和可观察结果

开发者获得一个可执行的本地完整验证入口及 JSON 证据。它只会在测试从
干净、已提交的 Git 树开始，所有必需 lane 已执行并成功，且结束时
`HEAD`、tree 和工作区仍完全相同时，写出 `claim: local_full`。快速、
编译和浏览器局部入口仍可用于迭代，但证据固定标记为局部结果。

PR 的 Actions 摘要和 artifact 显示 PR 首轮 attempt（保留当时 head）、
当前 head 的最新 attempt，以及每个 lane 的保守分类和原始 GitHub 状态。
`check` 继续是唯一硬门禁；报告不会让任何失败、取消、跳过或缺环境的 run
变绿。

### 边界和禁止行为

- 不要求开发者在运行 `fast`、`compile` 或专项浏览器检查前清理工作区；
  这些结果只描述开始/结束快照，永不具备 `local_full` 声明。
- `local_full` 不接受未追踪、暂存或未暂存源文件；报告目录必须位于 Git
  工作树外（默认系统临时目录），不能靠忽略用户源文件来绕过检查。
- 本地 macOS 与 Linux CI 的环境不同。完整本地结果记录 OS、arch、
  Go/Node/Python 版本，不记录数据库 URL、Token 或完整环境变量，且不
  声称环境等价。缺 PostgreSQL 16、Chromium、固定工具或供体输入时，结果
  为 `not_verified`，不是跳过后的完整通过。
- 本地完整验证只连接 loopback 的 PostgreSQL 16 `aicrm_ci` 或
  `aicrm_test_*` 数据库；共享库、生产库和远程地址一律拒绝。两个冻结供体
  必须分别处于 CI 权威配置中的固定 SHA 且工作区 clean，空目录不构成前提。
- 不修改业务数据、OneID、任务、Provider 或部署；不新增队列、外部效果或
  数据库迁移；不放宽现有 `check`/部署条件。
- 报告不能把命令返回非零直接写为“代码缺陷”。它只能写
  `assertion_or_verification_failure`，并保留失败 lane、step、exit code 和
  原始 Actions conclusion 供人工判断。

### 架构分类

```text
OneID: not involved — 只读取 Git 与 Actions 元数据，不读取或写入客户身份。
Persistence: stateless — 证据为临时本地文件或 Actions artifact，不写业务表。
External Effects: not involved — 不调用 Provider，也不改变发布或生产配置。
```

## 2. GitHub 参考与采用理由

| 参考 | 采用 | 不采用 |
| --- | --- | --- |
| [GitHub Actions concurrency](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-workflow-concurrency) | 保留当前 PR 同 ref 的取消策略；报告把 `cancelled` 独立列出，而不计入断言失败。官方说明新 run 可以取消同并发组中的旧 run。 | 不取消 main 的编号发布，也不靠关闭取消来伪造更高首次通过率。 |
| [List workflow runs](https://docs.github.com/en/rest/actions/workflow-runs?apiVersion=2022-11-28#list-workflow-runs-for-a-repository) | 通过 `event=pull_request` 和 `head_sha` 查询同一个当前 PR head 的 runs；官方 API 明确提供这些筛选及 `cancelled`、`failure`、`pending` 等状态。 | 不把不同 head 或 main push 的绿灯计为当前 PR 的最终结果。 |
| [Get a workflow run attempt](https://docs.github.com/en/rest/actions/workflow-runs?apiVersion=2022-11-28#get-a-workflow-run-attempt) | 用 `(run_id, attempt_number)` 固定首轮和最新 attempt，支持重跑复核。 | 不只按 run 名称或日志时间猜测重跑归属。 |

本仓 `gh api` 在设计时返回连接 EOF，故未将一次不可用 API 查询当作
参考证据；以上为 GitHub 官方 Actions 文档。实现中 API 不可用时输出
`history: unknown`，不会编造统计。

## 3. 现状与问题

`scripts/dev_preflight.py` 当前仅在报告开头记录 `HEAD` 和工作区状态；
`fast` 运行格式、边界、差异和冻结检查，`compile` 只执行
`go test -run '^$'`。它们没有完整声明，也没有结束快照。CI 则并行执行
preflight、PostgreSQL/race、前端、Chromium 和 SDK lane，并由
`scripts/ci/verification.py gate` 保持严格成功要求。

因此，本地快速证据和 CI 完整证据是不同契约。后续改动必须复用同一组
lane 命令，不能维护第二份会漂移的“本地完整 CI”。

## 4. 交付设计

### 4.1 单一 lane 命令定义

新增受版本控制的 `scripts/ci/quality-lane`（或等价的 Python 入口），其
参数是 `preflight`、`backend`、`frontend`、`browser`、`archive-sdk`。
每个 lane 内封装现有 CI 的**检查命令本身**；Actions 在完成现有 pinned
setup、PostgreSQL service 和 Chromium 安装后调用它。`local-full` 以相同
顺序调用同一入口，绝不复制命令清单。

环境准备仍属于平台职责，而不是把 GitHub runner 安装脚本复制到本机：
本地入口先验证每个 lane 的前提。任何缺失前提、空 Chromium 集合、skip、
非零退出或中途 source drift 都生成失败/未验证证据，禁止产生完整声明。
`workflow_dispatch` 没有 `event.before` 时，共享 preflight 明确复用既有
去重脚本规则，以检出的 `HEAD^` 作为基线；不是把该检查跳过。

### 4.2 本地证据协议

`dev_preflight.py` 增加 `full` phase（或调用同一独立入口），并把所有
phase 的 JSON schema 升级为以下语义：

```json
{
  "schema": 2,
  "claim": "fast | compile | browser | local_full | not_verified",
  "eligible_for_delivery": false,
  "source": {
    "start": {"head": "40-hex", "tree": "40-hex", "status": []},
    "end": {"head": "40-hex", "tree": "40-hex", "status": []},
    "unchanged_during_execution": true
  },
  "environment": {"os": "…", "arch": "…", "go": "…", "node": "…", "python": "…"},
  "lanes": [{"name": "backend", "result": "success", "command": ["…"], "exit_code": 0}]
}
```

`fast`、`compile`、`browser` 的 `eligible_for_delivery` 永远为 `false`。
只有 `full` 同时满足开始与结束 status 均为空、HEAD/tree 未变、五个 lane
均为 `success`、每个环境前提明确满足时，才写
`claim: local_full` 和 `eligible_for_delivery: true`。报告目录不在 source
tree，状态扫描使用 `git status --porcelain=v1 --untracked-files=all`，不
允许通过 path 排除源文件。

### 4.3 CI 结果收据与分类

每个 CI lane 使用有 ID 的 `setup` 和 `verification` step；一个
`if: always()` 的收据步骤以结构化 step outcome 写入临时 JSON，再上传
lane artifact。分类器只使用这些字段及 job conclusion：

| 分类 | 结构化触发 | 含义 |
| --- | --- | --- |
| `success` | 所有所需 setup 和 verification step 成功 | 此 lane 通过。 |
| `assertion_or_verification_failure` | setup 成功且实际检查命令失败 | 测试、合同、构建或断言没有通过；不推断根因。 |
| `environment_setup_failure` | 被标记 setup 的 checkout/action/tool/dependency step 失败 | 执行环境或依赖准备失败；保留具体 step，不声称是临时网络故障。 |
| `cancelled` | run/job/step 明确为 cancelled | 由并发取消或人工取消等造成，单列。 |
| `pending_or_incomplete` | queued/pending/in_progress/required step 未产生终态 | 尚无结果，不能进入通过分母。 |
| `unknown_failure` | completed failure 但没有完整结构化收据 | 保守未知失败，仍失败，要求查看原始 log。 |

`quality-report` 仅使用 `pull_request` 工作流和最小 `actions: read`、
`pull-requests: read`、`contents: read` 权限；不使用 `workflow_run`，不
使用 secrets、写权限或来自 fork head 的高权限事件。它在 `if: always()`
下读取 `needs` JSON 和同一 workflow 的 run metadata，上传只读结果
artifact，并不成为 `check` 的 prerequisite。

报告 job 本身运行时 workflow lifecycle 仍可能是 `in_progress`。因此报告
分开写入已完成的 `check`/lane verification 结果与 workflow lifecycle，绝不
把“check 已通过”伪称成“整个 workflow 已完成”。

### 4.4 首轮与最终口径

主观测单元是 `(repository, pull_request_number)`，而 current-head 子观测
单元是 `(repository, pull_request_number, current_head_sha)`。两个口径不能
互相替代。

- **PR 首轮 attempt（主指标）**：此 PR 最早 CI run 的 attempt `1`，保留该
  run 的原始 `head_sha`。即使后续推送新 head，也不得抹去它；若它是
  cancelled、pending、失败或 API 历史不完整，分别保留该状态或 `unknown`，
  不得跳过它选择首个 success。
- **最终 attempt（主指标）**：当前 PR head 创建时间最新的 `run_id` 的最高
  可见 `run_attempt`。若它 pending/in_progress，最终状态就是未完成；旧
  head success 不能替代它。若当前 head 无 run 或 API 不完整，状态为
  `unknown`。
- **current-head 首轮（辅助指标）**：当前 head 最早 CI run 的 attempt `1`；
  它用于诊断一次推送后的首次质量，不能替代 PR 首轮。
- **首次通过**：首轮 attempt 的严格 `check == success`；取消、pending、
  unknown 单列，不并入成功，也不藏进失败率。
- **最终通过**：最终 attempt 的严格 `check == success`。只有该 head 的
  成功可以成立，旧 SHA、不同 PR、main push 或树相同的复用证明都不替代
  PR 当前 head 的此统计。

质量摘要使用 schema 2，除首轮、current-head 首次和最终 attempt 外，追加
不可覆盖的 `timeline` 事件。每个事件保存 head SHA、run/attempt、lane 或
阶段、结果分类和 run/artifact 定位；后续推送只能追加新事件，不能改写首轮
失败。轻量 `ci-quality-summary` 与 `ci-verification` manifest 保留 90 天，
详细 lane 日志、截图和大型测试包继续使用各自的 14/30 天保留期。

批量报表按 PR 同时给出 `prs_observed`、`first_success`、`first_failure`、
`first_cancelled`、`first_pending_or_incomplete`、`first_unknown` 及同组
final 字段，并另列 current-head first 字段。通过率只显示为
`success / terminal_observed`，其中 `terminal_observed = success + failure`；
cancelled、pending/incomplete、unknown 在分母外分别显示。当 unknown 或
pending 存在时标注“未完成或资料不全样本，非代码错误率”。

首轮查询从 PR 创建时间开始分页读取 workflow history，并只接受 GitHub
返回的 `pull_requests` 明确关联到该 PR 的 run；所以被 force-push/rebase
移出当前提交图的早期 head 仍会被保留。分页、关联、attempt 或 job 历史任一
无法完整读取时，首轮为 `unknown`。批量输入按 `(repository, PR)` 去重，
重复快照直接拒绝，避免把同一 PR 多次计入分母。

## 5. 修改范围

1. `scripts/dev_preflight.py`：区分局部/完整 claim，采集前后 source 与
   安全环境指纹，增加 full 入口和不可交付状态。
2. `scripts/ci/`：新增共享 lane 调度、结构化 lane 收据、PR current-head
   报告与单元测试；已有 `verification.py` 的树复用安全性不放宽。
3. `.github/workflows/ci.yml`：CI 调用共享 lane、为 receipt 保留
   `always()` 步骤及 artifact，新增最小权限的报告 job；`check` 的 required
   success 条件原样保持。
4. `scripts/test_dev_preflight.py`、`scripts/ci/test_*.py`：覆盖 dirty
   fast 可运行、full 的 dirty/变更/缺环境拒绝、共同 lane 调用、分类优先级、
   首轮取消不得被后续成功覆盖、最终只能对应 current head。
5. `AGENTS.md` 第 9 节与开发前置文档：要求按结果 claim 汇报，并在需要
   可交付完整本地证据时运行新入口；保留 CI 是最终门禁的表述。

## 6. 验收与回归

- dirty worktree 可完成 fast/compile，JSON 明示不可交付；没有 `local_full`。
- clean committed tree 上，模拟检查期间 HEAD/tree/status 任一变化，full
  拒绝并写 `not_verified`。
- 模拟 PostgreSQL/Chromium/tool 缺失、浏览器 skip/空集合、任一 lane nonzero
  时，full 不能写成功。
- CI 中每类结构化 outcome 生成相应 receipt；setup 失败不得被标成断言失败；
  未能分类的 completed failure 为 unknown，仍令原 check 失败。
- 同一 PR 的首轮 head 被取消、后续 head success 时，PR 首轮仍为
  cancelled、最终为当前 head success；旧 head success 不可复用为新 head
  的最终成功。current-head first 作为单独辅助字段保留。
- `scripts/ci/verification.py gate` 现有 success/failed/skipped 测试继续
  通过；工作流任何 phase failure/cancel/skipped 仍不能使 `check` 成功。

## 7. 风险与回滚

本变更只调整本地检查、CI 证据和说明。Actions API 不可用时，完整 CI
仍执行并由 `check` 约束；报告 history 写 unknown，不影响业务或发布。
回滚该 PR 即恢复旧报告方式；无需数据回滚或 Provider 补偿。
