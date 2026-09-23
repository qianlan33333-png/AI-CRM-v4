import { build } from "esbuild";
import { JSDOM } from "jsdom";
import assert from "node:assert/strict";

const bundle = await build({
  entryPoints: ["web/v3/excelBatches.ts"],
  bundle: true,
  format: "iife",
  globalName: "ExcelTest",
  write: false,
});
const feedbackBundle = await build({
  entryPoints: ["web/donor-sources/v2-6bfbe5816bb89913c70adaca87d6a486260e016e/web/src/shared/ui/feedback.ts"],
  bundle: true,
  format: "iife",
  globalName: "ExcelFeedbackTest",
  write: false,
});
const dom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const win = dom.window;
Object.defineProperty(win.crypto, "randomUUID", {
  value: (() => {
    let n = 0;
    return () => `00000000-0000-4000-8000-${String(++n).padStart(12, "0")}`;
  })(),
});
win.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
win.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};

let cover = "",
  approved = 0,
  excluded = false;
let failCoverUploadOnce = false;
let failImportOnce = false;
let releaseFirstRowPatch;
let pauseFirstRowPatch = true;
let releaseInitialDetail;
let pauseInitialDetail = true;
let releasePostWriteDetail;
let pausePostWriteDetail = false;
let failNextContentReadback = false;
const contentResponseVersionOverrides = [];
let importedBatch;
let selectedCoverID = 0;
let coverSelectionPosts = 0;
const coverLibrary = Array.from({ length: 13 }, (_, index) => ({
  id: 42 + index,
  name: index === 12 ? "第二页封面" : `启用图片-${index + 1}`,
  enabled: true,
  thumb_160_url: `/api/admin/image-library/${42 + index}/variants/thumb_160`,
}));
const calls = [];
const batch = {
  id: 918,
  batch_key: "xb_fixture",
  state: "pending_review",
  version: 3,
  cover_digest: "",
  cover_image_id: 0,
  current_content_version: 1,
  summary: {
    total_rows: 1,
    excluded_rows: 0,
    empty_title_rows: 0,
    expected_tasks: 1,
  },
};
const row = {
  id: 33,
  version: 4,
  unionid: "<script>unsafe</script>",
  sender_userid: "staff",
  text: "原话术",
  card: {
    appid: "configured-app",
    path: "pages/article?id=1",
    title: "案例标题",
    cover_digest: "",
  },
  segment: "A",
  excluded: false,
  delivery_state: "pending_submission",
  failure_reason: "",
  sent_at: null,
};
const extraRows = Array.from({ length: 50 }, (_, index) => ({
  ...row,
  id: 34 + index,
  unionid: index === 49 ? "第二页用户" : `用户-${index + 2}`,
  version: 1,
}));
extraRows[0] = {
  ...extraRows[0],
  review_state: "approved",
  delivery_state: "delivery_proven",
  sent_at: "2026-09-30T16:00:00.611265Z",
};
extraRows[49] = {
  ...extraRows[49],
  review_state: "approved",
  delivery_state: "final_failed",
  failure_reason: "provider_rejected",
};
const receiptRows = Array.from({ length: 51 }, (_, index) => ({
  unionid: index === 50 ? "第二页回执用户" : `回执用户-${index + 1}`,
  sender_userid: "staff",
  delivery_state: "delivery_proven",
  sent_at: "2026-09-30T16:00:00.611265Z",
  failure_reason: "",
}));
const historyRows = [
  {
    ...row,
    unionid: "历史第一页用户",
    version: 7,
    review_state: "approved",
    delivery_state: "delivery_proven",
    sent_at: "2026-09-30T16:00:00.611265Z",
    card: { ...row.card, cover_digest: "sha256:historic-cover" },
  },
  {
    ...row,
    id: 999,
    unionid: "历史第二页用户",
    version: 8,
    segment: "B",
    excluded: true,
    review_state: "rejected",
    delivery_state: "final_failed",
    failure_reason: "provider_rejected",
    card: { ...row.card, cover_digest: "" },
  },
];
receiptRows[1] = {
  ...receiptRows[1],
  delivery_state: "unmapped_provider_state",
  sent_at: "2026-02-31T00:00:00Z",
  failure_reason: "服务失败 provider_error",
};
receiptRows[2] = {
  ...receiptRows[2],
  delivery_state: "outcome_unknown",
  failure_reason: "wecom_errcode_45009",
};
const json = (body, status = 200) => ({
  ok: status >= 200 && status < 300,
  status,
  json: async () => body,
});
win.fetch = async (raw, init = {}) => {
  const url = String(raw);
  calls.push({ url, init });
  if (url === "/api/admin/operation-batches/strategy-summaries?limit=20&offset=0")
    return json({
      items: [
        {
          strategy_key: "weekly.review",
          title: "每周复盘",
          status: "active",
          latest_batch_status: "ready",
          latest_batch: {
            id: 918,
            state: "pending_review",
            summary: { expected_tasks: 1 },
          },
        },
      ],
      total: 1,
      limit: 20,
      offset: 0,
      has_more: false,
      next_offset: null,
    });
  if (url === "/api/admin/operation-batches/legacy")
    return json({
      items: [
        { id: 917, name: "旧批次", state: "pending_review", linkable: true },
      ],
    });
  if (url === "/api/admin/operation-batches/strategies/weekly.review")
    return json({
      strategy: { strategy_key: "weekly.review", title: "每周复盘" },
      items: importedBatch ? [importedBatch, batch] : [batch],
    });
  if (
    url.startsWith(
      "/api/admin/operation-batches/strategies/weekly.review/imports",
    )
  ) {
    if (failImportOnce) {
      failImportOnce = false;
      return json({}, 503);
    }
    importedBatch = {
      ...batch,
      id: 919,
      batch_key: "xb_imported",
      version: 1,
    };
    return json({ batch: importedBatch });
  }
  if (url.startsWith("/api/admin/operation-batches/918?")) {
    if (failNextContentReadback) {
      failNextContentReadback = false;
      return json({}, 503);
    }
    if (pauseInitialDetail) {
      pauseInitialDetail = false;
      await new Promise((resolve) => {
        releaseInitialDetail = resolve;
      });
    }
    if (pausePostWriteDetail) {
      pausePostWriteDetail = false;
      await new Promise((resolve) => {
        releasePostWriteDetail = resolve;
      });
    }
    const cursor = new URL(url, "https://fixture.test").searchParams.get(
      "cursor",
    );
    const returnedBatch = contentResponseVersionOverrides.length
      ? { ...batch, current_content_version: contentResponseVersionOverrides.shift() }
      : batch;
    return json(
      cursor === "detail-page-2"
        ? { batch: returnedBatch, rows: [extraRows[49]], next_cursor: "" }
        : {
            batch: returnedBatch,
            rows: [row, ...extraRows.slice(0, 49)],
            next_cursor: "detail-page-2",
          },
    );
  }
  if (url.startsWith("/api/admin/operation-batches/919?"))
    return json({ batch: importedBatch, rows: [row], next_cursor: "" });
  if (url.startsWith("/api/admin/operation-batches/918/cover?")) {
    const contentType = new Headers(init.headers).get("Content-Type") || "";
    if (contentType.startsWith("application/json")) {
      const body = JSON.parse(init.body);
      assert.equal(body.cover_image_id, 42);
      coverSelectionPosts += 1;
      selectedCoverID = body.cover_image_id;
      cover = batch.cover_digest = row.card.cover_digest = "sha256:existing-cover";
      batch.cover_image_id = selectedCoverID;
    } else {
      if (failCoverUploadOnce) {
        failCoverUploadOnce = false;
        return json({}, 503);
      }
      assert.ok(init.body instanceof win.File);
      cover = batch.cover_digest = row.card.cover_digest = "sha256:cover";
      batch.cover_image_id = 43;
    }
    batch.version++;
    batch.current_content_version++;
    return json({ batch, cover_digest: cover, cover_image_id: batch.cover_image_id });
  }
  if (url.startsWith("/api/admin/image-library?") && url.includes("enabled_only=true")) {
    const query = new URL(url, "https://fixture.test").searchParams;
    assert.equal(query.get("limit"), "12");
    const offset = Number(query.get("offset"));
    const items = coverLibrary.slice(offset, offset + 12);
    return json({
      items,
      total: coverLibrary.length,
      limit: 12,
      offset,
      has_more: offset + items.length < coverLibrary.length,
      next_offset: offset + items.length < coverLibrary.length ? offset + items.length : null,
    });
  }
  if (url.endsWith("/preview-approval")) {
    assert.equal(JSON.parse(init.body).expected_version, batch.version);
    return json({ preview_digest: "sha256:preview", summary: batch.summary });
  }
  if (url.endsWith("/approve")) {
    const body = JSON.parse(init.body);
    assert.equal(body.preview_digest, "sha256:preview");
    approved++;
    batch.state = "dispatching";
    row.review_state = "approved";
    row.delivery_state = "task_created_waiting_employee";
    return json({ batch });
  }
  if (url.endsWith("/rows/33")) {
    if (pauseFirstRowPatch) {
      pauseFirstRowPatch = false;
      await new Promise((resolve) => {
        releaseFirstRowPatch = resolve;
      });
    }
    const body = JSON.parse(init.body);
    assert.equal(body.expected_version, row.version);
    row.text = body.text;
    row.card.path = body.path;
    row.card.title = body.title;
    excluded = row.excluded = body.excluded;
    row.version++;
    batch.version++;
    batch.current_content_version++;
    return json({ batch, row });
  }
  if (url.startsWith("/api/admin/operation-batches/918/receipts?")) {
    const cursor = new URL(url, "https://fixture.test").searchParams.get(
      "cursor",
    );
    return json(
      cursor === "receipts-page-2"
        ? { batch_id: 918, content_version: batch.current_content_version, items: [receiptRows[50]], next_cursor: "" }
        : {
            batch_id: 918,
            content_version: batch.current_content_version,
            items: receiptRows.slice(0, 50),
            next_cursor: "receipts-page-2",
          },
    );
  }
  if (url.endsWith("/report"))
    return json({
      updated_at: "2026-09-09T00:00:00Z",
      segment_source: "excel",
      has_segments: true,
      overall: {
        12: {
          sent: 1,
          matured: 1,
          observing: 0,
          opened: 1,
          unavailable: 0,
          open_rate: 1,
        },
      },
      windows: {
        12: {
          has_segments: true,
          overall: {
            sent: 1,
            matured: 1,
            observing: 0,
            opened: 1,
            unavailable: 0,
            open_rate: 1,
          },
          groups: {
            A: {
              sent: 1,
              matured: 1,
              observing: 0,
              opened: 1,
              unavailable: 0,
              open_rate: 1,
            },
          },
        },
      },
    });
  if (url.endsWith("/versions"))
    return json({
      items: [
        { content_version: 1, cover_image_id: 42, cover_digest: "sha256:existing-cover", created_at: "2026-09-09T00:00:00Z" },
        { content_version: 2, cover_image_id: 42, cover_digest: "sha256:existing-cover", created_at: "2026-09-10T00:00:00Z" },
      ],
    });
  if (url.startsWith("/api/admin/operation-batches/918/versions/1?")) {
    const cursor = new URL(url, "https://fixture.test").searchParams.get("cursor");
    return json(
      cursor === "history-page-2"
        ? { batch_id: 918, content_version: { plan_id: 918, content_version: 1, cover_image_id: 42 }, read_only: true, rows: [historyRows[1]], next_cursor: "" }
        : { batch_id: 918, content_version: { plan_id: 918, content_version: 1, cover_image_id: 42 }, read_only: true, rows: [historyRows[0]], next_cursor: "history-page-2" },
    );
  }
  if (url.startsWith("/api/admin/operation-batches/918/versions/2?"))
    return json({ batch_id: 918, content_version: { plan_id: 918, content_version: 2, cover_image_id: 42 }, read_only: true, rows: [], next_cursor: "" });
  throw new Error(`unexpected request ${url}`);
};
win.eval(bundle.outputFiles[0].text + ";window.ExcelTest=ExcelTest;");
await win.ExcelTest.mountOperationExcelWorkspace(
  win.document.getElementById("stage"),
);
assert.equal(
  win.document.querySelectorAll(".xeb-plan").length,
  0,
  "only the long-term plan is a first-level row",
);
assert.ok(win.document.body.textContent.includes("查看详情"));
assert.ok(
  win.document.body.textContent.includes("批次 #918 · 待审核 · 预计任务 1"),
);
const click = async (label) => {
  const node = [...win.document.querySelectorAll("button")].find(
    (item) => item.textContent === label,
  );
  assert.ok(node, label);
  node.click();
  await new Promise((resolve) => setTimeout(resolve, 20));
};
await click("查看详情");
assert.ok(
  [...win.document.querySelectorAll("button")].some(
    (item) => item.textContent === "新建发送批次",
  ),
  "new batch remains available while the initial detail is loading",
);
await click("新建发送批次");
const importInput = win.document.querySelector('dialog input[type="file"]');
assert.ok(
  importInput.closest("label.admin-field"),
  "new batch file input did not use the standard field wrapper",
);
await click("上传并开始审核");
const importDialog = importInput.closest("dialog");
assert.ok(importDialog?.open, "missing Excel file closed the new-batch dialog");
assert.ok(
  importDialog.querySelector("[data-excel-feedback]")?.textContent.includes("请选择 Excel 文件"),
  "missing Excel file did not remain visible inside its dialog",
);
assert.equal(
  [...importDialog.querySelectorAll("button")].find((item) => item.textContent === "上传并开始审核")?.getAttribute("aria-busy"),
  null,
  "missing Excel file left the dialog action busy",
);
Object.defineProperty(importInput, "files", {
  value: [new win.File(["fixture"], "batch.xlsx")],
});
failImportOnce = true;
await click("上传并开始审核");
assert.ok(importDialog.open, "failed Excel import closed the new-batch dialog");
assert.ok(
  importDialog.querySelector("[data-excel-feedback]")?.textContent.includes("批次服务暂时不可用"),
  "failed Excel import did not remain visible inside its dialog",
);
assert.equal(importInput.files?.[0]?.name, "batch.xlsx", "failed Excel import discarded the selected file");
assert.equal(
  [...importDialog.querySelectorAll("button")].find((item) => item.textContent === "上传并开始审核")?.getAttribute("aria-busy"),
  null,
  "failed Excel import left the dialog action busy",
);
await click("上传并开始审核");
releaseInitialDetail();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(
  win.document.body.textContent.includes("当前批次 #919"),
  "a stale initial detail response cannot overwrite the newly imported batch",
);
const history = win.document.querySelector('select[aria-label="历史批次"]');
assert.ok(
  history.closest("label.admin-field"),
  "historical batch selector did not use the standard field wrapper",
);
history.value = "918";
history.dispatchEvent(new win.Event("change"));
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(
  win.document.querySelectorAll(".xeb-detail-nav button").length,
  2,
  "detail has exactly the two requested dimensions on its left side",
);
assert.ok(win.document.body.textContent.includes("内容准备与发送"));
assert.equal(
  win.document.body.textContent.includes("第二页用户"),
  false,
  "initial content render must not prefetch the second cursor page",
);
const contentPager = () =>
  win.document.querySelector('[data-excel-page="content"]');
