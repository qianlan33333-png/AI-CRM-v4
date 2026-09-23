import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../../../../web/scripts/test-browser-bundle.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const adapter = fs.readFileSync(path.join(here, "automation_create_code_adapter.js"), "utf8");
const bundle = await buildTestBrowserBundle(path.join(root, "web", "src", "admin", "main.ts"));
const wait = () => new Promise((resolve) => setTimeout(resolve, 80));
const valid = (code) => /^agent_[a-f0-9]{32}$/.test(code);

const agent = (patch = {}) => ({
  id: 7, automation_type: "agent", agent_code: "welcome_agent", agent_name: "欢迎 Agent",
  bound_package_key: "", bound_package_id: null, bound_package_name: "", fixed_material_summary: { image_count: 0, miniprogram_count: 0, attachment_count: 0, group_invite_count: 0 },
  status: "paused", execution_enabled: false, materials_configured: false, updated_at: "2026-09-03T00:00:00Z", automation_type_label: "Agent 机器人",
  draft_role_prompt: "旧角色", draft_task_prompt: "旧任务", published_role_prompt: "旧角色", published_task_prompt: "旧任务",
  draft_version: 1, published_version: 1, has_unpublished_changes: false,
  fixed_content_package: { content_text: "", image_library_ids: [], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [] },
  fixed_content_package_preview: { content_text: "", material_summary: { image_count: 0, miniprogram_count: 0, attachment_count: 0, group_invite_count: 0 }, materials: [] }, legacy_configuration: {}, ...patch,
});
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ "Content-Type": "application/json" }), text: async () => JSON.stringify(body) });

function page(query, createCode = "") {
  let html = fs.readFileSync(path.join(root, "web", "dist", "admin", "agentEdit.html"), "utf8");
  // Model a real parser: the v3 host script is deferred, so it runs only
  // after body parsing, then the unchanged donor module mounts its controller.
  html = html.replace("<head>", `<head><script defer data-v3-host-adapter></script>`);
  if (createCode) html = html.replace("<body ", `<body data-automation-create-code="${createCode}" `);
  const requests = [];
  const dom = new JSDOM(html, {
    url: `https://test.invalid/admin/agentEdit.html${query}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.__AICRM_TEST_MOCK__ = false;
      window.document.cookie = `aicrm_csrf=${"c".repeat(43)}`;
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        const method = init.method || "GET";
        const request = { path: url.pathname, method, body: init.body ? JSON.parse(String(init.body)) : undefined };
        requests.push(request);
        if (url.pathname === "/api/admin/automation-agents" && method === "GET") return json({ ok: true, items: [agent()], total: 1 });
        if (url.pathname === "/api/admin/automation-agents/7" && method === "GET") return json({ ok: true, agent: agent() });
        if (url.pathname === "/api/admin/automation-agents" && method === "POST") return json({ ok: true, agent: agent({ id: 9, agent_code: request.body.agent_code, agent_name: request.body.agent_name }) });
        return json({ ok: false, code: "unexpected_request" }, 500);
      };
    },
  });
  // Deferred host adapter executes after parsing; frozen donor module mounts
  // after it. The adapter's observer and capture listener must bridge both
  // orders without changing donor source.
  dom.window.eval(adapter);
  dom.window.eval(bundle);
  return { dom, requests };
}

const agentCode = "agent_0123456789abcdef0123456789abcdef";
const created = page("?type=agent", agentCode);
await wait();
const createDoc = created.dom.window.document;
const createInput = createDoc.querySelector("#agentCode");
if (!createInput || createInput.value !== agentCode || !valid(createInput.value) || !createInput.readOnly) throw new Error("deferred adapter did not prefill the readonly Agent create code");
let agentInputEvents = 0;
createInput.addEventListener("input", () => { agentInputEvents += 1; });
createDoc.querySelector("#agentName").value = "新 Agent";
createDoc.querySelector("#agentRolePrompt").value = "角色";
createDoc.querySelector("#agentTaskPrompt").value = "任务";
createDoc.querySelector("[data-agent-save]").click();
await wait();
const createdPost = created.requests.find((request) => request.path === "/api/admin/automation-agents" && request.method === "POST");
if (!createdPost || createdPost.body.agent_code !== agentCode || createdPost.body.automation_type !== "agent") throw new Error("unchanged donor controller did not POST the host code for Agent");
if (agentInputEvents === 0) throw new Error("capture-time Agent code synchronization did not reach the mounted frozen controller");
created.dom.window.close();

const fixedCode = "agent_fedcba9876543210fedcba9876543210";
const fixed = page("?type=fixed_script", fixedCode);
await wait();
const fixedDoc = fixed.dom.window.document;
if (fixedDoc.querySelector("#agentCode")?.value !== fixedCode || !valid(fixedCode)) throw new Error("deferred adapter did not prefill fixed-script create code");
let fixedInputEvents = 0;
fixedDoc.querySelector("#agentCode")?.addEventListener("input", () => { fixedInputEvents += 1; });
fixedDoc.querySelector("#agentName").value = "新固定话术";
fixedDoc.querySelector("#agentType").value = "fixed_script";
fixedDoc.querySelector("#agentRolePrompt").value = "固定角色";
fixedDoc.querySelector("#agentTaskPrompt").value = "固定任务";
fixedDoc.querySelector("[data-agent-save]").click();
await wait();
const fixedPost = fixed.requests.find((request) => request.path === "/api/admin/automation-agents" && request.method === "POST");
if (!fixedPost || fixedPost.body.agent_code !== fixedCode || fixedPost.body.automation_type !== "fixed_script") throw new Error("unchanged donor controller did not POST the host code for fixed script");
if (fixedInputEvents === 0) throw new Error("capture-time fixed-script code synchronization did not reach the mounted frozen controller");
fixed.dom.window.close();

const userValue = page("?type=agent", "agent_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb");
await wait();
const userValueDoc = userValue.dom.window.document;
userValueDoc.querySelector("#agentCode").value = "user_supplied_code";
userValueDoc.querySelector("#agentName").value = "用户值";
userValueDoc.querySelector("#agentRolePrompt").value = "角色";
userValueDoc.querySelector("#agentTaskPrompt").value = "任务";
userValueDoc.querySelector("[data-agent-save]").click();
await wait();
const userValuePost = userValue.requests.find((request) => request.path === "/api/admin/automation-agents" && request.method === "POST");
if (!userValuePost || userValuePost.body.agent_code !== "user_supplied_code") throw new Error("host adapter overwrote a supplied create code");
userValue.dom.window.close();

const existing = page("?id=7", "agent_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa");
await wait();
if (existing.dom.window.document.querySelector("#agentCode")?.value !== "welcome_agent") throw new Error("host binding changed an existing immutable agent code");
existing.dom.window.close();

console.log("automation-create-code-adapter-browser: PASS");
