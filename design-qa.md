# Paid referral sharing QA

Source visual truth: `/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-3eed7d5a-4c5b-4547-b32d-a749103d7657.jpg`, current `web/v3/referral.css`, published campaign 5 poster and QR layout. Implementation captures: `/Users/qianlan/Documents/Codex/2026-10-10/referral-paid-fix/mobile-ranking.jpg` and `mobile-poster.jpg`.

Viewport: 360 × 715 CSS pixels. Source original 1080 × 2400 (3x Android capture including roughly 255px status/browser header); compare its application region at width 360 and about 715px height. Implementation screenshot 360 × 715 (1x). Browser chrome is excluded. Source attachment display was resized to 922 × 2048, which is not a separate design change.

State: active paid campaign, participant with one valid paid invite; participant has no distributor/receiver prerequisite. Local browser uses the exact changed referralCenter bundle and unchanged stylesheet, current published poster 1250 × 2426 with QR region x830/y1952/size330, and synthetic authenticated display/API responses. This visual test is separate from PostgreSQL actual composed Order checkout/fanout tests and installed-artifact acceptance.

Full-view comparison: retains title, paired personal statistics, rounded leaderboard card, individual/period controls, fixed remaining-time/invite footer and warm gradient. Expected content changes are score 1/rank 1, populated ranking and removal of failed-invite message. Existing desktop browser font/color rendering versus Android and the current stylesheet's warmer hue are retained; this fix does not redesign tokens.

Focused comparison: statistics and leaderboard text/controls remain readable at 360px; populated row and my-rank affordance fit; poster has original full artwork and composed invitation QR in its configured rectangle. The modal footer (copy, download, close) fits in the viewport. No horizontal overflow (clientWidth=scrollWidth=360).

Required fidelity surfaces:
- Fonts/typography: current system Chinese font stack, heading/score weights and wrapping preserved. Browser/platform rasterization differs as expected.
- Spacing/layout rhythm: unchanged cards, radii, margins and footer. Poster fills the existing contain region without cropping; open dialog count stays 1 after reopening.
- Colors/tokens: unchanged stylesheet; original warm campaign theme retained. No newly introduced palette or contrast finding.
- Image/asset quality: actual campaign poster reused, original 1250x2426 artwork remains sharp and QR is composed into the saved layout. Existing fallback avatar shown only because the local synthetic user has no photo.
- Copy/content: public Chinese activity wording retained; explicit score/rank state and successful copy feedback appear. No registration/receiver step is shown.

Interaction evidence: invite opens composed PNG; close restores leaderboard; re-open produces one modal and current poster; copy produces “话术已复制”. Console warn/error list empty. Poster switching, stale image, exact copy, download and fallback paths pass the focused frontend contract tests.

Findings: no actionable P0/P1/P2 findings. Comparison history: one rendered review, no visual code changes required. Residual gap: no real mobile WeChat authorization, scan or payment Provider invoked by this local QA.

Implementation checklist: complete backend and frontend selected checks; submit exact domestic ref to release workbench; verify installed journey and production readback separately.

final result: passed
