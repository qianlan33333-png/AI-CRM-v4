# Transaction Management and OneID Migration Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Deliver the complete v2-proven transaction-management capability in AI-CRM-v3, adapted to current OneID and External Effects rules, and migrate all in-scope historical user and commerce rows from production with row-level reconciliation.

**Architecture:** Keep the frozen v2 UI behavior, rebuild Order and Payment as separate v3 domains, coordinate identity only through `internal/identity/port`, and route payment/refund Provider effects through a versioned External Effects payment contract. Historical data moves through one-off encrypted snapshots and a replayable migration CLI; the runtime never reads the old database.

**Tech Stack:** Go, PostgreSQL 16, pgx, River via `internal/platform/jobqueue`, OpenAPI, TypeScript donor frontend, SHA-256 manifests, table-driven/race/PostgreSQL integration tests.

---

## Progress checkpoint（2026-09-03）

- Task 1 complete: `27d55a9` freezes the donor contract and SHA evidence.
- Task 2 complete: `fb468f1` makes transaction capability readiness truthful and keeps the route explicitly unavailable.
- Task 3 complete: `0152001` adds the canonical Order aggregate, ports, PostgreSQL schema/store, replay, cursor, settlement and historical-effect guards.
- Production discovery/import, Provider execution and deployment have not started.

---

## Execution rules

- Start from a clean worktree based on current `origin/main`; do not implement on `codex/fix-wecom-tag-sync-csrf-v2`.
- Freeze donor `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e` in a transaction-specific manifest.
- Run production discovery read-only. Never run an apply/cutover command merely because this plan exists.
- Keep all Provider switches false until the explicit gray-release task.
- Use TDD and one commit per task below; each PR boundary is listed in the PRD.

### Task 1: Freeze the transaction behavior contract

**Files:**

- Create: `docs/migration/transaction/pr-tx01-contract-audit.md`
- Create: `docs/migration/transaction/pr-tx01-donor-manifest.yaml`
- Create: `docs/migration/transaction/pr-tx01-donor-sha256.txt`
- Create: `scripts/check-pr-tx01-donor-manifest.sh`
- Modify: `docs/source-baselines.yaml`
- Modify: `modules/registry.yaml`

**Steps:**

1. Record v2 routes, UI actions, RBAC, failure semantics and the exact Alipay read-only limitation.
2. Hash only the selected templates, TypeScript adapter code, protocol vectors and characterization tests.
3. Add a failing script test for donor SHA drift and forbidden v2 runtime imports.
4. Implement the manifest checker and run `bash scripts/check-pr-tx01-donor-manifest.sh`; expect PASS.
5. Mark `order/payment` as `contracted` only after the contract and ADR are approved.
6. Commit: `docs(transaction): freeze v2 transaction behavior contract`.

### Task 2: Correct frontend capability truthfulness

**Files:**

- Modify: `web/src/api/capabilities.ts`
- Modify: `web/src/api/admin.test.ts`
- Modify: `cmd/aicrm/adapters_test.go`

**Steps:**

1. Write a failing test proving order reads/refunds are `backend_blocked` while readiness is false.
2. Add a typed `transactionReadiness` projection; never infer readiness from page presence.
3. Make navigation keep the donor screen but show an explicit unavailable state before TX02/TX05.
4. Run `npm test --prefix web` and the focused Go adapter test; expect PASS.
5. Commit: `fix(web): report real transaction readiness`.

### Task 3: Build the Order domain and PostgreSQL schema

**Files:**

- Create: `migrations/0020_order.sql`
- Replace: `internal/order/domain/order.go`
- Create: `internal/order/domain/order_test.go`
- Create: `internal/order/port/port.go`
- Create: `internal/order/app/service.go`
- Create: `internal/order/app/service_test.go`
- Create: `internal/order/store/postgres.go`
- Create: `internal/order/store/postgres_integration_test.go`
- Create: `internal/order/README.md`

**Steps:**

1. Write domain tests for money, provider, lifecycle, immutable item snapshots, refund balance and payer/beneficiary separation.
2. Run `go test ./internal/order/domain`; expect failures for missing aggregate behavior.
3. Define `orders`, `order_items`, `order_status_history`, `order_export_receipts`, `order_import_*` with Owner comments, unique source keys, versions and `record_origin/effect_eligible` constraints.
4. Publish narrow Order ports: command service, query, settlement writer and historical importer. Do not expose Store types.
5. Implement transaction-bound Store writes and stable `(created_at,id)` cursor queries.
6. Add PostgreSQL tests for same-key replay, payload drift, amount constraints, historical effect rejection and rollback.
7. Run `go test -race ./internal/order/...`; expect PASS.
8. Commit: `feat(order): add canonical order aggregate and store`.

