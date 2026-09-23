import { openDetailDrawer } from './shared/ui/detailDrawer';
import { installCommittedTextSearch } from './shared/ui/committedTextSearch';
import { mountPageHeaderActions } from './shared/ui/pageHeaderActions';
import { openShareQrDialog } from './shared/ui/shareQrDialog';
import { distributionAdjustmentLabel, distributionCommissionStatusLabel, distributionExceptionLabel, distributionSettlementStatusLabel } from './distributionPresentation';
import type { ConfirmationDialogOptions, ConfirmationDialogResult } from './shared/ui/confirmationDialog';

export {};

declare global {
  interface Window {
    AICRMConfirmation?: { confirm(options: ConfirmationDialogOptions): Promise<ConfirmationDialogResult> };
  }
}

type Row = Record<string, unknown>;
type Tab = 'distributors' | 'orders' | 'exceptions';
type SummaryPeriod = 'today' | '7d' | '30d';
type SummaryStatus = 'ready' | 'zero' | 'data_missing' | 'failed';
type PageState = { rows: Row[]; cursor: string; loading: boolean; failure: string; draftFilter: string; committedFilter: string };
type FilterControl = { element: HTMLElement; input: HTMLInputElement };
type LoadResult = 'applied' | 'failed' | 'stale';

class RequestError extends Error {
  constructor(readonly status: number, message: string) { super(message); }
}

const root = document.getElementById('distribution-admin-root');
if (!root) throw new Error('分销管理容器缺失');
const distributionRoot: HTMLElement = root;
const summaryHost = document.createElement('section');
const controlsHost = document.createElement('section');
const tabsHost = document.createElement('section');
let mountedFilter: HTMLElement | undefined;
const tableHost = document.createElement('section');
const paginationHost = document.createElement('section');
const messageHost = document.createElement('p');
messageHost.dataset.distributionAdminMessage = '';
messageHost.className = 'distribution-message';
let pageMounted = false;

const obj = (value: unknown): Row => value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Row : {};
const optionalText = (value: unknown): string | undefined => typeof value === 'string' && value.trim() === value && value !== '' ? value : undefined;
const text = (value: unknown, fallback = '—'): string => optionalText(value) || fallback;
const integer = (value: unknown): number | undefined => {
  if (typeof value === 'number' && Number.isSafeInteger(value)) return value;
  if (typeof value !== 'string' || value.trim() !== value || !/^-?\d+$/.test(value)) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
};
const idText = (value: unknown, fallback = ''): string => {
  const id = integer(value);
  return optionalText(value) || (id !== undefined && id > 0 ? String(id) : fallback);
};
const esc = (value: unknown): string => text(value).replace(/[&<>'"]/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' }[character] || character));
const currency = (value: unknown): string | undefined => {
  const code = optionalText(value);
  return code && /^[A-Z]{3}$/.test(code) ? code : undefined;
};
const money = (value: unknown, code: unknown): string => {
  const amount = integer(value);
  const currentCurrency = currency(code);
  if (amount === undefined || !currentCurrency) return '—';
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency: currentCurrency }).format(amount / 100);
};
const numericText = (value: unknown, suffix = ''): string => {
  const number = integer(value);
  return number === undefined ? '待确认' : `${number.toLocaleString('zh-CN')}${suffix}`;
};
const rateText = (value: unknown): string => {
  const basisPoints = integer(value);
  return basisPoints === undefined ? '比例待确认' : `${(basisPoints / 100).toFixed(2)}%`;
};
const policyText = (version: unknown, rate: unknown, waitDays: unknown): string => `版本 ${numericText(version)} · ${rateText(rate)} · ${numericText(waitDays, ' 天')}`;
const qualifyLabel = (value: unknown): string => ({ eligible: '资格有效', ineligible: '未满足购买资格', suspended: '退款核验中', identity_conflict: '身份关联待核验', evidence_unavailable: '购买凭证待核验' } as Row)[text(value, '')] as string || '资格待确认';
const reasonLabel = (value: unknown): string => ({ buyer_refund: '买家退款', buyer_refund_after_paid: '买家退款后已存在已付金额', qualification_refund_evidence_pending: '退款凭证待核验', refund_outcome_pending: '退款结果待核验', settlement_outcome_unknown: '结算结果待核验', split_deadline_within_24h: '结算时限不足 24 小时', payment_receiver_account_abnormal: '接收方账户异常，请核验微信账户状态', payment_receiver_relation_removed: '接收方分账关系已解除，请完成关系核验', payment_receiver_high_risk: '接收方被风控限制，请联系商家核验', payment_receiver_real_name_unverified: '接收方未完成实名认证，请完成后再核验', payment_merchant_permission_revoked: '商户分账权限已解除，请在商户平台核验', payment_receiver_receipt_limit: '接收方收款额度已达上限，请核验额度', payment_payer_account_abnormal: '付款方账户异常，请核验付款方状态', payment_invalid_split_request: '分账请求被拒绝，请核验订单与接收方参数', manual_recovery: '已登记人工追回', merchant_liability: '已登记商户承担' } as Row)[text(value, '')] as string || '原因待确认';
const displayName = (value: unknown): string => text(value, '').trim() || '未设置昵称';
const receiverStatusLabel = (ready: unknown, reason: unknown, label: unknown): string => text(label, '').trim() || (() => {
  if (ready === true) return '收款准备完成';
  return ({ receiver_accepted: '收款申请已受理，等待支付侧确认', receiver_outcome_unknown: '收款结果待支付侧核验', receiver_final_failed: '收款准备失败，需管理员核验支付侧状态', receiver_unavailable: '收款准备不可用，需管理员核验支付侧条件' } as Row)[text(reason, '')] as string || '收款准备待支付侧核验';
})();
const auditEventLabel = (value: unknown): string => ({ 'distribution.exception_opened.v1': '已记录异常', 'distribution.exception_reconciled.v1': '已查询最新结果', 'distribution.recovery_recorded.v1': '已登记追回', 'distribution.merchant_liability_recorded.v1': '已登记商户承担' } as Row)[text(value, '')] as string || '已记录处理';

