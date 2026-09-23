# Survey PRD-02 实施与验收证据

本轮分类：问卷 OAuth/外部身份通过既有 Identity Port 解析；提交、Survey 回执、审计、Outbox 与 External Effects 接纳在同一 PostgreSQL Unit of Work 中提交。Provider 网络调用在事务外，默认关闭。

| 能力 | 实现与覆盖 |
| --- | --- |
| 提交完成外推 | `SubmissionService` 在提交事务中冻结配置、结果、答案版本及加密发送身份，绑定 `KindSurveyCompletion` 效果；重复提交复用既有绑定。 |
| 外推协议与安全 | Outbound 仅在启用的配置目标执行，使用注册 Webhook 的 client-id、Unix 秒 timestamp、event-id、HMAC 签名；禁止重定向，端点和查询参数不进入错误文本。 |
| 身份和数据边界 | 缺少可信身份保留未发送状态；身份读取错误使接纳事务回滚。外部身份、手机和答案均不进入 EER、日志或明文 JSON。 |
| 配置与旧页面入口 | Host Adapter 在问卷操作页提供 type/day/frequency/expires/remark 与键值 custom params 的编辑、重开回填和保存；URL/Secret 仍只属于部署配置引用。 |
| 并发保存 | metadata 写入的 `configuration_version` 是 CAS 条件。页面在写前读取版本；后端和 PostgreSQL 更新均验证版本。409 时界面显示重新打开提示，不显示已保存。既有运营页面的仅开关/引用 PUT 保持兼容并保留 metadata，仍由服务端 CAS 保护。 |

本地已通过：

```
GOCACHE=/private/tmp/aicrm-survey-gocache GOFLAGS=-p=1 go test ./internal/survey/app ./internal/survey/http ./internal/survey/store
node internal/webshell/static/admin_console/survey_qr_bridge.test.mjs
```

第二个命令覆盖零回执页面可编辑、CSRF header、参数回填保存以及 409 冲突提示。HTTP 单元测试覆盖 CSRF 拒绝、过期配置版本，以及既有 `saveQuestionnaireOpsDto` 的 completion→external-push 顺序保存后 metadata 不丢失；前端契约测试确认该旧 DTO 仍只发送开关和配置引用。PostgreSQL 集成测试覆盖 A 读取配置、B 改开关和引用、A 带旧版本保存时的冲突，以及审计和 Outbox 无残留。

本机未配置隔离 `DATABASE_URL`；本地运行该 PostgreSQL 集成测试会明确跳过。此前尝试启动本机隔离 PostgreSQL 被沙箱共享内存限制阻断，未触及任何已有数据库。PR CI 的 PostgreSQL 16 service 是该集成测试的待验证环境；本文不将其标记为已通过。

## 增量：旧版“测试外推”闭环（0073）

旧版管理员测试按钮不是禁用操作收据：它冻结 `user_id=questionnaire_test`、空答案、`phone_number="NULL"`、`is_test=true` 与独立 `test_run_id`，再交给已有外推链路。本增量在 Survey-owned `survey_completion_test_push_snapshots` 保存不可变合成正文和非密目标策略；不创建或借用 Customer/Submission，不保存身份、手机或答案。`KindSurveyCompletion` 仍由 Outbound/EER 执行，Provider 默认关闭，网络不在提交事务内。