### Task 4: Add OneID-aware order attribution

**Files:**

- Create: `internal/identity/port/commerce.go`
- Create: `internal/order/app/attribution.go`
- Create: `internal/order/app/attribution_test.go`
- Modify: `internal/identity/query/postgres.go`
- Modify: `internal/identity/query/postgres_integration_test.go`

**Steps:**

1. Write tests for exact scoped resolution, payer different from beneficiary, no-scope OpenID/UnionID rejection, declared phone non-provision and multi-root conflict.
2. Add a read-only batch resolver returning internal identity/customer IDs and a status; no raw IDs leave Identity.
3. Implement Order attribution through that Port only; do not query `customer_identities` from Order Store.
4. Allow unresolved history to remain floating with a quarantine receipt; require verified identity for native checkout.
5. Run `go test -race ./internal/identity/... ./internal/order/...`; expect PASS.
6. Commit: `feat(order): bind transactions through scoped OneID`.

### Task 5: Implement order admin APIs and safe export

**Files:**

- Create: `internal/order/http/handler.go`
- Create: `internal/order/http/handler_test.go`
- Modify: `api/openapi.yaml`
- Modify: `cmd/aicrm/composition.go`
- Modify: `cmd/aicrm/composition_test.go`

**Steps:**

1. Add failing HTTP tests for list/detail/items/refunds, filters, cursor tamper, ambiguous `order_ref`, RBAC, CSRF and no-store.
2. Add OpenAPI contracts for the PRD route list and generate clients using the repository generator.
3. Implement list/detail composition without cross-domain Store imports.
4. Implement preview/export with 10,000-row and 5-MiB limits, formula escaping and receipt-backed idempotency.
5. Run the OpenAPI validator, focused Go tests and `go test -race ./internal/order/... ./cmd/aicrm`.
6. Commit: `feat(order): expose transaction board and safe exports`.

### Task 6: Connect the frozen transaction UI

**Files:**

- Preserve: `web/src/admin/templates/orders.html`
- Preserve: `web/src/admin/templates/orderDetail.html`
- Modify: `web/src/api/admin.ts`
- Modify: `web/src/api/admin.test.ts`
- Modify: `web/scripts/e2e.mjs`
- Modify: `web/src/api/capabilities.ts`

**Steps:**

1. Add failing browser tests against a real v3-compatible API fixture for server-side filters, pagination, detail timeline, historical badge, error states and export.
2. Keep the donor visual hierarchy and interaction; change only the adapter/readiness and text that would make a false capability claim.
3. Ensure OneID search sends a safe server filter and never embeds a raw external ID in the route or page source.
4. Enable read capability only when TX02 migrations/API readiness pass.
5. Run `npm run typecheck --prefix web`, `npm test --prefix web`, `npm run build --prefix web` and the donor hash checker.
6. Commit: `feat(web): connect transaction management to v3 orders`.

### Task 7: Build the Payment domain and versioned External Effects contract

**Files:**

- Create: `migrations/0021_payment.sql`
- Replace: `internal/payment/domain/status.go`
- Create: `internal/payment/domain/payment.go`
- Create: `internal/payment/domain/payment_test.go`
- Create: `internal/payment/port/port.go`
- Create: `internal/payment/app/service.go`
- Create: `internal/payment/app/service_test.go`
- Create: `internal/payment/store/postgres.go`
- Create: `internal/payment/store/postgres_integration_test.go`
- Create: `internal/externaleffects/port/payment_v1.go`
- Modify: `internal/externaleffects/runtime.go`
- Modify: `internal/externaleffects/runtime_test.go`

**Steps:**

1. Write failing state-machine tests for prepay, callback settlement, partial/full refund, replay and `outcome_unknown`.
2. Define Payment-owned tables and the unique payment/refund-to-effect bindings.
3. Define `payment-v1` effect kinds and four immutable digests without using `OwnerOutbound`.
4. Add a transaction-bound accept method that reuses the current External Effects repository and River queue.
5. Prove one UoW contains Order reservation, Payment command, receipt, audit, Outbox and effect acceptance.
6. Prove Provider execution runs after commit and without a live DB transaction.
7. Run `go test -race ./internal/payment/... ./internal/externaleffects/... ./internal/order/...`.
8. Commit: `feat(payment): add atomic payment effects boundary`.

