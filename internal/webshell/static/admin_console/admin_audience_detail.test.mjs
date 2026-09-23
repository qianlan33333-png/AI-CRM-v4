import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const adapter = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience.html"), "utf8")
  .replace(/^\{\{define "admin_audience"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const wait = (milliseconds = 80) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const gatewayUnavailable = () => ({ ok: false, status: 502, json: async () => { throw new Error("non-json gateway response"); } });
const requests = [];
const confirmations = [];
let precheckAttempts = 0;
let persistGatewayFailure = false;
let archiveFailures = 1;
let groupDeleted = false;

const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.AICRMConfirmation = { confirm: (options) => new Promise((resolve) => confirmations.push({ options, resolve })) };
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = init.method || "GET";
      requests.push({ path: url.pathname, search: url.search, method, body: init.body, headers: Object.fromEntries(new window.Headers(init.headers || {})) });
      if (url.pathname === "/api/admin/ai-audience/package-groups" && method === "GET") return json({ items: groupDeleted ? [] : [{ id: 17, name: "临时分组", version: 4 }] });
      if (url.pathname === "/api/admin/ai-audience/package-groups/17" && method === "DELETE") {
        groupDeleted = true;
        return json({});
      }
      if (url.pathname === "/api/admin/ai-audience/templates") return json({ items: [{ key: "active_contacts", label: "活跃客户", description: "筛选最近有互动的客户", available: true }] });
      if (url.pathname === "/api/admin/ai-audience/packages" && method === "GET") {
        return json({ items: [{ id: 13, code: "audience-073da67f778402ce", name: "近30天活跃客户", membership_mode: "rule", lifecycle: "paused", version: 2, member_count: 23460, published_at: "2026-09-04T08:30:00Z", readiness: "not_ready" }], total: 1, limit: 100, offset: 0 });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/precheck" && method === "POST") {
        precheckAttempts += 1;
        if (precheckAttempts === 1 || persistGatewayFailure) return gatewayUnavailable();
        return json({ precheck: { ready: false, reasons: ["automation_binding_missing", "sender_set_missing", "provider_disabled"] } });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/activate" && method === "POST") {
        return json({ package: { id: 13 } });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13" && method === "DELETE") {
        if (archiveFailures > 0) { archiveFailures -= 1; return json({ error: "not_ready" }, 503); }
        return json({ package: { id: 13 } });
      }
      return json({ error: "unexpected_request" }, 500);
    };
  },
});

dom.window.eval(adapter);
await wait();

const document = dom.window.document;
document.querySelector("#createPackageBtn").click();
if (document.querySelector("#packageCreateTemplate") || !document.querySelector("#packageCreateGroup") || !document.querySelector("#packageFormHelp").textContent.includes("空人群包")) throw new Error("new package must be an empty AI container without a template");
document.querySelector("#cancelPackageBtn").click();
document.querySelector('[data-action="edit"][data-package-id="13"]').click();
if (document.querySelector('#packageCreateName').value !== '近30天活跃客户' || document.querySelector('#packageModalTitle').textContent !== '编辑人群包') throw new Error('list edit failed to hydrate');
document.querySelector('#packageCreateName').value = '取消的改名';
document.querySelector('#cancelPackageBtn').click();
if (requests.some(r => r.method === 'PATCH')) throw new Error('cancel edited persistent state');
const row = document.querySelector("#audRows tr");
const renderedCount = row?.querySelectorAll("td")[1]?.textContent.trim();
if (!row || Number(renderedCount) !== 23460) {
  throw new Error(`published snapshot count was not rendered: ${row?.textContent || "missing row"}`);
}
document.querySelector('[data-action="activate"][data-package-id="13"]')?.click();
await wait(300);

const notice = document.querySelector("#audNotice")?.textContent || "";
if (!notice.includes("未绑定已发布的话术智能体") || !notice.includes("未配置发送人白名单") || !notice.includes("发送服务") || notice.includes("provider_disabled")) {
  throw new Error(`activation blockers were not explained: ${notice}`);
}
if (requests.some((request) => request.path.endsWith("/activate"))) {
  throw new Error("activation mutation ran after a failed readiness precheck");
}
if (precheckAttempts !== 2) {
  throw new Error(`transient precheck was not retried exactly once: ${precheckAttempts}`);
}

