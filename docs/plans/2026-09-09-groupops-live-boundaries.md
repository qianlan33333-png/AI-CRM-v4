# Group Ops live acceptance follow-up

OneID: not involved. Existing staff IDs and group references remain unchanged;
no customer resolution, provisioning, or identity linking is added.

Persistence: Provider reads plus local PostgreSQL transactions. Keep the existing
directory snapshot owner, Unit of Work, CAS, idempotency receipts, and events.
No Provider write, new queue, worker, retry kernel, or External Effect is added.

Live acceptance of release 2369c94 confirmed `huang` filtering and a successful
staff profile refresh with real display names. Owned-group refresh still returned
503, and configuring a Webhook for a paused plan returned a state conflict.

This follow-up permits absent group names through the read adapter and directory
constraint without fabricating names, preserves genuine protocol failures, and
allows only Webhook descriptor configuration while a plan remains paused. Active
plan editing remains rejected. The plan overview reads the existing staff profile
directory by its saved local ID. Cross-origin requests bypass the Group Ops Host
transport adapter without CSRF or body injection.

Verification includes named/unnamed mixed Provider responses, a PostgreSQL
directory round trip, paused Webhook CAS and idempotency replay, and browser
adapter regressions. Final real Provider acceptance and deployment remain
separate from these local checks.
