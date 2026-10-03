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

本地 AI 根据准确 diff、直接调用点和字段直接消费者给出相关包/具名用例短清单。取消完整依赖闭包、登记证明、全量对照和未知自动扩散。小包整包测，大包选用例；身份、支付、权限与数据变动只加强对应合同。局部证据明确标注范围，不能称全量回归。

## Small-step delivery

- Keep the implementation and its child candidates in the same Codex task. Each candidate delivers one independently releasable, reversible user-visible behavior or clear defect with related tests. Split by behavior, not line or file quota. 日常候选是国内 `codex/*` 分支的准确 SHA/base；GitHub PR 仅作人工归档，不决定部署。
- Use the Product Design plugin/skill before implementing any sidebar, customer-facing, or admin UI.
- 发布由唯一路由指定的工作台完成；仅用户或适用规则明确要求代理时，使用一个 `gpt-6-luna` max agent 负责本次发布全程，不为构建、交接、观察各开任务。其他开发模型不受限。
- Preserve exact head/tree and selected scope. Unknown/shared/policy changes use the AI list; no automatic full fallback.
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` shows the AI plan in `docs/release-checks.json`. Run only its checks; reuse valid unit results and test caches. Required Linux/PG/browser checks execute once in that environment. Empty/skip/unknown results are incomplete. Failures rerun only affected checks.

Routine implementation choices within an authorized PRD do not need another confirmation. Revisit the brief only when business scope, data ownership, or an external contract materially changes.

## 日常交付

使用 [日常国内发布](../../docs/operations/domestic-main-release.md#工作台交接和当前基线)。国内准确候选 ref 实际推送后，读取 `/Users/qianlan/Downloads/新CRM/release-control/workstation.json`，通过 `send_message_to_thread` 向其 `thread_id` 实际投递原任务 ID、base/head/tree/ref、五项影响、测试收据、未验证项和授权范围；不硬编码旧工作台 ID，不只写“等待发布”，不自行安装共享预发或生产。不创建 merge-preview、handoff 事件或延期验收日期。普通发布沿用同包路径和现有队列/锁；源码、收据和生产身份出现无法解释的不一致时停止对账。

## 2026-10-03 极简发布

遵循唯一 [发布流程](../../docs/operations/domestic-main-release.md) 和 [获批 PRD](../../docs/prd/2026-10-03-extreme-release.md)。本地 AI 列相关包及具名用例；取消日常全量回退和重复准备，源码先累计，最终构建/预发各一次，已有授权同包生产并独立读回。失败只修复并重跑受影响检查；安装不明停止写入对账。沿用现有队列/锁/attempt，不增加审批或新发布通道。