const contentNext = [
  ...(contentPager()?.querySelectorAll("button") || []),
].find((item) => item.textContent === "下一页");
assert.ok(contentNext && !contentNext.disabled, "content must expose the next cursor page");
contentNext.click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(
  win.document.body.textContent.includes("第二页用户"),
  "content follows next_cursor only after the user requests the second page",
);
assert.ok(
  win.document.body.textContent.includes("企微拒绝发送请求"),
  "known provider rejection codes use the approved Chinese business label",
);
const contentPrevious = [...contentPager().querySelectorAll("button")].find((item) => item.textContent === "上一页");
assert.ok(contentPrevious && !contentPrevious.disabled, "content second page must expose the previous cursor page");
contentPrevious.click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(
  win.document.body.textContent.includes("发送成功\n2026-10-01 00:00:00"),
  "batch rows present real delivery instants in Shanghai time",
);
assert.equal(
  win.document.body.textContent.includes("2026-09-30T16:00:00.611265Z"),
  false,
  "batch rows do not leak the RFC3339 delivery instant",
);
assert.ok(
  win.document.body.textContent.includes("<script>unsafe</script>"),
  "untrusted text must stay text",
);
assert.equal(
  win.document.querySelectorAll("script").length,
  0,
  "batch data must not create script elements",
);
const approve = () =>
  [...win.document.querySelectorAll("button")].find(
    (item) => item.textContent === "审核通过并创建企微群发任务",
  );
