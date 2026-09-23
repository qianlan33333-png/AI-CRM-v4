# PR09 配置、AdminOps、发布与诊断 donor 契约审计

本文件冻结 PR09 的可观察行为和接入边界。当前提交是
`preparation_only`：不注册 HTTP/OpenAPI，不修改 Composition Root、迁移、部署或
共享壳，也不把 donor 当作 v3 的运行时依赖。

- v3 基线：`19384b93fe362c7786edc81dd5595b79570f6bb1`。当前分支保留在该基线，
  后续由 Root 统一 rebase 到新的 `origin/main`。
- donor：只读树 `/tmp/aicrm-v2-audit.yN3jmr@6bfbe5816bb89913c70adaca87d6a486260e016e`。
- 目标分支：`codex/import-adminops`，工作树为
  `/private/tmp/aicrm-v3-migration.jDLgdz/adminops`。
- 原样前端保存于 `web/donors/adminops-v2/src`，逐文件 SHA/cmp 证据见
  `pr09-donor-sha256.txt`；这些文件不是 v3 active web tree。

## 领域边界

本切片只保留配置读写、配置能力开关、AdminOps 的安全本地投影、发布记录/发布
证据、运行观察和健康/API 文档契约：

- 非敏感配置 key 的闭集校验、事务内变更、审计和本地事件 seam；
- app-settings 的可编辑投影、掩码 secret configured 状态、审计元数据；
- setup wizard 的 Corp ID/Agent ID 两键 CAS、回执和读回；
- AdminOps credential reference、category、release record、job/notification 的
  本地叶子行为，以及不暴露 secret 的投影/运行观察；
- release candidate、prerequisite evidence、readiness、cutover journal/generation/
  fence、rollback check 和 reconciliation-gated 的本地生命周期；
- config、API 文档、setup wizard、release、AdminOps-safe、health/diagnostic 的
  原样页面/生成请求契约。

明确不属于 PR09：客户、Customer/OneID、手机号、external user、Segment/Audience、
Campaign、问卷、雷达、成员网格；订单、支付、退款、权益、会员；直接 WeCom、Feishu、
Provider、LLM、备份、部署、流量切换、发送、重试和 delivery claim；旧 config startup
load/schema、Store/SQLC、migration、worker、HTTP/auth、OpenAPI、composition、deploy、
CI 和依赖锁。Secret/token/private key/webhook locator 只允许以 configured/masked 或
经过校验的 secret reference 表示，绝不保存或回显明文。

## 页面入口与 Journey

以下是冻结 donor 页面事实。后续只能由 v3-owned adapter 将业务挂到 PR10 的
`internal/webshell/admin_base` 和唯一一级侧边栏；不能部署 v2 完整页面或第二套 `.side`。

| 页面/入口 | 原样 donor 文件 | 可观察交互 | PR09 边界 |
| --- | --- | --- | --- |
| `config` | `web/src/admin/templates/config.html` | 展示配置类目、状态/能力开关，打开类目详情；“企微接入基础配置”打开 setup wizard；相邻“后台访问成员”按钮仍保留原文 | 配置和 setup wizard 只作候选挂载；AdminAccess 属 `internal/access`，不得暴露 |
| `configDetail` | `web/src/admin/templates/configDetail.html` | 展示字段、开关、掩码密码框、检查/保存/返回 | 只允许 v3 Config/AdminOps adapter 的闭集字段；不能让密码框写入 secret |
| `apidocs` | `web/src/admin/templates/apidocs.html` | 展示 donor API 列表、参数和下载 OpenAPI 按钮 | 仅为 API 文档页面证据；不修改/重新生成 v3 `api/openapi.yaml`，下载动作须由 Terra 单独 gate |
| setup wizard | `web/src/admin/sections/setupWizard.ts` | 读取本地快照；编辑 Corp ID/Agent ID；空 secret 字段提交；检查 local-only/external/runtime_applied；校验两条 audit 和两条 event 后读回 | 原样模块新增的实际依赖 `web/src/api/transport.ts` 也一并归档；读写均不得表示已应用到运行时或已调用企微 |
| AdminOps/release/diagnostics | 生成客户端与页面 operation contract | 类目/能力、release read/record、运行/健康观察可由 adapter 选择挂载 | 生成文件是混合契约证据；未选中的 job、Provider、AdminAccess、跨域 schema 必须保持未挂载 |

