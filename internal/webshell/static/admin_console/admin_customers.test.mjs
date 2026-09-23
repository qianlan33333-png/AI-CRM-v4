import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../../../../web/scripts/test-browser-bundle.mjs";

const here = path.dirname(fileURLToPath(import.meta.url));
const repository = path.resolve(here, "../../../..");
const script = fs.readFileSync(path.join(here, "admin_customers.js"), "utf8");
const customerDetailTemplate = fs.readFileSync(path.join(repository, "internal", "webshell", "templates", "admin_customers.html"), "utf8");
if (customerDetailTemplate.includes('class="customer-tag-draft-selectors"') || customerDetailTemplate.includes('class="customer-tag-draft-actions"')) throw new Error("profile must not expose retired tag draft controls");
if (script.includes("企业微信机器人")) throw new Error("unverified customer contact type must remain pending rather than receiving an invented label");
if (!script.includes('cache: "no-store"') || !script.includes('}, 30000);')) throw new Error("authorized phone display must remain no-store and clear after 30 seconds");
const standardHost = await buildTestBrowserBundle(path.join(repository, "web", "v3", "standardComponentsHost.ts"));
const standardTagPickerArtifact = path.join(repository, "web", "dist", "assets", "standard-components", "wecom_tag_picker.js");
let standardTagPicker;
try {
  standardTagPicker = fs.readFileSync(standardTagPickerArtifact, "utf8");
} catch (error) {
  throw new Error(`missing manifest-verified standard tag-picker release artifact at ${standardTagPickerArtifact}; run npm run build and node scripts/build-v3-host-adapters.mjs before this suite`, { cause: error });
}
const readyForTags = (capabilities) => {
  if (!Array.isArray(capabilities) || capabilities.length !== 1 || capabilities[0] !== "tags") throw new Error(`unexpected standard component request: ${JSON.stringify(capabilities)}`);
  return Promise.resolve();
};
const html = `<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-sync-url="/api/admin/customer-sync-runs" data-tag-preview-url="/api/v1/customer-tag-commands/preview" data-tag-command-url="/api/v1/customer-tag-commands" data-tags-url="/api/admin/wecom/tags">
  <form id="customer-list-filters"><input name="keyword"><input name="phone"><select name="status"><option value=""></option></select></form>
  <button id="customer-list-clear"></button><button id="customer-list-refresh"></button>
  <span id="customer-list-summary"></span><div id="customer-list-state"></div>
  <div id="customer-list-table-wrap"><table><tbody id="customer-list-body"></tbody></table></div>
  <button id="customer-prev-page"></button><button id="customer-next-page"></button>
  <form id="customer-tag-batch"><select name="add_tag_ids" multiple disabled></select><select name="remove_tag_ids" multiple disabled></select><button type="submit">confirm</button><button id="customer-tag-batch-refresh" type="button" hidden>refresh</button></form><span id="customer-tag-batch-result"></span>
</div>`;
const dom = new JSDOM(html, { url: "https://test.invalid/admin/customers", runScripts: "outside-only" });
dom.window.Headers = Headers;
dom.window.AbortController = AbortController;
dom.window.document.cookie = "aicrm_admin_csrf=test-csrf; path=/";
dom.window.confirm = () => true;
dom.window.AdminDateTime = {};
dom.window.AdminFmt = { localTime: (value) => value === "2026-09-05T00:00:00Z" ? "2026-09-05 08:00:00" : "时间暂不可用", whenAdminDateTimeReady: (ready) => ready(dom.window.AdminDateTime) };
const tagCalls = [];
const pickerCalls = [];
let tagPreviewUnavailable = false;
let customerListReads = 0;
dom.window.AICRMStandardComponents = { ready: () => Promise.resolve(), readyFor: readyForTags };
dom.window.AICRMTagPicker = {
  createCatalogPageLoader: (source, reader) => async ({ signal }) => {
    const catalog = await reader({ signal });
    const resolved = (catalog.items || []).map((tag) => ({ source, tag_id: String(tag.id || tag.tag_id), group_id: String(tag.group_id), tag_name: String(tag.tag_name || tag.name), group_name: String(tag.group_name) }));
    return { items: resolved, resolved };
  },
  unresolvedRecord: (source, tagID) => ({ source, tag_id: String(tagID), group_id: "", tag_name: `标签 #${tagID}`, group_name: "目录状态待确认", unavailable_reason: "标签目录状态待确认" }),
  open: (options) => { pickerCalls.push(options); },
};
dom.window.fetch = async (input, options = {}) => {
  const url = new URL(String(input), dom.window.location.origin);
  if (url.pathname === "/api/admin/wecom/tags") return { ok: true, status: 200, json: async () => ({ read_model_status: "ready", groups: [{ group_id: 1, group_name: "分组" }], items: [{ id: 9, group_id: 1, group_name: "分组", tag_name: "标签九" }, { id: 10, group_id: 1, group_name: "分组", tag_name: "标签十" }], count: 2, total_tags: 2, tag_limit: 1000 }) };
  if (url.pathname === "/api/v1/customer-tag-commands/preview") { tagCalls.push({ path: url.pathname, options }); if (tagPreviewUnavailable) return { ok: false, status: 503, json: async () => ({ error: "provider_unavailable（上游错误）" }) }; return { ok: true, status: 200, json: async () => ({ state: "preview", lines: [{ customer_id: 42, state: "eligible" }] }) }; }
  if (url.pathname === "/api/v1/customer-tag-commands") { tagCalls.push({ path: url.pathname, options }); return { ok: true, status: 202, json: async () => ({ id: 7, state: "queued", lines: [{ customer_id: 42, state: "queued", effect_ref: "eer_7" }] }) }; }
  if (url.pathname === "/api/v1/customers/42/tag-commands") return { ok: true, status: 200, json: async () => ({ items: [{ id: 7, state: "executed", lines: [{ customer_id: 42, state: "executed" }] }] }) };
  if (url.pathname === "/api/admin/customers/42/tags") return { ok: true, status: 200, json: async () => ({ items: [{ name: "标签九", group_name: "分组", status: "active" }] }) };
  if (url.pathname !== "/api/admin/customers") throw new Error("unexpected request: " + url.pathname);
  customerListReads += 1;
  return { ok: true, status: 200, json: async () => ({ items: [{ customer_id: 42, display_name: "测试客户", oneid: "cus_42", phone_masked: "138****0000", last_synced_at: "2026-09-05T00:00:00Z" }], total: 1, total_is_estimate: false }) };
};
dom.window.eval(script);
await new Promise((resolve) => setTimeout(resolve, 20));

