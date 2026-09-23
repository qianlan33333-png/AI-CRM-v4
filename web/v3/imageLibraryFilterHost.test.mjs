import assert from "node:assert/strict";
import { JSDOM, VirtualConsole } from "jsdom";
import { fileURLToPath } from "node:url";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const sleep = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => new Response(JSON.stringify(body), {
  status,
  headers: { "Content-Type": "application/json" },
});
const imageHost = await buildTestBrowserBundle(fileURLToPath(new URL("./imageLibraryFilterHost.ts", import.meta.url)));
const materialHost = await buildTestBrowserBundle(fileURLToPath(new URL("./materialSaveAdapter.ts", import.meta.url)));

function item(id, name, enabled = true) {
  return {
    id,
    name,
    file_name: `${name}.png`,
    mime_type: "image/png",
    file_size: 32,
    description: "素材说明",
    tags: ["回归"],
    category: "海报",
    width: 160,
    height: 90,
    enabled,
    created_at: "2026-09-12T00:00:00Z",
    original_url: `/api/admin/image-library/${id}/variants/original`,
    thumb_320_url: `/api/admin/image-library/${id}/variants/thumb_320`,
  };
}

const first = item(11, "默认启用素材");
const secondPage = item(31, "第二页素材");
const fresh = item(12, "新的搜索结果");
const stale = item(13, "旧的搜索结果");
const inactive = item(14, "已停用素材", false);
const confirmedDelete = item(41, "已删除但列表回读失败素材");
const calls = [];
let materialRefreshReads = 0;
let releaseStale;
let releaseDialogRead;
let releaseMutationReadback;
let defaultName = first.name;
let failSecondPage = false;
let failNextReadback = false;
let loseFirstDeleteResponse = false;
let imageDeleted = false;
let finalPageOnly = false;
let finalPageDeleted = false;
let finalDeleteAttempt = 0;
let delayNextMutationReadback = false;
let confirmedDeleteScenario = false;
let confirmedDeleteCommitted = false;
let failConfirmedDeleteReadback = false;

