import { readMemberUsageHistory, readMemberUsageHistoryDetail, readMemberViewHistory, readMemberViewHistoryDetail } from './memberGridHistory';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
const at = '2026-08-28T00:00:00.000000Z';
const digest = Array.from({ length: 32 }, (_, index) => index);
const view = { id: 31, source_key_digest: digest, source_view_id: 8, source_service_product_id: 9, product_id: 7, name: '', position: -1, is_default: false, schema_version: -1, config_digest: digest, version: -2, created_at: at, updated_at: at, source_payload_digest: digest };
const usage = { id: 41, source_key_digest: digest, customer_id: 7, formally_logged_in: false, has_token_usage: false, learning_plan_id: '', learning_plan_current: null, learning_plan_total: 0, open_count_7d: 0, last_open_at: null, refreshed_at: at, source_payload_digest: digest, recovery_entry_digest: digest };
const page = (items: unknown[]) => ({ source: 'v1_history', read_only: true, real_external_call_executed: false, items, total: items.length, limit: 20, offset: 0 });

export async function runMemberGridHistoryAdapterTests(): Promise<void> {
  const originalFetch = globalThis.fetch;
  const calls: { url: URL; init: RequestInit }[] = [];
  let body: unknown = page([view]);
  let status = 200;
  globalThis.fetch = async (input, init = {}) => {
    calls.push({ url: new URL(String(input), 'http://localhost'), init });
    return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
  };
  const rejected = async (run: () => Promise<unknown>, message: string): Promise<void> => {
    try { await run(); } catch (error) { assert(error instanceof Error && error.message.includes(message), 'unexpected Member Grid history error'); return; }
    throw new Error('Member Grid history request unexpectedly succeeded');
  };
  try {
    assert(JSON.stringify((await readMemberViewHistory(0, 20, 7)).items[0]) === JSON.stringify(view), 'view false, empty text, nullable facts or signed values changed');
    body = { source: 'v1_history', read_only: true, real_external_call_executed: false, item: view };
    assert(JSON.stringify(await readMemberViewHistoryDetail(31, 7)) === JSON.stringify(view), 'view detail changed');
    body = page([usage]);
    assert(JSON.stringify((await readMemberUsageHistory(0, 20, 7)).items[0]) === JSON.stringify(usage), 'usage false, null or empty facts changed');
    body = { source: 'v1_history', read_only: true, real_external_call_executed: false, item: usage };
    assert(JSON.stringify(await readMemberUsageHistoryDetail(41, 7)) === JSON.stringify(usage), 'usage detail changed');
    assert(calls.map((call) => call.url.pathname).join(',') === '/api/admin/member-grid-history/views,/api/admin/member-grid-history/views/31,/api/admin/member-grid-history/usage,/api/admin/member-grid-history/usage/41', 'not using four generated Member Grid history GET routes');
    assert(calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && !init.body), 'Member Grid history transport must be authenticated read-only GET');
    assert(calls[0]?.url.search === '?offset=0&limit=20&product_id=7' && calls[2]?.url.search === '?offset=0&limit=20&customer_id=7', 'Member Grid history filters were not sent by generated client');
    body = { ...page([view]), items: [{ ...view, product_id: 8 }] };
    await rejected(() => readMemberViewHistory(0, 20, 7), '响应无效');
    body = { source: 'v1_history', read_only: true, real_external_call_executed: false, item: { ...usage, id: 42 } };
    await rejected(() => readMemberUsageHistoryDetail(41, 7), '响应无效');
    body = page([{ ...usage, customer_id: 8 }]);
    await rejected(() => readMemberUsageHistory(0, 20, 7), '响应无效');
    body = { ...page([view]), extra_identity: 'raw' };
    await rejected(() => readMemberViewHistory(), '响应无效');
    body = { ...page([view]), items: [{ ...view, config_digest: digest.slice(1) }] };
    await rejected(() => readMemberViewHistory(), '响应无效');
    body = { ...page([view]), items: [{ ...view, source_key_digest: [-1, ...digest.slice(1)] }] };
    await rejected(() => readMemberViewHistory(), '响应无效');
    body = page([{ ...usage, open_count_7d: -1 }]);
    await rejected(() => readMemberUsageHistory(), '响应无效');
    body = page([{ ...usage, learning_plan_total: -1 }]);
    await rejected(() => readMemberUsageHistory(), '响应无效');
    body = page([{ ...view, updated_at: '2025-01-01T00:00:00Z' }]);
    await rejected(() => readMemberViewHistory(), '响应无效');
    status = 503; body = { code: 'unavailable' };
    await rejected(() => readMemberViewHistory(), 'HTTP 503');
    await rejected(() => readMemberUsageHistory(), 'HTTP 503');
    const count = calls.length;
    for (const run of [() => readMemberViewHistory(-1), () => readMemberUsageHistory(0, 101), () => readMemberViewHistoryDetail(0), () => readMemberUsageHistoryDetail(1.5)]) await rejected(run, '无效');
    assert(calls.length === count, 'invalid Member Grid history request reached transport');
  } finally { globalThis.fetch = originalFetch; }
}