const links = [...dom.window.document.querySelectorAll("#customer-list-body a")];
if (!dom.window.document.querySelector("#customer-list-body")?.textContent.includes("2026-09-05 08:00:00")) throw new Error("customer timestamp did not use exact Shanghai seconds");
if (!links.some((link) => link.textContent === "查看档案" && link.getAttribute("href") === "/admin/customers/42")) throw new Error("existing customer profile entry was not preserved");
if (!links.some((link) => link.textContent === "会话存档" && link.getAttribute("href") === "/admin/message-archive/customers/42")) throw new Error("selected canonical customer did not receive a message archive entry");
const searchInput = dom.window.document.querySelector('#customer-list-filters [name="keyword"]');
searchInput.value = "候选客户";
searchInput.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, isComposing: true }));
await new Promise((resolve) => setTimeout(resolve, 20));
if (customerListReads !== 1) throw new Error("IME candidate Enter submitted the customer search");
searchInput.dispatchEvent(new dom.window.CompositionEvent("compositionstart", { bubbles: true }));
searchInput.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, keyCode: 229 }));
await new Promise((resolve) => setTimeout(resolve, 20));
if (customerListReads !== 1) throw new Error("Safari IME candidate Enter submitted the customer search");
searchInput.dispatchEvent(new dom.window.CompositionEvent("compositionend", { bubbles: true }));
searchInput.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, keyCode: 229 }));
await new Promise((resolve) => setTimeout(resolve, 20));
if (customerListReads !== 1) throw new Error("legacy IME keyCode 229 submitted the customer search");
searchInput.dispatchEvent(new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true, isComposing: false }));
await new Promise((resolve) => setTimeout(resolve, 20));
if (customerListReads !== 2) throw new Error("committed Enter did not submit the customer search");
for (const control of [dom.window.document.querySelector('#customer-list-refresh'), dom.window.document.querySelector('#customer-list-clear'), dom.window.document.querySelector('#customer-list-filters select[name="status"]')]) {
  const enter = new dom.window.KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true });
  control.dispatchEvent(enter);
  if (enter.defaultPrevented) throw new Error("customer text-search Enter guard intercepted a native control");
}
const checkbox = dom.window.document.querySelector('input[type="checkbox"]');
checkbox.checked = true;
checkbox.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
const tagForm = dom.window.document.querySelector("#customer-tag-batch");
const tagPickerButton = [...tagForm.querySelectorAll("button")].find((button) => button.textContent === "选择标签");
if (!tagPickerButton) throw new Error("customer tag draft did not mount the V3 picker entry");
tagPickerButton.click();
await new Promise((resolve) => setTimeout(resolve, 0));
if (pickerCalls.length !== 1 || pickerCalls[0].scope !== "customer.tag_draft" || pickerCalls[0].mode !== "multiple" || tagCalls.length !== 0) throw new Error("customer tag draft did not open the scoped V3 picker without sending a command");
if ([...tagForm.querySelector('[name="add_tag_ids"]').selectedOptions].length !== 0) throw new Error("opening then cancelling the V3 picker changed the existing customer tag draft");
pickerCalls[0].onCommit({ selected: [{ source: "local_tag_catalog", tag_id: "9", group_id: "1", tag_name: "标签九", group_name: "分组" }, { source: "local_tag_catalog", tag_id: "10", group_id: "1", tag_name: "标签十", group_name: "分组" }] });
if ([...tagForm.querySelector('[name="add_tag_ids"]').selectedOptions].map((option) => option.value).join(",") !== "9,10" || tagCalls.length !== 0) throw new Error("customer V3 picker did not retain the add-tag draft without sending");
for (const option of tagForm.querySelector('[name="add_tag_ids"]').options) option.selected = ["9", "10"].includes(option.value);
tagForm.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
await new Promise((resolve) => setTimeout(resolve, 20));
if (tagCalls.length !== 2 || tagCalls[0].path !== "/api/v1/customer-tag-commands/preview" || tagCalls[1].path !== "/api/v1/customer-tag-commands" || tagCalls[1].options.headers.get("X-CSRF-Token") !== "test-csrf") throw new Error("tag preview/confirm did not use the controlled Host contract");
const commandBody = JSON.parse(tagCalls[1].options.body);
if (commandBody.customer_ids[0] !== 42 || commandBody.add_tag_ids.join(",") !== "9,10" || commandBody.remove_tag_ids.length !== 0) throw new Error("tag command body was not canonical");
if (!dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("用户 #42：已执行") || dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("executed")) throw new Error("tag history refresh did not present the persisted per-user result in Chinese");
const refresh = dom.window.document.querySelector("#customer-tag-batch-refresh");
if (refresh.hidden) throw new Error("accepted tag command did not expose an explicit result refresh action");
refresh.click();
await new Promise((resolve) => setTimeout(resolve, 20));
if (!dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("观察标签：分组 / 标签九（已生效）") || dom.window.document.querySelector("#customer-tag-batch-result").textContent.includes("active")) throw new Error("explicit tag result refresh did not retain the observed-provider readback in Chinese");
tagPreviewUnavailable = true;
tagForm.dispatchEvent(new dom.window.Event("submit", { bubbles: true, cancelable: true }));
await new Promise((resolve) => setTimeout(resolve, 20));
const tagFailure = dom.window.document.querySelector("#customer-tag-batch-result").textContent || "";
if (!tagFailure.includes("标签服务暂不可用") || tagFailure.includes("provider_unavailable") || tagFailure.includes("上游错误")) throw new Error(`tag failure leaked technical detail: ${tagFailure}`);
dom.window.close();

