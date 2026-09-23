---
name: aicrm-v3-development
description: Plan, implement, migrate, or review AI-CRM-v3 capabilities with an explicit first-pass decision on OneID identity coordination, PostgreSQL persistence, durable internal jobs, and External Effects reuse. Use for development work inside the 新CRM/AI-CRM-v3 repository; do not force these dependencies onto genuinely unrelated local-only work.
---

# AI-CRM-v3 Development Decision Manual

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
