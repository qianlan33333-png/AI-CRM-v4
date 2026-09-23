import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const output = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-automation-history-'));
const entry = path.join(root, 'src/admin/sections/automationHistory.ts');
const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window };
let passed = 0;
const ok = (value, message) => { if (!value) throw new Error(message); passed++; };
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));

try {
  await build({ entryPoints: [entry], bundle: true, platform: 'node', format: 'esm', outdir: output, logLevel: 'warning' });
  const { mountAutomationHistory } = await import(pathToFileURL(path.join(output, 'automationHistory.js')).href);
  const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/config.html?automation_history=1', pretendToBeVisual: true });
  globalThis.window = dom.window;
  globalThis.document = dom.window.document;
  const stage = dom.window.document.querySelector('#stage');
  const calls = [];
  let fail = false;
  const digest = Array.from({ length: 32 }, (_, index) => index);
  const at = '2026-08-28T01:02:03.123456Z';
  const sop = (id) => ({ id, source_id: id + 100, source_key_digest: digest, source_payload_digest: digest, pool_key: ' legacy ', day_index: -1, content_masked: '<b>已脱敏</b>', images_digest: digest, original_enabled: false, created_at: at, updated_at: at });
  globalThis.fetch = async (input, init = {}) => {
    const url = new URL(String(input), 'https://test.invalid');
    calls.push({ path: url.pathname, query: url.search, method: init.method, body: init.body });
    if (fail) return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503 });
    const id = Number(url.pathname.split('/').at(-1));
    const detail = /^\/api\/admin\/automation-history\/sops\/[1-9]\d*$/.test(url.pathname);
    const limit = Number(url.searchParams.get('limit'));
    const offset = Number(url.searchParams.get('offset'));
    const items = Array.from({ length: Math.min(limit, Math.max(0, 21 - offset)) }, (_, index) => sop(offset + index + 1));
    const body = { source: 'v1_history', read_only: true, real_external_call_executed: false, ...(detail ? { item: sop(id) } : { items, total: 21, limit, offset }) };
    return new Response(JSON.stringify(body), { status: 200 });
  };
  await mountAutomationHistory(stage, { kind: 'sop' });
  ok(stage.querySelectorAll('tbody tr').length === 20 && stage.textContent.includes('不代表当前启用') && ![...stage.querySelectorAll('button')].some((button) => /保存|执行|激活/.test(button.textContent)), 'sop list is read-only and uses 20 rows');
  stage.querySelector('[data-history-next]').click();
  await sleep(10);
  ok(calls.at(-1).path === '/api/admin/automation-history/sops' && calls.at(-1).query === '?limit=20&offset=20' && calls.every((call) => call.method === 'GET' && call.body === undefined), 'pagination remains generated GET only');
  await mountAutomationHistory(stage, { kind: 'sop', historyID: '2' });
  ok(stage.textContent.includes('详情 #2') && stage.textContent.includes('<b>已脱敏</b>') && !stage.querySelector('b') && calls.at(-1).path === '/api/admin/automation-history/sops/2', 'detail escapes masked text and uses exact ID');
  fail = true;
  await mountAutomationHistory(stage, { kind: 'sop' });
  ok(stage.querySelector('[role="alert"]')?.textContent.includes('未显示历史数据') && !stage.textContent.includes('历史记录  #'), 'failure clears old content without fallback');
  fail = false;
  stage.querySelector('[data-history-retry]').click();
  await sleep(10);
  ok(stage.querySelectorAll('tbody tr').length === 20, 'retry reloads the same list page');
  console.log(`automation-history DOM checks: ${passed}`);
} finally {
  globalThis.fetch = saved.fetch;
  globalThis.document = saved.document;
  globalThis.window = saved.window;
  fs.rmSync(output, { recursive: true, force: true });
}
