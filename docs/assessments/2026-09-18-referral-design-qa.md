# Referral activity detail design QA — 2026-09-18

Reference: `/Users/qianlan/.codex/generated_images/01a09618-1ae2-7fa3-bfa3-bd39b50766c1/exec-1745dc9b-3d26-4122-a8a4-deaf0ed35efe.png`.

Evidence was captured from the real composed HTTPS Host and isolated PostgreSQL 16 fixture by `TestPostgreSQLReferralChromiumJourney`:

- `/tmp/referral-dashboard-review/admin-1440.png` — level-one activity list.
- `/tmp/referral-dashboard-review/admin-detail-1440.png` — default activity detail.
- `/tmp/referral-dashboard-review/admin-teams-1440.png` — team metrics, member drilldown and captain-entry actions.
- `/tmp/referral-dashboard-review/admin-member-drilldown-1440.png` — a named member's direct invite detail.
- `/tmp/referral-dashboard-review/admin-captain-entry-1440.png` — the shared QR dialog for the captain-owned campaign entry.
- `/tmp/referral-dashboard-review/mobile-375.png` and `mobile-430.png` — member-facing campaign.

The final employee detail retains the CRM shell, blue-and-white surface, horizontal secondary navigation, and a data-first hierarchy from the reference. The selected direction deliberately replaces its chart/ranking-heavy body with four real metrics, a compact daily data list, and a real member list: the approved product requirement calls for a minimal core dashboard with operational records directly below it. The activity title, lifecycle state, Beijing time window, and actions stay in the detail header; numeric metrics use 32px semibold weight for scanability.

Verified interactions: detail opens from the level-one list; members include a zero-invitation participant; team, participant and score columns remain readable; the team action applies its team filter; a named member drilldown reaches only that member's direct invitations; and the current-filter export remains participation-scoped. The Host fixture creates 53 real trusted-session participations and verifies “加载更多” reaches the later records. The captain QR points only to the same-origin `/referral?campaign=…` login/participation page; it never lets an administrator accept on the captain's behalf.
