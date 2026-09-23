import { ownerMigrationRowsFromFile, ownerMigrationTemplateXLSX, ownerMigrationWorkbookXLSX } from "./owner_migration_file";

type Staff = { ID: number; UserID: string; DisplayName: string; Active: boolean };
type Context = { staff: Staff[]; operator: string };
type Row = { Line: number; CustomerID: number; ExpectedOwnerID: number; ExternalUserID: string; CustomerDisplayName: string; CurrentOwnerUserID: string; State: string; Reason: string };
type Preview = { ID: string; Hash: string; ConfirmationPhrase: string; Rows: Row[]; Mode: string };
type BatchLine = { Line: number; CustomerID: number; State: string; TransferStatus: number };
type Batch = { ID: string; State: string; Mode: string; Lines?: BatchLine[] };
type ImportedRow = { Line: number; ExternalUserID: string; MoveFlag: string; CurrentOwnerUserID: string; CustomerDisplayName: string; Remark: string; ParseStatus: string; ParseReason: string };
type DisplayRow = { Line: number; ExternalUserID: string; CustomerDisplayName: string; MoveFlag: string; CurrentOwnerUserID: string; Remark: string; State: string; Reason: string; CustomerID?: number };
type StaffPickerRecord = { source: string; staff_id: string; user_id: string; display_name: string; active?: boolean; unavailable_reason?: string };
type StaffPicker = { open(options: {
  title: string; source: string; scope: string; selectedRecords: StaffPickerRecord[]; mode: "single"; limit: number; directoryHint: string;
  loadPage(request: { query: string; cursor?: string; signal: AbortSignal }): Promise<{ items: StaffPickerRecord[]; nextCursor?: string }>;
  refresh(request: { query: string; signal: AbortSignal }): Promise<void>;
  accessLossMessage(error: unknown): string | undefined;
  onCommit(result: { selected: StaffPickerRecord[] }): void;
}): void };

declare global { interface Window { AICRMStaffPicker?: StaffPicker } }

