# Referral settings design QA

final result: blocked

Source: user /Users/qianlan/Downloads/1.png and retained CRM template reference.
Implementation: http://127.0.0.1:4189/admin/referral/settings, actual webshell renderer and built Referral assets with read-only local API fixtures. Chrome native UI inspected on 2026-09-19. Screenshot /tmp/referral-settings-preview.png.

Fixed during visual verification: settings failed to mount because shared product selector script was not loaded; large participation dropdown stretched to adjacent picker height; form controls moved out of page header. Re-capture confirms complete-page route, persistent CRM navigation, four white setting sections, blue numbered headings, page-header actions and non-stretched product controls.

Source and implementation were emitted together for comparison twice. Current desktop viewport is 1051x768 including browser chrome, source is 1487x1058. Exact viewport/state comparison and mobile verification remain open, so full design QA is not marked passed. Existing cover URL field is retained; image upload, radio/switch styling and chosen-product thumbnail do not yet reproduce the reference. Do not report pixel fidelity or full Product Design completion.

Functional evidence: PostgreSQL and JSDOM tests are recorded in the PRD. Local visual fixtures are not production readback or purchase acceptance.

2026-09-20 follow-up: Chrome native capture remained unavailable after the user reopened the preview. The in-app browser could load and interact with the settings page, including selecting product-purchase mode. Its viewport screenshot had scaling/blur/duplicated paint artifacts, including after resetting through the supported viewport/CDP controls. DOM interaction evidence does not substitute for reliable visual comparison. Visual QA therefore remains blocked. Active product controls are now disabled as a fieldset so asynchronous directory completion cannot re-enable clearing the locked product.

## 2026-09-26 支付宝原付款页修复

本次付款蒙版及成功反馈范围：passed。Product Design 已按 360/390/430px 实际浏览器截图及原页自动成功行为验收。详见 [验收记录](docs/qa/2026-09-26-alipay-checkout-design.md)。上方历史 referral QA 状态不代表本次付款页面。生产实付证据待发布后取得。
