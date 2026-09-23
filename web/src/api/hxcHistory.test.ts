import { getHxcHistory, readHxcHistory } from './hxcHistory';
import { ApiError } from './transport';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
const digest = Array(32).fill(1);
const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
const stamp = '2026-08-28T01:02:03.123456Z';
const snapshot = { id: 4, source_id: -7, source_key_digest: digest, source_payload_digest: digest, customer_id: null, observation: 'observed_snapshot', observed_at: stamp, in_lead_pool: false, in_people: false, in_questionnaire: false, class_term_no: null, class_term_label: '', crm_hxc_state: '', crm_created_at: '2024-02-29', last_questionnaire_at: null, hxc_member_hit: false, hxc_user_hit: false, funnel_state: '', hxc_member_status: '', hxc_registered_at: null, hxc_last_login_at: null, membership_type: '', membership_status: '', membership_end_at: null, membership_days_left: null, consultation_used: null, consultation_limit: null, conversation_chat: 0, conversation_consult: 0, conversation_lesson: 0, messages_user: 0, messages_ai: 0, consult_completed: 0, last_message_at: null, subscription_tier: '', subscription_expires: null, subscription_quota: null, subscription_used: null, subscription_period_start: null };
export async function runHxcHistoryAdapterTests(): Promise<void> {
  const previous = globalThis.fetch; const calls: Array<{ url: string; init?: RequestInit }> = []; let body: unknown = { ...safety, items: [snapshot], total: 1, limit: 20, offset: 0 }; let status = 200;
  globalThis.fetch = async (input, init) => { calls.push({ url: String(input), init }); return new Response(JSON.stringify(body), { status }); };
  const rejects = async (fn: () => Promise<unknown>): Promise<void> => { try { await fn(); } catch { return; } throw new Error('invalid HXC history accepted'); };
  try {
    const page = await readHxcHistory('snapshot');
    assert((page.items[0] as typeof snapshot).source_id === -7 && calls[0].url === '/api/admin/hxc-history/snapshots?limit=20&offset=0', 'snapshot uses generated GET and preserves source ID');
    body = { ...safety, item: { ...snapshot, subscription_period_start: null } };
    assert((await getHxcHistory('snapshot', 4)).id === 4 && calls[1].url === '/api/admin/hxc-history/snapshots/4', 'detail uses generated GET and nullable date');
    for (const patch of [{ observation: '' }, { observation: 'current' }, { crm_created_at: '2024-02-30' }, { source_payload_digest: [1] }, { raw_payload: 'no' }, { customer_id: 0 }]) { body = { ...safety, item: { ...snapshot, ...patch } }; await rejects(() => getHxcHistory('snapshot', 4)); }
    body = { ...safety, items: [{ ...snapshot, subscription_period_start: '' }], total: 1, limit: 20, offset: 0 }; await rejects(() => readHxcHistory('snapshot'));
    body = { ...safety, item: { ...snapshot, source_payload_digest: Array(32).fill(0) } }; await rejects(() => getHxcHistory('snapshot', 4));
    const before = calls.length; await rejects(() => getHxcHistory('batch', 0)); await rejects(() => readHxcHistory('activation', -1)); assert(calls.length === before, 'invalid ID or pagination fails before transport');
    await rejects(() => readHxcHistory('meta', 0, 20, 9)); await rejects(() => readHxcHistory('lead', 0, 20, undefined, 'public/user_ops_activation_status_source')); assert(calls.length === before, 'filters for the wrong history kind fail before transport');
    body = { ...safety, items: [{ ...snapshot, customer_id: 8 }], total: 1, limit: 20, offset: 0 }; await rejects(() => readHxcHistory('snapshot', 0, 20, 9));
    const activation = { id: 5, source_id: 0, source_key_digest: digest, source_payload_digest: digest, source_table: 'public/user_ops_huangxiaocan_activation_source', original_state: '', is_active: false, legacy_import_batch_ref: null, created_at: stamp, updated_at: stamp };
    body = { ...safety, items: [activation], total: 1, limit: 20, offset: 0 }; await rejects(() => readHxcHistory('activation', 0, 20, undefined, 'public/user_ops_activation_status_source'));
    const sender = { id: 6, source_id: -8, priority: -2, original_is_active: false, created_at: stamp, updated_at: stamp };
    body = { ...safety, items: [sender], total: 1, limit: 20, offset: 0 };
    assert(((await readHxcHistory('sender_config')).items[0] as typeof sender).source_id === -8 && calls[calls.length - 1].url === '/api/admin/hxc-history/sender-configs?limit=20&offset=0', 'sender config preserves signed source ID through generated GET');
    body = { ...safety, item: sender };
    assert((await getHxcHistory('sender_config', 6)).id === 6 && calls[calls.length - 1].url === '/api/admin/hxc-history/sender-configs/6', 'sender config detail uses generated GET');
    const record = { id: 7, source_id: 0, task_type: '', original_status: '', selected_count: -1, eligible_count: 0, sent_count: 1, skipped_count: -2, planned_count: 3, queued_count: 4, dispatching_count: 5, succeeded_count: 6, failed_count: 7, blocked_count: 8, cancelled_count: 9, image_count: 10, include_do_not_disturb: false, target_source: '', target_source_id: -9, created_at: stamp, last_status_sync_at: null, last_refreshed_at: stamp };
    body = { ...safety, items: [record], total: 1, limit: 20, offset: 0 };
    assert(((await readHxcHistory('send_record')).items[0] as typeof record).source_id === 0 && calls[calls.length - 1].url === '/api/admin/hxc-history/send-records?limit=20&offset=0', 'send record preserves signed IDs and counts');
    body = { ...safety, item: record };
    assert((await getHxcHistory('send_record', 7)).id === 7 && calls[calls.length - 1].url === '/api/admin/hxc-history/send-records/7', 'send record detail uses generated GET');
    for (const patch of [{ target_source_id: 1.5 }, { last_status_sync_at: '' }, { selected_count: 1.5 }, { source_payload_digest: digest }]) { body = { ...safety, item: { ...record, ...patch } }; await rejects(() => getHxcHistory('send_record', 7)); }
    const usage = { id: 8, generation: -9, is_member: false, is_registered: false, has_real_usage: false, registered_at: null, first_used_at: null, last_used_at: null, member_since: null, membership_expires_at: null, updated_at: null, membership_tier: '', membership_status: '', membership_source: '', registration_source: '', usage_source: '', projected_at: stamp };
    body = { ...safety, items: [usage], total: 1, limit: 20, offset: 0 };
    assert((await readHxcHistory('member_usage', 0, 20, undefined, undefined, -9)).items[0].id === 8 && calls[calls.length - 1].url === '/api/admin/hxc-history/member-usage?limit=20&offset=0&generation=-9', 'member usage uses generated GET with signed generation filter');
    body = { ...safety, item: usage };
    assert((await getHxcHistory('member_usage', 8)).id === 8 && calls[calls.length - 1].url === '/api/admin/hxc-history/member-usage/8', 'member usage detail uses generated GET');
    for (const patch of [{ generation: 1.5 }, { projected_at: '' }, { registered_at: '' }, { union_id: 'private' }, { source_key_digest: digest }]) { body = { ...safety, item: { ...usage, ...patch } }; await rejects(() => getHxcHistory('member_usage', 8)); }
    body = { ...safety, items: [{ ...usage, generation: -8 }], total: 1, limit: 20, offset: 0 }; await rejects(() => readHxcHistory('member_usage', 0, 20, undefined, undefined, -9));
    const usageCalls = calls.length; await rejects(() => readHxcHistory('member_usage', 0, 20, undefined, undefined, 1.5)); await rejects(() => readHxcHistory('meta', 0, 20, undefined, undefined, 1)); assert(calls.length === usageCalls, 'invalid generation fails before transport');
    const chat = { id: 9, source_id: -9, queue_source_id: -3, member_source_id: null, original_status: '', send_channel: '', send_record_source_id: 0, created_at: stamp, updated_at: stamp, finished_at_source: 'not-a-timestamp' };
    body = { ...safety, items: [chat], total: 1, limit: 20, offset: 0 };
    assert(((await readHxcHistory('chat_job')).items[0] as typeof chat).source_id === -9 && calls[calls.length - 1].url === '/api/admin/hxc-history/chat-jobs?limit=20&offset=0', 'chat job uses generated GET and preserves signed source IDs');
    body = { ...safety, item: chat };
    assert((await getHxcHistory('chat_job', 9)).id === 9 && calls[calls.length - 1].url === '/api/admin/hxc-history/chat-jobs/9', 'chat job detail uses generated GET');
    for (const patch of [{ queue_source_id: 1.5 }, { member_source_id: '1' }, { created_at: '' }, { finished_at_source: null }, { phone: 'private' }, { request_payload: '{}' }, { source_key_digest: digest }]) { body = { ...safety, item: { ...chat, ...patch } }; await rejects(() => getHxcHistory('chat_job', 9)); }
    body = { ...safety, items: [], total: 0, limit: 20, offset: 0 };
    assert((await readHxcHistory('chat_job')).items.length === 0, 'chat job accepts an empty page');
    status = 503; await rejects(() => readHxcHistory('meta')); assert(calls[calls.length - 1]?.init?.method === 'GET' && calls.every((call) => call.init?.credentials === 'include' && call.init?.body === undefined), 'history is same-origin GET only');
  } finally { globalThis.fetch = previous; }
}
