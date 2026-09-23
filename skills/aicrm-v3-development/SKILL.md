---
name: aicrm-v3-development
description: Plan, implement, migrate, or review AI-CRM-v4 capabilities with an explicit first-pass decision on OneID, persistence, durable jobs, and External Effects. The v3 path name remains for tooling compatibility.
---

# AI-CRM-v4 Development Decision Manual

## Completion and notification contract

Before editing, classify the business judgment, acceptance journey, OneID,
Persistence, and External Effects boundaries. Completion requires a clean,
committed worktree and evidence bound to the exact tree and package. The origin
task emits `handoff_ready`; a returned source issue requires a new commit and
new handoff. A compile, mock, HTTP 200, synthetic fixture, or queued Provider
effect is not a completed business acceptance.

Use this skill after reading the repository `AGENTS.md`. Its purpose is to make OneID and durable execution deliberate shared foundations without turning them into universal dependencies.

## Start With a Two-Axis Classification

Before editing code, record a short decision in the implementation plan or PR:

```text
OneID: not involved | reads canonical customer | resolves identity | provisions customer | links/merges identity
Persistence: stateless | local transaction | internal durable job | Provider read | Provider write/external effect
```

If an axis is not involved, state why and continue without adding a dependency. Revisit the classification when scope changes.

## OneID Decision

OneID is involved when a capability reads or assigns a customer, accepts an external identity, correlates channels, or changes identity ownership.

When involved:

- Use `customers.id` as the channel-neutral customer key.
- Resolve external identities through `internal/identity/port`; do not query identity tables or reproduce matching rules.
- Preserve identity `kind`, `scope`, `value`, `assurance`, and `source`. OpenID and UnionID must retain their required scopes.
- Treat Provider verification as an internal Adapter fact. HTTP payloads cannot declare themselves verified.
- `Resolve` does not create a customer. Provision only through the explicit verified-identity use case.
- Keep uncertain evidence pending or conflict. Do not guess, silently merge, or bind to the nearest customer.
- Cross-root identity evidence creates an auditable merge candidate unless a separately approved workflow permits more.

OneID is normally not involved in local configuration, content definitions, product rules, diagnostics, or other records that do not identify a customer. Do not add `customer_id` merely for uniformity.

## Persistence and Execution Decision

Classify work by observable side effect:

### Stateless or local transaction

- Keep durable business state in PostgreSQL 16 under one table Owner.
- Commit business state, idempotency receipt, audit, and Outbox in one Unit of Work when they belong to one command.
- Use locks, CAS, or versions for concurrent changes.

### Internal durable job

Use `internal/platform/jobqueue` for restart-safe internal work such as scheduled evaluation, snapshot preparation, or directory refresh. An internal job is not automatically an External Effect.

Do not introduce an in-process ticker, a domain-specific worker framework, or another retry table.

### Provider read

Use the owning connector read Adapter, such as `wecom` for trusted directory facts. Record safe audit facts and failure stages, but do not label a read as an executed outbound effect.

### Provider write or other external effect

- The business domain freezes an immutable intent; it does not call the Provider.
- WeCom business writes go through the sole `outbound` owner.
- Submit the opaque intent through `internal/externaleffects/port`; reuse its queue, generation, lease/fence, attempts, receipts, retry classification, and reconciliation.
- Use stable source, target, payload, and policy digests plus a stable idempotency key. Retrying the same logical operation must not mint a new key.
- Keep raw customer IDs, `external_userid`, openid, phone numbers, message bodies, tokens, and Provider responses out of External Effects tables and structured logs.
- Store the `effect_id` binding in the owning business domain, then consume effect status through a stable read Port or versioned event. Never query another domain's tables.
- `accepted` or `queued` is not Provider success. `executed` is not delivery proof. `outcome_unknown` permits only original-key lookup, trusted callback, explicit reconciliation, or Provider-authorized idempotent retry.

Payment and refund effects must reuse these reliability semantics through an explicitly versioned payment contract. Never disguise them as outbound messages.

## Coordination Check Before Implementation

When OneID or External Effects is involved, confirm:

1. Which domain owns the business record and table?
2. Which stable Port or versioned event is used?
3. Does the command require one PostgreSQL Unit of Work?
4. What is the deterministic idempotency scope?
5. For an external effect, what are its kind and four immutable digests?
6. Where is the business record to `effect_id` relationship stored?
7. Who owns Provider reads, Provider writes, callbacks, and reconciliation?
8. How are restart, replay, concurrent execution, and `outcome_unknown` tested?
9. Has the implementation avoided a second customer identity system, queue, Worker, retry loop, or effect state machine?

Do not assume the current adapter participates in the caller's Unit of Work. Verify it. If effect acceptance opens an independent transaction while the business command requires atomicity, treat that as an architecture gap and fix the shared Port/adapter rather than compensating in the business module.

## Completion Evidence

开发进度必须使用四级完成状态，且逐级满足：

- `code_complete`：干净 commit 和适用本地验证完成。
- `staging_built`：准确 merge-preview 已由唯一 Linux amd64 节点构建，包和 built receipt
  绑定 PR/base/head/preview/tree。
- `staging_self_accepted`：原开发任务完成受影响业务 readback，并持有可校验的 accepted
  staging receipt。
- `handoff_ready`：不可变 handoff 与持久事件已完成，可以通知发布指挥台。

四级链适用于运行时变更。纯治理或文档工作在 `code_complete` 后以治理检查证据进入
`handoff_ready`，并把 `staging_built`、`staging_self_accepted`、runtime package 和
staging app install 明确标记 N/A。

运行时 handoff 必须包含准确 PR URL、base/head/merge-preview SHA 与 tree、package SHA-256、
staging receipt 和受影响业务 readback。纯治理或文档工作必须标记
`change_class=governance_only`，明确 runtime package、staging app install 和业务运行时
readback 为 N/A，并提供治理检查证据。

先持久化 `handoff_ready`、`new_commit`、`blocked` 或 `needs_review` 事件，再发送通知。
返工必须由原开发任务产生新 commit、新 candidate、新证据和新事件；旧候选不可覆盖。
发布指挥台只读判断并负责串行队列、合并、部署、观察和打回，不能修改候选源码或 PR。
生产只晋级预发布验收的同一包；`outcome_unknown` 只允许原身份只读对账。用户延期真实
业务验收时，生产最多保持 `observing`，不得标记 `released`。

如果预发布外部效果采用虚拟 Adapter，`staging_self_accepted` 仅表示虚拟合同和确定性
业务读回通过，必须保留 `effect_mode=virtual`；不得表述为 live Provider 或真实外部效果通过。

The final handoff or PR should contain a compact section like:

```text
OneID decision: involved/not involved, with reason and Port used
Persistence decision: classification and transaction boundary
External Effects decision: involved/not involved, kind/idempotency/reconciliation when applicable
No-duplication evidence: no new identity matcher, Provider writer, queue, Worker, retry, or reconciliation kernel
```

These are review prompts, not universal feature gates. Apply only the relevant tests and contracts, but never omit the initial classification.

## Stop Conditions

Stop and report instead of improvising when the design would cause identity misattribution, implicit customer creation, cross-domain table writes, independent commits that can split one required transaction, duplicate Provider effects, blind retry of `outcome_unknown`, or a second execution/identity kernel.
