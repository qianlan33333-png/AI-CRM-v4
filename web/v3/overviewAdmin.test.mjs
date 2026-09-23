import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./overviewAdmin.ts', import.meta.url)));
const delay = (milliseconds = 10) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) { for (let attempt = 0; attempt < 100; attempt += 1) { if (check()) return; await delay(); } throw new Error(message); }
const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
function overview(period, { amount = 12500, refund = 1200 } = {}) {
  const section = { status: 'ready', as_of: '2026-09-15T02:00:00Z', scope: 'admin_authorized_global' };
  const days = period === '30d' ? 30 : period === '7d' ? 7 : 1;
  const rangeStart = new Date(Date.UTC(2026, 8, 15 - days, 16)).toISOString();
  const rangeEnd = new Date(Date.UTC(2026, 8, 15, 16)).toISOString();
  const sourceDays = days === 1 ? [0] : [0, Math.floor(days / 2), days - 1];
  const dailyAmount = Math.floor(amount / sourceDays.length);
  const trend = sourceDays.map((offset, index) => {
    const date = new Date(Date.UTC(2026, 8, 15 - (days - 1) + offset)).toISOString().slice(0, 10);
    const amountMinor = index === sourceDays.length - 1 ? amount - dailyAmount * (sourceDays.length - 1) : dailyAmount;
    return { date, gross: [{ amount_minor: amountMinor, currency: 'CNY' }], order_count: amountMinor > 0 ? 1 : 0 };
  });
  return {
    range: { period, timezone: 'Asia/Shanghai', start: rangeStart, end: rangeEnd },
    contract: { scope: 'admin_authorized_global' },
    paid: { ...section, gross: [{ amount_minor: amount, currency: 'CNY' }], order_count: 2, distinct_canonical_payers: 1, missing_confirmation_evidence_count: 1, trend },
    customers: { ...section, status: 'data_missing', reason_code: 'customer_creation_source_unknown', new_canonical_customers: 1, historical_excluded: 2, unknown_source_count: 3 },
    refunds: { ...section, completed_amount: refund ? [{ amount_minor: refund, currency: 'CNY' }] : [], completed_count: refund ? 1 : 0, net_amount: [{ amount_minor: amount - refund, currency: 'CNY' }] },
    distribution: { ...section, period_paid_sales_minor: amount, period_initial_commission_minor: 1600, period_commission_count: 1, current_unsettled_minor: 900, current_settled_minor: 700, currency: 'CNY' },
    todos: { ...section, items: [{ code: 'distribution_exceptions', count: 2, href: '/admin/distribution' }] },
  };
}
let scenario = 'today';
let releaseDeferred;
const calls = [];
let releasePaidRecords;
let paidRecordsMoreAttempts = 0;
function paidRecordsPage(cursor = '') {
  const range = { period: 'custom', timezone: 'Asia/Shanghai', start: '2026-09-14T16:00:00Z', end: '2026-09-15T16:00:00Z' };
  if (cursor) return { range, items: [{ provider: 'wechat_shop', order_reference: 'M-OVERVIEW-COLLISION', payer_customer_id: null, amount_minor: 2500, currency: 'CNY', paid_confirmed_at: '2026-09-15T01:30:00Z' }], next_cursor: '' };
  return { range, items: [{ provider: 'wechat_pay', order_reference: 'M-OVERVIEW-COLLISION', payer_customer_id: 17, amount_minor: 10000, currency: 'CNY', paid_confirmed_at: '2026-09-15T02:00:00Z' }], next_cursor: 'next-paid-records-page' };
}
const dom = new JSDOM('<!doctype html><header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">经营总览</h1></div></header><main id="overview-admin-root"></main>', {
  url: 'https://crm.example/admin', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    window.HTMLDialogElement.prototype.showModal = function showModal() { this.open = true; };
    window.HTMLDialogElement.prototype.close = function close() { this.open = false; this.dispatchEvent(new window.Event('close')); };
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href); calls.push(url);
      if (url.pathname === '/api/admin/overview/paid-records') {
        if (scenario === 'paid-records-pending') return new Promise((resolve) => { releasePaidRecords = () => resolve(reply(paidRecordsPage(url.searchParams.get('cursor') || ''))); });
        if (scenario === 'paid-records-unavailable') return reply({ error: { message: 'unavailable' } }, 503);
        if (scenario === 'paid-records-forbidden') return reply({ error: { message: 'permission_denied' } }, 403);
        if (scenario === 'paid-records-more-forbidden' && url.searchParams.get('cursor')) return reply({ error: { message: 'permission_denied' } }, 403);
        if (scenario === 'paid-records-more-fail-once' && url.searchParams.get('cursor')) { paidRecordsMoreAttempts += 1; if (paidRecordsMoreAttempts === 1) return reply({ error: { message: 'unavailable' } }, 503); }
        return reply(paidRecordsPage(url.searchParams.get('cursor') || ''));
      }
      if (scenario === 'network') throw new Error('网络暂时不可用');
      if (scenario === 'deferred') return new Promise((resolve) => { releaseDeferred = () => resolve(reply(overview('7d', { amount: 20000 }))); });
      if (scenario === 'forbidden') return reply({ error: { message: 'permission_denied' } }, 403);
      if (scenario === 'malformed') { const bad = overview('30d'); bad.paid.gross = [{ amount_minor: Number.MAX_SAFE_INTEGER + 1, currency: 'CNY' }]; return reply(bad); }
      if (scenario === 'negative') return reply(overview(url.searchParams.get('period'), { amount: 500, refund: 1200 }));
      if (scenario === 'refund-zero') return reply(overview(url.searchParams.get('period'), { refund: 0 }));
      if (scenario === 'trend-missing') { const body = overview(url.searchParams.get('period')); body.paid.status = 'data_missing'; body.paid.reason_code = 'paid_confirmation_time_missing'; body.paid.trend = []; return reply(body); }
      if (scenario === 'canonical-payer-unavailable') { const body = overview(url.searchParams.get('period')); body.paid.status = 'data_missing'; body.paid.reason_code = 'canonical_payer_unavailable'; delete body.paid.distinct_canonical_payers; return reply(body); }
      if (scenario === 'payment-timeout') { const body = overview(url.searchParams.get('period')); body.paid.status = 'failed'; body.paid.reason_code = 'payment_aggregate_timeout'; delete body.paid.distinct_canonical_payers; return reply(body); }
      if (scenario === 'distribution-not-configured') { const body = overview(url.searchParams.get('period')); body.distribution.status = 'data_missing'; body.distribution.reason_code = 'distribution_not_configured'; body.distribution.period_paid_sales_minor = 0; body.distribution.period_initial_commission_minor = 0; body.distribution.current_unsettled_minor = 0; body.distribution.current_settled_minor = 0; body.todos.status = 'data_missing'; body.todos.reason_code = 'distribution_not_configured'; body.todos.items = []; return reply(body); }
      if (scenario === 'long-custom') { const body = overview('custom'); body.range = { period: 'custom', timezone: 'Asia/Shanghai', start: '2026-01-01T16:00:00Z', end: '2026-02-03T16:00:00Z' }; return reply(body); }
      return reply(overview(url.searchParams.get('period'), { amount: scenario === 'seven' ? 20000 : 12500 }));
    };
  },
});
dom.window.eval(bundle);
await waitFor(() => dom.window.document.body.textContent.includes('已确认支付'), 'overview did not render its first response');
assert.equal(calls[0].pathname, '/api/admin/overview');
assert.equal(calls[0].search, '?period=today', 'initial request must explicitly use today');
const topbar = dom.window.document.querySelector('.admin-topbar');
const headerActions = topbar?.querySelector('[data-page-header-actions="overview-range"]');
const themeActions = topbar?.querySelector('[data-page-header-actions="overview-theme"]');
assert.equal(dom.window.document.querySelectorAll('.admin-topbar').length, 1, 'overview must retain the shell’s single topbar');
assert.equal(dom.window.document.querySelectorAll('.admin-page-title').length, 1, 'overview must retain the shell’s single title');
assert.equal(headerActions?.querySelectorAll('button').length, 4, 'all four interval controls must mount once in the shell header');
assert.equal(topbar?.contains(headerActions || null), true, 'interval controls must stay inside the existing shell header');
assert.equal(dom.window.document.querySelector('[data-overview-period]'), null, 'the content area must not retain parallel interval controls');
assert.equal(dom.window.document.querySelector('.overview-toolbar'), null, 'the content area must not repeat the overview heading');
assert.equal(dom.window.document.body.textContent.includes('统计口径以各项数据的确认时间为准'), false, 'the redundant statistics-copy must be removed');
assert.equal(headerActions?.querySelector('[data-page-header-action="period-today"]')?.getAttribute('aria-pressed'), 'true', 'the active header range must expose its state');
assert.equal(themeActions?.querySelectorAll('button').length, 3, 'all three visual templates must mount once in the existing shell header');
assert.equal(dom.window.document.body.textContent.includes('来源待核实'), false, 'the former provenance copy must not occupy the dashboard');
assert.equal(dom.window.document.body.textContent.includes('最近读取：'), false, 'normal dashboard cards must not repeat observation timestamps');
assert.equal(dom.window.document.body.textContent.includes('数据读取：支付'), false, 'normal dashboard cards must not repeat the unified observation summary');
assert.equal(dom.window.document.querySelectorAll('.overview-status[aria-label="数据待确认"]').length, 1, 'missing provenance must retain an accessible compact state marker');
assert.ok(dom.window.document.querySelector('.overview-status[aria-label="数据待确认"]')?.title.length > 0, 'data gap marker must explain its reason on hover');
assert.equal(dom.window.document.body.textContent.includes('支付客户'), false, 'overview-visible customer terminology must be renamed');
assert.ok(dom.window.document.body.textContent.includes('支付用户'), 'overview-visible user terminology must render');
assert.equal(dom.window.document.querySelector('a[href="/admin/distribution"]')?.textContent?.includes('分销异常待处理'), true, 'real todo route must stay usable');
assert.equal(dom.window.document.body.textContent.includes('OneID'), false, 'internal identity terminology must not render');
assert.equal(dom.window.document.body.textContent.toLowerCase().includes('contract'), false, 'API contract must not render');
assert.equal(dom.window.document.querySelectorAll('.overview-metric a').length, 0, 'metric cards must not fabricate a date-filtered drill-down');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 1, 'today must use a narrow single trend column');
assert.equal(dom.window.document.querySelector('.overview-chart__bar')?.getAttribute('height'), '112', 'the maximum payment amount must use the defined SVG plot height');

