import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/spProductForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const wait = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, label) { for (let i = 0; i < 80; i += 1) { if (check()) return; await wait(20); } throw new Error(label); }
const calls = [];
const uploadCalls = [];
let version = 1;
let projection = { schema_version: 1, status: 'draft', enabled: false, buy_button_text: '', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, wecom_tagging: {}, slices: [] };
const navigationErrors = [];
const editorConsole = new VirtualConsole();
editorConsole.on('jsdomError', error => { if (String(error.message).includes('navigation')) navigationErrors.push(error.message); });
const dom = new JSDOM(page, { url: 'https://test.invalid/admin/spProductForm.html', runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole: editorConsole, beforeParse(window) {
  window.__AICRM_TEST_MOCK__ = false; window.Request = Request; window.Response = Response; window.Headers = Headers;
  if (!window.crypto.subtle && globalThis.crypto?.subtle) Object.defineProperty(window.crypto, 'subtle', { value: globalThis.crypto.subtle });
  window.AICRMStandardComponents = { ready: () => Promise.resolve() };
  window.fetch = async (input, init = {}) => {
    const url = new URL(input instanceof Request ? input.url : String(input), window.location.href); const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
    calls.push({ path: url.pathname, method, key: new Headers(init.headers || {}).get('Idempotency-Key') || '', body: typeof init.body === 'string' ? init.body : '' }); const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
    if (url.pathname === '/api/admin/service-period-products' && method === 'POST') { projection = JSON.parse(init.body).admin_projection; return json({ product: { duration_days: 90, service_product_id: 201, product_code: 'sp-media', name: '周期素材', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: ['/api/admin/image-library/39/variants/original'], admin_projection: projection, distribution_policy: { enabled: true, commission_rate_basis_points: 2345, wait_days: 9, version: 1 }, version: 1 } }, 201); }
    if (url.pathname === '/api/admin/service-period-products/201/external-push') return json({ product_id: 201, product_kind: 'service_period', enabled: false, configuration_reference: '', revision: 0, webhook_url: '', push_type: '', expires_at_ts: null, day: null, frequency: null, remark: '', custom_params: {}, custom_params_json: '{}', updated_at: '2026-09-08T00:00:00Z' });
    if (url.pathname === '/api/admin/service-period-products/201') {
      if (method === 'PUT') { assert.equal(JSON.parse(init.body).duration_days, 90, 'preserve persisted duration required by backend'); assert.equal(JSON.parse(init.body).expected_version, version); projection = JSON.parse(init.body).admin_projection; version += 1; }
      return json({ product: { duration_days: 90, service_product_id: 201, product_code: 'sp-media', name: '周期素材', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, version } });
    }
    if (url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/v1/products') return json({ items: [], total: 0, has_more: false });
    if (url.pathname === '/api/admin/image-library/upload' && method === 'POST') {
      const body = init.body; const file = body && typeof body === 'object' && 'get' in body && typeof body.get === 'function' ? body.get('image') : null;
      uploadCalls.push({ name: file && typeof file === 'object' && 'name' in file ? String(file.name) : '', key: new Headers(init.headers).get('Idempotency-Key') || '' });
      return json({ ok: true, item: { id: 41, name: '周期上传素材', original_url: '/api/admin/image-library/41/variants/original', thumb_320_url: '/api/admin/image-library/41/variants/thumb_320', enabled: true } });
    }
    if (url.pathname === '/api/admin/image-library/38') return json({ item: { id: 38, name: '首页素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true } });
    if (url.pathname === '/api/admin/image-library/39') return json({ item: { id: 39, name: '周期后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true } });
    if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return json({ items: [{ id: 38, name: '首页素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }], has_more: true, next_offset: 1 });
    if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return json({ items: [{ id: 39, name: '周期后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: false });
    if (url.pathname === '/api/admin/image-library') return json({ items: [{ id: 39, name: '周期后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: false });
    if (['/api/admin/channels','/api/admin/wecom/tags','/api/admin/attachment-library','/api/admin/mini-program-library','/api/admin/wecom/tag-groups','/api/admin/questionnaires','/api/admin/customers','/api/admin/orders','/api/admin/coupons'].includes(url.pathname)) return json({ items: [], groups: [], total: 0, has_more: false });
    if (['/api/admin/config','/api/admin/app-settings','/api/admin/push-capabilities','/api/admin/releases'].includes(url.pathname)) return json({ categories: [] });
    return json({ code: 'unexpected' }, 500);
  };
} });
dom.window.document.body.insertAdjacentHTML('afterbegin', '<header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">创建周期商品</h1></div></header>');
const fixtureFetch = dom.window.fetch;
dom.window.eval(host);  dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
const document = dom.window.document;
await waitFor(() => document.getElementById('spfName'), 'frozen periodic form did not mount');

await waitFor(() => document.querySelector('[data-product-alipay-enabled]'), 'period Alipay checkbox missing');
assert.equal(document.querySelector('[data-product-alipay-enabled]').checked,true,'new period defaults both methods');
for (const [id,value] of [['spfName','周期支付'],['spfCode','sp-media'],['spfPrice','0.02'],['spfStock','1'],['spfDurationDays','90']]) document.getElementById(id).value=value;
const control=()=>document.querySelector('[data-product-alipay-enabled]');
control().checked=false;control().dispatchEvent(new dom.window.Event('change',{bubbles:true}));
assert.match(document.getElementById('spfAlipayHint').textContent,/仅支持微信/);
const save=()=>document.querySelector('.admin-topbar [data-page-header-actions="product-editor"] button:last-child');
save().click();
await waitFor(()=>calls.some(c=>c.path==='/api/admin/service-period-products'&&c.method==='POST'),'period create missing');
assert.equal(JSON.parse(calls.find(c=>c.path==='/api/admin/service-period-products'&&c.method==='POST').body).admin_projection.alipay_enabled,false);
await waitFor(()=>new URL(dom.window.location.href).searchParams.get('id')==='201','period create identity missing');
await wait(100);
assert.equal(control().checked,false,'create readback preserves disabled choice');
document.querySelector('a[href="#sp-action"]').click();control().checked=true;
save().click();await waitFor(()=>version===2,'other dimension save missing');await wait(100);
assert.equal(JSON.parse(calls.filter(c=>c.method==='PUT').at(-1).body).admin_projection.alipay_enabled,false,'other dimension does not publish unsaved draft');
document.querySelector('a[href="#sp-sale"]').click();control().checked=true;
save().click();await waitFor(()=>version===3,'enable save missing');await wait(100);
assert.equal(projection.alipay_enabled,true,'saved period readback enabled');
assert.equal(control().checked,true);
const periodWrites = calls.filter(c => c.method === 'POST' || c.method === 'PUT');
assert.equal(new Set(periodWrites.map(c => c.key)).size, periodWrites.length, 'each confirmed policy/dimension command has a distinct stable key');
dom.window.dispatchEvent(new dom.window.Event('pagehide'));await wait(0);dom.window.close();
console.log('period Alipay create, disable, readback, dimension isolation, enable: PASS');
