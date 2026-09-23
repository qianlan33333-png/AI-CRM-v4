import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import { existsSync, readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const materializedSrc = path.join(root, 'web/src');
const transform = (html) => html
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
  .replace(/<\/sc-for>/g, '</template>')
  .replace(/<sc-if\s+([^>]*?)value="([^"]*)"([^>]*)>/g, (_match, _a, value) => `<template data-sc-if="${value}">`)
  .replace(/<\/sc-if>/g, '</template>');

const bundle = await build({
  stdin: {
    contents: `import './web/v3/surveyAdapter';
      import { AdminController } from './web/src/admin/controller';
      import { api } from './web/src/shared/api/client';
      import { mount } from './web/src/shared/ui/runtime';
      window.SurveyControllerFixture = AdminController;
      window.SurveyApiFixture = api;
      window.SurveyMountFixture = mount;`,
    resolveDir: root,
    loader: 'ts',
  },
  plugins: [{
    name: 'survey-adapter-materialized-view',
    setup(pluginBuild) {
      pluginBuild.onResolve({ filter: /^\.\.\/src\/admin\/main$/ }, () => ({ path: 'empty-admin-main', namespace: 'survey-test' }));
      pluginBuild.onLoad({ filter: /.*/, namespace: 'survey-test' }, () => ({ contents: 'export {};', loader: 'ts' }));
      pluginBuild.onResolve({ filter: /^\.\.\/src\// }, (args) => {
        const raw = path.join(materializedSrc, args.path.replace('../src/', ''));
        const resolved = ['.ts', '.tsx', '.js'].map((extension) => `${raw}${extension}`).find(existsSync) || raw;
        return { path: resolved };
      });
    },
  }],
  bundle: true,
  format: 'iife',
  platform: 'browser',
  write: false,
  logLevel: 'silent',
});

const dom = new JSDOM('<!doctype html><body data-page="questionnaires"><main id="stage"></main></body>', {
  url: 'https://test.invalid/admin/questionnaires.html',
  runScripts: 'outside-only',
});
const pause = () => new Promise((resolve) => setTimeout(resolve, 0));

