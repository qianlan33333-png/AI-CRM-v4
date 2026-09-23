# AI Assistant donor behavior contract

Baseline: `AI-CRM@dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f`.

## Included behavior

- Plan list with keyword/status filtering, four summary cards, refresh and detail navigation.
- Plan detail with metadata, whole-plan approve/reject, 50-recipient paging and recipient drawer.
- Recipient approve/reject, message cards, content editing, material selection and previews.
- Loading, empty, disabled, rejected, partial, approved and execution-result states.

## Required v3 adaptations

- Preserve DOM hierarchy, visual tokens, labels and interaction order unless `frontend-audit.md` explicitly permits a difference.
- Replace raw external identities with OneID-safe display fields.
- Derive actor from authenticated v3 context; ignore donor operator/action-token fields.
- Individual approval is review-only. Whole-plan confirmation creates Outbound/EER effects.
- Never map `provider_accepted` to donor `sent`; only trusted `delivery_proven` evidence may render the donor sent state.
- Do not expose Campaign or Observability routes from this module.

Donor SQL, identity matching, `broadcast_jobs`, `external_effect_job`, workers and Provider adapters are evidence only and are not migrated.
