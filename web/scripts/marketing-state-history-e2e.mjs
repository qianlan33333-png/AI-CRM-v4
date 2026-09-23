import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-marketing-state-history-'));
const output = path.join(out, 'marketingStateHistory.js');
await build({ entryPoints: [path.join(root, 'src/admin/sections/marketingStateHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const { mountMarketingStateHistory } = await import(pathToFileURL(output).href);
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/config.html?marketing_state_history=1&history_kind=state_snapshot', pretendToBeVisual: true });
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
const stamp = '2026-08-28T01:02:03.123456Z';
const fields = {
  state_snapshot: { source_id: -9, automation_key: '', main_stage: '', sub_stage: '', activated: false, converted: false, eligible_for_conversion: false, lifecycle_status: '', last_activation_at: 'source text', last_conversion_marked_at: '', last_message_at: '', last_batch_status: '', last_batch_window_start: '', last_batch_window_end: '', last_trigger_message_at: '', entered_at: null, exited_at: null, exit_reason: '', created_at: stamp, updated_at: stamp },
  state_change: { source_id: -9, automation_key: '', main_stage: '', sub_stage: '', activated: false, converted: false, eligible_for_conversion: false, lifecycle_status: '', last_activation_at: '', last_conversion_marked_at: '', last_message_at: '', exit_reason: '', change_reason: '', recorded_at: stamp, created_at: stamp },
  value_snapshot: { source_id: -9, segment: '', segment_rank: -2147483648, score: 0, scoring_version: '', computed_reason: '', evaluated_at: stamp, computed_at: stamp, created_at: stamp, updated_at: stamp },
  value_change: { source_id: -9, segment: '', segment_rank: 2147483647, score: 0, scoring_version: '', change_reason: '', evaluated_at: stamp, recorded_at: stamp, created_at: stamp },
};
const endpoints = { state_snapshot: 'state-snapshots', state_change: 'state-changes', value_snapshot: 'value-snapshots', value_change: 'value-changes' };
const calls = []; let fail = false, privateLeak = false;
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid'); calls.push({ url, init });
  const kind = Object.keys(endpoints).find((key) => url.pathname.endsWith(endpoints[key]) || url.pathname.includes(`/${endpoints[key]}/`));
  if (!kind) throw new Error(`unexpected endpoint ${url.pathname}`);
  const part = url.pathname.split('/').pop(), detail = /^\d+$/.test(part), offset = Number(url.searchParams.get('offset') ?? '0');
  const item = { id: 7, ...fields[kind] };
  const body = fail ? '{"raw":"NO"}' : JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, ...(detail ? { item: privateLeak ? { ...item, unionid: 'private' } : item } : { items: offset === 0 ? Array.from({ length: 20 }, (_, index) => ({ ...item, id: index + 1 })) : [{ ...item, id: 21 }], total: 21, limit: 20, offset }) });
  return new Response(body, { status: fail ? 503 : 200 });
};
try {
  const stage = document.querySelector('#stage');
  for (const kind of Object.keys(endpoints)) {
    await mountMarketingStateHistory(stage, { kind });
    if (!stage.querySelector(`[data-marketing-state-history-kind="${kind}"]`)) throw new Error(`missing ${kind} navigation`);
    await mountMarketingStateHistory(stage, { kind, historyID: '7' });
    if (!stage.querySelector('[data-marketing-state-history-id="7"]')) throw new Error(`missing ${kind} detail`);
  }
  await mountMarketingStateHistory(stage, { kind: 'state_snapshot' });
  if (!stage.textContent.includes('-9') || !stage.textContent.includes('false') || !stage.textContent.includes('（空字符串）') || !stage.textContent.includes('NULL') || !stage.textContent.includes('source text')) throw new Error('signed, false, nullable, empty, or source text fields were changed');
  await mountMarketingStateHistory(stage, { kind: 'value_snapshot' });
  if (!stage.textContent.includes('-2147483648') || !stage.textContent.includes('0')) throw new Error('int32 rank or score were not preserved');
  await mountMarketingStateHistory(stage, { kind: 'value_change' }); stage.querySelector('[data-marketing-state-history-next]').click(); await new Promise((resolve) => setTimeout(resolve, 0));
  if (calls.at(-1).url.searchParams.get('offset') !== '20' || !stage.textContent.includes('#21')) throw new Error('pagination failed');
  privateLeak = true; await mountMarketingStateHistory(stage, { kind: 'state_snapshot', historyID: '7' });
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('private')) throw new Error('private response field was rendered');
  privateLeak = false; fail = true; await mountMarketingStateHistory(stage, { kind: 'state_change' });
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('NO')) throw new Error('failed page retained old or raw data');
  await mountMarketingStateHistory(stage, { kind: 'value_snapshot', historyID: '01' });
  if (!stage.querySelector('[role="alert"]')) throw new Error('invalid history ID was accepted');
  if (calls.length < 8 || !calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('marketing state history made a non-GET request');
  console.log('marketing-state-history-e2e: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location });
  dom.window.close(); fs.rmSync(out, { recursive: true, force: true });
}
