import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM, VirtualConsole } from 'jsdom';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const editorHost = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const materializedSrc = path.join(root, 'web/src');
const transform = (html) => html
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => '<template data-sc-for="' + list + '" data-as="' + as + '">')
  .replace(/<\/sc-for>/g, '</template>')
  .replace(/<sc-if\s+([^>]*?)value="([^"]*)"([^>]*)>/g, (_match, _a, value) => '<template data-sc-if="' + value + '">')
  .replace(/<\/sc-if>/g, '</template>');

const bundle = await build({
  stdin: {
    contents: "import './web/v3/productAdapter';\nimport { AdminController } from './web/src/admin/controller';\nimport { mount } from './web/src/shared/ui/runtime';\nimport { api } from './web/src/shared/api/client';\nwindow.ProductControllerFixture = AdminController;\nwindow.ProductMountFixture = mount;\nwindow.ProductApiFixture = api;",
    resolveDir: root,
    loader: 'ts',
  },
  plugins: [{
    name: 'product-adapter-materialized-view',
    setup(pluginBuild) {
      pluginBuild.onResolve({ filter: /^\.\.\/src\/admin\/main$/ }, () => ({ path: 'empty-admin-main', namespace: 'product-test' }));
      pluginBuild.onLoad({ filter: /.*/, namespace: 'product-test' }, () => ({ contents: 'export {};', loader: 'ts' }));
      pluginBuild.onResolve({ filter: /^\.\.\/src\// }, (args) => {
        const raw = path.join(materializedSrc, args.path.replace('../src/', ''));
        const resolved = ['.ts', '.tsx', '.js'].map((extension) => raw + extension).find(existsSync) || raw;
        return { path: resolved };
      });
    },
  }],
  bundle: true,
  format: 'iife',
  platform: 'browser',
  write: false,
  logLevel: 'silent',
});

const dom = new JSDOM('<!doctype html><body data-page="products"><header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">商品管理</h1></div></header><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/products.html',
  runScripts: 'outside-only',
  pretendToBeVisual: true,
});
const pause = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = check();
    if (value) return value;
    await pause(10);
  }
  throw new Error(message);
}
const source = (resourceId, name, version) => ({
  resourceId, code: 'product-' + resourceId, name, price: '99.00', status: '已启用', tone: 'ok',
  sold: '1', updated: '2026-09-15T01:02:03Z', lifecycle: 'enabled', version,
});

