#!/usr/bin/env node
import { JSDOM, VirtualConsole } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = await buildTestBrowserBundle(path.join(repository, 'web', 'v3', 'operationCyclesAdapter.ts'));
const donor = fs.readFileSync(path.join(repository, 'web', 'donors', 'operation-cycles-v2', 'src', 'admin', 'templates', 'cycles.html'), 'utf8')
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
  .replace(/<\/sc-for>/g, '</template>');
const detailDonor = fs.readFileSync(path.join(repository, 'web', 'donors', 'operation-cycles-v2', 'src', 'admin', 'templates', 'cyclesDetail.html'), 'utf8')
  .replace(/<sc-for\s+([^>]*?)list="([^"]*)"([^>]*?)as="([^"]*)"([^>]*)>/g, (_match, _a, list, _b, as) => `<template data-sc-for="${list}" data-as="${as}">`)
  .replace(/<\/sc-for>/g, '</template>');
const requests = [];
const alerts = [];
const jsdomErrors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => {
  if (!String(error?.message || error).includes('navigation to another Document')) jsdomErrors.push(String(error?.message || error));
});
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, clone() { return this; }, json: async () => body, text: async () => JSON.stringify(body) });
const snapshot = {
  schema_version: 'operation_cycle_snapshot.v1', name: '每周复盘', cron: '每周一 09:00', dot: '#2EA121',
  action: '开始复盘', action_key: 'start_review', run_key: 'weekly.review.001',
  steps: [{ label: '复盘', color: '#2EA121', dim: false }],
};
const excelBatch = {
  id: 918,
  state: 'pending_review',
  version: 3,
  current_content_version: 1,
  summary: { total_rows: 1, excluded_rows: 0, empty_title_rows: 0, expected_tasks: 1 },
};
const historyBatch = {
  ...excelBatch,
  id: 917,
  state: 'completed',
  version: 2,
  current_content_version: 2,
};
const excelRow = {
  id: 33,
  version: 4,
  unionid: 'fixture-unionid',
  sender_userid: 'fixture-staff',
  text: '待审核话术',
  card: { appid: 'fixture-app', path: 'pages/fixture', title: '验收标题' },
  segment: '',
  excluded: false,
  review_state: 'pending_review',
  delivery_state: 'pending_submission',
  failure_reason: '',
  sent_at: null,
};
const historyRow = { ...excelRow, id: 32, text: '历史批次话术', review_state: 'approved', delivery_state: 'delivery_proven' };
const dom = new JSDOM(`<!doctype html><html><body class="admin-shell" data-page="cycles"><main id="stage"></main><template id="tpl">${donor}</template><script>${bundle}</script></body></html>`, {
  url: 'https://test.invalid/admin/operation-cycles', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.alert = (message) => alerts.push(String(message));
    window.document.cookie = `aicrm_csrf=${'c'.repeat(43)}`;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      requests.push({ path: url.pathname + url.search, method: init.method || 'GET', headers: Object.fromEntries(new Headers(init.headers).entries()), body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (url.pathname === '/api/admin/operation-cycles/strategies' && (init.method || 'GET') === 'GET') return json({ items: [
        { strategy_key: 'weekly.review', title: '每周复盘', status: 'active', version: 4, run_ordinal: 73, snapshot },
        { strategy_key: 'paused.review', title: '暂停复盘', status: 'paused', version: 2, snapshot: { ...snapshot, name: '暂停复盘', run_key: '' } },
      ] });
      if (url.pathname === '/api/admin/operation-batches/strategy-summaries') return json({
        items: [
          { strategy_key: 'weekly.review', title: '每周复盘', status: 'active', version: 4, snapshot, latest_batch_status: 'ready', latest_batch: excelBatch },
          { strategy_key: 'paused.review', title: '暂停复盘', status: 'paused', version: 2, snapshot: { ...snapshot, name: '暂停复盘', run_key: '' }, latest_batch_status: 'ready', latest_batch: null },
        ], total: 2, limit: 20, offset: 0, has_more: false, next_offset: null,
      });
      if (url.pathname === '/api/admin/operation-batches/legacy') return json({ items: [] });
      if (url.pathname === '/api/admin/operation-batches/strategies/weekly.review') return json({ strategy: { strategy_key: 'weekly.review' }, items: [excelBatch, historyBatch] });
      if (url.pathname === '/api/admin/operation-batches/strategies/paused.review') return json({ strategy: { strategy_key: 'paused.review' }, items: [] });
      if (url.pathname === '/api/admin/operation-batches/918') return json({ batch: excelBatch, rows: [excelRow], next_cursor: '' });
      if (url.pathname === '/api/admin/operation-batches/917') return json({ batch: historyBatch, rows: [historyRow], next_cursor: '' });
      if (url.pathname.endsWith('/actions/start_review/start') && init.method === 'POST') return json({ request_id: 'ocact_0123456789012345678901234567', status: 'queued' }, 202);
      if (url.pathname.endsWith('/paused.review/status') && init.method === 'POST') return json({ strategy_key: 'paused.review', status: 'active', version: 3 });
      return json({ ok: false, code: 'unexpected_request' }, 500);
    };
  },
});

