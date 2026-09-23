import assert from 'node:assert/strict';
import { webcrypto } from 'node:crypto';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/sections/questionnaireEditor.ts'));
const template = fs.readFileSync(path.join(root, 'dist/admin/questionnaireDetail.html'), 'utf8')
  .replace(/<script[^>]*src="[^"]+"[^>]*><\/script>/g, '');
const waitFor = async (check, label) => {
  for (let attempt = 0; attempt < 60; attempt += 1) {
    if (check()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(label);
};
const questionnaire = (overrides = {}) => ({
  id: 7,
  name: '问卷名称',
  title: '问卷标题',
  description: '',
  answer_display_mode: 'all_in_one',
  assessment_enabled: false,
  assessment_config: {},
  slug: 'survey-name',
  is_disabled: false,
  enabled: true,
  status: 'active',
  version: 4,
  questions: [{ id: 1, type: 'textarea', title: '需求', required: true, sort_order: 0, options: [] }],
  score_rules: [],
  ...overrides,
});

const calls = [];
let detailReads = 0;
const dom = new JSDOM(template, {
  url: 'https://test.invalid/admin/questionnaireDetail.html?id=7',
  runScripts: 'outside-only',
  pretendToBeVisual: true,
  beforeParse(window) {
    Object.defineProperty(window, 'crypto', { value: webcrypto, configurable: true });
    window.Response = Response;
    window.Headers = Headers;
    window.Request = Request;
    window.fetch = async (input, init = {}) => {
      const url = new URL(typeof input === 'string' ? input : input.url, window.location.href);
      const method = (init.method || 'GET').toUpperCase();
      calls.push({ method, path: url.pathname, body: init.body || '' });
      if (url.pathname === '/api/admin/wecom/tags') return new Response(JSON.stringify({ items: [] }), { status: 200 });
      if (url.pathname === '/api/admin/questionnaires' && method === 'GET') return new Response(JSON.stringify({ items: [questionnaire()] }), { status: 200 });
      if (url.pathname === '/api/admin/questionnaires/7' && method === 'GET') {
        detailReads += 1;
        return new Response(JSON.stringify({ questionnaire: detailReads > 1 ? questionnaire({ version: 6 }) : questionnaire() }), { status: 200 });
      }
      if (url.pathname === '/api/admin/questionnaires/7' && method === 'PUT') return new Response(JSON.stringify({ questionnaire: questionnaire({ enabled: false, status: 'disabled', version: 5 }) }), { status: 200 });
      if (url.pathname === '/api/admin/questionnaires/7/public-publish' && method === 'POST') return new Response(JSON.stringify({ questionnaire: questionnaire({ version: 6 }) }), { status: 200 });
      return new Response(JSON.stringify({ message: `unexpected ${method} ${url.pathname}` }), { status: 500 });
    };
  },
});

try {
  dom.window.eval(bundle);
  await waitFor(() => dom.window.document.getElementById('field-name')?.value === '问卷名称', 'editor did not load the questionnaire');
  assert.equal(dom.window.document.getElementById('editor-share-btn'), null, 'editor must not show a share action');
  const title = dom.window.document.getElementById('field-title');
  title.value = '更新后的问卷标题';
  title.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
  dom.window.document.getElementById('save-btn').click();
  await waitFor(() => calls.some((call) => call.path.endsWith('/public-publish')), `published questionnaire save did not explicitly publish: ${JSON.stringify(calls)}`);
  const publish = calls.find((call) => call.path.endsWith('/public-publish'));
  assert.equal(JSON.parse(publish.body).expected_questionnaire_version, 5, 'publish must use the exact version returned by save');
  await waitFor(() => detailReads >= 2, 'published questionnaire save did not read back the published version');
  assert.equal(calls.filter((call) => call.path.endsWith('/public-publish')).length, 1, 'one save must issue one publish');

  const disabled = dom.window.document.getElementById('field-is-disabled');
  disabled.checked = true;
  disabled.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  dom.window.document.getElementById('save-btn').click();
  await waitFor(() => calls.filter((call) => call.method === 'PUT' && call.path === '/api/admin/questionnaires/7').length === 2, 'disabled save did not persist its draft');
  await new Promise((resolve) => setTimeout(resolve, 20));
  assert.equal(calls.filter((call) => call.path.endsWith('/public-publish')).length, 1, 'an explicit disable must not re-publish the questionnaire');
} finally {
  dom.window.close();
}

console.log('questionnaire editor publish lifecycle: PASS');
