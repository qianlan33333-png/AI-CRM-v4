# Media refresh core verification

Architecture classification: OneID is not involved because material records do
not identify customers. Persistence uses the Media write Unit of Work plus
River durable jobs. Provider writes remain owned by Outbound and pass through
External Effects; no second worker, lease, retry, or reconciliation engine was
introduced.

Local verification used only isolated PostgreSQL databases and local fake
providers. It did not call WeCom or any production service.

- Fresh database migration evidence: `/tmp/media-core-final-migrations.log`.
  River schema version 6 and platform migrations 0125/0126 are present.
- Material PostgreSQL evidence: `/tmp/media-core-final-material-pg.log`.
  Covers shared-file deduplication, forced refresh, old credential fallback,
  expiry/send margin, unknown reconciliation, retry/cancel projection, daily
  catch-up/restart/membership, and operation-key conflicts.
- External Effects PostgreSQL evidence: `/tmp/media-core-final-eer-pg.log`.
  Covers preflight without message attempts, actual River lane concurrency
  3/2, lane-preserving retry, bounded automatic media/Excel rate-limit retry,
  and typed media reconciliation atomicity.
- Group Ops automatic-resume PostgreSQL evidence:
  `/tmp/aicrm-daily-media-refresh-0910/reports/groupops-material-auto-resume-final.log`.
  A single accepted source-only intent snoozes its original River job with the
  Group effect still queued and attempt count zero, accepts a generic Media
  preparation effect, then resumes that same Group job after the fake upload
  succeeds. The fixture applies migrations 0036/0120/0124/0125/0126 and does
  not require a second operator action.
- Fast preflight evidence: `/private/tmp/media-core-final-fast2`; all stages
  passed. Compile-only checks for the affected Go packages also passed.

The timeout chain is: configurable material HTTP upload 120 seconds by default
and at most 240 seconds, EER River job timeout 270 seconds, and EER attempt lease
300 seconds. Message HTTP calls retain their existing 8-second timeout.

Group Ops accepts an immutable `GroupOpsMaterialIntentSnapshot` without a
temporary Provider media ID. Its already-accepted EER job uses the existing
preflight to enqueue generic preparation, snoozes with message attempt count
zero, and continues the same job after the material succeeds. Provider-ready
legacy snapshots remain readable through the strict historical validator.

Excel and material lane concurrency is per effects-worker process. Multiple
deployed worker replicas would multiply the total concurrency; this local
change did not alter production replica configuration.
