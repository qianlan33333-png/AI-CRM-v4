# 兑换码候选本机验证

基线：8245526b89d04111acb2b03f66416b9c10d4064e。父 PRD：2026-10-12-survey-redemption-attachments。

已通过：
- `go test ./internal/survey/app ./internal/survey/domain ./internal/survey/http -count=1`：相关小包整包检查。
- `go test ./internal/survey/store -run '^(TestPostgreSQLRedemption.*|TestPostgreSQLSubmissionClaimConcurrentReceiptReplay|TestPostgreSQLSubmissionRejectsDefinitionSwitchedAfterValidation)$' -count=1 -race`：本机隔离 PostgreSQL 16.13，真实抢码/回滚/复用收据/跨问卷/复制空库/再导入不恢复/旧版本竞争。
- `go test ./internal/outbound -run '^(TestSurveyCompletionRedemptionNeverExportsRawCode|TestSurveyCompletionProvider)' -count=1`：资格题安全兼容投影。
- `node web/v3/surveyRedemptionLibrary.test.mjs`：安全文本展示、导入未知结果用原 key、认证失败清除代码行。
- `node web/v3/surveyRedemptionH5.test.mjs`：全页/逐题必填、已使用话术、保留输入和原 key。
- `node web/v3/surveyOperationsHost.test.mjs`、`surveySubmissionResume.test.mjs`、`surveyPublicHost.test.mjs`：既有页面、授权和重试合同。
- `node scripts/validate-openapi.mjs`：507 路径及显式路由合同检查通过。

修正：Excel 无显式类型数字单元格最初逃过精度检查，现已覆盖 Unset/Number 两类；数字精度失败不再返回部分有效码。新 worktree 最初缺生成共享源视图，已按现有 prepare-consumer-views 补齐，并更新准确 V4 API 内容绑定/锁。未修改冻结源。

环境：macOS，Go 原生检查；Node 24.21.0 / npm 11.19.0。前端依赖安装有锁定工具版本提示；目标 Linux 运行环境和最终构件由工作台另验。

未验证：最终安装包/真实浏览器完整旅程、真实微信授权与 Provider 接收；附件属于后续成员。本机合同不是完整回归或生产安装。

授权：实现及候选交付，尚无本范围生产部署命令。后续附件成员仍在准备，工作台先累计源码，勿提前封批、构建或安装。