assert.equal(
  approve().disabled,
  true,
  "frontend prevents approval before a batch cover exists",
);
assert.ok(
  approve().classList.contains("admin-button--primary"),
  "the approval action did not retain its primary action hierarchy",
);
assert.ok(
  [...win.document.querySelectorAll("button")].every((button) =>
    button.classList.contains("admin-button"),
  ),
  "Excel actions did not use the shared standard button component",
);
await click("选择已有启用图片");
const picker = () => win.document.querySelector('dialog[aria-label="选择已有启用图片"]');
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(picker(), "enabled image picker did not open");
assert.ok(picker().textContent.includes("启用图片-1"), "picker did not render the first image page");
assert.equal(batch.cover_image_id, 0, "opening the picker changed the frozen cover before selection");
const nextCoverPage = [...picker().querySelectorAll("button")].find((item) => item.textContent === "下一页");
assert.ok(nextCoverPage && !nextCoverPage.disabled, "picker did not expose its second page");
nextCoverPage.click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(picker().textContent.includes("第二页封面"), "picker did not follow image-library offset pagination");
const cancelPicker = [...picker().querySelectorAll("button")].find((item) => item.textContent === "取消");
assert.ok(cancelPicker, "picker did not expose cancellation");
cancelPicker.click();
await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(picker(), null, "cancelling the image picker did not close it");
assert.equal(batch.cover_image_id, 0, "cancelling the image picker changed the batch cover");
await click("选择已有启用图片");
await new Promise((resolve) => setTimeout(resolve, 20));
const firstCoverChoice = picker().querySelector('button[data-cover-image-id="42"]');
assert.ok(firstCoverChoice, "picker did not expose the selected stable image id");
firstCoverChoice.click();
await new Promise((resolve) => setTimeout(resolve, 30));
assert.equal(coverSelectionPosts, 1, "selecting an existing image did not issue one JSON cover command");
assert.equal(selectedCoverID, 42, "existing cover command used the wrong stable material id");
assert.ok(
  calls.some((call) => call.url.startsWith("/api/admin/operation-batches/918/cover?") && String(call.init.headers["Content-Type"] || "").startsWith("application/json") && JSON.parse(call.init.body).cover_image_id === 42),
  "existing cover selection did not use the documented JSON body",
);
assert.ok(win.document.body.textContent.includes("冻结封面：素材 #42"), "selected cover was not shown as frozen material evidence");
// This is the real Excel-row callsite: the Composer retains a local IME draft
// and only the pre-existing owner action submits a PATCH.
await click("修改");
const rowEditor = [...win.document.querySelectorAll("dialog")].find(
  (dialog) => dialog.open && dialog.textContent.includes("修改发送内容"),
);
assert.ok(rowEditor, "Excel row editor did not open");
const rowText = rowEditor.querySelector("textarea");
assert.equal(rowText.readOnly, true, "Excel text must be edited through the shared Composer");
const rowWritesBeforeComposer = calls.filter((call) => call.url.endsWith("/rows/33")).length;
const openComposer = [...rowEditor.querySelectorAll("button")].find(
  (button) => button.textContent === "编辑话术与预览",
);
assert.ok(openComposer, "Excel row editor did not expose the shared Composer");
openComposer.click();
await new Promise((resolve) => setTimeout(resolve, 20));
const composer = win.document.querySelector('[data-v3-content-composer="1"]');
assert.ok(composer, "Excel row editor did not mount the shared Composer");
assert.equal(composer.tagName, "DIALOG", "Excel nested Composer must enter the browser top layer");
assert.equal(composer.getAttribute("aria-labelledby"), "aicrm-v3-content-composer-title", "nested Composer must retain a native dialog label");
assert.equal(composer.querySelector(".aicrm-content-composer").hasAttribute("role"), false, "nested Composer must not expose a second dialog role");
assert.equal(composer.textContent.includes("添加图片"), false, "Excel fixed-card content exposed a generic media selector");
const composerText = composer.querySelector("textarea[data-v3-composer-text]");
const originalComposerText = composerText;
composerText.dispatchEvent(new win.CompositionEvent("compositionstart"));
composerText.value = "中文组合输入草稿";
composerText.dispatchEvent(new win.Event("input", { bubbles: true }));
assert.equal(
  composer.querySelector("textarea[data-v3-composer-text]"),
  originalComposerText,
  "Excel Composer replaced its textarea during an IME composition",
);
composerText.dispatchEvent(new win.CompositionEvent("compositionend"));
assert.ok(
  composer.querySelector('[data-content-presentation="preview"]')?.textContent.includes("中文组合输入草稿"),
  "Excel Composer preview did not use the local text draft",
);
const cardPreview = composer.querySelector('[data-content-presentation-supplement] img');
assert.ok(
  cardPreview?.src.includes("sha256%3Aexisting-cover"),
  "Excel Composer did not use the row card's actual current cover digest",
);
assert.equal(
  calls.filter((call) => call.url.endsWith("/rows/33")).length,
  rowWritesBeforeComposer,
  "opening or editing the Composer patched an Excel row",
);
composer.querySelector("button[data-v3-composer-confirm]").click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(rowText.value, "中文组合输入草稿", "Composer confirmation did not update only the row dialog draft");
assert.equal(
  calls.filter((call) => call.url.endsWith("/rows/33")).length,
  rowWritesBeforeComposer,
  "Composer confirmation sent an Owner patch before explicit save",
);
pauseFirstRowPatch = false;
const saveRow = [...rowEditor.querySelectorAll("button")].find(
  (button) => button.textContent === "保存并重新审核",
);
assert.ok(saveRow, "Excel row editor did not retain the existing Owner save action");
saveRow.click();
await new Promise((resolve) => setTimeout(resolve, 30));
const composerPatch = calls.filter((call) => call.url.endsWith("/rows/33")).at(-1);
assert.equal(composerPatch.init.method, "PATCH", "Excel local draft did not use the existing PATCH endpoint");
assert.equal(JSON.parse(composerPatch.init.body).text, "中文组合输入草稿", "Excel patch did not carry the confirmed local draft");
pauseFirstRowPatch = true;
releaseFirstRowPatch = undefined;
await click("查看旧版本");
await new Promise((resolve) => setTimeout(resolve, 20));
const historyDialog = [...win.document.querySelectorAll("dialog")].find((dialog) => dialog.open && dialog.textContent.includes("历史上传内容版本"));
assert.ok(historyDialog?.classList.contains("xeb-history-dialog"), "history content uses the dedicated readable-width dialog rather than the generic narrow editor dialog");
assert.ok(win.document.body.textContent.includes("素材 #42"), "history did not retain the frozen cover material id");
await click("只读查看");
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(win.document.body.textContent.includes("历史第一页用户"), "history first page did not render");
assert.equal(win.document.body.textContent.includes("历史第二页用户"), false, "history must not prefetch its second cursor page");
const historyScroll = win.document.querySelector('[data-excel-history-page] .xeb-scroll');
assert.equal(historyScroll?.tabIndex, 0, "history table must be keyboard-focusable for horizontal scrolling");
assert.ok(
  historyScroll?.querySelector(".xeb-history-table"),
  "history fields must retain readable column widths inside the local horizontal scroller",
);
assert.equal(
  historyScroll?.getAttribute("aria-label"),
  "历史内容行字段；可横向滚动查看完整状态和版本追溯",
  "history table must explain its horizontal-scroll affordance",
);
assert.ok(
  win.document.body.textContent.includes("表格可横向滚动查看完整状态和版本追溯。"),
  "history table must make its trace fields discoverable on narrow screens",
);
assert.ok(
  win.document.body.textContent.includes("分层：A") &&
    win.document.body.textContent.includes("行状态：参与") &&
    win.document.body.textContent.includes("审核：已批准") &&
    win.document.body.textContent.includes("执行：发送成功") &&
    win.document.body.textContent.includes("发送时间：2026-10-01 00:00:00") &&
    win.document.body.textContent.includes("行 #33 · 行版本 #7") &&
    win.document.body.textContent.includes("内容版本 #1"),
  "history table must preserve segment, separate review/delivery, sent time, and row/content versions",
);
await click("查看内容");
await new Promise((resolve) => setTimeout(resolve, 20));
const historicalPresentation = win.document.querySelector('[data-v3-content-readonly="1"]');
assert.ok(historicalPresentation, "history row did not use the shared readonly content renderer");
assert.equal(historicalPresentation.tagName, "DIALOG", "history readonly presentation must enter the browser top layer");
assert.equal(historicalPresentation.querySelector(".aicrm-content-composer").hasAttribute("role"), false, "history readonly presentation must not expose a second dialog role");
assert.match(historicalPresentation.textContent, /所选历史版本的已保存内容/, "history readonly presentation must distinguish its frozen snapshot from the current saved row");
assert.ok(
  historicalPresentation.querySelector("img")?.src.includes("sha256%3Ahistoric-cover"),
  "historical readonly preview did not use the historical row card cover digest",
);
historicalPresentation.querySelector("button[data-v3-content-readonly-close]").click();
const historyNext = [
  ...(win.document.querySelector('[data-excel-page="history"]')?.querySelectorAll("button") || []),
].find((item) => item.textContent === "下一页");
assert.ok(historyNext && !historyNext.disabled, "history must expose its next cursor page");
historyNext.click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(win.document.body.textContent.includes("历史第二页用户"), "history next cursor page did not render");
assert.ok(
  win.document.body.textContent.includes("分层：B") &&
    win.document.body.textContent.includes("行状态：已排除") &&
    win.document.body.textContent.includes("审核：已拒绝") &&
    win.document.body.textContent.includes("执行：明确失败") &&
    win.document.body.textContent.includes("企微拒绝发送请求") &&
    win.document.body.textContent.includes("行 #999 · 行版本 #8"),
  "history must not collapse excluded or rejected rows into a single delivery label",
);
await click("查看内容");
await new Promise((resolve) => setTimeout(resolve, 20));
const unrecordedHistoricalPresentation = win.document.querySelector('[data-v3-content-readonly="1"]');
assert.ok(
  unrecordedHistoricalPresentation.textContent.includes("该内容版本未记录统一封面，不能推断为当前批次封面。"),
  "a historical row without a cover guessed the current batch cover",
);
assert.equal(
  unrecordedHistoricalPresentation.querySelector("img"),
  null,
  "a historical row without a cover rendered a substituted current cover",
);
unrecordedHistoricalPresentation.querySelector("button[data-v3-content-readonly-close]").click();
const closeHistory = [...win.document.querySelectorAll("dialog button")].find((item) => item.textContent === "关闭");
assert.ok(closeHistory, "history dialog did not expose close");
closeHistory.click();
await new Promise((resolve) => setTimeout(resolve, 10));
const coverInput = win.document.querySelector(
  'input[aria-label="统一封面图片"]',
);
assert.ok(
  coverInput.closest("label.admin-field"),
  "cover upload input did not use the standard field wrapper",
);
Object.defineProperty(coverInput, "files", {
  value: [new win.File(["fixture"], "cover.png", { type: "image/png" })],
});
failCoverUploadOnce = true;
await click("上传统一封面");
const feedback = win.document.querySelector("[data-excel-feedback]");
assert.ok(
  feedback.classList.contains("admin-alert--error") &&
    feedback.textContent.includes("批次服务暂时不可用"),
  "failed upload did not retain an error in the persistent feedback container",
);
assert.equal(
  win.document.querySelectorAll("[data-excel-feedback] .v3-action-busy").length,
  0,
  "feedback retained the action spinner after the failed upload completed",
);
assert.equal(
  [...win.document.querySelectorAll("button")].find(
    (item) => item.textContent === "上传统一封面",
  )?.getAttribute("aria-busy"),
  null,
  "failed upload left the action button busy",
);
assert.equal(
  approve().disabled,
  false,
  "a failed replacement upload changed the existing frozen-cover approval guard",
);
await click("上传统一封面");
assert.equal(cover, "sha256:cover");
contentResponseVersionOverrides.push(
  batch.current_content_version + 2,
  batch.current_content_version + 3,
);
const driftCoverInput = win.document.querySelector('input[aria-label="统一封面图片"]');
Object.defineProperty(driftCoverInput, "files", {
  value: [new win.File(["fixture"], "cover-drift.png", { type: "image/png" })],
});
await click("上传统一封面");
const driftReadbackFeedback = win.document.querySelector("[data-excel-feedback]");
assert.ok(
  driftReadbackFeedback.classList.contains("admin-alert--error") &&
    driftReadbackFeedback.textContent.includes("内容回读失败：内容版本持续变化，请重新读取后重试"),
  "a write followed by a drifting recovery page must fail its required readback",
);
assert.equal(
  driftReadbackFeedback.textContent.includes("统一封面已更新；请重新核对预览。"),
  false,
  "a write whose drift recovery failed must not show its success message",
);
await click("重新读取当前页");
assert.ok(
  win.document.body.textContent.includes("中文组合输入草稿"),
  "version-drift recovery did not refresh metadata and restore the stable content page",
);
pausePostWriteDetail = true;
await click("排除");
assert.equal(
  approve().disabled,
  true,
  "approval is disabled while a row write still holds the current batch version",
);
releaseFirstRowPatch();
for (let attempt = 0; attempt < 20 && !releasePostWriteDetail; attempt += 1)
  await new Promise((resolve) => setTimeout(resolve, 10));
