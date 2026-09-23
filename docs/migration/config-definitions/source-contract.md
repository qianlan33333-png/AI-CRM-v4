# Configuration-definition migration source contract

This is the frozen contract for the one-time configuration-definition import.
It describes the shape and cardinality of the source snapshot; it is not a
production-import receipt.

## Architecture classification

* OneID: not involved. These rows do not resolve, create, merge, or update a
  customer root or an external identity.
* External Effects: not involved. The import submits no provider request,
  message, execution, webhook, payment, or other external effect.
* Transaction: local PostgreSQL migration transaction. The import ledger and
  definition writes are committed or rolled back together.

## Frozen record counts

| Definition | Source shape | Expected count | Target owner |
| --- | --- | ---: | --- |
| Products | 29 ordinary + 2 service-period definitions; duration retained | 31 | `products`, `product_imported_service_period_definitions` |
| Coupon definitions | Rule definitions only | 15 | `coupon_rules` |
| Coupon bindings | Product-to-coupon bindings only | 15 | `coupon_rule_targets` |
| Group plans | Local plan definitions | 12 | `group_ops_plans` |
| Group references | Opaque group references | 14 | `group_ops_plan_group_assets` |
| Group text nodes | Text node configuration | 3 | `group_ops_plan_nodes` |
| Agent runtime configurations | Local runtime configuration | 10 | `automation_agents` |

The count contract is intentionally explicit: `31/2/15/15/12/3/14/10`,
where the second value is the service-period partition of the first.

## Source allowlist

The read-only source extractor may read only definition columns from these
source tables (the deployment may expose either the canonical v2 table name or
the production-compatible alias):

* `wechat_pay_products` (ordinary product definitions)
* `service_period_products` (the two service-period definitions)
* `commerce_coupons`
* `commerce_coupon_product_bindings`
* `automation_group_ops_plans`
* `automation_group_ops_plan_nodes` (three text nodes)
* `automation_group_ops_plan_groups` (fourteen opaque group references)
* `automation_agent_runtime_config`

The target may normalize aliases and discard source primary keys. Every source
record still receives a deterministic canonical-record digest and source-map
key, so a second run of the same snapshot is a no-op and a different snapshot
under the same batch key is drift and must be rejected.

Coupon `claim_starts_at` and `claim_ends_at` are allowed rule-definition
timestamps. They describe when a coupon rule can be claimed; they are not claim
rows. `commerce_coupon_claims`, `commerce_coupon_redemptions`, and their
historical aliases are excluded.

Group text nodes carry text configuration only. `message_text`/`text_content`
does not authorize importing message records, message history, message IDs, or
delivery state. Group references are opaque local configuration references, not
Media or material references.

Only service-period `duration_days` is retained from the legacy membership
configuration; link slugs and membership configuration identifiers/names are
not extracted. The source plan code/type/description and node day/time/action
labels are retained as inert GroupOps-owned JSON metadata so they are not
silently lost, but they cannot trigger execution while every imported plan is
paused. Coupon and GroupOps source creator/updater labels are stored only in
the non-executing import source-map record; all target ownership fields use the
explicit existing v3 administrator selected for the batch.

## Explicit exclusions

The snapshot must not contain or query:

* tags, tag groups, or label assignments;
* Media, material, image, attachment, mini-program, or fixed-content asset
  identifiers;
* customer roots, OneID identities, openid/unionid/external-user identifiers,
  phone/email identity data, or audience/recipient records;
* history, message records, message IDs, execution runs, effect IDs, provider
  receipts, outboxes, or reconciliation evidence;
* webhook URLs, webhook secrets/tokens, credentials, passwords, or API keys;
* orders, payments, entitlements, claims, or redemptions.

An empty target-owned projection field is not source data and must remain empty.
No source value may be silently moved into an excluded field.

## Frontend boundary

The donor web tree is byte-frozen for this migration. No donor HTML, TypeScript,
JavaScript, CSS, icon, image, template, or copy file may be changed. The
boundary check in
`scripts/check-config-definition-import-boundary.sh` fails when a `web/donors`
path appears in the tracked or untracked diff.
