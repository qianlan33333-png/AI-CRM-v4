import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole } = jsdom;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/couponAdapter.ts'));
const sourceTemplate = await fs.readFile(path.join(root, 'web/src/admin/templates/coupons.html'), 'utf8');
const template = sourceTemplate
  .replace(/<sc-for list="([^"]+)" as="([^"]+)"([^>]*)>/g, '<template data-sc-for="$1" data-as="$2"$3>')
  .replace(/<\/sc-for>/g, '</template>')
  .replace(/<sc-if value="([^"]+)"([^>]*)>/g, '<template data-sc-if="$1"$2>')
  .replace(/<\/sc-if>/g, '</template>');
const listCoupon = {
  id: 32, name: '普通商品券', discount_amount_total: 1, currency: 'CNY', status: 'published', availability_status: 'active', total_issue_limit: 3, per_user_issue_limit: 1, issued_count: 0,
  claim_starts_at: '2026-09-08T00:00:00Z', claim_ends_at: '2026-09-15T15:59:59Z', validity_mode: 'relative_days',
  target_refs: ['standard_product:32', 'service_period:18'],
  target_products: [{ target_ref: 'standard_product:32', name: '上线验收普通商品', state: 'available' }, { target_ref: 'service_period:18', state: 'not_found' }],
  created_by: 7, updated_by: 7, version: 1, created_at: '2026-09-08T00:00:00Z', updated_at: '2026-09-08T00:00:00Z',
};
const payload = { ok: true, coupons: [listCoupon], items: [listCoupon], total: 1, limit: 50, offset: 0 };
const calls = [];
const errors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => errors.push(error));
const dom = new JSDOM(`<!doctype html><body class="admin-shell" data-page="coupons"><main id="stage"></main><template id="tpl">${template}</template></body>`, {
  url: 'https://test.invalid/admin/coupons', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    Object.defineProperty(window, 'innerWidth', { value: 390, configurable: true });
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      calls.push({ url, method: String(init.method || 'GET').toUpperCase() });
      if (url.pathname === '/api/admin/coupons') return Response.json(payload);
      return Response.json({ ok: true, items: [], total: 0, limit: 50, offset: 0 });
    };
  },
});
const waitFor = async (check, message) => {
  for (let attempt = 0; attempt < 100; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 15));
  }
  throw new Error(message);
};
try {
  dom.window.eval(host);
  const document = dom.window.document;
  await waitFor(() => (document.querySelector('tbody')?.textContent || '').includes('上线验收普通商品'), 'coupon list did not render its server projection');
  assert.equal(document.querySelectorAll('th')[3]?.textContent, '领取时间范围');
  const rendered = document.querySelector('tbody')?.textContent || '';
  assert.match(rendered, /上线验收普通商品/);
  assert.match(rendered, /商品已删除或不可用/);
  assert.match(rendered, /2026-09-08 08:00:00 至 2026-09-15 23:59:59/);
  assert.match(rendered, /可领取/);
  assert.doesNotMatch(rendered, /standard_product:32|service_period:18|北京时间/);
  const table = document.querySelector('table[data-coupon-presentation-list="true"]');
  assert.ok(table, 'Host must mark the coupon presentation table');
  assert.equal(table.style.minWidth, '860px', '390px uses a horizontal-scrolling presentation instead of clipped columns');
  assert.equal(table.parentElement.style.overflowX, 'auto');
  assert.equal(table.parentElement.dataset.couponPresentationCard, 'true');
  assert.equal(table.parentElement.getAttribute('aria-label'), '优惠券列表；可横向滚动查看完整列');
  assert.equal(table.previousElementSibling?.textContent, '左右滑动查看领取时间范围、状态和操作');
  assert.equal(table.previousElementSibling?.previousElementSibling?.dataset.couponPresentationToolbar, 'true');
  assert.ok(document.querySelector('style[data-coupon-page-mobile-layout]')?.textContent.includes('data-coupon-presentation-toolbar'), 'Coupon Host must install a route-scoped mobile layout instead of changing the global shell');
  const before = calls.filter((call) => call.url.pathname === '/api/admin/coupons').length;
  const input = document.querySelector('input[placeholder="搜索优惠券名称"]');
  input.value = '普通';
  input.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  await new Promise((resolve) => setTimeout(resolve, 15));
  assert.equal(calls.filter((call) => call.url.pathname === '/api/admin/coupons').length, before, 'local filtering reuses the projected page');
  assert.doesNotMatch(document.querySelector('tbody')?.textContent || '', /standard_product:32|service_period:18/);
  assert.deepEqual(errors, []);
} finally {
  // Keep the one browser document alive until this isolated Node process exits:
  // closing it schedules JSDOM's observer callbacks after globals are torn down.
}
console.log('coupon list Product names, Shanghai window, Chinese state and 390px DOM: PASS');