function actorScopeLabel(value: unknown): string {
  const scope = text(value, '');
  if (scope.startsWith('access:')) return '管理员';
  if (scope.startsWith('worker:distribution-due')) return '系统结算检查';
  if (scope.startsWith('order-refund:')) return '订单退款处理';
  return scope ? '系统记录' : '未记录';
}

function csrf(): string {
  for (const part of document.cookie.split(';')) {
    const [name, ...rest] = part.trim().split('=');
    if (name === 'aicrm_csrf' || name === 'aicrm_admin_csrf') return decodeURIComponent(rest.join('='));
  }
  return '';
}

function errText(status: number, payload: unknown): string {
  const code = obj(payload).error;
  const labels: Row = { conflict: '记录已变化，请重新读取后再处理。', forbidden: '当前管理员无权执行此操作。', permission_denied: '当前管理员无权查看此记录。', unavailable: '服务暂不可用，未执行操作。', invalid_request: '请求参数无效。', not_found: '记录不存在或已不可见。' };
  return typeof code === 'string' && typeof labels[code] === 'string' ? labels[code] as string : `请求失败（HTTP ${status}）`;
}

const keys = new Map<string, string>();
const pendingMutations = new Set<string>();
const pendingConfirmations = new Set<string>();
function key(scope: string): string {
  let value = keys.get(scope);
  if (!value) {
    value = `distribution-admin-${crypto.randomUUID()}`;
    keys.set(scope, value);
  }
  return value;
}

async function request(path: string, init: RequestInit = {}, scope = ''): Promise<unknown> {
  const headers = new Headers(init.headers);
  headers.set('Accept', 'application/json');
  if (init.method && init.method !== 'GET') {
    headers.set('Content-Type', 'application/json');
    const token = csrf();
    if (token) headers.set('X-CSRF-Token', token);
    headers.set('Idempotency-Key', scope ? key(scope) : `distribution-admin-${crypto.randomUUID()}`);
  }
  let response: Response;
  try {
    response = await fetch(path, { ...init, headers, credentials: 'same-origin', cache: 'no-store' });
  } catch {
    throw new Error('网络不可用，未确认任何分销操作。');
  }
  const payload = await response.json().catch(() => ({}));
  if (!response.ok) throw new RequestError(response.status, errText(response.status, payload));
  return payload;
}

function button(label: string, run: () => void | Promise<void>, primary = false): HTMLButtonElement {
  const element = document.createElement('button');
  element.type = 'button';
  element.textContent = label;
  element.className = `admin-button distribution-button${primary ? ' admin-button--primary primary' : ''}`;
  element.addEventListener('click', () => {
    if (element.disabled) return;
    const pending = run();
    if (!(pending instanceof Promise)) return;
    element.disabled = true;
    element.setAttribute('aria-busy', 'true');
    void pending.finally(() => {
      element.disabled = false;
      element.removeAttribute('aria-busy');
    });
  });
  return element;
}

function notice(value: string, failed = false): void {
  const element = document.querySelector<HTMLElement>('[data-distribution-admin-message]');
  if (element) {
    element.textContent = value;
    element.dataset.error = String(failed);
  }
}

function fact(label: string, value: string): HTMLElement {
  const element = document.createElement('div');
  element.className = 'distribution-detail-fact';
  element.innerHTML = `<span>${esc(label)}</span><strong>${esc(value)}</strong>`;
  return element;
}

function drawer(title: string, target: Tab): HTMLElement {
  const body = document.createElement('section');
  body.className = 'distribution-detail-body';
  body.textContent = '正在读取服务端明细…';
  const dialog = openDetailDrawer(title, body);
  activeDetailBodies.set(body, target);
  dialog.addEventListener('close', () => activeDetailBodies.delete(body), { once: true });
  return body;
}

function failed(body: HTMLElement, error: unknown): void {
  body.replaceChildren(Object.assign(document.createElement('p'), { className: 'distribution-error', textContent: error instanceof Error ? error.message : '明细读取失败。' }));
}

function detailCanRender(body: HTMLElement, generation: number, requestAccessGeneration: number): boolean {
  return body.isConnected && generation === detailGeneration && requestAccessGeneration === accessGeneration;
}

function handleDetailFailure(body: HTMLElement, target: Tab, error: unknown, generation: number, requestAccessGeneration: number): void {
  if (!detailCanRender(body, generation, requestAccessGeneration)) return;
  if (accessFailure(error, 401)) {
    invalidateSession();
    return;
  }
  if (accessFailure(error, 403)) {
    revokeListAccess(target, error.message);
    return;
  }
  failed(body, error);
}

function timeText(value: unknown): string {
  const raw = optionalText(value);
  if (!raw) return '—';
  const date = new Date(raw);
  if (Number.isNaN(date.valueOf())) return '待确认';
  return new Intl.DateTimeFormat('zh-CN', { dateStyle: 'medium', timeStyle: 'short', timeZone: 'Asia/Shanghai', hour12: false }).format(date);
}

function recordList(title: string, items: Row[], renderItem: (item: Row) => HTMLElement): HTMLElement {
  const list = document.createElement('section');
  list.className = 'distribution-detail-list';
  list.append(Object.assign(document.createElement('h3'), { textContent: title }));
  if (!items.length) list.append(Object.assign(document.createElement('p'), { textContent: '暂无记录。' }));
  for (const item of items) list.append(renderItem(item));
  return list;
}

