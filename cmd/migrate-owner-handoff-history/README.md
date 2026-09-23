# Owner-handoff history import

This tool imports only the frozen rows of the legacy `owner_migration_results`
ledger. It never accepts an external effect, calls a Provider, changes
`customer_local_owners`, or provisions/links OneID identities.

1. On an operator workstation, set all of
   `AICRM_OWNER_HANDOFF_SOURCE_SSH_HOST`,
   `AICRM_OWNER_HANDOFF_SOURCE_SSH_USER` (defaults to `ubuntu`),
   `AICRM_OWNER_HANDOFF_SOURCE_SSH_KEY_FILE`,
   `AICRM_OWNER_HANDOFF_SOURCE_KNOWN_HOSTS_FILE`, and
   `AICRM_OWNER_HANDOFF_HISTORY_CORP_SCOPE=wecom-corp:<corp-id>`.
   Run `scripts/capture-owner-handoff-history-source.sh /secure/owner.stream`.
   The script runs one repeatable-read, read-only PostgreSQL transaction over
   `owner_migration_results` and its `rows_json` ordinality. The pinned source
   host must provide the existing `psql-stdin` wrapper: it must pass stdin to
   PostgreSQL without adding writes, and the source credential must only permit
   the read-only capture query.
2. On the target host with `AICRM_DATABASE_URL` and the existing
   `AICRM_SURVEY_DATA_KEY`, create an encrypted, mode-0600 snapshot:
   `migrate-owner-handoff-history --mode=inspect-stream --source-stream=/secure/owner.stream --snapshot=/secure/owner.snapshot`.
3. Record `--mode=dry-run` output. Apply only after a human supplies the exact
   printed SHA-256: `--mode=apply --snapshot=/secure/owner.snapshot
   --manifest-sha256=<exact> --confirm-apply`.
4. Re-run the same command with `--mode=verify` and the same digest. It checks
   every source key, source/result digest, frozen mapping result, occurrence,
   and protected snapshot before marking the import reconciled.

Rows with no unique existing OneID/Access mapping remain `pending_mapping` or
`conflict`; malformed historical rows become explicit `invalid` facts. A
second source capture may add new source keys, but a changed existing source
row is rejected. The release package contains this binary only; the installer
does not run it automatically.
