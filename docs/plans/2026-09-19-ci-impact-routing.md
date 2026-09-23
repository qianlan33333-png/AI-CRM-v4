# CI 影响范围分流方案

## 业务判断

目标是减少无关 GitHub Actions lane 的等待，同时保留最终合并和发布的可审计门禁。GitHub workflow 本身始终触发，不使用顶层 `paths` 过滤，避免 required check 因 workflow 被跳过而长期 Pending。

## 分类

OneID：不涉及。本次只修改 CI 编排、验证报告和测试，不读写客户身份。

Persistence：stateless。只生成临时 CI 计划和报告，不修改业务数据库、迁移或持久任务。

External Effects：不涉及。不改变 Provider、外部效果、部署脚本或生产配置。

## 分流规则

- `high` 风险、`.github/`、`scripts/ci/`、`docs/governance/`、`deploy/`、`Makefile`、`AGENTS.md` 和共享平台/Composition/迁移改动：运行全部 canonical lanes。
- `medium` 风险：始终运行 `preflight`、`governance`、`backend`、`frontend`、`browser`；能力映射涉及 archive-sdk 时再运行该 lane。现有 registry 不是完整依赖图，不能据此跳过核心回归。未知 lane 或空映射回退全量。
- `low` 风险且所有改动均为 docs 下非治理 Markdown 文档：只运行 `preflight` 和 `governance`。
- `check` 根据计划明确的 lane 集合验收；被计划跳过的 lane 必须是显式 skipped，不得以失败、取消或未知状态通过。
- `main` 只有在找到同一 Git tree 的完整成功 PR 证据时才复用；没有完整证据仍运行全部 lanes。影响分流只优化 PR 返工，不降低主线发布门禁。

## 验收

验证高风险改动仍选择全部 lanes；中风险保留核心回归并按映射选择 archive-sdk；低风险改动不触发重型 lanes；未知/保护路径升级为 high；workflow 语法和现有 governance/verification 测试通过。

## 边界与参考

本次是影响范围分流，不是本地 full 证明上传机制；本地测试结果不能自动替代云端完整证明。GitHub 自身门禁修改仍须全量验证。

参考 GitHub 官方的 job 条件控制与 required checks 规则，以及 dorny/paths-filter 的按变化决定下游任务案例。实现复用项目现有 governance impact，不引入第三方 Action。

- https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-jobs-with-conditions
- https://github.com/dorny/paths-filter
