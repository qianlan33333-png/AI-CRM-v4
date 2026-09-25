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

- Keep the implementation and its child candidates in the same Codex task. Each candidate delivers one independently releasable, reversible user-visible behavior or clear defect with related tests. Split by behavior, not line or file quota. Before domestic activation a candidate is a GitHub PR; afterward it is a domestic `codex/*` branch submitted at an exact SHA and base.
- Use the Product Design plugin/skill before implementing any sidebar, customer-facing, or admin UI.
- Diagnose or fix release failures with a separate `gpt-6-luna` max agent; this does not restrict model choice for other work.
- Preserve exact head/tree and test scope in candidate evidence. Unknown, shared, migration, executable check-policy, or release-path changes remain conservative.
- `python3 scripts/dev_preflight.py affected --base SHA --head SHA --dry-run` prints a candidate plan. A normal local run executes its lanes, complete tests for affected Go packages (including new tests), and mapped checks. Missing environment or execution evidence is incomplete. Before cutover this does not alter GitHub's required `check`; after cutover the stage publisher independently verifies its trusted exact-SHA test receipt.
- The former PR2 ten-PR shadow trial remains historical evidence for the GitHub gate. Domestic cutover does not prove a faster check safe or faster. Keep unverified categories on complete checks; a confirmed omission or unknown receipt closes the relevant fast path.

Routine implementation choices within an authorized PRD do not need another confirmation. Revisit the brief only when business scope, data ownership, or an external contract materially changes.
