import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-deferred-identity-history-'));
const dist = path.join(root, 'dist');
const at = '2026-08-29T01:02:03.123456Z';
const people = Array.from({ length: 51 }, (_, index) => ({ id: index + 1, source_id: index ? -index : 0, created_at: at, updated_at: at }));
const conflicts = Array.from({ length: 51 }, (_, index) => ({ id: index + 101, source_id: index ? -index : 0, conflict_type: index ? '' : '<img src=x>', source_type: '', status: '', resolution_status: '', created_at: at, updated_at: at, resolved_at: index ? at : null }));
const missingRoots = Array.from({ length: 51 }, (_, index) => ({ id: index + 201, source_id: index ? -index : 0, quarantine_reason: 'missing_customer_root', type: index ? -index : null, status: '', first_seen_at: at, last_seen_at: at, created_at: at, updated_at: at }));
const calls = [];
let fail = false;
let empty = false;
let privateField = false;
const saved = { fetch: globalThis.fetch, location: globalThis.location };

function assert(value, message) { if (!value) throw new Error(message); }
function response(value, status = 200) { return new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } }); }
function source(pathname) {
  if (pathname.includes('/people')) return people;
  if (pathname.includes('/conflicts')) return conflicts;
  if (pathname.includes('/missing-roots')) return missingRoots;
  return undefined;
}

globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid');
  calls.push({ path: url.pathname, query: url.search, method: init.method, credentials: init.credentials, body: init.body });
  if (fail) return response({ code: 'unavailable' }, 503);
  const rows = source(url.pathname);
  const detail = /^\/api\/admin\/deferred-identity-history\/(people|conflicts|missing-roots)\/[1-9]\d*$/.test(url.pathname);
  const list = /^\/api\/admin\/deferred-identity-history\/(people|conflicts|missing-roots)$/.test(url.pathname);
  if (!rows || (!detail && !list)) return response({ code: 'unexpected' }, 500);
  const id = Number(url.pathname.split('/').at(-1));
  const item = rows.find((row) => row.id === id);
  const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
  if (detail) return item ? response({ ...safety, item: privateField ? { ...item, private_digest: 'must-not-render' } : item }) : response({ code: 'not_found' }, 404);
  const limit = Number(url.searchParams.get('limit'));
  const offset = Number(url.searchParams.get('offset'));
  const body = { ...safety, items: empty ? [] : rows.slice(offset, offset + limit), total: empty ? 0 : rows.length, limit, offset };
  return response(privateField ? { ...body, private_digest: 'must-not-render' } : body);
};

