# PR08 运营周期 donor 前端闭包审计

审计结论：PR08 的 donor 前端已经收敛为 2 个 v2 HTML fragment，并且在
`6bfbe5816bb89913c70adaca87d6a486260e016e` 上通过 SHA-256 和 `cmp` 字节级
证明；没有 cycle 专属 TS、CSS 或静态资源被带入 v3。这个提交完成的是
“可验证的 donor 证据闭包”，不是可上线的运营周期 Journey。后续必须由
Terra/Web 在 v3 webshell 中实现适配、API/Store 与真实数据闭环。

## 1. 审计边界与输入

| 项目 | 值 |
| --- | --- |
| donor 仓库 | `qianlan33333-png/AI-CRM-v2`（本机审计 checkout 由 `PR08_DONOR_DIR` 指定） |
| donor commit | `6bfbe5816bb89913c70adaca87d6a486260e016e` |
| v3 准备基线 | `codex/import-operation-cycles`，HEAD `9d2094ac` |
| 已检查 prep | `1c52d56`（PR08 准备）和 `9d2094a`（冻结 donor contract） |
| 本审计分支 | `codex/import-cycles-audit`，独立 worktree `/private/tmp/aicrm-v3-operation-cycles-audit` |
| 允许新增物 | 本审计文档、`scripts/check-pr08-frontend-donor-manifest.sh` |
| 明确未触碰 | donor 前端字节、`cmd/aicrm`、OpenAPI、migration、`go.mod/go.sum`、lock、deploy、CI、`internal/webshell`、Composition Root |

按 `skills/aicrm-v3-development/SKILL.md` 的开发前判断：

- OneID / 外部身份：**不属于 PR08 生命周期实现，但执行边界审计**。策略、run、runner、action 和 proposal 都是本地运营周期事实；任何模板中的“人群”“发送”“会员”等 donor 语义不得直接变成客户或外部身份绑定。
- Persistence：**本审计无新增持久化**；现有 v3 prep 是 operationcycle-owned 的本地事务/UoW 接缝，Store、schema、migration 和 HTTP adapter 留给 Terra。
- External Effects：**不涉及 Provider 写入**。start/report/action event/heartbeat 仅是本地意图、状态和回执接缝；不创建企微发送、recipient 执行、Provider retry 或第二队列。

## 2. 结果摘要

| 检查项 | 结果 | 证据 |
| --- | --- | --- |
| 活动 donor 页面 | 2 个：列表与运行档案 | `registry.json` 的 `cycles` / `cyclesDetail`；`build.mjs` 模板页分支 |
| 冻结目标文件 | 2 个 HTML，26 行/4,081 bytes 与 164 行/22,905 bytes | `web/donors/operation-cycles-v2/src/admin/templates/*` |
| cycle 专属 TS/CSS/assets | 未发现 | donor SHA 下按 `web/src` 的 cycle path 检索仅命中两个模板；静态历史代码单独延期 |
| shared runtime | 未复制 | `controller.ts`、`legacy.ts`、`client.ts`、`types.ts`、`mockData.ts` 与 v2 shell 混合其他域 |
| donor action | 列表 action 明确 blocked | `controller.ts:2561-2568`：`execution-runtime DTO 不等价` |
| HTTP 真实数据 | 未闭合 | `readAdminRows` 没有 cycles endpoint；`readAdminPage` 没有 cycle 分支，HTTP 页得到空 cycle 数据 |
| v3 入口 | 只有保留的单侧栏 shell route/placeholder | `internal/webshell/contract.go:101,120`、`handler.go:165-169` |
| 第二实现 | 未发现第二套活动 cycle 页面；发现整包 donor shell 的装配风险 | v3 nav 已有 `operation_cycles`；v2 `build.mjs` 会产生 `.side` |
| PR08 生产完成 | **否** | donor archive 已完成；v3 API/Store/adapter/Journey 尚未实现 |

## 3. 实际页面与模板闭包

### 3.1 活动页面

