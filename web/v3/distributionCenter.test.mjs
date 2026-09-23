import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./distributionCenter.ts', import.meta.url)));
const delay = (ms = 15) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, message) { for (let attempt = 0; attempt < 100; attempt++) { if (check()) return; await delay(); } throw new Error(message); }
const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
const calls = [];
let credentialURL = 'https://crm.example/d/dpc_12345678901234567890';
const me = { distributor: { public_no: 'D-0001', enabled: true, agreement_version: '2026-09', registered_at: '2026-09-14T00:00:00Z' }, receiver: { ready: true, reason: '', app_id: 'wx-app' }, settlement: { enabled: true, reason: '' }, registration_required: false, current_agreement_version: '2026-09' };
const dom = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL; window.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new window.Event('close')); };
  window.fetch = async (input, init = {}) => { const url = new URL(String(input), window.location.href); calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body || '', idempotencyKey: new Headers(init.headers).get('Idempotency-Key') });
    if (url.pathname === '/api/v1/distribution/me') return json(me);
    if (url.pathname === '/api/v1/distribution/agreement') return json({ version: '2026-09', content: '分销协议正文' });
    if (url.pathname === '/api/v1/distribution/products') return json({ items: [{ product_id: 7, product_type: 'standard_product', cover_url: '', purchase_url: '/p/growth-course', name: '增长课', price_minor: 19900, currency: 'CNY', commission_rate_basis_points: 333, estimated_commission_minor: 662, wait_days: 7, promotion_ready: true, promotion_block_reason: '' }], next_cursor: '' });
    if (url.pathname === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 19900, successful_refunds_minor: 0, initial_commission_minor: 662, commission_adjustments_minor: 0, unsettled_payable_minor: 662, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' });
    if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [{ commission_id: 'c1', order_reference: 'O-1', product_name: '增长课', initial_minor: 662, current_payable_minor: 662, paid_minor: 0, status: 'pending', hold_reason: '', cancel_reason: '', exception_reason: '', paid_confirmed_at: '2026-09-14T00:00:00Z', due_at: '2026-09-21T00:00:00Z', paid_at: '', created_at: '2026-09-14T00:00:00Z', currency: 'CNY' }], next_cursor: '' });
    if (url.pathname === '/api/v1/distribution/products/7/promotion-credentials') return json({ url: credentialURL, expires_at: '2026-09-15T00:00:00Z' }, 201);
    return json({ error: 'not_found' }, 404);
  };
} });
dom.window.eval(bundle); await waitFor(() => dom.window.document.body.textContent.includes('增长课'), 'distribution products did not render');
assert.match(dom.window.document.body.textContent, /预计佣金 ¥6\.62/, 'estimated commission must render server minor amount');
assert.match(dom.window.document.body.textContent, /售价 ¥199\.00/, 'promotion card must render the sale price');
assert.match(dom.window.document.body.textContent, /预计佣金 ¥6\.62（3\.33%）/, 'promotion card must render estimated commission and rate');
assert.equal(dom.window.document.querySelector('.distribution-product img'), null, 'promotion card must not reserve a cover column');
assert.doesNotMatch(dom.window.document.querySelector('.distribution-product')?.textContent || '', /等待.*天|分销员编号|协议：|已启用/, 'promotion card must omit non-selling facts');
assert.equal(dom.window.document.querySelector('[data-testid="distribution-referral-entry"]')?.getAttribute('href'), '/referral', 'distribution navigation must expose the independent referral campaign entry');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '复制分销链接').click();
await waitFor(() => calls.some((call) => call.path.endsWith('/promotion-credentials')), 'promotion credential did not use the real API');
await waitFor(() => dom.window.document.querySelector('dialog'), 'promotion dialog did not render');
const credential = calls.find((call) => call.path.endsWith('/promotion-credentials'));
assert.equal(credential.body, '', 'credential request must not carry product type, distributor, amount, receiver, or policy');
assert.match(credential.idempotencyKey || '', /^[0-9a-f-]{16,}$/i, 'credential request must carry a stable server replay key');
assert.equal(dom.window.document.querySelector('a[href="/p/growth-course"]'), null, 'a promotion-ready product must not offer an unnecessary repeat purchase');
assert.equal(dom.window.document.querySelector('dialog input[readonly]')?.value, 'https://crm.example/d/dpc_12345678901234567890', 'manual-copy fallback must expose only the signed credential URL');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '关闭')?.click();
let copiedURL = '';
Object.defineProperty(dom.window.navigator, 'clipboard', { configurable: true, value: { writeText: async (value) => { copiedURL = value; } } });
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '复制分销链接').click();
await waitFor(() => copiedURL === 'https://crm.example/d/dpc_12345678901234567890', 'primary action must copy only the server-issued promotion URL');
await waitFor(() => dom.window.document.body.textContent.includes('分销链接已复制'), 'successful copy did not settle before the next state');
assert.equal(dom.window.document.querySelector('dialog'), null, 'successful clipboard copy must not require the manual-copy dialog');
assert.equal(calls.filter((call) => call.path.endsWith('/promotion-credentials')).length, 2, 'each primary action must issue a controlled credential request');
credentialURL = 'https://crm.example/d/not_a_distribution_credential';
copiedURL = '';
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '复制分销链接').click();
await waitFor(() => dom.window.document.body.textContent.includes('请从系统正式页面重新打开后生成链接'), 'a same-origin non-dpc credential must be rejected');
assert.equal(copiedURL, '', 'a malformed server credential must never reach the clipboard');
assert.equal(dom.window.document.querySelector('dialog'), null, 'a malformed server credential must not open the manual-copy fallback');
[...dom.window.document.querySelectorAll('button')].find((button) => button.textContent === '我的收益').click();
await waitFor(() => dom.window.document.body.textContent.includes('累计推广成交额'), 'earnings tab did not render');
assert.match(dom.window.document.body.textContent, /未结算佣金/, 'earnings must expose unsettled definition');
const statusFilter = dom.window.document.querySelector('select[name="commission-status"]');
assert.deepEqual([...statusFilter.options].map((option) => option.value), ['', 'pending', 'held', 'settling', 'paid', 'cancelled', 'exception'], 'commission statuses must remain available through the compact native select');
statusFilter.value = 'paid'; statusFilter.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => calls.some((call) => call.path === '/api/v1/distribution/commissions' && call.query === '?status=paid&limit=50'), 'commission status select did not preserve the real status query');
await delay();
dom.window.close();