try {
  const calls = [];
  let failOnce = true;
  dom.window.Headers = globalThis.Headers;
  Object.defineProperty(dom.window, 'crypto', { value: globalThis.crypto, configurable: true });
  dom.window.fetch = async (url, init = {}) => {
    calls.push({ url: String(url), method: init.method, headers: init.headers, body: init.body });
    if (failOnce) {
      failOnce = false;
      return { ok: false, status: 503, clone: () => ({ json: async () => ({ code: 'temporary_failure' }) }), text: async () => 'temporary_failure' };
    }
    return { ok: true, status: 200 };
  };
  dom.window.eval(bundle.outputFiles[0].text);
  await pause();
  const controller = new dom.window.SurveyControllerFixture({ mode: 'mock' }, 'questionnaires');
  const source = {
    resourceId: 8,
    name: '过期问卷标题',
    internalName: 'Imported questionnaire',
    title: '过期问卷标题',
    version: 4,
    off: false,
    action: 'active',
    created: '2026-09-14T00:00:00Z',
    count: '0',
  };
  controller.db.rows.questionnaires = [source];
  controller.state.questionnaireQuery = 'imported questionnaire';
  let rows = controller.renderVals().rows.questionnaires;
  assert.equal(rows.length, 1, 'management search must use questionnaire name');
  assert.equal(rows[0].name, 'Imported questionnaire', 'management primary label must use questionnaire name');
  assert.equal(source.name, '过期问卷标题', 'bridge must restore the server DTO after rendering');

  controller.state.questionnaireQuery = '过期问卷标题';
  rows = controller.renderVals().rows.questionnaires;
  assert.equal(rows.length, 0, 'management search must not use questionnaire title');

  controller.state.questionnaireQuery = '';
  controller.init = async () => {
    controller.db.rows.questionnaires = [];
    controller.__render?.();
  };
  dom.window.SurveyMountFixture(dom.window.document.getElementById('stage'), transform(readFileSync(path.join(root, 'web/src/admin/templates/questionnaires.html'), 'utf8')), controller);
  const deleteLink = [...dom.window.document.querySelectorAll('a')].find((node) => node.textContent === '删除');
  assert.ok(deleteLink, 'the rendered questionnaire table has the archive action');
  deleteLink.click();
  assert.equal(dom.window.document.getElementById('fb-head')?.textContent, '删除问卷', 'the rendered action opens the owner-visible delete confirmation');
  assert.match(dom.window.document.getElementById('fb-body')?.textContent || '', /过期问卷标题/, 'the confirmation identifies the frozen row');
  dom.window.document.getElementById('fb-ok').click();
  await pause();
  assert.equal(calls.length, 1, 'the first confirmed click sends one archive request');

  const retryLink = [...dom.window.document.querySelectorAll('a')].find((node) => node.textContent === '删除');
  assert.ok(retryLink, 'a recoverable failure keeps the rendered archive action available');
  retryLink.click();
  dom.window.document.getElementById('fb-ok').click();
  await pause();
  assert.equal(calls.length, 2, 'a retry sends exactly one more archive request');
  assert.equal(calls[0].url, '/api/admin/questionnaires/8');
  assert.equal(calls[0].method, 'DELETE');
  assert.equal(calls[0].body, JSON.stringify({ expected_version: 4 }));
  assert.equal(calls[1].body, calls[0].body, 'retry retains the exact frozen CAS body');
  assert.equal(calls[1].headers['Idempotency-Key'], calls[0].headers['Idempotency-Key'], 'retry retains the exact idempotency key');
  assert.equal(dom.window.document.querySelectorAll('tbody tr').length, 0, 'successful readback removes the archived questionnaire from the rendered list');

  const listItem = {
    id: 12, name: 'directory-questionnaire', title: 'Directory questionnaire', status: 'active', is_disabled: false,
    public_path: '/q/directory-questionnaire', assessment_enabled: false, created_at: '2026-09-15T00:00:00Z',
    submission_count: 0, answer_display_mode: 'after_submit', assessment_config: {}, slug: 'directory-questionnaire',
    questions: [], score_rules: [], version: 1,
  };
  let directoryMode = '503';
  let resolveStaleRead;
  dom.window.fetch = async (url, init = {}) => {
    if (String(init.method || '').toUpperCase() === 'DELETE') return new Response(JSON.stringify({ ok: true }), { status: 200 });
    if (new URL(String(url), dom.window.location.href).pathname !== '/api/admin/questionnaires') throw new Error(`unexpected directory request: ${url}`);
    if (directoryMode === '503') return new Response(JSON.stringify({ code: 'temporary_failure' }), { status: 503 });
    if (directoryMode === '401') return new Response(JSON.stringify({ code: 'unauthenticated' }), { status: 401 });
    if (directoryMode === '403') return new Response(JSON.stringify({ code: 'forbidden' }), { status: 403 });
    if (directoryMode === 'malformed') return new Response(JSON.stringify({ items: {} }), { status: 200 });
    if (directoryMode === 'stale') return new Promise(resolve => { resolveStaleRead = resolve; });
    return new Response(JSON.stringify({ items: directoryMode === 'empty' ? [] : [listItem] }), { status: 200 });
  };
  const readController = new dom.window.SurveyControllerFixture(dom.window.SurveyApiFixture, 'questionnaires');
  dom.window.SurveyMountFixture(dom.window.document.getElementById('stage'), transform(readFileSync(path.join(root, 'web/src/admin/templates/questionnaires.html'), 'utf8')), readController);
  await readController.init();
  await pause();
  const initialFailure = dom.window.document.querySelector('[data-surface-table-read-state="error"]');
  assert.match(initialFailure?.textContent || '', /问卷列表暂时无法读取/, 'a fresh 503 renders a recoverable state rather than an empty directory');
  directoryMode = 'empty';
  initialFailure.querySelector('button').click();
  await pause();
  await pause();
  assert.equal(dom.window.document.querySelector('[data-surface-table-read-state="empty"]')?.textContent, '当前暂无问卷，可通过右上角创建新问卷。', 'initial failure retry reuses the directory reader and recovers to the confirmed empty state');

  directoryMode = 'one';
  await readController.init();
  await readController.init();
  readController.state.questionnaireQuery = 'absent';
  readController.__render();
  await pause();
  assert.match(dom.window.document.querySelector('[data-surface-table-read-state="no-match"]')?.textContent || '', /未找到与“absent”匹配/, 'a local filter miss remains distinct from an empty directory');

  readController.state.questionnaireQuery = '';
  readController.__render();
  await pause();
  directoryMode = 'malformed';
  await readController.init();
  await pause();
  assert.match(dom.window.document.querySelector('[data-surface-table-read-state="error"]')?.textContent || '', /暂时无法读取/, 'malformed 2xx cannot be normalized into an empty directory');
  directoryMode = 'one';
  dom.window.document.querySelector('[data-surface-table-read-state="error"] button').click();
  await pause();
  await pause();

  directoryMode = '503';
  await readController.init();
  await pause();
  const temporaryFailure = dom.window.document.querySelector('[data-surface-table-read-state="error"]');
  assert.match(temporaryFailure?.textContent || '', /已保留上次成功加载的当前列表/, 'recoverable failure preserves the last authorized rows');
  assert.ok([...dom.window.document.querySelectorAll('tbody tr')].some(row => row.textContent.includes('directory-questionnaire')), 'recoverable failure keeps the prior directory row visible');
  directoryMode = 'one';
  temporaryFailure.querySelector('button').click();
  await pause();
  await pause();
  assert.equal(dom.window.document.querySelector('[data-surface-table-read-state]'), null, 'retry reuses controller init and clears its error notice after a successful read');

  const confirmArchiveReadFailure = async (mode, label) => {
    directoryMode = mode;
    const archive = [...dom.window.document.querySelectorAll('a')].find(node => node.textContent === '删除');
    assert.ok(archive, `${label} keeps the existing archive control visible before the owner command`);
    archive.click();
    dom.window.document.getElementById('fb-ok').click();
    await pause();
    await pause();
    assert.match(dom.window.document.getElementById('fb-toast')?.textContent || '', /归档已受理，但列表回读失败/, `${label} never converts a failed post-delete readback into a claimed deletion`);
  };
  await confirmArchiveReadFailure('503', '503');
  directoryMode = 'one';
  await readController.init();
  await confirmArchiveReadFailure('401', '401');
  directoryMode = 'one';
  await readController.init();

  directoryMode = '403';
  await readController.init();
  await pause();
  const forbidden = dom.window.document.querySelector('[data-surface-table-read-state="error"]');
  assert.match(forbidden?.textContent || '', /没有查看问卷列表的权限/, '403 clears stale directory data and explains the access boundary');
  assert.equal(forbidden.querySelector('button'), null, 'authorization failure does not offer a retry against an unchanged access boundary');
  assert.equal([...dom.window.document.querySelectorAll('tbody tr')].filter(row => !row.dataset.surfaceTableReadState).length, 0, '403 removes prior authorized rows');

  directoryMode = 'one';
  await readController.init();
  directoryMode = '401';
  await readController.init();
  await pause();
  assert.match(dom.window.document.querySelector('[data-surface-table-read-state="error"]')?.textContent || '', /登录状态已失效/, '401 clears loaded rows with its distinct authentication message');

  directoryMode = 'stale';
  const staleRead = readController.init();
  directoryMode = 'one';
  await readController.init();
  resolveStaleRead(new Response(JSON.stringify({ items: [] }), { status: 200 }));
  await assert.rejects(() => staleRead);
  await pause();
  assert.equal(dom.window.document.querySelector('[data-surface-table-read-state]'), null, 'a late stale read cannot overwrite the latest successful directory state');
  assert.ok([...dom.window.document.querySelectorAll('tbody tr')].some(row => row.textContent.includes('directory-questionnaire')), 'the latest directory result remains visible after a stale response resolves');

  directoryMode = '503';
  const leavingRead = readController.init();
  dom.window.document.body.dataset.page = 'products';
  await leavingRead;
  await pause();
  assert.equal(dom.window.document.querySelector('[data-surface-table-read-state]'), null, 'a failed directory read after leaving questionnaires cannot paint its state into another page');
  dom.window.document.body.dataset.page = 'questionnaires';
} finally {
  dom.window.close();
}

console.log('survey archive DOM action: PASS');
