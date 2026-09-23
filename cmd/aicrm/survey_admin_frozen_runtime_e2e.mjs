import { JSDOM } from "jsdom";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../../web/scripts/test-browser-bundle.mjs";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const origin = process.env.AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN;
if (!origin) throw new Error("AICRM_SURVEY_ADMIN_RUNTIME_ORIGIN is required");

const editorBundle = await buildTestBrowserBundle(path.join(root, "web/src/admin/sections/questionnaireEditor.ts"));
const adminBundle = await buildTestBrowserBundle(path.join(root, "web/src/admin/main.ts"));
const hostBridge = await readFile(path.join(root, "internal/webshell/static/admin_console/survey_operations.js"), "utf8");
let nextKey = 1;

async function waitFor(label, predicate, timeout = 4000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (predicate()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(`timed out waiting for ${label}`);
}

function input(window, element, value) {
  if (!element) throw new Error("expected input is missing");
  element.value = value;
  element.dispatchEvent(new window.Event("input", { bubbles: true }));
}

function change(window, element, checked) {
  if (!element) throw new Error("expected checkbox is missing");
  element.checked = checked;
  element.dispatchEvent(new window.Event("change", { bubbles: true }));
}

function click(element) {
  if (!element) throw new Error("expected control is missing");
  element.click();
}

function summarizeCalls(calls) {
  return JSON.stringify(calls.map(({ path, method, status, response }) => {
    const questionnaire = response?.questionnaire || response?.data?.questionnaire;
    return {
      path,
      method,
      status,
      questionnaire: questionnaire ? {
        id: questionnaire.id,
        version: questionnaire.version,
        status: questionnaire.status,
        enabled: questionnaire.enabled,
      } : undefined,
    };
  }));
}

async function openEditor(query, { requestDelayMs = 0 } = {}) {
  const response = await fetch(`${origin}/admin/questionnaireDetail.html${query}`);
  if (!response.ok) throw new Error(`actual Host editor response=${response.status} body=${(await response.text()).slice(0, 240)}`);
  const html = await response.text();
  if (!html.includes('data-admin-shell-source="v3_webshell"') || !html.includes('id="questionnaire-editor-config"')) {
    throw new Error("actual Survey Host did not render the frozen editor inside the v3 shell");
  }
  const dom = new JSDOM(html, {
    url: `${origin}/admin/questionnaireDetail.html${query}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = globalThis.Headers;
      window.Response = globalThis.Response;
      window.confirm = () => true;
      window.prompt = () => null;
      Object.defineProperty(window.crypto, "randomUUID", { configurable: true, value: () => `00000000-0000-4000-8000-${String(nextKey++).padStart(12, "0")}` });
      window.__surveyRuntimeDownloads = [];
      window.URL.createObjectURL = (blob) => {
        window.__surveyRuntimeDownloads.push({ blob, clicked: false, filename: "" });
        return `blob:survey-runtime-${window.__surveyRuntimeDownloads.length}`;
      };
      window.URL.revokeObjectURL = () => {};
      window.HTMLAnchorElement.prototype.click = function clickAnchor() {
        const download = window.__surveyRuntimeDownloads.at(-1);
        if (download) {
          download.clicked = true;
          download.filename = this.download || "";
        }
      };
    },
  });
  const calls = [];
  dom.window.fetch = async (inputValue, init = {}) => {
    const target = new URL(String(inputValue), origin);
    const call = { path: target.pathname, method: init.method || "GET", body: typeof init.body === "string" ? init.body : "", status: 0 };
    calls.push(call);
    // The Host must retain the exact adapter promise even when a browser-side
    // wrapper delays the request before it reaches the actual Survey HTTP API.
    if (requestDelayMs > 0) await new Promise((resolve) => setTimeout(resolve, requestDelayMs));
    const response = await globalThis.fetch(target, init);
    call.status = response.status;
    response.clone().json().then((value) => { call.response = value; }).catch(() => {});
    return response;
  };
  dom.window.eval(editorBundle);
  dom.window.eval(hostBridge);
  await waitFor("frozen editor initial render", () => dom.window.document.querySelector("#save-btn") !== null && dom.window.document.querySelector("#inspector-body") !== null);
  return { dom, calls };
}

async function openManagement() {
  const response = await fetch(`${origin}/admin/questionnaires.html`);
  if (!response.ok) throw new Error(`actual Host management response=${response.status} body=${(await response.text()).slice(0, 240)}`);
  const html = await response.text();
  if (!html.includes('data-admin-shell-source="v3_webshell"') || !html.includes('data-page="questionnaires"')) {
    throw new Error("actual Survey Host did not render the frozen management page inside the v3 shell");
  }
  const dom = new JSDOM(html, {
    url: `${origin}/admin/questionnaires.html`, runScripts: "outside-only", pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = globalThis.Headers;
      window.Response = globalThis.Response;
      window.confirm = () => true;
      Object.defineProperty(window.crypto, "randomUUID", { configurable: true, value: () => `00000000-0000-4000-8000-${String(nextKey++).padStart(12, "0")}` });
    },
  });
  const calls = [];
  dom.window.fetch = async (inputValue, init = {}) => {
    const target = new URL(String(inputValue), origin);
    const call = { path: target.pathname, method: init.method || "GET", body: typeof init.body === "string" ? init.body : "", status: 0 };
    calls.push(call);
    const response = await globalThis.fetch(target, init);
    call.status = response.status;
    response.clone().json().then((value) => { call.response = value; }).catch(() => {});
    return response;
  };
  dom.window.eval(adminBundle);
  dom.window.eval(hostBridge);
  dom.window.document.dispatchEvent(new dom.window.Event("DOMContentLoaded"));
  return { dom, calls };
}

function managementRow(document, questionnaireID, title) {
  const idText = `#${questionnaireID}`;
  return [...document.querySelectorAll("tr")].find((item) => item.textContent?.includes(title)
    && [...item.querySelectorAll("span")].some((span) => span.textContent?.trim() === idText)) || null;
}

function managementAction(document, questionnaireID, title, label) {
  const row = managementRow(document, questionnaireID, title);
  if (!row) return null;
  return [...row.querySelectorAll("a,button")].find((item) => item.textContent?.trim() === label) || null;
}

const normal = await openEditor("");
const normalDocument = normal.dom.window.document;
input(normal.dom.window, normalDocument.querySelector("#field-name"), "冻结后台实际问卷");
input(normal.dom.window, normalDocument.querySelector("#field-title"), "冻结后台实际标题");
input(normal.dom.window, normalDocument.querySelector("#field-slug"), "frozen-admin-runtime");
click(normalDocument.querySelector("#add-single"));
input(normal.dom.window, normalDocument.querySelector("#question-title"), "第一道真实题");
input(normal.dom.window, normalDocument.querySelector('[data-option-field="option_text"]'), "第一题选项 A");
click(normalDocument.querySelector("#add-option-btn"));
await waitFor("first question second option", () => normalDocument.querySelectorAll('[data-option-field="option_text"]').length === 2);
input(normal.dom.window, normalDocument.querySelectorAll('[data-option-field="option_text"]')[1], "第一题选项 B");
click(normalDocument.querySelector("#add-single"));
input(normal.dom.window, normalDocument.querySelector("#question-title"), "第二道真实题");
input(normal.dom.window, normalDocument.querySelector('[data-option-field="option_text"]'), "第二题选项 A");
click(normalDocument.querySelector("#add-option-btn"));
await waitFor("second question second option", () => normalDocument.querySelectorAll('[data-option-field="option_text"]').length === 2);
input(normal.dom.window, normalDocument.querySelectorAll('[data-option-field="option_text"]')[1], "第二题选项 B");
if (!normalDocument.querySelector("#preview-questions")?.textContent.includes("第二道真实题")) {
  throw new Error("frozen preview did not render the edited question before save");
}
click(normalDocument.querySelector("#save-btn"));
await waitFor("normal editor create", () => /\?id=[1-9]\d*$/.test(normal.dom.window.location.search) && normalDocument.querySelector("#editor-duplicate-btn") !== null).catch((error) => {
  const toast = normalDocument.querySelector("#toast")?.textContent || "";
  throw new Error(`${error.message}; toast=${toast}; calls=${JSON.stringify(normal.calls)}`);
});
const normalID = Number(new URLSearchParams(normal.dom.window.location.search).get("id"));
if (!Number.isSafeInteger(normalID) || normalID < 1) throw new Error("created editor did not retain a server questionnaire id");
const frozenNormalPayload = JSON.parse(normal.calls.find((call) => call.path === "/api/admin/questionnaires" && call.method === "POST")?.body || "{}");
if (!frozenNormalPayload.assessment_config || !frozenNormalPayload.questions?.every((question) => question.options.every((option) => option.other_max_length === 80 && option.is_other === false))) {
  throw new Error(`fixture no longer captures the frozen hidden defaults: ${JSON.stringify(frozenNormalPayload)}`);
}

click(normalDocument.querySelector("#editor-duplicate-btn"));
await waitFor("frozen duplicate", () => normal.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/duplicate` && call.method === "POST" && call.status === 200 && Number(call.response?.questionnaire?.id || call.response?.data?.questionnaire?.id) > normalID));
const duplicateCall = normal.calls.find((call) => call.path === `/api/admin/questionnaires/${normalID}/duplicate` && call.method === "POST" && call.status === 200);
const copyID = Number(duplicateCall?.response?.questionnaire?.id || duplicateCall?.response?.data?.questionnaire?.id);
if (!Number.isSafeInteger(copyID) || copyID <= normalID) {
  throw new Error(`frozen duplicate response did not identify its copy: ${JSON.stringify(duplicateCall)}`);
}
// A duplicate response precedes the controller's list refresh and copy hydration.
// Keep the realm alive until its final success UI has rendered.
await waitFor("duplicate editor hydration", () => normal.calls.some(call => call.path === `/api/admin/questionnaires/${copyID}` && call.status === 200) && normalDocument.querySelector('#toast')?.textContent.includes('问卷已复制')).catch(error=>{throw new Error(error.message+'; search='+normal.dom.window.location.search+'; toast='+normalDocument.querySelector('#toast')?.textContent+'; calls='+summarizeCalls(normal.calls));});
normal.dom.window.close();

// The frozen management page owns the actual enable/disable controls. A saved
// definition is shown there as disabled; the first enable must freeze and
// publish that exact draft, while stopping and re-enabling use the lifecycle.
const management = await openManagement();
const managementDocument = management.dom.window.document;
await waitFor("normal management action", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "启用") !== null);
click(managementAction(managementDocument, normalID, "冻结后台实际标题", "启用"));
await waitFor("normal list enable conflict", () => management.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/enable` && call.status === 409)).catch((error) => {
  const row = managementRow(managementDocument, normalID, "冻结后台实际标题");
  throw new Error(`${error.message}; row=${row?.textContent || "missing"}; calls=${JSON.stringify(management.calls)}`);
});
await waitFor("normal draft publish", () => management.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/public-publish` && call.status === 200));
const normalPublish = management.calls.find((call) => call.path === `/api/admin/questionnaires/${normalID}/public-publish` && call.status === 200);
if (!normalPublish || !Number.isSafeInteger(Number(JSON.parse(normalPublish.body || "{}").expected_questionnaire_version)) || Number(JSON.parse(normalPublish.body || "{}").expected_questionnaire_version) < 1) {
  throw new Error(`normal draft publish was not a versioned CAS: ${JSON.stringify(normalPublish)}`);
}
await waitFor("normal list stop action", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "停用") !== null);
click(managementAction(managementDocument, normalID, "冻结后台实际标题", "停用"));
await waitFor("normal stop confirmation", () => [...managementDocument.querySelectorAll("button")].some((item) => item.textContent?.trim() === "确认停用"));
click([...managementDocument.querySelectorAll("button")].find((item) => item.textContent?.trim() === "确认停用"));
await waitFor("normal list disable", () => management.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/disable` && call.status === 200));
await waitFor("normal list re-enable action", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "启用") !== null);
click(managementAction(managementDocument, normalID, "冻结后台实际标题", "启用"));
await waitFor("normal list re-enable", () => management.calls.filter((call) => call.path === `/api/admin/questionnaires/${normalID}/enable` && call.status === 200).length === 1);
// The frozen controller refreshes its own list after a successful toggle. Wait
// for that actual rendered state before closing this JSDOM realm so its pending
// refresh cannot observe a torn-down window.
await waitFor("normal re-enable refresh", () => managementAction(managementDocument, normalID, "冻结后台实际标题", "停用") !== null);
management.dom.window.close();