const refreshDOM = new JSDOM(`<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-sync-url="/api/admin/customer-sync-runs" data-tag-preview-url="/api/v1/customer-tag-commands/preview" data-tag-command-url="/api/v1/customer-tag-commands" data-tags-url="/api/admin/wecom/tags">
  <form id="customer-list-filters"><input name="keyword"><input name="phone"><select name="status"><option value=""></option></select></form>
  <button id="customer-list-clear"></button><button id="customer-list-refresh"></button><span id="customer-list-summary"></span><div id="customer-list-state"></div><div id="customer-list-table-wrap"><table><tbody id="customer-list-body"></tbody></table></div><button id="customer-prev-page"></button><button id="customer-next-page"></button>
  <form id="customer-tag-batch"><select name="add_tag_ids" multiple disabled></select><select name="remove_tag_ids" multiple disabled></select><button type="submit">preview</button></form><span id="customer-tag-batch-result"></span>
</div>`, { url: "https://test.invalid/admin/customers", runScripts: "outside-only" });
refreshDOM.window.Headers = Headers;
refreshDOM.window.AbortController = AbortController;
refreshDOM.window.AdminDateTime = {};
refreshDOM.window.AdminFmt = { localTime: (value) => value, whenAdminDateTimeReady: (ready) => ready(refreshDOM.window.AdminDateTime) };
refreshDOM.window.AICRMStandardComponents = { ready: () => Promise.resolve(), readyFor: readyForTags };
const refreshPickerCalls = [];
refreshDOM.window.AICRMTagPicker = {
  createCatalogPageLoader: (source, reader) => async ({ signal }) => {
    const catalog = await reader({ signal });
    const resolved = (catalog.items || []).map((tag) => ({ source, tag_id: String(tag.id || tag.tag_id), group_id: String(tag.group_id), tag_name: String(tag.tag_name || tag.name), group_name: String(tag.group_name) }));
    return { items: resolved, resolved };
  },
  unresolvedRecord: (source, tagID) => ({ source, tag_id: String(tagID), group_id: "", tag_name: `标签 #${tagID}`, group_name: "目录状态待确认", unavailable_reason: "标签目录状态待确认" }),
  open: (options) => { refreshPickerCalls.push(options); },
};
let refreshedCatalog = false;
const refreshResponse = (payload, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => payload });
refreshDOM.window.fetch = async (input) => {
  const url = new URL(String(input), refreshDOM.window.location.origin);
  if (url.pathname === "/api/admin/wecom/tags") {
    const items = refreshedCatalog
      ? [{ id: 9, group_id: 1, group_name: "旧分组", tag_name: "A" }, { id: 10, group_id: 2, group_name: "新分组", tag_name: "B" }]
      : [{ id: 9, group_id: 1, group_name: "旧分组", tag_name: "A" }];
    return refreshResponse({ read_model_status: "ready", groups: refreshedCatalog ? [{ group_id: 1, group_name: "旧分组" }, { group_id: 2, group_name: "新分组" }] : [{ group_id: 1, group_name: "旧分组" }], items, count: items.length, total_tags: items.length, tag_limit: 1000 });
  }
  if (url.pathname === "/api/admin/customers") return refreshResponse({ items: [], total: 0, total_is_estimate: false });
  throw new Error("unexpected refresh fixture request: " + url.pathname);
};
refreshDOM.window.eval(script);
await new Promise((resolve) => setTimeout(resolve, 20));
const refreshForm = refreshDOM.window.document.getElementById("customer-tag-batch");
const refreshButton = [...refreshForm.querySelectorAll("button")].find((button) => button.textContent === "选择标签");
if (!refreshButton || [...refreshForm.querySelector('[name="add_tag_ids"]').options].map((option) => option.value).join(",") !== "9") throw new Error("customer tag draft did not retain its initial catalog");
refreshedCatalog = true;
refreshButton.click();
await new Promise((resolve) => setTimeout(resolve, 0));
const refreshedPage = await refreshPickerCalls[0].loadPage({ query: "", signal: new AbortController().signal });
const refreshedB = refreshedPage.resolved.find((tag) => tag.tag_id === "10");
refreshPickerCalls[0].onCommit({ selected: [refreshedB] });
if (new refreshDOM.window.FormData(refreshForm).getAll("add_tag_ids").join(",") !== "10") throw new Error("refreshed catalog tag B was not retained in the original customer form draft");
refreshButton.click();
await new Promise((resolve) => setTimeout(resolve, 0));
if (refreshPickerCalls[1].selectedRecords.map((tag) => tag.tag_id).join(",") !== "10") throw new Error("refreshed catalog tag B was not retained when reopening the customer picker");
refreshDOM.window.close();

