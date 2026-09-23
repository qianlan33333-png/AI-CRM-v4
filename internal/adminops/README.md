# PR09 Admin Ops slice

This package freezes the local Admin Ops control-plane contracts and leaf
behavior: credential references and safe metadata validation, configuration
category/release/job mutation semantics, safe historical projections, and
read-only execution-runtime normalization/redaction. Secret material is never
accepted as a value; only opaque references and masks may cross the port.

`port.Repository` remains the broader v3-local control-plane persistence seam;
its credential/category/job mutation surface is intentionally not mounted by
the PR09 Config UI. The bounded `ProjectionStore` is now implemented by the
AdminOps-owned PostgreSQL projection store and composed through
`ProjectionService`. It selects only release SHA/status/time and diagnostic
key/status/time; the storage `details` JSON is never selected or returned.
`DiagnosticsService` records only bounded local observations. There is no
worker execution, customer or identity repair, or Provider call.
