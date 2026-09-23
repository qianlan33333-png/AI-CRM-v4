# AI Assistant review journeys

## Create without effects

1. A trusted internal caller submits canonical Customer IDs, internal Staff IDs, content candidates and a source digest.
2. The service sorts/deduplicates canonical roots and writes the plan, recipients, content versions, receipt, audit and outbox in one UoW.
3. The plan is visible as `pending_review`; no Outbound intent or External Effect exists.

## Review then dispatch

1. An authenticated reviewer edits pending content with an idempotency key and expected version.
2. Individual approve/reject changes review state only.
3. A supervisor obtains a preview digest and confirms the same plan version.
4. One UoW writes approval, Outbound intents, EER acceptance/River jobs, effect bindings, receipt, audit and outbox.
5. Provider calls happen only after commit.

## Unknown outcome

1. A Provider call is attempted and the connection fails after dispatch may have occurred.
2. The effect and recipient become `outcome_unknown`; automatic retry stops.
3. Reconciliation requires the original effect, generation, fence and evidence digest; it never creates a replacement key.

## Identity safety

- External integration facts must be verified and scoped before Identity Resolve.
- `not_found`, `conflict` and invalid scope never become recipients.
- No journey provisions, binds or merges Customer roots.