const concurrencyDOM = new JSDOM(`<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-tag-preview-url="/api/v1/customer-tag-commands/preview" data-tags-url="/api/admin/wecom/tags">
  <form id="customer-list-filters"><input name="keyword"><input name="phone"><select name="status"><option value=""></option></select></form>
  <button id="customer-list-clear"></button><button id="customer-list-refresh"></button>
  <span id="customer-list-summary"></span><div id="customer-list-state"></div>
  <div id="customer-list-table-wrap"><table><tbody id="customer-list-body"></tbody></table></div>
  <button id="customer-prev-page"></button><button id="customer-next-page"></button>
  <form id="customer-tag-batch"><select name="add_tag_ids" multiple disabled></select><select name="remove_tag_ids" multiple disabled></select><button type="submit">confirm</button></form><span id="customer-tag-batch-result"></span>
</div>`, { url: "https://test.invalid/admin/customers", runScripts: "outside-only" });
concurrencyDOM.window.Headers = Headers;
concurrencyDOM.window.AbortController = AbortController;
concurrencyDOM.window.document.cookie = "aicrm_admin_csrf=test-csrf; path=/";
concurrencyDOM.window.confirm = () => false;
concurrencyDOM.window.AdminDateTime = {};
concurrencyDOM.window.AdminFmt = { localTime: (value) => value, whenAdminDateTimeReady: (ready) => ready(concurrencyDOM.window.AdminDateTime) };
concurrencyDOM.window.AICRMStandardComponents = { ready: () => Promise.resolve(), readyFor: readyForTags };
concurrencyDOM.window.AICRMTagPicker = {
  createCatalogPageLoader: (source, reader) => async ({ signal }) => {
    const catalog = await reader({ signal });
    const resolved = (catalog.items || []).map((tag) => ({ source, tag_id: String(tag.id || tag.tag_id), group_id: String(tag.group_id || 1), tag_name: String(tag.tag_name || tag.name), group_name: String(tag.group_name) }));
    return { items: resolved, resolved };
  },
  unresolvedRecord: (source, tagID) => ({ source, tag_id: String(tagID), group_id: "", tag_name: `标签 #${tagID}`, group_name: "目录状态待确认" }),
  open: () => {},
};
const listRequests = [];
let crossPagePreview = null;
const response = (payload, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => payload });
const customerPage = (customerID, total, nextCursor, label) => response({
  items: [{ customer_id: customerID, display_name: label, oneid: "cus_" + customerID, phone_masked: "138****0000", last_synced_at: "2026-09-05T00:00:00Z" }],
  total,
  total_is_estimate: false,
  next_cursor: nextCursor,
});
concurrencyDOM.window.fetch = (input, options = {}) => {
  const url = new URL(String(input), concurrencyDOM.window.location.origin);
  if (url.pathname === "/api/admin/wecom/tags") return Promise.resolve(response({ items: [{ id: 9, group_name: "分组", tag_name: "标签九" }] }));
  if (url.pathname === "/api/v1/customer-tag-commands/preview") {
    crossPagePreview = JSON.parse(options.body);
    return Promise.resolve(response({ lines: [{ customer_id: 1, state: "eligible" }, { customer_id: 2, state: "eligible" }] }));
  }
  if (url.pathname !== "/api/admin/customers") return Promise.reject(new Error("unexpected request: " + url.pathname));
  return new Promise((resolve, reject) => { listRequests.push({ url, options, resolve, reject }); });
};
const settle = async () => {
  await new Promise((resolve) => setTimeout(resolve, 0));
  for (let index = 0; index < 4; index += 1) await Promise.resolve();
};
const latestListRequest = () => listRequests[listRequests.length - 1];
const customerCheckbox = () => concurrencyDOM.window.document.querySelector('#customer-list-body input[type="checkbox"]');
const customerForm = concurrencyDOM.window.document.getElementById("customer-list-filters");
const customerRefresh = concurrencyDOM.window.document.getElementById("customer-list-refresh");
const customerPrevious = concurrencyDOM.window.document.getElementById("customer-prev-page");
const customerNext = concurrencyDOM.window.document.getElementById("customer-next-page");

