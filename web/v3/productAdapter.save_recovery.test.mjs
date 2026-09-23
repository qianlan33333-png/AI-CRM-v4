import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM, VirtualConsole } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/productForm.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/productAdapter.ts'));
const admin = await buildTestBrowserBundle(path.join(root, 'web/src/admin/main.ts'));
const standardMaterialPicker = fs.readFileSync(path.join(root, 'web/donors/ai-assistant-production/static/material_picker.js'), 'utf8');
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function waitFor(check, message) {
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const value = check();
    if (value) return value;
    await wait(20);
  }
  throw new Error(message);
}

const projection = { schema_version: 1, status: 'active', enabled: true, buy_button_text: '立即购买', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, purchase_action_enabled: false, purchase_action_mode: '', wecom_tagging: {}, slices: [] };

async function verifyDefaultPolicyAtActualCreateAlias() {
  const calls = [];
  const created = { id: 201, product_code: 'default-policy-product', name: '默认分销商品', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, distribution_policy: { enabled: false, commission_rate_basis_points: 0, wait_days: 7, version: 1 }, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z' };
  const errors = [];
  const console = new VirtualConsole();
  console.on('jsdomError', error => errors.push(String(error.message)));
  const dom = new JSDOM(page, {
    url: 'https://test.invalid/admin/wechat-pay/productForm.html', runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole: console,
    beforeParse(window) {
      window.__AICRM_TEST_MOCK__ = false;
      window.Request = Request;
      window.Response = Response;
      window.Headers = Headers;
      window.AICRMTagPicker = { open() {} };
      window.fetch = async (input, init = {}) => {
        const raw = input instanceof Request ? input.url : String(input);
        const url = new URL(raw, window.location.href);
        const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
        calls.push({ path: url.pathname, method, body: typeof init.body === 'string' ? init.body : '' });
        const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
        if (url.pathname === '/api/v1/products' && method === 'GET') return reply({ items: [{ id: 88, product_code: 'already-used', name: '已有商品', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z' }], next_cursor: '' });
        if (url.pathname === '/api/v1/products' && method === 'POST') return reply(created, 201);
        if (url.pathname === '/api/admin/wechat-pay/products/201/external-push' && method === 'PUT') {
          const body = JSON.parse(String(init.body || '{}'));
          return reply({ product_id: 201, product_kind: 'wechat_pay', ...body, revision: 1, configuration_reference: '', custom_params: JSON.parse(body.custom_params), custom_params_json: body.custom_params, updated_at: '2026-09-08T00:01:00Z' });
        }
        if (url.pathname === '/api/admin/channels') return reply({ items: [{ id: 17, channel_name: '付款后添加企微', channel_code: 'paid-lead', status: 'active' }, { id: 18, channel_name: '已归档渠道', channel_code: 'archived', status: 'archived' }], total: 2 });
        if (url.pathname === '/api/admin/wecom/tags') return reply({ read_model_status: 'ready', groups: [], items: [], count: 0, total_tags: 0, tag_limit: 1000 });
        if (url.pathname === '/api/admin/image-library' || url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return reply({ items: [], total: 0, has_more: false });
        if (url.pathname === '/api/admin/config') return reply({ categories: [] });
        if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return reply({});
        return reply({ code: 'unexpected_default_alias_request' }, 500);
      };
    },
  });
  dom.window.eval(standardMaterialPicker);
  dom.window.eval(host);
  dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
  await waitFor(() => dom.window.document.getElementById('pfName'), 'actual ordinary create alias must mount the frozen Product form');
  const policy = await waitFor(() => dom.window.document.querySelector('[data-distribution-policy]'), 'actual ordinary create alias must mount Product distribution controls');
  assert.equal(policy.querySelector('[data-distribution-policy-enabled]').checked, false, 'actual ordinary create alias starts with a disabled Product policy');
  const leadChannels = dom.window.document.querySelector('[data-product-purchase-lead-channel]');
  assert.deepEqual([...leadChannels.options].map((option) => [option.value, option.textContent]), [['', '不配置引流渠道码'], ['17', '付款后添加企微']], 'new Product action must list active Channel resources and exclude archived choices');
  const pushPanel = await waitFor(() => dom.window.document.querySelector('[data-product-parity-push]'), 'new Product must mount the same external-push panel as edit');
  assert.equal(pushPanel.querySelector('[data-product-parity-push-url]')?.disabled, false, 'new Product external-push fields are editable before the first save');
  const pushEnabled = pushPanel.querySelector('[data-product-parity-push-enabled]');
  pushEnabled.checked = true; pushEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  pushPanel.querySelector('[data-product-parity-push-url]').value = 'https://hooks.example.test/new-product';
  pushPanel.querySelector('[data-product-parity-push-type]').value = 'paid_notify';
  const actionEnabled = dom.window.document.querySelector('[data-product-purchase-enabled]');
  actionEnabled.checked = true; actionEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  const qr = dom.window.document.querySelector('input[name="pfPurchaseActionMode"][value="qr"]');
  qr.checked = true; qr.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  for (const [id, value] of [['pfName', '默认分销商品'], ['pfCode', 'default-policy-product'], ['pfPrice', '0.02'], ['pfStock', '1']]) {
    const field = dom.window.document.getElementById(id);
    field.value = value;
    field.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  }
  const save = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
  assert.ok(save, 'actual ordinary create alias must retain the frozen save action');
  save.click();
  await waitFor(() => dom.window.document.querySelector('#fb-toast')?.textContent.includes('请选择引流渠道码'), 'QR action without a Channel must show an actionable validation message');
  assert.equal(calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST').length, 0, 'missing QR Channel must not send an invalid Product command');
  leadChannels.value = '17'; leadChannels.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  save.click();
  await waitFor(() => calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST').length === 1, 'actual ordinary create alias must submit the first Product command');
  const create = calls.find((call) => call.path === '/api/v1/products' && call.method === 'POST');
  assert.deepEqual(JSON.parse(create.body).distribution_policy, { enabled: false, commission_rate_basis_points: 0, wait_days: 7, version: 0 }, 'actual ordinary create alias must atomically submit the default Product policy');
  assert.equal(JSON.parse(create.body).admin_projection.enabled, true, 'new Product is enabled by default in its first subject command');
  assert.equal(JSON.parse(create.body).admin_projection.status, 'active', 'new Product starts in the enabled lifecycle');
  assert.equal(JSON.parse(create.body).admin_projection.lead_channel_id, 17, 'new Product action must persist the selected Channel resource');
  await waitFor(() => calls.some((call) => call.path === '/api/admin/wechat-pay/products/201/external-push' && call.method === 'PUT'), 'first Product save must persist the parity external-push draft after receiving its ID');
  const pushWrite = calls.find((call) => call.path === '/api/admin/wechat-pay/products/201/external-push' && call.method === 'PUT');
  assert.deepEqual(JSON.parse(pushWrite.body), { enabled: true, webhook_url: 'https://hooks.example.test/new-product', push_type: 'paid_notify', expires_at_ts: null, day: null, frequency: null, remark: '', custom_params: '{}', expected_revision: 0 });
  await waitFor(() => dom.window.document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度'), 'actual ordinary create alias must finish the complete saved-product flow');
  assert.equal(dom.window.document.querySelector('#product-v3-toast')?.textContent.includes('分销设置尚未加载'), false, 'actual ordinary create alias must not reject its default policy as unloaded');
  await waitFor(() => new URL(dom.window.location.href).searchParams.get('id') === '201', 'actual ordinary create alias must retain the created ID');
  assert.equal(dom.window.location.pathname, '/admin/wechat-pay/productForm.html', 'actual ordinary create alias must remain on the canonical page');
  assert.equal(errors.length, 0, 'actual ordinary create alias must not fail before the Product command');
  dom.window.close();
}

async function verifyDuplicateCodeIsExplainedBeforeCreate() {
  const calls = [];
  const dom = new JSDOM(page, {
    url: 'https://test.invalid/admin/wechat-pay/productForm.html', runScripts: 'outside-only', pretendToBeVisual: true,
    beforeParse(window) {
      window.Request = Request; window.Response = Response; window.Headers = Headers;
      window.AICRMTagPicker = { open() {} };
      window.fetch = async (input, init = {}) => {
        const url = new URL(input instanceof Request ? input.url : String(input), window.location.href);
        const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
        calls.push({ path: url.pathname, method });
        const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
        if (url.pathname === '/api/v1/products' && method === 'GET') return reply({ items: [{ id: 5, product_code: '123', name: '测试商品', description: '', price_minor: 0, currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z' }] });
        if (url.pathname === '/api/admin/channels') return reply({ items: [], total: 0 });
        if (url.pathname === '/api/admin/wecom/tags') return reply({ read_model_status: 'ready', groups: [], items: [], count: 0, total_tags: 0, tag_limit: 1000 });
        if (url.pathname.startsWith('/api/admin/')) return reply({ items: [], total: 0, has_more: false });
        return reply({ code: 'unexpected' }, 500);
      };
    },
  });
  dom.window.eval(host); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
  await waitFor(() => dom.window.document.getElementById('pfName'), 'duplicate-code form must mount');
  for (const [id, value] of [['pfName', '定金'], ['pfCode', '123'], ['pfPrice', '100'], ['pfStock', '1']]) dom.window.document.getElementById(id).value = value;
  [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度').click();
  await waitFor(() => dom.window.document.querySelector('#fb-toast')?.textContent.includes('商品编码「123」已存在'), 'duplicate code must have a business error');
  assert.equal(calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST').length, 0, 'known duplicate code must not POST');
  dom.window.close();
}

await verifyDefaultPolicyAtActualCreateAlias();
await verifyDuplicateCodeIsExplainedBeforeCreate();
const created = { id: 101, product_code: 'recovery-product', name: '恢复商品', description: '', price_minor: 2, currency: 'CNY', stock_quantity: 1, images: [], admin_projection: projection, lifecycle: 'draft', enabled: false, paid_order_count: 0, refund_order_count: 0, sold_count: 0, version: 1, distribution_policy: { enabled: true, commission_rate_basis_points: 1234, wait_days: 8, version: 1 }, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z' };
const calls = [];
let savedVersion = 1;
let failEditProduct = false;
let failCreatePush = true;
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', () => undefined);
const navigationErrors = [];
const editorConsole = new VirtualConsole();
editorConsole.on('jsdomError', error => { if (String(error.message).includes('navigation')) navigationErrors.push(error.message); });
const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/wechat-pay/productForm.html', runScripts: 'outside-only', pretendToBeVisual: true, virtualConsole: editorConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.AICRMStandardComponents = { ready: () => Promise.resolve() };
    window.AICRMTagPicker = { open(options) { options.loadPage({ query: '', signal: new AbortController().signal }).then((page) => options.onCommit({ selected: page.items.slice(0, 1), added: page.items.slice(0, 1), removed: [] })); } };
    window.fetch = async (input, init = {}) => {
      const raw = input instanceof Request ? input.url : String(input);
      const url = new URL(raw, window.location.href);
      const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
      calls.push({ path: url.pathname, method, key: new Headers(init.headers || (input instanceof Request ? input.headers : undefined)).get('Idempotency-Key') || '', body: typeof init.body === 'string' ? init.body : '' });
      const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });
      if (url.pathname === '/api/v1/products/101') {
        if (method === 'PUT') {
          assert.equal(JSON.parse(init.body).expected_version, savedVersion);
          if (failEditProduct) { failEditProduct = false; return reply({ code: 'dependency_unavailable' }, 503); }
          savedVersion++;
        }
        return reply({ ...created, version: savedVersion });
      }
      if (url.pathname === '/api/v1/products' && method === 'GET') return reply({ items: [], next_cursor: '' });
      if (url.pathname === '/api/v1/products' && method === 'POST') return reply(created);
      if (url.pathname === '/api/admin/wechat-pay/products/101/external-push') {
        if (method === 'PUT' && failCreatePush) { failCreatePush = false; return reply({ code: 'dependency_unavailable' }, 503); }
        const body = method === 'PUT' ? JSON.parse(String(init.body || '{}')) : {};
        return reply({ product_id: 101, product_kind: 'wechat_pay', enabled: body.enabled ?? false, configuration_reference: '', revision: method === 'PUT' ? 1 : 0, webhook_url: body.webhook_url ?? '', push_type: body.push_type ?? '', expires_at_ts: body.expires_at_ts ?? null, day: body.day ?? null, frequency: body.frequency ?? null, remark: body.remark ?? '', custom_params: JSON.parse(body.custom_params ?? '{}'), custom_params_json: body.custom_params ?? '{}' });
      }
      if (url.pathname === '/api/admin/channels') return reply({ items: [{ id: 17, channel_name: '付款后添加企微', channel_code: 'paid-lead', status: 'active' }], total: 1 });
      if (url.pathname === '/api/admin/wecom/tags') return reply({ read_model_status: 'ready', groups: [{ group_id: 4, group_name: '已同步标签' }], items: [{ tag_id: 37, tag_name: '已购买', group_id: 4, group_name: '已同步标签' }], count: 1, total_tags: 1, tag_limit: 1000 });
      if (url.pathname === '/api/admin/image-library/38') return reply({ item: { id: 38, name: '页面素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true } });
      if (url.pathname === '/api/admin/image-library/39') return reply({ item: { id: 39, name: '后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true } });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '0') return reply({ items: [{ id: 38, name: '页面素材', original_url: '/api/admin/image-library/38/variants/original', thumb_320_url: '/api/admin/image-library/38/variants/thumb_320', enabled: true }], total: 1, has_more: true, next_offset: 1 });
      if (url.pathname === '/api/admin/image-library' && url.searchParams.get('offset') === '1') return reply({ items: [{ id: 39, name: '后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], total: 2, has_more: false });
      if (url.pathname === '/api/admin/image-library') return reply({ items: [{ id: 39, name: '后续页素材', original_url: '/api/admin/image-library/39/variants/original', thumb_320_url: '/api/admin/image-library/39/variants/thumb_320', enabled: true }], total: 1, has_more: false });
      if (url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/questionnaires' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons') return reply({ items: [], total: 0, has_more: false });
      if (url.pathname === '/api/admin/config') return reply({ categories: [] });
      if (url.pathname === '/api/admin/app-settings' || url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return reply({});
      return reply({ code: 'unexpected_product_request' }, 500);
    };
  },
});

dom.window.eval(standardMaterialPicker);
dom.window.eval(host);

dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
await waitFor(() => dom.window.document.getElementById('pfName'), 'frozen product form must mount through the real Admin client');
const newPolicy = await waitFor(() => dom.window.document.querySelector('[data-distribution-policy]'), 'new ordinary product must render editable distribution controls in sale information');
assert.equal(newPolicy.parentElement.id, 'product-sale', 'new ordinary distribution controls belong only to sale information');
assert.equal(newPolicy.querySelector('[data-distribution-policy-enabled]').checked, false, 'new ordinary product defaults distribution to disabled');
assert.equal(newPolicy.querySelector('[data-distribution-policy-rate]').value, '0.00', 'new ordinary product defaults commission rate');
assert.equal(newPolicy.querySelector('[data-distribution-policy-wait-days]').value, '7', 'new ordinary product defaults refund-review wait days');
const materialOpen = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '从素材库选择');
materialOpen.click();
await waitFor(() => dom.window.document.querySelector('[data-v3-selection-session="material"]'), 'the V3 product picker must open over the original product draft');
assert.equal(dom.window.document.querySelector('.pk-mask'), null, 'the V3 product bridge must not open a second frozen picker');
assert.match(dom.window.document.querySelector('.aicrm-material-picker__head p').textContent, /上传或外部图片请在页面原图列表中管理/, 'the V3 picker must explain that non-library images retain the owner page controls');
  const firstPageMaterial = await waitFor(() => dom.window.document.querySelector('[data-v3-material-key$=":38"]'), 'the V3 product picker must render its first server page');
  firstPageMaterial.click();
  dom.window.document.querySelector('[data-v3-picker-more]').click();
  const materialRow = await waitFor(() => dom.window.document.querySelector('[data-v3-material-key$=":39"]'), 'the V3 product picker must retain later catalog pages');
  materialRow.click();
  assert.match(dom.window.document.querySelector('[data-v3-picker-selected]').textContent, /页面素材.*后续页素材/s, 'product selection must retain the complete temporary multi-select draft across catalog pages');
  dom.window.document.querySelector('[data-v3-material-remove$=":38"]').click();
  assert.match(dom.window.document.querySelector('[data-v3-picker-selected]').textContent, /后续页素材/, 'removing one item must remain a temporary V3 draft change');
assert.ok(dom.window.document.querySelector('[data-v3-selection-session="material"]'), 'later-page material stays temporary until confirmation');
dom.window.document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => dom.window.document.querySelector('[data-v3-selection-session="material"]') === null, 'confirmed V3 selection must close only after the original product draft accepts it');
await waitFor(() => [...dom.window.document.querySelectorAll('img')].some((image) => image.src.includes('/39/variants/thumb_320')), 'the frozen form must render the original later-page material selection');
materialOpen.click();
await waitFor(() => dom.window.document.querySelector('[data-v3-picker-selected]')?.textContent.includes('后续页素材'), 'reopening must revalidate and reconstruct the product owner draft as selectedRecords');
dom.window.document.querySelector('[data-v3-picker-close]').click();
assert.ok([...dom.window.document.querySelectorAll('img')].some((image) => image.src.includes('/39/variants/thumb_320')), 'cancelling a reopened picker must retain the product draft');
const tagOpen = await waitFor(() => dom.window.document.querySelector('[data-product-tag-open]'), 'standard product tag control must mount');
tagOpen.click();
await waitFor(() => dom.window.document.getElementById('pfWecomTagging').value.includes('37'), 'V3 tag picker must return an Owner catalog tag to the product draft');
const tagEnabled = dom.window.document.querySelector('[data-product-tag-enabled]');
tagEnabled.checked = true; tagEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
await waitFor(() => dom.window.document.getElementById('pfWecomTagging').value === '{"enabled":true,"tag_ids":[37]}', 'original tag picker must persist an enabled canonical numeric tag id');
tagEnabled.checked = false; tagEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
assert.equal(dom.window.document.getElementById('pfWecomTagging').value, '{"enabled":false,"tag_ids":[37]}', 'disabling tags preserves the selected draft without enabling paid tagging');
tagEnabled.checked = true; tagEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
const actionEnabled = dom.window.document.querySelector('[data-product-purchase-enabled]');
actionEnabled.checked = true; actionEnabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
const qr = dom.window.document.querySelector('input[name="pfPurchaseActionMode"][value="qr"]');
qr.checked = true; qr.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
dom.window.document.querySelector('[data-product-purchase-lead-channel]').value = '17';
assert.equal(dom.window.document.querySelector('[data-product-purchase-lead]').hidden, false, 'QR fields must appear only for QR mode');
assert.equal(dom.window.document.querySelector('[data-product-purchase-redirect]').hidden, true, 'redirect fields must stay hidden in QR mode');
for (const [id, value] of [['pfName', '恢复商品'], ['pfCode', 'recovery-product'], ['pfPrice', '0.02'], ['pfStock', '1']]) {
  const field = dom.window.document.getElementById(id);
  field.value = value;
  field.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
}
const policyBeforeCreate = dom.window.document.querySelector('[data-distribution-policy]');
policyBeforeCreate.querySelector('[data-distribution-policy-enabled]').checked = true;
policyBeforeCreate.querySelector('[data-distribution-policy-enabled]').dispatchEvent(new dom.window.Event('change', { bubbles: true }));
policyBeforeCreate.querySelector('[data-distribution-policy-rate]').value = '12.34';
policyBeforeCreate.querySelector('[data-distribution-policy-rate]').dispatchEvent(new dom.window.Event('input', { bubbles: true }));
policyBeforeCreate.querySelector('[data-distribution-policy-wait-days]').value = '8';
policyBeforeCreate.querySelector('[data-distribution-policy-wait-days]').dispatchEvent(new dom.window.Event('input', { bubbles: true }));
assert.equal(dom.window.document.querySelector('[data-distribution-policy-rate]').value, '12.34', 'new ordinary product must retain the edited commission before create');
assert.equal(dom.window.document.querySelector('[data-distribution-policy-wait-days]').value, '8', 'new ordinary product must retain the edited wait days before create');
assert.equal(dom.window.document.querySelector('[data-distribution-policy-enabled]').checked, true, 'new ordinary product must retain the selected state before create');
const save = [...dom.window.document.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存当前维度');
assert.ok(save, 'frozen product form must retain save action');
save.click();
save.click();
await waitFor(() => dom.window.document.body.textContent.includes('商品已创建，外部推送保存失败'), 'a failed first external-push write must report that the Product already exists');
const creates = calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST');
assert.equal(creates.length, 1, 'duplicate save clicks must create one product');
assert.match(creates[0].key, /^product-save-/, 'subject create must carry an idempotency key');
const createPayload = JSON.parse(creates[0].body);
assert.deepEqual(createPayload.admin_projection.wecom_tagging, { enabled: true, tag_ids: [37] }, 'product save must forward the enabled catalog-selected numeric tag IDs');
assert.deepEqual(createPayload.images, ['/api/admin/image-library/39/variants/original'], 'product save must preserve the original picker later-page URL');
assert.deepEqual(createPayload.distribution_policy, { enabled: true, commission_rate_basis_points: 1234, wait_days: 8, version: 0 }, 'ordinary product create must atomically carry the edited distribution policy');
assert.equal(createPayload.admin_projection.purchase_action_enabled, true, 'product save must enable the selected purchase action');
assert.equal(createPayload.admin_projection.purchase_action_mode, 'qr', 'product save must preserve the selected QR action mode');
assert.equal(new URL(dom.window.location.href).searchParams.get('id'), '101', 'created product must enter its editor URL');
dom.window.document.querySelector('[data-product-parity-push-save]').click();
await waitFor(() => calls.filter((call) => call.path === '/api/admin/wechat-pay/products/101/external-push' && call.method === 'PUT').length === 2, 'the retained parity panel must retry only the failed configuration command');
await waitFor(() => dom.window.document.querySelector('[data-product-parity-push-result]')?.textContent.includes('配置已保存'), 'the retained parity panel must finish its retry');
assert.equal(calls.filter((call) => call.path === '/api/v1/products' && call.method === 'POST').length, 1, 'external-push retry must not create a second Product');
const policyAfterCreate = dom.window.document.querySelector('[data-distribution-policy]');
assert.equal(policyAfterCreate.dataset.distributionPolicyVersion, '1', 'new ordinary product must read back the server policy revision after receiving an ID');
assert.equal(policyAfterCreate.querySelector('[data-distribution-policy-enabled]').checked, true, 'new ordinary product readback must retain the selected distribution state');
assert.equal(policyAfterCreate.querySelector('[data-distribution-policy-rate]').value, '12.34', 'new ordinary product readback must retain the edited commission rate');
assert.equal(policyAfterCreate.querySelector('[data-distribution-policy-wait-days]').value, '8', 'new ordinary product readback must retain the edited wait days');

await wait(300);
assert.equal(dom.window.location.pathname, '/admin/wechat-pay/productForm.html', 'successful create must remain in the actual ordinary-product alias');
assert.equal(new URL(dom.window.location.href).searchParams.get('id'), '101');
dom.reconfigure({url:'https://test.invalid/admin/wechat-pay/products/101/edit'});
const actionLink = dom.window.document.querySelector('a[href="#product-action"]');
actionLink.click();
assert.equal(actionLink.getAttribute('aria-current'), 'step', 'saved editor must retain working dimension navigation');
for (const expected of [2, 3]) {
  [...dom.window.document.querySelectorAll('button')].find(button => button.textContent.trim() === '保存当前维度').click();
  await waitFor(() => savedVersion === expected, 'consecutive save must advance opened CAS version');
  await wait(80);
  assert.match(dom.window.document.querySelector('#product-v3-toast').textContent, /已保存当前维度/);
}
failEditProduct = true;
const editSave = [...dom.window.document.querySelectorAll('button')].find(button => button.textContent.trim() === '保存当前维度');
editSave.click();
await waitFor(() => dom.window.document.querySelector('#fb-toast')?.textContent.includes('HTTP 503'), 'failed subject save must retain the editable product form');
const failedEdit = calls.filter(call => call.path === '/api/v1/products/101' && call.method === 'PUT').at(-1);
editSave.click();
await waitFor(() => savedVersion === 4 && dom.window.document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度'), 'retry must submit the retained subject form');
const retriedEdit = calls.filter(call => call.path === '/api/v1/products/101' && call.method === 'PUT').at(-1);
assert.equal(retriedEdit.key, failedEdit.key, 'subject retry must retain its original idempotency key');
assert.equal(navigationErrors.length, 0, 'successful saves must not navigate to the list');
[...dom.window.document.querySelectorAll('button')].find(button => button.textContent.trim() === '返回商品管理').click();
await wait(30);
assert.equal(navigationErrors.length, 1, 'explicit Back must still invoke list navigation');
dom.window.close();
console.log('product Host duplicate-save and partial-recovery journey: PASS');
