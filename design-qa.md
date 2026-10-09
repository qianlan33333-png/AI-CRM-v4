# Invoice settings design QA

## Result

Passed for this configuration extension. Product Design used the existing rendered V4 configuration page as its source. Actual mounted Go composition, PostgreSQL Config handler, Access session and CSRF were exercised in an isolated local environment. No real Provider write or production credential change was performed by the browser journey.

## Source and matched comparison

Evidence directory: `/Users/qianlan/Documents/Codex/2026-10-09/invoice-admin-settings/`.

- Source: baseline `2217897214dbf859dace6fe1a236e6e352abc1b1`, rendered `/admin/configDetail.html?cat=wechat_pay`, untouched form, APIv3/private key references absent in the isolated fixture.
- Desktop: 1440 × 1000 viewport, DPR 1. Source screenshot `source-desktop.png` (1440 × 1184); final `implementation-desktop-v2.png` (1440 × 2395). Combined `comparison-desktop-v2.png`; common-shell focus `comparison-desktop-focused-v2.png`.
- Mobile: 446 × 959 viewport, DPR 1. Source screenshot `source-mobile.png` (446 × 1542); final `implementation-mobile-v2.png` (446 × 3087). Combined `comparison-mobile-v2.png`; common-shell focus `comparison-mobile-focused-v2.png`; new settings focus `configuration-mobile-focused-v2.png`.
- Full combined and focused comparisons were visually inspected. The page grows vertically for the requested fields; it retains the same shell, card grouping, typography, blue controls, spacing and borders. No image assets are added or changed.

## Corrections and recheck

Initial mobile rendering put explanatory text beside the card heading and squeezed its title (P2). V2 moves explanations below the full-width heading and wraps them. It also removes a promise about refund processing when invoices are disabled that the runtime does not guarantee. Rechecked full and focused desktop/mobile comparisons: no unresolved P0–P2 visual issue.

Fonts remain `PingFang SC`, `HarmonyOS Sans SC`, `Hiragino Sans GB`, `Microsoft YaHei`, then system sans-serif. Final mobile measurement: viewport/scroll width 446/446, DPR 1. Inputs fit their table cells; buttons wrap within the card. Enterprise-only invoice copy and default-tax fallback/override copy match the approved business behavior.

## Actual interactions

Changed the fixture seller name to `隔离验收销售方已保存`, selected 1%, confirmed the card template, refreshed status, saved a draft, ran cross-module validation, published, navigated independently back to the configuration page and read back the values. Unsaved form edits disable card submission even after status refresh. Published values are distinct from the still-running API's startup configuration; card submission remains disabled until exact applied configuration readback. Empty key upload shows a local validation message; no secret is entered or transmitted. Captured `ui-published-isolated.png`, `ui-published-readback-isolated.png` and `ui-published-readback-mobile.png`. Console warnings/errors: none in final operation tab.

## Limits

Real key upload permissions on the installed host, authenticated production readback, and live Tencent card-template writes require separate installed/provider evidence. This local design pass does not assert those outcomes. Actual previous invoice/card delivery is recorded separately in the parent task evidence.
