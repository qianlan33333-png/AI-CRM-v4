# Group Ops frontend donor snapshot

This directory is a byte-exact snapshot of the frozen v2 Group Ops pages,
templates, generated request contracts, Group Ops history, and the broadcast
job-history observation page plus the shared files required by the unchanged
donor modules. It is archive-only and is not included in the v3 web build.

The shared files intentionally retain their original customer, audience,
Campaign, and other legacy branches because changing or deleting donor bytes
would invalidate the snapshot. Terra/Web must extract the Group Ops mount
points and keep excluded branches out of the v3 integration.

The 14 preserved shared files are the PR01 frozen runtime bundle. PR06 reuses
that bundle byte-for-byte; it must not crop, rewrite, or create a second
runtime copy while mounting the Group Ops pages.

The only production shell is the v3-owned PR10 `internal/webshell` and
`admin_base`; do not deploy this snapshot as a second v2 shell or introduce a
second `.side` sidebar. Reconciliation is recorded in
`docs/migration/groupops/pr06-donor-sha256.txt`.
