import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(root, "dist");
const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const fail = (message) => { throw new Error(`customer Host regression: ${message}`); };
const response = (payload, status = 200) => new Response(JSON.stringify(payload), {
  status,
  headers: { "Content-Type": "application/json" },
});

const host = await buildTestBrowserBundle(path.join(root, "v3", "customerAdapter.ts"));
const admin = await buildTestBrowserBundle(path.join(root, "src", "admin", "main.ts"));
const operationMemberPicker = fs.readFileSync(path.join(root, "..", "internal", "webshell", "static", "admin_console", "operation_member_picker_dd8d60d.js"), "utf8");
const wecomTagPicker = fs.readFileSync(path.join(root, "donors", "standard-components-production", "static", "wecom_tag_picker.js"), "utf8");
const standardReady = `window.AICRMStandardComponents={ready:()=>Promise.resolve()};window.AICRMTagPicker={open:(options)=>{window.__tagPickerOptions=options;options.loadPage({query:'',signal:new AbortController().signal}).then((page)=>options.onCommit({selected:page.items.slice(0,1),added:page.items.slice(0,1),removed:[]}));}};`;

function documentWithHost(page, query, fetcher) {
  const source = fs.readFileSync(path.join(dist, "admin", page), "utf8");
  const html = source.replace(/<script type="module" src="[^"]+"><\/script>/g, "")
    .replace("</body>", () => `<script>${standardReady}</script><script>${operationMemberPicker}</script><script>${wecomTagPicker}</script><script>${host}</script><script>${admin}</script></body>`);
  return new JSDOM(html, {
    url: `https://test.invalid/admin/${page}${query}`,
    runScripts: "dangerously",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response;
      window.Headers = Headers;
      window.fetch = fetcher(window);
    },
  });
}

const listRequests = [];
const list = documentWithHost("customers.html", "", (window) => async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
  listRequests.push({ path: url.pathname, query: url.search, method: init.method || "GET", headers: new Headers(init.headers) });
  if (url.pathname === "/api/admin/common/operation-members") {
    return response({ items: [{ staff_id: 101, user_id: "alice", display_name: "Alice", active: true }] });
  }
  if (url.pathname === "/api/admin/wecom/tags") {
    return response({ read_model_status: "ready", groups: [{ group_id: 1, group_name: "分组", tags: [{ tag_id: 8, tag_name: "会员" }] }], items: [{ tag_id: 8, group_id: 1, tag_name: "会员", group_name: "分组" }], count: 1, total_tags: 1, tag_limit: 1000 });
  }
  if (url.pathname === "/api/admin/customers") {
    return response({
      items: [{
        customer_id: 7,
        display_name: "客户七",
        oneid: "oneid-label-7",
        phone_masked: "138****0007",
        owner_staff_id: 101,
        activation_status: "active",
        updated_at: "2026-09-08T00:00:00Z",
      }],
      total: 1,
      total_is_estimate: false,
    });
  }
  return response({ code: "unexpected" }, 500);
});
try {
  await sleep(100);
  const document = list.window.document;
  const owner = document.querySelector("tbody tr td:nth-child(2)")?.textContent?.trim();
  const phone = document.querySelector("tbody tr td:nth-child(3)")?.textContent?.trim();
  if (owner !== "负责人 #101（姓名暂不可用）" || phone !== "138****0007") {
    fail("frozen customer row did not show the local owner and masked phone presentation");
  }

  const ownerInput = document.getElementById("fCustomerOwner");
  const tagInput = document.getElementById("fCustomerTag");
  if (!(ownerInput instanceof list.window.HTMLInputElement) || !(tagInput instanceof list.window.HTMLInputElement) || !ownerInput.hidden || !tagInput.hidden) {
    fail("customer filters did not replace ID entry with standard picker triggers");
  }
  const ownerButton = [...document.querySelectorAll("button")].find((button) => button.textContent?.trim() === "选择负责人");
  if (!ownerButton || ownerButton.disabled) fail("authorized owner selection was not enabled");
  ownerButton.click();
  await sleep(30);
  document.querySelector("[data-operation-member-row-select]")?.click();
  document.querySelector("[data-operation-member-confirm]")?.click();
  await sleep(10);
  if (ownerInput.value !== "101") fail("standard owner picker did not preserve staff_id separately from user_id");
  const tagButton = [...document.querySelectorAll("button")].find((button) => button.textContent?.trim() === "选择标签");
  if (!tagButton) fail("standard tag selector trigger is missing");
  tagButton.click();
  await sleep(30);
  if (tagInput.value !== "8" || list.window.__tagPickerOptions?.scope !== "customer.filter.tag" || list.window.__tagPickerOptions?.source !== "local_tag_catalog") fail("V3 tag picker did not return its canonical Customer filter tag ID");
  [...document.querySelectorAll("button")].find((button) => button.textContent?.trim() === "查询")?.click();
  await sleep(100);
  const filtered = listRequests.filter((request) => request.path === "/api/admin/customers").at(-1);
  const query = new URLSearchParams(filtered?.query);
  if (query.get("owner_staff_id") !== "101" || query.get("tag_id") !== "8") {
    fail("owner/tag filters were not forwarded to the Customer directory");
  }

  await list.window.fetch("/logout", { method: "POST", headers: { "X-CSRF-Token": "current-session-csrf" } });
  const logout = listRequests.at(-1);
  if (logout?.path !== "/logout" || logout.method !== "POST" || logout.headers.get("X-CSRF-Token") !== "current-session-csrf") {
    fail("logout did not retain the current session's original HTTP request");
  }
} finally {
  // The Host observes the rendered document. Keep this fixture alive until
  // process exit so JSDOM does not tear down its Location during a queued turn.
}

if (fs.existsSync(path.join(dist, "admin", "customerDetail.html"))) fail("retired detail page must not be emitted");
const profileLink = list.window.document.querySelector('a[href="/admin/customers/7"]');
if (!profileLink) fail("customer list must open the canonical profile with the same numeric Customer ID");
console.log("customer Host frozen-template interactions: ok");
