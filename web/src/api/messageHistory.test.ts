import { readMessageHistory, readMessageHistoryDetail } from './messageHistory';
import { ApiError } from './transport';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
export async function runMessageHistoryAdapterTests(): Promise<void> {
  const previous = globalThis.fetch;
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const row = { id: 41, source_id: 7, sequence: -1, customer_id: 9, chat_type: 'private', message_type: 'text', content_masked: ' <b>脱敏正文</b>\n第二行 ', original_send_time: '2024-01-02 03:04:05', send_time_basis: 'civil_unzoned', sent_at: null, created_at: '2026-08-28T00:00:00.123456Z', source_payload_digest: Array(32).fill(3) };
  const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
  const page = { ...safety, items: [row], total: 21, limit: 20, offset: 20 };
  let body: unknown = page, status = 200;
  globalThis.fetch = async (input, init) => { calls.push({ url: String(input), init }); return new Response(JSON.stringify(body), { status }); };
  const rejects = async (fn: () => Promise<unknown>, code?: number): Promise<void> => {
    try { await fn(); } catch (error) { assert(code === undefined || error instanceof ApiError && error.status === code, 'unexpected history rejection'); return; }
    throw new Error('invalid history accepted');
  };
  try {
    const result = await readMessageHistory({ customerID: 9, chatType: 'private', offset: 20 });
    assert(result.items[0].content_masked === row.content_masked && result.items[0].sequence === -1 && result.items[0].sent_at === null && result.items[0].original_send_time === row.original_send_time, 'civil source text, masked content and negative sequence preserved');
    assert(calls[0].url === '/api/admin/message-history?customer_id=9&chat_type=private&limit=20&offset=20', 'generated query uses canonical ID, type and bounded pagination');
    body = { ...safety, item: { ...row, customer_id: null, sequence: null, content_masked: null } };
    const detail = await readMessageHistoryDetail(41);
    assert(detail.customer_id === null && detail.sequence === null && detail.content_masked === null && calls[1].url === '/api/admin/message-history/41', 'detail ID route and NULLs preserved');
    body = { ...safety, item: { ...row, sequence: 0, content_masked: '', send_time_basis: 'explicit_offset', original_send_time: '2024-01-02T03:04:05+08:00', sent_at: '2024-01-01T19:04:05Z' } };
    assert((await readMessageHistoryDetail(41)).content_masked === '', 'empty body is not fabricated as missing');
    for (const patch of [{ id: 42 }, { sent_at: '2024-01-02T03:04:05Z' }, { send_time_basis: 'unknown' }, { source_payload_digest: [1] }, { raw_payload: 'must-not-render' }, { sender: 'must-not-render' }, { customer_id: 0 }]) {
      body = { ...safety, item: { ...row, ...patch } }; await rejects(() => readMessageHistoryDetail(41));
    }
    for (const patch of [{ source: 'current' }, { read_only: false }, { real_external_call_executed: true }, { offset: 0 }, { limit: 100 }, { items: [{ ...row, customer_id: 8 }] }, { items: [{ ...row, chat_type: 'group' }] }]) {
      body = { ...page, ...patch }; await rejects(() => readMessageHistory({ customerID: 9, chatType: 'private', offset: 20 }));
    }
    const count = calls.length;
    await rejects(() => readMessageHistoryDetail(0));
    await rejects(() => readMessageHistory({ customerID: Number.MAX_SAFE_INTEGER + 1 }));
    await rejects(() => readMessageHistory({ offset: -1 }));
    assert(calls.length === count, 'invalid ID and pagination rejected before request');
    for (const code of [401, 403, 503]) { status = code; await rejects(() => readMessageHistory(), code); }
    assert(calls.every((call) => call.init?.method === 'GET' && call.init.credentials === 'include' && call.init.body === undefined), 'history reads only existing same-origin GETs, never sync/send/fallback');
  } finally { globalThis.fetch = previous; }
}
