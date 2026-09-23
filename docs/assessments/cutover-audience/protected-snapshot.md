# Production audience protected snapshot

The command supports protected source `extract`/`inspect` and owner-mediated
static `preflight`/`apply`/`reconcile`. No mode activates audiences, executes source
SQL, restores source jobs or calls Providers. See the static import contract below.
Source is the real singular `ai_audience_package_group`, `ai_audience_package`,
`ai_audience_package_version`, `ai_audience_member_current` contract. It preserves
all fields via JSONB, including historical natural-language definitions/prompts,
parameters, original SQL text, all 83 versions and both active/exited membership.
Counts here describe the coordinator's source inspection, not a capture receipt:
38 packages (6 active, 32 archived), 83 versions, 29,248 members (14,556 active,
14,692 exited); 80 versions have no template key. Extraction independently checks
counts/digests rather than trusting these numbers.

## Execution

Prepare a cryptographically random 32-byte key, raw standard-base64 encoded,
in a separate 0600 file. Keep snapshot and key outside git and logs. On the
source service host, pass its effective source DB URL through an environment
variable, never a command-line URL. Use an existing protected connection.

```
migrate-audience-history extract \
  --source-url-env AICRM_AUDIENCE_SOURCE_DATABASE_URL \
  --source-system aicrm-v2-production \
  --snapshot /protected/audience.enc \
  --snapshot-key-file /separate/audience.key
migrate-audience-history inspect \
  --snapshot /protected/audience.enc \
  --snapshot-key-file /separate/audience.key \
  --expected-sha256 <digest-returned-by-extract>
```

Optional `--declared-corp-scope` and `--declared-unionid-scope` preserve an
operator-supplied declaration only. Omitted scope is explicitly empty. Every
snapshot has `scope_verified=false` and `executable=false`; inspect rejects a
snapshot claiming otherwise. A declaration is not sufficient verified OneID
provenance. Output includes only fixed count buckets, capture time and digest;
no names, scopes, identities, definitions, SQL, or raw DB errors.

Snapshot uses unique magic + AES-256-GCM authenticated encryption. Every row and
table has a digest, source IDs are unique and ordered, required group/package/
current-version associations are checked, and a current version belonging to
another package fails closed. Output is 0600, exclusive creation, fsynced. An
interrupted write is removed. Plaintext never lands on disk. Do not execute any
historical SQL, including compiled SQL; even inspection merely hashes it.

## Verification

Unit/race tests cover authenticated encryption, tamper/wrong-key rejection,
precision of large JSON integers, preservation of SQL as inert text, no sensitive
summary output, exact digest, file permissions, overwrite/symlink rejection,
foreign-key/source drift and unsupported activation flags. Isolated PostgreSQL
integration captures real tables and verifies a hostile source view attempting
INSERT fails inside READ ONLY, with zero inserted rows. Production execution and receipts are the cutover coordinator responsibility.

## Static import contract (2026-09-11)

Classification: OneID scoped Resolve only (declared reference, never Provision or
merge). Segment owns transactional persistence; no Provider calls, queued jobs,
source SQL execution or customer messages. Core development skill applied.

`preflight`, `apply`, and `reconcile` require the exact inspected `--expected-sha256`
and a real `--admin-id`. The default target environment name is `AICRM_DATABASE_URL`;
change `--target-url-env` when the deployment uses another name. Source scope
values must have been recorded from verified configuration during extract. An empty
scope remains `missing_scope` quarantine. No ID prefix guessing or phone-hash lookup.

```sh
migrate-audience-history preflight --snapshot /protected/audience.enc --snapshot-key-file /separate/audience.key --expected-sha256 "$SNAPSHOT_DIGEST" --admin-id "$IMPORTING_ADMIN_ID"
migrate-audience-history apply --snapshot /protected/audience.enc --snapshot-key-file /separate/audience.key --expected-sha256 "$SNAPSHOT_DIGEST" --admin-id "$IMPORTING_ADMIN_ID" --confirm-static-import
migrate-audience-history reconcile --snapshot /protected/audience.enc --snapshot-key-file /separate/audience.key --expected-sha256 "$SNAPSHOT_DIGEST" --admin-id "$IMPORTING_ADMIN_ID"
```

Apply requires migration `0130_segment_historical_import.sql`. It writes exclusively
through the Segment `HistoricalImporter` Port under the same PostgreSQL UoW as
Identity Resolve. Existing Segment operation receipts provide idempotency; the
historical tables retain source-to-target maps, every row digest, all member
outcomes, and the complete authenticated encrypted envelope. Keep the decryption
key outside the database and retain it with the protected migration archive.

Source active/paused packages become paused/manual; archived packages remain
archived. Groups and package display names are preserved; native codes are stable
migration-generated identifiers and original keys remain in encrypted evidence.
Any existing group-name uniqueness conflict fails the transaction, never merges
unrelated groups. The original 83 version facts, NL, SQL and refresh parameters
remain historical evidence, not runnable DSL. A deliberately inert configuration
marker is rejected by the closed runtime compiler. A DB constraint prevents
historical packages becoming active. There is no restored lease, schedule cursor,
member entered/exited event, or refresh job. Native package list and snapshot reader
Ports expose static resolved active members; duplicate canonical customers appear
once in a snapshot while every source membership retains a ledger entry. Exited
and quarantined source rows remain preserved and counted.

Reconcile compares source row conservation and exact native member sets against
the ledger (both EXCEPT directions), including every imported immutable snapshot.
It is read-only. Counts distinguish source resolved members from deduplicated
published members; a zero-error import with quarantines is not full usable coverage.

The same source/digest/admin replays its original receipt. A newer consistent
capture (strictly greater captured_at) imports a new immutable snapshot on the same
mapped packages; old captures cannot overwrite it. This is also the recovery path
after missing OneIDs have been legitimately repaired: take a fresh source capture,
then preflight/apply/reconcile. A failure anywhere rolls back all records/receipts.
A later source capture can update only mapped historical groups/packages, never
normal target packages. Deletions are not inferred from absent source records;
source archived rows must remain present in the capture. No destructive rollback
command is provided; restore the validated pre-import backup before traffic, or
forward-correct with a newer source capture. Once target writes begin, backup restore
would discard new facts and is not an automatic safe rollback.

Validation: real isolated PostgreSQL tests cover native package/snapshot/member
readback, conservation, scoped identity conflict/missing-scope isolation, exact
replay, newer snapshot import, stale evidence guards, late failure and caller UoW
rollback, activation constraint, and zero jobs/member events. No production apply
was executed by the implementation agent.
