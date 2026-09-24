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

## Small-step delivery

- Keep the implementation and its child PRs in the same Codex task. Each PR delivers one independently mergeable, reversible user-visible behavior or clear defect, with its related tests in that PR. Split by behavior; do not use line or file quotas.
- Use the Product Design plugin/skill before implementing any sidebar, customer-facing, or admin UI.
- Diagnose or fix release failures with a separate `gpt-6-luna` max agent; this does not restrict model choice for other work.
- Preserve exact head/tree and test scope in PR evidence. Unknown, shared, migration, executable check-policy, or release-path changes remain conservative.
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` prints a shadow candidate beside the existing enforced plan. It does not alter GitHub's required `check`; ordinary local execution remains `fast` plus `compile` for Go changes. Activate only after 10 valid PRs show zero known omissions, at least 30% paired median time reduction, and no per-capability check-time increase; these user-authorized criteria need no repeated confirmation.

Routine implementation choices within an authorized PRD do not need another confirmation. Revisit the brief only when business scope, data ownership, or an external contract materially changes.