function record(facts: [string, string][], open?: () => void): HTMLElement {
  const entry = document.createElement(open ? 'button' : 'div');
  entry.className = 'distribution-detail-record';
  if (entry instanceof HTMLButtonElement) {
    entry.type = 'button';
    entry.addEventListener('click', () => void open?.());
  }
  for (const [label, value] of facts) entry.append(fact(label, value));
  return entry;
}

function applicationURL(): string { return new URL('/distribution', location.origin).toString(); }

async function copyApplicationLink(): Promise<void> {
  const url = applicationURL();
  if (!navigator.clipboard?.writeText) {
    notice('当前环境不支持自动复制，请复制申请页面地址。', true);
    return;
  }
  try {
    await navigator.clipboard.writeText(url);
    notice('分销申请链接已复制。');
  } catch {
    notice('未能自动复制，请复制申请页面地址。', true);
  }
}

function showApplicationEntry(): void {
  const url = applicationURL();
  openShareQrDialog({
    title: '分销申请二维码',
    url,
    qrLabel: '分销员申请入口',
    actions: [{ label: '复制申请链接', primary: true, onClick: () => copyApplicationLink() }],
  });
}

let tab: Tab = 'distributors';
const pageStates: Record<Tab, PageState> = {
  distributors: { rows: [], cursor: '', loading: false, failure: '', draftFilter: '', committedFilter: '' },
  orders: { rows: [], cursor: '', loading: false, failure: '', draftFilter: '', committedFilter: '' },
  exceptions: { rows: [], cursor: '', loading: false, failure: '', draftFilter: '', committedFilter: '' },
};
const filterControls: Partial<Record<Tab, FilterControl>> = {};
const listGenerations: Record<Tab, number> = { distributors: 0, orders: 0, exceptions: 0 };
let summaryPeriod: SummaryPeriod = '7d';
let overview: Row | undefined;
let overviewFailure = '';
let overviewLoading = false;
let overviewGeneration = 0;
let accessGeneration = 0;
let detailGeneration = 0;
const activeDetailBodies = new Map<HTMLElement, Tab>();

function pageState(value = tab): PageState { return pageStates[value]; }

function actionTargetIsCurrent(target: Tab, id: string, version: number, requestAccessGeneration: number): boolean {
  if (requestAccessGeneration !== accessGeneration || tab !== target || pageState(target).loading) return false;
  const key = target === 'distributors' ? 'id' : 'exception_id';
  return pageState(target).rows.some((row) => idText(row[key]) === id && integer(row.version) === version);
}

async function confirmAction(
  scope: string,
  target: Tab,
  id: string,
  version: number,
  options: ConfirmationDialogOptions,
): Promise<ConfirmationDialogResult | undefined> {
  if (pendingConfirmations.has(scope)) {
    notice('确认窗口已打开，请先完成或取消当前操作。', true);
    return undefined;
  }
  const confirm = window.AICRMConfirmation?.confirm;
  if (typeof confirm !== 'function') {
    notice('确认界面未完成加载，本次操作未提交；请重新加载页面后再试。', true);
    return undefined;
  }
  const requestAccessGeneration = accessGeneration;
  pendingConfirmations.add(scope);
  try {
    const result = await confirm(options);
    if (!result.confirmed) return undefined;
    if (!actionTargetIsCurrent(target, id, version, requestAccessGeneration)) {
      notice('目标记录或授权范围已变化，未提交操作；请重新读取后确认。', true);
      return undefined;
    }
    return result;
  } catch {
    notice('确认界面暂时不可用，本次操作未提交；请重新加载页面后再试。', true);
    return undefined;
  } finally {
    pendingConfirmations.delete(scope);
  }
}

function accessFailure(error: unknown, status: number): error is RequestError {
  return error instanceof RequestError && error.status === status;
}

function resetPageForAccess(target: Tab, message: string): void {
  ++listGenerations[target];
  const state = pageState(target);
  state.rows = [];
  state.cursor = '';
  state.loading = false;
  state.failure = message;
  state.draftFilter = '';
  state.committedFilter = '';
}

function invalidateSession(message = '登录已失效，请重新登录后再读取。'): void {
  ++accessGeneration;
  ++detailGeneration;
  ++overviewGeneration;
  overview = undefined;
  overviewLoading = false;
  overviewFailure = message;
  for (const target of ['distributors', 'orders', 'exceptions'] as const) resetPageForAccess(target, message);
  for (const body of activeDetailBodies.keys()) failed(body, new Error(message));
  render();
  notice(message, true);
}

function revokeListAccess(target: Tab, message = '当前管理员无权查看此记录。'): void {
  ++detailGeneration;
  resetPageForAccess(target, message);
  for (const [body, detailTarget] of activeDetailBodies) if (detailTarget === target) failed(body, new Error(message));
  if (target === tab) render();
  notice(message, true);
}

function transientReadFailure(error: unknown): string {
  const message = error instanceof Error ? error.message : '分销管理读取失败。';
  return `读取未更新：${message}`;
}

function summaryStatus(section: Row): SummaryStatus | undefined {
  const status = optionalText(section.status);
  return status === 'ready' || status === 'zero' || status === 'data_missing' || status === 'failed' ? status : undefined;
}

function overviewResponse(value: unknown): Row {
  const response = obj(value);
  if (!summaryStatus(obj(response.distribution)) || !summaryStatus(obj(response.todos))) {
    throw new Error('分销汇总响应无效，请重试。');
  }
  return response;
}

