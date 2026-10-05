import { productSourceTestPage } from '../scripts/product-source-test-page.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = productSourceTestPage('spProductForm.html');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const wait = (ms = 0) => new Promise(resolve => setTimeout(resolve, ms));
async function until(check, message) { for (let i = 0; i < 100; i++) { if (check()) return; await wait(20); } throw new Error(message); }

for (const creating of [false, true]) {
  let version = 7;
  let projection = { schema_version: 1, status: 'service_period_enabled', enabled: true, require_mobile: true, contact_collection_level: 'shipping_address', buy_button_text: '', lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, purchase_action_enabled: false, purchase_action_mode: '', slices: [], wecom_tagging: {} };
  const writes = [];
  const product = () => ({ service_product_id: 201, product_code: 'period-contact', name: '周期信息收集', description: '', price_minor: 990, currency: 'CNY', stock_quantity: 1, images: [], duration_days: 30, admin_projection: projection, lifecycle: 'enabled', enabled: true, version });
  const virtualConsole = new VirtualConsole();
  const errors = [];
  virtualConsole.on('jsdomError', error => { if (!error.message.includes('navigation')) errors.push(error.message); });
  const dom = new JSDOM(page, {
    url: `https://test.invalid/admin/spProductForm.html${creating ? '' : '?id=201'}`, runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole,
    beforeParse(window) {
      window.Request = Request; window.Response = Response; window.Headers = Headers;
      window.__AICRM_TEST_MOCK__ = false;
      window.fetch = async (input, init = {}) => {
        const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
        const method = init.method || 'GET';
        const reply = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
        if (url.pathname === '/api/admin/service-period-products/201' || (url.pathname === '/api/admin/service-period-products' && method === 'POST')) {
          if (method === 'PUT' || method === 'POST') {
            const body = JSON.parse(init.body); writes.push(body);
            assert.equal(body.duration_days, 30);
            if (method === 'PUT') assert.equal(body.expected_version, version);
            assert.ok(new Headers(init.headers).get('Idempotency-Key'));
            projection = body.admin_projection; version++;
          }
          return reply({ product: product() });
        }
        if (url.pathname === '/api/admin/service-period-products/201/external-push') return reply({ product_id: 201, product_kind: 'service_period', enabled: false, configuration_reference: '', revision: 0, type: '', day: null, frequency: null, expires_at_ts: null, remark: '', custom_params: {}, custom_params_json: '{}' });
        if (url.pathname === '/api/admin/wecom/tags') return reply({ groups: [], items: [] });
        if (['/api/admin/config','/api/admin/app-settings','/api/admin/push-capabilities','/api/admin/releases'].includes(url.pathname)) return reply({ categories: [] });
        return reply({ items: [], groups: [], total: 0, has_more: false });
      };
    },
  });
  dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
  const d = dom.window.document;
  await until(() => d.getElementById('spfContactCollectionLevel'), `periodic contact selector missing (creating=${creating})`);
  assert.equal(d.getElementById('spfContactCollectionLevel').value, creating ? 'mobile' : 'shipping_address');
  assert.ok(d.getElementById('spfContactCollectionLevel').closest('#sp-sale'), 'periodic collection belongs to sale, not the hidden purchase-action legacy field');
  assert.deepEqual([...d.getElementById('spfContactCollectionLevel').options].map(option => option.value), ['none', 'mobile', 'shipping_address']);
  const save = () => [...d.querySelectorAll('#sp-sale button')].find(button => button.textContent.trim() === '保存当前维度');
  if (creating) {
    for (const [id, value] of [['spfName','周期信息收集'],['spfCode','period-contact'],['spfPrice','9.90'],['spfDurationDays','30']]) d.getElementById(id).value = value;
  }
  for (const level of creating ? ['shipping_address'] : ['none', 'mobile', 'shipping_address']) {
    const before = writes.length;
    const select = d.getElementById('spfContactCollectionLevel'); select.value = level;
    select.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
    save().click();
    await until(() => writes.length === before + 1, 'periodic contact write missing');
    assert.equal(writes.at(-1).admin_projection.contact_collection_level, level, 'frozen serializer must retain exact level');
    assert.equal(writes.at(-1).admin_projection.require_mobile, level !== 'none');
    await wait(150);
    assert.equal(d.getElementById('spfContactCollectionLevel').value, level, 'authoritative save rerender lost policy');
    assert.equal((await dom.window.fetch('/api/admin/service-period-products/201').then(response => response.json())).product.admin_projection.contact_collection_level, level);
  }
  if (!creating) {
    d.getElementById('spfContactCollectionLevel').value = 'none';
    d.querySelector('a[href="#sp-action"]').click();
    const before = writes.length;
    [...d.querySelectorAll('#sp-action button')].find(button => button.textContent.trim() === '保存当前维度').click();
    await until(() => writes.length === before + 1, 'other dimension save missing');
    assert.equal(writes.at(-1).admin_projection.contact_collection_level, 'shipping_address', 'unsaved contact draft leaked through another dimension');
    await wait(150);
    d.querySelector('a[href="#sp-sale"]').click();
    assert.equal(d.getElementById('spfContactCollectionLevel').value, 'none', 'switching dimensions preserves the unsaved sale draft');
  }
  assert.deepEqual(errors, []);
  dom.window.close();
}
console.log('periodic contact create/edit serialized save, readback and dimension isolation: PASS');
