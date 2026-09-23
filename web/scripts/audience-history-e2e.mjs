import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-audience-history-'));
const output = path.join(out, 'audienceHistory.js');
await build({ entryPoints: [path.join(root, 'src/admin/sections/audienceHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const { mountAudienceHistory } = await import(pathToFileURL(output).href);
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/automation.html?history=1', pretendToBeVisual: true });
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
const stamp = '2026-08-28T01:02:03.123456Z';
const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
const run = { id: 70, package_history_id: 42, version_history_id: null, run_type: 'refresh', original_status: 'done', refresh_started_at: stamp, refresh_finished_at: null, last_watermark_at: null, next_watermark_at: null, returned_count: -1, entered_count: 0, updated_count: 2, exited_count: 3, member_event_count: 4, duration_ms: -5, created_at: stamp };
const event = { id: 71, package_history_id: 42, run_history_id: null, member_history_id: null, event_type: 'entered', occurred_at: stamp, created_at: stamp };
const calls = [];
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid');
  calls.push({ url, init });
  const activity = url.pathname.endsWith('/activity-runs') ? run : url.pathname.endsWith('/activity-member-events') ? event : null;
  return new Response(JSON.stringify({ ...safety, items: activity ? [activity] : [], total: activity ? 1 : 0, limit: Number(url.searchParams.get('limit')), offset: Number(url.searchParams.get('offset')) }), { status: 200 });
};
try {
  const stage = document.querySelector('#stage');
  await mountAudienceHistory(stage);
  const activityRuns = stage.querySelector('[data-history-kind="activity_runs"]');
  const activityEvents = stage.querySelector('[data-history-kind="activity_member_events"]');
  if (!activityRuns?.textContent.includes('-1') || !activityRuns.textContent.includes('NULL（历史关联未确认）') || !activityEvents?.textContent.includes('entered') || activityRuns.innerHTML.includes('source_id') || activityEvents.innerHTML.includes('source_id')) throw new Error('audience activity history safe projection failed');
  if (!calls.some(({ url }) => url.pathname.endsWith('/activity-runs')) || !calls.some(({ url }) => url.pathname.endsWith('/activity-member-events')) || !calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('audience activity history did not use generated read-only GETs');
  console.log('audience-history-e2e: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location });
  dom.window.close();
  fs.rmSync(out, { recursive: true, force: true });
}
