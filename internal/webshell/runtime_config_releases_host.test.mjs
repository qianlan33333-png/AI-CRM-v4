import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const host = fs.readFileSync(path.join(here, "static/admin_console/runtime_config_releases_host.js"), "utf8");
const token = "a".repeat(43);
const calls = [];
const wait = () => new Promise((resolve) => setTimeout(resolve, 0));
const waitFor = async (predicate, message) => {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    if (predicate()) return;
    await wait();
  }
  throw new Error(message);
};
const json = (body, status = 200) => ({ok: status >= 200 && status < 300, status, json: async () => body});
let release = null;
let validationAttempts = 0;
const dom = new JSDOM('<!doctype html><html><body data-runtime-config-page="runtimeReleaseNew"><main data-runtime-release-host></main></body></html>', {
  url: "https://test.invalid/admin/config/releases/new", runScripts: "outside-only", pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.AdminDateTime = {};
    window.AdminFmt = { localTime: (value) => value === "2026-09-06T00:00:00Z" ? "2026-09-06 08:00:00" : value === "2026-09-06T00:01:00Z" ? "2026-09-06 08:01:00" : "时间暂不可用", whenAdminDateTimeReady: (ready) => ready(window.AdminDateTime) };
    window.document.cookie = "aicrm_admin_session=session";
    window.document.cookie = `aicrm_admin_csrf=${"c".repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const body = init.body ? JSON.parse(String(init.body)) : null;
      calls.push({path: url.pathname, method: init.method || "GET", body, headers: new Headers(init.headers)});
      if (url.pathname === "/api/admin/config/runtime-releases" && !init.method) return json({runtime_releases: {active_revision: 0, effective: {automation_max_recipients_per_run: 1, source: "environment_default"}, releases: []}, admin_action_token: token});
      if (url.pathname === "/api/admin/config/runtime-releases" && init.method === "POST") { release = {id: 1, state: "draft", base_revision: 0, checksum: "b".repeat(64), settings: body.settings, created_by: "7", created_at: "2026-09-06T00:00:00Z", validation_errors: []}; return json({runtime_release: release}, 201); }
      if (/^\/api\/admin\/config\/runtime-releases\/\d+$/.test(url.pathname) && !init.method) return json({runtime_release: release, actions: {validate: token, publish: token, rollback: token}});
      if (/^\/api\/admin\/config\/runtime-releases\/\d+\/usage$/.test(url.pathname)) return json({usage: []});
      if (url.pathname === "/api/admin/config/runtime-releases/1/validate") {
        validationAttempts += 1;
        release = validationAttempts === 1
          ? {...release, state: "validation_failed", validation_errors: [{key: "automation.operations.max_recipients_per_run", error: "provider_unavailable（upstream failure）"}]}
          : {...release, state: "validated", validation_errors: []};
        return json({runtime_release: release});
      }
      if (url.pathname === "/api/admin/config/runtime-releases/1/publish") { release = {...release, state: "published", published_by: "7", published_at: "2026-09-06T00:01:00Z"}; return json({runtime_release: release}); }
      if (url.pathname === "/api/admin/config/runtime-releases/1/rollback") { release = {...release, id: 2, state: "published", rollback_of_release_id: 1}; return json({runtime_release: release}); }
      return json({error: "unexpected"}, 500);
    };
  },
});
dom.window.eval(host);
await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-create] button[type=submit]") && !dom.window.document.querySelector("[data-runtime-release-create] button[type=submit]").disabled), "new release host did not render donor-compatible draft form");
const form = dom.window.document.querySelector("[data-runtime-release-create]");
form.querySelector('[name="max_recipients"]').value = "2";
form.querySelector('[name="confirm"]').checked = true;
form.dispatchEvent(new dom.window.Event("submit", {bubbles: true, cancelable: true}));
await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-validate]")), "draft page did not render validation action");
if (!dom.window.document.body.textContent.includes("状态草稿") || dom.window.document.body.textContent.includes("state=draft")) throw new Error("runtime release draft state was not presented in Chinese");
let validate = dom.window.document.querySelector("[data-runtime-release-validate]");
validate.click(); await waitFor(() => dom.window.document.body.textContent.includes("校验未通过"), "validation failure did not render");
const validationText = dom.window.document.body.textContent;
if (!validationText.includes("该配置项未通过校验。") || validationText.includes("provider_unavailable") || validationText.includes("upstream failure")) throw new Error(`validation error was not safely localized: ${validationText}`);
validate = dom.window.document.querySelector("[data-runtime-release-validate]");
validate.click(); await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-publish]")), "validated page did not render publish action");
if (!dom.window.document.body.textContent.includes("状态已校验")) throw new Error("runtime release validated state was not presented in Chinese");
const publish = dom.window.document.querySelector("[data-runtime-release-publish]");
publish.click(); await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-rollback]")), "published page did not render rollback action");
if (!dom.window.document.body.textContent.includes("状态已发布")) throw new Error("runtime release published state was not presented in Chinese");
const rollback = dom.window.document.querySelector("[data-runtime-release-rollback]");
rollback.click(); await waitFor(() => dom.window.document.body.textContent.includes("已创建并发布回滚记录"), "rollback result was not rendered");
const actual = calls.filter((call) => call.method === "POST");
const paths = actual.map((call) => call.path).join(",");
if (paths !== "/api/admin/config/runtime-releases,/api/admin/config/runtime-releases/1/validate,/api/admin/config/runtime-releases/1/validate,/api/admin/config/runtime-releases/1/publish,/api/admin/config/runtime-releases/1/rollback") throw new Error(`release operation sequence=${paths}`);
if (actual[0].body.expected_base_revision !== 0 || actual[0].body.settings[0].value !== 2 || actual[3].body.expected_checksum !== "b".repeat(64) || actual.some((call) => !call.body.admin_action_token) || actual.some((call) => !call.headers.get("X-CSRF-Token") || !call.headers.get("Idempotency-Key"))) throw new Error("host did not supply current revision, checksum, CSRF, idempotency key and action proof");
if (!dom.window.document.body.textContent.includes("已创建并发布回滚记录")) throw new Error("rollback result was not rendered");
if (!dom.window.document.body.textContent.includes("2026-09-06 08:00:00")) throw new Error("runtime release timestamp did not use exact Shanghai seconds");
if (rollback.textContent !== "用此版本回滚") throw new Error(`rollback label=${rollback.textContent}`);
dom.window.close();
console.log("runtime_config_releases_host: PASS");
