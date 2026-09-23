import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import { readFileSync } from 'node:fs';

const bundle = await build({ entryPoints: ['web/v3/governanceAdmin.ts'], bundle: true, format: 'iife', platform: 'browser', write: false, target: 'es2020' });
const flush = async () => { for (let i = 0; i < 4; i++) await new Promise((resolve) => setTimeout(resolve, 0)); };
const at = '2026-09-18T08:00:00Z';
const overview = (title = '真实巡查', status = 'unknown') => ({ fresh: false, observed_at: at, latest: { id: 11, release_sha: 'release-fixture' }, checks: [{ id: 'fixture.check', owner: 'fixture', title, status, code: 'no_evidence', observed_at: at, metrics: { observed: 0 } }], issues: [{ id: 17, check_id: 'fixture.check', code: 'no_evidence', status: 'open', severity: 'unknown', version: 9, first_seen: at, last_seen: at, occurrences: 2 }] });
function resourceCoverage(enabled = true) {
  const data = JSON.parse(readFileSync('internal/adminops/retention_resources.generated.json', 'utf8'));
  for (const item of data.items) if (item.coverage_status === 'policy_available') item.coverage_status = enabled ? 'scheduled' : 'disabled';
  const count = (field, values) => data.items.filter(item => values.includes(item[field])).length;
  return { ...data, automatic_cleanup_enabled: enabled, allowlist_policy_count: 9, summary: {
    registered_tables: count('kind', ['table']), registered_resources: count('kind', ['resource']), registered_filesystem_prefixes: count('kind', ['filesystem_prefix']),
    gap_resources: count('coverage_status', ['gap', 'owner_not_bound']), protected_resources: count('coverage_status', ['protected']), owner_managed_resources: count('coverage_status', ['owner_managed']), scheduled_resources: count('coverage_status', ['scheduled']), disabled_resources: count('coverage_status', ['disabled']), unbound_resources: count('coverage_status', ['owner_not_bound']), unobserved_resources: count('coverage_status', ['native_unobserved', 'host_unobserved']), security_ttl_resources: count('policy', ['security_ttl']), mixed_payload_resources: count('policy', ['protected_mixed_payload']), unclassified_resources: count('policy', ['protected_unclassified']), coordination_resources: count('policy', ['runtime_coordination']),
  } };
}
function setup() {
  const dom = new JSDOM('<!doctype html><header class="admin-topbar"><h1>运行治理</h1><div class="admin-topbar-meta"></div></header><section id="governance-admin-root"></section>', { url: 'https://fixture.invalid/admin/ops', runScripts: 'outside-only' });
  dom.window.Headers = Headers;
  dom.window.document.cookie = 'aicrm_admin_csrf=fixture-csrf';
  dom.window.HTMLDialogElement.prototype.showModal = function () { this.open = true; };
  dom.window.HTMLDialogElement.prototype.close = function () { this.open = false; this.dispatchEvent(new dom.window.Event('close')); };
  const pending = [];
  dom.window.fetch = (url, init) => new Promise((resolve) => pending.push({ url, init, respond: (body, status = 200) => resolve({ status, headers: new Headers(), ok: status >= 200 && status < 300, json: async () => body }) }));
  dom.window.eval(bundle.outputFiles[0].text);
  return { dom, pending, root: dom.window.document.querySelector('#governance-admin-root'), tab: (name) => dom.window.document.querySelector(`[data-tab="${name}"]`).click() };
}

