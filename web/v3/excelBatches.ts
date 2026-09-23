import { runAction } from "./actionFeedback";
import {
  openContentComposer,
  openReadonlyContentPresentation,
  type ContentTextRule,
} from "./shared/ui/contentComposer";
import type { ContentPresentationSupplement } from "./shared/ui/contentPresentation";

// Browser host only: it never resolves identity, enqueues work, or calls WeCom.
// It submits the versioned, CSRF-protected commands defined in the batch API.
import { formatShanghaiDateTime } from "./adminDateTime";

type Obj = Record<string, any>;
const base = "/api/admin/operation-batches";
const states: Record<string, string> = {
  pending_review: "待审核",
  partially_approved: "待审核",
  approved: "已审核",
  dispatching: "任务创建中",
  needs_attention: "需要处理",
  completed: "已完成",
  completed_with_failures: "已完成，存在明确失败",
  rejected: "已拒绝",
};
const delivery: Record<string, string> = {
  pending_submission: "待提交",
  task_created_waiting_employee: "任务已创建，待员工执行",
  delivery_proven: "发送成功",
  final_failed: "明确失败",
  outcome_unknown: "结果待核实",
};
const review: Record<string, string> = {
  pending_review: "待审核",
  approved: "已批准",
  rejected: "已拒绝",
  ineligible: "不适用",
};

function displayDateTime(value: unknown): string {
  if (value === null || value === undefined || value === "") return "—";
  const formatted = formatShanghaiDateTime(value);
  return formatted === "未提供" ? "时间暂时无法显示" : formatted;
}

function deliveryLabel(value: unknown, empty = "待提交"): string {
  const state = typeof value === "string" ? value : "";
  if (!state) return empty;
  return delivery[state] || "状态待核对";
}

function reviewLabel(value: unknown): string {
  const state = typeof value === "string" ? value : "";
  if (!state) return "审核状态待核对";
  return review[state] || "审核状态待核对";
}

const deliveryFailure: Record<string, string> = {
  title_missing: "发送内容缺少标题",
  cover_missing: "发送内容缺少统一封面",
  unionid_not_unique: "接收对象身份待核对",
  unionid_unverified: "接收对象身份待核对",
  wecom_identity_unavailable: "接收对象企微身份暂不可用",
  target_unavailable: "接收对象暂不可用",
  payload_unavailable: "发送内容暂不可用",
  outcome_unknown: "发送结果待核对",
  provider_rejected: "企微拒绝发送请求",
  wecom_errcode_45009: "企微接口调用频率受限，请稍后重试",
  wecom_errcode_45011: "企微接口调用频率受限，请稍后重试",
  "wecom_errcode_-1": "企微服务暂时繁忙，请稍后核对",
};

function deliveryReason(value: unknown): string {
  const reason = typeof value === "string" ? value.trim() : "";
  if (!reason) return "—";
  if (deliveryFailure[reason]) return deliveryFailure[reason];
  if (/^wecom_errcode_-?\d+$/.test(reason)) return "企微发送未成功";
  if (/^wecom_status_[2-4]$/.test(reason)) return "企微发送未成功";
  return "失败原因待核对";
}
function el<K extends keyof HTMLElementTagNameMap>(
  tag: K,
  text?: string,
): HTMLElementTagNameMap[K] {
  const node = document.createElement(tag);
  if (text !== undefined) node.textContent = text;
  return node;
}
function key(prefix: string): string {
  return `${prefix}-${crypto.randomUUID()}`;
}
function csrf(): string {
  for (const part of document.cookie.split(";")) {
    const [name, ...value] = part.trim().split("=");
    if (name === "aicrm_csrf" || name === "aicrm_admin_csrf")
      return decodeURIComponent(value.join("="));
  }
  return "";
}
function errorText(status: number, body: Obj): string {
  if (status === 403) return "没有此操作权限";
  if (status === 409 && body.error === "version_conflict")
    return "内容已变化，请刷新后重新操作";
  if (status === 409 && body.error === "batch_submitted")
    return "该批次已提交，内容不能再修改";
  if (status === 409 && body.error === "duplicate_file")
    return "该文件已有批次；请切换到已有批次，或明确创建新的发送批次";
  if (status === 400 && body.error === "cover_required")
    return "请先上传本批次的统一封面";
  return (
    body.message ||
    body.error ||
    (status === 503 ? "批次服务暂时不可用" : "请求失败，请检查输入后重试")
  );
}
async function api(
  path: string,
  method = "GET",
  body?: Obj | Blob,
  idempotencyKey = key("excel-batch"),
  signal?: AbortSignal,
): Promise<Obj> {
  const headers: Record<string, string> = { Accept: "application/json" };
  let encoded: BodyInit | undefined;
  if (method !== "GET") {
    headers["X-CSRF-Token"] = csrf();
    headers["Idempotency-Key"] = idempotencyKey;
  }
  if (body instanceof Blob) {
    encoded = body;
    headers["Content-Type"] =
      body.type ||
      (body instanceof File && body.name.toLowerCase().endsWith(".xlsx")
        ? "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
        : "application/octet-stream");
  } else if (body !== undefined) {
    headers["Content-Type"] = "application/json";
    encoded = JSON.stringify(body);
  }
  const response = await fetch(path, {
    method,
    headers,
    body: encoded,
    credentials: "same-origin",
    signal,
  });
  const result = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(errorText(response.status, result));
  return result;
}
function pagedPath(path: string, cursor = ""): string {
  const query = new URLSearchParams({ limit: "50" });
  if (cursor) query.set("cursor", cursor);
  return `${path}${path.includes("?") ? "&" : "?"}${query}`;
}
function aborted(error: unknown): boolean {
  return (error as { name?: string } | null)?.name === "AbortError";
}
type CursorPage = {
  items: Obj[];
  cursors: string[];
  index: number;
  nextCursor: string;
  retryCursor: string;
  retryFromMetadata: boolean;
  loading: boolean;
  error: string;
  requestID: number;
  controller: AbortController | null;
};
function cursorPage(): CursorPage {
  return {
    items: [],
    cursors: [""],
    index: 0,
    nextCursor: "",
    retryCursor: "",
    retryFromMetadata: false,
    loading: false,
    error: "",
    requestID: 0,
    controller: null,
  };
}
type ReportRead = {
  value: Obj | null;
  loading: boolean;
  error: string;
  requestID: number;
  controller: AbortController | null;
};
function reportRead(): ReportRead {
  return { value: null, loading: false, error: "", requestID: 0, controller: null };
}
type ActionKind = "primary" | "secondary" | "ghost" | "danger";