try {
  const calls = [];
  let failOrdinaryOnce = true;
  dom.window.Request = globalThis.Request;
  dom.window.Response = globalThis.Response;
  dom.window.Headers = globalThis.Headers;
  Object.defineProperty(dom.window, 'crypto', { value: globalThis.crypto, configurable: true });
  dom.window.fetch = async (url, init = {}) => {
    const requestURL = new URL(String(url), dom.window.location.href);
    const headers = new dom.window.Headers(init.headers);
    calls.push({ path: requestURL.pathname, method: init.method, body: init.body, key: headers.get('Idempotency-Key') || '' });
    if (requestURL.pathname === '/api/admin/wechat-pay/products/101' && failOrdinaryOnce) {
      failOrdinaryOnce = false;
      return new dom.window.Response(JSON.stringify({ code: 'temporary_failure' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
    }
    return new dom.window.Response(JSON.stringify({ ok: true }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  };
  dom.window.eval(bundle.outputFiles[0].text);
  const mount = dom.window.ProductMountFixture;
  const Controller = dom.window.ProductControllerFixture;
  const stage = dom.window.document.getElementById('stage');
  const actionMenuTrigger = prefix => dom.window.document.querySelector(`[data-table-action-menu-trigger^="${prefix}-"]`);
  const visibleActionMenuPanel = () => [...dom.window.document.querySelectorAll('[data-table-action-menu-panel]')].find((panel) => !panel.hidden);
  const visibleAction = label => {
    const panel = visibleActionMenuPanel();
    return panel && [...panel.querySelectorAll('button')].find((node) => node.textContent?.trim() === label);
  };

  const ordinary = new Controller({ mode: 'mock' }, 'products');
  ordinary.db.rows.products = [source(101, '已下单商品', 3)];
  ordinary.db.rows.spProducts = [];
  ordinary.init = async () => {
    ordinary.db.rows.products = [];
    ordinary.__render?.();
  };
  mount(stage, transform(readFileSync(path.join(root, 'web/src/admin/templates/products.html'), 'utf8')), ordinary);
  const ordinaryHeaderAction = await waitFor(
    () => dom.window.document.querySelector('[data-page-header-actions="product-list-products"] > button'),
    'ordinary create action must relocate to the shared page header',
  );
  assert.equal(ordinaryHeaderAction.textContent, '创建商品');
  assert.equal(stage.textContent.includes('商品管理'), false, 'the donor list heading must not duplicate the shell title');
  assert.match(stage.textContent, /2026-09-15 09:02:03/, 'ordinary product updated time is rendered with the shared Shanghai formatter');
  assert.equal(stage.textContent.includes('2026-09-15T01:02:03Z'), false, 'ordinary product list does not expose raw ISO time');
  const ordinaryMore = await waitFor(
    () => actionMenuTrigger('product-products'),
    'ordinary frozen product row must expose the shared overflow action menu',
  );
  ordinaryMore.click();
  const ordinaryDelete = await waitFor(
    () => visibleAction('删除'),
    'ordinary delete must be reachable through the visible overflow menu',
  );
  ordinaryDelete.click();
  assert.equal(dom.window.document.getElementById('fb-head')?.textContent, '删除商品');
  dom.window.document.getElementById('fb-cancel').click();
  assert.equal(calls.length, 0, 'cancelled ordinary delete must send zero writes');

  ordinaryMore.click();
  const ordinaryConfirmedDelete = await waitFor(
    () => visibleAction('删除'),
    'ordinary delete must remain reachable after cancellation',
  );
  ordinaryConfirmedDelete.click();
  dom.window.document.getElementById('fb-ok').click();
  await waitFor(() => calls.length === 1, 'first ordinary delete request was not sent');
  const retryMenu = await waitFor(
    () => actionMenuTrigger('product-products'),
    'failed delete must retain the visible row and its overflow menu',
  );
  retryMenu.click();
  const retry = await waitFor(
    () => visibleAction('删除'),
    'retry must be reachable through the visible overflow menu',
  );
  retry.click();
  dom.window.document.getElementById('fb-ok').click();
  await waitFor(() => calls.length === 2, 'retry must issue exactly one request');
  assert.equal(calls[0].path, '/api/admin/wechat-pay/products/101');
  assert.equal(calls[0].method, 'DELETE');
  assert.equal(calls[0].body, JSON.stringify({ expected_version: 3 }));
  assert.equal(calls[1].body, calls[0].body, 'retry must retain the original CAS body');
  assert.equal(calls[1].key, calls[0].key, 'retry must retain the original idempotency key');
  await waitFor(() => !stage.textContent.includes('已下单商品'), 'successful owner readback must remove the archived ordinary row');

  dom.window.document.body.dataset.page = 'spProducts';
  dom.window.document.querySelector('.admin-page-title').textContent = '周期商品管理';
  stage.replaceChildren();
  const periodic = new Controller({ mode: 'mock' }, 'spProducts');
  periodic.db.rows.products = [];
  periodic.db.rows.spProducts = [source(201, '已有权益周期商品', 5)];
  periodic.init = async () => {
    periodic.db.rows.spProducts = [];
    periodic.__render?.();
  };
  mount(stage, transform(readFileSync(path.join(root, 'web/src/admin/templates/spProducts.html'), 'utf8')), periodic);
  const periodicHeaderAction = await waitFor(
    () => dom.window.document.querySelector('[data-page-header-actions="product-list-spProducts"] > button'),
    'service-period create action must relocate to the shared page header',
  );
  assert.equal(periodicHeaderAction.textContent, '创建周期商品');
  assert.equal(stage.textContent.includes('周期商品管理'), false, 'the periodic donor heading must not duplicate the shell title');
  const periodicMore = await waitFor(
    () => actionMenuTrigger('product-spProducts'),
    'service-period frozen product row must expose the shared overflow action menu',
  );
  periodicMore.click();
  const periodicDelete = await waitFor(
    () => visibleAction('删除'),
    'service-period delete must be visible in its overflow menu',
  );
  periodicDelete.click();
  assert.equal(dom.window.document.getElementById('fb-head')?.textContent, '删除周期商品');
  dom.window.document.getElementById('fb-ok').click();
  await waitFor(() => calls.some((call) => call.path === '/api/admin/service-period-products/201'), 'periodic delete request was not sent');
  const periodicCall = calls.find((call) => call.path === '/api/admin/service-period-products/201');
  assert.equal(periodicCall.method, 'DELETE');
  assert.equal(periodicCall.body, JSON.stringify({ expected_version: 5 }));
  await waitFor(() => !stage.textContent.includes('已有权益周期商品'), 'successful owner readback must remove the archived periodic row');
} finally {
  dom.window.dispatchEvent(new dom.window.Event('pagehide'));
  dom.window.close();
}

console.log('product and service-period delete DOM lifecycle: PASS');

// The overflow panel lives under document.body, while the original lifecycle
// action was rendered in a particular list row. A refresh may reorder or
// remove rows while a stale panel is still visible. The Host must retain the
// original Product subject (not the old array position), and reject a removed
// source row before it can produce a lifecycle write.
{
  const lifecycleCalls = [];
  let rawProducts = [];
  const raw = (id, version) => ({ id, lifecycle: 'enabled', enabled: true, version, paid_order_count: 1, refund_order_count: 0, sold_count: 1, admin_projection: { enabled: true } });
  const lifecycle = new JSDOM('<!doctype html><body data-page="products"><header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">商品管理</h1></div></header><main id="stage"></main></body>', {
    url: 'https://test.invalid/admin/products.html', runScripts: 'outside-only', pretendToBeVisual: true,
    beforeParse(window) {
      window.__AICRM_TEST_MOCK__ = true;
      window.Request = Request; window.Response = Response; window.Headers = Headers;
      window.fetch = async (url, init = {}) => {
        const requestURL = new URL(String(url), window.location.href);
        const method = String(init.method || 'GET').toUpperCase();
        if (requestURL.pathname === '/api/v1/products') return new Response(JSON.stringify({ items: rawProducts }), { status: 200, headers: { 'Content-Type': 'application/json' } });
        if (method === 'POST' && /^\/api\/admin\/wechat-pay\/products\/(501|502)\/disable$/.test(requestURL.pathname)) {
          lifecycleCalls.push(requestURL.pathname);
          const id = Number(requestURL.pathname.split('/')[5]);
          return new Response(JSON.stringify({ id, version: 4, lifecycle: 'disabled', enabled: false }), { status: 200, headers: { 'Content-Type': 'application/json' } });
        }
        return new Response(JSON.stringify({ items: [] }), { status: 200, headers: { 'Content-Type': 'application/json' } });
      };
    },
  });
  try {
    lifecycle.window.eval(bundle.outputFiles[0].text);
    const api = lifecycle.window.ProductApiFixture;
    const first = source(501, '原始商品 A', 3);
    const second = source(502, '原始商品 B', 3);
    // Seed the MockApi base rows before its Product adapter validates the
    // authoritative `/api/v1/products` facts against those rows.
    rawProducts = [];
    const stored = await api.loadDb({ page: 'products' });
    stored.rows.products = [first, second];
    lifecycle.window.sessionStorage.setItem('aicrm.mock.db.v4', JSON.stringify(stored));
    rawProducts = [raw(501, 3), raw(502, 3)];
    await api.loadDb({ page: 'products' });
    const controller = new lifecycle.window.ProductControllerFixture({ mode: 'mock' }, 'products');
    controller.db.rows.products = [first, second]; controller.db.rows.spProducts = [];
    lifecycle.window.ProductMountFixture(lifecycle.window.document.getElementById('stage'), transform(readFileSync(path.join(root, 'web/src/admin/templates/products.html'), 'utf8')), controller);
    const action = async (name) => {
      const row = await waitFor(() => [...lifecycle.window.document.querySelectorAll('tbody tr')].find((item) => item.textContent.includes(name)), `missing ${name} row`);
      const trigger = await waitFor(() => row.querySelector('button[data-table-action-menu-trigger]'), `missing ${name} overflow trigger`);
      trigger.click();
      return waitFor(() => {
        const panel = lifecycle.window.document.getElementById(trigger.getAttribute('aria-controls'));
        return panel && [...panel.querySelectorAll('button')].find((button) => button.textContent?.trim() === '停用');
      }, `missing ${name} overflow lifecycle action`);
    };
    const originalA = await action('原始商品 A');
    rawProducts = [raw(502, 3), raw(501, 3)];
    await api.loadDb({ page: 'products' });
    originalA.click();
    await waitFor(() => lifecycleCalls.length === 1, 'reordered list did not issue its original lifecycle command');
    assert.deepEqual(lifecycleCalls, ['/api/admin/wechat-pay/products/501/disable'], 'a reordered read must not retarget the retained action to product B');

    // A queued projection can update before an old render is mounted again.
    // Deliberately retain A/B DOM rows while the current projection is B/A:
    // the first button must refuse, never turn A into a write for B.
    controller.db.rows.products = [first, second];
    lifecycle.window.ProductMountFixture(lifecycle.window.document.getElementById('stage'), transform(readFileSync(path.join(root, 'web/src/admin/templates/products.html'), 'utf8')), controller);
    const mismatchedA = await action('原始商品 A');
    assert.equal(mismatchedA.disabled, false, 'the mismatched source action remains an actual enabled action');
    let mismatchedActionClicked = false;
    mismatchedA.addEventListener('click', () => { mismatchedActionClicked = true; });
    mismatchedA.click();
    await pause(20);
    assert.equal(mismatchedActionClicked, true, 'the mismatched source action must receive a real click');
    assert.equal(lifecycleCalls.length, 1, 'a mismatched initial row binding must not issue a lifecycle write');

    const removedB = await action('原始商品 B');
    rawProducts = [raw(501, 3)];
    await api.loadDb({ page: 'products' });
    const bRow = [...lifecycle.window.document.querySelectorAll('tbody tr')].find((item) => item.textContent.includes('原始商品 B'));
    bRow.remove();
    removedB.click();
    await pause(20);
    assert.equal(lifecycleCalls.length, 1, 'a removed source row must not issue a lifecycle write from its stale overflow panel');
    assert.match(lifecycle.window.document.body.textContent, /商品操作上下文已失效/, 'a removed source row gives an explicit no-write refresh message');
  } finally {
    lifecycle.window.dispatchEvent(new lifecycle.window.Event('pagehide'));
    lifecycle.window.close();
  }
}
console.log('product overflow lifecycle retains its original subject and rejects stale rows: PASS');

const projection = {
  schema_version: 1, status: 'archived', enabled: false, buy_button_text: '', require_mobile: false,
  lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '',
  completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null,
  wecom_tagging: {}, slices: [],
};

async function assertArchivedEditor({ periodic, id }) {
  const pageName = periodic ? 'spProductForm.html' : 'productForm.html';
  const route = periodic ? `/admin/service-period-products/${id}/edit` : `/admin/wechat-pay/products/${id}/edit`;
  const page = readFileSync(path.join(root, 'web/dist/admin', pageName), 'utf8');
  const calls = [];
  const errors = [];
  const virtualConsole = new VirtualConsole();
  virtualConsole.on('jsdomError', (error) => errors.push(String(error.message)));
  const archived = periodic
    ? { service_product_id: id, product_code: 'archived-period', name: '已删除周期商品', description: '', price_minor: 100, currency: 'CNY', stock_quantity: 0, images: [], admin_projection: { ...projection, status: 'service_period_archived' }, lifecycle: 'archived', archived: true, enabled: false, version: 6, duration_days: 30, distribution_policy: { enabled: false, commission_rate_basis_points: 0, wait_days: 7, version: 1 } }
    : { id, product_code: 'archived-product', name: '已删除普通商品', description: '', price_minor: 100, currency: 'CNY', stock_quantity: 0, images: [], admin_projection: projection, lifecycle: 'archived', enabled: false, paid_order_count: 1, refund_order_count: 0, sold_count: 1, version: 6, distribution_policy: { enabled: false, commission_rate_basis_points: 0, wait_days: 7, version: 1 } };
  const dom = new JSDOM(page, {
    url: 'https://test.invalid' + route,
    runScripts: 'outside-only',
    pretendToBeVisual: true,
    virtualConsole,
    beforeParse(window) {
      window.__AICRM_TEST_MOCK__ = false;
      window.Request = Request;
      window.Response = Response;
      window.Headers = Headers;
      window.fetch = async (input, init = {}) => {
        const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
        const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
        calls.push({ path: url.pathname, method });
        const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
        if (!periodic && url.pathname === `/api/v1/products/${id}`) return json(archived);
        if (periodic && url.pathname === `/api/admin/service-period-products/${id}`) return json({ ok: true, product: archived });
        if (url.pathname.endsWith('/external-push')) return json({ product_id: id, product_kind: periodic ? 'service_period' : 'wechat_pay', enabled: false, configuration_reference: '', updated_at: '' });
        if (url.pathname.endsWith('/member-grid/access')) return json({ service_product_id: id, access: { role: 'admin' }, can_view: true, can_query: true, can_edit: true, can_manage_views: true, can_share: true });
        if (url.pathname.endsWith('/member-grid/schema')) return json({ service_product_id: id, columns: [] });
        if (url.pathname.endsWith('/member-views')) return json({ service_product_id: id, items: [] });
        if (url.pathname.endsWith('/member-grid/share-settings')) return json({ service_product_id: id, items: [], external_share_supported: true, external_share_enabled: false, external_share_version: 1, real_external_call_executed: false });
        return json({ items: [], groups: [], categories: [], total: 0, has_more: false });
      };
    },
  });
  try {
    dom.window.eval(editorHost);
    dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
    const terminal = await waitFor(
      () => dom.window.document.querySelector('[data-v3-archived-product-editor]'),
      `${periodic ? 'periodic' : 'ordinary'} archived direct URL did not render a terminal page`,
    );
    assert.match(terminal.textContent, /该商品已删除/);
    const back = terminal.querySelector('a');
    assert.equal(back?.getAttribute('href'), periodic ? '/admin/service-period-products' : '/admin/wechat-pay/products');
    assert.equal(dom.window.document.querySelectorAll('#stage input, #stage textarea, #stage select, #stage button').length, 0, 'an archived editor must expose no write, share, or external-push control');
    assert.equal(calls.some((call) => call.method !== 'GET'), false, 'an archived direct URL must not issue a write');
    assert.equal(errors.length, 0, `archived editor emitted JSDOM errors: ${errors.join('; ')}`);
  } finally {
    dom.window.dispatchEvent(new dom.window.Event('pagehide'));
    await pause(20);
    dom.window.close();
  }
}

await assertArchivedEditor({ periodic: false, id: 301 });
await assertArchivedEditor({ periodic: true, id: 401 });
console.log('archived ordinary and periodic edit URLs render terminal read-only DOM: PASS');
