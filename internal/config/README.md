# PR09 configuration slice

This package is PR09's bounded local-configuration implementation. It owns the
non-secret registry, PostgreSQL settings/receipt/audit/outbox tables, safe
admin projection, and the local setup-wizard CAS/readback behavior. Values
that are secret-bearing are represented only by configured/masked facts and
are rejected before persistence or event append.

The application layer depends only on v3 `platform/port` and these local
configuration ports. The v3 HTTP adapter exposes the frozen app-settings and
setup-wizard DTOs behind session, role, CSRF, route-bound action-token and
idempotency checks. It saves only local material facts: `external=false`,
`local_only=true`, and `runtime_applied=false`; it neither applies runtime
configuration nor calls a Provider. Release and diagnostics reads are served
by the composed AdminOps-owned safe projection adapter, which returns real
persisted rows and fails closed on malformed data rather than returning an
empty success placeholder. The PR10 host adapter mounts only the verified
donor fragments in the sole v3 admin shell.

## Runtime-release UI evidence

`TestRuntimeReleaseHostJSDOMJourneyUsesActualPostgreSQLHTTP` is a fast DOM,
actual-HTTP, PostgreSQL regression; it does not claim a real browser.
`TestPostgreSQLRuntimeReleaseChromiumJourney` starts an isolated Chromium
profile against the production Composition Root and follows the real Access
login, Secure session, CSRF, embedded Config shell and Host through draft,
validation, publication, rollback and effective-value readback.