const overviewCallsBeforeTheme = calls.filter((call) => call.pathname === '/api/admin/overview').length;
themeActions?.querySelector('[data-page-header-action="theme-dark"]')?.click();
assert.equal(dom.window.document.querySelector('#overview-admin-root')?.dataset.overviewTheme, 'dark', 'dark template must apply without a rerendered business state');
assert.equal(dom.window.localStorage.getItem('aicrm.overview.theme'), 'dark', 'template choice must persist only in browser storage');
assert.equal(calls.filter((call) => call.pathname === '/api/admin/overview').length, overviewCallsBeforeTheme, 'template switching must not issue an overview request');
assert.equal(themeActions?.querySelector('[data-page-header-action="theme-dark"]')?.getAttribute('aria-pressed'), 'true', 'selected template must expose its state');

scenario = 'deferred';
const sevenDayButton = headerActions?.querySelector('[data-page-header-action="period-7d"]');
sevenDayButton?.focus();
sevenDayButton?.click();
await waitFor(() => typeof releaseDeferred === 'function', 'deferred preset request did not start');
assert.equal(topbar?.querySelector('[data-page-header-action="period-7d"]'), sevenDayButton, 'loading must not replace a focused header action');
assert.equal(dom.window.document.activeElement, sevenDayButton, 'loading must preserve the current header action focus');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '自定义').click();
assert.equal(dom.window.document.querySelector('[data-overview-custom]')?.classList.contains('is-open'), true, 'custom draft must open while a preset request is pending');
const customFrom = dom.window.document.querySelector('[data-overview-custom] [name="from"]');
customFrom.value = '2026-09-01';
const overviewCallsBeforeAurora = calls.filter((call) => call.pathname === '/api/admin/overview').length;
themeActions?.querySelector('[data-page-header-action="theme-aurora"]')?.click();
assert.equal(dom.window.document.querySelector('#overview-admin-root')?.dataset.overviewTheme, 'aurora', 'aurora template must apply while a custom draft is open');
assert.equal(dom.window.document.querySelector('[data-overview-custom] [name="from"]')?.value, '2026-09-01', 'template switching must retain the unsubmitted custom-date draft');
assert.equal(calls.filter((call) => call.pathname === '/api/admin/overview').length, overviewCallsBeforeAurora, 'template switching must not interrupt the pending preset request');
releaseDeferred();
await delay(30);
assert.equal(dom.window.document.querySelector('[data-overview-custom]')?.classList.contains('is-open'), true, 'an obsolete preset response must not close the custom draft');
assert.ok(dom.window.document.body.textContent.includes('当前展示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'a custom draft must retain the range of the last successful response');

scenario = 'today';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.querySelector('[data-overview-paid-records]'), 'a fresh matching overview did not restore the paid-record action');

const paidRecordsButton = dom.window.document.querySelector('[data-overview-paid-records]');
assert.ok(paidRecordsButton, 'only the confirmed-payment metric must expose its detail action');
assert.equal([...dom.window.document.querySelectorAll('.overview-metric')].find((metric) => metric.textContent.includes('支付用户'))?.querySelector('[data-overview-paid-records]'), null, 'canonical payer count must not reuse payment-order records as an invalid drill-down');
paidRecordsButton.focus();
scenario = 'paid-records-pending';
paidRecordsButton.click();
await waitFor(() => dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('正在读取支付记录'), 'paid-record drawer did not show its loading state');
assert.equal(dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('已确认无支付记录'), false, 'a pending first page must not be presented as a confirmed zero');
scenario = 'paid-records';
releasePaidRecords();
await waitFor(() => dom.window.document.querySelector('.overview-paid-records')?.textContent.includes('M-OVERVIEW-COLLISION'), 'paid-record drawer did not render its first page');
const paidInitialCall = calls[calls.length - 1];
assert.equal(paidInitialCall.pathname, '/api/admin/overview/paid-records');
assert.equal(paidInitialCall.search, '?period=custom&from=2026-09-15&to=2026-09-15', 'drawer must freeze the already-rendered Beijing range, including across midnight');
assert.equal(dom.window.document.querySelector('.overview-paid-records a')?.getAttribute('href'), '/admin/orderDetail.html?id=M-OVERVIEW-COLLISION&provider=wechat', 'Payment wechat_pay must use the existing provider-scoped Order detail URL');
assert.ok(dom.window.document.querySelector('.overview-paid-records')?.textContent.includes('付款时用户 #17'), 'historical payer fact must be labelled without pretending it is a current root');
assert.ok(dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('付款时用户是历史事实，不代表当前归并用户。'), 'the drawer must retain the necessary historical-identity boundary');
const overviewCallsBeforeDrawerTheme = calls.filter((call) => call.pathname === '/api/admin/overview').length;
themeActions?.querySelector('[data-page-header-action="theme-business"]')?.click();
assert.equal(dom.window.document.querySelector('#overview-admin-root')?.dataset.overviewTheme, 'business', 'template selection must apply while the drawer remains open');
assert.equal(Boolean(dom.window.document.querySelector('.shared-detail-drawer[open] .overview-paid-records')), true, 'template selection must not close the paid-record drawer');
assert.equal(calls.filter((call) => call.pathname === '/api/admin/overview').length, overviewCallsBeforeDrawerTheme, 'template selection must not reload the overview while the drawer is open');

scenario = 'paid-records-more-fail-once';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '加载更多').click();
await waitFor(() => dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('HTTP 503'), 'load-more failure did not remain inside the drawer');
assert.ok(dom.window.document.querySelector('.overview-paid-records')?.textContent.includes('付款时用户 #17'), 'load-more failure must retain the successful first page');
assert.equal(calls[calls.length - 1].search, '?cursor=next-paid-records-page', 'load-more must send the original continuation alone');
scenario = 'paid-records';
[...dom.window.document.querySelectorAll('.shared-detail-drawer button')].find((button) => button.textContent === '重试').click();
await waitFor(() => dom.window.document.querySelector('.overview-paid-records')?.textContent.includes('付款时用户待确认'), 'load-more retry did not preserve and extend the original page');
assert.equal(dom.window.document.querySelector('.overview-paid-records a[href*="provider=wechat_shop"]')?.getAttribute('href'), '/admin/orderDetail.html?id=M-OVERVIEW-COLLISION&provider=wechat_shop', 'Payment wechat_shop must retain its provider-scoped Order detail URL');

