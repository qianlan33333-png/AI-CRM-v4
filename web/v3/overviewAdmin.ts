import { openDetailDrawer } from './shared/ui/detailDrawer';
import { mountPageHeaderActions } from './shared/ui/pageHeaderActions';

type Period = 'today' | '7d' | '30d' | 'custom';
type OverviewTheme = 'business' | 'dark' | 'aurora';
type SectionStatus = 'ready' | 'zero' | 'data_missing' | 'failed';
type Money = { amount_minor: number; currency: string };
type Section = { status: SectionStatus; as_of: string; scope: string; reason_code?: string };
type TrendPoint = { date: string; gross: Money[]; order_count: number };
type Todo = { code: string; count: number; href: string };
type PaidRecord = { provider: 'wechat_pay' | 'wechat_shop'; order_reference: string; payer_customer_id: number | null; amount_minor: number; currency: string; paid_confirmed_at: string };
type PaidRecordsPage = { range: Overview['range']; items: PaidRecord[]; next_cursor: string };
type Overview = {
  range: { period: Period; timezone: string; start: string; end: string };
  paid: Section & { gross: Money[]; order_count: number; distinct_canonical_payers?: number; missing_payer_count?: number; missing_confirmation_evidence_count?: number; missing_confirmation_evidence_amount?: Money[]; trend: TrendPoint[] };
  customers: Section & { new_canonical_customers: number; historical_excluded: number; unknown_source_count?: number };
  refunds: Section & { completed_amount: Money[]; completed_count: number; missing_completion_evidence_count?: number; net_amount: Money[] | null };
  distribution: Section & { period_paid_sales_minor: number; period_initial_commission_minor: number; period_commission_count: number; current_unsettled_minor: number; current_settled_minor: number; currency: string };
  todos: Section & { items: Todo[] };
};
type Query = { period: Period; from?: string; to?: string };
type AccessState = 'none' | 'login' | 'forbidden';
type ViewState = { data: Overview | null; query: Query; customDraft: { from: string; to: string }; loading: boolean; stale: boolean; error: string; access: AccessState; requestID: number };

const root = document.querySelector<HTMLElement>('#overview-admin-root');
const state: ViewState = { data: null, query: { period: 'today' }, customDraft: { from: '', to: '' }, loading: false, stale: false, error: '', access: 'none', requestID: 0 };
const labels: Record<Period, string> = { today: '今日', '7d': '近 7 天', '30d': '近 30 天', custom: '自定义区间' };
const themeLabels: Record<OverviewTheme, string> = { business: '清爽', dark: '深色', aurora: '科技' };
const overviewThemeStorageKey = 'aicrm.overview.theme';
const allowedThemes = new Set<OverviewTheme>(Object.keys(themeLabels) as OverviewTheme[]);
const reasonMessages: Record<string, string> = {
  paid_confirmation_time_missing: '部分历史支付缺少确认时间，已确认部分仍会显示。',
  payer_customer_missing: '部分支付用户待确认。',
  net_paid_confirmation_time_missing: '净收款仅包含确认时间完整的支付记录。',
  net_paid_aggregate_unavailable: '净收款暂时无法计算，退款金额仍会显示。',
  refund_completed_at_missing: '部分退款完成时间待核实。',
  customer_creation_source_unknown: '部分新增用户暂待确认。',
  customer_provenance_aggregate_timeout: '用户来源读取超时，可稍后重试。',
  payment_aggregate_timeout: '已确认支付读取超时，可稍后重试。',
  canonical_payer_unavailable: '付款用户归并关系暂时无法确认，人数未显示。',
  refund_aggregate_timeout: '退款数据读取超时，可稍后重试。',
  distribution_aggregate_timeout: '分销数据读取超时，可稍后重试。',
  distribution_todo_aggregate_timeout: '待处理事项读取超时，可稍后重试。',
  distribution_not_configured: '分销数据暂未接入。',
};