persistGatewayFailure = true;
document.querySelector('[data-action="activate"][data-package-id="13"]')?.click();
await wait(900);
const gatewayNotice = document.querySelector("#audNotice")?.textContent || "";
if (!gatewayNotice.includes("服务正在发布或短暂不可用") || gatewayNotice.includes("unknown_error")) {
  throw new Error(`persistent gateway failure was not classified: ${gatewayNotice}`);
}
if (precheckAttempts !== 5 || requests.some((request) => request.path.endsWith("/activate"))) {
  throw new Error(`gateway retry crossed the read-only precheck boundary: attempts=${precheckAttempts}`);
}

const groupButton = document.querySelector('[data-group-id="17"]');
groupButton?.click();
await wait();
const deleteGroup = () => document.querySelector("#deleteGroupBtn")?.click();
deleteGroup();
deleteGroup();
await wait();
if (confirmations.length !== 1 || confirmations[0].options.title !== "删除空分组" || !confirmations[0].options.description.includes("临时分组")) {
  throw new Error(`empty-group confirmation did not freeze its target: ${JSON.stringify(confirmations.map((entry) => entry.options))}`);
}
confirmations.shift().resolve({ confirmed: false });
await wait();
if (requests.some((request) => request.method === "DELETE" && request.path === "/api/admin/ai-audience/package-groups/17")) {
  throw new Error("cancelled empty-group deletion made a mutation");
}
deleteGroup();
await wait();
confirmations.shift().resolve({ confirmed: true });
await wait(120);
const groupWrites = requests.filter((request) => request.method === "DELETE" && request.path === "/api/admin/ai-audience/package-groups/17");
if (groupWrites.length !== 1 || groupWrites[0].body !== undefined || !groupWrites[0].headers["idempotency-key"]) {
  throw new Error(`empty-group deletion did not preserve its Owner request: ${JSON.stringify(groupWrites)}`);
}
document.querySelector('[data-group-id=""]')?.click();
await wait();

const archive = () => document.querySelector('[data-action="archive"][data-package-id="13"]')?.click();
archive();
archive();
await wait();
if (confirmations.length !== 1 || confirmations[0].options.title !== "归档人群包" || !confirmations[0].options.description.includes("近30天活跃客户")) {
  throw new Error(`archive confirmation did not freeze the visible package target: ${JSON.stringify(confirmations.map((entry) => entry.options))}`);
}
confirmations.shift().resolve({ confirmed: false });
await wait();
if (requests.some((request) => request.method === "DELETE" && request.path === "/api/admin/ai-audience/packages/13")) {
  throw new Error("cancelled archive made a mutation");
}
archive();
await wait();
confirmations.shift().resolve({ confirmed: true });
await wait(180);
let archiveWrites = requests.filter((request) => request.method === "DELETE" && request.path === "/api/admin/ai-audience/packages/13");
if (archiveWrites.length !== 1 || archiveWrites[0].search !== "?expected_version=2" || archiveWrites[0].body !== JSON.stringify({ expected_version: 2 }) || !archiveWrites[0].headers["idempotency-key"] || !document.querySelector("#audNotice")?.textContent.includes("能力尚未满足")) {
  throw new Error(`archive failure did not keep the frozen Owner request and retryable feedback: ${JSON.stringify({ archiveWrites, notice: document.querySelector("#audNotice")?.textContent })}`);
}
archive();
await wait();
confirmations.shift().resolve({ confirmed: true });
await wait(180);
archiveWrites = requests.filter((request) => request.method === "DELETE" && request.path === "/api/admin/ai-audience/packages/13");
if (archiveWrites.length !== 2 || !archiveWrites[1].headers["idempotency-key"]) {
  throw new Error(`archive retry did not submit exactly one new original Owner command: ${JSON.stringify(archiveWrites)}`);
}

dom.window.close();
console.log("admin-audience-activation-readiness-browser: PASS");
