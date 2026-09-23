# Operation-cycle domain

This package is the constrained PR08 transfer of the operation-cycle
capability. It keeps the local contracts for strategy definitions and
versions, run snapshots, runner compatibility, action stages, enable/pause/
archive state, proposals, and transaction-bound local facts.

The domain intentionally carries no customer, external-user, segment,
campaign, recipient, tenant, order, product, membership, subscription,
entitlement, service-period, credential, or Provider identity. `app.Service`
records local lifecycle facts through the operation-cycle event and delivery
ports; it does not call a Provider, enqueue a campaign recipient, or retry an
`outcome_unknown` effect. `domain` validation fails closed when excluded
identifiers or external-effect payloads appear in a snapshot, result,
proposal, or context filter.

The Store, HTTP adapter, runner wiring, PostgreSQL migrations, and generated
queries are implemented in this module. Strategy versions and run revisions are
immutable owner-table history; the current tables are latest projections only.
The donor's numeric detail id resolves through an immutable owner-table mapping
to one run key and is never reconstructed from mutable list ordering.
Historical observation/static-history readers remain outside PR08. Any future
outbound integration must preserve the local `accepted` fact boundary and use
the versioned outbound contract.

The frozen donor inventory and exact UI hashes are recorded in
`docs/donor-manifests/pr08-operation-cycles.yaml` and
`docs/donor-manifests/pr08-operation-cycles.sha256`; raw templates are under
`web/donors/operation-cycles-v2`. The only browser seam is
`web/v3/operationCyclesAdapter.ts`: it installs typed v3 read bindings plus the
minimal authorized host write binding for the donor's existing primary button,
then dynamically starts the byte-frozen donor `main.ts -> legacy.ts ->
AdminController` chain inside PR10's one admin shell. It has no replacement
renderer, styles, navigation, mock fallback, or donor-file changes.

Report snapshots are decoded through a typed allowlist before persistence. The
original JSON object is never retained: the rebuilt projection is the only
value that can reach operationcycle tables, audit facts, or outbox events.
Secrets, credentials, tokens, cookies, private keys, external/customer
identifiers, phone numbers, and email addresses are rejected.
