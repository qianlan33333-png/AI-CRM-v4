import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./distributionCenter.ts', import.meta.url)));
const json = (body) => new Response(JSON.stringify(body), { headers: { 'Content-Type': 'application/json' } });
const dom = new JSDOM('<!doctype html><main id="distribution-root"></main>', {
  url: 'https://crm.example/distribution', runScripts: 'outside-only',
  beforeParse(window) {
    window.Response = Response; window.Headers = Headers; window.URL = URL;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname.endsWith('/me')) return json({ distributor: { public_no: 'SYNTHETIC', enabled: true, agreement_version: 'v1', registered_at: '2026-10-09T00:00:00Z' }, receiver: { ready: true, reason: '', app_id: 'wx-test' }, settlement: { enabled: true, reason: '' }, registration_required: false, current_agreement_version: 'v1' });
      if (url.pathname.endsWith('/products')) return json({ items: [], next_cursor: '', empty_reason: 'no_saleable_policy_products' });
      if (url.pathname.endsWith('/earnings')) return json({ gross_paid_sales_minor: 10000, successful_refunds_minor: 0, initial_commission_minor: 1000, commission_adjustments_minor: 0, unsettled_payable_minor: 1000, paid_commission_minor: 0, recovered_minor: 0, currency: 'CNY' });
      if (url.pathname.endsWith('/commissions')) return json({ items: ['settlement_outcome_unknown', 'synthetic_private_diagnostic', '商家正在核验收款信息'].map((reason, i) => ({ commission_id: `c${i}`, order_reference: `SYNTHETIC-${i}`, product_name: `合成商品${i}`, initial_minor: 1000, current_payable_minor: 1000, paid_minor: 0, status: 'held', hold_reason: reason, cancel_reason: '', exception_reason: '', paid_confirmed_at: '2026-10-09T00:00:00Z', due_at: '2026-10-16T00:00:00Z', created_at: '2026-10-09T00:00:00Z', currency: 'CNY' })), next_cursor: '' });
      throw new Error(`unexpected synthetic route ${url.pathname}`);
    };
  },
});
async function waitFor(check) {
  for (let i = 0; i < 100; i++) { if (check()) return; await new Promise((resolve) => setTimeout(resolve, 15)); }
  throw new Error('synthetic earnings view did not settle');
}
try {
  dom.window.eval(bundle);
  await waitFor(() => [...dom.window.document.querySelectorAll('button')].some((b) => b.textContent === '我的收益'));
  [...dom.window.document.querySelectorAll('button')].find((b) => b.textContent === '我的收益').click();
  await waitFor(() => dom.window.document.body.textContent.includes('SYNTHETIC-2'));
  const text = dom.window.document.body.textContent;
  assert.match(text, /分账结果待核验/, 'unknown settlement result must remain a readable pending fact');
  assert.match(text, /原因待确认/, 'unknown protocol diagnostic must remain pending');
  assert.match(text, /商家正在核验收款信息/, 'existing Chinese business reason must be preserved');
  assert.doesNotMatch(text, /settlement_outcome_unknown|synthetic_private_diagnostic/, 'public commission details must not expose protocol diagnostics');
  console.log('distribution commission reason presentation: PASS');
} finally { dom.window.close(); }
