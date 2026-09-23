import { JSDOM } from "jsdom";
import { build } from "esbuild";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../..");
const donor = fs.readFileSync(path.join(here, "static/admin_console/owner_migration_dd8d60d.html"), "utf8");
const output = await build({ entryPoints: [path.join(here, "static_src/admin_console/owner_handoff_host.ts")], bundle: true, format: "iife", platform: "browser", target: "es2022", write: false, logLevel: "silent" });
const host = output.outputFiles[0].text;
const wait = () => new Promise(resolve => setTimeout(resolve, 40));
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body, text: async () => body });

async function mountFixture(contextBody, contextStatus = 200, exercisePicker = false, exerciseLegacyImport = false, exercisePreview = false, previewStatus = 200) {
  const requests = [];
  const pickerOpens = [];
  const dom = new JSDOM('<!doctype html><html><body><main data-owner-handoff-host></main></body></html>', {
    url: "https://owner-host.fixture/admin/owner-migration", runScripts: "outside-only", pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = Headers;
      window.AICRMStaffPicker = { open(options) {
        pickerOpens.push({ source: options.source, scope: options.scope, title: options.title, hint: options.directoryHint });
        const controller = new AbortController();
        void options.loadPage({ query: "", signal: controller.signal }).then((page) => {
          const selected = page.items.find((item) => options.title.includes("原负责人") ? item.active === false : item.active !== false);
          if (selected) options.onCommit({ selected: [selected], added: [selected], removed: [] });
        });
      } };
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        const method = init.method || "GET";
        requests.push({ path: url.pathname, query: url.search, method, body: init.body || "" });
        if (url.pathname === "/static/admin_console/owner_migration_dd8d60d.html" && method === "GET") return response(donor);
        if (url.pathname === "/api/admin/customers/owner-handoffs/context" && method === "GET") return response(contextBody, contextStatus);
        if (url.pathname === "/api/admin/common/operation-members" && method === "GET") {
          const inactive = url.searchParams.get("include_inactive") === "true";
          return response({ items: (contextBody.staff || []).filter((staff) => inactive || staff.Active !== false).map((staff) => ({ staff_id: staff.ID, user_id: staff.UserID, display_name: staff.DisplayName, active: staff.Active })) });
        }
        if (url.pathname === "/api/admin/customers/owner-handoffs/previews" && method === "POST") {
          if (previewStatus !== 200) return response({ error: "provider_unavailable（服务暂不可用）" }, previewStatus);
          return response({
            ID: "preview-1", Hash: "preview-hash", ConfirmationPhrase: "确认迁移", Mode: "wecom_then_crm",
            Rows: [
              { Line: 1, CustomerID: 21, ExternalUserID: "owner-ready", CustomerDisplayName: "已知客户", CurrentOwnerUserID: "inactive-source", State: "ready", Reason: "" },
              { Line: 2, CustomerID: 22, ExternalUserID: "owner-unknown", CustomerDisplayName: "待核实客户", CurrentOwnerUserID: "inactive-source", State: "outcome_unknown", Reason: "provider_unavailable" },
            ],
          });
        }
        if (url.pathname === "/api/admin/customers/owner-handoffs/confirm" && method === "POST") {
          return response({ ID: "batch-1", State: "accepted", Mode: "wecom_then_crm", Lines: [{ Line: 2, CustomerID: 22, State: "outcome_unknown", TransferStatus: 2 }] });
        }
        return response({ error: "unexpected fixture route" }, 500);
      };
    },
  });
  dom.window.eval(host);
  await wait(); await wait();
  const stage = dom.window.document.querySelector("[data-owner-handoff-host]");
  const page = stage?.querySelector("[data-owner-migration-page]");
  if (exercisePicker && page) {
    page.querySelector('[data-owner-picker="source"]')?.click();
    await wait(); await wait();
    page.querySelector('[data-owner-picker="target"]')?.click();
    await wait(); await wait();
  }
  if (exerciseLegacyImport && page) {
    const csv = [
      "external_userid,是否迁移,当前负责人userid,客户备注名,备注",
      "browser-external,是,inactive-source,已知客户,可迁移",
      "browser-external,是,inactive-source,重复客户,不可迁移",
      ",是,inactive-source,缺少 external_userid,不可迁移",
      "browser-invalid,maybe,inactive-source,非法标记,不可迁移",
      "browser-skipped,否,inactive-source,文件跳过,保留",
      "browser-mismatch,是,other-owner,负责人不符,保留",
    ].join("\n");
    const file = new Blob([csv], { type: "text/csv" });
    Object.defineProperty(file, "name", { value: "legacy-owner-list.xls" });
    const input = page.querySelector("[data-import-file]");
    Object.defineProperty(input, "files", { configurable: true, value: [file] });
    page.querySelector('[data-scope-segment="excel_include"]')?.click();
    page.querySelector("[data-upload-file]")?.click();
    await wait(); await wait(); await wait();
  }
  if (exercisePreview && page) {
    page.querySelector("[data-preview]")?.click();
    await wait(); await wait();
    const phrase = page.querySelector("[data-confirm-phrase-input]");
    phrase.value = "确认迁移";
    page.querySelector("[data-execute]")?.click();
    await wait(); await wait();
  }
  const diagnostic = {
    init: stage?.dataset.ownerHandoffInit || "missing",
    http_status: stage?.dataset.ownerHandoffInitStatus || "",
    page: Boolean(page),
    owner_heading: page?.querySelector("h1")?.textContent?.trim() || "",
    scope_copy: [...(page?.querySelectorAll(".owner-migration-hint") || [])].map((node) => node.textContent?.trim() || ""),
    confirm_placeholder: page?.querySelector("[data-confirm-phrase-input]")?.getAttribute("placeholder") || "",
    has_curly_marker: Boolean(page?.innerHTML.includes("{{")),
    has_block_marker: Boolean(page?.innerHTML.includes("{%")),
    operator_ready: Boolean(page?.querySelector("#operator")?.value.startsWith("管理员 #")),
    welcome_ready: page?.querySelector("[data-transfer-welcome-msg]")?.value === "您好，后续将由新的服务同事继续为您服务。",
    wecom_checked: Boolean(page?.querySelector("[data-include-wecom-transfer]")?.checked),
    donor_gets: requests.filter(request => request.path === "/static/admin_console/owner_migration_dd8d60d.html" && request.method === "GET").length,
    context_gets: requests.filter(request => request.path === "/api/admin/customers/owner-handoffs/context" && request.method === "GET").length,
    user_message: stage?.textContent || "",
    picker_opens: pickerOpens,
    member_reads: requests.filter(request => request.path === "/api/admin/common/operation-members"),
    preview_body: requests.find(request => request.path === "/api/admin/customers/owner-handoffs/previews")?.body || "",
    source_id: page?.querySelector('[data-owner-userid="source"]')?.value || "",
    target_id: page?.querySelector('[data-owner-userid="target"]')?.value || "",
    import_visible: !page?.querySelector("[data-import-summary]")?.hidden,
    import_filename: page?.querySelector("[data-import-filename]")?.textContent || "",
    import_total_rows: page?.querySelector('[data-import-stat="total_rows"]')?.textContent || "",
    import_unique_external_userids: page?.querySelector('[data-import-stat="unique_external_userids"]')?.textContent || "",
    import_marked_move: page?.querySelector('[data-import-stat="marked_move"]')?.textContent || "",
    import_marked_skip: page?.querySelector('[data-import-stat="marked_skip"]')?.textContent || "",
    import_duplicate_rows: page?.querySelector('[data-import-stat="duplicate_rows"]')?.textContent || "",
    import_invalid_rows: page?.querySelector('[data-import-stat="invalid_rows"]')?.textContent || "",
    preview_states: [...(page?.querySelectorAll("[data-preview-rows] .owner-migration-status") || [])].map((node) => node.textContent || ""),
    preview_reasons: [...(page?.querySelectorAll("[data-preview-rows] td:last-child") || [])].map((node) => node.textContent || ""),
    batch_id: page?.dataset.ownerHandoffBatchId || "",
    execution_log: page?.querySelector("[data-execution-log]")?.textContent || "",
    notice: page?.querySelector("[data-workbench-notice]")?.textContent || "",
  };
  dom.window.close();
  return diagnostic;
}