关键 Journey：

1. **配置读写**：页面读取闭集 projection → 仅编辑非敏感字段 → adapter 验证认证、
   CSRF、幂等和 actor → Config service 在一个 UoW 中锁 key、写 setting/audit/event、
   返回不含 old/new value 的 receipt → 页面再读 projection。重复 request key 必须回放
   相同结果，payload 变化冲突；secret 输入在进入事务前 fail closed。
2. **setup wizard**：GET 得到 `expected_digest`、两项可编辑值和 mask-only 状态 → POST
   带 digest、route-bound action proof 和 Idempotency-Key → 同时锁两 key，写两条审计/事件
   或整体冲突回滚 → 检查 `local_only=true`、`external=false`、`runtime_applied=false`，
   再 GET 读回。任何非空 secret、回执不完整或读回不一致都停止展示成功。
3. **能力/安全 AdminOps**：读 category/push capability → 对闭集 enabled/settings 做
   本地 CAS 变更 → 返回 masked projection、审计/幂等事实。`check` 只能做本地诊断；
   category、scheduler 或 capability 变化不触发 provider、发送、worker 或外部网络。
4. **发布记录**：记录 commit/artifact/manifest/config digest → 逐候选记录精确主体的
   prerequisite evidence → 计算 readiness → 准备 candidate → 以 generation/fence
   开始或重启本地 cutover journal → 按固定顺序追加步骤 → 仅在完整 journal 后标记
   activated。rollback 先记录 schema/data/outbound reconciliation check，再请求/完成
   本地 rollback；任何状态都不代表本进程执行了 deploy、备份恢复、流量切换或 Provider
   写入。
5. **运行与健康诊断**：只读取有界观察、时间线、data-health registry、legacy/system
   readiness；对 URL query、详情、节点和 graph 做去敏/截断；`observed_only`、
   `real_external_call_executed`、`outcome_unknown` 等事实必须原样区分，不能把 queued
   或 HTTP 200 解释为外部成功。

## 配置与 setup wizard 请求契约

以下路径/方法/DTO 是冻结 `p4-config-settings-compat.ts`、`p4-setup-wizard.ts` 的
逐字节生成证据；当前 v3 不注册这些路径。

| 方法 | URL | 请求 DTO | 成功 DTO/语义 |
| --- | --- | --- | --- |
| GET | `/admin/config/app-settings?q&scope` | `GetLegacyAppSettingsPageParams{q,scope=editable\|masked}` | `string` 页面；仅页面兼容证据 |
| POST | `/admin/config/app-settings/save` | `LegacyAppSettingsSaveForm`（CSRF/action proof、确认、12 key，其中敏感 key `maxLength=0`） | `302`；旧表单兼容证据，不应绕过 JSON/幂等 adapter |
| GET | `/api/admin/config/app-settings?q&scope` | `GetLegacyAppSettingsResourceParams` | `LegacyAppSettingsResponse`：12-key projection、3 summary cards、最多 10 条 audit、masked rows |
| PUT | `/api/admin/config/app-settings` | `LegacyAppSettingsResourceSaveRequest{settings,confirm,admin_action_token}` | `LegacyAppSettingsResourceSaveResponse`：changed projection、`changed_count`、`real_external_call_executed=false` |
| GET | `/api/admin/setup-wizard` | 无 | `SetupWizardReadResponse`：`expected_digest`、Corp/Agent editable、mask-only secrets、local flags |
| POST | `/api/admin/setup-wizard` | `SetupWizardSaveRequest{wecom.corp_id,wecom.agent_id,四个空敏感字段,expected_digest,admin_action_token}` | `SetupWizardSaveResponse`：两条 audit、两条 `setting.updated` event、local flags |

`internal/access` 对应的 `GET/PUT /api/admin/admin-access`、
`AdminAccessReadResponse/AdminAccessSaveRequest` 虽在 setup-wizard 生成文件中出现，
是为了保持 donor artifact 完整；它们不在 PR09 的后端叶子、路由或挂载范围内。

## AdminOps-safe 请求契约

### PR09 候选的本地配置/发布/诊断操作

