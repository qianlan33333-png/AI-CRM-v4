# Sidebar service-period history source contract

This document defines the protected input for
`cmd/migrate-sidebar-history`. It describes a read-only historical import; it
does not authorize a production capture or target write.

## Boundary

OneID resolves an existing verified UnionID to a canonical Customer through
the existing Identity port. It never provisions, merges, or otherwise creates
a Customer. Order owns service-period entitlement targets and Coupon owns
customer-claim targets. The command invokes their existing historical
importers, then records a migration-boundary map or quarantine receipt. It
does not open orders, grant service, redeem coupons, send messages, or make a
Provider call.

## Protected capture format

`scripts/capture-sidebar-history-source.sh` runs one repeatable-read,
read-only source transaction and emits marker-prefixed JSON rows. The command
seals the rows into a `schema_version: 2` manifest. Version 1 remains accepted
only for already-protected snapshots and is unmarshaled without changing its
canonical row layout or receipt digest.

The source-side `admin_alliance` conversion follows the frozen service-period
write/display code in `aicrm_next/extensions/commerce/service_period/domain.py`:
`str(value or "").strip()`. Version 2 therefore preserves source key presence
and JSON type in the stream, then applies the Python Unicode whitespace set to
string values before the manifest is sealed. This includes normal spaces,
tabs, NBSP, and Python's U+001C through U+001F controls. PostgreSQL `BTRIM`
is not used because its default behavior is ASCII-space only.

The distinction is intentional:

| Source `metadata_json.admin_alliance` | Version 2 manifest fact |
| --- | --- |
| key missing | no `alliance` key; historical unknown |
| JSON `null` | `"alliance": null`; source explicitly unknown |
| JSON string, including whitespace-only | Unicode-stripped string; `""` stays an explicit clear |
| another JSON type | capture fails before a snapshot can be sealed |

The source stream contains no legacy coupon number. The mandatory target
`claim_no_masked` field is therefore imported as the explicit safe conversion
`""`; no ID, UnionID, or generated value may be substituted.

## Reconcile proof

The exact manifest digest, each row digest, and its mapped or quarantined
receipt must all remain intact. A mapped entitlement must still match its
customer, product mapping, status, period, remark, alliance, source receipt,
and source timestamps. A mapped customer coupon must still match its customer,
rule mapping, lifecycle status, explicit empty masked number, claimed/valid/
redeemed timestamps, source receipt, and source timestamps. A quarantined row
must retain its subject, digest, and still-applicable resolution reason.

The map and quarantine receipts are mutually exclusive. When the same protected
row later becomes resolvable, creation of its first map and deletion of its
previous quarantine happen in one serializable receipt transaction. A row that
already has a map cannot be turned into a quarantine by a later failed replay;
that is evidence drift for reconciliation. Each mapped customer is also
resolved again from the scoped, verified UnionID inside the reconciliation
transaction, so a source map and target changed together to the same wrong
customer still fail. The deleted quarantine is the superseded migration
outcome, not an append-only source log: the sealed source row and manifest
digest remain protected for the mapped outcome's reconciliation.

Reconciliation uses one serializable transaction for the migration batch and
only marks it `reconciled` after every fact matches. It is read verification
plus that batch-ledger transition; it creates no historical effects.
