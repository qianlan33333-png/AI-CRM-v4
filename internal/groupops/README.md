# Group Ops migration closure (PR06)

This package is the bounded Group Ops donor slice. It keeps local plan
definitions, staff membership references, opaque group bindings, ordered
message/delay nodes, webhook descriptor metadata, durable schedule/run/receipt
and reconciliation contracts, and transaction-bound draft lifecycle behavior.

The package deliberately carries no customer, OneID, segment, audience,
Campaign, recipient, or customer-marking operation. `DispatchProvider` is a
contract only; no implementation here calls WeCom or another Provider. A
Provider write, if later approved, must be accepted and reconciled through the
versioned `outbound` boundary with `accepted`, `attempted`,
`outcome_unknown`, and delivery-evidence states kept distinct.

Content-package creation, validation, versioning, and Media-owned material
freezing remain owned by the PR02 Media slice. The Group Ops HTTP boundary has
API-only create/update/version/preview/bind/readback routes that call the
Media `ContentDeliveryService` stable port; it does not add a donor content
package page, read Media tables directly, or duplicate Media's owner files.
Runtime acceptance captures sources and freezes immutable provider-shaped
snapshots through the Media `GroupOpsMaterialSourceCapturer` and
`GroupOpsMaterialSnapshotFreezer` ports. Group invites freeze directly from
Media's captured title/url/description; image, attachment, and miniprogram
references require a Media-owned persisted preparation receipt with an
unexpired lease. The approved preparation writer is separate from Group Ops
and can only record a receipt after Provider preparation; the disabled
Provider never calls it. A missing/expired receipt therefore fails closed for
those media kinds without inventing a Provider ID or digest.

The Store, PostgreSQL schema (`migrations/0012_group_ops.sql`), HTTP/auth,
composition wiring, durable runtime, disabled Provider adapter, and
completion/reconcile projection are v3-owned here. The exact donor frontend
files are staged under `web/donors/groupops-v2/` and mirrored into the active
donor build path byte-for-byte; they are mounted through the PR10 v3-owned
webshell and `admin_base`. This archive is not a second deployable v2 shell
and must not introduce a second `.side` navigation.

Source and target SHA-256 reconciliation is recorded in
`docs/migration/groupops/pr06-donor-sha256.txt`; the historical donor inventory
and preparation evidence are recorded in
`docs/migration/groupops/pr06-donor-manifest.yaml`.