function escapeHTML(value: string | number | undefined | null): string {
  return String(value ?? '').replace(/[&<>'"]/g, (character) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;' })[character] || character);
}
function isRecord(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null; }
function isSafeInteger(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value); }
function isSafeCount(value: unknown): value is number { return isSafeInteger(value) && value >= 0; }
function isCurrency(value: unknown): value is string { return typeof value === 'string' && /^[A-Z0-9]{1,16}$/.test(value); }
function isMoney(value: unknown): value is Money { return isRecord(value) && isSafeInteger(value.amount_minor) && isCurrency(value.currency); }
function isMoneyList(value: unknown): value is Money[] { return Array.isArray(value) && value.every(isMoney); }
function isOverviewRange(value: unknown): value is Overview['range'] {
  return isRecord(value) && ['today', '7d', '30d', 'custom'].includes(String(value.period)) && value.timezone === 'Asia/Shanghai'
    && typeof value.start === 'string' && !Number.isNaN(new Date(value.start).getTime())
    && typeof value.end === 'string' && !Number.isNaN(new Date(value.end).getTime());
}
function isSection(value: unknown): value is Section {
  return isRecord(value) && ['ready', 'zero', 'data_missing', 'failed'].includes(String(value.status)) && typeof value.as_of === 'string' && typeof value.scope === 'string' && (value.reason_code === undefined || typeof value.reason_code === 'string');
}
function isTrend(value: unknown): value is TrendPoint {
  return isRecord(value) && typeof value.date === 'string' && isMoneyList(value.gross) && isSafeCount(value.order_count);
}
function isPaid(value: unknown): value is Overview['paid'] {
  if (!isRecord(value)) return false;
  const record = value;
  return isSection(value) && isMoneyList(record.gross) && isSafeCount(record.order_count)
    && (record.distinct_canonical_payers === undefined ? record.status !== 'ready' && record.status !== 'zero' : isSafeCount(record.distinct_canonical_payers))
    && (record.missing_payer_count === undefined || isSafeCount(record.missing_payer_count))
    && (record.missing_confirmation_evidence_count === undefined || isSafeCount(record.missing_confirmation_evidence_count))
    && (record.missing_confirmation_evidence_amount === undefined || isMoneyList(record.missing_confirmation_evidence_amount))
    && Array.isArray(record.trend) && record.trend.every(isTrend);
}
function isCustomers(value: unknown): value is Overview['customers'] {
  if (!isRecord(value)) return false;
  const record = value;
  return isSection(value) && isSafeCount(record.new_canonical_customers) && isSafeCount(record.historical_excluded)
    && (record.unknown_source_count === undefined || isSafeCount(record.unknown_source_count));
}
function isRefunds(value: unknown): value is Overview['refunds'] {
  if (!isRecord(value)) return false;
  const record = value;
  return isSection(value) && isMoneyList(record.completed_amount) && isSafeCount(record.completed_count)
    && (record.missing_completion_evidence_count === undefined || isSafeCount(record.missing_completion_evidence_count))
    && (record.net_amount === null || isMoneyList(record.net_amount));
}
function isDistribution(value: unknown): value is Overview['distribution'] {
  if (!isRecord(value)) return false;
  const record = value;
  return isSection(value) && isSafeCount(record.period_paid_sales_minor) && isSafeCount(record.period_initial_commission_minor)
    && isSafeCount(record.period_commission_count) && isSafeCount(record.current_unsettled_minor) && isSafeCount(record.current_settled_minor) && isCurrency(record.currency);
}
function isTodos(value: unknown): value is Overview['todos'] {
  if (!isRecord(value)) return false;
  const record = value;
  return isSection(value) && Array.isArray(record.items)
    && record.items.every((item: unknown) => isRecord(item) && typeof item.code === 'string' && isSafeCount(item.count) && typeof item.href === 'string');
}
function isOverview(value: unknown): value is Overview {
  if (!isRecord(value) || !isOverviewRange(value.range)) return false;
  return isPaid(value.paid) && isCustomers(value.customers) && isRefunds(value.refunds) && isDistribution(value.distribution) && isTodos(value.todos);
}
function isPaidRecord(value: unknown): value is PaidRecord {
  return isRecord(value) && (value.provider === 'wechat_pay' || value.provider === 'wechat_shop')
    && typeof value.order_reference === 'string' && value.order_reference.trim().length > 0
    && (value.payer_customer_id === null || (isSafeInteger(value.payer_customer_id) && value.payer_customer_id > 0))
    && isSafeInteger(value.amount_minor) && value.amount_minor > 0 && isCurrency(value.currency)
    && typeof value.paid_confirmed_at === 'string' && !Number.isNaN(new Date(value.paid_confirmed_at).getTime());
}
function isPaidRecordsPage(value: unknown): value is PaidRecordsPage {
  return isRecord(value) && isOverviewRange(value.range) && Array.isArray(value.items) && value.items.length <= 25
    && value.items.every(isPaidRecord) && typeof value.next_cursor === 'string' && value.next_cursor.length <= 2048;
}
function unavailable(section: Section): boolean { return section.status === 'failed'; }
function storedTheme(): OverviewTheme {
  try {
    const value = window.localStorage.getItem(overviewThemeStorageKey);
    return allowedThemes.has(value as OverviewTheme) ? value as OverviewTheme : 'business';
  } catch { return 'business'; }
}
let overviewTheme = storedTheme();
let trendLayoutFrame = 0;
const trendResizeObserver = typeof ResizeObserver === 'function'
  ? new ResizeObserver(() => scheduleTrendLabelLayout())
  : null;
