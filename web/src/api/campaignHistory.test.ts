import {
  getCampaignHistoryPlanItem,
  getCampaignHistorySegmentItem,
  readCampaignHistoryMembers,
  readCampaignHistoryMessages,
  readCampaignHistoryPlans,
  readCampaignHistoryRecipients,
  readCampaignHistorySegments,
} from './campaignHistory';
import { ApiError } from './transport';

function assert(value: unknown, message: string): asserts value { if (!value) throw new Error(message); }
const date = '2026-08-28T01:02:03.123456Z';
const digest = Array.from({ length: 32 }, (_, index) => index);
const segment = { id: 11, source_id: 101, campaign_source_id: 1001, segment_source_id: -9, source_parent_state: 'missing_campaign', code: ' code ', priority: -3, label: '<legacy>', created_at: date, source_payload_digest: digest };
const member = { id: 21, source_id: 102, campaign_source_id: -1, campaign_segment_source_id: -2, segment_source_id: -3, member_source_id: -4, segment_history_id: 11, customer_id: null, joined_at: date, anchor_date: '', current_step_index: -5, next_due_at: null, original_status: '', stop_reason: '', last_step_sent_at: date, retry_count: -6, created_at: date, updated_at: date, source_payload_digest: digest };
const plan = { id: 31, source_id: 103, source_plan_id: ' plan ', campaign_source_id: -1, segment_source_id: null, display_name: '<plan>', intent: '', content_strategy: 'legacy', content_template_masked: 'masked', max_recipients: -1, candidate_count: -2, skipped_count: -3, requires_manual_copy: true, original_status: '', original_review_status: '', original_run_status: '', committed_at: null, expires_at: date, created_at: date, updated_at: date, runtime_digest: digest, source_payload_digest: digest };
const recipient = { id: 41, source_id: 104, plan_history_id: 31, customer_id: 7, display_name: '', planned_message_count: -1, original_approval_status: '', original_send_status: '', approved_at: null, rejected_at: date, created_at: date, updated_at: date, source_payload_digest: digest };
const message = { id: 51, source_id: 105, plan_history_id: 31, recipient_history_id: 41, customer_id: null, sequence_index: -1, day_offset: -2, original_send_time: 'old civil', content_masked: '<old>', original_status: '', sent_at: null, created_at: date, updated_at: date, content_payload_digest: digest, attachments_digest: digest, source_payload_digest: digest };
const page = (items: unknown[], limit: number, offset: number, parent: Record<string, unknown> = {}) => ({ source: 'v1_history', read_only: true, real_external_call_executed: false, items, total: offset + items.length, limit, offset, ...parent });

