import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/productForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const frozenPicker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, label) {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    const value = check();
    if (value) return value;
    await wait(10);
  }
  throw new Error(label + ` diagnostics=${JSON.stringify(diagnostics)}`);
}

const projection = { schema_version: 1, status: 'active', enabled: false, buy_button_text: '立即购买', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, purchase_action_enabled: false, purchase_action_mode: '', wecom_tagging: {}, slices: [] };
const absolute39 = 'https://test.invalid/api/admin/image-library/39/variants/original';
const opaqueUpload = 'https://uploads.example.invalid/products/original-current.png';
const initial = { id: 101, product_code: 'order-preserving-product', name: '顺序保留商品', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [absolute39, opaqueUpload, '/api/admin/image-library/38/variants/original'], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, created_at: '2026-09-15T00:00:00Z', updated_at: '2026-09-15T00:00:00Z' };
let saved;
let holdProductMetadata = false;
const heldProductMetadata = [];
let productMetadataCalls = 0;
const uploadCalls = [];
let holdUpload = false;
const heldUploads = [];
const diagnostics = [];
function deferredProductMetadata(body, init, json) {
  productMetadataCalls += 1;
  if (!holdProductMetadata) return json(body);
  return new Promise((resolve, reject) => {
    const signal = init?.signal;
    const abort = () => reject(new DOMException('aborted', 'AbortError'));
    if (signal?.aborted) { abort(); return; }
    signal?.addEventListener('abort', abort, { once: true });
    heldProductMetadata.push(() => {
      signal?.removeEventListener('abort', abort);
      resolve(json(body));
    });
  });
}
function releaseHeldProductMetadata() {
  const pending = heldProductMetadata.splice(0);
  pending.forEach((release) => release());
}
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/productForm.html?id=101', runScripts: 'outside-only', pretendToBeVisual: true,
  virtualConsole: (() => { const console = new VirtualConsole(); console.on('jsdomError', (error) => diagnostics.push(String(error.stack || error.message))); return console; })(),
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    if (!window.crypto.subtle && globalThis.crypto?.subtle) Object.defineProperty(window.crypto, 'subtle', { value: globalThis.crypto.subtle });
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.fetch = async (input, init = {}) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      diagnostics.push(`${method} ${url.pathname}${url.search}`);
      const json = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/products/101') {
        if (method === 'PUT') { saved = JSON.parse(String(init.body)); return json({ ...initial, ...saved, version: 2 }); }
        return json(initial);
      }
      if (url.pathname === '/api/v1/products/101/local-entitlements') return json({ items: [] });
      if (url.pathname === '/api/v1/products' && method === 'GET') return json({ items: [initial], next_cursor: '' });
      if (url.pathname === '/api/admin/wechat-pay/products/101/external-push') return json({ product_id: 101, product_kind: 'wechat_pay', enabled: false, configuration_reference: '', revision: 0, webhook_url: '', push_type: '', expires_at_ts: null, day: null, frequency: null, remark: '', custom_params: {}, custom_params_json: '{}', updated_at: '' });
      if (url.pathname === '/api/admin/image-library/upload' && method === 'POST') {
        const body = init.body;
        const file = body && typeof body === 'object' && 'get' in body && typeof body.get === 'function' ? body.get('image') : null;
        uploadCalls.push({ name: file && typeof file === 'object' && 'name' in file ? String(file.name) : '', key: new Headers(init.headers).get('Idempotency-Key') || '' });
        const response = () => {
          const attempts = uploadCalls.filter((call) => call.name === (file && typeof file === 'object' && 'name' in file ? String(file.name) : '')).length;
          if ((String(file?.name) === 'retry.png' || String(file?.name) === 'cap-b.png') && attempts === 1) return json({ code: 'upload_temporarily_failed' }, 503);
          const ids = { 'first.png': 41, 'retry.png': 42, 'fill-a.png': 43, 'fill-b.png': 44, 'fill-c.png': 45, 'fill-d.png': 46, 'cap-a.png': 47, 'cap-b.png': 48, 'late.png': 49 };
          const id = ids[String(file?.name)] || 50;
          return json({ ok: true, item: { id, name: `图片素材 ${id}`, original_url: `/api/admin/image-library/${id}/variants/original`, thumb_320_url: `/api/admin/image-library/${id}/variants/thumb_320`, enabled: true } });
        };
        if (!holdUpload) return response();
        return new Promise((resolve) => heldUploads.push(() => resolve(response())));
      }
      if (url.pathname === '/api/admin/image-library/38') return deferredProductMetadata({ item: { id: 38, name: '待移除素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true } }, init, json);
      if (url.pathname === '/api/admin/image-library/39') return deferredProductMetadata({ item: { id: 39, name: '保留字面 URL 素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true } }, init, json);
      if (url.pathname === '/api/admin/image-library/40') return deferredProductMetadata({ item: { id: 40, name: '后续新增素材', original_url: '/api/admin/image-library/40/variants/original', thumb_320_url: '/api/admin/image-library/40/variants/thumb_320', enabled: true } }, init, json);
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return json({ items: [{ id: 38, name: '待移除素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }], has_more: true, next_offset: 1 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return json({ items: [{ id: 39, name: '保留字面 URL 素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: true, next_offset: 2 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '2') return json({ items: [{ id: 40, name: '后续新增素材', original_url: '/api/admin/image-library/40/variants/original', thumb_320_url: '/api/admin/image-library/40/variants/thumb_320', enabled: true }], has_more: false });
      if (url.pathname === '/api/admin/image-library') return json({ items: [{ id: 38, name: '待移除素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }, { id: 39, name: '保留字面 URL 素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], has_more: false });
      if (url.pathname === '/api/admin/channels' || url.pathname === '/api/admin/wecom/tags' || url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return json({ items: [], groups: [], total: 0, has_more: false, tag_limit: 1000, read_model_status: 'ready' });
      if (url.pathname === '/api/admin/config') return json({ categories: [] });
      if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return json({});
      return json({ code: 'unexpected_product_material_order_request', path: url.pathname }, 500);
    };
  },
});

dom.window.eval(frozenPicker);
dom.window.eval(host);
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
const document = dom.window.document;
await waitFor(() => document.getElementById('pfName'), 'the real frozen product editor must load the existing owner draft');
document.querySelector('a[href="#product-media"]').click();
const open = await waitFor(() => [...document.querySelectorAll('#product-media button')].find((button) => button.textContent.trim() === '从素材库选择'), 'product material caller did not mount');
open.click();
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('保留字面 URL 素材') && document.querySelector('[data-v3-picker-selected]')?.textContent.includes('待移除素材'), 'reopening must resolve both recognized current library URLs without treating the external URL as a library item');
assert.equal(document.querySelector('[data-v3-picker-selected]').textContent.includes('products/original-current'), false, 'the opaque uploaded URL must stay in the original owner draft, not become a fake library record');
document.querySelector('[data-v3-material-remove$=":38"]').click();
document.querySelector('[data-v3-picker-more]').click();
await waitFor(() => document.querySelector('[data-v3-material-key$=":39"]'), 'the second page must remain available after a temporary removal');
document.querySelector('[data-v3-picker-more]').click();
const appended = await waitFor(() => document.querySelector('[data-v3-material-key$=":40"]'), 'the authorized later page must load for a new product material');
appended.click();
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => !document.querySelector('[data-v3-selection-session="material"]'), 'the owner draft must accept the full selection before V3 closes');
const save = [...document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
save.click();
await waitFor(() => Array.isArray(saved?.images), 'the original product save must serialize the caller-owned draft');
assert.deepEqual(saved.images, [absolute39, opaqueUpload, '/api/admin/image-library/40/variants/original'], 'canonical matching may decide membership but must preserve surviving literal URL order and append only the new authorized material');
open.click();
await waitFor(() => document.querySelector('[data-v3-picker-selected]')?.textContent.includes('保留字面 URL 素材') && document.querySelector('[data-v3-picker-selected]')?.textContent.includes('后续新增素材'), 'reopen must read the owner draft after save rather than a stale dialog cache');
document.querySelector('[data-v3-picker-cancel]').click();

// Direct-record verification is an owner pre-open. A second click joins the
// first flight; once the product route changes, neither delayed result may open
// a dialog for the former page.
holdProductMetadata = true;
const beforeDuplicate = productMetadataCalls;
open.click();
open.click();
await waitFor(() => heldProductMetadata.length === 2, 'the current two library records must begin one shared pre-open read');
assert.equal(productMetadataCalls - beforeDuplicate, 2, 'double-clicking the real owner button must not duplicate metadata reads or dialogs');
dom.window.history.replaceState(null, '', '/admin/productForm.html?id=202');
releaseHeldProductMetadata();
holdProductMetadata = false;
await wait(30);
assert.equal(document.querySelector('[data-v3-selection-session="material"]'), null, 'a late prior-product pre-open may not open a picker after the route changes');

// The owner draft can retain the same image URLs while its active dimension
// changes. A new picker gesture then belongs to the new dimension, so it may
// not join the stale pre-open and silently disappear when that old context is
// rejected.
dom.window.history.replaceState(null, '', '/admin/productForm.html?id=101');
holdProductMetadata = true;
open.click();
await waitFor(() => heldProductMetadata.length === 2, 'a current media-dimension pre-open must read both recognised records');
document.querySelector('a[href="#product-sale"]').click();
open.click();
await waitFor(() => heldProductMetadata.length === 4, 'a picker gesture after a dimension change must start its own current pre-open');
releaseHeldProductMetadata();
holdProductMetadata = false;
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]'), 'the current-dimension picker request must not join a stale pre-open');
document.querySelector('[data-v3-picker-cancel]').click();
document.querySelector('a[href="#product-media"]').click();

// The current owner draft can also change through its original remove control
// while metadata is in flight. It must win over the old selectedRecords input.
holdProductMetadata = true;
open.click();
await waitFor(() => heldProductMetadata.length === 2, 'a fresh current-page pre-open must read the two recognised records');
const ownerRemove = [...document.querySelectorAll('#product-media button')].find((button) => button.textContent.trim() === '移除');
assert.ok(ownerRemove, 'the frozen product form exposes its original draft removal control');
ownerRemove.click();
releaseHeldProductMetadata();
holdProductMetadata = false;
await wait(30);
assert.equal(document.querySelector('[data-v3-selection-session="material"]'), null, 'a delayed pre-open may not overwrite or open against a changed original product draft');
assert.equal(document.querySelectorAll('[data-v3-product-material-action="remove"]').length, 2, 'the active V3 product-material draft preserves the owner removal while V3 discards the stale pre-open');

// Uploads use the typed Media receipt directly, append each success to this
// open product draft, retain the active media dimension, and keep the failed
// file's original idempotency key for a later explicit retry. The first file
// must never be uploaded twice after the second file fails.
const makeImage = (name, bytes) => {
  const file = new dom.window.File([new Uint8Array(bytes)], name, { type: 'image/png', lastModified: 100 });
  Object.defineProperty(file, 'arrayBuffer', { value: async () => new Uint8Array(bytes).buffer });
  return file;
};
const firstUpload = makeImage('first.png', [1, 2, 3]);
const retryUpload = makeImage('retry.png', [4, 5, 6]);
const upload = document.querySelector('#pfImageUpload');
assert.ok(upload instanceof dom.window.HTMLInputElement, 'product upload source input must remain mounted');
const draftName = document.querySelector('#pfName');
assert.ok(draftName instanceof dom.window.HTMLInputElement, 'product draft name input must remain mounted');
draftName.value = '保持中的商品草稿';
Object.defineProperty(upload, 'files', { configurable: true, value: [firstUpload, retryUpload] });
upload.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => uploadCalls.length === 2 && document.querySelector('[data-v3-product-material-key="image:41"]'), 'the first upload must append before a later upload failure');
assert.equal(document.querySelector('a[href="#product-media"]')?.getAttribute('aria-current'), 'step', 'upload must not switch the editor away from 页面素材');
assert.equal(draftName.value, '保持中的商品草稿', 'upload must not rebuild or discard another current-dimension draft field');
assert.deepEqual(uploadCalls.map((call) => call.name), ['first.png', 'retry.png'], 'the first batch issues one Media request per selected file until failure');
assert.ok(uploadCalls.every((call) => call.key.startsWith('product-media-upload-')), 'each upload writes with a controlled idempotency key');
const firstCount = uploadCalls.length;
const retryFirst = makeImage('first.png', [1, 2, 3]);
const retrySecond = makeImage('retry.png', [4, 5, 6]);
Object.defineProperty(upload, 'files', { configurable: true, value: [retryFirst, retrySecond] });
upload.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => uploadCalls.length === firstCount + 1 && document.querySelector('[data-v3-product-material-key="image:42"]'), 'a retry must reuse the failed file key and append its new typed receipt');
assert.deepEqual(uploadCalls.map((call) => call.name), ['first.png', 'retry.png', 'retry.png'], 'a confirmed first file is reused from this editor session instead of being uploaded again');
assert.equal(uploadCalls[2].key, uploadCalls[1].key, 'the failed file retry retains its original idempotency key');
const summaryMaterialLabel = [...document.querySelectorAll('span')].find((label) => label.textContent.trim() === '页面素材' && label.parentElement?.querySelector(':scope > strong'));
assert.equal(summaryMaterialLabel?.parentElement?.querySelector(':scope > strong')?.textContent, '4', 'local product material redraw updates the persisted summary count without a full editor reload');
assert.equal([...document.querySelectorAll('#product-media div')].find((node) => node.children.length === 0 && node.textContent.includes('保存后按当前顺序展示。'))?.textContent, '保存后按当前顺序展示。', 'material guidance describes the user-visible saved order without donor storage details');
const moved = document.querySelector('[data-v3-product-material-key="image:42"] [data-v3-product-material-action="up"]');
assert.ok(moved instanceof dom.window.HTMLButtonElement, 'the newly appended typed Media item exposes keyboard sorting');
moved.focus(); moved.click();
await waitFor(() => document.activeElement?.getAttribute('data-v3-product-material-action') === 'up' && document.activeElement?.closest('[data-v3-product-material-key]')?.getAttribute('data-v3-product-material-key') === 'image:42', 'sorting must restore focus to the same reachable row action');
document.activeElement.click(); document.activeElement.click();
await waitFor(() => document.activeElement?.getAttribute('data-v3-product-material-action') === 'down' && document.activeElement?.closest('[data-v3-product-material-key]')?.getAttribute('data-v3-product-material-key') === 'image:42', 'moving to the first position must retain focus on an enabled action in that same row');
assert.equal(document.querySelector('a[href="#product-media"]')?.getAttribute('aria-current'), 'step', 'sorting must keep the active 页面素材 dimension');

// A late upload from the former route can create a Media record, but it may
// never attach that record to a different product's draft.
holdUpload = true;
const staleUpload = makeImage('late.png', [7, 8, 9]);
Object.defineProperty(upload, 'files', { configurable: true, value: [staleUpload] });
upload.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => heldUploads.length === 1, 'stale-route upload must reach its controllable Media request');
dom.window.history.replaceState(null, '', '/admin/productForm.html?id=202');
heldUploads.splice(0).forEach((release) => release());
holdUpload = false;
await wait(30);
assert.equal(document.querySelector('[data-v3-product-material-key="image:42"]')?.isConnected, true, 'late former-product upload may not replace the current material draft');
assert.equal(document.querySelector('[data-v3-product-material-key="image:49"]'), null, 'late former-product receipt must not attach to a new route');
dom.window.history.replaceState(null, '', '/admin/productForm.html?id=101');

// Fill to eight current items, then retry a partially-successful four-file
// selection. Cached first items count as no new slots; the lone failed item
// reuses its own receipt key and reaches the ten-item boundary exactly.
const fillers = ['fill-a.png', 'fill-b.png', 'fill-c.png', 'fill-d.png'].map((name, index) => makeImage(name, [20 + index]));
Object.defineProperty(upload, 'files', { configurable: true, value: fillers });
upload.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]').length === 8, 'four fresh typed uploads must fill the current draft to eight');
const capA = makeImage('cap-a.png', [31]); const capB = makeImage('cap-b.png', [32]);
Object.defineProperty(upload, 'files', { configurable: true, value: [makeImage('first.png', [1, 2, 3]), makeImage('retry.png', [4, 5, 6]), capA, capB] });
upload.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => document.querySelector('[data-v3-product-material-key="image:47"]') && uploadCalls.filter((call) => call.name === 'cap-b.png').length === 1, 'near-capacity selection must append its acknowledged item before the final failure');
const failedCapKey = uploadCalls.find((call) => call.name === 'cap-b.png')?.key;
Object.defineProperty(upload, 'files', { configurable: true, value: [makeImage('first.png', [1, 2, 3]), makeImage('retry.png', [4, 5, 6]), makeImage('cap-a.png', [31]), makeImage('cap-b.png', [32])] });
upload.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => document.querySelector('[data-v3-product-material-key="image:48"]') && document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]').length === 10, 'partial retry must account for cached successful items and append only the remaining item');
const capRetries = uploadCalls.filter((call) => call.name === 'cap-b.png');
assert.equal(capRetries.length, 2, 'partial retry issues only the previously failed Media request');
assert.equal(capRetries[1].key, failedCapKey, 'partial retry retains the failed request idempotency key');
assert.equal(uploadCalls.filter((call) => ['first.png', 'retry.png', 'cap-a.png'].includes(call.name)).length, 4, 'cached confirmed items are not uploaded again near the capacity boundary');
save.click();
await waitFor(() => Array.isArray(saved?.images) && saved.images.includes('/api/admin/image-library/42/variants/original'), 'saving the current material dimension must serialize its reordered typed Media receipts');
assert.ok(saved.images.indexOf('/api/admin/image-library/42/variants/original') < saved.images.indexOf('/api/admin/image-library/41/variants/original'), 'the current-dimension save preserves the displayed drag/button order');

// JSDOM close does not dispatch browser lifecycle events. Flush the Product
// adapter's normal pagehide disposal path before destroying this test window.
dom.window.dispatchEvent(new dom.window.Event('pagehide'));
await wait(0);
dom.window.close();
console.log('product material owner URL ordering, typed upload, retry, sort, and stale-route isolation: PASS');