const virtualConsole = new VirtualConsole();
virtualConsole.forwardTo(console);
const dom = new JSDOM(`<!doctype html><html><body data-page="images"><header class="admin-topbar"><div class="admin-topbar-head"><h1>素材库</h1></div><div class="admin-topbar-meta"></div></header><main id="stage" data-image-library-v3-root data-material-library-workspace="true"></main><script>${materialHost}</script><script>${imageHost}</script></body></html>`, {
  url: "https://test.invalid/admin/materials?tab=images",
  runScripts: "dangerously",
  pretendToBeVisual: true,
  virtualConsole,
  beforeParse(window) {
    window.Response = Response;
    window.Headers = Headers;
    window.Request = Request;
    window.confirm = () => true;
    window.fetch = async (input, init = {}) => {
      const raw = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
      const url = new URL(raw, window.location.origin);
      const method = String(init.method || (typeof input === "string" || input instanceof URL ? "GET" : input.method)).toUpperCase();
      calls.push({ path: url.pathname, query: url.searchParams.toString(), method, body: init.body, headers: Object.fromEntries(new Headers(init.headers).entries()) });
      if(url.pathname==='/api/admin/image-library/groups')return json({can_write:true,items:[{id:0,name:'',version:0,count:0},{id:1,name:'海报',version:1,count:1}]});
      if(url.pathname==='/api/admin/image-library/group-members')return json({items:url.searchParams.get('ids').split(',').map(id=>({id:Number(id),version:1,group_id:1,category:'海报'}))});
      if (url.pathname === "/api/admin/media-preparations") {
        materialRefreshReads += 1;
        return json({ items: [], failures: [], done: true, next_cursor: "" });
      }
      if (url.pathname === "/api/admin/image-library" && method === "GET") {
        const query = url.searchParams.get("q") || "";
        const offset = Number(url.searchParams.get("offset") || "0");
        const enabledOnly = url.searchParams.get("enabled_only");
        if (query === "旧") return new Promise((resolve) => { releaseStale = () => resolve(json({ items: [stale], total: 1, limit: 20, offset, has_more: false })); });
        if (query === "弹窗延迟") return new Promise((resolve) => { releaseDialogRead = () => resolve(json({ items: [fresh], total: 1, limit: 20, offset, has_more: false })); });
        if (query === "失败") return json({ code: "unavailable" }, 503);
        if (query === "服务错误") return json({ code: "DEPENDENCY_UNAVAILABLE", message: "postgres connection refused" }, 503);
        if (query === "网络错误") throw new window.TypeError("Failed to fetch image-library internal endpoint");
        if (query === "空") return json({ items: [], total: 0, limit: 20, offset, has_more: false });
        if (query === "新") return json({ items: enabledOnly === "false" ? [fresh, inactive] : [fresh], total: enabledOnly === "false" ? 2 : 1, limit: 20, offset, has_more: false });
        if (delayNextMutationReadback) {
          delayNextMutationReadback = false;
          return new Promise((resolve) => {
            releaseMutationReadback = () => resolve(json({ items: [item(11, defaultName)], total: 41, limit: 20, offset, has_more: true }));
          });
        }
        if (confirmedDeleteScenario) {
          if (failConfirmedDeleteReadback) {
            failConfirmedDeleteReadback = false;
            return json({ code: "readback_unavailable" }, 503);
          }
          return json({
            items: confirmedDeleteCommitted ? [] : [confirmedDelete],
            total: confirmedDeleteCommitted ? 0 : 1,
            limit: 20,
            offset,
            has_more: false,
          });
        }
        if (finalPageOnly) {
          if (offset === 20) return json({ items: finalPageDeleted ? [] : [secondPage], total: finalPageDeleted ? 20 : 21, limit: 20, offset, has_more: false });
          return json({ items: [fresh], total: finalPageDeleted ? 20 : 21, limit: 20, offset, has_more: !finalPageDeleted });
        }
        if (failNextReadback) {
          failNextReadback = false;
          return json({ code: "readback_unavailable" }, 503);
        }
        if (offset === 20 && failSecondPage) return json({ code: "page_unavailable" }, 503);
        if (offset === 20) return json({ items: [secondPage], total: 41, limit: 20, offset, has_more: true });
        return json({ items: imageDeleted ? [] : enabledOnly === "false" ? [item(11, defaultName), inactive] : [item(11, defaultName)], total: imageDeleted ? 0 : 41, limit: 20, offset, has_more: !imageDeleted });
      }
      if (url.pathname === "/api/admin/image-library/11" && method === "GET") {
        return imageDeleted ? json({ code: "not_found" }, 404) : json({ ok: true, item: item(11, defaultName) });
      }
      if (url.pathname === "/api/admin/image-library/31" && method === "GET") {
        return finalPageDeleted ? json({ code: "not_found" }, 404) : json({ ok: true, item: secondPage });
      }
      if (url.pathname === "/api/admin/image-library/41" && method === "GET") {
        return confirmedDeleteCommitted ? json({ code: "not_found" }, 404) : json({ ok: true, item: confirmedDelete });
      }
      if (url.pathname === "/api/admin/image-library/11" && method === "PUT") {
        const body = JSON.parse(String(init.body));
        defaultName = body.name;
        return json({ ok: true, item: item(11, defaultName) });
      }
      if (url.pathname === "/api/admin/image-library/11" && method === "DELETE") {
        if (loseFirstDeleteResponse) {
          loseFirstDeleteResponse = false;
          throw new window.TypeError("delete response lost");
        }
        imageDeleted = true;
        return json({ ok: true });
      }
      if (url.pathname === "/api/admin/image-library/31" && method === "DELETE") {
        finalDeleteAttempt += 1;
        if (finalDeleteAttempt === 1) return json({ code: "unavailable" }, 503);
        if (finalDeleteAttempt === 2) throw new window.TypeError("delete response lost before commit");
        finalPageDeleted = true;
        return json({ ok: true });
      }
      if (url.pathname === "/api/admin/image-library/41" && method === "DELETE") {
        confirmedDeleteCommitted = true;
        failConfirmedDeleteReadback = true;
        throw new window.TypeError("delete response lost after commit");
      }
      return json({ code: "unexpected", path: url.pathname, method }, 500);
    };
  },
});

