import { build } from "esbuild";
import { JSDOM } from "jsdom";
import assert from "node:assert/strict";

const bundle = await build({
  entryPoints: ["web/v3/excelBatches.ts"],
  bundle: true,
  format: "iife",
  globalName: "ExcelPaginationTest",
  write: false,
});
const waitFor = async (check, message) => {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(message);
};
const response = (body, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => body,
});
const setBrowserSupport = (window) => {
  Object.defineProperty(window.crypto, "randomUUID", { value: () => "00000000-0000-4000-8000-000000000001" });
  window.HTMLDialogElement.prototype.showModal = function () { this.open = true; };
  window.HTMLDialogElement.prototype.close = function () { this.open = false; };
};

const pagingDom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const pagingWindow = pagingDom.window;
setBrowserSupport(pagingWindow);
const total = 121;
const pageOffsets = [];
const detailReads = [];
pagingWindow.fetch = async (input) => {
  const url = new URL(String(input), pagingWindow.location.href);
  if (url.pathname === "/api/admin/operation-batches/strategy-summaries") {
    const limit = Number(url.searchParams.get("limit"));
    const offset = Number(url.searchParams.get("offset"));
    assert.equal(limit, 20, "strategy summary page must retain the default limit of 20");
    pageOffsets.push(offset);
    const end = Math.min(offset + limit, total);
    return response({
      items: Array.from({ length: Math.max(0, end - offset) }, (_, index) => {
        const number = offset + index + 1;
        if (number === 2) return { strategy_key: "strategy-2", title: "摘要不可用计划", status: "active", version: 1, latest_batch_status: "unavailable", latest_batch: null };
        return { strategy_key: `strategy-${number}`, title: `长期计划 ${number}`, status: "active", version: 1, latest_batch_status: "ready", latest_batch: { id: 1000 + number, state: "pending_review", summary: { expected_tasks: number } } };
      }),
      total,
      limit,
      offset,
      has_more: end < total,
      next_offset: end < total ? end : null,
    });
  }
  if (url.pathname === "/api/admin/operation-batches/legacy") return response({ items: [] });
  if (url.pathname === "/api/admin/operation-batches/strategies/strategy-121") {
    detailReads.push(url.pathname);
    return response({ strategy: { strategy_key: "strategy-121", title: "长期计划 121" }, items: [{ id: 1121, state: "pending_review", version: 1, current_content_version: 1, summary: { total_rows: 1, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 1 } }] });
  }
  if (url.pathname === "/api/admin/operation-batches/1121") return response({ batch: { id: 1121, state: "pending_review", version: 1, current_content_version: 1, summary: { total_rows: 1, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 1 } }, rows: [], next_cursor: "" });
  throw new Error(`unexpected request ${url.pathname}${url.search}`);
};
try {
  pagingWindow.eval(bundle.outputFiles[0].text + ";window.ExcelPaginationTest=ExcelPaginationTest;");
  await pagingWindow.ExcelPaginationTest.mountOperationExcelWorkspace(pagingWindow.document.querySelector("#stage"));
  await waitFor(() => pagingWindow.document.body.textContent.includes("第 1–20 项，共 121 项"), "first strategy page did not render");
  assert(pagingWindow.document.body.textContent.includes("批次摘要暂不可用"), "unavailable summary must not appear as an absent batch");
  assert(!pagingWindow.document.body.textContent.includes("暂无批次"), "known unavailable summary must not be presented as no batch");
  assert.deepEqual(pageOffsets, [0], "one strategy-summary request must replace the old per-strategy reads");
  assert.equal(detailReads.length, 0, "list loading must not request every strategy's batch history");
  const css = Array.from(pagingWindow.document.querySelectorAll("style")).map((item) => item.textContent).join("\n");
  assert(css.includes("@media screen and (max-width:800px)"), "responsive Excel media query must be valid and explicit");
  assert(!css.includes("@media(max-width"), "obsolete malformed Excel media query must not remain");

  for (let step = 0; step < 6; step += 1) {
    const next = Array.from(pagingWindow.document.querySelectorAll("button")).find((item) => item.textContent === "下一页");
    assert(next && !next.disabled, `page ${step} must expose its next page control: ${pagingWindow.document.body.innerHTML}`);
    next.click();
    await waitFor(() => pageOffsets.length === step + 2, `strategy page ${step + 2} did not load`);
    await waitFor(() => pagingWindow.document.body.textContent.includes(`第 ${Math.min((step + 1) * 20 + 1, total)}–`), `strategy page ${step + 2} did not finish rendering`);
  }
  assert.deepEqual(pageOffsets, [0, 20, 40, 60, 80, 100, 120], "every page after the first 100 must remain reachable by bounded offset");
  assert(pagingWindow.document.body.textContent.includes("第 121–121 项，共 121 项"), "last page range is incorrect");
  const next = Array.from(pagingWindow.document.querySelectorAll("button")).find((item) => item.textContent === "下一页");
  assert(next?.disabled, "last page must disable the next control");
  const detail = Array.from(pagingWindow.document.querySelectorAll("button")).find((item) => item.textContent === "查看详情");
  assert(detail, "last-page plan must remain actionable");
  detail.click();
  await waitFor(() => pagingWindow.document.body.textContent.includes("当前批次 #1121"), "last-page strategy detail did not load");
  assert.equal(detailReads.length, 1, "opening one plan must read one history, not replay list N+1 reads");
  console.log("excel-batches-pagination-dom: PASS");
} finally {
  pagingDom.window.close();
}

