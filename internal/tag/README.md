# Tag catalog domain

This package is the constrained PR03 transfer of the WeCom tag-management
capability. It owns local tag groups, catalog rows, stable ordering, archive
commands, the fail-closed execution gate, and sync acceptance facts. It
deliberately does not own customer tag assignments, customer unmark
operations or provider credentials. The optional read-only catalog connector is
owned by outbound/WeCom and cannot mark or unmark a customer.

`app.Service` records catalog mutations, idempotency receipts, audit facts and
outbox events in its PostgreSQL Unit of Work. `app.SyncService` records a
manual or due refresh and atomically accepts the immutable intent through the
shared External Effects/River `TransactionalAccepter`, via the sole
`outbound` WeCom-write adapter. It stores the real effect, queue and operation
receipt identifiers; queued is not a WeCom execution receipt. The shared
worker owns `accepted -> queued -> attempted -> executed/outcome_unknown ->
reconciled` and reconciliation. A validated completed catalog read appends the
opaque snapshot receipt in the same completion transaction as the EER CAS;
unknown/final-failed states never append one. The narrow catalog read adapter
is default-disabled (including id-dev), requires an explicit
`catalog-read-authorized` permission to enable, and an unknown outcome is never
retried under a new idempotency key. Generic reconciliation may close an
unknown effect but never fabricates an observed snapshot.

The frozen `tag-execution-status` page gate remains deliberately
`provider_execution_unavailable`: it is a local permission/compatibility gate
for customer tag writes, which are outside PR03, not a diagnostic of the
implemented catalog-read runtime switch.

Reorder commands require the complete current ID set but may change its order;
partial, duplicate, unknown, or stale memberships fail closed before a store
mutation. `SameIDs` remains the ordered comparison used to verify replayed
receipts.

The store and reference guard are interfaces only. The store must access tag
owned tables; any customer-reference check must be supplied through the
read-only `ReferenceGuard` port. No v2 package is imported and no v2 database
table is a runtime dependency.

The page entry, route/method/DTO/error contract, provider receipt lifecycle,
exclusions, source/UI inventory, and exact frozen hashes are recorded in
`docs/donor-manifests/pr03-wecom-tags.yaml` and
`docs/donor-manifests/pr03-wecom-tags.sha256`. The completed v3 module mounts
the frozen donor tags workspace inside the PR10 one-sidebar shell; all
authentication, CSRF and DTO compatibility remains in the v3 HTTP adapter.