| 页面 key | v2 实际 URL | donor 源（冻结 SHA） | 当前目标 | 页面内容与交互 |
| --- | --- | --- | --- | --- |
| `cycles` | `admin/cycles.html`（v2 build 输出 `dist/admin/cycles.html`） | `web/src/admin/templates/cycles.html` | `web/donors/operation-cycles-v2/src/admin/templates/cycles.html` | “运营闭环”任务列表、任务数量、周期、步骤颜色、主 action、查看详情、空态 |
| `cyclesDetail` | `admin/cyclesDetail.html?id=<positive-number>`；donor E2E 使用 `?id=1` | `web/src/admin/templates/cyclesDetail.html` | `web/donors/operation-cycles-v2/src/admin/templates/cyclesDetail.html` | 单次运行只读档案；返回列表、8 个章节、证据索引 |

`registry.json` 在 donor SHA 的 `180-186` 行把 `cycles` 注册为一级“运营闭环”，
`351-357` 行把 `cyclesDetail` 注册为二级“运营闭环 · 运行档案”；`nav.json:9-12`
提供 donor 的 inline SVG 图标和“运营闭环”标签。这些 registry/nav/icon 是 v2
共用 shell 证据，**不在冻结 archive 中，不得复制进 v3**。

### 3.2 `cycles.html`（100% 原样）

- 26 行、4,081 bytes；只有 inline style 和 mini-runtime 标记，没有独立 stylesheet、script、`<html>`、`<body>`、`<aside>` 或 v2 shell。
- `cycles.total` 显示任务数；`cycles.rows` 显示 `t.dot`、`t.name`、`t.cron`、`t.steps[*].color/tc/label`。
- 每行保留两个 donor hook：`onClick="{{ t.act }}"` 和 `onClick="{{ t.viewDetail }}"`；文案由 `t.action` 与固定“查看详情”提供。
- 空态固定为“暂无运营任务 / 策略完成首次上报后会显示在这里。”。
- v3 adapter 只能提供与本地 operationcycle facts 等价的字段；不能因为模板出现“人群”或发送语义而增加客户/recipient 数据。

### 3.3 `cyclesDetail.html`（100% 原样）

模板为 164 行、22,905 bytes，保留以下完整视觉/字段边界，不得改词、改 inline
style 或改 interaction hook：

1. 任务目标与时间：`run.objective`、`run.audience`、意图/计划/首次/最后发送时间。
2. 前置检查与执行尝试：`run.attempts[*]` 的 status、summary、时间和 stages。
3. 人群分层与去留判断：`run.funnel[*]`、`run.audienceNote`。
4. 人审与计划版本：review、plan version/status/source、`targetCount`。
5. 实际发送事实：sent/failed/retryable/rate/status/source/首末发送时间；含“已删除好友”原文。
6. 分窗口结果：windows、metrics、时间范围、quality、limitation；明确写“观察信号 · 不作为因果提升结论”。
7. 结果复盘与限制：retro summary/detail/findings/limitations。
8. 下一轮优化：next status、summary、rationale、confirmed/applied version、note、changes。
9. 证据索引：`run.references` 只读列表。

上述第 1、3、4、5 项是 donor 的 presentation evidence，不是 PR08 的客户
受众/发送契约。v3 若没有等价的本地事实，必须 fail closed 或显示安全的缺失状态，
不得用 mock 数字补齐。

### 3.4 没有遗漏的 TS/CSS/assets

在 donor SHA 对完整 `web/src` path 做 cycle/operation-cycle 检索，活动页面命中
只有：

```text
web/src/admin/templates/cycles.html
web/src/admin/templates/cyclesDetail.html
```

没有 `cycle*.ts`、`operationcycle*.ts`、`cycle*.css`、`operationcycle*.css`、
图片、字体或其他独立 cycle asset。目标 archive 的 exact set 也只允许上述两个
相对路径；检查脚本会拒绝任何第三个文件或非 HTML 文件。

被检出但延期、不能视为 PR08 活动页实现的历史前端是：

```text
web/src/api/generated/p4-static-history/p4-static-history.ts
web/src/api/staticHistory.ts
web/src/admin/sections/staticHistory.ts
```