function summaryMoney(section: Row, amount: unknown, code: unknown): string {
  const status = summaryStatus(section);
  if (status === 'data_missing') return '待确认';
  if (status === 'failed') return '读取失败';
  if (!status) return '—';
  return money(amount, code);
}

function summaryNumber(section: Row, value: unknown): string {
  const status = summaryStatus(section);
  if (status === 'data_missing') return '待确认';
  if (status === 'failed') return '读取失败';
  if (!status) return '—';
  return numericText(value);
}

function exceptionOrderNumber(section: Row, value: unknown): string {
  const status = summaryStatus(section);
  if (status === 'data_missing') return '待确认';
  if (status === 'failed') return '读取失败';
  if (!status) return '—';
  const count = integer(value);
  // This is a count, unlike adjustment amounts elsewhere in the Distribution
  // UI. Negative values are invalid facts and must remain explicitly unknown.
  return count === undefined || count < 0 ? '待确认' : count.toLocaleString('zh-CN');
}

function metric(label: string, value: string, status: SummaryStatus | undefined): HTMLElement {
  const card = document.createElement('article');
  card.className = 'distribution-summary-card';
  card.dataset.distributionSummaryStatus = status || 'loading';
  card.append(Object.assign(document.createElement('span'), { className: 'distribution-summary-card__label', textContent: label }));
  card.append(Object.assign(document.createElement('strong'), { textContent: value }));
  return card;
}

function summary(): HTMLElement {
  const section = obj(overview?.distribution);
  const status = summaryStatus(section);
  const summaryRoot = document.createElement('section');
  summaryRoot.className = 'distribution-summary';
  summaryRoot.setAttribute('aria-label', '分销汇总');
  const head = document.createElement('header');
  head.className = 'distribution-summary__head';
  const copy = document.createElement('div');
  copy.append(Object.assign(document.createElement('h2'), { textContent: '分销概览' }));
  const periods = document.createElement('div');
  periods.className = 'distribution-summary__periods';
  for (const [period, label] of [['today', '今日'], ['7d', '近 7 天'], ['30d', '近 30 天']] as const) {
    const control = button(label, () => { summaryPeriod = period; void loadOverview(); }, summaryPeriod === period);
    control.dataset.distributionSummaryPeriod = period;
    control.setAttribute('aria-pressed', String(summaryPeriod === period));
    periods.append(control);
  }
  head.append(copy, periods);
  const cards = document.createElement('div');
  cards.className = 'distribution-summary__grid';
  cards.append(
    metric('成交额', summaryMoney(section, section.period_paid_sales_minor, section.currency), status),
    metric('待结算佣金', summaryMoney(section, section.current_unsettled_minor, section.currency), status),
    metric('已结算佣金', summaryMoney(section, section.current_settled_minor, section.currency), status),
    metric('待处理异常订单', exceptionOrderNumber(section, section.current_exception_order_count), status),
  );
  summaryRoot.append(head, cards);
  if (overviewFailure) {
    const feedback = document.createElement('p');
    feedback.className = 'distribution-summary__feedback';
    feedback.append(document.createTextNode(overviewFailure), button('重新读取', () => loadOverview()));
    summaryRoot.append(feedback);
  }
  return summaryRoot;
}

async function loadOverview(): Promise<void> {
  const generation = ++overviewGeneration;
  const requestAccessGeneration = accessGeneration;
  overview = undefined;
  overviewFailure = '';
  overviewLoading = true;
  render();
  try {
    const response = overviewResponse(await request(`/api/admin/overview?period=${summaryPeriod}`));
    if (generation !== overviewGeneration || requestAccessGeneration !== accessGeneration) return;
    overview = response;
  } catch (error) {
    if (generation !== overviewGeneration || requestAccessGeneration !== accessGeneration) return;
    if (accessFailure(error, 401)) {
      invalidateSession();
      return;
    }
    const message = error instanceof Error ? error.message : '分销汇总读取失败。';
    overview = { distribution: { status: 'failed', reason_code: 'distribution_summary_unavailable' }, todos: { status: 'failed', reason_code: 'distribution_summary_unavailable', items: [] } };
    overviewFailure = `汇总读取失败：${message}`;
  } finally {
    if (generation !== overviewGeneration || requestAccessGeneration !== accessGeneration) return;
    overviewLoading = false;
    render();
  }
}

async function load(next = '', target: Tab = tab): Promise<LoadResult> {
  if (target !== tab) return 'stale';
  const targetGeneration = ++listGenerations[target];
  const requestAccessGeneration = accessGeneration;
  const state = pageState(target);
  state.loading = true;
  state.failure = '';
  if (target === tab) render();
  notice('正在读取服务端分销管理事实…');
  try {
    const payload = obj(await request(`/api/admin/distribution/${target}${next ? `?cursor=${encodeURIComponent(next)}` : ''}`));
    if (!Array.isArray(payload.items)) throw new Error('分销管理列表响应无效');
    if (targetGeneration !== listGenerations[target] || requestAccessGeneration !== accessGeneration || target !== tab) return 'stale';
    state.rows = payload.items.map(obj);
    state.cursor = text(payload.next_cursor, '');
    state.loading = false;
    render();
    notice('');
    return 'applied';
  } catch (error) {
    if (targetGeneration !== listGenerations[target] || requestAccessGeneration !== accessGeneration || target !== tab) return 'stale';
    if (accessFailure(error, 401)) {
      invalidateSession();
      return 'stale';
    }
    if (accessFailure(error, 403)) {
      revokeListAccess(target, error.message);
      return 'failed';
    }
    state.loading = false;
    state.failure = transientReadFailure(error);
    render();
    notice(state.failure, true);
    return 'failed';
  }
}

