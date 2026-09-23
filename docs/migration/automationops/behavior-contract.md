# Automation Operations donor behavior contract

## Decision classification

```text
OneID: involved — canonical customer reads and verified scoped identity resolution; no provisioning, binding, or merge
Persistence: local transaction + internal durable job + Provider read + Provider write/external effect
External Effects: involved — outbound_message, stable four digests and recipient receipt key; unknown outcomes are never blindly retried
```

## Active donor behavior

The active v2 path is `build.mjs -> main.ts -> legacy.ts -> AdminController`.
`automation.html` lists groups and packages and exposes create-group, edit,
copy, activate, pause, archive, delete, and navigate-to-detail behavior.
`audienceEdit.html` exposes package basics/template configuration, Agent
binding, sender ordering, immutable configuration preview/materialization, and
member display.

The donor group-send control is present but its active handler is exactly a
blocked boundary: `AI 人群包 API 不等于群发任务创建契约`. The detail page also
states that local audience APIs do not create send jobs or prove Provider
effects. A successful HTTP response, refresh, queue acceptance, or local
active flag must therefore never be projected as delivery success.

## Compatibility contract

- Preserve the two templates, visible hierarchy, wording, actions, and request
  field meanings. Mount them through the one v3 webshell.
- Replace Mock/sessionStorage/hard-coded rows only in a v3-owned adapter.
- Use `customers.id` for published membership. Raw external identities enter
  only a trusted adapter and resolve through Identity; no match is not a
  customer-creation signal.
- Persist only internal Staff references for senders and published
  Agent/version/digest references for content bindings.
- Freeze configuration, snapshot, content, sender policy, target set, and
  action before a run. A repeated logical operation returns its original
  receipt; changed payload under the same key conflicts.
- All WeCom business writes are Outbound-owned and accepted through External
  Effects in the caller UoW. Provider calls occur after attempt persistence and
  outside database transactions.
- `outcome_unknown` stops automatic retry and requires evidence-based
  reconciliation under the original effect identity.

## Explicit non-contracts

The presence of generated clients, historical packages, inactive sections, or
server routes in v2 is not proof that a user journey is active. This migration
does not import a generic workflow platform, a second customer identity
system, GroupOps ownership, or donor runtime dependencies.