function notice(
  text = "",
  kind: "success" | "error" = "success",
): HTMLParagraphElement {
  const node = el("p");
  node.dataset.excelFeedback = "";
  setNotice(node, text, kind);
  return node;
}
function setNotice(
  node: HTMLElement,
  text: string,
  kind: "success" | "error" = "success",
): void {
  node.textContent = text;
  node.className = text ? `admin-alert admin-alert--${kind} xeb-status` : "xeb-status";
  node.hidden = !text;
  node.setAttribute("role", kind === "error" ? "alert" : "status");
}
function field(label: string, control: HTMLElement): HTMLLabelElement {
  const node = el("label");
  node.className = "admin-field xeb-field";
  node.append(el("span", label), control);
  return node;
}
function action(
  text: string,
  run: () => Promise<void> | void,
  kind: ActionKind = "secondary",
): HTMLButtonElement {
  const node = el("button", text);
  node.type = "button";
  node.className = `admin-button admin-button--${kind}`;
  const owned = node as HTMLButtonElement & { __dcBound?: boolean };
  // Shared feedback listens during capture. Mark this V3-owned handler before
  // the button is inserted so its real local/API action is never misclassified.
  owned.__dcBound = true;
  node.dataset.capabilityState = "real";
  node.removeAttribute("aria-description");
  node.onclick = () => {
    void runAction(node, async () => {
      try {
        await run();
      } catch (error) {
        const dialog = node.closest("dialog");
        const scope = dialog || node.closest(".operation-excel-workspace");
        let status = scope?.querySelector<HTMLElement>("[data-excel-feedback]");
        if (!status && dialog) {
          status = notice();
          const actions = node.closest(".xeb-actions");
          if (actions?.parentElement === dialog) dialog.insertBefore(status, actions);
          else dialog.append(status);
        }
        if (status) setNotice(status, (error as Error).message, "error");
      }
    });
  };
  return node;
}
function table(
  headings: string[],
  rows: Array<Array<string | HTMLElement>>,
): HTMLTableElement {
  const node = el("table"),
    head = el("tr");
  node.className = "admin-table";
  headings.forEach((value) => head.append(el("th", value)));
  node.append(head);
  rows.forEach((values) => {
    const row = el("tr");
    values.forEach((value) => {
      const cell = el("td");
      typeof value === "string"
        ? cell.append(document.createTextNode(value))
        : cell.append(value);
      row.append(cell);
    });
    node.append(row);
  });
  return node;
}
function style(): void {
  if (document.getElementById("operation-excel-batch-style")) return;
  const node = el("style");
  node.id = "operation-excel-batch-style";
  node.textContent = `
    .operation-excel-workspace { margin: 8px 0; color: #1f2329; }
    .operation-excel-workspace * { box-sizing: border-box; }
    .xeb-card { overflow: hidden; border: 1px solid #dee0e3; border-radius: 10px; background: #fff; }
    .xeb-head { padding: 14px 16px; border-bottom: 1px solid #eff0f1; }
    .xeb-head h2, .xeb-head h3 { margin: 0; font-size: 16px; }
    .operation-excel-workspace small { color: #8f959e; font-size: 12px; }
    .xeb-body { padding: 16px; }
    .xeb-meta, .xeb-actions { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
    .xeb-actions.admin-toolbar { gap: 8px; }
    .xeb-actions .admin-field { min-width: 168px; margin: 0; }
    .xeb-pagination { justify-content: space-between; }
    .xeb-history-dialog { width: min(1120px, calc(100vw - 32px)) !important; max-width: 94vw; max-height: calc(100vh - 32px); }
    .xeb-history-dialog .xeb-scroll { max-width: 100%; }
    .xeb-history-table { min-width: 1320px; }
    .xeb-history-table th:nth-child(1), .xeb-history-table td:nth-child(1) { min-width: 130px; }
    .xeb-history-table th:nth-child(2), .xeb-history-table td:nth-child(2) { min-width: 190px; }
    .xeb-history-table th:nth-child(3), .xeb-history-table td:nth-child(3) { min-width: 190px; }
    .xeb-history-table th:nth-child(4), .xeb-history-table td:nth-child(4) { min-width: 130px; }
    .xeb-history-table th:nth-child(5), .xeb-history-table td:nth-child(5) { min-width: 110px; }
    .xeb-history-table th:nth-child(6), .xeb-history-table td:nth-child(6) { min-width: 160px; }
    .xeb-history-table th:nth-child(7), .xeb-history-table td:nth-child(7) { min-width: 120px; }
    .xeb-history-table th:nth-child(8), .xeb-history-table td:nth-child(8) { min-width: 150px; }
    .xeb-history-table th:nth-child(9), .xeb-history-table td:nth-child(9) { min-width: 90px; }
    .xeb-table-hint { margin: 10px 0 6px; color: #5f6b7a; font-size: 13px; }
    .xeb-detail { display: grid; grid-template-columns: 190px minmax(0, 1fr); gap: 16px; margin-top: 16px; }
    .xeb-detail-nav { padding: 8px; }
    .xeb-detail-nav .admin-button { display: flex; width: 100%; justify-content: flex-start; margin: 2px 0; }
    .xeb-detail-nav .admin-button[data-selected=true] { border-color: #c9d8ff; color: #245bdb; background: #eff4ff; }
    .xeb-grid { display: grid; grid-template-columns: repeat(4, minmax(0, 1fr)); gap: 10px; margin: 14px 0; }
    .xeb-stat { padding: 10px; border: 1px solid #eff0f1; border-radius: 8px; background: #fafbfc; }
    .xeb-stat b { display: block; font-size: 18px; }
    .xeb-scroll { overflow: auto; margin-top: 14px; }
    .xeb-scroll .admin-table { min-width: 760px; }
    .operation-excel-workspace .admin-table { border-collapse: collapse; }
    .operation-excel-workspace .admin-table th, .operation-excel-workspace .admin-table td { white-space: pre-wrap; overflow-wrap: anywhere; vertical-align: top; }
    .xeb-cover { width: 80px; height: 60px; border-radius: 4px; object-fit: cover; }
    .xeb-status { margin: 12px 0 0; }
    .xeb-status[hidden] { display: none; }
    .operation-excel-workspace dialog { width: min(680px, 92vw); padding: 20px; border: 1px solid #dee0e3; border-radius: 10px; }
    .operation-excel-workspace dialog .admin-field { width: 100%; margin: 10px 0 4px; }
    .operation-excel-workspace dialog .admin-field textarea { width: 100%; }
    .operation-excel-workspace .admin-check-line { margin: 10px 0 4px; }
    @media screen and (max-width: 800px) {
      .xeb-detail { grid-template-columns: 1fr; }
      .xeb-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); }
      .xeb-head, .xeb-body { padding: 12px; }
      .xeb-pagination { align-items: flex-start; flex-direction: column; }
      .xeb-actions .admin-field { width: 100%; min-width: 0; }
      .xeb-history-dialog { width: calc(100vw - 16px) !important; max-width: calc(100vw - 16px); padding: 14px; }
    }
  `;
  document.head.append(node);
  const pickerStyle = el("style");
  pickerStyle.textContent = `.xeb-cover-picker{display:grid;gap:12px}.xeb-cover-picker-list{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:10px;max-height:420px;overflow:auto}.xeb-cover-picker-item{display:grid;gap:6px;padding:8px;border:1px solid #dee0e3;border-radius:8px;background:#fff;text-align:left}.xeb-cover-picker-item img{width:100%;height:96px;object-fit:cover;background:#f5f6f7;border-radius:5px}.xeb-cover-picker-item small{overflow-wrap:anywhere}@media screen and (max-width:800px){.xeb-cover-picker-list{grid-template-columns:repeat(2,minmax(0,1fr))}}`;
  document.head.append(pickerStyle);
}
function batchState(batch: Obj): string {
  return states[String(batch.state)] || String(batch.state || "未知");
}
function rowState(row: Obj): string {
  if (row.excluded) return "已排除";
  if (row.review_state !== "approved") return "未批准";
  return deliveryLabel(row.delivery_state || row.state);
}

function historicalSegment(row: Obj): string {
  return `分层：${String(row.segment || "未分层")}\n行状态：${row.excluded ? "已排除" : "参与"}`;
}

function historicalReview(row: Obj): string {
  return `审核：${reviewLabel(row.review_state)}`;
}

function historicalDelivery(row: Obj): string {
  const facts = [`执行：${deliveryLabel(row.delivery_state || row.state)}`];
  if (row.failure_reason) facts.push(`原因：${deliveryReason(row.failure_reason)}`);
  return facts.join("\n");
}

function historicalTrace(row: Obj, contentVersion: number): string {
  const rowID = Number.isSafeInteger(Number(row.id)) && Number(row.id) > 0
    ? String(row.id)
    : "未记录";
  const rowVersion = Number.isSafeInteger(Number(row.version)) && Number(row.version) > 0
    ? String(row.version)
    : "未记录";
  return `行 #${rowID} · 行版本 #${rowVersion}\n内容版本 #${contentVersion}`;
}
function rate(value: unknown): string {
  return typeof value === "number"
    ? `${(value * 100).toFixed(1)}%`
    : "暂不可统计";
}
function card(row: Obj): HTMLElement {
  const wrap = el("div"),
    value = row.card || {};
  if (value.cover_digest) {
    const image = el("img") as HTMLImageElement;
    image.className = "xeb-cover";
    image.src = `${base}/covers/${encodeURIComponent(String(value.cover_digest))}`;
    image.alt = "统一封面";
    wrap.append(image);
  } else wrap.append(el("small", "未上传统一封面"));
  wrap.append(
    el("div", String(value.title || "标题为空：执行时明确失败")),
    el("small", String(value.path || "")),
  );
  return wrap;
}

// Excel rows are an Owner-defined two-block contract: text followed by the
// Excel card.  The Composer is deliberately text-only here; it only returns a
// local form draft and does not acquire a media-library contract.
const excelTextRule: ContentTextRule = {
  normalize: (value) => value,
  validate: (value) => {
    if (value.trim() === "") return "请输入话术。";
    if (new TextEncoder().encode(value).length > 8000)
      return "话术不能超过 8000 字节，请删减后再确认。";
    return undefined;
  },
};

function excelCardSupplement(
  row: Obj,
  versionKey: string,
): ContentPresentationSupplement[] {
  const source = row.card && typeof row.card === "object" ? row.card : {};
  const title = String(source.title || "");
  const path = String(source.path || "");
  // A historical row's card is the sole authority for its historical cover.
  // In particular, never substitute the batch's current cover digest here.
  const digest = String(source.cover_digest || "");
  const reasons: string[] = [];
  if (!title) reasons.push("Excel 标题为空，执行时会明确失败。");
  if (!path) reasons.push("小程序路径未记录，当前内容不能执行。");
  if (!digest)
    reasons.push("该内容版本未记录统一封面，不能推断为当前批次封面。");
  return [{
    key: `excel-card:${versionKey}:${String(row.id || "unknown")}`,
    kind: "excel_card",
    title: title || "标题为空：执行时明确失败",
    description: path ? `小程序路径：${path}` : undefined,
    thumbnailURL: digest
      ? `${base}/covers/${encodeURIComponent(digest)}`
      : undefined,
    unavailableReason: reasons.join(" ") || undefined,
  }];
}