// Export is a readback operation on the published Owner definition. Re-open
// the actual frozen detail Host after the lifecycle transition so the browser
// waits for both the response and the completed Blob/download gesture.
const normalPublished = await openEditor(`?id=${normalID}`);
const normalPublishedDocument = normalPublished.dom.window.document;
await waitFor("published editor export action", () => normalPublishedDocument.querySelector("#editor-export-btn") !== null);
click(normalPublishedDocument.querySelector("#editor-export-btn"));
await waitFor("actual CSV download", () => {
  const downloads = normalPublished.dom.window.__surveyRuntimeDownloads || [];
  return normalPublished.calls.some((call) => call.path === `/api/admin/questionnaires/${normalID}/export` && call.status === 200)
    && downloads.length === 1 && downloads[0].clicked
    && typeof downloads[0].blob?.arrayBuffer === "function" && typeof downloads[0].blob?.text === "function";
}).catch((error) => {
  throw new Error(`${error.message}; calls=${JSON.stringify(normalPublished.calls)}; downloads=${JSON.stringify((normalPublished.dom.window.__surveyRuntimeDownloads || []).map((value) => ({ clicked: value.clicked, filename: value.filename })))} `);
});
const csv = await normalPublished.dom.window.__surveyRuntimeDownloads[0].blob.text();
if (!csv.includes("submission_id") || !csv.includes("customer_id")) throw new Error(`downloaded CSV did not contain the Survey export contract: ${csv.slice(0, 160)}`);
normalPublished.dom.window.close();

// Legacy assessment URLs cannot restore the retired builder or create it through the API.
const retired = await openEditor('?mode=assessment', {requestDelayMs: 100});
await waitFor('retired editor boot settled', () => retired.calls.length >= 2 && retired.calls.every(call => call.status > 0) && retired.dom.window.document.querySelector('#tag-catalog-message')?.className.includes('error'));
if (retired.dom.window.document.querySelector('[data-assessment-step], #open-assessment-settings') || !retired.dom.window.document.querySelector('#field-name')) throw new Error('retired assessment builder is available');
const rejectedPayload = JSON.parse(normal.calls.find(call=>call.path === '/api/admin/questionnaires' && call.method === 'POST').body);
rejectedPayload.assessment_enabled = true;
rejectedPayload.slug = 'retired-assessment-attempt';
const rejected = await fetch(`${origin}/api/admin/questionnaires`, {method:'POST', headers:{'Content-Type':'application/json','Idempotency-Key':'retired-assessment-runtime'}, body:JSON.stringify(rejectedPayload)});
if (rejected.status !== 400) throw new Error(`assessment create status=${rejected.status}`);
retired.dom.window.close();
console.log(JSON.stringify({normalID, copyID, assessmentRetired:true}));