[...dom.window.document.querySelectorAll('.shared-detail-drawer button')].find((button) => button.textContent === '关闭').click();
assert.equal(dom.window.document.activeElement, paidRecordsButton, 'closing the drawer must return focus to the still-mounted overview action');
scenario = 'paid-records-unavailable';
paidRecordsButton.click();
await waitFor(() => dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('HTTP 503'), 'drawer did not disclose its independent 503 state');
assert.equal(dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('已确认无支付记录'), false, '503 must not be presented as a confirmed zero');
assert.ok(dom.window.document.querySelector('.shared-detail-drawer [data-overview-paid-records-retry]'), '503 without a completed page must offer an in-drawer retry');
[...dom.window.document.querySelectorAll('.shared-detail-drawer button')].find((button) => button.textContent === '关闭').click();

scenario = 'paid-records';
paidRecordsButton.click();
await waitFor(() => dom.window.document.querySelector('.overview-paid-records')?.textContent.includes('付款时用户 #17'), 'fresh drawer did not read its first page before permission loss');
scenario = 'paid-records-more-forbidden';
[...dom.window.document.querySelectorAll('.shared-detail-drawer button')].find((button) => button.textContent === '加载更多').click();
await waitFor(() => dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('没有查看支付记录的权限'), 'drawer did not disclose its independent 403 state');
assert.equal(dom.window.document.querySelector('.shared-detail-drawer')?.textContent.includes('已确认无支付记录'), false, '403 must not be presented as a confirmed zero');
assert.equal(Boolean(dom.window.document.querySelector('.overview-paid-records')?.textContent.includes('付款时用户 #17')), false, '403 after a successful page must clear records that are no longer authorized');
assert.equal(dom.window.document.querySelector('.shared-detail-drawer [data-overview-paid-records-retry]'), null, '403 must remove a retry that cannot restore authorization');
[...dom.window.document.querySelectorAll('.shared-detail-drawer button')].find((button) => button.textContent === '关闭').click();