function currentPageRows(): Row[] {
  const state = pageState();
  const query = state.committedFilter.trim().toLocaleLowerCase();
  const rows = state.rows;
  if (!query) return rows;
  return rows.filter((row) => Object.values(row).some((value) => typeof value === 'string' && value.toLocaleLowerCase().includes(query)));
}

function commitCurrentPageFilter(input: HTMLInputElement): void {
  const state = pageState();
  state.draftFilter = input.value;
  state.committedFilter = input.value;
  render();
}

function saveCurrentFilterDraft(): void {
  const control = filterControls[tab];
  if (control) pageState().draftFilter = control.input.value;
}

function filterControl(value = tab): HTMLElement {
  const existing = filterControls[value];
  if (existing) return existing.element;
  const filter = document.createElement('section');
  filter.className = 'distribution-admin-filter';
  const input = document.createElement('input');
  input.type = 'search';
  input.value = pageState(value).draftFilter;
  input.placeholder = '按当前已加载记录筛选';
  input.setAttribute('aria-label', '仅筛选当前已加载页');
  input.addEventListener('input', () => commitCurrentPageFilter(input));
  filter.append(Object.assign(document.createElement('span'), { textContent: '当前页筛选' }));
  filter.append(input);
  filter.append(button('筛选', () => commitCurrentPageFilter(input)));
  filterControls[value] = { element: filter, input };
  return filter;
}

function ensurePage(): void {
  if (pageMounted) return;
  controlsHost.className = 'distribution-admin-controls';
  controlsHost.append(tabsHost);
  distributionRoot.replaceChildren(summaryHost, controlsHost, tableHost, paginationHost, messageHost);
  // The shell title is the page’s only title. Mount its actions once so table,
  // filter and summary redraws preserve header focus and an in-flight command.
  mountPageHeaderActions('distribution-admin', [
    { label: '申请二维码', onClick: () => showApplicationEntry() },
  ]);
  pageMounted = true;
}

function render(): void {
  ensurePage();
  summaryHost.replaceChildren(summary());
  const nav = document.createElement('nav');
  nav.className = 'distribution-tabs';
  for (const [value, label] of [['distributors', '分销员'], ['orders', '订单'], ['exceptions', '异常']] as const) {
    const control = button(label, () => { saveCurrentFilterDraft(); tab = value; void load('', value); }, tab === value);
    control.setAttribute('aria-pressed', String(tab === value));
    nav.append(control);
  }
  tabsHost.replaceChildren(nav);
  const currentFilter = filterControl();
  if (mountedFilter !== currentFilter) {
    mountedFilter?.remove();
    controlsHost.append(currentFilter);
    mountedFilter = currentFilter;
  }
  tableHost.replaceChildren(tab === 'distributors' ? distributors() : tab === 'orders' ? orders() : exceptions());
  paginationHost.replaceChildren();
  if (pageState().cursor) {
    const footer = document.createElement('footer');
    footer.className = 'distribution-pagination';
    footer.append(button('下一页', () => { void load(pageState().cursor); }));
    paginationHost.append(footer);
  }
}

function table(headers: string[], body: string): HTMLElement {
  const view = document.createElement('div');
  view.className = 'admin-table-wrap distribution-table-scroll';
  view.innerHTML = `<table class="admin-table"><thead><tr>${headers.map((header) => `<th>${esc(header)}</th>`).join('')}</tr></thead><tbody>${body}</tbody></table>`;
  return view;
}

function distributors(): HTMLElement {
  const shown = currentPageRows();
  const state = pageState();
  const empty = state.loading ? '正在读取分销员记录…' : state.committedFilter ? '当前已加载页没有匹配记录。' : state.failure || '暂无分销员记录。完成微信可信登录、同意协议并注册后，记录会出现在这里。';
  const body = shown.map((row) => `<tr><td>${esc(displayName(row.display_name))}</td><td>${esc(row.agreement_version)}</td><td>${row.enabled === true ? '启用' : row.enabled === false ? '停用' : '待确认'}</td><td>${esc(receiverStatusLabel(row.receiver_ready, row.receiver_status, row.receiver_status_label))}</td><td>${esc(timeText(row.registered_at))}</td><td><span data-actions="${integer(row.id) || ''}" data-version="${integer(row.version) || ''}"></span></td></tr>`).join('') || `<tr><td colspan="6">${empty}</td></tr>`;
  const view = table(['用户昵称', '协议', '状态', '收款状态', '注册时间', '操作'], body);
  for (const holder of view.querySelectorAll<HTMLElement>('[data-actions]')) {
    const id = integer(holder.dataset.actions);
    const version = integer(holder.dataset.version);
    if (id === undefined || id < 1 || version === undefined || version < 1) continue;
    const enabled = shown.find((row) => integer(row.id) === id)?.enabled === true;
    holder.append(button('查看详情', () => showDistributor(id)), button(enabled ? '停用' : '启用', () => setDistributor(id, version, enabled ? 'disable' : 'enable')));
  }
  return view;
}