export async function runCampaignHistoryAdapterTests(): Promise<void> {
  const savedFetch = globalThis.fetch;
  const calls: Array<{ url: URL; init?: RequestInit }> = [];
  let patch: Record<string, unknown> = {};
  let status = 200;
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ url, init });
    const limit = Number(url.searchParams.get('limit') || 20);
    const offset = Number(url.searchParams.get('offset') || 0);
    let body: unknown;
    if (url.pathname === '/api/admin/campaign-history/segments') body = page([segment], limit, offset);
    else if (url.pathname === '/api/admin/campaign-history/segments/11') body = { source: 'v1_history', read_only: true, real_external_call_executed: false, item: segment };
    else if (url.pathname === '/api/admin/campaign-history/members') body = page([member], limit, offset);
    else if (url.pathname === '/api/admin/campaign-history/broadcast-plans') body = page([plan], limit, offset);
    else if (url.pathname === '/api/admin/campaign-history/broadcast-plans/31') body = { source: 'v1_history', read_only: true, real_external_call_executed: false, item: plan };
    else if (url.pathname === '/api/admin/campaign-history/broadcast-plans/31/recipients') body = page([recipient], limit, offset, { plan_history_id: 31 });
    else if (url.pathname === '/api/admin/campaign-history/broadcast-recipients/41/messages') body = page([message], limit, offset, { recipient_history_id: 41 });
    else throw new Error(`unexpected URL ${url.pathname}`);
    return new Response(JSON.stringify({ ...(body as Record<string, unknown>), ...patch }), { status, headers: { 'Content-Type': 'application/json' } });
  };
  const rejects = async (call: () => Promise<unknown>, match: (error: unknown) => boolean): Promise<void> => {
    try { await call(); } catch (error) { assert(match(error), `unexpected error ${String(error)}`); return; }
    throw new Error('request unexpectedly succeeded');
  };
  try {
    assert((await readCampaignHistorySegments()).items[0]?.source_parent_state === 'missing_campaign', 'preserve orphan source parent state');
    assert((await getCampaignHistorySegmentItem(11)).label === '<legacy>', 'segment detail changed source text');
    assert((await readCampaignHistoryMembers(11)).items[0]?.current_step_index === -5, 'member source counters changed');
    assert((await readCampaignHistoryPlans()).items[0]?.candidate_count === -2, 'plan counters changed');
    assert((await getCampaignHistoryPlanItem(31)).source_plan_id === ' plan ', 'plan detail changed opaque source ID');
    assert((await readCampaignHistoryRecipients(31)).items[0]?.customer_id === 7, 'verified customer link changed');
    assert((await readCampaignHistoryMessages(41)).items[0]?.original_send_time === 'old civil', 'message civil time was parsed or changed');
    assert(calls.map((call) => call.url.pathname).join('|') === [
      '/api/admin/campaign-history/segments', '/api/admin/campaign-history/segments/11', '/api/admin/campaign-history/members',
      '/api/admin/campaign-history/broadcast-plans', '/api/admin/campaign-history/broadcast-plans/31',
      '/api/admin/campaign-history/broadcast-plans/31/recipients', '/api/admin/campaign-history/broadcast-recipients/41/messages',
    ].join('|'), 'did not use all seven generated read-only GET operations');
    assert(calls.every((call) => call.init?.method === 'GET' && call.init.credentials === 'include' && call.init.body === undefined), 'history adapter issued non-read-only transport');
    assert(calls[2]?.url.search === '?limit=20&offset=0&segment_history_id=11', 'member parent filter missing');
    assert(calls[5]?.url.search === '?limit=20&offset=0' && calls[6]?.url.search === '?limit=20&offset=0', 'child pagination missing');

    patch = { source: 'current' };
    await rejects(() => readCampaignHistorySegments(), (error) => error instanceof Error && error.message.includes('响应不完整'));
    patch = { items: [{ ...message, recipient_history_id: 99 }], recipient_history_id: 41 };
    await rejects(() => readCampaignHistoryMessages(41), (error) => error instanceof Error && error.message.includes('响应不完整'));
    patch = { item: { ...segment, id: 99 } };
    await rejects(() => getCampaignHistorySegmentItem(11), (error) => error instanceof Error && error.message.includes('响应不完整'));
    patch = { items: [{ ...plan, runtime_digest: digest.slice(1) }] };
    await rejects(() => readCampaignHistoryPlans(), (error) => error instanceof Error && error.message.includes('响应不完整'));
    patch = {};
    const before = calls.length;
    for (const call of [() => getCampaignHistorySegmentItem(0), () => readCampaignHistoryMembers(-1), () => readCampaignHistoryPlans(-1), () => readCampaignHistoryRecipients(1.5), () => readCampaignHistoryMessages(1, 0, 101)]) {
      await rejects(call, (error) => error instanceof Error && error.message.includes('无效'));
    }
    assert(calls.length === before, 'invalid input reached generated transport');
    status = 503;
    await rejects(() => readCampaignHistorySegments(), (error) => error instanceof ApiError && error.status === 503);
  } finally {
    globalThis.fetch = savedFetch;
  }
}
