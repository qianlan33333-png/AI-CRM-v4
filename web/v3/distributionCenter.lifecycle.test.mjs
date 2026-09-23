import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./distributionCenter.ts', import.meta.url)));
const delay = (ms = 10) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, message) { for (let attempt = 0; attempt < 150; attempt++) { if (check()) return; await delay(); } throw new Error(message); }
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const deferred = () => { let resolve; const promise = new Promise((done) => { resolve = done; }); return { promise, resolve }; };
const profile = (overrides = {}) => ({ distributor: { public_no: 'D-001', enabled: true, agreement_version: '2026-09', registered_at: '2026-09-15T00:00:00Z' }, receiver: { ready: true, reason: '', app_id: 'wx-app' }, settlement: { enabled: false, reason: 'merchant_settlement_unavailable' }, registration_required: false, current_agreement_version: '2026-09', ...overrides });
const earnings = { gross_paid_sales_minor: 1000, successful_refunds_minor: 0, initial_commission_minor: 100, commission_adjustments_minor: 0, unsettled_payable_minor: 100, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' };
const product = (name) => ({ product_id: 7, product_type: 'standard_product', cover_url: '', purchase_url: '/p/growth-course', name, price_minor: 1000, currency: 'CNY', commission_rate_basis_points: 100, estimated_commission_minor: 10, wait_days: 1, promotion_ready: true, promotion_block_reason: '' });
const commission = (id, status, name = id) => ({ commission_id: id, order_reference: `O-${id}`, product_name: name, initial_minor: 100, current_payable_minor: 100, paid_minor: status === 'paid' ? 100 : 0, status, hold_reason: '', cancel_reason: '', exception_reason: '', paid_confirmed_at: '2026-09-15T00:00:00Z', due_at: '2026-09-16T00:00:00Z', settlement_confirmed_at: '', created_at: '2026-09-15T00:00:00Z', currency: 'CNY' });
function viewWith(fetch, url = 'https://crm.example/distribution') {
  const view = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url, runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
    window.Response = Response; window.Headers = Headers; window.URL = URL;
    window.fetch = fetch;
  } });
  view.window.eval(bundle);
  return view;
}
function refresh(view) { const button = [...view.window.document.querySelectorAll('button')].find((node) => node.textContent === '刷新状态'); assert.ok(button, 'refresh action must be visible'); button.click(); }
function earningsTab(view) { const button = [...view.window.document.querySelectorAll('button')].find((node) => node.textContent === '我的收益'); assert.ok(button, 'earnings tab must be visible'); button.click(); }

// A read error must preserve the last confirmed page rather than replacing it with
// an error-only screen. The message makes the freshness boundary visible.
let transientReads = 0;
const transient = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') { transientReads += 1; return json(profile()); }
  if (url.pathname === '/api/v1/distribution/products') return transientReads === 1 ? json({ items: [product('已确认商品')], next_cursor: '' }) : json({ error: 'unavailable' }, 503);
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [commission('all', 'pending')], next_cursor: '' });
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => transient.window.document.body.textContent.includes('已确认商品'), 'initial confirmed product did not render');
refresh(transient);
await waitFor(() => transient.window.document.body.textContent.includes('分销状态未更新'), 'transient failure did not expose stale state');
assert.match(transient.window.document.body.textContent, /已确认商品/, 'transient failure must retain confirmed facts');
transient.window.close();

// Two status reads can resolve out of order. Only the newest selected status may
// replace the list; an unavailable new status must not relabel the old result.
const pendingRead = deferred(); const paidRead = deferred(); let statusReads = 0;
const filters = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') return json(profile({ settlement: { enabled: true, reason: '' } }));
  if (url.pathname === '/api/v1/distribution/products') return json({ items: [product('筛选商品')], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') {
    if (url.search === '?limit=50') return json({ items: [commission('all', 'pending', '全部初始记录')], next_cursor: '' });
    if (url.search === '?status=pending&limit=50') { statusReads += 1; return pendingRead.promise; }
    if (url.search === '?status=paid&limit=50') { statusReads += 1; return paidRead.promise; }
  }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => filters.window.document.body.textContent.includes('筛选商品'), 'filter fixture did not render');