const ready = await mountFixture({ staff: [], operator: "管理员 #42" });
if (ready.init !== "ready" || !ready.page || ready.has_curly_marker || ready.has_block_marker || !ready.operator_ready || !ready.welcome_ready || !ready.wecom_checked || ready.donor_gets !== 1 || ready.context_gets !== 1 || ready.owner_heading !== "用户负责人迁移 / 在职继承" || !ready.scope_copy.includes("迁移原负责人当前全部候选用户。保留现有能力。") || ready.confirm_placeholder !== "确认将 0 个用户从 source 迁移到 target") throw new Error(`owner handoff Host ready fixture mismatch ${JSON.stringify(ready)}`);

const denied = await mountFixture({ error: "forbidden fixture" }, 403);
if (denied.init !== "context_error" || denied.page || denied.http_status !== "403" || denied.donor_gets !== 1 || denied.context_gets !== 1 || denied.user_message !== "负责人迁移页面不可用。") throw new Error(`owner handoff Host context failure fixture mismatch ${JSON.stringify(denied)}`);


const picker = await mountFixture({
  staff: [
    { ID: 11, UserID: "inactive-source", DisplayName: "Inactive source", Active: false },
    { ID: 12, UserID: "active-target", DisplayName: "Active target", Active: true },
  ],
  operator: "管理员 #42",
}, 200, true);
if (picker.init !== "ready" || picker.source_id !== "11" || picker.target_id !== "12" || JSON.stringify(picker.picker_opens) !== JSON.stringify([
  { source: "owner_migration.operation_members", scope: "owner_migration", title: "选择原负责人", hint: "本页只显示前 100 项或搜索结果；未出现的原选择仍保留，不能据此判定失效。" },
  { source: "owner_migration.operation_members", scope: "owner_migration", title: "选择目标负责人", hint: "本页只显示前 100 项或搜索结果；未出现的原选择仍保留，不能据此判定失效。" },
]) || !picker.member_reads.some((request) => request.query.includes('include_inactive=true')) || !picker.member_reads.some((request) => request.query.includes('include_inactive=false'))) throw new Error(`owner handoff Host picker contract mismatch ${JSON.stringify(picker)}`);

