import assert from 'node:assert/strict';
import fs from 'node:fs';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const host = await buildTestBrowserBundle('web/v3/productAdapter.ts');
const waitFor = async (check) => {
  for (let i = 0; i < 100; i += 1) { if (check()) return; await new Promise((resolve) => setTimeout(resolve, 20)); }
  throw new Error('timed out');
};
const projection = { schema_version: 1, status: 'active', enabled: true, buy_button_text: '立即购买', require_mobile: false, lead_program_id: null, lead_channel_id: 14, lead_qr_title: '旧标题', lead_qr_subtitle: '旧副标题', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, purchase_action_enabled: true, purchase_action_mode: 'qr', wecom_tagging: {}, slices: [] };

for (const periodic of [false, true]) {
  const page = fs.readFileSync(periodic ? 'web/dist/admin/spProductForm.html' : 'web/dist/admin/productForm.html', 'utf8');
  const calls = [];
  let rejectNextPushSave = false;
  let externalPushReads = 0;
  let releasePanelRead;
  const panelRead = new Promise((resolve) => { releasePanelRead = resolve; });
  let releaseTestPost;
  const testPost = new Promise((resolve) => { releaseTestPost = resolve; });
  const virtualConsole = new VirtualConsole();
  virtualConsole.on('jsdomError', (error) => process.stderr.write(`jsdom: ${error.message}\n`));
  const product = { id: 101, ...(periodic ? { service_product_id: 101, duration_days: 30 } : {}), product_code: 'parity-fixture', name: '一致性商品', price_minor: 990, currency: 'CNY', stock_quantity: 1, description: '', images: [], version: 1, lifecycle: 'enabled', enabled: true, paid_order_count: 0, refund_order_count: 0, sold_count: 0, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z', admin_projection: projection };
  let config = { product_id: 101, product_kind: periodic ? 'service_period' : 'wechat_pay', enabled: true, configuration_reference: 'parity-push-101', revision: 1, webhook_url: 'https://hooks.example.test/paid', push_type: 'paid_notify', expires_at_ts: 123, day: 7, frequency: 2, remark: '旧备注', custom_params: { campaign: 'control', count: 9007199254740992, nested: [{ inner: 9007199254740992 }], note: 'a,b \"quoted\"' }, custom_params_json: '{\"campaign\":\"control\",\"count\":9007199254740993,\"nested\":[{\"inner\":9007199254740993}],\"note\":\"a,b \\\"quoted\\\"\"}' };
  const dom = new JSDOM(page, { url: `https://example.test/admin/${periodic ? 'spProductForm' : 'productForm'}.html?id=101`, runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole, beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(input instanceof Request ? input.url : String(input), window.location.href); const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase(); const body = typeof init.body === 'string' ? JSON.parse(init.body) : undefined; calls.push({ path: url.pathname, method, body });
      if (url.pathname.endsWith('/external-push') && method === 'PUT' && rejectNextPushSave) { rejectNextPushSave = false; return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } }); }
      if (url.pathname.endsWith('/external-push') && method === 'GET' && ++externalPushReads === 2) await panelRead;
      if (url.pathname.endsWith('/external-push/test') && method === 'POST') await testPost;
      if (url.pathname.endsWith('/external-push') && method === 'PUT') {
        const rawParams = String(body?.custom_params ?? '');
        config = { ...config, ...body, custom_params: JSON.parse(rawParams), custom_params_json: rawParams, revision: config.revision + 1 };
      }
      const value = url.pathname.endsWith('/external-push/test') ? { state: 'outcome_unknown', effect_id: 'eer_test_1', delivery_id: 'dlv_test_1' } : url.pathname.endsWith('/external-push') ? config : url.pathname === '/api/v1/products' ? { items: [product] } : url.pathname === '/api/v1/products/101' ? product : url.pathname.endsWith('/101') ? { product } : { items: [], total: 0, has_more: false };
      return new Response(JSON.stringify(value), { status: url.pathname.endsWith('/test') ? 202 : 200, headers: { 'Content-Type': 'application/json' } });
    };
  } });
  dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
  const d = dom.window.document;
  await waitFor(() => d.querySelector('[data-product-purchase-action]') && d.querySelector('[data-product-parity-push]')).catch((error) => { console.log(JSON.stringify(calls), d.querySelector('#stage')?.textContent, d.querySelector('#stage')?.innerHTML?.slice(0, 1200)); throw error; });
  assert.equal(d.querySelector('[data-product-parity-push-save]').disabled, true, 'external-push controls remain locked until their read finishes');
  d.querySelector('[data-product-parity-push-save]').click();
  assert.equal(calls.some((call) => call.method === 'PUT' && call.path.endsWith('/external-push')), false, 'a pending configuration read cannot save an empty form');
  releasePanelRead();
  await waitFor(() => !d.querySelector('[data-product-parity-push-save]').disabled);
  assert.equal(d.querySelector('[data-product-purchase-lead-channel]').tagName, 'SELECT');
  assert.equal(d.querySelector('[data-product-parity-push-url]').value, config.webhook_url);
  assert.equal(d.querySelectorAll('[data-mapping-conversion], [data-external-push-configuration-save]').length, 0);
  for (const [id, value] of periodic ? [['spfName', '一致性商品'], ['spfCode', 'parity-fixture'], ['spfPrice', '9.90'], ['spfStock', '1']] : [['pfName', '一致性商品'], ['pfCode', 'parity-fixture'], ['pfPrice', '9.90'], ['pfStock', '1']]) {
    const field = d.getElementById(id); field.value = value; field.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  }
  const actionEnabled = d.querySelector('[data-product-purchase-enabled]'); actionEnabled.checked = true; actionEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  const redirect = d.querySelector('input[value="redirect"]'); redirect.checked = true; redirect.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  const targetType = d.querySelector('[data-product-purchase-target-type]'); targetType.value = 'url_link'; targetType.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  d.querySelector('[data-product-purchase-url-link-source]').value = 'https://link.example.test/source'; d.querySelector('[data-product-purchase-url-link-key]').value = 'destination';
  d.querySelector('[data-product-purchase-save]').click();
  const subjectWrite = (call) => call.method === 'PUT' && !call.path.endsWith('/external-push') && !call.path.endsWith('/external-push/test');
  await waitFor(() => calls.some(subjectWrite)).catch((error) => { console.log(JSON.stringify(calls), d.querySelector('#fb-toast, #product-v3-toast')?.textContent); throw error; });
  const redirectWrite = calls.find(subjectWrite).body.admin_projection;
  assert.equal(redirectWrite.purchase_action_mode, 'redirect');
  assert.deepEqual(redirectWrite.completion_target.url_link, { enabled: true, url: '', source_url: 'https://link.example.test/source', response_url_key: 'destination' });
  targetType.value = 'h5'; targetType.dispatchEvent(new dom.window.Event('change', { bubbles: true })); d.querySelector('[data-product-purchase-h5-url]').value = '/paid/complete'; d.querySelector('[data-product-purchase-save]').click();
  await waitFor(() => calls.filter(subjectWrite).length >= 2);
  const h5Write = calls.filter(subjectWrite).at(-1).body.admin_projection;
  assert.equal(h5Write.completion_target.h5_url, '/paid/complete');
  actionEnabled.checked = false; actionEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true })); d.querySelector('[data-product-purchase-save]').click();
  await waitFor(() => calls.filter(subjectWrite).length >= 3);
  const disabledAction = calls.filter(subjectWrite).at(-1).body.admin_projection;
  assert.equal(disabledAction.purchase_action_enabled, false); assert.equal(disabledAction.completion_target, null);
  const enabled = d.querySelector('[data-product-parity-push-enabled]'); enabled.checked = true; enabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  d.querySelector('[data-product-parity-push-url]').value = 'https://hooks.example.test/new'; d.querySelector('[data-product-parity-push-save]').click();
  const parityPushWrite = (call) => call.method === 'PUT' && call.path.endsWith('/external-push') && Object.hasOwn(call.body || {}, 'webhook_url');
  await waitFor(() => calls.some(parityPushWrite));
  const saved = calls.find(parityPushWrite).body;
  assert.deepEqual(Object.keys(saved).sort(), ['custom_params', 'day', 'enabled', 'expected_revision', 'expires_at_ts', 'frequency', 'push_type', 'remark', 'webhook_url']);
  assert.equal(saved.expected_revision, 1, 'config save uses the revision returned by its GET');
  const originalParams = '{\"campaign\":\"control\",\"count\":9007199254740993,\"nested\":[{\"inner\":9007199254740993}],\"note\":\"a,b \\\"quoted\\\"\"}';
  assert.equal(saved.custom_params, originalParams, 'unmodified historical custom params preserve nested integers and escaped commas exactly');
  assert.equal(d.querySelectorAll('[data-product-parity-param-row]').length, 4, 'raw structured parameters remain available as legacy rows');
  await waitFor(() => !d.querySelector('[data-product-parity-push-save]').disabled);
  const nested = [...d.querySelectorAll('[data-product-parity-param-row]')].find((row) => row.querySelector('[data-product-parity-param-key]')?.value === 'nested');
  const nestedKey = nested?.querySelector('[data-product-parity-param-key]');
  nestedKey.value = 'renamed_nested'; nestedKey.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  d.querySelector('[data-product-parity-push-save]').click();
  await waitFor(() => calls.filter(parityPushWrite).length >= 2);
  const renamed = calls.filter(parityPushWrite).at(-1).body;
  assert.equal(renamed.expected_revision, 2, 'the response revision advances before a key-only rename');
  assert.equal(renamed.custom_params, '{\"campaign\":\"control\",\"count\":9007199254740993,\"renamed_nested\":[{\"inner\":9007199254740993}],\"note\":\"a,b \\\"quoted\\\"\"}', 'a key-only rename preserves the untouched nested integer value token');
  await waitFor(() => !d.querySelector('[data-product-parity-push-save]').disabled);
  const campaign = [...d.querySelectorAll('[data-product-parity-param-row]')].find((row) => row.querySelector('[data-product-parity-param-key]')?.value === 'campaign');
  const campaignValue = campaign?.querySelector('[data-product-parity-param-value]');
  campaignValue.value = 'renewal'; campaignValue.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  d.querySelector('[data-product-parity-push-save]').click();
  await waitFor(() => calls.filter(parityPushWrite).length >= 3);
  const edited = calls.filter(parityPushWrite).at(-1).body;
  assert.equal(edited.expected_revision, 3, 'the response revision advances before a value edit');
  assert.equal(edited.custom_params, '{\"campaign\":\"renewal\",\"count\":9007199254740993,\"renamed_nested\":[{\"inner\":9007199254740993}],\"note\":\"a,b \\\"quoted\\\"\"}', 'editing one value preserves untouched nested integers and escaped string fragments');
  await waitFor(() => !d.querySelector('[data-product-parity-push-test]').disabled);
  rejectNextPushSave = true; d.querySelector('[data-product-parity-push-test]').click();
  await waitFor(() => calls.filter(parityPushWrite).length >= 2);
  await waitFor(() => !d.querySelector('[data-product-parity-push-test]').disabled);
  assert.equal(calls.some((call) => call.method === 'POST' && call.path.endsWith('/external-push/test')), false, 'failed pre-save never POSTs a test');
  d.querySelector('[data-product-parity-push-test]').click();
  await waitFor(() => calls.some((call) => call.method === 'POST' && call.path.endsWith('/external-push/test')));
  assert.equal(d.querySelector('[data-product-parity-push-test]').disabled, true, 'test remains locked while the provider request is outstanding');
  d.querySelector('[data-product-parity-push-test]').click();
  assert.equal(calls.filter((call) => call.method === 'POST' && call.path.endsWith('/external-push/test')).length, 1, 'a second click cannot create another test delivery');
  releaseTestPost();
  await waitFor(() => !d.querySelector('[data-product-parity-push-test]').disabled);
  assert.ok(calls.filter(parityPushWrite).length >= 3, 'test saves first');
  dom.window.dispatchEvent(new dom.window.Event('pagehide')); dom.window.close();
}

