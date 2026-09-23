# Audience history source role repair

## Business decision

`migrate-audience-history extract` defaults to `AICRM_AUDIENCE_SOURCE_DATABASE_URL`, but the shared closed configuration allowlist rejects that name. Its PostgreSQL integration test also asks for an undeclared admin URL, so the CI database cannot exercise the read-only extract path. The repair admits exactly the default source-role name and binds the test to the existing canonical CI database URL.

The command continues to require an explicit source URL and snapshots only the existing historical audience facts. It does not make source selection dynamic, accept arbitrary environment names, expose URL values in errors, activate imported packages, or connect to a production database.

## GitHub reference

This follows the existing Automation Operations migration delivered in GitHub PR #65: named configuration roles remain a closed allowlist, and the migration test uses `t.Setenv` to bind only its isolated source. The existing `NamedDatabaseURL` boundary is extended; no new configuration mechanism is introduced.

## Architecture classification

```text
OneID: involved only at the existing downstream historical-import boundary; this repair does not resolve, provision, link, or merge identities.
Persistence: isolated PostgreSQL read-only source snapshot and local test database; no new tables, transaction model, or durable job.
External Effects: not involved; no Provider read/write, queue, worker, retry, or reconciliation behavior.
```

## Acceptance

1. The allowlist accepts the target URL, the existing Automation Operations source URL, and this exact audience source URL; unknown and empty values fail without disclosing a URL.
2. The integration test creates a temporary local PostgreSQL source database through `AICRM_DATABASE_URL`, runs the default `extract` source role, produces an encrypted snapshot, and retains the existing hostile-view read-only enforcement check.
3. Run `go test ./internal/platform/config ./cmd/migrate-audience-history`; CI must run the PostgreSQL case with its canonical `AICRM_DATABASE_URL`.