{
  const h = setup();
  h.pending.shift().respond(overview()); await flush();
  assert.match(h.root.textContent, /未知|过期/);
  assert.ok(h.root.querySelector('[data-status="unknown"]'), 'missing evidence is not green');
  assert.equal(h.dom.window.document.querySelectorAll('.admin-topbar').length, 1);
  assert.equal(h.dom.window.document.querySelectorAll('.admin-topbar [data-page-header-actions="governance"] button').length, 2, 'actions reuse the existing header');
  h.tab('checks'); const old = h.pending.shift();
  h.tab('issues'); const latest = h.pending.shift();
  latest.respond(overview('new-read')); await flush();
  old.respond(overview('late-stale-read', 'ok')); await flush();
  assert.ok(h.root.querySelector('[data-ack="17"]'), 'late read cannot replace current issues view');
  assert.doesNotMatch(h.root.textContent, /late-stale-read/);
  h.root.querySelector('[data-ack="17"]').click();
  const ack = h.pending.shift();
  assert.equal(ack.init.method, 'PATCH');
  assert.deepEqual(JSON.parse(ack.init.body), { version: 9, status: 'acknowledged' });
  assert.equal(ack.init.headers.get('X-CSRF-Token'), 'fixture-csrf');
  assert.ok(ack.init.headers.get('Idempotency-Key'));
  assert.equal(h.root.querySelector('[data-ack="17"]').disabled, true);
  ack.respond({}, 409); await flush();
  assert.match(h.root.querySelector('[role="alert"]').textContent, /记录已更新/);
  h.dom.window.close();
}

for (const lateStatus of [200, 409, 403]) {
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  h.tab('issues'); h.pending.shift().respond(overview()); await flush();
  h.root.querySelector('[data-ack="17"]').click(); const ack = h.pending.shift();
  h.tab('reports'); h.pending.shift().respond({ items: [{ hour_key: at, notification_kind: 'critical', effect_state: 'outcome_unknown', effect_id: 'eer_55', content: { msg_type: 'text', content: { text: '<script>must remain text</script>' } } }] }); await flush();
  ack.respond({}, lateStatus); await flush();
  assert.ok(h.root.querySelector('[data-report="0"]'), `late mutation ${lateStatus} must not replace newer navigation`);
  assert.equal(h.pending.length, 0, 'late mutation must not issue a new fetch for a different tab');
  const button = h.root.querySelector('[data-report="0"]'); button.focus(); button.click();
  const dialog = h.dom.window.document.querySelector('dialog');
  assert.ok(dialog?.open, 'report uses the shared detail drawer');
  assert.equal(dialog.querySelector('script'), null, 'report payload never becomes HTML');
  assert.match(dialog.textContent, /must remain text/);
  dialog.querySelector('button').click();
  assert.equal(h.dom.window.document.activeElement, button, 'drawer close returns focus');
  h.dom.window.close();
}

for (const lateStatus of [202, 500]) {
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  [...h.dom.window.document.querySelectorAll('[data-page-header-actions="governance"] button')].find((button) => button.textContent === '立即巡查').click();
  const run = h.pending.shift();
  assert.equal(run.init.method, 'POST');
  assert.equal(run.url, '/api/admin/ops-inspections/runs');
  h.tab('reports'); h.pending.shift().respond({ items: [] }); await flush();
  const rendered = h.root.textContent;
  run.respond({}, lateStatus); await flush();
  assert.equal(h.root.textContent, rendered, `late manual scan ${lateStatus} must preserve current view`);
  assert.equal(h.pending.length, 0, 'late manual scan must not issue another tab refresh');
  h.dom.window.close();
}

