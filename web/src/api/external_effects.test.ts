import {
  cancelExternalEffectRuntimeDto,
  cancelPushCenterJobDto,
  getPushCenterJobDetailDto,
  readExternalEffectsWorkspaceDto,
  retryPushCenterJobDto,
} from './external_effects';

function assert(ok: unknown, message: string): asserts ok { if (!ok) throw new Error(message); }

const local = { local_fact_only: true, real_external_call_executed: false, delivery_proven: false, delivery_semantics: 'local_state_not_delivery_proof' };
const pushJob = { job_id: 18, task_id: 5, customer_id: 7, status: 'outcome_unknown', attempt_count: 1, failure_present: true, failure_class: 'outcome_unknown', provider_receipt_present: false, queue_job: { river_job_id: 9, generation: 1, kind: 'outbound_enqueue_one' }, created_at: '2026-08-27T00:00:00Z', status_updated_at: '2026-08-27T00:01:00Z', ...local };
const pushEnvelope = { ok: true, fallback_used: false, source_status: 'v2_outbound_service', ...local };
const receipt = { receipt_id: 3, task_id: 5, operation: 'manual_retry', state: 'completed', generation: 2, river_job_id: 10, job_kind: 'outbound_enqueue_one', event_id: 11, task_status: 'pending', completed_at: '2026-08-27T00:02:00Z', provider_receipt_present: false, ...local };

