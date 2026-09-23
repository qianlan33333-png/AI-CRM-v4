import {
  readAutomationHistoryAgent, readAutomationHistoryAgents, readAutomationHistoryConfig, readAutomationHistoryConfigs,
  readAutomationHistoryPrompt, readAutomationHistoryPrompts, readAutomationHistorySOP, readAutomationHistorySOPs,
} from './automationHistory';
import { ApiError } from './transport';

function assert(value: unknown, message: string): asserts value { if (!value) throw new Error(message); }
async function rejects(call: () => Promise<unknown>, check: (error: unknown) => boolean = (error) => error instanceof Error): Promise<void> {
  try { await call(); } catch (error) { assert(check(error), 'unexpected rejection'); return; }
  throw new Error('expected failure without fallback');
}

export async function runAutomationHistoryAdapterTests(): Promise<void> {
  const savedFetch = globalThis.fetch;
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const at = '2026-08-28T01:02:03.123456Z';
  const digest = Array.from({ length: 32 }, (_, index) => index);
  const sop = { id: 1, source_id: 101, source_key_digest: digest, source_payload_digest: digest, pool_key: ' legacy ', day_index: -1, content_masked: '内容已脱敏', images_digest: digest, original_enabled: false, created_at: at, updated_at: at };
  const config = { id: 2, source_id: 102, source_key_digest: digest, source_payload_digest: digest, agent_code: 'agent', display_name: '配置', scenario_code: 'scenario', original_enabled: true, draft_version: -2, published_version: 0, published_at: 'source civil time', last_modified_at: '', last_modified_source: 'legacy', submitted_for_publish: false, submitted_at: 'invalid civil timestamp', created_at: at, updated_at: at, actors_digest: digest, config_digest: digest };
  const prompt = { id: 3, source_id: 103, source_key_digest: digest, source_payload_digest: digest, agent_code: 'agent', display_name: '提示词', original_enabled: false, version: -3, created_at: at, updated_at: at, prompt_digest: digest };
  const agent = { id: 4, source_id: 104, source_key_digest: digest, source_payload_digest: digest, program_source_id: -1, workflow_source_id: 0, node_source_id: 1, task_source_id: 2, agent_code: 'agent', agent_name: '历史 Agent', original_type: 'legacy', original_status: 'disabled', sort_order: -4, original_enabled: false, created_at: at, updated_at: at, archived_at: '', actors_digest: digest, configuration_digest: digest };
  const fixtures: Record<string, Record<string, unknown>> = { sops: sop, configs: config, prompts: prompt, agents: agent };
  let patch: Record<string, unknown> = {};
  let status = 200;
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ url: url.pathname + url.search, init });
    const parts = url.pathname.split('/');
    const kind = parts[parts.length - 1]!;
    const detail = /^\/api\/admin\/automation-history\/(sops|configs|prompts|agents)\/[1-9]\d*$/.test(url.pathname);
    const row = fixtures[detail ? parts[parts.length - 2]! : kind];
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    return new Response(JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, ...(detail ? { item: row } : { items: [row], total: offset + 1, limit, offset }), ...patch }), { status });
  };
  const reads = [
    () => readAutomationHistorySOPs(20, 3), () => readAutomationHistoryConfigs(20, 3), () => readAutomationHistoryPrompts(20, 3), () => readAutomationHistoryAgents(20, 3),
    () => readAutomationHistorySOP(1), () => readAutomationHistoryConfig(2), () => readAutomationHistoryPrompt(3), () => readAutomationHistoryAgent(4),
  ];
  try {
    const historicalConfig = (await readAutomationHistoryConfigs()).items[0];
    const historicalAgent = (await readAutomationHistoryAgents()).items[0];
    assert(historicalConfig.draft_version === -2 && historicalAgent.workflow_source_id === 0, 'historical negative and zero values changed');
    assert(historicalConfig.published_at === 'source civil time' && historicalConfig.last_modified_at === '' && historicalConfig.submitted_at === 'invalid civil timestamp' && historicalAgent.archived_at === '', 'source time text changed or rejected');
    calls.length = 0;
    for (const read of reads) await read();
    assert(calls.map((call) => call.url).join('|') === [
      '/api/admin/automation-history/sops?limit=20&offset=3', '/api/admin/automation-history/configs?limit=20&offset=3', '/api/admin/automation-history/prompts?limit=20&offset=3', '/api/admin/automation-history/agents?limit=20&offset=3',
      '/api/admin/automation-history/sops/1', '/api/admin/automation-history/configs/2', '/api/admin/automation-history/prompts/3', '/api/admin/automation-history/agents/4',
    ].join('|'), 'eight generated GET routes mismatch');
    assert(calls.every((call) => call.init?.method === 'GET' && call.init.credentials === 'include' && call.init.body === undefined), 'history adapter issued a write or omitted authenticated transport');
    const before = calls.length;
    for (const value of ['', '0', '01', '-1', '1.5', '1/activate', '9007199254740992']) await rejects(() => readAutomationHistorySOP(value));
    for (const [limit, offset] of [[0, 0], [101, 0], [20, -1], [20, 0.5], [20, 2147483648]]) await rejects(() => readAutomationHistorySOPs(limit, offset));
    assert(calls.length === before, 'invalid input issued a request');
    for (const invalid of [{ source: 'current' }, { read_only: false }, { real_external_call_executed: true }, { items: null }, { total: 2 }, { limit: 19 }, { offset: 4 }]) {
      patch = invalid;
      await rejects(reads[0]);
    }
    patch = { items: [{ ...sop, raw_prompt: 'must-not-render' }] };
    await rejects(reads[0]);
    patch = { item: { ...config, id: 3 } };
    await rejects(reads[5]);
    patch = { items: [{ ...agent, actors_digest: [1, 2] }] };
    await rejects(reads[3]);
    patch = { items: [{ ...config, config_digest: Array.from({ length: 32 }, () => 0) }] };
    await rejects(reads[1]);
    patch = { items: [], total: 0 };
    assert((await readAutomationHistoryPrompts(20, 3)).items.length === 0, 'valid empty page rejected');
    patch = {};
    for (const code of [401, 403, 503]) {
      status = code;
      for (const read of reads) await rejects(read, (error) => error instanceof ApiError && error.status === code);
    }
    globalThis.fetch = async () => { throw new Error('network'); };
    await rejects(reads[0], (error) => error instanceof Error && error.message === 'network');
  } finally { globalThis.fetch = savedFetch; }
}
