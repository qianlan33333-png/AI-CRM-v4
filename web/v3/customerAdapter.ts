/**
 * V3-owned compatibility Host for the byte-frozen customer list
 * document. The frozen generated client still names v2 `/api/v1` resources;
 * this seam translates only those reads into current Customer-owned safe
 * projections before the frozen admin entry executes. It neither creates
 * customers nor changes Customer mutations, OneID resolution, or ownership.
 */
import { createTagCatalogPageLoader, unresolvedTagRecord, type TagPickerRecord } from './shared/ui/tagPickerAdapter';

export {};

type JSONRecord = Record<string, unknown>;
type CustomerPresentation = {
  phoneMasked: string;
  ownerStaffID: number | null;
  ownerDisplayName: string;
  oneID: string;
  activationStatus: string;
};

const originalCustomerFetch = window.fetch.bind(window);
const directoryPresentation = new Map<number, CustomerPresentation>();
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
    } catch {
      // A disposed document has no remaining interactive surface to update.
    }
  });
}

function customerIDFromLink(link: HTMLAnchorElement): number | null {
  const id = new URL(link.href, window.location.origin).pathname.split("/").pop() ?? null;
  return positiveInteger(id);
}

function renderDirectoryPresentation(): void {
  for (const link of Array.from(document.querySelectorAll<HTMLAnchorElement>('a[href^="/admin/customers/"]'))) {
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

function ensurePresentationObserver(): void {
  if (presentationObserver || !document.documentElement) return;
  presentationObserver = new MutationObserver(schedulePresentation);
  presentationObserver.observe(document.documentElement, { childList: true, subtree: true });
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
  // Session/logout and detail reads retain their original request.
  // Only the customer list and its empty stage directory are adapted.
  return originalCustomerFetch(input, init);
};