export async function runExternalEffectsAdapterTests(): Promise<void> {
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const savedFetch = globalThis.fetch;
  globalThis.fetch = async (input, init) => {
    const url = String(input);
    calls.push({ url, init });
    if (url.includes('/external-effects/diagnostics')) return new Response(JSON.stringify({ accepted: 1, queued: 2, attempted: 1, outcome_unknown: 1, retryable_failed: 0 }), { status: 200 });
    if (url.includes('/external-effects/jobs')) return new Response(JSON.stringify({ ok: true, items: [{ id: 'eej_v1_abcdefghijklmnopqrstuv', status: 'outcome_unknown', classification: 'manual_review', attempt_count: 1, created_at: '2026-08-27T00:00:00Z', status_updated_at: '2026-08-27T00:01:00Z' }], next_cursor: null, page_size: 50, applied_filters: { status: null, classification: null }, provider_execution_eligible: false, ...local }), { status: 200 });
    if (url.includes('/external-effects/18/cancel')) return new Response(JSON.stringify({ id: '18', owner: 'campaign', kind: 'campaign_dispatch', state: 'cancelled', attempt_count: 0, generation: 1, updated_at: '2026-08-27T00:00:00Z' }), { status: 200 });
    if (url.includes('/external-effects')) return new Response(JSON.stringify({ items: [{ id: '18', owner: 'campaign', kind: 'campaign_dispatch', state: 'accepted', attempt_count: 0, generation: 1, updated_at: '2026-08-27T00:00:00Z' }] }), { status: 200 });
    if (url.includes('/push-center/sections')) return new Response(JSON.stringify({ ok: true, sections: [{ key: 'order', label: '订单', count: 2 }], status_definitions: [], filters: {}, route_owner: 'ai_crm_next' }), { status: 200 });
    if (url.includes('/push-center/stats')) return new Response(JSON.stringify({ ok: true, counts: { total: 2, pending: 1, running: 0, succeeded: 0, sent: 1, failed: 0, shadow_warning: 0, by_effective_status: {}, by_status: {}, by_section: {} }, sections: [], status_definitions: [], filters: {}, route_owner: 'ai_crm_next', real_external_call_executed: false, runtime_queue: {}, capability_owner: 'ai_crm_next/platform_foundation/push_center' }), { status: 200 });
    if (url.endsWith('/reconciliation')) return new Response(JSON.stringify({ ...pushEnvelope, job: pushJob, attempts: [{ attempt_id: 1, history_id: 2, generation: 1, river_job_id: 9, attempt: 1, max_attempts: 3, state: 'outcome_unknown', failure_present: true, failure_class: 'outcome_unknown', provider_receipt_present: false, dispatch_started_at: '2026-08-27T00:00:00Z', ...local }], control_receipts: [receipt] }), { status: 200 });
    if (url.endsWith('/18')) return new Response(JSON.stringify({ ...pushEnvelope, job: pushJob }), { status: 200 });
    if (url.endsWith('/18/cancel')) return new Response(JSON.stringify({ ok: true, fallback_used: false, source_status: 'v2_outbound_cancel_service', control_receipt: { ...receipt, operation: 'cancel', task_status: 'cancelled' }, ...local }), { status: 202 });
    if (url.endsWith('/18/retry')) return new Response(JSON.stringify({ ok: true, fallback_used: false, source_status: 'v2_outbound_manual_retry_service', control_receipt: receipt, ...local }), { status: 202 });
    if (url.includes('/push-center/jobs')) return new Response(JSON.stringify({ ...pushEnvelope, jobs: [pushJob], items: [pushJob], count: 1, has_more: false, limit: 50, offset: 0 }), { status: 200 });
    return new Response(JSON.stringify({ code: 'unexpected' }), { status: 500 });
  };
  try {
    const workspace = await readExternalEffectsWorkspaceDto();
    assert(workspace.runtime.length === 1 && workspace.jobs[0].status === 'outcome_unknown', 'External Effects adapter reads only generated local projections');
    assert(workspace.push.counts?.sent === 1 && workspace.push.jobs[0].status === 'outcome_unknown', 'Push Center adapter keeps sent as a local state and preserves outcome_unknown');
    assert(calls.some((call) => call.url === '/api/admin/external-effects/diagnostics' && call.init?.method === 'GET') && calls.some((call) => call.url.includes('/push-center/jobs?limit=50&offset=0') && call.init?.method === 'GET'), 'External Effects uses existing generated diagnostic and Push Center reads');
    assert(!calls.some((call) => call.url.includes('run-due')), 'Missing run-due contract is never guessed or requested');
    const detail = await getPushCenterJobDetailDto(18);
    assert(detail.job.jobID === 18 && detail.attempts[0].state === 'outcome_unknown' && detail.receipts[0].taskStatus === 'pending', 'Push Center detail and reconciliation remain job-scoped local facts');
    await cancelPushCenterJobDto(18);
    await retryPushCenterJobDto(18);
    await cancelExternalEffectRuntimeDto('18');
    const cancelCall = calls.find((call) => call.url.endsWith('/push-center/jobs/18/cancel'));
    const retryCall = calls.find((call) => call.url.endsWith('/push-center/jobs/18/retry'));
    const effectCancelCall = calls.find((call) => call.url.endsWith('/external-effects/18/cancel'));
    assert(cancelCall?.init?.method === 'POST' && retryCall?.init?.method === 'POST' && effectCancelCall?.init?.method === 'POST' && Boolean(new Headers(cancelCall?.init?.headers).get('Idempotency-Key')), 'Local control mutations require generated POST and an idempotency key');
  } finally { globalThis.fetch = savedFetch; }

  globalThis.fetch = async (input) => {
    const url = String(input);
    if (url.includes('/external-effects/diagnostics')) return new Response(JSON.stringify({ accepted: 0, queued: 0, attempted: 0, outcome_unknown: 0, retryable_failed: 0 }), { status: 200 });
    if (url.includes('/external-effects/jobs')) return new Response(JSON.stringify({ ok: true, items: [], next_cursor: null, page_size: 50, applied_filters: {}, provider_execution_eligible: false, ...local, real_external_call_executed: true }), { status: 200 });
    if (url.includes('/external-effects')) return new Response(JSON.stringify({ items: [] }), { status: 200 });
    if (url.includes('/push-center/sections')) return new Response(JSON.stringify({ ok: true, sections: [], status_definitions: [], filters: {}, route_owner: 'ai_crm_next' }), { status: 200 });
    if (url.includes('/push-center/stats')) return new Response(JSON.stringify({ ok: true, counts: { total: 0, pending: 0, running: 0, sent: 0, failed: 0 }, sections: [], status_definitions: [], filters: {}, route_owner: 'ai_crm_next', real_external_call_executed: false }), { status: 200 });
    return new Response(JSON.stringify({ ...pushEnvelope, items: [], jobs: [] }), { status: 200 });
  };
  try { await readExternalEffectsWorkspaceDto(); assert(false, 'External effect external-call claim must fail closed'); }
  catch (error) { assert(error instanceof Error && error.message.includes('本地事实边界'), 'External effect external-call claim fails closed'); }
  finally { globalThis.fetch = savedFetch; }
}