concurrencyDOM.window.eval(script);
await settle();
if (listRequests.length !== 1) throw new Error("customer concurrency contract did not issue its initial list request");
latestListRequest().resolve(customerPage(1, 2, "page-2", "第一页客户"));
await settle();
customerCheckbox().checked = true;
customerCheckbox().dispatchEvent(new concurrencyDOM.window.Event("change", { bubbles: true }));

customerForm.dispatchEvent(new concurrencyDOM.window.Event("submit", { bubbles: true, cancelable: true }));
await settle();
latestListRequest().resolve(customerPage(1, 2, "page-2", "第一页客户"));
await settle();
if (!customerCheckbox().checked) throw new Error("same-filter query cleared the existing customer selection");

customerNext.click();
await settle();
const secondPage = latestListRequest();
if (secondPage.url.searchParams.get("cursor") !== "page-2") throw new Error("next page did not retain the server cursor");
if (!customerRefresh.disabled || !customerPrevious.disabled || !customerNext.disabled || concurrencyDOM.window.document.querySelector("[data-customer-directory-root]").getAttribute("aria-busy") !== "true") throw new Error("pagination controls were not held busy during an in-flight page request");
secondPage.resolve(customerPage(2, 2, "page-3", "第二页客户"));
await settle();
if (customerPrevious.hidden) throw new Error("second page did not expose previous-page navigation");
customerCheckbox().checked = true;
customerCheckbox().dispatchEvent(new concurrencyDOM.window.Event("change", { bubbles: true }));

customerRefresh.click();
await settle();
const pageTwoRefresh = latestListRequest();
if (pageTwoRefresh.url.searchParams.get("cursor") !== "page-2") throw new Error("refresh did not retain the committed second-page cursor");
pageTwoRefresh.resolve(customerPage(2, 2, "page-3", "第二页客户"));
await settle();
if (!customerCheckbox().checked) throw new Error("same-filter refresh cleared the cross-page selection");
const addTags = concurrencyDOM.window.document.querySelector('[name="add_tag_ids"]');
addTags.options[0].selected = true;
concurrencyDOM.window.document.getElementById("customer-tag-batch").dispatchEvent(new concurrencyDOM.window.Event("submit", { bubbles: true, cancelable: true }));
await settle();
if (!crossPagePreview || crossPagePreview.customer_ids.join(",") !== "1,2") throw new Error("cross-page selection was not preserved for the existing tag preview contract");

