# Survey behavior contract

Baseline: `AI-CRM-v2@6bfbe5816bb89913c70adaca87d6a486260e016e`.

This document freezes user-observable behavior before the v3 backend is introduced. Compatibility means the same intent, validation outcome and response envelope where safe; it does not permit legacy identity matching, database ownership, Provider execution, or status overstatement.

## Route inventory (36)

| Area | Method family | Path |
|---|---|---|
| unresolved history | GET | `/api/admin/survey-history/submissions` |
| unresolved history | GET | `/api/admin/survey-history/submissions/{history_id}` |
| unresolved history | GET | `/api/admin/survey-history/submissions/{history_id}/answers` |
| sidebar | GET | `/api/sidebar/v2/questionnaires` |
| customer history | GET | `/api/v1/customers/{customer_id}/survey-answers` |
| public | GET | `/api/public/questionnaires/{slug}` |
| public | POST | `/api/public/questionnaires/{slug}/submissions` |
| public | POST | `/api/public/survey-submission-results/query` |
| publish | POST | `/api/admin/questionnaires/{questionnaire_id}/public-publish` |
| publish | POST | `/api/admin/questionnaires/{questionnaire_id}/public-disable` |
| analytics | GET | `/api/admin/questionnaires/{questionnaire_id}/public-analytics` |
| H5 identity | GET | `/api/h5/surveys/oauth/start` |
| H5 identity | GET | `/api/h5/surveys/oauth/callback` |
| effect | POST | `/api/admin/questionnaires/{questionnaire_id}/submissions/{submission_id}/external-push` |
| effect | POST | `/api/admin/questionnaires/{questionnaire_id}/submissions/{submission_id}/external-push/reconcile` |
| admin UI | GET | `/admin/questionnaires` |
| admin UI | GET | `/admin/questionnaires/ui` |
| admin | GET | `/api/admin/questionnaires/preflight` |
| admin | GET, POST | `/api/admin/questionnaires` |
| admin | GET, PUT, DELETE | `/api/admin/questionnaires/{questionnaire_id}` |
| admin | POST | `/api/admin/questionnaires/{questionnaire_id}/duplicate` |
| admin | POST | `/api/admin/questionnaires/{questionnaire_id}/disable` |
| admin | POST | `/api/admin/questionnaires/{questionnaire_id}/enable` |
| results | GET | `/api/admin/questionnaires/{questionnaire_id}/results` |
| operations UI | GET | `/admin/questionnaires/external-push-logs` |
| operations UI | GET | `/admin/questionnaires/{questionnaire_id}/external-push-logs` |
| operations UI | GET | `/admin/questionnaires/{questionnaire_id}/operations` |
| operations | GET | `/api/admin/questionnaires/{questionnaire_id}/operations` |
| operations | PUT | `/api/admin/questionnaires/{questionnaire_id}/operations/completion` |
| operations | PUT | `/api/admin/questionnaires/{questionnaire_id}/operations/external-push` |
| operations | POST | `/api/admin/questionnaires/{questionnaire_id}/operations/external-push/test` |
| analysis | GET | `/api/admin/questionnaires/{questionnaire_id}/analysis` |
| export | GET | `/api/admin/questionnaires/{questionnaire_id}/export/preview` |
| submissions | GET | `/api/admin/questionnaires/{questionnaire_id}/submissions` |
| export | GET | `/api/admin/questionnaires/{questionnaire_id}/export` |
| customer profile | GET | `/api/admin/customers/profile/questionnaire-answers` |

## Admin journeys

- List supports search, status filtering, pagination and accurate total.
- Create and update preserve name, title, description, slug, display mode, disabled state, four question types, ordered options and score rules.
- Duplicate produces a distinct disabled draft. Enable and disable preserve history. Hard delete is allowed only for an unreferenced draft.
- Editor exposes ordinary and assessment modes. Assessment dimensions, types, score levels, total levels, strengths, weaknesses and final recommendation are editable.
- Every admin write requires an authenticated principal, same-origin CSRF and an actor-scoped idempotency key. Version drift is a conflict, not last-write-wins.

## Public journeys

- A published, enabled slug returns an immutable public version; draft/admin-only fields never appear.
- `all_in_one` and `one_by_one` displays are preserved.
- v3 extends the donor selection contract to `textarea` and `mobile` answers and to assessment results.
- Submission replay with the same key and payload returns the original receipt; payload drift conflicts.
- Result lookup accepts the token in a POST body. Logs and response metadata never echo it.
- Anonymous submission remains valid. A Customer is attached only from a trusted H5 session resolved through OneID.

## Results, history and export

- Historical answers render from their submission snapshot even if the current definition row is absent.
- Default analysis, sidebar and export preview omit raw external identities, result tokens, mobile and free text.
- Sensitive CSV requires separate permission, CSRF, `Cache-Control: no-store`, audit and a fixed column allowlist.
- Unresolved identity is visible as a reasoned state and never guessed into a Customer.

## Operations and effects

- Survey stores opaque configuration references only; URLs and credentials are rejected.
- The established operations page writes completion first and then writes only the external-push toggle/reference. That narrow legacy PUT preserves externally configured metadata. Metadata editors must send the readback `configuration_version`; a drift returns `configuration_conflict` and commits nothing.
- Accepted and queued are not Provider success. Executed is not delivery proof.
- `outcome_unknown` can only reconcile the original effect identity.
- Historical push/SCRM results are read-only and can never create a queue job.
- Production Provider execution remains disabled until separate approval.

## Stable error families

`authentication_required`, `permission_denied`, `csrf_invalid`, `invalid_request`, `questionnaire_not_found`, `definition_version_conflict`, `questionnaire_disabled`, `invalid_schema`, `invalid_answer`, `submission_payload_conflict`, `rate_limited`, `identity_pending`, `identity_conflict`, `provider_disabled`, `outcome_unknown`, `migration_source_drift`, and `migration_reconciliation_failed`.
