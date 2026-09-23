# 开发窗口合同

开发窗口以 `handoff_ready` 结束。原始开发任务负责开发前业务判断、GitHub 参考检索、
复用评估、确认 PRD、实现、本地验证、受影响板块的预发布验收和准确交接；发布指挥台
负责只读复核、串行队列、合并、同包晋级、生产读回、观察和打回，绝不修改候选源码或 PR。

```text
business_decision -> github_reference -> frozen_prd
-> code_complete -> staging_built -> staging_self_accepted -> handoff_ready
```

该四级链用于运行时变更。纯治理/文档变更在 `code_complete` 后完成治理检查即可进入
`handoff_ready`；`staging_built`、`staging_self_accepted`、runtime package 和 staging
app install 均标记 N/A，不生成占位包或收据。

`code_complete` 只证明干净提交和适用本地验证；`staging_built` 只证明准确候选已由唯一
Linux amd64 节点构建；`staging_self_accepted` 才表示原开发任务已完成受影响业务旅程和
技术读回；`handoff_ready` 还要求不可变交接与持久事件完整。虚拟 Provider 旅程必须标记
`effect_mode=virtual`，只能证明虚拟合同和状态读回，不能冒称 live Provider 业务验收。

运行时 handoff 包含公开仓库、PR、独立 worktree、准确 base/head/preview SHA 与 tree、
package 摘要、staging built/accepted receipt、受影响业务 readback、OneID/Persistence/
External Effects 分类、风险和回滚点。纯治理/文档变更标记 `governance_only`，runtime
package、staging app install 和业务运行时 readback 明确为 N/A，只附治理检查证据。

开发任务完成或返工时先写持久事件，再发送指挥台通知。候选入队后冻结；任何源码修复
都产生新 commit、候选、receipt 和事件，旧候选不可覆盖。
若 `code_complete` 后缺少真实预发证据，先用 `release_control.py handoff checkpoint`
在现有发布状态中写入 `blocked_development`，包括 owner、阻塞原因、重试条件与证据；
通知投递和 ack 不代表业务验收。完整 handoff 成功登记新候选才关闭该检查点。

用户明确要求把真实业务验收放到生产后时，必须附独立 deferred acceptance，列出具体
未验证旅程、Owner、截止时间和后续动作。技术验收、同包、版本、健康、认证和指定读回
不可延期，生产状态最多为 `observing`，不能写成 `business_verified` 或 `released`。
生产出现 `outcome_unknown` 时只读对账，不换幂等键重试或用其他候选插队。