assert.equal(typeof releasePostWriteDetail, "function", "the row write did not start its required readback");
assert.equal(
  approve().disabled,
  true,
  "batch mutation controls stay disabled until the post-write readback finishes",
);
releasePostWriteDetail();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(excluded, true);
failNextContentReadback = true;
await click("恢复");
assert.equal(excluded, false);
const readbackFeedback = win.document.querySelector("[data-excel-feedback]");
assert.ok(
  readbackFeedback.classList.contains("admin-alert--error") &&
    readbackFeedback.textContent.includes("内容回读失败：批次服务暂时不可用"),
  `a failed required GET readback must surface an error instead of a write-success message: ${readbackFeedback.className} ${readbackFeedback.textContent}`,
);
await click("审核通过并创建企微群发任务");
assert.equal(
  approved,
  1,
  "one click previews then submits one approval command",
);
assert.equal(
  [...win.document.querySelectorAll("button")].some((item) => item.textContent === "选择已有启用图片"),
  false,
  "submitted batch still exposed existing-cover selection",
);
assert.ok(win.document.body.textContent.includes("任务已创建，待员工执行"));
await click("发送效果与复盘");
assert.equal(
  win.document.querySelector('.xeb-detail-nav button[data-tab="effects"]')
    .dataset.selected,
  "true",
  "left navigation selection follows the active dimension",
);
assert.ok(win.document.body.textContent.includes("逐人回执"));
assert.ok(
  win.document.body.textContent.includes("分层来源：Excel；已按分层统计。"),
  "report identifies its actual segment source",
);
assert.ok(
  win.document.body.textContent.includes("第二页回执用户") === false,
  "effects must not prefetch the second receipt cursor page",
);
assert.ok(
  win.document.body.textContent.includes("2026-10-01 00:00:00"),
  "receipt delivery instants render in Shanghai time",
);
assert.ok(
  win.document.body.textContent.includes("状态待核对"),
  "unknown receipt states do not leak raw machine values",
);
assert.ok(
  win.document.body.textContent.includes("时间暂时无法显示"),
  "invalid receipt times do not leak raw text",
);
assert.equal(
  win.document.body.textContent.includes("服务失败 provider_error"),
  false,
  "mixed human and machine failure text is not business-facing feedback",
);
assert.ok(
  win.document.body.textContent.includes("失败原因待核对"),
  "unknown receipt reasons use a safe Chinese fallback",
);
assert.ok(
  win.document.body.textContent.includes("企微接口调用频率受限，请稍后重试"),
  "known provider rate-limit codes use their exact Chinese business label",
);
const receiptPager = () =>
  win.document.querySelector('[data-excel-page="receipts"]');