async function waitFor(predicate, label) {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (predicate()) return;
    await sleep(10);
  }
  throw new Error(`image-library V3 Host regression: ${label}; stage=${dom.window.document.getElementById("stage")?.textContent?.trim()}; calls=${JSON.stringify(calls)}`);
}

function controls() {
  const input = dom.window.document.querySelector('input[data-image-library-query="true"]');
  const includeInactive = dom.window.document.querySelector('input[data-image-library-include-inactive="true"]');
  const reset = dom.window.document.querySelector('button[data-image-library-reset="true"]');
  assert.ok(input instanceof dom.window.HTMLInputElement, "source-owned image query input was not mounted");
  assert.ok(includeInactive instanceof dom.window.HTMLInputElement, "source-owned inactive checkbox was not mounted");
  assert.ok(reset instanceof dom.window.HTMLButtonElement, "source-owned reset was not mounted");
  return { input, includeInactive, reset };
}

function commitSearch(input) {
  input.dispatchEvent(new dom.window.KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "Enter", code: "Enter" }));
}

await waitFor(() => Boolean(dom.window.document.querySelector('input[data-image-library-query="true"]')), "Host did not mount");
assert.equal(dom.window.document.querySelector('[data-material-refresh="true"]'), null, "operator library must not mount credential diagnostics");
assert.ok([...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "刷新"), "normal library refresh must be available");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query === "limit=20&offset=0&enabled_only=true"),
  `default image-library read did not explicitly request a bounded enabled-only page: ${JSON.stringify(calls)}`,
);
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "default enabled image was not rendered");
assert.ok(dom.window.document.body.textContent.includes("已启用"), "enabled image did not render a Chinese status");
assert.ok(dom.window.document.body.textContent.includes("2026-09-12 08:00:00"), "image time did not render in Asia/Shanghai YYYY-MM-DD HH:mm:ss form");
assert.ok(!dom.window.document.body.textContent.includes("2026-09-12T00:00:00Z"), "raw ISO time leaked into the image workspace");
const directoryHeaders = [...dom.window.document.querySelectorAll('[data-image-library-directory] th')].map((node) => node.textContent?.trim());
assert.deepEqual(directoryHeaders, ["图片 / 名称", "所属分组", "大小", "上传时间", "状态", "操作"], "image directory keeps dimensions and tags with the compact image identity");
assert.match(dom.window.document.body.textContent || "", /160 × 90 · 默认启用素材\.png · 海报 · 回归/, "image dimensions, filename, and existing tags render beneath the name");
const initialThumbnail = dom.window.document.querySelector('[data-image-library-thumbnail="true"]');
const initialImage = initialThumbnail?.querySelector('img');
assert.equal(initialThumbnail?.dataset.materialThumbnailState, "loading", "source-owned card exposes the shared thumbnail loading state");
assert.equal(initialImage?.style.objectFit, "contain", "image directory thumbnail preserves complete landscape and portrait sources");
initialImage?.dispatchEvent(new dom.window.Event('error'));
assert.equal(initialThumbnail?.dataset.materialThumbnailState, "error", "a source-owned card keeps the shared thumbnail error state");
assert.match(initialThumbnail?.textContent || "", /预览不可用/, "image directory error has a visible fallback");