try {
  await build({ entryPoints: { api: path.join(root, 'src/api/deferredIdentityHistory.ts'), section: path.join(root, 'src/admin/sections/deferredIdentityHistory.ts') }, bundle: true, format: 'esm', platform: 'node', outdir, logLevel: 'warning' });
  const api = await import(pathToFileURL(path.join(outdir, 'api.js')).href);
  const section = await import(pathToFileURL(path.join(outdir, 'section.js')).href);

  const personPage = await api.readDeferredPeople();
  const conflictPage = await api.readDeferredIdentityConflicts();
  const missingPage = await api.readMissingRootIdentities();
  assert(personPage.items[0].source_id === 0 && personPage.items[1].source_id === -1, 'person signed/zero source IDs changed');
  assert(conflictPage.items[0].resolved_at === null && missingPage.items[0].type === null && missingPage.items[1].type === -1, 'nullable public values changed');
  await api.getDeferredPersonItem(1);
  await api.getDeferredIdentityConflictItem(101);
  await api.getMissingRootIdentityItem(201);
  assert(calls.slice(0, 6).map((call) => call.path).join('|') === [
    '/api/admin/deferred-identity-history/people',
    '/api/admin/deferred-identity-history/conflicts',
    '/api/admin/deferred-identity-history/missing-roots',
    '/api/admin/deferred-identity-history/people/1',
    '/api/admin/deferred-identity-history/conflicts/101',
    '/api/admin/deferred-identity-history/missing-roots/201',
  ].join('|'), 'adapter did not issue all six generated GET operations');
  assert(calls.every((call) => call.method === 'GET' && call.credentials === 'include' && call.body === undefined), 'adapter issued a non-read-only request');

  privateField = true;
  await api.readDeferredPeople().then(() => { throw new Error('private response field was accepted'); }, (error) => assert(error instanceof Error && error.message.includes('只读契约'), 'private response did not fail closed'));
  privateField = false;

  const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/config.html?deferred_identity_history=1&history_kind=conflicts', pretendToBeVisual: true });
  globalThis.location = dom.window.location;
  const stage = dom.window.document.querySelector('#stage');
  await section.mountDeferredIdentityHistory(stage, { kind: 'conflicts' });
  assert(stage.textContent.includes('不创建客户、不绑定身份、不可执行') && stage.innerHTML.includes('&lt;img src=x&gt;') && !stage.innerHTML.includes('<img src=x>'), 'read-only warning or escaped public source text changed');
  stage.querySelector('[data-deferred-identity-history-next]').click();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert(calls.some((call) => call.path === '/api/admin/deferred-identity-history/conflicts' && call.query === '?offset=50&limit=50'), 'next page did not request offset 50');
  await section.mountDeferredIdentityHistory(stage, { kind: 'missing-roots', historyID: '201' });
  assert(stage.textContent.includes('固定原因') && stage.textContent.includes('missing_customer_root') && calls.at(-1).path === '/api/admin/deferred-identity-history/missing-roots/201', 'missing-root detail did not use the generated detail endpoint');
  empty = true;
  await section.mountDeferredIdentityHistory(stage, { kind: 'people' });
  assert(stage.textContent.includes('暂无 V1 历史记录'), 'empty page was not rendered');
  empty = false;
  fail = true;
  await section.mountDeferredIdentityHistory(stage, { kind: 'people' });
  assert(stage.querySelector('[role="alert"]')?.textContent.includes('未显示旧数据，也未回退 Mock'), 'failed page retained data or used a mock fallback');
  fail = false;

  const html = fs.readFileSync(path.join(dist, 'admin/config.html'), 'utf8');
  const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
  for (const [kind, endpoint] of [['people', 'people'], ['conflicts', 'conflicts'], ['missing-roots', 'missing-roots']]) {
    const bootCalls = [];
    const boot = new JSDOM(html, {
      url: `http://localhost/admin/config.html?deferred_identity_history=1&history_kind=${kind}`,
      runScripts: 'outside-only',
      pretendToBeVisual: true,
      beforeParse(window) {
        window.__AICRM_TEST_MOCK__ = false;
        window.Headers = Headers;
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          bootCalls.push({ path: url.pathname, query: url.search, method: init.method, credentials: init.credentials, body: init.body });
          const rows = source(url.pathname);
          const limit = Number(url.searchParams.get('limit'));
          const offset = Number(url.searchParams.get('offset'));
          return { status: 200, headers: new Headers(), text: async () => JSON.stringify({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: rows.slice(offset, offset + limit), total: rows.length, limit, offset }) };
        };
      },
    });
    boot.window.eval(bundle);
    await new Promise((resolve) => setTimeout(resolve, 20));
    const bootStage = boot.window.document.querySelector('#stage');
    assert(bootStage?.querySelector('[data-deferred-identity-history]') && bootCalls.length === 1 && bootCalls[0].path === `/api/admin/deferred-identity-history/${endpoint}` && bootCalls[0].query === '?offset=0&limit=50' && bootCalls[0].method === 'GET' && bootCalls[0].credentials === 'include' && bootCalls[0].body === undefined, `built config boot did not use only the ${kind} generated GET`);
    boot.window.close();
  }
  console.log('e2e_deferred_identity_history: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  globalThis.location = saved.location;
  fs.rmSync(outdir, { recursive: true, force: true });
}