function applyTheme(): void {
  if (root) root.dataset.overviewTheme = overviewTheme;
}
function setTheme(next: OverviewTheme): void {
  if (!allowedThemes.has(next)) return;
  overviewTheme = next;
  applyTheme();
  try { window.localStorage.setItem(overviewThemeStorageKey, next); } catch { /* browser privacy settings retain the in-memory choice */ }
  syncThemeHeaderActions();
}
function amount(money: Money): string {
  const value = money.amount_minor / 100;
  try { return new Intl.NumberFormat('zh-CN', { style: 'currency', currency: money.currency, currencyDisplay: 'narrowSymbol' }).format(value); } catch { return `${money.currency} ${value.toFixed(2)}`; }
}
function amounts(values: Money[] | null | undefined, section: Section, confirmedEmpty = ''): string {
  if (unavailable(section)) return '—';
  if (!values?.length) {
    if (section.status === 'zero') return confirmedEmpty || '0（本期无币种记录）';
    if (section.status === 'ready' && confirmedEmpty) return confirmedEmpty;
    return '—';
  }
  return values.map(amount).join(' · ');
}
function minorAmount(value: number, currency: string, section: Section): string {
  return unavailable(section) || (section.status === 'data_missing' && section.reason_code === 'distribution_not_configured') ? '—' : amount({ amount_minor: value, currency });
}
function integer(value: number | undefined, section: Section): string { return unavailable(section) || !isSafeCount(value) ? '—' : new Intl.NumberFormat('zh-CN').format(value); }
function timestamp(value: string): string {
  const date = new Date(value); if (Number.isNaN(date.getTime())) return '—';
  return new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', month: 'numeric', day: 'numeric', hour: '2-digit', minute: '2-digit', hour12: false }).format(date);
}
function statusBadge(section: Section): string {
  if (section.status === 'ready' || section.status === 'zero') return '';
  if (section.status === 'data_missing') {
    const hint = reasonMessages[section.reason_code || ''] || '部分数据待确认。';
    return `<span class="overview-status overview-status--data_missing" tabindex="0" role="img" aria-label="数据待确认" aria-description="${escapeHTML(hint)}" title="${escapeHTML(hint)}">?</span>`;
  }
  return '<span class="overview-status overview-status--failed">读取失败</span>';
}
function failureHint(section: Section): string {
  return section.status === 'failed' && section.reason_code && reasonMessages[section.reason_code]
    ? reasonMessages[section.reason_code]
    : '';
}
function metric(title: string, value: string, section: Section, action = ''): string {
  const summary = section.status === 'failed' ? failureHint(section) : '';
  const density = value.length > 14 ? ' overview-metric__value--compact' : '';
  const scrollable = value.length > 22 ? ' overview-metric__value--scrollable' : '';
  return `<article class="overview-metric"><div class="overview-metric__head"><span>${escapeHTML(title)}</span>${statusBadge(section)}</div><strong class="overview-metric__value${density}${scrollable}">${escapeHTML(value)}</strong>${summary ? `<p>${escapeHTML(summary)}</p>` : ''}${action}</article>`;
}
function customRangeControls(): string {
  if (state.query.period !== 'custom') return '';
  return `<section class="overview-range overview-range--custom" aria-label="自定义统计区间"><form class="overview-range__custom is-open" data-overview-custom><label>开始日期<input name="from" type="date" value="${escapeHTML(state.customDraft.from)}"></label><label>结束日期<input name="to" type="date" value="${escapeHTML(state.customDraft.to)}"></label><button type="submit" class="admin-button admin-button--secondary">应用</button></form></section>`;
}
function beijingDate(value: string, inclusiveEnd = false): string | null {
  const timestampValue = new Date(value).getTime();
  if (Number.isNaN(timestampValue)) return null;
  const date = new Date(inclusiveEnd ? timestampValue - 1 : timestampValue);
  const parts = new Intl.DateTimeFormat('en-CA', { timeZone: 'Asia/Shanghai', year: 'numeric', month: '2-digit', day: '2-digit' }).formatToParts(date);
  const part = (type: Intl.DateTimeFormatPartTypes) => parts.find((item) => item.type === type)?.value;
  const year = part('year'); const month = part('month'); const day = part('day');
  return year && month && day ? `${year}-${month}-${day}` : null;
}
function actualRangeDates(data: Overview): string | null {
  const start = beijingDate(data.range.start);
  const end = beijingDate(data.range.end, true);
  return start && end ? `北京时间 ${start} 至 ${end}` : null;
}
function actualRangeLabel(data: Overview): string {
  const dates = actualRangeDates(data);
  return `${labels[data.range.period]}${dates ? `（${dates}）` : ''}`;
}
function staleRangeNotice(data: Overview): string {
  if (rangeMatches(data) && !state.loading && !state.stale) return '';
  return `<p class="overview-snapshot">当前展示：${escapeHTML(actualRangeLabel(data))}（上次成功读取）</p>`;
}
function rangeMatches(data: Overview): boolean {
  if (data.range.period !== state.query.period) return false;
  if (state.query.period !== 'custom') return true;
  return beijingDate(data.range.start) === state.query.from && beijingDate(data.range.end, true) === state.query.to;
}
function sameReportingRange(left: Overview['range'], right: Overview['range']): boolean {
  return left.timezone === right.timezone && new Date(left.start).getTime() === new Date(right.start).getTime() && new Date(left.end).getTime() === new Date(right.end).getTime();
}
function rangeDayCount(range: Overview['range']): number | null {
  const start = beijingDate(range.start);
  const end = beijingDate(range.end, true);
  if (!start || !end) return null;
  const first = new Date(`${start}T00:00:00Z`);
  const last = new Date(`${end}T00:00:00Z`);
  const dayCount = Math.round((last.getTime() - first.getTime()) / 86_400_000) + 1;
  return dayCount > 0 ? dayCount : null;
}
function completeReadyTrend(points: TrendPoint[], section: Section, range: Overview['range']): TrendPoint[] {
  if (section.status !== 'ready' && section.status !== 'zero') return points;
  const start = beijingDate(range.start);
  const dayCount = rangeDayCount(range);
  if (!start || !dayCount || dayCount > 31) return points;
  const first = new Date(`${start}T00:00:00Z`);
  const last = new Date(first);
  last.setUTCDate(last.getUTCDate() + dayCount - 1);
  const byDate = new Map(points.map((point) => [point.date, point]));
  const result: TrendPoint[] = [];
  for (let cursor = new Date(first); cursor <= last; cursor.setUTCDate(cursor.getUTCDate() + 1)) {
    const date = cursor.toISOString().slice(0, 10);
    result.push(byDate.get(date) || { date, gross: [], order_count: 0 });
  }
  return result;
}
function errorMessage(error: unknown): string {
  return isRecord(error) && typeof error.message === 'string' && error.message.trim()
    ? error.message
    : '读取经营数据失败，请稍后重试。';
}
function renderTrend(source: TrendPoint[], section: Section, range: Overview['range']): string {
  const points = completeReadyTrend(source, section, range);
  if (unavailable(section)) return '<p class="overview-empty">支付趋势暂时无法读取。</p>';
  if (!points.length) return `<p class="overview-empty">${section.status === 'data_missing' ? '暂无可用趋势数据。' : '该区间已确认无支付趋势记录。'}</p>`;
  const currencies = [...new Set(points.flatMap((point) => point.gross.map((money) => money.currency)))];
  if (!currencies.length) return `<p class="overview-empty">${section.status === 'data_missing' ? '暂无可用趋势数据。' : '该区间已确认无支付趋势记录。'}</p>`;
  const chartCurrency = currencies.length === 1 ? currencies[0] : '';
  const values = chartCurrency ? points.map((point) => Math.max(0, point.gross.find((money) => money.currency === chartCurrency)?.amount_minor || 0)) : [];
  const max = Math.max(...values, 0);
  const plotHeight = 112;
  const chart = chartCurrency ? `<div class="overview-chart${points.length > 7 ? ' overview-chart--dense' : ''}" aria-label="${escapeHTML(chartCurrency)} 支付趋势图">${points.map((point, index) => {
    const money = point.gross.find((item) => item.currency === chartCurrency) || { amount_minor: 0, currency: chartCurrency };
    const barHeight = values[index] > 0 && max > 0 ? Math.max(8, Math.round((values[index] / max) * plotHeight)) : 0;
    const visibleDate = points.length <= 7 || index % 5 === 0 || index === points.length - 1;
    const visibleAmount = points.length <= 7 && barHeight ? escapeHTML(amount(money)) : '';
    const title = escapeHTML(amount(money));
    return `<div class="overview-chart__column"><span class="overview-chart__value">${visibleAmount}</span><span class="overview-chart__plot">${barHeight ? `<svg class="overview-chart__bar" viewBox="0 0 100 ${plotHeight}" width="100" height="${plotHeight}" preserveAspectRatio="none" aria-label="${title}"><title>${title}</title><rect x="0" y="${plotHeight - barHeight}" width="100" height="${barHeight}" rx="5"></rect></svg>` : ''}</span><time>${visibleDate ? escapeHTML(point.date.slice(5)) : ''}</time></div>`;
  }).join('')}</div>` : '<p class="overview-panel__hint">该区间存在多种币种，未合并换算趋势。</p>';
  const rows = points.map((point) => `<tr><td>${escapeHTML(point.date)}</td><td>${escapeHTML(amounts(point.gross, section, '0（当日无确认支付）'))}</td><td>${integer(point.order_count, section)}</td></tr>`).join('');
  const table = `<div class="overview-trend-table-wrap"><table class="overview-trend-table"><thead><tr><th>日期</th><th>已确认支付</th><th>订单</th></tr></thead><tbody>${rows}</tbody></table></div>`;
  return `${chart}${points.length > 7 ? `<details class="overview-trend-details"><summary>查看每日明细（${points.length} 天）</summary>${table}</details>` : table}`;
}
function safeTodoHref(raw: string): string | null {
  try { const target = new URL(raw, window.location.origin); return target.origin === window.location.origin && target.pathname.startsWith('/admin/') ? target.pathname + target.search + target.hash : null; } catch { return null; }
}
function renderTodos(items: Todo[], section: Section): string {
  if (unavailable(section)) return '<p class="overview-empty">待处理事项暂时无法读取。</p>';
  if (section.status === 'data_missing' && section.reason_code === 'distribution_not_configured') return '<p class="overview-empty">分销数据暂未接入，暂无法确认待处理事项。</p>';
  if (!items.length) return '<p class="overview-empty">暂无需要处理的事项。</p>';
  return `<ul class="overview-todos">${items.map((todo) => { const href = safeTodoHref(todo.href); const label = todo.code === 'distribution_exceptions' ? '分销异常待处理' : '待处理事项'; const contents = `<span>${escapeHTML(label)}</span><strong>${integer(todo.count, section)}</strong>`; return `<li>${href ? `<a href="${escapeHTML(href)}">${contents}<span aria-hidden="true">→</span></a>` : contents}</li>`; }).join('')}</ul>`;
}
function paidRecordsAction(data: Overview): string {
  if (!rangeMatches(data) || state.loading || state.stale || data.paid.status === 'failed') return '';
  return '<button type="button" class="overview-metric__action admin-button admin-button--secondary" data-overview-paid-records>查看支付记录</button>';
}
function renderData(data: Overview): string {
  const { paid, customers, refunds, distribution, todos } = data;
  return `${staleRangeNotice(data)}<div class="overview-dashboard">
    <section class="overview-metrics overview-metrics--primary" aria-label="核心经营指标">${metric('已确认支付', amounts(paid.gross, paid), paid, paidRecordsAction(data))}${metric('支付订单', integer(paid.order_count, paid), paid)}${metric('支付用户', integer(paid.distinct_canonical_payers, paid), paid)}${metric('新增用户', integer(customers.new_canonical_customers, customers), customers)}</section>
    <section class="overview-metrics overview-metrics--secondary" aria-label="补充经营指标">${metric('完成退款', amounts(refunds.completed_amount, refunds, '0（本期无退款）'), refunds)}${metric('净收款', amounts(refunds.net_amount, refunds), refunds)}</section>
    <section class="overview-panels"><article class="overview-panel overview-panel--wide"><div class="overview-panel__head"><h2>支付趋势</h2>${statusBadge(paid)}</div>${renderTrend(paid.trend, paid, data.range)}${failureHint(paid) ? `<p class="overview-panel__hint">${escapeHTML(failureHint(paid))}</p>` : ''}</article>
      <article class="overview-panel"><div class="overview-panel__head"><h2>分销进度</h2>${statusBadge(distribution)}</div><dl class="overview-facts"><div><dt>区间支付业绩</dt><dd>${escapeHTML(minorAmount(distribution.period_paid_sales_minor, distribution.currency, distribution))}</dd></div><div><dt>区间初始佣金</dt><dd>${escapeHTML(minorAmount(distribution.period_initial_commission_minor, distribution.currency, distribution))}</dd></div><div><dt>当前待结算</dt><dd>${escapeHTML(minorAmount(distribution.current_unsettled_minor, distribution.currency, distribution))}</dd></div><div><dt>当前已结算</dt><dd>${escapeHTML(minorAmount(distribution.current_settled_minor, distribution.currency, distribution))}</dd></div></dl>${failureHint(distribution) ? `<p class="overview-panel__hint">${escapeHTML(failureHint(distribution))}</p>` : ''}</article>
      <article class="overview-panel"><div class="overview-panel__head"><h2>待处理事项</h2>${statusBadge(todos)}</div>${renderTodos(todos.items, todos)}${failureHint(todos) ? `<p class="overview-panel__hint">${escapeHTML(failureHint(todos))}</p>` : ''}</article></section></div>`;
}
function initialPaidRecordsURL(data: Overview): string | null {
  const from = beijingDate(data.range.start);
  const to = beijingDate(data.range.end, true);
  if (!from || !to) return null;
  const url = new URL('/api/admin/overview/paid-records', window.location.origin);
  // The overview itself may remain open across midnight. A custom first page
  // binds the drawer to its already-rendered half-open range rather than
  // asking Payment to recalculate a fresh relative "today" range.
  url.searchParams.set('period', 'custom');
  url.searchParams.set('from', from);
  url.searchParams.set('to', to);
  return url.pathname + url.search;
}
function paidRecordDetailHref(item: PaidRecord): string | null {
  const provider = item.provider === 'wechat_pay' ? 'wechat' : item.provider === 'wechat_shop' ? 'wechat_shop' : '';
  if (!provider) return null;
  const url = new URL('/admin/orderDetail.html', window.location.origin);
  url.searchParams.set('id', item.order_reference);
  url.searchParams.set('provider', provider);
  return url.pathname + url.search;
}
function renderPaidRecordRows(items: PaidRecord[], loaded: boolean): string {
  if (!loaded && !items.length) return '';
  if (!items.length) return '<p class="overview-empty">该统计区间已确认无支付记录。</p>';
  return `<ol class="overview-paid-records">${items.map((item) => {
    const href = paidRecordDetailHref(item);
    const payer = item.payer_customer_id === null ? '付款时用户待确认' : `付款时用户 #${item.payer_customer_id}`;
    const order = href ? `<a href="${escapeHTML(href)}">查看订单</a>` : '<span>订单详情暂不可定位</span>';
    return `<li><div class="overview-paid-records__head"><strong>${escapeHTML(amount({ amount_minor: item.amount_minor, currency: item.currency }))}</strong>${order}</div><dl><div><dt>支付确认时间</dt><dd>${escapeHTML(timestamp(item.paid_confirmed_at))}</dd></div><div><dt>付款时用户</dt><dd>${escapeHTML(payer)}</dd></div><div><dt>订单号</dt><dd>${escapeHTML(item.order_reference)}</dd></div></dl></li>`;
  }).join('')}</ol>`;
}
function openPaidRecordsDrawer(data: Overview): void {
  const initialURL = initialPaidRecordsURL(data);
  if (!initialURL) return;
  const body = document.createElement('section');
  body.className = 'overview-paid-records-drawer';
  const dialog = openDetailDrawer(`支付记录 · ${actualRangeLabel(data)}`, body);
  let records: PaidRecord[] = [];
  let nextCursor = '';
  let loading = false;
  let error = '';
  let accessDenied = false;
  let loaded = false;
  let requestID = 0;
  let closed = false;
  let lastURL = initialURL;
  const partialNote = data.paid.status === 'data_missing' && data.paid.missing_confirmation_evidence_count
    ? '仅展示可定位到当前统计区间的支付记录；缺少确认时间的历史支付未计入本明细。'
    : '记录按系统确认支付时间排序。付款时用户是历史事实，不代表当前归并用户。';
  const renderDrawer = (): void => {
    const feedback = loading ? '<p class="overview-paid-records-drawer__loading" aria-live="polite">正在读取支付记录…</p>' : '';
    const failure = error ? `<div class="overview-error" role="alert"><span>${escapeHTML(error)}</span>${accessDenied ? '' : '<button type="button" class="admin-button admin-button--secondary" data-overview-paid-records-retry>重试</button>'}</div>` : '';
    const more = !loading && !accessDenied && nextCursor ? '<button type="button" class="admin-button admin-button--secondary" data-overview-paid-records-more>加载更多</button>' : '';
    body.innerHTML = `<p class="overview-paid-records-drawer__hint">${escapeHTML(partialNote)}</p>${feedback}${failure}${renderPaidRecordRows(records, loaded)}${more ? `<div class="overview-paid-records-drawer__more">${more}</div>` : ''}`;
    body.querySelector<HTMLButtonElement>('[data-overview-paid-records-retry]')?.addEventListener('click', () => { void load(lastURL, lastURL !== initialURL); });
    body.querySelector<HTMLButtonElement>('[data-overview-paid-records-more]')?.addEventListener('click', () => {
      const url = new URL('/api/admin/overview/paid-records', window.location.origin);
      url.searchParams.set('cursor', nextCursor);
      void load(url.pathname + url.search, true);
    });
  };
  const load = async (url: string, append: boolean): Promise<void> => {
    const currentRequest = ++requestID;
    loading = true; error = ''; accessDenied = false; lastURL = url; renderDrawer();
    try {
      const response = await fetch(url, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
      const payload = await response.json().catch(() => null);
      if (response.status === 401 || response.status === 403) {
        if (closed || currentRequest !== requestID) return;
        records = []; nextCursor = ''; loaded = false; accessDenied = true;
        throw new Error(response.status === 401 ? '登录状态已失效，请重新登录后再查看支付记录。' : '当前账号没有查看支付记录的权限。');
      }
      if (!response.ok) throw new Error(`读取支付记录失败（HTTP ${response.status}）`);
      if (!isPaidRecordsPage(payload) || !sameReportingRange(payload.range, data.range)) throw new Error('支付记录格式或统计区间无效，请稍后重试。');
      if (closed || currentRequest !== requestID) return;
      records = append ? [...records, ...payload.items] : payload.items; loaded = true;
      nextCursor = payload.next_cursor;
    } catch (readError) {
      if (closed || currentRequest !== requestID) return;
      error = errorMessage(readError);
    } finally {
      if (!closed && currentRequest === requestID) { loading = false; renderDrawer(); }
    }
  };
  dialog.addEventListener('close', () => { closed = true; requestID += 1; }, { once: true });
  renderDrawer();
  void load(initialURL, false);
}
function accessPanel(): string {
  if (state.access === 'login') return '<section class="overview-empty-state"><strong>登录状态已失效</strong><p>请重新登录后再查看经营数据。</p><a class="admin-button admin-button--primary" href="/login?next=%2Fadmin">重新登录</a></section>';
  if (state.access === 'forbidden') return '<section class="overview-empty-state"><strong>暂无查看权限</strong><p>请联系管理员确认后台访问范围。</p></section>';
  return '';
}
function render(): void {
  if (!root) return;
  applyTheme();
  root.setAttribute('aria-busy', String(state.loading));
  const loading = state.loading ? '<span class="overview-feedback">正在更新数据…</span>' : '';
  const error = state.error ? `<div class="overview-error" role="alert"><span>${escapeHTML(state.error)}</span>${state.access === 'none' ? '<button type="button" class="admin-button admin-button--secondary" data-overview-retry>重试</button>' : ''}</div>` : '';
  const content = state.access !== 'none' ? accessPanel() : state.data ? renderData(state.data) : '<section class="overview-empty-state"><strong>暂未读取到经营数据</strong><p>请重试后再查看。</p></section>';
  root.innerHTML = `<div class="overview-admin">${loading ? `<p class="overview-read-status" role="status">${loading}</p>` : ''}${customRangeControls()}${error}${content}</div>`;
  observeTrendCharts();
  syncTrendLabelLayout();
  syncRangeHeaderActions();
}
async function load(query: Query): Promise<void> {
  if (!root) return;
  const requestID = ++state.requestID;
  state.query = query; state.loading = true; state.stale = state.data !== null; state.error = ''; state.access = 'none'; render();
  const params = new URLSearchParams({ period: query.period });
  if (query.period === 'custom') { params.set('from', query.from || ''); params.set('to', query.to || ''); }
  try {
    const response = await fetch(`/api/admin/overview?${params.toString()}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } });
    if (response.status === 401 || response.status === 403) {
      if (requestID !== state.requestID) return;
      state.data = null; state.stale = false; state.access = response.status === 401 ? 'login' : 'forbidden'; state.error = response.status === 401 ? '登录状态已失效。' : '当前账号没有查看经营总览的权限。'; return;
    }
    const payload = await response.json().catch(() => null);
    if (!response.ok) throw new Error(`读取经营数据失败（HTTP ${response.status}）`);
    if (!isOverview(payload)) throw new Error('经营数据格式无效，请稍后重试。');
    if (requestID !== state.requestID) return;
    state.data = payload; state.stale = false; state.query = { period: payload.range.period, from: query.from, to: query.to };
  } catch (error) {
    if (requestID !== state.requestID) return;
    state.stale = state.data !== null;
    state.error = errorMessage(error);
  } finally { if (requestID === state.requestID) { state.loading = false; render(); } }
}
function applyCustom(form: HTMLFormElement): void {
  const formData = new FormData(form); state.customDraft = { from: String(formData.get('from') || ''), to: String(formData.get('to') || '') };
  if (!state.customDraft.from || !state.customDraft.to || state.customDraft.to < state.customDraft.from) { state.error = '请选择有效的开始和结束日期。'; render(); return; }
  void load({ period: 'custom', ...state.customDraft });
}
function openCustomDraft(): void {
  // A draft is an explicit user intent, rather than a request. Invalidate an
  // older preset response so it cannot close the form or relabel retained data.
  state.requestID += 1;
  state.query = { period: 'custom', ...state.customDraft };
  state.loading = false;
  state.stale = state.data !== null;
  state.error = '';
  state.access = 'none';
  render();
}