const legacyImport = await mountFixture({
  staff: [
    { ID: 11, UserID: "inactive-source", DisplayName: "Inactive source", Active: false },
    { ID: 12, UserID: "active-target", DisplayName: "Active target", Active: true },
  ],
  operator: "管理员 #42",
}, 200, true, true);
if (legacyImport.init !== "ready" || legacyImport.source_id !== "11" || legacyImport.target_id !== "12" || !legacyImport.import_visible || legacyImport.import_filename !== "legacy-owner-list.xls" || legacyImport.import_total_rows !== "6" || legacyImport.import_unique_external_userids !== "4" || legacyImport.import_marked_move !== "2" || legacyImport.import_marked_skip !== "1" || legacyImport.import_duplicate_rows !== "1" || legacyImport.import_invalid_rows !== "2") throw new Error(`owner handoff Host legacy file-import fixture mismatch ${JSON.stringify(legacyImport)}`);

const legacyPreview = await mountFixture({
  staff: [
    { ID: 11, UserID: "inactive-source", DisplayName: "Inactive source", Active: false },
    { ID: 12, UserID: "active-target", DisplayName: "Active target", Active: true },
  ],
  operator: "管理员 #42",
}, 200, true, true, true);
if (!legacyPreview.preview_reasons.includes("当前负责人标识与选择的原负责人不一致。") || legacyPreview.preview_reasons.some((reason) => reason.includes("userid") || reason.includes("external_userid"))) throw new Error(`owner handoff Host must map local row reasons without technical identifiers ${JSON.stringify(legacyPreview)}`);

const visibleStates = await mountFixture({
  staff: [
    { ID: 11, UserID: "inactive-source", DisplayName: "Inactive source", Active: false },
    { ID: 12, UserID: "active-target", DisplayName: "Active target", Active: true },
  ],
  operator: "管理员 #42",
}, 200, true, false, true);
if (JSON.stringify(visibleStates.preview_states) !== JSON.stringify(["可迁移", "结果待核实"]) || !visibleStates.preview_reasons.includes("迁移原因待确认。") || visibleStates.batch_id !== "batch-1" || !visibleStates.execution_log.includes("迁移方式：先企微转接后本地迁移") || !visibleStates.execution_log.includes("批次状态：已受理") || !visibleStates.execution_log.includes("结果待核实") || visibleStates.execution_log.includes("wecom_then_crm") || visibleStates.execution_log.includes("outcome_unknown")) throw new Error(`owner handoff Host must present states in Chinese while retaining the response-bound batch reference ${JSON.stringify(visibleStates)}`);

