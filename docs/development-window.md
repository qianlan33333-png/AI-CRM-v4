# 开发窗口合同

开发窗口以 `handoff_ready` 结束。原始开发任务负责业务判断、实现、本地验证、受影响板块的预发布验收和准确交接；发布指挥台负责只读复核、串行队列、合并、同包晋级、生产读回和观察。

```text
business_decision -> frozen_prd -> code_complete -> local_verified
-> staging_built -> staging_accepted -> handoff_ready
```

用户明确要求把真实业务验收放到生产后时，`staging_accepted` 只表示技术验收，必须附独立 deferred acceptance，列出具体未验证旅程、Owner、截止时间和后续动作。它不能被写成 `business_verified` 或 `released`。

handoff 包含公开仓库、PR、独立 worktree、准确 base/head/preview SHA 与 tree、package 摘要、OneID/Persistence/External Effects 分类、验证证据、风险、回滚点和 receipt。候选入队后冻结；任何源码修复都产生新 commit、候选、receipt 和事件。