function orders(): HTMLElement {
  const shown = currentPageRows();
  const state = pageState();
  const empty = state.loading ? '正在读取归因订单…' : state.committedFilter ? '当前已加载页没有匹配记录。' : state.failure || '暂无归因订单记录。';
  const body = shown.map((row) => `<tr><td>${esc(row.order_reference)}</td><td>${esc(row.product_name)}</td><td>${esc(displayName(row.distributor_display_name))}</td><td>${esc(qualifyLabel(row.qualification_state))}</td><td>${esc(row.qualification_evidence_reference)}</td><td>${esc(policyText(row.policy_version, row.rate_basis_points, row.wait_days))}</td><td>${esc(money(row.paid_minor, row.currency))}</td><td><span data-order="${integer(row.attribution_id) || ''}"></span></td></tr>`).join('') || `<tr><td colspan="8">${empty}</td></tr>`;
  const view = table(['订单', '商品', '分销员', '推广资格', '购买凭证', '佣金政策', '商品实付', '详情'], body);
  for (const holder of view.querySelectorAll<HTMLElement>('[data-order]')) {
    const id = integer(holder.dataset.order);
    if (id !== undefined && id > 0) holder.append(button('查看详情', () => showOrder(id)));
  }
  return view;
}

function exceptions(): HTMLElement {
  const shown = currentPageRows();
  const state = pageState();
  const empty = state.loading ? '正在读取异常记录…' : state.committedFilter ? '当前已加载页没有匹配记录。' : state.failure || '暂无异常记录。';
  const body = shown.map((row) => `<tr><td>${esc(idText(row.exception_id))}</td><td>${esc(displayName(row.distributor_display_name))}</td><td>${esc(row.order_reference)}</td><td>${esc(distributionExceptionLabel(row.kind))}</td><td>尚欠 ${esc(money(row.unpaid_due_minor, row.currency))}<br>系统分账成功确认 ${esc(money(row.already_paid_minor, row.currency))}</td><td>${esc(reasonLabel(row.reason))}</td><td><span data-exception="${esc(idText(row.exception_id))}" data-version="${integer(row.version) || ''}" data-reconcile="${String(row.can_reconcile === true)}" data-recovery="${String(row.can_record_recovery === true)}" data-liability="${String(row.can_record_merchant_liability === true)}"></span></td></tr>`).join('') || `<tr><td colspan="7">${empty}</td></tr>`;
  const view = table(['异常', '分销员', '订单', '类型', '资金事实', '原因', '人工处理'], body);
  for (const holder of view.querySelectorAll<HTMLElement>('[data-exception]')) {
    const id = holder.dataset.exception || '';
    const version = integer(holder.dataset.version);
    if (!id || version === undefined || version < 1) continue;
    holder.append(button('查看详情', () => showException(id)));
    if (holder.dataset.reconcile === 'true') holder.append(button('查询最新结果', () => reconcile(id, version)));
    if (holder.dataset.recovery === 'true') holder.append(button('登记追回', () => recovery(id, version)));
    if (holder.dataset.liability === 'true') holder.append(button('登记商户承担', () => liability(id, version)));
  }
  return view;
}

function distributorOrdersList(distributorID: number, first: Row): HTMLElement {
  const list = document.createElement('div');
  list.className = 'distribution-detail-list';
  list.append(Object.assign(document.createElement('h3'), { textContent: '关联推广订单' }));
  const seen = new Set<string>();
  const append = (page: Row): void => {
    const items = Array.isArray(page.items) ? page.items.map(obj) : undefined;
    if (!items) throw new Error('关联推广订单响应无效');
    for (const item of items) {
      const attributionID = idText(item.attribution_id);
      const orderID = integer(item.attribution_id);
      if (!attributionID || seen.has(attributionID) || orderID === undefined || orderID < 1) continue;
      seen.add(attributionID);
      list.append(button(`${text(item.order_reference)} · ${text(item.product_name)}`, () => showOrder(orderID)));
    }
    if (!seen.size) list.append(Object.assign(document.createElement('p'), { textContent: '暂无归因订单。' }));
    const next = text(page.next_cursor, '');
    if (!next) return;
    const more = button('加载更多订单', async () => {
      more.disabled = true;
      more.textContent = '正在读取更多订单…';
      try {
        const page = obj(await request(`/api/admin/distribution/distributors/${distributorID}/orders?limit=10&cursor=${encodeURIComponent(next)}`));
        if (!Array.isArray(page.items)) throw new Error('关联推广订单响应无效');
        more.remove();
        list.querySelector('[data-distribution-more-error]')?.remove();
        append(page);
      } catch (error) {
        more.disabled = false;
        more.textContent = '重试读取更多订单';
        let feedback = list.querySelector<HTMLElement>('[data-distribution-more-error]');
        if (!feedback) {
          feedback = document.createElement('p');
          feedback.dataset.distributionMoreError = '';
          feedback.className = 'distribution-error';
          list.append(feedback);
        }
        feedback.textContent = `读取更多关联订单失败：${error instanceof Error ? error.message : '请重试。'}`;
      }
    });
    list.append(more);
  };
  append(first);
  return list;
}

async function showDistributor(id: number): Promise<void> {
  const body = drawer('分销员详情', 'distributors');
  const generation = ++detailGeneration;
  const requestAccessGeneration = accessGeneration;
  try {
    const [detail, orderPage] = await Promise.all([request(`/api/admin/distribution/distributors/${id}`), request(`/api/admin/distribution/distributors/${id}/orders?limit=10`)]);
    if (!detailCanRender(body, generation, requestAccessGeneration)) return;
    const response = obj(detail);
    const distributor = obj(response.distributor);
    const earnings = obj(response.earnings);
    const orders = obj(orderPage);
    const grid = document.createElement('div');
    grid.className = 'distribution-detail-grid';
    grid.append(
      fact('用户昵称', displayName(distributor.display_name)), fact('分销编号', text(distributor.public_no, '待确认')), fact('协议版本', text(distributor.agreement_version)), fact('注册时间', timeText(distributor.registered_at)), fact('状态', distributor.enabled === true ? '启用' : distributor.enabled === false ? '停用' : '待确认'), fact('收款状态', receiverStatusLabel(distributor.receiver_ready, distributor.receiver_status, distributor.receiver_status_label)),
      fact('推广成交', money(earnings.gross_paid_sales_minor, earnings.currency)), fact('累计退款', money(earnings.successful_refunds_minor, earnings.currency)), fact('初始佣金', money(earnings.initial_commission_minor, earnings.currency)), fact('佣金调整', money(earnings.commission_adjustments_minor, earnings.currency)), fact('当前待付', money(earnings.unsettled_payable_minor, earnings.currency)), fact('系统分账成功确认', money(earnings.paid_commission_minor, earnings.currency)), fact('已追回', money(earnings.recovered_minor, earnings.currency)),
    );
    body.replaceChildren(grid, distributorOrdersList(id, orders));
  } catch (error) { handleDetailFailure(body, 'distributors', error, generation, requestAccessGeneration); }
}

