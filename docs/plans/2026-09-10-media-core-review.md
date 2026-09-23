# 每日媒资刷新核心最终复查

复查范围为 `codex/daily-media-refresh` 相对 `dc640ee` 的媒资准备、每日刷新、External Effects 与其正常调用方。OneID 不涉及：本次没有客户或外部身份解析。持久化和外部效果涉及：准备记录、EER 接受/完成、刷新轮次和审计记录均由同一 PostgreSQL UoW 提交；上传只在 EER 提交后的 worker 边界发生。

本轮所有 PostgreSQL 证据使用专用本地库 `aicrm_daily_media_refresh_review_0910`。Provider 使用本地伪实现；没有发起真实企微或其他 Provider 写入。

## 已修复并复查

### 专用队列在首次接受和重试时保持一致

0125 将 `external_effects.delivery_lane` 作为持久调度策略写入，而不是从 kind 推测；见 [0125 迁移](/Users/qianlan/Downloads/新CRM/daily-media-refresh/migrations/0125_outbound_material_preparation.sql:2)。`AcceptAndQueueWithin` 写入 lane，[External Effects store](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/store.go:232) 在控制操作中锁定并读取它，[重试入队](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/store.go:515) 继续使用 `effectQueue(kind, lane)`。因此 `outbound_media` 与 `outbound_excel` 的重试不会回落到普通 `outbound`。

### 媒资 unknown 对账必须给出类型化结论，并原子投影

媒资 lane 的对账只接受 `no_effect`，或带 `outbound.material.upload.v1` artifact 的 `confirmed_effect`；其余 generic reconcile 被拒绝，见 [control 校验](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/store.go:460)。同一事务内，EER 状态和 completion sink 一起更新，[sink 调用](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/store.go:558) 将 `no_effect` 投影为确定失败，或把确认的 artifact 投影为 `executed`。

[MaterialPreparationService.CompleteEffect](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material.go:415) 锁住准备记录，拒绝没有 artifact、未来 provider 时间戳或错误状态的完成；确认上传后才切换 `is_current`，并在同一事务更新刷新 item 与轮次。这关闭了“EER 已对账但媒资永久 unresolved”的路径。`internal/outbound/material_postgres_integration_test.go` 和 `internal/externaleffects/preflight_postgres_integration_test.go` 覆盖 no-effect、确认 artifact、重放和冲突。

### 管理员强制刷新有稳定的幂等语义

[请求校验](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material.go:47) 要求任何携带 `ActorAdminID` 的请求同时为 force 且有操作键；HTTP `/prepare` 也拒绝非 force 请求，[handler](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/media/http/handler.go:196)。因此不存在“管理员带 key 的非强制 ready 结果没有回执”的可调用入口；内部 `ReadyForSend` 不含管理员 actor/key，负责自动准备。

准备服务按 cache key 和 operation digest 排序取得两把事务 advisory lock，并把命令摘要与回执一起读取；相同 key 不同 body 返回 `ErrMaterialOperationConflict`，[实现](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material.go:159)。这消除了并发不同内容请求把唯一约束错误暴露为暂时性服务器失败的问题。

### 凭据状态与最新刷新状态分开计算

当前有效凭据在普通准备时保留，强制刷新也只在新的明确上传成功后替换它，[现有凭据路径](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material.go:206)。`GetMaterialStatus` 依据 `expires_at` 计算 `ready`、`expired`、`missing` 和 `credential_usable`，[状态计算](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material.go:104)，所以一次最新刷新失败不会掩盖仍可用的旧凭据，过去的 expiry 也不能显示为可用。

### 日轮次按唯一文件推进，可从游标恢复

刷新项以 `(round_id, cache_key_digest)` 唯一，迁移见 [0125](/Users/qianlan/Downloads/新CRM/daily-media-refresh/migrations/0125_outbound_material_preparation.sql:83)。刷新执行在一页内和跨重启后都先按 cache key 查找并连接同一 item；见 [刷新实现](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material_refresh.go:179)。同内容的多个引用只创建一次上传和一个轮次成员，符合“轮次总数按唯一上传文件”的业务定义；unknown item 仍保留为 unresolved，跨日不会盲目创建另一个上传。

`source_count` 记录每个唯一上传文件在本轮中覆盖的来源引用数。0149 先以 `0` 保存已原子绑定 effect、尚未认领页面的成员；只有游标 compare-and-swap 成功时才在同一事务内累加页面来源数。游标未推进会在创建 effect 前失败，竞争或重入页面不会重复计数，且计数更新不覆盖已经写入的 Provider 终态。它不参与唯一文件总数、去重或状态机判定。

### Worker lease、预检与自动重试边界