它们挂在 `config.html?static_history=1`，是 V1 封存静态历史只读面，不是
`cycles.html` 的 lifecycle runtime。

## 4. 依赖与装配路径

donor `main.ts:4-10` 对 `cycles` 走 `import("./legacy")`，而不是专属页面模块。
`legacy.ts:412-421` 对模板页统一创建 `AdminController`、调用 `controller.init()`，
再把 `tpl.innerHTML` mount 到 `stage`。`controller.ts:2561-2568` 才为 cycles
组装 rows/run：详情使用 `?id=` 数字 query，列表 action 固定 blocked。

这意味着以下文件是 shared mixed runtime，不能作为“cycle frontend closure”复制：

| donor 路径 | 审计处置 | 原因 |
| --- | --- | --- |
| `web/src/admin/main.ts`、`legacy.ts`、`controller.ts` | 不复制 | 负责其他页面、客户/人群/群运营/收件人分支及 v2 shell runtime |
| `web/src/shared/api/types.ts`、`mockData.ts`、`client.ts` | 不复制 | `Cycle*` 类型与 mock DB 嵌在跨域模型中，`MockApi` 还会写 sessionStorage |
| `web/src/api/capabilities.ts` | 不复制 | 共享能力矩阵；其 donor capability 明确将 `cycles/cyclesDetail` 标为 `backend_blocked` |
| `web/src/admin/nav.json`、`registry.json`、`web/scripts/build.mjs` | 不复制 | 生成完整 v2 shell、导航和所有旧页面 |
| `web/src/shared/ui/tokens.css` 及其它 shared CSS | 不复制 | 会把非 cycle 视觉/页面依赖带入 v3；模板已有 inline 样式 |

### 4.1 延期静态历史 URL/DTO

这是审计中发现的“相关但不活动”的读取面，记录完整以免后续误接：

- 列表：`GET /api/admin/static-history/cycle-strategies`、`cycle-versions`、`cycle-documents`、`cycle-metrics`、`cycle-references`。
- 详情：上述五类分别追加 `/{historyId}`；versions 可用 `strategy_history_id`，documents 可用 `version_history_id` 作为 parent filter。
- `StaticHistoryEnvelope` 强制 `source: "v1_history"`、`read_only: true`、`real_external_call_executed: false`。
- DTO 字段包括 `CycleStrategy(strategy_key,title,description,cadence,timezone,original_status,current_version)`、`CycleVersion(strategy_source_id,strategy_history_id,version,label,objective,version_hash,governance/effective/confirmed/operation_skill_hash)`、`CycleDocument(version_history_id,schema_version,三类 guide sha256/document_pack_hash)`、`CycleMetric(run_source_id,metric_key,numerator/denominator/value,observation_window,data_source,data_quality,limitations,is_causal,value_status,last_snapshot_source_id)` 和 `CycleReference(run_source_id,reference_key/reference_type,label,source_system,reference_source_id,evidence_hash,data_status,last_snapshot_source_id)`。
- 前端入口是 `config.html?static_history=1&history_kind=CycleStrategy|CycleVersion|CycleDocument|CycleMetric|CycleReference`，只读子链接使用 `history_id`/`history_parent_id`。它要求独立 v3 history schema/HTTP/OpenAPI/ownership，当前不接入 PR08。

## 5. URL、DTO 与 donor backend contract

### 5.1 donor 页面/API URL

`cmd/aicrm/api.go:3978-3989` 注册的 authenticated admin contract 是：

```text
GET  /admin/operation-cycles
GET  /admin/operation-cycles/{strategy_key}
GET  /admin/operation-cycles/{strategy_key}/runs/{run_key}
GET  /api/admin/operation-cycles/action-requests/{request_id}/result
GET  /api/admin/operation-cycles/runs/{run_key}
GET  /api/admin/operation-cycles/strategies
GET  /api/admin/operation-cycles/strategies/{strategy_key}
POST /api/admin/operation-cycles/strategies/{strategy_key}/actions/{action_key}/start
GET  /api/admin/operation-cycles/strategies/{strategy_key}/current-action
GET  /api/admin/operation-cycles/strategies/{strategy_key}/runs
GET  /api/admin/operation-cycles/strategies/{strategy_key}/strategy-change-proposals
POST /api/admin/operation-cycles/strategy-change-proposals/{proposal_id}/decision
```