let current = controls();
current.input.value = "旧";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
current.input.dispatchEvent(new dom.window.FocusEvent("blur", { bubbles: true }));
await sleep(300);
assert.equal(releaseStale, undefined, "typing or blurring an image query did not schedule a directory read");
current.input.dispatchEvent(new dom.window.CompositionEvent("compositionstart", { bubbles: true }));
current.input.dispatchEvent(new dom.window.CompositionEvent("compositionend", { bubbles: true }));
const imageCandidateEnter = new dom.window.KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "Enter" });
Object.defineProperty(imageCandidateEnter, "keyCode", { value: 229 });
current.input.dispatchEvent(imageCandidateEnter);
assert.equal(imageCandidateEnter.defaultPrevented, false, "an image-search IME candidate Enter stays with the browser");
assert.equal(releaseStale, undefined, "an image-search IME candidate Enter did not schedule a directory read");
await sleep(0);
commitSearch(current.input);
await waitFor(() => typeof releaseStale === "function", "debounced first search did not start");
const focusedQuery = current.input;
focusedQuery.focus();
current.input.value = "新";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => dom.window.document.body.textContent.includes("新的搜索结果"), "newer search result did not render");
assert.equal(dom.window.document.querySelector('[data-image-library-query="true"]'), focusedQuery, "debounced search rebuilt the focused query input");
assert.equal(dom.window.document.activeElement, focusedQuery, "debounced search lost query focus");
assert.equal(focusedQuery.value, "新", "debounced search lost the current query text");
releaseStale();
await sleep(30);
assert.ok(!dom.window.document.body.textContent.includes("旧的搜索结果"), "stale search response overwrote the newer result");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=true")),
  "search did not send q with enabled_only=true",
);

current = controls();
current.input.value = "弹窗延迟";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => typeof releaseDialogRead === "function", "delayed dialog read did not start");
const upload = dom.window.document.querySelector('[data-page-header-actions="image-library"] button');
assert.ok(upload?.textContent === "上传图片", "upload action missing from the one shared page header");
upload.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgUpFile")), "upload dialog did not open during pending read");
const pendingFile = dom.window.document.querySelector("#fImgUpFile");
const pendingName = dom.window.document.querySelector("#fImgUpName");
assert.ok(pendingFile instanceof dom.window.HTMLInputElement && pendingName instanceof dom.window.HTMLInputElement, "upload controls missing");
const selectedFile = new dom.window.File(["image"], "keep-selected.png", { type: "image/png" });
Object.defineProperty(pendingFile, "files", { configurable: true, value: { 0: selectedFile, length: 1 } });
pendingName.value = "仍在编辑的素材名称";
releaseDialogRead();
await waitFor(() => dom.window.document.body.textContent.includes("新的搜索结果"), "pending dialog read did not finish");
assert.equal(dom.window.document.querySelector("#fImgUpFile"), pendingFile, "read completion rebuilt the upload file input");
assert.equal(pendingFile.files?.[0], selectedFile, "read completion discarded the selected upload file");
assert.equal(pendingName.value, "仍在编辑的素材名称", "read completion discarded in-progress upload fields");
dom.window.document.querySelector('button[aria-label="关闭弹窗"]')?.click();
current = controls();
const callsBeforeReturningQuery = calls.length;
current.input.value = "新";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => calls.slice(callsBeforeReturningQuery).some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=true")), "query did not return to the active filter after dialog preservation check");

current = controls();
current.includeInactive.checked = true;
current.includeInactive.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
await waitFor(() => dom.window.document.body.textContent.includes("已停用素材"), "include-inactive read did not render a disabled image");
assert.ok(dom.window.document.body.textContent.includes("已停用"), "disabled image did not render a Chinese status");
assert.ok(
  calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("q=%E6%96%B0") && call.query.includes("enabled_only=false")),
  "include-inactive read did not send enabled_only=false",
);

current = controls();
current.reset.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "reset did not restore the default result");
current = controls();
assert.equal(current.input.value, "", "reset did not clear the query");
assert.equal(current.includeInactive.checked, false, "reset did not restore enabled-only filtering");

const next = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "下一页");
assert.ok(next, "image pagination next control missing");
next.click();
await waitFor(() => dom.window.document.body.textContent.includes("第二页素材"), "next page did not load images after the first 20");
assert.ok(calls.some((call) => call.path === "/api/admin/image-library" && call.query === "limit=20&offset=20&enabled_only=true"), "next page did not use a reachable offset");

const previous = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "上一页");
assert.ok(previous, "image pagination previous control missing");
previous.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "previous page did not return to the first results");

current = controls();
current.input.value = "空";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => Boolean(dom.window.document.querySelector("[data-image-library-empty]")), "empty image result did not render its explicit empty state");
current = controls();
current.input.value = "";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "successful empty-filter reset did not restore the latest list");

