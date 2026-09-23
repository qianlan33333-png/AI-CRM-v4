import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const html = fs.readFileSync(path.join(root, 'dist/admin/productForm.html'), 'utf8');
const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
const calls = [];
const projection = {
  schema_version: 1, status: 'draft', enabled: false, buy_button_text: '购买课程', require_mobile: false,
  lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false,
  completion_redirect_url: '', completion_target: null, wecom_tagging: {}, slices: [],
};
const libraryItem = {
  id: 8, file_name: '主视觉.png',
  original_url: '/api/admin/image-library/8/variants/original',
  thumb_320_url: '/api/admin/image-library/8/variants/thumb_320',
};
const product = (patch = {}) => ({
  id: 21, product_code: 'P-21', name: '课程', description: '说明', price_minor: 19900, currency: 'CNY', stock_quantity: 9,
  images: ['/api/admin/image-library/8/variants/original'], admin_projection: projection, created_by: 1,
  created_at: '2026-08-30T00:00:00Z', updated_at: '2026-08-30T00:00:00Z', version: 1, ...patch,
});
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(body) });
const dom = new JSDOM(html, {
  url: 'http://localhost/admin/productForm.html?id=21', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.document.cookie = `aicrm_csrf=${'c'.repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin); const method = init.method || 'GET';
      calls.push({ path: url.pathname, method, headers: new Headers(init.headers), body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (url.pathname === '/api/admin/channels') return json({ channels: [] });
      if (url.pathname === '/api/admin/image-library' && method === 'GET') return json({ items: [libraryItem] });
      if (url.pathname === '/api/admin/wecom/tag-groups' && method === 'GET') return json({ items: [] });
      if (url.pathname === '/api/admin/wecom/tags' && method === 'GET') return json({ items: [] });
      if (url.pathname === '/api/v1/products' && method === 'GET') return json({ items: [product()] });
      if (url.pathname === '/api/v1/products/21/local-entitlements') return json({ items: [] });
      if (url.pathname === '/api/admin/wechat-pay/products/21/external-push') {
        if (method === 'PUT') return json({ product_id: 21, product_kind: 'wechat_pay', enabled: true, configuration_reference: 'paid-course-21', updated_at: '2026-08-30T00:01:00Z' });
        return json({ product_id: 21, product_kind: 'wechat_pay', enabled: false, updated_at: '2026-08-30T00:00:00Z' });
      }
      if (url.pathname === '/api/v1/products/21' && method === 'PUT') return json(product({ name: '课程新版', images: init.body ? JSON.parse(String(init.body)).images : [], admin_projection: init.body ? JSON.parse(String(init.body)).admin_projection : projection, version: 2 }));
      if (url.pathname === '/api/v1/products/21') return json(product());
      return json({ code: 'unexpected_request', path: url.pathname }, 500);
    };
  },
});
dom.window.eval(bundle);
await new Promise((resolve) => setTimeout(resolve, 60));
const document = dom.window.document;
if (document.querySelector('#pfImages')) throw new Error('pfImages 输入框不应存在');
if (!document.querySelector('#pfImageUpload')) throw new Error('pfImageUpload 上传控件未渲染');
const thumb = document.querySelector('img[src="/api/admin/image-library/8/variants/thumb_320"]');
if (!thumb || thumb.getAttribute('alt') !== '主视觉.png') throw new Error('图片库缩略图未正确渲染');
const wecomTagging = document.querySelector('#pfWecomTagging');
if (!wecomTagging || !wecomTagging.closest('details')) throw new Error('企微打标控件未位于 details 内');
if (!document.body.textContent.includes('未定义标签选择 DTO')) throw new Error('缺少未定义标签选择 DTO 提示');
if (!document.querySelector('#pfExternalPushReference') || document.body.textContent.includes('后端能力未就绪')) throw new Error('普通商品完整编辑表单未渲染');
document.querySelector('#pfName').value = '课程新版';
document.querySelector('#pfBuyButtonText').value = '立即购买';
document.querySelector('#pfRequireMobile').value = 'true';
document.querySelector('#pfCompletionRedirectEnabled').value = 'true';
document.querySelector('#pfCompletionRedirectUrl').value = 'https://example.test/complete';
document.querySelector('#pfWecomTagging').value = '{"tag_ids":["tag-1"]}';
document.querySelector('#pfExternalPushEnabled').value = 'true';
document.querySelector('#pfExternalPushReference').value = 'paid-course-21';
[...document.querySelectorAll('button')].find((button) => button.textContent.includes('保存当前维度'))?.click();
await new Promise((resolve) => setTimeout(resolve, 60));
const update = calls.find((call) => call.path === '/api/v1/products/21' && call.method === 'PUT');
const push = calls.find((call) => call.path.endsWith('/external-push') && call.method === 'PUT');
if (!update || update.body.images[0] !== '/api/admin/image-library/8/variants/original' || update.body.admin_projection.buy_button_text !== '立即购买' || update.body.admin_projection.wecom_tagging.tag_ids[0] !== 'tag-1' || !update.headers.get('Idempotency-Key') || !push || push.body.configuration_reference !== 'paid-course-21' || !push.headers.get('Idempotency-Key')) throw new Error('普通商品完整编辑未发出真实写入请求');
for (const path of ['/api/admin/image-library', '/api/admin/wecom/tag-groups', '/api/admin/wecom/tags']) {
  const read = calls.find((call) => call.path === path);
  if (!read || read.method !== 'GET') throw new Error(`${path} 未通过 GET 读取`);
}
if (calls.some((call) => /\/test$|checkout|refund|dispatch|send/.test(call.path))) throw new Error('商品编辑意外触发外部效果');
dom.window.close();

// 场景二：服务期商品独立编辑（spProductForm.html?id=8）
const spHtml = fs.readFileSync(path.join(root, 'dist/admin/spProductForm.html'), 'utf8');
const spCalls = [];
const spProduct = (patch = {}) => ({
  id: 8, product_code: 'SP-8', name: '服务期商品', description: '说明', price_minor: 19900, currency: 'CNY', stock_quantity: 9,
  images: ['/api/admin/image-library/8/variants/original'], admin_projection: projection, created_by: 1,
  created_at: '2026-08-30T00:00:00Z', updated_at: '2026-08-30T00:00:00Z', version: 1, ...patch,
});
const spDom = new JSDOM(spHtml, {
  url: 'http://localhost/admin/spProductForm.html?id=8', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.document.cookie = `aicrm_csrf=${'c'.repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin); const method = init.method || 'GET';
      spCalls.push({ path: url.pathname, method, headers: new Headers(init.headers), body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (url.pathname === '/api/admin/channels') return json({ channels: [] });
      if (url.pathname === '/api/admin/image-library' && method === 'GET') return json({ items: [libraryItem] });
      if (url.pathname === '/api/admin/wecom/tag-groups' && method === 'GET') return json({ items: [] });
      if (url.pathname === '/api/admin/wecom/tags' && method === 'GET') return json({ items: [] });
      if (url.pathname === '/api/admin/service-period-products' && method === 'GET') return json({ items: [spProduct()] });
      if (url.pathname === '/api/admin/service-period-products/8/members' && method === 'GET') return json({ items: [] });
      if (url.pathname === '/api/admin/service-period-products/8/member-grid/access' && method === 'GET') return json({ role: 'admin' });
      if (url.pathname === '/api/admin/service-period-products/8/member-grid/schema' && method === 'GET') return json({ version: 1 });
      if (url.pathname === '/api/admin/service-period-products/8/member-views' && method === 'GET') return json({ items: [] });
      if (url.pathname === '/api/admin/service-period-products/8/member-grid/share-settings' && method === 'GET') return json({ external_share_enabled: false, external_edit_enabled: false });
      if (url.pathname === '/api/admin/service-period-products/8/external-push') {
        if (method === 'PUT') return json({ product_id: 8, product_kind: 'service_period', enabled: true, configuration_reference: 'service-paid-8', updated_at: '2026-08-30T00:01:00Z' });
        return json({ product_id: 8, product_kind: 'service_period', enabled: false, updated_at: '2026-08-30T00:00:00Z' });
      }
      if (url.pathname === '/api/admin/service-period-products/8' && method === 'PUT') {
        const payload = init.body ? JSON.parse(String(init.body)) : {};
        return json({ product: spProduct({ name: payload.name ?? '服务期商品', images: payload.images ?? [], admin_projection: payload.admin_projection ?? projection, version: 2 }) });
      }
      if (url.pathname === '/api/admin/service-period-products/8' && method === 'GET') return json({ product: spProduct() });
      return json({ code: 'unexpected_request', path: url.pathname }, 500);
    };
  },
});
spDom.window.eval(bundle);
await new Promise((resolve) => setTimeout(resolve, 60));
const spDocument = spDom.window.document;
if (spDocument.querySelector('#spfImages')) throw new Error('spfImages 输入框不应存在');
if (!spDocument.querySelector('#spfImageUpload')) throw new Error('spfImageUpload 上传控件未渲染');
const spThumb = spDocument.querySelector('img[src="/api/admin/image-library/8/variants/thumb_320"]');
if (!spThumb || spThumb.getAttribute('alt') !== '主视觉.png') throw new Error('服务期商品图片库缩略图未正确渲染');
const spWecomTagging = spDocument.querySelector('#spfWecomTagging');
if (!spWecomTagging || !spWecomTagging.closest('details')) throw new Error('服务期商品企微打标控件未位于 details 内');
if (!spDocument.body.textContent.includes('未定义标签选择 DTO')) throw new Error('服务期商品缺少未定义标签选择 DTO 提示');
spDocument.querySelector('#spfName').value = '服务期商品新版';
spDocument.querySelector('#spfBuyButtonText').value = '立即开通';
spDocument.querySelector('#spfRequireMobile').value = 'true';
spDocument.querySelector('#spfCompletionRedirectEnabled').value = 'true';
spDocument.querySelector('#spfCompletionRedirectUrl').value = 'https://example.test/sp-complete';
spDocument.querySelector('#spfWecomTagging').value = '{"tag_ids":["tag-sp"]}';
spDocument.querySelector('#spfExternalPushEnabled').value = 'true';
spDocument.querySelector('#spfExternalPushReference').value = 'service-paid-8';
[...spDocument.querySelectorAll('button')].find((button) => button.textContent.includes('保存当前维度'))?.click();
await new Promise((resolve) => setTimeout(resolve, 60));
const spUpdate = spCalls.find((call) => call.path === '/api/admin/service-period-products/8' && call.method === 'PUT');
const spPush = spCalls.find((call) => call.path === '/api/admin/service-period-products/8/external-push' && call.method === 'PUT');
if (!spUpdate || spUpdate.body.images[0] !== '/api/admin/image-library/8/variants/original' || spUpdate.body.admin_projection.buy_button_text !== '立即开通' || spUpdate.body.admin_projection.require_mobile !== true || spUpdate.body.admin_projection.completion_redirect_enabled !== true || spUpdate.body.admin_projection.wecom_tagging.tag_ids[0] !== 'tag-sp' || !spUpdate.headers.get('Idempotency-Key')) throw new Error('服务期商品编辑未保留原图或管理投影字段');
if (!spPush || spPush.body.configuration_reference !== 'service-paid-8' || !spPush.headers.get('Idempotency-Key')) throw new Error('服务期商品 external-push 写入未使用 reference 或缺少 Idempotency-Key');
for (const path of ['/api/admin/service-period-products', '/api/admin/service-period-products/8', '/api/admin/service-period-products/8/members', '/api/admin/service-period-products/8/member-grid/access', '/api/admin/service-period-products/8/member-grid/schema', '/api/admin/service-period-products/8/member-views', '/api/admin/service-period-products/8/member-grid/share-settings', '/api/admin/service-period-products/8/external-push', '/api/admin/channels', '/api/admin/image-library', '/api/admin/wecom/tag-groups', '/api/admin/wecom/tags']) {
  const read = spCalls.find((call) => call.path === path);
  if (!read || read.method !== 'GET') throw new Error(`${path} 未通过 GET 读取`);
}
if (spCalls.some((call) => /\/test$|checkout|refund|dispatch|send/.test(call.path))) throw new Error('服务期商品编辑意外触发外部效果');
spDom.window.close();

console.log('product-edit-e2e: PASS');