### Task 8: Implement the trusted payment identity session

**Files:**

- Create: `internal/payment/session/service.go`
- Create: `internal/payment/session/service_test.go`
- Create: `internal/payment/http/session.go`
- Modify: `internal/identity/port/commerce.go`
- Modify: `api/openapi.yaml`

**Steps:**

1. Write failing tests showing browser-supplied `customer_id`, openid, unionid and assurance are rejected.
2. Accept only an opaque, expiring, HttpOnly/Secure/SameSite payment-session token issued after a trusted OAuth Adapter supplies a verified scoped identity.
3. Resolve/provision through Identity Port and persist only token/identity digests in Payment.
4. Support an explicitly authorized admin-assisted beneficiary selection without changing payer identity.
5. Test expiry, replay, wrong App scope, different payer/beneficiary and session fixation.
6. Commit: `feat(payment): resolve checkout actors through OneID`.

### Task 9: Implement WeChat Pay checkout, callbacks, refund and reconciliation

**Files:**

- Create: `internal/payment/provider/wechatpay.go`
- Create: `internal/payment/provider/wechatpay_test.go`
- Create: `internal/payment/http/wechatpay.go`
- Create: `internal/payment/http/wechatpay_test.go`
- Create: `internal/payment/worker/wechatpay.go`
- Modify: `cmd/aicrm/composition.go`
- Modify: `deploy/aicrm.env.example`
- Modify: `api/openapi.yaml`

**Steps:**

1. Port official signature/decryption characterization vectors from the frozen donor.
2. Add disabled-provider tests proving zero HTTP requests.
3. Implement prepay/refund Provider Adapter with redacted credentials and bounded timeouts.
4. Implement verified payment/refund callbacks with receipt-last atomic settlement.
5. Register River workers for prepay/refund execution and original-key active-query reconciliation.
6. Test timeout-before-send, timeout-after-send, duplicate callback, amount drift, expired handoff and recovery after crash.
7. Run focused race, PostgreSQL and HTTP tests; keep the production flag false.
8. Commit: `feat(payment): close WeChat Pay settlement lifecycle`.

### Task 10: Implement WeChat Shop refund lifecycle

**Files:**

- Create: `internal/payment/provider/wechatshop.go`
- Create: `internal/payment/provider/wechatshop_test.go`
- Create: `internal/payment/http/wechatshop.go`
- Create: `internal/payment/worker/wechatshop.go`
- Modify: `api/openapi.yaml`
- Modify: `cmd/aicrm/composition.go`
- Modify: `deploy/aicrm.env.example`

**Steps:**

1. Write failing tests for material read, line-level refundable count/amount, encrypted callback and query reconciliation.
2. Implement order-material Provider read outside transactions and persist a Payment-owned safe snapshot.
3. Create refund + job + receipt atomically; execute Provider call outside the transaction.
4. Treat callback as a reconciliation trigger, not automatic refund proof; query the exact after-sale ID.
5. Test missing material, ambiguous order reference, partial refunds, duplicate callback, unknown outcome and disabled provider.
6. Commit: `feat(payment): close WeChat Shop refund lifecycle`.

### Task 11: Build the historical migration CLI

**Files:**

- Create: `cmd/migrate-commerce-history/main.go`
- Create: `cmd/migrate-commerce-history/main_test.go`
- Create: `cmd/migrate-commerce-history/main_integration_test.go`
- Create: `internal/order/migration/manifest.go`
- Create: `internal/order/migration/reader.go`
- Create: `internal/order/migration/runner.go`
- Create: `internal/order/migration/reconcile.go`
- Create: `docs/runbooks/交易与历史用户数据生产迁移.md`

**Steps:**