async function showOrder(id: number): Promise<void> {
  const body = drawer('推广订单详情', 'orders');
  const generation = ++detailGeneration;
  const requestAccessGeneration = accessGeneration;
  try {
    const response = obj(await request(`/api/admin/distribution/orders/${id}`));
    if (!detailCanRender(body, generation, requestAccessGeneration)) return;
    const order = obj(response.order);
    const commission = obj(response.commission);
    const commissionCurrency = commission.currency;
    const grid = document.createElement('div');
    grid.className = 'distribution-detail-grid';
    grid.append(fact('订单', text(order.order_reference)), fact('商品', text(order.product_name)), fact('分销员', displayName(order.distributor_display_name)), fact('推广资格', qualifyLabel(order.qualification_state)), fact('购买凭证', text(order.qualification_evidence_reference)), fact('佣金政策', policyText(order.policy_version, order.rate_basis_points, order.wait_days)), fact('商品实付', money(order.paid_minor, order.currency)));
    if (Object.keys(commission).length) grid.append(fact('佣金状态', distributionCommissionStatusLabel(commission.status)), fact('初始佣金', money(commission.initial_minor, commission.currency)), fact('累计退款', money(commission.successful_refund_minor, commission.currency)), fact('当前应付', money(commission.current_payable_minor, commission.currency)), fact('系统分账成功确认', money(commission.paid_minor, commission.currency)), fact('订单支付确认', timeText(commission.paid_confirmed_at)), fact('预计结算时间', timeText(commission.due_at)));
    const adjustments = Array.isArray(response.adjustments) ? response.adjustments.map(obj) : [];
    const settlements = Array.isArray(response.settlements) ? response.settlements.map(obj) : [];
    const exceptions = Array.isArray(response.exceptions) ? response.exceptions.map(obj) : [];
    body.replaceChildren(
      grid,
      recordList('退款与资格调整', adjustments, (item) => record([['类型', distributionAdjustmentLabel(item.kind)], ['变动金额', money(item.delta_minor, commissionCurrency)], ['调整后应付', money(item.resulting_payable_minor, commissionCurrency)], ['原因', reasonLabel(item.reason)], ['凭证参考', text(item.source_reference)], ['发生时间', timeText(item.occurred_at)]])),
      recordList('结算记录', settlements, (item) => {
        const facts: [string, string][] = [['结算单', text(item.reference)], ['金额', money(item.amount_minor, item.currency)], ['状态', distributionSettlementStatusLabel(item.state)], ['最晚结算时间', timeText(item.provider_deadline_at)], ['创建时间', timeText(item.created_at)], ['记录更新时间', timeText(item.updated_at)]];
        if (item.state === 'receiver_succeeded') facts.splice(4, 0, ['系统分账成功时间', timeText(item.settlement_confirmed_at) === '—' ? '未记录' : timeText(item.settlement_confirmed_at)]);
        return record(facts);
      }),
      recordList('关联异常', exceptions, (item) => record([['异常', idText(item.exception_id)], ['类型', distributionExceptionLabel(item.kind)], ['金额', money(item.amount_minor, item.currency)], ['原因', reasonLabel(item.reason)], ['更新时间', timeText(item.updated_at)]], () => showException(idText(item.exception_id)))),
    );
  } catch (error) { handleDetailFailure(body, 'orders', error, generation, requestAccessGeneration); }
}

async function showException(id: string): Promise<void> {
  const body = drawer('异常详情', 'exceptions');
  const generation = ++detailGeneration;
  const requestAccessGeneration = accessGeneration;
  try {
    const response = obj(await request(`/api/admin/distribution/exceptions/${encodeURIComponent(id)}`));
    if (!detailCanRender(body, generation, requestAccessGeneration)) return;
    const grid = document.createElement('div');
    grid.className = 'distribution-detail-grid';
    const status = text(response.status);
    grid.append(
      fact('异常', idText(response.exception_id)), fact('分销员', displayName(response.distributor_display_name)), fact('订单', text(response.order_reference)), fact('类型', distributionExceptionLabel(response.kind)), fact('状态', status === 'open' ? '待处理' : status === 'resolved' ? '已处理' : status === 'recovery_recorded' ? '已登记追回' : status === 'merchant_liability_recorded' ? '已登记商户承担' : '状态待确认'), fact('尚欠应付', money(response.unpaid_due_minor, response.currency)), fact('系统分账成功确认', money(response.already_paid_minor, response.currency)), fact('原因', reasonLabel(response.reason)), fact('凭证参考', text(response.evidence_reference)), fact('处理来源', actorScopeLabel(response.actor_scope)), fact('创建时间', timeText(response.created_at)), fact('更新时间', timeText(response.updated_at)),
    );
    const audit = Array.isArray(response.audit) ? response.audit.map(obj) : [];
    body.replaceChildren(grid, recordList('处理审计', audit, (item) => record([['处理', auditEventLabel(item.event_type)], ['处理来源', actorScopeLabel(item.actor_scope)], ['金额', money(item.amount_minor, response.currency)], ['原因', reasonLabel(item.reason)], ['凭证参考', text(item.evidence_reference)], ['处理时间', timeText(item.occurred_at)]])));
  } catch (error) { handleDetailFailure(body, 'exceptions', error, generation, requestAccessGeneration); }
}