current = controls();
current.input.value = "失败";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => Boolean(dom.window.document.querySelector('[data-image-library-filter-feedback][role="alert"]')), "failed read did not show an in-context error");
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "failed read discarded the last successful image list");
assert.equal(current.input.value, "失败", "failed read discarded the query being retried");
assert.ok(dom.window.document.body.textContent.includes("仍显示上一次成功结果"), "failed read did not identify the preserved result as stale");

current.input.value = "";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "successful retry did not restore the latest list");

current = controls();
current.input.value = "服务错误";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => dom.window.document.body.textContent.includes("读取图片素材服务暂不可用，请稍后重试。"), "ApiError read failure did not use the controlled Chinese service message");
const visibleImageWorkspace = dom.window.document.getElementById("stage")?.textContent || "";
assert.ok(!visibleImageWorkspace.includes("DEPENDENCY_UNAVAILABLE"), "ApiError business code leaked into the image page");
assert.ok(!visibleImageWorkspace.includes("postgres connection refused"), "ApiError implementation detail leaked into the image page");
current.input.value = "网络错误";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => dom.window.document.body.textContent.includes("读取图片素材网络暂不可用，请检查网络后重试。"), "network read failure did not use the controlled Chinese retry message");
assert.ok(!(dom.window.document.getElementById("stage")?.textContent || "").includes("Failed to fetch image-library internal endpoint"), "browser TypeError leaked into the image page");
current.input.value = "";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材") && [...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "下一页" && !button.disabled), "controlled-error recovery did not restore the active list");

failSecondPage = true;
const failingNext = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "下一页");
assert.ok(failingNext, "next page action missing before failed-page retry");
failingNext.click();
await waitFor(() => Boolean(dom.window.document.querySelector('[data-image-library-filter-feedback][role="alert"]')), "failed second page did not show an in-context error");
assert.ok(dom.window.document.body.textContent.includes("默认启用素材"), "failed second page discarded the first successful page");
assert.ok(dom.window.document.querySelector('button[data-image-library-retry="true"]'), "failed second page did not offer an explicit retry");
assert.ok(!calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("offset=40")), "failed page advanced the pagination offset before retry");
failSecondPage = false;
dom.window.document.querySelector('button[data-image-library-retry="true"]')?.click();
await waitFor(() => dom.window.document.body.textContent.includes("第二页素材"), "retry did not request the failed second page");
assert.ok(!calls.some((call) => call.path === "/api/admin/image-library" && call.query.includes("offset=40")), "retry skipped from page two to page three");
const retryPrevious = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "上一页");
assert.ok(retryPrevious, "previous page action missing after retry");
retryPrevious.click();
await waitFor(() => dom.window.document.body.textContent.includes("默认启用素材"), "previous page did not restore the successful first page after retry");