let registrationBody; let registrationIdempotencyKey;
const registration = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL; window.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new window.Event('close')); };
  window.fetch = async (input, init = {}) => { const path = new URL(String(input), window.location.href).pathname; if (path === '/api/v1/distribution/me') return json(registrationBody ? me : { distributor: null, receiver: { ready: false, reason: '待验证', app_id: '' }, settlement: { enabled: true, reason: '' }, registration_required: true, current_agreement_version: '2026-09' }); if (path === '/api/v1/distribution/agreement') return json({ version: '2026-09', content: '分销协议正文' }); if (path === '/api/v1/distribution/registration') { registrationBody = JSON.parse(String(init.body)); registrationIdempotencyKey = new Headers(init.headers).get('Idempotency-Key'); return json(me); } if (path === '/api/v1/distribution/products') return json({ items: [], next_cursor: '' }); if (path === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 0, successful_refunds_minor: 0, initial_commission_minor: 0, commission_adjustments_minor: 0, unsettled_payable_minor: 0, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' }); if (path === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' }); return json({ error: 'not_found' }, 404); };
} });
registration.window.eval(bundle); await waitFor(() => registration.window.document.body.textContent.includes('申请成为分销员'), 'registration view did not render');
const check = registration.window.document.querySelector('input[type="checkbox"]'); check.checked = true; check.dispatchEvent(new registration.window.Event('change', { bubbles: true }));
[...registration.window.document.querySelectorAll('button')].find((button) => button.textContent === '同意并注册').click();
await waitFor(() => registrationBody, 'registration did not call the real API');
assert.deepEqual(registrationBody, { agreement_version: '2026-09' }, 'registration may submit only server-advertised agreement version');
assert.match(registrationIdempotencyKey || '', /^[0-9a-f-]{16,}$/i, 'registration must carry a replay key');
await waitFor(() => registration.window.document.body.textContent.includes('推广商品'), 'registration reload did not settle');
await delay();
registration.window.close();
console.log('distribution center contract: PASS');

