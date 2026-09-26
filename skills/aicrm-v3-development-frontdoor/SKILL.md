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

新增权限、长度、格式、数量、超时、重试或审批限制前，应用 [核心 Skill 的限制必要性判断](../aicrm-v3-development/SKILL.md#限制必要性判断奥卡姆剃刀原则)，在父 PRD 或 PR 简短记录依据；没有新增限制时写“不涉及新增限制”。该要求仅针对今后新开发，不启动既有限制审计。

## Small-step delivery

- Keep the implementation and its child PRs in the same Codex task. Each PR delivers one independently mergeable, reversible user-visible behavior or clear defect, with its related tests in that PR. Split by behavior; do not use line or file quotas.
- Use the Product Design plugin/skill before implementing any sidebar, customer-facing, or admin UI.
- Diagnose or fix release failures with a separate `gpt-6-luna` max agent; this does not restrict model choice for other work.
- Preserve exact head/tree and test scope in PR evidence. Unknown, shared, migration, executable check-policy, or release-path changes remain conservative.
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` prints a shadow candidate beside the existing enforced plan. A normal local run executes the candidate lanes, full tests for affected Go packages (including new tests), and mapped checks. Missing environment or execution evidence is incomplete, never a pass. This does not alter GitHub's required `check`.
- PR2 collects ten distinct PR observations from the already required CI receipts; collecting them does not rerun tests. Paired timing evidence is separate and must come from actual full and targeted lane receipts on the same SHA/environment/cache, with at least three distinct PR pairs per candidate class and at least 30% p50 savings. Capability cost inventory binds each business capability to explicit PR numbers under the parent PRD. For each listed PR, use `python3 scripts/ci/affected_shadow.py export-ci-history --github-repo <owner/repo> --pr-number <number> --out ci-history.json`, then pass that complete export as `pair --ci-jobs`; all CI workflow runs and attempts, including failures, cancellations, and terminal runs with no jobs, count toward summed CI cost. Missing pagination, active runs, or unowned same-branch runs make the history incomplete. Add the existing release `state.json`, matching `domestic-release.json`, and `last_release_timings_seconds.total` for deployment cost. Any confirmed omission, unknown candidate result, missing replay, incomplete PR inventory, or missing cost keeps the existing gate active. PR2 also records daily full regressions; the candidate remains shadow-only.
- Treat the parent-PRD owner's reviewed capability-to-PR list as the authoritative trial scope. The evaluator proves coverage only for listed PRs; it cannot establish that the list exhausts a business capability. Until the scope is reviewed and recorded, do not claim complete capability cost or enable a faster gate.

Routine implementation choices within an authorized PRD do not need another confirmation. Revisit the brief only when business scope, data ownership, or an external contract materially changes.