function choosePeriod(period: Period): void {
  if (period === 'custom') { openCustomDraft(); return; }
  void load({ period });
}

function syncRangeHeaderActions(): void {
  for (const period of Object.keys(labels) as Period[]) {
    const control = document.querySelector<HTMLButtonElement>(`[data-page-header-actions="overview-range"] [data-page-header-action="period-${period}"]`);
    if (!control) continue;
    const selected = state.query.period === period;
    control.classList.toggle('is-active', selected);
    control.setAttribute('aria-pressed', String(selected));
  }
}

function syncThemeHeaderActions(): void {
  for (const theme of Object.keys(themeLabels) as OverviewTheme[]) {
    const control = document.querySelector<HTMLButtonElement>(`[data-page-header-actions="overview-theme"] [data-page-header-action="theme-${theme}"]`);
    if (!control) continue;
    const selected = overviewTheme === theme;
    control.classList.toggle('is-active', selected);
    control.setAttribute('aria-pressed', String(selected));
  }
}

function syncTrendLabelLayout(): void {
  if (!root) return;
  for (const chart of Array.from(root.querySelectorAll<HTMLElement>('.overview-chart:not(.overview-chart--dense)'))) {
    chart.style.removeProperty('--overview-chart-column-min');
    const labels = Array.from(chart.querySelectorAll<HTMLElement>('.overview-chart__value')).filter((label) => label.textContent?.trim());
    if (!labels.length) continue;
    const required = labels.map((label) => {
      const range = document.createRange();
      range.selectNodeContents(label);
      const naturalWidth = typeof range.getBoundingClientRect === 'function' ? range.getBoundingClientRect().width : 0;
      const labelWidth = label.getBoundingClientRect().width;
      const overflowed = naturalWidth > labelWidth + 0.5 || label.scrollWidth > label.clientWidth + 1;
      return overflowed && naturalWidth > 0 ? Math.ceil(naturalWidth) + 8 : 0;
    });
    const widest = Math.max(...required);
    // The chart owns horizontal scrolling on narrow viewports. Only an actual
    // label overflow expands its independent label lane; an already-fitting
    // chart retains its native layout and compact bars.
    if (widest > 0) chart.style.setProperty('--overview-chart-column-min', `${Math.max(58, widest)}px`);
  }
}