| 方法 | URL | 请求 DTO | 成功 DTO/边界 |
| --- | --- | --- | --- |
| GET | `/api/admin/config/categories` | 无 | `AdminOpsLocalOKResponse`，闭集 category projection |
| GET | `/api/admin/config/categories/{categoryKey}` | 路径 key | `AdminOpsLocalOKResponse`，单 category，settings 只允许安全值 |
| PUT | `/api/admin/config/categories/{categoryKey}/enabled` | `AdminOpsEnabledRequest{enabled,admin_action_token}` | `AdminOpsLocalOKResponse`，本地 enabled CAS |
| PUT | `/api/admin/config/categories/{categoryKey}/settings` | `AdminOpsCategorySettingsRequest{settings,admin_action_token}` | `AdminOpsLocalOKResponse`，不能带 secret material |
| POST | `/api/admin/config/categories/{categoryKey}/check` | `AdminOpsActionRequest{confirm,admin_action_token}` | `AdminOpsLocalOKResponse`，本地 check，不调用 provider |
| GET | `/api/admin/config/push-capabilities` | 无 | `AdminOpsLocalOKResponse`，scheduler/capability safe projection |
| PATCH | `/api/admin/config/push-capabilities/scheduler` | `AdminOpsEnabledRequest` | `AdminOpsLocalOKResponse`，只改本地 scheduler gate |
| PATCH | `/api/admin/config/push-capabilities/{capabilityKey}` | `AdminOpsEnabledRequest` | `AdminOpsLocalOKResponse`，只改闭集 capability |
| GET | `/api/admin/config/releases` | 无 | `AdminOpsLocalOKResponse`，本地 release rows |
| POST | `/api/admin/config/releases` | `AdminOpsCreateReleaseRequest{confirm,changes,admin_action_token}` | `AdminOpsLocalOKResponse`，创建带 checksum 的本地 release record |
| GET | `/api/admin/config/releases/{releaseId}` | 路径 ID | `AdminOpsLocalOKResponse`，masked release projection |
| GET | `/api/admin/config/releases/{releaseId}/shadow-compare` | 路径 ID | `AdminOpsLocalOKResponse`，只读 compare，`external_calls=false` |
| POST | `/api/admin/config/releases/{releaseId}/validate` | `AdminOpsActionRequest` | `AdminOpsLocalOKResponse`，本地 validate |
| POST | `/api/admin/config/releases/{releaseId}/publish` | `AdminOpsPublishReleaseRequest{confirm,checksum,admin_action_token}` | `AdminOpsLocalOKResponse`，只记录 published fact，不部署 |
| POST | `/api/admin/config/releases/{releaseId}/rollback` | `AdminOpsConfirmedActionRequest` | `AdminOpsLocalOKResponse`，只记录本地 rollback fact，不执行回滚 |
| GET | `/admin/config` | 无 | `string` 配置页入口证据 |
| GET | `/admin/config/releases` | 无 | `string` release 页入口证据 |
| GET | `/admin/config/releases/new` | 无 | `string` new-release 页入口证据 |
| GET | `/admin/config/releases/{releaseId}` | 路径 ID | `string` release detail 页入口证据 |

上表是可供 Terra adapter 选择的候选面；本准备提交仍不暴露任何路径。
`AdminOpsReleaseChangesRead/Write` 只允许 `wecom.corp_id`、`wecom.agent_id`、
`outbound.rate_per_second`、`outbound.max_attempts` 等闭集值，`wecom.webhook_ref`
只返回 `"masked"` 或接受格式化 `secret://`/`secretref:` 引用，不能回显 locator。

### 生成文件中的混合 job/notification 分支

`p4-adminops-safe.ts` 必须逐字节保存，因而也包含以下 donor operations：

- `GET /api/admin/broadcast-jobs`、`GET/POST /api/admin/broadcast-jobs/{jobId}`
  （list/get/approve/cancel）；
- `POST /api/admin/broadcast-jobs/feishu-hourly-report/run`、
  `GET/PUT /api/admin/broadcast-jobs/notification-settings/feishu`、
  `POST /api/admin/broadcast-jobs/notification-settings/feishu/validate`；
- `GET /api/admin/jobs/summary`、`/archive-sync`、`/callbacks`、
  `/deferred-jobs`、`/webhook-deliveries`、`/message-batches` 及 message batch ack；