scenario = 'network';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 7 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('网络暂时不可用'), 'failed refresh did not expose retry');
assert.ok(dom.window.document.body.textContent.includes('¥125.00'), 'ordinary failed refresh must retain prior values');
assert.ok(dom.window.document.body.textContent.includes('当前展示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'retained figures must disclose their actual successful range');

scenario = 'seven';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '重试').click();
await waitFor(() => dom.window.document.body.textContent.includes('¥200.00'), 'retry did not load the selected range');
assert.equal(calls[calls.length - 1].search, '?period=7d', 'retry must retain selected period');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 7, 'the seven-day trend must retain all daily columns');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__bar').length, 3, 'ready ranges must infer zero-value days only between known payment dates');
assert.ok([...dom.window.document.querySelectorAll('.overview-chart__bar')].some((bar) => bar.getAttribute('height') === '112'), 'the seven-day maximum must use a visible fixed-height bar');

scenario = 'trend-missing';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 30 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('暂无可用趋势数据'), 'missing payment evidence did not render');
assert.ok(dom.window.document.querySelector('.overview-panel--wide .overview-status[aria-label="数据待确认"]'), 'missing payment evidence must retain a compact accessible state marker');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 0, 'data-missing payment trends must not infer zero-value dates');

scenario = 'canonical-payer-unavailable';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => [...dom.window.document.querySelectorAll('.overview-metric')].find((metric) => metric.textContent.includes('支付用户'))?.querySelector('strong')?.textContent === '—', 'unknown canonical payer count did not expose its state');
const payerMetric = [...dom.window.document.querySelectorAll('.overview-metric')].find((metric) => metric.textContent.includes('支付用户'));
assert.equal(payerMetric?.querySelector('strong')?.textContent, '—', 'omitted canonical payer count must not render as zero');