const acceptedCommand = { state: 'accepted', job_id: 71, accepted_at: at };
const completedCommand = { ...acceptedCommand, state: 'completed', run_id: 12, completed_at: at };
const headerAction = (h, label) => [...h.dom.window.document.querySelectorAll('[data-page-header-actions="governance"] button')].find((button) => button.textContent === label);
async function acceptManual(h) {
  headerAction(h, '立即巡查').click();
  const command = h.pending.shift();
  assert.equal(command.init.headers.get('X-CSRF-Token'), 'fixture-csrf');
  assert.ok(command.init.headers.get('Idempotency-Key'));
  command.respond({ ...acceptedCommand, replay: false }, 202); await flush();
  assert.equal(h.pending[0].url, '/api/admin/ops-inspections/commands/71');
  assert.equal(h.pending[0].init.method, 'GET');
  h.pending.shift().respond(acceptedCommand); await flush();
  h.pending.shift().respond(overview()); await flush();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  await acceptManual(h);
  assert.match(h.root.textContent, /巡查已受理，任务 71，等待执行结果/);
  assert.ok(h.root.querySelector('[data-status="unknown"]'), 'acceptance does not create a healthy result');
  headerAction(h, '刷新').click();
  h.pending.shift().respond(acceptedCommand); await flush();
  const newerScheduled = overview('另一定期巡查', 'warning'); newerScheduled.latest.id = 14; newerScheduled.fresh = true;
  h.pending.shift().respond(newerScheduled); await flush();
  assert.match(h.root.textContent, /等待执行结果/, 'a newer unrelated run must not complete this manual command');
  assert.ok(h.root.querySelector('[data-status="warning"]'), 'latest overview remains independently readable while the command waits');
  headerAction(h, '刷新').click();
  h.pending.shift().respond(completedCommand); await flush();
  h.pending.shift().respond(newerScheduled); await flush();
  assert.doesNotMatch(h.root.textContent, /等待执行结果/, 'the matching permanent command receipt, not an increase in latest run, proves completion');
  h.dom.window.close();
}
for (const response of [
  { body: {}, status: 503 },
  { body: { ...completedCommand, job_id: 72 }, status: 200 },
  { body: { ...acceptedCommand, state: 'completed' }, status: 200 },
  { body: { ...completedCommand, completed_at: 'invalid' }, status: 200 },
]) {
  const h = setup(); h.pending.shift().respond(overview()); await flush(); await acceptManual(h);
  headerAction(h, '刷新').click();
  h.pending.shift().respond(response.body, response.status); await flush();
  assert.match(h.root.textContent, /等待执行结果/, 'unavailable, mismatched or incomplete evidence cannot clear the accepted command');
  assert.ok(h.root.querySelector('[role="alert"]'));
  assert.equal(h.pending.length, 0, 'uncertain completion cannot silently refresh into a finished claim');
  h.dom.window.close();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush(); await acceptManual(h);
  headerAction(h, '刷新').click(); const oldCommand = h.pending.shift();
  h.tab('reports'); const latestCommand = h.pending.shift();
  latestCommand.respond(acceptedCommand); await flush();
  assert.equal(h.pending[0].url, '/api/admin/ops-inspections/reports');
  h.pending.shift().respond({ items: [] }); await flush();
  const rendered = h.root.textContent;
  oldCommand.respond(completedCommand); await flush();
  assert.equal(h.root.textContent, rendered, 'late command response cannot overwrite newer navigation');
  h.tab('overview'); h.pending.shift().respond(acceptedCommand); await flush();
  h.pending.shift().respond(overview()); await flush();
  assert.match(h.root.textContent, /等待执行结果/, 'late response also cannot mutate the remembered command behind a newer view');
  h.dom.window.close();
}

