# Chromium Journey Readiness

## Business decision

The affected journeys must exercise the existing user-visible controls only
after the current Host has installed the relevant picker and the current
rendered control is reachable. This prevents a test from reporting a product
failure when a stale DOM node or a partially initialized Host was clicked.

## Scope

- Distribution recovery: activate the current actionable recovery control in
  the same settled DOM turn that verifies its geometry and hit target.
- Channel entry tags: wait for the existing V3 tag-picker capability before
  clicking the existing form control, then fail explicitly if its existing
  not-ready state is rendered.
- Owner handoff: wait for the alias Host and V3 staff-picker capability, and
  record the actual authorised `include_inactive=true` directory response
  before asserting the inactive source option.

No runtime Host, handler, role, request, persistence, Provider, or business
assertion changes are in scope.

## Architecture classification

OneID: not involved. The journeys retain existing authorised local-directory
reads and do not resolve, provision, or link customer identity.

Persistence: stateless test-harness changes. Existing product, channel,
distribution, and owner-handoff save/readback assertions remain owned by their
current commands and stores.

External Effects: not involved. The tests introduce no Provider read or write,
queue, retry, or reconciliation behavior.

## Frontend consistency

The component map identifies the existing channel and picker surfaces; this PR
reuses their current V3 Host contracts and adds no page component or shell.
Product Design catalog was unavailable in this session, so no Product Design
route was run. GitHub code search found no direct public repository case for
the two readiness patterns; the implementation follows the existing local CDP
journey and shared-picker contracts.

## Acceptance

1. Recovery still opens the existing confirmation dialog for the intended
   exception and retains all owned request/idempotency assertions.
2. Channel selection opens the existing V3 picker only after its capability is
   ready; an existing not-ready error remains a failure.
3. Owner handoff proves the alias Host is ready, receives a successful
   `include_inactive=true` directory response, and renders the inactive source
   before selection.
4. Chromium journey tests exercise the real composed pages; no timeout is
   increased solely to mask readiness races.
