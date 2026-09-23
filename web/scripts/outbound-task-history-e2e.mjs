import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const out = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-outbound-task-history-'));
const output = path.join(out, 'outboundTaskHistory.js');
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
const sleep = () => new Promise((resolve) => setTimeout(resolve, 0));

try {
  await build({ entryPoints: [path.join(root, 'src/admin/sections/outboundTaskHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
  const { mountOutboundTaskHistory } = await import(pathToFileURL(output).href);
  const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/automation.html?outbound_task_history=1', pretendToBeVisual: true });
  Object.assign(globalThis, { document: dom.window.document, window: dom.window, location: dom.window.location });
  const stage = document.querySelector('#stage');
  const stamp = '2026-08-28T01:02:03.123456Z';
  const calls = []; let fail = false; let privateField = false; let empty = false;
  const task = (id) => ({ id, source_id: id === 1 ? -4 : id === 2 ? 0 : id + 100, task_type: 'legacy_task', status: 'queued', created_at: stamp, broadcast_job_history_id: id === 2 ? null : id + 40 });
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(String(input), 'https://test.invalid'); calls.push({ url, init });
    if (fail) return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
    const detail = /^\/api\/admin\/outbound-task-history\/[1-9]\d*$/.test(url.pathname);
    const id = Number(url.pathname.split('/').at(-1)); const limit = Number(url.searchParams.get('limit')); const offset = Number(url.searchParams.get('offset'));
    const item = privateField ? { ...task(detail ? id : 1), provider_token: 'private-should-never-render' } : task(detail ? id : 1);
    const body = { source: 'v1_history', read_only: true, real_external_call_executed: false, ...(detail ? { item } : { items: empty ? [] : Array.from({ length: Math.min(limit, Math.max(0, 51 - offset)) }, (_, index) => task(offset + index + 1)), total: empty ? 0 : 51, limit, offset }) };
    return new Response(JSON.stringify(body), { status: 200 });
  };

  await mountOutboundTaskHistory(stage);
  if (stage.querySelectorAll('tbody tr').length !== 50 || !stage.textContent.includes('原状态不代表本次发送、重试或外部效果') || [...stage.querySelectorAll('button')].some((button) => /发送|重试/.test(button.textContent))) throw new Error('list is not read-only or does not use the default page size');
  const firstParent = stage.querySelector('[data-outbound-task-history-parent]');
  if (firstParent?.getAttribute('href') !== 'automation.html?broadcast_job_history=1&history_id=41' || !stage.textContent.includes('源任务 #-4') || !stage.textContent.includes('源任务 #0') || !stage.textContent.includes('无已验证的群发任务历史关联')) throw new Error('signed source ID or parent relation was not preserved');
  stage.querySelector('[data-outbound-task-history-next]').click(); await sleep();
  if (calls.at(-1).url.pathname !== '/api/admin/outbound-task-history' || calls.at(-1).url.search !== '?limit=50&offset=50' || !stage.textContent.includes('历史 #51')) throw new Error('pagination did not request the generated list endpoint');
  await mountOutboundTaskHistory(stage, { historyID: '7' });
  if (!stage.querySelector('[data-outbound-task-history-id="7"]') || !stage.textContent.includes('源任务 #107') || calls.at(-1).url.pathname !== '/api/admin/outbound-task-history/7' || stage.querySelector('[data-outbound-task-history-parent]')?.getAttribute('href') !== 'automation.html?broadcast_job_history=1&history_id=47') throw new Error('detail did not use exact generated endpoint or parent link');
  privateField = true; await mountOutboundTaskHistory(stage, { historyID: '7' });
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('private-should-never-render')) throw new Error('private response field was rendered');
  privateField = false; empty = true; await mountOutboundTaskHistory(stage);
  if (!stage.textContent.includes('暂无历史记录')) throw new Error('empty result was not rendered');
  empty = false; fail = true; await mountOutboundTaskHistory(stage);
  if (!stage.querySelector('[role="alert"]') || stage.textContent.includes('unavailable') || stage.textContent.includes('历史 #')) throw new Error('failed result retained raw or fallback data');
  if (!calls.every(({ init }) => init.method === 'GET' && init.credentials === 'include' && init.body === undefined)) throw new Error('history UI made a non-GET request');
  let html = fs.readFileSync(path.join(root, 'dist', 'admin/automation.html'), 'utf8');
  const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
  const bootCalls = [];
  const boot = new JSDOM(html, {
    url: 'http://localhost/admin/automation.html?outbound_task_history=1',
    runScripts: 'outside-only',
    pretendToBeVisual: true,
    beforeParse(window) {
      window.__AICRM_TEST_MOCK__ = false;
      window.Headers = Headers;
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        bootCalls.push({ path: url.pathname, query: url.search, method: init.method, body: init.body });
        return { status: 200, headers: new Headers(), text: async () => JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: [task(1)], total: 1, limit: Number(url.searchParams.get('limit')), offset: Number(url.searchParams.get('offset')) }) };
      };
    },
  });
  boot.window.eval(bundle);
  await new Promise((resolve) => setTimeout(resolve, 20));
  const bootStage = boot.window.document.querySelector('#stage');
  if (!bootStage?.querySelector('[data-outbound-task-history]') || bootCalls.length !== 1 || bootCalls[0].path !== '/api/admin/outbound-task-history' || bootCalls[0].query !== '?limit=50&offset=0' || bootCalls[0].method !== 'GET' || bootCalls[0].body !== undefined) throw new Error('built automation boot did not make the expected generated GET');
  boot.window.close();
  console.log('outbound-task-history-e2e: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location });
  fs.rmSync(out, { recursive: true, force: true });
}
