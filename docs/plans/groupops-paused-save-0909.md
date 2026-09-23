# 停用计划基础配置保存

OneID: 不新增身份；沿用已有本地运营人员 Port 校验。
Persistence: 计划本地持久化，沿用 plan 锁、CAS、幂等收据、审计的同一 PostgreSQL UoW。
External Effects: 保存不创建外部效果、任务或重试；保留停用状态，旧执行继续受状态与版本约束。

只允许 paused 的 plan_update，不放开 active/archived 或其他维度写操作。保存名称、类型和明确选择的有效运营人员后仍停用，启用仍须显式操作及完整定义校验。