EER worker 的最大执行时间为 4 分 30 秒，[worker](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/worker.go:24)，短于 5 分钟 EER lease；上传的 Provider timeout 为 120 秒。预检读取 queued envelope 后在事务外准备素材，[queued view](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/store.go:801)，权威的 queued/generation/job CAS 仍在 `RunAttempt` 内完成。对于已实际 HTTP 调用、但 Provider 明确拒绝且确认未产生业务效果的媒资/Excel lane，自动重试保留同一 effect、发送意图和原 lane，并使用有限退避；新的 generation 会创建新的 River job，[自动重试](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/externaleffects/store.go:743)。结果 unknown 不会被换 key 重发。

### GroupOps 首次接受后自动等待和续跑

GroupOps 接受时冻结的是来源与 provider-neutral 字段：`ResolveMaterialIntentSnapshot` 生成 source-only typed facts，[adapter](/Users/qianlan/Downloads/新CRM/daily-media-refresh/cmd/aicrm/group_ops_adapters.go:568)。Group EER 预检对同一已接受 effect 请求 `ReadyForSend`；素材尚 queued 时 job snooze、attempt 保持 0，素材完成后同一 Group job 使用刚取得的 MediaID 继续。不会要求操作员进行第二次 Accept。

真实 PG 用例 `TestGroupOpsSharedRiverMaterialPreparationAutoResumes` 已通过，日志为 `/tmp/aicrm-daily-media-refresh-0910/reports/groupops-material-auto-resume-final.log`。它断言一次 Accept、同一 River job、queued group effect/attempt 0、`metadata.snoozes` 增长、媒资 effect 持久化、释放本地上传 gate 后同一 job 发送。River v0.24 对一秒 snooze 可保存为 `available`，测试相应接受 `available|scheduled` 而不把瞬态调度状态误当合同。

### 正常组合路径不再按消息上传原始 bytes

`cmd/aicrm` 的正常 composition 同时注入 `MaterialSourceReader`、`MaterialPreparer`、scope digest、通用 uploader 和 `MaterialEffectMux`，[组合根](/Users/qianlan/Downloads/新CRM/daily-media-refresh/cmd/aicrm/composition.go:1340)。私聊图片、小程序封面、文件在该配置下调用 `readyFrozenMedia`，构造只含 `MediaID` 的 attachment；预检只读冻结事实并安排准备任务，从不返回 bytes，[AI adapter](/Users/qianlan/Downloads/新CRM/daily-media-refresh/cmd/aicrm/aiassistant_adapters.go:558)。侧边栏图片也先读 source snapshot、调用 `ReadyForSend`，最终仅将 `MediaID` 写入 SDK payload，[sidebar adapter](/Users/qianlan/Downloads/新CRM/daily-media-refresh/cmd/aicrm/sidebar_image_preparation.go:23) 和 [handler](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/sidebar/handler.go:703)。

WeCom client 的 `uploadPrivateImage` / `uploadPrivateFile` 仍保留在 [兼容调用](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/wecom/adapter/client.go:1564)，用于历史直接 payload 或旧在途效果；不能据此宣称所有旧调用点已经删除。新建的正常组合任务按 effect binding 优先交给 Generic material provider/completion，[mux](/Users/qianlan/Downloads/新CRM/daily-media-refresh/internal/outbound/material_mux.go:18)，并由上述 sources/preparer 使 image/file/miniprogram payload 带 MediaID、没有 Content bytes。`TestAutomationAIAssistantAndGroupOpsShareRiverRuntime` 已调整为真实通用准备链路，断言两个去重上传而不是每条消息逐字节上传。

## 验证证据

- `TestGroupOpsSharedRiverMaterialPreparationAutoResumes`：通过；`/tmp/aicrm-daily-media-refresh-0910/reports/groupops-material-auto-resume-final.log`。
- 首轮失败的七个 `cmd/aicrm` 目标在修复后集中通过；`/tmp/aicrm-daily-media-refresh-0910/reports/cmd-seven-failures-rerun-final.log`（`ok .../cmd/aicrm 33.644s`）。
- Excel Python suite：12/12 通过；`/tmp/aicrm-daily-media-refresh-0910/reports/excel-unittest-final.log`。
- `go vet ./...`：通过；`/tmp/aicrm-daily-media-refresh-0910/reports/go-vet-final.log`。
- `scripts/dev_preflight.py compile`：通过；`/tmp/aicrm-daily-media-refresh-0910/reports/compile-final-rerun/summary.json`。
- 最终全仓 `go test -p 1 -race -count=1 ./...`：通过，退出码 0；`/tmp/aicrm-daily-media-refresh-0910/reports/go-test-race-final-rerun.log`。

浏览器阶段使用本地 Chromium 和伪 Provider；Linux-only browser cases 在本机平台按脚本条件跳过。它们与真实 Provider 接受、真实上传回执是不同的验证边界，本轮没有把这些跳过或本地伪回执当作真实 Provider 验收。