try {
  await new Promise((resolve) => setTimeout(resolve, 500));
  const workspace = dom.window.document.querySelector('.operation-excel-workspace');
  const buttons = Array.from(workspace?.querySelectorAll('button') || []).filter((button) => button.textContent?.trim() === '查看详情');
  if (!workspace || buttons.length !== 2 || buttons.some((button) => button.textContent?.trim() !== '查看详情') || !workspace.textContent?.includes('长期计划') || !workspace.textContent?.includes('批次 #918') || workspace.textContent?.includes('开始复盘')) throw new Error(`Excel long-plan list did not replace the frozen donor actions: ${dom.window.document.getElementById('stage')?.innerHTML} requests=${JSON.stringify(requests)}`);
  buttons[0].click();
  await new Promise((resolve) => setTimeout(resolve, 80));
  const tabs = Array.from(dom.window.document.querySelectorAll('.xeb-detail-nav button')).map((button) => button.textContent?.trim());
  if (tabs.length !== 2 || tabs[0] !== '内容准备与发送' || tabs[1] !== '发送效果与复盘' || dom.window.document.querySelector('.xeb-detail-main h2')?.textContent?.trim() !== '每周复盘' || !workspace.textContent?.includes('当前批次 #918') || !workspace.textContent?.includes('待审核话术')) throw new Error(`Excel detail did not preserve its plan title and two-dimension workspace: ${workspace?.innerHTML} requests=${JSON.stringify(requests)}`);
  const history = dom.window.document.querySelector('select[aria-label="历史批次"]');
  if (!history) throw new Error('Excel detail did not render the batch history selector');
  history.value = '917';
  history.dispatchEvent(new dom.window.Event('change'));
  await new Promise((resolve) => setTimeout(resolve, 80));
  if (!workspace.textContent?.includes('当前批次 #917') || !workspace.textContent?.includes('历史批次话术')) throw new Error(`Excel history selection did not load the selected batch: ${workspace?.innerHTML} requests=${JSON.stringify(requests)}`);
  const writes = requests.filter((item) => item.method !== 'GET');
  if (writes.length || requests.some((item) => item.path.includes('/actions/start_review/start')) || !requests.some((item) => item.path === '/api/admin/operation-batches/strategy-summaries?limit=20&offset=0') || !requests.some((item) => item.path === '/api/admin/operation-batches/legacy') || !requests.some((item) => item.path === '/api/admin/operation-batches/strategies/weekly.review') || !requests.some((item) => item.path === '/api/admin/operation-batches/918?limit=50') || !requests.some((item) => item.path === '/api/admin/operation-batches/917?limit=50')) throw new Error(`Excel workspace read contract drifted: ${JSON.stringify(requests)}`);
  if (jsdomErrors.length) throw new Error(`browser errors: ${JSON.stringify(jsdomErrors)}`);
  console.log('operation-cycle Excel workspace browser Journey: PASS');
} finally {
  dom.window.close();
}

// A numeric donor URL is resolved by the immutable backend mapping, never by
// fetching the currently sorted strategy list again. The mock list below is
// deliberately reordered as if a concurrent report arrived before detail load.
const detailRequests = [];
const dossierSnapshot = {
  ...snapshot,
  dossier: {
    label: '稳定档案 A', objective: '验证稳定编号', strategy: '每周复盘', audience: '本地运营对象',
    intended_send_at: '2026-09-01 09:00', plan_scheduled_for: '2026-09-01 09:00', first_sent_at: '2026-09-01 09:01', last_sent_at: '2026-09-01 09:02',
    attempts: [], funnel: [], audience_note: '无', review_status: '已完成', review_tone: 'ok', plan_version: 'v1', plan_status: '已发布', plan_source: '本地', target_count: '1',
    delivery: { sent: '1', failed: '0', retryable: '0', rate: '100', status_label: '完成', source: '本地事实', failure_summary: '无' },
    windows: [], retro: { summary: '完成', detail: '稳定档案 A', findings: [], limitations: [] },
    next: { status_label: '完成', tone: 'ok', summary: '保持', rationale: '稳定', confirmed_at: '2026-09-01 10:00', applied_version: 'v1', note: '无', changes: [] }, references: [],
  },
};
const detailDom = new JSDOM(`<!doctype html><html><body class="admin-shell" data-page="cyclesDetail"><main id="stage"></main><template id="tpl">${detailDonor}</template><script>${bundle}</script></body></html>`, {
  url: 'https://test.invalid/admin/operation-cycles/cyclesDetail.html?id=73', runScripts: 'dangerously', pretendToBeVisual: true, virtualConsole,
  beforeParse(window) {
    window.__AICRM_TEST_MOCK__ = false;
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      detailRequests.push(url.pathname + url.search);
      if (url.pathname === '/api/admin/operation-cycles/run-ordinals/73') return json({ run_ordinal: 73, run_key: 'weekly.review.001', snapshot: dossierSnapshot });
      if (url.pathname === '/api/admin/operation-cycles/strategies') return json({ items: [{ strategy_key: 'concurrent.new', run_ordinal: 99, snapshot: { ...snapshot, run_key: 'concurrent.new.001' } }] });
      return json({ ok: false, code: 'unexpected_request', method: init.method || 'GET' }, 500);
    };
  },
});
try {
  await new Promise((resolve) => setTimeout(resolve, 500));
  const rendered = detailDom.window.document.getElementById('stage')?.textContent || '';
  if (!rendered.includes('稳定档案 A') || !rendered.includes('weekly.review.001')) throw new Error(`stable run dossier did not render: ${rendered}`);
  if (detailRequests.length !== 1 || detailRequests[0] !== '/api/admin/operation-cycles/run-ordinals/73') throw new Error(`detail re-resolved a mutable list: ${JSON.stringify(detailRequests)}`);
  console.log('operation-cycle stable ordinal detail Journey: PASS');
} finally {
  detailDom.window.close();
}