let preparationRequest;
const preparation = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL; window.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new window.Event('close')); };
  window.fetch = async (input, init = {}) => { const path = new URL(String(input), window.location.href).pathname; if (path === '/api/v1/distribution/me') return json({ ...me, receiver: { ready: false, reason: 'receiver_not_ready', app_id: 'wx-app' } }); if (path === '/api/v1/distribution/agreement') return json({ version: '2026-09', content: '分销协议正文' }); if (path === '/api/v1/distribution/products') return json({ items: [], next_cursor: '' }); if (path === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 0, successful_refunds_minor: 0, initial_commission_minor: 0, commission_adjustments_minor: 0, unsettled_payable_minor: 0, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' }); if (path === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' }); if (path === '/api/v1/distribution/receiver-preparation') { preparationRequest = { method: init.method, body: init.body || '', idempotencyKey: new Headers(init.headers).get('Idempotency-Key') }; return json({ receiver: { ready: false, reason: 'receiver_accepted', reference: 'r-1', app_id: 'wx-app', checked_at: '2026-09-14T00:00:00Z' }, setup: { state: 'processing', action_url: '/api/v1/distribution/receiver-preparation', retry_after_seconds: 30 } }); } return json({ error: 'not_found' }, 404); };
} });
preparation.window.eval(bundle); await waitFor(() => preparation.window.document.body.textContent.includes('完成收款准备'), 'receiver preparation action did not render');
[...preparation.window.document.querySelectorAll('button')].find((button) => button.textContent === '完成收款准备').click();
await waitFor(() => preparationRequest, 'receiver preparation did not use the real API');
assert.deepEqual({ method: preparationRequest.method, body: preparationRequest.body }, { method: 'POST', body: '' }, 'receiver preparation must not submit identity, receiver, or app identifiers');
assert.match(preparationRequest.idempotencyKey || '', /^[0-9a-f-]{16,}$/i, 'receiver preparation must carry a replay key');
await waitFor(() => preparation.window.document.body.textContent.includes('30 秒后'), 'receiver preparation processing state did not render');
await delay();
preparation.window.close();
console.log('distribution receiver preparation contract: PASS');

const emptyEarnings = { gross_paid_sales_minor: 0, successful_refunds_minor: 0, initial_commission_minor: 0, commission_adjustments_minor: 0, unsettled_payable_minor: 0, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' };
const blockedProduct = (reason) => ({ product_id: 7, product_type: 'standard_product', cover_url: '', purchase_url: '/p/growth-course', name: '增长课', price_minor: 19900, currency: 'CNY', commission_rate_basis_points: 333, estimated_commission_minor: 662, wait_days: 7, promotion_ready: false, promotion_block_reason: reason });
async function readinessProductsView({ profile = me, page, prepare }) {
  const viewCalls = [];
  const view = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
    window.Response = Response; window.Headers = Headers; window.URL = URL;
    window.fetch = async (input, init = {}) => { const url = new URL(String(input), window.location.href); viewCalls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
      if (url.pathname === '/api/v1/distribution/me') return json(typeof profile === 'function' ? profile() : profile);
      if (url.pathname === '/api/v1/distribution/products') return json(typeof page === 'function' ? page(url) : page);
      if (url.pathname === '/api/v1/distribution/earnings') return json(emptyEarnings);
      if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' });
      if (url.pathname === '/api/v1/distribution/receiver-preparation') return prepare ? prepare(init) : json({ error: 'not_found' }, 404);
      return json({ error: 'not_found' }, 404);
    };
  } });
  view.window.eval(bundle);
  await waitFor(() => view.window.document.body.textContent.includes('分销中心'), 'receiver readiness view did not render');
  return { view, viewCalls };
}