for (const response of [{ body: { checks: [], issues: [] }, code: 200, expected: /格式不完整/ }, { body: {}, code: 403, expected: /超级管理员/ }]) {
  const h = setup(); h.pending.shift().respond(response.body, response.code); await flush();
  assert.match(h.root.querySelector('[role="alert"]').textContent, response.expected);
  assert.equal(h.root.querySelector('[data-status="ok"]'), null);
  h.dom.window.close();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  h.tab('diagnostics'); h.pending.shift().respond({ items: [] }); await flush();
  h.root.querySelector('[name="correlation"]').value = 'a'.repeat(32);
  h.root.querySelector('form').dispatchEvent(new h.dom.window.Event('submit', { bubbles: true, cancelable: true }));
  const filtered = h.pending.shift();
  assert.equal(filtered.url, '/api/admin/ops-diagnostics?correlation=' + 'a'.repeat(32));
  filtered.respond({ items: [{ code: 'owner_failure', route_template: '/api/orders/{id}', correlation_digest: 'digest-only', release_sha: 'test-version', job_ref: 'river_5', effect_ref: 'eer_6', occurred_at: at }] }); await flush();
  assert.match(h.root.textContent, /owner_failure.*orders.*digest-only/s);
  h.root.querySelector('[data-clear-query]').click();
  assert.equal(h.pending[0].url, '/api/admin/ops-diagnostics');
  h.pending.shift().respond({ items: [] }); await flush();
  h.dom.window.close();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  h.tab('retention'); h.pending.shift().respond({ items: [{ id: 'ops_results', resource: '巡查明细', retention: '720 小时', status: 'preview_only' }, { id: 'business_facts', resource: '订单问卷', retention: '永久', status: 'protected' }] }); await flush();
  assert.equal(h.pending[0].url, '/api/admin/ops-retention/runs');
  h.pending.shift().respond({ items: [{ policy: 'ops_results', cutoff: at, state: 'completed', deleted_rows: 12, payload_bytes: 512, completed_at: at }], release: { status: 'gap', reason: 'two_verified_schema_compatible_rollbacks_missing', deleted_count: null, deleted_bytes: null, rollback_count: 1, space_observations: [{ resource: 'release_storage', state: 'observed', available_bytes_before: 1000, available_bytes_after: 900, available_bytes_net_change: -100 }] } }); await flush();
  assert.match(h.root.textContent, /最近数据库清理记录/);
  assert.match(h.root.textContent, /发布包与回滚保护.*gap.*未知.*-100/s, 'unknown deletion cannot become zero; disk net change may be negative');
  assert.equal(h.root.querySelectorAll('[data-preview]').length, 1, 'protected business data has no cleanup preview command');
  h.root.querySelector('[data-preview]').click();
  assert.equal(h.pending[0].url, '/api/admin/ops-retention/preview?policy=ops_results');
  assert.equal(h.pending[0].init.method, 'GET', 'preview must stay read-only');
  h.pending.shift().respond({ cutoff: at, candidates: 1000, has_more: true, estimated_payload_bytes: 2048, protected_reason: '业务保护' }); await flush();
  assert.match(h.dom.window.document.querySelector('dialog').textContent, /本批候选：1000.*仍有更多.*2048.*业务保护/s);
  h.dom.window.document.querySelector('dialog button').click();
  h.root.querySelector('[data-preview]').click(); const oldPreview = h.pending.shift();
  h.tab('reports'); h.pending.shift().respond({ items: [] }); await flush();
  oldPreview.respond({ cutoff: at, candidates: 999, estimated_payload_bytes: 999 }); await flush();
  assert.equal(h.dom.window.document.querySelector('dialog'), null, 'late preview cannot open over another tab');
  h.dom.window.close();
}

