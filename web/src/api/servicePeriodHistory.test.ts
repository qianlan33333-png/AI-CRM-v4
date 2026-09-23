import { readServicePeriodHistoryDefinitions, readServicePeriodHistoryEntitlements, readServicePeriodHistoryEvents } from './servicePeriodHistory';
import { ApiError } from './transport';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
const at = '2026-08-27T10:11:12.123456Z';
const definition = { id: 17, source_definition_id: 2, product_id: 91, membership_config_id: '', membership_config_name: ' 原名 ', duration_days: -7, deleted: true, created_at: at, updated_at: at, product_code: 'P-91', product_name: '周期历史', price_minor: 9900, currency: 'CNY' };
const entitlement = { id: 23, source_entitlement_id: 8, definition_id: 17, customer_id: null, membership_config_id: '', status: 'expired', start_at: at, end_at: '2025-01-01T00:00:00Z', last_order_id: null, last_out_trade_no: ' trade ', renewal_count: -2, created_at: at, updated_at: at };
const event = { id: 29, source_event_id: 12, definition_id: 17, entitlement_id: null, customer_id: null, order_id: null, event_id: 'event-12', event_type: 'grant_failed_missing_unionid', duration_days: -5, out_trade_no: '', before_start_at: null, before_end_at: null, after_start_at: null, after_end_at: null, created_at: at };
const envelope = (item: unknown, offset = 0, limit = 1): Record<string, unknown> => ({ source: 'v1_history', read_only: true, real_external_call_executed: false, definition_id: 17, items: [item], total: offset + 1, limit, offset });

export async function runServicePeriodHistoryAdapterTests(): Promise<void> {
  const previous = globalThis.fetch;
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  let body = envelope(definition, 3);
  let status = 200;
  globalThis.fetch = async (input, init) => {
    calls.push({ url: String(input), init });
    return new Response(JSON.stringify(body), { status });
  };
  const rejects = async (read: () => Promise<unknown>, check: (error: unknown) => boolean = (error) => error instanceof Error): Promise<void> => {
    try { await read(); } catch (error) { assert(check(error), 'history rejected with unexpected error'); return; }
    throw new Error('invalid history response accepted');
  };
  try {
    const definitions = await readServicePeriodHistoryDefinitions(3, 1);
    assert(definitions.items[0].product_id === 91 && definitions.items[0].duration_days === -7 && definitions.items[0].membership_config_name === ' 原名 ', 'definition preserves product identity and raw values');
    body = envelope(entitlement, 4);
    const entitlements = await readServicePeriodHistoryEntitlements(17, 4, 1);
    assert(entitlements.items[0].customer_id === null && entitlements.items[0].last_order_id === null && entitlements.items[0].status === 'expired' && entitlements.items[0].renewal_count === -2 && entitlements.items[0].last_out_trade_no === ' trade ', 'historical state, NULL and negative counts are not rewritten');
    body = envelope(event, 5);
    const events = await readServicePeriodHistoryEvents(17, 5, 1);
    assert(events.items[0].entitlement_id === null && events.items[0].after_end_at === null && events.items[0].duration_days === -5 && events.items[0].event_type === 'grant_failed_missing_unionid', 'failed grant remains a nullable historical fact');
    assert(calls.map((call) => call.url).join('|') === '/api/admin/service-period-history?limit=1&offset=3|/api/admin/service-period-history/17/entitlements?limit=1&offset=4|/api/admin/service-period-history/17/events?limit=1&offset=5', 'all three generated GET paths and pagination must be exact');
    assert(calls.every((call) => call.init?.method === 'GET' && call.init.credentials === 'include' && call.init.body === undefined), 'history only reads using same-origin transport');

    for (const patch of [{ source: 'native' }, { read_only: false }, { real_external_call_executed: true }, { real_external_call_executed: undefined }, { offset: 1 }, { limit: 2 }, { items: null }, { total: 0 }]) {
      body = { ...envelope(definition), ...patch };
      await rejects(() => readServicePeriodHistoryDefinitions(0, 1));
    }
    body = { ...envelope(entitlement), definition_id: 18 };
    await rejects(() => readServicePeriodHistoryEntitlements(17, 0, 1));
    body = envelope({ ...entitlement, definition_id: 18 });
    await rejects(() => readServicePeriodHistoryEntitlements(17, 0, 1));
    body = envelope({ ...event, customer_id: 0 });
    await rejects(() => readServicePeriodHistoryEvents(17, 0, 1));
    body = envelope({ ...event, after_end_at: undefined });
    await rejects(() => readServicePeriodHistoryEvents(17, 0, 1));
    body = envelope({ ...definition, product_id: '91' });
    await rejects(() => readServicePeriodHistoryDefinitions(0, 1));
    body = { ...envelope(definition), items: [definition, definition], total: 2, limit: 2 };
    await rejects(() => readServicePeriodHistoryDefinitions(0, 2));
    body = { ...envelope(definition), items: [], total: 0 };
    assert((await readServicePeriodHistoryDefinitions(0, 1)).items.length === 0, 'real empty history remains empty');

    const before = calls.length;
    await rejects(() => readServicePeriodHistoryDefinitions(-1));
    await rejects(() => readServicePeriodHistoryDefinitions(0, 101));
    await rejects(() => readServicePeriodHistoryEntitlements(0));
    await rejects(() => readServicePeriodHistoryEvents(Number.MAX_SAFE_INTEGER + 1));
    assert(calls.length === before, 'invalid query must not fetch');
    for (const failure of [401, 403, 503]) {
      status = failure;
      await rejects(() => readServicePeriodHistoryDefinitions(), (error) => error instanceof ApiError && error.status === failure);
      await rejects(() => readServicePeriodHistoryEntitlements(17), (error) => error instanceof ApiError && error.status === failure);
      await rejects(() => readServicePeriodHistoryEvents(17), (error) => error instanceof ApiError && error.status === failure);
    }
    globalThis.fetch = async () => { throw new Error('network unavailable'); };
    await rejects(() => readServicePeriodHistoryDefinitions());
  } finally { globalThis.fetch = previous; }
}