customerRefresh.click();
await settle();
const obsoleteRequest = latestListRequest();
if (obsoleteRequest.url.searchParams.get("cursor") !== "page-2") throw new Error("second-page refresh did not use its committed cursor");
customerForm.querySelector('[name="keyword"]').value = "new";
customerForm.dispatchEvent(new concurrencyDOM.window.Event("submit", { bubbles: true, cancelable: true }));
await settle();
const failedFilterReset = latestListRequest();
if (!obsoleteRequest.options.signal.aborted) throw new Error("a superseded list request was not aborted");
obsoleteRequest.reject(new Error("late network failure"));
await settle();
if (!customerRefresh.disabled || !customerPrevious.disabled || !customerNext.disabled || concurrencyDOM.window.document.querySelector("[data-customer-directory-root]").getAttribute("aria-busy") !== "true" || !concurrencyDOM.window.document.getElementById("customer-list-state").textContent.includes("正在加载用户")) throw new Error("a rejected superseded request changed the latest loading state");
failedFilterReset.resolve(response({ error: "temporary_failure" }, 503));
await settle();
if (!concurrencyDOM.window.document.getElementById("customer-list-state").textContent.includes("用户列表暂时不可用") || customerRefresh.disabled || !customerPrevious.disabled || !customerNext.disabled) throw new Error("failed filter reset did not leave only its retry available");
const requestCountBeforeDisabledNavigation = listRequests.length;
customerPrevious.dispatchEvent(new concurrencyDOM.window.Event("click", { bubbles: true, cancelable: true }));
customerNext.dispatchEvent(new concurrencyDOM.window.Event("click", { bubbles: true, cancelable: true }));
await settle();
if (listRequests.length !== requestCountBeforeDisabledNavigation) throw new Error("a failed filter reset allowed an old page cursor to navigate the new filter");
customerForm.querySelector('[name="keyword"]').value = "unsubmitted";
customerRefresh.click();
await settle();
const retry = latestListRequest();
if (retry.url.searchParams.get("keyword") !== "new" || retry.url.searchParams.has("cursor")) throw new Error("retry combined unsubmitted fields or an old cursor with the committed filter");
retry.resolve(customerPage(1, 7, "new-page-2", "重试客户"));
await settle();
if (customerCheckbox().checked || !concurrencyDOM.window.document.getElementById("customer-list-body").textContent.includes("重试客户") || customerRefresh.disabled || concurrencyDOM.window.document.querySelector("[data-customer-directory-root]").getAttribute("aria-busy") !== "false") throw new Error("retry did not restore the latest list, controls, and filter-scoped selection");

customerRefresh.click();
await settle();
const lateSuccess = latestListRequest();
customerForm.querySelector('[name="keyword"]').value = "latest";
customerForm.dispatchEvent(new concurrencyDOM.window.Event("submit", { bubbles: true, cancelable: true }));
await settle();
const currentRequest = latestListRequest();
lateSuccess.resolve(customerPage(2, 99, "stale-page-2", "过期客户"));
await settle();
if (!customerRefresh.disabled || concurrencyDOM.window.document.querySelector("[data-customer-directory-root]").getAttribute("aria-busy") !== "true" || concurrencyDOM.window.document.getElementById("customer-list-summary").textContent !== "共 7 位用户") throw new Error("a late aborted success changed the current loading state");
currentRequest.resolve(customerPage(3, 8, "latest-page-2", "最新筛选客户"));
await settle();
if (!concurrencyDOM.window.document.getElementById("customer-list-body").textContent.includes("最新筛选客户") || concurrencyDOM.window.document.getElementById("customer-list-summary").textContent !== "共 8 位用户" || customerRefresh.disabled) throw new Error("latest response did not replace the stale list state");
concurrencyDOM.window.close();