const receiptNext = [
  ...(receiptPager()?.querySelectorAll("button") || []),
].find((item) => item.textContent === "下一页");
assert.ok(receiptNext && !receiptNext.disabled, "receipts must expose the next cursor page");
receiptNext.click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(
  win.document.body.textContent.includes("第二页回执用户"),
  "receipts follow next_cursor only after the user requests the second page",
);
assert.ok(win.document.body.textContent.includes("100.0%"));
assert.equal(
  calls.some((call) => call.url.includes("/cloud-orchestrator/")),
  false,
  "Excel never routes through AI Assistant UI",
);
assert.ok(
  calls
    .filter((call) => call.url.endsWith("/approve"))
    .every((call) => call.init.headers["Idempotency-Key"]),
  "approval includes idempotency",
);
dom.window.close();
console.log(
  "Excel operation workspace list/detail, review, receipts, and report: PASS",
);

const visiblePageDom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const visiblePageWindow = visiblePageDom.window;
Object.defineProperty(visiblePageWindow.crypto, "randomUUID", {
  value: () => "00000000-0000-4000-8000-000000000099",
});
visiblePageWindow.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
visiblePageWindow.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
const waitForVisiblePage = async (check, message) => {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(message);
};
let strategyVersion = 1;
let initialContentReject;
let initialContentSignal;
let receiptResolve;
let reportResolve;
let firstContentPending = true;
let receiptPageTwoFailures = 0;
const contentRequestCursors = [];
const receiptRequestCursors = [];
const reportRequests = [];
const driftVersions = [];
let visibleStrategyReads = 0;
let nextVisibleContentBatch;
const visibleBatch = () => ({
  id: 801,
  state: "pending_review",
  version: strategyVersion,
  current_content_version: strategyVersion,
  summary: { total_rows: 151, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 151 },
});
visiblePageWindow.fetch = (raw, init = {}) => {
  const url = new URL(String(raw), visiblePageWindow.location.href);
  if (url.pathname === "/api/admin/operation-batches/strategy-summaries")
    return Promise.resolve(json({
      items: [{ strategy_key: "visible.fixture", title: "可见分页", latest_batch_status: "ready", latest_batch: visibleBatch() }],
      total: 1,
      limit: 20,
      offset: 0,
      has_more: false,
      next_offset: null,
    }));
  if (url.pathname === "/api/admin/operation-batches/legacy")
    return Promise.resolve(json({ items: [] }));
  if (url.pathname === "/api/admin/operation-batches/strategies/visible.fixture") {
    visibleStrategyReads += 1;
    return Promise.resolve(json({
      strategy: { strategy_key: "visible.fixture", title: "可见分页" },
      items: [visibleBatch()],
    }));
  }
  if (url.pathname === "/api/admin/operation-batches/801") {
    const cursor = url.searchParams.get("cursor") || "";
    contentRequestCursors.push(cursor);
    if (firstContentPending) {
      firstContentPending = false;
      initialContentSignal = init.signal;
      return new Promise((_, reject) => {
        initialContentReject = reject;
      });
    }
    const version = driftVersions.length ? driftVersions.shift() : strategyVersion;
    strategyVersion = version;
    const returnedBatch = nextVisibleContentBatch || visibleBatch();
    nextVisibleContentBatch = undefined;
    return Promise.resolve(json({
      batch: returnedBatch,
      rows: [{ unionid: `内容版本-${version}`, sender_userid: "staff", text: "可见页", card: {}, version: 1 }],
      next_cursor: "",
    }));
  }
  if (url.pathname === "/api/admin/operation-batches/801/receipts") {
    const cursor = url.searchParams.get("cursor") || "";
    receiptRequestCursors.push(cursor);
    if (!cursor)
      return new Promise((resolve) => {
        receiptResolve = resolve;
      });
    if (cursor === "receipt-page-2" && receiptPageTwoFailures++ === 0)
      return Promise.resolve(json({}, 503));
    return Promise.resolve(json({
      batch_id: 801,
      content_version: strategyVersion,
      items: [{ unionid: "回执第二页", sender_userid: "staff", delivery_state: "delivery_proven" }],
      next_cursor: "",
    }));
  }
  if (url.pathname === "/api/admin/operation-batches/801/report") {
    reportRequests.push(url.pathname);
    return new Promise((resolve) => {
      reportResolve = resolve;
    });
  }
  throw new Error(`unexpected visible-page request ${url.pathname}${url.search}`);
};
try {
  visiblePageWindow.eval(bundle.outputFiles[0].text + ";window.ExcelTest=ExcelTest;");
  await visiblePageWindow.ExcelTest.mountOperationExcelWorkspace(
    visiblePageWindow.document.getElementById("stage"),
  );
  const detail = [...visiblePageWindow.document.querySelectorAll("button")].find((node) => node.textContent === "查看详情");
  assert.ok(detail, "visible-page fixture did not render its detail action");
  detail.click();
  await waitForVisiblePage(() => Boolean(initialContentReject), "content first page did not start");
  assert.deepEqual(contentRequestCursors, [""], "content initial view must issue exactly one first-page request");
  assert.equal(receiptRequestCursors.length, 0, "content view must not read receipts");
  assert.equal(reportRequests.length, 0, "content view must not read the effects report");

  const effects = visiblePageWindow.document.querySelector('.xeb-detail-nav button[data-tab="effects"]');
  effects.click();
  await waitForVisiblePage(
    () => receiptRequestCursors.length === 1 && reportRequests.length === 1,
    "effects did not start its receipt page and report independently",
  );
  assert.equal(initialContentSignal?.aborted, true, "switching tabs must abort the stale content read");
  assert.ok(
    visiblePageWindow.document.body.textContent.includes("正在读取当前回执页"),
    "effects receipt region must stay loading while its own request is pending",
  );
  reportResolve(json({
    segment_source: "excel",
    has_segments: false,
    overall: { 12: { sent: 151, matured: 0, observing: 151, opened: 0, unavailable: 0, open_rate: null } },
    windows: { 12: { overall: { sent: 151, matured: 0, observing: 151, opened: 0, unavailable: 0, open_rate: null }, groups: {} } },
  }));
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("分层数据暂不可用，只显示总体"),
    "report did not render before the receipt page finished",
  );
  initialContentReject(new Error("stale content failed"));
  await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(
    visiblePageWindow.document.body.textContent.includes("内容页读取失败：stale content failed"),
    false,
    "late rejected content read must not overwrite the active effects state",
  );
  receiptResolve(json({
    batch_id: 801,
    content_version: 1,
    items: Array.from({ length: 50 }, (_, index) => ({ unionid: `回执-${index + 1}`, sender_userid: "staff", delivery_state: "delivery_proven" })),
    next_cursor: "receipt-page-2",
  }));
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("回执-50"),
    "first receipt page did not render",
  );
  const receiptNext = [
    ...(visiblePageWindow.document.querySelector('[data-excel-page="receipts"]')?.querySelectorAll("button") || []),
  ].find((node) => node.textContent === "下一页");
  receiptNext.click();
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("逐人回执暂不可读取：批次服务暂时不可用"),
    "receipt failure did not remain retryable",
  );
  const receiptRetry = [...visiblePageWindow.document.querySelectorAll("button")].find((node) => node.textContent === "重新读取当前回执页");
  receiptRetry.click();
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("回执第二页"),
    "receipt retry did not replay the exact failed cursor",
  );
  assert.deepEqual(
    receiptRequestCursors,
    ["", "receipt-page-2", "receipt-page-2"],
    "receipt retry must reuse its immutable page cursor",
  );

  strategyVersion = 10;
  driftVersions.push(11, 12);
  const content = visiblePageWindow.document.querySelector('.xeb-detail-nav button[data-tab="content"]');
  content.click();
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("内容页读取失败：内容版本持续变化，请重新读取后重试"),
    "a second version drift must stop after one explicit refresh",
  );
  assert.deepEqual(
    contentRequestCursors.slice(-2),
    ["", ""],
    "version drift may refresh only the visible first page once",
  );
  strategyVersion = 20;
  const strategyReadsBeforeDriftRetry = visibleStrategyReads;
  const driftRetry = [...visiblePageWindow.document.querySelectorAll("button")].find(
    (node) => node.textContent === "重新读取当前页",
  );
  assert.ok(driftRetry, "persistent version drift did not expose a recovery action");
  driftRetry.click();
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("内容版本-20"),
    "persistent version drift retry did not fetch fresh metadata then render the stable first page",
  );
  assert.equal(
    visibleStrategyReads,
    strategyReadsBeforeDriftRetry + 1,
    "persistent version drift retry must refresh metadata instead of reusing its old content version",
  );
  strategyVersion = 30;
  driftVersions.push(31);
  const contentTab = () => visiblePageWindow.document.querySelector('.xeb-detail-nav button[data-tab="content"]');
  contentTab().click();
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("内容版本-31"),
    "one version drift must refresh the visible first page and then render the stable version",
  );
  nextVisibleContentBatch = {
    ...visibleBatch(),
    state: "dispatching",
    version: 32,
    cover_image_id: 77,
    summary: { total_rows: 151, excluded_rows: 1, empty_title_rows: 0, expected_tasks: 150 },
  };
  contentTab().click();
  await waitForVisiblePage(
    () => visiblePageWindow.document.body.textContent.includes("状态：任务创建中"),
    "a fresh content page did not refresh stale batch header metadata",
  );
  assert.ok(
    visiblePageWindow.document.body.textContent.includes("冻结封面：素材 #77"),
    "fresh content metadata did not refresh the frozen-cover summary",
  );
  assert.equal(
    [...visiblePageWindow.document.querySelectorAll("button")].some((node) => node.textContent === "审核通过并创建企微群发任务"),
    false,
    "a fresh dispatching batch page retained an approval action closed over stale metadata",
  );
  assert.ok(
    visiblePageWindow.document.body.textContent.includes("内容版本-31"),
    "refreshing batch metadata did not preserve the visible content page",
  );
  console.log("Excel visible-page pagination, retry, abort, drift, and metadata: PASS");
} finally {
  visiblePageDom.window.close();
}

