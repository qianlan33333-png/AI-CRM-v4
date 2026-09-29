---
name: aicrm-v3-development-frontdoor
description: "AI-CRM-v4 development intake: decide business flow, check references and reuse, write one reusable parent PRD, classify shared boundaries, and split work into independently releasable PRs. The v3 path name remains for compatibility."
---

# AI-CRM-v4 Development Frontdoor

Read repository `AGENTS.md` and `skills/aicrm-v3-development/SKILL.md`. Use the current v4 repository and exact Git source only; old repositories and runtimes are not dependencies.

## Before editing

1. Map user action, state changes, permission/error branches, success criteria, and real acceptance location. For a bug, record the root-cause hypothesis and reproduction evidence.
2. Search GitHub or established products for references, then identify repository-owned domains, components, and interfaces to reuse.
3. Write one concise parent PRD with the flow, interface/data boundaries, tests, acceptance, rollback, dependencies, and OneID/Persistence/External Effects classifications. If the user authorized the parent brief, child PRs inherit it and record only their scope delta; do not ask for the same approval again.

新增限制前使用 [核心 skill 的必要性判断](../aicrm-v3-development/SKILL.md#限制必要性判断奥卡姆剃刀原则)；没有新增限制时一句“不涉及新增限制”即可。


## 五项影响判断

每个候选在 PRD 和交付摘要中说明：

| 判断 | 必须说明 |
| --- | --- |
| 对外合同 | 接口、事件、错误行为及最终业务输出是否变化 |
| 业务机制 | 数据、事务、身份权限和外部效果是否变化 |
| 关联模块 | 直接、间接调用方及消费者，附影响依据 |
| 页面影响 | 页面、入口、展示和交互；后端变化也须评估 |
| 验证证据 | 模块完整测试、连接合同、业务旅程、最终输出及未覆盖项 |

作者声明由准确 diff、生产／测试／embed 依赖图和可信检查映射复核；填写“无影响”不能缩减检查。影响可靠时执行本模块全集、关联合同及最终输出；影响无法界定时扩大相关阶段，必要时全仓并说明原因。高风险加强相关断言，不直接扩大到无关业务。共享、迁移、策略和未验证类别保守执行；规则经完整结果对照后逐类启用。页面能力开发继续使用 Product Design。

## Small-step delivery

- Keep the implementation and its child candidates in the same Codex task. Each candidate delivers one independently releasable, reversible user-visible behavior or clear defect with related tests. Split by behavior, not line or file quota. 日常候选是国内 `codex/*` 分支的准确 SHA/base；GitHub PR 仅作人工归档，不决定部署。
- Use the Product Design plugin/skill before implementing any sidebar, customer-facing, or admin UI.
- 发布由同一负责人完成；需要代理时使用一个 `gpt-6-luna` max agent，负责本次发布全程，不为构建、交接、观察各开任务。其他开发模型不受限。
- Preserve exact head/tree and test scope in candidate evidence. Unknown, shared, migration, executable check-policy, or release-path changes remain conservative.
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` prints a candidate plan. A normal local run executes its lanes, named changed tests and whole affected packages only where a narrower relationship is unknown. Missing environment or execution evidence is incomplete. 发布者核对准确 SHA/tree、影响范围、环境和结果；相同适用 Linux 证据可复用，不因为进入发布阶段重跑相同测试。macOS 证据不替代必需 Linux 验证。
- The former PR2 ten-PR shadow trial remains historical evidence for the GitHub gate. Domestic cutover does not prove a faster check safe or faster. Keep unverified categories on complete checks; a confirmed omission or unknown receipt closes the relevant fast path.

Routine implementation choices within an authorized PRD do not need another confirmation. Revisit the brief only when business scope, data ownership, or an external contract materially changes.

## 日常交付

使用 [日常国内发布](../../docs/operations/domestic-main-release.md)。开发者只提供准确候选、相关测试和变化说明；不创建 merge-preview、handoff 事件或延期验收日期。普通发布沿用热修同包路径，工具维护和完整基线初始化只在实际需要时处理；源码、收据和生产身份不一致仍停止并对账。