- `AdminOpsActionRequest`、`AdminOpsConfirmedActionRequest`、
  `AdminOpsCancelJobRequest`、`AdminOpsBatchAckRequest`、
  `AdminOpsNotificationSettingRequest` 和 `AdminOpsLocalAcceptedResponse`。

这些 branches 可能带 broadcast、Feishu、archive-sync、callback、message-batch、
order-identity 或 Provider 邻接语义，全部是 archive-only/unmounted；它们不能因为
生成客户端存在就获得成功 route。当前后端仅保留通用本地 job/notification 叶子，Terra
必须逐 operation 复核 job kind、owner 表、回执和 outbound/EER 边界，并默认隐藏或
拒绝上述混合分支。donor schema 中 `order_identity_repair`、`order_identity` 也必须
继续排除。

## 发布平面请求契约

`p4-release-plane.ts` 是独立的 release candidate 证据客户端。DTO 只含 digest、
evidence、generation/fence、步骤和 reconciliation facts；当前分支不注册路径。

| 方法 | URL | 请求 DTO | 成功 DTO |
| --- | --- | --- | --- |
| GET | `/api/v1/admin/release-candidates?limit` | `ListReleaseCandidatesParams{limit 1..100}` | `ReleaseCandidateList` |
| POST | `/api/v1/admin/release-candidates` | `RegisterReleaseCandidateRequest{commit_sha,artifact_digest,manifest_digest,config_digest,target_schema_version}` | `ReleaseCandidate` (201) |
| GET | `/api/v1/admin/release-candidates/{candidateId}` | 无 | `ReleaseCandidate` |
| POST | `/api/v1/admin/release-candidates/{candidateId}/prerequisites` | `RecordReleasePrerequisiteRequest{kind,evidence_sha}` | `ReleasePrerequisiteReceipt` (201) |
| POST | `/api/v1/admin/release-candidates/{candidateId}/prepare` | 无 | `ReleaseCandidate`，readiness gate |
| POST | `/api/v1/admin/release-candidates/{candidateId}/cutover/start` | 无 | `ReleaseWorkerLease`，generation/fence |
| POST | `/api/v1/admin/release-candidates/{candidateId}/cutover/restart` | `ReleaseWorkerCommand{generation,fence}` | `ReleaseWorkerLease`，旧 generation 被 fence |
| POST | `/api/v1/admin/release-candidates/{candidateId}/cutover/steps/{step}/complete` | `ReleaseWorkerCommand` | `ReleaseCutoverProgress`，固定顺序 |
| POST | `/api/v1/admin/release-candidates/{candidateId}/activate` | `ReleaseWorkerCommand` | `ReleaseCandidate`，完整 journal 后仅改本地状态 |
| POST | `/api/v1/admin/release-candidates/{candidateId}/rollback-checks` | `RecordReleaseRollbackCheckRequest{kind,passed,evidence_sha}` | `ReleaseRollbackCheck` (201) |
| POST | `/api/v1/admin/release-candidates/{candidateId}/rollback/request` | 无 | `ReleaseCandidate`，reconciliation gate |
| POST | `/api/v1/admin/release-candidates/{candidateId}/rollback/complete` | 无 | `ReleaseCandidate`，只接受 execution reconciliation fact |

Prerequisite 的 `campaign_closure`、`commerce_closure`、`outbound_closure` 等只是
candidate subject 的精确证据标签，不表示 Release import 了 Campaign/Commerce/Outbound
store；Terra 必须通过跨域 port/版本化事件提供证据。`switch` 步骤、`activate` 和
`rollback` 名称也不授权流量切换、部署、备份恢复或外部回滚执行。

## 运行与健康诊断请求契约

| 方法 | URL | 请求 DTO | 成功 DTO/语义 |
| --- | --- | --- | --- |
| GET | `/admin/execution-runtime` | 无 | `302` page redirect，页面证据 |
| GET | `/api/admin/execution-runtime` | 无 | `LegacyExecutionRuntimeResponse`，bounded/redacted observed snapshot |
| GET | `/api/admin/executions/{executionId}` | 路径 `exe_...` | `LegacyExecutionTimelineResponse`，graph/timeline only |
| GET | `/api/admin/data-health/checks` | 无 | `LegacyDataHealthChecksResponse`，4 项本地 registry observation |
| GET | `/api/admin/data-health/checks/{checkId}` | 路径 check ID | `LegacyDataHealthCheckResponse` |
| GET | `/api/admin/data-health/summary` | 无 | `LegacyDataHealthSummaryResponse`，不调用外部系统 |
| GET | `/health` | 无 | `LegacyRuntimeHealthSnapshot`，只给 mode/readiness flags |
| GET | `/api/system/health` | 无 | `SystemHealthResponse`，组件状态及 `pii/secrets_in_output` guard |
| GET | `/healthz` | 无 | `HealthResponse{status: ok}` liveness |

