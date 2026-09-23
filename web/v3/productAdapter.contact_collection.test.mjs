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
let projection = {
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
          writes.push(body); projection = body.admin_projection;
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
const select = await waitFor(() => document.getElementById('pfContactCollectionLevel'), 'collection selector missing');
const save = () => [...document.querySelectorAll('#product-sale button')].find(b => b.textContent.trim() === '保存当前维度');
select.value = 'shipping_address';
select.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
save().click();
await waitFor(() => writes.length === 1, 'shipping save did not submit');
assert.equal(writes[0].admin_projection.contact_collection_level, 'shipping_address', 'actual serialized request must preserve selected shipping level');
await wait(150);
assert.equal(document.getElementById('pfContactCollectionLevel').value, 'shipping_address', 'save rerender must retain shipping');
const readback = await dom.window.fetch('/api/v1/products/101').then(r => r.json());
assert.equal(readback.admin_projection.contact_collection_level, 'shipping_address');
for (const level of ['none', 'mobile', 'shipping_address']) {
  const count = writes.length;
  const field = document.getElementById('pfContactCollectionLevel');
  field.value = level; field.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  save().click(); await waitFor(() => writes.length === count + 1, 'level update missing');
  assert.equal(writes.at(-1).admin_projection.contact_collection_level, level);
  assert.equal(writes.at(-1).admin_projection.require_mobile, level !== 'none');
  await wait(150);
}
dom.window.close();
console.log('contact collection serialized save and readback: PASS');