const merchantDisabled = await readinessProductsView({ profile: { ...me, receiver: { ready: false, reason: 'receiver_final_failed', app_id: 'wx-app' }, settlement: { enabled: false, reason: 'merchant_settlement_disabled' } }, page: { items: [blockedProduct('receiver_final_failed')], next_cursor: '', empty_reason: '' } });
assert.match(merchantDisabled.view.window.document.body.textContent, /商户尚未启用分佣结算，分销员资格会保留/, 'merchant settlement gate must not be shown as an individual receiver failure');
assert.equal([...merchantDisabled.view.window.document.querySelectorAll('button')].some((button) => /收款准备/.test(button.textContent)), false, 'merchant-disabled state must not offer an invalid receiver action');
assert.equal(merchantDisabled.viewCalls.some((call) => call.path === '/api/v1/distribution/receiver-preparation'), false, 'merchant-disabled state must not prepare a receiver');
await delay();
merchantDisabled.view.window.close();

const purchaseRequired = await readinessProductsView({ page: { items: [blockedProduct('qualification_purchase_required')], next_cursor: '', empty_reason: '' } });
assert.equal(purchaseRequired.view.window.document.body.textContent.includes('增长课'), false, 'a product without a verified effective purchase must not render as a promotion candidate');
assert.equal(purchaseRequired.view.window.document.querySelector('a[href="/p/growth-course"]'), null, 'the distribution center must never offer an unverified purchase as a promotion path');
await delay();
purchaseRequired.view.window.close();

const paidConfirmationMissing = await readinessProductsView({ page: { items: [blockedProduct('qualification_payment_confirmation_missing')], next_cursor: '', empty_reason: 'qualification_payment_confirmation_missing' } });
await waitFor(() => paidConfirmationMissing.view.window.document.body.textContent.includes('正在核验付款确认'), 'paid confirmation message did not render');
assert.equal(paidConfirmationMissing.view.window.document.querySelector('a[href="/p/growth-course"]'), null, 'a paid purchase missing confirmation must not be sent to buy again');
assert.equal([...paidConfirmationMissing.view.window.document.querySelectorAll('button')].some((button) => /收款准备/.test(button.textContent)), false, 'a paid purchase missing confirmation must not be sent to receiver preparation');
await delay();
paidConfirmationMissing.view.window.close();

const checkUnavailable = await readinessProductsView({ page: { items: [blockedProduct('qualification_check_unavailable')], next_cursor: '', empty_reason: '' } });
assert.match(checkUnavailable.view.window.document.body.textContent, /联系商家/, 'qualification read failure must identify the responsible follow-up');
assert.equal(checkUnavailable.view.window.document.querySelector('a[href="/p/growth-course"]'), null, 'qualification read failure must not offer a repeat purchase');
assert.equal([...checkUnavailable.view.window.document.querySelectorAll('button')].some((button) => /收款准备/.test(button.textContent)), false, 'qualification read failure must not offer receiver preparation');
await delay();
checkUnavailable.view.window.close();

const emptyPolicyProducts = await readinessProductsView({ page: { items: [], next_cursor: '', empty_reason: 'no_saleable_policy_products' } });
assert.match(emptyPolicyProducts.view.window.document.body.textContent, /没有可售且已开启分销的商品/, 'saleable-policy empty state must explain why no product card exists');
await delay();
emptyPolicyProducts.view.window.close();
const filteredFirstPage = await readinessProductsView({ page: (url) => url.search.includes('cursor=after-filter') ? { items: [{ ...blockedProduct(''), promotion_ready: true, name: '后续可推广商品' }], next_cursor: '', empty_reason: '' } : { items: [], next_cursor: 'after-filter', empty_reason: 'qualification_purchase_required' } });
const loadMoreProducts = [...filteredFirstPage.view.window.document.querySelectorAll('button')].find((button) => button.textContent === '加载更多商品');
assert.ok(loadMoreProducts, 'a strict-filtered empty page with next_cursor must retain the load-more action');
loadMoreProducts.click();
await waitFor(() => filteredFirstPage.view.window.document.body.textContent.includes('后续可推广商品'), 'load more must continue past an empty strictly filtered page');
assert.ok(filteredFirstPage.viewCalls.some((call) => call.path === '/api/v1/distribution/products' && call.query === '?limit=50&cursor=after-filter'), 'load more must use the returned cursor');
await delay();
filteredFirstPage.view.window.close();
const disabledDistributorProducts = await readinessProductsView({ profile: { ...me, distributor: { ...me.distributor, enabled: false } }, page: { items: [], next_cursor: '', empty_reason: 'distributor_disabled' } });
assert.match(disabledDistributorProducts.view.window.document.body.textContent, /分销员当前已停用/, 'disabled distributor empty state must remain distinct from no products');
await delay();
disabledDistributorProducts.view.window.close();

