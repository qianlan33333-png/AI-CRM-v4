import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const output = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-radar-marketing-history-'));
const entry = path.join(root, 'src/admin/sections/radarMarketingHistory.ts');
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window };
let passed = 0;
const ok = (value, message) => { if (!value) throw new Error(message); passed++; };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

const at = '2026-08-28T01:02:03.123456Z';
const radarClick = (id) => ({
  id, source_id: 0, link_source_id: -7, radar_link_id: null, customer_id: null,
  code: '<img src=x onerror=alert(1)>', raw_stage: 'legacy', source_channel: '',
  target_type_snapshot: 'article', source_channel_snapshot: 'manual', error_code: '', created_at: at,
});
const config = (id) => ({
  id, source_id: -9, automation_key: 'legacy-key', automation_name: '旧配置', target_event: 'answer',
  channel_type: 'qrcode', original_status: 'disabled', do_not_start_after_hour: -1, created_at: at, updated_at: at,
});
const rule = (id) => ({
  id, source_id: -11, config_id: 7, config_source_id: 0, questionnaire_source_id: null, question_source_id: null,
  rule_code: 'legacy-rule', rule_name: '旧规则', answer_match_type: 'exact', score_delta: -3,
  segment_hint: '', stage_hint: '', original_active: false, sort_order: -1, created_at: at, updated_at: at,
});
const kindFor = (pathname) => pathname.includes('radar-click-history') ? 'radar_click' : pathname.includes('/configs') ? 'marketing_config' : 'marketing_rule';
const factFor = (kind, id) => kind === 'radar_click' ? radarClick(id) : kind === 'marketing_config' ? config(id) : rule(id);

try {
  await build({ entryPoints: [entry], bundle: true, platform: 'node', format: 'esm', outdir: output, logLevel: 'warning' });
  const { mountRadarMarketingHistory } = await import(pathToFileURL(path.join(output, 'radarMarketingHistory.js')).href);
  const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/radar.html?click_history=1', pretendToBeVisual: true });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  const stage = dom.window.document.querySelector('#stage');
  const calls = [];
  let mode = 'ok';
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ path: url.pathname, query: url.search, method: init.method, body: init.body, credentials: init.credentials });
    if (mode === 'fail') return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
    const kind = kindFor(url.pathname);
    const detail = /\/[1-9]\d*$/.test(url.pathname);
    const id = Number(url.pathname.split('/').at(-1));
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    const item = factFor(kind, detail ? id : offset + 1);
    const privateField = (value) => {
      if (mode === 'private') value.private_payload = 'must-not-render';
      return value;
    };
    const body = {
      source: 'v1_history', read_only: true, real_external_call_executed: false,
      ...(detail ? { item: privateField(item) } : { items: mode === 'empty' ? [] : Array.from({ length: Math.min(limit, Math.max(0, 21 - offset)) }, (_, index) => privateField(factFor(kind, offset + index + 1))), total: mode === 'empty' ? 0 : 21, limit, offset }),
    };
    return new Response(JSON.stringify(body), { status: 200 });
  };

  await mountRadarMarketingHistory(stage, { kind: 'radar_click' });
  ok(stage.querySelectorAll('details').length === 20 && stage.textContent.includes('封存历史') && !stage.querySelector('img'), 'radar list escapes source text and preserves nullable source relations');
  stage.querySelector('[data-history-next]').click();
  await sleep(10);
  ok(calls.at(-1).path === '/api/admin/radar-click-history' && calls.at(-1).query === '?offset=20&limit=20', 'radar pagination uses the generated offset and limit');

  await mountRadarMarketingHistory(stage, { kind: 'marketing_config' });
  ok(stage.textContent.includes('V1状态（非当前执行状态）') && stage.textContent.includes('disabled'), 'marketing config retains original status without treating it as current execution');
  await mountRadarMarketingHistory(stage, { kind: 'marketing_rule' });
  ok(stage.textContent.includes('V1启用标记（不启用V2自动化）') && stage.textContent.includes('false'), 'marketing rule retains original active flag without enabling automation');

  await mountRadarMarketingHistory(stage, { kind: 'radar_click', historyID: '2' });
  await mountRadarMarketingHistory(stage, { kind: 'marketing_config', historyID: '3' });
  await mountRadarMarketingHistory(stage, { kind: 'marketing_rule', historyID: '4' });
  const expected = new Set([
    '/api/admin/radar-click-history', '/api/admin/radar-click-history/2',
    '/api/admin/marketing-config-history/configs', '/api/admin/marketing-config-history/configs/3',
    '/api/admin/marketing-config-history/rules', '/api/admin/marketing-config-history/rules/4',
  ]);
  ok([...expected].every((path) => calls.some((call) => call.path === path)) && calls.every((call) => call.method === 'GET' && call.body === undefined && call.credentials === 'include'), 'all six generated history GETs keep same-origin credentials and never submit a body');
  ok(![...stage.querySelectorAll('button')].some((button) => /执行|启动|启用|发送|保存/.test(button.textContent)), 'detail exposes no execution action');

  mode = 'empty';
  await mountRadarMarketingHistory(stage, { kind: 'radar_click' });
  ok(stage.textContent.includes('暂无历史记录'), 'empty history page is rendered explicitly');

  mode = 'private';
  await mountRadarMarketingHistory(stage, { kind: 'marketing_config' });
  ok(stage.querySelector('[role="alert"]')?.textContent.includes('未显示旧数据，也未回退 Mock') && !stage.textContent.includes('must-not-render'), 'unexpected private fields fail closed without a mock fallback');

  mode = 'fail';
  await mountRadarMarketingHistory(stage, { kind: 'marketing_rule' });
  ok(stage.querySelector('[role="alert"]')?.textContent.includes('未显示旧数据，也未回退 Mock') && !stage.querySelector('details'), 'HTTP failure clears old rows and stays read-only');
  mode = 'ok';
  stage.querySelector('[data-history-retry]').click();
  await sleep(10);
  ok(stage.querySelectorAll('details').length === 20, 'retry reloads the generated list request');
  console.log(`radar-marketing-history DOM checks: ${passed}`);
} finally {
  globalThis.fetch = saved.fetch;
  globalThis.document = saved.document;
  globalThis.window = saved.window;
  fs.rmSync(output, { recursive: true, force: true });
}
