# Group Ops Webhook Inbound Implementation Plan

**Delivery status:** This is a branch-level implementation plan and contract, not a deployed production capability. It must not be used to send a production message until review, merge, migration, release, and runtime/provider gates each have independent evidence.

**Goal:** Let an active, configured Webhook group-operations plan accept one signed, idempotent dynamic message request and queue immutable, target-scoped group-message effects without requiring preconfigured plan nodes.

**Architecture:** A Webhook plan remains a Group Ops-owned plan with a configured descriptor and bound opaque group references. The inbound handler authenticates exact request bytes, parses a strict dynamic-message command, and calls a Group Ops application use case. That use case first reads the plan and validates that requested targets are a non-empty subset of plan bindings, then freezes message/source facts and EER acceptance in one PostgreSQL Unit of Work. `outbound`/External Effects remains the only provider-write path. Media owns image/file references and mini-program cover resolution; its built-in resolver accepts only the audited daily-lesson AppID/path form, and other inputs fail closed without an EER intent.

**Tech Stack:** Go modular monolith, PostgreSQL 16, existing Group Ops, Media, Outbound, External Effects, River job queue, WeCom provider adapter.

---

## Business and architecture decision

```text
OneID: not involved — the request contains no customer or external-user identity;
       target values are plan-bound opaque group references only.
Persistence: local transaction + Provider write/external effect — Group Ops owns
             the frozen inbound run/intent/execution facts; Media owns source and
             preparation facts; Outbound is the only WeCom writer through EER.
```

- A `plan_type=webhook` plan may activate with zero nodes, but only if it has the existing responsible operator, a configured Webhook descriptor, and at least one bound group. Standard plans retain their node requirement.
- The historical node-triggered Webhook behavior is not a product contract for this capability. Existing standard plans and prior runs are read-only compatibility data.
- A Webhook target must equal an existing `group_ops_plan_group_assets.asset_reference` for the resolved plan. Display names are never accepted as a target selector.
- No new customer identity matcher, provider writer, queue, worker, retry loop, or reconciliation state machine is permitted.

## Inbound contract

The exact bytes are HMAC-authenticated using the existing descriptor headers:

- `X-AICRM-Client-Id: aicrm-webhook-group-ops`
- `X-AICRM-Timestamp`: Unix seconds
- `X-AICRM-Event-Id`: caller-stable event identifier
- `X-AICRM-Signature`: HMAC-SHA256 of the established canonical signing input

The exact HMAC input is the UTF-8 byte sequence:

```text
<X-AICRM-Timestamp> + "\n" + <X-AICRM-Event-Id> + "\n" + <exact request body bytes>
```

The signature is lower-case hexadecimal SHA-256, optionally prefixed with
`sha256=`. Timestamps must be within five minutes in the past and one minute
in the future. The 64 KiB body limit is applied before signature verification.
The legacy signature intentionally has no descriptor field, so an event ID is
**global for `aicrm-webhook-group-ops` across all Group Ops Webhook URLs**.
The replay receipt's payload digest commits both descriptor reference and body:
reusing a valid signature at a different descriptor conflicts rather than
creating a second group send.

Request JSON has no free-form URL fetches:

```json
{
  "webhook_reference": "groupops-46d055d8-0276-449c-a2b5-170af39c3c92",
  "target_chat_references": ["stable-bound-chat-reference"],
  "messages": [
    {"type": "text", "text": "今日话术"},
    {"type": "miniprogram", "appid": "wx-example", "path": "pages/course/index", "title": "课程详情"},
    {"type": "image", "image_id": 123},
    {"type": "file", "attachment_id": 456}
  ]
}
```

- The current WeCom group-message request represents one optional text block followed by an attachment array. Therefore `text` may occur at most once and only as `messages[0]`; `image`, `file`, and `miniprogram` follow in the supplied attachment order. A text block after an attachment, two text blocks, or more attachments than the existing bound is rejected. This release does not create a multi-effect ordering chain.
- `webhook_reference` is required and must exactly equal the final opaque path segment of the URL. It is part of the HMAC-authenticated body, so a signed request cannot be first accepted at a different descriptor URL.
- `image_id` and `attachment_id` are existing Media-owned IDs created via their authenticated multipart upload APIs. JSON never accepts `url`, raw binary, a remote filename, or a provider media ID.
- A mini-program cover is resolved only through a Media-owned AppID-plus-path resolver. The implemented resolver accepts only the audited daily-lesson AppID/path form; an unsupported AppID/path returns `miniprogram_cover_unsupported`, while a supported source that cannot be read or verified returns `miniprogram_cover_unavailable`. It never selects a default cover, a preconfigured path mapping, or follows a caller URL.
- The title is required for a mini-program command unless the approved resolver contract returns a canonical title as part of the same immutable result. The resolver interface must make that choice explicit.

## Idempotency, transaction, and material lifecycle