1. Freeze snapshot schemas for users, identities, orders, items, payments, refunds and events after production discovery.
2. Implement `inspect`, `dry-run`, `apply`, `delta`, `reconcile`, `rollback-plan` and guarded `rollback` modes.
3. Require manifest digest, source schema digest, run key and explicit confirmation flags for mutations.
4. Route identity writes through Identity Port, order writes through Order Port and payment terminal history through Payment Port.
5. Ensure the CLI cannot construct a Provider Adapter, enqueue an effect or set `effect_eligible=true` for history.
6. Add fixtures for all three Providers, duplicate replay, source drift, missing scope, multi-root conflict, missing parent and interrupted resume.
7. Run focused tests against two PostgreSQL 16 databases and assert every reconciliation identity.
8. Commit: `feat(migration): add replayable commerce history import`.

### Task 12: Perform production read-only discovery

**Files:**

- Create locally, do not commit PII: `/secure/aicrm-migration/production-schema-report.json`
- Create: `docs/migration/transaction/production-discovery-summary.md` using aggregates only
- Update: `docs/runbooks/交易与历史用户数据生产迁移.md`

**Steps:**

1. Obtain the verified host key, restricted username and approved read-only commands for `150.158.82.186`.
2. Confirm the source backup and record the backup ID without exposing credentials.
3. Run schema/constraint/aggregate discovery only; do not export row data yet.
4. Freeze the real table/column map and source-key/watermark strategy.
5. Red-team the identity provenance classification with the data Owner.
6. Commit only aggregate summary and schema digests: `docs(migration): freeze production commerce source map`.

### Task 13: Rehearse full and delta migration in staging

**Files:**

- Create: `scripts/check-transaction-migration-reconcile.sh`
- Create: `journeys/transaction-history-migration.md`
- Update: `docs/migration/transaction/production-discovery-summary.md`

**Steps:**

1. Produce an encrypted, access-controlled snapshot and record hashes.
2. Run inspect and dry-run; require all bucket equations to balance.
3. Restore a clean staging target and run apply twice; the second run must add zero rows.
4. Simulate interruption at each table boundary and resume.
5. Apply a delta snapshot and re-run count/amount/refund invariants.
6. Execute source/snapshot/API/UI samples using approved redacted identifiers.
7. Complete rollback rehearsal and document which referenced rows correctly refuse rollback.
8. Commit non-PII evidence: `test(migration): prove commerce full delta and replay`.

### Task 14: Deploy disabled and cut over historical reads

**Files:**

- Update: `modules/registry.yaml`
- Update: `docs/runbooks/交易与历史用户数据生产迁移.md`
- Update: `docs/migration/transaction/production-discovery-summary.md`

**Steps:**

1. Deploy migrations/API/UI with all Payment Provider flags false.
2. Verify `/readyz`, RBAC, CSRF, no-store and zero Provider calls.
3. Run production full import, delta and reconcile with explicit apply authorization.
4. Keep old source writes unchanged while shadow comparing only reads.
5. When counts, amounts, refunds and samples all match, switch transaction read routing to v3.
6. Observe read errors, latency, quarantine and mismatches; rollback routing if any hard invariant fails.
7. Mark `order=shadow/cutover` only with live evidence.

### Task 15: Gray-release Provider writes and finish cutover

**Files:**

- Update: `modules/registry.yaml`
- Update: `docs/runbooks/交易与历史用户数据生产迁移.md`
- Update: `docs/migration/transaction/production-discovery-summary.md`

**Steps:**

1. Approve and execute the exact old-system transaction write freeze.
2. Take the final T1 delta and require every count/amount invariant to balance.
3. Enable one Provider at a time for a bounded cohort.
4. Verify real Provider receipt, callback/reconciliation and database final state for payment and refund.
5. Monitor one complete settlement cycle; do not equate HTTP acceptance with outcome.
6. Set `order/payment=cutover` only when v3 is the sole writer and all effects reconcile.
7. Keep old transaction paths read-only for the approved rollback window, then retire them separately.

## Final verification checklist

- `make check`
- `go test -race ./internal/order/... ./internal/payment/... ./internal/identity/... ./internal/externaleffects/... ./cmd/aicrm ./cmd/migrate-commerce-history`
- OpenAPI generation/validation and web typecheck/test/build
- PostgreSQL 16 migration up/down safety tests; populated financial tables must refuse destructive down migration
- Provider disabled zero-call tests and real gray evidence where authorized
- Production row-count, amount, refund and identity-bucket reconciliations all equal 100%
- No second identity matcher, queue, Worker framework, retry kernel or reconciliation state machine
- No production write, deployment or Provider execution may be claimed unless the runbook evidence records it
