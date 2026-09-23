#!/usr/bin/env node
import assert from 'node:assert/strict';
import { JSDOM, VirtualConsole } from 'jsdom';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = (await build({
  stdin: {
    contents: "import './web/v3/orderAdapter'; import { AdminController } from './web/src/admin/controller'; import { api } from './web/src/shared/api/client'; window.OrderAdapterController = AdminController; window.OrderAdapterApi = api;",
    resolveDir: repository,
    loader: 'ts',
  },
  bundle: true,
  format: 'iife',
  platform: 'browser',
  write: false,
  logLevel: 'silent',
})).outputFiles[0].text.replace(/<\/script/gi, '<\\/script');
const requests = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', () => undefined);
const dom = new JSDOM(`<!doctype html><body data-page="orders"><div><input id="orderTransactionId" value="WX-1"><input id="orderProductCode" value="sku-a"><input id="orderMobile" value="wm-order-fixture"><button>查询</button></div><table><thead><tr><th>创建时间</th><th>微信 / 平台单号</th><th>付款人 / 客户身份</th><th>商品</th><th>金额</th><th>状态</th><th>支付来源</th><th>操作</th></tr></thead><tbody><tr><td>2026-09-15T00:00:00Z</td><td><div>merchant-ref-9</div></td><td><div>付款人</div><div>customer:1</div></td><td>测试商品</td><td>1.00</td><td><span>paid</span></td><td>微信支付</td><td><a data-capability-state="real">查看详情</a></td></tr></tbody></table><script>${bundle}</script></body>`, {
  url: 'https://test.invalid/admin/orders.html', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.href);
      requests.push(url);
      return new Response(JSON.stringify({ items: [{ id: 1, merchant_order_no: 'merchant-ref-9', detail_url: '/admin/orderDetail.html?id=merchant-ref-9&provider=wechat', provider: 'wechat', provider_label: '微信支付', currency: 'CNY', distribution_read_state: 'available', distribution: [] }] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
try {
  await new Promise((resolve) => setTimeout(resolve, 20));
  const response = await dom.window.fetch('/api/admin/orders?limit=20');
  const body = await response.json();
  assert.equal(body.items[0].currency, 'CNY', 'raw order readers retain the canonical currency');
  assert.equal(body.items[0].provider_label, '微信支付', 'raw order readers keep the separate provider label');
  assert.ok(!JSON.stringify(body).includes('aicrm-order-v3:'), 'raw order JSON never carries renderer correlation data');
  assert.ok(!dom.window.document.body.textContent.includes('aicrm-order-v3:'), 'the host never exposes renderer correlation data in the DOM');
  assert.equal(requests[0].searchParams.get('order_ref'), 'WX-1');
  assert.equal(requests[0].searchParams.get('product'), 'sku-a');
  assert.equal(requests[0].searchParams.get('external_userid'), 'wm-order-fixture');
  assert.equal(requests[0].searchParams.has('customer_id'), false, 'the browser never interprets a search value as an internal customer identity');
  const db = await dom.window.OrderAdapterApi.loadDb({ page: 'orders' });
  const controller = new dom.window.OrderAdapterController({ mode: 'http' }, 'orders');
  controller.db = db;
  const values = controller.renderVals();
  assert.equal(values.rows.orders[0].pay, '微信支付', 'the frozen payment column receives the provider label only in the in-memory renderer DTO');
  dom.window.document.querySelector('tbody tr').append(dom.window.document.createElement('span'));
  await new Promise((resolve) => setTimeout(resolve, 20));
  const link = dom.window.document.querySelector('a');
  link.click();
  assert.match(link.href, /orderDetail\.html\?id=merchant-ref-9/, 'actual detail action must target the server-backed detail route');
  assert.equal(link.textContent, '正在打开…');
  console.log('order host query and detail journey: PASS');
} finally { dom.window.close(); }
