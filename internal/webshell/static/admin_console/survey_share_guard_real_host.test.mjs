import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../../../../web/scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../../..');
const page = fs.readFileSync(path.join(root, 'web/dist/admin/questionnaires.html'), 'utf8');
const host = fs.readFileSync(path.join(root, 'internal/webshell/static/admin_console/survey_operations.js'), 'utf8');
const admin = await buildTestBrowserBundle(path.join(root, 'web/src/admin/main.ts'));
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const reply = (value, status = 200) => new Response(JSON.stringify(value), { status, headers: { 'Content-Type': 'application/json' } });

const questionnaires = [
  { id: 71, name: 'draft-share', title: '草稿问卷', description: '', status: 'disabled', is_disabled: true, public_path: '/q/draft-share', assessment_enabled: false, answer_display_mode: 'all_in_one', submission_count: 0, slug: 'draft-share', questions: [], score_rules: [], version: 1, created_at: '2026-09-08T00:00:00Z' },
  { id: 72, name: 'published-share', title: '已发布问卷', description: '', status: 'active', is_disabled: false, public_path: '/q/published-share', assessment_enabled: false, answer_display_mode: 'all_in_one', submission_count: 0, slug: 'published-share', questions: [], score_rules: [], version: 2, created_at: '2026-09-08T00:00:00Z' },
  { id: 73, name: 'disabled-share', title: '停用问卷', description: '', status: 'active', is_disabled: true, public_path: '/q/disabled-share', assessment_enabled: false, answer_display_mode: 'all_in_one', submission_count: 0, slug: 'disabled-share', questions: [], score_rules: [], version: 3, created_at: '2026-09-08T00:00:00Z' },
];

const dom = new JSDOM(page, {
  url: 'https://test.invalid/admin/questionnaires.html', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.Request = Request;
    window.Response = Response;
    window.Headers = Headers;
    window.fetch = async (input) => {
      const url = new URL(String(input), window.location.href);
      if (url.pathname === '/api/admin/questionnaires') return reply({ items: questionnaires, total: questionnaires.length });
      if (url.pathname === '/api/admin/channels') return reply({ items: [], total: 0 });
      if (url.pathname === '/api/v1/products' || url.pathname === '/api/admin/service-period-products' || url.pathname === '/api/admin/coupons' || url.pathname === '/api/admin/image-library' || url.pathname === '/api/admin/attachment-library' || url.pathname === '/api/admin/mini-program-library' || url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/wecom/tags' || url.pathname === '/api/admin/customers' || url.pathname === '/api/admin/orders') return reply({ items: [], total: 0, has_more: false });
      if (url.pathname === '/api/admin/config') return reply({ categories: [] });
      if (url.pathname === '/api/admin/app-settings') return reply({});
      if (url.pathname === '/api/admin/push-capabilities' || url.pathname === '/api/admin/releases') return reply({});
      return reply({ items: [] });
    };
  },
});

dom.window.eval(admin);
dom.window.eval(host);
dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
await wait(180);
// The frozen mini-runtime's final write is asynchronous in JSDOM. This
// mutation is equivalent to the browser's settled render and triggers the
// V3 Host observer without replacing the donor template.
dom.window.document.getElementById('stage').appendChild(dom.window.document.createComment('settled'));
await wait(40);

function shareFor(title) {
  const row = [...dom.window.document.querySelectorAll('tbody tr')].find((item) => item.textContent.includes(title));
  assert.ok(row, `${title} must render from the frozen questionnaire template`);
  const share = [...row.querySelectorAll('a')].find((item) => item.textContent.trim() === '分享');
  assert.ok(share, `${title} must retain r.shareIt anchor`);
  return share;
}

const draft = shareFor('草稿问卷');
assert.equal(draft.getAttribute('aria-disabled'), 'true', 'draft with public_path must be marked unavailable');
draft.click();
await wait(20);
assert.equal(dom.window.document.querySelector('#shareQrBox'), null, 'draft r.shareIt must not open the frozen share dialog');
assert.match(dom.window.document.querySelector('[data-survey-host-share-status]')?.textContent || '', /尚未发布或已停用/, 'draft share must explain the publish prerequisite');

const disabled = shareFor('停用问卷');
assert.equal(disabled.getAttribute('aria-disabled'), 'true', 'disabled questionnaire must be marked unavailable');
disabled.click();
await wait(20);
assert.equal(dom.window.document.querySelector('#shareQrBox'), null, 'disabled r.shareIt must not open the frozen share dialog');

const published = shareFor('已发布问卷');
assert.equal(published.getAttribute('aria-disabled'), 'false', 'active enabled questionnaire with public_path must remain shareable');
published.click();
await wait(30);
assert.equal(dom.window.document.querySelector('input[readonly]')?.value, 'https://test.invalid/q/published-share', 'published r.shareIt must retain its public share dialog');

dom.window.close();
console.log('survey real-template share guard: PASS');
