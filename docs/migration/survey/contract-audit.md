# Survey donor contract audit

The Survey behavior donor is `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`. It is read-only and is never a Go module, submodule, runtime service, migration source, or normal data source for v3.

## Development classification

```text
OneID: resolves identity / provisions customer from verified Provider fact / reads canonical customer
Persistence: local transaction + internal durable job + Provider read + Provider write/external effect
```

Survey owns definitions, immutable versions, submissions, answer snapshots, assessment results, operation configuration, effect bindings, and migration receipts. Identity owns channel identity and Customer linkage. Outbound owns Provider writes. External Effects owns acceptance, attempts, retry classification, outcome-unknown handling, and reconciliation.

## Source disposition

| Source | Disposition | Rule |
|---|---|---|
| Questionnaire editor, list, operations, unresolved-history and sidebar frontend | BEHAVIOR | Preserve visible behavior through v3 adapters; 17 direct files are hash-frozen. |
| The 36 Survey/questionnaire OpenAPI paths | BEHAVIOR | Preserve route intent and envelope compatibility; enforce v3 auth, CSRF, OneID and effect semantics. |
| Survey value objects and validation vectors | BEHAVIOR | Reimplement as pure v3 domain code, including assessment support absent from v2. |
| Customer-answer and operation interfaces | PORT | Re-express as stable v3 ports with canonical `customers.id`. |
| PostgreSQL queries | BEHAVIOR | Use only as query and edge-case references; v3 owns a fresh schema. |
| v2 app/store/http packages | ADAPTER REFERENCE | Do not import or bulk-copy. Implement against v3 UoW and composition root. |
| v2 identities, queues, workers, providers and migrations | DISCARD | Replaced by Identity, platform/jobqueue, Outbound, External Effects and v3 migrations. |

## Frozen route inventory

The donor exposes 36 route shapes: three unresolved-history routes, one sidebar route, two customer-history routes, three public routes, three public lifecycle/analytics routes, two H5 OAuth routes, two submission effect routes, nine admin definition routes, three UI routes, five operations/log routes, and three analysis/export routes. The executable contract is the pinned donor OpenAPI plus characterization tests; generated clients are not evidence that the v3 backend exists.

## Explicit gaps to close

- v2 rejects assessment/F02 despite editor support; v3 must implement scoring and result ranges.
- v2 public submissions are selection-only and anonymous; v3 must support all four question types and verified H5 OneID sessions.
- v2 local push tests do not prove Provider execution; v3 must use Outbound and External Effects while production remains disabled.
- Historical answers whose current definition row is missing remain visible through immutable answer snapshots.
- Historical identities that cannot be safely resolved remain unlinked; no legacy identity value provisions a Customer.

## Migration boundary

The production source is read through one repeatable-read snapshot. Every in-snapshot questionnaire row receives an imported or unresolved receipt. External-push and SCRM history keeps only questionnaire/submission reference, result status, attempt count, timestamps and safe failure classification. URL, request payload, response payload and raw legacy identity are neither imported nor archived by this project.