const historyRaceDom = new JSDOM("<!doctype html><main id=stage></main>", {
  url: "https://fixture.test/admin/operation-cycles",
  runScripts: "outside-only",
});
const historyRaceWindow = historyRaceDom.window;
Object.defineProperty(historyRaceWindow.crypto, "randomUUID", {
  value: () => "00000000-0000-4000-8000-000000000188",
});
historyRaceWindow.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
historyRaceWindow.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
const waitForHistoryRace = async (check, message) => {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(message);
};
const raceBatch = (id) => ({
  id,
  state: "pending_review",
  version: 1,
  current_content_version: 1,
  summary: { total_rows: 1, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 1 },
});
const historyVersionDTO = (revision, user) => ({
  batch_id: 901,
  content_version: {
    plan_id: 901,
    content_version: revision,
    file_digest: `sha256:version-${revision}`,
    cover_image_id: 42,
  },
  read_only: true,
  rows: [{ unionid: user, sender_userid: "staff", text: `版本 ${revision}`, card: {}, version: 1 }],
  next_cursor: "",
});
let holdVersionsList = true;
let releaseVersionsList;
let firstAResolve;
let secondAResolve;
let staleBReject;
let historicAReads = 0;
let historicBReads = 0;
historyRaceWindow.fetch = (raw) => {
  const url = new URL(String(raw), historyRaceWindow.location.href);
  if (url.pathname === "/api/admin/operation-batches/strategy-summaries")
    return Promise.resolve(json({
      items: [{ strategy_key: "history.race", title: "历史版本竞态", latest_batch_status: "ready", latest_batch: raceBatch(901) }],
      total: 1,
      limit: 20,
      offset: 0,
      has_more: false,
      next_offset: null,
    }));
  if (url.pathname === "/api/admin/operation-batches/legacy")
    return Promise.resolve(json({ items: [] }));
  if (url.pathname === "/api/admin/operation-batches/strategies/history.race")
    return Promise.resolve(json({
      strategy: { strategy_key: "history.race", title: "历史版本竞态" },
      items: [raceBatch(901), raceBatch(902)],
    }));
  if (url.pathname === "/api/admin/operation-batches/901")
    return Promise.resolve(json({ batch: raceBatch(901), rows: [], next_cursor: "" }));
  if (url.pathname === "/api/admin/operation-batches/902")
    return Promise.resolve(json({ batch: raceBatch(902), rows: [], next_cursor: "" }));
  if (url.pathname === "/api/admin/operation-batches/901/versions") {
    const result = {
      items: [
        { content_version: 1, cover_image_id: 42, created_at: "2026-09-09T00:00:00Z" },
        { content_version: 2, cover_image_id: 42, created_at: "2026-09-10T00:00:00Z" },
      ],
    };
    if (!holdVersionsList) return Promise.resolve(json(result));
    return new Promise((resolve) => {
      releaseVersionsList = () => resolve(json(result));
    });
  }
  if (url.pathname === "/api/admin/operation-batches/901/versions/1") {
    historicAReads += 1;
    if (historicAReads === 1)
      return new Promise((resolve) => {
        firstAResolve = () => resolve(json(historyVersionDTO(1, "旧 A 响应")));
      });
    if (historicAReads === 2)
      return new Promise((resolve) => {
        secondAResolve = () => resolve(json(historyVersionDTO(1, "当前 A 响应")));
      });
    return Promise.resolve(json(historyVersionDTO(1, "最终 A 响应")));
  }
  if (url.pathname === "/api/admin/operation-batches/901/versions/2") {
    historicBReads += 1;
    if (historicBReads === 1)
      return Promise.resolve(json(historyVersionDTO(2, "版本 B 响应")));
    return new Promise((_, reject) => {
      staleBReject = () => reject(new Error("旧 B 错误"));
    });
  }
  throw new Error(`unexpected history-race request ${url.pathname}${url.search}`);
};
try {
  historyRaceWindow.eval(bundle.outputFiles[0].text + ";window.ExcelTest=ExcelTest;");
  await historyRaceWindow.ExcelTest.mountOperationExcelWorkspace(
    historyRaceWindow.document.getElementById("stage"),
  );
  const detail = [...historyRaceWindow.document.querySelectorAll("button")].find((node) => node.textContent === "查看详情");
  assert.ok(detail, "history-race fixture did not render its detail action");
  detail.click();
  await waitForHistoryRace(
    () => historyRaceWindow.document.body.textContent.includes("当前批次 #901"),
    "history-race fixture did not render batch 901",
  );
  const history = () => [...historyRaceWindow.document.querySelectorAll("button")].find((node) => node.textContent === "查看旧版本");
  history().click();
  await waitForHistoryRace(() => Boolean(releaseVersionsList), "history version list did not start");
  const batchSelect = historyRaceWindow.document.querySelector('select[aria-label="历史批次"]');
  batchSelect.value = "902";
  batchSelect.dispatchEvent(new historyRaceWindow.Event("change"));
  await waitForHistoryRace(
    () => historyRaceWindow.document.body.textContent.includes("当前批次 #902"),
    "switching batch did not complete before delayed history list returned",
  );
  releaseVersionsList();
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(
    historyRaceWindow.document.querySelectorAll("dialog").length,
    0,
    "a history list started for an old batch must not attach its dialog after the batch switches",
  );
  batchSelect.value = "901";
  batchSelect.dispatchEvent(new historyRaceWindow.Event("change"));
  await waitForHistoryRace(
    () => historyRaceWindow.document.body.textContent.includes("当前批次 #901"),
    "switching back to batch 901 did not complete",
  );
  holdVersionsList = false;
  history().click();
  await waitForHistoryRace(
    () => historyRaceWindow.document.querySelectorAll("dialog").length === 1,
    "current history dialog did not open",
  );
  const versionButtons = () => [...historyRaceWindow.document.querySelectorAll("dialog table button")];
  versionButtons()[0].click();
  await waitForHistoryRace(() => Boolean(firstAResolve), "first A history page did not start");
  versionButtons()[1].click();
  await waitForHistoryRace(
    () => historyRaceWindow.document.body.textContent.includes("版本 B 响应"),
    "version B did not replace the first A view",
  );
  versionButtons()[0].click();
  await waitForHistoryRace(() => Boolean(secondAResolve), "second A history page did not start");
  secondAResolve();
  await waitForHistoryRace(
    () => historyRaceWindow.document.body.textContent.includes("当前 A 响应"),
    "current A Host DTO did not render",
  );
  firstAResolve();
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(
    historyRaceWindow.document.body.textContent.includes("旧 A 响应"),
    false,
    "late success from the first A request must not overwrite the later A view",
  );
  assert.ok(
    historyRaceWindow.document.body.textContent.includes("当前 A 响应"),
    "late success from the first A request replaced the current A view",
  );
  versionButtons()[1].click();
  await waitForHistoryRace(() => Boolean(staleBReject), "stale B history page did not start");
  versionButtons()[0].click();
  await waitForHistoryRace(
    () => historyRaceWindow.document.body.textContent.includes("最终 A 响应"),
    "final A history page did not render",
  );
  staleBReject();
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(
    historyRaceWindow.document.body.textContent.includes("旧 B 错误"),
    false,
    "late failure from the stale B request must not replace the current A view",
  );
  assert.ok(
    historyRaceWindow.document.body.textContent.includes("最终 A 响应"),
    "late failure from the stale B request replaced the current A view",
  );
  console.log("Excel history Host DTO, batch switch, and ABA races: PASS");
} finally {
  historyRaceDom.window.close();
}

