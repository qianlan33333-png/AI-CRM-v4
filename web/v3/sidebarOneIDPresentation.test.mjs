import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const repository = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const dist = path.join(repository, "web", "dist");
const sidebarDocumentPath = path.join(dist, "sidebar", "index.html");

if (!fs.existsSync(sidebarDocumentPath)) {
  throw new Error("sidebar OneID presentation requires the normal npm build output at web/dist/sidebar/index.html");
}

const template = fs.readFileSync(sidebarDocumentPath, "utf8");
const manifest = JSON.parse(fs.readFileSync(path.join(dist, "asset-manifest.json"), "utf8"));
const documentDOM = new JSDOM(template, { url: "https://sidebar.test.invalid/sidebar/index.html" });
const documentRoot = documentDOM.window.document.querySelector("#sidebar-workbench-root");
if (!documentRoot) throw new Error("built sidebar document does not contain its root");
const overlayURL = documentRoot.dataset.overlayUrl;
const hostURL = [...documentDOM.window.document.querySelectorAll('script[type="module"][src]')]
  .map((script) => script.getAttribute("src"))
  .find((src) => src === `../${manifest.entries.sidebarHost}`);
documentDOM.window.close();

function readBuiltSidebarAsset(relativeURL, label) {
  if (!relativeURL) throw new Error(`built sidebar document is missing ${label}`);
  const url = new URL(relativeURL, "https://sidebar.test.invalid/sidebar/index.html");
  const assetPath = path.resolve(dist, `.${url.pathname}`);
  const relativePath = path.relative(dist, assetPath);
  if (relativePath.startsWith("..") || path.isAbsolute(relativePath) || !fs.existsSync(assetPath)) {
    throw new Error(`built sidebar ${label} is not a release asset: ${relativeURL}`);
  }
  return fs.readFileSync(assetPath, "utf8");
}

assert.equal(template.includes('id="customer-oneid"'), true, "normal build must mount the canonical OneID field");
assert.equal(template.includes("customer-external-userid"), false, "normal build must not release a raw external identifier field");
assert.equal(overlayURL, `../${manifest.entries.sidebarStandardOverlay}`, "built sidebar must reference its manifest Overlay");
const overlay = readBuiltSidebarAsset(overlayURL, "Overlay");
const releasedHost = readBuiltSidebarAsset(hostURL, "trusted Host");
assert.match(releasedHost, /aicrm-sidebar-context-ready/, "built sidebar Host must notify the Overlay when a trusted context is ready");
assert.match(releasedHost, /getCurExternalContact/, "built sidebar Host must retain the WeCom current-contact verification boundary");

function deferred() {
  let resolve;
  return { promise: new Promise((done) => { resolve = done; }), resolve };
}

async function until(check, message) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
  assert.fail(message);
}

const oldWorkbench = deferred();
const freshWorkbench = deferred();
let requestCount = 0;
let context = "first";
const dom = new JSDOM(template, {
  url: "https://sidebar.test.invalid/sidebar",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const bridge = {
  async start() {},
  async retry() { context = "second"; },
  contextIdentity() { return context; },
  request(path) {
    const url = new URL(path);
    assert.equal(url.pathname, "/api/sidebar/v2/workbench", "overlay reads only the Host workbench projection");
    requestCount += 1;
    return requestCount === 1 ? oldWorkbench.promise : freshWorkbench.promise;
  },
};
dom.window.__AICRMSidebarBridge = bridge;
dom.window.eval(overlay);

await until(() => requestCount === 1, "initial trusted workbench request did not start");
dom.window.dispatchEvent(new dom.window.CustomEvent("aicrm-sidebar-context-invalidated"));
assert.equal(dom.window.document.querySelector("#customer-oneid").textContent, "", "context invalidation immediately clears a displayed OneID");
dom.window.dispatchEvent(new dom.window.CustomEvent("aicrm-sidebar-context-retry-requested"));
await until(() => requestCount === 2, "retry did not create a fresh trusted workbench request");
freshWorkbench.resolve({ customer: { display_name: "新客户", oneid: "CID-52" }, profile: {}, workflow: {} });
await until(() => dom.window.document.querySelector("#customer-oneid").textContent === "OneID CID-52", "ready canonical OneID did not render");

oldWorkbench.resolve({ customer: { display_name: "旧客户", oneid: "CID-41" }, profile: {}, workflow: {} });
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(dom.window.document.querySelector("#customer-oneid").textContent, "OneID CID-52", "a late old workbench response must not restore a former OneID");

dom.window.close();

const invalidDOM = new JSDOM(template, {
  url: "https://sidebar.test.invalid/sidebar",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
invalidDOM.window.__AICRMSidebarBridge = {
  async start() {},
  contextIdentity() { return "invalid-oneid"; },
  async request() { return { customer: { display_name: "当前客户", oneid: "external-user-id" }, profile: {}, workflow: {} }; },
};
invalidDOM.window.eval(overlay);
await until(() => invalidDOM.window.document.querySelector("#customer-name").textContent === "当前客户", "invalid OneID fixture did not finish loading");
assert.equal(invalidDOM.window.document.querySelector("#customer-oneid").textContent, "", "an invalid or raw external identifier must not render as OneID");
invalidDOM.window.close();

const host = await buildTestBrowserBundle("web/v3/sidebar/main.ts");
let actualContact = "external-7";
const actualDOM = new JSDOM(template, {
  url: "https://sidebar.test.invalid/sidebar",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === "string" ? input : input.url, "https://sidebar.test.invalid");
      if (url.pathname === "/api/sidebar/jssdk-config") return new Response(JSON.stringify({ corp_id: "test-corp", agent_id: "test-agent", config: { timestamp: 1, nonceStr: "regular", signature: "regular" }, agent_config: { timestamp: 1, nonceStr: "agent", signature: "agent" } }), { headers: { "Content-Type": "application/json" } });
      if (url.pathname === "/api/sidebar/v2/bootstrap") {
        const customerID = actualContact === "external-8" ? 8 : 7;
        return new Response(JSON.stringify({ state: "ready", customer_id: customerID, context_token: `context-${customerID}`, workbench: { profile: { customer_id: customerID, oneid: `CID-${customerID}`, display_name: `客户${customerID}` } } }), { headers: { "Content-Type": "application/json" } });
      }
      return new Response(JSON.stringify({ code: "unexpected" }), { status: 500, headers: { "Content-Type": "application/json" } });
    };
    let ready;
    window.wx = {
      config() { queueMicrotask(() => ready?.()); },
      ready(callback) { ready = callback; },
      error() {},
      agentConfig(options) { queueMicrotask(() => options.success?.()); },
      invoke(method, _payload, callback) {
        queueMicrotask(() => callback(method === "getCurExternalContact" ? { err_msg: "getCurExternalContact:ok", external_userid: actualContact } : { err_msg: `${method}:ok` }));
      },
    };
  },
});
actualDOM.window.eval(host);
actualDOM.window.document.dispatchEvent(new actualDOM.window.Event("DOMContentLoaded"));
actualDOM.window.eval(overlay);
await until(() => actualDOM.window.document.querySelector("#customer-oneid").textContent === "OneID CID-7", "actual Host initial OneID did not render");
actualContact = "external-8";
actualDOM.window.dispatchEvent(new actualDOM.window.Event("focus"));
await until(() => actualDOM.window.document.querySelector("#customer-oneid").textContent === "OneID CID-8", "actual focus revalidation did not reload the OneID for the new contact");
actualDOM.window.close();

console.log("sidebar OneID presentation and context race: PASS");
