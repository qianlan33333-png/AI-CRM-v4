import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-campaign-definition-history-'));
const adapterOutput = path.join(out, 'adapter.js');
const sectionOutput = path.join(out, 'section.js');
await build({ entryPoints: [path.join(root, 'src/api/campaignDefinitionHistory.test.ts')], bundle: true, platform: 'node', format: 'esm', outfile: adapterOutput, logLevel: 'warning' });
const { runCampaignDefinitionHistoryAdapterTests } = await import(pathToFileURL(adapterOutput).href);
await runCampaignDefinitionHistoryAdapterTests();
await build({ entryPoints: [path.join(root, 'src/admin/sections/campaignDefinitionHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: sectionOutput, logLevel: 'warning' });
const { mountCampaignDefinitionHistory } = await import(pathToFileURL(sectionOutput).href);
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/campaigns.html?definition_history=1', pretendToBeVisual: true });
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
const stamp = '2026-08-28T01:02:03.123456Z';
const definition = { id: 7, source_id: -9, code: '<old>', display_name: '', intent: '', anchor_mode: '', anchor_date: '', review_status: 'legacy', run_status: 'old', approved_at: null, started_at: null, finished_at: null, paused_at: null, paused_reason: '', created_at: stamp, updated_at: stamp, original_disposition: 'archive', original_reason: 'old' };
const step = { id: 8, source_id: 0, campaign_source_id: -9, segment_source_id: -3, history_definition_id: null, current_campaign_code: null, source_parent_state: 'unresolved_definition', step_index: -1, day_offset: -2, send_time: '', timezone: '', content_masked: '<old content>', stop_on_reply: false, skip_recent_days: -4, created_at: stamp, updated_at: stamp, original_disposition: 'quarantine', original_reason: 'missing' };
const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
const calls = []; let fail = false;
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid'); calls.push({ url, init });
  const detail = /\/definitions\/\d+$/.test(url.pathname);
  const steps = url.pathname.endsWith('/definition-steps');
  const offset = Number(url.searchParams.get('offset') ?? '0');
  const items = offset === 0 ? Array.from({ length: 20 }, (_, index) => ({ ...definition, id: index + 1 })) : [{ ...definition, id: 21 }];
  const stepItems = [step, { ...step, id: 9, source_parent_state: 'current_definition', current_campaign_code: 'old-code' }];
  const body = fail ? '{"raw":"NO"}' : JSON.stringify({ ...safety, ...(detail ? { item: definition } : steps ? { items: stepItems, total: 2, limit: 20, offset } : { items, total: 21, limit: 20, offset }) });
  return new Response(body, { status: fail ? 503 : 200 });
};
try {
  const stage = document.querySelector('#stage');
  await mountCampaignDefinitionHistory(stage);
  if (!stage.textContent.includes('-9') || !stage.textContent.includes('当前 code（仅源关联）：old-code') || stage.innerHTML.includes('<old>') || !stage.querySelector('[data-campaign-definition-next]') || !calls.some(({ url }) => url.pathname.endsWith('/definition-steps') && !url.searchParams.has('campaign_source_id')) || stage.querySelector('input,textarea') || Array.from(stage.querySelectorAll('button')).some((button) => /启动|审批|发送|重发|Provider/.test(button.textContent))) throw new Error('definition history list boundary failed');
  stage.querySelector('[data-campaign-definition-next]').click(); await new Promise((resolve) => setTimeout(resolve, 0));
  if (!stage.textContent.includes('21')) throw new Error('definition history pagination failed');
  dom.reconfigure({ url: 'https://test.invalid/admin/campaigns.html?definition_history=1&history_id=7' });
  await mountCampaignDefinitionHistory(stage);
  if (!stage.querySelector('[data-campaign-definition-id="7"]') || !stage.textContent.includes('unresolved_definition') || stage.innerHTML.includes('<old content>') || !calls.some(({ url }) => url.pathname.endsWith('/definition-steps') && url.searchParams.get('campaign_source_id') === '-9')) throw new Error('definition detail or signed parent filter failed');
  fail = true; await mountCampaignDefinitionHistory(stage);
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('NO')) throw new Error('definition history failure retained old data');
  if (!calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('definition history made a non-GET request');
  console.log('campaign-definition-history-e2e: PASS');
} finally { globalThis.fetch = saved.fetch; Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location }); dom.window.close(); fs.rmSync(out, { recursive: true, force: true }); }
