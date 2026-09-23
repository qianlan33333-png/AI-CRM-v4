# AI Assistant frontend audit

| Surface | Classification | v3 treatment |
|---|---|---|
| Review template and styles | exact | Byte-frozen and rendered through Host Adapter |
| Review JavaScript | mixed | Frozen evidence; adapter replaces data/security calls |
| Composer, readonly detail and material picker | exact/mixed | Preserve UX; use v3 Media refs/digests |
| Raw `external_userid` and `owner_userid` | rejected | Render OneID and safe display names |
| Client operator/action token | rejected | Server derives actor from Session/RBAC/CSRF |
| Donor queue calls | rejected | Whole-plan approval uses Outbound/EER |
| Campaign and Observability links | excluded | No capability added |

Allowed adapter differences are limited to Jinja/shell bootstrap, generated API DTOs, OneID-safe fields, Session/CSRF/idempotency/version headers and honest effect status labels. Any other difference requires a manifest update and visual review.
