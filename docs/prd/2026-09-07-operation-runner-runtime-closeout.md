# Operation Runner runtime closeout

## Classification

- **OneID:** not involved. The Owner handles local operation action facts and no customer or external identity.
- **Persistence:** OperationCycle owns request, lease, event, audit, outbox, and the new action execution snapshot in one PostgreSQL Unit of Work.
- **External task endpoint:** Codex is a stateful local endpoint. This change has no CRM queue, new worker framework, customer effect, or production task invocation.

## Frozen compatibility boundary

The read-only donor used OAuth, a nested `claim.request`, `wait_seconds=25`, and
its local Unix completion socket. V3 uses its configured Bearer service token,
flat action claims, and `wait_seconds=0`; it never accepts a browser session or
legacy OAuth credential. Both contracts require an exact managed app-server
handshake and do not invoke `codex exec` or guess a working directory.

## Defects addressed

An initial `StartThread`/`StartTurn` success followed by an event acknowledgement
loss could leave the stored binding incomplete. Re-opening such an action could
create a second external task. A separate gap let the runner form a generic prompt
from a title and live facts, so later strategy/run edits could alter already queued
work. The old Go draft also exposed `Renew` and `Complete` without an installed
control/socket lifecycle that used them.

## Delivered contract

Migration 0104 records the execution DTO and context summary at action creation:
objective, `codex_prompt`, required binding keys, result schema, strategy version,
run revision, and canonical hashes. The Owner inserts that snapshot with the
request in its existing transaction. The HTTPS claim adapter recomputes and
compares both canonical hashes before it can form a local prompt. Claim reads
only that snapshot. An existing
pre-0104 request with no snapshot returns `missing_execution_snapshot`; the
runner records a manual-review terminal state and does not rebuild it from a live
strategy.

The Go runner includes the frozen DTO in the prompt together with exact local
bindings and the configured literal private control-socket path for the `aicrm-operation-cycle-result` command; it does not assume an environment variable is inherited by an already running Codex app-server. It starts only a first
claim. Any recovered action missing a thread or turn, any uncertain Start call,
and any lost thread/turn acknowledgement records `start_outcome_unknown` and
stops. It never starts a replacement action or creates a new idempotency key.

`aicrm-operation-cycle-runner` is the explicit service-manager entrypoint. It
requires HTTPS, a named protected service-token environment variable, absolute
Codex/control paths, an exact Codex version, and explicit binding mappings. It
runs the Unix result consumer, renews the active 60-second lease at a configured
interval below one minute, and handles SIGINT/SIGTERM. The companion result
command has no CRM credential and submits a reviewed aggregate JSON document only
to that socket. Installation and shutdown/recovery constraints are in
`docs/runbooks/operation-cycle-runner.md`.

## Verification boundary

Tests cover flat ServiceToken protocol, stale sockets, initialize rejection,
version mismatch, timeout, hash mismatch, missing snapshot blocking,
Start/acknowledgement uncertainty, socket result submission, active lease
renewal, and immutable PostgreSQL strategy/run-version action snapshots. The
local `codex-cli 0.153.4` read-only generated app-server schema declares the
`initialize` request and the required `initialized` client notification; the
controlled wire test verifies their order and notification form. Tests use local
PostgreSQL, controlled TLS/Unix endpoints, and stubs; they do not create a real
Codex task, contact production, or send a customer message.
