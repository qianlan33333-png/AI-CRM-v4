import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { webcrypto } from 'node:crypto';
import { build } from 'esbuild';
import path from 'node:path';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle = await build({ stdin: { contents: "import './web/v3/orderAdapter'; import {AdminController} from './web/src/admin/controller'; import {api} from './web/src/shared/api/client'; window.OrderControllerFixture = AdminController; window.OrderAdapterApi = api;", resolveDir: root, loader: 'ts' }, bundle: true, format: 'iife', write: false, platform: 'browser', logLevel: 'silent' });
const host = bundle.outputFiles[0].text;
const pause = () => new Promise((resolve) => setTimeout(resolve, 15));
const refundActorBinding = 'b'.repeat(64);
const alternateRefundActorBinding = 'c'.repeat(64);

function recoveryNotFound(actorBinding = refundActorBinding) {
  return new Response(JSON.stringify({ found: false, actor_binding: actorBinding }), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

function browserRuntime(window) {
  window.Request = Request; window.Response = Response; window.Headers = Headers;
  Object.defineProperty(window, 'crypto', { value: webcrypto, configurable: true });
}

function scopedRefundPage(refunds = []) {
  return { items: refunds, refunds, total: refunds.length, limit: 50, offset: 0, has_more: false };
}

const calls = [];
const dom = new JSDOM(`<!doctype html><body>
  <input id="orderTransactionId"><input id="orderProductCode"><input id="orderMobile"><button type="button">导出微信支付 CSV</button>
  <table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th><th>商品</th><th>金额</th><th>状态</th><th>支付来源</th><th>操作</th></tr></thead><tbody><tr><td>2026-09-08T00:00:00Z</td><td><div>merchant-1</div></td><td><div>付款人姓名</div><div>customer:123</div></td><td>测试商品</td><td>20.00</td><td><span>paid</span></td><td>wechat</td><td><a>查看详情</a></td></tr></tbody></table>
</body>`, {
  url: 'https://test.invalid/admin/orders', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      calls.push(url);
      return new Response(JSON.stringify({ items: [{ id: 101, merchant_order_no: 'merchant-1', detail_url: '/admin/orderDetail.html?id=merchant-1&provider=wechat', provider: 'wechat', distribution_read_state: 'available', distribution: [], created_at: '2026-09-08T00:00:00Z', payer_name: '付款人姓名', payer_id: 'customer:123', provider_label: '微信支付', currency: 'CNY' }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});

try {
  dom.window.eval(host);
  await pause();
  const document = dom.window.document;
  assert.equal(document.querySelector('thead th:nth-child(3)').textContent, '付款人', 'the list must not label an internal identity as customer-facing data');
  assert.equal(document.querySelector('tbody td:first-child').textContent, '2026-09-08 08:00:00', 'created time must display Beijing seconds without a zone suffix');
  assert.equal(document.querySelector('tbody td:nth-child(3) div:nth-child(2)').hidden, true, 'internal customer references must not render under the payer name');
  assert.equal(document.querySelector('tbody td:nth-child(6) span').textContent, '已支付', 'the order-list status must not expose the raw protocol enum');
  // A mutation after formatting must preserve Beijing text; it may not treat
  // completed display text as a new UTC instant under a non-Shanghai browser.
  document.querySelector('tbody tr').append(document.createElement('span')); await pause();
  assert.equal(document.querySelector('tbody td:first-child').textContent, '2026-09-08 08:00:00', 'repeated DOM presentation must not shift Beijing time');

  const controller = new dom.window.OrderControllerFixture({ mode: 'http' }, 'orders');
  controller.state.orderFilters = { transactionId: 'server-platform-reference', payer: '13800138000', product: 'server-product-code', status: '', createdFrom: '', createdTo: '' };
  controller.db.rows.orders = [{ no: 'merchant-returned', payer: '实际客户姓名', uid: 'customer:7', product: '实际商品名', time: '2026-09-08', tone: 'ok' }];
  controller.db.orderList = { total: 51, hasMore: true };
  controller.state.orderOffset = 50;
  const values = controller.renderVals();
  assert.equal(values.rows.orders.length, 1, 'server-resolved phone/contact matches must not be discarded by donor local name filtering');
  assert.equal(values.orderPage.filters.payer, '13800138000', 'rendering must retain the user query');
  assert.match(values.orderPage.summary, /51/, 'server paging must retain the returned total and offset');

  document.getElementById('orderMobile').value = '138 0013 8000';
  document.querySelector('button').click(); await pause();
  assert.match(document.body.textContent, /筛选暂不支持导出/, 'identity-filtered result sets must not silently export all orders');
  document.getElementById('orderMobile').value = '138 0013 8000';
  const directListResponse = await dom.window.fetch('/api/admin/orders?limit=50&offset=0');
  assert.equal(calls.at(-1).searchParams.get('phone'), '13800138000', 'phone searches must stay server-side and preserve paging');
  assert.equal((await directListResponse.json()).items[0].currency, 'CNY', 'direct API consumers retain the canonical currency code');
  assert.equal(calls.at(-1).searchParams.get('external_userid'), null, 'phone and external-contact filters are mutually exclusive');

  // The public response remains canonical. The frozen order DTO receives its
  // payment-channel label only after this exact loadDb call has completed.
  const renderedDb = await dom.window.OrderAdapterApi.loadDb({ page: 'orders' });
  assert.equal(renderedDb.rows.orders[0].pay, '微信支付', 'the frozen DTO payment column receives the provider label after projection');
  assert.ok(!JSON.stringify(renderedDb.rows.orders).includes('aicrm-order-v3:'), 'the DTO carries no serialised correlation field');
  controller.db = renderedDb;
  const renderedValues = controller.renderVals();
  assert.equal(renderedValues.rows.orders[0].pay, '微信支付', 'the exact loadDb association survives the frozen controller object-spread path');
  document.querySelector('tbody tr').append(document.createElement('span')); await pause();
  assert.doesNotMatch(document.querySelector('tbody td:nth-child(2)').textContent, /非分销订单|分销：/, 'the actual loadDb-to-renderVals path activates the matching distribution summary');
  assert.ok(!document.body.textContent.includes('aicrm-order-v3:'), 'the full renderer path never exposes a correlation marker');

  document.getElementById('orderMobile').value = 'external-contact-fixture';
  await dom.window.fetch('/api/admin/orders?limit=50&offset=50');
  assert.equal(calls.at(-1).searchParams.get('external_userid'), 'external-contact-fixture', 'external-contact searches must use the server identity filter');
  assert.equal(calls.at(-1).searchParams.get('phone'), null, 'external-contact filters must not add a phone dimension');
} finally {
  dom.window.close();
}

console.log('order Host identity query and presentation journey: PASS');

const collisionReference = 'merchant-provider-collision';
const collisionDom = new JSDOM(`<!doctype html><body>
  <table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th><th>商品</th><th>金额</th><th>状态</th><th>支付来源</th><th>操作</th></tr></thead><tbody>
    <tr><td>2026-09-15T00:00:00Z</td><td><div>${collisionReference}</div></td><td><div>买家甲</div><div>customer:1</div></td><td>成功商品</td><td>1.00</td><td><span>paid</span></td><td>微信支付</td><td><a>查看详情</a></td></tr>
    <tr><td>2026-09-15T00:01:00Z</td><td><div>${collisionReference}</div></td><td><div>买家乙</div><div>customer:2</div></td><td>待核验商品</td><td>1.00</td><td><span>paid</span></td><td>支付宝</td><td><a>查看详情</a></td></tr>
  </tbody></table>
</body>`, {
  url: 'https://test.invalid/admin/orders', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname !== '/api/admin/orders') return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [
        { id: 701, merchant_order_no: collisionReference, detail_url: `/admin/orderDetail.html?id=${collisionReference}&provider=wechat`, provider: 'wechat', provider_label: '微信支付', currency: 'CNY', distribution_read_state: 'available', distribution: [{ distributor_display_name: '分销员成功', has_commission: true, current_payable_minor: 100, currency: 'CNY' }] },
        { id: 702, merchant_order_no: collisionReference, detail_url: `/admin/orderDetail.html?id=${collisionReference}&provider=alipay`, provider: 'alipay', provider_label: '支付宝', currency: 'CNY', distribution_read_state: 'available', distribution: [] },
      ] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  collisionDom.window.eval(host);
  const collisionController = new collisionDom.window.OrderControllerFixture({ mode: 'http' }, 'orders');
  collisionController.db = await collisionDom.window.OrderAdapterApi.loadDb({ page: 'orders' });
  collisionController.renderVals();
  collisionDom.window.document.querySelector('tbody tr').append(collisionDom.window.document.createElement('span'));
  await pause();
  const rows = Array.from(collisionDom.window.document.querySelectorAll('tbody tr'));
  assert.match(rows[0].textContent, /分销：分销员成功 · 佣金总额 ¥1\.00/, 'provider-scoped successful row labels the preserved commission total rather than an unpaid balance');
  assert.doesNotMatch(rows[1].textContent, /非分销订单|分销：/, 'same merchant reference from another provider cannot borrow a distribution summary');
  assert.equal(new URL(rows[0].dataset.orderDetailUrl).searchParams.get('provider'), 'wechat', 'row detail URL preserves server-owned WeChat provider');
  assert.equal(new URL(rows[1].dataset.orderDetailUrl).searchParams.get('provider'), 'alipay', 'row detail URL preserves server-owned Alipay provider');
} finally {
  collisionDom.window.close();
}

const mixedDistributionDom = new JSDOM(`<!doctype html><body>
  <table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th><th>商品</th><th>金额</th><th>状态</th><th>支付来源</th><th>操作</th></tr></thead><tbody>
    <tr><td>2026-09-15T00:02:00Z</td><td><div>merchant-mixed-distribution</div></td><td><div>买家丙</div><div>customer:3</div></td><td>混合归因商品</td><td>3.00</td><td><span>paid</span></td><td>微信支付</td><td><a>查看详情</a></td></tr>
  </tbody></table>
</body>`, {
  url: 'https://test.invalid/admin/orders', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async () => new Response(JSON.stringify({ items: [{
      id: 703, merchant_order_no: 'merchant-mixed-distribution', detail_url: '/admin/orderDetail.html?id=merchant-mixed-distribution&provider=wechat', provider: 'wechat', provider_label: '微信支付', currency: 'CNY', distribution_read_state: 'available', distribution: [
        { distributor_display_name: '分销员成功', has_commission: true, current_payable_minor: 100, currency: 'CNY' },
        { distributor_display_name: '分销员待形成', has_commission: false, current_payable_minor: null, currency: 'CNY' },
        { distributor_display_name: '分销员金额待确认', has_commission: true, current_payable_minor: null, currency: 'CNY' },
      ],
    }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  },
});
try {
  mixedDistributionDom.window.eval(host);
  const mixedController = new mixedDistributionDom.window.OrderControllerFixture({ mode: 'http' }, 'orders');
  mixedController.db = await mixedDistributionDom.window.OrderAdapterApi.loadDb({ page: 'orders' });
  mixedController.renderVals();
  mixedDistributionDom.window.document.querySelector('tbody tr').append(mixedDistributionDom.window.document.createElement('span'));
  await pause();
  const summary = mixedDistributionDom.window.document.querySelector('tbody tr').textContent;
  assert.match(summary, /2项已形成 \/ 1项待形成/, 'a partially formed order must preserve both formed and pending attribution lines');
  assert.match(summary, /佣金总额 ¥1\.00（另有金额待确认）/, 'unknown commission-total data must not be rendered as a real zero amount');
  assert.doesNotMatch(summary, /已归因 · 未形成佣金/, 'a pending line must not hide the formed commission summary for the same order');
} finally {
  mixedDistributionDom.window.close();
}

let resolveEarlierList;
const listRaceDom = new JSDOM(`<!doctype html><body>
  <div id="stage"></div><table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th><th>商品</th><th>金额</th><th>状态</th><th>支付来源</th><th>操作</th></tr></thead><tbody>
  <tr><td>2026-09-15T00:01:00Z</td><td><div>${collisionReference}</div></td><td><div>买家乙</div><div>customer:2</div></td><td>待核验商品</td><td>1.00</td><td><span>paid</span></td><td>支付宝</td><td><a>查看详情</a></td></tr>
  </tbody></table>
</body>`, {
  url: 'https://test.invalid/admin/orders', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    let orderRequestCount = 0;
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const wechatResponse = () => new Response(JSON.stringify({ items: [{ id: 801, created_at: '2026-09-15T00:00:00Z', merchant_order_no: collisionReference, detail_url: `/admin/orderDetail.html?id=${collisionReference}&provider=wechat`, provider: 'wechat', provider_label: '微信支付', payer_name: '买家甲', payer_id: 'customer:1', product_name: '成功商品', amount_yuan: '1.00', status: 'paid', currency: 'CNY', distribution_read_state: 'available', distribution: [{ distributor_display_name: '分销员成功', has_commission: true, current_payable_minor: 100, currency: 'CNY' }] }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      const alipayResponse = () => new Response(JSON.stringify({ items: [{ id: 802, created_at: '2026-09-15T00:01:00Z', merchant_order_no: collisionReference, detail_url: `/admin/orderDetail.html?id=${collisionReference}&provider=alipay`, provider: 'alipay', provider_label: '支付宝', payer_name: '买家乙', payer_id: 'customer:2', product_name: '待核验商品', amount_yuan: '1.00', status: 'paid', currency: 'CNY', distribution_read_state: 'available', distribution: [] }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/orders' && ++orderRequestCount === 1) return new Promise((resolve) => { resolveEarlierList = () => resolve(wechatResponse()); });
      return alipayResponse();
    };
  },
});
try {
  listRaceDom.window.eval(host);
  const earlier = listRaceDom.window.OrderAdapterApi.loadDb({ page: 'orders' });
  await pause();
  assert.equal(typeof resolveEarlierList, 'function', 'the earlier page has claimed its own list request before the later page starts');
  const rawReader = await listRaceDom.window.fetch('/api/admin/orders?raw-reader=1');
  assert.equal((await rawReader.json()).items[0].currency, 'CNY', 'an unrelated raw reader keeps canonical JSON and cannot claim the pending page association');
  const laterDb = await listRaceDom.window.OrderAdapterApi.loadDb({ page: 'orders' });
  const controller = new listRaceDom.window.OrderControllerFixture({ mode: 'http' }, 'orders');
  controller.db = laterDb;
  const firstValues = controller.renderVals();
  assert.equal(firstValues.rows.orders[0].pay, '支付宝', 'the later load binds its own server provider label after the frozen controller projection');
  assert.ok(Object.getOwnPropertySymbols(firstValues.rows.orders[0]).length > 0, 'the exact record association survives the donor object-spread renderer');
  assert.ok(!JSON.stringify(laterDb.rows.orders).includes('aicrm-order-v3:'), 'the later DTO has no serialised opaque correlation data');
  listRaceDom.window.document.querySelector('tbody tr').append(listRaceDom.window.document.createElement('span'));
  await pause();
  assert.doesNotMatch(listRaceDom.window.document.querySelector('tbody tr').textContent, /非分销订单|分销：/, 'the later provider response activates its own rendered row');
  assert.ok(!listRaceDom.window.document.body.textContent.includes('aicrm-order-v3:'), 'the opaque association never reaches DOM text');
  resolveEarlierList();
  const earlierDb = await earlier;
  assert.equal(earlierDb.rows.orders[0].pay, '微信支付', 'the delayed page keeps its own provider label rather than adopting the later response');
  controller.renderVals();
  listRaceDom.window.document.querySelector('tbody tr').append(listRaceDom.window.document.createElement('span'));
  await pause();
  assert.doesNotMatch(listRaceDom.window.document.querySelector('tbody tr').textContent, /非分销订单|分销：/, 'a delayed response cannot replace the later rendered provider row');
  assert.ok(!listRaceDom.window.document.body.textContent.includes('分销员成功'), 'a delayed WeChat response never leaks into the rendered Alipay row');
  controller.db.rows.orders = [{ time: '2026-09-15T00:01:00Z', no: collisionReference, plat: '支付宝', payer: '买家乙', uid: 'customer:2', product: '待核验商品', amount: '1.00', status: 'paid', pay: '支付宝', tone: 'ok' }];
  controller.renderVals();
  listRaceDom.window.document.querySelector('tbody tr').append(listRaceDom.window.document.createElement('span'));
  await pause();
  assert.ok(!listRaceDom.window.document.querySelector('tbody tr').textContent.includes('分销：'), 'a missing renderer association clears the prior selection instead of retaining stale distribution facts');
  assert.equal(listRaceDom.window.document.querySelector('tbody tr').dataset.orderDetailUrl, undefined, 'a missing renderer association also removes the stale provider detail URL');
} finally {
  listRaceDom.window.close();
}

const scopedDetailCalls = [];
const scopedDetailDom = new JSDOM(`<!doctype html><body data-page="orderDetail">
  <div><span>${collisionReference}</span><span>paid</span></div>
  <div><div><h2>订单详情</h2></div><div></div></div>
  <div><div><h2>事件时间线</h2></div><div></div></div>
  <div><div><h2>申请退款</h2></div><div></div></div>
</body>`, {
  url: `https://test.invalid/admin/orderDetail.html?id=${collisionReference}&provider=alipay`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      scopedDetailCalls.push(url);
      if (url.pathname === `/api/admin/orders/${collisionReference}`) return new Response(JSON.stringify({ record_origin: 'native', merchant_order_no: collisionReference, provider: 'alipay', product_name: '待核验商品', amount_yuan: '1.00', refundable_amount_total: 0, created_at: '2026-09-15T00:01:00Z', status: 'paid', distribution_read_state: 'available', distribution: [{ item_line: 1, product_name: '待核验商品', distributor_display_name: '分销员待核验', rate_basis_points: 1000, wait_days: 7, policy_version: 1, has_commission: true, initial_minor: 100, current_payable_minor: 100, paid_minor: 0, currency: 'CNY', status: 'exception', hold_reason: '', cancel_reason: '', exception_reason: 'unmapped_engine_reason', due_at: '2026-09-22T00:01:00Z', settlement_confirmed_at: null, adjustments: [], settlements: [{ reference: 'dstl_unknown', amount_minor: 100, currency: 'CNY', state: 'outcome_unknown', settlement_confirmed_at: null }], exceptions: [] }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
	scopedDetailDom.window.eval(host);
	await new Promise((resolve) => setTimeout(resolve, 60));
  const orderRead = scopedDetailCalls.find((url) => url.pathname === `/api/admin/orders/${collisionReference}`);
  assert.equal(orderRead?.searchParams.get('provider'), 'alipay', 'detail read forwards the server-owned provider rather than inferring it from a duplicate merchant number');
  const body = scopedDetailDom.window.document.body.textContent;
  assert.match(body, /分销员待核验/, 'provider-scoped detail shows the selected provider order');
  assert.match(body, /原因待确认/, 'unknown distribution reason code has a safe Chinese pending label');
  assert.ok(!body.includes('unmapped_engine_reason'), 'unknown distribution reason code is never exposed directly');
} finally {
  scopedDetailDom.window.close();
}

const invalidProviderCalls = [];
const invalidProviderDom = new JSDOM(`<!doctype html><body data-page="orderDetail">
  <div><div><h2>订单详情</h2></div><div></div></div>
</body>`, {
  url: `https://test.invalid/admin/orderDetail.html?id=${collisionReference}&provider=manual-invalid`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      invalidProviderCalls.push(new URL(typeof input === 'string' ? input : input.url, window.location.href));
      return new Response(JSON.stringify({ merchant_order_no: collisionReference }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  invalidProviderDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(invalidProviderCalls.length, 0, 'an invalid provider query must not fall back to a legacy detail read');
  assert.match(invalidProviderDom.window.document.body.textContent, /订单定位信息无效/, 'an invalid provider query has a staff-readable fail-closed state');
} finally {
  invalidProviderDom.window.close();
}

const detailCalls = [];
const detailDom = new JSDOM(`<!doctype html><body data-page="orderDetail">
  <div><span>M-ORDER-TEST-0001</span><span>paid</span></div>
  <div><div><h2>订单详情</h2></div><div></div></div>
  <div><div><h2>事件时间线</h2></div><div></div></div>
  <div><div><h2>申请退款</h2></div><div></div></div>
</body>`, {
  url: 'https://test.invalid/admin/orderDetail.html?id=M-ORDER-TEST-0001', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      detailCalls.push({ url, method: init.method || 'GET' });
      if (url.pathname === '/api/admin/orders/M-ORDER-TEST-0001') return new Response(JSON.stringify({
        record_origin: 'native', merchant_order_no: 'M-ORDER-TEST-0001', provider: 'wechat',
        transaction_id: '4200000000000000000000000000', payer_name: '测试买家', payer_id: 'customer:101', payer_phone_masked: '138****0000',
        product_name: '测试商品', amount_yuan: '20.00', refundable_amount_total: 2000, created_at: '2026-09-30T16:01:02Z', status: 'paid',
        distribution_read_state: 'available', distribution: [{ item_line: 1, product_name: '测试商品', distributor_display_name: '分销员甲', rate_basis_points: 1234, wait_days: 7, policy_version: 3, has_commission: true, initial_minor: 246, current_payable_minor: 222, paid_minor: 100, currency: 'CNY', status: 'exception', hold_reason: '退款复核中', cancel_reason: '', exception_reason: '部分退款待处理', due_at: '2026-10-07T16:01:02Z', settlement_confirmed_at: '2026-10-08T16:01:02Z', adjustments: [{ kind: 'buyer_refund', delta_minor: -24, resulting_payable_minor: 222, reason: '部分退款', occurred_at: '2026-10-02T16:01:02Z' }], settlements: [{ reference: 'dstl_1', amount_minor: 100, currency: 'CNY', state: 'receiver_succeeded', settlement_confirmed_at: '2026-10-08T16:01:02Z' }, { reference: 'dstl_2', amount_minor: 22, currency: 'CNY', state: 'receiver_succeeded', settlement_confirmed_at: null }], exceptions: [{ kind: 'buyer_refund_after_paid', status: 'open', amount_minor: 100, reason: '退款后待处理', evidence_reference: 'refund_1' }] }],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage([
        { refund_no: 'RF-TEST-1', refund_amount_total: 2000, status: 'completed', reason: '测试退款', created_at: '2026-10-01T00:01:02+08:00' },
      ])), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname.endsWith('/external-push-deliveries')) return new Response(JSON.stringify({
        effects: [
          { external_effect_state: 'outcome_unknown', updated_at: '2026-09-30T16:01:02Z' },
          { external_effect_state: 'succeeded', updated_at: '2026-09-30T16:01:02Z' },
          { external_effect_state: 'unknown_after_dispatch', updated_at: '2026-09-30T16:01:02Z' },
          { external_effect_state: 'simulated', updated_at: '2026-09-30T16:01:02Z' },
          { external_effect_state: 'blocked', updated_at: '2026-09-30T16:01:02Z' },
          { external_effect_state: 'cancelled', updated_at: '2026-09-30T16:01:02Z' },
        ],
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  detailDom.window.eval(host);
  await pause();
  await detailDom.window.fetch('/api/admin/refunds');
  await detailDom.window.fetch('/api/admin/wechat-pay/orders/M-ORDER-TEST-0001/external-push-deliveries');
  await new Promise((resolve) => setTimeout(resolve, 60));
  const refundFetch = detailCalls.find((call) => call.url.pathname === '/api/admin/refunds');
  assert.equal(refundFetch.url.searchParams.get('provider'), 'wechat', 'detail refund fetch must select the exact payment provider');
  assert.equal(refundFetch.url.searchParams.get('order_no'), 'M-ORDER-TEST-0001', 'detail refund fetch must select the exact merchant order');
  assert.match(detailDom.window.document.body.textContent, /订单信息/, 'detail must use the business partition');
  assert.match(detailDom.window.document.body.textContent, /支付信息/, 'payment facts belong in their own partition');
  assert.match(detailDom.window.document.body.textContent, /买家信息/, 'buyer facts belong in their own partition');
  assert.match(detailDom.window.document.body.textContent, /商品与金额/, 'item and amount facts belong in their own partition');
  assert.match(detailDom.window.document.body.textContent, /分销信息/, 'detail must include the Distribution-owned item snapshot section');
  assert.match(detailDom.window.document.body.textContent, /冻结佣金比例12.34%/, 'detail must use the frozen attribution ratio, not current product policy');
  assert.match(detailDom.window.document.body.textContent, /退款复核等待7 天/, 'detail must show the frozen refund-review wait days');
  assert.match(detailDom.window.document.body.textContent, /当前佣金总额（含已分账）¥2\.22/, 'the preserved current payable field is labeled as a total, never inferred as an unpaid balance');
  assert.match(detailDom.window.document.body.textContent, /分账成功确认时间2026-10-09 00:01:02/, 'detail labels audit fact as system split confirmation, not bank arrival');
  assert.match(detailDom.window.document.body.textContent, /买家退款调整/, 'partial refund adjustment evidence remains visible');
  assert.match(detailDom.window.document.body.textContent, /退款后已分账/, 'exception evidence remains visible');
  assert.match(detailDom.window.document.body.textContent, /分账记录 dstl_1.*分账成功确认时间\s*2026-10-09 00:01:02/, 'each settlement must use its own audit confirmation time');
  assert.match(detailDom.window.document.body.textContent, /分账记录 dstl_2.*分账成功确认时间\s*未记录/, 'a settlement without an audit fact must not borrow created or updated time');
  assert.match(detailDom.window.document.body.textContent, /CID-101/, 'customer information must expose a business-facing canonical customer number');
  assert.ok(!detailDom.window.document.body.textContent.includes('customer:101'), 'the internal canonical key must not be shown directly');
  assert.match(detailDom.window.document.body.textContent, /4200000000000000000000000000/, 'the true provider transaction identifier remains available for confirmation');
  assert.match(detailDom.window.document.body.textContent, /退款完成/, 'refund status must be a Chinese refund-domain label');
  assert.match(detailDom.window.document.body.textContent, /¥20.00/, 'refund rows must use the scoped API refund_amount_total minor-unit field');
  assert.match(detailDom.window.document.body.textContent, /退款金额（最多 ¥20.00）/, 'the native form must default and cap to refundable_amount_total');
  assert.match(detailDom.window.document.body.textContent, /2026-10-01 00:01:02/, 'detail time must be fixed to Asia\/Shanghai seconds');
  assert.match(detailDom.window.document.body.textContent, /外部处理记录/, 'external feedback must remain visible in a separate Chinese section');
  assert.match(detailDom.window.document.body.textContent, /结果待核对/, 'external-effect state must not expose its raw enum');
  for (const [raw, label] of [
    ['succeeded', '历史记录：外推成功'],
    ['unknown_after_dispatch', '历史记录：已发起，结果待核对'],
    ['simulated', '历史记录：模拟处理'],
    ['blocked', '历史记录：已拦截，外部结果待核对'],
    ['cancelled', '历史记录：已取消，外部结果待核对'],
  ]) {
    assert.match(detailDom.window.document.body.textContent, new RegExp(label), `legacy effect ${raw} must have its own factual Chinese presentation`);
    assert.ok(!detailDom.window.document.body.textContent.includes(raw), `legacy effect ${raw} must not expose a raw enum`);
  }
  assert.ok(!detailDom.window.document.body.textContent.includes('事件时间线'), 'the donor mixed timeline must be replaced by scoped external feedback');
  assert.match(detailDom.window.document.body.textContent, /再次输入微信支付交易单号/, 'refund confirmation must ask for transaction_id, not merchant order number');
} finally {
  detailDom.window.close();
}

const historyDom = new JSDOM(`<!doctype html><body data-page="orderDetail">
  <div><div><h2>订单详情</h2></div><div></div></div>
  <div><div><h2>事件时间线</h2></div><div></div></div>
  <div><div><h2>V1 历史只读</h2></div><div></div></div>
</body>`, {
  url: 'https://test.invalid/admin/orderDetail.html?id=M-HISTORY-TEST-0001', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage([
        { refund_no: 'H-R-1', refund_amount_total: 100, status: 'history_requested', created_at: '2026-10-01T00:01:02Z' },
        { refund_no: 'H-R-2', refund_amount_total: 100, status: 'history_processing', created_at: '2026-10-01T00:01:02Z' },
        { refund_no: 'H-R-3', refund_amount_total: 100, status: 'history_failed', created_at: '2026-10-01T00:01:02Z' },
        { refund_no: 'H-R-4', refund_amount_total: 100, status: 'history_closed', created_at: '2026-10-01T00:01:02Z' },
        { refund_no: 'H-R-5', refund_amount_total: 100, status: 'legacy_unclassified', created_at: '2026-10-01T00:01:02Z' },
      ])), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({
        record_origin: 'v1_history', merchant_order_no: 'M-HISTORY-TEST-0001', provider: 'wechat',
        payer_name: '测试买家', payer_id: 'customer:102', product_name: '历史测试商品', amount_yuan: '20.00', created_at: '2026-10-01T00:01:02Z', status: 'paid',
      }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  historyDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 30));
  await historyDom.window.fetch('/api/admin/refunds');
  await new Promise((resolve) => setTimeout(resolve, 30));
  const bodyText = historyDom.window.document.body.textContent;
  assert.match(bodyText, /历史订单，仅供查询/, 'historical users must not see V1\/V2 implementation wording');
  assert.ok(!bodyText.includes('V1'), 'historical presentation must not expose a V1 technical generation label');
  assert.match(bodyText, /历史记录：已支付/, 'historical paid data must remain a history fact, not a current provider-confirmed assertion');
  for (const label of ['历史记录：已申请', '历史记录：处理中', '历史记录：失败', '历史记录：已关闭']) assert.match(bodyText, new RegExp(label), `historical refund fact ${label} must remain distinct`);
  assert.match(bodyText, /退款状态待核对/, 'unknown historical refund values must not be presented as failed or raw machine states');
  assert.ok(!bodyText.includes('legacy_unclassified'), 'historical unknown values must not expose a raw enum');
} finally {
  historyDom.window.close();
}

console.log('order detail scope, Chinese presentation, and historical read-only journey: PASS');

function nativeOrderFixture(orderNo = 'M-REFUND-TEST-0001') {
  return {
    record_origin: 'native', merchant_order_no: orderNo, provider: 'wechat',
    transaction_id: '4200000000000000000000000001', payer_name: '退款测试买家', payer_id: 'customer:201', payer_phone_masked: '139****0001',
    product_name: '退款测试商品', amount_yuan: '20.00', refundable_amount_total: 2000,
    created_at: '2026-09-30T16:01:02Z', status: 'paid',
  };
}

function refundDetailHTML(orderNo) {
  return `<!doctype html><body data-page="orderDetail">
    <div><span>${orderNo}</span><span>paid</span></div>
    <div><div><h2>订单详情</h2></div><div></div></div>
    <div><div><h2>事件时间线</h2></div><div></div></div>
    <div><div><h2>申请退款</h2></div><div></div></div>
  </body>`;
}

const unknownCalls = [];
let resolveUnknownPost;
let unknownIntentStorage = '';
const unknownOrderNo = 'M-REFUND-TEST-UNKNOWN';
const unknownDom = new JSDOM(refundDetailHTML(unknownOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${unknownOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      unknownCalls.push({ url, method, body: init.body, idempotencyKey: new Headers(init.headers).get('Idempotency-Key') });
      if (url.pathname === `/api/admin/orders/${unknownOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(unknownOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') return recoveryNotFound();
      if (url.pathname === '/api/admin/refunds' && method === 'GET') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === `/api/admin/wechat-pay/orders/${unknownOrderNo}/refunds` && method === 'POST') return new Promise((resolve) => { resolveUnknownPost = resolve; });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  unknownDom.window.eval(host);
  await pause();
  await unknownDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = unknownDom.window.document;
  const amount = document.querySelector('[data-order-refund-amount]');
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const reason = document.querySelector('[data-order-refund-reason]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(amount && transaction && checked && submit, 'a native order with a known refundable amount must expose a guarded refund form');
  assert.ok(amount.classList.contains('input') && transaction.classList.contains('input'), 'refund text controls use the standard admin input class');
  assert.ok(reason?.classList.contains('select'), 'refund reason uses the standard admin select class');
  assert.ok(submit.classList.contains('btn') && submit.classList.contains('primary'), 'refund submit uses the standard primary admin button class');
  transaction.value = '4200000000000000000000000001';
  checked.checked = true;
  submit.dispatchEvent(new unknownDom.window.MouseEvent('click', { bubbles: true }));
  await pause();
  assert.equal(unknownCalls.filter((call) => call.method === 'POST').length, 1, 'the first click creates one refund intent');
  amount.value = '1.00';
  submit.dispatchEvent(new unknownDom.window.MouseEvent('click', { bubbles: true }));
  submit.dispatchEvent(new unknownDom.window.MouseEvent('click', { bubbles: true }));
  await pause();
  assert.equal(unknownCalls.filter((call) => call.method === 'POST').length, 1, 'double-clicks or a changed payload may not create another refund request');
  assert.match(document.body.textContent, /退款申请正在提交/, 'a second click while the original payload is unresolved remains locked to the original submission');
  assert.ok(unknownCalls.find((call) => call.method === 'POST')?.idempotencyKey, 'the unresolved intent receives one stable idempotency key');
  resolveUnknownPost(new Response(JSON.stringify({ error: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } }));
  await new Promise((resolve) => setTimeout(resolve, 40));
  unknownIntentStorage = unknownDom.window.localStorage.getItem('aicrm.order-refund-intents.v1') || '';
  const durableUnknown = JSON.parse(unknownIntentStorage);
  assert.deepEqual(Object.keys(durableUnknown[0]).sort(), ['actor_binding', 'idempotency_key', 'order_no', 'payload_digest', 'provider', 'state'], 'durable lock state contains only scope, key, digest, and state');
  assert.match(durableUnknown[0].payload_digest, /^[a-f0-9]{64}$/, 'durable refund state stores a SHA-256 payload digest');
  assert.equal(durableUnknown[0].actor_binding, refundActorBinding, 'durable refund state is partitioned by the current authenticated actor marker');
  assert.ok(!unknownIntentStorage.includes('4200000000000000000000000001'), 'the provider transaction number is never persisted with the refund intent');
  assert.match(document.body.textContent, /退款金额、原因和交易单号已锁定/, 'a lost or 5xx response locks the original payload and prevents any changed request');
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'unknown intent state removes mutable refund controls');
  const readBack = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '读取当前订单退款记录');
  assert.ok(readBack, 'unknown intent state offers readback instead of a mutation retry');
  readBack.click();
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.equal(unknownCalls.filter((call) => call.method === 'POST').length, 1, 'readback performs no second refund mutation');
  assert.ok(unknownCalls.filter((call) => call.url.pathname === '/api/admin/refunds').every((call) => call.url.searchParams.get('provider') === 'wechat' && call.url.searchParams.get('order_no') === unknownOrderNo), 'all detail refund reads remain exact Payment-scoped');
} finally {
  unknownDom.window.close();
}

const restoredCalls = [];
const restoredDom = new JSDOM(refundDetailHTML(unknownOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${unknownOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.localStorage.setItem('aicrm.order-refund-intents.v1', unknownIntentStorage);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      restoredCalls.push({ url, method: init.method || 'GET' });
      if (url.pathname === `/api/admin/orders/${unknownOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(unknownOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') return recoveryNotFound();
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  restoredDom.window.eval(host);
  await pause();
  await restoredDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = restoredDom.window.document;
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'a refresh restores the unknown refund lock even before a new mutation can be formed');
  assert.ok(Array.from(document.querySelectorAll('button')).some((button) => button.textContent === '读取当前订单退款记录'), 'a restored unknown lock offers scoped readback');
  assert.equal(restoredCalls.filter((call) => call.method === 'POST').length, 0, 'a restored unknown intent cannot create a new refund request');
} finally {
  restoredDom.window.close();
}

const recoveryOrderNo = 'M-REFUND-TEST-RECOVERY';
const recoveryCalls = [];
let recoveryRows = [{ id: 702, refund_id: 'RF-other', refund_amount_total: 1000, status: 'completed', created_at: '2026-09-30T16:01:02Z' }];
const recoveryDom = new JSDOM(refundDetailHTML(recoveryOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${recoveryOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.localStorage.setItem('aicrm.order-refund-intents.v1', JSON.stringify([{
      provider: 'wechat', order_no: recoveryOrderNo, idempotency_key: 'refund-recovery-key-0001', payload_digest: 'a'.repeat(64), actor_binding: refundActorBinding, state: 'unknown',
    }]));
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      recoveryCalls.push({ url, method: init.method || 'GET', key: new Headers(init.headers).get('Idempotency-Key'), cache: init.cache });
      if (url.pathname === `/api/admin/orders/${recoveryOrderNo}`) return new Response(JSON.stringify({ ...nativeOrderFixture(recoveryOrderNo), refundable_amount_total: 1000 }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') {
        const key = new Headers(init.headers).get('Idempotency-Key');
        if (key !== 'refund-recovery-key-0001') return recoveryNotFound();
        return new Response(JSON.stringify({
          found: true, receipt_id: 701, refund_no: 'RF-recovery-701', status: 'completed', actor_binding: refundActorBinding,
        }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage(recoveryRows)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  recoveryDom.window.eval(host);
  await pause();
  await recoveryDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = recoveryDom.window.document;
  let readback = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '读取当前订单退款记录');
  assert.ok(readback);
  const refundReadbackStart = recoveryCalls.filter((call) => call.url.pathname === '/api/admin/refunds').length;
  readback.click(); await new Promise((resolve) => setTimeout(resolve, 45));
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'a completed refund for another receipt cannot release the recovered intent');
  assert.equal(Array.from(document.querySelectorAll('button')).some((button) => button.textContent === '开启新的退款申请'), false, 'a same-order but different receipt cannot unlock a later partial refund');
  const recoveryCall = recoveryCalls.find((call) => call.url.pathname === '/api/admin/refunds/recovery' && call.key === 'refund-recovery-key-0001');
  assert.equal(recoveryCall.url.searchParams.get('provider'), 'wechat', 'recovery lookup remains Payment-scoped');
  assert.equal(recoveryCall.url.searchParams.get('order_no'), recoveryOrderNo, 'recovery lookup carries the exact merchant order in the query');
  assert.equal(recoveryCall.key, 'refund-recovery-key-0001', 'the original key is sent only in a request header');
  assert.equal(recoveryCall.cache, 'no-store', 'recovery reads never reuse a receipt cached for another original idempotency key');
  assert.ok(recoveryCalls.filter((call) => call.url.pathname === '/api/admin/refunds').slice(refundReadbackStart).every((call) => call.cache === 'no-store'), 'current-order refund readbacks bypass the browser HTTP cache');

  recoveryRows = [{ id: 701, refund_id: 'RF-recovery-701', refund_amount_total: 1000, status: 'completed', created_at: '2026-09-30T16:01:02Z' }];
  readback = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '读取当前订单退款记录');
  assert.ok(readback);
  readback.click(); await new Promise((resolve) => setTimeout(resolve, 45));
  const restart = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '开启新的退款申请');
  assert.ok(restart, 'only the exact recovered receipt in a terminal state may offer a new partial refund application');
  restart.click(); await pause();
  assert.ok(document.querySelector('[data-order-refund-amount]'), 'the explicit restart clears only the terminal recovered intent');
  assert.match(document.body.textContent, /退款金额（最多 ¥10.00）/, 'a new refund form uses the freshly read remaining refundable amount');
} finally {
  recoveryDom.window.close();
}


const bindingRetryOrderNo = 'M-REFUND-TEST-BINDING-RETRY';
let bindingRetryCalls = 0;
const bindingRetryDom = new JSDOM(refundDetailHTML(bindingRetryOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${bindingRetryOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.localStorage.setItem('aicrm.order-refund-intents.v1', JSON.stringify([{
      provider: 'wechat', order_no: bindingRetryOrderNo, idempotency_key: 'refund-binding-retry-key', payload_digest: 'd'.repeat(64), actor_binding: refundActorBinding, state: 'unknown',
    }]));
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${bindingRetryOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(bindingRetryOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') {
        bindingRetryCalls += 1;
        return bindingRetryCalls === 1 ? new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } }) : recoveryNotFound();
      }
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  bindingRetryDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 45));
  const document = bindingRetryDom.window.document;
  const retry = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '重新核验当前登录账号');
  assert.ok(retry, 'a failed actor-binding read stops in a visible read-only state with a manual retry');
  retry.click(); await new Promise((resolve) => setTimeout(resolve, 45));
  assert.equal(bindingRetryCalls, 2, 'actor-binding failure does not loop automatically and a single manual retry can recover');
  assert.ok(Array.from(document.querySelectorAll('button')).some((button) => button.textContent === '读取当前订单退款记录'), 'the recovered actor partition restores only the original readback control');
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'an unresolved durable intent remains non-mutating after an actor-binding retry');
} finally {
  bindingRetryDom.window.close();
}

const malformedIntentOrderNo = 'M-REFUND-TEST-INTENT-STORAGE';
let malformedIntentPosts = 0;
const malformedIntentDom = new JSDOM(refundDetailHTML(malformedIntentOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${malformedIntentOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.localStorage.setItem('aicrm.order-refund-intents.v1', '{malformed');
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${malformedIntentOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(malformedIntentOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (init.method === 'POST') malformedIntentPosts += 1;
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  malformedIntentDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 35));
  const document = malformedIntentDom.window.document;
  assert.match(document.body.textContent, /退款确认记录无法安全读取/, 'malformed durable intent storage fails closed instead of becoming an empty lock set');
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'malformed durable storage never enables a new refund mutation');
  const readback = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '读取当前订单退款记录');
  assert.ok(readback, 'malformed durable storage preserves the read-only scoped readback path');
  readback.click(); await pause();
  assert.equal(malformedIntentPosts, 0, 'readback after malformed intent storage cannot overwrite it with a new request');
  assert.equal(malformedIntentDom.window.localStorage.getItem('aicrm.order-refund-intents.v1'), '{malformed', 'malformed durable storage is not overwritten by a failed-open parser');
} finally {
  malformedIntentDom.window.close();
}

const staleOrderOrderNo = 'M-REFUND-TEST-STALE-ORDER';
const staleOrderDom = new JSDOM(refundDetailHTML(staleOrderOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${staleOrderOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.localStorage.setItem('aicrm.order-refund-intents.v1', JSON.stringify([{
      provider: 'wechat', order_no: staleOrderOrderNo, idempotency_key: 'refund-stale-order-key', payload_digest: 'e'.repeat(64), actor_binding: refundActorBinding, state: 'unknown',
    }]));
    let orderReads = 0;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${staleOrderOrderNo}`) {
        orderReads += 1;
        return orderReads === 1
          ? new Response(JSON.stringify(nativeOrderFixture(staleOrderOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } })
          : new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      }
      if (url.pathname === '/api/admin/refunds/recovery') {
        const key = new Headers(init.headers).get('Idempotency-Key');
        return key === 'refund-stale-order-key'
          ? new Response(JSON.stringify({ found: true, receipt_id: 818, refund_no: 'RF-stale-order', status: 'completed', actor_binding: refundActorBinding }), { status: 200, headers: { 'Content-Type': 'application/json' } })
          : recoveryNotFound();
      }
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage([{ id: 818, refund_id: 'RF-stale-order', refund_amount_total: 1000, status: 'completed', created_at: '2026-09-30T16:01:02Z' }])), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  staleOrderDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 45));
  const document = staleOrderDom.window.document;
  const readback = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '读取当前订单退款记录');
  assert.ok(readback);
  readback.click(); await new Promise((resolve) => setTimeout(resolve, 45));
  assert.match(document.body.textContent, /当前可退金额无法确认/, 'a fresh refund page alone cannot reuse a stale order refundable amount');
  assert.equal(Array.from(document.querySelectorAll('button')).some((button) => button.textContent === '开启新的退款申请'), false, 'failed fresh order readback never unlocks a later partial refund');
  assert.equal(document.querySelector('[data-order-refund-amount]'), null, 'failed fresh order readback never recreates an editable refund form');
} finally {
  staleOrderDom.window.close();
}

const nonTerminalOrderNo = 'M-REFUND-TEST-NONTERMINAL';
const nonTerminalDom = new JSDOM(refundDetailHTML(nonTerminalOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${nonTerminalOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${nonTerminalOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(nonTerminalOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage([
        { refund_amount_total: 2000, status: 'effect_accepted', created_at: '2026-09-30T16:01:02Z' },
      ])), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  nonTerminalDom.window.eval(host);
  await pause();
  await nonTerminalDom.window.fetch('/api/admin/refunds');
  await pause();
  assert.equal(nonTerminalDom.window.document.querySelector('[data-order-refund-amount]'), null, 'a nonterminal refund found by server readback disables a new refund request');
  assert.match(nonTerminalDom.window.document.body.textContent, /正在处理中或待核对/, 'the server-side nonterminal refund state explains why a new request is blocked');
} finally {
  nonTerminalDom.window.close();
}

const acceptedCalls = [];
const acceptedOrderNo = 'M-REFUND-TEST-ACCEPTED';
const acceptedDom = new JSDOM(refundDetailHTML(acceptedOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${acceptedOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      acceptedCalls.push({ url, method, body: init.body });
      if (url.pathname === `/api/admin/orders/${acceptedOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(acceptedOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') return recoveryNotFound();
      if (url.pathname === '/api/admin/refunds' && method === 'GET') return new Response(JSON.stringify(scopedRefundPage(
        acceptedCalls.some((call) => call.method === 'POST')
          ? [{ refund_amount_total: 2000, status: 'effect_accepted', reason: '测试原因', created_at: '2026-09-30T16:01:02Z' }]
          : [],
      )), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === `/api/admin/wechat-pay/orders/${acceptedOrderNo}/refunds` && method === 'POST') return new Response(JSON.stringify({
        id: 901, refund_id: 'RF-TEST-ACCEPTED', out_refund_no: 'RF-TEST-ACCEPTED', status: 'pending_external_gate', external_effect_id: '21', auto_retry_allowed: false,
      }), { status: 202, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  acceptedDom.window.eval(host);
  await pause();
  await acceptedDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = acceptedDom.window.document;
  const amount = document.querySelector('[data-order-refund-amount]');
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(amount && transaction && checked && submit);
  amount.value = '0'; transaction.value = '4200000000000000000000000001'; checked.checked = true;
  submit.click(); await pause();
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 0, 'zero amount is rejected before any refund request');
  amount.value = '-1';
  submit.click(); await pause();
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 0, 'an invalid negative amount cannot bypass the refund amount validation');
  amount.value = '20.01'; transaction.value = '4200000000000000000000000001'; checked.checked = true;
  submit.click(); await pause();
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 0, 'the browser blocks an amount above refundable_amount_total before any request');
  amount.value = '20.00'; transaction.value = 'v3pay_not_a_transaction';
  submit.click(); await pause();
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 0, 'the browser blocks a merchant-order style confirmation before any request');
  transaction.value = '4200000000000000000000000001';
  submit.click();
  await new Promise((resolve) => setTimeout(resolve, 60));
  assert.equal(acceptedCalls.filter((call) => call.method === 'POST').length, 1, 'a verified transaction confirmation produces one request');
  assert.match(document.body.textContent, /退款申请已受理/, 'accepted intent state must be presented as accepted, not completed');
  assert.match(document.body.textContent, /¥20.00/, 'the success path reads the exact refund page and renders its minor-unit amount');
  assert.ok(acceptedCalls.filter((call) => call.url.pathname === '/api/admin/refunds').every((call) => call.url.searchParams.get('provider') === 'wechat' && call.url.searchParams.get('order_no') === acceptedOrderNo), 'success readback remains Payment-scoped');
} finally {
  acceptedDom.window.close();
}


const delayedDigestOrderNo = 'M-REFUND-TEST-DELAYED-DIGEST';
const delayedDigestCalls = [];
let releaseDelayedDigest;
let delayedDigestStarted = false;
const delayedDigestDom = new JSDOM(refundDetailHTML(delayedDigestOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${delayedDigestOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    const platformDigest = webcrypto.subtle.digest.bind(webcrypto.subtle);
    Object.defineProperty(window, 'crypto', { configurable: true, value: {
      randomUUID: webcrypto.randomUUID.bind(webcrypto),
      subtle: {
        digest(...args) {
          if (delayedDigestStarted) return platformDigest(...args);
          delayedDigestStarted = true;
          return new Promise((resolve) => {
            releaseDelayedDigest = () => { void platformDigest(...args).then(resolve); };
          });
        },
      },
    } });
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      delayedDigestCalls.push({ url, method });
      if (url.pathname === `/api/admin/orders/${delayedDigestOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(delayedDigestOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') return recoveryNotFound();
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === `/api/admin/wechat-pay/orders/${delayedDigestOrderNo}/refunds` && method === 'POST') return new Response(JSON.stringify({
        id: 880, refund_id: 'RF-delayed-digest', out_refund_no: 'RF-delayed-digest', status: 'pending_external_gate', external_effect_id: 'fixture-effect-880', auto_retry_allowed: false,
      }), { status: 202, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  delayedDigestDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 35));
  const document = delayedDigestDom.window.document;
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(transaction && checked && submit);
  transaction.value = '4200000000000000000000000001'; checked.checked = true;
  submit.click();
  for (let attempt = 0; attempt < 20 && !releaseDelayedDigest; attempt += 1) await pause();
  assert.equal(typeof releaseDelayedDigest, 'function', 'the test must hold the first payload digest after the submission lock is claimed');
  submit.click(); await pause();
  assert.equal(delayedDigestCalls.filter((call) => call.method === 'POST').length, 0, 'a second click while SHA-256 is delayed cannot issue an early second refund POST');
  releaseDelayedDigest();
  await new Promise((resolve) => setTimeout(resolve, 70));
  assert.equal(delayedDigestCalls.filter((call) => call.method === 'POST').length, 1, 'two clicks while digesting issue exactly one refund POST with one stable intent');
} finally {
  delayedDigestDom.window.close();
}

const changedActorOrderNo = 'M-REFUND-TEST-ACTOR-CHANGED';
let changedActorReads = 0;
let changedActorPosts = 0;
const changedActorDom = new JSDOM(refundDetailHTML(changedActorOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${changedActorOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      if (url.pathname === `/api/admin/orders/${changedActorOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(changedActorOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') {
        changedActorReads += 1;
        return recoveryNotFound(changedActorReads === 1 ? refundActorBinding : alternateRefundActorBinding);
      }
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (method === 'POST') changedActorPosts += 1;
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  changedActorDom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 35));
  const document = changedActorDom.window.document;
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(transaction && checked && submit);
  transaction.value = '4200000000000000000000000001'; checked.checked = true;
  submit.click(); await new Promise((resolve) => setTimeout(resolve, 45));
  assert.equal(changedActorReads, 2, 'a refund submission re-reads the current authenticated actor marker immediately before POST');
  assert.equal(changedActorPosts, 0, 'a changed current actor marker stops before persisting or issuing a refund mutation');
  assert.equal(changedActorDom.window.localStorage.getItem('aicrm.order-refund-intents.v1'), null, 'a changed actor marker does not create a cross-actor durable refund lock');
  assert.match(document.body.textContent, /当前登录账号已变化/, 'a changed current actor is explained without exposing an internal principal identifier');
} finally {
  changedActorDom.window.close();
}

for (const acceptanceFailure of [
  { label: 'a 200 login document', status: 200, body: '<html>login</html>', contentType: 'text/html' },
  { label: 'a malformed 202 body', status: 202, body: '{not-json', contentType: 'application/json' },
]) {
  const orderNo = `M-REFUND-TEST-RECEIPT-${acceptanceFailure.status}`;
  const receiptDom = new JSDOM(refundDetailHTML(orderNo), {
    url: `https://test.invalid/admin/orderDetail.html?id=${orderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
    virtualConsole: new VirtualConsole(),
    beforeParse(window) {
      browserRuntime(window);
      window.fetch = async (input, init = {}) => {
        const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
        const method = init.method || 'GET';
        if (url.pathname === `/api/admin/orders/${orderNo}`) return new Response(JSON.stringify(nativeOrderFixture(orderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
        if (url.pathname === '/api/admin/refunds/recovery') return recoveryNotFound();
        if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
        if (url.pathname === `/api/admin/wechat-pay/orders/${orderNo}/refunds` && method === 'POST') return new Response(acceptanceFailure.body, { status: acceptanceFailure.status, headers: { 'Content-Type': acceptanceFailure.contentType } });
        return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      };
    },
  });
  try {
    receiptDom.window.eval(host);
    await pause();
    await receiptDom.window.fetch('/api/admin/refunds');
    await pause();
    const document = receiptDom.window.document;
    const transaction = document.querySelector('[data-order-refund-transaction]');
    const checked = document.querySelector('[data-order-refund-checked]');
    const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
    assert.ok(transaction && checked && submit);
    transaction.value = '4200000000000000000000000001'; checked.checked = true;
    submit.click();
    await new Promise((resolve) => setTimeout(resolve, 35));
    assert.match(document.body.textContent, /退款申请结果待核对/, `${acceptanceFailure.label} cannot be presented as an accepted refund receipt`);
    assert.equal(document.querySelector('[data-order-refund-amount]'), null, `${acceptanceFailure.label} keeps the original intent read-only`);
    const stored = JSON.parse(receiptDom.window.localStorage.getItem('aicrm.order-refund-intents.v1'));
    assert.equal(stored[0].state, 'unknown', `${acceptanceFailure.label} preserves the stable idempotency intent for readback`);
  } finally {
    receiptDom.window.close();
  }
}

const rejectedCalls = [];
const rejectedOrderNo = 'M-REFUND-TEST-REJECTED';
const rejectedDom = new JSDOM(refundDetailHTML(rejectedOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${rejectedOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      const method = init.method || 'GET';
      rejectedCalls.push({ url, method });
      if (url.pathname === `/api/admin/orders/${rejectedOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(rejectedOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds/recovery') return recoveryNotFound();
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === `/api/admin/wechat-pay/orders/${rejectedOrderNo}/refunds` && method === 'POST') return new Response(JSON.stringify({ code: 'invalid_request' }), { status: 400, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  rejectedDom.window.eval(host);
  await pause();
  await rejectedDom.window.fetch('/api/admin/refunds');
  await pause();
  const document = rejectedDom.window.document;
  const transaction = document.querySelector('[data-order-refund-transaction]');
  const checked = document.querySelector('[data-order-refund-checked]');
  const submit = Array.from(document.querySelectorAll('button')).find((button) => button.textContent === '确认提交退款申请');
  assert.ok(transaction && checked && submit);
  transaction.value = '4200000000000000000000000001'; checked.checked = true;
  submit.click(); await new Promise((resolve) => setTimeout(resolve, 35));
  assert.equal(document.querySelector('[data-order-refund-amount]') != null, true, 'only a recognized service rejection may restore the editable confirmation form');
  assert.equal(JSON.parse(rejectedDom.window.localStorage.getItem('aicrm.order-refund-intents.v1')).length, 0, 'a recognized validation rejection clears the durable intent');
  submit.click(); await new Promise((resolve) => setTimeout(resolve, 35));
  assert.equal(rejectedCalls.filter((call) => call.method === 'POST').length, 2, 'a known server-side validation rejection, unlike a proxy failure, permits a corrected retry');
} finally {
  rejectedDom.window.close();
}

const unavailableOrderNo = 'M-REFUND-TEST-UNAVAILABLE';
const unavailableDom = new JSDOM(refundDetailHTML(unavailableOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${unavailableOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${unavailableOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(unavailableOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify({ error: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  unavailableDom.window.eval(host);
  await pause();
  await unavailableDom.window.fetch('/api/admin/refunds');
  await new Promise((resolve) => setTimeout(resolve, 40));
  assert.match(unavailableDom.window.document.body.textContent, /退款记录暂不可读取/, 'a non-200 scoped refund read must be unavailable, never rendered as an empty page');
  assert.equal(unavailableDom.window.document.querySelector('[data-order-refund-amount]'), null, 'refund mutation is disabled when the exact refund page cannot be read');
  assert.ok(Array.from(unavailableDom.window.document.querySelectorAll('button')).some((button) => button.textContent === '读取当前订单退款记录'), 'an unavailable read still provides only the scoped readback recovery action');
} finally {
  unavailableDom.window.close();
}

const malformedOrderNo = 'M-REFUND-TEST-MALFORMED';
const malformedDom = new JSDOM(refundDetailHTML(malformedOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${malformedOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${malformedOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(malformedOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  malformedDom.window.eval(host);
  await pause();
  await malformedDom.window.fetch('/api/admin/refunds');
  await pause();
  assert.match(malformedDom.window.document.body.textContent, /退款记录暂不可读取/, 'a malformed 200 refund page fails closed rather than becoming an empty refund list');
  assert.equal(malformedDom.window.document.querySelector('[data-order-refund-amount]'), null, 'a malformed 200 refund page never enables a refund mutation');
} finally {
  malformedDom.window.close();
}

const networkOrderNo = 'M-REFUND-TEST-NETWORK';
const networkDom = new JSDOM(refundDetailHTML(networkOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${networkOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${networkOrderNo}`) return new Response(JSON.stringify(nativeOrderFixture(networkOrderNo)), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') throw new Error('network down');
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  networkDom.window.eval(host);
  await pause();
  await assert.rejects(networkDom.window.fetch('/api/admin/refunds'), /network down/);
  await pause();
  assert.match(networkDom.window.document.body.textContent, /退款记录暂不可读取/, 'an initial refund read network failure marks the page unavailable');
  assert.equal(networkDom.window.document.querySelector('[data-order-refund-amount]'), null, 'a network failure never leaves the initial refund form writable');
  assert.ok(Array.from(networkDom.window.document.querySelectorAll('button')).some((button) => button.textContent === '读取当前订单退款记录'), 'network failure keeps the scoped readback recovery control');
} finally {
  networkDom.window.close();
}


const attributedUnpaidOrderNo = 'M-DISTRIBUTION-UNPAID';
const attributedUnpaidDom = new JSDOM(refundDetailHTML(attributedUnpaidOrderNo), {
  url: `https://test.invalid/admin/orderDetail.html?id=${attributedUnpaidOrderNo}`, runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: new VirtualConsole(),
  beforeParse(window) {
    browserRuntime(window);
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      if (url.pathname === `/api/admin/orders/${attributedUnpaidOrderNo}`) return new Response(JSON.stringify({ ...nativeOrderFixture(attributedUnpaidOrderNo), distribution_read_state: 'available', distribution: [{ item_line: 2, product_name: '待付款归因商品', distributor_display_name: '分销员乙', rate_basis_points: 2345, wait_days: 9, policy_version: 4, has_commission: false, initial_minor: 0, current_payable_minor: 0, paid_minor: 0, currency: 'CNY', status: '', hold_reason: '', cancel_reason: '', exception_reason: '', due_at: null, settlement_confirmed_at: null, adjustments: [], settlements: [], exceptions: [] }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/refunds') return new Response(JSON.stringify(scopedRefundPage()), { status: 200, headers: { 'Content-Type': 'application/json' } });
      return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  attributedUnpaidDom.window.eval(host);
  await pause();
  await attributedUnpaidDom.window.fetch('/api/admin/refunds');
  await pause();
  const body = attributedUnpaidDom.window.document.body.textContent;
  assert.match(body, /分销员乙/, 'unpaid attribution keeps its distributor display name');
  assert.match(body, /冻结佣金比例23.45%/, 'unpaid attribution keeps its frozen policy snapshot');
  assert.match(body, /退款复核等待9 天/, 'unpaid attribution keeps its frozen wait days');
  assert.match(body, /已归因 · 未形成佣金/, 'unpaid attribution is distinct from a zero commission or non-distribution order');
} finally {
  attributedUnpaidDom.window.close();
}

console.log('order refund idempotency, exact readback, and unavailable-state journeys: PASS');