{
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  h.tab('resources'); assert.equal(h.pending[0].url, '/api/admin/ops-retention/resources');
  const data = resourceCoverage(); h.pending.shift().respond(data); await flush();
  const rows = () => [...h.root.querySelectorAll('[data-resource-table] tbody tr')];
  const text = () => h.root.querySelector('[data-resource-table]').textContent;
  const submit = () => h.root.querySelector('[data-resource-filters]').dispatchEvent(new h.dom.window.Event('submit', { bubbles: true, cancelable: true }));
  const set = (name, value) => { h.root.querySelector(`[name="resource_${name}"]`).value = value; };
  assert.match(h.root.textContent, /白名单健康不代表全部资源已覆盖/);
  assert.match(h.root.textContent, new RegExp(`已登记资源${data.items.length}`));
  assert.equal(rows().length, 25, 'the full registry must not become an unbounded table');
  const first = text(); h.root.querySelector('[data-resource-page="next"]').click();
  assert.equal(rows().length, 25); assert.notEqual(text(), first);
  assert.match(h.root.querySelector('[data-resource-count]').textContent, /第 2/);
  const last = data.items.at(-1);
  set('query', last.name);
  h.root.querySelector('[name="resource_query"]').dispatchEvent(new h.dom.window.Event('input', { bubbles: true }));
  assert.equal(rows().length, 25, 'draft search must not redraw on each key');
  h.root.querySelector('[name="resource_query"]').dispatchEvent(new h.dom.window.CompositionEvent('compositionstart', { bubbles: true }));
  submit(); assert.equal(rows().length, 25, 'IME composition does not commit a filter');
  h.root.querySelector('[name="resource_query"]').dispatchEvent(new h.dom.window.CompositionEvent('compositionend', { bubbles: true }));
  submit();
  assert.match(text(), new RegExp(last.owner));
  assert.match(h.root.querySelector('[data-resource-count]').textContent, /第 1/);
  assert.ok(rows().length < 25, 'filter covers the complete registry, including later pages');
  assert.equal(h.pending.length, 0, 'local full-registry filters do not send unsupported server query parameters');
  h.root.querySelector('[data-resource-reset]').click(); set('status', 'native_unobserved'); submit();
  assert.match(text(), /river_job.*原生执行未观察.*需要原生清理器/s);
  assert.equal(h.root.querySelector('[data-status="ok"]'), null);
  assert.ok(h.root.querySelector('[data-coverage-status="native_unobserved"][data-status="unknown"]'));
  set('status', 'host_unobserved'); submit();
  assert.ok(rows().length > 0); assert.ok(h.root.querySelector('[data-coverage-status="host_unobserved"][data-status="unknown"]'));
  set('status', ''); set('owner', 'access'); set('policy', 'security_ttl'); submit();
  assert.ok(rows().length > 0); assert.match(text(), /缺少物理清理.*授权是否有效由安全 TTL 独立判断/s);
  assert.ok(rows().every(row => row.cells[1].textContent === 'access'));
  h.root.querySelector('[data-resource-detail]').focus(); h.root.querySelector('[data-resource-detail]').click();
  const dialog = h.dom.window.document.querySelector('dialog');
  assert.match(dialog.textContent, /Owner：access.*安全有效期.*由 Owner 安全 TTL.*登记来源.*SHA256/s);
  dialog.querySelector('button').click();
  assert.ok(h.dom.window.document.activeElement.hasAttribute('data-resource-detail'), 'resource detail uses shared focus-return drawer');
  set('query', 'no-such-registered-resource'); submit();
  assert.equal(h.root.querySelector('[data-surface-table-read-state="no-match"]')?.textContent, '没有匹配资源，请调整筛选；这不代表系统没有治理缺口。');
  assert.equal(h.root.querySelector('[data-resource-page="next"]').disabled, true);
  headerAction(h, '刷新').click(); h.pending.shift().respond(data); await flush();
  assert.equal(h.root.querySelector('[name="resource_query"]').value, 'no-such-registered-resource', 'refresh preserves committed filters');
  h.dom.window.close();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush(); h.tab('resources');
  const data = resourceCoverage(false);
  data.items.find(item => item.coverage_status === 'gap').reason = '<img src=x onerror="window.__unsafe=true">';
  h.pending.shift().respond(data); await flush();
  assert.match(h.root.textContent, /自动执行未启用/);
  h.root.querySelector('[name="resource_status"]').value = 'disabled';
  h.root.querySelector('form').dispatchEvent(new h.dom.window.Event('submit', { cancelable: true }));
  assert.ok(h.root.querySelector('[data-coverage-status="disabled"]'));
  assert.equal(h.root.querySelector('[data-status="ok"]'), null);
  h.root.querySelector('[name="resource_query"]').value = '<img'; h.root.querySelector('[name="resource_status"]').value = '';
  h.root.querySelector('form').dispatchEvent(new h.dom.window.Event('submit', { cancelable: true }));
  h.root.querySelector('[data-resource-detail]').click();
  assert.match(h.dom.window.document.querySelector('dialog').textContent, /<img/);
  assert.equal(h.dom.window.document.querySelector('dialog img'), null, 'registry text must not become executable HTML');
  assert.equal(h.dom.window.__unsafe, undefined);
  h.dom.window.close();
}
for (const mutate of [
  data => { delete data.summary; },
  data => { data.summary.registered_tables++; },
  data => { data.items[0].coverage_status = 'new_unrecognized_state'; },
  data => { data.items[1] = data.items[0]; },
]) {
  const h = setup(); h.pending.shift().respond(overview()); await flush(); h.tab('resources');
  const data = resourceCoverage(); mutate(data); h.pending.shift().respond(data); await flush();
  assert.match(h.root.querySelector('[role="alert"]').textContent, /资源登记数据不完整/);
  assert.equal(h.root.querySelector('[data-resource-table]'), null);
  h.dom.window.close();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush(); h.tab('resources');
  h.pending.shift().respond(resourceCoverage()); await flush();
  headerAction(h, '刷新').click(); h.pending.shift().respond({}, 403); await flush();
  assert.match(h.root.querySelector('[role="alert"]').textContent, /超级管理员/);
  assert.equal(h.root.querySelector('[data-resource-table]'), null, 'permission loss clears previously authorized resource metadata');
  h.tab('resources'); const late = h.pending.shift();
  h.tab('reports'); h.pending.shift().respond({ items: [] }); await flush(); const current = h.root.textContent;
  late.respond(resourceCoverage()); await flush();
  assert.equal(h.root.textContent, current, 'late coverage response cannot overwrite report navigation');
  h.dom.window.close();
}

