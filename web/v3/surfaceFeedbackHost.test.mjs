import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const temporary = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-surface-feedback-'));
const output = path.join(temporary, 'surface-feedback.js');
try {
  await build({
    entryPoints: [path.join(root, 'web/v3/surfaceFeedbackHost.ts')],
    bundle: true,
    format: 'iife',
    platform: 'browser',
    outfile: output,
    logLevel: 'silent',
  });
  const slowPageTimers = [];
  const dom = new JSDOM('<!doctype html><body data-ui-surface="admin"><main id="stage"></main><p id="confirmed">正在读取页面数据</p><a id="new-tab" href="/next" target="_blank">next</a></body>', {
    url: 'https://crm.example/admin/customers.html', runScripts: 'dangerously', pretendToBeVisual: true,
    beforeParse(window) {
      const original = window.setTimeout.bind(window);
      window.setTimeout = (callback, delay, ...args) => {
        if (delay === 30000) { slowPageTimers.push(callback); return 0; }
        return original(callback, delay, ...args);
      };
    },
  });
  dom.window.eval(fs.readFileSync(output, 'utf8'));
  await new Promise((resolve) => dom.window.setTimeout(resolve, 0));

  const stage = dom.window.document.querySelector('#stage');
  assert.ok(stage.querySelector('.surface-feedback__spinner'), 'an empty stage receives a 24px initial spinner');
  assert.equal(stage.querySelector('.surface-feedback__spinner').getAttribute('aria-hidden'), 'true');
  assert.equal(dom.window.document.querySelector('#confirmed .surface-feedback__spinner'), null, 'business text outside a workspace is never rewritten');
  slowPageTimers[0]();
  assert.match(stage.textContent, /仍未完成加载/);
  assert.equal(stage.querySelector('.surface-feedback__spinner'), null, 'slow initial mount ends its spinner and offers a deliberate reload');
  stage.textContent = '正在读取页面数据…';
  await new Promise((resolve) => dom.window.setTimeout(resolve, 0));
  assert.ok(stage.querySelector('.surface-feedback__spinner'), 'the actual asynchronous donor placeholder becomes a spinner');

  stage.replaceChildren(dom.window.document.createElement('section'));
  await new Promise((resolve) => dom.window.setTimeout(resolve, 0));
  assert.equal(stage.querySelector('.surface-feedback__busy'), null, 'real content clears only its own initial spinner');
  stage.firstChild.innerHTML = '<span>加载中</span><div>正在读取页面数据…</div>';
  await new Promise(resolve => dom.window.setTimeout(resolve, 0));
  assert.equal(stage.querySelector('.surface-feedback__spinner'), null, 'business values that resemble status text are never replaced');

  const rootHandle = dom.window.__AICRMSurfaceFeedback.busy(stage);
  rootHandle.clear();
  await new Promise(resolve => dom.window.setTimeout(resolve, 0));
  assert.equal(stage.children.length, 0, 'explicit root clear must not respawn the spinner');
  const pageTimerCount = slowPageTimers.length;
  stage.innerHTML = '<div id="config-extension-host">正在读取配置…</div>';
  await new Promise(resolve => dom.window.setTimeout(resolve, 0));
  assert.ok(stage.querySelector('.surface-feedback__spinner'));
  assert.equal(slowPageTimers.length, pageTimerCount, 'local readers retain their own retry and draft lifecycle; no generic reload timeout');

  const table = dom.window.document.createElement('table');
  const tableBody = dom.window.document.createElement('tbody');
  const prior = dom.window.document.createElement('tr'); prior.innerHTML = '<td>已确认行</td>'; tableBody.append(prior); table.append(tableBody); stage.replaceChildren(table);
  const tableState = dom.window.__AICRMSurfaceFeedback.tableReadState(tableBody, { state: 'error', message: '读取失败，已保留上次成功数据。', colSpan: 1, preserveRows: true, retry: { run() {} } });
  assert.equal(tableState.dataset.surfaceTableReadState, 'error', 'shared surface feedback exposes the table read-state renderer');
  assert.match(tableBody.textContent, /已确认行/, 'shared error state does not clear retained rows');
  assert.ok(tableState.querySelector('button'), 'shared error state can expose a caller-owned retry action');

  const local = dom.window.document.createElement('div');
  dom.window.document.body.append(local);
  const handle = dom.window.__AICRMSurfaceFeedback.busy(local, { label: '正在保存' });
  assert.match(local.textContent, /正在保存/);
  handle.clear();
  assert.equal(local.querySelector('.surface-feedback__busy'), null, 'the internal busy API clears its local feedback');

  dom.window.document.querySelector('#new-tab').dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, button: 0 }));
  assert.equal(dom.window.document.querySelector('.surface-feedback__navigation'), null, 'modified or targetted navigation never gets a page hint');
  const normal = dom.window.document.createElement('a');
  normal.href = '/admin/products.html';
  dom.window.document.body.append(normal);
  const cancelled = new dom.window.MouseEvent('click', { bubbles: true, button: 0, cancelable: true });
  normal.addEventListener('click', event => event.preventDefault(), { once: true });
  normal.dispatchEvent(cancelled);
  await new Promise((resolve) => dom.window.setTimeout(resolve, 0));
  assert.equal(dom.window.document.querySelector('.surface-feedback__navigation'), null, 'cancelled navigation does not leave a spinner');
  normal.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true, button: 0 }));
  await new Promise((resolve) => dom.window.setTimeout(resolve, 0));
  assert.ok(dom.window.document.querySelector('.surface-feedback__navigation'));
  dom.window.dispatchEvent(new dom.window.Event('pageshow'));
  assert.equal(dom.window.document.querySelector('.surface-feedback__navigation'), null);
  const script = dom.window.document.createElement('script');
  dom.window.document.head.append(script);
  script.dispatchEvent(new dom.window.Event('error'));
  assert.ok(stage.querySelector('[data-surface-resource-error] button'), 'resource failure has an explicit reload action');
  assert.equal(stage.querySelector('.surface-feedback__spinner'), null);
  dom.window.close();
} finally {
  fs.rmSync(temporary, { recursive: true, force: true });
}

console.log('surface feedback host contract passed');
