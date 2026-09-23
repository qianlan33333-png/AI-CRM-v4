import { getCampaignDefinitionHistory, readCampaignDefinitionHistory, readCampaignDefinitionSteps } from './campaignDefinitionHistory';

const assert = (value: unknown, message: string): void => { if (!value) throw new Error(message); };
const stamp = '2026-08-28T01:02:03.123456Z';
const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
const definition = { id: 7, source_id: -9, code: 'old-code', display_name: '', intent: '', anchor_mode: '', anchor_date: '', review_status: 'legacy', run_status: 'old', approved_at: null, started_at: null, finished_at: null, paused_at: null, paused_reason: '', created_at: stamp, updated_at: stamp, original_disposition: 'archive', original_reason: 'old' };
const step = { id: 8, source_id: 0, campaign_source_id: -9, segment_source_id: -3, history_definition_id: 7, current_campaign_code: null, source_parent_state: 'history_definition', step_index: -1, day_offset: -2, send_time: '', timezone: '', content_masked: '<legacy>', stop_on_reply: false, skip_recent_days: -4, created_at: stamp, updated_at: stamp, original_disposition: 'quarantine', original_reason: 'unresolved' };

export async function runCampaignDefinitionHistoryAdapterTests(): Promise<void> {
  const previous = globalThis.fetch;
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  let body: unknown = { ...safety, items: [definition], total: 1, limit: 20, offset: 0 };
  let status = 200;
  globalThis.fetch = async (input, init) => { calls.push({ url: String(input), init }); return new Response(JSON.stringify(body), { status }); };
  const rejects = async (read: () => Promise<unknown>): Promise<void> => { try { await read(); } catch { return; } throw new Error('invalid Campaign definition history response accepted'); };
  try {
    const page = await readCampaignDefinitionHistory();
    assert(page.items[0].source_id === -9 && calls[0].url === '/api/admin/campaign-history/definitions?limit=20&offset=0', 'definition list must use generated GET and preserve signed source ID');
    body = { ...safety, item: definition };
    assert((await getCampaignDefinitionHistory(7)).id === 7 && calls[1].url === '/api/admin/campaign-history/definitions/7', 'definition detail must use generated GET');
    body = { ...safety, items: [step], total: 1, limit: 20, offset: 0 };
    assert((await readCampaignDefinitionSteps(undefined)).items[0].campaign_source_id === -9 && calls[2].url === '/api/admin/campaign-history/definition-steps?limit=20&offset=0', 'all definition steps must be visible without a guessed parent filter');
    assert((await readCampaignDefinitionSteps(-9)).items[0].step_index === -1 && calls[3].url === '/api/admin/campaign-history/definition-steps?campaign_source_id=-9&limit=20&offset=0', 'definition steps must filter using the signed source ID');
    for (const patch of [{ source_id: 1.1 }, { original_disposition: 'import' }, { source_key_digest: Array(32).fill(1) }]) { body = { ...safety, item: { ...definition, ...patch } }; await rejects(() => getCampaignDefinitionHistory(7)); }
    body = { ...safety, items: [{ ...step, current_campaign_code: 'must-not-coexist' }], total: 1, limit: 20, offset: 0 }; await rejects(() => readCampaignDefinitionSteps(-9));
    body = { ...safety, items: [{ ...step, campaign_source_id: -8 }], total: 1, limit: 20, offset: 0 }; await rejects(() => readCampaignDefinitionSteps(-9));
    body = { ...safety, items: [{ ...step, id: 9, history_definition_id: null, current_campaign_code: 'old-code', source_parent_state: 'current_definition' }], total: 1, limit: 20, offset: 0 }; assert((await readCampaignDefinitionSteps(-9)).items[0].source_parent_state === 'current_definition', 'current source parent remains a historical observation');
    const before = calls.length; await rejects(() => getCampaignDefinitionHistory(0)); await rejects(() => readCampaignDefinitionSteps(1.1)); assert(calls.length === before, 'invalid IDs must fail before transport');
    status = 503; body = { error: 'raw' }; await rejects(() => readCampaignDefinitionHistory());
    assert(calls.every((call) => call.init?.method === 'GET' && call.init?.credentials === 'include' && call.init?.body === undefined), 'history transport must be same-origin GET only');
  } finally { globalThis.fetch = previous; }
}