const previewFailure = await mountFixture({
  staff: [
    { ID: 11, UserID: "inactive-source", DisplayName: "Inactive source", Active: false },
    { ID: 12, UserID: "active-target", DisplayName: "Active target", Active: true },
  ],
  operator: "管理员 #42",
}, 200, true, false, true, 503);
if (previewFailure.notice !== "迁移服务暂不可用，请稍后重试。" || previewFailure.notice.includes("provider_unavailable")) throw new Error(`owner handoff Host must not expose provider status codes ${JSON.stringify(previewFailure)}`);

console.log("owner_handoff_host: PASS");

// The V3 session rejects superseded loader results. The Host also keeps its
// staff_id → UserID map commit-owned: a late old page must not corrupt the
// actual Excel preview after a newer source employee has been selected.
let releaseOldDirectory;
const lateDirectory = new JSDOM('<!doctype html><html><body><main data-owner-handoff-host></main></body></html>', {
  url: 'https://owner-host.fixture/admin/owner-migration', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = Headers;
    window.AICRMStaffPicker = { open(options) { window.__ownerPickerOptions = options; } };
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = String(init.method || 'GET').toUpperCase();
      if (url.pathname === '/static/admin_console/owner_migration_dd8d60d.html') return response(donor);
      if (url.pathname === '/api/admin/customers/owner-handoffs/context') return response({ staff: [{ ID: 14, UserID: 'target-user', DisplayName: '目标负责人', Active: true }], operator: '管理员 #42' });
      if (url.pathname === '/api/admin/common/operation-members') {
        if (url.searchParams.get('q') === 'old') return new Promise((resolve) => { releaseOldDirectory = () => resolve(response({ items: [{ staff_id: 12, user_id: 'old-source', display_name: '旧目录结果', active: false }] })); });
        return response({ items: [{ staff_id: 13, user_id: 'new-source', display_name: '新目录结果', active: false }] });
      }
      if (url.pathname === '/api/admin/customers/owner-handoffs/previews' && method === 'POST') {
        window.__ownerPreviewBody = init.body;
        return response({ ID: 'preview-race', Hash: 'race-hash', ConfirmationPhrase: '确认迁移', Mode: 'local_only', Rows: [] });
      }
      return response({ error: 'unexpected route' }, 500);
    };
  },
});
lateDirectory.window.eval(host);
await wait(); await wait();
const latePage = lateDirectory.window.document.querySelector('[data-owner-migration-page]');
latePage.querySelector('[data-owner-userid="target"]').value = '14';
latePage.querySelector('[data-owner-picker="source"]').click();
await wait();
const sourceOptions = lateDirectory.window.__ownerPickerOptions;
if (!sourceOptions || sourceOptions.title !== '选择原负责人') throw new Error('owner handoff race fixture did not open the source V3 picker');
const stale = sourceOptions.loadPage({ query: 'old', signal: new AbortController().signal });
const current = await sourceOptions.loadPage({ query: 'new', signal: new AbortController().signal });
sourceOptions.onCommit({ selected: [current.items[0]], added: [current.items[0]], removed: [] });
releaseOldDirectory();
await stale;
const csv = new Blob(['external_userid,是否迁移,当前负责人userid,客户备注名,备注\nrace-external,是,new-source,新映射,验证'], { type: 'text/csv' });
Object.defineProperty(csv, 'name', { value: 'mapping-race.csv' });
const fileInput = latePage.querySelector('[data-import-file]');
Object.defineProperty(fileInput, 'files', { configurable: true, value: [csv] });
latePage.querySelector('[data-scope-segment="excel_include"]').click();
latePage.querySelector('[data-upload-file]').click();
await wait(); await wait();
latePage.querySelector('[data-preview]').click();
await wait(); await wait();
const raceBody = JSON.parse(lateDirectory.window.__ownerPreviewBody || '{}');
if (raceBody.source_staff_id !== 13 || raceBody.target_staff_id !== 14 || JSON.stringify(raceBody.external_userids) !== JSON.stringify(['race-external'])) throw new Error(`owner handoff late directory result changed the preview mapping ${JSON.stringify(raceBody)}`);
lateDirectory.window.close();

console.log('owner_handoff_host: mapping race PASS');
