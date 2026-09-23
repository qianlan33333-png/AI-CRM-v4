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

let tagRetry = false;
let chatRetry = false;
const detailRequests = [];
const detail = documentWithHost("customerDetail.html", "?id=7", (window) => async (input, init = {}) => {
  const url = new URL(typeof input === "string" ? input : input.url, window.location.origin);
  detailRequests.push({ path: url.pathname, query: url.search, method: init.method || "GET" });
  if (url.pathname === "/api/admin/customers/7/360") {
    return response({
      tags_status: "unavailable",
      chat: { status: chatRetry ? "ready" : "unavailable" },
      profile: {
        status: "ready",
        data: {
          display_name: "客户七",
          owner_staff_id: 101,
          oneid: "oneid-label-7",
          phone_masked: "138****0007",
          last_synced_at: "2026-09-08T00:00:00Z",
          updated_at: "2026-09-08T00:00:00Z",
        },
      },
      recent_touchpoints: { status: "ready", data: [] },
    });
  }
  if (url.pathname === "/api/admin/customers/7/tags") {
    return tagRetry ? response({ status: "ready", items: [{ id: 8, name: "已恢复标签" }] }) : response({ code: "unavailable" }, 503);
  }
  if (url.pathname === "/api/admin/customers/7/chat-activity") {
    return chatRetry
      ? response({ status: "ready", items: [{ chat_type: "private", message_type: "text", occurred_at: "2026-09-07T00:00:00Z" }] })
      : response({ code: "unavailable" }, 503);
  }
  if (url.pathname === "/api/v1/customers/7/survey-answers") return response({ items: [], total: 0 });
  return response({ code: "unexpected" }, 500);
});
try {
  await sleep(120);
  const document = detail.window.document;
  const notices = [...document.querySelectorAll("[data-customer-auxiliary-status]")];
  if (notices.length !== 2 || !document.body.textContent?.includes("标签加载失败，请重试。") || !document.body.textContent?.includes("聊天加载失败，请重试。")) {
    fail("unavailable tags/chat did not show scoped retryable failures");
  }
  if (!document.body.textContent?.includes("负责人 #101（姓名暂不可用）")) {
    fail("detail did not retain the local CRM owner presentation");
  }
  const retry = document.querySelector('[data-customer-auxiliary-retry="tags"]');
  if (!retry) fail("tag retry is absent from the actual frozen detail Host");
  retry.click();
  await sleep(80);
  if (retry.disabled || retry.textContent?.trim() !== "重试" || !document.querySelector('[data-customer-auxiliary-status="tags"]')) {
    fail("failed tag retry did not remain actionable");
  }
  tagRetry = true;
  retry.click();
  await sleep(80);
  if (document.querySelector('[data-customer-auxiliary-status="tags"]')) {
    fail("second tag retry did not clear the scoped unavailable notice after a ready response");
  }
  if (!document.body.textContent?.includes("已恢复标签")) {
    fail("tag retry cleared the failure notice without rendering the recovered tag result");
  }
  if (!detailRequests.some((request) => request.path === "/api/admin/customers/7/tags")) {
    fail("tag retry did not reach the Customer-owned auxiliary endpoint");
  }
  chatRetry = true;
  const chatRetryButton = document.querySelector('[data-customer-auxiliary-retry="chat"]');
  if (!chatRetryButton) fail("chat retry is absent from the actual frozen detail Host");
  chatRetryButton.click();
  await sleep(80);
  const chatSummary = [...document.querySelectorAll("div")].find((element) => element.textContent?.trim() === "私聊 · 2026-09-07 08:00:00");
  if (!chatSummary || document.body.textContent?.includes("2026-09-07T00:00:00Z")) {
    fail("chat summary did not render Shanghai seconds without RFC3339");
  }
  const projected = await (await detail.window.fetch("/api/v1/customers/7/context")).json();
  if (projected.chat.items[0]?.sent_at !== "2026-09-07T00:00:00Z" || projected.chat.items[0]?.message_type !== "text") {
    fail("customer context must retain raw message metadata and its RFC3339 instant");
  }
  const refresh = [...document.querySelectorAll("button")].find((button) => button.textContent?.trim() === "刷新记录");
  if (!refresh) fail("frozen customer detail did not retain its refresh control");
  refresh.click();
  await sleep(120);
  if (!document.body.textContent?.includes("私聊 · 2026-09-07 08:00:00") || !document.body.textContent?.includes("消息类型：文本") || document.body.textContent?.includes("2026-09-07T00:00:00Z")) {
    fail("frozen customer detail did not render the projected Shanghai time and Chinese message type directly");
  }
} finally {
  // See the list fixture above.
}

console.log("customer Host frozen-template interactions: ok");
