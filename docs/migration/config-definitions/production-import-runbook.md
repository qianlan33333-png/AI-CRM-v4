# Production runbook: configuration-definition import

This runbook is for the one-time, configuration-only migration. It does not
authorize a production run by itself and it does not replace the change
approval, database backup, or operator evidence required by the deployment.

## Preconditions

1. Confirm that the source snapshot is read-only and has a recorded source
   revision. Do not point the importer at a writable source database.
2. Confirm the expected manifest in
   `docs/migration/config-definitions/expected-manifest.json`. The frozen
   cardinalities are 31 products (29 ordinary and 2 service-period), 15
   coupon definitions, 15 coupon bindings, 12 group plans, 14 group
   references, 3 group text nodes, and 10 agent runtime configurations.
3. Run the boundary check from the repository root:

   ```sh
   scripts/check-config-definition-import-boundary.sh
   ```

   It must pass before an operator prepares an encrypted snapshot. A failure
   for a donor web diff or a forbidden source field is a stop condition.
4. Verify that the target database is the intended single-tenant PostgreSQL
   database and that the migration ledger migration has been applied. Record
   the target release SHA and the manifest digest in the change record.
5. Provide an explicit administrator actor for audit attribution. Never put
   credentials, connection strings, keys, or snapshot contents into logs,
   tickets, or chat.

## Source snapshot and preflight

The extractor must use one repeatable read-only transaction and only the
definition tables listed by the source contract. Seal the snapshot before
transfer; retain the key separately from the encrypted file. Inspect the
manifest and digest offline before any target write.

The only accepted source shapes are:

* `wechat_pay_products` plus `service_period_products`;
* `commerce_coupons` plus `commerce_coupon_product_bindings`;
* `automation_group_ops_plans`, `automation_group_ops_plan_nodes`, and
  `automation_group_ops_plan_groups`;
* `automation_agent_runtime_config`.

Coupon `claim_starts_at` and `claim_ends_at` are rule-definition fields and are
allowed. Claim rows, redemption rows, tags, Media/material rows, customer or
identity rows, history/message rows, execution rows, provider receipts, and
external-effect rows are not accepted source inputs.

Before `apply`, perform an offline dry run that verifies:

* the source snapshot digest matches the approved manifest;
* all eight expected counters are exact;
* source keys and record digests are unique and deterministic;
* excluded fields are absent, including webhook URL/secret/token fields;
* the import is local-only: OneID is not involved and External Effects are not
  involved.

## Apply and replay behavior

The coordinator starts one local PostgreSQL transaction. In that transaction it
creates or locks the import batch, validates the source digest, writes only the
approved definition target tables (including the Product-owned service-period
duration projection), records source maps, and advances the batch
status. No provider network call may occur while applying this batch.

Use the approved batch key and snapshot digest exactly as recorded. If the same
batch key and digest already have an applied/verified batch, the operation is
an idempotent no-op. If the batch key exists with a different snapshot digest,
stop: this is drift, not a new retry. Resolve it by approving a new batch key
after a fresh review.

If the transaction fails, verify that its status and all definition writes were
rolled back before retrying. Do not manually insert target rows or bypass the
ledger.

## Verification and evidence

After commit, verify in the target database:

1. The import batch has the approved digest and a terminal status.
2. The imported counters are exactly 31/15/15/12/14/3/10, with the product
   partition 29/2 and exactly two positive service-period duration definitions.
3. Source-map rows are unique and map only to the target definition owners.
   Source actor labels, when present, are provenance only and never become a
   target administrator identity.
4. No tag, material, customer, history, message, execution, effect, webhook
   URL/secret, claim, or redemption row was created by this import.
5. Provider/network effect counters remain unchanged.

Store the command output, manifest digest, source revision, target release SHA,
batch ID, terminal status, and verification timestamp as migration evidence.
Do not store raw snapshots, decrypted keys, PII, or secrets in the evidence
record.

## Stop conditions

Stop and escalate on any count mismatch, source digest mismatch, duplicate or
conflicting source key, forbidden field, donor web diff, transaction boundary
violation, non-terminal batch, or unexpected provider/network activity. A
successful database commit is not sufficient evidence if the verification
queries or the boundary check fail.