scenario = 'payment-timeout';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 7 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('已确认支付读取超时'), 'payment timeout did not expose its reason');
assert.equal([...dom.window.document.querySelectorAll('.overview-metric')].find((metric) => metric.textContent.includes('支付用户'))?.querySelector('strong')?.textContent, '—', 'timed-out omitted payer count must not render as zero');

scenario = 'distribution-not-configured';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('分销数据暂未接入，暂无法确认待处理事项'), 'unconfigured distribution did not expose its unavailable todo state');
const distributionValues = [...dom.window.document.querySelectorAll('.overview-panel')].find((panel) => panel.textContent.includes('分销进度'))?.querySelectorAll('dd') || [];
assert.deepEqual([...distributionValues].map((node) => node.textContent), ['—', '—', '—', '—'], 'unconfigured distribution must not present default zeroes as known amounts');
assert.equal([...dom.window.document.querySelectorAll('.overview-panel')].filter((panel) => panel.textContent.includes('分销进度') || panel.textContent.includes('待处理事项')).filter((panel) => Boolean(panel.querySelector('.overview-status[aria-label="数据待确认"]'))).length, 2, 'unconfigured distribution and todos must disclose their unknown state');
assert.equal(dom.window.document.body.textContent.includes('暂无需要处理的事项。'), false, 'unconfigured distribution must not present an empty todo list as confirmed');

