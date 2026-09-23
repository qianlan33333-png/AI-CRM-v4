import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/productForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = check();
    if (value) return value;
    await wait(20);
  }
  throw new Error(message);
}

const policy = { enabled: false, commission_rate_basis_points: 1234, wait_days: 8, version: 2 };
let persistedPolicy = { ...policy };
let productVersion = 7;
const writes = [];
const requests = [];
let deferPolicyWrite = false;
let resolveDeferredPolicyWrite;
const editorConsole = new VirtualConsole();
editorConsole.on('jsdomError', (error) => process.stderr.write(`JSDOM: ${error.message}\n`));
const projection = {
  schema_version: 1, status: 'draft', enabled: false, buy_button_text: '', require_mobile: false,
  lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '',
  completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null,
  purchase_action_enabled: false, purchase_action_mode: '', wecom_tagging: {}, slices: [],
};
const product = () => ({
  id: 101, product_code: 'dimension-policy', name: '维度分销商品', description: '', price_minor: 990,
  currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft',
  enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: productVersion,
  created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z', distribution_policy: persistedPolicy,
});
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/wechat-pay/products/101/edit', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: editorConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      requests.push({ path: url.pathname, method, key: new Headers(init.headers || (input instanceof Request ? input.headers : undefined)).get('Idempotency-Key') || '' });
      const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/products/101') {
        if (method === 'PUT') {
          const body = JSON.parse(init.body);
          writes.push(body);
          if (Object.hasOwn(body, 'distribution_policy') && deferPolicyWrite) {
            deferPolicyWrite = false;
            return new Promise((resolve) => {
              resolveDeferredPolicyWrite = () => {
                persistedPolicy = { ...body.distribution_policy, version: body.distribution_policy.version + 1 };
                productVersion += 1;
                resolve(reply(product()));
              };
            });
          }
          if (Object.hasOwn(body, 'distribution_policy')) persistedPolicy = body.distribution_policy;
          productVersion += 1;
        }
        return reply(product());
      }
      if (url.pathname === '/api/v1/products') return reply({ items: [product()], next_cursor: '' });
      if (url.pathname === '/api/admin/wechat-pay/products/101/external-push') {
        return reply({ product_id: 101, product_kind: 'wechat_pay', enabled: false, configuration_reference: '', revision: 0, webhook_url: '', push_type: '', expires_at_ts: null, day: null, frequency: null, remark: '', custom_params: {}, custom_params_json: '{}', updated_at: '2026-09-15T00:00:00Z' });
      }
      if (url.pathname === '/api/admin/channels' || url.pathname === '/api/admin/image-library' || url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return reply({ items: [], total: 0, has_more: false });
      if (url.pathname === '/api/admin/wecom/tags') return reply({ groups: [], items: [] });
      if (url.pathname === '/api/admin/config') return reply({ categories: [] });
      if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return reply({});
      return reply({ code: 'unexpected_product_request', path: url.pathname }, 500);
    };
  },
});
dom.window.document.body.insertAdjacentHTML('afterbegin', '<header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">商品管理</h1></div></header>');

dom.window.eval(host);
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
const document = dom.window.document;
const sale = await waitFor(() => document.getElementById('product-sale'), 'ordinary product editor did not mount');
const headerActions = await waitFor(() => document.querySelectorAll('.admin-topbar [data-page-header-actions="product-editor"] button').length === 2, 'ordinary topbar must receive the existing return and save controls');
assert.equal(document.querySelectorAll('.admin-topbar .admin-page-title').length, 1, 'ordinary editor retains the shell title as the only header title');
assert.deepEqual([...document.querySelectorAll('.admin-topbar [data-page-header-actions="product-editor"] button')].map((button) => button.textContent.trim()), ['返回商品管理', '保存当前维度'], 'ordinary topbar retains the existing commands in their original order');
const ordinaryEditorTitle = [...document.querySelectorAll('#stage h2')].find((heading) => heading.textContent.trim() === '编辑普通商品');
assert.ok(ordinaryEditorTitle?.hidden, 'ordinary body summary must not repeat the shell header title');
const ordinaryFrozenHeader = document.getElementById('stage').firstElementChild.firstElementChild;
assert.ok(ordinaryFrozenHeader?.hidden && ordinaryFrozenHeader.dataset.v3ProductFrozenHeader === 'hidden', 'ordinary editor must hide the frozen 52px donor header after the SSR shell renders');
assert.equal([...document.querySelectorAll('#stage button')].some((button) => button.textContent.trim() === '返回商品管理'), false, 'ordinary body summary must not retain a second return control');
const distribution = await waitFor(() => document.querySelector('[data-distribution-policy]'), 'sale dimension did not mount its distribution controls');
assert.equal(distribution.parentElement, sale, 'distribution controls must be owned by the sale dimension');
assert.equal(distribution.querySelectorAll('input').length, 3, 'policy exposes only its switch, commission rate, and refund wait days');
for (const selector of ['[data-distribution-application-entry]', '[data-distribution-application-link]', '[data-distribution-application-qr]', '[data-distribution-application-pending]']) {
  assert.equal(document.querySelector(selector), null, `${selector} must not exist in the product editor`);
}