{
const tagRetryDom = new JSDOM(`<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-tags-url="/api/admin/wecom/tags">
  <form id="customer-list-filters"><input name="keyword"><input name="phone"><select name="status"><option value=""></option></select></form>
  <button id="customer-list-clear"></button><button id="customer-list-refresh"></button>
  <span id="customer-list-summary"></span><div id="customer-list-state"></div><div id="customer-list-table-wrap"><table><tbody id="customer-list-body"></tbody></table></div>
  <button id="customer-prev-page"></button><button id="customer-next-page"></button>
  <form id="customer-tag-batch"><select name="add_tag_ids" multiple disabled></select><select name="remove_tag_ids" multiple disabled></select></form>
</div>`, { url: "https://test.invalid/admin/customers", runScripts: "outside-only" });
tagRetryDom.window.Headers = Headers;
tagRetryDom.window.AdminDateTime = {};
tagRetryDom.window.AdminFmt = { localTime: (value) => value, whenAdminDateTimeReady: (ready) => ready(tagRetryDom.window.AdminDateTime) };
let tagCatalogReads = 0;
let tagAssetAttempts = 0;
let tagCommandCalls = 0;
tagRetryDom.window.fetch = async (input) => {
  const url = new URL(String(input), tagRetryDom.window.location.origin);
  if (url.pathname === "/api/admin/wecom/tags") {
    tagCatalogReads += 1;
    return { ok: true, status: 200, json: async () => ({
      read_model_status: "ready",
      groups: [{ group_id: 1, group_name: "分组" }],
      items: [{ id: 9, group_id: 1, group_name: "分组", tag_name: "标签九" }],
      count: 1,
      total_tags: 1,
      tag_limit: 1000,
    }) };
  }
  if (url.pathname === "/api/admin/customers") return { ok: true, status: 200, json: async () => ({ items: [], total: 0 }) };
  if (url.pathname === "/api/admin/customer-sync-runs") return { ok: true, status: 200, json: async () => ({ items: [] }) };
  if (url.pathname.startsWith("/api/v1/customer-tag-commands")) tagCommandCalls += 1;
  throw new Error("unexpected request: " + url.pathname);
};
const appendTagRetryScript = tagRetryDom.window.document.head.append.bind(tagRetryDom.window.document.head);
tagRetryDom.window.document.head.append = (...nodes) => {
  appendTagRetryScript(...nodes);
  for (const node of nodes) {
    if (!(node instanceof tagRetryDom.window.HTMLScriptElement) || !node.dataset.aicrmStandardComponent) continue;
    setTimeout(() => {
      tagAssetAttempts += 1;
      if (tagAssetAttempts === 1) node.dispatchEvent(new tagRetryDom.window.Event("error"));
      else {
        tagRetryDom.window.eval(standardTagPicker);
        node.dispatchEvent(new tagRetryDom.window.Event("load"));
      }
    }, 0);
  }
};
const waitForTagRetryState = async (predicate, message) => {
  const deadline = Date.now() + 1000;
  while (!predicate()) {
    if (Date.now() >= deadline) throw new Error(message);
    await new Promise((resolve) => setTimeout(resolve, 0));
  }
};
tagRetryDom.window.eval(standardHost);
tagRetryDom.window.eval(script);
await waitForTagRetryState(
  () => Boolean(tagRetryDom.window.document.querySelector("[data-customer-tag-loader-retry]")),
  "tag asset failure did not expose a retry action",
);
const retry = tagRetryDom.window.document.querySelector("[data-customer-tag-loader-retry]");
if (!retry || !tagRetryDom.window.document.querySelector("[role=alert]")?.textContent.includes("标签目录暂不可用") || !tagRetryDom.window.document.querySelector('[name="add_tag_ids"]').disabled) throw new Error("tag asset failure did not expose a local retry state");
retry.click();
retry.click();
await waitForTagRetryState(
  () => tagAssetAttempts === 2 && tagCatalogReads === 1 && !tagRetryDom.window.document.querySelector("[data-customer-tag-loader-error]") && !tagRetryDom.window.document.querySelector('[name="add_tag_ids"]').disabled,
  "tag asset retry did not complete the V3 selector recovery",
);
if (tagAssetAttempts !== 2 || tagCatalogReads !== 1 || tagCommandCalls !== 0) throw new Error("tag asset retry did not remain a single-flight GET/asset-only recovery");
if (tagRetryDom.window.document.querySelector("[data-customer-tag-loader-error]") || tagRetryDom.window.document.querySelector('[name="add_tag_ids"]').disabled) throw new Error("successful tag retry did not clear the local error and restore the native control");
if ([...tagRetryDom.window.document.querySelectorAll("button")].filter((button) => button.textContent === "选择标签").length !== 2) throw new Error("tag retry duplicated picker buttons");
if (typeof tagRetryDom.window.AICRMTagPicker?.open !== "function") throw new Error("tag retry did not restore the V3 tag picker adapter");
if (!tagRetryDom.window.document.querySelector("#customer-list-summary")?.textContent.includes("共 0 位用户")) throw new Error("tag asset failure blocked the independent customer list read");
tagRetryDom.window.close();
}