scenario = 'negative';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('-¥7.00'), 'a valid negative net amount was rejected');

scenario = 'network';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('网络暂时不可用'), 'a same-period retry must mark its previous-day-capable cache as stale');
assert.ok(dom.window.document.body.textContent.includes('当前展示：今日（北京时间 2026-09-15 至 2026-09-15）（上次成功读取）'), 'same-period cached data must show its actual successful range');
assert.ok(dom.window.document.body.textContent.includes('-¥7.00'), 'same-period failure must retain the last verified figures');

scenario = 'refund-zero';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '重试').click();
await waitFor(() => dom.window.document.body.textContent.includes('0（本期无退款）'), 'a confirmed refund zero must not render as missing');

scenario = 'malformed';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 30 天').click();
await waitFor(() => dom.window.document.body.textContent.includes('经营数据格式无效'), 'malformed data must be rejected');
assert.ok(dom.window.document.body.textContent.includes('¥125.00'), 'malformed data must not replace known figures with a synthetic zero');

scenario = 'forbidden';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '今日').click();
await waitFor(() => dom.window.document.body.textContent.includes('暂无查看权限'), '403 must show a distinct permission state');
assert.equal(dom.window.document.body.textContent.includes('¥200.00'), false, 'permission loss must hide cached operating metrics');

scenario = 'seven';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '近 30 天').click();
await waitFor(() => dom.window.document.querySelectorAll('.overview-chart__column').length === 30, 'a ready thirty-day period did not fill its complete Beijing date range');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 30, 'the thirty-day trend must retain all daily columns');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__bar').length, 3, 'zero-value days must not paint a misleading non-zero bar');
assert.equal(dom.window.document.querySelector('.overview-chart')?.classList.contains('overview-chart--dense'), true, 'thirty-day charts must use the dense layout');
assert.equal([...dom.window.document.querySelectorAll('.overview-chart--dense .overview-chart__value')].filter((node) => node.textContent.trim()).length, 0, 'dense charts must not truncate monetary labels');
assert.equal([...dom.window.document.querySelectorAll('.overview-chart--dense .overview-chart__bar title')].map((node) => node.textContent).join('|'), '¥66.66|¥66.66|¥66.68', 'dense payment bars must retain complete hover labels');
assert.equal(dom.window.document.querySelector('.overview-trend-details')?.open, false, 'the thirty-day trend table must stay collapsed until requested');

[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '自定义').click();
let custom = dom.window.document.querySelector('[data-overview-custom]');
scenario = 'long-custom';
custom.querySelector('[name="from"]').value = '2026-01-02';
custom.querySelector('[name="to"]').value = '2026-02-03';
custom.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
await waitFor(() => dom.window.document.querySelectorAll('.overview-chart__column').length === 1, 'long custom ranges must retain their existing sparse payment-date strategy');
assert.equal(dom.window.document.querySelectorAll('.overview-chart__column').length, 1, 'long custom ranges must not fabricate every date');

custom = dom.window.document.querySelector('[data-overview-custom]');
custom.querySelector('[name="from"]').value = '2026-09-01';
custom.querySelector('[name="to"]').value = '';
custom.dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
await waitFor(() => dom.window.document.body.textContent.includes('请选择有效的开始和结束日期'), 'invalid custom range did not show validation feedback');
assert.equal(dom.window.document.querySelector('[name="from"]').value, '2026-09-01', 'custom-range draft must survive validation feedback');

dom.window.close();

const storageBlocked = new JSDOM('<!doctype html><header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">经营总览</h1></div></header><main id="overview-admin-root"></main>', {
  url: 'https://crm.example/admin', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers;
    Object.defineProperty(window, 'localStorage', { configurable: true, get() { throw new Error('storage unavailable'); } });
    window.fetch = async () => reply(overview('today'));
  },
});
storageBlocked.window.eval(bundle);
await waitFor(() => storageBlocked.window.document.body.textContent.includes('已确认支付'), 'blocked localStorage prevented the overview from loading');
assert.equal(storageBlocked.window.document.querySelector('#overview-admin-root')?.dataset.overviewTheme, 'business', 'blocked localStorage must fall back to the business template');
storageBlocked.window.close();
console.log('overview admin host: PASS');
