import assert from "node:assert/strict";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";

const baseURL = process.env.AICRM_RUNTIME_RELEASE_TEST_URL;
if (!/^https?:\/\//.test(baseURL || "")) throw new Error("Config Center PostgreSQL HTTP test server is required");
const expectCatalogReleaseRace = process.env.AICRM_CONFIG_CENTER_EXPECT_CATALOG_RELEASE_RACE === "1";
const expectPendingClose = process.env.AICRM_CONFIG_CENTER_EXPECT_PENDING_CLOSE === "1";
const expectedAutomationMode = process.env.AICRM_CONFIG_CENTER_EXPECT_AUTOMATION_MODE || "limited";
const here = path.dirname(fileURLToPath(import.meta.url));
const host = fs.readFileSync(path.join(here, "static/admin_console/config_center_host.js"), "utf8");
const wait = (milliseconds = 25) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const waitFor = async (predicate, message) => {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (predicate()) return;
    await wait();
  }
  throw new Error(message);
};
let submitted;
let centerSubmitted;
let centerCatalog;
const centerSubmissions = [];
const cookie = `aicrm_admin_session=config-center-browser; aicrm_admin_csrf=${"c".repeat(43)}`;
const dom = new JSDOM('<!doctype html><html><body data-runtime-config-page="runtimeConfigCategory"><main data-runtime-release-host></main></body></html>', {
  url: `${baseURL}/admin/configDetail.html?cat=wecom_base`, runScripts: "outside-only", pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.document.cookie = "aicrm_admin_session=config-center-browser";
    window.document.cookie = `aicrm_admin_csrf=${"c".repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const headers = new Headers(init.headers);
      headers.set("Cookie", cookie);
      const response = await globalThis.fetch(url, { ...init, headers });
      if (url.pathname === "/api/admin/config/runtime-releases" && String(init.method || "GET").toUpperCase() === "POST") {
        submitted = JSON.parse(String(init.body || "{}"));
      }
      return response;
    };
  },
});
const centerDom = new JSDOM('<!doctype html><html><body data-runtime-config-page="runtimeConfigCenter"><main data-runtime-release-host></main></body></html>', {
  url: `${baseURL}/admin/config`, runScripts: "outside-only", pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.document.cookie = "aicrm_admin_session=config-center-browser";
    window.document.cookie = `aicrm_admin_csrf=${"c".repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const headers = new Headers(init.headers); headers.set("Cookie", cookie);
      const response = await globalThis.fetch(url, { ...init, headers });
      if (url.pathname === "/api/admin/config/runtime-catalog" && String(init.method || "GET").toUpperCase() === "GET") {
        centerCatalog = await response.clone().json();
      }
      if (url.pathname === "/api/admin/config/runtime-releases" && String(init.method || "GET").toUpperCase() === "POST") {
        centerSubmitted = JSON.parse(String(init.body || "{}"));
        centerSubmissions.push(centerSubmitted);
      }
      return response;
    };
  },
});
try {
  centerDom.window.eval(host);
  await waitFor(() => centerDom.window.document.querySelectorAll("[data-category-row]").length === 6, "Config Center did not render the six operational categories");
  assert.ok(centerDom.window.document.querySelector('a[href="/admin/configDetail.html?cat=ai_models"]'), "dedicated model settings entry is missing");
  for (const key of ["api_access", "stability", "sidebar_identity", "ai_automation"]) assert.equal(centerDom.window.document.querySelector(`[data-category-row="${key}"]`),null,"technical category must be absent");
  const headers = [...centerDom.window.document.querySelectorAll(".cc-category-table thead th")].map((cell) => cell.textContent.trim());
  assert.deepEqual(headers, ["类目", "是否生效", "生效开关", "配置"], "Config Center must retain the donor table columns");
  assert.ok(centerDom.window.document.querySelector('[data-category-row="wecom_base"] [data-category-enabled]') === null, "Config Center must not invent a data-category-enabled command");
  const wecomSwitch = centerDom.window.document.querySelector('[data-category-row="wecom_base"] .cc-switch input');
  assert.ok(wecomSwitch, "WeCom retains its owned primary switch");
  const categorySwitches = [...centerDom.window.document.querySelectorAll("[data-category-row] .cc-switch input")];
  assert.deepEqual(categorySwitches.map((input) => input.closest("[data-category-row]")?.dataset.categoryRow), ["wecom_base", "wechat_pay", "alipay", "wechat_shop", "wechat_oauth"], "Config Center must render the five primary switches declared by the real RuntimeCatalog");
  const oauthSwitch = centerDom.window.document.querySelector('[data-category-row="wechat_oauth"] .cc-switch input');
  assert.ok(oauthSwitch, "the official-account OAuth category must bind its RuntimeCatalog enable field");
  assert.equal(centerDom.window.document.querySelector('[data-category-row="sidebar_identity"] .cc-switch'), null, "a category without one primary enable field must show no aggregate switch");
  if (expectPendingClose) {
    const state = centerDom.window.document.querySelector('[data-category-row="wecom_base"] .cc-state');
    assert.equal(state?.textContent.trim(), "待生效", "a closed release remains pending until every required role reads its exact snapshot");
    assert.equal(state?.classList.contains("is-on"), false, "a pending close must not render as currently effective");
  } else {
    const originalWeComEnabled = wecomSwitch.checked;
    wecomSwitch.checked = !originalWeComEnabled;
    wecomSwitch.dispatchEvent(new centerDom.window.Event("change", { bubbles: true }));
    if (expectCatalogReleaseRace) {
      await waitFor(() => centerDom.window.document.querySelector("[data-config-center-status]")?.textContent.includes("配置已更新"), "Config Center did not reject a catalog/release version mismatch");
      assert.equal(centerSubmitted, undefined, "Config Center must not create a full-snapshot draft from mismatched reads");
    } else {
      await waitFor(() => centerSubmissions.length === 1, "WeCom switch did not create a draft through the actual HTTP endpoint");
      const centerValues = new Map((centerSubmitted.settings || []).map((item) => [item.key, item.value]));
      assert.equal(centerValues.get("wecom.enabled"), !originalWeComEnabled, "WeCom switch must stage its owned enabled field");
      assert.equal(centerValues.get("wecom.agent_id"), "agent-preserved", "WeCom switch must retain the full effective snapshot");

      const originalOAuthEnabled = oauthSwitch.checked;
      oauthSwitch.checked = !originalOAuthEnabled;
      oauthSwitch.dispatchEvent(new centerDom.window.Event("change", { bubbles: true }));
      await waitFor(() => centerSubmissions.length === 2, "OAuth switch did not create a draft through the actual HTTP endpoint");
      const oauthValues = new Map((centerSubmitted.settings || []).map((item) => [item.key, item.value]));
      assert.equal(oauthValues.get("survey.oauth_enabled"), !originalOAuthEnabled, "OAuth switch must stage its RuntimeCatalog enabled field");
      assert.equal(oauthValues.get("wecom.enabled"), originalWeComEnabled, "each homepage action must start from the unchanged effective snapshot");
      assert.equal(oauthValues.has("survey.oauth.enabled"), false, "OAuth switch must never submit the obsolete dotted key");
      assert.equal(oauthValues.get("wecom.agent_id"), "agent-preserved", "OAuth switch must retain the full effective snapshot");
      assert.equal(oauthValues.get("survey.oauth_app_id"), "oauth-app-preserved", "OAuth switch must retain the bound AppID from the effective snapshot");
      assert.equal(oauthValues.get("survey.oauth_open_platform_id"), "oauth-platform-preserved", "OAuth switch must retain the bound Open Platform ID from the effective snapshot");
      assert.equal(centerSubmitted.settings.length, centerCatalog.effective.settings.length, "OAuth switch must submit the complete effective snapshot");
      assert.deepEqual([...oauthValues.keys()].sort(), centerCatalog.effective.settings.map((item) => item.key).sort(), "OAuth draft keys must exactly match the real runtime catalog snapshot");
    }
  }
} finally {
  centerDom.window.close();
}

try {
  dom.window.eval(host);
  await waitFor(() => dom.window.document.querySelector('[data-runtime-setting="wecom.agent_id"]')?.value === "agent-preserved", "native AgentID string was not rendered");
  const form = dom.window.document.querySelector("form");
  assert.ok(form, "Config Center detail form was not rendered");
  form.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
  await waitFor(() => Boolean(submitted), "Config Center did not create a draft through the actual HTTP endpoint");
  const values = new Map((submitted.settings || []).map((item) => [item.key, item.value]));
  assert.equal(values.get("wecom.agent_id"), "agent-preserved", "saving an untouched category must retain AgentID");
  assert.equal(values.get("automation.operations.provider_mode"), expectedAutomationMode, "saving an untouched category must retain the native automation mode string");
  assert.equal(values.has("WECOM_API_BASE"), false, "deployment-maintained donor fields must remain informational and cannot be submitted");
  console.log("config_center_host_pg: PASS");
} finally {
  dom.window.close();
}
