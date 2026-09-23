import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const outdir = fs.mkdtempSync(path.join(os.tmpdir(), 'aicrm-survey-unresolved-history-'));
const at = '2026-08-28T02:03:04.123456Z';
const submission = { id: 7, source_id: -1, questionnaire_source_id: 0, questionnaire_id: null, customer_id: null, matched_by: '', source_channel: '', total_score: 0, final_tags: null, submitted_at: at, created_at: at };
const answer = { id: 8, source_id: 0, submission_id: 7, submission_source_id: -1, question_source_id: 0, question_type: '', question_title_snapshot: '<b>来源题目</b>', selected_option_ids: null, selected_option_texts: [], selected_option_scores: [], selected_option_tags: [], text_value: '<img src=x>', score_contribution: 0, created_at: at };
const page = (items, total = items.length) => ({ source: 'v1_history', read_only: true, real_external_call_executed: false, definition_mapping: 'historical_source_only', items, total, limit: 20, offset: 0 });
const detail = { source: 'v1_history', read_only: true, real_external_call_executed: false, definition_mapping: 'historical_source_only', item: submission };
const assert = (value, message) => { if (!value) throw new Error(message); };
try {
  await build({ entryPoints: { api: path.join(root, 'src/api/surveyUnresolvedHistory.ts'), section: path.join(root, 'src/admin/sections/surveyUnresolvedHistory.ts') }, bundle: true, platform: 'node', format: 'esm', outdir, logLevel: 'warning' });
  const api = await import(pathToFileURL(path.join(outdir, 'api.js')).href);
  let received = null;
  const reader = api.surveyUnresolvedHistoryReader({ listSubmissions: async (query) => { received = query; return page([submission]); }, getSubmission: async () => detail, listAnswers: async () => page([answer]) });
  const values = await reader.listSubmissions();
  assert(received.limit === 20 && received.offset === 0 && values.items[0].source_id === -1, 'adapter did not preserve source signed facts');
  try { await reader.listAnswers('0'); throw new Error('invalid submission ID reached adapter'); } catch (error) { assert(error instanceof Error && error.message.includes('ID 无效'), 'invalid ID error changed'); }
  const rejects = async (call, message) => { try { await call(); } catch (error) { assert(error instanceof Error && error.message.includes(message), 'unexpected adapter rejection'); return; } throw new Error('invalid association was accepted'); };
  await rejects(() => api.surveyUnresolvedHistoryReader({ listSubmissions: async () => page([{ ...submission, questionnaire_id: null }]), getSubmission: async () => detail, listAnswers: async () => page([answer]) }).listSubmissions(20, 0, 9), '响应无效');
  await rejects(() => api.surveyUnresolvedHistoryReader({ listSubmissions: async () => page([submission]), getSubmission: async () => detail, listAnswers: async () => page([{ ...answer, submission_id: 9 }]) }).listAnswers(7), '响应无效');
  const dom = new JSDOM('<main></main>');
  await (await import(pathToFileURL(path.join(outdir, 'section.js')).href)).mountSurveyUnresolvedHistory(dom.window.document.querySelector('main'), { listSubmissions: async () => page([submission]), getSubmission: async () => detail, listAnswers: async () => page([answer]) });
  const html = dom.window.document.body.innerHTML;
  assert(html.includes('历史提交') && html.includes('来源定义（未映射）'), 'history list did not render');
  assert(!html.includes('source_key_digest') && !html.includes('redirect_url'), 'private identity or URL details rendered');
  const detailDom = new JSDOM('<main></main>');
  await (await import(pathToFileURL(path.join(outdir, 'section.js')).href)).mountSurveyUnresolvedHistory(detailDom.window.document.querySelector('main'), { listSubmissions: async () => page([submission]), getSubmission: async () => detail, listAnswers: async () => page([answer]) }, { historyID: '7' });
  assert(detailDom.window.document.body.innerHTML.includes('答案快照') && detailDom.window.document.body.innerHTML.includes('共 1 条'), 'detail did not render paged answers');
  console.log('survey-unresolved-history-contract: PASS');
} finally { fs.rmSync(outdir, { recursive: true, force: true }); }