const rate = distribution.querySelector('[data-distribution-policy-rate]');
rate.value = '23.45';
rate.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
for (const id of ['product-media', 'product-action', 'product-wecom', 'product-push']) {
  document.querySelector(`a[href="#${id}"]`).click();
  assert.equal(document.querySelector(`#${id} [data-distribution-policy]`), null, `${id} must not render distribution controls`);
  assert.equal(rate.value, '23.45', 'switching dimensions must retain the sale draft');
  if (id === 'product-push') continue;
  const save = [...document.querySelectorAll(`#${id} button`)].find((button) => button.textContent.trim() === '保存当前维度');
  assert.ok(save, `${id} must retain its own save button`);
  const expectedWrites = writes.length + 1;
  save.click();
  await waitFor(() => writes.length === expectedWrites, `${id} save did not write its current dimension; requests=${JSON.stringify(requests)}`);
  assert.equal(Object.hasOwn(writes.at(-1), 'distribution_policy'), false, `${id} save must leave the stored distribution policy untouched`);
}

const afterOtherDimensions = await dom.window.fetch('/api/v1/products/101').then((response) => response.json());
assert.deepEqual(afterOtherDimensions.distribution_policy, policy, 'other-dimension saves must leave the server-read policy unchanged');

document.querySelector('a[href="#product-sale"]').click();
assert.equal(rate.value, '23.45', 'returning to sale information must retain its draft');
const saleSave = [...sale.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
assert.ok(saleSave, 'sale information must retain its save button');
const expectedWrites = writes.length + 1;
saleSave.click();
await waitFor(() => writes.length === expectedWrites, 'sale save did not write the product');
assert.deepEqual(writes.at(-1).distribution_policy, { enabled: false, commission_rate_basis_points: 2345, wait_days: 8, version: 2 }, 'sale save must persist the disabled policy exactly as read and drafted');
const afterSaleSave = await dom.window.fetch('/api/v1/products/101').then((response) => response.json());
assert.deepEqual(afterSaleSave.distribution_policy, { enabled: false, commission_rate_basis_points: 2345, wait_days: 8, version: 2 }, 'sale save must persist the drafted policy for the next server read');
const firstPolicySaveKey = requests.filter(request => request.path === '/api/v1/products/101' && request.method === 'PUT').at(-1).key;

rate.value = '12.34';
rate.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
deferPolicyWrite = true;
const writesBeforeRace = writes.length;
saleSave.click();
await waitFor(() => typeof resolveDeferredPolicyWrite === 'function', 'sale policy save did not reach the deferred server response');
rate.value = '23.45';
rate.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
resolveDeferredPolicyWrite();
await waitFor(() => writes.length === writesBeforeRace + 1, 'deferred sale save did not write the product');
await waitFor(() => distribution.dataset.distributionPolicyVersion === '3', 'deferred sale save did not advance the saved policy revision');
assert.equal(rate.value, '23.45', 'a response must not overwrite a newer in-flight sale draft');
const editedPolicySaveKey = requests.filter(request => request.path === '/api/v1/products/101' && request.method === 'PUT').at(-1).key;
assert.match(firstPolicySaveKey, /^product-save-/, 'policy save must use a Product command idempotency key');
assert.notEqual(editedPolicySaveKey, firstPolicySaveKey, 'a policy edit after a confirmed save must use a new Product command key');

dom.window.close();
console.log('product distribution policy dimensions, drafts, and save boundaries: PASS');