let profileReads = 0;
const stalePreparation = await readinessProductsView({ profile: () => {
  profileReads++;
  return profileReads === 1 ? { ...me, receiver: { ready: false, reason: 'receiver_not_ready', app_id: 'wx-app' } } : { ...me, receiver: { ready: false, reason: 'receiver_not_ready', app_id: 'wx-app' }, settlement: { enabled: false, reason: 'merchant_settlement_disabled' } };
}, page: { items: [blockedProduct('receiver_not_ready')], next_cursor: '', empty_reason: '' }, prepare: () => json({ receiver: { ready: false, reason: 'receiver_not_ready', reference: '', app_id: 'wx-app', checked_at: '2026-09-14T00:00:00Z' }, setup: { state: 'merchant_settlement_disabled', action_url: '/api/v1/distribution/receiver-preparation', retry_after_seconds: 0 } }) });
[...stalePreparation.view.window.document.querySelectorAll('button')].find((button) => button.textContent === '完成收款准备').click();
await waitFor(() => profileReads >= 2 && stalePreparation.view.window.document.body.textContent.includes('商户尚未启用分佣结算'), 'receiver response must reload the profile before presenting merchant responsibility');
assert.equal(stalePreparation.viewCalls.filter((call) => call.path === '/api/v1/distribution/receiver-preparation').length, 1, 'receiver disabled response is a single no-op request');
await delay();
stalePreparation.view.window.close();

const historicalReceiverFailure = await readinessProductsView({ profile: { ...me, receiver: { ready: false, reason: 'receiver_final_failed', app_id: 'wx-app' } }, page: { items: [blockedProduct('receiver_final_failed')], next_cursor: '', empty_reason: '' } });
assert.match(historicalReceiverFailure.view.window.document.body.textContent, /请联系商家核验/, 'a historical receiver failure must be escalated to the merchant');
assert.equal([...historicalReceiverFailure.view.window.document.querySelectorAll('button')].some((button) => /重试收款准备|完成收款准备/.test(button.textContent)), false, 'public H5 must not retry a historical receiver failure');
await delay();
historicalReceiverFailure.view.window.close();

const receiverPermissionDenied = await readinessProductsView({ profile: { ...me, receiver: { ready: false, reason: 'receiver_provider_permission_denied', app_id: 'wx-app' } }, page: { items: [blockedProduct('receiver_provider_permission_denied')], next_cursor: '', empty_reason: '' } });
assert.match(receiverPermissionDenied.view.window.document.body.textContent, /支付侧拒绝了收款准备/, 'payment permission denial must name the safe responsible boundary');
assert.equal([...receiverPermissionDenied.view.window.document.querySelectorAll('button')].some((button) => /完成收款准备|重新完成微信登录/.test(button.textContent)), false, 'payment permission denial must not retry receiver preparation');
assert.equal(receiverPermissionDenied.viewCalls.some((call) => call.path.endsWith('/promotion-credentials')), false, 'blocked receiver state must not issue a credential');
await delay();
receiverPermissionDenied.view.window.close();
const receiverAccountAbnormal = await readinessProductsView({ profile: { ...me, receiver: { ready: false, reason: 'payment_receiver_account_abnormal', app_id: 'wx-app' } }, page: { items: [blockedProduct('payment_receiver_account_abnormal')], next_cursor: '', empty_reason: '' } });
assert.match(receiverAccountAbnormal.view.window.document.body.textContent, /收款账户状态异常，请联系商家核验/, 'a controlled receiver-account reason must be rendered as Chinese business guidance');
assert.equal([...receiverAccountAbnormal.view.window.document.querySelectorAll('button')].some((button) => /完成收款准备|重新完成微信登录/.test(button.textContent)), false, 'a controlled payment final reason must not suggest a public retry');
await delay();
receiverAccountAbnormal.view.window.close();
console.log('distribution receiver readiness states: PASS');