const stalePageDom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const stalePageWindow = stalePageDom.window;
setBrowserSupport(stalePageWindow);
const stalePageOffsets = [];
stalePageWindow.fetch = async (input) => {
  const url = new URL(String(input), stalePageWindow.location.href);
  if (url.pathname === "/api/admin/operation-batches/strategy-summaries") {
    const offset = Number(url.searchParams.get("offset"));
    stalePageOffsets.push(offset);
    if (offset === 120) return response({ items: [], total: 121, limit: 20, offset, has_more: false, next_offset: null });
    const totalAfterDelete = stalePageOffsets.filter((value) => value === 120).length ? 120 : 121;
    const count = Math.max(0, Math.min(20, totalAfterDelete - offset));
    const end = offset + count;
    return response({
      items: Array.from({ length: count }, (_, index) => ({ strategy_key: `stale-${offset + index}`, title: `计划 ${offset + index}`, status: "active", version: 1, latest_batch_status: "ready", latest_batch: null })),
      total: totalAfterDelete,
      limit: 20,
      offset,
      has_more: end < totalAfterDelete,
      next_offset: end < totalAfterDelete ? end : null,
    });
  }
  if (url.pathname === "/api/admin/operation-batches/legacy") return response({ items: [] });
  throw new Error(`unexpected stale-page request ${url.pathname}`);
};
try {
  stalePageWindow.eval(bundle.outputFiles[0].text + ";window.ExcelPaginationTest=ExcelPaginationTest;");
  await stalePageWindow.ExcelPaginationTest.mountOperationExcelWorkspace(stalePageWindow.document.querySelector("#stage"));
  await waitFor(() => stalePageWindow.document.body.textContent.includes("第 1–20 项，共 121 项"), "stale-page fixture did not render its first page");
  for (let step = 0; step < 6; step += 1) {
    const next = Array.from(stalePageWindow.document.querySelectorAll("button")).find((item) => item.textContent === "下一页");
    assert(next && !next.disabled, `stale page ${step} must have a next control`);
    next.click();
    await waitFor(() => stalePageOffsets.length >= step + 2, `stale page ${step + 1} did not load`);
    if (step < 5) await waitFor(() => {
      const after = Array.from(stalePageWindow.document.querySelectorAll("button")).find((item) => item.textContent === "下一页");
      return Boolean(after && !after.disabled);
    }, `stale page ${step + 1} did not finish rendering`);
  }
  await waitFor(() => stalePageOffsets.length === 8, "empty stale final page did not return to the previous page exactly once");
  assert.deepEqual(stalePageOffsets, [0, 20, 40, 60, 80, 100, 120, 100], "empty stale page must not retry its current offset");
  assert(stalePageWindow.document.body.textContent.includes("第 101–120 项，共 120 项"), "stale page fallback did not render the preceding page");
  console.log("excel-batches-pagination-stale-page-dom: PASS");
} finally {
  stalePageDom.window.close();
}

const unavailableDom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const unavailableWindow = unavailableDom.window;
setBrowserSupport(unavailableWindow);
let summaryAttempts = 0;
unavailableWindow.fetch = async (input) => {
  const url = new URL(String(input), unavailableWindow.location.href);
  if (url.pathname === "/api/admin/operation-batches/strategy-summaries") {
    summaryAttempts += 1;
    if (summaryAttempts === 1) return response({ error: "unavailable" }, 503);
    return response({ items: [], total: 0, limit: 20, offset: 0, has_more: false, next_offset: null });
  }
  if (url.pathname === "/api/admin/operation-batches/legacy") return response({ items: [] });
  throw new Error(`unexpected unavailable request ${url.pathname}`);
};
try {
  unavailableWindow.eval(bundle.outputFiles[0].text + ";window.ExcelPaginationTest=ExcelPaginationTest;");
  await unavailableWindow.ExcelPaginationTest.mountOperationExcelWorkspace(unavailableWindow.document.querySelector("#stage"));
  await waitFor(() => unavailableWindow.document.querySelector('[role="alert"]'), "strategy list failure must render an explicit error state");
  const retry = Array.from(unavailableWindow.document.querySelectorAll("button")).find((item) => item.textContent === "重新读取");
  assert(retry, "strategy list failure must offer a retry action");
  retry.click();
  await waitFor(() => unavailableWindow.document.body.textContent.includes("暂无可访问的长期计划。"), "retry did not replace failure with empty state");
  assert.equal(summaryAttempts, 2, "retry must issue one new strategy summary request");
  console.log("excel-batches-pagination-states-dom: PASS");
} finally {
  unavailableDom.window.close();
}