{
  const page = fs.readFileSync('web/dist/admin/productForm.html', 'utf8');
  const product = { id: 101, product_code: 'bad-response-fixture', name: '坏响应商品', price_minor: 990, currency: 'CNY', stock_quantity: 1, description: '', images: [], version: 1, lifecycle: 'enabled', enabled: true, paid_order_count: 0, refund_order_count: 0, sold_count: 0, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z', admin_projection: projection };
  const bad = { product_id: 101, product_kind: 'wechat_pay', enabled: true, revision: 1 };
  const dom = new JSDOM(page, { url: 'https://example.test/admin/productForm.html?id=101', runScripts: 'outside-only', pretendToBeVisual: true, beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input) => {
      const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
      const value = url.pathname === '/api/v1/products' ? { items: [product] } : url.pathname === '/api/v1/products/101' ? product : url.pathname.endsWith('/external-push') ? bad : { items: [], total: 0, has_more: false };
      return new Response(JSON.stringify(value), { headers: { 'Content-Type': 'application/json' } });
    };
  } });
  dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
  const d = dom.window.document;
  await waitFor(() => d.querySelector('[data-product-parity-push]'));
  await waitFor(() => d.querySelector('[data-product-parity-push-result]')?.textContent.includes('配置响应不完整'));
  assert.equal(d.querySelector('[data-product-parity-push-save]').disabled, true, 'a malformed 200 configuration remains locked and cannot overwrite saved values');
  dom.window.close();
}
console.log('product payment-action and external-push legacy parity: PASS');
