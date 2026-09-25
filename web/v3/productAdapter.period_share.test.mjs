import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/spProducts.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const wait = (ms = 0) => new Promise((resolve) => setTimeout(resolve, ms));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await wait(20);
  }
  throw new Error(label);
}

const product = {
  service_product_id: 8, product_code: 'SP-8', name: '季度会员', description: '', price_minor: 398000,
  currency: 'CNY', stock_quantity: 5, images: [], lifecycle: 'enabled', enabled: true, version: 3,
  admin_projection: { schema_version: 1, status: 'service_period_enabled', enabled: true, buy_button_text: '', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, wecom_tagging: {}, slices: [] },
};
const calls = [];
const errors = [];
let shareMode = 'valid';
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => errors.push(String(error.message)));
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/service-period-products', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    if (!window.crypto.subtle && globalThis.crypto?.subtle) Object.defineProperty(window.crypto, 'subtle', { value: globalThis.crypto.subtle });
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.URL.createObjectURL = () => 'blob:period-share';
    window.URL.revokeObjectURL = () => {};
    window.open = () => null;
    window.fetch = async (input, init = {}) => {
      const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      calls.push({ path: url.pathname, method });
      const json = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/admin/service-period-products') return json({ items: [product] });
      if (url.pathname === '/api/admin/service-period-products/8/share') {
        const share = { ok: true, service_product_id: 8, product_code: 'SP-8', public_path: '/s/SP-8', local_only: true, real_external_call_executed: false };
        if (shareMode === 'wrong_id') return json({ ...share, service_product_id: 9 });
        if (shareMode === 'wrong_code') return json({ ...share, product_code: 'OTHER' });
        if (shareMode === 'old_path') return json({ ...share, public_path: '/p/service_period/8' });
        if (shareMode === 'nested_path') return json({ ...share, public_path: '/s/SP-8/pay' });
        if (shareMode === 'query') return json({ ...share, public_path: '/s/SP-8?next=elsewhere' });
        if (shareMode === 'remote') return json({ ...share, public_path: 'https://elsewhere.invalid/s/SP-8' });
        if (shareMode === 'external') return json({ ...share, real_external_call_executed: true });
        return json(share);
      }
      return json({ items: [], total: 0 });
    };
  },
});

try {
  dom.window.eval(host);
  dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
  const document = dom.window.document;
  await waitFor(() => [...document.querySelectorAll('button')].some((item) => item.textContent.trim() === '分享'), `period list did not mount: ${errors.join('; ')}`);
  const shareButton = () => [...document.querySelectorAll('button')].find((item) => item.textContent.trim() === '分享');
  shareButton().click();
  await waitFor(() => document.querySelector('input[readonly]')?.value === 'https://test.invalid/s/SP-8', `live period share did not open: ${errors.join('; ')}`);
  assert.deepEqual(calls.filter((call) => call.path.endsWith('/share')), [{ path: '/api/admin/service-period-products/8/share', method: 'GET' }]);
  assert.ok(document.querySelector('#shareQrBox svg'), 'the existing period share dialog renders a QR code');
  [...document.querySelectorAll('button')].find((item) => item.textContent.trim() === '×').click();

  for (const mode of ['wrong_id', 'wrong_code', 'old_path', 'nested_path', 'query', 'remote', 'external']) {
    shareMode = mode;
    document.querySelector('#fb-toast').textContent = '';
    shareButton().click();
    await waitFor(() => document.querySelector('#fb-toast')?.textContent === '周期商品分享响应不完整或越过本地边界', `${mode} was not rejected`);
    assert.equal(document.querySelector('input[readonly]'), null, `${mode} must not reopen the share dialog`);
  }
  assert.deepEqual(errors, [], 'period share Host must not throw browser errors');
  console.log('service-period share Host accepts the live code route and rejects unsafe projections: PASS');
} finally {
  dom.window.dispatchEvent(new dom.window.Event('pagehide'));
  dom.window.close();
}
