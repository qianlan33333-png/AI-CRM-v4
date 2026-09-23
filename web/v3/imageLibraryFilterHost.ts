import { GroupManagement, bindMaterialGroup } from './materialGroupManagement';
// The image library is a V3-owned workspace. It reads the existing Media
// contract directly and leaves all mutations on the existing typed DTO and
// MaterialSaveHost path; no donor controller or generated template is mounted.
import { imagePageDto, saveImageItemDto } from "../src/api/admin";
import { deleteLegacyImage, getLegacyImage, getLegacyImageList } from "../src/api/generated/p4-media-compat/p4-media-compat";
import { ApiError, apiRequestOptions, unwrapGenerated } from "../src/api/transport";
import type { ImageItem } from "../src/shared/api/types";
import { mountMaterialLibraryTabs } from "./materialLibraryPresentation";
import { mountPageHeaderActions } from "./shared/ui/pageHeaderActions";
import { installCommittedTextSearch } from "./shared/ui/committedTextSearch";
import { renderMaterialThumbnail } from "./shared/ui/materialThumbnailPresentation";

import { MaterialGroupSidebar, materialGroupLayout, type MaterialGroupOption } from "./materialGroupSidebar";

installCommittedTextSearch();

const PAGE_SIZE = 20;
const MEDIA_CONTENT_CHANGED_EVENT = "aicrm:media-content-changed";

type ImageListResponse = {
  items?: unknown[];
  total?: unknown;
  limit?: unknown;
  offset?: unknown;
  has_more?: unknown;
};

// The generated image DTO intentionally keeps the editing contract small.
// The directory only reads these already-present Media fields to present a
// compact table; it neither changes their owner nor writes them back.
type ImageDirectoryItem = ImageItem & {
  fileName: string;
  width?: number;
  height?: number;
};

type Dialog =
  | { id: number; kind: "upload"; error: string; readbackPending?: boolean }
  | { id: number; kind: "edit"; item: ImageItem; error: string; readbackPending?: boolean }
  | undefined;

type LoadResult = "success" | "failed" | "aborted";

type DeleteIntent = {
  dialogID: number;
  itemID: string;
  key: string;
  readbackOffset: number;
  inFlight: boolean;
};

type DeleteVerification = "gone" | "present" | "unknown";

type ErrorContext = "read" | "save" | "delete";

function errorCode(error: ApiError): string {
  if (error.details === null || typeof error.details !== "object") return "";
  const value = (error.details as { code?: unknown }).code;
  return typeof value === "string" ? value.trim().toUpperCase() : "";
}

function errorText(error: unknown, context: ErrorContext): string {
  const action = context === "read" ? "读取图片素材" : context === "save" ? "保存图片素材" : "删除图片素材";
  if (error instanceof ApiError) {
    const code = errorCode(error);
    if (error.status === 401 || code === "UNAUTHORIZED") return "登录状态已失效，请重新登录后继续操作。";
    if (error.status === 403 || code === "FORBIDDEN") return "当前账号无权操作图片素材。";
    if (error.status === 404 || code === "NOT_FOUND") {
      return context === "read"
        ? "图片素材不存在或已被删除，请刷新列表。"
        : context === "save"
          ? "图片素材不存在或已被删除，请刷新列表后再保存。"
          : "图片素材不存在或已被删除，请刷新列表确认。";
    }
    if (error.status === 409 || code === "CONFLICT") {
      return context === "delete"
        ? "图片素材状态已变化或仍被使用，请刷新列表后再删除。"
        : `${action}时发现内容已变化，请刷新列表后重试。`;
    }
    if (error.status === 400 || error.status === 405 || error.status === 422 || code === "MALFORMED_REQUEST" || code === "VALIDATION_FAILED") {
      return context === "read" ? "读取条件无效，请刷新页面后重试。" : `${action}的内容不符合要求，请检查后重试。`;
    }
    if (error.status === 429) return `${action}过于频繁，请稍后重试。`;
    if (error.status >= 500 || code === "DEPENDENCY_UNAVAILABLE" || code === "UNAVAILABLE") return `${action}服务暂不可用，请稍后重试。`;
    if (error.kind === "network" || error.status === 0) return `${action}网络暂不可用，请检查网络后重试。`;
  }
  // A few local validation messages are deliberate, user-actionable copy.
  // All other Error messages (including browser and server implementation
  // detail) stay out of the UI.
  if (error instanceof Error && error.message === "请选择真实图片文件后再上传") return error.message;
  return `${action}网络暂不可用，请检查网络后重试。`;
}

function button(label: string, kind: "primary" | "secondary" | "danger" = "secondary"): HTMLButtonElement {
  const node = document.createElement("button");
  node.type = "button";
  node.className = `admin-button admin-button--${kind}`;
  node.textContent = label;
  return node;
}

function field(label: string, input: HTMLInputElement | HTMLSelectElement): HTMLLabelElement {
  const wrap = document.createElement("label");
  wrap.className = "admin-field";
  const title = document.createElement("span");
  title.textContent = label;
  wrap.append(title, input);
  return wrap;
}

function chinaTime(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return "时间暂不可用";
  const parts = new Intl.DateTimeFormat("zh-CN", {
    timeZone: "Asia/Shanghai",
    year: "numeric", month: "2-digit", day: "2-digit",
    hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23",
  }).formatToParts(parsed);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((entry) => entry.type === type)?.value || "00";
  return `${part("year")}-${part("month")}-${part("day")} ${part("hour")}:${part("minute")}:${part("second")}`;
}