class Workspace {
  private root: HTMLElement;
  private plans: Obj[] = [];
  private planTotal = 0;
  private planLimit = 20;
  private planOffset = 0;
  private planHasMore = false;
  private planNextOffset: number | null = null;
  private plansLoading = false;
  private plansError = "";
  private legacy: Obj[] = [];
  private legacyError = "";
  private batches: Obj[] = [];
  private strategyKey = "";
  private strategy: Obj = {};
  private batchID = 0;
  private tab = "content";
  private generation = 0;
  private batch: Obj | null = null;
  private metadataRequestID = 0;
  private metadataController: AbortController | null = null;
  private contentPage = cursorPage();
  private receiptPage = cursorPage();
  private report = reportRead();
  private versionRefreshes = 0;
  private batchWritePending = false;
  constructor(parent: HTMLElement) {
    style();
    this.root = el("section");
    this.root.className = "operation-excel-workspace";
    parent.replaceChildren(this.root);
  }
  async mount(): Promise<void> {
    await this.loadPlans(0, true);
  }
  private right(): HTMLElement {
    return this.root.querySelector<HTMLElement>(".xeb-detail-main")!;
  }
  private tell(value: string, kind: "success" | "error" = "success"): void {
    const node = this.root.querySelector<HTMLElement>("[data-excel-feedback]");
    if (node) setNotice(node, value, kind);
  }
  private batchAction(
    node: HTMLButtonElement,
    approve = false,
    coverReady = false,
  ): HTMLButtonElement {
    node.dataset.batchMutation = "true";
    if (approve) {
      node.dataset.batchApprove = "true";
      node.dataset.coverReady = String(coverReady);
    }
    node.disabled = this.batchWritePending || (approve && !coverReady);
    return node;
  }
  private syncBatchMutationControls(): void {
    this.root
      .querySelectorAll<HTMLButtonElement>("button[data-batch-mutation]")
      .forEach((node) => {
        const needsCover = node.dataset.batchApprove === "true";
        node.disabled =
          this.batchWritePending ||
          (needsCover && node.dataset.coverReady !== "true");
      });
  }
  private async withBatchWrite<T>(run: () => Promise<T>): Promise<T> {
    if (this.batchWritePending)
      throw new Error("本批次正在保存，请等待当前操作完成");
    this.batchWritePending = true;
    this.syncBatchMutationControls();
    try {
      return await run();
    } finally {
      this.batchWritePending = false;
      this.syncBatchMutationControls();
    }
  }
  private resetCursorPage(page: CursorPage): void {
    page.controller?.abort();
    page.controller = null;
    page.items = [];
    page.cursors = [""];
    page.index = 0;
    page.nextCursor = "";
    page.retryCursor = "";
    page.retryFromMetadata = false;
    page.loading = false;
    page.error = "";
    page.requestID += 1;
  }
  private resetReport(): void {
    this.report.controller?.abort();
    this.report.controller = null;
    this.report.value = null;
    this.report.loading = false;
    this.report.error = "";
    this.report.requestID += 1;
  }
  private cancelVisibleReads(): void {
    this.metadataController?.abort();
    this.metadataController = null;
    this.metadataRequestID += 1;
    this.resetCursorPage(this.contentPage);
    this.resetCursorPage(this.receiptPage);
    this.resetReport();
  }
  private beginWorkspaceRead(keepVersionRefresh = false): number {
    this.generation += 1;
    if (!keepVersionRefresh) this.versionRefreshes = 0;
    this.cancelVisibleReads();
    return this.generation;
  }
  private currentWorkspace(
    workspaceGeneration: number,
    batchID: number,
    tab?: string,
  ): boolean {
    return (
      workspaceGeneration === this.generation &&
      batchID === this.batchID &&
      (tab === undefined || tab === this.tab)
    );
  }
  private currentContentVersion(batch: Obj | null): number {
    const version = Number(batch?.current_content_version);
    return Number.isSafeInteger(version) && version > 0 ? version : 0;
  }
  private batchPresentationChanged(previous: Obj, current: Obj): boolean {
    const fields = [
      "version",
      "state",
      "cover_digest",
      "cover_image_id",
      "current_content_version",
    ];
    return (
      fields.some((field) => previous[field] !== current[field]) ||
      JSON.stringify(previous.summary || {}) !== JSON.stringify(current.summary || {})
    );
  }
  private historicalContentVersion(detail: Obj): number {
    const version = detail.content_version;
    if (!version || typeof version !== "object") return 0;
    const revision = Number(version.content_version);
    return Number.isSafeInteger(revision) && revision > 0 ? revision : 0;
  }
  private setCurrentBatch(batch: Obj): void {
    this.batch = batch;
    const id = Number(batch.id);
    this.batches = this.batches.map((item) =>
      Number(item.id) === id ? { ...item, ...batch } : item,
    );
  }
  private paginationError(
    page: CursorPage,
    message: string,
    retryCursor = page.cursors[page.index] || "",
    retryFromMetadata = false,
  ): void {
    page.loading = false;
    page.error = message;
    page.nextCursor = "";
    page.retryCursor = retryCursor;
    page.retryFromMetadata = retryFromMetadata;
    page.controller = null;
  }
  private async loadPlans(offset = this.planOffset, openHash = false): Promise<void> {
    if (this.plansLoading) return;
    this.plansLoading = true;
    this.plansError = "";
    this.renderShell();
    const [summaryResult, legacyResult] = await Promise.all([
      api(`${base}/strategy-summaries?limit=20&offset=${Math.max(0, offset)}`)
        .then((value) => ({ value, error: "" }))
        .catch((error) => ({ value: {} as Obj, error: (error as Error).message })),
      api(`${base}/legacy`)
        .then((value) => ({ value, error: "" }))
        .catch((error) => ({
          value: { items: [] },
          error: (error as Error).message,
        })),
    ]);
    this.plansLoading = false;
    this.legacy = Array.isArray(legacyResult.value.items)
      ? legacyResult.value.items
      : [];
    this.legacyError = legacyResult.error;
    if (summaryResult.error) {
      this.plans = [];
      this.plansError = `长期计划读取失败：${summaryResult.error}`;
      this.renderShell();
      return;
    }
    const result = summaryResult.value;
    const items = Array.isArray(result.items) ? result.items : [];
    const total = Number(result.total);
    const limit = Number(result.limit);
    const returnedOffset = Number(result.offset);
    const next = result.next_offset === null ? null : Number(result.next_offset);
    if (!Number.isSafeInteger(total) || total < 0 || !Number.isSafeInteger(limit) || limit !== 20 || !Number.isSafeInteger(returnedOffset) || returnedOffset < 0 || returnedOffset !== Math.max(0, offset) || items.length > limit || (next !== null && (!Number.isSafeInteger(next) || next <= returnedOffset))) {
      this.plans = [];
      this.plansError = "长期计划分页响应无效，请刷新后重试";
      this.renderShell();
      return;
    }
    if (!items.length && returnedOffset > 0 && total > 0) {
      const lastPageOffset = Math.floor((total - 1) / limit) * limit;
      // A concurrent delete may leave this page empty after the server counted
      // strategies. Never retry the same offset: fall back one page when the
      // newly-computed last page is not before the requested offset.
      const fallbackOffset =
        lastPageOffset < returnedOffset
          ? lastPageOffset
          : Math.max(0, returnedOffset - limit);
      await this.loadPlans(fallbackOffset, openHash);
      return;
    }
    this.plans = items;
    this.planTotal = total;
    this.planLimit = limit;
    this.planOffset = returnedOffset;
    this.planHasMore = result.has_more === true;
    this.planNextOffset = this.planHasMore ? next : null;
    if (this.planHasMore !== (returnedOffset + items.length < total) || (this.planHasMore && this.planNextOffset !== returnedOffset + items.length)) {
      this.plans = [];
      this.plansError = "长期计划分页游标不一致，请刷新后重试";
      this.renderShell();
      return;
    }
    this.renderShell();
    const strategy = openHash
      ? new URLSearchParams(location.hash.slice(1)).get("strategy")
      : "";
    if (strategy)
      await this.openStrategy(strategy, false);
  }
  private renderShell(): void {
    this.root.replaceChildren();
    const left = el("section");
    left.className = "xeb-card";
    const head = el("div", "长期计划");
    head.className = "xeb-head";
    left.append(head);
    if (this.plansLoading) {
      const loading = el("div", "正在读取长期计划…");
      loading.className = "admin-state admin-state--loading";
      left.append(loading);
      this.root.append(left);
      return;
    }
    if (this.plansError) {
      const error = notice(this.plansError, "error");
      left.append(error, action("重新读取", () => this.loadPlans(this.planOffset)));
      this.root.append(left);
      return;
    }
    if (!this.plans.length) {
      const empty = el("div", "暂无可访问的长期计划。");
      empty.className = "admin-state";
      left.append(empty);
    }
    const rows = this.plans.map((plan) => {
      const latest = plan.latest_batch || {};
      const progress = latest.id
        ? `批次 #${latest.id} · ${batchState(latest)} · 预计任务 ${latest.summary?.expected_tasks ?? 0}`
        : plan.latest_batch_status === "unavailable"
          ? "批次摘要暂不可用"
          : "暂无批次";
      return [
        String(plan.title || plan.strategy_key),
        progress,
        action("查看详情", () => this.openStrategy(String(plan.strategy_key))),
      ];
    });
    left.append(table(["任务名称", "当前进度", "操作"], rows));
    const pager = el("div");
    pager.className = "admin-toolbar admin-pagination xeb-actions xeb-pagination";
    const start = this.planTotal ? this.planOffset + 1 : 0;
    const end = Math.min(this.planOffset + this.plans.length, this.planTotal);
    const previous = action("上一页", () => this.loadPlans(Math.max(0, this.planOffset - this.planLimit)));
    previous.disabled = this.planOffset <= 0;
    const next = action("下一页", () => this.loadPlans(this.planNextOffset ?? this.planOffset));
    next.disabled = !this.planHasMore || this.planNextOffset === null;
    pager.append(el("small", `第 ${start}–${end} 项，共 ${this.planTotal} 项`), previous, next);
    left.append(pager);
    if (this.legacy.length) {
      const legacy = el("div");
      legacy.className = "xeb-head";
      legacy.append(
        el("h3", "未关联的旧 Excel 审核记录"),
        el("small", "仅供发现；没有自动关联或迁移入口。"),
      );
      this.legacy.forEach((item) =>
        legacy.append(
          el(
            "small",
            `${item.name || `计划 #${item.id}`} · ${batchState(item)}`,
          ),
        ),
      );
      left.append(legacy);
    }
    if (this.legacyError)
      left.append(el("p", `旧 Excel 审核记录暂不可读取：${this.legacyError}`));
    this.root.append(left);
  }
  private renderDetailShell(): void {
    this.root.replaceChildren();
    const header = el("section");
    header.className = "xeb-card";
    const head = el("div");
    head.className = "xeb-head xeb-meta";
    head.append(
      action("返回计划列表", () => {
        this.beginWorkspaceRead();
        this.strategyKey = "";
        this.strategy = {};
        this.batch = null;
        this.batchID = 0;
        location.hash = "";
        this.renderShell();
      }, "ghost"),
      el("h2", "运营闭环批次详情"),
    );
    header.append(head);
    const layout = el("div");
    layout.className = "xeb-detail";
    const nav = el("aside");
    nav.className = "xeb-card xeb-detail-nav";
    [
      ["content", "内容准备与发送"],
      ["effects", "发送效果与复盘"],
    ].forEach(([id, label]) => {
      const node = action(label, async () => {
        this.tab = id;
        this.updateTabNav();
        if (this.batchID) await this.loadSelected();
      }, "ghost");
      node.dataset.tab = id;
      node.dataset.selected = String(this.tab === id);
      nav.append(node);
    });
    const main = el("section");
    main.className = "xeb-card xeb-detail-main";
    const loading = el("div", "正在读取批次详情…");
    loading.className = "admin-state admin-state--loading";
    main.append(loading);
    layout.append(nav, main);
    this.root.append(header, layout);
  }
  private updateTabNav(): void {
    this.root
      .querySelectorAll<HTMLElement>(".xeb-detail-nav button[data-tab]")
      .forEach((node) => {
        node.dataset.selected = String(node.dataset.tab === this.tab);
      });
  }
  private async openStrategy(
    next: string,
    updateLocation = true,
    preferredBatchID = 0,
    requireReadback = false,
  ): Promise<void> {
    const currentGeneration = this.beginWorkspaceRead();
    this.strategyKey = next;
    this.batchID = preferredBatchID;
    this.batch = null;
    if (updateLocation)
      location.hash = new URLSearchParams({ strategy: next }).toString();
    this.renderDetailShell();
    try {
      const requestID = ++this.metadataRequestID;
      const controller = new AbortController();
      this.metadataController = controller;
      const result = await api(
        `${base}/strategies/${encodeURIComponent(next)}`,
        "GET",
        undefined,
        undefined,
        controller.signal,
      );
      if (
        requestID !== this.metadataRequestID ||
        !this.currentWorkspace(currentGeneration, this.batchID) ||
        next !== this.strategyKey
      )
        return;
      this.batches = Array.isArray(result.items) ? result.items : [];
      this.batchID = Number(
        this.batches.find((batch) => Number(batch.id) === preferredBatchID)
          ?.id ||
          this.batches[0]?.id ||
          0,
      );
      const listedStrategy =
        this.plans.find(
          (value) => String(value.strategy_key) === this.strategyKey,
        ) || {};
      this.strategy = { ...listedStrategy, ...(result.strategy || {}) };
      this.batch =
        this.batches.find((batch) => Number(batch.id) === this.batchID) ||
        null;
      this.metadataController = null;
      await this.loadSelected(true, false, requireReadback);
    } catch (error) {
      if (
        !aborted(error) &&
        currentGeneration === this.generation &&
        next === this.strategyKey
      ) {
        const failed = el("div", (error as Error).message);
        failed.className = "admin-state admin-state--error";
        this.right().replaceChildren(failed);
      }
      if (requireReadback) throw error;
    }
  }
  private async refreshSelectedMetadata(
    workspaceGeneration: number,
    id: number,
  ): Promise<Obj | null> {
    const requestID = ++this.metadataRequestID;
    const controller = new AbortController();
    this.metadataController = controller;
    const result = await api(
      `${base}/strategies/${encodeURIComponent(this.strategyKey)}`,
      "GET",
      undefined,
      undefined,
      controller.signal,
    );
    if (
      requestID !== this.metadataRequestID ||
      !this.currentWorkspace(workspaceGeneration, id)
    )
      return null;
    this.batches = Array.isArray(result.items) ? result.items : [];
    const listedStrategy =
      this.plans.find(
        (value) => String(value.strategy_key) === this.strategyKey,
      ) || {};
    this.strategy = { ...listedStrategy, ...(result.strategy || {}) };
    const batch =
      this.batches.find((item) => Number(item.id) === id) || null;
    if (!batch) throw new Error("当前批次已变化，请返回计划列表后重新选择");
    this.setCurrentBatch(batch);
    this.metadataController = null;
    return batch;
  }
  private async loadSelected(
    reuseCurrentMetadata = false,
    keepVersionRefresh = false,
    requireReadback = false,
  ): Promise<void> {
    const id = this.batchID;
    const workspaceGeneration = this.beginWorkspaceRead(keepVersionRefresh);
    if (!id) {
      this.batch = null;
      this.renderDetail();
      return;
    }
    const loading = el("div", "正在读取批次详情…");
    loading.className = "admin-state admin-state--loading";
    this.right().replaceChildren(loading);
    try {
      const batch = reuseCurrentMetadata
        ? this.batch
        : await this.refreshSelectedMetadata(workspaceGeneration, id);
      if (
        !batch ||
        !this.currentWorkspace(workspaceGeneration, id) ||
        !this.currentContentVersion(batch)
      ) {
        if (this.currentWorkspace(workspaceGeneration, id) && batch)
          throw new Error("批次读取响应缺少当前内容版本");
        return;
      }
      this.setCurrentBatch(batch);
      this.renderDetail();
      if (this.tab === "content") {
        await this.loadContentPage("", requireReadback);
        if (!this.currentWorkspace(workspaceGeneration, id)) return;
        if (requireReadback && this.contentPage.error) {
          const failure = new Error(`内容回读失败：${this.contentPage.error}`) as Error & {
            readback?: boolean;
          };
          failure.readback = true;
          this.tell(failure.message, "error");
          throw failure;
        }
      } else {
        await Promise.all([this.loadReceiptPage(""), this.loadReport()]);
      }
    } catch (error) {
      const readback = (error as { readback?: boolean } | null)?.readback === true;
      if (
        !readback &&
        !aborted(error) &&
        this.currentWorkspace(workspaceGeneration, id)
      ) {
        const failed = el("div", (error as Error).message);
        failed.className = "admin-state admin-state--error";
        this.right().replaceChildren(failed);
      }
      if (requireReadback) {
        if (readback) throw error;
        const failure = new Error(`批次回读失败：${(error as Error).message}`) as Error & {
          readback?: boolean;
        };
        failure.readback = true;
        this.tell(failure.message, "error");
        throw failure;
      }
    }
  }
  private renderDetail(): void {
    const batch = this.batch;
    const right = this.right();
    right.replaceChildren();
    const head = el("div");
    head.className = "xeb-head";
    const actions = el("div");
    actions.className = "admin-toolbar xeb-actions";
    actions.append(action("新建发送批次", () => this.importDialog(), "primary"));
    if (this.batches.length) {
      const select = el("select") as HTMLSelectElement;
      select.setAttribute("aria-label", "历史批次");
      this.batches.forEach((item) => {
        const option = el(
          "option",
          `批次 #${item.id} · ${batchState(item)}`,
        ) as HTMLOptionElement;
        option.value = String(item.id);
        option.selected = Number(item.id) === this.batchID;
        select.append(option);
      });
      select.onchange = async () => {
        this.batchID = Number(select.value);
        await this.loadSelected();
      };
      actions.append(field("历史批次", select));
    }
    head.append(el("h2", String(this.strategy.title || this.strategyKey)), actions);
    right.append(head);
    if (!this.batchID) {
      const empty = el(
        "div",
        "此长期计划还没有 Excel 批次。文件在本地选择期间不会创建任何计划或批次。",
      );
      empty.className = "admin-state";
      right.append(empty, notice());
      return;
    }
    if (!batch) throw new Error("批次读取响应缺少当前批次");
    const body = el("div");
    body.className = "xeb-body";
    body.append(this.batchSummary(batch));
    const status = notice();
    body.append(status);
    right.append(body);
    if (this.tab === "content") this.content(body, batch);
    else this.effects(body, batch);
  }
  private batchSummary(batch: Obj): HTMLElement {
    const summary = batch.summary || {};
    const wrap = el("div");
    const meta = el("div");
    meta.className = "xeb-meta";
    meta.append(
      el("small", `当前批次 #${batch.id}`),
      el("small", `状态：${batchState(batch)}`),
      el("small", `内容版本：${batch.current_content_version || 1}`),
    );
    const grid = el("div");
    grid.className = "xeb-grid";
    [
      ["总行数", summary.total_rows],
      ["已排除", summary.excluded_rows],
      ["空标题", summary.empty_title_rows],
      ["预计任务", summary.expected_tasks],
    ].forEach(([label, value]) => {
      const stat = el("div");
      stat.className = "xeb-stat";
      stat.append(el("small", String(label)), el("b", String(value ?? 0)));
      grid.append(stat);
    });
    const coverImageID = Number(batch.cover_image_id || 0);
    const cover = el(
      "p",
      coverImageID > 0
        ? `冻结封面：素材 #${coverImageID}；内容摘要已冻结，凭据刷新不会触发重新审核。`
        : batch.cover_digest
          ? "冻结封面：已上传内容；内容摘要已冻结。"
          : "统一封面：未设置。",
    );
    cover.className = "admin-alert xeb-status";
    wrap.append(meta, grid, cover);
    return wrap;
  }
  private editable(batch: Obj): boolean {
    return ["pending_review", "partially_approved"].includes(
      String(batch.state),
    );
  }
  private content(parent: HTMLElement, batch: Obj): void {
    const id = Number(batch.id),
      editable = this.editable(batch),
      actions = el("div");
    actions.className = "admin-toolbar xeb-actions";
    if (editable) {
      actions.append(
        this.batchAction(action("替换 Excel", () => this.importDialog(batch))),
      );
      const cover = el("input") as HTMLInputElement;
      cover.type = "file";
      cover.accept = "image/png,image/jpeg";
      cover.setAttribute("aria-label", "统一封面图片");
      const approve = this.batchAction(
        action("审核通过并创建企微群发任务", async () => {
          await this.withBatchWrite(async () => {
            const preview = await api(
              `${base}/${id}/preview-approval`,
              "POST",
              { expected_version: batch.version },
              previewKey,
            );
            const digest = String(preview.preview_digest || "");
            if (!digest) throw new Error("审核预览无效，请刷新后重试");
            await api(
              `${base}/${id}/approve`,
              "POST",
              { expected_version: batch.version, preview_digest: digest },
              approveKey,
            );
            await this.loadSelected(false, false, true);
            this.tell(
              "企微任务意图已创建，员工仍需在企微端执行；这不等于发送成功。",
            );
          });
        }, "primary"),
        true,
        Boolean(batch.cover_digest || Number(batch.cover_image_id || 0) > 0),
      );
      const previewKey = key(`excel-preview-${id}-${batch.version}`);
      const approveKey = key(`excel-approve-${id}-${batch.version}`);
      const coverKey = key(`excel-cover-${id}-${batch.version}`);
      const selectCover = this.batchAction(
        action("选择已有启用图片", () => this.coverPickerDialog(batch)),
      );
      actions.append(
        field("上传统一封面（PNG 或 JPEG）", cover),
        this.batchAction(
          action("上传统一封面", async () => {
            await this.withBatchWrite(async () => {
              const file = cover.files?.[0];
              if (!file) throw new Error("请选择 PNG 或 JPEG 封面");
              await api(
                `${base}/${id}/cover?expected_version=${batch.version}`,
                "POST",
                file,
                coverKey,
              );
              await this.loadSelected(false, false, true);
              this.tell("统一封面已更新；请重新核对预览。");
            });
          }),
        ),
        selectCover,
        approve,
      );
    }
    actions.append(action("查看旧版本", () => this.versionDialog(id), "ghost"));
    parent.append(actions);
    if (!(batch.cover_digest || Number(batch.cover_image_id || 0) > 0) && editable)
      parent.append(el("p", "没有统一封面，不能审核通过并创建企微群发任务。"));
    const page = el("div");
    page.dataset.excelContentPage = "";
    parent.append(page);
    this.renderContentPage(batch);
  }
  private pageNavigation(
    page: CursorPage,
    kind: "content" | "receipts" | "history",
    onPage: (cursor: string) => Promise<void>,
  ): HTMLElement {
    const pager = el("div");
    pager.className = "admin-toolbar admin-pagination xeb-actions xeb-pagination";
    pager.dataset.excelPage = kind;
    const previous = action("上一页", () => {
      const cursor = page.cursors[page.index - 1];
      if (cursor !== undefined) return onPage(cursor);
    }, "ghost");
    previous.disabled = page.loading || this.batchWritePending || page.index <= 0;
    const next = action("下一页", () => {
      if (page.nextCursor) return onPage(page.nextCursor);
    }, "ghost");
    next.disabled = page.loading || this.batchWritePending || !page.nextCursor;
    pager.append(
      el("small", `第 ${page.index + 1} 页 · 当前页 ${page.items.length} 项`),
      previous,
      next,
    );
    return pager;
  }
  private renderContentPage(batch: Obj): void {
    const id = Number(batch.id);
    const target = this.root.querySelector<HTMLElement>("[data-excel-content-page]");
    if (!target || this.tab !== "content" || Number(batch.id) !== this.batchID)
      return;
    const page = this.contentPage;
    target.replaceChildren();
    if (page.loading) {
      const loading = el("div", "正在读取当前内容页…");
      loading.className = "admin-state admin-state--loading";
      target.append(loading);
      return;
    }
    if (page.error) {
      target.append(
        notice(`内容页读取失败：${page.error}`, "error"),
        action("重新读取当前页", () =>
          page.retryFromMetadata
            ? this.loadSelected()
            : this.loadContentPage(
                page.retryCursor || page.cursors[page.index] || "",
              ),
        ),
      );
      return;
    }
    const scroll = el("div");
    scroll.className = "xeb-scroll";
    scroll.append(
      table(
        ["UnionID / 员工", "话术", "小程序卡片", "分层", "状态", "审核操作"],
        page.items.map((row) => {
          const controls = el("div");
          controls.className = "admin-toolbar xeb-actions";
          controls.append(
            action(
              "查看已保存内容",
              () =>
                this.openReadonlyExcelRow(
                  row,
                  "已保存 Excel 行内容",
                  `current:${id}:${String(batch.current_content_version || "current")}`,
                ),
              "ghost",
            ),
          );
          if (this.editable(batch)) {
            controls.append(
              this.batchAction(action("修改", () => this.rowDialog(batch, row), "ghost")),
              this.batchAction(
                action(row.excluded ? "恢复" : "排除", async () => {
                  await this.withBatchWrite(async () => {
                    await api(
                      `${base}/${id}/rows/${row.id}`,
                      "PATCH",
                      {
                        expected_version: row.version,
                        text: row.text,
                        path: row.card?.path || "",
                        title: row.card?.title || "",
                        segment: row.segment || "",
                        excluded: !row.excluded,
                      },
                      key(`excel-row-${id}-${row.id}`),
                    );
                    await this.loadSelected(false, false, true);
                  });
                }, row.excluded ? "ghost" : "danger"),
              ),
            );
          }
          return [
            `${row.unionid}\n${row.sender_userid}`,
            String(row.text || ""),
            card(row),
            String(row.segment || "未分层"),
            `${rowState(row)}${row.failure_reason ? `\n${deliveryReason(row.failure_reason)}` : ""}${row.sent_at ? `\n${displayDateTime(row.sent_at)}` : ""}`,
            controls,
          ];
        }),
      ),
    );
    target.append(scroll);
    if (!page.items.length) target.append(el("p", "当前页没有内容行。"));
    target.append(
      this.pageNavigation(page, "content", (cursor) =>
        this.loadContentPage(cursor),
      ),
    );
  }
  private async loadContentPage(
    cursor: string,
    requireReadback = false,
  ): Promise<void> {
    const batch = this.batch;
    const id = Number(batch?.id);
    const expectedVersion = this.currentContentVersion(batch);
    const page = this.contentPage;
    const workspaceGeneration = this.generation;
    const index = page.cursors.indexOf(cursor);
    if (
      !batch ||
      this.tab !== "content" ||
      !Number.isSafeInteger(id) ||
      id < 1 ||
      !expectedVersion ||
      index < 0
    )
      return;
    page.controller?.abort();
    const requestID = ++page.requestID;
    const controller = new AbortController();
    page.controller = controller;
    page.loading = true;
    page.error = "";
    this.renderContentPage(batch);
    try {
      const result = await api(
        pagedPath(`${base}/${id}`, cursor),
        "GET",
        undefined,
        undefined,
        controller.signal,
      );
      if (
        requestID !== page.requestID ||
        !this.currentWorkspace(workspaceGeneration, id, "content")
      )
        return;
      const current = result.batch || result.plan;
      const returnedVersion = this.currentContentVersion(current);
      if (
        !current ||
        Number(current.id) !== id ||
        returnedVersion !== expectedVersion
      ) {
        await this.recoverVersionDrift(page, "内容", cursor, requireReadback);
        return;
      }
      const next = typeof result.next_cursor === "string" ? result.next_cursor : "";
      const knownNext = page.cursors[index + 1] || "";
      if (
        next &&
        (next === cursor ||
          page.cursors.slice(0, index + 1).includes(next) ||
          (knownNext && knownNext !== next))
      ) {
        this.paginationError(page, "分页游标重复，已停止读取以避免混合批次数据", cursor);
        this.renderContentPage(batch);
        return;
      }
      page.items = Array.isArray(result.rows) ? result.rows : [];
      page.cursors = page.cursors.slice(0, index + 1);
      if (next) page.cursors.push(next);
      page.index = index;
      page.nextCursor = next;
      page.loading = false;
      page.error = "";
      page.retryFromMetadata = false;
      page.controller = null;
      const presentationChanged = this.batchPresentationChanged(batch, current);
      this.setCurrentBatch(current);
      if (presentationChanged) this.renderDetail();
      else this.renderContentPage(current);
    } catch (error) {
      if ((error as { readback?: boolean } | null)?.readback === true)
        throw error;
      if (
        !aborted(error) &&
        requestID === page.requestID &&
        this.currentWorkspace(workspaceGeneration, id, "content")
      ) {
        this.paginationError(page, (error as Error).message, cursor);
        this.renderContentPage(batch);
      }
    }
  }
  private recoverVersionDrift(
    page: CursorPage,
    label: string,
    retryCursor: string,
    requireReadback = false,
  ): Promise<void> {
    if (this.versionRefreshes >= 1) {
      this.paginationError(
        page,
        `${label}版本持续变化，请重新读取后重试`,
        retryCursor,
        true,
      );
      if (this.batch) this.renderDetail();
      return Promise.resolve();
    }
    this.versionRefreshes += 1;
    return this.loadSelected(false, true, requireReadback);
  }
  private coverPickerDialog(batch: Obj): void {
    if (!this.editable(batch) || Number(batch.id) !== this.batchID) return;
    const boundBatch = Number(batch.id);
    const boundGeneration = this.generation;
    const dialog = el("dialog") as HTMLDialogElement;
    dialog.className = "xeb-cover-picker";
    dialog.setAttribute("aria-label", "选择已有启用图片");
    const heading = el("h3", "选择已有启用图片");
    const hint = el("p", "仅显示当前启用的图片。选择后会冻结图片编号和内容摘要，凭据刷新不会改变已提交批次。");
    const query = el("input") as HTMLInputElement;
    query.type = "search";
    query.setAttribute("aria-label", "搜索启用图片");
    query.placeholder = "搜索图片名称";
    const search = action("查询", () => load(0));
    const searchRow = el("div");
    searchRow.className = "admin-toolbar xeb-actions";
    searchRow.append(field("搜索启用图片", query), search);
    const status = notice();
    const list = el("div");
    list.className = "xeb-cover-picker-list";
    const pager = el("div");
    pager.className = "admin-toolbar admin-pagination xeb-actions";
    const range = el("small");
    const previous = action("上一页", () => load(Math.max(0, offset - pageSize)), "ghost");
    const next = action("下一页", () => load(nextOffset), "ghost");
    pager.append(range, previous, next);
    const close = action("取消", () => {
      closed = true;
      generation += 1;
      dialog.close();
      dialog.remove();
    }, "ghost");
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      close.click();
    });
    dialog.append(heading, hint, searchRow, status, list, pager, close);
    this.root.append(dialog);

    const pageSize = 12;
    let offset = 0;
    let nextOffset = pageSize;
    let hasMore = false;
    let generation = 0;
    let closed = false;
    let loading = false;
    const selectionKeys = new Map<number, string>();
    const imageID = (value: Obj): number => Number(value.id ?? value.resource_id ?? value.material_id ?? 0);
    const imageName = (value: Obj, id: number): string => String(value.name || value.file_name || value.filename || `图片素材 #${id}`);
    const imageThumb = (value: Obj, id: number): string => String(value.thumb_160_url || value.thumb_320_url || value.thumb_url || value.variant_url || (id > 0 ? `/api/admin/image-library/${id}/variants/thumb_160` : ""));
    const setLoading = (busy: boolean): void => {
      loading = busy;
      search.disabled = busy;
      previous.disabled = busy || offset <= 0;
      next.disabled = busy || !hasMore;
      list.querySelectorAll<HTMLButtonElement>("button[data-cover-image-id]").forEach((button) => { button.disabled = busy; });
    };
    const choose = async (item: Obj, button: HTMLButtonElement): Promise<void> => {
      if (closed || loading || boundBatch !== this.batchID || boundGeneration !== this.generation) {
        setNotice(status, "批次已切换，未保存封面选择。", "error");
        return;
      }
      const id = imageID(item);
      if (!Number.isSafeInteger(id) || id < 1 || item.enabled === false) {
        setNotice(status, "该图片已停用或编号无效，请重新选择启用图片。", "error");
        return;
      }
      const selectionKey = selectionKeys.get(id) || key(`excel-cover-select-${boundBatch}-${batch.version}-${id}`);
      selectionKeys.set(id, selectionKey);
      setLoading(true);
      button.textContent = "保存中…";
      try {
        await api(
          `${base}/${boundBatch}/cover?expected_version=${batch.version}`,
          "POST",
          { cover_image_id: id },
          selectionKey,
        );
        selectionKeys.delete(id);
        closed = true;
        dialog.close();
        dialog.remove();
        await this.loadSelected(false, false, true);
        this.tell("已选择启用图片作为统一封面；请重新核对预览。");
      } catch (error) {
        button.disabled = false;
        button.textContent = "选择";
        setLoading(false);
        setNotice(status, `${(error as Error).message}；可重试，本次封面选择尚未确认。`, "error");
      }
    };
    const draw = (items: Obj[]): void => {
      list.replaceChildren();
      items.filter((item) => item.enabled !== false).forEach((item) => {
        const id = imageID(item);
        if (!Number.isSafeInteger(id) || id < 1) return;
        const card = el("div");
        card.className = "xeb-cover-picker-item";
        const thumb = imageThumb(item, id);
        if (thumb) {
          const image = el("img") as HTMLImageElement;
          image.src = thumb;
          image.alt = imageName(item, id);
          card.append(image);
        }
        card.append(el("small", `${imageName(item, id)} · 素材 #${id}`));
        const chooseButton = action("选择", () => choose(item, chooseButton));
        chooseButton.dataset.coverImageId = String(id);
        card.append(chooseButton);
        list.append(card);
      });
      if (!list.children.length) list.append(el("small", "当前页没有可选择的启用图片。"));
    };
    async function load(nextPageOffset: number): Promise<void> {
      if (loading || closed) return;
      const requestGeneration = ++generation;
      offset = Math.max(0, nextPageOffset);
      nextOffset = offset + pageSize;
      hasMore = false;
      setLoading(true);
      setNotice(status, "正在读取启用图片…");
      try {
        const params = new URLSearchParams({ limit: String(pageSize), offset: String(offset), enabled_only: "true" });
        if (query.value.trim()) params.set("q", query.value.trim());
        const result = await api(`/api/admin/image-library?${params}`);
        if (closed || requestGeneration !== generation) return;
        const items = Array.isArray(result.items) ? result.items : Array.isArray(result.images) ? result.images : [];
        const providedNext = Number(result.next_offset);
        nextOffset = Number.isSafeInteger(providedNext) && providedNext > offset ? providedNext : offset + pageSize;
        hasMore = result.has_more === true || (result.has_more === undefined && items.length === pageSize);
        draw(items.map((value) => value as Obj));
        const total = Number(result.total);
        range.textContent = Number.isFinite(total) && total > 0
          ? `第 ${offset + 1}–${Math.min(offset + items.length, total)} 项，共 ${total} 项`
          : `第 ${offset + 1}–${offset + items.length} 项`;
        setNotice(status, "请选择一张启用图片；取消不会修改批次。");
      } catch (error) {
        if (closed || requestGeneration !== generation) return;
        list.replaceChildren();
        range.textContent = "";
        setNotice(status, (error as Error).message, "error");
      } finally {
        if (!closed && requestGeneration === generation) setLoading(false);
      }
    }
    dialog.showModal();
    void load(0);
  }
  private effects(parent: HTMLElement, batch: Obj): void {
    const report = el("div");
    report.dataset.excelReport = "";
    const receipts = el("div");
    receipts.dataset.excelReceiptsPage = "";
    parent.append(report, receipts);
    this.renderReport(batch);
    this.renderReceiptPage(batch);
  }
  private renderReport(batch: Obj): void {
    const target = this.root.querySelector<HTMLElement>("[data-excel-report]");
    if (!target || this.tab !== "effects" || Number(batch.id) !== this.batchID)
      return;
    const state = this.report;
    target.replaceChildren();
    const report = state.value;
    target.append(
      el(
        "p",
        `报告按每人实际成功发送时间计算；结果未知先进入对账，不会换 key 重发。${report?.updated_at ? ` 最近采集：${displayDateTime(report.updated_at)}` : ""}`,
      ),
    );
    if (state.loading) {
      const loading = el("div", "正在读取效果报告…");
      loading.className = "admin-state admin-state--loading";
      target.append(loading);
      return;
    }
    if (state.error) {
      target.append(
        notice(`效果报告暂不可统计：${state.error}`, "error"),
        action("重新读取效果报告", () => this.loadReport()),
      );
      return;
    }
    if (!report) return;
    const source =
      report.segment_source === "excel"
        ? "Excel"
        : report.segment_source === "legacy_snapshot"
          ? "旧审核快照"
          : "暂不可识别";
    target.append(
      el(
        "p",
        report.has_segments
          ? `分层来源：${source}；已按分层统计。`
          : `分层来源：${source}；分层数据暂不可用，只显示总体。`,
      ),
    );
    const hours = el("select") as HTMLSelectElement;
    hours.setAttribute("aria-label", "观察窗口");
    [12, 24, 48].forEach((value) => {
      const option = el("option", `${value} 小时累计`) as HTMLOptionElement;
      option.value = String(value);
      hours.append(option);
    });
    const grid = el("div");
    grid.className = "xeb-scroll";
    const draw = () => {
      const window = report.windows?.[hours.value] || {};
      const groups: Array<[string, Obj]> = [
        ["总体", window.overall || report.overall?.[hours.value] || {}],
        ...(Object.entries(window.groups || {}) as Array<[string, Obj]>),
      ];
      grid.replaceChildren(
        table(
          [
            "分组",
            "成功发送",
            "已满窗口",
            "观察中",
            "打开人数",
            "数据缺失",
            "打开率",
          ],
          groups.map(([label, stats]) => [
            label,
            String(stats.sent ?? 0),
            String(stats.matured ?? 0),
            String(stats.observing ?? 0),
            String(stats.opened ?? 0),
            String(stats.unavailable ?? 0),
            rate(stats.open_rate),
          ]),
        ),
      );
    };
    hours.onchange = draw;
    draw();
    const id = Number(batch.id);
    const download = el("a", "下载逐人报告");
    download.className = "admin-button admin-button--secondary";
    download.href = `${base}/${id}/report.csv`;
    download.download = `excel-batch-${id}-report.csv`;
    target.append(field("观察窗口", hours), grid, download);
  }
  private async loadReport(): Promise<void> {
    const batch = this.batch;
    const id = Number(batch?.id);
    const workspaceGeneration = this.generation;
    if (!batch || this.tab !== "effects" || !Number.isSafeInteger(id) || id < 1)
      return;
    const state = this.report;
    state.controller?.abort();
    const requestID = ++state.requestID;
    const controller = new AbortController();
    state.controller = controller;
    state.loading = true;
    state.error = "";
    this.renderReport(batch);
    try {
      const report = await api(
        `${base}/${id}/report`,
        "GET",
        undefined,
        undefined,
        controller.signal,
      );
      if (
        requestID !== state.requestID ||
        !this.currentWorkspace(workspaceGeneration, id, "effects")
      )
        return;
      state.value = report;
      state.loading = false;
      state.error = "";
      state.controller = null;
      this.renderReport(batch);
    } catch (error) {
      if (
        !aborted(error) &&
        requestID === state.requestID &&
        this.currentWorkspace(workspaceGeneration, id, "effects")
      ) {
        state.loading = false;
        state.error = (error as Error).message;
        state.controller = null;
        this.renderReport(batch);
      }
    }
  }
  private renderReceiptPage(batch: Obj): void {
    const target = this.root.querySelector<HTMLElement>("[data-excel-receipts-page]");
    if (!target || this.tab !== "effects" || Number(batch.id) !== this.batchID)
      return;
    const page = this.receiptPage;
    target.replaceChildren(el("h3", "逐人回执"));
    if (page.loading) {
      const loading = el("div", "正在读取当前回执页…");
      loading.className = "admin-state admin-state--loading";
      target.append(loading);
      return;
    }
    if (page.error) {
      target.append(
        notice(`逐人回执暂不可读取：${page.error}`, "error"),
        action("重新读取当前回执页", () =>
          page.retryFromMetadata
            ? this.loadSelected()
            : this.loadReceiptPage(
                page.retryCursor || page.cursors[page.index] || "",
              ),
        ),
      );
      return;
    }
    const scroll = el("div");
    scroll.className = "xeb-scroll";
    scroll.append(
      table(
        ["接收人", "发送员工", "状态", "实际发送时间", "原因"],
        page.items.map((item) => [
          String(item.unionid || item.recipient || ""),
          String(item.sender_userid || ""),
          deliveryLabel(item.delivery_state || item.state, "状态待核对"),
          displayDateTime(item.sent_at),
          deliveryReason(item.failure_reason),
        ]),
      ),
    );
    target.append(scroll);
    if (!page.items.length) target.append(el("p", "当前页没有逐人回执。"));
    target.append(
      this.pageNavigation(page, "receipts", (cursor) =>
        this.loadReceiptPage(cursor),
      ),
    );
  }
  private async loadReceiptPage(cursor: string): Promise<void> {
    const batch = this.batch;
    const id = Number(batch?.id);
    const expectedVersion = this.currentContentVersion(batch);
    const page = this.receiptPage;
    const workspaceGeneration = this.generation;
    const index = page.cursors.indexOf(cursor);
    if (
      !batch ||
      this.tab !== "effects" ||
      !Number.isSafeInteger(id) ||
      id < 1 ||
      !expectedVersion ||
      index < 0
    )
      return;
    page.controller?.abort();
    const requestID = ++page.requestID;
    const controller = new AbortController();
    page.controller = controller;
    page.loading = true;
    page.error = "";
    this.renderReceiptPage(batch);
    try {
      const result = await api(
        pagedPath(`${base}/${id}/receipts`, cursor),
        "GET",
        undefined,
        undefined,
        controller.signal,
      );
      if (
        requestID !== page.requestID ||
        !this.currentWorkspace(workspaceGeneration, id, "effects")
      )
        return;
      const returnedID = Number(result.batch_id);
      const returnedVersion = Number(result.content_version);
      if (returnedID !== id || returnedVersion !== expectedVersion) {
        await this.recoverVersionDrift(page, "回执", cursor);
        return;
      }
      const next = typeof result.next_cursor === "string" ? result.next_cursor : "";
      const knownNext = page.cursors[index + 1] || "";
      if (
        next &&
        (next === cursor ||
          page.cursors.slice(0, index + 1).includes(next) ||
          (knownNext && knownNext !== next))
      ) {
        this.paginationError(page, "分页游标重复，已停止读取以避免混合批次数据", cursor);
        this.renderReceiptPage(batch);
        return;
      }
      page.items = Array.isArray(result.items)
        ? result.items
        : Array.isArray(result.rows)
          ? result.rows
          : [];
      page.cursors = page.cursors.slice(0, index + 1);
      if (next) page.cursors.push(next);
      page.index = index;
      page.nextCursor = next;
      page.loading = false;
      page.error = "";
      page.retryFromMetadata = false;
      page.controller = null;
      this.renderReceiptPage(batch);
    } catch (error) {
      if (
        !aborted(error) &&
        requestID === page.requestID &&
        this.currentWorkspace(workspaceGeneration, id, "effects")
      ) {
        this.paginationError(page, (error as Error).message, cursor);
        this.renderReceiptPage(batch);
      }
    }
  }
  private openReadonlyExcelRow(
    row: Obj,
    title: string,
    versionKey: string,
    topLayer = false,
    readonlyNote?: string,
  ): void {
    openReadonlyContentPresentation({
      title,
      value: { content_text: String(row.text || "") },
      textRule: excelTextRule,
      presentationSupplements: excelCardSupplement(row, versionKey),
      overlayMount: topLayer ? "top-layer" : "body",
      readonlyNote,
    });
  }
  private rowDialog(batch: Obj, row: Obj): void {
    const boundBatch = Number(batch.id),
      boundGeneration = this.generation,
      dialog = el("dialog") as HTMLDialogElement,
      text = el("textarea") as HTMLTextAreaElement,
      path = el("input") as HTMLInputElement,
      title = el("input") as HTMLInputElement,
      segment = el("select") as HTMLSelectElement;
    text.value = String(row.text || "");
    text.readOnly = true;
    path.value = String(row.card?.path || "");
    title.value = String(row.card?.title || "");
    ["", "A", "B", "C", "D"].forEach((value) => {
      const option = el("option", value || "未分层") as HTMLOptionElement;
      option.value = value;
      option.selected = value === String(row.segment || "");
      segment.append(option);
    });
    dialog.append(el("h3", "修改发送内容"));
    [
      ["话术（使用编辑器编辑）", text],
      ["小程序 path", path],
      ["标题（可空，执行时明确失败）", title],
      ["分层", segment],
    ].forEach(([label, control]) =>
      dialog.append(field(String(label), control as HTMLElement)),
    );
    const feedback = notice();
    feedback.dataset.excelFeedback = "";
    const openComposer = action("编辑话术与预览", () => {
      openContentComposer({
        title: "编辑 Excel 行话术",
        value: { content_text: text.value },
        materialKinds: [],
        textRule: excelTextRule,
        overlayMount: "top-layer",
        presentationSupplements: excelCardSupplement(
          {
            ...row,
            card: { ...(row.card || {}), path: path.value, title: title.value },
          },
          `current:${boundBatch}:${String(batch.current_content_version || "current")}`,
        ),
        onConfirm: (result) => {
          if (
            !dialog.isConnected ||
            boundBatch !== this.batchID ||
            boundGeneration !== this.generation
          )
            throw new Error("批次已切换，未将编辑写入其他批次");
          text.value = result.package.content_text;
          setNotice(feedback, "已更新当前行草稿；请保存并重新审核以提交。", "success");
        },
      });
    }, "secondary");
    const openReadonly = action("查看已保存内容", () =>
      this.openReadonlyExcelRow(
        row,
        "已保存 Excel 行内容",
        `current:${boundBatch}:${String(batch.current_content_version || "current")}`,
        true,
      ), "ghost");
    const actions = el("div");
    actions.className = "admin-toolbar xeb-actions";
    actions.append(
      openComposer,
      openReadonly,
      action("保存并重新审核", async () => {
        if (boundBatch !== this.batchID || boundGeneration !== this.generation)
          throw new Error("批次已切换，未将编辑写入其他批次");
        await this.withBatchWrite(async () => {
          await api(
            `${base}/${boundBatch}/rows/${row.id}`,
            "PATCH",
            {
              expected_version: row.version,
              text: text.value,
              path: path.value.trim(),
              title: title.value.trim(),
              segment: segment.value,
              excluded: Boolean(row.excluded),
            },
            key(`excel-row-${boundBatch}-${row.id}`),
          );
          dialog.close();
          dialog.remove();
          await this.loadSelected(false, false, true);
        });
      }, "primary"),
      action("取消", () => {
        dialog.close();
        dialog.remove();
      }, "ghost"),
    );
    dialog.append(feedback, actions);
    this.root.append(dialog);
    dialog.showModal();
  }
  private importDialog(replacing?: Obj): void {
    const dialog = el("dialog") as HTMLDialogElement,
      file = el("input") as HTMLInputElement,
      fresh = el("input") as HTMLInputElement;
    file.type = "file";
    file.accept = ".xlsx";
    fresh.type = "checkbox";
    const strategyKey = this.strategyKey;
    const replacedID = Number(replacing?.id || 0);
    let uploadKey = key(
      replacing ? `excel-replace-${replacedID}` : `excel-import-${strategyKey}`,
    );
    file.onchange = () => {
      uploadKey = key(
        replacing
          ? `excel-replace-${replacedID}`
          : `excel-import-${strategyKey}`,
      );
    };
    fresh.onchange = () => {
      uploadKey = key(`excel-import-${strategyKey}`);
    };
    dialog.append(
      el("h3", replacing ? "替换 Excel 批次" : "新建发送批次"),
      el(
        "p",
        replacing
          ? "替换保留批次编号与已上传封面，旧内容版本只读保留，并重新审核。"
          : "文件在本地选择期间不会创建任何计划或批次。",
      ),
      field("Excel 文件", file),
    );
    if (!replacing) {
      const freshLabel = el("label");
      freshLabel.className = "admin-check-line";
      freshLabel.append(fresh, el("span", "同一文件也明确创建新的发送批次"));
      dialog.append(freshLabel);
    }
    const actions = el("div");
    actions.className = "admin-toolbar xeb-actions";
    actions.append(
      action(replacing ? "替换并重新审核" : "上传并开始审核", async () => {
        const source = file.files?.[0];
        if (!source) throw new Error("请选择 Excel 文件");
        const target = replacing
          ? `${base}/${replacing.id}/import?expected_version=${replacing.version}`
          : `${base}/strategies/${encodeURIComponent(strategyKey)}/imports${fresh.checked ? "?new=1" : ""}`;
        const result = await api(
          target,
          replacing ? "PUT" : "POST",
          source,
          uploadKey,
        );
        const selectedID = Number(
          (result.batch || result.plan || {}).id || replacedID,
        );
        dialog.close();
        dialog.remove();
        await this.openStrategy(strategyKey, false, selectedID, true);
      }, "primary"),
      action("取消", () => {
        dialog.close();
        dialog.remove();
      }, "ghost"),
    );
    dialog.append(actions);
    this.root.append(dialog);
    dialog.showModal();
  }
  private async versionDialog(id: number): Promise<void> {
    const boundGeneration = this.generation;
    if (id !== this.batchID) return;
    const dialog = el("dialog") as HTMLDialogElement;
    dialog.className = "xeb-history-dialog";
    dialog.append(el("h3", "历史上传内容版本"));
    let result: Obj;
    try {
      result = await api(`${base}/${id}/versions`);
    } catch (error) {
      if (boundGeneration !== this.generation || id !== this.batchID) return;
      throw error;
    }
    if (boundGeneration !== this.generation || id !== this.batchID) return;
    const items = Array.isArray(result.items) ? result.items : [];
    const viewer = el("div");
    viewer.dataset.excelHistoryPage = "";
    let selectedVersion = 0;
    let viewerGeneration = 0;
    let page = cursorPage();
    let closed = false;
    const renderViewer = (): void => {
      viewer.replaceChildren();
      if (!selectedVersion) return;
      viewer.append(el("h4", `历史版本 ${selectedVersion}（只读）`));
      if (page.loading) {
        const loading = el("div", "正在读取当前历史内容页…");
        loading.className = "admin-state admin-state--loading";
        viewer.append(loading);
        return;
      }
      if (page.error) {
        viewer.append(
          notice(`历史内容页读取失败：${page.error}`, "error"),
          action("重新读取当前历史页", () =>
            load(
              selectedVersion,
              page.retryCursor || page.cursors[page.index] || "",
            ),
          ),
        );
        return;
      }
      const view = el("div");
      view.className = "xeb-scroll";
      view.tabIndex = 0;
      view.setAttribute(
        "aria-label",
        "历史内容行字段；可横向滚动查看完整状态和版本追溯",
      );
      const tableHint = el("p", "表格可横向滚动查看完整状态和版本追溯。");
      tableHint.className = "xeb-table-hint";
      const historyTable = table(
          [
            "UnionID / 员工",
            "话术",
            "小程序卡片",
            "分层与行状态",
            "审核状态",
            "执行状态",
            "发送时间",
            "版本追溯",
            "操作",
          ],
          page.items.map((row) => [
            `${String(row.unionid || "")}\n${String(row.sender_userid || "")}`,
            String(row.text || ""),
            card(row),
            historicalSegment(row),
            historicalReview(row),
            historicalDelivery(row),
            `发送时间：${displayDateTime(row.sent_at)}`,
            historicalTrace(row, selectedVersion),
            action(
              "查看内容",
              () =>
                this.openReadonlyExcelRow(
                  row,
                  `历史版本 ${selectedVersion} 内容`,
                  `history:${id}:${selectedVersion}`,
                  true,
                  "此内容为所选历史版本的已保存内容。",
                ),
              "ghost",
            ),
          ]),
        );
      historyTable.classList.add("xeb-history-table");
      view.append(historyTable);
      viewer.append(tableHint, view);
      if (!page.items.length) viewer.append(el("p", "当前历史页没有内容行。"));
      viewer.append(
        this.pageNavigation(page, "history", (cursor) =>
          load(selectedVersion, cursor),
        ),
      );
    };
    const load = async (revision: number, cursor: string): Promise<void> => {
      const requestedPage = page;
      const requestedViewerGeneration = viewerGeneration;
      if (
        closed ||
        boundGeneration !== this.generation ||
        id !== this.batchID ||
        revision !== selectedVersion
      )
        return;
      const index = requestedPage.cursors.indexOf(cursor);
      if (index < 0) return;
      requestedPage.controller?.abort();
      const requestID = ++requestedPage.requestID;
      const controller = new AbortController();
      requestedPage.controller = controller;
      requestedPage.loading = true;
      requestedPage.error = "";
      renderViewer();
      try {
        const detail = await api(
          pagedPath(`${base}/${id}/versions/${revision}`, cursor),
          "GET",
          undefined,
          undefined,
          controller.signal,
        );
        if (
          closed ||
          requestedPage !== page ||
          requestedViewerGeneration !== viewerGeneration ||
          requestID !== requestedPage.requestID ||
          boundGeneration !== this.generation ||
          id !== this.batchID ||
          revision !== selectedVersion
        )
          return;
        if (
          Number(detail.batch_id) !== id ||
          this.historicalContentVersion(detail) !== revision ||
          detail.read_only !== true
        ) {
          this.paginationError(
            requestedPage,
            "历史版本响应无效，请重新读取后重试",
            cursor,
          );
          renderViewer();
          return;
        }
        const next = typeof detail.next_cursor === "string" ? detail.next_cursor : "";
        const knownNext = requestedPage.cursors[index + 1] || "";
        if (
          next &&
          (next === cursor ||
            requestedPage.cursors.slice(0, index + 1).includes(next) ||
            (knownNext && knownNext !== next))
        ) {
          this.paginationError(
            requestedPage,
            "分页游标重复，已停止读取以避免混合版本数据",
            cursor,
          );
          renderViewer();
          return;
        }
        requestedPage.items = Array.isArray(detail.rows) ? detail.rows : [];
        requestedPage.cursors = requestedPage.cursors.slice(0, index + 1);
        if (next) requestedPage.cursors.push(next);
        requestedPage.index = index;
        requestedPage.nextCursor = next;
        requestedPage.loading = false;
        requestedPage.error = "";
        requestedPage.controller = null;
        renderViewer();
      } catch (error) {
        if (
          !aborted(error) &&
          !closed &&
          requestedPage === page &&
          requestedViewerGeneration === viewerGeneration &&
          requestID === requestedPage.requestID &&
          boundGeneration === this.generation &&
          id === this.batchID &&
          revision === selectedVersion
        ) {
          this.paginationError(requestedPage, (error as Error).message, cursor);
          renderViewer();
        }
      }
    };
    dialog.append(
      table(
        ["版本", "封面素材", "创建时间", "操作"],
        items.map((item: Obj) => [
          String(item.content_version || item.version),
          Number(item.cover_image_id || 0) > 0
            ? `素材 #${item.cover_image_id}`
            : item.cover_digest
              ? "已上传内容"
              : "—",
          displayDateTime(item.created_at),
          action("只读查看", () => {
            const revision = Number(item.content_version || item.version);
            if (!Number.isSafeInteger(revision) || revision < 1)
              throw new Error("历史版本编号无效");
            page.controller?.abort();
            viewerGeneration += 1;
            page = cursorPage();
            selectedVersion = revision;
            void load(revision, "");
          }, "ghost"),
        ]),
      ),
    );
    dialog.append(viewer);
    const close = action("关闭", () => {
      closed = true;
      viewerGeneration += 1;
      page.controller?.abort();
      page.requestID += 1;
      dialog.close();
      dialog.remove();
    }, "ghost");
    dialog.addEventListener("cancel", (event) => {
      event.preventDefault();
      close.click();
    });
    dialog.append(close);
    this.root.append(dialog);
    dialog.showModal();
  }
}

export async function mountOperationExcelWorkspace(
  parent: HTMLElement,
): Promise<void> {
  const workspace = new Workspace(parent);
  await workspace.mount();
}
