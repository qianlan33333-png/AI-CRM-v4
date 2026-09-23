# V2 runtime Config release history

`migrate-v2-runtime-config-releases` copies a protected, frozen V2
`config_releases` snapshot into Config's **read-only historical ledger**. It is
not a configuration publisher: it never inserts `config_runtime_releases`,
changes `config_runtime_active_release`, creates an Outbox event, creates a job,
or makes a Provider call.

OneID is not involved: no customer or external identity enters this import.
Persistence is a single serializable Config transaction, with a protected source
revision and a per-row target reconciliation check.

## Source contract

`extract` uses one repeatable-read, read-only source transaction and the V2
columns `id`, `release_key`, `profile_id`, `status`, `changes_json`,
`before_json`, `validation_errors_json`, `checksum`, `based_on_release_id`,
`rollback_of_release_id`, `created_by`, `created_at`, `validated_at`,
`published_by`, and `published_at`, matching V2 migration
`migrations/versions/0144_config_release_control_plane.py`. The caller must
name the immutable 40-character source revision.

The operator supplies a pre-created 0600 base64 AES-256 key file; the command
uses it to write an AES-256-GCM protected snapshot. Both the supplied key and
snapshot must be regular 0600 files for every mode.
The command prints only source counts, classification counts, and the snapshot
digest; it never prints release values.

## Classification

The V2 managed-runtime catalog contains no key semantically equivalent to the
V3-only `automation.operations.max_recipients_per_run` consumer boundary.
Every V2 release row is therefore recorded as `excluded` with
`no_v3_runtime_equivalence`; numeric shape alone never establishes a mapping.

The sealed snapshot retains the complete `changes_json` and `before_json` for
source-digest verification. The V3 ledger stores only its digest plus the
release key, profile, checksum, state, creator/publisher, all source timestamps,
and based-on/rollback relations. It deliberately stores no raw setting value.

## Operator flow

1. `extract` produces the protected snapshot from the source database.
2. `inspect` reports its safe classification summary.
3. `dry-run --manifest-sha256 …` verifies the exact snapshot before any write.
4. `apply --confirm-apply --manifest-sha256 …` records history only.
5. Re-running the same source revision and digest returns the original receipt;
a changed digest for that revision fails.
6. `verify --manifest-sha256 …` reclassifies each protected source row and
compares its actual target key/profile/checksum, state, creator/publisher,
timestamps, based-on/rollback relations, classification, and read-only fact
before marking the batch reconciled.

This is an offline tool. It does not activate historical values or make old
runtime jobs use them. Existing Automation previews and runs remain explicitly
unobserved unless they received a new V3 Config snapshot at their own creation
boundary.