const edit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(edit, "edit action missing from source-owned card");
edit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "edit dialog did not open");
const name = dom.window.document.querySelector("#fImgName");
assert.ok(name instanceof dom.window.HTMLInputElement, "edit name field missing");
name.value = "已更新素材";
const save = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "保存");
assert.ok(save, "edit save action missing");
failNextReadback = true;
save.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")) && dom.window.document.body.textContent.includes("素材已保存，但列表回读失败"), "write/readback failure did not preserve the edit dialog");
assert.equal(dom.window.document.querySelector("#fImgName"), name, "write/readback failure rebuilt the edit input");
assert.equal(name.value, "已更新素材", "write/readback failure discarded the edited name");
assert.equal([...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "重新读取列表")?.disabled, false, "write/readback failure did not offer a read-only retry");
assert.ok(calls.some((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT"), "edit did not reuse the typed Media update route");
const writeAt = calls.findIndex((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT");
assert.ok(calls.slice(writeAt + 1).some((call) => call.path === "/api/admin/image-library" && call.method === "GET"), "successful edit did not perform its required list readback");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT").length, 1, "readback failure retried the write instead of preserving its idempotent result");
const materialReadsBeforeConfirmedEdit = materialRefreshReads;
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => !dom.window.document.querySelector("#fImgName") && dom.window.document.body.textContent.includes("已更新素材"), "readback retry did not confirm the saved edit");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library/11" && call.method === "PUT").length, 1, "readback retry repeated the saved mutation");
assert.equal(materialRefreshReads, materialReadsBeforeConfirmedEdit, "a confirmed write must not restart retired diagnostics polling");

// Closing an accepted edit and opening another dialog before its list readback
// returns must not let the old result close the newer dialog.
const raceEdit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(raceEdit, "edit action missing before different-dialog readback race");
raceEdit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "race edit dialog did not open");
const raceName = dom.window.document.querySelector("#fImgName");
assert.ok(raceName instanceof dom.window.HTMLInputElement, "race edit input missing");
// This is the exact accepted payload from the preceding readback-failure
// check, so MaterialSaveHost may correctly reuse that pending stable key.
raceName.value = "已更新素材";
releaseMutationReadback = undefined;
delayNextMutationReadback = true;
const raceSave = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "保存");
assert.ok(raceSave, "race save action missing");
raceSave.click();
await waitFor(() => typeof releaseMutationReadback === "function", "accepted edit did not begin its list readback");
dom.window.document.querySelector('button[aria-label="关闭弹窗"]')?.click();
const raceUpload = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "上传图片");
assert.ok(raceUpload, "upload action missing during readback race");
raceUpload.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgUpFile")), "later upload dialog did not open during readback race");
const laterDialogFile = dom.window.document.querySelector("#fImgUpFile");
releaseMutationReadback();
await sleep(30);
assert.equal(dom.window.document.querySelector("#fImgUpFile"), laterDialogFile, "old write readback closed or replaced a later dialog");
dom.window.document.querySelector('button[aria-label="关闭弹窗"]')?.click();

// If an accepted write's list read is superseded, it is still an unconfirmed
// readback and must not restore the normal save action.
const abortedEdit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(abortedEdit, "edit action missing before aborted-readback check");
abortedEdit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "aborted-readback edit dialog did not open");
const abortedName = dom.window.document.querySelector("#fImgName");
assert.ok(abortedName instanceof dom.window.HTMLInputElement, "aborted-readback edit input missing");
abortedName.value = "中止回读素材";
releaseMutationReadback = undefined;
delayNextMutationReadback = true;
const abortedSave = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "保存");
assert.ok(abortedSave, "aborted-readback save action missing");
abortedSave.click();
await waitFor(() => typeof releaseMutationReadback === "function", "aborted-readback edit did not begin its list readback");
current = controls();
current.input.value = "新";
current.input.dispatchEvent(new dom.window.Event("input", { bubbles: true }));
commitSearch(current.input);
releaseMutationReadback();
await waitFor(() => dom.window.document.body.textContent.includes("素材已保存，但列表回读失败"), "aborted readback restored a normal write action");
assert.equal([...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "重新读取列表")?.disabled, false, "aborted readback did not leave a read-only retry");
dom.window.document.querySelector('button[aria-label="关闭弹窗"]')?.click();
current = controls();
current.reset.click();
await waitFor(() => dom.window.document.body.textContent.includes("中止回读素材"), "reset after aborted readback did not restore the active list");

const deleteEdit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(deleteEdit, "edit action missing before delete recovery check");
deleteEdit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "delete recovery edit dialog did not open");
loseFirstDeleteResponse = true;
const remove = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "删除");
assert.ok(remove, "delete action missing from source-owned dialog");
remove.click();
await waitFor(() => dom.window.document.body.textContent.includes("删除结果暂不可确认"), "lost delete response did not require a readback check");
assert.equal(remove.disabled, true, "lost delete response left the destructive button enabled");
const deleteCalls = () => calls.filter((call) => call.path === "/api/admin/image-library/11" && call.method === "DELETE");
assert.equal(deleteCalls().length, 1, "lost delete response triggered a duplicate request");
const deleteKey = deleteCalls()[0].headers["idempotency-key"];
assert.ok(typeof deleteKey === "string" && deleteKey.startsWith("image-delete-"), "delete did not send a controlled idempotency key");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => [...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "按原操作重试删除"), "delete verification did not offer a same-intent retry");
assert.equal(deleteCalls().length, 1, "delete verification sent another destructive request");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => !dom.window.document.querySelector("#fImgName") && Boolean(dom.window.document.querySelector("[data-image-library-empty]")), "same-key delete retry did not read back the removed image");
assert.equal(deleteCalls().length, 2, "same-intent retry did not issue exactly one follow-up delete");
assert.equal(deleteCalls()[1].headers["idempotency-key"], deleteKey, "delete retry changed the original idempotency key");

