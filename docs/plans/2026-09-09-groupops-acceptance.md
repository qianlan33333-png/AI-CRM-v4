# Reopened production acceptance

Baseline: a96fab6f5ca2f0ef42807f49fc1fb785d6540157.

OneID: directory search and plan configuration do not identify customers; no new identity dependency. Survey OAuth resolves verified identities through the existing Identity Port; preserve application and open-platform scopes.
Persistence: staff/group refresh are Provider reads followed by existing owner transactions. Webhook configuration uses the existing audited, versioned plan transaction. No new Provider writes, dispatch activation, queues or identity matching.

Repair and verify: member keyword filtering, member refresh failure, group refresh 503, group name projection, usable signed Webhook address, and survey WeChat authorization. Installed release and unit tests alone do not close production acceptance.
