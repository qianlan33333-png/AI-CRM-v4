import { readGroupOpsHistoryPlans, readGroupOpsHistoryDirectory, readGroupOpsHistoryGroups, readGroupOpsHistoryNodes } from './groupOpsHistory';
import { ApiError } from './transport';

function assert(value: unknown, message: string): asserts value { if (!value) throw new Error(message); }
async function rejects(call: () => Promise<unknown>, check: (error: unknown) => boolean): Promise<void> {
  try { await call(); } catch (error) { assert(check(error), 'unexpected rejection'); return; }
  throw new Error('expected failure without fallback');
}

export async function runGroupOpsHistoryAdapterTests(): Promise<void> {
  const savedFetch = globalThis.fetch;
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const planID = '9007199254740993';
  const date = '2026-08-28T01:02:03.123456Z';
  const plan = { plan_id: planID, name: '  历史计划  ', status: 'archived', revision: 1, source_plan_id: 11, source_code: '', plan_type: 'legacy', original_status: 'active', owner_staff_id: null, created_at: date, updated_at: date, archived_at: null };
  const directory = { id: 1, source_kind: 'wecom_group_chat_snapshots', source_id: null, chat_reference: '历史引用', display_name: null, owner_staff_id: null, owner_name: '', member_count: null, internal_member_count: 0, external_member_count: 2, original_status: '', recorded_at: date };
  const group = { id: 2, source_group_id: 12, source_plan_id: 11, plan_id: planID, owner_staff_id: null, original_status: 'removed', created_at: date, removed_at: null };
  const node = { id: 3, source_node_id: 13, source_plan_id: 11, plan_id: planID, day_index: 0, trigger_time: '  入群后  ', sort_order: 0, original_status: 'disabled', content_package: { text: '<b>历史内容</b>' }, created_at: date, updated_at: date };
  let patch: Record<string, unknown> = {};
  let status = 200;
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ url: url.pathname + url.search, init });
    const kind = url.pathname.split('/').pop();
    const row = kind === 'plans' ? plan : kind === 'directory' ? directory : kind === 'groups' ? group : node;
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    return new Response(JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: [row], total: offset + 1, limit, offset, ...(kind === 'groups' || kind === 'nodes' ? { plan_id: planID } : {}), ...patch }), { status });
  };
  try {
    const plans = await readGroupOpsHistoryPlans(20, 20);
    const directories = await readGroupOpsHistoryDirectory(10, 10);
    const groups = await readGroupOpsHistoryGroups(planID, 5, 5);
    const nodes = await readGroupOpsHistoryNodes(planID, 1, 1);
    assert(plans.items[0].plan_id === planID && plans.items[0].name === plan.name && plans.items[0].original_status === 'active', 'preserve opaque ID, whitespace and source status');
    assert(directories.items[0].display_name === null && directories.items[0].member_count === null && directories.items[0].internal_member_count === 0 && directories.items[0].recorded_at === date, 'preserve null, zero and microsecond timestamps');
    assert(groups.items[0].removed_at === null && nodes.items[0].trigger_time === '  入群后  ' && nodes.items[0].day_index === 0 && nodes.items[0].content_package.text === '<b>历史内容</b>', 'preserve original group/node facts');
    assert(calls.map((c) => c.url).join('|') === [
      '/api/admin/automation-conversion/group-ops/history/plans?limit=20&offset=20',
      '/api/admin/automation-conversion/group-ops/history/directory?limit=10&offset=10',
      `/api/admin/automation-conversion/group-ops/history/plans/${planID}/groups?limit=5&offset=5`,
      `/api/admin/automation-conversion/group-ops/history/plans/${planID}/nodes?limit=1&offset=1`,
    ].join('|'), 'four generated routes use exact pagination and string plan IDs');
    assert(calls.every((c) => c.init?.method === 'GET' && c.init.credentials === 'include' && c.init.body === undefined), 'read-only authenticated transport');
    const before = calls.length;
    for (const id of ['', '0', '01', '-1', '1.5', '1/activate', '9223372036854775808']) await rejects(() => readGroupOpsHistoryGroups(id), (e) => e instanceof Error);
    for (const [limit, offset] of [[0, 0], [101, 0], [20, -1], [20, 0.5], [20, 2147483648]]) await rejects(() => readGroupOpsHistoryPlans(limit, offset), (e) => e instanceof Error);
    assert(calls.length === before, 'invalid inputs never issue requests');
    for (const invalid of [{ source: 'current' }, { read_only: false }, { real_external_call_executed: true }, { items: null }, { total: 2 }, { offset: 1 }, { limit: 19 }]) {
      patch = invalid;
      await rejects(() => readGroupOpsHistoryPlans(), (e) => e instanceof Error);
    }
    patch = { plan_id: '7' };
    await rejects(() => readGroupOpsHistoryGroups(planID), (e) => e instanceof Error);
    patch = { items: [{ ...node, plan_id: '7' }] };
    await rejects(() => readGroupOpsHistoryNodes(planID), (e) => e instanceof Error);
    patch = { items: [{ ...directory, source_kind: 'current_directory' }] };
    await rejects(() => readGroupOpsHistoryDirectory(), (e) => e instanceof Error);
    patch = { items: [{ ...plan, status: 'active' }] };
    await rejects(() => readGroupOpsHistoryPlans(), (e) => e instanceof Error);
    patch = { items: [{ ...plan, plan_id: 7 }] };
    await rejects(() => readGroupOpsHistoryPlans(), (e) => e instanceof Error);
    patch = { items: [], total: 0 };
    assert((await readGroupOpsHistoryPlans()).items.length === 0, 'valid empty page');
    patch = {};
    for (const code of [401, 403, 503]) {
      status = code;
      for (const call of [() => readGroupOpsHistoryPlans(), () => readGroupOpsHistoryDirectory(), () => readGroupOpsHistoryGroups(planID), () => readGroupOpsHistoryNodes(planID)]) await rejects(call, (e) => e instanceof ApiError && e.status === code);
    }
    globalThis.fetch = async () => { throw new Error('network'); };
    await rejects(() => readGroupOpsHistoryPlans(), (e) => e instanceof Error && e.message === 'network');
  } finally { globalThis.fetch = savedFetch; }
}
