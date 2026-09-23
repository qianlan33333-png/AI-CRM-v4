# PR09 projection closure follow-up

Date: 2026-09-03

This follow-up closes the previous `EmptySafeProjectionReader` gap without
changing any donor frontend file or adding a second admin shell.

## Composed path

```text
adminops/store.ProjectionStore (PostgreSQL)
  -> adminops/app.ProjectionService (bounded validation/redaction)
     -> config/port.SafeProjectionReader
        -> config/http GET /api/admin/config/releases
                         GET /api/admin/config/diagnostics
```

The Composition Root also wires `release/app.ObservationService` through a
release observation port and `adminops/app.DiagnosticsService`. On successful
composition it records the configured release SHA as `observed` and a bounded
`aicrm.composition=ok` diagnostic. Those are local observations only; they do
not claim deploy, cutover, Provider execution, or runtime-secret application.

The active donor request contract is preserved at the host boundary. A config
list/detail read makes the donor's real four GETs (`categories`,
`app-settings`, `push-capabilities`, and `releases`); the adapter does not
rewrite, suppress, or replace a request that the active page emits. The two
read-only rows use the exact response keys consumed by `readAdminRows`:

```text
GET /api/admin/config/push-capabilities
  {"capabilities":{"local_projection":...,"provider_write":...}, ...}
GET /api/admin/config/releases
  {"releases":[{"id":1,"state":"observed","checksum":"..."}], ...}
GET /api/admin/config/diagnostics
  {"diagnostics":[{"id":1,"key":"aicrm.composition","status":"ok","observed_at":"..."}], ...}
```

Capability values are a bounded local runtime-policy projection: the local
read projection is available and provider writes are disabled because no
outbound adapter is composed. They are not a fabricated Provider result.
Release rows expose only `id`, `state`, and `checksum`; diagnostics expose only
the bounded key/status/time tuple. A projection read failure or empty persisted
projection is `503`, never a successful empty placeholder. `configDetail.html?cat=` accepts only the three
active keys `app-settings`, `push-capabilities`, and `releases` (plus the
trailing-slash config alias); unknown keys remain `404`. Donor toggle/check
methods still fail closed without an HTTP request, as they do in the frozen
`HttpApi`; no write action was invented.

`ProjectionStore` owns and writes only `adminops_release_projections` and
`adminops_diagnostic_snapshots`. Queries select `id`, SHA/key, status, and
timestamp only. The physical `details` column is never selected, and write
methods always insert `{}`; no secret, PII, URL, actor credential, or external
result can cross the Config HTTP DTO boundary. Persisted rows with an unsafe
identifier/status fail closed as HTTP 503 rather than being skipped or turned
into a fabricated empty 200 response.

## PostgreSQL Journey

`cmd/aicrm/projection_journey_integration_test.go` creates an isolated schema,
applies `0015_config_adminops.sql`, and proves all of the following:

1. Config writes commit through the Config Manager and an immediate read after
   refresh returns the stored `wecom.corp_id` value.
2. The same transaction records a queryable Config audit and outbox fact.
3. A release observation and runtime composition diagnostic are persisted by
   the AdminOps projection store and returned by the safe adapter.
4. Projection details remain `{}` and are not part of the returned DTO.

The test skips only when `DATABASE_URL` is absent, matching the other
PostgreSQL integration journeys. Unit tests cover malformed persisted keys,
status allowlists, bounded writes, and release observation validation.

## Scope boundary

This is the safe read/observation slice required by the frozen PR09 Config
page. The larger AdminOps `Repository` (credentials, categories, jobs) and
the Release candidate/cutover state machine remain unmounted; no new frontend
buttons, loader, route, DTO, or donor behavior was introduced for them. The
existing PR10 host remains the sole sidebar/shell owner.

The 16-file PR09 archive remains byte-exact against donor commit
`6bfbe5816bb89913c70adaca87d6a486260e016e`; the freeze ledger and `cmp` gate
are unchanged. The donor build chain (`build.mjs` -> `main.ts` -> `legacy.ts`
-> `AdminController`) is evidence for the frozen runtime, not a product-only
loader to be recreated in v3. PR10 may extract the allowlisted fragments and
mount them in its existing `admin_base`/single primary sidebar only; no donor
`.shell`, `.side`, nav, or second shell is mounted.