`cmd/aicrm/api.go:4056-4062` 的 service-authenticated contract 是：

```text
POST /api/operation-cycles/action-requests/claim
POST /api/operation-cycles/action-requests/{request_id}/events
GET  /api/operation-cycles/context-index
POST /api/operation-cycles/reports
POST /api/operation-cycles/runner/heartbeat
GET  /api/operation-cycles/strategies/{strategy_key}/context
POST /api/operation-cycles/strategy-change-proposals
```

这些是 donor contract evidence，**不是当前 v3 浏览器 API**。当前 donor `web/src/api/admin.ts:2014-2064`
的 `readAdminRows` 只按其它页面调用 API，没有 `needs('cycles')`/`needs('cyclesDetail')`；
`readAdminPage:2067` 也没有 cycle detail 分支。因此把 donor routes 直接当成已接通
frontend Journey 是错误结论。

### 5.2 前端 view DTO（仅 donor shape）

`web/src/shared/api/types.ts:231-321` 定义的 donor view shape 为：

- `CycleTask`：`id/name/cron/dot/steps[{label,color,dim}]/action/runId`。
- `CycleAttempt`：`label/statusLabel/tone/summary/startedAt/finishedAt/stages[{label,status}]`。
- `CycleWindow`：`label/statusLabel/tone/metrics[{label,value,desc}]/start/end/quality/limitation`。
- `CycleRun`：`id/label/objective/strategy/runKey/snapshotRev/audience/intendedSendAt/planScheduledFor/firstSentAt/lastSentAt`；`attempts`、`funnel[{label,value}]`、`audienceNote`；review/plan/version/`targetCount`；`delivery{sent,failed,retryable,rate,statusLabel,source,failureSummary}`；`windows`；`retro{summary,detail,findings,limitations}`；`next{statusLabel,tone,summary,rationale,confirmedAt,appliedVersion,note,changes}`；`references[{label,desc}]`。

这些类型与 `AdminDb` 混在 shared API，不能原样引入 v3，因为它们的字段覆盖
audience、发送、recipient-oriented delivery 等已排除语义；只可作为模板输入清单和
适配验收证据。

### 5.3 v3 prep 的 local DTO/状态边界

v3 `internal/operationcycle/port/port.go` 仅保留本地 `Strategy`、`Run`、`Runner`、
`ActionRequest`、`Proposal`：策略/运行键和版本、runner compatibility/heartbeat、
action stages、proposal decision facts；没有 `customer_id`、`external_userid`、
OneID、segment、audience、campaign、recipient、order、membership、subscription、
entitlement 或 Provider 标识。`domain/cycle.go` 对 JSON payload/filter 执行
`ContainsForbidden` fail-closed。

donor command contract 的安全要点（`legacy_operation_cycle_api.go`）是：

- human start 需要 `Idempotency-Key`，body 为 `run_key`/`parent_request_id`，只产生本地 queued action，返回 `202`；
- service event 使用 `Idempotency-Key` 作为 event ID，schema `operation_cycle_action_event.v1`；
- report 使用 `operation_cycle_snapshot.v1`，heartbeat 使用 `operation_cycle_runner_heartbeat.v1`；
- `completed` 的 `outcome_unknown` 仍是 terminal fact，不得换 key 盲重试；
- proposal 使用 `operation_cycle_strategy_change_proposal.v1`，决策需 actor/time；
- body 上限、unknown-field/trailing JSON、limit/offset 和 401/403/404/409/503 错误边界保持 donor contract evidence。

## 6. Journey 闭包

### 6.1 donor mock 可观察 Journey（不可当生产能力）