运行观察的 `status_url` 会剥除 query，details/message 通过敏感 key、PII key、长度、
graph 深度/节点数进行 redact/bound；读错误返回 unavailable，不能降级为“provider
成功”。`SystemHealthResponse` 的 `real_calls_enabled`、unknown count 等只是组件事实，
不会开启调用。

## v3 叶子代码、测试与供体证据

后端适配叶子按 Config、AdminOps、Release 三个 owner 分组，目标文件映射和每个 donor
source SHA 见 manifest/SHA 文件：

- Config：`internal/config/port`、registry、manager、settings compatibility、setup
  wizard 及对应测试；不复制 donor `load.go`、`schema.go`、`store`、`http`。
- AdminOps：`internal/adminops/port`、safe projection、credential/job/notification
  service、execution runtime reader 及对应测试；不复制 store/generated、HTTP 或
  order-identity repair。
- Release：`internal/release/port`、candidate lifecycle service/test；不复制 store、
  HTTP 或 deployment worker。

冻结 donor 的叶子测试证据为：

- `go test ./internal/config/... ./internal/adminops/... ./internal/release/...`；
- donor acceptance `go test ./acceptance/adminops ./acceptance/release`（需要显式隔离
  PostgreSQL URL；无 URL 时只允许安全 skip，不宣称 PG 验证通过）。

v3 目标验证为同样的 `go test`、`go vet`、`go test -race` 三组领域命令，加上 setup
wizard 的 esbuild 解析 smoke test，以及 16 个 frontend source/target 的逐字节 cmp。
测试结果写入 manifest 的 `verification`，不可用 HTTP 200、Mock、queued 或文件存在
替代行为验证。

## Terra 适配事项与阻塞风险

- **Terra/Config**：决定 v3 startup/config ownership；新建 Config-owned 表、CAS/锁、
  audit/idempotency 与独立 migration；把 EventAppender 接到 v3 versioned event/outbox，
  事件不得包含 secret 或 setting value。不能引入 donor 混合 startup schema/load。
- **Terra/AdminOps**：实现 AdminOps-owned repository、schema、generated SQL、receipt、
  audit 和 authenticated HTTP/OpenAPI；保留 secret-reference-only projection，逐项决定
  safe job kind，默认拒绝 customer/identity-repair/order/payment/recipient/provider。
- **Terra/Release**：实现 release-owned 表、锁/CAS、审计、幂等 receipt/outbox 和 route
  adapter；把 prerequisite 事实接到稳定 port，保持 publish/activate/rollback 只是本地
  证据，不执行 deploy、traffic switch、backup 或 provider。
- **Terra/Outbound**：若未来需要外部效果，只接收 Config/AdminOps/Release 产生的意图，
  独立持久化 accepted/attempted/outcome_unknown/executed/reconciled 和对账；本 PR 不拥有
  WeCom/Feishu/provider write。
- **Terra/Web**：从生成混合 artifact 中收窄 import，只挂载 config、API docs、setup
  wizard、release-read 和 diagnostic hooks；`setupWizard.ts` 的原样 `api/transport.ts`
  依赖必须通过 v3 adapter 复用；所有挂载进入 PR10 唯一 `admin_base`，不能复制 v2
  `controller/main/legacy` 或第二套 `.side`，不能修改 donor 文件。
- **鉴权/中央层**：HTTP/auth、`admin_action_token`/CSRF、Composition Root、OpenAPI、
  `internal/platform`、`internal/access`、`internal/webshell` 均由 Terra/Root 负责；本
  commit 不触碰这些文件。

若适配需要跨域 Store、Customer/OneID、secret plaintext、Provider write、第二主写或
绕过 release/outbound 对账，应停止并回报，不在 donor 分支中自行改中央层。