earningsTab(filters);
const select = filters.window.document.querySelector('select[name="commission-status"]');
select.value = 'pending'; select.dispatchEvent(new filters.window.Event('change', { bubbles: true }));
select.value = 'paid'; select.dispatchEvent(new filters.window.Event('change', { bubbles: true }));
await waitFor(() => statusReads === 2, 'both status reads did not start');
paidRead.resolve(json({ items: [commission('paid', 'paid', '新 paid 结果')], next_cursor: '' }));
await waitFor(() => filters.window.document.body.textContent.includes('新 paid 结果'), 'newest filter did not render');
pendingRead.resolve(json({ items: [commission('pending', 'pending', '旧 pending 结果')], next_cursor: '' }));
await delay(30);
assert.match(filters.window.document.body.textContent, /新 paid 结果/, 'stale filter result overwrote the newest selection');
assert.doesNotMatch(filters.window.document.body.textContent, /旧 pending 结果/, 'stale filter data leaked into the newest selection');
filters.window.close();

// A reload owns the filter generation it started with. Returning to the same
// status is still a new selection: an older reload for A must not overwrite a
// later A after A -> B -> A.
const staleReloadAll = deferred(); const freshAll = deferred(); const pendingSwitch = deferred(); let allReads = 0;
const reloadFilters = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') return json(profile({ settlement: { enabled: false, reason: 'merchant_settlement_unavailable' } }));
  if (url.pathname === '/api/v1/distribution/products') return json({ items: [product('重载筛选商品')], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') {
    if (url.search === '?limit=50') {
      allReads += 1;
      if (allReads === 1) return json({ items: [commission('all-initial', 'pending', '初始 A 结果')], next_cursor: '' });
      if (allReads === 2) return staleReloadAll.promise;
      if (allReads === 3) return freshAll.promise;
    }
    if (url.search === '?status=pending&limit=50') return pendingSwitch.promise;
  }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => reloadFilters.window.document.body.textContent.includes('重载筛选商品'), 'reload/filter fixture did not render');
refresh(reloadFilters);
await waitFor(() => allReads === 2, 'reload did not start its A status read');
earningsTab(reloadFilters);
const reloadSelect = reloadFilters.window.document.querySelector('select[name="commission-status"]');
reloadSelect.value = 'pending'; reloadSelect.dispatchEvent(new reloadFilters.window.Event('change', { bubbles: true }));
reloadSelect.value = ''; reloadSelect.dispatchEvent(new reloadFilters.window.Event('change', { bubbles: true }));
await waitFor(() => allReads === 3, 'new A selection did not start after A -> B -> A');
freshAll.resolve(json({ items: [commission('all-fresh', 'pending', '新 A 结果')], next_cursor: '' }));
await waitFor(() => reloadFilters.window.document.body.textContent.includes('新 A 结果'), 'new A result did not render');
staleReloadAll.resolve(json({ items: [commission('all-stale', 'pending', '旧 reload A 结果')], next_cursor: '' }));
await delay(30);
assert.match(reloadFilters.window.document.body.textContent, /新 A 结果/, 'stale reload overwrote the newer same-status selection');
assert.doesNotMatch(reloadFilters.window.document.body.textContent, /旧 reload A 结果/, 'stale reload leaked into the newer A result');
reloadFilters.window.close();

let failedStatusReads = 0;
const failedFilter = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') return json(profile({ settlement: { enabled: true, reason: '' } }));
  if (url.pathname === '/api/v1/distribution/products') return json({ items: [product('失败筛选商品')], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') {
    if (url.search === '?limit=50') return json({ items: [commission('all', 'pending', '旧全部记录')], next_cursor: '' });
    if (url.search === '?status=paid&limit=50') { failedStatusReads += 1; return json({ error: 'unavailable' }, 503); }
  }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => failedFilter.window.document.body.textContent.includes('失败筛选商品'), 'failed-filter fixture did not render');
earningsTab(failedFilter);
const failedSelect = failedFilter.window.document.querySelector('select[name="commission-status"]');
failedSelect.value = 'paid'; failedSelect.dispatchEvent(new failedFilter.window.Event('change', { bubbles: true }));
await waitFor(() => failedStatusReads === 1 && failedFilter.window.document.body.textContent.includes('所选状态尚未得到确认'), 'failed selection did not retain an explicit unknown state');
assert.doesNotMatch(failedFilter.window.document.body.textContent, /旧全部记录/, 'old all-status result must not be labelled as the failed paid selection');
assert.doesNotMatch(failedFilter.window.document.body.textContent, /正在读取所选状态/, 'failed selection must not remain in a loading state');
failedFilter.window.close();

