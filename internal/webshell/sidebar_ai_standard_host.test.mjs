import assert from "node:assert/strict";
import { test } from "node:test";
import { build } from "esbuild";
import { JSDOM } from "jsdom";
import fs from "node:fs";
import { execFileSync } from "node:child_process";

execFileSync(process.execPath, ["scripts/build-sidebar-standard-overlay.mjs"], { stdio: "pipe" });
const overlay = fs.readFileSync("web/dist/sidebar/sidebar_workbench_v3_overlay.js", "utf8");
const host = (await build({ entryPoints: ["web/v3/sidebar/main.ts"], bundle: true, format: "iife", write: false, target: "es2020", logLevel: "silent" })).outputFiles[0].text;
const template = fs.readFileSync("internal/webshell/static/sidebar_workbench/sidebar_customer_workbench_dd8d60d.html", "utf8")
  .replace(/<script[\s\S]*?<\/script>/g, "")
  .replace('id="customer-external-userid"', 'id="customer-oneid"')
  .replace('data-workbench-url=', 'data-overlay-url="/synthetic-overlay.js" data-workbench-url=');
const tick = () => new Promise(resolve => setTimeout(resolve, 5));
async function until(check) { for (let i = 0; i < 80; i++) { if (check()) return; await tick(); } assert.ok(check(), "fixture did not reach expected state"); }

async function fixture() {
  const calls = [], sdk = [];
  let contact = "synthetic-a", unauthorized = false;
  const dom = new JSDOM(template, { url: "https://crm.fixture/sidebar", runScripts: "outside-only", pretendToBeVisual: true });
  const w = dom.window;
  w.AbortController = AbortController;
  w.AbortSignal = AbortSignal;
  w.Headers = Headers;
  w.fetch = async (input, options = {}) => {
    const path = new URL(input, w.location.origin).pathname;
    calls.push({ path, options });
    let body = { items: [], total: 0, offset: 0, limit: 20 }, status = 200;
    const customerID = contact === "synthetic-a" ? 42 : 43;
    if (path === "/api/sidebar/jssdk-config") body = { corp_id: "synthetic", agent_id: "1", config: { timestamp: 1, nonceStr: "fixture", signature: "fixture" }, agent_config: { timestamp: 1, nonceStr: "fixture", signature: "fixture" } };
    if (path === "/api/sidebar/v2/bootstrap") body = { state: "ready", customer_id: customerID, context_token: "fixture-" + customerID, workbench: { profile: { customer_id: customerID, oneid: "CID-" + customerID, display_name: "合成客户" + customerID, phone_masked: "138****5678", phone_assurance: "declared", profile_version: 1 } } };
    if (path === "/api/sidebar/v2/ai-operations") {
      body = { state: "ready", data: { customer_id: customerID, summary: "合成摘要" + customerID, synced_at: "2026-10-09T03:00:00Z", last_customer_message: { type: "text", text: "合成客户末句" + customerID, occurred_at: "2026-10-08T10:12:00Z" } }, snapshot_token: "fixture-snapshot-" + customerID, expires_at: new Date(Date.now() + 300000).toISOString(), generation_ready: true };
      if (unauthorized) { status = 401; body = { error: { code: "invalid_context" } }; }
    }
    if (path.endsWith("/suggestions")) body = { snapshot_token: "fixture-snapshot-" + customerID, suggestion: "合成销售建议" + customerID, auto_sent: false };
    return { ok: status < 400, status, text: async () => JSON.stringify(body) };
  };
  w.wx = { ready: fn => fn(), error: () => {}, config: () => {}, agentConfig: config => config.success(), invoke: (method, _args, callback) => { sdk.push(method); callback({ err_msg: method + ":ok", external_userid: contact }); } };
  w.eval(host);
  await until(() => w.__AICRMSidebarBridge);
  w.eval(overlay);
  try { await until(() => w.document.body.textContent.includes("合成摘要42")); }
  catch (error) { const diagnostic = w.document.body.textContent.trim() + " requests=" + calls.map(call => call.path).join(",") + " sdk=" + sdk.join(","); dom.window.close(); throw new Error(error.message + " " + diagnostic); }
  return { dom, w, calls, sdk, setContact(value) { contact = value; }, denyRead() { unauthorized = true; } };
}
const button = (w, label) => [...w.document.querySelectorAll("button")].find(node => node.textContent.trim() === label);

test("production Host and generated overlay expose confirmed manual-only AI operations", async () => {
  const f = await fixture();
  try {
    assert.equal(f.w.document.querySelector('#tabs [data-tab="profile"]').textContent, "AI运营");
    assert.equal(button(f.w, "用户时间线"), undefined);
    assert.equal(f.calls.some(call => call.path.endsWith("/timeline") || call.path.endsWith("/suggestions")), false);
    button(f.w, "辅助成交").click();
    assert.ok(f.w.document.body.textContent.includes("合成客户末句42"));
    assert.equal(button(f.w, "生成话术").disabled, true);
    const check = f.w.document.querySelector(".ai-confirm input");
    check.checked = true; check.dispatchEvent(new f.w.Event("change"));
    button(f.w, "生成话术").click();
    await until(() => f.w.document.body.textContent.includes("合成销售建议42"));
    const generated = f.calls.filter(call => call.path.endsWith("/suggestions"));
    assert.equal(generated.length, 1);
    assert.equal(generated[0].options.headers["X-Sidebar-Context-Token"], "fixture-42");
    assert.deepEqual(JSON.parse(generated[0].options.body), { snapshot_token: "fixture-snapshot-42", confirmed: true });
    assert.equal(f.sdk.includes("sendChatMessage"), false);
    button(f.w, "问卷").click();
    await until(() => f.w.document.body.textContent.includes("暂无问卷记录"));
    assert.equal(f.w.document.body.textContent.includes("合成销售建议42"), false);
  } finally { f.dom.window.close(); }
});

test("current contact changes without focus refuse generation and replace the old projection", async () => {
  const f = await fixture();
  try {
    button(f.w, "辅助成交").click();
    const check = f.w.document.querySelector(".ai-confirm input");
    check.checked = true; check.dispatchEvent(new f.w.Event("change"));
    f.setContact("synthetic-b");
    button(f.w, "生成话术").click();
    await until(() => f.w.document.body.textContent.includes("合成摘要43"));
    assert.equal(f.calls.some(call => call.path.endsWith("/suggestions")), false);
    assert.equal(f.w.document.body.textContent.includes("合成客户末句42"), false);
    assert.equal(f.w.document.querySelector("#customer-oneid").textContent, "OneID CID-43");
  } finally { f.dom.window.close(); }
});

test("401 on AI read clears the Host scope without disposal replacing the recovery surface", async () => {
  const f = await fixture();
  try {
    f.denyRead();
    button(f.w, "刷新").click();
    await until(() => f.w.document.body.textContent.includes("重新打开侧边栏"));
    assert.equal(f.w.__AICRMSidebarBridge.contextIdentity(), "");
    assert.equal(f.w.document.body.textContent.includes("合成摘要42"), false);
    assert.equal(button(f.w, "生成话术"), undefined);
  } finally { f.dom.window.close(); }
});
