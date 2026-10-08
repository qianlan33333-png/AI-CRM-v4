# Product Design QA — invoice title and history

Result: PASS for the approved UI scope on the mounted candidate page. Real WeChat authorization, provider issuance, card insertion and red-letter completion remain separate acceptance items.

## Reference and reproduction scope

Selected user references: `codex-clipboard-30c49d7e-f61a-4f33-b308-9426f4c5599e.jpg` (form), `codex-clipboard-b7241261-6a8b-494f-8299-2af8b6531df8.jpg` (confirmation), `codex-clipboard-36976faa-93cd-4cb7-ac95-4350e01c86dd.jpg` (result), all in `/var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/`. Original references 1140×2616; displayed downscaled 892×2048. Native-app chrome and travel information are reference context, not implemented content.

Approved changes: V4 blue theme and native font stack; latest instruction removes title-type selection entirely and supports organizations only; PDF and WeChat card pack fixed; no email, personal title, travel information, ad or 24-hour promise. Pending acceptance has no misleading issued-success checkmark. Existing V4 order cards and dialog focus controller reused.

Mounted `web/v3/customerOrders.ts` and its CSS at `http://127.0.0.1:7188/my/orders`, bundled from this worktree with esbuild. The private local server uses synthetic orders and receipts and has no Provider connection. This is source UI evidence, not an installed-production claim.

## Rendered checks

Evidence directory: `/Users/qianlan/Documents/Codex/2026-10-08/invoice-title-history-checks/`.

- `form-mobile.png`, `confirm-mobile.png`, `result-mobile.png`, `history-mobile.png`: 390×844 CSS viewport and output pixels (1× screenshot density). Form and result viewed in the same comparison input with their selected reference images. Confirmation compared against the selected reference, then reread after the organization-only change. History compared with existing order-card hierarchy.
- `orders-320.png`, `history-320.png`: 320×740 viewport. Orders, form and history each report document width equal to viewport width after the tab-margin fix.
- Layout: 16px mobile gutter, rounded white cards, simple stacked name/tax-ID inputs, clear amount at 24px, 44px or greater buttons, confirmation bottom sheet. Font family remains V4 system/PingFang/Microsoft YaHei; no substituted image assets.
- Copy: organization name and tax ID first; PDF, server amount and card delivery visible; confirmation includes no title-type row. Result says application submitted/issuing. History separately shows issuing, issued and card delivery.
- Browser operations: fill synthetic company/tax ID, submit opens confirmation; return to edit retains fields and restores submit focus; final confirm shows pending result; view invoices opens owner history; issued entry has authenticated inline-PDF URL; return loads orders. Error/warn console empty.
- Automated mounted journeys cover combined filters, single selection, confirm-before-write, server amount, organization-only controls, legacy awaiting-title recovery, session expiry, duplicate/ambiguous replay and history refresh/PDF state.

## Findings and fixes

P2: amount initially had metadata-sized text; increased emphasis in form and confirmation and inspected updated render.
P2: existing ≤360px order tabs retained -16px margins despite 12px root gutters, causing 4px overflow on each side; matched margins/padding to 12px and independently read width 320/320.

No unresolved P0/P1/P2 UI findings in the tested scope. Physical-phone keyboard, real WeChat OAuth and Provider delivery need device/provider acceptance; browser input/focus tests do not claim a physical keyboard test.
