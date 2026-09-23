# PRD: GroupOps plan-list bound-group projection

## Decision

The GroupOps plan list will receive `bound_group_count` from its existing `GET /plans` DTO. This removes the Host adapter's per-plan `summary → groupsForPlan → detail + directory` waterfall for list rendering. This is a source-level request-path finding, not a measured production latency claim.

The change concerns local GroupOps PostgreSQL reads only. It does not involve OneID, Provider reads or writes, external effects, persistent jobs, authorization scope, caches, queues, or new APIs. The V3 Host adapter and GroupOps Standard are V3-owned assets; the byte-frozen `web/donors/groupops-v2` remains unchanged.

## Evidence and scope

`web/v3/groupOpsHostAdapter.ts` currently projected each list row through `summary`. `summary` calls `groupsForPlan`, which reads the plan detail and pages the group directory. `internal/groupops/port/port.go` and `internal/groupops/store/postgres.go` already supply the list's plan, queue count, owner, order, `limit`, and `offset`, but not the persisted group-asset count.

The repository will add a non-negative `bound_group_count` to `PlanListItem`, calculate it with a same-domain `group_ops_plan_group_assets` count in `Repository.List`, and expose it through the existing `PlanPage`. It retains current queue filtering, owner projection, `updated_at DESC, id DESC` order, and offset/limit behavior. It does not add an index from source inference: the existing `(plan_id, asset_reference, id)` index covers the filter.

The count includes every persisted binding, including an archived plan and an asset reference whose directory record is absent. It does not change detail-page directory/owner semantics. The list's `List` and `Count` statements retain PostgreSQL READ COMMITTED behavior; this PR does not claim they share a snapshot.

## UI and compatibility contract

The Host maps the list field directly and does not fall back to `summary`, detail, or directory reads. A response that lacks the new field remains readable but renders the count and aggregate as unknown (`—` and “已绑定群（暂不可用）”), rather than fabricating zero. A negative or fractional value is a visible list-read error. The adapter validates all list-row counts before publishing any row revision, so a malformed later row cannot advance the CAS revision of an earlier row that remains visible after the failed load.

`GroupOpsPlan` stays closed. `GroupOpsPlanListItem` becomes an independent, complete closed schema rather than an `allOf` extension of that closed detail schema; this allows the legitimate list counters while rejecting undeclared fields. The existing OpenAPI validator adds a real zero-counter payload, an undeclared-field rejection, and a negative-count rejection using Swagger Parser's existing Ajv dependency without adding a package.

## Verification and delivery gates

The PostgreSQL fixture creates a random schema only after an explicit `AICRM_DATABASE_URL`, applies `0003_access`, `0005_external_effects` (a required existing foreign-key dependency only), `0012_group_ops`, and required GroupOps read migrations, and closes the pool then drops that schema through `t.Cleanup`, reporting a failed drop. It covers zero bindings, missing directory records, archive retention, removal, new binding, stable page order, owner, and queue values.

On 2026-09-15 the fixture passed on a new Provider-disabled localhost PostgreSQL 16.13 instance with a random schema. A normal invocation with both database URL variables unset correctly skipped before connecting and is recorded as a skip, not coverage. Go unit and HTTP tests cover zero serialization and negative domain rejection.

The Host DOM test is already part of `scripts/run-donor-view-consumers.sh`. It asserts an initial list reads only `GET /plans` plus the existing operation-member projection, with no per-plan detail or directory calls. It also covers missing, invalid, and mixed valid/invalid count DTOs. The complete OpenAPI validator and donor-host test require the standard API source-authority metadata synchronization after the functional commit; until that sync, canonical source-view materialization correctly refuses the changed `api/openapi.yaml`. The functional commit, authority sync, required consumer run, CI, merge, deployment, and browser readback are separate delivery records.
