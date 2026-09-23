import assert from "node:assert/strict";
import crypto from "node:crypto";
import fs from "node:fs";
import { execFileSync } from "node:child_process";

// This test reads the generated dd8 overlay, rather than the retired V3
// renderer. Browser coverage separately executes the same hashed artifact.
execFileSync(process.execPath, ["scripts/build-sidebar-standard-overlay.mjs"], { stdio: "inherit" });
const overlay = fs.readFileSync("web/dist/sidebar/sidebar_workbench_v3_overlay.js", "utf8");
const bridge = fs.readFileSync("web/v3/sidebar/main.ts", "utf8");
const imageLoader = fs.readFileSync("web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js");
assert.equal(crypto.createHash("sha256").update(imageLoader).digest("hex"), "38090abd86d19b7027841e7035bb8e8b12548487914a98a893fd71a5ec51187d", "standard image loader must retain audited dd8 bytes");
assert.match(imageLoader.toString("utf8"), /createPager:\s*createPager/, "standard image loader must provide the frozen pager");

for (const renderer of ["renderProfile", "renderQuestionnaires", "renderProducts", "renderOrders", "renderCoupons", "renderMaterials", "renderRadarLinks"]) {
  assert.match(overlay, new RegExp(`function ${renderer}\\(`), `${renderer} must remain donor-rendered`);
}
for (const removed of ["other_staff_messages", "其他客服聊天", "chat_activity", "other-staff-messages", "/api/sidebar/v2/other-staff-messages", "sessionStorage", "sidebar_oauth", "sidebar_owner_token"]) {
  assert.equal(overlay.includes(removed), false, `generated overlay retained retired capability: ${removed}`);
}
for (const contract of ["window.__AICRMSidebarBridge", "bridge.request(url, options || {})", "await bridge.start()", "__AICRMSidebarBridge.send"]) {
  assert.equal(overlay.includes(contract), true, `generated overlay lacks trusted bridge contract: ${contract}`);
}
assert.match(overlay, /customer-oneid/, "generated overlay must render the OneID field");
assert.match(overlay, /\^CID-\[1-9\]\[0-9\]\*\$/, "generated overlay must reject non-canonical OneID values");
assert.match(bridge, /const oneID = canonicalOneID\(profile\.oneid\)/, "Host must retain only a validated backend OneID from the ready workbench");
assert.match(bridge, /oneid: this\.oneID/, "Host must pass the ready-context OneID to the overlay");
assert.match(bridge, /customer_number: profile\.customer_number/, "Host must pass the persisted public number separately from identity validation");
assert.match(overlay, /用户编号/, "overlay must display the persisted user number");
assert.match(overlay, /customer\.customer_number/, "overlay must read the same public number supplied by the Host");
assert.equal(overlay.includes("window.fetch ="), false, "overlay must not monkey-patch global fetch");
assert.equal(overlay.includes("getCurExternalContact"), false, "overlay must not own WeCom identity lookup");
assert.equal(overlay.includes("sendChatMessage"), false, "overlay must not invoke WeCom directly");
assert.equal(bridge.includes("sidebarApi"), false, "Host must not revive the retired V3 sidebar renderer");
for (const contract of ["getCurExternalContact", "sendChatMessage", "X-Sidebar-Context-Token", "outcome_unknown"]) {
  assert.equal(bridge.includes(contract), true, `Host lacks required trusted boundary: ${contract}`);
}
console.log("sidebar standard overlay bridge contract: ok");
