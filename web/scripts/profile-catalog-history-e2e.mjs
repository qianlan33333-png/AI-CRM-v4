import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-profile-catalog-history-'));
const output = path.join(outdir, 'profileCatalogHistory.js');
await build({ entryPoints: [path.join(root, 'src/admin/sections/profileCatalogHistory.ts')], bundle: true, platform: 'node', format: 'esm', outfile: output, logLevel: 'warning' });
const {
  mountProfileCatalogHistory,
  readProfileHistoryCategories,
  readProfileHistoryTemplate,
  readProfileHistoryTemplates,
  requireProfileCatalogHistoryID,
} = await import(pathToFileURL(output).href);

function assert(value, message) { if (!value) throw new Error(message); }
async function rejects(call) { try { await call(); } catch { return; } throw new Error('expected failure without fallback'); }
const pause = async () => { await new Promise((resolve) => setTimeout(resolve, 0)); };

const saved = { fetch: globalThis.fetch, document: globalThis.document, window: globalThis.window, location: globalThis.location };
const dom = new JSDOM('<main id="stage"></main>', { url: 'https://test.invalid/admin/config.html?profile_catalog_history=1' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, location: dom.window.location });
const calls = [];
const digest = Array.from({ length: 32 }, (_, index) => index);
const zeroDigest = Array.from({ length: 32 }, () => 0);
const timestamp = '2026-08-28T01:02:03.123456Z';
const template = { id: 42, source_id: -7, source_key_digest: digest, source_payload_digest: digest, template_code: 'T<1>', template_name: '历史模板<em>', questionnaire_source_id: null, segmentation_question_source_id: 0, program_source_id: -2, description: '', original_enabled: false, version: 0, created_by_digest: digest, updated_by_digest: digest, created_at: timestamp, updated_at: timestamp };
const category = { id: 8, source_id: 0, source_key_digest: digest, source_payload_digest: digest, template_source_id: -7, template_history_id: 42, category_key: 'C', category_name: '类目', description: '', sort_order: -1, original_enabled: false, created_at: timestamp, updated_at: timestamp };
const mapping = { id: 9, source_id: -3, source_key_digest: digest, source_payload_digest: digest, template_source_id: -7, category_source_id: 0, template_history_id: 42, category_history_id: 8, question_source_id: -4, option_source_id: 0, created_at: timestamp };
const rule = { id: 11, source_key_digest: digest, source_payload_digest: digest, tag_source_id: 'signup:legacy', tag_name: '报名<标签>', signup_status: '', original_active: false, updated_at: timestamp };
let failTemplatePage = false;
let invalid = false;
let zeroField = '';
let invalidTime = false;
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), 'https://test.invalid');
  calls.push({ path: url.pathname + url.search, method: init.method, credentials: init.credentials, body: init.body });
  if (failTemplatePage && url.pathname.endsWith('/templates') && url.searchParams.get('offset') === '20') return new Response('{"code":"unavailable","raw":"RAW_ERROR_MUST_NOT_RENDER"}', { status: 503 });
  const limit = Number(url.searchParams.get('limit'));
  const offset = Number(url.searchParams.get('offset'));
  const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
  const templateResponse = { ...template, ...(zeroField ? { [zeroField]: zeroDigest } : {}), ...(invalidTime ? { updated_at: 'not-a-date-time' } : {}) };
  if (url.pathname.endsWith('/templates/42')) return new Response(JSON.stringify({ ...safety, item: invalid ? { ...templateResponse, id: 43 } : templateResponse }), { status: 200 });
  if (url.pathname.endsWith('/templates/42/categories/8/option-mappings')) return new Response(JSON.stringify({ ...safety, items: [mapping], total: offset + 1, limit, offset }), { status: 200 });
  if (url.pathname.endsWith('/templates/42/categories')) return new Response(JSON.stringify({ ...safety, items: [invalid ? { ...category, template_history_id: 43 } : category], total: offset + 1, limit, offset }), { status: 200 });
  if (url.pathname.endsWith('/signup-tag-rules')) return new Response(JSON.stringify({ ...safety, items: [rule], total: offset + 1, limit, offset }), { status: 200 });
  if (url.pathname.endsWith('/templates')) return new Response(JSON.stringify({ ...safety, items: offset === 0 ? Array.from({ length: 20 }, () => templateResponse) : [templateResponse], total: 21, limit, offset }), { status: 200 });
  return new Response(JSON.stringify({ ...safety, items: [template], total: offset + 1, limit, offset }), { status: 200 });
};

