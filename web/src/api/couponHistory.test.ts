import { readCouponHistoryDefinitions, readCouponHistoryClaims, readCouponHistoryRedemptions } from './couponHistory';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
const at = '2026-08-28T00:00:00.000000Z';
const before = '2025-01-01T00:00:00Z';
const definition = {
  id: 31, source_coupon_id: 7, name: '原券', discount_amount_total: 9, currency: 'CNY',
  status: 'archived', availability_status: 'archived', history_only: true, original_status: 'expired',
  total_issue_limit: 100, per_user_issue_limit: 2, issued_count: 26, claim_starts_at: before, claim_ends_at: at,
  validity_mode: 'relative_days', use_starts_at: null, use_ends_at: null, relative_validity_days: 7,
  instructions: ' 原说明 ', target_refs: ['standard_product:8', 'standard_product:2'], first_claim_at: null,
  created_by: 2, updated_by: 2, version: 1, created_at: before, updated_at: at,
};
const claim = {
  id: 41, source_claim_id: 9, source_coupon_id: 7, coupon_id: 31, customer_id: null,
  claim_no: '', status: '', discount_amount_total: 0, currency: 'CNY', valid_from: at, valid_until: before,
  claimed_at: at, reserved_at: null, consumed_at: before, expired_at: null, created_at: at, updated_at: before,
};
const redemption = {
  id: 51, source_redemption_id: 10, source_claim_id: 9, source_order_id: 11, claim_history_id: 41, order_id: null,
  out_trade_no: '', status: 'released', original_amount_total: 5, discount_amount_total: 9, payable_amount_total: 17,
  currency: 'CNY', reserved_until: before, release_reason: ' 原因 ', reserved_at: at, consumed_at: null,
  released_at: before, created_at: at, updated_at: before,
};
const page = (items: unknown[], couponID?: number) => ({ source: 'v1_history', read_only: true, real_external_call_executed: false,
  items, total: items.length, limit: 20, offset: 0, ...(couponID === undefined ? {} : { coupon_id: couponID }) });

export async function runCouponHistoryAdapterTests(): Promise<void> {
  const originalFetch = globalThis.fetch;
  const calls: { url: URL; init: RequestInit }[] = [];
  let body: unknown = page([definition]);
  let status = 200;
  globalThis.fetch = async (input, init = {}) => {
    calls.push({ url: new URL(String(input), 'http://localhost'), init });
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  };
  const rejected = async (run: () => Promise<unknown>, message: string): Promise<void> => {
    try { await run(); } catch (error) { assert(error instanceof Error && error.message.includes(message), 'unexpected history error'); return; }
    throw new Error('history request unexpectedly succeeded');
  };
  try {
    const definitions = await readCouponHistoryDefinitions();
    assert(JSON.stringify(definitions.items[0]) === JSON.stringify(definition), 'definition facts or reference order changed');
    body = page([claim], 31);
    const claims = await readCouponHistoryClaims(31);
    assert(JSON.stringify(claims.items[0]) === JSON.stringify(claim), 'nullable links, empty states or inverted dates changed');
    body = page([redemption], 31);
    const redemptions = await readCouponHistoryRedemptions(31);
    assert(JSON.stringify(redemptions.items[0]) === JSON.stringify(redemption), 'historical amounts or release facts recalculated');
    assert(calls.map((call) => call.url.pathname).join(',') === '/api/admin/coupon-history,/api/admin/coupon-history/31/claims,/api/admin/coupon-history/31/redemptions', 'not using the three generated GET routes');
    assert(calls.every(({ url, init }) => url.search === '?limit=20&offset=0' && init.method === 'GET' && init.credentials === 'include' && !init.body), 'history transport must be authenticated read-only GET');
    body = { ...page([definition]), offset: 20, total: 21, limit: 1 };
    await readCouponHistoryDefinitions(20, 1);
    assert(calls[calls.length - 1]?.url.search === '?limit=1&offset=20', 'pagination was not passed to generated client');
    body = page([]);
    assert((await readCouponHistoryDefinitions()).total === 0, 'valid empty response failed');

    for (const change of [{ read_only: false }, { real_external_call_executed: true }, { source: 'seed' }, { offset: 1 }, { total: 2 }, { items: [{ ...definition, history_only: false }] }, { items: [{ ...definition, id: Number.MAX_SAFE_INTEGER + 1 }] }, { items: [{ ...definition, created_at: undefined }] }]) {
      body = { ...page([definition]), ...change };
      await rejected(() => readCouponHistoryDefinitions(), '响应不完整');
    }
    for (const invalid of [{ ...page([claim], 32) }, { ...page([{ ...claim, coupon_id: 32 }], 31) }, { ...page([{ ...claim, customer_id: undefined }], 31) }]) {
      body = invalid;
      await rejected(() => readCouponHistoryClaims(31), '响应不完整');
    }
    for (const [code, message] of [[401, '登录状态已失效'], [503, 'HTTP 503']] as const) {
      status = code; body = { code: 'unavailable' };
      await rejected(() => readCouponHistoryDefinitions(), message);
      await rejected(() => readCouponHistoryClaims(31), message);
      await rejected(() => readCouponHistoryRedemptions(31), message);
    }
    const count = calls.length;
    for (const run of [() => readCouponHistoryDefinitions(-1), () => readCouponHistoryDefinitions(2147483648), () => readCouponHistoryDefinitions(0, 101), () => readCouponHistoryClaims(0), () => readCouponHistoryRedemptions(1.5)]) {
      await rejected(run, '无效');
    }
    assert(calls.length === count, 'invalid request reached transport');
  } finally { globalThis.fetch = originalFetch; }
}