// A double click can only issue one page read per returned cursor for both lists.
let productMoreReads = 0; const productMore = deferred();
const pagination = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') return json(profile({ settlement: { enabled: true, reason: '' } }));
  if (url.pathname === '/api/v1/distribution/products') {
    if (url.search === '?limit=50') return json({ items: [product('商品第一页')], next_cursor: 'product-next' });
    if (url.search === '?limit=50&cursor=product-next') { productMoreReads += 1; return productMore.promise; }
  }
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [commission('commission-first', 'pending', '佣金第一页')], next_cursor: 'commission-next' });
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => pagination.window.document.body.textContent.includes('商品第一页'), 'pagination fixture did not render');
let productMoreButton = [...pagination.window.document.querySelectorAll('button')].find((node) => node.textContent === '加载更多商品');
productMoreButton.click(); productMoreButton.click();
await waitFor(() => productMoreReads === 1, 'product double click issued duplicate cursor reads');
productMore.resolve(json({ items: [product('商品第二页')], next_cursor: '' }));
await waitFor(() => pagination.window.document.body.textContent.includes('商品第二页'), 'product next page did not render');
assert.equal((pagination.window.document.body.textContent.match(/商品第二页/g) || []).length, 1, 'product page was appended more than once');
earningsTab(pagination);
let commissionMoreReads = 0; const commissionMore = deferred();
// Swap only the second-page response handler while keeping the fixture data.
const originalFetch = pagination.window.fetch;
pagination.window.fetch = async (input, init) => {
  const url = new URL(String(input), pagination.window.location.href);
  if (url.pathname === '/api/v1/distribution/commissions' && url.search === '?limit=50&cursor=commission-next') { commissionMoreReads += 1; return commissionMore.promise; }
  return originalFetch(input, init);
};
await waitFor(() => pagination.window.document.body.textContent.includes('佣金第一页'), 'commission first page did not render');
const commissionMoreButton = [...pagination.window.document.querySelectorAll('button')].find((node) => node.textContent === '加载更多明细');
commissionMoreButton.click(); commissionMoreButton.click();
await waitFor(() => commissionMoreReads === 1, 'commission double click issued duplicate cursor reads');
commissionMore.resolve(json({ items: [commission('commission-next', 'pending', '佣金第二页')], next_cursor: '' }));
await waitFor(() => pagination.window.document.body.textContent.includes('佣金第二页'), 'commission next page did not render');
assert.equal((pagination.window.document.body.textContent.match(/佣金第二页/g) || []).length, 1, 'commission page was appended more than once');
pagination.window.close();

// A stale 401 must not bridge, clear, or replace facts from a newer successful
// read. A current 401 still clears the in-memory facts before showing login.
const stale401 = deferred(); let meReads = 0; let bridges = 0;
const access = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') {
    meReads += 1;
    if (meReads === 2) return stale401.promise;
    if (meReads >= 4) return json({ error: 'distribution_session_required' }, 401);
    return json(profile({ settlement: { enabled: false, reason: 'merchant_settlement_unavailable' } }));
  }
  if (url.pathname === '/api/v1/distribution/products') return json({ items: [product(meReads >= 3 ? '新会话商品' : '旧会话商品')], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [commission('access', 'pending')], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/session/bridge') { bridges += 1; return json({ error: 'payment_session_required' }, 401); }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => access.window.document.body.textContent.includes('旧会话商品'), 'access fixture did not render');
refresh(access);
await waitFor(() => meReads === 2, 'older request did not start before the newer reload');
refresh(access);
await waitFor(() => access.window.document.body.textContent.includes('新会话商品'), 'newer session read did not render');
stale401.resolve(json({ error: 'distribution_session_required' }, 401));
await delay(30);
assert.match(access.window.document.body.textContent, /新会话商品/, 'stale 401 revoked newer facts');
assert.equal(bridges, 0, 'stale 401 must not start a bridge request');
refresh(access);
await waitFor(() => access.window.document.body.textContent.includes('使用微信登录'), 'current 401 did not render login');
assert.doesNotMatch(access.window.document.body.textContent, /新会话商品|旧会话商品/, 'current 401 left sensitive facts visible');
assert.equal(bridges, 1, 'current 401 must attempt one controlled bridge');
access.window.close();

// A successful application-context read belongs to the same authorized
// session. If the later profile read loses authorization, its product name
// must not be put back while the controlled bridge is pending.
const contextBridge = deferred();
const context401 = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/application-context') return json({ product_id: 7, product_type: 'standard_product', name: '失权前申请商品' });
  if (url.pathname === '/api/v1/distribution/me') return json({ error: 'distribution_session_required' }, 401);
  if (url.pathname === '/api/v1/distribution/session/bridge') return contextBridge.promise;
  return json({ error: 'not_found' }, 404);
}, 'https://crm.example/distribution?product_id=7&product_type=standard_product');
await waitFor(() => context401.window.document.body.textContent.includes('正在确认微信登录状态'), 'profile 401 did not start the controlled bridge');
assert.doesNotMatch(context401.window.document.body.textContent, /失权前申请商品/, 'authorization failure restored an application target from the old session');
contextBridge.resolve(json({ error: 'distribution_session_required' }, 401));
await waitFor(() => context401.window.document.body.textContent.includes('使用微信登录'), 'failed bridge did not return to login');
assert.doesNotMatch(context401.window.document.body.textContent, /失权前申请商品/, 'login after failed bridge retained the old application target');
context401.window.close();

