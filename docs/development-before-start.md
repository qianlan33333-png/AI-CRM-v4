# 开发开始前

1. 只使用当前 AI-CRM-v4 仓库和准确 `main`；在同一 Codex task 中使用新的 `codex/<work-item>` worktree/分支。
2. 写出用户动作、状态/错误分支和验收；检索 GitHub/成熟产品参考，并确认仓库复用点。
3. 为父任务形成一份简短 PRD，包含业务流程、验收、回退及 OneID、Persistence、External Effects 分类。父 brief 获授权后，子 PR 复用它，只记录自身范围差异，不重复确认。
4. 每个 PR 包含一个独立可合并、可回退的用户行为或缺陷及其测试；按行为边界拆分，不按行数配额拆分。涉及 UI 时，编码前使用 Product Design 插件/skill。
5. 查看共享文件与并行改动，保存准确 base/head/tree 和工作树状态；基线、映射或边界不明时使用全量检查。

新增限制前，应用 [核心 Skill 的限制必要性判断](../skills/aicrm-v3-development/SKILL.md#限制必要性判断奥卡姆剃刀原则)，先判断不限制是否仍满足业务、安全和资源要求，再决定不加、复用已有边界或增加最小必要约束。仅约束今后新开发，不审计既有限制。

候选 `affected` 计划目前仅在影子模式观察：`--dry-run` 只打印绑定准确 Git SHA/tree 和策略指纹的计划；普通本地运行会执行计划中的 lane、受影响 Go 包完整测试（含新增测试）和登记检查，缺少环境或收据时报告不完整。GitHub 现行必需 `check` 不变。至少观察 10 个有效 PR，并达到零已知漏选、每个拟启用类别有至少 3 个真实配对且 p50 耗时下降至少 30%、每个能力的完整 CI wall time 加部署总耗时不增加，才按已授权标准启用；能力计时必须绑定同一 run/attempt 的 plan 开始至 required `check` 完成，以及 source-bound 发布收据。否则继续影子观察，无需重复确认既定标准。

受保护 `main` 与准确提交的必需 `check` 决定能否进入国内构建队列。合并后预备机按第一父链处理提交，基础验收后通过内网晋级同一文件树。业务真实验收在生产技术安装后独立记录。见 `docs/operations/domestic-release.md`。发布失败诊断和修复由单独的 `gpt-6-luna` max agent 执行；其他工作不受此模型限制。