1. Strictly decode the bounded JSON command, require `webhook_reference` to equal the URL key, and validate its shape before authentication can claim a replay receipt. This avoids polluting a valid event when a signed body is delivered to the wrong URL.
2. Authenticate the exact bytes and claim the existing digest-only **global client/event** replay receipt. Read the resolved plan before any mini-program resolver work; inactive plans, missing descriptors, and targets outside its bindings fail at this point.
3. For a new valid event, resolve an automatic mini-program through the Media-owned AppID/path resolver outside the Group Ops PostgreSQL UoW. The implemented resolver permits only the audited daily-lesson contract, verifies its fixed PNG source, and returns a prepared material value. In the final accepted UoW it materializes the local image and mini-program alongside the frozen Group Ops source facts, locks/revalidates the plan, reserves a run keyed by descriptor/plan plus `event_id`, creates immutable execution intents, accepts EER effects, and binds their `effect_id`s. Unsupported paths do not resolve and do not create an EER intent.
4. The command snapshot contains target references, ordered content blocks, source snapshots, and their digests. EER payloads/logs contain digests only where current privacy rules require it. A same run key with a different canonical command digest conflicts even for a non-HTTP Runtime caller.
5. Media source capture in that UoW is database-only. Existing Outbound preflight performs Provider-specific media preparation after commit. A retry returns the existing event receipt and frozen command/bindings; it does not re-resolve an AppID/path, substitute a changed cover, or mint a new key.
6. Provider results continue to distinguish accepted/queued/attempted/executed/outcome_unknown/reconciled and Group Ops delivery evidence remains separate.

## Failure semantics

| Condition | Result | Effects |
| --- | --- | --- |
| invalid/missing HMAC, timestamp, or client ID | `401 protocol_authentication_failed` | none |
| invalid JSON or shape | `400 invalid_request` | none |
| same event identifier, different body digest | `409 idempotency_conflict` | none new |
| plan inactive, descriptor missing, no bindings, or target outside plan | `409 operations_conflict` | none |
| invalid Media reference or cover resolver absent/fails before acceptance | explicit unavailable/configuration error | none |
| frozen source later cannot be prepared by Outbound | existing media-pending/final-failed effect state | EER may remain; zero provider group-message call |
| same signed event and same canonical payload | `202` existing run summary | no duplicate EER |
| outbound disabled | independently visible runtime configuration failure; never report it as delivered | no provider call |

## Implementation tasks

### Task 1: Pin Webhook plan activation and input contracts

**Files:**
- Modify: `internal/groupops/port/port.go`
- Modify: `internal/groupops/domain/groupops.go`
- Modify: `internal/groupops/app/service.go`
- Test: `internal/groupops/app/service_test.go`

Write tests first for a node-free configured/bound Webhook plan activating, a node-free standard plan being rejected, and malformed dynamic message commands. Introduce typed plan-type and bounded command/value types; do not use `map[string]any` after the HTTP edge.

### Task 2: Receive, freeze, and idempotently accept dynamic Webhook runs

**Files:**
- Modify: `internal/groupops/http/handler.go`
- Modify: `internal/groupops/port/runtime.go`
- Modify: `internal/groupops/app/runtime.go`
- Modify: `internal/groupops/store/postgres.go`
- Test: `internal/groupops/http/handler_test.go`
- Test: `internal/groupops/app/runtime_test.go`

Pass a typed canonical payload from the verified handler to the runtime. Lock the plan, require exact target-subset membership, store its frozen snapshots, and reuse `AcceptAndQueueWithin` in the same UoW. Test same-event same-body replay, same-event payload drift, plan-binding rejection, and no EER creation on validation failure.

### Task 3: Reserve the Media-owned mini-program resolution boundary

**Files:**
- Modify: `internal/media/port/*.go`
- Modify: `internal/media/app/*.go`
- Modify: `cmd/aicrm/group_ops_adapters.go`
- Test: `internal/media/..._test.go`
- Test: `cmd/aicrm/group_ops_*_test.go`

Use existing Media IDs for images/files and existing preparation receipts. The branch implements the audited daily-lesson AppID-plus-path cover resolver only. Any later resolver for another mini-program form requires separate review, a safe localizable image source and canonical card metadata; it must record Media-owned source facts and preparation receipt before the caller can freeze them. It must never follow arbitrary caller URLs or persist raw provider credentials.

### Task 4: Deliver ordered mixed content through the existing Outbound boundary

**Files:**
- Modify: `internal/outbound/group_message.go`
- Modify: `internal/wecom/port/*.go` only if the existing request object cannot represent the approved provider capability
- Test: `internal/outbound/group_message_test.go`

Update the frozen snapshot parser without mutating historical v1 snapshots. Preserve the supported shape: a single optional text block followed by the attachment order. Reject unsupported mixed ordering at the HTTP edge; do not introduce multiple EER intents merely to simulate an ordering the current WeCom API cannot express. No direct Provider call may enter Group Ops or Media.

### Task 5: Publish the contract and run focused verification

**Files:**
- Modify: `api/openapi.yaml`
- Modify: `docs/contracts/groupops-webhook-inbound.md`
- Test: relevant Group Ops, Media, Outbound, and composition integration suites

Document the actual HMAC computation already implemented by the protocol authenticator, JSON schema, upload-then-reference flow, response/error codes, and a `curl` example without a secret. Run Go unit tests, a PostgreSQL integration journey that proves atomic rollback and replay stability, OpenAPI checks, and the existing Group Ops runtime journey with Provider disabled.

## Acceptance evidence

- The production plan named `正式群运营计划测试` is a plan, not a target group; it has ten bindings and may activate as a configured zero-node Webhook plan.
- A valid signed sample addressed to bound reference(s) creates only frozen local run/EER intent facts. No test or deployment sends a real production message.
- Changing a local template/cover after first acceptance cannot change a replayed run's material, targets, or block order; source drift fails before an outbound provider call.
- The audited daily-lesson AppID/path resolves through Media; every other AppID/path fails closed with zero EER intent until a separately approved resolver exists.
