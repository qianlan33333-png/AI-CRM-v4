import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import jsdom from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const { JSDOM, VirtualConsole } = jsdom;
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/couponAdapter.ts'));
const sourceTemplate = await fs.readFile(path.join(root, 'web/src/admin/templates/couponData.html'), 'utf8');
const template = sourceTemplate
  .replace(/<sc-for list="([^"]+)" as="([^"]+)"([^>]*)>/g, '<template data-sc-for="$1" data-as="$2"$3>')
  .replace(/<\/sc-for>/g, '</template>')
  .replace(/<sc-if value="([^"]+)"([^>]*)>/g, '<template data-sc-if="$1"$2>')
  .replace(/<\/sc-if>/g, '</template>');
const now = Date.now();
const activeClaim = {
  claim_id: 91, customer_id: 88001, coupon_id: 41, status: 'claimed', claim_no_masked: '***1',
  claimed_at: new Date(now - 5_000).toISOString(), valid_from: new Date(now - 60_000).toISOString(), valid_until: new Date(now + 60_000).toISOString(), redeemed_at: null,
};
const availableClaim = {
  claim_id: 93, customer_id: 88003, coupon_id: 41, status: 'available', claim_no_masked: '***3',
  claimed_at: new Date(now - 5_000).toISOString(), valid_from: new Date(now - 60_000).toISOString(), valid_until: new Date(now + 60_000).toISOString(), redeemed_at: null,
};
const futureClaim = {
  claim_id: 94, customer_id: 88004, coupon_id: 41, status: 'claimed', claim_no_masked: '***4',
  claimed_at: new Date(now - 5_000).toISOString(), valid_from: new Date(now + 60_000).toISOString(), valid_until: new Date(now + 120_000).toISOString(), redeemed_at: null,
};
const expiredClaim = {
  claim_id: 95, customer_id: 88005, coupon_id: 41, status: 'claimed', claim_no_masked: '***5',
  claimed_at: new Date(now - 120_000).toISOString(), valid_from: new Date(now - 120_000).toISOString(), valid_until: new Date(now - 60_000).toISOString(), redeemed_at: null,
};
const redeemedClaim = {
  claim_id: 96, customer_id: 88006, coupon_id: 41, status: 'redeemed', claim_no_masked: '***6',
  claimed_at: new Date(now - 120_000).toISOString(), valid_from: new Date(now - 120_000).toISOString(), valid_until: new Date(now + 60_000).toISOString(), redeemed_at: new Date(now - 30_000).toISOString(),
};
const unconfirmedClaim = {
  claim_id: 92, customer_id: 88002, coupon_id: 41, status: 'claimed', claim_no_masked: '***2',
  claimed_at: new Date(now - 5_000).toISOString(), valid_from: null, valid_until: null, redeemed_at: null,
};
const coupon = {
  id: 41, name: '领取展示券', discount_amount_total: 100, currency: 'CNY', status: 'published', availability_status: 'active',
  total_issue_limit: 5, per_user_issue_limit: 1, issued_count: 2,
  claim_starts_at: new Date(now - 60_000).toISOString(), claim_ends_at: new Date(now + 60_000).toISOString(), validity_mode: 'relative_days',
  target_refs: ['standard_product:9'], version: 2,
};
const calls = [];
const errors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => errors.push(error));
const dom = new JSDOM(`<!doctype html><body class="admin-shell" data-page="couponData"><main id="stage"></main><template id="tpl">${template}</template></body>`, {
  url: 'https://test.invalid/admin/couponData.html?id=41', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input instanceof window.URL ? input.toString() : input.url, window.location.href);
      calls.push({ url, method: String(init.method || 'GET').toUpperCase() });
      if (url.pathname === '/api/admin/coupons/41') return Response.json({ ok: true, coupon });
      if (url.pathname === '/api/admin/coupons/41/share') return Response.json({ ok: true, url: '/c/coupon-41' });
      if (url.pathname === '/api/admin/coupons/41/claims') {
        if (url.searchParams.get('forbidden') === '1') return Response.json({ code: 'forbidden' }, { status: 403 });
        const second = url.searchParams.get('offset') === '50';
        return Response.json({ ok: true, coupon_id: 41, items: second ? [unconfirmedClaim] : [activeClaim, availableClaim, futureClaim, expiredClaim, redeemedClaim], total: 51, limit: 50, offset: second ? 50 : 0 });
      }
      if (url.pathname === '/api/admin/coupons/product-options') return Response.json({ ok: true, items: [], total: 0, limit: 50, offset: 0 });
      if (url.pathname === '/api/admin/coupons') return Response.json({ ok: true, items: [coupon], total: 1, limit: 50, offset: 0 });
      return Response.json({ ok: true, items: [], total: 0, limit: 50, offset: 0 });
    };
  },
});
const waitFor = async (check, message) => {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 15));
  }
  throw new Error(message);
};
try {
  dom.window.eval(host);
  const document = dom.window.document;
  await waitFor(() => (document.querySelector('#stage')?.textContent || '').includes('领取展示券'), 'couponData did not render its HTTP projection');
  const rendered = document.querySelector('#stage')?.textContent || '';
  assert.match(rendered, /已发布/, 'header status must use the existing Coupon presentation label');
  assert.match(rendered, /可用/, 'current claimed and available windows must become the confirmed available presentation');
  assert.match(rendered, /待生效/, 'a confirmed future window must not be counted as currently available');
  assert.match(rendered, /已过期/, 'a confirmed expired window must retain its lifecycle presentation');
  assert.match(rendered, /已使用/, 'a redeemed claim must retain its lifecycle presentation');
  assert.match(rendered, /当前可用\s*2/, 'current-page summary must count both current claimed and available records');
  assert.equal(document.querySelector('[data-v3-coupon-data-claim-label]')?.textContent, '领取时间', 'the coupon header must name its claim-start/end interval accurately');
  assert.equal(document.querySelector('[data-v3-coupon-data-issue-label]')?.textContent, '已领取 / 发行量', 'the coupon issue order label must match its existing claim/issue value');
  assert.match(rendered, /累计领取\s*51\s*发行 5/, 'the current total card must state the coupon issue total, not the claimed count');
  assert.match(rendered, /适用范围\s*指定商品（1项）/, 'coupon data must not expose a technical target reference as a product name');
  assert.doesNotMatch(rendered, /standard_product:9/, 'coupon data must not expose a technical target reference');
  assert.match(rendered, /\d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2} 至 \d{4}-\d{2}-\d{2} \d{2}:\d{2}:\d{2}/, 'coupon and claim windows must use Shanghai display times');
  assert.doesNotMatch(rendered, /published|claimed|T\d{2}:\d{2}:\d{2}\.\d{3}Z|88001/, 'the view must not expose raw lifecycle, ISO timestamps, or canonical customer ids');
  const denied = await dom.window.fetch('/api/admin/coupons/41/claims?forbidden=1');
  assert.equal(denied.status, 403, 'HTTP authorization failures remain untouched by the presentation adapter');
  assert.deepEqual(await denied.json(), { code: 'forbidden' });
  document.querySelector('#claim-next')?.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
  await waitFor(() => (document.querySelector('#stage')?.textContent || '').includes('待确认'), 'missing windows must remain explicitly unconfirmed on the next page');
  const nextPage = document.querySelector('#stage')?.textContent || '';
  assert.match(nextPage, /当前可用\s*—/, 'unconfirmed windows must not fabricate an available count');
  assert.match(nextPage, /已过期\s*—/, 'unconfirmed windows must not fabricate an expired count');
  assert.match(nextPage, /当前页含待确认记录/);
  assert.match(nextPage, /有效期待确认/);
  assert.deepEqual(errors, []);
} finally {
  // Keep this document alive until process exit: donor runtime observers are
  // intentionally mounted by the real AdminController lifecycle.
}
console.log('couponData claim lifecycle, Shanghai dates, unknown-window and HTTP-boundary presentation: PASS');
