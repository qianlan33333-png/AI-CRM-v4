import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const dist = path.join(root, 'dist');
const pageFile = path.join(dist, 'admin/questionnaires.html');
const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
const wait = () => new Promise((resolve) => setTimeout(resolve, 20));
let passed = 0;
const ok = (value, message) => { if (!value) throw new Error(message); passed++; };
const source = { source: 'v1_history', read_only: true, real_external_call_executed: false, definition_mapping: 'historical_source_only' };
const submission = (id) => ({ id, source_id: -id, questionnaire_source_id: 0, questionnaire_id: null, customer_id: null, matched_by: 'source_only', source_channel: 'legacy', total_score: 0, final_tags: null, submitted_at: '2026-08-28T01:02:03.123456Z', created_at: '2026-08-28T01:02:03.123456Z' });
const answer = (id, submissionID) => ({ id, source_id: -id, submission_id: submissionID, submission_source_id: -submissionID, question_source_id: 0, question_type: 'legacy', question_title_snapshot: '来源题目', selected_option_ids: null, selected_option_texts: null, selected_option_scores: null, selected_option_tags: null, text_value: '', score_contribution: 0, created_at: '2026-08-28T01:02:03.123456Z' });
const page = (items, total, limit, offset) => ({ ...source, items, total, limit, offset });
const response = (body, status = 200) => ({ status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(body) });

async function load(query, mode = 'normal') {
  if (!fs.existsSync(pageFile)) throw new Error('run npm run build before survey unresolved HTTP e2e');
  let html = fs.readFileSync(pageFile, 'utf8');
  html = html.replace(/<script type="module" src="\.\.\/assets\/admin-[^"]+\.js"><\/script>/, () => `<script>${bundle}</script>`);
  const calls = [];
  const dom = new JSDOM(html, {
    url: `http://localhost/admin/questionnaires.html?${query}`,
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = Headers;
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        calls.push({ path: url.pathname, query: url.search, method: init.method, body: init.body });
        const limit = Number(url.searchParams.get('limit'));
        const offset = Number(url.searchParams.get('offset'));
        if (mode === 'failure') return response({ code: 'survey_unresolved_history_unavailable' }, 503);
        if (mode === 'answer-failure' && url.pathname.endsWith('/answers')) return response({ code: 'survey_unresolved_history_unavailable' }, 503);
        if (url.pathname === '/api/admin/survey-history/submissions') {
          if (mode === 'empty') return response(page([], 0, limit, offset));
          const total = 21;
          return response(page(Array.from({ length: Math.min(limit, Math.max(0, total - offset)) }, (_, index) => submission(offset + index + 31)), total, limit, offset));
        }
        const detail = /^\/api\/admin\/survey-history\/submissions\/[1-9]\d*$/.test(url.pathname);
        if (detail) return response({ ...source, item: submission(Number(url.pathname.split('/').at(-1))) });
        const answers = /^\/api\/admin\/survey-history\/submissions\/[1-9]\d*\/answers$/.test(url.pathname);
        if (answers) {
          const total = 21;
          const submissionID = Number(url.pathname.split('/')[5]);
          return response(page(Array.from({ length: Math.min(limit, Math.max(0, total - offset)) }, (_, index) => answer(offset + index + 71, submissionID)), total, limit, offset));
        }
        return response({ code: 'unexpected_survey_unresolved_history_request' }, 500);
      };
    },
  });
  await wait();
  return { dom, calls };
}

{
  const { dom, calls } = await load('unresolved_history=1');
  ok(dom.window.document.querySelector('[data-survey-unresolved-history]') && dom.window.document.querySelectorAll('tbody tr').length === 20, 'questionnaires history boot renders generated list GET');
  ok(calls.length === 1 && calls[0].path === '/api/admin/survey-history/submissions' && calls[0].query === '?limit=20&offset=0', 'list uses exact generated paging parameters');
  dom.window.document.querySelector('[data-history-next]')?.click();
  await wait();
  ok(calls.at(-1).path === '/api/admin/survey-history/submissions' && calls.at(-1).query === '?limit=20&offset=20' && calls.every((call) => call.method === 'GET' && call.body === undefined), 'list pagination stays generated GET only');
  dom.window.close();
}

{
  const { dom, calls } = await load('unresolved_history=1', 'empty');
  ok(dom.window.document.body.getAttribute('data-page') === 'questionnaires' && dom.window.document.querySelector('[data-survey-unresolved-history]')?.textContent.includes('暂无历史提交') && calls.length === 1, 'empty history page remains a closed read-only page');
  dom.window.close();
}

{
  const { dom, calls } = await load('unresolved_history=1', 'failure');
  ok(dom.window.document.querySelector('[role="alert"]')?.textContent.includes('未显示历史数据') && !dom.window.document.querySelector('tbody') && calls.length === 1, 'list failure closes without current questionnaire fallback');
  dom.window.close();
}

{
  const { dom, calls } = await load('unresolved_history=1&history_id=31');
  ok(calls.length === 2 && calls[0].path === '/api/admin/survey-history/submissions/31' && calls[0].query === '' && calls[1].path === '/api/admin/survey-history/submissions/31/answers' && calls[1].query === '?limit=20&offset=0', 'detail boot calls generated detail and answer GET routes');
  dom.window.document.querySelector('[data-answer-next]')?.click();
  await wait();
  ok(calls.at(-1).path === '/api/admin/survey-history/submissions/31/answers' && calls.at(-1).query === '?limit=20&offset=20' && calls.every((call) => call.method === 'GET' && call.body === undefined), 'detail answers paginate with exact generated query');
  dom.window.close();
}

{
  const { dom, calls } = await load('unresolved_history=1&history_id=31', 'answer-failure');
  ok(dom.window.document.querySelector('[role="alert"]')?.textContent.includes('未显示答案快照') && dom.window.document.querySelector('h2')?.textContent.includes('历史提交 #31') && calls.length === 2, 'answer failure closes only the answer snapshot');
  dom.window.close();
}

console.log(`survey-unresolved-history HTTP e2e: ${passed}`);
