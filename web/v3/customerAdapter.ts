/**
 * V3-owned compatibility Host for the byte-frozen customer list and detail
 * documents. The frozen generated client still names v2 `/api/v1` resources;
 * this seam translates only those reads into current Customer-owned safe
 * projections before the frozen admin entry executes. It neither creates
 * customers nor changes Customer mutations, OneID resolution, or ownership.
 */
import { formatShanghaiDateTime } from "./adminDateTime";
import { createTagCatalogPageLoader, unresolvedTagRecord, type TagPickerRecord } from './shared/ui/tagPickerAdapter';

export {};

type JSONRecord = Record<string, unknown>;
type Availability = "ready" | "unavailable";

type CustomerPresentation = {
  phoneMasked: string;
  ownerStaffID: number | null;
  ownerDisplayName: string;
  oneID: string;
  activationStatus: string;
};

type AuxiliaryRead = {
  status: Availability;
  value: unknown;
};

type DetailPresentation = CustomerPresentation & {
  tags: AuxiliaryRead;
  chat: AuxiliaryRead;
};

const originalCustomerFetch = window.fetch.bind(window);
const directoryPresentation = new Map<number, CustomerPresentation>();
const detailPresentation = new Map<number, DetailPresentation>();
let presentationObserver: MutationObserver | undefined;
let presentationQueued = false;

function record(value: unknown): JSONRecord {
  return value !== null && typeof value === "object" && !Array.isArray(value)
    ? (value as JSONRecord)
    : {};
}

function items(value: unknown): unknown[] {
  return Array.isArray(record(value).items) ? (record(value).items as unknown[]) : [];
}

