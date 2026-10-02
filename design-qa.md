**Findings**

- Prior P1: one-by-one input hint wrapper was incorrectly presented as a question border, nesting borders around the textarea. Fixed by giving the real question the same title/required/hint/input structure and padding as all-in-one; the hint wrapper has no border. Both modes retain one textarea border.
- Prior P1: fixed demo navigation and generated screen labels appeared in mobile questionnaire pages. Removed before template mount; actual authenticated definition title now supplies document.title. Neutral initial HTML title is 问卷.
- Prior P1: canceled authorization added a red authorization warning. Removed in the Controller; answers and original submit remain. Every user submit checks the current Owner session; successful official authorization auto-continues once with the original key.

**Visual Truth and Evidence**

- Source: /var/folders/dq/56xlfzsx05zc7vqhbl7lgv6c0000gn/T/codex-clipboard-2ff82c77-ef86-4513-b2e4-c73868aa4736.png (1170×2532, normalized to 390×844, source density3). Browser-owned navigation/status/home areas excluded (content crop y91–822).
- Selected truth is existing all-in-one card/input styling; the user keeps all/one behavior and requests no new design. Pagination/progress/footer differences are intentional.
- Implementation: /tmp/crm-survey-diagnosis-20261002/revision-mobile/one-choice-iab.png and one-text-iab.png; each390×744, CSSviewport390×744, density1 verified before capture. Local visual fixture serves actual built H5/Host/CSS with two reference-content questions, local-only simulated reads; it is not business acceptance.
- Full comparison: /tmp/crm-survey-diagnosis-20261002/revision-mobile/comparison-full.png (780×744), source left / one selected choice right. Same source choice text and selected third option; source all-in-one shows the next card while one-by-one intentionally shows one card.
- Focused comparison: /tmp/crm-survey-diagnosis-20261002/revision-mobile/comparison-input.png (724×230), source left / one textarea right. Same question and synthetic answer text; current right screenshot intentionally has input focus. Its existing blue focus ring and Chromium resize handle differ from the unfocused iOS source; no second wrapper border exists.
- All-in-one local capture: all-iab.png; DOM/title/choice/text interaction inspected. In-app browser later changed viewport on navigation (433×826 observed), so this capture is supplemental and not used for exact scale assertions. Real Host browser journeys independently use390×844 mobile emulation.

**Required Fidelity Surfaces**

- Typography: existing H5 system font and18px title/16px control hierarchy retained; Chinese wrapping aligns in normalized question and text regions. OS antialiasing differs as expected.
- Spacing/layout: shared16px question padding, existing12px card and option radius, hint-before-input hierarchy preserved; one page's progress and previous/next controls are intentional. No fake mobile nav and no viewport-hidden submit control.
- Colors/tokens: existing Survey gray borders, white panels, blue selection/focus and primary action reused. Focus/unfocused states are distinguished.
- Image quality/assets: no new visual assets; native browser chrome is excluded from app content. No generated phone frame or icons introduced.
- Copy/content: real questionnaire title observed in tab, required indicator now retained in one mode. No authorization banner in canceled submit flow, no demo growth header. Next/previous instructions remain relevant only in one mode.

**Interaction Evidence and Limits**

In-app browser: choice selection, next-question, textarea editing, title readback, no console errors in the functioning preview. Initial stale tab sync failed; fresh tab worked. Temporary local fixture404 bug corrected without changing app source. The in-app viewport drift is recorded above.

Actual Host+independent PG16 regression adds assertions for title/no-demo-navigation, outer card border, unbordered hint, cancellation without error or separate login action, and unchanged answer/key replay. Existing full presentation journey covers all/one valid submission,503 retry and double click. Final exact-head receipts are handed off separately. Real WeChat official UI remains Provider-controlled and not validated by synthetic browser.

**Comparison History**

1. User supplied before images show nested borders, fixed growth title/navigation and red auth warning (P1 findings above).
2. Current screenshots opened alongside source in full and focused comparison canvases after fixes. No remaining actionable P0/P1/P2 issue in the requested card/nav/copy scope. The blue focus ring is the existing native interaction state, not extra wrapping.

**Implementation Checklist**

- Shared question card hierarchy and supported/unsupported title preservation implemented.
- Demo navigation removed; real browser title bound to authenticated definition.
- Cancel/error401 handled by original submit flow, no authorization reminder/extra login button.
- Exact-head checks/build/real Host regression and release handoff recorded outside source.

**Follow-up Polish**

No new design polish added. Stability follow-up reuses the same error/retry control for stalled reads or a changed definition and the auth gate's existing action for read retry. Ordinary draft restoration does not add a banner or automatic submission. Actual mobile Host journeys additionally reload a filled questionnaire and recover a gateway failure after a real Owner commit. Real iOS/WeChat title refresh and official consent UI require production business acceptance.

final result: passed