const detailDom = new JSDOM(`<!doctype html>
<div data-customer-directory-root data-customers-url="/api/admin/customers" data-sync-url="/api/admin/customer-sync-runs" data-tag-preview-url="/api/v1/customer-tag-commands/preview" data-tag-command-url="/api/v1/customer-tag-commands" data-tags-url="/api/admin/wecom/tags">
  <div id="customer-page-alert"></div><div id="customer-profile-name"></div>
  <div id="customer-detail-state"></div><div id="customer-detail-content" hidden></div>
  <div id="customer-detail-fields"></div><div id="customer-profile-meta"></div>
  <div id="customer-phone-ephemeral" hidden></div>

  <div id="customer-360-sections" hidden><div id="customer-360-main"></div><div id="customer-360-sidebar"></div></div>
</div>`, { url: "https://test.invalid/admin/customers/42", runScripts: "outside-only" });
detailDom.window.Headers = Headers;
detailDom.window.AbortController = AbortController;
detailDom.window.AdminDateTime = {};
detailDom.window.AdminFmt = { localTime: (value) => value === "2026-09-05T00:00:00Z" ? "2026-09-05 08:00:00" : "时间暂不可用", whenAdminDateTimeReady: (ready) => ready(detailDom.window.AdminDateTime) };
const detailPickerCalls = [];
detailDom.window.AICRMStandardComponents = { ready: () => Promise.resolve(), readyFor: readyForTags };
detailDom.window.AICRMTagPicker = {
  createCatalogPageLoader: (source, reader) => async ({ signal }) => {
    const catalog = await reader({ signal });
    const resolved = (catalog.items || []).map((tag) => ({ source, tag_id: String(tag.id || tag.tag_id), group_id: String(tag.group_id), tag_name: String(tag.tag_name || tag.name), group_name: String(tag.group_name) }));
    return { items: resolved, resolved };
  },
  unresolvedRecord: (source, tagID) => ({ source, tag_id: String(tagID), group_id: "", tag_name: `标签 #${tagID}`, group_name: "目录状态待确认", unavailable_reason: "标签目录状态待确认" }),
  open: (options) => { detailPickerCalls.push(options); },
};
detailDom.window.fetch = async (input) => {
  const url = new URL(String(input), detailDom.window.location.origin);
  if (url.pathname === "/api/admin/wecom/tags") return { ok: true, status: 200, json: async () => ({ read_model_status: "ready", groups: [{ group_id: 1, group_name: "分组" }], items: [{ id: 9, group_id: 1, group_name: "分组", tag_name: "标签九" }], count: 1, total_tags: 1, tag_limit: 1000 }) };
  if (url.pathname !== "/api/admin/customers/42/360") throw new Error("unexpected detail request: " + url.pathname);
  return { ok: true, status: 200, json: async () => ({
    profile: { status: "ready", data: { customer_id: 42, customer_number: "1000042", display_name: "测试客户", oneid: "cus_42", status: "active", contact_type: 1, last_synced_at: "2026-09-05T00:00:00Z" } },
    identity_summary: { status: "ready", data: { identities: [], phones: [] } },
    order_summary: { status: "ready", data: { total: 1, paid: 1, refunded: 0, failed: 0, recent: [{ id: 71, merchant_order_no: "MO-71", status: "paid", provider:"wechat_pay", items:[{product_name:"课程商品"}] }] } },
    questionnaire_summary: { status: "ready", data: { total: 2, recent: [{ id: 81, title: "首份问卷", assessment_label: "已完成", submitted_at: "2026-09-05T00:00:00Z" }, { id: 82, title: "后续问卷", score: 0, submitted_at: "2026-09-05T00:00:00Z" }] } },
    risk: { status: "ready", data: { level: "low", reasons: [] } },
    recent_touchpoints: { status: "ready", data: [{ id: 91, title: "首次触达", source_domain: "customer", occurred_at: "2026-09-05T00:00:00Z" }, { id: 92, title: "后续触达", source_domain: "order", occurred_at: "2026-09-05T00:00:00Z" }, { id: 93, title: "历史触点", source_domain: "legacy_import", occurred_at: "2026-09-05T00:00:00Z" }] },
  }) };
};
detailDom.window.eval(script);
await new Promise((resolve) => setTimeout(resolve, 20));
const detailText = detailDom.window.document.getElementById("customer-360-main")?.textContent || "";
if (!detailText.includes("订单总数1") || !detailText.includes("退款相关0") || !detailText.includes("MO-71") || !detailText.includes("已支付") || detailText.includes("paid")) throw new Error(`customer record table did not retain known facts without a machine status: ${detailText}`);
if (!detailDom.window.document.body.textContent.includes("1000042") || !detailText.includes("课程商品") || !detailDom.window.document.querySelector('a[href="/admin/orderDetail.html?id=MO-71&provider=wechat"]')) throw new Error("profile numbering/product/order link missing");
const detailMetaText = detailDom.window.document.getElementById("customer-profile-meta")?.textContent || "";
if (detailMetaText !== "") throw new Error(`customer contact type was not rendered as an Owner-defined business label: ${detailMetaText}`);
if (!detailText.includes("待确认")) throw new Error(`missing order time was presented as a known value: ${detailText}`);
if (!detailText.includes("首份问卷") || !detailText.includes("后续问卷") || !detailText.includes("评分 0")) throw new Error(`questionnaire records were truncated or an actual zero score was hidden: ${detailText}`);
const touchpointText = detailDom.window.document.getElementById("customer-360-sidebar")?.textContent || "";
if (!touchpointText.includes("首次触达") || !touchpointText.includes("后续触达") || !touchpointText.includes("历史触点") || !touchpointText.includes("用户档案") || !touchpointText.includes("交易") || !touchpointText.includes("其他（legacy_import）")) throw new Error(`approved touchpoint records were truncated or source labels leaked: ${touchpointText}`);
if (touchpointText.includes("风险摘要")) throw new Error("profile retained the retired risk card");
detailDom.window.close();
console.log("admin-customers-browser: PASS");

{
  const shellDOM = new JSDOM('<body><aside>用户管理后台</aside><header class="admin-topbar"><h1>交易管理</h1></header><main id="stage"><div><div><span>客户管理后台</span><span>/</span><span>交易</span></div><div>交易管理</div><button>刷新</button></div></main></body>', {runScripts:'outside-only', url:'https://example.test/admin/orders'});
  const shell = fs.readFileSync(new URL('./admin_shell.js', import.meta.url), 'utf8');
  shellDOM.window.eval(shell);
  await new Promise(resolve=>setTimeout(resolve,0));
  if (shellDOM.window.document.querySelector('#stage').textContent !== '交易管理刷新') throw new Error('donor breadcrumb remains or title/actions removed');
  if (!shellDOM.window.document.querySelector('aside').textContent.includes('用户管理后台')) throw new Error('sidebar brand changed');
  shellDOM.window.close();
}