try {
  const stage = document.querySelector('#stage');
  await mountProfileCatalogHistory(stage);
  assert(calls.map((call) => call.path).join('|') === '/api/admin/profile-catalog-history/templates?limit=20&offset=0', 'default template list is the first generated GET');
  assert(stage.textContent.includes('-7') && stage.textContent.includes('false') && stage.textContent.includes('NULL（源未记录）') && stage.textContent.includes(timestamp), 'signed IDs, false, NULL and source text remain visible');
  assert(!stage.innerHTML.includes('<em>') && stage.textContent.includes('历史模板<em>'), 'source text is escaped');
  assert(!stage.querySelector('input,textarea') && !Array.from(stage.querySelectorAll('button')).some((button) => /保存|启用|执行|Provider/.test(button.textContent)), 'page has no current-state execution controls');
  const next = stage.querySelector('[data-history-kind="templates"] [data-next]');
  failTemplatePage = true;
  next.click();
  await pause();
  assert(stage.querySelector('[data-history-kind="templates"] [role="alert"]') && !stage.textContent.includes('RAW_ERROR_MUST_NOT_RENDER'), 'page failure clears old rows without mock data');
  failTemplatePage = false;
  stage.querySelector('[data-history-kind="templates"] [data-retry]').click();
  await pause();
  assert(calls.filter((call) => call.path === '/api/admin/profile-catalog-history/templates?limit=20&offset=20').length === 2, 'retry reads the same failed page');

  calls.length = 0;
  await mountProfileCatalogHistory(stage, { templateID: '42' });
  assert(calls.map((call) => call.path).join('|') === '/api/admin/profile-catalog-history/templates/42|/api/admin/profile-catalog-history/templates/42/categories?limit=20&offset=0', 'template detail loads only detail and parent-bound categories');
  calls.length = 0;
  await mountProfileCatalogHistory(stage, { templateID: '42', categoryID: '8' });
  assert(calls.map((call) => call.path).join('|') === '/api/admin/profile-catalog-history/templates/42/categories/8/option-mappings?limit=20&offset=0', 'mapping list binds both historical parents');
  calls.length = 0;
  await mountProfileCatalogHistory(stage, { view: 'signup_rules' });
  assert(calls.map((call) => call.path).join('|') === '/api/admin/profile-catalog-history/signup-tag-rules?limit=20&offset=0', 'signup rules use their fifth generated GET');
  assert(calls.every((call) => call.method === 'GET' && call.credentials === 'include' && call.body === undefined), 'history transport is credentialed GET only');

  const before = calls.length;
  for (const value of ['', '0', '-1', '1.5', '1/enable', '9007199254740992']) await rejects(() => readProfileHistoryTemplate(value));
  await rejects(() => readProfileHistoryTemplates(0));
  await rejects(() => readProfileHistoryCategories(42, 20, -1));
  assert(calls.length === before && requireProfileCatalogHistoryID('42') === 42, 'invalid IDs and pages fail before transport');
  invalid = true;
  await rejects(() => readProfileHistoryTemplate(42));
  await rejects(() => readProfileHistoryCategories(42));
  invalid = false;
  for (const field of ['source_key_digest', 'source_payload_digest', 'created_by_digest', 'updated_by_digest']) {
    zeroField = field;
    await rejects(() => readProfileHistoryTemplate(42));
  }
  zeroField = '';
  invalidTime = true;
  await rejects(() => readProfileHistoryTemplate(42));
  invalidTime = false;
  invalid = true;
  await rejects(() => readProfileHistoryTemplate(42));
  console.log('profile-catalog-history-e2e: PASS');
} finally {
  globalThis.fetch = saved.fetch;
  Object.assign(globalThis, { document: saved.document, window: saved.window, location: saved.location });
  dom.window.close();
  fs.rmSync(outdir, { recursive: true, force: true });
}