| PRD-02 能力 | 代码证据 | 自动测试证据 |
| --- | --- | --- |
| 管理、复制、发布、禁用与幂等 | `internal/survey/app/questionnaires.go`、`internal/survey/http/handler.go` | `TestQuestionnaireLifecycleCreatesDuplicatesAndPublishesIdempotently`；`TestPostgreSQLSetStatusPersistsReceiptAuditAndOutboxAtomically` |
| 四题型、必填/长度/选择校验 | `internal/survey/domain/questionnaire.go` | `TestQuestionnaireSupportsFourQuestionTypes`、`TestAnswerValidationFailsClosed` |
| 评分、维度、推荐规则 | `internal/survey/domain/assessment.go` | `TestAssessmentScoresDimensionsTypesAndOverallLevel`、`TestAssessmentRejectsOverlapsAndUnknownReferences` |
| H5 与冻结 donor 宿主 | `internal/survey/ui.go`、`internal/survey/http/handler.go` | `TestSurveyUIUsesFrozenFragmentAndV3Assets`、`TestSurveyPublicUIServesOnlyH5AndImmutableAssets` |
| OAuth 与 OneID 信任边界 | `internal/survey/app/oauth.go`、`internal/survey/provider/wechat_oauth.go` | `TestOAuthConsumesStateAndProvisionsOnlyProviderVerifiedFact`、`TestEnabledOAuthRequiresOpenPlatformScopeAndInteractiveScope`、`TestPublicSurveyCannotBypassOAuth` |
| 提交、结果快照、外推接纳 | `internal/survey/app/submissions.go`、`cmd/aicrm/survey_completion_adapter.go` | `TestSubmissionAcceptsAndBindsConfiguredCompletionOnce`、`TestPostgreSQLCompletionReceiptBindsReadsAndRollsBackAtomically` |
| 历史归属导入与加密快照 | `cmd/migrate-survey-v2`、`internal/survey/store/postgres.go` | `TestHistoricalQuestionnaireAuditParametersHaveConcretePostgreSQLTypes`、`TestEncryptedSnapshotAuthenticatesAndRejectsTampering`、`TestReadKeyRequiresOwnerOnlyPermissions` |
| 旧版测试外推、参数过滤、重放和结果回读 | `QueueCompletionTest`、`ReadCompletionPayload`、`SurveyCompletionProvider` | `TestQueueCompletionTestFreezesSyntheticRequestAndReplaysSameEffect`、`TestExternalPushTestUsesSyntheticQueueAndFailsClosedWhenDisabled`、`TestPostgreSQLSyntheticCompletionTestSnapshotReplaysWithoutCustomer` |
| 重启执行一次、unknown 不盲重试、配置漂移和重定向 | `external_effects` + `SurveyCompletionProvider` | `TestPostgreSQLSurveySyntheticPushSurvivesRepositoryRestartAndDoesNotBlindRetryUnknown`；`TestSurveyCompletionProviderDoesNotForwardBodyOnRedirect`；`TestSurveyCompletionProviderRejectsTargetConfigDriftBeforeCallingNewEndpoint` |

未冻结的 V3 Host `internal/webshell/static/admin_console/survey_operations.js` 在原有外部推送卡片和日志容器上做窄挂载：冻结的 queued-only mapper 在初始化期间只得到空记录，Host 保留提交后动作、渠道选择和原测试按钮，直接调用 `POST /api/admin/questionnaires/{id}/operations/external-push/test` 并在原日志容器重新读取本问卷与全局真实记录。请求携带 Host 的 CSRF header；接受响应只陈述“测试已创建”，不会把已发生的尝试或结果固定显示为零/否。Host 也保留开关、配置引用和参数的 CAS 保存。`survey_qr_bridge.test.mjs` 覆盖冻结壳接管、旧按钮屏蔽、一次 POST、CSRF、queued/executed/outcome_unknown 与 disabled 历史记录的浏览器旅程；`survey_operations_frozen_runtime.test.mjs` 使用构建后的真实问卷运营模板和冻结控制器验证保留提交后动作、原测试按钮、外推参数保存、当前/全局真实日志和切换后不重绘。`TestExternalPushTestRequiresCSRFBeforeAcceptingSyntheticEffect` 证明拒绝时不会接纳效果。禁用配置返回 `409 provider_disabled` 且零效果接受/零 Provider 调用。

本次本地已通过：

```
GOCACHE=/private/tmp/aicrm-survey-gocache GOFLAGS=-p=1 go test ./internal/survey/app ./internal/survey/store ./internal/survey/http ./internal/outbound ./cmd/aicrm
npm run typecheck -- --pretty false
npm run admin:adapter:contract
npm run transport:contract
npm run orval:check
```

本机仍未配置隔离 `DATABASE_URL`，上述 PostgreSQL 集成测试在本机按测试代码跳过；它们会在 PR 的 PostgreSQL 16 service 中执行。不得将这次本地跳过标为 PostgreSQL 已通过。

本增量的 River 证据使用 `platformjobqueue.NewRuntime` 启动实际 worker：先由事务入队，再启动 runtime 等待本地 HTTP 接收，停止后以新 runtime 重启并确认不重复写入。`outcome_unknown` 使用同一实际 runtime 路径，重启后保持一次调用；执行成功、调用后超时和调用前拒绝分别持久化真实的尝试、调用及结果事实。它不是手动 `RunAttempt` 或仅重建 Repository 的替代测试。全局测试记录页保留合成测试的文本运行 ID、旧数字记录 ID、真实状态和可用的尝试事实；页面分别展示等待结果、已收到结果、结果待确认或当时未启用外推配置。

部署待办：在 PostgreSQL 16 上顺序应用 `0066_channel_welcome_intents.sql`、`0074_survey_external_operation_execution_facts.sql` 与 `0075_external_effects_survey_completion_kind.sql`。0075 由 External Effects Owner 维护，只注册 `outbound/survey_completion`，且保留 welcome 队列约束和既有 Outbound、Payment owner/kind 组合；0074 的旧行保持未知，不能据此显示“未调用”或“未收到结果”。
