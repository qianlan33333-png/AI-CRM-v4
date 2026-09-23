# WeCom Tag Catalog Sync Closure

## Scope and architecture classification

- OneID/external identity: not involved. The snapshot contains only provider tag and group identifiers and must not resolve, create, or merge customers.
- Persistence/external effects: involved. The existing Tag -> Outbound -> External Effects/River path remains the only durable execution path. This change adds a Tag-owned single-flight receipt, status projection, and catalog projection in the same PostgreSQL transaction as External Effects completion. It adds no queue, worker, timer, or provider caller.
- Frozen donor boundary: `web/src/admin/templates/tags.html`, `web/src/admin/controller.ts`, and the donor API/runtime files remain byte-identical. A v3 WebShell bridge provides status polling and button state.

## Observed failure

Production accepted every click as a distinct receipt/effect. External Effects executed successfully and stored an opaque snapshot observation (36 groups, 125 tags), but the Tag completion sink never projected that snapshot into `tag_groups`/`tag_catalog_tags`, and receipts stayed `queued`. The frozen page therefore had neither a terminal state to poll nor updated catalog rows to render.

## Chosen design

1. Add Tag-owned provider-ID bindings and extend sync receipts with terminal states, counters, and completion time.
2. Enforce one active sync globally with a partial unique index. Idempotency replay remains supported; a different key while active returns a stable `sync_in_progress` conflict.
3. On an executed catalog effect, validate and canonicalize the artifact, append the immutable observation, reconcile provider-bound rows while preserving unbound local rows, write safe audit/outbox metadata, and complete the sync receipt atomically with the External Effects state transition.
4. Project non-success completion states into the Tag receipt. `outcome_unknown` and `retryable_failed` remain active and block new manual syncs; terminal failure/cancel/reconcile releases the single-flight lock.
5. Add an authenticated read-only sync-status endpoint. The v3 WebShell bridge polls it across rerenders/reloads, disables the donor button while active, refreshes after successful projection, and reports terminal failure without claiming Provider success.

## Projection rules

- Provider group/tag IDs are stored only in Tag-owned binding tables; local numeric IDs remain the catalog identity.
- Provider order is authoritative for provider-bound rows. Existing unbound local rows are preserved and appended in their prior relative order.
- Provider-bound rows absent from a later snapshot are archived only when Tag-owned references are zero; otherwise reconciliation fails closed and the transaction rolls back.
- Snapshot limits match the local catalog contract: names at most 200 characters and at most 1000 total tags.
- Audit/outbox payloads contain only receipt/effect identifiers, digest, counts, and state; no raw provider IDs or names.

## Implementation sequence

1. Add failing unit/integration/HTTP/WebShell tests for completion projection, single-flight, status JSON, and frozen-donor-safe button behavior.
2. Add migration `0019_tag_catalog_sync_projection.sql`, repository projection/status methods, and sync conflict classification.
3. Expand the outbound completion sink and ensure External Effects passes its computed completion state.
4. Add the status route and v3 WebShell bridge asset.
5. Run focused Go/Node tests, race tests for changed Go packages, architecture/donor gates, then the repository verification suite.
6. Merge, deploy, and verify one production click produces one effect, reaches `executed`, populates 36 groups/125 tags, and leaves no active receipt.

## Rollback and safety

- Code rollback is release-pointer based. Migration changes are additive and retain all prior receipts/observations.
- Existing successful observations are backfilled to terminal receipts before the active unique index is created, preventing historical `queued` rows from blocking the first corrected run.
- No production row is deleted. Archiving is reversible and is limited to provider-bound, unreferenced catalog rows.
