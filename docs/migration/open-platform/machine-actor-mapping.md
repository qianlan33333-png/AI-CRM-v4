# Open-platform machine actor mapping

## Classification

```text
OneID: not involved in actor attribution. Customer references still resolve through Identity before a customer-scoped command.
Persistence: each owning module records the actor in its own PostgreSQL Unit of Work; Access retains the authenticated machine-client record.
External Effects: a machine command only enters the existing domain command path. It does not bypass approval, River, outbound, or External Effects gates.
```

## Rule

`MachinePrincipal.ClientRecord` and a payload-supplied operator are never an
`admin_users.id`, a super-admin, or a human session. The canonical machine
subject is the bounded value `machine:<client_id>`. It is derived only after
Access has authenticated the current credential and is not accepted from a
request body.

## Existing seams and required adapters

| Frozen routes | Owning V3 boundary | Machine actor mapping | Status |
| --- | --- | --- | --- |
| `/api/operation-cycles/*` reads and writes | Operation Cycle commands and event journal | Set the existing string `ActorID` / `PrincipalID` to `machine:<client_id>`. The event journal must classify that prefix as a `machine` audit actor rather than its current blanket `admin` classification. Reports use the same value for their reporter/client fields. | Existing Port can represent the subject; adapter pending. |
| `/api/automation/group-ops/broadcast` | `groupopsport.AcceptPlanCommand` | Use the existing string `AcceptedBy` as `machine:<client_id>` with `RunTriggerBroadcast`. Do not call `AcceptBroadcast(planID, adminID)`, which manufactures `admin:<id>`. A narrow stable runtime interface exposes `AcceptPlan`; the app already implements it and preserves the value in `group_ops_runs.accepted_by`. | Existing data model can represent the subject; Port adapter pending. |
| `/api/ai-assist/external/*` | `aiassistantport.Actor` and Access machine audit | The owner command uses the existing `ActorService` kind, never `ActorAdmin`. Its service ID is configured at Composition; the authenticated client is recorded by an Access invocation audit with route and request digest only. The current V3 AI Assistant owner has no campaign/preparation persistence contract, so a route is not mounted until its smallest owner Port/data adapter preserves the frozen preparation, approval, and no-direct-send behavior. | Actor type exists; campaign/preparation owner adapter pending. |
| `/api/external/ai-audience/*` and `/api/ai/audience/*` write routes | Segment configuration, snapshot, and execution services | `segmentport.MutationActor` freezes `admin:<staff_id>` versus `machine:<client_id>` and rejects a staff ID on a machine subject. Current Segment commands, domain rows, mutation facts, and 0039 constraints still require a positive integer actor, so an Access machine-client row ID must not be passed there. The next smallest owner change adds `actor_kind` and `actor_ref` persistence while retaining the existing positive integer actor fields for human compatibility. Machine writes keep that human field unset and record `machine:<client_id>` only in the new owner-owned fields, in the same transaction. It must not create an Access admin, write Access tables, or replace Segment's existing River/EER paths. | Stable actor Port frozen; owner persistence/runtime adaptation required before write routes mount. |

## Consequences for route wiring

- Read-only routes do not synthesize an actor. Their resource checks continue
  to use Access capabilities, token scopes, and owner scope before the owner
  Query Port reads data.
- A user-supplied `operator`, `actor`, `admin_user_id`, or equivalent field is
  input data only. It cannot choose the actor recorded by a V3 command.
- A failed authorization or validation writes no business effect. Where the
  owner command has a receipt, its existing idempotency key remains scoped to
  the mapped machine subject.
- The operation-cycle and Group Ops adapters preserve their existing
  transaction, event, and provider-disabled gates. They do not invoke a
  legacy HTTP handler.
