import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-static-history-'));
const output = path.join(out, 'staticHistory.js');
await build({ entryPoints: [path.join(root, 'src/admin/sections/staticHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const { mountStaticHistory } = await import(pathToFileURL(output).href);
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/config.html?static_history=1&history_kind=GroupInvite', pretendToBeVisual: true });
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
const digest = Array(32).fill(2), stamp = '2026-08-28T01:02:03.123456Z';
const kinds = ['GroupInvite', 'ProductPageSlice', 'CycleStrategy', 'CycleVersion', 'CycleDocument', 'CycleMetric', 'CycleReference'];
const fields = {
  GroupInvite: { name: '', title: '<title>', description: '', original_state: 'old', original_auto_create: false, room_base_name: '', room_base_source_id: null, original_enabled: false, original_binding_state: 'unbound', created_at: stamp, updated_at: stamp },
  ProductPageSlice: { product_source_id: -8, image_source_id: 0, sort_order: -1, original_enabled: false, created_at: stamp, updated_at: stamp },
  CycleStrategy: { strategy_key: '', title: '<strategy>', description: '', cadence: 'weekly', timezone: 'UTC', original_status: 'old', current_version: -1, created_at: stamp, updated_at: stamp },
  CycleVersion: { strategy_source_id: -8, strategy_history_id: 7, version: -1, label: '', objective: '', version_hash: 'x', effective_from: null, original_governance: 'old', confirmed_at: null, operation_skill_hash: 'y', created_at: stamp },
  CycleDocument: { strategy_version_source_id: -8, version_history_id: 7, schema_version: '', execution_guide_sha256: 'a', execution_guide_generated_at: null, copy_guide_sha256: 'b', copy_guide_generated_at: null, measurement_guide_sha256: 'c', measurement_guide_generated_at: null, document_pack_hash: 'd', created_at: stamp },
  CycleMetric: { run_source_id: -8, metric_key: 'conversion', label: '<metric>', numerator: null, denominator: -2.5, value: 0, unit: 'count', observation_window: 'weekly', data_source: 'legacy', data_quality: 'partial', limitations: [null, { note: 'original' }], is_causal: false, value_status: 'unknown', last_snapshot_source_id: -9, created_at: stamp, updated_at: stamp },
  CycleReference: { run_source_id: -8, reference_key: 'reference', reference_type: 'other', label: '<reference>', source_system: 'legacy', reference_source_id: '-7', evidence_hash: 'evidence', data_status: 'unknown', last_snapshot_source_id: -9, created_at: stamp, updated_at: stamp },
};
const endpoint = { GroupInvite: 'group-invites', ProductPageSlice: 'page-slices', CycleStrategy: 'cycle-strategies', CycleVersion: 'cycle-versions', CycleDocument: 'cycle-documents', CycleMetric: 'cycle-metrics', CycleReference: 'cycle-references' };
const calls = []; let fail = false, invalid = '', empty = false;
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid'); calls.push({ url, init });
  const kind = kinds.find((candidate) => url.pathname.endsWith(endpoint[candidate]) || url.pathname.includes(`/${endpoint[candidate]}/`));
  if (!kind) throw new Error(`unexpected endpoint ${url.pathname}`);
  const id = url.pathname.split('/').pop(); const isDetail = /^\d+$/.test(id);
  const item = { id: 7, source_id: -9, ...(!['CycleMetric', 'CycleReference'].includes(kind) ? { source_key_digest: digest, source_payload_digest: digest } : {}), ...fields[kind] };
  if (invalid === 'private' && kind === 'CycleReference') item.href = 'https://private.example/token';
  if (invalid === 'missing' && kind === 'CycleMetric') delete item.limitations;
  const offset = Number(url.searchParams.get('offset') ?? '0');
  const body = fail ? '{"raw":"NO"}' : JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, ...(isDetail ? { item } : { items: empty ? [] : offset === 0 ? Array.from({ length: 20 }, (_, index) => ({ ...item, id: index + 1 })) : [{ ...item, id: 21 }], total: empty ? 0 : 21, limit: 20, offset }) });
  return new Response(body, { status: fail ? 503 : 200 });
};
try {
  const stage = document.querySelector('#stage');
  for (const kind of kinds) {
    await mountStaticHistory(stage, { kind });
    if (!stage.querySelector(`[data-static-history-kind="${kind}"]`) || !stage.textContent.includes('-9')) throw new Error(`missing ${kind} list or signed source ID`);
    await mountStaticHistory(stage, { kind, historyID: '7' });
    if (!stage.querySelector('[data-static-history-id="7"]')) throw new Error(`missing ${kind} detail`);
  }
  await mountStaticHistory(stage, { kind: 'CycleVersion', parentID: '7' });
  if (calls.at(-1).url.searchParams.get('strategy_history_id') !== '7') throw new Error('cycle version parent filter was not sent');
  await mountStaticHistory(stage, { kind: 'CycleDocument', parentID: '7' });
  if (calls.at(-1).url.searchParams.get('version_history_id') !== '7') throw new Error('cycle document parent filter was not sent');
  await mountStaticHistory(stage, { kind: 'CycleVersion', historyID: '7' });
  if (!stage.querySelector('[data-static-history-child="CycleDocument"]') || stage.innerHTML.includes('<title>') || stage.querySelector('input,textarea') || Array.from(stage.querySelectorAll('button')).some((button) => /启用|执行|发送|Provider/.test(button.textContent))) throw new Error('static history detail boundary failed');
  await mountStaticHistory(stage, { kind: 'ProductPageSlice' }); stage.querySelector('[data-static-history-next]').click(); await new Promise((resolve) => setTimeout(resolve, 0));
  if (calls.at(-1).url.searchParams.get('offset') !== '20' || !stage.textContent.includes('#21')) throw new Error('static history pagination failed');
  await mountStaticHistory(stage, { kind: 'CycleMetric', historyID: '7' });
  if (!stage.textContent.includes('original') || stage.textContent.includes('摘要 020202')) throw new Error('metric JSON was rendered as a digest');
  await mountStaticHistory(stage, { kind: 'CycleReference', historyID: '7' });
  if (stage.innerHTML.includes('private.example') || Array.from(stage.querySelectorAll('a')).some((link) => link.href.includes('private.example'))) throw new Error('reference href leaked or became a link');
  invalid = 'private'; await mountStaticHistory(stage, { kind: 'CycleReference' });
  if (!stage.querySelector('[role="alert"]') || stage.innerHTML.includes('private.example')) throw new Error('private reference field did not fail closed');
  invalid = 'missing'; await mountStaticHistory(stage, { kind: 'CycleMetric' });
  if (!stage.querySelector('[role="alert"]')) throw new Error('missing metric field did not fail closed');
  invalid = '';
  empty = true; await mountStaticHistory(stage, { kind: 'CycleReference' });
  if (!stage.textContent.includes('暂无历史记录')) throw new Error('empty cycle reference page was not rendered');
  empty = false;
  fail = true; await mountStaticHistory(stage, { kind: 'GroupInvite' });
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('NO')) throw new Error('failed page retained old or raw data');
  if (!calls.some(({ url }) => url.pathname === '/api/admin/static-history/cycle-metrics' && url.searchParams.get('limit') === '20' && url.searchParams.get('offset') === '0') || !calls.some(({ url }) => url.pathname === '/api/admin/static-history/cycle-references/7')) throw new Error('cycle observation generated GET parameters were not used');
  if (calls.length < 10 || !calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('static history made a non-GET request');
  console.log('static-history-e2e: PASS');
} finally { globalThis.fetch = saved.fetch; Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location }); dom.window.close(); fs.rmSync(out, { recursive: true, force: true }); }