function scheduleTrendLabelLayout(): void {
  if (trendLayoutFrame) return;
  trendLayoutFrame = window.requestAnimationFrame(() => {
    trendLayoutFrame = 0;
    syncTrendLabelLayout();
  });
}

function observeTrendCharts(): void {
  if (!root || !trendResizeObserver) return;
  trendResizeObserver.disconnect();
  for (const chart of Array.from(root.querySelectorAll<HTMLElement>('.overview-chart:not(.overview-chart--dense)'))) trendResizeObserver.observe(chart);
}

function mountRangeHeaderActions(): void {
  mountPageHeaderActions('overview-range', (Object.keys(labels) as Period[]).map((period) => ({
    id: `period-${period}`,
    label: period === 'custom' ? '自定义' : labels[period],
    variant: period === 'custom' ? 'secondary' : undefined,
    onClick: () => choosePeriod(period),
  })));
  syncRangeHeaderActions();
}

function mountThemeHeaderActions(): void {
  mountPageHeaderActions('overview-theme', (Object.keys(themeLabels) as OverviewTheme[]).map((theme) => ({
    id: `theme-${theme}`,
    label: themeLabels[theme],
    variant: 'ghost',
    onClick: () => setTheme(theme),
  })));
  syncThemeHeaderActions();
}

if (root) {
  root.addEventListener('click', (event) => {
    const target = (event.target as Element | null)?.closest<HTMLElement>('[data-overview-retry], [data-overview-paid-records]');
    if (!target) return;
    if (target.hasAttribute('data-overview-retry')) { void load(state.query); return; }
    if (state.data && rangeMatches(state.data) && !state.loading && !state.stale && state.data.paid.status !== 'failed') openPaidRecordsDrawer(state.data);
  });
  root.addEventListener('submit', (event) => { const form = (event.target as Element | null)?.closest<HTMLFormElement>('[data-overview-custom]'); if (!form) return; event.preventDefault(); applyCustom(form); });
  window.addEventListener('resize', scheduleTrendLabelLayout, { passive: true });
  mountRangeHeaderActions();
  mountThemeHeaderActions();
  void load({ period: 'today' });
}