function formatFileSize(value: string): string {
  const bytes = Number(value);
  if (!Number.isFinite(bytes) || bytes < 0) return value || "—";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${Math.ceil(bytes / 1024)} KB`;
  return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function imageDimensions(item: ImageDirectoryItem): string {
  return Number.isSafeInteger(item.width) && Number.isSafeInteger(item.height) && item.width! > 0 && item.height! > 0
    ? `${item.width} × ${item.height}`
    : "—";
}

function imageDirectoryDto(value: unknown): ImageDirectoryItem {
  const item = imagePageDto(value);
  const source = value !== null && typeof value === 'object' ? value as Record<string, unknown> : {};
  const fileName = typeof source.file_name === 'string'
    ? source.file_name
    : typeof source.filename === 'string' ? source.filename : item.name;
  const dimension = (field: 'width' | 'height'): number | undefined => {
    const number = Number(source[field]);
    return Number.isSafeInteger(number) && number > 0 ? number : undefined;
  };
  const width = dimension('width');
  const height = dimension('height');
  return { ...item, fileName, ...(width ? { width } : {}), ...(height ? { height } : {}) };
}

function imageDeleteMutationKey(): string {
  return `image-delete-${globalThis.crypto?.randomUUID?.() || `${Date.now()}-${Math.random().toString(16).slice(2)}`}`;
}

class ImageLibraryHost {
  private groupManager!: GroupManagement;
  private readonly stage: HTMLElement;
  private readonly scroll: HTMLElement;
  private readonly workspace: HTMLElement;
  private readonly toolbarNode: HTMLElement;
  private readonly stateNode: HTMLElement;
  private readonly cardsNode: HTMLElement;
  private readonly paginationNode: HTMLElement;
  private readonly dialogLayer: HTMLElement;
  private queryInput!: HTMLInputElement;
  private includeInactiveInput!: HTMLInputElement;
  private items: ImageDirectoryItem[] = [];
  private query = "";
  private group = "";
  private groupSidebar!: MaterialGroupSidebar;
  private groupOptions: MaterialGroupOption[] = [{ value: "", label: "全部分组" }, { value: "__ungrouped__", label: "未分组" }];
  private includeInactive = false;
  // Offset always identifies the last successfully-read page. A requested
  // page stays separate until its response validates, so a failed next page
  // cannot make the visible page controls skip ahead.
  private offset = 0;
  private failedOffset?: number;
  private total = 0;
  private loading = false;
  private searchPending = false;
  private error = "";
  private hasSuccessfulRead = false;
  private dialog: Dialog;
  private readGeneration = 0;
  private readAbort?: AbortController;
  private deleteIntent?: DeleteIntent;
  private nextDialogID = 0;

  constructor(stage: HTMLElement) {
    this.stage = stage;
    this.scroll = document.createElement("div");
    // MaterialSaveHost discovers this stable scroll region and mounts its
    // refresh/credential panel ahead of the workspace. Host re-renders never
    // replace this region, so that existing panel keeps its own lifecycle.
    this.scroll.dataset.imageLibraryScrollRegion = "true";
    this.scroll.style.cssText = "flex:1 1 0;min-height:0;overflow:auto;padding:16px 20px;display:grid;grid-template-columns:minmax(0,1fr);gap:12px;align-content:start";
    this.workspace = document.createElement("section");
    this.workspace.dataset.imageLibraryWorkspace = "true";
    this.workspace.style.cssText = "display:grid;grid-template-columns:minmax(0,1fr);gap:12px;align-content:start";
    const initial = new URL(location.href).searchParams;
    if (initial.has('material_group')) this.group = initial.get('material_group') ? 'category:' + initial.get('material_group') : '__ungrouped__';
    this.groupSidebar = new MaterialGroupSidebar(value => {
      this.group = value; this.groupSidebar.select(value); this.updateGroupURL(); this.groupManager.clear(); void this.load(0);
    });
    this.groupSidebar.render(this.groupOptions, this.group);
    this.toolbarNode = this.toolbar();
    this.stateNode = document.createElement("section");
    this.cardsNode = document.createElement("section");
    this.cardsNode.dataset.imageLibraryCards = "true";
    this.cardsNode.style.cssText = "overflow-x:auto;background:#fff;border:1px solid #DEE0E3;border-radius:8px";
    this.paginationNode = document.createElement("section");
    this.dialogLayer = document.createElement("section");
    this.dialogLayer.dataset.imageLibraryDialogLayer = "true";
    this.workspace.append(this.toolbarNode, this.stateNode, this.cardsNode, this.paginationNode);
 this.groupManager=new GroupManagement("image",this.workspace,this.toolbarNode);
    const layout = materialGroupLayout();
    layout.style.padding = '0'; layout.style.overflow = 'visible';
    layout.append(this.groupSidebar.element, this.workspace);
    this.scroll.append(layout);
    this.stage.replaceChildren(this.scroll, this.dialogLayer);
    if (this.stage.dataset.materialLibraryWorkspace === "true") {
      mountMaterialLibraryTabs(this.stage, "images");
    }
  }

  start(): void {
    if (this.stage.dataset.materialLibraryWorkspace === "true") {
      mountPageHeaderActions("image-library", [{
        label: "上传图片", variant: "primary", onClick: () => this.openDialog(this.newUploadDialog()),
      }]);
    }
    this.render();
    void this.loadGroups();
    void this.load(0);
  }

  private commitSearch(value: string): void {
    this.query = value;
    // Abort and invalidate only after an explicit committed search. The input
    // itself remains a browser-owned draft so IME composition never starts a
    // read or replaces the focused control.
    this.readAbort?.abort();
    this.readAbort = undefined;
    this.readGeneration += 1;
    this.loading = false;
    this.searchPending = true;
    this.failedOffset = undefined;
    this.error = "";
    void this.load(0);
    this.render();
  }

  private async load(offset = this.offset): Promise<LoadResult> {
    this.groupManager.clear();
    this.readAbort?.abort();
    const abort = new AbortController();
    this.readAbort = abort;
    const generation = ++this.readGeneration;
    this.loading = true;
    this.searchPending = false;
    this.failedOffset = undefined;
    this.error = "";
    this.render();
    try {
      const query = this.query.trim();
      const includeInactive = this.includeInactive;
      const payload = unwrapGenerated(await getLegacyImageList({
        limit: String(PAGE_SIZE),
        offset: String(offset),
        enabled_only: includeInactive ? "false" : "true",
        ...(query ? { q: query } : {}),
        ...(this.group === "__ungrouped__" ? { only_ungrouped: "true" } : this.group ? { category: this.group.slice("category:".length) } : {}),
      }, apiRequestOptions({ signal: abort.signal }))) as ImageListResponse;
      if (generation !== this.readGeneration) return "aborted";
      const rawItems = Array.isArray(payload.items) ? payload.items : [];
      const total = Number(payload.total);
      const responseLimit = Number(payload.limit);
      const responseOffset = Number(payload.offset);
      if (!Number.isSafeInteger(total) || total < 0 || responseLimit !== PAGE_SIZE || responseOffset !== offset || rawItems.length > PAGE_SIZE) {
        throw new Error("图片素材分页响应无效");
      }
      this.items = rawItems.map(imageDirectoryDto);
      this.total = total;
      this.offset = offset;
      this.hasSuccessfulRead = true;
      this.error = "";
      this.failedOffset = undefined;
      return "success";
    } catch (error) {
      if (generation !== this.readGeneration || (error instanceof DOMException && error.name === "AbortError")) return "aborted";
      this.error = errorText(error, "read");
      this.failedOffset = offset;
      return "failed";
    } finally {
      if (generation === this.readGeneration) {
        this.loading = false;
        this.render();
      }
    }
  }

  private render(): void {
    // Toolbar and modal are long-lived DOM. Only the data-bearing regions are
    // updated after a read, preserving focus, selected files, and in-progress
    // edits while a request completes.
    this.renderState();
    this.renderCards();
    this.renderPagination();
  }

  private async loadGroups(): Promise<void> {
    try {
      const groups=await this.groupManager.load();
 const result={categories:groups.filter(g=>g.id>0).map(g=>g.name)};
      if (!Array.isArray(result.categories)) return;
      this.groupOptions = [{ value: "", label: "全部分组" }, { value: "__ungrouped__", label: "未分组" }, ...result.categories.map(value => ({ value: "category:" + value, label: value }))];
      if (this.group && !this.groupOptions.some(x => x.value === this.group)) this.groupOptions.push({ value: this.group, label: this.group.slice('category:'.length) });
      this.groupOptions=this.groupOptions.map(o=>({...o,count:o.value===''?groups.reduce((n,g)=>n+g.count,0):groups.find(g=>g.name===(o.value==='__ungrouped__'?'':o.value.slice(9)))?.count||0}));
 this.groupSidebar.render(this.groupOptions, this.group); this.groupSidebar.message('');
    } catch { this.groupSidebar.message('分组加载失败，请刷新重试。'); }
  }

  private updateGroupURL(): void {
    const url = new URL(location.href);
    url.searchParams.set("tab", "images");
    if (this.group) url.searchParams.set('material_group', this.group === '__ungrouped__' ? '' : this.group.slice('category:'.length));
    else url.searchParams.delete('material_group');
    history.replaceState(history.state, '', url.href);
  }

  private toolbar(): HTMLElement {
    const toolbar = document.createElement("section");
    toolbar.className = "admin-filter-bar admin-toolbar";
    toolbar.style.cssText = "background:#fff;border:1px solid #DEE0E3;border-radius:8px;padding:12px 16px;display:flex;align-items:center;gap:12px;flex-wrap:wrap";
    const input = document.createElement("input");
    input.type = "search";
    input.placeholder = "搜索素材名或标签";
    input.dataset.imageLibraryQuery = "true";
    input.setAttribute("aria-label", "搜索图片素材");
    input.style.cssText = "flex:1 1 240px;min-width:160px;max-width:420px";
    input.addEventListener("input", () => this.commitSearch(input.value));
    this.queryInput = input;
    const refresh = button("刷新");
    refresh.addEventListener("click", () => { void this.loadGroups(); void this.load(this.offset); });
    const includeLabel = document.createElement("label");
    includeLabel.style.cssText = "display:flex;align-items:center;gap:6px;font-size:13px;color:#646A73;margin-left:auto;cursor:pointer";
    const include = document.createElement("input");
    include.type = "checkbox";
    include.dataset.imageLibraryIncludeInactive = "true";
    include.setAttribute("aria-label", "包含已停用图片");
    include.addEventListener("change", () => { this.includeInactive = include.checked; void this.load(0); });
    this.includeInactiveInput = include;
    includeLabel.append(include, document.createTextNode("含已停用"));
    const reset = button("重置");
    reset.dataset.imageLibraryReset = "true";
    reset.addEventListener("click", () => {
      this.query = "";
      this.group = ""; this.groupSidebar.select(""); this.updateGroupURL();
      this.includeInactive = false;
      this.queryInput.value = "";
      this.includeInactiveInput.checked = false;
      void this.load(0);
    });
    toolbar.append(input, includeLabel, reset, refresh);
    return toolbar;
  }

  private renderState(): void {
    this.stateNode.replaceChildren();
    const line = document.createElement("p");
    line.dataset.imageLibraryFilterFeedback = "true";
    line.style.cssText = "margin:0;color:#646A73;font-size:13px;line-height:20px;min-height:20px";
    if (this.error) {
      line.setAttribute("role", "alert");
      line.style.color = "#D83931";
      line.textContent = this.hasSuccessfulRead
        ? `图片素材读取失败，仍显示上一次成功结果：${this.error}`
        : `图片素材读取失败：${this.error}`;
      this.stateNode.append(line);
      if (this.failedOffset !== undefined) {
        const retry = button("重试读取");
        retry.dataset.imageLibraryRetry = "true";
        retry.style.marginLeft = "8px";
        retry.addEventListener("click", () => void this.load(this.failedOffset));
        this.stateNode.append(retry);
      }
      return;
    } else if (this.loading) {
      line.setAttribute("role", "status");
      line.textContent = "正在读取图片素材…";
    } else if (this.searchPending) {
      line.setAttribute("role", "status");
      line.textContent = "等待输入完成后搜索…";
    } else if (this.hasSuccessfulRead) {
      line.setAttribute("role", "status");
      line.textContent = `显示 ${this.items.length ? this.offset + 1 : 0}-${this.offset + this.items.length} / ${this.total}`;
    }
    this.stateNode.append(line);
  }

  private renderCards(): void {
    this.cardsNode.replaceChildren();
    if (!this.loading && !this.error && this.hasSuccessfulRead && this.items.length === 0) {
      const empty = document.createElement("p");
      empty.dataset.imageLibraryEmpty = "true";
      empty.textContent = "没有符合当前筛选条件的图片素材。";
      empty.style.cssText = "margin:0;padding:24px;color:#646A73;text-align:center";
      this.cardsNode.append(empty);
      return;
    }
    const table = document.createElement("table");
    table.dataset.imageLibraryDirectory = "true";
    table.style.cssText = "width:100%;border-collapse:collapse;table-layout:fixed";
    const header = document.createElement("thead");
    const heading = document.createElement("tr");
    for (const [label, width] of [["图片 / 名称", "40%"], ["所属分组", "20%"], ["大小", "12%"], ["上传时间", "19%"], ["状态", "9%"], ["操作", "8%"]] as const) {
      const cell = document.createElement("th");
      cell.textContent = label;
      cell.style.cssText = `padding:10px 12px;width:${width};font-size:12px;font-weight:500;color:#8F959E;text-align:left;background:#FAFAFB;border-bottom:1px solid #DEE0E3;white-space:nowrap`;
      heading.append(cell);
    }
    header.append(heading);
    const body = document.createElement("tbody");
    for (const item of this.items) body.append(this.row(item));
    table.append(header, body);
    this.cardsNode.append(table);
  }

  private row(item: ImageDirectoryItem): HTMLTableRowElement {
    const row = document.createElement("tr");
    row.dataset.imageLibraryRow = item.resourceId || "";
    row.style.cssText = item.enabled ? "" : "opacity:.64";
    const cell = () => {
      const node = document.createElement("td");
      node.style.cssText = "padding:10px 12px;border-bottom:1px solid #F2F3F5;font-size:13px;text-align:left;vertical-align:middle";
      return node;
    };
    const identity = cell();
    const identityWrap = document.createElement("div");
    identityWrap.style.cssText = "display:flex;align-items:center;gap:10px;min-width:0";
    const preview = document.createElement("button");
    preview.type = "button";
    preview.dataset.imageLibraryThumbnail = "true";
    preview.setAttribute("aria-label", `查看图片素材：${item.name}`);
    preview.style.cssText = "display:grid;place-items:center;position:relative;width:64px;height:48px;flex:none;padding:0;border:0;border-radius:5px;background:#EFF4FF;overflow:hidden;cursor:pointer;color:#646A73;font:inherit";
    const thumbnail = renderMaterialThumbnail(preview, {
      url: item.thumbnailUrl,
      alt: item.name,
      loadingLabel: "加载图片…",
      unavailableLabel: "预览不可用",
      noURLLabel: "暂无预览",
      imageDisplay: "block",
      loadingDisplay: "grid",
      fallbackDisplay: "grid",
    });
    for (const status of [thumbnail.loading, thumbnail.fallback]) {
      status.style.position = "absolute";
      status.style.inset = "0";
      status.style.placeItems = "center";
      status.style.padding = "4px";
      status.style.fontSize = "10px";
      status.style.lineHeight = "14px";
      status.style.textAlign = "center";
      status.style.overflowWrap = "anywhere";
      status.style.background = "#EFF4FF";
    }
    preview.title = "图片预览暂不可用时仍可编辑素材";
    if (thumbnail.image) {
      thumbnail.image.style.width = "100%";
      thumbnail.image.style.height = "48px";
      thumbnail.image.style.objectFit = "contain";
      thumbnail.image.style.background = "#F7F9FC";
    }
    preview.addEventListener("click", () => this.openDialog(this.newEditDialog(item)));
    const labels = document.createElement("div");
    labels.style.cssText = "min-width:0;display:grid;gap:3px";
    const name = document.createElement("strong");
    name.textContent = item.name;
    name.style.cssText = "display:block;font-size:13px;font-weight:500;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;cursor:pointer;color:#1F2329";
    name.addEventListener("click", () => this.openDialog(this.newEditDialog(item)));
    const details = document.createElement("span");
    const groupValue = [item.tag, item.tags].filter(Boolean).join(" · ");
    details.textContent = [imageDimensions(item), item.fileName, groupValue].filter(Boolean).join(" · ");
    details.style.cssText = "font-size:12px;color:#8F959E;white-space:nowrap;overflow:hidden;text-overflow:ellipsis";
    labels.append(name, details);
    identityWrap.append(preview, labels);
    identity.append(identityWrap);
    const size = cell(); size.textContent = formatFileSize(item.size); size.style.color = "#646A73";
    const time = cell(); time.textContent = chinaTime(item.uploadedAt); time.style.cssText += ";color:#646A73;overflow-wrap:anywhere;line-height:18px";
    const state = cell();
    const chip = document.createElement("span");
    chip.className = "admin-chip";
    chip.textContent = item.enabled ? "已启用" : "已停用";
    chip.style.cssText = `display:inline-flex;min-height:20px;padding:0 7px;align-items:center;border-radius:4px;font-size:11px;${item.enabled ? "color:#237804;background:#F6FFED" : "color:#8F959E;background:#F2F3F5"}`;
    state.append(chip);
    const actions = cell();
    actions.style.textAlign = "right";
    const edit = button("编辑");
    edit.style.cssText = "height:24px;padding:0 8px;border:0;border-radius:4px;background:transparent;color:#245BDB;font-size:12px;cursor:pointer";
    edit.addEventListener("click", () => this.openDialog(this.newEditDialog(item)));
    actions.append(edit);
    row.append(identity, size, time, state, actions);
    const groupCell=cell();row.insertBefore(groupCell,row.children[1]||null);
    const numericID=Number(String(item.resourceId||'').replace(/^image:/,''));if(numericID>0)bindMaterialGroup(row,numericID,groupCell,identityWrap);
    return row;
  }

  private renderPagination(): void {
    this.paginationNode.replaceChildren();
    const nav = document.createElement("nav");
    nav.dataset.imageLibraryPagination = "true";
    nav.setAttribute("aria-label", "图片素材分页");
    nav.style.cssText = "display:flex;align-items:center;justify-content:flex-end;gap:8px;flex-wrap:wrap";
    const previous = button("上一页");
    previous.disabled = this.loading || this.offset === 0;
    previous.addEventListener("click", () => void this.load(Math.max(0, this.offset - PAGE_SIZE)));
    const next = button("下一页");
    next.disabled = this.loading || this.offset + this.items.length >= this.total;
    next.addEventListener("click", () => void this.load(this.offset + PAGE_SIZE));
    nav.append(previous, next);
    this.paginationNode.append(nav);
  }

  private newUploadDialog(): Exclude<Dialog, undefined> {
    return { id: ++this.nextDialogID, kind: "upload", error: "" };
  }

  private newEditDialog(item: ImageItem): Exclude<Dialog, undefined> {
    return { id: ++this.nextDialogID, kind: "edit", item, error: "" };
  }

  private dialogMatches(dialogID: number): boolean {
    return this.dialog?.id === dialogID;
  }

  private openDialog(dialog: Exclude<Dialog, undefined>): void {
    this.dialog = dialog;
    this.dialogLayer.replaceChildren(this.modal(dialog));
    const intent = this.deleteIntent;
    if (dialog.kind !== "edit" || !intent || intent.itemID !== dialog.item.resourceId) return;
    // Reopening the same resource continues the original delete intent and
    // its idempotency key. A result from a different resource must never
    // mutate this dialog.
    intent.dialogID = dialog.id;
    this.setDeleteBusy(true, dialog.id);
    this.setDialogError("删除结果暂不可确认。请核对删除结果，勿重复删除。", dialog.id);
    this.setDialogAction("重新核对删除结果", "delete-verify", dialog.id);
  }

  private closeDialog(expectedDialogID?: number): void {
    if (expectedDialogID !== undefined && !this.dialogMatches(expectedDialogID)) return;
    if (expectedDialogID !== undefined && this.deleteIntent?.dialogID === expectedDialogID) {
      // The exact-resource check is the only safe way to settle an
      // outcome-unknown delete. Keeping this dialog open leaves that action
      // reachable even when a later list refresh no longer includes the card.
      this.setDialogError("删除结果暂不可确认。请先点击“重新核对删除结果”，确认后再关闭。", expectedDialogID);
      this.setDialogAction("重新核对删除结果", "delete-verify", expectedDialogID);
      return;
    }
    this.dialog = undefined;
    this.dialogLayer.replaceChildren();
  }

  private modal(dialog: Exclude<Dialog, undefined>): HTMLElement {
    const overlay = document.createElement("section");
    overlay.setAttribute("role", "dialog");
    overlay.setAttribute("aria-modal", "true");
    overlay.style.cssText = "position:fixed;inset:0;background:rgba(15,23,42,.34);z-index:80;display:flex;align-items:center;justify-content:center;overflow:auto;padding:16px;box-sizing:border-box";
    const panel = document.createElement("form");
    panel.dataset.imageLibraryDialog = "true";
    panel.className = "admin-modal";
    panel.style.cssText = "width:min(520px,100%);max-height:calc(100dvh - 32px);margin:auto;padding:0;display:flex;flex-direction:column;box-sizing:border-box;overflow:hidden";
    panel.addEventListener("submit", (event) => {
      event.preventDefault();
      panel.querySelector<HTMLButtonElement>("[data-image-library-dialog-submit]")?.click();
    });
    const heading = document.createElement("header");
    heading.style.cssText = "display:flex;align-items:center;justify-content:space-between;padding:14px 18px;border-bottom:1px solid #EFF0F1";
    const title = document.createElement("strong");
    title.textContent = dialog.kind === "upload" ? "上传图片" : "编辑图片素材";
    const close = button("×");
    close.setAttribute("aria-label", "关闭弹窗");
    close.style.cssText = "width:28px;height:28px;padding:0;border:0;border-radius:6px;background:#F2F3F5;color:#646A73;font-size:14px";
    close.addEventListener("click", () => this.closeDialog(dialog.id));
    heading.append(title, close);
    const fields = document.createElement("div");
    fields.dataset.imageLibraryDialogFields = "true";
    fields.className = "admin-form-grid admin-form-grid--stacked";
    fields.style.cssText = "padding:18px;overflow:auto;flex:1 1 auto;align-content:start";
    const category=this.groupManager.select(dialog.kind==='edit'?(this.groupManager.groups.find(g=>g.name===dialog.item.tag)?.id||null):(this.groupManager.current()?.id||null));
 category.id="fImgCategory";category.dataset.materialGroupSelect="true";
 if(dialog.kind==='edit'){const version=this.groupManager.versionFor(Number(dialog.item.resourceId));if(version)category.dataset.expectedVersion=String(version);}
 fields.append(field("所属分组",category));
    if (dialog.kind === "upload") {
      const file = document.createElement("input");
      file.id = "fImgUpFile";
      file.type = "file";
      file.accept = "image/png,image/jpeg,image/gif";
      file.required = true;
      fields.append(field("图片文件", file));
      const name = document.createElement("input");
      name.id = "fImgUpName";
      name.placeholder = "留空则使用文件名";
      fields.append(field("素材名称", name));
      const tags = document.createElement("input");
      tags.id = "fImgUpTags";
      tags.placeholder = "如：直播,预告";
      fields.append(field("标签（逗号分隔）", tags));
    } else {
      const name = document.createElement("input");
      name.id = "fImgName";
      name.required = true;
      name.value = dialog.item.name;
      fields.append(field("素材名称", name));
      const description = document.createElement("input");
      description.id = "fImgDesc";
      description.value = dialog.item.desc;
      description.placeholder = "用途说明，便于同事选择";
      fields.append(field("描述", description));
      const tags = document.createElement("input");
      tags.id = "fImgTags";
      tags.value = dialog.item.tags;
      fields.append(field("标签（逗号分隔）", tags));
      const enabled = document.createElement("input");
      enabled.id = "fImgEnabled";
      enabled.type = "checkbox";
      enabled.checked = dialog.item.enabled;
      const enabledLabel = document.createElement("label");
      enabledLabel.className = "admin-checkbox";
      enabledLabel.append(enabled, document.createTextNode("启用此图片素材"));
      fields.append(enabledLabel);
    }
    if (dialog.error) {
      const error = document.createElement("p");
      error.dataset.imageLibraryMutationFeedback = "true";
      error.setAttribute("role", "alert");
      error.textContent = dialog.error;
      error.style.cssText = "margin:0;color:#D83931;font-size:13px;line-height:20px";
      fields.append(error);
    }
    const footer = document.createElement("footer");
    footer.style.cssText = "display:flex;align-items:center;justify-content:space-between;gap:8px;padding:12px 18px;border-top:1px solid #EFF0F1;background:#FAFBFC";
    const left = document.createElement("div");
    if (dialog.kind === "edit") {
      const remove = button("删除", "danger");
      remove.dataset.imageLibraryDelete = "true";
      remove.addEventListener("click", () => void this.remove(dialog.item, dialog.id));
      left.append(remove);
    }
    const right = document.createElement("div");
    right.style.cssText = "display:flex;gap:8px";
    const cancel = button("取消");
    cancel.addEventListener("click", () => this.closeDialog(dialog.id));
    const submit = button(dialog.kind === "upload" ? "上传" : "保存", "primary");
    submit.dataset.imageLibraryDialogSubmit = "true";
    // MaterialSaveHost deliberately marks this button busy in capture phase.
    // Bind the write on click (like the frozen donor) so that busy marking does
    // not suppress native form submission before our handler can run.
    submit.addEventListener("click", () => {
      const action = submit.dataset.imageLibraryDialogAction;
      if (action === "delete-verify") {
        void this.verifyDelete(dialog.id);
        return;
      }
      if (action === "delete-retry") {
        void this.retryDelete(dialog.id);
        return;
      }
      const current = this.dialog;
      if (!current || current.id !== dialog.id) return;
      if (current.readbackPending) {
        void this.retryReadback(current.id);
      } else if (current.kind === "upload") {
        void this.submitUpload(panel);
      } else {
        void this.submitEdit(panel, current.item);
      }
    });
    right.append(cancel, submit);
    footer.append(left, right);
    panel.append(heading, fields, footer);
    overlay.append(panel);
    return overlay;
  }

  private formValue(form: HTMLFormElement, id: string): string {
    return (form.querySelector<HTMLInputElement>(`#${id}`)?.value || "").trim();
  }

  private async submitUpload(form: HTMLFormElement): Promise<void> {
    const dialogID = this.dialog?.kind === "upload" ? this.dialog.id : undefined;
    if (dialogID === undefined) return;
    const file = form.querySelector<HTMLInputElement>("#fImgUpFile")?.files?.[0];
    if (!file) return this.setDialogError("请选择真实图片文件后再上传", dialogID);
    const name = this.formValue(form, "fImgUpName") || file.name;
    try {
      await saveImageItemDto(null, {
        name,
        file,
        tags: this.formValue(form, "fImgUpTags"),
        desc: "",
        size: String(file.size),
        tag: this.formValue(form, "fImgCategory"),
        tone: "gray",
        bg: "#EFF4FF",
        enabled: true,
        uploadedAt: "刚刚",
      });
      await this.readbackAfterMutation(dialogID);
      void this.loadGroups();
    } catch (error) {
      this.setDialogError(errorText(error, "save"), dialogID);
    }
  }

  private async submitEdit(form: HTMLFormElement, item: ImageItem): Promise<void> {
    const dialogID = this.dialog?.kind === "edit" && this.dialog.item.resourceId === item.resourceId
      ? this.dialog.id
      : undefined;
    if (dialogID === undefined) return;
    const name = this.formValue(form, "fImgName");
    if (!name) return this.setDialogError("请输入素材名称", dialogID);
    try {
      await saveImageItemDto(item.name, {
        ...item,
        resourceId: item.resourceId,
        name,
        desc: this.formValue(form, "fImgDesc"),
        tags: this.formValue(form, "fImgTags"),
        tag: this.formValue(form, "fImgCategory"),
        enabled: Boolean(form.querySelector<HTMLInputElement>("#fImgEnabled")?.checked),
      });
      await this.readbackAfterMutation(dialogID);
      void this.loadGroups();
    } catch (error) {
      this.setDialogError(errorText(error, "save"), dialogID);
    }
  }

  private async remove(item: ImageItem, dialogID: number): Promise<void> {
    if (this.deleteIntent?.inFlight) return;
    if (!this.dialogMatches(dialogID)) return;
    if (this.deleteIntent && this.deleteIntent.itemID !== item.resourceId) {
      // A dismissed outcome-unknown deletion keeps its original key until the
      // exact resource is reconciled. Starting another deletion would replace
      // that key in this in-memory host and make the earlier outcome unsafe to
      // retry, so require that confirmation first.
      this.setDialogError("另一张图片素材的删除结果暂不可确认。请先重新打开该素材核对，再删除其他图片。", dialogID);
      return;
    }
    if (!window.confirm(`确认删除「${item.name}」？删除后不可恢复。`)) return;
    if (!item.resourceId) {
      this.setDialogError("图片素材标识无效，请重新读取列表后再删除。", dialogID);
      return;
    }
    const readbackOffset = this.items.length === 1 && this.offset > 0 ? Math.max(0, this.offset - PAGE_SIZE) : this.offset;
    const existing = this.deleteIntent?.itemID === item.resourceId ? this.deleteIntent : undefined;
    const intent: DeleteIntent = existing || {
      itemID: item.resourceId,
      key: imageDeleteMutationKey(),
      readbackOffset,
      inFlight: false,
      dialogID,
    };
    intent.dialogID = dialogID;
    this.deleteIntent = intent;
    await this.dispatchDelete(intent);
  }

  private async retryDelete(dialogID: number): Promise<void> {
    const intent = this.deleteIntent;
    const dialog = this.dialog;
    if (!intent || intent.inFlight || intent.dialogID !== dialogID || !dialog || dialog.id !== dialogID || dialog.kind !== "edit" || dialog.item.resourceId !== intent.itemID) return;
    await this.dispatchDelete(intent);
  }

  private async dispatchDelete(intent: DeleteIntent): Promise<void> {
    intent.inFlight = true;
    this.setDeleteBusy(true, intent.dialogID);
    try {
      // This explicit key reaches the Media receipt. A retry keeps the exact
      // same delete intent instead of falling through the legacy per-request
      // server compatibility key.
      await unwrapGenerated(await deleteLegacyImage(intent.itemID, undefined, apiRequestOptions({ headers: { "Idempotency-Key": intent.key } })));
      intent.inFlight = false;
      await this.verifyDelete(intent.dialogID);
    } catch (error) {
      intent.inFlight = false;
      if (this.deleteIntent !== intent) return;
      if (error instanceof ApiError && error.status >= 400 && error.status < 500) {
        this.deleteIntent = undefined;
        this.setDeleteBusy(false, intent.dialogID);
        this.setDialogError(errorText(error, "delete"), intent.dialogID);
        return;
      }
      // A transport loss or 5xx can happen after the transaction commits. Do
      // not enable a fresh delete; retain this key and use the single-resource
      // read before allowing a same-key retry.
      this.setDialogError("删除结果暂不可确认。请核对删除结果，勿重复删除。", intent.dialogID);
      this.setDialogAction("重新核对删除结果", "delete-verify", intent.dialogID);
    }
  }

  private async deleteExistence(intent: DeleteIntent): Promise<DeleteVerification> {
    try {
      const response = await getLegacyImage(intent.itemID, undefined, apiRequestOptions());
      if (response.status === 404) return "gone";
      if (response.status === 200) return "present";
    } catch {
      // A failed verifier is outcome_unknown: the original delete intent must
      // remain available with its stable key.
    }
    return "unknown";
  }

  private async verifyDelete(expectedDialogID: number): Promise<void> {
    const intent = this.deleteIntent;
    if (!intent || intent.dialogID !== expectedDialogID) return;
    const outcome = await this.deleteExistence(intent);
    if (this.deleteIntent !== intent) return;
    if (outcome === "unknown") {
      this.setDialogError("删除结果暂不可确认。请核对删除结果，勿重复删除。", intent.dialogID);
      this.setDialogAction("重新核对删除结果", "delete-verify", intent.dialogID);
      return;
    }
    if (outcome === "gone") {
      this.deleteIntent = undefined;
      // The detail 404 is the deletion fact. The paginated list only updates
      // what the user sees and is never used as deletion evidence.
      const result = await this.load(intent.readbackOffset);
      if (result === "success") {
        this.closeDialog(intent.dialogID);
        this.notifyMaterialRefresh();
      }
      else this.markDeletedButUnread(intent.dialogID);
      return;
    }
    this.setDialogError("删除尚未确认。可按原操作重试删除。", intent.dialogID);
    this.setDialogAction("按原操作重试删除", "delete-retry", intent.dialogID);
  }

  private setDeleteBusy(busy: boolean, expectedDialogID: number): void {
    if (!this.dialogMatches(expectedDialogID)) return;
    const remove = this.dialogLayer.querySelector<HTMLButtonElement>("[data-image-library-delete]");
    if (remove) remove.disabled = busy || Boolean(this.deleteIntent);
  }

  private setDialogAction(label: string, action: "delete-verify" | "delete-retry", expectedDialogID: number): void {
    if (!this.dialogMatches(expectedDialogID)) return;
    const submit = this.dialogLayer.querySelector<HTMLButtonElement>("[data-image-library-dialog-submit]");
    if (!submit) return;
    submit.textContent = label;
    submit.dataset.imageLibraryDialogAction = action;
    submit.disabled = false;
  }

  private async readbackAfterMutation(dialogID: number, offset = this.offset): Promise<void> {
    const result = await this.load(offset);
    if (result === "success") {
      this.closeDialog(dialogID);
      this.notifyMaterialRefresh();
    } else {
      // An aborted read cannot safely restore a normal write entry either.
      // Keep the accepted mutation in readback-pending state until a user
      // confirms the current list with a read-only retry.
      this.markSavedButUnread(dialogID);
    }
  }

  private async retryReadback(dialogID: number): Promise<void> {
    if (!this.dialogMatches(dialogID)) return;
    if (this.deleteIntent?.dialogID === dialogID) {
      await this.verifyDelete(dialogID);
      return;
    }
    const result = await this.load(this.offset);
    if (result === "success") {
      this.closeDialog(dialogID);
      this.notifyMaterialRefresh();
    }
    else this.markSavedButUnread(dialogID);
  }

  private notifyMaterialRefresh(): void {
    window.dispatchEvent(new Event(MEDIA_CONTENT_CHANGED_EVENT));
  }

  private markSavedButUnread(expectedDialogID: number): void {
    if (!this.dialogMatches(expectedDialogID) || !this.dialog) return;
    const value = "素材已保存，但列表回读失败；编辑内容已保留。请重新读取确认，勿重复提交。";
    this.dialog = { ...this.dialog, error: value, readbackPending: true };
    this.setDialogError(value, expectedDialogID);
    const panel = this.dialogLayer.querySelector<HTMLFormElement>("form[data-image-library-dialog]");
    const submit = panel?.querySelector<HTMLButtonElement>("[data-image-library-dialog-submit]");
    if (submit) {
      submit.textContent = "重新读取列表";
      submit.disabled = false;
    }
  }

  private markDeletedButUnread(expectedDialogID: number): void {
    if (!this.dialogMatches(expectedDialogID) || !this.dialog) return;
    const value = "图片已删除，但列表回读未完成。请重新读取确认。";
    this.dialog = { ...this.dialog, error: value, readbackPending: true };
    this.setDialogError(value, expectedDialogID);
    const submit = this.dialogLayer.querySelector<HTMLButtonElement>("[data-image-library-dialog-submit]");
    if (submit) {
      // The exact-resource 404 already confirmed deletion and cleared its
      // intent. A leftover delete-verify action would otherwise call a
      // verifier with no intent forever instead of performing the list-only
      // readback promised by this recovery state.
      delete submit.dataset.imageLibraryDialogAction;
      submit.textContent = "重新读取列表";
      submit.disabled = false;
    }
  }

  private setDialogError(value: string, expectedDialogID?: number): void {
    if (!this.dialog || (expectedDialogID !== undefined && !this.dialogMatches(expectedDialogID))) return;
    this.dialog = { ...this.dialog, error: value };
    const panel = this.dialogLayer.querySelector<HTMLFormElement>("form[data-image-library-dialog]");
    if (panel) {
      let feedback = panel.querySelector<HTMLElement>("[data-image-library-mutation-feedback]");
      if (!feedback) {
        feedback = document.createElement("p");
        feedback.dataset.imageLibraryMutationFeedback = "true";
        feedback.setAttribute("role", "alert");
        feedback.style.cssText = "margin:0;color:#D83931;font-size:13px;line-height:20px";
        panel.querySelector("footer")?.before(feedback);
      }
      feedback.textContent = value;
      // Keep typed field values and the browser-owned file selection intact so
      // the user can correct the request and retry from the same dialog.
      return;
    }
    this.openDialog(this.dialog);
  }
}

function boot(): void {
  if (document.body?.dataset.page !== "images") return;
  const stage = document.querySelector<HTMLElement>("main#stage[data-image-library-v3-root]");
  if (!stage || stage.dataset.imageLibraryHostMounted === "true") return;
  stage.dataset.imageLibraryHostMounted = "true";
  new ImageLibraryHost(stage).start();
}

if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot, { once: true });
else boot();
