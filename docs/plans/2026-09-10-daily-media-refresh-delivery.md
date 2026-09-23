# Excel 独立发送与素材每日刷新：本地交付记录

## 范围与状态

工作区：`/Users/qianlan/Downloads/新CRM/daily-media-refresh`。
分支：`codex/daily-media-refresh`，开发基线：`dc640ee157634345744aabeabcf423ff819e7210`。
本轮只执行本地开发、审查和隔离验收；未推送 GitHub、未合并、未部署，也未向真实企微上传文件或发送消息。

本地开发与验收已完成。代码保留在该分支工作区，未创建提交。前端检查生成的本地构建包已移至 `/tmp/aicrm-daily-media-build-artifacts-0aexqajh/release`，不混入源码改动。

每日北京时间 02:00 全量刷新启用的临时媒体素材；继续使用现有素材编号和原文件。Excel 与素材准备分别使用现有 River 中独立的 3 / 2 并发队列，不新增主动 QPS 限速。并发数针对当前单 effects-worker 进程；本轮未改变生产副本数。

每日轮次、分页、共享文件去重、旧有效凭据保留、失效准备及显式限流退避均使用现有持久任务与 External Effects。Provider 结果不明时不盲重试。刷新媒体凭据不改变 Excel 已冻结的封面内容与审核记录。

素材上传默认超时 120 秒，上限 240 秒；对应 Worker 270 秒，低于现有 300 秒执行租约。普通消息 HTTP 超时保持现有值。

## 本地验收证据

使用独立本地 PostgreSQL 数据库、临时 Python SQLite、真实 XLSX、实际 Host 和 Chromium；企微只使用本地测试端点。

| 检查 | 结果与证据 |
|---|---|
| Python Excel 组件 | 12 项通过；`/tmp/aicrm-daily-media-refresh-0910/reports/` |
| Go 静态检查 | `go vet ./...` 通过；同上 |
| 真实 Excel / Media / GroupOps 浏览器旅程 | 最终业务修改后均执行并通过；`/private/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/aicrm-preflight-ibhdfk04` |
| 浏览器完整门禁 | 退出 1：全部可执行旅程通过，但两条既有 Linux-CDP 专用旅程在 macOS 被跳过；不能记作完整门禁通过 |
| 正式前端 CI 阶段的本地复跑 | 通过，退出 0；`/tmp/aicrm-daily-media-frontend-final.log`；使用 Node 24.18.0 与固定 donor SHA，执行 CI 的八条正式前端命令，包括类型、DOM、Host、冻结合同及最终包布局 |
| 完整 Go race 与 PostgreSQL 回归 | 专用数据库下 `go test -p 1 -race -count=1 ./...` 全部通过，退出 0；`/tmp/aicrm-daily-media-refresh-0910/reports/go-test-race-final-rerun.log` |
| 最终 fast / compile | 均通过；最后回归修复后的 fast 为 `/private/tmp/aicrm-daily-media-root-final-fast/summary.json`；compile 证据为 `/tmp/aicrm-daily-media-refresh-0910/reports/compile-final-rerun/summary.json`，最终整仓 race 另覆盖完整编译 |
| 新数据库迁移 | River 与 0125 / 0126 迁移及回读通过；`/tmp/media-core-final-migrations.log` |
| 媒资与 EER PostgreSQL 专项 | 通过；`/tmp/media-core-final-material-pg.log`、`/tmp/media-core-final-eer-pg.log` |
| GroupOps 真实队列自动恢复 | `TestGroupOpsSharedRiverMaterialPreparationAutoResumes` 通过；`/tmp/aicrm-daily-media-refresh-0910/reports/groupops-material-auto-resume-final.log` |
| 旧流程回归修复 | 首轮整仓检查发现的 7 个失败用例集中复跑全部通过；`/tmp/aicrm-daily-media-refresh-0910/reports/cmd-seven-failures-rerun-final.log`；覆盖纯文本无素材依赖、发送前本地阻断、真实封面摘要、业务范围任务计数及跨入口媒体复用 |

两条平台跳过项：`TestPostgreSQLOpenPlatformV1ChromiumJourney` 与 `TestPostgreSQLProductExternalPushChromiumJourney`。本机没有 Docker，未修改平台保护来制造通过结果。

浏览器确认：上传后为未批准草稿，真实 Python 解析五个必填字段及可选分层；素材首次准备及手动刷新得到不同媒体凭据；缺失原文件明确提示补传；未产生消息意图或批准计划。

GroupOps 队列旅程单独验证：只接受一次原发送意图，冻结不含临时媒体 ID 的内容快照；素材未就绪时原 EER 保持 queued、attempt 为 0，River 持久 snooze；本地测试上传完成后，同一 River job 自动继续，最终恰好一次上传和一次消息 Provider 调用，无需再次点击。这里的 Provider 均为隔离测试端点，不代表真实客户已收到消息。

专项细节参见 [Media 验证](2026-09-10-media-catalog-verification.md)、[UI 验证](2026-09-10-media-ui-verification.md)、[核心验证](2026-09-10-media-core-verification.md)、[核心审查](2026-09-10-media-core-review.md) 和 [接口契约](../contracts/media-preparation-api.md)。

## 本轮 GitHub 合并与本地 SSH 发布授权

上文“只执行本地开发、审查和隔离验收”的结论记录的是此前已完成的本地阶段。用户现已授权将本交付提交至 GitHub、在 required CI 全绿后合并，并以该精确 merge SHA 进行本地 SSH 生产发布。实际合并仍须等待 required `check` 成功及本地 SSH 可用性确认；实际生产发布仍须在合并后单独执行和记录。

`main` 的 GitHub Actions 会在 `check` 成功后自动触发云端 SSH deploy。本轮发布采用用户指定的本地 SSH 路径，因此合并后必须先按 merge SHA 找到并取消该 main-push workflow，确认其 deploy job 未启动，再进行本地发布。该取消只阻止重复的云端部署，不代替 CI、SSH、生产健康检查或真实 Provider 验收。

## 后续发布边界

真实企微吞吐、大文件实际网络耗时、员工执行与客户回执不属于本轮已验证事实。后续 GitHub、Linux CI、部署及真实发送验收应分别记录结果；本地测试通过不能代替这些步骤。
