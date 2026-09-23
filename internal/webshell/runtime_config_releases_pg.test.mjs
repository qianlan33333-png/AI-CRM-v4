import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const baseURL = process.env.AICRM_RUNTIME_RELEASE_TEST_URL;
if (!/^https?:\/\//.test(baseURL || "")) throw new Error("AICRM runtime release test server is required");
const here = path.dirname(fileURLToPath(import.meta.url));
const host = fs.readFileSync(path.join(here, "static/admin_console/runtime_config_releases_host.js"), "utf8");
const wait = (ms = 25) => new Promise((resolve) => setTimeout(resolve, ms));
const waitFor = async (predicate, message) => {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (predicate()) return;
    await wait();
  }
  throw new Error(message);
};
const cookie = `aicrm_admin_session=runtime-release-browser; aicrm_admin_csrf=${"c".repeat(43)}`;
const dom = new JSDOM('<!doctype html><html><body data-runtime-config-page="runtimeReleaseNew"><main data-runtime-release-host></main></body></html>', {
  url: `${baseURL}/admin/config/releases/new`, runScripts: "outside-only", pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.AdminDateTime = {};
    window.AdminFmt = { localTime: () => "2030-01-02 03:04:05", whenAdminDateTimeReady: (ready) => ready(window.AdminDateTime) };
    window.document.cookie = "aicrm_admin_session=runtime-release-browser";
    window.document.cookie = `aicrm_admin_csrf=${"c".repeat(43)}`;
    window.fetch = (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const headers = new Headers(init.headers);
      headers.set("Cookie", cookie);
      return globalThis.fetch(url, {...init, headers});
    };
  },
});
dom.window.eval(host);
await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-create] button[type=submit]") && !dom.window.document.querySelector("[data-runtime-release-create] button[type=submit]").disabled), "runtime release page did not render a draft form");
const form = dom.window.document.querySelector("[data-runtime-release-create]");
form.querySelector('[name="max_recipients"]').value = "2";
form.querySelector('[name="confirm"]').checked = true;
form.dispatchEvent(new dom.window.Event("submit", {bubbles: true, cancelable: true}));
await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-validate]")), "created release did not expose validate");
const validate = dom.window.document.querySelector("[data-runtime-release-validate]");
validate.click(); await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-publish]")), "validated release did not expose publish");
const publish = dom.window.document.querySelector("[data-runtime-release-publish]");
publish.click(); await waitFor(() => Boolean(dom.window.document.querySelector("[data-runtime-release-rollback]")), "published release did not expose rollback");
const rollback = dom.window.document.querySelector("[data-runtime-release-rollback]");
rollback.click(); await waitFor(() => dom.window.document.body.textContent.includes("已创建并发布回滚记录"), "rollback result was not rendered");
if (!dom.window.document.body.textContent.includes("实际使用回读")) throw new Error("runtime usage readback was not rendered");
dom.window.close();
console.log("runtime_config_releases_pg: PASS");