const applicationCalls = [];
const application = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution?product_id=7&product_type=standard_product', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
  window.Response = Response; window.Headers = Headers; window.URL = URL;
  window.fetch = async (input, init = {}) => { const url = new URL(String(input), window.location.href); applicationCalls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
    if (url.pathname === '/api/v1/distribution/application-context') return json({ product_id: 7, product_type: 'standard_product', policy_enabled: true, product_name: '申请商品', purchase_url: '/p/application-product' });
    if (url.pathname === '/api/v1/distribution/me') return json({ error: 'distribution_session_required' }, 401);
    if (url.pathname === '/api/v1/distribution/session/bridge') return json({ error: 'payment_session_required' }, 401);
    return json({ error: 'not_found' }, 404);
  };
} });
application.window.eval(bundle);
await waitFor(() => application.window.document.body.textContent.includes('申请推广：申请商品'), 'application link must display the server-confirmed product before login');
const login = application.window.document.querySelector('a.distribution-button');
assert.equal(login?.getAttribute('href'), '/api/h5/wechat-pay/oauth/start?return_url=%2Fdistribution%3Fproduct_id%3D7%26product_type%3Dstandard_product', 'OAuth login must preserve only the canonical application context');
assert.ok(applicationCalls.some((call) => call.path === '/api/v1/distribution/application-context' && call.query === '?product_id=7&product_type=standard_product'), 'application context must be read from the controlled server API');
await delay();
application.window.close();
console.log('distribution application context contract: PASS');

async function applicationProductsView(targetResponse) {
  const view = new JSDOM('<!doctype html><main id="distribution-root"></main>', { url: 'https://crm.example/distribution?product_id=7&product_type=standard_product', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
    window.Response = Response; window.Headers = Headers; window.URL = URL;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname === '/api/v1/distribution/application-context') return targetResponse;
      if (url.pathname === '/api/v1/distribution/me') return json(me);
      if (url.pathname === '/api/v1/distribution/products') return json({ items: [{ product_id: 8, product_type: 'standard_product', cover_url: '', purchase_url: '/p/other-product', name: '其他可推广商品', price_minor: 100, currency: 'CNY', commission_rate_basis_points: 100, estimated_commission_minor: 1, wait_days: 1, promotion_ready: true, promotion_block_reason: '' }], next_cursor: '' });
      if (url.pathname === '/api/v1/distribution/earnings') return json({ gross_paid_sales_minor: 0, successful_refunds_minor: 0, initial_commission_minor: 0, commission_adjustments_minor: 0, unsettled_payable_minor: 0, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' });
      if (url.pathname === '/api/v1/distribution/commissions') return json({ items: [], next_cursor: '' });
      return json({ error: 'not_found' }, 404);
    };
  } });
  view.window.eval(bundle);
  return view;
}

const eligibleElsewhere = await applicationProductsView(json({ product_id: 7, product_type: 'standard_product', policy_enabled: true, product_name: '申请商品', purchase_url: '/p/application-product' }));
await waitFor(() => eligibleElsewhere.window.document.body.textContent.includes('其他可推广商品'), 'application products did not render');
assert.doesNotMatch(eligibleElsewhere.window.document.body.textContent, /申请商品/, 'application context must not inject a post-registration product card');
assert.equal(eligibleElsewhere.window.document.querySelector('a[href="/p/application-product"]'), null, 'application context must not create a purchase path');
await delay();
eligibleElsewhere.window.close();

const unavailableTarget = await applicationProductsView(json({ error: 'not_found' }, 404));
await waitFor(() => unavailableTarget.window.document.body.textContent.includes('其他可推广商品'), 'unavailable application target should not replace eligible product results');
assert.doesNotMatch(unavailableTarget.window.document.body.textContent, /申请商品|当前不可用/, 'an unavailable application context must not inject a post-registration product card');
await delay();
unavailableTarget.window.close();

const failedTarget = await applicationProductsView(json({ error: 'unavailable' }, 503));
await waitFor(() => failedTarget.window.document.body.textContent.includes('其他可推广商品'), 'failed application target should not replace eligible product results');
assert.doesNotMatch(failedTarget.window.document.body.textContent, /申请商品|暂时无法读取商品/, 'a failed application context must not inject a post-registration product card');
await delay();
failedTarget.window.close();
console.log('distribution application product states: PASS');