1. v2 `admin/cycles.html` 通过 `main.ts -> legacy.ts -> AdminController` mount 模板。
2. `MockApi.restore()` 从 `sessionStorage` 读取 `aicrm.mock.db.v4`，没有数据时深拷贝 `SEED_DB`；种子有 3 行任务和 3 个 run。
3. donor 种子为周一沙龙邀约、沉默用户唤回、会员到期续费；run 中含人群、会员、企微发送回执等禁止在 v3 绑定的语义。
4. “查看详情”调用 `goto('cyclesDetail', '?id=' + t.runId)`，打开 `admin/cyclesDetail.html?id=1`，渲染 8 章与证据索引。
5. 主 action 调用 `blocked('当前复盘会话壳与 execution-runtime DTO 不等价')`，不会启动真实 execution runtime。
6. donor E2E (`web/scripts/e2e.mjs:1717-1726`) 只直接加载 `cyclesDetail.html?id=1` 并断言“分窗口结果 / 结果复盘与限制 / 证据索引”；没有真实 API、Provider receipt 或生产 Journey 断言。

### 6.2 donor HTTP 模式的闭环缺口

`HttpApi.loadDb()` 明确委托 `readAdminPage(context)`，且“production never merges
SEED_DB”。但 cycles 不在 `readAdminRows` 的 request fan-out 中，`readAdminPage`
也不读取 operation-cycle backend routes；所以切换 HTTP mode 后不能得到 donor mock
的 3 行数据，也不能完成 detail/action Journey。该缺口是审计结论，不在本提交修复。

### 6.3 后续 v3 Journey 验收门（非本提交实现）

Terra/Web 只有在下列证据齐全时才可宣称 implemented：v3 单侧栏认证入口；策略/run
读取来自 operationcycle-owned Store 的真实 DTO；action start 使用幂等键和 CAS/UoW
收据；runner/event 状态可重放且 `outcome_unknown` 不盲重试；proposal/context
只读/本地决策可审计；模板字段缺失时无 mock fallback；全程无 customer/OneID/recipient
绑定、无 Provider 写入。任何企微写若另行授权，必须由 outbound/External Effects
统一协调、记录 attempted/executed/outcome_unknown/reconciled，不得从页面或
operationcycle 直调 Provider。

## 7. OneID / customer 边界

| 边界 | PR08 结论 |
| --- | --- |
| 策略/run/runner/action/proposal 主键 | 本地 key/ID；不构造第二套 customer 主键，不解析 OneID |
| donor 模板 `audience`、`targetCount`、delivery、`人群`、`发送` | 仅原样保留的 donor presentation evidence；禁止绑定客户、external_userid、订单或权益状态 |
| donor mock 的会员/企微/收件人数字 | fixture-only；不得加载到 v3，不能作为 Provider receipt 或客户归属证据 |
| 未来确需客户受众/身份 | 另立能力；通过 `internal/identity/port` 读取 canonical customer/identity，不能在 operationcycle 自建匹配、隐式建客或自动合并；不属于 PR08 |
| OneID 模块 | 本提交未 import、未写表、未改变身份归属；v3 shell 的 OneID nav 与 PR08 生命周期无关 |

因此本项的分类是“**OneID 不参与 PR08 local lifecycle，但必须阻断 donor raw
audience/customer 语义越界**”。不能为了复用 donor template 而给 operationcycle
增加 `customer_id` 或外部身份字段。

## 8. PR10 单侧栏装配风险

donor `web/scripts/build.mjs:77-99` 的 `adminShell()` 每页生成完整 v2 shell，含
`<div class="shell">`、`<aside class="side">`、v2 nav、用户区和 admin bundle；
`adminPage():120-124` 再将 `cycles.html`/`cyclesDetail.html` 包入该 shell。把
`dist/admin/*`、`build.mjs`、`main.ts`、`legacy.ts` 或 nav/registry 整包接入 v3
会同时引入旧页面、旧路由、shared mock 及第二个侧栏。

v3 `internal/webshell/templates/admin_base.html:30` 已有唯一
`<aside class="admin-sidebar">`；`contract.go:101,120` 已声明唯一
`api.admin_operation_cycles_page` / “运营闭环”，`PathFor:277-280` 已定义
策略与 run 的 canonical path。PR10 装配必须遵守：