const feedbackDom = new JSDOM(
  '<!doctype html><button id="unowned-send">发送未接入操作</button><main id="stage"></main>',
  { url: "https://fixture.test/admin/operation-cycles", runScripts: "outside-only" },
);
const feedbackWindow = feedbackDom.window;
Object.defineProperty(feedbackWindow.crypto, "randomUUID", {
  value: () => "00000000-0000-4000-8000-000000000001",
});
feedbackWindow.HTMLDialogElement.prototype.showModal = function () {
  this.open = true;
};
feedbackWindow.HTMLDialogElement.prototype.close = function () {
  this.open = false;
};
let detailReads = 0;
let effectReads = 0;
let feedbackRequests = 0;
feedbackWindow.fetch = async (input) => {
  const url = new URL(String(input), feedbackWindow.location.href);
  feedbackRequests += 1;
  if (url.pathname === "/api/admin/operation-batches/strategy-summaries")
    return json({
      items: [{ strategy_key: "feedback.fixture", title: "反馈测试计划", status: "active", latest_batch_status: "ready", latest_batch: { id: 701, state: "pending_review", summary: { expected_tasks: 1 } } }],
      total: 1,
      limit: 20,
      offset: 0,
      has_more: false,
      next_offset: null,
    });
  if (url.pathname === "/api/admin/operation-batches/legacy")
    return json({ items: [] });
  if (url.pathname === "/api/admin/operation-batches/strategies/feedback.fixture")
    return json({ strategy: { strategy_key: "feedback.fixture", title: "反馈测试计划" }, items: [{ id: 701, state: "pending_review", version: 1, current_content_version: 1, summary: { total_rows: 1, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 1 } }] });
  if (url.pathname === "/api/admin/operation-batches/701") {
    detailReads += 1;
    return json({ batch: { id: 701, state: "pending_review", version: 1, current_content_version: 1, summary: { total_rows: 1, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 1 } }, rows: [], next_cursor: "" });
  }
  if (url.pathname === "/api/admin/operation-batches/701/receipts") {
    effectReads += 1;
    return json({ batch_id: 701, content_version: 1, items: [], next_cursor: "" });
  }
  if (url.pathname === "/api/admin/operation-batches/701/report")
    return json({ segment_source: "excel", has_segments: true, overall: {}, windows: {} });
  throw new Error(`unexpected feedback ownership request ${url.pathname}${url.search}`);
};
const waitForFeedback = async (check, message) => {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(message);
};
try {
  feedbackWindow.eval(feedbackBundle.outputFiles[0].text + ";window.ExcelFeedbackTest=ExcelFeedbackTest;");
  feedbackWindow.ExcelFeedbackTest.initFeedback();
  feedbackWindow.eval(bundle.outputFiles[0].text + ";window.ExcelTest=ExcelTest;");
  await feedbackWindow.ExcelTest.mountOperationExcelWorkspace(feedbackWindow.document.getElementById("stage"));
  await waitForFeedback(
    () => [...feedbackWindow.document.querySelectorAll("button")].some((node) => node.textContent === "查看详情"),
    "feedback ownership fixture did not render its plan action",
  );
  const detail = [...feedbackWindow.document.querySelectorAll("button")].find((node) => node.textContent === "查看详情");
  assert.equal(detail.__dcBound, true, "Excel action did not declare ownership before feedback classification");
  assert.equal(detail.dataset.capabilityState, "real", "Excel action was not classified as real");
  detail.click();
  await waitForFeedback(
    () => feedbackWindow.document.querySelector('.xeb-detail-nav button[data-tab="content"]'),
    "feedback ownership fixture did not render detail tabs",
  );
  const content = feedbackWindow.document.querySelector('.xeb-detail-nav button[data-tab="content"]');
  const effects = feedbackWindow.document.querySelector('.xeb-detail-nav button[data-tab="effects"]');
  assert.equal(content.__dcBound, true, "content tab was not owned before its capture-phase click");
  assert.equal(effects.__dcBound, true, "effects tab was not owned before its capture-phase click");
  const toast = feedbackWindow.document.getElementById("fb-toast");
  const readsBeforeEffects = effectReads;
  effects.click();
  await waitForFeedback(
    () => effects.dataset.selected === "true" && effectReads > readsBeforeEffects,
    "effects tab did not run its existing loadSelected handler",
  );
  const readsBeforeContent = detailReads;
  content.click();
  await waitForFeedback(
    () => content.dataset.selected === "true" && detailReads > readsBeforeContent,
    "content tab did not run its existing loadSelected handler",
  );
  assert.doesNotMatch(toast.textContent || "", /后端能力未就绪/, "owned Excel tabs still showed an unavailable-backend toast");
  const requestsBeforeUnowned = feedbackRequests;
  feedbackWindow.document.getElementById("unowned-send").click();
  assert.match(toast.textContent || "", /后端能力未就绪/, "an unowned business action lost the shared feedback guard");
  assert.equal(feedbackRequests, requestsBeforeUnowned, "unowned business action issued a request");
  console.log("Excel action feedback ownership: PASS");
} finally {
  feedbackDom.window.close();
}