function text(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function positiveInteger(value: unknown): number | null {
  const number = Number(value);
  return Number.isSafeInteger(number) && number > 0 ? number : null;
}

function availability(value: unknown): Availability {
  return text(value) === "unavailable" ? "unavailable" : "ready";
}

const messageTypeName: Record<string, string> = {
  text: "文本",
  image: "图片",
  voice: "语音",
  video: "视频",
  file: "文件",
  link: "链接",
  emotion: "表情",
  location: "位置",
  weapp: "小程序",
  revoke: "撤回消息",
};

function messageTypeLabel(value: unknown): string {
  return messageTypeName[text(value)] || "消息类型待确认";
}

function json(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestURL(input: RequestInfo | URL): URL {
  if (input instanceof URL) return new URL(input.toString(), window.location.origin);
  if (typeof input === "string") return new URL(input, window.location.origin);
  return new URL(input.url, window.location.origin);
}

function currentCustomerID(pathname: string, suffix: string): number | undefined {
  const match = new RegExp(`^/api/v1/customers/([1-9][0-9]*)/${suffix}$`).exec(pathname);
  if (!match) return undefined;
  const id = Number(match[1]);
  return Number.isSafeInteger(id) && id > 0 ? id : undefined;
}

function customerPresentation(value: unknown): CustomerPresentation {
  const customer = record(value);
  return {
    phoneMasked: text(customer.phone_masked),
    ownerStaffID: positiveInteger(customer.owner_staff_id),
    // A local CRM owner is not a WeCom follow relation. Do not derive a name
    // from another projection when this directory response does not provide it.
    ownerDisplayName: text(customer.owner_display_name),
    oneID: text(customer.customer_number) || text(customer.oneid),
    activationStatus: text(customer.activation_status),
  };
}

function setText(element: Element | null | undefined, value: string): void {
  if (element && element.textContent !== value) element.textContent = value;
}

function schedulePresentation(): void {
  if (presentationQueued) return;
  presentationQueued = true;
  queueMicrotask(() => {
    presentationQueued = false;
    // JSDOM test fixtures may tear down a page while its final mutation is
    // pending. A browser document is still live through page unload; avoid
    // dereferencing a disposed test Window without changing page behavior.
    try {
      if (!document.documentElement) return;
      renderDirectoryPresentation();
      renderDetailPresentation();
    } catch {
      // A disposed document has no remaining interactive surface to update.
    }
  });
}

function customerIDFromLink(link: HTMLAnchorElement): number | null {
  const id = new URL(link.href, window.location.origin).searchParams.get("id");
  return positiveInteger(id);
}

function renderDirectoryPresentation(): void {
  for (const link of Array.from(document.querySelectorAll<HTMLAnchorElement>('a[href^="customerDetail.html?id="]'))) {
    const id = customerIDFromLink(link);
    if (id == null) continue;
    const presentation = directoryPresentation.get(id);
    const row = link.closest("tr");
    if (!presentation || !row || row.children.length < 3) continue;
    const owner = presentation.ownerStaffID == null
      ? "未分配"
      : presentation.ownerDisplayName
        ? `${presentation.ownerDisplayName}（${presentation.ownerStaffID}）`
        : `负责人 #${presentation.ownerStaffID}（姓名暂不可用）`;
    setText(row.children.item(1), owner);
    setText(row.children.item(2), presentation.phoneMasked || "暂无手机号");
    if (row.dataset.customerPresentation !== "ready") row.dataset.customerPresentation = "ready";
  }
}

function cardForHeading(heading: string): HTMLElement | null {
  const headingElement = Array.from(document.querySelectorAll<HTMLElement>("h2"))
    .find((element) => element.textContent?.trim() === heading);
  return headingElement?.closest("div[style*='background']") as HTMLElement | null;
}

function auxiliaryNotice(kind: "tags" | "chat", id: number): HTMLElement {
  const notice = document.createElement("p");
  notice.dataset.customerAuxiliaryStatus = kind;
  notice.style.cssText = "margin:8px 0 0;font-size:12px;color:#D46B08";
  notice.append(kind === "tags" ? "标签加载失败，请重试。" : "聊天加载失败，请重试。");
  const retry = document.createElement("button");
  retry.type = "button";
  retry.dataset.customerAuxiliaryRetry = kind;
  retry.textContent = "重试";
  retry.style.cssText = "margin-left:4px;border:0;padding:0;background:transparent;color:#3370ff;font-size:12px;cursor:pointer";
  retry.addEventListener("click", () => { void retryAuxiliary(id, kind, retry); });
  notice.append(retry);
  return notice;
}

function renderAuxiliaryNotice(card: HTMLElement | null, kind: "tags" | "chat", id: number, status: Availability): void {
  if (!card) return;
  const existing = card.querySelector(`[data-customer-auxiliary-status="${kind}"]`);
  if (status === "ready") {
    existing?.remove();
    return;
  }
  if (existing) return;
  const header = card.firstElementChild;
  header?.append(auxiliaryNotice(kind, id));
}

function renderDetailPresentation(): void {
  const id = positiveInteger(new URL(window.location.href).searchParams.get("id"));
  if (id == null) return;
  const presentation = detailPresentation.get(id);
  if (!presentation) return;
  const ownerLabel = Array.from(document.querySelectorAll<HTMLElement>("div"))
    .find((element) => element.textContent?.trim() === "负责人（staff ID）");
  const ownerValue = ownerLabel?.nextElementSibling;
  if (ownerValue) {
    const owner = presentation.ownerStaffID == null
      ? "未分配"
      : presentation.ownerDisplayName
        ? `${presentation.ownerDisplayName}（${presentation.ownerStaffID}）`
        : `负责人 #${presentation.ownerStaffID}（姓名暂不可用）`;
    setText(ownerValue, owner);
  }
  renderAuxiliaryNotice(cardForHeading("实时标签"), "tags", id, presentation.tags.status);
  const chatCard = cardForHeading("聊天活动摘要");
  renderAuxiliaryNotice(chatCard, "chat", id, presentation.chat.status);
  // The compatibility context endpoint keeps protocol fields untouched. Once
  // the frozen renderer has consumed that DTO, this bounded Host projection
  // owns only the visible chat-summary card.
  if (presentation.chat.status === "ready" && (chatCard?.lastElementChild as HTMLElement | null)?.dataset.customerAuxiliaryPresentation !== "chat") {
    renderAuxiliaryItems("chat", presentation.chat.value);
  }
}

function ensurePresentationObserver(): void {
  if (presentationObserver || !document.documentElement) return;
  presentationObserver = new MutationObserver(schedulePresentation);
  presentationObserver.observe(document.documentElement, { childList: true, subtree: true });
  schedulePresentation();
}

async function readAuxiliary(path: string, init?: RequestInit): Promise<AuxiliaryRead> {
  try {
    const response = await originalCustomerFetch(path, init);
    if (!response.ok) return { status: "unavailable", value: undefined };
    const value = await response.json();
    return { status: availability(record(value).status), value };
  } catch {
    return { status: "unavailable", value: undefined };
  }
}

function auxiliaryPath(id: number, kind: "tags" | "chat"): string {
  return kind === "tags"
    ? `/api/admin/customers/${id}/tags`
    : `/api/admin/customers/${id}/chat-activity?limit=20`;
}

function renderAuxiliaryItems(kind: "tags" | "chat", value: unknown): void {
  const card = cardForHeading(kind === "tags" ? "实时标签" : "聊天活动摘要");
  const content = card?.lastElementChild as HTMLElement | null;
  if (!content) return;
  content.dataset.customerAuxiliaryPresentation = kind;
  content.replaceChildren();
  if (kind === "tags") {
    const tags = items(value);
    if (!tags.length) {
      const empty = document.createElement("div");
      empty.style.cssText = "padding:24px;text-align:center;color:#8F959E;font-size:13px";
      empty.textContent = "暂无实时标签";
      content.append(empty);
      return;
    }
    for (const value of tags) {
      const tag = record(value);
      const label = document.createElement("span");
      label.style.cssText = "display:inline-flex;align-items:center;height:26px;padding:0 10px;border-radius:4px;background:#F2F3F5;color:#646A73;font-size:12px";
      label.textContent = text(tag.name) || "未命名标签";
      content.append(label);
    }
    return;
  }
  const messages = items(value);
  if (!messages.length) {
    const empty = document.createElement("div");
    empty.style.cssText = "padding:24px;text-align:center;color:#8F959E;font-size:13px";
    empty.textContent = "暂无聊天活动摘要";
    content.append(empty);
    return;
  }
  for (const value of messages) {
    const message = record(value);
    const row = document.createElement("div");
    row.style.cssText = "padding:8px 12px;border:1px solid #EFF0F1;border-radius:8px;background:#FAFAFB";
    const meta = document.createElement("div");
    meta.style.cssText = "font-size:11px;color:#A6AAB0;margin-bottom:4px";
    meta.textContent = `${text(message.chat_type) === "private" ? "私聊" : "群聊"} · ${formatShanghaiDateTime(text(message.occurred_at))}`;
    const type = document.createElement("div");
    type.style.cssText = "font-size:13px;color:#646A73";
    type.textContent = `消息类型：${messageTypeLabel(message.message_type)}`;
    row.append(meta, type);
    content.append(row);
  }
}

async function retryAuxiliary(id: number, kind: "tags" | "chat", button: HTMLButtonElement): Promise<void> {
  button.disabled = true;
  button.textContent = "读取中…";
  const result = await readAuxiliary(auxiliaryPath(id, kind));
  const current = detailPresentation.get(id);
  if (!current) return;
  detailPresentation.set(id, { ...current, [kind]: result });
  if (result.status === "ready") {
    renderAuxiliaryItems(kind, result.value);
  } else {
    button.disabled = false;
    button.textContent = "重试";
  }
  schedulePresentation();
}

async function customerDirectory(url: URL, init?: RequestInit): Promise<Response> {
  const query = new URLSearchParams();
  query.set("limit", url.searchParams.get("limit") || "50");
  for (const key of ["cursor", "keyword", "owner_staff_id", "tag_id"] as const) {
    const value = url.searchParams.get(key);
    if (value) query.set(key, value);
  }
  const mobile = url.searchParams.get("mobile");
  if (mobile) query.set("phone", mobile);
  const response = await originalCustomerFetch(`/api/admin/customers?${query.toString()}`, init);
  if (!response.ok) return response;
  const page = record(await response.json());
  const adaptedItems = items(page).map((value) => {
    const customer = record(value);
    const id = positiveInteger(customer.customer_id);
    if (id == null) return null;
    const presentation = customerPresentation(customer);
    directoryPresentation.set(id, presentation);
    return {
      id,
      name: text(customer.display_name) || `用户 ${id}`,
      owner_staff_id: presentation.ownerStaffID,
      stage_id: null,
      is_deleted: false,
      extra: {},
      mobile: presentation.phoneMasked,
      oneid: presentation.oneID,
      activation_status: presentation.activationStatus,
      owner_display_name: presentation.ownerDisplayName || "姓名暂不可用",
      created_at: text(customer.last_synced_at),
      updated_at: text(customer.updated_at),
    };
  }).filter((value): value is NonNullable<typeof value> => value !== null);
  schedulePresentation();
  return json({ ...page, items: adaptedItems });
}

async function customerContext(id: number, init?: RequestInit): Promise<Response> {
  const response = await originalCustomerFetch(`/api/admin/customers/${id}/360`, init);
  if (!response.ok) return response;
  const source = record(await response.json());
  const profile = record(record(source.profile).data);
  const sourcePresentation = customerPresentation(profile);
  const requestedTags = availability(source.tags_status) === "ready"
    ? await readAuxiliary(`/api/admin/customers/${id}/tags`, init)
    : { status: "unavailable" as const, value: undefined };
  const sourceChat = record(source.chat);
  const requestedChat = availability(sourceChat.status) === "ready"
    ? await readAuxiliary(`/api/admin/customers/${id}/chat-activity?limit=20`, init)
    : { status: "unavailable" as const, value: undefined };
  const tags = requestedTags.status === "ready" ? items(requestedTags.value) : [];
  const recentTouchpoints = record(source.recent_touchpoints);
  const timeline = recentTouchpoints.status === "ready" && Array.isArray(recentTouchpoints.data)
    ? recentTouchpoints.data
    : [];
  const chat = requestedChat.status === "ready" ? items(requestedChat.value).map((value) => {
    const item = record(value);
    return {
      chat_type: item.chat_type,
      message_type: item.message_type,
      sent_at: item.occurred_at,
    };
  }) : [];
  detailPresentation.set(id, {
    ...sourcePresentation,
    tags: requestedTags,
    chat: requestedChat,
  });
  schedulePresentation();
  return json({
    customer: {
      id,
      name: text(profile.display_name) || `用户 ${id}`,
      owner_staff_id: sourcePresentation.ownerStaffID,
      stage_id: null,
      channel_id: null,
      added_at: profile.last_synced_at ?? null,
      last_interact_at: profile.updated_at ?? null,
    },
    tags,
    tags_status: requestedTags.status,
    timeline,
    chat: { items: chat, total: chat.length, status: requestedChat.status, local_archive_available: false },
    hxc: { available: false },
    non_atomic_snapshot: true,
    real_external_call_executed: false,
  });
}

async function customerSurvey(id: number, init?: RequestInit): Promise<Response> {
  const response = await originalCustomerFetch(`/api/v1/customers/${id}/survey-answers`, init);
  if (!response.ok) return response;
  const source = record(await response.json());
  const sourceItems = items(source);
  return json({
    customer_id: id,
    identity_values_included: false,
    free_text_included: false,
    real_external_call_executed: false,
    non_atomic_snapshot: true,
    scan_truncated: false,
    result_truncated: Number(source.total) > sourceItems.length,
    items: sourceItems.map((value) => {
      const submission = record(value);
      const choices = (Array.isArray(submission.answers) ? submission.answers : [])
        .map(record)
        .filter((answer) => answer.question_type === "single_choice" || answer.question_type === "multi_choice")
        .map((answer) => ({
          question_id: answer.question_id,
          question_type: answer.question_type,
          sort_order: Number.isSafeInteger(Number(answer.sort_order)) ? Number(answer.sort_order) : 0,
          option_ids: (Array.isArray(answer.selected_options) ? answer.selected_options : [])
            .map(record)
            .map((option) => option.option_id)
            .filter((optionID) => Number.isSafeInteger(Number(optionID)) && Number(optionID) > 0),
        }));
      return {
        submission_id: submission.id,
        questionnaire_id: submission.questionnaire_id,
        submitted_at: submission.submitted_at,
        score: submission.total_score,
        choice_answers: choices,
      };
    }),
  });
}

type StandardMember = { staff_id?: number; user_id?: string; display_name?: string };
type StandardSelectorWindow = Window & {
  AICRMStandardComponents?: { ready(): Promise<void> };
  OperationMemberPicker?: { open(options: { title: string; scope: string; page_size: number; selectedMember?: StandardMember; onSelect(member: StandardMember | null): void }): void };
  AICRMTagPicker?: { open(options: { title: string; source: string; scope: string; selectedRecords: TagPickerRecord[]; mode: 'single'; loadPage: ReturnType<typeof createTagCatalogPageLoader>; onCommit(value: { selected: TagPickerRecord[] }): void; accessLossMessage(error: unknown): string | undefined }): void };
};

function standardSelectors(): StandardSelectorWindow {
  return window as StandardSelectorWindow;
}

function inputFor(id: string): HTMLInputElement | null {
  const element = document.getElementById(id);
  return element instanceof HTMLInputElement ? element : null;
}

function selectorSummary(input: HTMLInputElement): HTMLSpanElement {
  const existing = input.parentElement?.querySelector<HTMLSpanElement>('[data-standard-selector-summary]');
  if (existing) return existing;
  const summary = document.createElement('span');
  summary.dataset.standardSelectorSummary = '';
  summary.style.cssText = 'font-size:12px;color:#646A73;line-height:20px';
  input.parentElement?.append(summary);
  return summary;
}

function selectorButton(input: HTMLInputElement, label: string): HTMLButtonElement {
  const existing = input.parentElement?.querySelector<HTMLButtonElement>('[data-standard-selector-button]');
  if (existing) return existing;
  input.hidden = true;
  const button = document.createElement('button');
  button.type = 'button';
  button.dataset.standardSelectorButton = '';
  button.textContent = label;
  button.style.cssText = 'height:32px;padding:0 10px;border:1px solid #DEE0E3;border-radius:6px;background:#fff;color:#344054;font-size:13px;cursor:pointer';
  input.parentElement?.append(button);
  return button;
}

function tagCatalogLoadFailure(response: Response): Error {
  const error = new Error(`标签目录读取失败（HTTP ${response.status}）`) as Error & { status?: number };
  error.status = response.status;
  return error;
}

function attachTagSelector(inputID: string, title: string): void {
  const input = inputFor(inputID);
  const picker = standardSelectors().AICRMTagPicker;
  if (!input || !picker || input.dataset.standardSelectorReady) return;
  input.dataset.standardSelectorReady = 'true';
  const button = selectorButton(input, '选择标签');
  const summary = selectorSummary(input);
  const sync = (): void => { summary.textContent = input.value ? `已选标签 #${input.value}` : '暂未选择标签'; };
  sync();
  button.addEventListener('click', () => {
    const source = 'local_tag_catalog';
    picker.open({
      title,
      source,
      scope: 'customer.filter.tag',
      mode: 'single',
      selectedRecords: input.value ? [unresolvedTagRecord(source, input.value)].filter((value): value is TagPickerRecord => Boolean(value)) : [],
      loadPage: createTagCatalogPageLoader(source, async ({ signal }) => {
        const response = await originalCustomerFetch('/api/admin/wecom/tags', { credentials: 'same-origin', headers: { Accept: 'application/json' }, signal });
        const payload = await response.json().catch(() => ({}));
        if (!response.ok) throw tagCatalogLoadFailure(response);
        return payload;
      }),
      onCommit(value) { input.value = value.selected[0]?.tag_id || ''; sync(); },
      accessLossMessage(error) {
        return (error as { status?: number } | undefined)?.status === 403 ? '标签目录权限已失效；当前筛选条件仍保留，请取消后重新登录。' : undefined;
      },
    });
  });
}

function attachOwnerSelector(): void {
  const input = inputFor('fCustomerOwner');
  const picker = standardSelectors().OperationMemberPicker;
  if (!input || !picker || input.dataset.standardSelectorReady) return;
  input.dataset.standardSelectorReady = 'true';
  const button = selectorButton(input, '选择负责人');
  const summary = selectorSummary(input);
  const sync = (): void => { summary.textContent = input.value ? `已选负责人 staff_id #${input.value}` : '暂未选择负责人'; };
  sync();
  button.disabled = true;
  void originalCustomerFetch('/api/admin/common/operation-members?scope=owner_migration&page_size=1', {
    credentials: 'same-origin', headers: { Accept: 'application/json' },
  }).then((response) => {
    if (!response.ok) {
      button.hidden = true;
      summary.textContent = response.status === 403 ? '当前角色无负责人目录权限' : '负责人目录当前不可用';
      return;
    }
    button.disabled = false;
    button.addEventListener('click', () => {
      picker.open({
        title: '选择用户负责人',
        scope: 'owner_migration',
        page_size: 100,
        onSelect(member) {
          const staffID = positiveInteger(member?.staff_id);
          if (staffID == null) return;
          input.value = String(staffID);
          summary.textContent = `${member?.display_name || member?.user_id || `负责人 #${staffID}`}（staff_id #${staffID}）`;
        },
      });
    }, { once: true });
  }).catch(() => {
    button.hidden = true;
    summary.textContent = '负责人目录当前不可用';
  });
}

function attachFrozenCustomerSelectors(): void {
  attachOwnerSelector();
  attachTagSelector('fCustomerTag', '选择用户筛选标签');
}

async function installFrozenCustomerSelectors(): Promise<void> {
  const standard = standardSelectors().AICRMStandardComponents;
  if (!standard) return;
  await standard.ready();
  const observe = (): void => attachFrozenCustomerSelectors();
  const observer = new MutationObserver(observe);
  if (document.documentElement) observer.observe(document.documentElement, { childList: true, subtree: true });
  window.addEventListener('pagehide', () => observer.disconnect(), { once: true });
  observe();
}

ensurePresentationObserver();
void installFrozenCustomerSelectors();

window.fetch = async (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
  const url = requestURL(input);
  if (url.pathname === "/api/v1/customers") return customerDirectory(url, init);
  if (url.pathname === "/api/v1/stages") return json({ items: [] });
  const contextID = currentCustomerID(url.pathname, "context");
  if (contextID !== undefined) return customerContext(contextID, init);
  const surveyID = currentCustomerID(url.pathname, "survey-answers");
  if (surveyID !== undefined) return customerSurvey(surveyID, init);
  // Session/logout requests retain the Webshell's method, cookies, and CSRF
  // mechanism. The Host only adapts its four safe customer GET projections.
  return originalCustomerFetch(input, init);
};