// Current authorization failures from secondary reads share the same clearing
// path as reload. During a pending bridge no previous product or commission
// fact may remain in the DOM.
const pausedBridge = deferred(); let filterBridges = 0;
const filterAuthorization = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') return json(profile());
  if (url.pathname === '/api/v1/distribution/products') return json({ items: [product('筛选前敏感商品')], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') {
    if (url.search === '?limit=50') return json({ items: [commission('filter-old', 'pending', '筛选前敏感佣金')], next_cursor: '' });
    if (url.search === '?status=paid&limit=50') return json({ error: 'distribution_session_required' }, 401);
  }
  if (url.pathname === '/api/v1/distribution/session/bridge') { filterBridges += 1; return pausedBridge.promise; }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => filterAuthorization.window.document.body.textContent.includes('筛选前敏感商品'), 'filter authorization fixture did not render');
earningsTab(filterAuthorization);
const authFilter = filterAuthorization.window.document.querySelector('select[name="commission-status"]');
authFilter.value = 'paid'; authFilter.dispatchEvent(new filterAuthorization.window.Event('change', { bubbles: true }));
await waitFor(() => filterAuthorization.window.document.body.textContent.includes('正在确认微信登录状态'), 'filter 401 did not immediately replace the authorized page while bridge is pending');
assert.doesNotMatch(filterAuthorization.window.document.body.textContent, /筛选前敏感商品|筛选前敏感佣金/, 'filter 401 retained sensitive facts during bridge');
assert.equal(filterBridges, 1, 'filter 401 must start one controlled bridge');
pausedBridge.resolve(json({}));
await waitFor(() => filterAuthorization.window.document.body.textContent.includes('筛选前敏感商品'), 'successful bridge did not reload the authorized page');
filterAuthorization.window.close();

let paginationBridges = 0;
const paginationAuthorization = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') return json(profile());
  if (url.pathname === '/api/v1/distribution/products') {
    if (url.search === '?limit=50') return json({ items: [product('分页前敏感商品')], next_cursor: 'sensitive-next' });
    if (url.search === '?limit=50&cursor=sensitive-next') return json({ error: 'distribution_session_required' }, 401);
  }
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/session/bridge') { paginationBridges += 1; return json({ error: 'payment_session_required' }, 401); }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => paginationAuthorization.window.document.body.textContent.includes('分页前敏感商品'), 'pagination authorization fixture did not render');
const sensitiveMore = [...paginationAuthorization.window.document.querySelectorAll('button')].find((node) => node.textContent === '加载更多商品');
sensitiveMore.click();
await waitFor(() => paginationAuthorization.window.document.body.textContent.includes('使用微信登录'), 'pagination 401 did not show login after bridge failed');
assert.doesNotMatch(paginationAuthorization.window.document.body.textContent, /分页前敏感商品/, 'pagination 401 retained sensitive product facts');
assert.equal(paginationBridges, 1, 'pagination 401 must start one controlled bridge');
paginationAuthorization.window.close();

let forbiddenBridges = 0; let forbiddenReads = 0;
const forbidden = viewWith(async (input) => {
  const url = new URL(String(input), 'https://crm.example');
  if (url.pathname === '/api/v1/distribution/me') { forbiddenReads += 1; return json(profile()); }
  if (url.pathname === '/api/v1/distribution/products') return forbiddenReads === 1 ? json({ items: [product('已失权商品')], next_cursor: '' }) : json({ error: 'distribution_forbidden' }, 403);
  if (url.pathname === '/api/v1/distribution/earnings') return json(earnings);
  if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' });
  if (url.pathname === '/api/v1/distribution/session/bridge') { forbiddenBridges += 1; return json({}); }
  return json({ error: 'not_found' }, 404);
});
await waitFor(() => forbidden.window.document.body.textContent.includes('已失权商品'), 'forbidden fixture did not render');
refresh(forbidden);
await waitFor(() => forbidden.window.document.body.textContent.includes('无权读取分销信息'), 'current 403 did not render an access-denied state');
assert.doesNotMatch(forbidden.window.document.body.textContent, /已失权商品/, 'current 403 retained inaccessible facts');
assert.equal(forbiddenBridges, 0, '403 must not attempt a session bridge');
forbidden.window.close();

console.log('distribution center lifecycle: PASS');