// A list page cannot establish deletion: the last-page item may disappear
// merely because the UI reads the preceding page. First make the server return
// a 5xx, then lose a second DELETE response before it commits. Both paths must
// keep one intent/key and the single-resource GET must still see the item.
finalPageOnly = true;
current = controls();
current.reset.click();
await waitFor(() => dom.window.document.body.textContent.includes("新的搜索结果"), "last-page deletion fixture did not load its first page");
const finalNext = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "下一页");
assert.ok(finalNext, "last-page deletion fixture is missing its next-page control");
finalNext.click();
await waitFor(() => dom.window.document.body.textContent.includes("第二页素材"), "last-page deletion fixture did not load its single-item final page");
const finalEdit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(finalEdit, "last-page deletion fixture is missing its edit action");
finalEdit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "last-page deletion edit dialog did not open");
const finalRemove = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "删除");
assert.ok(finalRemove, "last-page deletion fixture is missing its delete action");
finalRemove.click();
await waitFor(() => dom.window.document.body.textContent.includes("删除结果暂不可确认"), "DELETE 5xx did not preserve an outcome-unknown delete intent");
const finalDeleteCalls = () => calls.filter((call) => call.path === "/api/admin/image-library/31" && call.method === "DELETE");
const finalDeleteKey = finalDeleteCalls()[0]?.headers["idempotency-key"];
assert.ok(finalDeleteKey?.startsWith("image-delete-"), "DELETE 5xx did not retain the controlled delete key");
assert.equal(finalRemove.disabled, true, "DELETE 5xx re-enabled a new destructive action instead of retaining the intent");

// An outcome-unknown deletion cannot be dismissed: the exact-resource
// verification action stays reachable even if a subsequent list read would no
// longer include this card.
dom.window.document.querySelector('button[aria-label="关闭弹窗"]')?.click();
await waitFor(() => dom.window.document.body.textContent.includes("请先点击“重新核对删除结果”"), "outcome-unknown delete dialog could be closed before exact-resource verification");
assert.ok(dom.window.document.querySelector("#fImgName"), "outcome-unknown delete dialog disappeared before verification");
assert.ok([...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "重新核对删除结果"), "outcome-unknown delete did not keep its exact-resource verification action reachable");
const listCallsBeforeFiveXXVerification = calls.filter((call) => call.path === "/api/admin/image-library" && call.method === "GET").length;
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => [...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "按原操作重试删除"), "single-image read did not expose same-key retry after DELETE 5xx");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library" && call.method === "GET").length, listCallsBeforeFiveXXVerification, "DELETE 5xx verification treated a paginated list as deletion evidence");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library/31" && call.method === "GET").length, 1, "DELETE 5xx did not verify the exact resource");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => dom.window.document.body.textContent.includes("删除结果暂不可确认"), "lost final-page DELETE response did not retain the original delete intent");
assert.equal(finalDeleteCalls().length, 2, "lost final-page DELETE response did not issue exactly one retry");
assert.equal(finalDeleteCalls()[1].headers["idempotency-key"], finalDeleteKey, "DELETE retry after 5xx changed the stable key");
const listCallsBeforeLostVerification = calls.filter((call) => call.path === "/api/admin/image-library" && call.method === "GET").length;
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => [...dom.window.document.querySelectorAll("button")].some((button) => button.textContent === "按原操作重试删除"), "uncommitted final-page DELETE was falsely confirmed as removed");
assert.equal(finalPageDeleted, false, "lost final-page DELETE test unexpectedly committed the deletion");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library" && call.method === "GET").length, listCallsBeforeLostVerification, "last-page list omission was used as deletion evidence");
assert.equal(calls.filter((call) => call.path === "/api/admin/image-library/31" && call.method === "GET").length, 2, "lost final-page DELETE did not use exact GET verification");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => !dom.window.document.querySelector("#fImgName") && dom.window.document.body.textContent.includes("新的搜索结果"), "confirmed final-page deletion did not refresh the preceding display page");
assert.equal(finalPageDeleted, true, "final-page delete did not commit on same-key retry");
assert.equal(finalDeleteCalls().length, 3, "confirmed final-page deletion did not issue its final same-key retry");
assert.equal(finalDeleteCalls()[2].headers["idempotency-key"], finalDeleteKey, "final same-key delete retry changed the original intent key");

