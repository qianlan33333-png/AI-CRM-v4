# 核心产品 AI 试运行上下文水位修复 PRD

## 业务判断

- 已启用的核心产品、已保存/发布的分配规则和已配置模型应能启动试运行。
- 每次试运行在接受外部效果前，必须以同一 UTC 时间水位冻结问卷、最近 20 条聊天、标签和激活信息。
- 聊天历史读取缺少必填 `Watermark` 时，不得将其误报为产品、规则或模型配置缺失。

## 现网失败证据

- 生产运行时配置 revision 2 已开启 `ai_agent_generation.enabled`，API、worker 和 effects-worker 均已读取。
- 客户编号 `1003540` 试运行仍在接受推荐任务前返回 `503 temporarily_unavailable`，没有产生 `segment_core_recommendations` 或 Provider 调用。
- `dynamicGenerationContextAdapter` 传入的消息查询没有 `Watermark`，而 Message Archive Store 明确拒绝零水位。

## 复用与架构分类

- OneID：读取 canonical customer；不新建身份匹配或客户主键。
- Persistence：Provider 读取 + 既有 Provider 写/External Effect；不改动现有事务、幂等、队列、重试或对账边界。
- 复用现有 `archiveport.CustomerQuery.Watermark` 和 Open Platform 已有的有界聊天历史读取模式，不建立新查询通道。
- GitHub 并行检查：未发现修改同一上下文适配器的开放 PR；仓库已有 `open_platform_activity.go` 作为同类水位参考。

## 实现与成功标准

1. 单次冻结只计算一次 `now().UTC()`，问卷和聊天查询使用同一水位。
2. 回归测试必须同时断言两个读取端口收到非零且相同的 UTC 水位。
3. 生产部署后，客户编号 `1003540` 的试运行能够入队，并读回为 `previewed`、`unassigned` 或明确的模型失败分类；不得再在上下文冻结阶段返回通用 503。

## 验证、发布与回滚

- 先运行定向 Go 测试，再运行 `dev_preflight.py fast` 和 `compile`，提交 GitHub PR 并等待当前 head 的 required checks。
- 合并后从准确 merge SHA 构建 Linux amd64 发布包，通过已验证 SSH 主机密钥安装。
- 上线读回版本、健康、运行时 revision、推荐任务状态和 Provider 尝试证据。
- 回滚代码时回到上一个发布 SHA；运行时 revision 2 是独立的模型启用决策，除非 Provider 风险要求停用，不随代码回滚自动撤销。