async function mutate(scope: string, path: string, body: string): Promise<void> {
  const targetTab = tab;
  if (pendingMutations.has(scope)) {
    notice('操作正在提交，请等待当前结果。');
    return;
  }
  pendingMutations.add(scope);
  try {
    await request(path, { method: 'POST', body }, scope);
    keys.delete(scope);
    const readback = await load('', targetTab);
    if (readback === 'failed') {
      notice('操作已提交，但最新服务端记录读取失败；请重新读取后确认。', true);
    } else if (readback === 'stale') {
      notice('操作已提交，但列表已切换或读取已过期；请重新读取后确认。', true);
    }
  } catch (error) {
    const readback = await load('', targetTab);
    const detail = error instanceof Error ? error.message : '请核对后再操作。';
    if (readback === 'applied') {
      notice(`提交结果未确认，已读取最新服务端记录：${detail}`, true);
    } else if (readback === 'failed') {
      notice(`提交结果仍未确认，最新服务端记录读取失败：${detail}`, true);
    } else {
      notice(`提交结果仍未确认，列表已切换或读取已过期：${detail}`, true);
    }
  } finally {
    pendingMutations.delete(scope);
  }
}

async function setDistributor(id: number, version: number, operation: 'disable' | 'enable'): Promise<void> {
  let reason = '管理员恢复';
  if (operation === 'disable') {
    const result = await confirmAction(`confirm:distributor:disable:${id}:${version}`, 'distributors', String(id), version, {
      title: '停用分销员',
      description: `即将停用分销员 ID ${id}。停用后该分销员不能继续推广。请填写原因，系统会记录审计。`,
      confirmLabel: '确认停用',
      tone: 'danger',
      fields: [{ name: 'reason', label: '停用原因', placeholder: '请输入停用原因', required: true, kind: 'textarea' }],
    });
    if (!result?.confirmed) return;
    reason = result.values?.reason || result.reason || '';
    if (!reason.trim()) {
      notice('必须填写停用原因，未提交操作。', true);
      return;
    }
  }
  await mutate(`distributor:${operation}:${id}:${version}:${reason}`, `/api/admin/distribution/distributors/${id}/${operation}`, JSON.stringify({ version, reason }));
}

async function reconcile(id: string, version: number): Promise<void> {
  await mutate(`reconcile:${id}:${version}`, `/api/admin/distribution/exceptions/${encodeURIComponent(id)}/reconcile`, JSON.stringify({ version }));
}

function amount(value: string | null): number | undefined {
  if (!/^\d+$/.test(value || '')) return undefined;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) && parsed > 0 ? parsed : undefined;
}

async function recovery(id: string, version: number): Promise<void> {
  const result = await confirmAction(`confirm:recovery:${id}:${version}`, 'exceptions', id, version, {
    title: '登记追回',
    description: `即将为异常编号 ${id} 登记追回。金额单位为分。请核对金额和凭证参考后再登记；此操作不会声明已完成微信分账。`,
    confirmLabel: '确认登记追回',
    tone: 'danger',
    fields: [
      { name: 'amount_minor', label: '追回金额（分）', placeholder: '请输入正整数（分）', required: true, kind: 'positive-integer' },
      { name: 'evidence_reference', label: '追回凭证参考', placeholder: '请输入凭证参考', required: true, kind: 'text' },
    ],
  });
  if (!result?.confirmed) return;
  const value = amount(result.values?.amount_minor || '');
  const evidence = result.values?.evidence_reference || '';
  if (value === undefined || !evidence?.trim()) {
    notice('追回金额必须是有效正整数分，且必须填写凭证参考；未提交操作。', true);
    return;
  }
  await mutate(`recovery:${id}:${version}:${value}:${evidence.trim()}`, `/api/admin/distribution/exceptions/${encodeURIComponent(id)}/recoveries`, JSON.stringify({ version, amount_minor: value, evidence_reference: evidence.trim() }));
}

async function liability(id: string, version: number): Promise<void> {
  const result = await confirmAction(`confirm:liability:${id}:${version}`, 'exceptions', id, version, {
    title: '登记商户承担',
    description: `即将为异常编号 ${id} 登记商户承担。金额单位为分。请核对金额和承担原因后再登记。`,
    confirmLabel: '确认登记商户承担',
    tone: 'danger',
    fields: [
      { name: 'amount_minor', label: '承担金额（分）', placeholder: '请输入正整数（分）', required: true, kind: 'positive-integer' },
      { name: 'reason', label: '承担原因', placeholder: '请输入承担原因', required: true, kind: 'textarea' },
    ],
  });
  if (!result?.confirmed) return;
  const value = amount(result.values?.amount_minor || '');
  const reason = result.values?.reason || '';
  if (value === undefined || !reason?.trim()) {
    notice('承担金额必须是有效正整数分，且必须填写原因；未提交操作。', true);
    return;
  }
  await mutate(`liability:${id}:${version}:${value}:${reason.trim()}`, `/api/admin/distribution/exceptions/${encodeURIComponent(id)}/merchant-liabilities`, JSON.stringify({ version, amount_minor: value, reason: reason.trim() }));
}

installCommittedTextSearch();
void load();
void loadOverview();