// The exact-resource 404 is sufficient to confirm deletion even if the
// subsequent paginated list read fails. Its recovery button must switch from
// delete verification to a plain list readback; otherwise it would retain an
// action that no longer has a delete intent to inspect.
confirmedDeleteScenario = true;
current = controls();
current.reset.click();
await waitFor(() => dom.window.document.body.textContent.includes("已删除但列表回读失败素材"), "confirmed-delete recovery fixture did not load");
const confirmedEdit = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "编辑");
assert.ok(confirmedEdit, "confirmed-delete recovery edit action missing");
confirmedEdit.click();
await waitFor(() => Boolean(dom.window.document.querySelector("#fImgName")), "confirmed-delete recovery dialog did not open");
const confirmedRemove = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "删除");
assert.ok(confirmedRemove, "confirmed-delete recovery delete action missing");
confirmedRemove.click();
await waitFor(() => dom.window.document.body.textContent.includes("删除结果暂不可确认"), "post-commit lost delete response did not require exact-resource verification");
const confirmedDeleteCalls = () => calls.filter((call) => call.path === "/api/admin/image-library/41" && call.method === "DELETE");
const confirmedDetailCalls = () => calls.filter((call) => call.path === "/api/admin/image-library/41" && call.method === "GET");
assert.equal(confirmedDeleteCalls().length, 1, "post-commit lost delete response retried before verification");
dom.window.document.querySelector("button[data-image-library-dialog-submit]")?.click();
await waitFor(() => dom.window.document.body.textContent.includes("图片已删除，但列表回读未完成"), "detail 404 followed by list failure did not preserve a list-only recovery state");
assert.equal(confirmedDetailCalls().length, 1, "confirmed deletion did not use one exact-resource GET");
const confirmedReadback = [...dom.window.document.querySelectorAll("button")].find((button) => button.textContent === "重新读取列表");
assert.ok(confirmedReadback, "confirmed deletion/list failure did not offer a list readback");
confirmedReadback.click();
await waitFor(() => !dom.window.document.querySelector("#fImgName") && Boolean(dom.window.document.querySelector("[data-image-library-empty]")), "list-only readback did not finish confirmed deletion recovery");
assert.equal(confirmedDeleteCalls().length, 1, "confirmed deletion/list recovery retried DELETE");
assert.equal(confirmedDetailCalls().length, 1, "confirmed deletion/list recovery retried stale delete verification");

for (const call of calls.filter((call) => call.path === "/api/admin/image-library" && call.method === "GET")) {
  assert.ok(call.query.includes("limit=20") && call.query.includes("offset=") && call.query.includes("enabled_only="), `image read escaped bounded pagination/filter contract: ${JSON.stringify(call)}`);
}

// A bare materials URL must become a durable images+group URL, not a server redirect.
dom.window.history.replaceState(null, '', '/admin/materials');
dom.window.document.querySelector('[data-material-group-value="__ungrouped__"]').click();
await waitFor(() => calls.some(call => call.query.includes('only_ungrouped=true')), 'sidebar ungrouped read');
assert.equal(new URL(dom.window.location.href).searchParams.get('tab'), 'images');
assert.equal(new URL(dom.window.location.href).searchParams.get('material_group'), '');
assert.equal(dom.window.document.querySelector('[data-material-group-value="__ungrouped__"]').getAttribute('aria-pressed'), 'true');
dom.window.document.querySelector('[data-material-group-value=""]').click();
await sleep(30);
assert.equal(new URL(dom.window.location.href).searchParams.has('material_group'), false);
dom.window.close();
console.log("image-library V3 Host DOM: PASS");