- 只把 allowlisted donor 两个 fragment mount 到 v3 `stage`/page body，不部署 donor 完整 HTML 或 v2 shell；
- 保留 v3 `admin-sidebar`，不复制 donor `.side`、nav、registry、inline icon 或用户区；
- `cyclesDetail` 的 donor `go.cycles` 及 `cycles` 的旧 `?id=` hook 必须由 v3 adapter 映射到 canonical `/admin/operation-cycles` 和 `/admin/operation-cycles/{strategy_key}/runs/{run_key}`；不能把数字 `id` 当成可信 strategy/run identity；
- v3 当前 `handler.go:165-169` 仍是“运营闭环入口已预留” placeholder，只有 API/Store/权限/Journey 齐全后才能替换内容；
- 装配测试必须断言响应中恰有一个 `.admin-sidebar`，无 `.side`/`class="shell"`，无 donor `SEED_DB`/sessionStorage fallback。

这不是发现第二实现，而是对“整包 donor 误接”造成第二 shell 的预防性审计。

## 9. SHA-256 与 `cmp` 证据

`docs/donor-manifests/pr08-operation-cycles.sha256` 中 source/target 两端期望值为：

| 相对文件 | donor SHA-256 | target SHA-256 | `git show <SHA>:<path> | cmp - target` |
| --- | --- | --- | --- |
| `admin/templates/cycles.html` | `19a1fbbdaaca0a6c4b3b0fbdb2f75c496d0e2c3192ff5d29af34b650a1cdd6bc5c` | `19a1fbbdaaca0a6c4b3b0fbdb2f75c496d0e2c3192ff5d29af34b650a1cdd6bc5c` | PASS |
| `admin/templates/cyclesDetail.html` | `e7d4f666f17c39b90e69e4cd698bb97cc077dbd1ac13753aa2b8d07265933877` | `e7d4f666f17c39b90e69e4cd698bb97cc077dbd1ac13753aa2b8d07265933877` | PASS |

可重复命令（使用本机 donor checkout 时显式传入路径）：

```bash
cd /private/tmp/aicrm-v3-operation-cycles-audit
PR08_DONOR_DIR=/Users/qianlan/Documents/kimi/AI-CRM-v2 \
  scripts/check-pr08-frontend-donor-manifest.sh
```

脚本同时校验 donor commit 可解析且等于冻结 SHA、YAML 的 `source_commit`/
`exact_file_count`/target root、source/target manifest hash、git blob 与 target
的 `cmp`、目标 exact file set、fragment-only（无 HTML document/v2 sidebar）。

本次执行结果：

```text
PASS admin/templates/cycles.html sha256=19a1fbbdaaca0a6c4b3b0fbdb2f75c496d0e2c3192ff5d29af34b650a1cdd6bc5c cmp=PASS
PASS admin/templates/cyclesDetail.html sha256=e7d4f666f17c39b90e69e4cd698bb97cc077dbd1ac13753aa2b8d07265933877 cmp=PASS
PR08 frontend donor freeze PASS: donor=6bfbe5816bb89913c70adaca87d6a486260e016e files=2
```

v3 prep 的窄域验证也通过：`go test -count=1 ./internal/operationcycle/...`、
`go vet ./internal/operationcycle/...` 和 `go test -race -count=1
./internal/operationcycle/...` 均为 PASS。

## 10. 非目标、红线与交付

本审计不实现：Store/PostgreSQL/migration/SQLC、HTTP/auth/OpenAPI/Composition Root、
PR10 webshell 改造、customer/OneID/segment/audience/recipient 选择、企微/Provider
调用、发送/重试/对账、历史导入、shared runtime 裁剪或 donor wording/style 改造。

若后续实现出现身份错误归属、重复 Provider effect、跨领域表写、鉴权绕过、PII/secret
日志、silent migration loss 或双主写，应停止该实现并重新审计。当前提交只新增本文件
与 `scripts/check-pr08-frontend-donor-manifest.sh`，不改变任何 donor frontend byte
或中央装配。

最终状态：**donor 前端 2-file byte-exact evidence closed；v3 运营周期用户可观察
能力仍待 Terra/Web 完成 API/Store/adapter/Journey 后验收。**
