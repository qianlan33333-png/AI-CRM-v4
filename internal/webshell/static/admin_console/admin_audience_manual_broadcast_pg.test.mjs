import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const baseURL = process.env.AICRM_RUNTIME_TEST_URL;
const packageID = Number(process.env.AICRM_RUNTIME_TEST_PACKAGE_ID);
if (!/^https?:\/\//.test(baseURL || "") || !Number.isSafeInteger(packageID) || packageID < 1) throw new Error("AICRM runtime test server is required");
const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience_detail.html"), "utf8")
  .replace(/^\{\{define "admin_audience_detail"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const detail = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const wait = (milliseconds = 25) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const waitFor = async (label, ready, failed) => {
  const deadline = Date.now() + 8000;
  for (;;) {
    const failure = failed?.();
    if (failure) throw new Error(`${label} failed: ${failure}`);
    if (ready()) return;
    if (Date.now() >= deadline) throw new Error(`${label} did not become ready`);
    await wait();
  }
};
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: `${baseURL}/admin/automation-conversion/packages/${packageID}`,
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === `/api/admin/ai-audience/packages/${packageID}/broadcast-previews` || url.pathname === `/api/admin/ai-audience/packages/${packageID}/runs` || url.pathname === "/api/admin/automation-runs") return globalThis.fetch(url, init);
      if (url.pathname === `/api/admin/ai-audience/packages/${packageID}`) return json({ package: { id: packageID, name: "PG audience", version: 4, lifecycle: "paused" } });
      if (url.pathname === "/api/admin/ai-audience/package-groups") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/templates") return json({ items: [] });
      if (url.pathname === `/api/admin/ai-audience/packages/${packageID}/configuration` || url.pathname === `/api/admin/ai-audience/packages/${packageID}/automation-binding` || url.pathname === `/api/admin/ai-audience/packages/${packageID}/senders` || url.pathname === `/api/admin/ai-audience/packages/${packageID}/members`) return json({ error: "not_found" }, 404);
      if (url.pathname === "/api/admin/automation-agents") return json({ items: [] });
      if (url.pathname === `/api/admin/ai-audience/packages/${packageID}/precheck`) return json({ precheck: { ready: true, reasons: [] } });
      throw new Error(`unexpected original-detail request ${url.pathname}`);
    };
  },
});
dom.window.eval(detail);
const document = dom.window.document;
const statusFailure = (node) => node?.dataset.state === "error" ? node.textContent.trim() || "the page reported an error" : "";
const capability = document.querySelector("#capabilityStatus");
await waitFor("original detail bootstrap", () => capability?.dataset.capabilityState === "ready", () => {
  const state = capability?.dataset.capabilityState;
  return state && state !== "loading" && state !== "ready" ? capability.textContent.trim() || `capability state ${state}` : "";
});
const previewState = document.querySelector("#broadcastPreviewState");
const confirm = document.querySelector("#broadcastConfirmBtn");
document.querySelector("#broadcastPreviewBtn").click();
await waitFor("PostgreSQL broadcast preview", () => previewState?.dataset.state === "success" && !confirm.disabled, () => statusFailure(previewState));
confirm.click();
await waitFor("persisted AI review handoff", () => {
  const review = document.querySelector('#sendRecordRows a[href^="/admin/cloud-orchestrator/plans/"]');
  return Boolean(review?.textContent.includes("AI 审阅与收件人"));
}, () => statusFailure(previewState) || statusFailure(document.querySelector("#sendRecordStatusLine")));
const review = document.querySelector('#sendRecordRows a[href^="/admin/cloud-orchestrator/plans/"]');
const planID = Number(review.getAttribute("href").split("/").at(-1));
if (!Number.isSafeInteger(planID) || planID < 1) throw new Error("original detail page returned an invalid AI plan ID");
console.log(JSON.stringify({ ai_plan_id: planID }));
dom.window.close();