const donorURL = "/static/admin_console/owner_migration_dd8d60d.html";
const key = () => `owner-handoff-${crypto.getRandomValues(new Uint32Array(2)).join("-")}`;
const text = (value: unknown) => String(value ?? "").trim();
const esc = (value: unknown) => text(value).replace(/[&<>'"]/g, char => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", "'": "&#39;", '"': "&quot;" }[char] || char));

type RequestFailure = Error & { httpStatus?: number; userMessage?: true };
const requestFailure = (message: string, status: number): RequestFailure => {
  const error = new Error(message) as RequestFailure;
  error.httpStatus = status;
  error.userMessage = true;
  return error;
};

async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = new Headers(init?.headers);
  if ((init?.method || "GET").toUpperCase() !== "GET") {
    if (!headers.has("Content-Type")) headers.set("Content-Type", "application/json");
    if (!headers.has("X-CSRF-Token")) {
      const cookie = document.cookie.split(";").map(part => part.trim()).find(part => part.startsWith("aicrm_admin_csrf="));
      if (cookie) headers.set("X-CSRF-Token", decodeURIComponent(cookie.slice("aicrm_admin_csrf=".length)));
    }
  }
  const response = await fetch(path, { credentials: "same-origin", ...init, headers });
  const body = await response.json().catch(() => ({}));
  if (!response.ok) throw requestFailure(ownerHandoffRequestMessage(response.status, text(body.error)), response.status);
  return body as T;
}

const localOwnerHandoffMessages = new Set([
  "请先选择不同的原负责人和目标负责人",
  "请选择包含旧模板五列的 XLSX、XLS 或 CSV 文件",
  "每一行必须包含旧模板的五列",
  "请先上传旧模板名单",
  "请先生成预览",
  "确认短语不匹配",
]);

function ownerHandoffRequestMessage(status: number, detail: string): string {
  const mapped = ({
    owner_handoff_provider_unavailable: "迁移服务暂不可用，请稍后重试。",
    owner_handoff_conflict: "迁移状态已变化，请重新生成预览后重试。",
    invalid_request: "迁移请求无效，请检查填写内容后重试。",
  } as Record<string, string>)[detail];
  if (mapped) return mapped;
  if (status === 401) return "登录状态已失效，请重新登录后继续。";
  if (status === 403) return "没有负责人迁移操作权限。";
  if (status === 404) return "迁移记录不存在或已不可读取，请重新生成预览。";
  if (status === 409) return "迁移状态已变化，请重新生成预览后重试。";
  if (status === 400 || status === 405 || status === 422) return "迁移请求无效，请检查填写内容后重试。";
  if (status >= 500) return "迁移服务暂不可用，请稍后重试。";
  return "迁移请求失败，请稍后重试。";
}

function ownerHandoffErrorMessage(error: unknown, fallback: string): string {
  if (error instanceof Error && (error as RequestFailure).userMessage) return error.message;
  const detail = error instanceof Error ? text(error.message) : "";
  return localOwnerHandoffMessages.has(detail) ? detail : fallback;
}

function scrubFrozenServerPlaceholders(page: HTMLElement): void {
  const marker = /\{\{|\{%/;
  const replacement = /\{\{[\s\S]*?\}\}|\{%[\s\S]*?%\}/g;
  [page, ...page.querySelectorAll<HTMLElement>("*")].forEach(element => {
    [...element.attributes].forEach(attribute => {
      if (marker.test(attribute.name)) element.removeAttribute(attribute.name);
      else if (marker.test(attribute.value)) element.setAttribute(attribute.name, attribute.value.replace(replacement, ""));
    });
  });
  const walker = document.createTreeWalker(page, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) node.nodeValue = (node.nodeValue || "").replace(replacement, "");
}

function adaptFrozenDonorCopy(page: HTMLElement): void {
  // The donor remains byte-frozen. Adapt only its known static labels after
  // mounting; imported values, legacy file headers and protocol tokens stay
  // untouched.
  const exact: Array<[string, string, string]> = [
    ["h1", "客户负责人迁移 / 在职继承", "用户负责人迁移 / 在职继承"],
    [".owner-migration-subtitle", "先完成企微客户转接，再同步 CRM 本地归属；执行前必须预览。", "先完成企微用户转接，再同步 CRM 本地归属；执行前必须预览。"],
    [".owner-migration-switch-line span", "先调用企微官方转接接口；企微成功的客户才同步 CRM。", "先调用企微官方转接接口；企微成功的用户才同步 CRM。"],
    ["[data-confirm-phrase-input]", "确认将 0 个客户从 source 迁移到 target", "确认将 0 个用户从 source 迁移到 target"],
  ];
  exact.forEach(([selector, source, target]) => {
    page.querySelectorAll<HTMLElement>(selector).forEach((node) => {
      if (node.textContent?.trim() === source) node.textContent = target;
      if (node instanceof HTMLInputElement && node.placeholder === source) node.placeholder = target;
    });
  });
  const replacements = new Map<string, string>([
    ["迁移原负责人当前全部候选客户。保留现有能力。", "迁移原负责人当前全部候选用户。保留现有能力。"],
    ["只迁移 Excel 中标记为“是”的客户。", "只迁移 Excel 中标记为“是”的用户。"],
    ["去重后客户数", "去重后用户数"],
    ["可迁移客户", "可迁移用户"],
    ["不可迁移客户", "不可迁移用户"],
    ["请求客户数", "请求用户数"],
  ]);
  page.querySelectorAll<HTMLElement>(".owner-migration-hint, .owner-migration-stat-label").forEach((node) => {
    const source = node.textContent?.trim() || "";
    const target = replacements.get(source);
    if (target) node.textContent = target;
  });
}

async function mountFrozenDonor(stage: HTMLElement): Promise<HTMLElement> {
  const response = await fetch(donorURL, { credentials: "same-origin" });
  if (!response.ok) throw requestFailure("负责人迁移页面暂不可用，请刷新后重试。", response.status);
  const source = new DOMParser().parseFromString(await response.text(), "text/html");
  const page = source.querySelector<HTMLElement>("[data-owner-migration-page]");
  const style = source.querySelector("style");
  if (!page || !style) throw new Error("冻结页面不含迁移工作台");
  // dd8d60d supplies the page, field order, copy and hooks. The Host only
  // mounts it, removes unrendered Jinja tokens, and connects stable V3 ports.
  const cloned = page.cloneNode(true) as HTMLElement;
  scrubFrozenServerPlaceholders(cloned);
  adaptFrozenDonorCopy(cloned);
  stage.replaceChildren(style.cloneNode(true), cloned);
  const mounted = stage.querySelector<HTMLElement>("[data-owner-migration-page]");
  if (!mounted) throw new Error("冻结迁移页面未挂载");
  return mounted;
}

function query<T extends Element>(root: ParentNode, selector: string): T {
  const node = root.querySelector<T>(selector);
  if (!node) throw new Error(`冻结页面缺少 ${selector}`);
  return node;
}

class OwnerStaffDirectory {
  private readonly byID = new Map<string, Staff>();

  constructor(initial: Staff[]) { initial.forEach((member) => this.upsert(member)); }

  upsert(member: Staff): void {
    const id = Number(member.ID);
    const userID = text(member.UserID);
    if (!Number.isSafeInteger(id) || id < 1 || !userID) return;
    this.byID.set(String(id), { ID: id, UserID: userID, DisplayName: text(member.DisplayName) || userID, Active: member.Active !== false });
  }

  get(rawID: unknown): Staff | undefined { return this.byID.get(text(rawID)); }
}

function ownerPickerFailure(response: Response, body: unknown): RequestFailure {
  const detail = body && typeof body === "object" && "error" in body ? text((body as { error?: unknown }).error) : "";
  return requestFailure(ownerHandoffRequestMessage(response.status, detail), response.status);
}

function ownerStaffRecord(member: Staff, kind: "source" | "target"): StaffPickerRecord {
  return {
    source: "owner_migration.operation_members", staff_id: String(member.ID), user_id: member.UserID,
    display_name: member.DisplayName || member.UserID, active: member.Active,
    unavailable_reason: kind === "target" && !member.Active ? "目标负责人必须是在职员工。" : undefined,
  };
}

function unresolvedOwnerStaffRecord(rawID: unknown): StaffPickerRecord | undefined {
  const id = text(rawID);
  return /^[1-9]\d*$/.test(id) ? {
    source: "owner_migration.operation_members", staff_id: id, user_id: "", display_name: `员工 #${id}`,
    unavailable_reason: "当前员工目录最多返回前 100 项或搜索结果；原选择仍保留，不能据此判定失效。",
  } : undefined;
}

function installPicker(root: HTMLElement, directory: OwnerStaffDirectory): void {
  const choose = (kind: "source" | "target") => {
    const picker = window.AICRMStaffPicker;
    if (!picker || typeof picker.open !== "function") {
      const notice = root.querySelector<HTMLElement>("[data-workbench-notice]");
      if (notice) notice.textContent = "员工选择器无法打开；当前负责人草稿未修改，请刷新后重试。";
      return;
    }
    const currentID = query<HTMLInputElement>(root, `[data-owner-userid="${kind}"]`).value;
    const current = directory.get(currentID);
    const initial = current ? ownerStaffRecord(current, kind) : unresolvedOwnerStaffRecord(currentID);
    const loadPage = async ({ query: search, signal }: { query: string; signal: AbortSignal }) => {
      const url = new URL("/api/admin/common/operation-members", window.location.origin);
      url.searchParams.set("scope", "owner_migration");
      url.searchParams.set("include_inactive", kind === "source" ? "true" : "false");
      url.searchParams.set("page_size", "100");
      if (text(search)) url.searchParams.set("q", text(search));
      const response = await fetch(url.toString(), { credentials: "same-origin", headers: { Accept: "application/json" }, signal });
      const payload = await response.json().catch(() => ({}));
      if (signal.aborted) throw new DOMException("负责人目录读取已替换", "AbortError");
      if (!response.ok) throw ownerPickerFailure(response, payload);
      const rawItems = payload && typeof payload === "object" && Array.isArray((payload as { items?: unknown[] }).items) ? (payload as { items: unknown[] }).items : null;
      if (!rawItems) throw new Error("员工目录响应不完整，请重试。");
      const items = rawItems.flatMap((raw): StaffPickerRecord[] => {
        if (signal.aborted) return [];
        const value = raw && typeof raw === "object" ? raw as Record<string, unknown> : {};
        const id = Number(value.staff_id); const userID = text(value.user_id);
        if (!Number.isSafeInteger(id) || id < 1 || !userID) return [];
        const member: Staff = { ID: id, UserID: userID, DisplayName: text(value.display_name) || userID, Active: value.active !== false };
        return [ownerStaffRecord(member, kind)];
      });
      return { items };
    };
    picker.open({
      title: kind === "source" ? "选择原负责人" : "选择目标负责人", source: "owner_migration.operation_members", scope: "owner_migration", mode: "single", limit: 1,
      selectedRecords: initial ? [initial] : [],
      directoryHint: "本页只显示前 100 项或搜索结果；未出现的原选择仍保留，不能据此判定失效。",
      loadPage,
      // This endpoint exposes an authorised local read only. Refresh merely
      // re-reads it; it never starts a Provider sync or mutation.
      refresh: async () => undefined,
      accessLossMessage: (error) => {
        const status = Number((error as RequestFailure | undefined)?.httpStatus);
        return status === 401 || status === 403 ? "负责人迁移员工目录权限已失效；当前选择仍可查看或取消。" : undefined;
      },
      onCommit: ({ selected }) => {
        const picked = selected[0];
        const member = picked && Number.isSafeInteger(Number(picked.staff_id)) && Number(picked.staff_id) > 0 && text(picked.user_id)
          ? { ID: Number(picked.staff_id), UserID: text(picked.user_id), DisplayName: text(picked.display_name) || text(picked.user_id), Active: picked.active !== false }
          : undefined;
        if (!member || (kind === "target" && !member.Active)) throw new Error("所选员工不再可用于负责人迁移，请重新读取目录。");
        // Only the selected record from the current, validated session enters
        // the live map used by preview.  A superseded directory read can never
        // overwrite this staff_id → UserID mapping after a newer selection.
        directory.upsert(member);
        query<HTMLInputElement>(root, `[data-owner-userid="${kind}"]`).value = String(member.ID);
        query<HTMLInputElement>(root, `[data-owner-label="${kind}"]`).value = member.DisplayName || member.UserID;
        root.dispatchEvent(new Event("owner-handoff-change"));
      },
    });
  };
  root.querySelectorAll<HTMLButtonElement>("[data-owner-picker]").forEach(button => button.addEventListener("click", () => choose(button.dataset.ownerPicker as "source" | "target")));
}

function currentMode(root: ParentNode): string { return query<HTMLInputElement>(root, "[data-include-wecom-transfer]").checked ? "wecom_then_crm" : "local_only"; }
function ownerID(root: ParentNode, kind: "source" | "target"): number { return Number(query<HTMLInputElement>(root, `[data-owner-userid="${kind}"]`).value); }
function ownerUserID(root: ParentNode, kind: "source" | "target", directory: OwnerStaffDirectory): string {
  return text(directory.get(ownerID(root, kind))?.UserID);
}

function selectedScope(root: ParentNode): string { return query<HTMLInputElement>(root, 'input[name="scope_type"]:checked').value; }
function transferStatusLabel(status: number): string {
  return ({ 0: "本地迁移", 1: "企微转接已完成", 2: "企微转接处理中", 3: "用户拒绝接替", 4: "目标成员用户上限", 5: "未找到企微转接记录" } as Record<number, string>)[status] || "企微转接状态待确认";
}

function ownerMigrationStateLabel(state: string): string {
  return ({
    ready: "可迁移", skipped_by_file: "已按文件跳过", not_under_source_owner: "负责人不一致",
    not_found: "未找到用户", conflict: "迁移冲突", unresolved: "待核实",
    missing_external_userid: "缺少用户标识", invalid_move_flag: "迁移标记无效", duplicate: "文件重复",
    accepted: "已受理", queued: "排队中", attempted: "正在执行", executed: "已执行",
    provider_accepted: "企微已受理", final_failed: "执行失败", outcome_unknown: "结果待核实",
    retryable_failed: "可重试失败", cancelled: "已取消", reconciled: "已核对", cas_conflict: "状态冲突",
    observed: "已读取结果",
  } as Record<string, string>)[state] || "迁移状态待确认";
}

function ownerMigrationReason(reason: string): string {
  return ({
    "external_userid is required": "缺少用户标识。",
    "duplicate external_userid; first row is kept": "文件中存在重复用户标识，已保留首次出现的记录。",
    "Excel marked skip": "已按文件标记跳过。",
    "no executable rows": "没有可执行迁移行。",
    "是否迁移字段非法": "迁移标记无效。",
    "未得到该行的安全预览结果": "未得到该行的安全预览结果。",
    "已按文件标记跳过。": "已按文件标记跳过。",
    "没有可执行迁移行": "没有可执行迁移行。",
    "当前负责人标识与选择的原负责人不一致，预览阶段将不可执行": "当前负责人标识与选择的原负责人不一致，预览阶段将不可执行。",
    "当前负责人标识与选择的原负责人不一致": "当前负责人标识与选择的原负责人不一致。",
    "当前负责人userid与选择的原负责人不一致": "当前负责人标识与选择的原负责人不一致。",
  } as Record<string, string>)[reason] || "迁移原因待确认。";
}

function ownerMigrationModeLabel(mode: string): string {
  return ({ local_only: "仅本地迁移", wecom_then_crm: "先企微转接后本地迁移" } as Record<string, string>)[mode] || "迁移方式待确认";
}
function downloadBlob(filename: string, blob: Blob): void {
  const url = URL.createObjectURL(blob);
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.hidden = true;
  document.body.append(link);
  link.click();
  // Chromium resolves blob: downloads asynchronously. Keep the URL alive until
  // that hand-off completes instead of revoking it in the click stack.
  window.setTimeout(() => { link.remove(); URL.revokeObjectURL(url); }, 1000);
}

function downloadWorkbook(filename: string, headers: string[], rows: string[][]): void {
  downloadBlob(filename, new Blob([ownerMigrationWorkbookXLSX(headers, rows)], { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }));
}

function normalizeMoveFlag(value: string): [string, boolean] {
  const normalized = text(value).toLowerCase();
  if (new Set(["", "是", "y", "yes", "true", "1", "迁移"]).has(normalized)) return ["是", true];
  if (new Set(["否", "n", "no", "false", "0", "不迁移"]).has(normalized)) return ["否", true];
  return [text(value), false];
}

function normalizeImportedRows(rawRows: string[][], sourceUserID: string): ImportedRow[] {
  const seen = new Set<string>();
  return rawRows.map((row, index) => {
    const external = text(row[0]);
    const [moveFlag, validFlag] = normalizeMoveFlag(text(row[1]));
    const current = text(row[2]);
    let parseStatus = "parsed";
    let parseReason = "";
    if (!external) { parseStatus = "missing_external_userid"; parseReason = "external_userid is required"; }
    else if (!validFlag) { parseStatus = "invalid_move_flag"; parseReason = "是否迁移字段非法"; }
    else if (seen.has(external)) { parseStatus = "duplicate"; parseReason = "duplicate external_userid; first row is kept"; }
    else {
      seen.add(external);
      if (current && current !== sourceUserID) parseReason = "当前负责人标识与选择的原负责人不一致，预览阶段将不可执行";
    }
    return { Line: index + 2, ExternalUserID: external, MoveFlag: moveFlag, CurrentOwnerUserID: current, CustomerDisplayName: text(row[3]), Remark: text(row[4]), ParseStatus: parseStatus, ParseReason: parseReason };
  });
}

function importStats(rows: ImportedRow[]): Record<string, number> {
  const unique = new Set(rows.filter(row => row.ExternalUserID && row.ParseStatus !== "duplicate").map(row => row.ExternalUserID));
  return {
    total_rows: rows.length,
    unique_external_userids: unique.size,
    marked_move: rows.filter(row => row.ParseStatus === "parsed" && row.MoveFlag === "是").length,
    marked_skip: rows.filter(row => row.ParseStatus === "parsed" && row.MoveFlag === "否").length,
    duplicate_rows: rows.filter(row => row.ParseStatus === "duplicate").length,
    invalid_rows: rows.filter(row => row.ParseStatus === "missing_external_userid" || row.ParseStatus === "invalid_move_flag").length,
  };
}

function displayFromServer(row: Row, item: ImportedRow): DisplayRow {
  const mappedState = row.State === "conflict" ? "not_under_source_owner" : row.State === "unresolved" ? "not_found" : row.State;
  return { Line: item.Line, ExternalUserID: row.ExternalUserID || item.ExternalUserID, CustomerDisplayName: row.CustomerDisplayName || item.CustomerDisplayName, MoveFlag: item.MoveFlag, CurrentOwnerUserID: row.CurrentOwnerUserID || item.CurrentOwnerUserID, Remark: item.Remark, State: mappedState, Reason: row.Reason || "已通过预览校验", CustomerID: row.CustomerID };
}

function previewDisplayRows(preview: Preview, scope: string, imported: ImportedRow[], sourceUserID: string): DisplayRow[] {
  if (scope !== "excel_include") return preview.Rows.map(row => ({ Line: row.Line, ExternalUserID: row.ExternalUserID, CustomerDisplayName: row.CustomerDisplayName, MoveFlag: "是", CurrentOwnerUserID: row.CurrentOwnerUserID, Remark: "", State: row.State, Reason: row.Reason || "已通过预览校验", CustomerID: row.CustomerID }));
  const serverRows = new Map(preview.Rows.map(row => [row.ExternalUserID, row]));
  return imported.map(item => {
    if (item.ParseStatus !== "parsed") return { ...item, State: item.ParseStatus, Reason: item.ParseReason };
    if (item.MoveFlag === "否") return { ...item, State: "skipped_by_file", Reason: "已按文件标记跳过。" };
    if (item.CurrentOwnerUserID && item.CurrentOwnerUserID !== sourceUserID) return { ...item, State: "not_under_source_owner", Reason: "当前负责人标识与选择的原负责人不一致" };
    const server = serverRows.get(item.ExternalUserID);
    if (!server) return { ...item, State: "not_found", Reason: "未得到该行的安全预览结果" };
    return displayFromServer(server, item);
  });
}

function renderRows(root: HTMLElement, rows: DisplayRow[], scope: string, source: number, target: number): void {
  const ready = rows.filter(row => row.State === "ready").length;
  const skipped = rows.filter(row => row.State === "skipped_by_file").length;
  const blocked = rows.length - ready - skipped;
  query<HTMLElement>(root, "[data-preview-basic]").textContent = `${scope === "excel_include" ? "Excel 指定名单" : "全部用户"} · 原负责人 #${source} → 目标负责人 #${target} · ${ready} 个可迁移用户；${blocked} 个不可迁移。`;
  const values: Record<string, number> = { total_rows: rows.length, unique_external_userids: new Set(rows.map(row => row.ExternalUserID).filter(Boolean)).size, ready, skipped_by_file: skipped, blocked, crm_updates: ready };
  Object.entries(values).forEach(([name, value]) => { const node = root.querySelector<HTMLElement>(`[data-preview-stat="${name}"]`); if (node) node.textContent = String(value); });
  query<HTMLElement>(root, "[data-preview-rows]").innerHTML = rows.map(row => `<tr><td>${row.Line}</td><td><code>${esc(row.ExternalUserID)}</code></td><td>${esc(row.CustomerDisplayName)}</td><td>${esc(row.MoveFlag)}</td><td>${esc(row.CurrentOwnerUserID)}</td><td><span class="owner-migration-status owner-migration-status--${row.State === "ready" ? "ready" : row.State === "skipped_by_file" ? "skip" : "block"}">${esc(ownerMigrationStateLabel(row.State))}</span></td><td>${esc(ownerMigrationReason(row.Reason))}</td></tr>`).join("") || '<tr><td colspan="7" class="owner-migration-empty">当前范围没有候选用户。</td></tr>';
  query<HTMLButtonElement>(root, "[data-download-errors]").disabled = blocked === 0;
  query<HTMLButtonElement>(root, "[data-execute]").disabled = ready === 0;
}

function renderPreview(root: HTMLElement, preview: Preview, scope: string, imported: ImportedRow[], sourceUserID: string): DisplayRow[] {
  query<HTMLElement>(root, "[data-preview-empty]").hidden = true;
  query<HTMLElement>(root, "[data-preview-content]").hidden = false;
  const rows = previewDisplayRows(preview, scope, imported, sourceUserID);
  renderRows(root, rows, scope, ownerID(root, "source"), ownerID(root, "target"));
  query<HTMLElement>(root, "[data-confirm-phrase-display]").textContent = preview.ConfirmationPhrase;
  return rows;
}

function renderBatch(root: HTMLElement, batch: Batch): void {
  // Keep the opaque batch reference for the Host's readback actions outside
  // the operator-facing log, whose states are localized below.
  root.dataset.ownerHandoffBatchId = batch.ID;
  query<HTMLElement>(root, "[data-execution-log]").textContent = [
    `迁移批次：${batch.ID}`,
    `迁移方式：${ownerMigrationModeLabel(batch.Mode)}`,
    `批次状态：${ownerMigrationStateLabel(batch.State)}`,
    ...(batch.Lines || []).map(line => `第 ${line.Line} 行，用户 #${line.CustomerID}：${ownerMigrationStateLabel(line.State)}；企微转接：${transferStatusLabel(line.TransferStatus)}`),
  ].join("\n");
}

async function boot(): Promise<void> {
  const stage = document.querySelector<HTMLElement>("[data-owner-handoff-host]");
  if (!stage) return;
  stage.dataset.ownerHandoffInit = "mounting";
  try {
    const root = await mountFrozenDonor(stage);
    stage.dataset.ownerHandoffInit = "donor_loaded";
    const context = await api<Context>("/api/admin/customers/owner-handoffs/context");
    stage.dataset.ownerHandoffInit = "context_loaded";
    const staffDirectory = new OwnerStaffDirectory(context.staff || []);
    installPicker(root, staffDirectory);
    query<HTMLInputElement>(root, '[data-owner-label="source"]').value = "";
    query<HTMLInputElement>(root, '[data-owner-label="target"]').value = "";
    query<HTMLInputElement>(root, '[data-owner-userid="source"]').value = "";
    query<HTMLInputElement>(root, '[data-owner-userid="target"]').value = "";
    query<HTMLInputElement>(root, "#operator").value = context.operator || "当前登录管理员";
    query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").value = "您好，后续将由新的服务同事继续为您服务。";
    query<HTMLInputElement>(root, "[data-include-wecom-transfer]").checked = true;
    query<HTMLElement>(root, "[data-wecom-pill]").textContent = "企微转接：默认开启";
    query<HTMLElement>(root, "[data-local-only-warning]").hidden = true;
    query<HTMLInputElement>(root, "[data-import-file]").setAttribute("accept", ".xlsx,.xls,.csv");
    const updateWelcomeCount = () => { query<HTMLElement>(root, "[data-welcome-count]").textContent = `${text(query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").value).length} 字`; };
    updateWelcomeCount();
    let preview: Preview | undefined;
    let batch: Batch | undefined;
    let fileExternalIDs: string[] = [];
    let importedRows: ImportedRow[] = [];
    let displayedRows: DisplayRow[] = [];
    const notice = query<HTMLElement>(root, "[data-workbench-notice]");
    const setNotice = (value: string, kind = "") => { notice.textContent = value; notice.className = `owner-migration-hint ${kind}`; };
    const reset = () => {
      preview = undefined; batch = undefined; displayedRows = [];
      delete root.dataset.ownerHandoffBatchId;
      query<HTMLElement>(root, "[data-preview-empty]").hidden = false;
      query<HTMLElement>(root, "[data-preview-content]").hidden = true;
      query<HTMLInputElement>(root, "[data-confirm-phrase-input]").value = "";
      query<HTMLButtonElement>(root, "[data-execute]").disabled = true;
      query<HTMLButtonElement>(root, "[data-download-errors]").disabled = true;
      query<HTMLButtonElement>(root, "[data-download-result]").disabled = true;
      const transferReader = root.querySelector<HTMLButtonElement>("[data-read-transfer-result]");
      if (transferReader) transferReader.disabled = true;
      query<HTMLElement>(root, "[data-execution-log]").textContent = "尚未执行。";
    };
    const updateWeComPresentation = () => {
      const enabled = query<HTMLInputElement>(root, "[data-include-wecom-transfer]").checked;
      const pill = query<HTMLElement>(root, "[data-wecom-pill]");
      pill.textContent = enabled ? "企微转接：默认开启" : "企微转接：已关闭";
      pill.classList.toggle("owner-migration-pill--success", enabled);
      pill.classList.toggle("owner-migration-pill--warn", !enabled);
      query<HTMLElement>(root, "[data-local-only-warning]").hidden = enabled;
    };
    root.addEventListener("owner-handoff-change", reset);
    root.querySelectorAll<HTMLElement>("[data-scope-segment]").forEach(segment => segment.addEventListener("click", () => {
      const scope = segment.dataset.scopeSegment || "all";
      root.querySelectorAll<HTMLElement>("[data-scope-segment]").forEach(value => value.classList.toggle("is-active", value === segment));
      query<HTMLElement>(root, "[data-mode-pill]").textContent = scope === "excel_include" ? "模式：Excel 指定名单" : "模式：全量迁移";
      query<HTMLInputElement>(root, `input[name="scope_type"][value="${scope}"]`).checked = true;
      query<HTMLElement>(root, "[data-excel-panel]").hidden = scope !== "excel_include";
      reset();
    }));
    query<HTMLButtonElement>(root, "[data-upload-file]").addEventListener("click", async () => {
      try {
        const source = ownerID(root, "source"); const target = ownerID(root, "target");
        const sourceUserID = ownerUserID(root, "source", staffDirectory);
        if (!source || !target || source === target || !sourceUserID) throw new Error("请先选择不同的原负责人和目标负责人");
        const file = query<HTMLInputElement>(root, "[data-import-file]").files?.[0];
        if (!file) throw new Error("请选择包含旧模板五列的 XLSX、XLS 或 CSV 文件");
        const rawRows = await ownerMigrationRowsFromFile(file);
        const headers = rawRows.shift() || [];
        const expectedHeaders = ["external_userid", "是否迁移", "当前负责人userid", "客户备注名", "备注"];
        if (headers.length !== expectedHeaders.length || headers.some((header, index) => text(header) !== expectedHeaders[index])) throw new Error(`第一行必须且只能是：${expectedHeaders.join("、")}`);
        if (rawRows.some(row => row.length !== expectedHeaders.length)) throw new Error("每一行必须包含旧模板的五列");
        importedRows = normalizeImportedRows(rawRows, sourceUserID);
        fileExternalIDs = importedRows.filter(row => row.ParseStatus === "parsed" && row.MoveFlag === "是" && row.ExternalUserID && (!row.CurrentOwnerUserID || row.CurrentOwnerUserID === sourceUserID)).map(row => row.ExternalUserID);
        const stats = importStats(importedRows);
        query<HTMLElement>(root, "[data-import-summary]").hidden = false;
        query<HTMLElement>(root, "[data-import-filename]").textContent = file.name;
        Object.entries(stats).forEach(([name, value]) => { const node = root.querySelector<HTMLElement>(`[data-import-stat="${name}"]`); if (node) node.textContent = String(value); });
        reset();
        setNotice("旧模板名单已解析；预览会保留每一行的标记、重复和负责人校验结果。", "ok");
      } catch (error) { setNotice(ownerHandoffErrorMessage(error, "文件解析失败，请检查文件后重试。"), "error"); }
    });
    root.querySelectorAll<HTMLInputElement>('input[name="scope_type"]').forEach(input => input.addEventListener("change", reset));
    query<HTMLInputElement>(root, "[data-include-wecom-transfer]").addEventListener("change", () => { updateWeComPresentation(); reset(); });
    query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").addEventListener("input", () => { updateWelcomeCount(); reset(); });
    query<HTMLButtonElement>(root, "[data-download-template]").addEventListener("click", () => {
      downloadBlob("owner_migration_template.xlsx", new Blob([ownerMigrationTemplateXLSX()], { type: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet" }));
    });
    query<HTMLButtonElement>(root, "[data-preview]").addEventListener("click", async () => {
      try {
        const source = ownerID(root, "source"); const target = ownerID(root, "target");
        const sourceUserID = ownerUserID(root, "source", staffDirectory);
        if (!source || !target || source === target || !sourceUserID) throw new Error("请先选择不同的原负责人和目标负责人");
        const scope = selectedScope(root);
        if (scope === "excel_include" && !importedRows.length) throw new Error("请先上传旧模板名单");
        if (scope === "excel_include" && !fileExternalIDs.length) {
          displayedRows = importedRows.map(row => row.ParseStatus === "parsed" && row.MoveFlag === "否" ? { ...row, State: "skipped_by_file", Reason: "已按文件标记跳过。" } : { ...row, State: row.ParseStatus, Reason: row.ParseReason || "没有可执行迁移行" });
          query<HTMLElement>(root, "[data-preview-empty]").hidden = true;
          query<HTMLElement>(root, "[data-preview-content]").hidden = false;
          renderRows(root, displayedRows, scope, source, target);
          setNotice("文件没有可执行迁移行，已保留逐行校验结果，不能确认执行。", "ok");
          return;
        }
        preview = await api<Preview>("/api/admin/customers/owner-handoffs/previews", { method: "POST", body: JSON.stringify({ mode: currentMode(root), scope, source_staff_id: source, target_staff_id: target, customer_ids: [], external_userids: scope === "excel_include" ? fileExternalIDs : [], welcome_message: query<HTMLTextAreaElement>(root, "[data-transfer-welcome-msg]").value, confirmation_phrase: `确认将当前候选用户迁移到 ${target}`, idempotency_key: key() }) });
        displayedRows = renderPreview(root, preview, scope, importedRows, sourceUserID);
        setNotice("预览已生成，请逐字输入确认短语。", "ok");
      } catch (error) { setNotice(ownerHandoffErrorMessage(error, "预览失败，请检查填写内容后重试。"), "error"); }
    });
    query<HTMLButtonElement>(root, "[data-execute]").addEventListener("click", async () => {
      try {
        if (!preview) throw new Error("请先生成预览");
        const phrase = query<HTMLInputElement>(root, "[data-confirm-phrase-input]").value;
        if (phrase !== preview.ConfirmationPhrase) throw new Error("确认短语不匹配");
        batch = await api<Batch>("/api/admin/customers/owner-handoffs/confirm", { method: "POST", body: JSON.stringify({ preview_id: preview.ID, preview_hash: preview.Hash, confirmation_phrase: phrase, idempotency_key: key() }) });
        renderBatch(root, batch);
        query<HTMLButtonElement>(root, "[data-download-result]").disabled = false;
        readTransfer.disabled = false;
        setNotice("迁移已受理；结果导出和企微结果读取会显示每一行实际状态。", "ok");
      } catch (error) { setNotice(ownerHandoffErrorMessage(error, "执行失败，请重新生成预览后重试。"), "error"); }
    });
    query<HTMLButtonElement>(root, "[data-reset-workbench]").addEventListener("click", reset);
    query<HTMLButtonElement>(root, "[data-download-errors]").addEventListener("click", () => {
      const blocked = displayedRows.filter(row => row.State !== "ready" && row.State !== "skipped_by_file");
      if (!blocked.length) return;
      downloadWorkbook("owner_migration_blocked_rows.xlsx", ["行号", "external_userid", "客户备注名", "Excel 标记", "当前负责人userid", "备注", "状态", "原因"], blocked.map(row => [String(row.Line), row.ExternalUserID, row.CustomerDisplayName, row.MoveFlag, row.CurrentOwnerUserID, row.Remark, ownerMigrationStateLabel(row.State), ownerMigrationReason(row.Reason)]));
    });
    query<HTMLButtonElement>(root, "[data-download-result]").addEventListener("click", async () => {
      if (!batch) { setNotice("请先执行迁移，再导出结果明细。", "error"); return; }
      try {
        batch = await api<Batch>(`/api/admin/customers/owner-handoffs/batches/${encodeURIComponent(batch.ID)}`);
        renderBatch(root, batch);
        const rowsByCustomer = new Map(displayedRows.filter(row => row.CustomerID).map(row => [row.CustomerID as number, row]));
        downloadWorkbook("owner_migration_result.xlsx", ["行号", "external_userid", "客户备注名", "当前负责人userid", "备注", "迁移状态", "企微转接状态"], (batch.Lines || []).map(line => {
          const row = rowsByCustomer.get(line.CustomerID);
          return [String(row?.Line || line.Line), row?.ExternalUserID || "", row?.CustomerDisplayName || "", row?.CurrentOwnerUserID || "", row?.Remark || "", ownerMigrationStateLabel(line.State), transferStatusLabel(line.TransferStatus)];
        }));
        setNotice("已导出当前批次结果明细。", "ok");
      } catch (error) { setNotice(ownerHandoffErrorMessage(error, "结果暂不可读取，请稍后重试。"), "error"); }
    });
    query<HTMLButtonElement>(root, "[data-download-result]").textContent = "下载结果明细";
    const readTransfer = document.createElement("button");
    readTransfer.type = "button"; readTransfer.className = "owner-migration-btn"; readTransfer.dataset.readTransferResult = ""; readTransfer.textContent = "读取企微转接结果"; readTransfer.disabled = true;
    query<HTMLElement>(root, "[data-download-result]").parentElement?.append(readTransfer);
    readTransfer.addEventListener("click", async () => {
      if (!batch) return;
      stage.dataset.ownerHandoffTransferResultStatus = "pending";
      try {
        batch = await api<Batch>(`/api/admin/customers/owner-handoffs/batches/${encodeURIComponent(batch.ID)}/transfer-result`, { method: "POST", body: JSON.stringify({ idempotency_key: key() }) });
        stage.dataset.ownerHandoffTransferResultStatus = "ok";
        renderBatch(root, batch); setNotice("已读取企微转接结果。", "ok");
      } catch (error) {
        const status = error && typeof error === "object" && "httpStatus" in error && typeof error.httpStatus === "number" ? error.httpStatus : 0;
        stage.dataset.ownerHandoffTransferResultStatus = status > 0 ? `http_${status}` : "error";
        setNotice(ownerHandoffErrorMessage(error, "企微转接结果暂不可读取，请稍后重试。"), "error");
      }
    });
    query<HTMLInputElement>(root, 'input[name="scope_type"][value="all"]').checked = true;
    root.querySelector<HTMLElement>('[data-scope-segment="all"]')?.classList.add("is-active");
    root.querySelector<HTMLElement>('[data-scope-segment="excel_include"]')?.classList.remove("is-active");
    query<HTMLElement>(root, "[data-excel-panel]").hidden = true;
    query<HTMLElement>(root, "[data-mode-pill]").textContent = "模式：全量迁移";
    updateWeComPresentation(); reset();
    setNotice("原负责人可含停用员工，目标负责人只列在职员工。");
    stage.dataset.ownerHandoffInit = "ready";
  } catch (error) {
    const phase = stage.dataset.ownerHandoffInit || "mounting";
    const failure = error as RequestFailure;
    stage.dataset.ownerHandoffInit = phase === "mounting" ? "donor_error" : phase === "donor_loaded" ? "context_error" : "host_error";
    if (failure.httpStatus) stage.dataset.ownerHandoffInitStatus = String(failure.httpStatus);
    else delete stage.dataset.ownerHandoffInitStatus;
    stage.textContent = "负责人迁移页面不可用。";
  }
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", () => { void boot(); }); else void boot();
