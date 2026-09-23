import {
  readAudienceHistoryGroups, readAudienceHistoryPackages, readAudienceHistoryVersions, readAudienceHistorySenders,
  readAudienceHistoryRules, readAudienceHistoryRuleVersions, readAudienceHistoryDefinitions, readAudienceHistoryMembers,
  readAudienceHistoryActivityRuns, readAudienceHistoryActivityMemberEvents,
  readAudienceHistoryPackage, readAudienceHistoryDefinition, audienceHistoryDigestHex,
} from './audienceHistory';
import { ApiError } from './transport';

function assert(value: unknown, message: string): asserts value { if (!value) throw new Error(message); }
async function rejects(call: () => Promise<unknown>, check: (error: unknown) => boolean = (e) => e instanceof Error): Promise<void> {
  try { await call(); } catch (error) { assert(check(error), 'unexpected rejection'); return; }
  throw new Error('expected failure without fallback');
}

export async function runAudienceHistoryAdapterTests(): Promise<void> {
  const savedFetch = globalThis.fetch;
  const calls: Array<{ url: string; init?: RequestInit }> = [];
  const date = '2026-08-28T01:02:03.123456Z';
  const common = { id: 7, source_id: 107, created_at: date, updated_at: date, original_status: ' active ' };
  const digest = Array.from({ length: 32 }, (_, i) => i);
  const fixture: Record<string, Record<string, unknown>> = {
    groups: { ...common, name: ' 历史分组 ' },
    packages: { ...common, id: 42, name: ' 历史包 ', group_history_id: null, current_version_source_id: null, runtime_digest: digest, incremental_enabled: true, daily_enabled: false, incremental_interval_seconds: -7, lookback_seconds: 0 },
    versions: { ...common, package_history_id: 42, version_number: -2, template_version: null, published_at: null, definition_digest: digest },
    senders: { ...common, package_history_id: 42, staff_id: null, priority: -3 },
    rules: { ...common, owner_staff_id: null },
    'rule-versions': { ...common, rule_history_id: 8, version: 0, definition_digest: digest },
    definitions: { ...common, id: 9, cached_headcount: -4, usage_count: 0, last_refreshed_at: null, definition_digest: digest },
    members: { ...common, package_history_id: 42, customer_id: null, exited_at: null, payload_digest: digest },
    'activity-runs': { id: 70, package_history_id: 42, version_history_id: null, run_type: 'refresh', original_status: 'done', refresh_started_at: date, refresh_finished_at: null, last_watermark_at: null, next_watermark_at: null, returned_count: -1, entered_count: 0, updated_count: 2, exited_count: 3, member_event_count: 4, duration_ms: -5, created_at: date },
    'activity-member-events': { id: 71, package_history_id: 42, run_history_id: null, member_history_id: null, event_type: 'entered', occurred_at: date, created_at: date },
  };
  let patch: Record<string, unknown> = {};
  let status = 200;
  globalThis.fetch = async (input, init) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ url: url.pathname + url.search, init });
    const path = url.pathname;
    const detail = /\/(packages\/42|definitions\/9)$/.test(path);
    const kind = detail ? (path.includes('/packages/') ? 'packages' : 'definitions') : path.includes('/rules/8/') ? 'rule-versions' : path.split('/').pop()!;
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    const parent = ['versions', 'senders', 'members'].includes(kind) ? { package_id: 42 } : kind === 'rule-versions' ? { rule_id: 8 } : {};
    return new Response(JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false,
      ...(detail ? { item: fixture[kind] } : { items: [fixture[kind]], total: offset + 1, limit, offset, ...parent }), ...patch }), { status });
  };
  const reads = [
    () => readAudienceHistoryGroups(2, 3), () => readAudienceHistoryPackages(2, 3),
    () => readAudienceHistoryVersions(42, 2, 3), () => readAudienceHistorySenders(42, 2, 3),
    () => readAudienceHistoryRules(2, 3), () => readAudienceHistoryRuleVersions(8, 2, 3),
    () => readAudienceHistoryDefinitions(2, 3), () => readAudienceHistoryMembers(42, 2, 3),
    () => readAudienceHistoryActivityRuns(2, 3), () => readAudienceHistoryActivityMemberEvents(2, 3),
    () => readAudienceHistoryPackage(42), () => readAudienceHistoryDefinition(9),
  ];
  try {
    const groups = await readAudienceHistoryGroups(2, 3);
    assert(groups.items[0].name === ' 历史分组 ' && groups.items[0].created_at === date, 'source whitespace and timestamp preserved');
    calls.length = 0;
    for (const read of reads) await read();
    assert(calls.map((v) => v.url).join('|') === [
      '/api/admin/audience-history/groups?limit=2&offset=3',
      '/api/admin/audience-history/packages?limit=2&offset=3',
      '/api/admin/audience-history/packages/42/versions?limit=2&offset=3',
      '/api/admin/audience-history/packages/42/senders?limit=2&offset=3',
      '/api/admin/audience-history/rules?limit=2&offset=3',
      '/api/admin/audience-history/rules/8/versions?limit=2&offset=3',
      '/api/admin/audience-history/definitions?limit=2&offset=3',
      '/api/admin/audience-history/packages/42/members?limit=2&offset=3',
      '/api/admin/audience-history/activity-runs?limit=2&offset=3',
      '/api/admin/audience-history/activity-member-events?limit=2&offset=3',
      '/api/admin/audience-history/packages/42',
      '/api/admin/audience-history/definitions/9',
    ].join('|'), 'twelve generated GET routes with exact parent IDs and paging');
    assert(calls.every((v) => v.init?.method === 'GET' && v.init.credentials === 'include' && v.init.body === undefined), 'only credentialed GET without write body');
    const pkg = (await readAudienceHistoryPackage('42')).item;
    assert(pkg.group_history_id === null && pkg.incremental_interval_seconds === -7 && pkg.lookback_seconds === 0 && pkg.incremental_enabled === true && pkg.daily_enabled === false, 'NULL, negative, zero and historical flags unchanged');
    assert((await readAudienceHistoryMembers(42)).items[0].customer_id === null, 'unconfirmed customer is not invented');
    assert((await readAudienceHistoryActivityRuns()).items[0].returned_count === -1 && (await readAudienceHistoryActivityMemberEvents()).items[0].run_history_id === null, 'activity history keeps signed counts and unresolved parents');
    assert(audienceHistoryDigestHex(digest) === digest.map((n) => n.toString(16).padStart(2, '0')).join(''), '32-byte digest renders exact hex');
    const before = calls.length;
    for (const id of ['', '0', '01', '-1', '1.5', '1/activate', '9007199254740992']) await rejects(() => readAudienceHistoryPackage(id));
    for (const [limit, offset] of [[0, 0], [101, 0], [20, -1], [20, 0.5], [20, 2147483648]]) await rejects(() => readAudienceHistoryGroups(limit, offset));
    assert(calls.length === before, 'invalid IDs and pages issue no requests');
    for (const invalid of [{ source: 'current' }, { read_only: false }, { real_external_call_executed: true }, { items: null }, { limit: 1 }, { offset: 4 }, { total: 5 }]) {
      patch = invalid;
      await rejects(reads[0]);
    }
    patch = { package_id: 43 };
    await rejects(reads[2]);
    patch = { items: [{ ...fixture.members, package_history_id: 43 }] };
    await rejects(reads[7]);
    patch = { item: { ...fixture.packages, id: 43 } };
    await rejects(reads[10]);
    patch = { items: [{ ...fixture.definitions, definition_digest: [1, 2] }] };
    await rejects(reads[6]);
    patch = { items: [{ ...fixture.groups, id: 9007199254740992 }] };
    await rejects(reads[0]);
    patch = { items: [], total: 0 };
    assert((await readAudienceHistoryGroups(2, 3)).items.length === 0, 'empty page stays empty');
    patch = {};
    for (const code of [401, 403, 503]) {
      status = code;
      for (const read of reads) await rejects(read, (e) => e instanceof ApiError && e.status === code);
    }
    globalThis.fetch = async () => { throw new Error('network'); };
    await rejects(reads[0], (e) => e instanceof Error && e.message === 'network');
  } finally { globalThis.fetch = savedFetch; }
}