const profiles = (enabled = true, items = []) => ({ enabled, items, target: 'api', duration_seconds: 5, worker_coverage: 'not_supported' });
{
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  h.tab('profiles');
  assert.equal(h.pending[0].url, '/api/admin/ops-diagnostics/cpu-profiles');
  h.pending.shift().respond(profiles(false)); await flush();
  const capture = () => [...h.dom.window.document.querySelectorAll('[data-page-header-actions="governance"] button')].find(b => /采样/.test(b.textContent));
  assert.equal(capture().disabled, true);
  assert.match(h.root.textContent, /API.*5 秒.*Worker 进程尚未覆盖/s);
  h.tab('profiles'); h.pending.shift().respond(profiles()); await flush();
  capture().click();
  const first = h.pending.shift();
  assert.equal(first.init.method, 'POST');
  assert.deepEqual(JSON.parse(first.init.body), {});
  assert.equal(first.init.headers.get('X-CSRF-Token'), 'fixture-csrf');
  const key = first.init.headers.get('Idempotency-Key');
  assert.ok(key);
  assert.equal(capture().disabled, true, 'one in-flight profile at a time');
  capture().click(); assert.equal(h.pending.length, 0);
  first.respond({}, 503); await flush();
  assert.equal(capture().textContent, '查看上次采样结果');
  capture().click();
  const replay = h.pending.shift();
  assert.equal(replay.init.headers.get('Idempotency-Key'), key, 'uncertain result must reuse its accepted command');
  h.tab('reports'); h.pending.shift().respond({ items: [] }); await flush();
  const content = h.root.textContent;
  replay.respond({ state: 'completed', id: 'a'.repeat(32) }); await flush();
  assert.equal(h.root.textContent, content, 'late capture cannot overwrite current navigation');
  assert.equal(h.pending.length, 0);
  h.dom.window.close();
}
{
  const h = setup(); h.pending.shift().respond(overview()); await flush();
  h.tab('profiles');
  h.pending.shift().respond(profiles(true, [
    { id: 'a'.repeat(32), state: 'completed', release_sha: 'version-a', accepted_at: at, expires_at: '2099-01-01T00:00:00Z', bytes: 128 },
    { id: '../../outside', state: 'completed', expires_at: '2099-01-01T00:00:00Z' },
    { id: 'b'.repeat(32), state: 'completed', expires_at: '2000-01-01T00:00:00Z' },
    { id: 'c'.repeat(32), state: 'outcome_unknown', expires_at: '2099-01-01T00:00:00Z' },
  ])); await flush();
  const links = [...h.root.querySelectorAll('a[download]')];
  assert.equal(links.length, 1, 'only completed, current, opaque IDs may link to downloads');
  assert.equal(links[0].getAttribute('href'), '/api/admin/ops-diagnostics/cpu-profiles/' + 'a'.repeat(32) + '/download');
  assert.match(h.root.textContent, /version-a/);
  h.dom.window.close();
}
console.log('governanceAdmin: unknown/stale, response fencing, CAS, authorization, profile idempotency, and report drawer PASS');
