import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const output = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-legacy-marketing-history-'));
const entry = path.join(root, 'src/admin/sections/legacyMarketingHistory.ts');
const dist = path.join(root, 'dist');
const pageFile = path.join(dist, 'admin/automation.html');
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window };
let passed = 0;
const ok = (value, message) => { if (!value) throw new Error(message); passed++; };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

try {
  await build({ entryPoints: [entry], bundle: true, platform: 'node', format: 'esm', outdir: output, logLevel: 'warning' });
  const { renderLegacyMarketingHistory, legacyMarketingHistoryApi } = await import(pathToFileURL(path.join(output, 'legacyMarketingHistory.js')).href);
  const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/automation.html?legacy_marketing_history=1', pretendToBeVisual: true });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  const stage = dom.window.document.querySelector('#stage');
  const calls = [];
  let fail = false;
  const at = '2026-08-28T01:02:03.123456Z';
  const state = (id) => ({ id, source_id: -id, scenario_key: ' legacy ', marketing_phase: '', phase_label: '待观察', phase_reason: '', lifecycle_status: 'paused', last_batch_status: '', last_batch_window_start: '', last_batch_window_end: '', last_trigger_message_at: '', entered_at: null, exited_at: at, exit_reason: '', created_at: at, updated_at: at });
  const value = (id) => ({ id, source_id: -id, scenario_key: ' legacy ', value_segment: 'high', segment_label: '高价值', score: -3, created_at: at, updated_at: at });
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ path: url.pathname, query: url.search, method: init.method, body: init.body });
    if (fail) return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
    const detail = /^\/api\/admin\/legacy-marketing-history\/(states|values)\/[1-9]\d*$/.exec(url.pathname);
    const kind = url.pathname.includes('/states') ? 'state' : 'value';
    const make = kind === 'state' ? state : value;
    if (detail) return new Response(JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, item: make(Number(url.pathname.split('/').at(-1))) }), { status: 200 });
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    const items = Array.from({ length: Math.min(limit, Math.max(0, 21 - offset)) }, (_, index) => make(offset + index + 1));
    return new Response(JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, items, total: 21, limit, offset }), { status: 200 });
  };
  await renderLegacyMarketingHistory(stage, legacyMarketingHistoryApi);
  ok(calls.length === 0 && stage.textContent.includes('不会自动加载') && ![...stage.querySelectorAll('button')].some((button) => /保存|执行|激活/.test(button.textContent)), 'initial page is manual and read-only');
  stage.querySelector('[data-legacy-marketing-load="state"]').click();
  await sleep(10);
  ok(calls.at(-1).path === '/api/admin/legacy-marketing-history/states' && calls.at(-1).query === '?limit=20&offset=0' && stage.textContent.includes('V1 source #-1') && stage.textContent.includes('（空字符串）'), 'state list uses generated GET and preserves signed/empty source facts');
  stage.querySelector('[data-legacy-marketing-next]').click();
  await sleep(10);
  ok(calls.at(-1).path === '/api/admin/legacy-marketing-history/states' && calls.at(-1).query === '?limit=20&offset=20' && calls.every((call) => call.method === 'GET' && call.body === undefined), 'state pagination remains GET only');
  stage.querySelector('[data-legacy-marketing-detail]').click();
  await sleep(10);
  ok(calls.at(-1).path === '/api/admin/legacy-marketing-history/states/21' && stage.textContent.includes('详情 #21') && stage.textContent.includes('NULL（源未记录）'), 'state detail uses exact history ID and retains NULL');
  stage.querySelector('[data-legacy-marketing-load="value"]').click();
  await sleep(10);
  ok(calls.at(-1).path === '/api/admin/legacy-marketing-history/values' && stage.textContent.includes('score=-3') && stage.textContent.includes('高价值'), 'value list uses the separate generated HTTP endpoint');
  fail = true;
  stage.querySelector('[data-legacy-marketing-load="state"]').click();
  await sleep(10);
  ok(stage.querySelector('[role="alert"]')?.textContent.includes('未显示历史数据，也未回退 Mock') && !stage.textContent.includes('高价值'), 'failed reads clear prior content without Mock fallback');
  const boot = async (mode = 'normal') => {
    if (!fs.existsSync(pageFile)) throw new Error('run npm run build before legacy marketing HTTP e2e');
    const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
    const html = fs.readFileSync(pageFile, 'utf8').replace(/<script type="module" src="\.\.\/assets\/admin-[^"]+\.js"><\/script>/, () => `<script>${bundle}</script>`);
    const bootCalls = [];
    const bootDOM = new JSDOM(html, {
      url: 'http://localhost/admin/automation.html?legacy_marketing_history=1', runScripts: 'dangerously', pretendToBeVisual: true,
      beforeParse(window) {
        window.Headers = Headers;
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          bootCalls.push({ path: url.pathname, query: url.search, method: init.method, body: init.body });
          if (mode === 'failure') return { status: 503, headers: new Headers(), text: async () => JSON.stringify({ code: 'unavailable' }) };
          const limit = Number(url.searchParams.get('limit'));
          const offset = Number(url.searchParams.get('offset'));
          return { status: 200, headers: new Headers(), text: async () => JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: Array.from({ length: Math.min(limit, Math.max(0, 21 - offset)) }, (_, index) => state(offset + index + 1)), total: 21, limit, offset }) };
        };
      },
    });
    await sleep(20);
    return { bootDOM, bootCalls };
  };
  {
    const { bootDOM, bootCalls } = await boot();
    const bootStage = bootDOM.window.document.querySelector('#stage');
    bootStage.querySelector('[data-legacy-marketing-load="state"]').click();
    await sleep(20);
    ok(bootDOM.window.document.body.getAttribute('data-page') === 'automation' && bootStage.querySelector('[data-legacy-marketing-history]') && bootCalls.length === 1 && bootCalls[0].path === '/api/admin/legacy-marketing-history/states' && bootCalls[0].query === '?limit=20&offset=0', 'automation boot enters the read-only module and its manual load uses the generated GET');
    bootDOM.window.close();
  }
  {
    const { bootDOM, bootCalls } = await boot('failure');
    const bootStage = bootDOM.window.document.querySelector('#stage');
    bootStage.querySelector('[data-legacy-marketing-load="state"]').click();
    await sleep(20);
    ok(bootStage.querySelector('[data-legacy-marketing-history]') && bootStage.querySelector('[role="alert"]')?.textContent.includes('未显示历史数据，也未回退 Mock') && !bootStage.querySelector('[data-automation-history]') && bootCalls.length === 1, '503 remains in the legacy read-only module without current-controller or Mock fallback');
    bootDOM.window.close();
  }
  console.log(`legacy-marketing-history DOM checks: ${passed}`);
} finally {
  globalThis.fetch = saved.fetch;
  globalThis.document = saved.document;
  globalThis.window = saved.window;
  fs.rmSync(output, { recursive: true, force: true });
}
