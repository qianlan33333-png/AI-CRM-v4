# Media migration preparation (PR02)

This package is the bounded PR02 Media capability, derived from a frozen
legacy donor. It contains Media-owned validation, PostgreSQL persistence,
opaque reference protection, compatibility HTTP routes, and
provider-independent image/attachment/card operations.

The package is registered only through the v3 composition root and uses the
single PostgreSQL UoW for resources, receipts, audit events, and outbox rows.
It imports neither the v2 module/runtime/database nor other domains' internal
layers. Provider calls remain disabled; content packages are owned by PR06 and
are not exposed by the PR02 routes.

## PR06 Media-owned content package closure

OneID decision: **not involved**. A content package names only Media records;
it neither accepts nor resolves customer or channel identities.

Persistence decision: **local transaction**. `0016_media_content_packages.sql`
adds Media-owned package/current-version/version-reference/binding/receipt
tables. Package state, idempotency receipt, Media audit and Media outbox are
committed in the same UoW. Each version reference records the actual source
digest observed while the enabled Media record is locked.

Group Ops receives only the stable `GroupOpsMaterialSourceCapturer` port and a
transaction-bound preparation reader. It locks and captures enabled Media
facts in the caller UoW; the reader returns Media-owned provider preparation
receipts/leases for image, attachment, and miniprogram references. A captured
group invite is already a complete link attachment and needs no receipt. An
approved Provider preparation adapter can use the separate transaction-neutral
writer after its network call to persist the receipt, audit, and outbox row in
one Media UoW. The provider-disabled adapter has no writer and cannot fabricate
readiness. This package does not manufacture Provider media IDs, add a worker,
call a Provider, or write Group Ops tables.

The exact v2 admin templates and their existing TypeScript/CSS/shared API
dependencies are staged under `web/donors/media-v2/`. They remain byte-exact
and are mounted only inside the v3 `admin_base` shell. Their source and
SHA-256 reconciliation are recorded in
`docs/migration/media/pr02-donor-manifest.yaml` and
`docs/migration/media/pr02-donor-sha256.txt`.
