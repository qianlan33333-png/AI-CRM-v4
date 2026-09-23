/**
 * 端到端 DOM 渲染验证（jsdom）：
 * 加载 dist/ 生成页，执行真实 bundle，断言渲染结果与关键交互。
 */
import { execFileSync } from 'node:child_process';
import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';
import { build } from 'esbuild';
import { memberGridPresentationPlugin } from '../../scripts/member-grid-presentation-source.mjs';

const ROOT = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const DIST = path.join(ROOT, 'dist');
// build.mjs creates the frozen sidebar document first. Replace that entry here
// so every existing e2e invocation executes the final staged V3 Host instead
// of accidentally asserting the pre-Host sidebar artifact.
execFileSync(process.execPath, [path.join(ROOT, '..', 'scripts', 'build-v3-host-adapters.mjs')], { stdio: 'inherit' });
const TEST_BUNDLES = {
  admin: await buildTestBrowserBundle(path.join(ROOT, 'src/admin/main.ts')),
  customerHost: await buildTestBrowserBundle(path.join(ROOT, 'v3/customerAdapter.ts')),
  productHost: await buildTestBrowserBundle(path.join(ROOT, 'v3/productAdapter.ts')),
  orderHost: await buildTestBrowserBundle(path.join(ROOT, 'v3/orderAdapter.ts')),
  questionnaireEditor: await buildTestBrowserBundle(path.join(ROOT, 'src/admin/sections/questionnaireEditor.ts')),
  surveyOperationsHost: await buildTestBrowserBundle(path.join(ROOT, 'v3/surveyOperationsHost.ts')),
  h5: await buildTestBrowserBundle(path.join(ROOT, 'src/h5/main.ts')),
  sidebar: await buildTestBrowserBundle(path.join(ROOT, 'v3/sidebar/main.ts')),
  memberGridShare: (await build({ entryPoints: [path.join(ROOT, 'v3/memberGridFeedbackHost.ts')], bundle: true, write: false, format: 'iife', plugins: [memberGridPresentationPlugin] })).outputFiles[0].text,
};
const OWNER_HANDOFF_DONOR = fs.readFileSync(path.resolve(ROOT, '../internal/webshell/static/admin_console/owner_migration_dd8d60d.html'), 'utf8');
const OWNER_HANDOFF_HOST = fs.readFileSync(path.resolve(ROOT, '../internal/webshell/static/admin_console/owner_handoff_host.js'), 'utf8');
const SIDEBAR_IMAGE_RESOURCE_LOADER = fs.readFileSync(path.resolve(ROOT, '../web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js'), 'utf8');

let pass = 0;
let fail = 0;
const ok = (name, cond) => {
  if (cond) {
    pass++;
    console.log('  ✓ ' + name);
  } else {
    fail++;
    console.log('  ✗ ' + name);
  }
};

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const waitFor = async (predicate, timeout = 1000) => {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) {
    if (predicate()) return true;
    await sleep(10);
  }
  return predicate();
};

async function loadMemberGridShare({ token, response, responses, status = 200 } = {}) {
  const trace = [];
  let fetchIndex = 0;
  const json = (body, resultStatus) => ({
    ok: resultStatus >= 200 && resultStatus < 300,
    status: resultStatus,
    json: async () => body,
  });
  const file = path.join(DIST, 'member-grid-share/index.html');
  let html = fs.readFileSync(file, 'utf8');
  html = html.replace(/<script type="module" src="\.\.\/assets\/memberGridFeedbackHost-[^"]+\.js"><\/script>/, () => `<script>${TEST_BUNDLES.memberGridShare}</script>`);
  const dom = new JSDOM(html, {
    url: 'http://localhost/member-grid-share/index.html#' + (token || ''),
    runScripts: 'dangerously',
    beforeParse(window) {
      const replaceState = window.history.replaceState.bind(window.history);
      window.history.replaceState = (...args) => {
        trace.push({ kind: 'replace', url: String(args[2] || '') });
        return replaceState(...args);
      };
      window.fetch = async (input, init = {}) => {
        trace.push({ kind: 'fetch', url: String(input), init });
        return json(responses?.[fetchIndex++] ?? response, status);
      };
    },
  });
  await sleep(20);
  return { dom, trace };
}

async function loadQuestionnaireEditor({ q = '', questionnaire } = {}) {
  const trace = [];
  const saved = questionnaire || {
    id: 55,
    name: '激活黄小璨AI登记',
    title: '激活黄小璨AI登记',
    description: '您在问卷中填写的信息会被录入黄小璨进行对黄小璨的初始设置',
    answer_display_mode: 'all_in_one',
    assessment_enabled: false,
    assessment_config: {},
    slug: 'q-20260825034422',
    is_disabled: false,
    public_path: '/q/q-20260825034422',
    questions: [{
      id: 60,
      type: 'mobile',
      title: '请输入手机号',
      placeholder_text: '请输入登录AI分身的唯一手机号码',
      assessment_dimension_key: '',
      sidebar_profile_field: '',
      required: true,
      sort_order: 0,
      options: [],
    }],
    score_rules: [],
  };
  const list = [saved];
  const response = (data, status = 200) => ({
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ 'Content-Type': 'application/json' }),
    json: async () => data,
    text: async () => JSON.stringify(data),
    clone() { return this; },
  });
  const file = path.join(DIST, 'admin/questionnaireDetail.html');
  let html = fs.readFileSync(file, 'utf8');
  html = html.replace(/<script type="module" src="[^\"]*assets\/questionnaireEditor-[^\"]+\.js"><\/script>/, () => `<script>${TEST_BUNDLES.questionnaireEditor}</script>`);
  const dom = new JSDOM(html, {
    url: 'http://localhost/admin/questionnaireDetail.html' + (q ? '?' + q : ''),
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = Headers;
      window.confirm = () => true;
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin);
        const method = init.method || 'GET';
        const body = init.body ? JSON.parse(String(init.body)) : null;
        trace.push({ path: url.pathname, method, body, credentials: init.credentials });
        if (url.pathname === '/api/admin/wecom/tags') return response({ items: [] });
        if (url.pathname === '/api/admin/questionnaires' && method === 'GET') return response({ questionnaires: list });
        if (url.pathname === '/api/admin/questionnaires' && method === 'POST') {
          const created = { ...body, id: 81, public_path: `/q/${body.slug}`, questions: body.questions, score_rules: body.score_rules };
          list.unshift(created);
          return response({ questionnaire: created });
        }
        if (url.pathname === `/api/admin/questionnaires/${saved.id}` && method === 'GET') return response({ questionnaire: saved });
        if (url.pathname === `/api/admin/questionnaires/${saved.id}` && method === 'PUT') return response({ questionnaire: { ...saved, ...body } });
        return response({ code: 'unexpected_questionnaire_editor_request' }, 500);
      };
    },
  });
  await sleep(50);
  return { dom, trace };
}

async function loadSurveyOperations(id = 1) {
  const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
  return new JSDOM(`<!doctype html><body data-page="questionnaireOps"><main id="stage"></main><script>${TEST_BUNDLES.surveyOperationsHost}</script></body>`, {
    url: `http://localhost/admin/questionnaireOps.html?id=${id}`,
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Headers = Headers;
      window.fetch = async (input) => {
        const url = new URL(String(input), window.location.origin);
        if (url.pathname === `/api/admin/questionnaires/${id}`) return json({ questionnaire: { id, title: '运营验收问卷', slug: 'ops-e2e', public_path: '/q/ops-e2e', is_disabled: false, submission_count: 3 } });
        if (url.pathname === `/api/admin/questionnaires/${id}/operations`) return json({ questionnaire_id: id, configuration_version: 2, provider_enabled: true, completion: { enabled: true, mode: 'lead_qr', lead_channel_id: 49, lead_qr_title: '扫码继续', lead_qr_subtitle: '添加顾问', completion_target: {} }, external_push: { enabled: true, webhook_url: 'https://hooks.example.test/survey', type: 'subscription', day: 30, frequency: 1, remark: '问卷激活', custom_params: { source: 'survey' } } });
        if (url.pathname === '/api/admin/channels') return json({ channels: [{ channel_id: 49, channel_name: '问卷渠道', status: 'active', carrier_type: 'qrcode', qrcode_asset_id: 9, qrcode_status: 'active', qr_url: 'https://cdn.example.test/survey-qr.png' }] });
        return json({ code: 'unexpected_questionnaire_operations_request' }, 500);
      };
    },
  });
}

async function loadPage(rel, { id, q, automationHistoryHttp = false, campaignHistoryHttp, campaignHttp = false, memberGridHistoryHttp, contactHistoryHttp, hxcHistoryHttp, messageHistoryHttp = false, customerListHttp = false, customerDetailHttp = false, groupDirectoryHttp = false, channelHttp = false, channelHttpFailure = false, channelHistoryHttpFailure = false, channelHistoryEmpty = false, channelQrUrl = false, opsGuardHttp = false, couponHistoryHttp, couponHttp = false, couponHttpFailure = false, audienceHttp = false, audienceEmpty = false, audienceActive = false, audienceHistoryHttp = false, radarHttp = false, productHttp = false, serviceProductHttp = false, orderHistoryHttp = false, h5Http, h5WeChat = false, serviceHistoryHttp = false, serviceHistoryEmpty = false, serviceHistoryFailure = '', groupOpsHistoryHttp, miniProgramHttp = false, ownerHandoffHttp = false } = {}) {
  const file = path.join(DIST, rel);
  // ownerMig is served by the Go Webshell Host, not the unrelated historical
  // web/dist template bearing the same route name. Use its actual stage.
  let html = ownerHandoffHttp
    ? '<!doctype html><html><head></head><body><main data-owner-handoff-host></main></body></html>'
    : fs.readFileSync(file, 'utf8');
  // 用 jsdom 执行内联脚本：把 bundle 内联进去，避免资源加载配置
  html = html.replace(/<script type="module" src="[^"]*assets\/(admin|orderHost|h5|sidebar(?:Host)?)-[^"]+\.js"><\/script>/, (_m, name) => {
    if (customerListHttp || customerDetailHttp) return `<script>${TEST_BUNDLES.customerHost}</script><script>${TEST_BUNDLES.admin}</script>`;
    const bundle = name.startsWith('sidebar') ? TEST_BUNDLES.sidebar : TEST_BUNDLES[name];
    return `<script>${productHttp ? TEST_BUNDLES.productHost : bundle}</script>`;
  });
  html = html.replace(/<script type="module" src="[^"]*assets\/surveyOperationsHost-[^"]+\.js"><\/script>/, `<script>${TEST_BUNDLES.surveyOperationsHost}</script>`);
  if (rel === 'sidebar/index.html') html = html.replace(/<script src="[^"]*assets\/sidebarImageResourceLoader-[^"]+\.js"><\/script>/, `<script>${SIDEBAR_IMAGE_RESOURCE_LOADER}</script>`);
  // ownerMig's Host is a separately served V3 adapter. Inline the exact built
  // adapter here so this regression executes the frozen donor mount rather
  // than asserting against only the empty Webshell stage.
  if (ownerHandoffHttp) html = html.replace('</body>', `<script>${OWNER_HANDOFF_HOST}</script></body>`);
  const qs = q || (id != null ? 'id=' + id : '');
  const dom = new JSDOM(html, {
    url: 'http://localhost/' + rel + (qs ? '?' + qs : ''),
    runScripts: 'dangerously',
    pretendToBeVisual: true,
    beforeParse(window) {
      // JSDOM supplies HTMLDialogElement but not its native methods. The
      // frozen-admin journey exercises both the existing overlay and V3
      // dialogs, so model browser open/close and their close event together.
      window.HTMLDialogElement.prototype.showModal = function showModal() { this.open = true; };
      window.HTMLDialogElement.prototype.close = function close() { this.open = false; this.dispatchEvent(new window.Event('close')); };
      if (rel === 'admin/orders.html' || rel === 'admin/orderDetail.html') {
        window.Request = Request;
        window.Response = Response;
        window.Headers = Headers;
        window.fetch = async () => { throw new Error('Unexpected HTTP request in mock order fixture'); };
        // jsdom.close does not emit browser pagehide; exercise Host cleanup
        // before it removes the document and delivers pending mutations.
        const close = window.close.bind(window);
        window.close = () => { window.dispatchEvent(new window.Event('pagehide')); close(); };
      }
      if (h5WeChat) Object.defineProperty(window.navigator, 'userAgent', { value: 'MicroMessenger/8.0', configurable: true });
      // Mock 仅由 DOM 回归测试显式注入；浏览器默认运行态不会走此路径。
      window.__AICRM_TEST_MOCK__ = !(automationHistoryHttp || campaignHistoryHttp || campaignHttp || memberGridHistoryHttp || contactHistoryHttp || hxcHistoryHttp || messageHistoryHttp || customerListHttp || customerDetailHttp || groupDirectoryHttp || channelHttp || couponHistoryHttp || couponHttp || audienceHttp || audienceHistoryHttp || radarHttp || productHttp || serviceProductHttp || orderHistoryHttp || h5Http || serviceHistoryHttp || groupOpsHistoryHttp || miniProgramHttp || ownerHandoffHttp);
      if (rel === 'admin/questionnaireOps.html' && id != null) {
        window.Headers = Headers;
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.fetch = async (input) => {
          const url = new URL(String(input), window.location.origin);
          if (url.pathname === `/api/admin/questionnaires/${id}`) return json({ questionnaire: { id, title: '运营验收问卷', slug: 'ops-e2e', public_path: '/q/ops-e2e', is_disabled: false, submission_count: 3 } });
          if (url.pathname === `/api/admin/questionnaires/${id}/operations`) return json({ questionnaire_id: id, configuration_version: 2, provider_enabled: true, completion: { enabled: true, mode: 'lead_qr', lead_channel_id: 49, lead_qr_title: '扫码继续', lead_qr_subtitle: '添加顾问', completion_target: {} }, external_push: { enabled: true, webhook_url: 'https://hooks.example.test/survey', type: 'subscription', day: 30, frequency: 1, remark: '问卷激活', custom_params: { source: 'survey' } } });
          if (url.pathname === '/api/admin/channels') return json({ channels: [{ channel_id: 49, channel_name: '问卷渠道', status: 'active', carrier_type: 'qrcode', qrcode_asset_id: 9, qrcode_status: 'active', qr_url: 'https://cdn.example.test/survey-qr.png' }] });
          return json({ code: 'unexpected_questionnaire_operations_request' }, 500);
        };
        return;
      }
      if (ownerHandoffHttp) {
        window.Headers = Headers;
        const staff = [
          { ID: 10, UserID: 'inactive-source', DisplayName: '离职原负责人', Active: false },
          { ID: 20, UserID: 'active-target', DisplayName: '在职目标负责人', Active: true },
        ];
        const test = window.__ownerHandoffHttpTest = { calls: [], pickerOpens: [] };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        const textResponse = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'text/html' }), text: async () => data, json: async () => JSON.parse(data) });
        window.AICRMStaffPicker = {
          open: async (options) => {
            test.pickerOpens.push({ source: options.source, scope: options.scope, title: options.title, directoryHint: options.directoryHint });
            const page = await options.loadPage({ query: '', signal: new AbortController().signal });
            const selected = page.items.find((member) => options.title === '选择原负责人' ? member.active === false : member.active !== false);
            if (selected) options.onCommit({ selected: [selected] });
          },
        };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          const method = init.method || 'GET';
          const body = init.body ? JSON.parse(String(init.body)) : undefined;
          test.calls.push({ path: url.pathname, query: url.search, method, body, credentials: init.credentials });
          if (url.pathname === '/static/admin_console/owner_migration_dd8d60d.html' && method === 'GET') return textResponse(OWNER_HANDOFF_DONOR);
          if (url.pathname === '/api/admin/customers/owner-handoffs/context' && method === 'GET') return json({ staff, operator: '管理员 #42' });
          if (url.pathname === '/api/admin/common/operation-members' && method === 'GET') {
            const includeInactive = url.searchParams.get('include_inactive') === 'true';
            return json({ items: staff.filter((member) => includeInactive || member.Active).map((member) => ({ staff_id: member.ID, user_id: member.UserID, display_name: member.DisplayName, active: member.Active })) });
          }
          if (url.pathname === '/api/admin/customers/owner-handoffs/previews' && method === 'POST') {
            const externalUserIDs = body.scope === 'excel_include' ? body.external_userids : ['external-42'];
            return json({ ID: 'preview-owner-host', Mode: body.mode, SourceStaffID: body.source_staff_id, TargetStaffID: body.target_staff_id, Hash: 'preview-hash', ConfirmationPhrase: 'CONFIRM', ExpiresAt: '2026-09-06T12:00:00Z', Rows: externalUserIDs.map((ExternalUserID, index) => ({ Line: index + 1, CustomerID: index + 42, ExternalUserID, CustomerDisplayName: `客户 ${index + 42}`, CurrentOwnerUserID: 'inactive-source', State: 'ready' })) });
          }
          if (url.pathname === '/api/admin/customers/owner-handoffs/confirm' && method === 'POST') return json({ ID: 'batch-owner-host', Mode: 'wecom_then_crm', State: 'accepted', Lines: [{ Line: 1, CustomerID: 42, State: 'queued' }] });
          if (url.pathname === '/api/admin/customers/owner-handoffs/batches/batch-owner-host/transfer-result' && method === 'POST') return json({ ID: 'batch-owner-host', Mode: 'wecom_then_crm', State: 'executing', Lines: [{ Line: 1, CustomerID: 42, State: 'observed', TransferStatus: 1, TakeoverAt: '2026-09-06T12:01:00Z' }] });
          if (url.pathname === '/api/admin/customers/owner-handoffs/batches/batch-owner-host' && method === 'GET') return json({ ID: 'batch-owner-host', Mode: 'wecom_then_crm', State: 'executing', Lines: [{ Line: 1, CustomerID: 42, State: 'observed', TransferStatus: 1, TakeoverAt: '2026-09-06T12:01:00Z' }] });
          return json({ error: 'unexpected_owner_handoff_request' }, 500);
        };
        return;
      }
      if (hxcHistoryHttp) {
        window.Headers = Headers;
        const test = window.__hxcHistoryHttpTest = { calls: [], fail: hxcHistoryHttp.fail || false };
        const at = '2026-08-29T01:02:03.123456Z';
        const senders = Array.from({ length: 21 }, (_, index) => ({ id: 71 + index, source_id: index ? -index : 0, priority: -index, original_is_active: false, created_at: at, updated_at: at }));
        const records = Array.from({ length: 21 }, (_, index) => ({ id: 101 + index, source_id: index ? -index : 0, task_type: index ? 'legacy' : '', original_status: '', selected_count: -index, eligible_count: 0, sent_count: index, skipped_count: -2, planned_count: 3, queued_count: 4, dispatching_count: 5, succeeded_count: 6, failed_count: 7, blocked_count: 8, cancelled_count: 9, image_count: 10, include_do_not_disturb: false, target_source: '', target_source_id: index ? -9 : null, created_at: at, last_status_sync_at: null, last_refreshed_at: at }));
        const json = (data, status = 200) => ({ status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', credentials: init.credentials, body: init.body });
          if (test.fail) return json({ code: 'unavailable' }, 503);
          const senderList = url.pathname === '/api/admin/hxc-history/sender-configs';
          const recordList = url.pathname === '/api/admin/hxc-history/send-records';
          const senderDetail = /^\/api\/admin\/hxc-history\/sender-configs\/\d+$/.test(url.pathname);
          const recordDetail = /^\/api\/admin\/hxc-history\/send-records\/\d+$/.test(url.pathname);
          if (!senderList && !recordList && !senderDetail && !recordDetail) return json({ code: 'unexpected_hxc_history_request' }, 500);
          const rows = senderList || senderDetail ? senders : records;
          if (senderDetail || recordDetail) {
            const item = rows.find((row) => row.id === Number(url.pathname.split('/').at(-1)));
            return item ? json({ source: 'v1_history', read_only: true, real_external_call_executed: false, item }) : json({ code: 'not_found' }, 404);
          }
          const limit = Number(url.searchParams.get('limit')); const offset = Number(url.searchParams.get('offset'));
          return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: rows.slice(offset, offset + limit), total: rows.length, limit, offset });
        };
        return;
      }
      if (contactHistoryHttp) {
        window.Headers = Headers;
        const test = window.__contactHistoryHttpTest = { calls: [], fail: contactHistoryHttp.fail || false };
        const at = '2026-08-27T10:11:12.123456Z';
        const digest = (seed) => Array.from({ length: 32 }, (_, index) => (seed + index) % 256);
        const sidebars = Array.from({ length: 21 }, (_, i) => ({ id: 31 + i, source_key_digest: digest(i + 1), customer_id: i ? 7 : null, source: i ? '历史来源' : '', industry: '行业 <历史>', industry_description: '<img src=x onerror=alert(1)>', needs_blockers_followup: '', updated_at: at, source_payload_digest: digest(i + 2) }));
        const owners = Array.from({ length: 21 }, (_, i) => ({ id: 61 + i, source_key_digest: digest(i + 20), scope_type: '', file_hash: `file-${i + 1}`, preview_hash: '', transfer_welcome_message: '<b>原欢迎语</b>', total_rows: 4, eligible_count: 3, wecom_success: 2, wecom_failed: 1, crm_updated: 2, include_wecom_transfer: true, session_relation: 'unresolved', preview_relation: 'resolved', created_at: at, executed_at: at, source_payload_digest: digest(i + 21) }));
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', credentials: init.credentials });
          if (test.fail === true || test.fail === url.pathname) return json({ code: 'unavailable' }, 503);
          const sidebar = url.pathname === '/api/admin/contact-history/sidebar-profiles';
          const owner = url.pathname === '/api/admin/contact-history/owner-migration-results';
          const sidebarDetail = /^\/api\/admin\/contact-history\/sidebar-profiles\/[1-9]\d*$/.test(url.pathname);
          const ownerDetail = /^\/api\/admin\/contact-history\/owner-migration-results\/[1-9]\d*$/.test(url.pathname);
          if (!sidebar && !owner && !sidebarDetail && !ownerDetail) return json({ code: 'unexpected_contact_history_request' }, 500);
          const rows = sidebar || sidebarDetail ? sidebars : owners;
          if (sidebarDetail || ownerDetail) {
            const item = rows.find((entry) => entry.id === Number(url.pathname.split('/').at(-1)));
            if (!item) return json({ code: 'not_found' }, 404);
            const result = contactHistoryHttp.wrongID ? { ...item, id: item.id + 1 } : contactHistoryHttp.wrongCustomer && sidebarDetail ? { ...item, customer_id: null } : item;
            return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, item: contactHistoryHttp.raw ? { ...result, raw_identity: 'must-not-render' } : result });
          }
          const limit = Number(url.searchParams.get('limit'));
          const offset = Number(url.searchParams.get('offset'));
          const customer = url.searchParams.get('customer_id');
          if (owner && customer !== null) return json({ code: 'unexpected_owner_customer_filter' }, 400);
          const filtered = sidebar && customer !== null ? rows.filter((entry) => entry.customer_id === Number(customer)) : rows;
          const body = { source: 'v1_history', read_only: true, real_external_call_executed: false, items: filtered.slice(offset, offset + limit), total: filtered.length, limit, offset };
          return json(contactHistoryHttp.raw ? { ...body, raw_identity: 'must-not-render' } : body);
        };
        return;
      }
      if (memberGridHistoryHttp) {
        window.Headers = Headers;
        const test = window.__memberGridHistoryHttpTest = { calls: [], fail: memberGridHistoryHttp.fail || false };
        const at = '2026-08-28T00:00:00.000000Z';
        const digest = Array.from({ length: 32 }, (_, index) => index);
        const views = Array.from({ length: 21 }, (_, index) => ({ id: 31 + index, source_key_digest: digest, source_view_id: 8 + index, source_service_product_id: 91, product_id: 91, name: index ? `旧视图 ${index + 1}` : '<img src=x onerror=alert(1)>', position: -index, is_default: false, schema_version: -1, config_digest: digest, version: -2, created_at: at, updated_at: at, source_payload_digest: digest }));
        const usage = Array.from({ length: 21 }, (_, index) => ({ id: 61 + index, source_key_digest: digest, customer_id: 7, formally_logged_in: false, has_token_usage: false, learning_plan_id: index ? `plan-${index + 1}` : '', learning_plan_current: null, learning_plan_total: 0, open_count_7d: 0, last_open_at: null, refreshed_at: at, source_payload_digest: digest, recovery_entry_digest: digest }));
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', credentials: init.credentials });
          if (test.fail === true || test.fail === url.pathname) return json({ code: 'unavailable' }, 503);
          const viewList = url.pathname === '/api/admin/member-grid-history/views';
          const usageList = url.pathname === '/api/admin/member-grid-history/usage';
          const viewDetail = /^\/api\/admin\/member-grid-history\/views\/\d+$/.test(url.pathname);
          const usageDetail = /^\/api\/admin\/member-grid-history\/usage\/\d+$/.test(url.pathname);
          if (!viewList && !usageList && !viewDetail && !usageDetail) return json({ code: 'unexpected_member_grid_history_request' }, 500);
          const rows = viewList || viewDetail ? views : usage;
          if (viewDetail || usageDetail) {
            const item = rows.find((row) => row.id === Number(url.pathname.split('/').at(-1)));
            return item ? json({ source: 'v1_history', read_only: true, real_external_call_executed: false, item }) : json({ code: 'not_found' }, 404);
          }
          const filter = Number(url.searchParams.get(viewList ? 'product_id' : 'customer_id'));
          if (filter && filter !== (viewList ? 91 : 7)) return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: [], total: 0, limit: Number(url.searchParams.get('limit')), offset: Number(url.searchParams.get('offset')) });
          const limit = Number(url.searchParams.get('limit')); const offset = Number(url.searchParams.get('offset'));
          return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: rows.slice(offset, offset + limit), total: rows.length, limit, offset });
        };
        return;
      }
      if (campaignHistoryHttp) {
        window.Headers = Headers;
        const test = window.__campaignHistoryHttpTest = { calls: [], fail: campaignHistoryHttp.fail || false };
        const date = '2026-08-28T01:02:03.123456Z', digest = Array.from({ length: 32 }, (_, i) => i);
        const segment = { id: 11, source_id: 101, campaign_source_id: 1001, segment_source_id: -9, source_parent_state: 'missing_campaign', code: ' code ', priority: -3, label: '<legacy>', created_at: date, source_payload_digest: digest };
        const member = { id: 21, source_id: 102, campaign_source_id: -1, campaign_segment_source_id: -2, segment_source_id: -3, member_source_id: -4, segment_history_id: 11, customer_id: null, joined_at: date, anchor_date: '', current_step_index: -5, next_due_at: null, original_status: '', stop_reason: '', last_step_sent_at: date, retry_count: -6, created_at: date, updated_at: date, source_payload_digest: digest };
        const plan = { id: 31, source_id: 103, source_plan_id: ' plan ', campaign_source_id: -1, segment_source_id: null, display_name: '<plan>', intent: '', content_strategy: 'legacy', content_template_masked: 'masked', max_recipients: -1, candidate_count: -2, skipped_count: -3, requires_manual_copy: true, original_status: '', original_review_status: '', original_run_status: '', committed_at: null, expires_at: date, created_at: date, updated_at: date, runtime_digest: digest, source_payload_digest: digest };
        const recipient = { id: 41, source_id: 104, plan_history_id: 31, customer_id: 7, display_name: '', planned_message_count: -1, original_approval_status: '', original_send_status: '', approved_at: null, rejected_at: date, created_at: date, updated_at: date, source_payload_digest: digest };
        const message = { id: 51, source_id: 105, plan_history_id: campaignHistoryHttp.wrongPlan ? 99 : 31, recipient_history_id: 41, customer_id: null, sequence_index: -1, day_offset: -2, original_send_time: 'old civil', content_masked: '<old>', original_status: '', sent_at: null, created_at: date, updated_at: date, content_payload_digest: digest, attachments_digest: digest, source_payload_digest: digest };
        const prefix = '/api/admin/campaign-history';
        const json = (body, status = 200) => ({ status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(body) });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', credentials: init.credentials, body: init.body });
          if (test.fail === true || test.fail === url.pathname) return json({ code: 'unavailable' }, 503);
          const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
          if (url.pathname === `${prefix}/segments/11`) return json({ ...safety, item: segment });
          if (url.pathname === `${prefix}/broadcast-plans/31`) return json({ ...safety, item: plan });
          const kind = url.pathname === `${prefix}/segments` ? segment : url.pathname === `${prefix}/members` ? member : url.pathname === `${prefix}/broadcast-plans` ? plan : url.pathname === `${prefix}/broadcast-plans/31/recipients` ? recipient : url.pathname === `${prefix}/broadcast-recipients/41/messages` ? message : undefined;
          if (!kind) return json({ code: 'unexpected_history_request' }, 500);
          const limit = Number(url.searchParams.get('limit')), offset = Number(url.searchParams.get('offset')), total = campaignHistoryHttp.empty ? 0 : 21;
          const items = Array.from({ length: Math.min(limit, Math.max(0, total - offset)) }, (_, i) => ({ ...kind, id: kind.id + offset + i, source_id: kind.source_id + offset + i }));
          return json({ ...safety, items, total, limit, offset, ...(kind === recipient ? { plan_history_id: 31 } : kind === message ? { recipient_history_id: 41 } : {}) });
        };
        return;
      }
      if (groupDirectoryHttp) {
        window.Headers = Headers;
        window.document.cookie = 'aicrm_csrf=group-directory-csrf';
        const test = window.__groupDirectoryTest = { calls: [], fail: false, syncFail: false, empty: false, ownersFail: false };
        const safety = { provider_execution_eligible: false, real_external_call_executed: false, provider_accepted: false, delivery_proven: false };
        const detail = { ...safety, plan: { plan_id: '10', name: '目录测试计划', revision: 1, status: groupDirectoryHttp.status || 'draft', queue_count: 0, created_at: '', updated_at: '' }, members: [{ staff_id: 7 }], nodes: [], group_assets: [{ group_asset_id: '1', asset_reference: 'group-old' }, { group_asset_id: '2', asset_reference: 'unknown-old' }], webhook_descriptor: { configured: false } };
        const provenExecution = groupDirectoryHttp.deliveryProven ? { execution_id: '71', run_id: '61', plan_id: '10', plan_revision: 1, node_id: '51', node_position: 1, target_reference: 'group-opaque-1', target_digest: 'sha256:' + 'a'.repeat(64), content_digest: 'sha256:' + 'b'.repeat(64), material_digest: 'sha256:' + 'c'.repeat(64), external_effect_id: 'eer_71', state: 'delivery_proven', runtime_state: 'provider_accepted', provider_accepted: true, delivery_proven: true, attempt_count: 1, provider_receipt_present: true, reconciliation_evidence_present: false, created_at: '2026-09-05T00:00:00Z', updated_at: '2026-09-05T00:01:00Z' } : null;
        test.detail = detail;
        const json = (body, status = 200) => ({ status, headers: new Headers(), text: async () => JSON.stringify(body) });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin), method = init.method || 'GET';
          const body = init.body ? JSON.parse(init.body) : {};
          test.calls.push({ path: url.pathname, query: url.search, method, body, headers: new Headers(init.headers), credentials: init.credentials });
          if (url.pathname === '/api/admin/common/operation-members') return test.ownersFail ? json({ code: 'unavailable' }, 503) : json({ ...safety, scope: 'group_ops', page_size: 100, items: [7, 8].map((staff_id) => ({ staff_id, sender_userid: 'staff-' + staff_id, display_name: '成员' + staff_id })) });
          if (url.pathname.endsWith('/groups') || url.pathname.endsWith('/groups/sync')) {
            const sync = method === 'POST';
            if (sync && test.syncFail || !sync && test.fail) return json({ code: 'unavailable' }, 503);
            const owner = sync ? body.owner_staff_id : Number(url.searchParams.get('owner_userid'));
            const offset = sync ? 0 : Number(url.searchParams.get('offset'));
            const rows = test.empty ? [] : Array.from({ length: owner === 7 ? 51 : 1 }, (_, i) => ({ chat_reference: 'group-' + owner + '-' + i, owner_staff_id: owner, display_name: i === 0 ? '<群' + owner + '>' : '群' + i, member_count: i, refreshed_at: '2026-08-28T00:00:00Z' }));
            return json({ ...safety, items: rows.slice(offset, offset + 50), total: rows.length, offset, limit: 50, has_more: offset + 50 < rows.length });
          }
          const requireMutation = () => new Headers(init.headers).get('Idempotency-Key') && body.expected_revision === detail.plan.revision;
          const revise = () => { detail.plan.revision++; return json(detail); };
          if (url.pathname.endsWith('/plans')) {
            if (method === 'GET') return json({ ...safety, items: [detail.plan], total: 1, limit: 100, offset: 0, has_more: false });
            if (method !== 'POST' || !new Headers(init.headers).get('Idempotency-Key') || typeof body.name !== 'string') return json({ code: 'conflict' }, 409);
            detail.plan = { ...detail.plan, name: body.name, revision: 1, status: 'draft' }; detail.members = []; detail.group_assets = []; detail.nodes = [];
            return json(detail, 201);
          }
          if (url.pathname.endsWith('/content/preview')) return json({ ...safety, preview_lines: ['本地预览：' + detail.plan.name], issue_codes: [] });
          if (url.pathname.endsWith('/run-due/preview')) return json({ ...safety, plan_id: '10', snapshot_revision: detail.plan.revision, due_execution_count: 0, blockers: [] });
          if (url.pathname.endsWith('/executions')) return json({ ...safety, items: provenExecution ? [provenExecution] : [], total: provenExecution ? 1 : 0, limit: 100, offset: 0, has_more: false });
          if (url.pathname.endsWith('/webhook-descriptor')) return json({ ...safety, configured: false });
          if (/\/plans\/10\/(activate|pause|archive)$/.test(url.pathname)) {
            if (method !== 'POST' || !requireMutation()) return json({ code: 'conflict' }, 409);
            const action = url.pathname.split('/').pop();
            if (action === 'activate' && !['draft', 'paused'].includes(detail.plan.status)) return json({ code: 'conflict' }, 409);
            if (action === 'pause' && detail.plan.status !== 'active') return json({ code: 'conflict' }, 409);
            if (action === 'archive' && detail.plan.status === 'archived') return json({ code: 'conflict' }, 409);
            detail.plan.status = action === 'activate' ? 'active' : action === 'pause' ? 'paused' : 'archived'; return revise();
          }
          if (url.pathname.endsWith('/plans/10') && method === 'DELETE') {
            if (!requireMutation()) return json({ code: 'conflict' }, 409);
            detail.plan.status = 'archived'; return json({ ...safety });
          }
          if (url.pathname.endsWith('/plans/10') && method === 'PATCH') {
            if (!requireMutation() || typeof body.name !== 'string') return json({ code: 'conflict' }, 409);
            detail.plan.name = body.name; return revise();
          }
          if (url.pathname.includes('/members')) {
            if (!requireMutation()) return json({ code: 'conflict' }, 409);
            if (method === 'POST') detail.members.push({ staff_id: body.staff_id });
            else if (method === 'DELETE') detail.members = detail.members.filter((item) => String(item.staff_id) !== url.pathname.split('/').pop());
            else return json({ code: 'unexpected' }, 400);
            return revise();
          }
          if (url.pathname.includes('/group-assets')) {
            if (!requireMutation()) return json({ code: 'conflict' }, 409);
            if (method === 'DELETE') {
              const reference = url.pathname.split('/').pop();
              if (!detail.group_assets.some((item) => item.asset_reference === reference)) return json({ code: 'not_found' }, 404);
              detail.group_assets = detail.group_assets.filter((item) => item.asset_reference !== reference);
            } else if (method === 'POST') detail.group_assets.push({ group_asset_id: String(detail.group_assets.length + 10), asset_reference: body.asset_reference });
            else return json({ code: 'unexpected' }, 400);
            detail.plan.revision++;
            return json(detail);
          }
          if (url.pathname.endsWith('/nodes')) {
            if (method !== 'POST' || !requireMutation()) return json({ code: 'conflict' }, 409);
            detail.nodes.push({ node_id: String(detail.nodes.length + 1), position: body.position, kind: body.kind, message_text: body.message_text, delay_minutes: body.delay_minutes, material_plan: body.material_plan });
            return revise();
          }
          if (url.pathname.endsWith('/plans/10')) return json(detail);
          return json({ code: 'unexpected_test_route' }, 404);
        };
      } else if (messageHistoryHttp) {
        window.Headers = Headers;
        const test = window.__messageHistoryTest = { calls: [], fail: false, empty: false, unsafe: false };
        const rows = Array.from({ length: 21 }, (_, i) => ({ id: 71 + i, source_id: i + 1, sequence: i === 0 ? null : -i, customer_id: i === 0 ? null : 9, chat_type: i % 2 ? 'group' : 'private', message_type: 'text', content_masked: i === 0 ? null : i === 1 ? '' : '<img src=x onerror=alert(1)> 脱敏正文\n第二行', original_send_time: i === 1 ? '2024-01-02T03:04:05+08:00' : '2024-01-02 03:04:05', send_time_basis: i === 1 ? 'explicit_offset' : 'civil_unzoned', sent_at: i === 1 ? '2024-01-01T19:04:05Z' : null, created_at: '2026-08-28T00:00:00.123456Z', source_payload_digest: Array(32).fill(i) }));
        const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
        const json = (body, status = 200) => ({ status, headers: new Headers(), text: async () => JSON.stringify(body) });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', credentials: init.credentials, body: init.body });
          if (test.fail) return json({ code: 'unavailable' }, 503);
          if (url.pathname === '/api/admin/message-history') {
            const limit = Number(url.searchParams.get('limit')), offset = Number(url.searchParams.get('offset'));
            const filtered = test.empty ? [] : rows.filter((row) => (!url.searchParams.has('customer_id') || row.customer_id === Number(url.searchParams.get('customer_id'))) && (!url.searchParams.has('chat_type') || row.chat_type === url.searchParams.get('chat_type')));
            return json({ ...safety, items: filtered.slice(offset, offset + limit).map((row) => test.unsafe ? { ...row, sender: 'forbidden-identity' } : row), total: filtered.length, limit, offset });
          }
          if (url.pathname.startsWith('/api/admin/message-history/')) {
            const item = rows.find((row) => row.id === Number(url.pathname.split('/').pop()));
            return item ? json({ ...safety, item }) : json({ code: 'unavailable' }, 503);
          }
          return json({ code: 'unexpected_current_or_provider_route' }, 404);
        };
      } else if (couponHistoryHttp) {
        window.Headers = Headers;
        const test = window.__couponHistoryHttpTest = { calls: [], fail: couponHistoryHttp.fail || false };
        const at = '2026-08-28T00:00:00.000000Z';
        const before = '2025-01-01T00:00:00Z';
        const definitions = Array.from({ length: 21 }, (_, i) => ({ id: 31 + i, source_coupon_id: 7 + i, name: `历史券 ${i + 1}`, discount_amount_total: 9, currency: 'CNY', status: 'archived', availability_status: 'archived', history_only: true, original_status: 'expired', total_issue_limit: 100, per_user_issue_limit: 2, issued_count: 26, claim_starts_at: before, claim_ends_at: at, validity_mode: 'relative_days', use_starts_at: null, use_ends_at: null, relative_validity_days: 7, instructions: '<img src=x onerror=alert(1)>', target_refs: ['standard_product:8', 'standard_product:2'], first_claim_at: null, created_by: 2, updated_by: 2, version: 1, created_at: before, updated_at: at }));
        const claims = Array.from({ length: 21 }, (_, i) => ({ id: 41 + i, source_claim_id: 9 + i, source_coupon_id: 7, coupon_id: 31, customer_id: i ? 71 : null, claim_no: `claim-${i + 1}`, status: i ? 'consumed' : '', discount_amount_total: 0, currency: 'CNY', valid_from: at, valid_until: before, claimed_at: at, reserved_at: null, consumed_at: before, expired_at: null, created_at: at, updated_at: before }));
        const redemptions = [{ id: 51, source_redemption_id: 10, source_claim_id: 9, source_order_id: 11, claim_history_id: 41, order_id: null, out_trade_no: '', status: 'released', original_amount_total: 5, discount_amount_total: 9, payable_amount_total: 17, currency: 'CNY', reserved_until: before, release_reason: '<b>原始原因</b>', reserved_at: at, consumed_at: null, released_at: before, created_at: at, updated_at: before }];
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', credentials: init.credentials });
          if (test.fail === true || test.fail === url.pathname) return json({ code: 'unavailable' }, 503);
          const rows = url.pathname === '/api/admin/coupon-history' ? definitions : url.pathname === '/api/admin/coupon-history/31/claims' ? claims : url.pathname === '/api/admin/coupon-history/31/redemptions' ? redemptions : undefined;
          if (!rows) return json({ code: 'unexpected_coupon_history_request' }, 500);
          const limit = Number(url.searchParams.get('limit'));
          const offset = Number(url.searchParams.get('offset'));
          const total = couponHistoryHttp.empty ? 0 : rows.length;
          return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, items: total ? rows.slice(offset, offset + limit) : [], total, limit, offset, ...(url.pathname.endsWith('/coupon-history') ? {} : { coupon_id: 31 }) });
        };
        return;
      }
      if (groupOpsHistoryHttp) {
        const calls = [];
        const failures = {};
        const planID = '9007199254740993';
        const date = '2026-08-28T01:02:03.123456Z';
        const rows = {
          plans: { plan_id: planID, name: '历史计划 <img src=x onerror=alert(1)>', status: 'archived', revision: 1, source_plan_id: 8, source_code: 'legacy-code', plan_type: 'normal', original_status: 'active', owner_staff_id: null, created_at: date, updated_at: date, archived_at: null },
          directory: { id: 1, source_kind: 'group_chats', source_id: 9, chat_reference: 'historical-chat', display_name: null, owner_staff_id: null, owner_name: null, member_count: 0, internal_member_count: null, external_member_count: null, original_status: '', recorded_at: date },
          groups: { id: 2, source_group_id: 10, source_plan_id: 8, plan_id: planID, chat_reference: 'plan-chat', display_name: '历史群', owner_staff_id: null, internal_member_count: 0, external_member_count: 2, original_status: 'removed', created_at: date, removed_at: null },
          nodes: { id: 3, source_node_id: 11, source_plan_id: 8, plan_id: planID, day_index: 0, trigger_time: '  入群后  ', sort_order: 0, original_status: 'legacy_disabled', action_title: '历史标题 <img src=x onerror=alert(1)>', text_content: '<script>历史消息</script>', attachments: [{ kind: 'image', id: 'm1' }], content_package: { text: '<script>历史消息</script>' }, created_at: date, updated_at: date },
        };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers(), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.Headers = Headers;
        window.__groupOpsHistoryHttpTest = { calls, failures };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          const kind = url.pathname.split('/').at(-1);
          if (!url.pathname.startsWith('/api/admin/automation-conversion/group-ops/history/') || !rows[kind]) return json({ code: 'unexpected_history_request' }, 500);
          if (failures[kind]) return json({ code: 'unavailable', detail: '<b>private database error</b>' }, failures[kind]);
          const limit = Number(url.searchParams.get('limit'));
          const offset = Number(url.searchParams.get('offset'));
          const total = groupOpsHistoryHttp.empty ? 0 : 21;
          const items = Array.from({ length: Math.min(limit, Math.max(0, total - offset)) }, (_, index) => kind === 'directory' && (offset + index) % 2 ? { ...rows[kind], source_kind: 'wecom_group_chat_snapshots', source_id: null, member_count: null, internal_member_count: 0, external_member_count: 2 } : rows[kind]);
          return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, items, total, limit, offset, ...(kind === 'groups' || kind === 'nodes' ? { plan_id: planID } : {}) });
        };
        // The actual Host loads these two immutable/owned scripts in this order before the frozen admin module.
        window.eval(fs.readFileSync(path.join(ROOT, 'donors/ai-assistant-production/static/send_content_readonly_detail.js'), 'utf8'));
        window.eval(fs.readFileSync(path.join(ROOT, '..', 'internal/webshell/static/admin_console/groupops_history_readonly_bridge.js'), 'utf8'));
        return;
      }
      if (automationHistoryHttp) {
        window.Headers = Headers;
        const state = window.__automationHistoryHttpTest = { calls: [], fail: false };
        const at = '2026-08-28T01:02:03.123456Z', digest = Array(32).fill(2);
        const identity = { id: 7, source_id: 9, source_key_digest: digest, source_payload_digest: digest };
        const rows = {
          sops: { ...identity, pool_key: 'source', day_index: -1, content_masked: '<b>遮罩内容</b>', images_digest: digest, original_enabled: true, created_at: at, updated_at: at },
          configs: { ...identity, agent_code: 'old', display_name: '旧配置', scenario_code: 'source', original_enabled: false, draft_version: -1, published_version: 0, published_at: '', last_modified_at: 'source civil time', last_modified_source: 'source', submitted_for_publish: true, submitted_at: '', created_at: at, updated_at: at, actors_digest: digest, config_digest: digest },
          prompts: { ...identity, agent_code: 'old', display_name: '旧提示词', original_enabled: false, version: -2, created_at: at, updated_at: at, prompt_digest: digest },
          agents: { ...identity, program_source_id: 0, workflow_source_id: -1, node_source_id: 0, task_source_id: 0, agent_code: 'old', agent_name: '旧执行器', original_type: 'source', original_status: 'source', sort_order: -4, original_enabled: false, created_at: at, updated_at: at, archived_at: '', actors_digest: digest, configuration_digest: digest },
        };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          state.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body });
          const match = /^\/api\/admin\/automation-history\/(sops|configs|prompts|agents)(?:\/(7))?$/.exec(url.pathname);
          const json = (body, status = 200) => ({ status, headers: new Headers(), text: async () => JSON.stringify(body) });
          if (!match || state.fail) return json({ code: 'unavailable' }, 503);
          const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
          if (match[2]) return json({ ...safety, item: rows[match[1]] });
          return json({ ...safety, items: [rows[match[1]]], total: 1, limit: Number(url.searchParams.get('limit')), offset: Number(url.searchParams.get('offset')) });
        };
        return;
      }
      if (audienceHistoryHttp) {
        const state = { calls: [], fail: false, empty: !!audienceHistoryHttp.empty };
        const date = '2026-08-28T01:02:03.123456Z';
        const common = { id: 7, source_id: 107, original_status: ' active ', created_at: date, updated_at: date };
        const digest = Array.from({ length: 32 }, (_, i) => i);
        const fixtures = {
          groups: { ...common, name: '历史分组' },
          packages: { ...common, id: 42, name: '历史包', package_key: 'history-package', group_history_id: null, current_version_source_id: null, natural_language_definition: '历史自然语言定义', query_mode: 'legacy', identity_policy: 'legacy', incremental_enabled: true, daily_enabled: false, incremental_interval_seconds: -7, daily_refresh_time: '12:00', timezone: '源时区', lookback_seconds: 0, last_incremental_at: null, last_daily_refreshed_at: null, next_incremental_at: null, next_daily_at: null, paused_reason: '', runtime_digest: digest, sql_definition: 'RAW_SQL_MUST_NOT_RENDER' },
          versions: { ...common, package_history_id: 42, version_number: -2, template_key: '模板', template_version: 0, template_fingerprint: 'fingerprint', natural_language_explanation: '历史说明', published_at: null, definition_digest: digest, ai_prompt: 'RAW_EXECUTABLE_MUST_NOT_RENDER' },
          senders: { ...common, package_history_id: 42, staff_id: null, display_name: '历史发送人', priority: -3 },
          rules: { ...common, id: 8, rule_key: 'rule', display_name: '历史规则', description: '历史规则说明', rule_type: 'legacy', owner_staff_id: null },
          rule_versions: { ...common, rule_history_id: 8, version: 0, executor_type: 'legacy', published_at: null, definition_digest: digest },
          definitions: { ...common, id: 9, code: 'definition', display_name: '历史定义', description: '历史定义说明', source_type: 'legacy', sql_dialect: 'postgresql', version: -1, cached_headcount: -4, usage_count: 0, last_refreshed_at: null, definition_digest: digest },
          members: { ...common, package_history_id: 42, customer_id: null, identity_kind: 'legacy_unionid', first_entered_at: date, last_seen_at: date, last_updated_at: date, exited_at: null, payload_digest: digest, unionid: 'RAW_UNION_MUST_NOT_RENDER' },
        };
        window.Headers = Headers;
        window.__audienceHistoryHttpTest = state;
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          state.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body });
          if (!url.pathname.startsWith('/api/admin/audience-history/')) return json({ code: 'unexpected_current_audience_request' }, 500);
          if (state.fail) return json({ code: 'unavailable', message: 'RAW_ERROR_MUST_NOT_RENDER' }, 503);
          const safety = { source: 'v1_history', read_only: true, real_external_call_executed: false };
          if (url.pathname === '/api/admin/audience-history/packages/42') return json({ ...safety, item: fixtures.packages });
          if (url.pathname === '/api/admin/audience-history/definitions/9') return json({ ...safety, item: fixtures.definitions });
          const kind = url.pathname.includes('/rules/8/') ? 'rule_versions' : url.pathname.split('/').pop();
          if (!fixtures[kind]) return json({ code: 'unexpected_history_path' }, 500);
          const limit = Number(url.searchParams.get('limit'));
          const offset = Number(url.searchParams.get('offset'));
          const total = state.empty ? 0 : 21;
          const items = Array.from({ length: Math.min(limit, Math.max(0, total - offset)) }, (_, i) => ({ ...fixtures[kind], id: fixtures[kind].id + offset + i, source_id: 1000 + offset + i, ...(kind === 'packages' ? { name: `历史包${offset + i + 1}` } : {}), ...(i === 1 && kind === 'members' ? { customer_id: 123 } : {}), ...(i === 1 && kind === 'senders' ? { staff_id: 234 } : {}), ...(i === 1 && kind === 'rules' ? { owner_staff_id: 345 } : {}) }));
          const parent = ['versions', 'senders', 'members'].includes(kind) ? { package_id: 42 } : kind === 'rule_versions' ? { rule_id: 8 } : {};
          return json({ ...safety, ...parent, items, total, limit, offset });
        };
        return;
      }
      if (h5Http) {
        const calls = [];
        let submissionAttempt = 0;
        window.Headers = Headers;
        window.__h5HttpTest = { calls };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body ? JSON.parse(String(init.body)) : null });
          if (url.pathname === '/api/public/questionnaires/uat-survey') return json(h5Http.definition, h5Http.definitionStatus || 200);
          if (url.pathname === '/api/h5/surveys/session') return json(h5Http.session || { code: 'survey_oauth_required' }, h5Http.sessionStatus || 401);
          if (url.pathname === '/api/public/questionnaires/uat-survey/submissions') {
            const status = h5Http.submissionStatuses?.[submissionAttempt++] ?? 202;
            if (status === 'network') throw new Error('network outcome unknown');
            return json(status === 202 ? { completion_action: { type: 'default' }, receipt: { questionnaire_id: 7, definition_version: 3, submission_id: 901 } } : { code: 'unavailable' }, status);
          }
          if (url.pathname === '/api/public/survey-submission-results/query') return json(h5Http.result, h5Http.resultStatus || 200);
          return json({ code: 'unexpected_h5_request' }, 500);
        };
        return;
      }
      if (serviceHistoryHttp) {
        const test = { calls: [], failure: serviceHistoryFailure };
        window.__serviceHistoryHttpTest = test;
        const at = '2026-08-27T10:11:12.123456Z';
        const json = (data, status = 200) => ({ status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          test.calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          const definition = url.pathname === '/api/admin/service-period-history';
          const entitlement = url.pathname === '/api/admin/service-period-history/17/entitlements';
          const event = url.pathname === '/api/admin/service-period-history/17/events';
          if (!definition && !entitlement && !event) return json({ code: 'unexpected_history_request' }, 500);
          if (test.failure === 'all' || (test.failure === 'events' && event)) return json({ code: 'history_unavailable' }, 503);
          const offset = Number(url.searchParams.get('offset'));
          const limit = Number(url.searchParams.get('limit'));
          const total = serviceHistoryEmpty ? 0 : 21;
          const items = Array.from({ length: Math.min(limit, Math.max(0, total - offset)) }, (_, index) => {
            const rowID = offset + index + 17;
            if (definition) return { id: rowID, source_definition_id: rowID + 100, product_id: 91, product_code: 'P-91', product_name: '历史周期商品 <原名>', price_minor: 9900, currency: 'CNY', membership_config_id: '', membership_config_name: '历史配置', duration_days: -3, deleted: true, created_at: at, updated_at: at };
            if (entitlement) return { id: rowID, source_entitlement_id: rowID + 200, definition_id: 17, customer_id: index ? 7 : null, membership_config_id: '', status: index ? 'active' : 'expired', start_at: at, end_at: '2025-01-01T00:00:00Z', last_order_id: null, last_out_trade_no: '', renewal_count: -2, created_at: at, updated_at: at };
            return { id: rowID, source_event_id: rowID + 300, definition_id: 17, entitlement_id: null, customer_id: null, order_id: null, event_id: 'event-' + rowID, event_type: index ? 'admin_adjusted' : 'grant_failed_missing_unionid', duration_days: -7, out_trade_no: '', before_start_at: null, before_end_at: null, after_start_at: null, after_end_at: null, created_at: at };
          });
          return json({ source: 'v1_history', read_only: true, real_external_call_executed: false, definition_id: 17, items, total, limit, offset });
        };
        return;
      }
      if (orderHistoryHttp) {
        const calls = [];
        const order = { id: 12, record_origin: 'v1_history', created_at: '2026-08-28T00:00:00Z', merchant_order_no: 'V1-H-12', out_trade_no: 'V1-H-12', order_no: 'V1-H-12', platform_transaction_no: 'TX-H-12', transaction_id: 'TX-H-12', payer_name: '历史客户', mobile: '', product_code: 'course-history', product_name: '历史课程', amount_yuan: '99.00', currency: 'CNY', status: 'paid', status_label: '已支付', provider: 'wechat', provider_label: '微信支付', detail_url: '/api/admin/orders/V1-H-12', refundable_amount_total: 0 };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.__orderHistoryHttpTest = { calls };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          if (url.pathname === '/api/admin/orders') return json({ items: [order], total: 1, limit: 20, has_more: false });
          if (url.pathname === '/api/admin/orders/V1-H-12') return json(order);
          if (url.pathname === '/api/admin/orders/V1-H-12/items') return json({ items: [] });
          if (url.pathname === '/api/admin/refunds') return json({ items: [{ refund_id: 'R-801', refund_amount_total: 1990, status: 'history_closed', reason: '历史退款', created_at: '2026-08-28T00:00:00Z' }], refunds: [{ refund_id: 'R-801', refund_amount_total: 1990, status: 'history_closed', reason: '历史退款', created_at: '2026-08-28T00:00:00Z' }], total: 1, limit: 20, offset: 0, has_more: false });
          if (url.pathname === '/api/admin/wechat-pay/orders/V1-H-12/external-push-deliveries') return json({ items: [], total: 0 });
          return json({ code: 'unexpected_order_history_request' }, 500);
        };
        return;
      }
      if (miniProgramHttp) {
        const calls = [];
        window.__miniProgramHttpTest = { calls };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          if (url.pathname !== '/api/admin/miniprogram-library') return json({ code: 'unexpected_mini_program_request' }, 500);
          if (url.searchParams.get('q') === '网络失败') return json({ code: 'unavailable' }, 503);
          const offset = Number(url.searchParams.get('offset'));
          const shrunk = url.searchParams.get('q') === '已收缩';
          return json({
            items: shrunk && offset === 50 ? [] : [{ id: offset + 1, name: `历史卡片-${offset + 1}`, appid: 'wx-history', pagepath: 'pages/history', title: '历史素材', thumbnail_status: 'ready', enabled: false }],
            total: shrunk ? 50 : 120,
            limit: Number(url.searchParams.get('limit')),
            offset,
          });
        };
        return;
      }
      if (opsGuardHttp) {
        const calls = [];
        window.__opsGuardTest = { calls };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          const body = JSON.stringify({ items: [], channels: [], questionnaires: [], total: 0 });
          return { ok: true, status: 200, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => body, json: async () => JSON.parse(body), clone() { return this; } };
        };
        return;
      }
      if (channelHttp) {
        const calls = [];
        const channel = { id: 49, channel_type: 'qrcode', carrier_type: 'qrcode', channel_name: 'V1 历史渠道', channel_code: 'v1-history-49', status: 'inactive', scene_value: '', qr_url: '', owner_staff_id: '', customer_channel: '', link_url: '', final_url: '', welcome_message: '', welcome_image_library_ids: [], welcome_miniprogram_library_ids: [], welcome_attachment_library_ids: [], welcome_group_invite_library_ids: [], auto_accept_friend: false, entry_tag_id: '', entry_tag_name: '', entry_tag_group_name: '', assignment_mode: 'single_owner', assignment_strategy: 'ratio', overflow_policy: '', assignment_config_json: {}, assignees: [], assignee_count: 0, channel_contact_count: 0 };
        if (channelQrUrl) channel.qr_download_url = '/api/admin/channels/49/assets/qr.png';
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.__channelHttpTest = { calls };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          if (channelHttpFailure && url.pathname === '/api/admin/channels') return json({ code: 'unavailable' }, 503);
          if (url.pathname === '/api/admin/channels') return json({ channels: [channel], total: 1, limit: 50, offset: 0 });
          if (url.pathname === '/api/admin/channels/49') return json({ channel });
          if (url.pathname === '/api/admin/channels/49/history') {
            if (channelHistoryHttpFailure) return json({ code: 'unavailable' }, 503);
            const limit = Number(url.searchParams.get('limit'));
            const offset = Number(url.searchParams.get('offset'));
            const sourceContactId = 801 + offset;
            return json({
              ok: true,
              source: 'v1_history',
              read_only: true,
              real_external_call_executed: false,
              channel_id: 49,
              contacts: channelHistoryEmpty ? [] : [{ id: 901 + offset, channel_id: 49, source_contact_id: sourceContactId, customer_id: offset === 0 ? 21 : null, owner_reference: 'legacy-owner-7', first_entered_at: '2026-08-01T10:00:00Z', last_entered_at: '2026-08-02T11:00:00Z', enter_count: 2, created_at: '2026-08-03T00:00:00Z', updated_at: '2026-08-03T00:00:00Z' }],
              total: channelHistoryEmpty ? 0 : 51,
              limit,
              offset,
              assignees: channelHistoryEmpty ? [] : [{ id: 701, channel_id: 49, source_assignee_id: 51, staff_reference: 'legacy-staff-9', display_name_snapshot: '历史客服', priority: 0, ratio_percent: null, max_scans_24h: null, status: 'inactive', source_created_at: '2026-08-01T08:00:00.000000', source_updated_at: '2026-08-02T08:00:00.000000' }],
            });
          }
          if (url.pathname === '/api/admin/wecom/tags') return json({ items: [] });
          if (url.pathname === '/api/admin/wecom/tag-groups') return json({ items: [] });
          if (url.pathname === '/api/admin/channels/49/acquisition-assets' && (init.method || 'GET') === 'GET') return json({ items: [], limit: 20, has_more: false, next_cursor: '' });
          if (url.pathname.startsWith('/api/admin/channels/49/')) return json({ code: 'unavailable' }, 503);
          return json({ code: 'unexpected_channel_request' }, 500);
        };
        return;
      }
      if (couponHttp) {
        const calls = [];
        const coupon = { id: 31, name: '新客券', discount_amount_total: 10000, total_issue_limit: 1200, issued_count: 0, per_user_issue_limit: 1, claim_starts_at: '2026-08-01T00:00:00Z', claim_ends_at: '2026-08-31T00:00:00Z', validity_mode: 'relative_days', relative_validity_days: 7, instructions: '说明', target_refs: ['standard_product:8'], status: 'draft', version: 1 };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.__couponHttpTest = { calls };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET' });
          if (couponHttpFailure && url.pathname === '/api/admin/coupons/31') return json({ code: 'unavailable' }, 503);
          if (url.pathname === '/api/admin/coupons/31') return json({ ok: true, coupon });
          if (url.pathname === '/api/admin/coupons/product-options') return json({ ok: true, items: [{ target_ref: 'standard_product:9', name: '增长课', price_minor: 9900, currency: 'CNY' }], total: 1, limit: 20, offset: 0 });
          return json({ code: 'unexpected_coupon_request' }, 500);
        };
        return;
      }
      if (audienceHttp) {
        const calls = [];
        let packageVersion = 3;
        let configurationVersion = 2;
        const packageDto = () => ({
          package_id: 6,
          name: '待唤醒客户',
          group_id: 2,
          lifecycle: audienceActive ? 'active' : 'paused',
          version: packageVersion,
          refresh_mode: 'manual',
          refresh_cron: null,
          member_count: audienceEmpty ? 0 : 2,
          refreshed_at: null,
          refresh_status: 'idle',
          created_at: '2026-08-27T00:00:00Z',
          updated_at: '2026-08-27T00:00:00Z',
          definition: { field: 'stage_id', op: 'eq', value: 3 },
        });
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.document.cookie = 'aicrm_csrf=' + 'c'.repeat(43);
        window.__audienceHttpTest = { calls };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body ? JSON.parse(String(init.body)) : null });
          if (url.pathname === '/api/admin/ai-audience/templates') return json({
            items: [
              { key: 'active_contacts', version: 1, parameters: [] },
              { key: 'stage_any', version: 1, parameters: [{ key: 'stage_ids', required: true }] },
              { key: 'tag_any', version: 1, parameters: [{ key: 'tag_ids', required: true }] },
              { key: 'owner_any', version: 1, parameters: [{ key: 'owner_staff_ids', required: true }] },
              { key: 'channel_any', version: 1, parameters: [{ key: 'channel_ids', required: true }] },
            ],
            local_projection: true,
            real_external_call_executed: false,
          });
          if (url.pathname.endsWith('/template-preview')) {
            const body = JSON.parse(String(init.body || '{}'));
            return json({
              package_id: 6,
              package_version: packageVersion,
              configuration_version: configurationVersion,
              selection: { key: body.template_key, version: body.template_version, parameters: body.parameters },
              definition: { field: 'is_deleted', op: 'eq', value: false },
              member_count: audienceEmpty ? 0 : 2,
              member_digest: 'a'.repeat(64),
              evaluated_at: '2026-08-27T00:00:00Z',
              saved: false,
              local_projection: true,
              real_external_call_executed: false,
            });
          }
          if (url.pathname.endsWith('/template-config')) {
            const body = JSON.parse(String(init.body || '{}'));
            packageVersion += 1;
            configurationVersion += 1;
            return json({
              package_id: 6,
              package_version: packageVersion,
              configuration_version: configurationVersion,
              selection: { key: body.template_key, version: body.template_version, parameters: body.parameters },
              definition: { field: 'is_deleted', op: 'eq', value: false },
              member_count: audienceEmpty ? 0 : 2,
              member_digest: 'a'.repeat(64),
              evaluated_at: '2026-08-27T00:00:00Z',
              saved: true,
              local_projection: true,
              real_external_call_executed: false,
            });
          }
          if (url.pathname === '/api/admin/automation-agents') return json({ items: [] });
          if (url.pathname === '/api/admin/ai-audience/package-groups') return json({ items: [{ group_id: 2, name: 'W4' }] });
          if (url.pathname === '/api/admin/ai-audience/packages') return json({ items: [packageDto()] });
          if (url.pathname === '/api/admin/ai-audience/packages/6') {
            if (init.method === 'PATCH') {
              packageVersion += 1;
              return json({ package: packageDto(), local_projection: true, real_external_call_executed: false });
            }
            return json({ package: packageDto(), local_projection: true, real_external_call_executed: false });
          }
          if (url.pathname.endsWith('/automation-binding')) return json({ binding: { version: 1 }, local_projection: true, real_external_call_executed: false });
          if (url.pathname.endsWith('/senders')) return json({ items: [], local_projection: true, real_external_call_executed: false });
          if (url.pathname.endsWith('/configuration-preview')) return json({ configuration_version: configurationVersion, package_version: packageVersion, member_count: audienceEmpty ? 0 : 2, member_digest: 'sha256:' + 'a'.repeat(64), evaluated_at: '2026-08-27T00:00:00Z', materialized: false, local_projection: true, real_external_call_executed: false });
          if (url.pathname.endsWith('/configuration')) {
            if (init.method === 'PUT') configurationVersion += 1;
            return json({ configuration: { version: configurationVersion }, local_projection: true, real_external_call_executed: false });
          }
          if (url.pathname.endsWith('/members')) return json({ items: [], local_projection: true, real_external_call_executed: false });
          return json({ code: 'unexpected_audience_request' }, 500);
        };
        return;
      }
      if (radarHttp) {
        Object.defineProperty(window.crypto, 'subtle', { configurable: true, value: globalThis.crypto.subtle });
        const calls = [];
        const downloads = [];
        const publicCode = 'rd_1234567890123456789012';
        const link = { link_id: 2, public_code: publicCode, name: '真实分享雷达', title: '真实分享雷达', destination_url: 'https://example.test/radar', cover_image_id: null, attachment_id: null, status: 'enabled', version: 1, created_by: 9, updated_by: 9, created_at: '2026-08-27T00:00:00Z', updated_at: '2026-08-27T00:00:00Z' };
        const projection = { link_id: 2, public_code: publicCode, status: 'enabled', available: true, share_path: '/r/' + publicCode, qr_payload: '/r/' + publicCode, local_projection: true, public_route_ready: true, real_external_call_executed: false };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.__radarHttpTest = { calls, downloads, lastBlob: null };
        window.URL.createObjectURL = (blob) => { window.__radarHttpTest.lastBlob = blob; return 'blob:radar-qr'; };
        window.URL.revokeObjectURL = () => {};
        window.HTMLAnchorElement.prototype.click = function () { downloads.push({ href: this.href, download: this.download }); };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body ? String(init.body) : null });
          if (url.pathname === '/api/admin/radar-links') return json({ items: [link], total: 1, limit: 50, offset: 0 });
          if (url.pathname === '/api/admin/radar-links/2/share') return json(projection);
          if (url.pathname === '/api/admin/attachment-library/uploads') return json({ upload_id: 44 }, 201);
          if (/^\/api\/admin\/attachment-library\/uploads\/44\/parts\/\d+$/.test(url.pathname)) return json({}, 204);
          if (url.pathname === '/api/admin/attachment-library/uploads/44/complete') return json({ attachment_id: 45 });
          return json({ code: 'unexpected_radar_request' }, 500);
        };
        return;
      }
      if (productHttp) {
        // productAdapter wraps the browser Fetch API while preserving the
        // frozen controller. Supply the browser constructors explicitly in
        // JSDOM so its `Request`/`Response` checks exercise the production
        // path instead of failing before the first authoritative product GET.
        window.Headers = Headers;
        window.Request = Request;
        window.Response = Response;
        const calls = [], downloads = [], opened = [];
        const projection = { schema_version: 1, status: 'active', enabled: true, buy_button_text: '立即购买', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, wecom_tagging: {}, slices: [] };
        const product = { id: 7, product_code: 'P/春季 sale', name: '真实商品', description: '公开商品', price_minor: 990, currency: 'CNY', stock_quantity: 5, images: [], admin_projection: projection, lifecycle: 'enabled', enabled: true, paid_order_count: 3, refund_order_count: 1, sold_count: 2, created_by: 9, version: 3, created_at: '2026-09-04T00:00:00Z', updated_at: '2026-09-04T00:00:00Z' };
        let externalConfiguration = { webhook_url: 'https://hooks.example.test/paid', url: 'https://hooks.example.test/paid', product_id: 7, product_kind: 'wechat_pay', enabled: true, configuration_reference: 'product-paid-notify', push_type: 'member_open', type: 'member_open', day: 30, frequency: 1, expires_at_ts: null, remark: '旧备注', custom_params: { campaign: 'control', count: 9007199254740992, enabled: true, nested: { keep: ' 空白 ' } }, custom_params_json: '{"campaign":"control","count":9007199254740993,"enabled":true,"nested":{"keep":" 空白 "}}', revision: 3, updated_at: '2026-09-04T00:00:00Z' };
        let externalTests = [{ product_id: 7, product_kind: 'wechat_pay', effect_id: 'eer_7', state: 'outcome_unknown', attempt_count: 1, provider_accepted: false, delivery_proven: false, real_external_call_executed: true, auto_retry_allowed: false, created_at: '2026-09-04T00:00:00Z', updated_at: '2026-09-04T00:01:00Z' }];
        let shareMode = 'valid';
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.__productHttpTest = { calls, downloads, opened, setShareMode: (mode) => { shareMode = mode; } };
        window.URL.createObjectURL = () => 'blob:product-qr';
        window.URL.revokeObjectURL = () => {};
        window.HTMLAnchorElement.prototype.click = function () { downloads.push({ href: this.href, download: this.download }); };
        window.open = (...args) => { opened.push(args); return null; };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin), method = init.method || 'GET';
          calls.push({ path: url.pathname, method, body: init.body ? String(init.body) : null, headers: new Headers(init.headers) });
          if (url.pathname === '/api/v1/products') return json({ items: [product], next_cursor: '' });
          if (url.pathname === '/api/v1/products/7') return json(product);
          if (url.pathname === '/api/admin/channels') return json({ ok: false, code: 'MALFORMED_REQUEST' }, 400);
          if (url.pathname === '/api/admin/image-library') return json({ items: [] });
          if (url.pathname === '/api/admin/wecom/tag-groups' || url.pathname === '/api/admin/wecom/tags') return json({ items: [] });
          if (url.pathname === '/api/admin/wechat-pay/products/7/external-push' && method === 'GET') return json(externalConfiguration);
          if (url.pathname === '/api/admin/wechat-pay/products/7/external-push' && method === 'PUT') {
            const update = JSON.parse(String(init.body || '{}'));
            const customParamsJSON = String(update.custom_params);
            externalConfiguration = { ...externalConfiguration, ...update, url: update.webhook_url, type: update.push_type, custom_params: JSON.parse(customParamsJSON), custom_params_json: customParamsJSON, revision: externalConfiguration.revision + 1, updated_at: '2026-09-04T00:03:00Z' };
            return json(externalConfiguration);
          }
          if (url.pathname === '/api/admin/wechat-pay/products/7/external-push/test' && method === 'GET') return json({ items: externalTests });
          if (url.pathname === '/api/admin/wechat-pay/products/7/external-push/test' && method === 'POST') {
            return json({ state: 'outcome_unknown', effect_id: 'eer_new_7', delivery_id: 'commerce_test_0123456789abcdef0123456789abcdef' }, 202);
          }
          if (url.pathname === '/api/admin/wechat-pay/products/7/share') {
            const share = { product_id: 7, product_code: product.product_code, lifecycle: 'enabled', available: true, purchase_url: '/p/P%2F%E6%98%A5%E5%AD%A3%20sale' };
            if (shareMode === 'wrong_id') return json({ ...share, product_id: 8 });
            if (shareMode === 'wrong_code') return json({ ...share, product_code: 'P/另一商品' });
            if (shareMode === 'nested_path') return json({ ...share, purchase_url: '/p/P/%E6%98%A5%E5%AD%A3%20sale' });
            if (shareMode === 'query') return json({ ...share, purchase_url: '/p/P%2F%E6%98%A5%E5%AD%A3%20sale?next=elsewhere' });
            return json(share);
          }
          return json({ code: 'unexpected_product_request' }, 500);
        };
        return;
      }
      if (serviceProductHttp) {
        const calls = [];
        const downloads = [];
        const product = { service_product_id: 8, product_code: 'SP-8', name: '季度会员', description: '本地周期商品', price_minor: 398000, currency: 'CNY', stock_quantity: 5, images: [], admin_projection: { schema_version: 1, status: 'service_period_enabled', enabled: true, buy_button_text: '', require_mobile: false, lead_program_id: null, lead_channel_id: null, lead_qr_title: '', lead_qr_subtitle: '', completion_redirect_enabled: false, completion_redirect_url: '', completion_target: null, wecom_tagging: {}, slices: [] }, lifecycle: 'enabled', enabled: true, archived: false, version: 3, created_at: '2026-08-01T00:00:00Z', updated_at: '2026-08-26T08:00:00Z' };
        const gridColumns = ['member_ref', 'service_product_id', 'customer_id', 'state', 'source', 'starts_at', 'expires_at', 'expired_at', 'removed_at', 'version', 'updated_at', 'display_name'].map((key) => ({ key, label: key, type: 'string', nullable: key !== 'member_ref' }));
        const memberRows = [
          { member_ref: 'spm_abcdefghijklmnopqrstuv', service_product_id: 8, customer_id: 21, display_name: '本地客户', state: 'active', source: 'manual', starts_at: '2026-08-01T00:00:00Z', expires_at: null, expired_at: null, removed_at: null, version: 2, updated_at: '2026-08-26T08:00:00Z' },
          { member_ref: 'spm_zyxwvutsrqponmlkjihgfe', service_product_id: 8, customer_id: 22, display_name: '过期客户', state: 'expired', source: 'paid_order', starts_at: '2026-07-01T00:00:00Z', expires_at: '2026-08-01T00:00:00Z', expired_at: '2026-08-01T00:00:00Z', removed_at: null, version: 1, updated_at: '2026-08-25T08:00:00Z' },
        ];
        let collaboratorRows = [{ collaborator_id: 6, service_product_id: 8, staff_id: 5, permission: 'view', version: 1, invited_by: 1, created_at: '2026-08-26T00:00:00Z', updated_at: '2026-08-26T00:00:00Z' }];
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data, clone() { return this; } });
        window.__serviceProductHttpTest = { calls, downloads, opened: [] };
        window.URL.createObjectURL = () => 'blob:service-product-qr';
        window.URL.revokeObjectURL = () => {};
        window.HTMLAnchorElement.prototype.click = function () { downloads.push({ href: this.href, download: this.download }); };
        window.open = (...args) => { window.__serviceProductHttpTest.opened.push(args); return null; };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          const method = init.method || 'GET';
          const body = init.body ? JSON.parse(String(init.body)) : null;
          calls.push({ path: url.pathname, query: url.search, method, body });
          if (url.pathname === '/api/admin/service-period-products') return json({ items: [product] });
          if (url.pathname === '/api/admin/service-period-products/8') return json({ product });
          if (url.pathname === '/api/admin/service-period-products/8/share') return json({ ok: true, service_product_id: 8, public_path: '/p/service_period/8', local_only: true, real_external_call_executed: false });
          if (url.pathname === '/api/admin/service-period-products/8/member-grid/access') return json({ product_id: 8, can_view: true, can_query: true, can_manage_views: false, can_share: false });
          if (url.pathname === '/api/admin/service-period-products/8/member-grid/schema') return json({ service_product_id: 8, columns: gridColumns });
          if (url.pathname === '/api/admin/service-period-products/8/member-views') return json({ product_id: 8, views: [{ id: 'default', name: '默认视图', source: 'built_in', read_only: true }] });
          if (url.pathname === '/api/admin/service-period-products/8/member-grid/share-settings') return json({ service_product_id: 8, saved_views: [], collaborators: collaboratorRows, external_share_supported: true, external_share_enabled: false, external_share_version: 0, real_external_call_executed: false, collaborator_edit_is_local_metadata_only: true, collaborator_edit_grants_central_permission: false });
          if (url.pathname === '/api/admin/common/operation-members') return json({ scope: 'group_ops', items: [{ staff_id: 5, sender_userid: 'staff-5', display_name: '客服五' }, { staff_id: 7, sender_userid: 'staff-7', display_name: '客服七' }], page_size: 100, provider_execution_eligible: true, real_external_call_executed: false, provider_accepted: false, delivery_proven: false });
          if (url.pathname === '/api/admin/service-period-products/8/member-grid/query') return json({ rows: memberRows, limit: 50, next_cursor: '', has_more: false });
          if (url.pathname === '/api/admin/service-period-products/8/member-grid/collaborators' && method === 'POST') {
            const row = { collaborator_id: 7, service_product_id: 8, staff_id: body.staff_id, permission: body.permission, version: 1, invited_by: 1, created_at: '2026-08-29T00:00:00Z', updated_at: '2026-08-29T00:00:00Z' };
            collaboratorRows = [...collaboratorRows, row];
            return json({ ok: true, collaborator: row, edit_permission_is_local_metadata_only: true, grants_central_products_permission: false }, 201);
          }
          const collaboratorID = url.pathname.match(/\/member-grid\/collaborators\/(\d+)$/)?.[1];
          if (collaboratorID && method === 'PUT') {
            const row = collaboratorRows.find((item) => item.collaborator_id === Number(collaboratorID));
            if (!row) return json({ code: 'not_found' }, 404);
            row.permission = body.permission;
            row.version += 1;
            return json({ ok: true, collaborator: row, edit_permission_is_local_metadata_only: true, grants_central_products_permission: false });
          }
          if (collaboratorID && method === 'DELETE') {
            const row = collaboratorRows.find((item) => item.collaborator_id === Number(collaboratorID));
            collaboratorRows = collaboratorRows.filter((item) => item.collaborator_id !== Number(collaboratorID));
            return json({ ok: true, deleted: true, collaborator: row, edit_permission_is_local_metadata_only: true, grants_central_products_permission: false });
          }
          return json({ code: 'unexpected_service_product_request' }, 500);
        };
        return;
      }
      if (campaignHttp) {
        const calls = [];
        const planID = 'ctp_' + 'a'.repeat(64);
        const campaignPrefix = '/api/admin/cloud-orchestrator/campaigns/spring-campaign';
        const localCampaign = { local_projection: true, real_external_call_executed: false, real_send: false, runtime_executed: false };
        const localTouchPlan = { local_only: true, provider_execution_eligible: false, runtime_executed: false, real_external_call_executed: false, delivery_proven: false };
        const campaign = { campaign_code: 'spring-campaign', name: '春季激活', approval_status: 'draft', runtime_status: 'idle', version: 3, updated_at: '2026-08-27T00:00:00Z' };
        const plan = { id: planID, campaign_code: 'spring-campaign', campaign_version: 3, source: { kind: 'customer_selection' }, target_count: 3, content_step_count: 1, created_at: '2026-08-27T00:00:00Z' };
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        window.__campaignHttpTest = { calls };
        window.fetch = async (input, init = {}) => {
          const url = new URL(String(input), window.location.origin);
          calls.push({ path: url.pathname, query: url.search, method: init.method || 'GET', body: init.body ? JSON.parse(String(init.body)) : null });
          if (url.pathname === `${campaignPrefix}/members`) {
            if (campaignHttp.failMembers) return json({ code: 'members_unavailable' }, 503);
            const offset = Number(url.searchParams.get('offset'));
            const limit = Number(url.searchParams.get('limit'));
            const items = offset === 0
              ? [
                { plan_id: planID, customer_id: 7, status: 'pending_review' },
                { plan_id: planID, customer_id: 8, status: 'approved' },
                { plan_id: planID, customer_id: 9, status: 'rejected' },
              ]
              : [{ plan_id: planID, customer_id: 51, status: 'approved' }];
            return json({ plan_id: planID, items, total: 51, limit, offset, safety: localTouchPlan });
          }
          if (url.pathname === `${campaignPrefix}/touch-plans`) return json({ items: [plan], ...localTouchPlan });
          if (url.pathname === campaignPrefix) return json({ campaign, steps: [], ...localCampaign });
          return json({ code: 'unexpected_campaign_request' }, 500);
        };
        return;
      }
      if (rel === 'admin/campaigns.html' && new URL(window.location.href).searchParams.get('legacy_admin_path') === '/admin/cloud-orchestrator/plans') {
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        const local = { local_only: true, provider_execution_eligible: false, runtime_executed: false, real_external_call_executed: false, delivery_proven: false };
        window.__planIndexCalls = [];
        window.fetch = async (input) => {
          const url = String(input);
          window.__planIndexCalls.push(url);
          return json({ items: [{ plan: { id: 'ctp_' + 'a'.repeat(64), campaign_code: 'spring-campaign', campaign_version: 3, source: { kind: 'customer_selection' }, target_count: 2, content_step_count: 1, created_at: '2026-08-27T00:00:00Z', ...local }, review_status: 'pending_review', review_version: 2 }], ...local });
        };
        return;
      }
      if (rel === 'admin/campaigns.html' && new URL(window.location.href).searchParams.get('recipient') === '7') {
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        const planID = 'ctp_' + 'a'.repeat(64);
        const local = { local_only: true, provider_execution_eligible: false, runtime_executed: false, real_external_call_executed: false, delivery_proven: false };
        const plan = { id: planID, campaign_code: 'spring-campaign', campaign_version: 3, source: { kind: 'customer_selection' }, target_count: 1, content_step_count: 1, created_at: '2026-08-27T00:00:00Z' };
        const handoffSafety = { local_only: true, provider_execution_eligible: false, real_external_call_executed: false, delivery_proven: false };
        const handoff = { id: 31, campaign_code: 'spring-campaign', plan_id: planID, review_version: 2, status: 'held', target_count: 1, step_count: 1, accepted_at: '2026-08-27T00:01:00Z', safety: handoffSafety };
        const dispatch = { handoff_id: 31, blocked: 0, accepted: 1, queued: 1, attempted: 0, executed: 0, outcome_unknown: 0, reconciled: 0, retryable_failed: 0, final_failed: 0, provider_execution_eligible: false, business_call_dispatched: true, real_external_call_executed: true, delivery_proven: false };
        window.__recipientCalls = [];
        window.fetch = async (input, init = {}) => {
          const url = String(input);
          window.__recipientCalls.push({ url, method: init.method || 'GET', body: init.body ? JSON.parse(String(init.body)) : null, headers: new Headers(init.headers) });
          if (url.endsWith('/recipients/7/dispatch')) return json(dispatch);
          if (url.endsWith('/dispatch-reconciliation')) return json(dispatch);
          if (url.endsWith('/reconciliation')) return json({ ...handoff, held_count: 1, blocked_count: 0, pending_count: 0, not_evaluated_count: 0, eligible_count: 1, inactive_count: 0, contact_policy_count: 0 });
          if (url.includes('/api/admin/outbound/campaign-handoffs/')) return json(handoff);
          if (url.endsWith('/recipients/7/review')) return json({ review: { canonical_customer_id: 7, status: 'approved', version: 2, updated_by_actor_id: 1, updated_at: '2026-08-27T00:00:00Z' }, ...local });
          if (url.endsWith(`/touch-plans/${planID}/review`)) return json({ review: { status: 'approved', version: 2, submitted_by_actor_id: 81, submitted_at: '2026-08-27T00:00:00Z', reviewed_by_actor_id: 82, reviewed_at: '2026-08-27T00:01:00Z' }, handoff: { status: 'held' }, ...local });
          if (url.endsWith('/recipients/7')) return json({ canonical_customer_id: 7, ...local });
          if (url.endsWith('/recipients?limit=50')) return json({ items: [{ canonical_customer_id: 7 }], next_cursor: null, ...local });
          if (url.endsWith(`/touch-plans/${planID}`)) return json({ ...plan, content: { steps: [{ step_index: 1, delay_minutes: 0, content: '本地审核内容' }] }, ...local });
          if (url.includes('/touch-plans')) return json({ items: [plan], ...local });
          return json({ campaign: { campaign_code: 'spring-campaign', name: '春季激活', approval_status: 'draft', runtime_status: 'idle', version: 3, updated_at: '2026-08-27T00:00:00Z' }, steps: [], local_projection: true, real_external_call_executed: false, real_send: false, runtime_executed: false });
        };
        return;
      }
      if (rel === 'admin/campaigns.html' && new URL(window.location.href).searchParams.get('view') === 'observability') {
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        window.__observabilityCalls = [];
        window.fetch = async (input, init = {}) => {
          const url = String(input);
          window.__observabilityCalls.push({ url, init });
          const parsed = new URL('http://localhost' + url);
          const traceID = parsed.searchParams.get('trace_id') || undefined;
          const sessionID = parsed.searchParams.get('session_id') || undefined;
          if (url.includes('/cloud-orchestrator/audit')) return json({ filter: { trace_id: traceID || '', session_id: sessionID || '', limit: 50 }, items: [{ event_id: 41, event_type: 'campaign_recipient_dispatch_requested', occurred_at: '2026-08-30T00:00:00Z', dispatched: true, pending: 0, processing: 0, completed: 1, final_failed: 0, outcome_unknown: 0 }], observed_at: '2026-08-30T00:00:01Z', local_facts_only: true, real_external_call_executed: false, delivery_proven: false });
          if (url.includes('/push-center/stats')) return json({ ok: true, counts: { total: 2, pending: 1, running: 0, succeeded: 0, sent: 1, failed: 0, shadow_warning: 0, by_effective_status: {}, by_status: {}, by_section: {} }, sections: [], status_definitions: [], filters: traceID ? { trace_id: traceID } : {}, route_owner: 'ai_crm_next', real_external_call_executed: false, runtime_queue: {}, capability_owner: 'ai_crm_next/platform_foundation/push_center' });
          return json({ ok: true, sections: [{ key: 'order', label: '订单', count: 2 }], status_definitions: [], filters: traceID ? { trace_id: traceID } : {}, route_owner: 'ai_crm_next' });
        };
        return;
      }
      if (rel === 'admin/campaigns.html' && new URL(window.location.href).searchParams.get('view') === 'external-effects') {
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data) });
        const local = { local_fact_only: true, real_external_call_executed: false, delivery_proven: false, delivery_semantics: 'local_state_not_delivery_proof' };
        const pushJob = { job_id: 18, task_id: 5, customer_id: 7, status: 'outcome_unknown', attempt_count: 1, failure_present: true, failure_class: 'outcome_unknown', provider_receipt_present: false, queue_job: { river_job_id: 9, generation: 1, kind: 'outbound_enqueue_one' }, created_at: '2026-08-27T00:00:00Z', status_updated_at: '2026-08-27T00:01:00Z', ...local };
        const pushEnvelope = { ok: true, fallback_used: false, source_status: 'v2_outbound_service', ...local };
        window.fetch = async (input) => {
          const url = String(input);
          if (url.includes('/external-effects/diagnostics')) return json({ accepted: 1, queued: 2, attempted: 1, outcome_unknown: 1, retryable_failed: 0 });
          if (url.includes('/external-effects/jobs')) return json({ ok: true, items: [{ id: 'eej_v1_abcdefghijklmnopqrstuv', status: 'outcome_unknown', classification: 'manual_review', attempt_count: 1, created_at: '2026-08-27T00:00:00Z', status_updated_at: '2026-08-27T00:01:00Z' }], next_cursor: null, page_size: 50, applied_filters: { status: null, classification: null }, provider_execution_eligible: false, ...local });
          if (url.includes('/external-effects')) return json({ items: [{ id: '18', owner: 'campaign', kind: 'campaign_dispatch', state: 'accepted', attempt_count: 0, generation: 1, updated_at: '2026-08-27T00:00:00Z' }] });
          if (url.includes('/push-center/sections')) return json({ ok: true, sections: [{ key: 'order', label: '订单', count: 2 }], status_definitions: [], filters: {}, route_owner: 'ai_crm_next' });
          if (url.includes('/push-center/stats')) return json({ ok: true, counts: { total: 2, pending: 1, running: 0, succeeded: 0, sent: 1, failed: 0, shadow_warning: 0, by_effective_status: {}, by_status: {}, by_section: {} }, sections: [], status_definitions: [], filters: {}, route_owner: 'ai_crm_next', real_external_call_executed: false, runtime_queue: {}, capability_owner: 'ai_crm_next/platform_foundation/push_center' });
          if (url.endsWith('/reconciliation')) return json({ ...pushEnvelope, job: pushJob, attempts: [{ attempt_id: 1, history_id: 2, generation: 1, river_job_id: 9, attempt: 1, max_attempts: 3, state: 'outcome_unknown', failure_present: true, failure_class: 'outcome_unknown', provider_receipt_present: false, dispatch_started_at: '2026-08-27T00:00:00Z', ...local }], control_receipts: [] });
          if (url.endsWith('/18')) return json({ ...pushEnvelope, job: pushJob });
          return json({ ...pushEnvelope, jobs: [pushJob], items: [pushJob], count: 1, has_more: false, limit: 50, offset: 0 });
        };
        return;
      }
      if (rel === 'admin/orders.html') {
        window.URL.createObjectURL = () => 'blob:wechat-order-export';
        window.URL.revokeObjectURL = () => {};
        window.HTMLAnchorElement.prototype.click = function () { window.__orderDownload = { href: this.href, download: this.download }; };
      }
      if (rel === 'admin/config.html') {
        const digest = 'a'.repeat(64);
        const token = 'b'.repeat(43);
        const state = { corpId: 'ww-existing', agentId: 1001 };
        const accessMembers = [
          { admin_user_id: 7, display_name: '管理员甲', role: 'admin', staff_id: 7, staff_wecom_userid: 'staff-7', staff_name: '管理员甲', is_active: true, login_enabled: true },
          { admin_user_id: 8, display_name: '运营乙', role: 'ops', staff_id: 8, staff_wecom_userid: 'staff-8', staff_name: '运营乙', is_active: true, login_enabled: false },
          { admin_user_id: 9, display_name: '停用成员', role: 'sales', staff_id: 9, staff_wecom_userid: 'staff-9', staff_name: '停用成员', is_active: false, login_enabled: false },
        ];
        window.document.cookie = 'aicrm_csrf=' + 'c'.repeat(43);
        const snapshot = () => ({
          ok: true,
          expected_digest: digest,
          editable: { 'wecom.corp_id': state.corpId, 'wecom.agent_id': state.agentId },
          editable_configured: { 'wecom.corp_id': true, 'wecom.agent_id': true },
          masked: {
            'wecom.secret': { configured: true, masked: true },
            'wecom.callback_token': { configured: true, masked: true },
            'wecom.callback_aes_key': { configured: false, masked: true },
            'ai.api_key': { configured: false, masked: true },
          },
          admin_action_token: token,
          external: false,
          local_only: true,
          runtime_applied: false,
        });
        const json = (data, status = 200) => ({
          ok: status >= 200 && status < 300,
          status,
          headers: new Headers({ 'Content-Type': 'application/json' }),
          text: async () => JSON.stringify(data),
        });
        window.__setupWizardTest = { posts: [] };
        window.__adminAccessTest = { puts: [] };
        window.fetch = async (input, init = {}) => {
          const url = String(input);
          if (url.includes('/api/admin/admin-access')) {
            if ((init.method || 'GET') === 'PUT') {
              const body = JSON.parse(init.body || '{}');
              const headers = new Headers(init.headers);
              window.__adminAccessTest.puts.push({ body, key: headers.get('Idempotency-Key'), csrf: headers.get('X-CSRF-Token') });
              for (const update of body.members || []) {
                const member = accessMembers.find((item) => item.admin_user_id === update.admin_user_id && item.is_active);
                if (member) member.login_enabled = update.login_enabled;
              }
              return json({ ok: true, members: accessMembers, idempotency_key: window.__adminAccessTest.puts.at(-1).key, local_only: true, external: false });
            }
            return json({ ok: true, members: accessMembers, local_only: true, external: false });
          }
          if (!url.includes('/api/admin/setup-wizard')) return json({ error: 'unexpected_request' }, 500);
          if ((init.method || 'GET') === 'POST') {
            const body = JSON.parse(init.body || '{}');
            window.__setupWizardTest.posts.push({ body, key: new Headers(init.headers).get('Idempotency-Key') });
            state.corpId = body['wecom.corp_id'];
            state.agentId = body['wecom.agent_id'];
            return json({
              ok: true,
              config: snapshot(),
              receipt: {
                idempotency_key: window.__setupWizardTest.posts.at(-1).key,
                replayed: false,
                audits: [{ key: 'wecom.corp_id', id: 1 }, { key: 'wecom.agent_id', id: 2 }],
                events: [{ key: 'wecom.corp_id', type: 'setting.updated' }, { key: 'wecom.agent_id', type: 'setting.updated' }],
              },
              external: false,
              local_only: true,
              runtime_applied: false,
            });
          }
          return json(snapshot());
        };
        return;
      }
      if (customerListHttp) {
        window.Response = Response;
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        const customers = Array.from({ length: 55 }, (_, index) => ({ customer_id: index + 1, status: 'active', display_name: index < 10 ? `李思远${index + 1}` : `客户${index + 1}`, oneid: `oneid-${index + 1}`, phone_masked: `138****${String(index).padStart(4, '0')}`, activation_status: 'active', updated_at: '2026-08-26T00:00:00Z' }));
        window.__customerListRequests = [];
        window.fetch = async (input) => {
          const url = new URL(String(input), window.location.origin);
          window.__customerListRequests.push(url.pathname + url.search);
          if (url.pathname !== '/api/admin/customers') return json({ code: 'unexpected_customer_request' }, 500);
          let rows = customers;
          if (url.searchParams.get('keyword')) rows = rows.slice(0, 10);
          const offset = url.searchParams.get('cursor') === 'customer-page-2' ? 50 : 0;
          return json({ items: rows.slice(offset, offset + 50), next_cursor: offset === 0 && rows.length > 50 ? 'customer-page-2' : undefined, total: rows.length, total_is_estimate: false, watermark: 'customer-test-watermark' });
        };
        return;
      }
      if (customerDetailHttp) {
        window.Response = Response;
        const json = (data, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(data), json: async () => data });
        window.__customerDetailRequests = [];
        window.fetch = async (input) => {
          const url = new URL(String(input), window.location.origin);
          window.__customerDetailRequests.push(url.pathname + url.search);
          if (url.pathname === '/api/admin/customers/7/360') return json({
            profile: { status: 'ready', data: { display_name: '李思远', last_synced_at: '2026-08-20T10:00:00Z', updated_at: '2026-08-25T09:30:00Z' } },
            recent_touchpoints: { status: 'ready', data: [{ id: 101, event_type: 'owner.assigned', occurred_at: '2026-08-21T08:00:00Z' }] },
          });
          if (url.pathname === '/api/admin/customers/7/tags') return json({ items: [{ id: 11, name: '高意向', group_sort_order: 1, sort_order: 1 }] });
          if (url.pathname === '/api/admin/customers/7/chat-activity') return json({ items: [{ chat_type: 'private', message_type: 'text', occurred_at: '2026-08-25T09:30:00Z' }] });
          if (url.pathname === '/api/v1/customers/7/survey-answers') return json({ items: [{ id: 7001, questionnaire_id: 41, total_score: 86, submitted_at: '2026-08-23T10:30:00Z', answers: [{ question_id: 5, question_type: 'single_choice', sort_order: 0, selected_options: [{ option_id: 12, option_text: '会', score: 10 }] }] }], total: 1 });
          return json({ code: 'unexpected_customer_detail_request' }, 500);
        };
        return;
      }
      if (rel !== 'sidebar/index.html') return;
      window.URL.createObjectURL = () => 'blob:sidebar-thumbnail';
      window.URL.revokeObjectURL = () => {};
      const scenario = new URL(window.location.href).searchParams.get('sidebar_case') || 'success';
      let readyCallback;
      let errorCallback;
      let regularConfigured = false;
      let agentConfigCalls = 0;
      let agentAPIs = new Set();
      let externalContactCalls = 0;
      if (scenario !== 'sdk_missing') {
        window.wx = {
          config(options) {
            window.__sidebarTest.wxStages.push({ stage: 'config', options });
            window.setTimeout(() => {
              if (scenario === 'regular_error') {
                window.__sidebarTest.wxStages.push({ stage: 'config_error' });
                errorCallback?.({ errMsg: 'preVerifyJSAPI:fail' });
                return;
              }
              if (scenario === 'regular_timeout') {
                window.__sidebarTest.wxStages.push({ stage: 'config_timeout' });
                return;
              }
              regularConfigured = true;
              window.__sidebarTest.wxStages.push({ stage: 'ready_callback' });
              readyCallback?.();
            }, 0);
          },
          ready(callback) {
            window.__sidebarTest.wxStages.push({ stage: 'ready' });
            readyCallback = callback;
            // The official dedicated SDK keeps regular readiness after a
            // successful wx.config. This exposes accidental re-config retries.
            if (regularConfigured) window.setTimeout(callback, 0);
          },
          error(callback) { window.__sidebarTest.wxStages.push({ stage: 'error' }); errorCallback = callback; },
          agentConfig(options) {
            agentConfigCalls += 1;
            agentAPIs = new Set(Array.isArray(options.jsApiList) ? options.jsApiList : []);
            window.__sidebarTest.wxStages.push({ stage: 'agentConfig', options });
            window.setTimeout(() => {
              if (scenario === 'agent_error' || scenario === 'sdk_error' || ((scenario === 'agent_retry' || scenario === 'agent_retry_storage_failure') && agentConfigCalls === 1)) {
                options.fail?.({ errMsg: 'agentConfig:fail' });
              } else options.success?.({ errMsg: 'agentConfig:ok' });
            }, 0);
          },
          invoke(method, payload, callback) {
            window.__sidebarTest.wxMessages.push({ method, payload });
            window.__sidebarTest.wxInvokes.push(method);
            if (!agentAPIs.has(method)) {
              callback({ errMsg: method + ':no permission' });
              return;
            }
            if (scenario === 'contact_error' && method === 'getCurExternalContact') {
              callback({ errMsg: 'getCurExternalContact:fail' });
              return;
            }
            if (scenario === 'late_contact_retry' && method === 'getCurExternalContact') {
              externalContactCalls += 1;
              if (externalContactCalls === 1) {
                window.__sidebarTest.releaseStaleContact = () => callback({ errMsg: 'getCurExternalContact:ok', external_userid: 'ext-stale' });
                return;
              }
              callback({ errMsg: 'getCurExternalContact:ok', external_userid: 'ext-current' });
              return;
            }
            if ((scenario === 'contact_switch' || scenario === 'contact_switch_unknown' || scenario === 'send_contact_no_focus' || scenario === 'send_accept_switch' || scenario === 'send_callback_switch_ok' || scenario === 'send_callback_switch_unknown') && method === 'getCurExternalContact') {
              externalContactCalls += 1;
              callback({ errMsg: 'getCurExternalContact:ok', external_userid: window.__sidebarTest.contactSwitched ? 'ext-8' : 'ext-7' });
              return;
            }
            if (method === 'sendChatMessage' && scenario === 'contact_switch_unknown' && !window.__sidebarTest.contactSwitched) {
              callback({});
              return;
            }
            if (method === 'sendChatMessage' && (scenario === 'send_callback_switch_ok' || scenario === 'send_callback_switch_unknown')) {
              window.__sidebarTest.releaseSend = () => callback(scenario === 'send_callback_switch_ok' ? { errMsg: 'sendChatMessage:ok' } : {});
              return;
            }
            if (method === 'sendChatMessage' && scenario === 'send_delayed') {
              window.__sidebarTest.releaseSend = () => callback({ errMsg: 'sendChatMessage:ok' });
              return;
            }
            if (method === 'sendChatMessage' && scenario === 'invoke_errmsg_fail') {
              callback({ errMsg: 'sendChatMessage:fail' });
              return;
            }
            if (method === 'sendChatMessage' && scenario === 'invoke_empty') {
              callback({});
              return;
            }
            callback({ errMsg: method + ':ok', ...(method === 'getCurExternalContact' ? { external_userid: 'ext-7' } : {}) });
          },
        };
      }
      const safety = { local_only: true, provider_execution_eligible: false, real_external_call_executed: false };
      // 本地后端真实投影形状（internal/sidebar + 各域 port）。member_ref 由
      // 前端适配器按 entitlement id 确定性编码（spm_ + 22 位补零数字）。
      const entitlementID = 5;
      const memberRef = 'spm_' + String(entitlementID).padStart(22, '0');
      const profile = {
        customer_id: 7,
        name: '侧边栏测试客户',
        display_name: '侧边栏测试客户',
        avatar_url: '',
        phone_masked: '',
        phone_assurance: '',
        profile_source: '企微',
        profile_version: 0,
        industry: '',
        industry_description: '',
        needs_blockers_followup: '',
        status: 'active',
        gender: 0,
        corp_name: '测试公司',
        source: '企微',
        version: 1,
        updated_at: '2026-08-26T01:00:00Z',
      };
      const entitlement = {
        id: entitlementID,
        customer_id: 7,
        service_product_id: 3,
        title: '周期课程',
        last_order_id: 88,
        status: 'active',
        start_at: '2026-08-01T00:00:00Z',
        end_at: '2026-09-01T00:00:00Z',
        remark: '首期备注',
        alliance: '测试联盟',
        version: 1,
        updated_at: '2026-08-01T00:00:00Z',
      };
      const json = (data, status = 200) => ({
        ok: status >= 200 && status < 300,
        status,
        headers: new Headers({ 'Content-Type': 'application/json' }),
        text: async () => JSON.stringify(data),
        json: async () => data,
        blob: async () => new window.Blob([JSON.stringify(data)], { type: 'application/json' }),
        clone() { return this; },
      });
      window.__sidebarTest = { contactSwitched: false, remarkBody: null, idempotencyKey: null, phoneBody: null, phoneKey: null, phoneKeys: [], phoneAttempts: 0, profileBodies: [], profileKeys: [], materialQueries: [], sendIntentBodies: [], sendIntentKeys: [], sendIntentTokens: [], sendOutcomeBodies: [], sendOutcomeTokens: [], wxMessages: [], wxInvokes: [], wxStages: [], requests: [], bootstrapBodies: [], releaseStaleContact: null, releaseSend: null, oauthTargets: [], jssdkStorageReads: 0 };
      window.addEventListener('aicrm-sidebar-oauth-required', (event) => window.__sidebarTest.oauthTargets.push(event.detail?.target || ''));
      if (scenario === 'agent_retry_storage_failure') {
        const values = new Map();
        Object.defineProperty(window, 'sessionStorage', {
          configurable: true,
          value: {
            getItem(key) { window.__sidebarTest.jssdkStorageReads += 1; return values.get(key) || null; },
            setItem(key, value) { values.set(key, String(value)); },
            removeItem() { throw new window.DOMException('cache removal blocked', 'SecurityError'); },
          },
        });
      }
      if (scenario === 'session_storage_unavailable') {
        Object.defineProperty(window, 'sessionStorage', {
          configurable: true,
          get() { throw new window.DOMException('storage disabled', 'SecurityError'); },
        });
      }
      if (scenario === 'sdk_cache' || scenario === 'sdk_cache_v2') {
        const pageURL = window.location.href.split('#', 1)[0];
        const legacy = scenario === 'sdk_cache_v2';
        const config = { corp_id: 'ww-test', agent_id: '1', config: { nonceStr: 'cached-config-nonce', timestamp: 1, signature: 'cached-config-signature', jsApiList: legacy ? ['getCurExternalContact'] : ['getCurExternalContact', 'sendChatMessage'] }, agent_config: { nonceStr: 'cached-agent-nonce', timestamp: 1, signature: 'cached-agent-signature', jsApiList: legacy ? ['getCurExternalContact'] : ['getCurExternalContact', 'sendChatMessage'] } };
        window.sessionStorage.setItem(`aicrm.sidebar.jssdk.config.${legacy ? 'v2' : 'v3'}`, JSON.stringify({ url: pageURL, usable_until: Date.now() + 2 * 60 * 1000, config }));
      }
      window.fetch = async (input, init = {}) => {
        const url = String(input);
        window.__sidebarTest.requests.push(url);
        if (url.includes('/jssdk-config')) {
          if (scenario === 'jssdk_401') return json({ code: 'authentication_required' }, 401);
          return json({
            corp_id: 'ww-test',
            agent_id: '1',
            config: { timestamp: 1, nonceStr: 'nonce-config', signature: 'sig-config', jsApiList: [] },
            agent_config: { timestamp: 1, nonceStr: 'nonce', signature: 'signature', jsApiList: ['getCurExternalContact', 'sendChatMessage'] },
          });
        }
        if (url.includes('/bootstrap')) {
          window.__sidebarTest.bootstrapBodies.push(JSON.parse(init.body || '{}'));
          if (scenario === 'viewer_session_required') return json({ state: 'viewer_session_required', safety }, 401);
          const bootstrapInput = window.__sidebarTest.bootstrapBodies.at(-1);
          const scopedCustomerID = bootstrapInput?.external_userid === 'ext-8' ? 8 : 7;
          return json({ state: 'ready', context_token: 'sidebar-context-token-' + scopedCustomerID + '-' + 'x'.repeat(50), customer_id: scopedCustomerID, workbench: { profile: { ...profile, customer_id: scopedCustomerID, oneid: 'CID-' + scopedCustomerID }, questionnaire_count: scenario === 'empty' ? 0 : 2, order_count: scenario === 'success' ? 1 : 0, periodic_order_count: scenario === 'success' ? 1 : 0, material_count: scenario === 'success' ? 2 : 0, safety }, safety });
        }
        if (url.includes('/phone-binding')) {
          window.__sidebarTest.phoneBody = JSON.parse(init.body || '{}');
          window.__sidebarTest.phoneKey = new Headers(init.headers).get('Idempotency-Key');
          window.__sidebarTest.phoneKeys.push(window.__sidebarTest.phoneKey);
          window.__sidebarTest.phoneAttempts += 1;
          if (scenario === 'phone_flaky' && window.__sidebarTest.phoneAttempts === 1) return json({ code: 'unavailable' }, 503);
          return json({ status: 'attached', phone_masked: '138****8000', phone_assurance: 'declared' });
        }
        if (url.includes('/api/sidebar/v2/profile')) {
          const body = JSON.parse(init.body || '{}');
          window.__sidebarTest.profileBodies.push(body);
          window.__sidebarTest.profileKeys.push(new Headers(init.headers).get('Idempotency-Key'));
          if (scenario === 'profile_conflict') return json({ code: 'conflict' }, 409);
          Object.assign(profile, {
            profile_source: body.source,
            industry: body.industry,
            industry_description: body.industry_description,
            needs_blockers_followup: body.needs_blockers_followup,
            profile_version: Number(profile.profile_version || 0) + 1,
          });
          return json({ customer: { ...profile } });
        }
        if (url.includes('/questionnaires')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          const secondPage = new URL(url, window.location.origin).searchParams.get('cursor') === 'survey-page-2';
          return json({
            customer_id: 7,
            items: scenario === 'empty' ? [] : secondPage
              ? [{ id: 12, title: '第二页问卷', submitted_at: '2026-08-25T01:00:00Z', score: 7.5, answers: [{ question: '是否继续', answers: ['继续'] }] }]
              : [{ id: 11, title: '满意度回访', submitted_at: '2026-08-26T01:00:00Z', score: 8.5, answers: [{ question: '是否满意', answers: ['满意'] }] }],
            total: scenario === 'empty' ? 0 : 2,
            limit: 20,
            has_more: !secondPage && scenario !== 'empty',
            next_cursor: secondPage || scenario === 'empty' ? '' : 'survey-page-2',
            source_status: 'ready',
            as_of: '2026-08-26T02:00:00Z',
          });
        }
        if (url.includes('/timeline')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          const secondPage = new URL(url, window.location.origin).searchParams.get('cursor') === 'timeline-page-2';
          return json({
            customer_id: 7,
            items: scenario === 'empty' ? [] : secondPage
              ? [{ id: 8, event_type: 'order_created', title: '创建订单', source_domain: 'order', occurred_at: '2026-08-25T00:00:00Z' }]
              : [{ id: 7, event_type: 'survey_submitted', title: '提交问卷', source_domain: 'survey', occurred_at: '2026-08-26T00:00:00Z' }],
            total: scenario === 'empty' ? 0 : 2,
            limit: 20,
            has_more: !secondPage && scenario !== 'empty',
            next_cursor: secondPage || scenario === 'empty' ? '' : 'timeline-page-2',
            source_status: 'ready',
            as_of: '2026-08-26T02:00:00Z',
          });
        }
        if (url.includes('/periodic-orders/') && url.includes('/remark')) {
          if (scenario === 'error') return json({ code: 'conflict' }, 409);
          window.__sidebarTest.remarkBody = JSON.parse(init.body || '{}');
          window.__sidebarTest.idempotencyKey = new Headers(init.headers).get('Idempotency-Key');
          return json({ ...entitlement, remark: window.__sidebarTest.remarkBody.remark, version: 2, updated_at: '2026-08-26T02:00:00Z' });
        }
        if (url.includes('/periodic-orders')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          return json({ items: scenario === 'empty' ? [] : [entitlement], total: scenario === 'empty' ? 0 : 1 });
        }
        if (url.includes('/orders')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          return json({
            items: scenario === 'empty' ? [] : [{ id: 9, provider: 'wechat_pay', source_system: 'aicrm-production', source_key: 'k1', merchant_order_no: 'M20260826001', amount: { amount_minor: 9900, currency: 'CNY' }, refunded_minor: scenario === 'order_refunded' ? 9900 : 0, status: scenario === 'order_refunded' ? 'refunded' : 'paid', items: [{ line_no: 1, product_code: 'course-1', product_name: '测试课程', unit_amount_minor: 9900, quantity: 1, line_amount_minor: 9900 }], record_origin: 'native', effect_eligible: false, version: 1, created_at: '2026-08-26T00:40:00Z', updated_at: '2026-08-26T00:40:00Z' }],
            next_cursor: '',
            total: scenario === 'empty' ? 0 : 1,
          });
        }
        if (url.includes('/products')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          return json({
            items: scenario === 'empty' ? [] : [
              { id: 41, code: 'course-ordinary', product_type: 'standard', name: '普通课程', price_minor: 9900, currency: 'CNY' },
              { id: 42, code: 'course-period', product_type: 'service_period', name: '周期课程', price_minor: 19900, currency: 'CNY' },
            ],
            total: 2,
            limit: 50,
            offset: 0,
          });
        }
        if (url.includes('/coupons')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          return json({
            items: scenario === 'empty' ? [] : [
              { coupon_id: 71, name: '可领取目录券', discount_minor: 990, currency: 'CNY', targets: [{ title: '普通课程', product_type: 'standard' }], claim_ends_at: '2026-09-30T00:00:00Z', url: 'http://localhost/c/coupon-71', availability_status: 'active', user_limit_reached: false },
              { coupon_id: 72, name: '已结束目录券', discount_minor: 100, currency: 'CNY', targets: [], claim_ends_at: '2026-08-01T00:00:00Z', availability_status: 'ended', user_limit_reached: true },
            ], total: scenario === 'empty' ? 0 : 2, limit: 20, offset: 0,
          });
        }
        if (url.includes('/send-intents') && url.includes('/outcome')) {
          window.__sidebarTest.sendOutcomeBodies.push(JSON.parse(init.body || '{}'));
          window.__sidebarTest.sendOutcomeTokens.push(new Headers(init.headers).get('X-Sidebar-Context-Token'));
          return json({ intent_id: 51, effect_id: 'eff-1', state: 'client_executed' });
        }
        if (url.includes('/send-intents')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          const command = JSON.parse(init.body || '{}');
          window.__sidebarTest.sendIntentBodies.push(command);
          window.__sidebarTest.sendIntentKeys.push(new Headers(init.headers).get('Idempotency-Key'));
          window.__sidebarTest.sendIntentTokens.push(new Headers(init.headers).get('X-Sidebar-Context-Token'));
          if (scenario === 'send_accept_response_lost' && window.__sidebarTest.sendIntentKeys.length === 1) throw new TypeError('accepted response lost');
          const payload = command.resource_kind === 'product'
            ? { msgtype: 'news', news: { link: 'http://localhost/p/course-ordinary', title: '普通课程', desc: '', imgUrl: 'https://assets.example.test/products/course-ordinary.png' } }
            : command.resource_kind === 'coupon'
              ? { msgtype: 'news', news: { link: 'http://localhost/c/coupon-71', title: '可领取目录券', desc: '点击领取优惠券' } }
            : { msgtype: 'image', image: { mediaid: 'media-real-31' } };
          if (scenario === 'send_accept_response_lost' && window.__sidebarTest.sendIntentKeys.length > 1) return json({ intent_id: 51, effect_id: 'eff-1', state: 'queued', payload, replayed: true }, 202);
          if (scenario === 'send_accept_switch') return await new Promise((resolve) => { window.__sidebarTest.releaseAccept = () => resolve(json({ intent_id: 51, effect_id: 'eff-1', state: 'queued', grant: 'grant-token', grant_expires_at: '2026-08-28T00:00:00Z', payload, replayed: false }, 202)); });
          return json({ intent_id: 51, effect_id: 'eff-1', state: 'queued', grant: 'grant-token', grant_expires_at: '2026-08-28T00:00:00Z', payload, replayed: false }, 202);
        }
        if (url.includes('/materials/') && url.includes('/variants/')) {
          if (url.includes('/materials/32/')) return json({ code: 'not_found' }, 404);
          return {
            ok: true,
            status: 200,
            headers: new Headers({ 'Content-Type': 'image/png', ETag: '"thumb"' }),
            text: async () => '',
            blob: async () => new window.Blob(['image-bytes'], { type: 'image/png' }),
          };
        }
        if (url.includes('/materials')) {
          if (scenario === 'error') return json({ code: 'unavailable' }, 503);
          window.__sidebarTest.materialQueries.push(url);
          return json({
            items: scenario === 'empty' ? [] : [
              { id: 31, name: '欢迎海报', file_name: 'welcome.png', mime_type: 'image/png', file_size: 1024, description: '测试素材', tags: ['欢迎语'], category: '海报', width: 800, height: 600, updated_at: '2026-08-26T00:50:00Z' },
              { id: 32, name: '课程卡片', file_name: 'course.png', mime_type: 'image/png', file_size: 2048, description: '', tags: ['课程'], category: '课程', width: 600, height: 400, updated_at: '2026-08-26T00:51:00Z' },
            ],
            total: scenario === 'empty' ? 0 : 2,
            limit: 20,
            offset: 0,
          });
        }
        return json({ code: 'unexpected_sidebar_request' }, 500);
      };
    },
  });
  // The Host dynamically appends the generated dd8 overlay in production.
  // jsdom does not fetch that appended external script, so execute the same
  // hashed release asset here after the Host has installed its bridge.
  if (rel === 'sidebar/index.html') {
    const manifest = JSON.parse(fs.readFileSync(path.join(DIST, 'asset-manifest.json'), 'utf8'));
    const overlay = manifest.entries?.sidebarStandardOverlay;
    if (typeof overlay !== 'string') throw new Error('sidebar standard overlay is absent from the release manifest');
    // jsdom does not execute module tags. Let parsing finish first: otherwise
    // the test Host registers a DOMContentLoaded callback while the overlay
    // starts immediately and sees no bridge. Then install the hashed Host and
    // execute the exact generated overlay.
    await sleep(0);
    if (!dom.window.__AICRMSidebarBridge) dom.window.eval(TEST_BUNDLES.sidebar);
    dom.window.eval(fs.readFileSync(path.join(DIST, overlay), 'utf8'));
  }
  // Wait for Host identity/bootstrap plus the standard overlay's first paint.
  await sleep(rel === 'sidebar/index.html' ? 120 : 700);
  return dom;
}

const click = (dom, el) => el.dispatchEvent(new dom.window.MouseEvent('click', { bubbles: true }));
const input = (dom, el, v) => {
  el.value = v;
  el.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
};

console.log('admin/mpLib.html（停用历史素材分页）');
{
  const dom = await loadPage('admin/mpLib.html', { miniProgramHttp: true });
  const d = dom.window.document;
  const test = dom.window.__miniProgramHttpTest;
  ok('素材库管理页仅读取首个有限分页并请求停用历史素材', test.calls.length === 1 && test.calls[0].path === '/api/admin/miniprogram-library' && test.calls[0].query.includes('limit=50') && test.calls[0].query.includes('offset=0') && test.calls[0].query.includes('enabled_only=false'));
  ok('停用素材明确展示为停用，页面说明仅包含历史素材而不自动启用', d.body.textContent.includes('历史卡片-1') && d.body.textContent.includes('已停用') && d.body.textContent.includes('包含已停用的历史素材；不会自动启用或发送'));
  click(dom, d.querySelector('#mpNext'));
  await sleep(40);
  ok('下一页保持停用筛选并使用 offset=50', test.calls.at(-1).query.includes('offset=50') && test.calls.at(-1).query.includes('enabled_only=false') && d.body.textContent.includes('历史卡片-51'));
  input(dom, d.querySelector('#fMpQuery'), '已收缩');
  click(dom, d.querySelector('#mpSearch'));
  await sleep(40);
  click(dom, d.querySelector('#mpNext'));
  await sleep(40);
  ok('服务端总数收缩时空页显示 0-0 且仍可返回上一页', d.body.textContent.includes('显示 0-0 / 50') && test.calls.at(-1).query.includes('offset=50'));
  click(dom, d.querySelector('#mpPrevious'));
  await sleep(40);
  ok('总数收缩后的上一页仍用同一查询读取 offset=0', test.calls.at(-1).query.includes('offset=0') && test.calls.at(-1).query.includes('q=%E5%B7%B2%E6%94%B6%E7%BC%A9'));
  input(dom, d.querySelector('#fMpQuery'), '网络失败');
  click(dom, d.querySelector('#mpSearch'));
  await sleep(40);
  ok('查询失败清除旧页并显示当前页重试入口', !d.body.textContent.includes('历史卡片-51') && d.body.textContent.includes('重试当前页'));
  click(dom, d.querySelector('#mpRetry'));
  await sleep(40);
  ok('重试沿用失败查询的同页请求而不回退 Mock', test.calls.at(-1).query.includes('offset=0') && test.calls.at(-1).query.includes('q=%E7%BD%91%E7%BB%9C%E5%A4%B1%E8%B4%A5'));
  dom.window.close();
}

/* ================= 后台 · 内容雷达 ================= */
console.log('admin/radar.html（列表）');
{
  const dom = await loadPage('admin/radar.html');
  const d = dom.window.document;
  ok('雷达列表渲染 5 行', d.querySelectorAll('#listRows tr').length === 5);
  ok('导航高亮落在内容雷达', !!d.querySelector('.nav-item.on[href="radar.html"]'));
  // 关键词筛选
  input(dom, d.querySelector('#fKeyword'), '沙龙');
  await sleep(30);
  ok('搜索「沙龙」后剩 1 行', d.querySelectorAll('#listRows tr').length === 1);
  input(dom, d.querySelector('#fKeyword'), '');
  await sleep(30);
  // 分享浮窗
  const shareBtn = d.querySelector('[data-share]');
  click(dom, shareBtn);
  await sleep(30);
  ok('分享浮窗打开且二维码缺口明确阻塞', d.querySelector('#shareMask').classList.contains('open') && d.querySelector('#shareQr').textContent.includes('backend_blocked') && !d.querySelector('#shareQr svg') && d.querySelector('#shareQrDownload').disabled);
  ok('本地/Mock 模式不启用链接复制', d.querySelector('#shareUrl').disabled && d.querySelector('#shareCopy').disabled);
  click(dom, d.querySelector('#shareMask .modal-x'));
  await sleep(30);
  ok('关闭分享浮窗', !d.querySelector('#shareMask').classList.contains('open'));
  // 停用 → 写穿并 toast
  const toggleBtn = [...d.querySelectorAll('[data-toggle]')].find((b) => b.textContent.trim() === '停用');
  click(dom, toggleBtn);
  await sleep(500);
  ok('停用后 toast 反馈且按钮变「启用」', d.body.textContent.includes('已停用'));
  dom.window.close();
}

console.log('admin/radar.html（真实分享投影与二维码）');
{
  const dom = await loadPage('admin/radar.html', { radarHttp: true });
  const d = dom.window.document;
  const expected = 'http://localhost/r/rd_1234567890123456789012';
  click(dom, d.querySelector('[data-share]'));
  await sleep(40);
  const test = dom.window.__radarHttpTest;
  const svg = d.querySelector('#shareQr svg');
  ok('HTTP 模式读取真实 Radar 分享投影', test.calls.some((call) => call.path === '/api/admin/radar-links/2/share'));
  ok('二维码内容是当前 origin 的公开 /r/{code} 绝对 URL', d.querySelector('#shareUrl').value === expected && svg?.getAttribute('data-qr-payload') === expected && svg?.getAttribute('aria-label')?.includes(expected));
  ok('浏览器生成真实 QR SVG 而非占位图', Boolean(svg && svg.querySelector('path') && d.querySelector('#shareQr').dataset.qrPayload === expected));
  click(dom, d.querySelector('#shareQrDownload'));
  await sleep(30);
  const qrBlob = test.lastBlob;
  let downloadedSvg = '';
  if (qrBlob) {
    const reader = new dom.window.FileReader();
    downloadedSvg = await new Promise((resolve) => { reader.addEventListener('load', () => resolve(String(reader.result))); reader.readAsText(qrBlob); });
  }
  ok('二维码可下载为真实 SVG 文件', test.downloads[0]?.download === 'radar-share-qr.svg' && qrBlob?.type.includes('image/svg+xml') && downloadedSvg.startsWith('<svg'));
  dom.window.close();
}

console.log('admin/radarDetail.html?id=2（详情）');
{
  const dom = await loadPage('admin/radarDetail.html', { id: 2 });
  const d = dom.window.document;
  ok('详情页读取 ?id=2 显示对应标题', d.body.textContent.includes('共学营开营通知'));
  ok('4 张统计卡（含授权转化率 79%）', d.querySelectorAll('.stat-row .stat').length === 4 && d.body.textContent.includes('79%'));
  ok('访问明细渲染 24 行', d.querySelectorAll('#dRows tr').length === 24);
  input(dom, d.querySelector('#dKeyword'), '2f9Qn');
  await sleep(30);
  ok('明细按外部联系人 ID 过滤', d.querySelectorAll('#dRows tr').length === 3);
  click(dom, d.querySelector('#dRefresh'));
  await sleep(400);
  ok('刷新重读真实事件并保留当前时间/用户筛选', d.querySelectorAll('#dRows tr').length === 3 && d.body.textContent.includes('已按当前时间条件刷新'));
  let exportedBlob = null;
  const originalCreateObjectURL = dom.window.URL.createObjectURL;
  const originalRevokeObjectURL = dom.window.URL.revokeObjectURL;
  const originalAnchorClick = dom.window.HTMLAnchorElement.prototype.click;
  dom.window.URL.createObjectURL = (blob) => {
    exportedBlob = blob;
    return 'blob:radar-export';
  };
  dom.window.URL.revokeObjectURL = () => {};
  dom.window.HTMLAnchorElement.prototype.click = () => {};
  click(dom, d.querySelector('#dExport'));
  await sleep(30);
  const reader = new dom.window.FileReader();
  const csv = await new Promise((resolve) => {
    reader.addEventListener('load', () => resolve(String(reader.result)));
    reader.readAsText(exportedBlob);
  });
  ok('导出仅包含当前筛选后的 3 条雷达事件', csv.split('\n').filter(Boolean).length === 4 && csv.includes('2f9Qn'));
  dom.window.URL.createObjectURL = originalCreateObjectURL;
  dom.window.URL.revokeObjectURL = originalRevokeObjectURL;
  dom.window.HTMLAnchorElement.prototype.click = originalAnchorClick;
  dom.window.close();
}

console.log('admin/radarForm.html（新建校验）');
{
  const dom = await loadPage('admin/radarForm.html');
  const d = dom.window.document;
  const save = [...d.querySelectorAll('button')].find((b) => b.textContent.includes('保存内容雷达'));
  click(dom, save);
  await sleep(30);
  ok('名称为空时阻止保存并提示', d.querySelector('#fb-toast').textContent === '请输入内容名称');
  // 切换到图片类型 → 素材区出现
  click(dom, d.querySelector('.type-card[data-t="image"]'));
  await sleep(30);
  ok('切换图片类型后素材与独立目标地址同时显示', !d.querySelector('#cfgMedia').hidden && !d.querySelector('#cfgUrl').hidden);
  // 选素材（通用选择器）→ 填名称 → 保存成功跳列表前 toast
  click(dom, d.querySelector('#btnPick'));
  await sleep(450);
  ok('图片素材选择器打开', !!d.querySelector('.pk-mask'));
  ok('选择器内存在显式 Mock 素材（resourceId=1）', !!d.querySelector('.pk-mask [data-pk-id]'));
  click(dom, d.querySelector('.pk-mask [data-pk-id]'));
  await sleep(30);
  click(dom, d.querySelector('.pk-mask [data-pk="ok"]'));
  await sleep(30);
  ok('选择素材后写回表单', d.body.textContent.includes('来自素材库'));
  input(dom, d.querySelector('#fName'), 'e2e 测试图片雷达');
  input(dom, d.querySelector('#fUrl'), 'https://example.test/poster');
  click(dom, save);
  await sleep(600);
  ok('保存成功 toast', d.querySelector('#fb-toast').textContent === '已保存内容雷达');
  dom.window.close();
}

console.log('admin/radarForm.html（真实 PDF 文件分片上传）');
{
  const dom = await loadPage('admin/radarForm.html', { radarHttp: true });
  const d = dom.window.document;
  click(dom, d.querySelector('.type-card[data-t="pdf"]'));
  const fileInput = d.querySelector('#fileInput');
  const file = new dom.window.File(['%PDF-1.7\\n%%EOF'], '浏览器文件.pdf', { type: 'application/pdf' });
  Object.defineProperty(fileInput, 'files', { configurable: true, value: [file] });
  fileInput.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  await sleep(120);
  const calls = dom.window.__radarHttpTest.calls.filter((call) => call.path.includes('/attachment-library/uploads'));
  ok('真实文件控件触发 PDF initiate/part/complete', calls.length === 3 && calls[0].path.endsWith('/uploads') && calls[1].path.endsWith('/parts/1') && calls[2].path.endsWith('/complete'));
  ok('PDF 上传完成后仅使用服务端附件 ID', d.querySelector('#mediaName').textContent === '浏览器文件.pdf' && d.querySelector('#mediaMeta').textContent.includes('分片上传'));
  dom.window.close();
}

console.log('admin/radarForm.html?id=2（编辑路由）');
{
  const dom = await loadPage('admin/radarForm.html', { id: 2 });
  const d = dom.window.document;
  ok('雷达编辑路由加载现有链接', d.querySelector('.page-title')?.textContent === '编辑雷达链接' && d.querySelector('#fName')?.value === '共学营开营通知（可追踪）' && d.querySelector('#fUrl')?.value === 'https://mp.weixin.qq.com/s/gongxueying-kaiying');
  dom.window.close();
}

/* ================= 后台 · AI 助手 ================= */
console.log('admin/ai.html（计划列表）');
{
  const dom = await loadPage('admin/ai.html');
  const d = dom.window.document;
  ok('计划列表渲染 8 行', d.querySelectorAll('#planList .plan-row').length === 8);
  ok('统计卡：待审批 2 / 执行中 1', d.querySelector('#stPending').textContent === '2' && d.querySelector('#stActive').textContent === '1');
  input(dom, d.querySelector('#fKeyword'), '共学营');
  await sleep(30);
  ok('搜索「共学营」后剩 1 条', d.querySelectorAll('#planList .plan-row').length === 1);
  dom.window.close();
}

console.log('admin/aiDetail.html?id=7（详情 + 抽屉）');
{
  const dom = await loadPage('admin/aiDetail.html', { id: 7 });
  const d = dom.window.document;
  ok('计划信息含目标人数 1,632', d.body.textContent.includes('1,632'));
  ok('人员分页加载（50 / 180）', d.querySelector('#rcLoaded').textContent.includes('50 / 180'));
  ok('人员表渲染 50 行', d.querySelectorAll('#rcRows tr').length === 50);
  // 继续加载
  click(dom, d.querySelector('#rcMore'));
  await sleep(30);
  ok('继续加载后为 100 行', d.querySelectorAll('#rcRows tr').length === 100);
  // 打开人员抽屉
  const openBtn = [...d.querySelectorAll('#rcRows [data-rc]')].find((b) => b.tagName === 'BUTTON');
  click(dom, openBtn);
  await sleep(30);
  ok('人员抽屉打开（话术任务可见）', d.querySelector('#drawer').classList.contains('open') && d.body.textContent.includes('话术任务 1'));
  // 批准这个人
  click(dom, d.querySelector('#dwApprove'));
  await sleep(400);
  ok('批准人员后 toast + 状态级联', d.querySelector('#fb-toast').textContent === '已批准这个人发送');
  click(dom, d.querySelector('#dwClose'));
  await sleep(30);
  // 整单批准（确认浮窗 → 级联）
  click(dom, d.querySelector('#dApprove'));
  await sleep(30);
  ok('整单批准弹出确认浮窗', d.querySelector('#fb-mask').hidden === false);
  click(dom, d.querySelector('#fb-ok'));
  await sleep(400);
  ok('批准后状态变「已批准」且按钮锁定', d.querySelector('#dStatus').textContent === '已批准' && d.querySelector('#dApprove').disabled);
  dom.window.close();
}

/* ================= 后台 · 模板页回归 ================= */
console.log('admin/questionnaires.html');
{
  const dom = await loadPage('admin/questionnaires.html');
  const d = dom.window.document;
  ok('问卷列表 6 行', d.querySelectorAll('tbody tr').length === 6);
  dom.window.close();
}

console.log('admin/questionnaireDetail.html（新建/编辑路由）');
{
  const created = await loadQuestionnaireEditor();
  const createDoc = created.dom.window.document;
  ok('问卷新建路由呈现 V1 三栏可视化编辑器', createDoc.body.textContent.includes('添加题目') && !!createDoc.querySelector('.phone-frame') && createDoc.querySelector('#inspector-title').textContent === '问卷设置');
  input(created.dom, createDoc.querySelector('#field-name'), 'V2 可视化问卷');
  input(created.dom, createDoc.querySelector('#field-title'), '公开标题');
  input(created.dom, createDoc.querySelector('#field-slug'), 'v2-visual-editor');
  click(created.dom, createDoc.querySelector('#add-single'));
  input(created.dom, createDoc.querySelector('#question-title'), '你现在最需要什么？');
  input(created.dom, createDoc.querySelector('[data-option-field="option_text"]'), '增长方案');
  click(created.dom, createDoc.querySelector('#save-btn'));
  await sleep(250);
  const createCall = created.trace.find((entry) => entry.path === '/api/admin/questionnaires' && entry.method === 'POST');
  ok('可视化创建调用真实 V2 POST 且保留同源会话', createCall?.credentials === 'include');
  ok('可视化载荷沿用 V1 内容并满足 V2 0-based 顺序契约', createCall?.body?.name === 'V2 可视化问卷' && createCall?.body?.questions?.[0]?.title === '你现在最需要什么？' && createCall?.body?.questions?.[0]?.sort_order === 0 && createCall?.body?.questions?.[0]?.options?.[0]?.sort_order === 0);
  ok('创建成功切换为编辑路由并保留三栏编辑态', created.dom.window.location.search === '?id=81' && createDoc.querySelector('#topbar-title').textContent === '公开标题');
  created.dom.window.close();

  const edited = await loadQuestionnaireEditor({ q: 'id=55' });
  const editDoc = edited.dom.window.document;
  ok('问卷编辑路由读取指定 V2 问卷详情', edited.trace.some((entry) => entry.path === '/api/admin/questionnaires/55' && entry.method === 'GET') && editDoc.querySelector('#topbar-title').textContent === '激活黄小璨AI登记');
  ok('已有题目在手机预览和右侧编辑区可继续编辑', editDoc.querySelectorAll('#preview-questions .preview-question').length === 1 && editDoc.body.textContent.includes('请输入手机号'));
  edited.dom.window.close();

  const assessment = await loadQuestionnaireEditor({ q: 'mode=assessment' });
  click(assessment.dom, assessment.dom.window.document.querySelector('#save-btn'));
  await sleep(250);
  const assessmentCreate = assessment.trace.find((entry) => entry.path === '/api/admin/questionnaires' && entry.method === 'POST');
  ok('旧测评 URL 不恢复测评创建入口或提交测评定义', !assessment.dom.window.document.querySelector('[data-assessment-step], #open-assessment-settings') && !!assessment.dom.window.document.querySelector('#field-name') && assessmentCreate?.body?.assessment_enabled === false && Object.keys(assessmentCreate.body.assessment_config || {}).length === 0);
  assessment.dom.window.close();
}

console.log('admin/customers.html（独立 V1 聊天历史）');
{
  const dom = await loadPage('admin/customers.html', { q: 'message_history=1', messageHistoryHttp: true });
  const d = dom.window.document, test = dom.window.__messageHistoryTest;
  const submit = () => d.querySelector('[data-message-filter]').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  ok('聊天历史提前挂载且首屏仅请求真实20条GET', d.body.textContent.includes('V1 聊天历史（只读）') && d.querySelectorAll('[data-message-id]').length === 20 && test.calls.length === 1 && test.calls[0].query === '?limit=20&offset=0' && !d.querySelector('#fCustomerKeyword'));
  ok('历史正文 NULL 与空字符串明确区分', d.querySelector('[data-message-id="71"] [data-message-body]').textContent === '未记录（NULL）' && d.querySelector('[data-message-id="72"] [data-message-body]').textContent === '（空字符串）');
  const body = d.querySelector('[data-message-id="73"] [data-message-body]');
  ok('脱敏正文转义且保留换行，不执行 HTML', body.textContent.includes('<img src=x onerror=alert(1)>') && body.textContent.includes('\n第二行') && body.style.whiteSpace === 'pre-wrap' && !body.querySelector('img'));
  ok('civil 时刻保留原文且不造 UTC，NULL 客户不猜归因', d.querySelector('[data-message-id="71"]').textContent.includes('客户关联：未解析') && d.querySelector('[data-message-id="71"] [data-message-time]').textContent.includes('2024-01-02 03:04:05 · 未定时区') && !d.querySelector('[data-message-id="71"] [data-message-time]').textContent.includes('2024-01-02T'));
  ok('已关联客户仅标 DM01 历史映射，单条链接用真实历史 ID', d.querySelector('[data-message-id="72"]').textContent.includes('DM01 历史映射 · customer_id=9') && d.querySelector('[data-message-id="72"] a').getAttribute('href') === 'customers.html?message_history=1&history_message_id=72');
  test.fail = true; click(dom, d.querySelector('[data-message-next]')); await sleep(30);
  ok('翻页失败清旧数据且无 Mock 回退', !d.querySelector('[data-message-id]') && d.querySelector('[role="alert"]').textContent.includes('HTTP 503'));
  test.fail = false; click(dom, d.querySelector('[data-message-retry]')); await sleep(30);
  ok('失败重试保留 offset20，只显示真实最后一条', test.calls.at(-1).query === '?limit=20&offset=20' && d.querySelectorAll('[data-message-id]').length === 1 && !!d.querySelector('[data-message-id="91"]'));
  click(dom, d.querySelector('[data-message-prev]')); await sleep(30);
  d.querySelector('[data-message-customer]').value = '9'; d.querySelector('[data-message-chat]').value = 'group'; submit(); await sleep(30);
  ok('客户与群聊筛选仅发送 canonical customer_id，分页归零', test.calls.at(-1).query === '?customer_id=9&chat_type=group&limit=20&offset=0' && d.querySelectorAll('[data-message-id]').length === 10);
  const count = test.calls.length;
  d.querySelector('[data-message-customer]').value = 'unionid-not-a-customer'; submit(); await sleep(20);
  ok('外部身份不能充当 customer_id，非法筛选无请求', test.calls.length === count && !d.querySelector('[data-message-id]') && d.querySelector('[role="alert"]').textContent.includes('canonical'));
  d.querySelector('[data-message-customer]').value = ''; d.querySelector('[data-message-chat]').value = 'private'; submit(); await sleep(30);
  ok('私聊筛选保留未解析客户记录，不强行归因', test.calls.at(-1).query === '?chat_type=private&limit=20&offset=0' && !!d.querySelector('[data-message-id="71"]'));
  test.empty = true; submit(); await sleep(30);
  ok('历史合法空页明确为空且不能翻页', d.body.textContent.includes('暂无符合筛选的聊天历史记录') && d.querySelector('[data-message-next]').disabled && d.querySelector('[data-message-prev]').disabled);
  test.empty = false; test.unsafe = true; submit(); await sleep(30);
  ok('契约外身份字段被拒绝，不渲染身份或旧正文', !d.querySelector('[data-message-id]') && !d.body.textContent.includes('forbidden-identity') && !!d.querySelector('[role="alert"]'));
  ok('历史模式只有历史GET，没有当前客户/同步/发送', test.calls.every((call) => call.path === '/api/admin/message-history' && call.method === 'GET' && call.credentials === 'include' && call.body === undefined) && ![...d.querySelectorAll('button')].some((button) => /发送|同步/.test(button.textContent)));
  dom.window.close();
}
{
  const dom = await loadPage('admin/customers.html', { q: 'message_history=1&history_message_id=72&customer_id=9', messageHistoryHttp: true });
  const d = dom.window.document, test = dom.window.__messageHistoryTest;
  ok('单条历史仅GET所选V2历史ID，不加载当前客户或列表', test.calls.length === 1 && test.calls[0].path === '/api/admin/message-history/72' && !d.querySelector('[data-message-filter]') && d.querySelectorAll('[data-message-id]').length === 1);
  ok('明确时区时刻按真实响应原样显示', d.querySelector('[data-message-time]').textContent.includes('2024-01-02T03:04:05+08:00') && d.querySelector('[data-message-time]').textContent.includes('2024-01-01T19:04:05Z'));
  ok('单条返回历史链接保留客户筛选', !!d.querySelector('a[href="customers.html?message_history=1&customer_id=9"]'));
  dom.window.close();
}
for (const query of ['message_history=1&history_message_id=bad', 'message_history=1&customer_id=unionid-test']) {
  const dom = await loadPage('admin/customers.html', { q: query, messageHistoryHttp: true });
  ok('无效历史入口保持只读壳且不读取任何数据', dom.window.document.querySelector('[data-message-history]') && dom.window.__messageHistoryTest.calls.length === 0 && !!dom.window.document.querySelector('[role="alert"]'));
  dom.window.close();
}
{
  const dom = await loadPage('admin/customers.html', { q: 'message_history=1&history_message_id=72&customer_id=8', messageHistoryHttp: true });
  ok('单条历史与显式客户筛选不符时失败关闭', !dom.window.document.querySelector('[data-message-id]') && !!dom.window.document.querySelector('[role="alert"]') && dom.window.__messageHistoryTest.calls.length === 1);
  dom.window.close();
}

console.log('admin/customers.html（筛选、opaque cursor 翻页与详情导航）');
{
  const dom = await loadPage('admin/customers.html', { customerListHttp: true });
  const d = dom.window.document;
  ok('客户首屏按服务端页大小渲染 50 行', d.querySelectorAll('tbody tr').length === 50);
  ok('客户列表使用真实总数 55', d.body.textContent.includes('共 55 位客户') && d.body.textContent.includes('第 1 – 50 条，共 55 条'));
  ok('客户行详情链接使用 canonical numeric OneID', d.querySelector('a[href="customerDetail.html?id=1"]')?.__dcBound === true);

  input(dom, d.querySelector('#fCustomerKeyword'), '李思远');
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查询'));
  await sleep(300);
  ok('关键词查询只保留匹配客户并重置到第 1 页', d.querySelectorAll('tbody tr').length === 10 && d.body.textContent.includes('共 10 位客户') && d.body.textContent.includes('第 1 页'));

  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '清空'));
  await sleep(300);
  input(dom, d.querySelector('#fCustomerOwner'), '101');
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查询'));
  await sleep(300);
  ok('负责人筛选透传 Customer-owned local owner 谓词', dom.window.__customerListRequests.at(-1)?.includes('owner_staff_id=101') && !d.querySelector('[data-customer-error]')?.textContent.trim());

  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '清空'));
  await sleep(300);
  input(dom, d.querySelector('#fCustomerTag'), '2');
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查询'));
  await sleep(300);
  ok('标签筛选透传当前本地标签谓词', dom.window.__customerListRequests.at(-1)?.includes('tag_id=2') && !d.querySelector('[data-customer-error]')?.textContent.trim());

  input(dom, d.querySelector('#fCustomerMobile'), '138000000000');
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查询'));
  await sleep(30);
  ok('非法手机号在请求前显示 11 位大陆号码错误', d.querySelector('[data-customer-error]')?.textContent.includes('11位'));

  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '清空'));
  await sleep(300);
  const next = [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '下一页');
  click(dom, next);
  await sleep(300);
  ok('下一页使用服务端 opaque cursor 并显示第 2 页 5 行', d.querySelectorAll('tbody tr').length === 5 && d.body.textContent.includes('第 2 页') && d.querySelector('a[href="customerDetail.html?id=51"]')?.__dcBound === true);
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '上一页'));
  await sleep(300);
  ok('上一页沿 cursor 栈返回第 1 页', d.querySelectorAll('tbody tr').length === 50 && d.body.textContent.includes('第 1 页'));
  dom.window.close();
}

console.log('admin/orders.html（筛选后微信支付 CSV 导出）');
{
  const dom = await loadPage('admin/orders.html');
  const d = dom.window.document;
  input(dom, d.querySelector('#orderProductCode'), '增长陪跑');
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '查询'));
  await sleep(30);
  ok('订单查询只筛选当前已加载数据', d.querySelectorAll('tbody tr').length === 3 && d.body.textContent.includes('当前筛选 3 / 5 条'));
  input(dom, d.querySelector('#orderProductCode'), '不存在的商品');
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '查询'));
  await sleep(30);
  ok('订单筛选无匹配时显示真实空态', d.body.textContent.includes('当前筛选暂无订单'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '清空'));
  await sleep(30);
  ok('清空订单筛选恢复已加载数据', d.querySelectorAll('tbody tr').length === 5 && d.querySelector('#orderProductCode')?.value === '');
  input(dom, d.querySelector('#orderTransactionId'), 'wx-42');
  input(dom, d.querySelector('#orderMobile'), '13800000000');
  input(dom, d.querySelector('#orderProductCode'), 'sku-1');
  input(dom, d.querySelector('#orderCreatedFrom'), '2026-08-01');
  input(dom, d.querySelector('#orderCreatedTo'), '2026-08-31');
  d.querySelector('#orderStatus').value = 'paid';
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.includes('导出微信支付 CSV')));
  await sleep(350);
  ok('身份筛选时阻止无筛选导出并说明原因', !dom.window.__orderDownload && d.querySelector('#order-v3-query-error')?.textContent.includes('暂不支持导出'));
  input(dom, d.querySelector('#orderMobile'), '');
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.includes('导出微信支付 CSV')));
  await sleep(350);
  ok('微信支付交易导出生成 CSV 下载', dom.window.__orderDownload?.href === 'blob:wechat-order-export' && dom.window.__orderDownload?.download === 'wechat-pay-orders.csv');
  dom.window.close();
}

console.log('admin/orderDetail.html（V1 历史只读退款）');
{
  const dom = await loadPage('admin/orderDetail.html', { id: 'V1-H-12', orderHistoryHttp: true });
  const d = dom.window.document;
  await sleep(80);
  ok('V1 历史订单只走真实 HTTP 详情读取且不回退 Mock',
    dom.window.__AICRM_TEST_MOCK__ === false &&
    dom.window.__orderHistoryHttpTest.calls.some((call) => call.path === '/api/admin/orders/V1-H-12' && call.method === 'GET'));
  const historyDetail = d.querySelector('#stage')?.textContent || '';
  ok('历史详情显示查询边界且不暴露实现代际', historyDetail.includes('历史订单，仅供查询') && historyDetail.includes('不支持退款确认') && !historyDetail.includes('V1历史') && !historyDetail.includes('V2支付'));
  ok('历史详情只显示本订单的退款状态、金额和原因',
    dom.window.__orderHistoryHttpTest.calls.some((call) => call.path === '/api/admin/refunds' && new URLSearchParams(call.query).get('order_no') === 'V1-H-12') &&
    historyDetail.includes('历史记录：已关闭 · ¥19.90') && historyDetail.includes('历史退款') && !historyDetail.includes('金额待确认'));
  ok('V1 历史详情不渲染退款 intent 按钮或表单', ![...d.querySelectorAll('button')].some((button) => button.textContent.includes('创建退款 intent')) && !d.querySelector('#refundAmount'));
  dom.window.close();
}

console.log('admin/customerDetail.html（安全 Customer360）');
{
  const dom = await loadPage('admin/customerDetail.html', { id: 1 });
  const d = dom.window.document;
  ok('Customer360 渲染原壳用户档案', d.body.textContent.includes('李思远') && d.body.textContent.includes('用户档案') && d.body.textContent.includes('渠道 ID'));
  ok('Customer360 渲染标签与时间线摘要', d.querySelectorAll('[data-customer-not-found]').length === 0 && d.querySelectorAll('tbody tr').length === 2 && d.body.textContent.includes('owner.assigned'));
  ok('Customer360 聊天只展示零正文摘要', d.querySelectorAll('[data-customer-not-found]').length === 0 && d.body.textContent.includes('消息类型：text') && d.body.textContent.includes('消息类型：image') && d.body.textContent.includes('仅展示类型和时间，不展示正文'));
  const rendered = d.querySelector('#stage')?.textContent || '';
  ok('Customer360 不展示手机号与外部身份', !rendered.includes('手机号') && !rendered.includes('external_userid') && !rendered.includes('unionid') && !rendered.includes('这个周期服务能开发票吗？'));
  ok('Customer360 渲染安全问卷 ID 投影', d.querySelector('[data-customer-survey]')?.textContent.includes('提交 7001') && d.body.textContent.includes('题目 5') && d.body.textContent.includes('选项 12'));
  ok('Customer360 明确隐藏自由文本与评测', d.querySelector('[data-customer-answer-policy]')?.textContent.includes('不展示自由文本') && d.body.textContent.includes('当前 V2 契约不可用'));
  dom.window.close();
}

console.log('admin/customerDetail.html（V3 Customer Host 安全投影）');
{
  const dom = await loadPage('admin/customerDetail.html', { id: 7, customerDetailHttp: true });
  const d = dom.window.document;
  await sleep(80);
  const requests = dom.window.__customerDetailRequests;
  ok('Customer Host 将冻结 V1 context/survey 读取映射到现有 Customer 投影',
    requests.includes('/api/admin/customers/7/360') &&
    requests.includes('/api/admin/customers/7/tags') &&
    requests.includes('/api/admin/customers/7/chat-activity?limit=20') &&
    requests.includes('/api/v1/customers/7/survey-answers'));
  ok('Customer Host 保留安全 360 页面与选择题 ID，不混入自由文本或身份',
    d.querySelector('#stage')?.textContent.includes('李思远') &&
    d.querySelector('[data-customer-survey]')?.textContent.includes('提交 7001') &&
    d.querySelector('#stage')?.textContent.includes('选项 12') &&
    !d.querySelector('#stage')?.textContent.includes('option_text'));
  dom.window.close();
}

console.log('admin/customerDetail.html?id=999（404 占位态）');
{
  const dom = await loadPage('admin/customerDetail.html', { id: 999 });
  const d = dom.window.document;
  const back = d.querySelector('[data-customer-not-found] button');
  ok('用户不存在显示明确占位', d.querySelector('[data-customer-not-found]')?.textContent.includes('用户档案不存在'));
  ok('用户不存在提供返回用户列表', back?.textContent.trim() === '返回用户列表' && back?.__dcBound === true && !d.querySelector('#fCustomerName'));
  dom.window.close();
}

/* ================= 本轮新增：二级页 + 通用选择器 ================= */
let audienceDetailPackageId = 1;
console.log('admin/automation.html（新增分组弹窗 → 测试 Mock 创建）');
{
  const dom = await loadPage('admin/automation.html');
  const d = dom.window.document;
  const audienceDetail = d.querySelector('a[href^="audienceEdit.html?id="]');
  const audienceDetailId = Number(audienceDetail && new URL(audienceDetail.href).searchParams.get('id'));
  if (Number.isSafeInteger(audienceDetailId) && audienceDetailId > 0) audienceDetailPackageId = audienceDetailId;
  ok('人群包详情链接保留实际 package_id', audienceDetail?.__dcBound === true && Number.isSafeInteger(audienceDetailId) && audienceDetailId > 0);
  const addBtn = [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '新增');
  click(dom, addBtn);
  await sleep(30);
  ok('新增分组弹窗打开', !!d.querySelector('#fGroupName'));
  input(dom, d.querySelector('#fGroupName'), 'e2e 测试分组');
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '确认'));
  await sleep(500);
  ok('创建后分组出现在分组列表', d.body.textContent.includes('e2e 测试分组'));
  dom.window.close();
}

console.log('admin/audienceEdit.html?id={package_id}（真实配置与发送人 DTO）');
{
  const dom = await loadPage('admin/audienceEdit.html', { id: audienceDetailPackageId });
  const d = dom.window.document;
  const nav4 = [...d.querySelectorAll('button')].find((b) => b.textContent.includes('成员列表'));
  click(dom, nav4);
  await sleep(30);
  const h3 = [...d.querySelectorAll('h3')].find((h) => h.textContent === '成员列表');
  let panel = h3 && h3.parentElement;
  while (panel && panel.style.display !== 'block' && panel.style.display !== 'none') panel = panel.parentElement;
  ok('切到成员列表面板', !!panel && panel.style.display === 'block');
  const nav3 = [...d.querySelectorAll('button')].find((b) => b.textContent.includes('发送人白名单'));
  click(dom, nav3);
  await sleep(30);
  ok('发送人使用 sender_userid 明文 DTO', !!d.querySelector('#aeSenders') && d.body.textContent.includes('最多 5 位'));
  ok('Mock 模式不伪造 Audience 模板目录', !d.querySelector('[data-template-contract="backend_blocked"]') && !d.querySelector('[data-template-contract="v2-local"]'));
  d.querySelector('#aeSenders').value = 'a\nb\nc\nd\ne\nf';
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '保存发送人白名单'));
  await sleep(30);
  ok('超过 5 位在发请求前被阻止', d.body.textContent.includes('发送人最多 5 位且不能重复'));
  dom.window.close();
}

console.log('admin/audienceEdit.html（HTTP 保存→配置快照→预览）');
{
  const dom = await loadPage('admin/audienceEdit.html', { id: 6, audienceHttp: true });
  const d = dom.window.document;
  await sleep(40);
  input(dom, d.querySelector('#aeName'), '已保存的待唤醒客户');
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存新版本并预览'));
  await sleep(80);
  const calls = dom.window.__audienceHttpTest.calls;
  const patch = calls.findIndex((call) => call.path === '/api/admin/ai-audience/packages/6' && call.method === 'PATCH');
  const snapshot = calls.findIndex((call) => call.path.endsWith('/configuration') && call.method === 'PUT');
  const preview = calls.findIndex((call) => call.path.endsWith('/configuration-preview') && call.query === '?configuration_version=3');
  ok('Audience 保存并预览只串行调用真实 package/configuration/preview 契约',
    dom.window.__AICRM_TEST_MOCK__ === false && patch >= 0 && snapshot > patch && preview > snapshot &&
    calls[patch]?.body?.expected_version === 3 && calls[snapshot]?.body?.expected_package_version === 4 &&
    d.querySelector('[data-audience-preview="ready"]')?.textContent.includes('配置 v3 预览：2 人'));
  ok('Audience 预览结果明确不创建群发或调用 Provider',
    d.body.textContent.includes('不创建群发') && d.body.textContent.includes('不调用企微') && !calls.some((call) => call.path.includes('materialize')));
  dom.window.close();
}

console.log('admin/audienceEdit.html（HTTP V2 模板目录/预览/保存）');
{
  const dom = await loadPage('admin/audienceEdit.html', { id: 6, audienceHttp: true });
  const d = dom.window.document;
  await sleep(40);
  const templateKey = d.querySelector('#aeTemplateKey');
  const templateParams = d.querySelector('#aeTemplateParams');
  ok('Audience 编辑页读取真实 V2 固定模板目录',
    dom.window.__AICRM_TEST_MOCK__ === false &&
    templateKey?.options.length === 5 &&
    d.querySelector('[data-template-contract="v2-local"]')?.textContent.includes('只编译为当前 V2 SegmentDefinition'));
  if (templateParams) templateParams.value = '{}';
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '预览模板'));
  await sleep(60);
  const calls = dom.window.__audienceHttpTest.calls;
  const preview = calls.find((call) => call.path.endsWith('/template-preview'));
  ok('Audience 模板预览调用真实契约且只返回本地成员摘要',
    preview?.method === 'POST' && preview.body?.template_key === 'active_contacts' &&
    d.querySelector('[data-template-preview="仅预览"]')?.textContent.includes('2 人') &&
    !calls.some((call) => call.path.includes('materialize') || call.path.includes('broadcast')));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存模板配置'));
  await sleep(60);
  const save = calls.find((call) => call.path.endsWith('/template-config'));
  ok('Audience 模板保存调用真实配置契约并展示保存结果',
    save?.method === 'PUT' && save.body?.expected_package_version === 3 && save.body?.expected_configuration_version === 2 &&
    d.querySelector('[data-template-preview="已保存"]')?.textContent.includes('包 v4'));
  const mismatchedKey = d.querySelector('#aeTemplateKey');
  const mismatchedParams = d.querySelector('#aeTemplateParams');
  if (mismatchedKey) mismatchedKey.value = 'stage_any';
  if (mismatchedParams) mismatchedParams.value = '{"stage_ids":[1],"tag_ids":[1]}';
  const previewCount = calls.filter((call) => call.path.endsWith('/template-preview')).length;
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '预览模板'));
  await sleep(40);
  ok('模板与参数错配在浏览器端拒绝且不发送请求',
    calls.filter((call) => call.path.endsWith('/template-preview')).length === previewCount &&
    d.querySelector('#fb-toast')?.textContent.includes('参数与模板不匹配'));
  dom.window.close();
}

console.log('admin/audienceEdit.html（HTTP 空人群显式确认）');
{
  const dom = await loadPage('admin/audienceEdit.html', { id: 6, audienceHttp: true, audienceEmpty: true });
  const d = dom.window.document;
  await sleep(40);
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存新版本并预览'));
  await sleep(80);
  ok('空人群预览要求明确确认且物化尚未执行',
    d.querySelector('[data-audience-preview="empty_pending"]')?.textContent.includes('物化已拒绝') &&
    d.querySelector('#fb-body')?.textContent.includes('预览结果为 0 人') &&
    !dom.window.__audienceHttpTest.calls.some((call) => call.path.includes('materialize')));
  click(dom, d.querySelector('#fb-ok'));
  await sleep(30);
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '物化成员'));
  await sleep(10);
  ok('已确认的空人群仍需第二次确认本地物化，未伪造外部效果',
    d.querySelector('[data-audience-preview="empty_confirmed"]')?.textContent.includes('仍需单独确认物化') &&
    d.querySelector('#fb-body')?.textContent.includes('当前已确认空人群') &&
    !dom.window.__audienceHttpTest.calls.some((call) => call.path.includes('materialize')));
  dom.window.close();
}

console.log('admin/audienceEdit.html（active 人群包拒绝配置保存与预览）');
{
  const dom = await loadPage('admin/audienceEdit.html', { id: 6, audienceHttp: true, audienceActive: true });
  const d = dom.window.document;
  await sleep(40);
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存新版本并预览'));
  await sleep(30);
  ok('active 人群包在详情页发请求前被拒绝，且不触发外发或启用',
    d.body.textContent.includes('请先停止后再保存或预览本地配置') &&
    !dom.window.__audienceHttpTest.calls.some((call) => call.method !== 'GET' || call.path.includes('activate') || call.path.includes('materialize')));
  dom.window.close();
}

console.log('admin/automation.html?history=1（真实 Audience 历史只读入口）');
{
  const dom = await loadPage('admin/automation.html', { q: 'history=1', audienceHistoryHttp: true });
  const doc = dom.window.document;
  const state = dom.window.__audienceHistoryHttpTest;
  const stage = doc.querySelector('#stage');
  ok('历史入口直接读取六个历史列表，不加载当前人群编辑器', !!stage.querySelector('[data-audience-history]') && stage.querySelectorAll('[data-history-kind]').length === 6 && state.calls.length === 6 && state.calls.every((c) => c.path.startsWith('/api/admin/audience-history/') && c.method === 'GET' && c.body === undefined));
  ok('历史包链接使用实际 V2 ID，不使用源 ID', stage.querySelector('[data-history-kind="packages"] a')?.getAttribute('href') === 'automation.html?history=1&history_package_id=42');
  ok('历史页面无保存、预览、物化或启动按钮', !stage.querySelector('input,textarea') && !Array.from(stage.querySelectorAll('button')).some((b) => /保存|预览|物化|刷新|activate|启用|群发/.test(b.textContent)) && !stage.querySelector('a[href*="audienceEdit"]'));
  state.fail = true;
  stage.querySelector('[data-history-kind="packages"] [data-next]').click();
  await sleep(20);
  const pkg = stage.querySelector('[data-history-kind="packages"]');
  ok('下一页失败清掉旧包数据，保留失败页 offset', !!pkg.querySelector('[role="alert"]') && !pkg.querySelector('[data-history-rows]') && pkg.textContent.includes('offset=20') && !pkg.textContent.includes('RAW_ERROR_MUST_NOT_RENDER'));
  state.fail = false;
  pkg.querySelector('[data-retry]').click();
  await sleep(20);
  const packageCalls = state.calls.filter((c) => c.path.endsWith('/packages'));
  ok('重试仍读取同一历史页，其他列表分页独立', packageCalls.map((c) => c.query).join('|') === '?limit=20&offset=0|?limit=20&offset=20|?limit=20&offset=20' && pkg.textContent.includes('历史包21') && !pkg.textContent.includes('历史包1') && state.calls.filter((c) => c.path.endsWith('/groups')).length === 1);
  dom.window.close();

  const detail = await loadPage('admin/automation.html', { q: 'history=1&history_package_id=42', audienceHistoryHttp: true });
  const details = detail.window.document.querySelector('#stage');
  const detailState = detail.window.__audienceHistoryHttpTest;
  ok('包详情使用真实详情与三类父绑定 GET', detailState.calls.length === 4 && detailState.calls.some((c) => c.path === '/api/admin/audience-history/packages/42') && ['versions', 'senders', 'members'].every((kind) => detailState.calls.some((c) => c.path === '/api/admin/audience-history/packages/42/' + kind && c.query === '?limit=20&offset=0')));
  ok('源状态、0、负数、NULL 和配置 flags 仅按历史事实展示', details.textContent.includes(' active ') && details.textContent.includes('-7') && details.textContent.includes('-3') && details.textContent.includes('NULL（历史关联未确认）') && details.textContent.includes('true') && details.textContent.includes('false') && details.textContent.includes('2026-08-28T01:02:03.123456Z'));
  ok('客户与员工仅标为 DM01 历史映射，NULL 明确未解析', details.textContent.includes('123（DM01 历史映射）') && details.textContent.includes('234（DM01 历史映射）') && details.textContent.includes('NULL（未解析）'));
  ok('不渲染原SQL、代码或外部身份，不开放当前写操作', !/RAW_SQL_MUST_NOT_RENDER|RAW_EXECUTABLE_MUST_NOT_RENDER|RAW_UNION_MUST_NOT_RENDER/.test(details.textContent) && !details.querySelector('input,textarea') && detailState.calls.every((c) => c.method === 'GET'));
  details.querySelector('[data-history-kind="members"] [data-next]').click();
  await sleep(20);
  ok('历史成员分页绑定原 V2 包 ID，版本与发送人保持本页', detailState.calls.filter((c) => c.path.endsWith('/members')).map((c) => c.query).join('|') === '?limit=20&offset=0|?limit=20&offset=20' && detailState.calls.filter((c) => c.path.endsWith('/versions')).length === 1 && detailState.calls.filter((c) => c.path.endsWith('/senders')).length === 1);
  detail.window.close();

  for (const [q, expected] of [
    ['history=1&history_rule_id=8', '/api/admin/audience-history/rules/8/versions'],
    ['history=1&history_definition_id=9', '/api/admin/audience-history/definitions/9'],
  ]) {
    const page = await loadPage('admin/automation.html', { q, audienceHistoryHttp: true });
    ok('独立历史入口只读取对应真实接口：' + expected, page.window.__audienceHistoryHttpTest.calls.length === 1 && page.window.__audienceHistoryHttpTest.calls[0].path === expected && !page.window.document.querySelector('#stage [role="alert"]'));
    page.window.close();
  }

  const empty = await loadPage('admin/automation.html', { q: 'history=1', audienceHistoryHttp: { empty: true } });
  ok('空历史保持真实空态，无Seed补位', empty.window.document.querySelectorAll('[data-history-rows]').length === 4 && Array.from(empty.window.document.querySelectorAll('[data-history-rows]')).every((node) => node.textContent === '暂无历史记录') && Array.from(empty.window.document.querySelectorAll('[data-next]')).every((button) => button.disabled));
  empty.window.close();
  const invalid = await loadPage('admin/automation.html', { q: 'history=1&history_package_id=0', audienceHistoryHttp: true });
  ok('非法历史 ID 失败关闭，零请求且不进入当前模式', invalid.window.__audienceHistoryHttpTest.calls.length === 0 && !!invalid.window.document.querySelector('[data-audience-history] [role="alert"]') && !invalid.window.document.querySelector('a[href*="audienceEdit"]'));
  invalid.window.close();
}

console.log('admin/cyclesDetail.html?id=1（8 章运行档案）');
{
  const dom = await loadPage('admin/cyclesDetail.html', { id: 1 });
  const d = dom.window.document;
  ok(
    '八章档案渲染（含分窗口结果 / 结果复盘 / 证据索引）',
    d.body.textContent.includes('分窗口结果') && d.body.textContent.includes('结果复盘与限制') && d.body.textContent.includes('证据索引'),
  );
  dom.window.close();
}

console.log('admin/wecom-tags.html（新建标签测试 Mock 建行）');
{
  const dom = await loadPage('admin/wecom-tags.html');
  const d = dom.window.document;
  const groupCards = [...d.querySelectorAll('[data-tag-group-card]')];
  ok('标签组以可选卡片展示并回显组内数量', groupCards.length === 5 && groupCards.every((card) => /\d+/.test(card.textContent)) && groupCards.some((card) => card.getAttribute('aria-pressed') === 'true'));
  ok('V1 标准组操作入口完整', ['编辑组名', '删除组'].every((label) => [...d.querySelectorAll('button')].some((button) => button.textContent.trim() === label)));
  ok('V1 标准标签操作入口完整', ['详情', '复制 tag_id', '编辑', '删除'].every((label) => [...d.querySelectorAll('tbody button')].some((button) => button.textContent.trim() === label)));
  ok('每组按 20 条分页并展示页码摘要', /\u7b2c\s*1\s*\/\s*1\s*\u9875/.test(d.body.textContent) && d.body.textContent.includes('每页 20 个'));
  const tagSearch = d.querySelector('input[placeholder="搜索标签组 / 标签 / tag_id"]');
  input(dom, tagSearch, '行业');
  await sleep(30);
  ok('搜索标签组后当前组与组操作保持一致', d.querySelectorAll('[data-tag-group-card]').length === 1 && d.querySelector('h2')?.textContent !== '行业' && [...d.querySelectorAll('h2')].some((heading) => heading.textContent === '行业'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '编辑组名'));
  await sleep(30);
  ok('搜索切换后编辑的是当前可见组', d.querySelector('#fTagGroupName')?.value === '行业');
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '取消'));
  input(dom, d.querySelector('input[placeholder="搜索标签组 / 标签 / tag_id"]'), '');
  await sleep(30);
  click(dom, [...d.querySelectorAll('tbody button')].find((button) => button.textContent.trim() === '详情'));
  await sleep(30);
  ok('标签详情展示 tag_id、所属组与使用人数', d.body.textContent.includes('tag_id') && d.body.textContent.includes('所属标签组') && d.body.textContent.includes('使用人数'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '关闭'));
  await sleep(30);
  const before = d.querySelectorAll('tbody tr').length;
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '新增标签'));
  await sleep(30);
  ok('新建标签编辑组件打开', !!d.querySelector('#fTagName'));
  input(dom, d.querySelector('#fTagName'), 'e2e标签X');
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '创建'));
  await sleep(500);
  ok('标签 Mock 创建（行数 +1）', d.querySelectorAll('tbody tr').length === before + 1);
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '同步企微标签'));
  await sleep(850);
  ok('标签同步只确认受理，未宣称 Provider 成功', d.querySelector('#fb-toast')?.textContent.includes('已受理') && d.querySelector('#fb-toast')?.textContent.includes('尚未收到 Provider 同步结果'));
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '删除组'));
  ok('删除标签组前明确警告组内标签会一并删除', d.querySelector('#fb-body')?.textContent.includes('组内标签将同时删除'));
  click(dom, d.querySelector('#fb-ok'));
  await sleep(500);
  ok('删除标签组真实更新组列表与组内标签', d.querySelectorAll('[data-tag-group-card]').length === 4 && !d.body.textContent.includes('教育培训'));
  dom.window.close();
}

console.log('admin/questionnaireOps.html?id=1（旧版同款问卷提交后动作与外部推送）');
{
  const dom = await loadSurveyOperations(1);
  const d = dom.window.document;
  await waitFor(() => !!d.querySelector('.qo-page'), 3500);
  ok('问卷运营页使用旧版同款布局与两个独立维度', !!d.querySelector('.qo-page') && !!d.querySelector('.qo-summary-grid') && d.querySelectorAll('.qo-nav button').length === 2);
  ok('二维码卡片展示渠道选择器与标题副标题', !!d.querySelector('#qo-lead-channel') && !!d.querySelector('[data-qr-title]') && !!d.querySelector('[data-qr-subtitle]'));
  click(dom, d.querySelector('[data-mode="redirect"]'));
  await sleep(30);
  ok('直接跳转复刻 H5 与动态 URL Link 配置', !!d.querySelector('[data-target-type]') && !!d.querySelector('[data-h5-url]') && !!d.querySelector('[data-source-url]') && !!d.querySelector('[data-response-key]'));
  click(dom, d.querySelector('[data-tab="push"]'));
  await sleep(30);
  ok('外部推送复刻 Webhook 与业务参数', !!d.querySelector('#qo-push-url') && !!d.querySelector('[data-push-type]') && !!d.querySelector('[data-expires]') && !!d.querySelector('[data-day]') && !!d.querySelector('[data-frequency]') && !!d.querySelector('[data-remark]'));
  ok('外部推送保留自定义参数、测试推送与独立保存', !!d.querySelector('[data-add-param]') && !!d.querySelector('[data-test]') && !!d.querySelector('[data-save-push]'));
  dom.window.close();
}

console.log('admin/questionnaireOps.html（缺少 id 的明确空态）');
{
  const dom = await loadPage('admin/questionnaireOps.html', { opsGuardHttp: true });
  await sleep(50);
  const d = dom.window.document;
  const calls = dom.window.__opsGuardTest.calls;
  ok('缺少 id 显示明确空态且不渲染配置表单', d.body.textContent.includes('缺少问卷 ID') && !d.querySelector('#opsNavigationTarget') && !d.querySelector('#opsChannelResourceId') && !d.querySelector('#opsConfigurationReference'));
  ok('缺少 id 不提供保存入口', ![...d.querySelectorAll('button')].some((b) => b.textContent.includes('保存')));
  ok('缺少 id 不发问卷详情或写请求', calls.every((c) => c.method === 'GET') && !calls.some((c) => /questionnaires\/\d/.test(c.path) || /survey-operations|external-push/.test(c.path)));
  dom.window.close();
}

console.log('admin/groupops.html（本地计划与目录边界）');
{
  const dom = await loadPage('admin/groupops.html');
  const d = dom.window.document;
  ok('群运营计划页显示本地计划和本地队列口径', d.body.textContent.includes('群运营计划') && d.body.textContent.includes('本地队列'));
  ok('当前计划页保留功能且迁移历史不占用主壳入口', !d.querySelector('a[href="groupops.html?history=1"]'));
  ok('运营成员选项展示可信数值 staff_id，目录与 Provider 验收分开', d.body.textContent.includes('运营成员选项') && d.body.textContent.includes('staff_id=') && ['S06-031', 'S06-032'].every((id) => d.body.textContent.includes(id)) && d.body.textContent.includes('真实 Provider 配置与回执仍需单独验收'));
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查看群目录'));
  await sleep(30);
  ok('缺少 owner_staff_id 时不触发目录同步或 Provider 成功提示', d.querySelector('#fb-toast')?.textContent.includes('owner_staff_id'));
  dom.window.close();
}

console.log('admin/groupops 历史（真实四 GET，只读独立分页）');
{
  const dom = await loadPage('admin/groupops.html', { q: 'history=1', groupOpsHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__groupOpsHistoryHttpTest;
  const primary = d.querySelector('#group-history-primary');
  const secondary = d.querySelector('#group-history-secondary');
  ok('历史列表仅读取计划和目录，不加载当前计划/运营成员', test.calls.length === 2 && test.calls.every((c) => c.method === 'GET' && c.path.includes('/history/') && c.query === '?limit=20&offset=0'));
  ok('历史计划保留原 active 与归档状态、NULL、微秒时间，文本安全转义', primary.textContent.includes('源状态：active') && primary.textContent.includes('archived / 1') && primary.textContent.includes('NULL') && primary.textContent.includes('2026-08-28T01:02:03.123456Z') && primary.textContent.includes('<img') && !primary.querySelector('img'));
  ok('历史计划详情链接保留字符串 ID 与 history 模式', primary.querySelector('a')?.getAttribute('href') === 'groupopsDetail.html?history=1&id=9007199254740993');
  ok('目录两来源逐条保留，空字符串不同于 NULL，且未合成当前目录', secondary.textContent.includes('group_chats（本页）') && secondary.textContent.includes('wecom_group_chat_snapshots（本页）') && secondary.textContent.includes('""（源空字符串）') && secondary.textContent.includes('NULL') && d.querySelector('#stage').textContent.includes('不合并为当前群目录'));
  click(dom, primary.querySelector('[data-next]'));
  await sleep(30);
  ok('计划独立翻页不刷新目录', test.calls.length === 3 && test.calls[2].path.endsWith('/plans') && test.calls[2].query === '?limit=20&offset=20' && secondary.textContent.includes('offset=0'));
  click(dom, secondary.querySelector('[data-next]'));
  await sleep(30);
  ok('目录独立翻页并禁用尾页下一页', test.calls[3].path.endsWith('/directory') && test.calls[3].query === '?limit=20&offset=20' && secondary.querySelector('[data-next]')?.disabled);
  test.failures.plans = 503;
  click(dom, primary.querySelector('[data-refresh]'));
  await sleep(30);
  ok('读取失败清除旧计划，错误不泄底层信息且保留相邻目录', !primary.querySelector('[data-history-rows]') && primary.textContent.includes('HTTP 503') && !primary.textContent.includes('private database') && !primary.textContent.includes('legacy-code') && !!secondary.querySelector('[data-history-rows]'));
  delete test.failures.plans;
  click(dom, primary.querySelector('[data-retry]'));
  await sleep(30);
  ok('失败重试同 offset，无 Mock 回退', test.calls.at(-1).query === '?limit=20&offset=20' && primary.textContent.includes('offset=20'));
  ok('历史列表无创建/同步/激活/发送控件及写请求', ![...d.querySelectorAll('#stage button')].some((b) => /创建|同步|激活|发送/.test(b.textContent)) && test.calls.every((c) => c.method === 'GET'));
  dom.window.close();
}
{
  const dom = await loadPage('admin/groupopsDetail.html', { q: 'history=1&id=9007199254740993', groupOpsHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__groupOpsHistoryHttpTest;
  const primary = d.querySelector('#group-history-primary');
  const secondary = d.querySelector('#group-history-secondary');
  ok('历史详情只读计划下群与节点，字符串 ID 无精度损失', test.calls.length === 2 && test.calls.every((c) => c.path.includes('/history/plans/9007199254740993/')));
  ok('源节点 day=0、触发标签空格、排序与原状态原样展示，正文和附件经冻结只读组件呈现且不执行', secondary.textContent.includes('源 day_index：0') && secondary.textContent.includes('源触发标签：  入群后  ') && secondary.textContent.includes('源排序：0') && secondary.textContent.includes('legacy_disabled') && secondary.textContent.includes('历史标题 <img') && secondary.textContent.includes('<script>历史消息</script>') && secondary.textContent.includes('image #m1') && !!secondary.querySelector('.send-readonly-detail') && !secondary.querySelector('script') && !secondary.querySelector('img'));
  ok('历史群保留 removed 原状态及空 removed_at，不推测当前群状态', primary.textContent.includes('源状态：removed') && primary.textContent.includes('移除时间：NULL'));
  click(dom, primary.querySelector('[data-next]'));
  await sleep(30);
  click(dom, secondary.querySelector('[data-next]'));
  await sleep(30);
  ok('计划群与节点各自分页', test.calls[2].path.endsWith('/groups') && test.calls[3].path.endsWith('/nodes') && test.calls.slice(2).every((c) => c.query === '?limit=20&offset=20'));
  test.failures.nodes = 403;
  click(dom, secondary.querySelector('[data-refresh]'));
  await sleep(30);
  ok('节点权限失败清空节点，保留群读取结果且不伪造空态', !!secondary.querySelector('[role="alert"]') && !secondary.querySelector('[data-history-rows]') && primary.textContent.includes('历史群') && !secondary.textContent.includes('暂无历史记录'));
  delete test.failures.nodes;
  click(dom, secondary.querySelector('[data-retry]'));
  await sleep(30);
  ok('节点同页重试恢复，未接当前节点/消息/Provider 写操作', secondary.textContent.includes('offset=20') && test.calls.at(-1).query === '?limit=20&offset=20' && test.calls.every((c) => c.method === 'GET' && c.path.includes('/history/')) && !d.querySelector('#groupOpsNodes'));
  dom.window.close();
}
for (const rel of ['admin/groupops.html', 'admin/groupopsDetail.html']) {
  const dom = await loadPage(rel, { q: 'history=1&id=9007199254740993', groupOpsHistoryHttp: { empty: true } });
  ok(`${rel} 合法空页独立显示真实空态`, dom.window.document.querySelectorAll('[data-history-rows]').length === 2 && [...dom.window.document.querySelectorAll('[data-history-rows]')].every((el) => el.textContent.includes('暂无历史记录')) && [...dom.window.document.querySelectorAll('[data-next]')].every((el) => el.disabled));
  dom.window.close();
}
{
  const dom = await loadPage('admin/groupopsDetail.html', { q: 'history=1&id=01', groupOpsHistoryHttp: {} });
  ok('历史计划非法 ID 在 HTTP 前失败关闭', dom.window.document.querySelector('[role="alert"]')?.textContent.includes('ID 无效') && dom.window.__groupOpsHistoryHttpTest.calls.length === 0);
  dom.window.close();
}

console.log('admin/groupopsDetail.html（真实 HTTP 群目录选择）');
{
  const dom = await loadPage('admin/groupopsDetail.html', { id: 10, groupDirectoryHttp: true });
  const d = dom.window.document, test = dom.window.__groupDirectoryTest;
  const open = () => click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.includes('从成员名下群选择')));
  const action = (name) => click(dom, d.querySelector('[data-gd="' + name + '"]'));
  const owner = (value) => { const el = d.querySelector('[data-gd="owner"]'); el.value = String(value); el.dispatchEvent(new dom.window.Event('change', { bubbles: true })); };
  open(); await sleep(30);
  ok('原群引用只读且打开目录不自动刷新 Provider', d.querySelector('#groupOpsAssets').readOnly && d.querySelectorAll('[data-gd-remove]').length === 2 && !test.calls.some((c) => c.path.endsWith('/groups/sync')));
  owner(7); await sleep(30);
  ok('可信负责人 GET 使用真实 owner_userid 与有限分页', test.calls.some((c) => c.query === '?owner_userid=7&limit=50&offset=0') && d.querySelectorAll('[data-gd-ref]').length === 50 && d.querySelector('#group-directory').textContent.includes('<群7>') && !d.querySelector('#group-directory img'));
  click(dom, d.querySelector('[data-gd-ref="group-7-0"]'));
  action('next'); await sleep(30);
  click(dom, d.querySelector('[data-gd-ref="group-7-50"]'));
  owner(8); await sleep(30);
  click(dom, d.querySelector('[data-gd-ref="group-8-0"]'));
  ok('切换负责人和翻页保留选择及未知旧引用', d.querySelectorAll('[data-gd-remove]').length === 5 && !!d.querySelector('[data-gd-remove="unknown-old"]') && d.querySelector('#group-directory').textContent.includes('未在已加载目录确认'));
  owner(7); await sleep(30);
  ok('返回原负责人仍显示跨页已选中状态', d.querySelector('[data-gd-ref="group-7-0"]').checked);
  test.fail = true; action('next'); await sleep(30);
  ok('目录失败清除旧行但保留已选引用', !d.querySelector('[data-gd-ref]') && d.querySelectorAll('[data-gd-remove]').length === 5 && d.querySelector('#group-directory').textContent.includes('读取失败'));
  test.fail = false; action('read'); await sleep(30);
  ok('失败重试沿同 owner 和 offset', test.calls.at(-1).query === '?owner_userid=7&limit=50&offset=50' && d.querySelector('[data-gd-ref="group-7-50"]').checked);
  click(dom, d.querySelector('[data-gd-remove="group-old"]'));
  action('apply'); await sleep(30);
  ok('使用选择仅更新待保存表单，未知引用不被静默删除', !d.querySelector('#group-directory') && d.querySelector('#groupOpsAssets').value.split('\n').join(',') === 'unknown-old,group-7-0,group-7-50,group-8-0' && !test.calls.some((c) => c.path.includes('/group-assets')));
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '保存计划'));
  await sleep(60);
  const writes = test.calls.filter((c) => c.path.includes('/group-assets'));
  ok('保存按真实 reference 删除与逐次 CAS 批量绑定', writes.length === 4 && writes[0].path.endsWith('/group-assets/group-old') && writes.map((c) => c.body.expected_revision).join(',') === '1,2,3,4' && test.detail.plan.revision === 5 && test.detail.group_assets.some((a) => a.asset_reference === 'unknown-old'));
  ok('绑定使用 CSRF 与各自幂等键且未发送群消息', writes.every((c) => c.headers.get('X-CSRF-Token') === 'group-directory-csrf' && c.headers.get('Idempotency-Key')) && !test.calls.some((c) => c.path.endsWith('/groups/sync') || c.path.endsWith('/run-due') || c.path.includes('/broadcast')));
  dom.window.close();
}
{
  const dom = await loadPage('admin/groupops.html', { groupDirectoryHttp: true });
  const d = dom.window.document, test = dom.window.__groupDirectoryTest;
  const action = (name) => click(dom, d.querySelector('[data-gd="' + name + '"]'));
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查看群目录')); await sleep(30);
  const select = d.querySelector('[data-gd="owner"]'); select.value = '8'; select.dispatchEvent(new dom.window.Event('change', { bubbles: true })); await sleep(30);
  ok('列表目录为只读浏览，不显示选择提交', !d.querySelector('[data-gd="apply"]') && !d.querySelector('[data-gd-ref]'));
  action('refresh');
  ok('刷新前明确 Provider 读取且确认前无请求', d.querySelector('#fb-body').textContent.includes('实际读取') && !test.calls.some((c) => c.path.endsWith('/groups/sync')));
  test.syncFail = true; click(dom, d.querySelector('#fb-ok')); await sleep(30);
  ok('刷新失败不展示旧快照或成功消息', d.querySelector('#group-directory').textContent.includes('读取失败') && !d.querySelector('#group-directory table'));
  test.syncFail = false; action('refresh'); click(dom, d.querySelector('#fb-ok')); await sleep(30);
  const refreshes = test.calls.filter((c) => c.path.endsWith('/groups/sync'));
  ok('刷新失败重试使用同幂等键并传闭合 owner body', refreshes.length === 2 && refreshes[0].headers.get('Idempotency-Key') === refreshes[1].headers.get('Idempotency-Key') && refreshes[1].body.owner_staff_id === 8 && refreshes[1].body.limit === 50);
  ok('刷新响应只宣称服务器目录快照，不等于送达', d.querySelector('#group-directory').textContent.includes('服务器返回目录快照') && d.querySelector('#group-directory').textContent.includes('当前响应无 Provider 读取回执'));
  test.empty = true; action('read'); await sleep(30);
  ok('合法空目录明确不等于企微没有群', d.querySelector('#group-directory').textContent.includes('本地目录为空；不等于企微没有群'));
  action('close'); test.ownersFail = true;
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '查看群目录')); await sleep(30);
  ok('成员读取失败不使用种子候选且禁用刷新', d.querySelector('#group-directory').textContent.includes('运营成员读取失败') && d.querySelectorAll('[data-gd="owner"] option').length === 1 && d.querySelector('[data-gd="refresh"]').disabled);
  dom.window.close();
}
{
  const dom = await loadPage('admin/groupopsDetail.html', { id: 10, groupDirectoryHttp: { deliveryProven: true } });
  await sleep(30);
  const d = dom.window.document, test = dom.window.__groupDirectoryTest;
  ok('旧群运营执行记录读取经 Host 投影的已验证送达，且保留真实 Provider 回执', d.querySelector('#stage').textContent.includes('已由已验证 Provider 回执证明送达') && d.querySelector('#stage').textContent.includes('Provider receipt=present') && test.calls.some((call) => call.path.endsWith('/plans/10/executions') && call.method === 'GET'));
  dom.window.close();
}
console.log('admin/groupopsDetail.html（真实 HTTP 保存与预览契约）');
{
  const dom = await loadPage('admin/groupopsDetail.html', { groupDirectoryHttp: true });
  const d = dom.window.document, test = dom.window.__groupDirectoryTest;
  d.querySelector('#groupOpsName').value = '新建的延时计划';
  d.querySelector('#groupOpsAssets').value = 'group-7-0';
  const staff = d.querySelector('#groupOpsStaff'); staff.options[0].selected = true;
  d.querySelector('#groupOpsNodes').value = JSON.stringify([
    { position: 1, kind: 'message', messageText: '第一条', materialPlan: { references: [{ kind: 'image', id: 17 }] } },
    { position: 2, kind: 'delay', delayMinutes: 5, materialPlan: { references: [] } },
    { position: 3, kind: 'message', messageText: '第二条', materialPlan: { references: [{ kind: 'attachment', id: 18 }] } },
  ]);
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存计划'));
  await sleep(120);
  const mutations = test.calls.filter((call) => call.method !== 'GET');
  const idempotentWrites = mutations.filter((call) => !call.path.endsWith('/content/preview'));
  const create = mutations.find((call) => call.path.endsWith('/plans') && call.method === 'POST');
  const delay = mutations.find((call) => call.path.endsWith('/nodes') && call.body.kind === 'delay');
  ok('新建计划以真实 HTTP 契约写入成员、群、即时/延时节点并读取本地预览', create?.body.name === '新建的延时计划' && create.headers.get('Idempotency-Key') && mutations.some((call) => call.path.endsWith('/members') && call.body.staff_id === 7) && mutations.some((call) => call.path.endsWith('/group-assets') && call.body.asset_reference === 'group-7-0') && delay?.body.delay_minutes === 5 && !('message_text' in delay.body) && delay.body.material_plan.references.length === 0 && test.calls.some((call) => call.path.endsWith('/content/preview') && call.method === 'POST') && idempotentWrites.every((call) => call.headers.get('Idempotency-Key')));
  dom.window.close();
}
{
  const dom = await loadPage('admin/groupopsDetail.html', { id: 10, groupDirectoryHttp: true });
  const d = dom.window.document, test = dom.window.__groupDirectoryTest;
  d.querySelector('#groupOpsName').value = '已编辑计划';
  d.querySelector('#groupOpsAssets').value = 'group-7-0';
  d.querySelector('#groupOpsNodes').value = JSON.stringify([{ position: 1, kind: 'message', messageText: '编辑后的消息', materialPlan: { references: [] } }, { position: 2, kind: 'delay', delayMinutes: 3, materialPlan: { references: [] } }]);
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存计划'));
  await sleep(120);
  const rename = test.calls.find((call) => call.path.endsWith('/plans/10') && call.method === 'PATCH');
  ok('编辑计划采用逐次 revision 与幂等键，替换群和节点后重新读取预览', rename?.body.name === '已编辑计划' && Number.isInteger(rename?.body.expected_revision) && rename.headers.get('Idempotency-Key') && test.calls.some((call) => call.path.endsWith('/group-assets/group-old') && call.method === 'DELETE') && test.calls.some((call) => call.path.endsWith('/nodes') && call.method === 'POST' && call.body.kind === 'delay' && call.body.delay_minutes === 3) && test.calls.filter((call) => call.path.endsWith('/plans/10')).some((call) => call.method === 'GET') && test.calls.some((call) => call.path.endsWith('/content/preview') && call.method === 'POST'));
  dom.window.close();
}
for (const [status, label, route] of [['active', '暂停', '/pause'], ['draft', '启用', '/activate'], ['paused', '启用', '/activate'], ['active', '归档', '/archive'], ['draft', '删除草稿', '/plans/10']]) {
  const dom = await loadPage('admin/groupops.html', { groupDirectoryHttp: { status } });
  const d = dom.window.document, test = dom.window.__groupDirectoryTest;
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === label));
  if (label !== '启用') click(dom, d.querySelector('#fb-ok'));
  await sleep(80);
  const request = test.calls.find((call) => call.path.endsWith(route) && (label === '删除草稿' ? call.method === 'DELETE' : call.method === 'POST'));
  ok(`原群运营列表${label}走真实生命周期 HTTP 契约`, Boolean(request?.headers.get('Idempotency-Key')) && Number.isInteger(request?.body.expected_revision));
  dom.window.close();
}

console.log('admin/groupopsDetail.html（typed 素材节点）');
{
  const dom = await loadPage('admin/groupopsDetail.html');
  const d = dom.window.document;
  ok('新建群运营计划生成且不含新客入群欢迎内容', d.body.textContent.includes('新建群运营计划') && !d.body.textContent.includes('新客入群欢迎'));
  ok('计划成员只能从目录多选且不再手填 staff_id', d.querySelector('#groupOpsStaff')?.multiple === true && d.querySelectorAll('#groupOpsStaff option').length > 0 && !d.querySelector('#groupOpsStaff textarea'));
  ok('节点素材选择器仅提供图片、小程序和附件', ['选择图片', '选择小程序', '选择附件'].every((text) => [...d.querySelectorAll('button')].some((b) => b.textContent.trim() === text)));
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '选择图片'));
  await sleep(160);
  ok('选择图片打开素材选择器', !!d.querySelector('.pk-mask'));
  click(dom, d.querySelector('[data-pk-id]'));
  click(dom, d.querySelector('[data-pk="ok"]'));
  await sleep(30);
  const nodes = JSON.parse(d.querySelector('#groupOpsNodes').value);
  ok('图片选择写入 typed materialPlan 而非旧引用', nodes[0].materialReference === undefined && nodes[0].materialPlan.references[0].kind === 'image' && Number.isInteger(nodes[0].materialPlan.references[0].id));
  dom.window.close();
}

console.log('admin/spProducts/spProductData（V1 历史只读 GET）');
{
  const dom = await loadPage('admin/spProducts.html', { q: 'history=1', serviceHistoryHttp: true });
  const d = dom.window.document;
  const test = dom.window.__serviceHistoryHttpTest;
  ok('历史定义显示真实商品、负时长、删除标记与只读链接', d.querySelector('#history-definitions')?.textContent.includes('历史周期商品 <原名>') && d.querySelector('#history-definitions')?.textContent.includes('-3 天') && d.querySelector('[data-history-definition="17"]')?.getAttribute('href') === 'spProductData.html?id=91&history=17' && !d.querySelector('原名'));
  ok('历史模式不显示创建/启用/分享等当前商品写按钮', ![...d.querySelectorAll('#stage button')].some((button) => /创建|启用|分享|购买|续费|授权/.test(button.textContent)));
  click(dom, d.querySelector('#history-definitions [data-history-next]'));
  await sleep(30);
  ok('定义下一页真实发送 limit/offset，渲染服务端第二页', test.calls.some((call) => call.path === '/api/admin/service-period-history' && call.query === '?limit=20&offset=20') && d.querySelectorAll('#history-definitions tbody tr').length === 1);
  click(dom, d.querySelector('#history-definitions [data-history-previous]'));
  await sleep(30);
  ok('定义上一页回到 offset=0，无 canonical 商品读取', test.calls.length === 3 && test.calls.every((call) => call.path === '/api/admin/service-period-history' && call.method === 'GET') && d.querySelectorAll('#history-definitions tbody tr').length === 20);
  test.failure = 'all';
  click(dom, d.querySelector('#history-definitions [data-history-next]'));
  await sleep(30);
  ok('翻页失败清除旧历史行并显示错误，不回退 Mock', !!d.querySelector('#history-definitions [role="alert"]') && d.querySelectorAll('#history-definitions tbody tr').length === 0);
  test.failure = '';
  click(dom, d.querySelector('#history-definitions [data-history-retry]'));
  await sleep(30);
  ok('读取重试仍为 GET 并保留失败页 offset', test.calls.at(-1)?.query === '?limit=20&offset=20' && test.calls.at(-1)?.method === 'GET' && d.querySelectorAll('#history-definitions tbody tr').length === 1);
  dom.window.close();
}
{
  const dom = await loadPage('admin/spProductData.html', { q: 'id=91&history=17', serviceHistoryHttp: true });
  const d = dom.window.document;
  const test = dom.window.__serviceHistoryHttpTest;
  ok('历史权益保留原状态、负续期、未关联客户，不造当前会员', d.querySelector('#history-entitlements')?.textContent.includes('V1 快照状态：expired') && d.querySelector('#history-entitlements')?.textContent.includes('续期计数：-2') && d.querySelector('#history-entitlements')?.textContent.includes('未关联客户') && !!d.querySelector('a[href="customerDetail.html?id=7"]'));
  ok('失败历史事件保留 NULL 和负调整，无执行按钮', d.querySelector('#history-events')?.textContent.includes('grant_failed_missing_unionid') && d.querySelector('#history-events')?.textContent.includes('调整天数：-7') && d.querySelector('#history-events')?.textContent.includes('未关联权益') && d.querySelector('#history-events')?.textContent.includes('未记录') && ![...d.querySelectorAll('#stage button')].some((button) => /执行|删除|编辑|授权|退款/.test(button.textContent)));
  click(dom, d.querySelector('#history-entitlements [data-history-next]'));
  click(dom, d.querySelector('#history-events [data-history-next]'));
  await sleep(30);
  ok('权益和事件分别按 definition_id 过滤并发送分页 GET', ['entitlements', 'events'].every((kind) => test.calls.some((call) => call.path === '/api/admin/service-period-history/17/' + kind && call.query === '?limit=20&offset=20')) && test.calls.length === 4 && test.calls.every((call) => call.method === 'GET' && call.path.startsWith('/api/admin/service-period-history/17/')));
  dom.window.close();
}
for (const fixture of [{ serviceHistoryEmpty: true }, { serviceHistoryFailure: 'events' }]) {
  const dom = await loadPage('admin/spProductData.html', { q: 'history=17', serviceHistoryHttp: true, ...fixture });
  const d = dom.window.document;
  ok(fixture.serviceHistoryEmpty ? '真实历史空页显示空态，分页关闭' : '事件读取失败只显示错误，权益区仍显示真实数据', fixture.serviceHistoryEmpty ? d.querySelector('#history-events')?.textContent.includes('暂无 V1 历史记录') && d.querySelector('#history-events [data-history-next]')?.disabled : !!d.querySelector('#history-events [role="alert"]') && d.querySelectorAll('#history-events tbody tr').length === 0 && d.querySelectorAll('#history-entitlements tbody tr').length === 20);
  dom.window.close();
}
{
  const dom = await loadPage('admin/spProductData.html', { q: 'history=0', serviceHistoryHttp: true });
  ok('无效历史定义 ID 在发请求前失败关闭', dom.window.document.querySelector('#stage')?.textContent.includes('定义 ID 无效') && dom.window.__serviceHistoryHttpTest.calls.length === 0);
  dom.window.close();
}

console.log('admin/spProductData.html（V1 Member Grid 历史只读 GET）');
{
  const dom = await loadPage('admin/spProductData.html', { q: 'member_grid_history=1&history_kind=view&product_id=91', memberGridHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__memberGridHistoryHttpTest;
  ok('旧保存视图只读页保留 false、负值和危险文本转义', d.querySelector('#stage')?.textContent.includes('默认：false') && d.querySelector('#stage')?.textContent.includes('位置：0') && d.querySelector('#stage')?.textContent.includes('<img src=x onerror=alert(1)>') && !d.querySelector('#stage img') && d.querySelector('#stage')?.textContent.includes('不表示当前登录、权益或权限'));
  ok('旧保存视图只调用真实生成 GET 且使用 Product 筛选', test.calls.length === 1 && test.calls[0].path === '/api/admin/member-grid-history/views' && test.calls[0].query === '?offset=0&limit=20&product_id=91' && test.calls[0].method === 'GET' && test.calls[0].credentials === 'include' && dom.window.__AICRM_TEST_MOCK__ === false);
  ok('切换类型会清空详情和异类型筛选', d.querySelector('a[href="spProductData.html?member_grid_history=1&history_kind=usage"]') !== null);
  const filter = d.querySelector('#member-grid-history-filter input');
  filter.value = '-1';
  d.querySelector('#member-grid-history-filter').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  ok('非法历史筛选留在当前列表并明确失败，不发 GET', d.querySelector('#member-grid-history-filter-error')?.textContent.includes('Product ID 必须') && test.calls.length === 1 && d.querySelector('#stage')?.textContent.includes('默认：false'));
  click(dom, d.querySelector('#member-grid-history-next'));
  await sleep(30);
  ok('旧保存视图以 20 条分页真实 GET', test.calls.at(-1)?.path === '/api/admin/member-grid-history/views' && test.calls.at(-1)?.query === '?offset=20&limit=20&product_id=91' && d.querySelector('#stage')?.textContent.includes('旧视图 21'));
  dom.window.close();
}
{
  const dom = await loadPage('admin/spProductData.html', { q: 'member_grid_history=1&history_kind=view&history_id=31&product_id=91', memberGridHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__memberGridHistoryHttpTest;
  ok('旧保存视图详情只使用详情 GET，且不泄露 ConfigJSON 或摘要', test.calls.length === 1 && test.calls[0].path === '/api/admin/member-grid-history/views/31' && test.calls[0].method === 'GET' && d.querySelector('#stage')?.textContent.includes('旧视图 ID') && !d.querySelector('#stage')?.textContent.includes('config_digest') && !d.querySelector('#stage')?.textContent.includes('source_key_digest'));
  test.fail = '/api/admin/member-grid-history/views/31';
  const retry = await loadPage('admin/spProductData.html', { q: 'member_grid_history=1&history_kind=view&history_id=31&product_id=91', memberGridHistoryHttp: { fail: '/api/admin/member-grid-history/views/31' } });
  ok('详情失败明确显示并允许 GET 重试，不回退 Mock', retry.window.document.querySelector('#stage')?.textContent.includes('HTTP 503') && !!retry.window.document.querySelector('#member-grid-history-detail-retry') && retry.window.__AICRM_TEST_MOCK__ === false);
  click(retry, retry.window.document.querySelector('#member-grid-history-detail-retry'));
  await sleep(30);
  ok('详情重试仍为同一路由 GET', retry.window.__memberGridHistoryHttpTest.calls.length === 2 && retry.window.__memberGridHistoryHttpTest.calls.every((call) => call.path === '/api/admin/member-grid-history/views/31' && call.method === 'GET'));
  retry.window.close();
  dom.window.close();
}
{
  const dom = await loadPage('admin/spProductData.html', { q: 'member_grid_history=1&history_kind=usage&customer_id=7', memberGridHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__memberGridHistoryHttpTest;
  ok('旧使用快照保留 false、NULL 和空学习计划', d.querySelector('#stage')?.textContent.includes('正式登录：false') && d.querySelector('#stage')?.textContent.includes('Token 使用：false') && d.querySelector('#stage')?.textContent.includes('（空）'));
  ok('旧使用快照只发送 Customer 筛选 GET', test.calls.length === 1 && test.calls[0].path === '/api/admin/member-grid-history/usage' && test.calls[0].query === '?offset=0&limit=20&customer_id=7' && test.calls[0].method === 'GET');
  dom.window.close();
}
{
  const dom = await loadPage('admin/spProductData.html', { q: 'member_grid_history=1&history_kind=usage&product_id=91', memberGridHistoryHttp: {} });
  ok('跨类型筛选请求前 fail closed', dom.window.document.querySelector('#stage')?.textContent.includes('不接受 Product ID 筛选') && dom.window.__memberGridHistoryHttpTest.calls.length === 0);
  dom.window.close();
}
{
  const dom = await loadPage('admin/spProductData.html', { q: 'member_grid_history=1&history_kind=view', memberGridHistoryHttp: { fail: true } });
  const text = dom.window.document.querySelector('#stage')?.textContent || '';
  ok('历史列表失败不伪装为空集或零计数，并保留 GET 重试', text.includes('HTTP 503') && text.includes('数量未获取') && text.includes('读取失败，未显示历史数据') && !text.includes('当前筛选没有 V1 历史记录') && !!dom.window.document.querySelector('#member-grid-history-retry'));
  dom.window.close();
}

console.log('admin/products.html（真实状态、销量与分享）');
{
  const dom = await loadPage('admin/products.html', { productHttp: true });
  const test = dom.window.__productHttpTest;
  let d = dom.window.document;
  ok('普通商品显示服务端生命周期与非零销量', d.querySelector('#stage')?.textContent.includes('已启用') && d.querySelector('#stage')?.textContent.includes('2') && !d.querySelector('#stage')?.textContent.includes('未投影'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '分享'));
  await sleep(40);
  // The frozen renderer may replace documentElement during its bootstrap. Read
  // the live document after the asynchronous share response, just as a browser
  // user sees the overlay rather than retaining a detached test node.
  d = dom.window.document;
  const expected = 'http://localhost/p/P%2F%E6%98%A5%E5%AD%A3%20sale';
  const dialog = d.querySelector('dialog[data-shared-qr-dialog="true"]');
  const svg = dialog?.querySelector('.shared-qr-dialog__code svg');
  ok('普通商品分享调用真实后端接口', test.calls.some((call) => call.path === '/api/admin/wechat-pay/products/7/share' && call.method === 'GET'));
  ok('分享弹窗接受 Owner 编码商品码并生成同源二维码', dialog?.open === true && dialog.classList.contains('shared-detail-drawer--center') && dialog.querySelector('input[readonly]')?.value === expected && svg?.getAttribute('data-qr-payload') === expected && svg?.querySelector('path'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '预览'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存二维码'));
  await sleep(20);
  ok('商品链接可预览且二维码可下载', test.opened[0]?.[0] === expected && test.downloads.length === 1);
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '关闭'));
  for (const [mode, label] of [['wrong_id', '错误商品 ID'], ['wrong_code', '错误商品编码'], ['nested_path', '额外路径'], ['query', '查询串']]) {
    test.setShareMode(mode);
    click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '分享'));
    await sleep(20);
    d = dom.window.document;
    ok(`分享拒绝${label}响应`, d.querySelector('#product-v3-toast')?.textContent === '商品分享响应不完整或越过站内边界' && !d.querySelector('#product-share-overlay'));
  }
  // jsdom.close() does not dispatch the browser pagehide lifecycle event. The
  // product-list Host owns a pagehide cleanup for its observer and overflow
  // action menus, so exercise that real teardown before removing the document.
  dom.window.dispatchEvent(new dom.window.Event('pagehide'));
  dom.window.close();
}
console.log('admin/productForm.html（渠道存量异常隔离）');
{
  const dom = await loadPage('admin/productForm.html', { id: 7, productHttp: true });
  const test = dom.window.__productHttpTest;
  let d = dom.window.document;
  ok('渠道目录 400 不再中断商品编辑页', d.body.textContent.includes('编辑普通商品') && d.querySelector('#pfName')?.value === '真实商品' && !d.body.textContent.includes('请求失败'));
  ok('编辑页仍读取权威商品与外推配置', test.calls.some((call) => call.path === '/api/v1/products/7') && test.calls.some((call) => call.path === '/api/admin/wechat-pay/products/7/external-push'));
  const mounted = await waitFor(() => {
    d = dom.window.document;
    return !!d.querySelector('[data-product-parity-push]') && !d.querySelector('[data-product-parity-push-save]')?.disabled;
  });
  d = dom.window.document;
  const configSave = d.querySelector('[data-product-parity-push-save]');
  const run = d.querySelector('[data-product-parity-push-test]');
  ok('冻结商品页由 V3 Host 挂载受控测试与未知状态', mounted && !!run && !!configSave && !d.body.textContent.includes('业务已送达'));
  input(dom, d.querySelector('[data-product-parity-push-type]'), 'member_renew');
  input(dom, d.querySelector('[data-product-parity-push-day]'), '45');
  input(dom, d.querySelector('[data-product-parity-push-frequency]'), '2');
  input(dom, d.querySelector('[data-product-parity-push-expires]'), '2147483647');
  input(dom, d.querySelector('[data-product-parity-push-remark]'), '保留业务备注');
  const campaignRow = [...d.querySelectorAll('[data-product-parity-param-row]')].find((row) => row.querySelector('[data-product-parity-param-key]')?.value === 'campaign');
  input(dom, campaignRow?.querySelector('[data-product-parity-param-value]'), 'renewal');
  click(dom, d.querySelector('a[href="#product-push"]'));
  click(dom, configSave);
  const expectedConfigBody = JSON.stringify({ enabled: true, webhook_url: 'https://hooks.example.test/paid', push_type: 'member_renew', expires_at_ts: 2147483647, day: 45, frequency: 2, remark: '保留业务备注', custom_params: '{"campaign":"renewal","count":9007199254740993,"enabled":true,"nested":{"keep":" 空白 "}}', expected_revision: 3 });
  const savedBusiness = await waitFor(() => {
    d = dom.window.document;
    return test.calls.some((call) => call.path === '/api/admin/wechat-pay/products/7/external-push' && call.method === 'PUT' && call.body === expectedConfigBody) && d.querySelector('[data-product-parity-push-result]')?.textContent === '配置已保存';
  });
  ok('商品外推业务参数以真实 HTTP 无损保存 JSON 类型与配置版本', savedBusiness && d.querySelector('[data-product-parity-push-save]')?.disabled === false);
  d = dom.window.document;
  const currentRun = d.querySelector('[data-product-parity-push-test]');
  if (currentRun) click(dom, currentRun);
  const unknownAndAccepted = await waitFor(() => {
    d = dom.window.document;
    return test.calls.filter((call) => call.path === '/api/admin/wechat-pay/products/7/external-push' && call.method === 'PUT').length >= 2 && test.calls.some((call) => call.path === '/api/admin/wechat-pay/products/7/external-push/test' && call.method === 'POST' && call.body === '{}') && d.querySelector('[data-product-parity-push-result]')?.textContent.includes('outcome_unknown') === true && d.querySelector('[data-product-parity-push-result]')?.textContent.includes('delivery_id: commerce_test_0123456789abcdef0123456789abcdef') === true;
  });
  ok('商品外推测试先保存再经实际 HTTP 受理，outcome_unknown 不称为业务送达', unknownAndAccepted && !d.body.textContent.includes('业务已送达'));
  dom.window.dispatchEvent(new dom.window.Event('pagehide'));
  dom.window.close();
}
console.log('admin/spProducts.html（真实周期商品分享）');
{
  const dom = await loadPage('admin/spProducts.html', { serviceProductHttp: true });
  let d = dom.window.document;
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '分享'));
  await sleep(40);
  d = dom.window.document;
  const test = dom.window.__serviceProductHttpTest;
  ok('当前周期商品列表不把迁移历史暴露为主壳入口', !d.querySelector('a[href="spProducts.html?history=1"]'));
  const expected = 'http://localhost/p/service_period/8';
  const svg = d.querySelector('#shareQrBox svg');
  ok('周期商品分享只读取真实 OpenAPI 投影', test.calls.some((call) => call.path === '/api/admin/service-period-products/8/share' && call.method === 'GET'));
  ok('分享链接与真实二维码均使用当前站点公开路径', d.querySelector('input[readonly]')?.value === expected && svg?.getAttribute('data-qr-payload') === expected && svg?.querySelector('path'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '预览'));
  click(dom, [...d.querySelectorAll('button')].find((button) => button.textContent.trim() === '保存二维码'));
  await sleep(30);
  ok('预览与二维码下载均沿用同一真实链接', test.opened[0]?.[0] === expected && test.downloads[0]?.download === 'SP-8-qr.svg');
  dom.window.close();
}
console.log('admin/spProductData.html?id=8（真实 Member Grid 受限查询与协作者）');
{
  const dom = await loadPage('admin/spProductData.html', { id: 8, serviceProductHttp: true });
  const d = dom.window.document;
  const test = dom.window.__serviceProductHttpTest;
  await sleep(70);
  const initialQuery = test.calls.find((call) => call.path.endsWith('/member-grid/query'));
  ok('Member Grid 初始读取使用真实默认视图与受限排序', initialQuery?.method === 'POST' && initialQuery.body?.view_id === 'default' && initialQuery.body?.sort === 'updated_at_desc' && initialQuery.body?.group_by === undefined && d.querySelector('#member-grid-staff option[value="7"]')?.textContent.includes('客服七'));
  const view = d.querySelector('#member-grid-view');
  const sort = d.querySelector('#member-grid-sort');
  const group = d.querySelector('#member-grid-group');
  view.value = '';
  sort.value = 'starts_at_desc';
  group.value = 'state';
  click(dom, d.querySelector('#member-grid-apply'));
  await sleep(50);
  const customQuery = test.calls.filter((call) => call.path.endsWith('/member-grid/query')).at(-1);
  ok('Member Grid 受限查询发送 starts_at_desc/state 且不伪造任意选择', customQuery?.body?.sort === 'starts_at_desc' && customQuery.body?.group_by === 'state' && customQuery.body?.view_id === undefined && d.querySelectorAll('[data-member-group]').length === 2);
  d.querySelector('#member-grid-staff').value = '7';
  d.querySelector('#member-grid-permission').value = 'edit';
  click(dom, d.querySelector('#member-grid-add'));
  await sleep(90);
  const addCall = test.calls.filter((call) => call.path.endsWith('/member-grid/collaborators')).at(-1);
  ok('Member Grid 协作者添加使用 active staff 真实目录与 V2 产品内权限', addCall?.method === 'POST' && addCall.body?.staff_id === 7 && addCall.body?.permission === 'edit' && d.body.textContent.includes('V2 产品内') && d.body.textContent.includes('不发送企微邀请'));
  const addedPermission = d.querySelector('[data-collab-permission="7"]');
  addedPermission.value = 'view';
  click(dom, d.querySelector('[data-collab-update="7"]'));
  await sleep(90);
  const updateCall = test.calls.filter((call) => call.path.endsWith('/member-grid/collaborators/7')).at(-1);
  ok('Member Grid 协作者改权发送真实本地 HTTP PUT 与版本', updateCall?.method === 'PUT' && updateCall.body?.permission === 'view' && Number(updateCall.body?.expected_version) === 1);
  dom.window.confirm = () => true;
  click(dom, d.querySelector('[data-collab-remove="7"]'));
  await sleep(90);
  const removeCall = test.calls.filter((call) => call.path.endsWith('/member-grid/collaborators/7')).at(-1);
  ok('Member Grid 协作者移除发送真实本地 HTTP DELETE 且不宣称企微成功', removeCall?.method === 'DELETE' && !d.querySelector('[data-collab-update="7"]') && d.body.textContent.includes('未调用企微/Provider'));
  dom.window.close();
}

console.log('admin/couponData.html?id=0（5 统计卡 + 8 明细行 + 分享失败关闭）');
{
  const dom = await loadPage('admin/couponData.html', { id: 0 });
  const d = dom.window.document;
  ok(
    '5 张统计卡文案齐全',
    ['累计领取', '当前可用', '支付预占', '已使用', '已过期'].every((t) => d.body.textContent.includes(t)),
  );
  ok('领取明细 8 行', d.querySelectorAll('tbody tr').length === 8);
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '分享'));
  await sleep(30);
  ok('Mock 不伪造优惠券分享成功', d.body.textContent.includes('测试 Mock 不提供伪成功') && !d.querySelector('#shareQrBox svg'));
  dom.window.close();
}

console.log('admin/coupons.html?history=1（真实历史定义只读分页）');
{
  const regular = await loadPage('admin/coupons.html');
  ok('原优惠券管理保留新建且迁移历史不占主壳入口', !regular.window.document.querySelector('a[href="coupons.html?history=1"]') && [...regular.window.document.querySelectorAll('button')].some((button) => button.textContent.includes('新建优惠券')));
  regular.window.close();
  const dom = await loadPage('admin/coupons.html', { q: 'history=1', couponHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__couponHistoryHttpTest;
  const section = d.querySelector('#coupon-history-definitions');
  ok('历史定义显示真实20行、原状态金额和未产生外部效果说明', section.querySelectorAll('tbody tr').length === 20 && section.textContent.includes('expired') && section.textContent.includes('9 分') && d.body.textContent.includes('不代表当前可用权益') && d.body.textContent.includes('未触发发券'));
  ok('历史定义只链接历史领取核销并转义源说明', d.querySelector('[data-history-coupon="31"]')?.getAttribute('href') === 'couponData.html?history=1&id=31' && section.textContent.includes('<img src=x onerror=alert(1)>') && !section.querySelector('img'));
  click(dom, section.querySelector('[data-history-next]'));
  await sleep(30);
  ok('历史定义下一页实际GET offset20且不全量拉取', section.querySelectorAll('tbody tr').length === 1 && section.textContent.includes('历史券 21') && test.calls.at(-1)?.query === '?limit=20&offset=20' && section.querySelector('[data-history-next]').disabled);
  click(dom, section.querySelector('[data-history-previous]'));
  await sleep(30);
  ok('历史定义上一页恢复真实第一页，全部请求仅GET', section.querySelectorAll('tbody tr').length === 20 && test.calls.every((call) => call.path === '/api/admin/coupon-history' && call.method === 'GET' && call.credentials === 'include'));
  dom.window.close();
}

console.log('admin/couponData.html?history=1&id=31（真实领取核销独立分页）');
{
  const dom = await loadPage('admin/couponData.html', { q: 'history=1&id=31', couponHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__couponHistoryHttpTest;
  const claims = d.querySelector('#coupon-history-claims');
  const redemptions = d.querySelector('#coupon-history-redemptions');
  ok('历史明细只请求所选券的两个真实GET', test.calls.length === 2 && test.calls.every((call) => ['/api/admin/coupon-history/31/claims', '/api/admin/coupon-history/31/redemptions'].includes(call.path) && call.method === 'GET' && call.query === '?limit=20&offset=0'));
  ok('领取展示NULL客户、空原状态和不重写的倒置日期', claims.textContent.includes('未关联客户') && claims.textContent.includes('原状态：（空）') && claims.textContent.includes('2026-08-28T00:00:00.000000Z → 2025-01-01T00:00:00Z'));
  ok('核销保留NULL订单及原金额、不按公式重算且源原因转义', redemptions.textContent.includes('未关联订单') && ['5 分', '9 分', '17 分', '<b>原始原因</b>'].every((text) => redemptions.textContent.includes(text)) && !redemptions.querySelector('b'));
  ok('历史页面没有编辑、复制、分享、领取或核销按钮', [...d.querySelectorAll('#coupon-history-claims button, #coupon-history-redemptions button')].every((button) => ['上一页', '下一页'].includes(button.textContent.trim())));
  test.fail = '/api/admin/coupon-history/31/claims';
  click(dom, claims.querySelector('[data-history-next]'));
  await sleep(30);
  ok('领取第二页503清空旧记录，不影响核销且不回退Mock', claims.textContent.includes('HTTP 503') && claims.querySelectorAll('tbody tr').length === 0 && redemptions.querySelectorAll('tbody tr').length === 1);
  test.fail = false;
  click(dom, claims.querySelector('[data-history-retry]'));
  await sleep(30);
  ok('失败重读沿用同一offset，独立返回第21条且不触发写操作', claims.querySelectorAll('tbody tr').length === 1 && claims.textContent.includes('claim-21') && test.calls.at(-1)?.query === '?limit=20&offset=20' && test.calls.filter((call) => call.path.endsWith('/redemptions')).length === 1 && test.calls.every((call) => call.method === 'GET'));
  dom.window.close();
}

console.log('优惠券历史（空态、失败态、无效ID）');
{
  const empty = await loadPage('admin/coupons.html', { q: 'history=1', couponHistoryHttp: { empty: true } });
  ok('历史空集明确显示空态且不填入演示券', empty.window.document.body.textContent.includes('暂无 V1 历史记录') && !empty.window.document.querySelector('[data-history-coupon]'));
  empty.window.close();
  const failed = await loadPage('admin/coupons.html', { q: 'history=1', couponHistoryHttp: { fail: true } });
  ok('历史定义503明确失败，不伪装为空或回退演示数据', failed.window.document.body.textContent.includes('HTTP 503') && !failed.window.document.body.textContent.includes('暂无 V1 历史记录') && !failed.window.document.querySelector('[data-history-coupon]'));
  failed.window.close();
  const invalid = await loadPage('admin/couponData.html', { q: 'history=1&id=0', couponHistoryHttp: {} });
  ok('无效历史券ID在请求前失败，不转为当前券或Mock', invalid.window.document.body.textContent.includes('V1 历史优惠券 ID 无效') && invalid.window.__couponHistoryHttpTest.calls.length === 0);
  invalid.window.close();
}

console.log('admin/couponForm.html?id=31（HTTP 表单与商品选项）');
{
  const dom = await loadPage('admin/couponForm.html', { id: 31, couponHttp: true });
  const d = dom.window.document;
  ok('HTTP 模式挂载真实优惠券编辑表单', d.querySelector('#coupon-name')?.value === '新客券' && !!d.querySelector('#coupon-target-refs'));
  const calls = dom.window.__couponHttpTest.calls;
  ok('编辑读取和商品选项均经当前 OpenAPI GET', calls.some((call) => call.path === '/api/admin/coupons/31' && call.method === 'GET') && calls.some((call) => call.path === '/api/admin/coupons/product-options' && call.method === 'GET'));
  click(dom, d.querySelector('[data-target-ref="standard_product:9"]'));
  await sleep(30);
  ok('商品选项仅加入服务端 target_ref', d.querySelector('#coupon-target-refs').value.includes('standard_product:8') && d.querySelector('#coupon-target-refs').value.includes('standard_product:9'));
  dom.window.close();
}

console.log('admin/couponForm.html（HTTP 新建与商品选项筛选）');
{
  const dom = await loadPage('admin/couponForm.html', { couponHttp: true });
  const d = dom.window.document;
  ok('HTTP 模式挂载新建优惠券表单', d.body.textContent.includes('创建优惠券') && !!d.querySelector('#coupon-name') && !d.querySelector('#couponName'));
  input(dom, d.querySelector('#option-query'), '增长');
  d.querySelector('#option-type').value = 'standard_product';
  click(dom, d.querySelector('#option-search'));
  await sleep(30);
  const calls = dom.window.__couponHttpTest.calls;
  ok('商品选项筛选携带关键词、类型与分页参数', calls.some((call) => call.path === '/api/admin/coupons/product-options' && call.query.includes('q=%E5%A2%9E%E9%95%BF') && call.query.includes('product_type=standard_product') && call.query.includes('limit=20') && call.query.includes('offset=0')));
  ok('新建页只展示服务端返回的商品引用', d.body.textContent.includes('standard_product:9') && !d.body.textContent.includes('service_period:'));
  dom.window.close();
}

console.log('admin/couponForm.html?id=31（HTTP 读取失败关闭）');
{
  const dom = await loadPage('admin/couponForm.html', { id: 31, couponHttp: true, couponHttpFailure: true });
  ok('HTTP 读取失败显示错误态且不回退 Mock/Seed', dom.window.document.body.textContent.includes('请求失败（HTTP 503）') && !dom.window.document.querySelector('#couponName'));
  dom.window.close();
}

console.log('admin/couponForm.html?id=0（Mock 模板草稿态）');
{
  const dom = await loadPage('admin/couponForm.html', { id: 0 });
  const d = dom.window.document;
  ok('Mock 模式走同一模板与 VM', !!d.querySelector('#coupon-name') && !d.querySelector('#couponName') && d.body.textContent.includes('编辑优惠券'));
  ok('Mock 不伪造商品选项目录', d.body.textContent.includes('测试 Mock 不提供服务端商品选项目录') && !d.querySelector('#option-search'));
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '保存并发布'));
  await sleep(30);
  ok('Mock 优惠券写入被明确拒绝', d.body.textContent.includes('测试 Mock 不提供伪成功'));
  dom.window.close();
}

console.log('admin/spProductData.html?id=0（Mock 不伪造成员网格）');
{
  const dom = await loadPage('admin/spProductData.html', { id: 0 });
  const d = dom.window.document;
  ok('Mock 模式走同一模板与 VM', d.body.textContent.includes('Member Grid') && d.body.textContent.includes('返回周期商品'));
  ok('Mock 不展示模拟成员数据或可用写控件', d.body.textContent.includes('测试 Mock 环境不提供 Member Grid 服务端查询') && !d.querySelector('#member-grid-apply') && !d.querySelector('#member-grid-add') && !d.querySelector('[data-member-edit]'));
  dom.window.close();
}

console.log('admin/configDetail.html?cat=wechat_pay（类目配置点渲染）');
{
  const dom = await loadPage('admin/configDetail.html', { q: 'cat=wechat_pay' });
  const d = dom.window.document;
  ok('微信支付类目字段渲染（含 WECHAT_PAY_MCH_ID）', d.body.textContent.includes('WECHAT_PAY_MCH_ID'));
  ok('支持检查的类目显示「检查」按钮', [...d.querySelectorAll('button')].some((b) => b.textContent.trim() === '检查'));
  dom.window.close();
}

console.log('admin/config.html（本地 setup wizard 最小闭环）');
{
  const dom = await loadPage('admin/config.html');
  const d = dom.window.document;
  ok('本地接入能力收进 Kimi 配置类目表', !!d.querySelector('#open-setup-wizard') && !!d.querySelector('#open-admin-access') && !d.querySelector('#setup-wizard-form'));
  click(dom, d.querySelector('#open-setup-wizard'));
  await sleep(50);
  ok('只展示密钥配置状态且不渲染密钥输入框', d.body.textContent.includes('已配置（值不展示）') && !d.querySelector('input[type="password"]') && !d.body.textContent.includes('secret-value'));
  input(dom, d.querySelector('#setup-corp-id'), '');
  input(dom, d.querySelector('#setup-agent-id'), '0');
  d.querySelector('#setup-wizard-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  await sleep(30);
  ok('无效输入在请求前失败', d.querySelector('#setup-wizard-result')?.textContent === 'validation_error' && dom.window.__setupWizardTest.posts.length === 0);

  input(dom, d.querySelector('#setup-corp-id'), 'ww-updated');
  input(dom, d.querySelector('#setup-agent-id'), '2002');
  d.querySelector('#setup-wizard-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  await sleep(80);
  const post = dom.window.__setupWizardTest.posts[0];
  ok('保存只写两个本地字段且密钥字段固定为空', post?.body['wecom.corp_id'] === 'ww-updated' && post?.body['wecom.agent_id'] === 2002 && ['wecom.secret', 'wecom.callback_token', 'wecom.callback_aes_key', 'ai.api_key'].every((key) => post.body[key] === ''));
  ok('保存携带幂等键并以本地回执成功，不跳转', typeof post?.key === 'string' && post.key.length > 0 && d.querySelector('#setup-wizard-result')?.textContent === 'save_success' && dom.window.location.pathname.endsWith('/admin/config.html'));
  click(dom, d.querySelector('#close-config-extension'));
  click(dom, d.querySelector('#open-admin-access'));
  await sleep(50);
  ok('后台访问成员读取且明确仅限本地登录', d.querySelectorAll('[data-admin-access-id]').length === 3 && d.body.textContent.includes('不创建账号、角色或企微身份，不同步企微'));
  const access = d.querySelector('[data-admin-access-id="7"]');
  access.checked = false;
  access.dispatchEvent(new dom.window.Event('change', { bubbles: true }));
  d.querySelector('#admin-access-form').dispatchEvent(new dom.window.Event('submit', { bubbles: true, cancelable: true }));
  await sleep(80);
  const accessPut = dom.window.__adminAccessTest.puts[0];
  ok('后台登录权限保存带 CSRF、幂等且不提交停用成员', accessPut?.body.members?.length === 2 && accessPut.body.members.find((member) => member.admin_user_id === 7)?.login_enabled === false && typeof accessPut.key === 'string' && accessPut.key.length > 0 && accessPut.csrf === 'c'.repeat(43) && d.querySelector('#admin-access-result')?.textContent === 'save_success');
  dom.window.close();
}

console.log('admin/channelForm.html（完整 OpenAPI 渠道 DTO）');
{
  const dom = await loadPage('admin/channelForm.html');
  const d = dom.window.document;
  ok('载体、客服、素材与标签字段齐全', !!d.querySelector('#channelType') && !!d.querySelector('#channelOwner') && !!d.querySelector('#channelImageIds') && !!d.querySelector('#channelTagId'));
  ok('分配策略使用服务端 JSON DTO', !!d.querySelector('#channelAssignmentMode') && !!d.querySelector('#channelAssignmentStrategy') && !!d.querySelector('#channelAssignmentConfig'));
  d.querySelector('#channelName').value = '新客渠道';
  d.querySelector('#channelCode').value = 'new-customer';
  d.querySelector('#channelImageIds').value = 'not-an-id';
  click(dom, [...d.querySelectorAll('button')].find((b) => b.textContent.trim() === '保存当前维度'));
  await sleep(30);
  ok('无效素材引用在发请求前被阻止', d.body.textContent.includes('素材引用必须是正整数 ID'));
  dom.window.close();
}

console.log('admin/channels.html（HTTP 历史停用渠道列表）');
{
  const dom = await loadPage('admin/channels.html', { channelHttp: true });
  await sleep(50);
  const d = dom.window.document;
  const calls = dom.window.__channelHttpTest.calls;
  ok('历史渠道列表经真实 OpenAPI GET 显示停用定义', d.body.textContent.includes('V1 历史渠道') && d.body.textContent.includes('停用') && !d.body.textContent.includes('>inactive<') && calls.some((call) => call.path === '/api/admin/channels' && call.query.includes('limit=50') && call.query.includes('include_archived=true') && call.method === 'GET'));
  ok('空旧二维码不伪造渠道资产或外部成功', d.body.textContent.includes('后端未返回二维码地址') && !d.body.textContent.includes('已执行') && !d.body.textContent.includes('Provider 已执行') && calls.every((call) => call.method === 'GET'));
  const missingRow = [...d.querySelectorAll('tr')].find((row) => row.textContent.includes('V1 历史渠道'));
  const missingAction = missingRow && [...missingRow.querySelectorAll('span, a')].find((el) => el.textContent.trim() === '后端未返回二维码地址');
  ok('缺 qr_download_url 时下载动作为禁用态且不是可点链接', !!missingAction && missingAction.tagName === 'SPAN' && missingAction.getAttribute('aria-disabled') === 'true');
  dom.window.close();
}

console.log('admin/channels.html（HTTP 有 qr_download_url 的下载语义）');
{
  const dom = await loadPage('admin/channels.html', { channelHttp: true, channelQrUrl: true });
  await sleep(50);
  const d = dom.window.document;
  const calls = dom.window.__channelHttpTest.calls;
  const row = [...d.querySelectorAll('tr')].find((tr) => tr.textContent.includes('V1 历史渠道'));
  const link = row && [...row.querySelectorAll('a')].find((a) => a.textContent.trim() === '下载二维码');
  ok('服务端返回 qr_download_url 时提供真实下载动作', !!link);
  const before = calls.length;
  link && click(dom, link);
  await sleep(30);
  ok('下载二维码不打开详情抽屉、不产生非 GET 请求', !d.body.textContent.includes('渠道详情与近期进入用户') && calls.slice(before).every((call) => call.method === 'GET'));
  dom.window.close();
}

console.log('admin/channelForm.html?id=49（HTTP 历史停用渠道安全默认）');
{
  const dom = await loadPage('admin/channelForm.html', { id: 49, channelHttp: true });
  await sleep(80);
  const d = dom.window.document;
  const calls = dom.window.__channelHttpTest.calls;
  const empty = (id) => d.querySelector(id)?.value === '';
  ok('编辑页经真实列表和详情 GET 显示停用定义', d.querySelector('#channelName')?.value === 'V1 历史渠道' && d.querySelector('#channelCode')?.value === 'v1-history-49' && d.querySelector('#channelStatus')?.value === 'inactive' && calls.some((call) => call.path === '/api/admin/channels' && call.method === 'GET') && calls.some((call) => call.path === '/api/admin/channels/49' && call.method === 'GET'));
  ok('空 QR、场景、欢迎语、素材、标签与客服保持空且本地资产读取不受企微开关影响', ['#channelScene', '#channelQrUrl', '#channelLinkUrl', '#channelFinalUrl', '#channelOwner', '#channelTagName', '#channelTagGroup', '#channelWelcome', '#channelImageIds', '#channelMiniIds', '#channelAttachmentIds', '#channelGroupInviteIds'].every(empty) && !d.querySelector('#channelAutoAccept')?.checked && d.querySelector('#channelFinalUrlPreview')?.textContent.includes('二维码载体不生成本地链接预览') && d.body.textContent.includes('尚未申请资产') && !d.body.textContent.includes('资产状态读取失败') && calls.some((call) => call.path === '/api/admin/channels/49/acquisition-assets' && call.method === 'GET') && ![...d.querySelectorAll('button')].some((button) => ['打开', '下载', '复制'].includes(button.textContent.trim())) && calls.every((call) => call.method === 'GET'));
  dom.window.close();
}

console.log('admin/channelForm.html?id=49（V1 归档历史只读分页）');
{
  const dom = await loadPage('admin/channelForm.html', { q: 'id=49&history=1', channelHttp: true });
  await sleep(80);
  const d = dom.window.document;
  const calls = dom.window.__channelHttpTest.calls;
  ok('V1 历史仅手动加载，不在渠道详情初始化时请求', !!d.querySelector('#channelHistoryLoad') && calls.every((call) => call.path !== '/api/admin/channels/49/history'));
  click(dom, d.querySelector('#channelHistoryLoad'));
  await sleep(50);
  ok('V1 历史使用真实生成 GET，展示归档联系人、未核验用户与客服快照', d.body.textContent.includes('801') && d.body.textContent.includes('21') && d.body.textContent.includes('legacy-owner-7') && d.body.textContent.includes('历史客服') && d.body.textContent.includes('不代表当前用户归属、员工权限') && calls.some((call) => call.path === '/api/admin/channels/49/history' && call.query.includes('limit=50') && call.query.includes('offset=0') && call.method === 'GET'));
  ok('V1 历史总数和下一页按 offset 准确呈现', d.querySelector('#channelHistoryRange')?.textContent.includes('共 51 条') && !!d.querySelector('#channelHistoryNext'));
  click(dom, d.querySelector('#channelHistoryNext'));
  await sleep(50);
  ok('V1 历史联系人按 50 条翻页，并保留 customer 未核验语义', d.body.textContent.includes('851') && d.body.textContent.includes('未核验') && calls.some((call) => call.path === '/api/admin/channels/49/history' && call.query.includes('offset=50') && call.method === 'GET'));
  dom.window.close();
}

console.log('admin/channelForm.html?id=49（V1 归档历史读取失败关闭）');
{
  const dom = await loadPage('admin/channelForm.html', { q: 'id=49&history=1', channelHttp: true, channelHistoryHttpFailure: true });
  await sleep(80);
  const d = dom.window.document;
  click(dom, d.querySelector('#channelHistoryLoad'));
  await sleep(50);
  ok('V1 历史 HTTP 失败明确显示且不回退 Mock/Seed', d.querySelector('#channelHistoryError')?.textContent.includes('请求失败（HTTP 503）') && !d.body.textContent.includes('legacy-owner-7') && dom.window.__channelHttpTest.calls.some((call) => call.path === '/api/admin/channels/49/history' && call.method === 'GET'));
  dom.window.close();
}

console.log('admin/channelForm.html?id=49（V1 归档历史空态）');
{
  const dom = await loadPage('admin/channelForm.html', { q: 'id=49&history=1', channelHttp: true, channelHistoryEmpty: true });
  await sleep(80);
  const d = dom.window.document;
  click(dom, d.querySelector('#channelHistoryLoad'));
  await sleep(50);
  ok('V1 历史空结果明确显示，不以渠道 Seed 或当前关系补全', d.querySelector('#channelHistoryEmpty')?.textContent.includes('没有可展示') && d.body.textContent.includes('没有可展示的 V1 历史客服快照') && d.querySelector('#channelHistoryRange')?.textContent.includes('暂无归档联系人') && !d.body.textContent.includes('legacy-owner-7'));
  dom.window.close();
}

console.log('admin/channelForm.html?id=49（HTTP 历史渠道读取失败关闭）');
{
  const dom = await loadPage('admin/channelForm.html', { id: 49, channelHttp: true, channelHttpFailure: true });
  await sleep(50);
  ok('历史渠道读取失败显示错误态且不回退 Mock/Seed', dom.window.document.body.textContent.includes('请求失败（HTTP 503）') && !dom.window.document.body.textContent.includes('V1 历史渠道') && dom.window.__channelHttpTest.calls.some((call) => call.path === '/api/admin/channels' && call.method === 'GET'));
  dom.window.close();
}

console.log('admin/ownerMig.html（冻结负责人迁移页 Host → Picker → Preview → Confirm → 回查）');
{
  const dom = await loadPage('admin/ownerMig.html', { ownerHandoffHttp: true });
  await sleep(160);
  const d = dom.window.document;
  const test = dom.window.__ownerHandoffHttpTest;
  const stage = d.querySelector('[data-owner-handoff-host]');
  const root = stage?.querySelector('[data-owner-migration-page]');
  const ownerCalls = () => test.calls.filter((call) => call.path.startsWith('/api/admin/customers/owner-handoffs'));
  ok('冻结页面由 V3 Host 实际挂载，读取可信上下文且不暴露手填 scope/客户 ID', stage?.dataset.ownerHandoffInit === 'ready' && Boolean(root) && !root.querySelector('[data-scope]') && !root.querySelector('[data-customers]') && root.querySelector('[data-import-file]')?.getAttribute('accept')?.includes('.xlsx') && ownerCalls().some((call) => call.path.endsWith('/context') && call.method === 'GET'));
  click(dom, root.querySelector('[data-owner-picker="source"]'));
  await sleep(20);
  click(dom, root.querySelector('[data-owner-picker="target"]'));
  await sleep(20);
  const memberDirectoryCalls = test.calls.filter((call) => call.path === '/api/admin/common/operation-members');
  ok('原/目标负责人经 V3 Staff Picker 分别选择停用源和在职目标', root.querySelector('[data-owner-userid="source"]')?.value === '10' && root.querySelector('[data-owner-userid="target"]')?.value === '20' && JSON.stringify(test.pickerOpens.map(({ source, scope, title }) => ({ source, scope, title }))) === JSON.stringify([
    { source: 'owner_migration.operation_members', scope: 'owner_migration', title: '选择原负责人' },
    { source: 'owner_migration.operation_members', scope: 'owner_migration', title: '选择目标负责人' },
  ]) && memberDirectoryCalls.some((call) => call.query.includes('scope=owner_migration') && call.query.includes('include_inactive=true')) && memberDirectoryCalls.some((call) => call.query.includes('scope=owner_migration') && call.query.includes('include_inactive=false')));
  click(dom, root.querySelector('[data-preview]'));
  await sleep(100);
  const preview = ownerCalls().find((call) => call.path.endsWith('/previews'));
  ok('Host 将全量范围和 wecom_then_crm 原样提交至真实 V3 preview 路由', preview?.method === 'POST' && preview.body.mode === 'wecom_then_crm' && preview.body.scope === 'all' && preview.body.source_staff_id === 10 && preview.body.target_staff_id === 20 && preview.body.customer_ids.length === 0 && d.querySelector('[data-preview-rows]')?.textContent.includes('external-42'));
  input(dom, root.querySelector('[data-confirm-phrase-input]'), 'CONFIRM');
  click(dom, root.querySelector('[data-execute]'));
  await sleep(100);
  const confirm = ownerCalls().find((call) => call.path.endsWith('/confirm'));
  ok('Host 只在冻结 preview hash 和确认语存在时确认，并展示逐行排队结果', confirm?.method === 'POST' && confirm.body.preview_id === 'preview-owner-host' && confirm.body.preview_hash === 'preview-hash' && confirm.body.confirmation_phrase === 'CONFIRM' && root?.dataset.ownerHandoffBatchId === 'batch-owner-host' && root.querySelector('[data-execution-log]')?.textContent.includes('排队中') && !root.querySelector('[data-execution-log]')?.textContent.includes('queued'));
  click(dom, root.querySelector('[data-read-transfer-result]'));
  await sleep(100);
  const readback = ownerCalls().find((call) => call.path.endsWith('/transfer-result'));
  ok('企微回查是独立 POST，不从页面直接写 Provider，并回填逐行中文状态', readback?.method === 'POST' && root.querySelector('[data-execution-log]')?.textContent.includes('已读取结果') && root.querySelector('[data-execution-log]')?.textContent.includes('企微转接已完成') && !root.querySelector('[data-execution-log]')?.textContent.includes('observed') && !root.querySelector('[data-execution-log]')?.textContent.includes('transfer_status=1'));
  dom.window.__aicrmDownload = null;
  dom.window.__aicrmDownloadRevocations = [];
  dom.window.URL.createObjectURL = () => 'blob:owner-handoff';
  dom.window.URL.revokeObjectURL = (url) => { dom.window.__aicrmDownloadRevocations.push(url); };
  dom.window.HTMLAnchorElement.prototype.click = function () { dom.window.__aicrmDownload = { filename: this.download, href: this.href }; };
  click(dom, root.querySelector('[data-download-result]'));
  await sleep(100);
  const completedDownload = dom.window.__aicrmDownload;
  const completedRevocations = dom.window.__aicrmDownloadRevocations.length;
  ok('结果导出重新读取批次，产出旧流程要求的 XLSX 明细，并在下载交接后再释放 blob URL', ownerCalls().some((call) => call.path.endsWith('/batches/batch-owner-host') && call.method === 'GET') && completedDownload?.filename === 'owner_migration_result.xlsx' && completedRevocations === 0);
  dom.window.close();
}
{
  const dom = await loadPage('admin/ownerMig.html', { ownerHandoffHttp: true });
  await sleep(160);
  const d = dom.window.document;
  const root = d.querySelector('[data-owner-handoff-host] [data-owner-migration-page]');
  click(dom, root.querySelector('[data-owner-picker="source"]'));
  await sleep(20);
  click(dom, root.querySelector('[data-owner-picker="target"]'));
  await sleep(20);
  click(dom, root.querySelector('[data-scope-segment="excel_include"]'));
  const file = new dom.window.File(['external_userid,是否迁移,当前负责人userid,客户备注名,备注\nexternal-51,是,inactive-source,客户 51,旧名单\n'], 'owner-range.csv', { type: 'text/csv' });
  Object.defineProperty(root.querySelector('[data-import-file]'), 'files', { configurable: true, value: [file] });
  click(dom, root.querySelector('[data-upload-file]'));
  await sleep(100);
  click(dom, root.querySelector('[data-preview]'));
  await sleep(100);
  const preview = dom.window.__ownerHandoffHttpTest.calls.find((call) => call.path.endsWith('/previews'));
  ok('CSV 范围复用冻结 XLSX/CSV 解析器，保留旧五列和外部标识解析后才生成 V3 预览', preview?.method === 'POST' && preview.body.scope === 'excel_include' && preview.body.external_userids?.[0] === 'external-51' && preview.body.source_staff_id === 10 && preview.body.target_staff_id === 20 && root.querySelector('[data-preview-rows]')?.textContent.includes('external-51'));
  dom.window.close();
}

console.log('admin/ownerMig.html?contact_history=1（V1 联系人历史只读）');
{
  const dom = await loadPage('admin/ownerMig.html', { q: 'contact_history=1', contactHistoryHttp: {} });
  const d = dom.window.document;
  const test = dom.window.__contactHistoryHttpTest;
  const section = d.querySelector('#contact-history-content');
  ok('默认负责人旧结果只读取真实20行，明确不是 V2/Provider 成功', section.querySelectorAll('tbody tr').length === 20 && section.textContent.includes('V1 企微成功记录 2') && d.body.textContent.includes('不是 V2 迁移执行，也不是 Provider 成功证据') && test.calls.length === 1 && test.calls[0].path === '/api/admin/contact-history/owner-migration-results' && test.calls[0].query === '?limit=20&offset=0' && test.calls[0].method === 'GET');
  ok('历史结果不提供执行按钮', [...section.querySelectorAll('button')].every((button) => ['上一页', '下一页'].includes(button.textContent.trim())));
  click(dom, section.querySelector('[data-history-next]'));
  await sleep(30);
  ok('负责人旧结果下一页仍为同一只读 GET', section.querySelectorAll('tbody tr').length === 1 && section.textContent.includes('历史结果 #81') && test.calls.at(-1)?.query === '?limit=20&offset=20' && test.calls.every((call) => call.path === '/api/admin/contact-history/owner-migration-results' && call.method === 'GET'));
  dom.window.close();
}
{
  const dom = await loadPage('admin/ownerMig.html', { q: 'contact_history=1&history_kind=sidebar&customer_id=7', contactHistoryHttp: {} });
  const d = dom.window.document;
  const section = d.querySelector('#contact-history-content');
  const test = dom.window.__contactHistoryHttpTest;
  ok('Sidebar 历史保留客户过滤、空文本和转义原文字', section.querySelectorAll('tbody tr').length === 20 && section.textContent.includes('行业 <历史>') && section.textContent.includes('<img src=x onerror=alert(1)>') && !section.querySelector('img') && d.querySelector('#contact-history-customer-filter input')?.value === '7' && test.calls.length === 1 && test.calls[0].path === '/api/admin/contact-history/sidebar-profiles' && test.calls[0].query === '?limit=20&offset=0&customer_id=7');
  click(dom, d.querySelector('[data-contact-history-clear]'));
  await sleep(30);
  ok('Sidebar 筛选可清除并重读未过滤历史', test.calls.at(-1)?.query === '?limit=20&offset=0' && d.querySelector('#contact-history-customer-filter input')?.value === '');
  dom.window.close();
}
{
  const dom = await loadPage('admin/ownerMig.html', { q: 'contact_history=1&history_kind=owner&history_id=61', contactHistoryHttp: {} });
  const d = dom.window.document;
  const section = d.querySelector('#contact-history-content');
  const test = dom.window.__contactHistoryHttpTest;
  ok('单条历史结果只请求真实详情并保留原欢迎文字', section.textContent.includes('<b>原欢迎语</b>') && !section.querySelector('b') && test.calls.length === 1 && test.calls[0].path === '/api/admin/contact-history/owner-migration-results/61' && test.calls[0].method === 'GET');
  ok('详情切换类型回各自列表，不沿用另一张表的历史 ID', d.querySelector('a[href="ownerMig.html?contact_history=1&history_kind=sidebar"]') && section.querySelector('a[href="ownerMig.html?contact_history=1&history_kind=owner"]'));
  dom.window.close();
}
{
  const dom = await loadPage('admin/ownerMig.html', { q: 'contact_history=1', contactHistoryHttp: { fail: true } });
  const d = dom.window.document;
  ok('历史读取失败明确关闭，不回退当前迁移或 Mock', d.querySelector('#contact-history-content')?.textContent.includes('HTTP 503') && !d.body.textContent.includes('尚未生成迁移预览') && !d.body.textContent.includes('历史结果 #61'));
  dom.window.close();
}
for (const fixture of [
  { q: 'contact_history=1', contactHistoryHttp: { raw: true }, text: '额外 raw 字段在列表响应中失败关闭' },
  { q: 'contact_history=1&history_kind=owner&history_id=61', contactHistoryHttp: { raw: true }, text: '额外 raw 字段在详情响应中失败关闭' },
  { q: 'contact_history=1&history_kind=owner&history_id=61', contactHistoryHttp: { wrongID: true }, text: '详情返回错 ID 时失败关闭' },
  { q: 'contact_history=1&history_kind=sidebar&history_id=32&customer_id=7', contactHistoryHttp: { wrongCustomer: true }, text: 'Sidebar 详情返回错客户时失败关闭' },
]) {
  const dom = await loadPage('admin/ownerMig.html', fixture);
  ok(fixture.text, dom.window.document.querySelector('#contact-history-content [role="alert"]')?.textContent.includes('响应无效') && !dom.window.document.body.textContent.includes('must-not-render'));
  dom.window.close();
}
{
  const dom = await loadPage('admin/ownerMig.html', { q: 'contact_history=1&history_kind=owner&history_id=61', contactHistoryHttp: { fail: true } });
  const d = dom.window.document;
  const test = dom.window.__contactHistoryHttpTest;
  test.fail = false;
  click(dom, d.querySelector('#contact-history-content [data-history-retry]'));
  await sleep(30);
  ok('详情失败后可按同一 GET 重试', d.querySelector('#contact-history-content')?.textContent.includes('<b>原欢迎语</b>') && test.calls.length === 2 && test.calls.every((call) => call.path === '/api/admin/contact-history/owner-migration-results/61' && call.method === 'GET'));
  dom.window.close();
}

/* ================= H5 ================= */
const h5Definition = {
  id: 7, slug: 'uat-survey', title: '真实问卷标题 <script>不执行</script>', description: '服务端说明', version: 3, answer_display_mode: 'all_in_one',
  questions: [
    { id: 101, type: 'single_choice', title: '真实单选一', required: true, sort_order: 0, minimum_selections: 1, maximum_selections: 1, options: [{ id: 11, option_text: '选项 A', sort_order: 0 }, { id: 12, option_text: '选项 B', sort_order: 1 }] },
    { id: 102, type: 'single_choice', title: '真实单选二', required: true, sort_order: 1, minimum_selections: 1, maximum_selections: 1, options: [{ id: 21, option_text: '选项 C', sort_order: 0 }, { id: 22, option_text: '选项 D', sort_order: 1 }] },
    { id: 103, type: 'multi_choice', title: '真实多选', required: true, sort_order: 2, minimum_selections: 2, maximum_selections: 2, options: [{ id: 31, option_text: '渠道甲', sort_order: 0 }, { id: 32, option_text: '渠道乙', sort_order: 1 }, { id: 33, option_text: '渠道丙', sort_order: 2 }] },
    { id: 104, type: 'single_choice', title: '真实选答题', required: false, sort_order: 3, minimum_selections: 1, maximum_selections: 1, options: [{ id: 41, option_text: '可跳过', sort_order: 0 }] },
  ],
};
const h5Result = { submission_id: 901, definition_version: 3, submitted_at: '2026-08-28T09:30:00Z', local_only: true, external_executed: false };
console.log('h5/all.html（真实定义、答案与幂等重试）');
{
  const dom = await loadPage('h5/all.html', { q: 'slug=uat-survey', h5Http: { definition: h5Definition, submissionStatuses: ['network', 503, 202] } });
  const d = dom.window.document;
  const calls = dom.window.__h5HttpTest.calls;
  const submissions = () => calls.filter((call) => call.path.endsWith('/submissions'));
  ok('H5 渲染服务端题目/选项，不显示 Seed 题目且文本不执行 HTML', d.querySelectorAll('[data-question-id]').length === 4 && d.body.textContent.includes(h5Definition.title) && d.body.textContent.includes('真实多选') && !d.body.textContent.includes('你目前最想解决') && !d.querySelector('#screen script'));
  ok('H5 无默认作答', d.querySelectorAll('[aria-pressed="true"]').length === 0);
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(20);
  ok('H5 未答必答题不发送提交请求', submissions().length === 0 && d.querySelector('[data-h5-error]')?.textContent.includes('真实单选一'));
  for (const id of [12, 21, 31, 32, 33]) click(dom, d.querySelector(`[data-option-id="${id}"]`));
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(20);
  ok('H5 校验多选数量上限', submissions().length === 0 && d.querySelector('[data-h5-error]')?.textContent.includes('真实多选'));
  click(dom, d.querySelector('[data-option-id="33"]'));
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(30);
  ok('H5 按 question ID 独立提交答案、选答空题省略', JSON.stringify(submissions()[0]?.body.answers) === JSON.stringify([{ question_id: 101, option_ids: [12] }, { question_id: 102, option_ids: [21] }, { question_id: 103, option_ids: [31, 32] }]) && submissions()[0]?.body.version === 3);
  const firstKey = submissions()[0]?.body.submission_key;
  ok('H5 32字节 base64url key 严格43字符', /^[A-Za-z0-9_-]{43}$/.test(firstKey || ''));
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(30);
  ok('H5 未知网络结果/HTTP失败保持同一答案同一key，未显示成功', submissions().length === 2 && submissions()[1].body.submission_key === firstKey && !d.querySelector('[data-h5-receipt]') && !!d.querySelector('[data-h5-error]'));
  click(dom, d.querySelector('[data-option-id="11"]'));
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(30);
  ok('H5 改答案使用新key并按真实 completion action 结束答题页', submissions().length === 3 && submissions()[2].body.submission_key !== firstKey && submissions()[2].body.answers[0].option_ids[0] === 11 && !d.querySelector('[data-h5-receipt]') && !d.querySelector('[data-h5-result-link]'));
  dom.window.close();
}

console.log('h5/one.html（真实逐题模式）');
{
  const dom = await loadPage('h5/one.html', { q: 'slug=uat-survey', h5Http: { definition: { ...h5Definition, answer_display_mode: 'one_by_one', questions: h5Definition.questions.slice(0, 2) } } });
  const d = dom.window.document;
  ok('逐题从真实第1/2题开始，不显示固定12题', d.querySelector('[data-h5-progress]')?.textContent.includes('第 1 / 2 题') && d.querySelectorAll('[data-question-id]').length === 1);
  click(dom, d.querySelector('[data-h5-next]'));
  ok('逐题未答不能跳过必答题', d.querySelector('[data-h5-error]')?.textContent.includes('真实单选一'));
  click(dom, d.querySelector('[data-option-id="12"]'));
  click(dom, d.querySelector('[data-h5-next]'));
  click(dom, d.querySelector('[data-option-id="21"]'));
  click(dom, d.querySelector('[data-h5-previous]'));
  ok('逐题往返保留各自答案', d.querySelector('[data-option-id="12"]')?.getAttribute('aria-pressed') === 'true');
  click(dom, d.querySelector('[data-h5-next]'));
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(30);
  const submitted = dom.window.__h5HttpTest.calls.find((call) => call.path.endsWith('/submissions'));
  ok('逐题只提交已选择的真实 option IDs', JSON.stringify(submitted?.body.answers) === JSON.stringify([{ question_id: 101, option_ids: [12] }, { question_id: 102, option_ids: [21] }]));
  dom.window.close();
}

console.log('h5/all.html（严格 11 位大陆手机号）');
{
  const mobileDefinition = {
    ...h5Definition,
    questions: [{ id: 105, type: 'mobile', title: '请输入手机号', required: true, sort_order: 0, options: [] }],
  };
  const dom = await loadPage('h5/all.html', { q: 'slug=uat-survey', h5Http: { definition: mobileDefinition } });
  const d = dom.window.document;
  const field = d.querySelector('[data-text-question-id="105"]');
  ok('手机号题使用单行 tel、数字键盘与 11 位上限', field?.tagName === 'INPUT' && field.getAttribute('type') === 'tel' && field.getAttribute('inputmode') === 'numeric' && field.getAttribute('maxlength') === '11');
  input(dom, field, '+8613800138000');
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(20);
  ok('手机号题拒绝 +86 且请求前失败', dom.window.__h5HttpTest.calls.every((call) => !call.path.endsWith('/submissions')) && d.querySelector('[data-h5-error]')?.textContent.includes('11位中国大陆手机号'));
  input(dom, field, '13800138000');
  click(dom, d.querySelector('[data-h5-submit]'));
  await sleep(30);
  const call = dom.window.__h5HttpTest.calls.find((entry) => entry.path.endsWith('/submissions'));
  ok('合法手机号按 11 位原值提交且不触发短信字段', call?.body.answers[0]?.text_value === '13800138000' && !('verification_code' in call.body));
  dom.window.close();
}

console.log('h5/result.html（真实结果与失败关闭）');
{
  const dom = await loadPage('h5/result.html', { q: '#result_token=' + 'r'.repeat(43), h5Http: { result: h5Result } });
  const d = dom.window.document;
  const call = dom.window.__h5HttpTest.calls[0];
  ok('结果token通过POST body查询真实结果', call?.path === '/api/public/survey-submission-results/query' && call.method === 'POST' && call.query === '' && call.body.result_token === 'r'.repeat(43));
  ok('结果只显示真实编号/版本/上海时间/本地效果，不伪造82分报告', !!d.querySelector('[data-h5-result]') && d.body.textContent.includes('901') && d.body.textContent.includes('v' + h5Result.definition_version) && d.body.textContent.includes('2026-08-28 17:30:00') && !d.body.textContent.includes('2026-08-28T09:30:00') && !d.body.textContent.includes('你的增长基本盘不错') && !d.body.textContent.includes('总分 / 100'));
  ok('结果页提供纯本地返回出口，不发请求', !!d.querySelector('[data-h5-local-exit]'));
  dom.window.close();
}
for (const scenario of [
  { page: 'all', q: '', definition: h5Definition, noRequest: true },
  { page: 'all', q: 'slug=uat-survey', definition: h5Definition, definitionStatus: 503 },
  { page: 'all', q: 'slug=uat-survey', definition: { ...h5Definition, questions: [] } },
  { page: 'all', q: 'slug=uat-survey', definition: { ...h5Definition, questions: [{ ...h5Definition.questions[0], type: 'free_text' }] } },
  { page: 'result', q: '', result: h5Result, noRequest: true },
  { page: 'result', q: 'result_token=' + 'r'.repeat(43), result: h5Result, resultStatus: 503 },
  { page: 'result', q: 'result_token=' + 'r'.repeat(43), result: { ...h5Result, external_executed: true } },
]) {
  const dom = await loadPage(`h5/${scenario.page}.html`, { q: scenario.q, h5Http: scenario });
  const d = dom.window.document;
  ok(`H5 ${scenario.page} 缺上下文/HTTP失败/非法契约均无Seed回退`, !!d.querySelector('[data-h5-error]') && !d.querySelector('[data-question-id]') && !d.querySelector('[data-h5-result]') && !d.querySelector('[data-h5-receipt]') && (!scenario.noRequest || dom.window.__h5HttpTest.calls.length === 0));
  dom.window.close();
}
{
  const dom = await loadPage('h5/all.html', { q: 'slug=uat-survey', h5Http: { definition: { ...h5Definition, questions: [...h5Definition.questions, { id: 105, type: 'free_text', title: '契约外开放题', required: false, sort_order: 4, minimum_selections: 0, maximum_selections: 1, options: [{ id: 51, option_text: '忽略', sort_order: 0 }] }] } } });
  const d = dom.window.document;
  ok('契约外题型降级为禁用题卡，不再整卷失败', d.querySelectorAll('[data-question-id]').length === 4 && !!d.querySelector('[data-h5-unsupported]') && d.querySelector('[data-h5-unsupported]')?.textContent.includes('当前端不支持该题型') && d.querySelector('[data-h5-error]')?.textContent.includes('暂不支持'));
  dom.window.close();
}
{
  const outside = await loadPage('h5/auth.html', { q: 'slug=uat-survey', h5Http: {} });
  const d = outside.window.document;
  ok('H5 auth 微信外禁止继续且提示在微信中打开', d.body.textContent.includes('请在微信中打开') && [...d.querySelectorAll('#screen button')].every((button) => button.disabled) && outside.window.__h5HttpTest.calls.length === 0);
  outside.window.close();

  const inside = await loadPage('h5/auth.html', { q: 'slug=uat-survey', h5Http: {}, h5WeChat: true });
  const insideDocument = inside.window.document;
  ok('H5 auth 微信内先读取安全会话并自动发起一次授权', ![...insideDocument.querySelectorAll('#screen button')].some((button) => !button.disabled) && inside.window.sessionStorage.getItem('survey.oauth:uat-survey') === 'started' && inside.window.__h5HttpTest.calls.length === 1 && inside.window.__h5HttpTest.calls[0].path === '/api/h5/surveys/session');
  inside.window.close();
}
for (const page of ['error', 'signup', 'active', 'expired', 'pay', 'qr']) {
  const dom = await loadPage(`h5/${page}.html`, { h5Http: {} });
  const d = dom.window.document;
  ok(`H5 ${page} 保留原壳但明确blocked，不调用Provider`, !!d.querySelector('[data-h5-blocked]') && d.body.textContent.includes('后端能力未就绪') && [...d.querySelectorAll('#screen button')].every((button) => button.disabled) && dom.window.__h5HttpTest.calls.length === 0 && !d.body.textContent.includes('诊断报告已生成'));
  dom.window.close();
}
{
  const dom = await loadPage('h5/done.html', { q: 'slug=uat-survey', h5Http: { sessionStatus: 200, session: { submitted: true, completion_action: { type: 'default' } } } });
  const d = dom.window.document;
  ok('H5 默认完成页只显示收到问卷的确认，不显示技术回执或结果入口', d.querySelector('[data-h5-done] h1')?.textContent === '收到你的问卷' && !d.querySelector('[data-h5-lead-qr]') && !d.querySelector('[data-h5-blocked]') && !d.querySelector('[data-h5-local-exit]'));
  dom.window.close();
}
for (const [label, session] of [
  ['未确认提交', { submitted: false, completion_action: { type: 'default' } }],
  ['redirect 动作', { submitted: true, completion_action: { type: 'redirect', redirect_url: 'https://completion.example/next' } }],
]) {
  const dom = await loadPage('h5/done.html', { q: 'slug=uat-survey', h5Http: { sessionStatus: 200, session } });
  const d = dom.window.document;
  ok(`H5 完成页${label}不闪现确认文案`, !d.querySelector('[data-h5-done]') && !d.body.textContent.includes('收到你的问卷'));
  dom.window.close();
}
for (const page of ['qr']) {
  const dom = await loadPage(`h5/${page}.html`, { h5Http: {} });
  const d = dom.window.document;
  ok(`H5 ${page} 提供可用的纯本地出口（非禁用按钮、不发请求）`, !!d.querySelector('#screen a[data-h5-local-exit]') && dom.window.__h5HttpTest.calls.length === 0);
  dom.window.close();
}
{
  const dom = await loadPage('h5/loading.html', { h5Http: {} });
  const d = dom.window.document;
  ok('H5 loading 为纯骨架屏：无 blocked 横幅、无业务按钮、不发请求', !d.querySelector('[data-h5-blocked]') && !d.body.textContent.includes('后端能力未就绪') && d.querySelectorAll('#screen button').length === 0 && dom.window.__h5HttpTest.calls.length === 0);
  dom.window.close();
}

/* ================= 侧边栏 ================= */
console.log('sidebar/index.html（dd8 标准 Overlay 与可信 Host）');
{
  const dom = await loadPage('sidebar/index.html');
  const d = dom.window.document;
  const sidebarManifest = JSON.parse(fs.readFileSync(path.join(DIST, 'asset-manifest.json'), 'utf8'));
  const sidebarHTML = fs.readFileSync(path.join(DIST, 'sidebar/index.html'), 'utf8');
  const sidebarHost = sidebarManifest.entries.sidebarHost;
  const sidebarOverlay = sidebarManifest.entries.sidebarStandardOverlay;
  const sidebarImageResourceLoader = sidebarManifest.entries.sidebarImageResourceLoader;
  const scripts = [...sidebarHTML.matchAll(/<script(?: type="module")? src="([^"]+)"><\/script>/g)].map((match) => match[1]);
  ok('最终 Sidebar 文档以企微 SDK、V3 Host 和哈希 dd8 Overlay 组成单一运行链',
    sidebarManifest.files[sidebarHost]?.entry_point === 'web/v3/sidebar/main.ts' &&
    sidebarManifest.files[sidebarOverlay]?.entry_point === 'web/dist/sidebar/sidebar_workbench_v3_overlay.js' &&
    sidebarManifest.files[sidebarImageResourceLoader]?.entry_point === 'web/donor-sources/production-dd8d60dd8ddb983aca2ec88cc9e65a9f7563f79f/static/image_resource_loader.js' &&
    JSON.stringify(scripts) === JSON.stringify(['https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js', `../${sidebarImageResourceLoader}`, `../${sidebarHost}`]) &&
    sidebarHTML.includes(`data-overlay-url="../${sidebarOverlay}"`) &&
    sidebarHTML.includes('id="customer-oneid"') && !sidebarHTML.includes('customer-external-userid') &&
    !sidebarHTML.includes('jweixin-1.6.0.js') && !sidebarHTML.includes('sidebar_workbench.js'));
  ok('标准 Overlay 保留六个业务菜单、统一壳和画像首屏，且不含聊天菜单',
    d.querySelector('#sidebar-workbench-root.wrap') && d.querySelector('.profile-card') &&
    [...d.querySelectorAll('#tabs [data-tab]')].map((node) => node.dataset.tab).join('|') === 'profile|questionnaires|products|orders|coupons|materials' &&
    d.body.textContent.includes('侧边栏测试客户') && d.querySelector('#customer-oneid')?.textContent === 'OneID CID-7' &&
    !d.body.textContent.includes('其他客服聊天') && !d.body.textContent.includes('chat_activity'));
  const stages = dom.window.__sidebarTest.wxStages.map((entry) => entry.stage);
  ok('Host 依次取得 regular config、agentConfig 和当前客户后只 bootstrap 一次',
    stages.indexOf('config') >= 0 && stages.indexOf('ready_callback') > stages.indexOf('config') &&
    stages.indexOf('agentConfig') > stages.indexOf('ready_callback') &&
    JSON.stringify(dom.window.__sidebarTest.wxStages.find((entry) => entry.stage === 'agentConfig')?.options?.jsApiList) === JSON.stringify(['getCurExternalContact', 'sendChatMessage']) &&
    dom.window.__sidebarTest.wxInvokes[0] === 'getCurExternalContact' && !dom.window.__sidebarTest.wxInvokes.includes('getContext') &&
    dom.window.__sidebarTest.requests.filter((url) => url.includes('/bootstrap')).length === 1 &&
    !dom.window.__sidebarTest.requests.some((url) => /other-staff-messages|chat-activity|chat_activity/.test(url)));
  dom.window.close();
}

for (const [scenario, expected] of [
  ['sdk_missing', '企微 SDK 未载入'],
  ['regular_error', 'preVerifyJSAPI:fail'],
  ['agent_error', 'agentConfig:fail'],
  ['contact_error', 'getCurExternalContact:fail'],
]) {
  const dom = await loadPage('sidebar/index.html', { q: `sidebar_case=${scenario}` });
  const d = dom.window.document;
  ok(`${scenario} 在可信联系人 bootstrap 前失败关闭并使用标准重试按钮`,
    d.body.textContent.includes(expected) && !!d.querySelector('[data-retry-boot]') &&
    dom.window.__sidebarTest.requests.filter((url) => url.includes('/bootstrap')).length === 0);
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=agent_retry' });
  const d = dom.window.document;
  click(dom, d.querySelector('[data-retry-boot]'));
  await sleep(80);
  const stages = dom.window.__sidebarTest.wxStages.map((entry) => entry.stage);
  ok('agentConfig 失败后重试重新建立 Host 上下文并只 bootstrap 一次',
    stages.filter((stage) => stage === 'config').length === 1 &&
    stages.filter((stage) => stage === 'agentConfig').length === 2 &&
    dom.window.__sidebarTest.requests.filter((url) => url.includes('/jssdk-config')).length === 2 &&
    dom.window.__sidebarTest.requests.filter((url) => url.includes('/bootstrap')).length === 1 &&
    !!d.querySelector('#tabs button[data-tab="materials"]:not([disabled])'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=success' });
  const d = dom.window.document;
  click(dom, d.querySelector('#tabs [data-tab="questionnaires"]'));
  await sleep(40);
  const questionnaireReads = dom.window.__sidebarTest.requests.filter((url) => url.includes('/questionnaires'));
  ok('问卷由 Overlay 标准卡片读取，重复菜单切换保持单次 scoped 请求',
    d.body.textContent.includes('满意度回访') && questionnaireReads.length === 1);

  click(dom, d.querySelector('#tabs [data-tab="coupons"]'));
  await sleep(40);
  const couponSend = (id) => d.querySelector(`[data-coupon-send="${id}"]`);
  ok('Coupon 目录显示规则状态而不创建客户领取记录；已有链接可分享给当前会话对象',
    d.body.textContent.includes('可领取目录券') && d.body.textContent.includes('已结束目录券') &&
    d.body.textContent.includes('已达到个人领取上限') &&
    couponSend('71')?.disabled === false && couponSend('72')?.disabled === true &&
    !d.body.textContent.includes('claim_id'));

  click(dom, d.querySelector('#tabs [data-tab="products"]'));
  await sleep(40);
  click(dom, d.querySelector('[data-product-send]'));
  await sleep(40);
  const news = dom.window.__sidebarTest.wxMessages.find((entry) => entry.payload?.msgtype === 'news');
  ok('商品先被 Outbound 接受，再以完整 news card 调用 JSSDK 并回写同一 grant',
    news?.method === 'sendChatMessage' && news.payload.news?.link === 'http://localhost/p/course-ordinary' &&
    news.payload.news?.imgUrl === 'https://assets.example.test/products/course-ordinary.png' &&
    dom.window.__sidebarTest.sendOutcomeBodies.some((body) => body.grant === 'grant-token' && body.outcome === 'client_executed'));

  click(dom, d.querySelector('#tabs [data-tab="coupons"]'));
  await sleep(40);
  click(dom, d.querySelector('[data-coupon-send="71"]'));
  await sleep(40);
  const couponNews = dom.window.__sidebarTest.wxMessages.find((entry) => entry.payload?.news?.link === 'http://localhost/c/coupon-71');
  ok('优惠券只发送既有领取链接：news 卡片经同一发送意图回写，不占用当前客户库存',
    dom.window.__sidebarTest.sendIntentBodies.some((body) => body.resource_kind === 'coupon' && body.resource_id === '71') &&
    couponNews?.method === 'sendChatMessage' && couponNews.payload.news?.desc === '点击领取优惠券' &&
    dom.window.__sidebarTest.sendOutcomeBodies.filter((body) => body.outcome === 'client_executed').length === 2);

  click(dom, d.querySelector('#tabs [data-tab="materials"]'));
  await sleep(40);
  click(dom, d.querySelector('[data-material-send]'));
  await sleep(40);
  const image = dom.window.__sidebarTest.wxMessages.find((entry) => entry.payload?.msgtype === 'image');
  const materialRead = new URL(dom.window.__sidebarTest.materialQueries[0], dom.window.location.origin);
  ok('图片素材经过接受回执后才调用 JSSDK；缩略图保持受 scope 的 blob 加载',
    image?.method === 'sendChatMessage' && image.payload.image?.mediaid === 'media-real-31' &&
    materialRead.pathname === '/api/sidebar/v2/materials' && !materialRead.searchParams.has('type') &&
    materialRead.searchParams.get('limit') === '5' && materialRead.searchParams.get('offset') === '0' &&
    !materialRead.searchParams.has('q') && typeof dom.window.ImageResourceLoader?.createPager === 'function' && !!d.querySelector('img[data-material-preview="ready"]'));
  dom.window.close();
}


for (const [scenario, expectedRequest] of [
  ['jssdk_401', '/api/sidebar/jssdk-config'],
  ['viewer_session_required', '/api/sidebar/v2/bootstrap'],
]) {
  const dom = await loadPage('sidebar/index.html', { q: `sidebar_case=${scenario}` });
  const d = dom.window.document;
  const state = dom.window.__sidebarTest;
  const scopedReads = state.requests.filter((url) => !url.includes('/jssdk-config') && !url.includes('/bootstrap'));
  ok(`${scenario} 在 401 后自动发起 OAuth，且不读取旧客户投影`,
    state.oauthTargets.length === 1 && state.requests.some((url) => url.includes(expectedRequest)) &&
    scopedReads.length === 0 && state.bootstrapBodies.length === (scenario === 'viewer_session_required' ? 1 : 0) &&
    !!d.querySelector('[data-retry-boot]'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=session_storage_unavailable' });
  const state = dom.window.__sidebarTest;
  ok('JSSDK sessionStorage 不可用仍以当次签名启动，未降级为匿名或缓存客户',
    state.requests.filter((url) => url.includes('/jssdk-config')).length === 1 &&
    state.bootstrapBodies.length === 1 && dom.window.document.body.textContent.includes('侧边栏测试客户'));
  dom.window.close();
}
for (const [scenario, expectedSignatureReads] of [['sdk_cache', 0], ['sdk_cache_v2', 1]]) {
  const dom = await loadPage('sidebar/index.html', { q: `sidebar_case=${scenario}` });
  const state = dom.window.__sidebarTest;
  const agentAPIs = state.wxStages.find((entry) => entry.stage === 'agentConfig')?.options?.jsApiList;
  ok(`${scenario} 只复用当前权限版本缓存，旧v2会重新读取正式签名`,
    state.requests.filter((url) => url.includes('/jssdk-config')).length === expectedSignatureReads &&
    JSON.stringify(agentAPIs) === JSON.stringify(['getCurExternalContact', 'sendChatMessage']) &&
    state.wxInvokes[0] === 'getCurExternalContact' && !state.wxInvokes.includes('getContext') &&
    state.bootstrapBodies.length === 1);
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=contact_switch' });
  const d = dom.window.document;
  const state = dom.window.__sidebarTest;
  state.contactSwitched = true;
  dom.window.dispatchEvent(new dom.window.Event('focus'));
  await sleep(100);
  ok('窗口重新可见时重验联系人；旧 context token 不可用于新客户',
    state.bootstrapBodies.map((body) => body.external_userid).join('|') === 'ext-7|ext-8' &&
    state.wxInvokes.filter((method) => method === 'getCurExternalContact').length === 3 &&
    dom.window.__AICRMSidebarBridge.contextToken().startsWith('sidebar-context-token-') &&
    d.querySelector('#customer-oneid')?.textContent === 'OneID CID-8');
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=regular_timeout' });
  const d = dom.window.document;
  const state = dom.window.__sidebarTest;
  await sleep(5_150);
  const configCallsBeforeRetry = state.wxStages.filter((entry) => entry.stage === 'config').length;
  click(dom, d.querySelector('[data-retry-boot]'));
  await sleep(100);
  ok('regular wx.config 超时后保持 unknown；重试不在同一 WebView 重配 regular SDK',
    configCallsBeforeRetry === 1 && state.wxStages.filter((entry) => entry.stage === 'config').length === 1 &&
    state.requests.filter((url) => url.includes('/bootstrap')).length === 0 &&
    d.body.textContent.includes('关闭并重新打开侧边栏'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=success' });
  const d = dom.window.document;
  const state = dom.window.__sidebarTest;
  input(dom, d.querySelector('[data-profile-field="source"]'), '活动报名');
  await waitFor(() => state.profileBodies.length === 1);
  click(dom, d.querySelector('#change-mobile-button'));
  input(dom, d.querySelector('#mobile-input'), '13800138000');
  click(dom, d.querySelector('#confirm-mobile-button'));
  await sleep(70);
  ok('画像保存带当前 profile CAS，手机号仅声明且 Host 丢弃 donor 外部标识字段',
    state.profileBodies.length === 1 && state.profileBodies[0].expected_profile_version === 0 &&
    state.profileBodies[0].source === '活动报名' && state.profileKeys[0]?.startsWith('sidebar-profile-') &&
    state.phoneBody?.phone === '13800138000' && !('external_userid' in state.phoneBody) &&
    state.phoneKey?.startsWith('sidebar-phone-') && d.body.textContent.includes('手机号已声明') &&
    !d.body.textContent.includes('手机号已验证'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=profile_conflict' });
  const d = dom.window.document;
  input(dom, d.querySelector('[data-profile-field="industry"]'), '教育');
  // Request capture precedes the asynchronous error response and Host render.
  // Assert the completed conflict state rather than racing response handling.
  await waitFor(() => dom.window.__sidebarTest.profileBodies.length === 1 && d.querySelector('#toast')?.textContent.includes('客户资料已更新'));
  ok('画像陈旧 CAS 冲突不会伪造已保存，也不会覆盖本地版本',
    dom.window.__sidebarTest.profileBodies.length === 1 &&
    dom.window.__sidebarTest.profileBodies[0].expected_profile_version === 0 &&
    d.querySelector('#toast')?.textContent.includes('客户资料已更新') && !d.querySelector('#toast')?.textContent.includes('已保存'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=success' });
  const d = dom.window.document;
  const state = dom.window.__sidebarTest;
  click(dom, d.querySelector('#tabs [data-tab="questionnaires"]'));
  await sleep(50);
  click(dom, d.querySelector('[data-load-more-questionnaires]'));
  await sleep(50);
  click(dom, d.querySelector('#tabs [data-tab="profile"]'));
  await sleep(20);
  click(dom, d.querySelector('[data-profile-view="timeline"]'));
  await sleep(50);
  click(dom, d.querySelector('[data-load-more-timeline]'));
  await sleep(50);
  const questionnaireReads = state.requests.filter((url) => url.includes('/questionnaires'));
  const timelineReads = state.requests.filter((url) => url.includes('/timeline'));
  ok('问卷和时间线使用 Customer 的 opaque cursor，逐页追加且不伪造 offset 或总数',
    d.body.textContent.includes('提交问卷') && d.body.textContent.includes('创建订单') &&
    questionnaireReads.length === 2 && questionnaireReads[1].includes('cursor=survey-page-2') &&
    timelineReads.length === 2 && timelineReads[1].includes('cursor=timeline-page-2') &&
    !questionnaireReads.concat(timelineReads).some((url) => url.includes('offset=')));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=success' });
  const d = dom.window.document;
  click(dom, d.querySelector('#tabs [data-tab="orders"]'));
  await sleep(50);
  const regularTruthful = d.body.textContent.includes('¥99.00') && d.body.textContent.includes('已支付') &&
    d.body.textContent.includes('创建时间') && !d.body.textContent.includes('退款¥') &&
    !d.body.textContent.includes('支付时间') && !d.querySelector('[data-order-detail-url]');
  click(dom, d.querySelector('[data-order-type="periodic"]'));
  await sleep(50);
  ok('订单和权益只渲染 Owner 投影：minor 金额、真实创建/周期边界，无虚构详情或付款时刻',
    regularTruthful && d.body.textContent.includes('生效时间') && d.body.textContent.includes('到期时间') &&
    d.body.textContent.includes('2026-08-01') && d.body.textContent.includes('2026-09-01') &&
    !d.querySelector('[data-order-detail-url]'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=order_refunded' });
  const d = dom.window.document;
  click(dom, d.querySelector('#tabs [data-tab="orders"]'));
  await sleep(50);
  ok('订单 Owner refunded 原码经既有状态契约显示已退款与真实退款金额',
    d.body.textContent.includes('已退款') && d.body.textContent.includes('退款¥99.00') &&
    d.body.textContent.includes('创建时间') && !d.body.textContent.includes('支付时间'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=send_accept_response_lost' });
  await dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' }).catch(() => undefined);
  const replayError = await dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' }).then(() => '', (error) => error.message);
  const lockedError = await dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' }).then(() => '', (error) => error.message);
  const state = dom.window.__sidebarTest;
  ok('发送意图接受响应丢失后同键重放无 grant 时锁定且不调用 JSSDK',
    state.sendIntentKeys.length === 2 && state.sendIntentKeys[0] === state.sendIntentKeys[1] &&
    state.wxMessages.filter((entry) => entry.method === 'sendChatMessage').length === 0 &&
    replayError.includes('已受理') && lockedError.includes('上次发送结果未确认'));
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=contact_switch_unknown' });
  await dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' }).catch(() => undefined);
  dom.window.__sidebarTest.contactSwitched = true;
  dom.window.dispatchEvent(new dom.window.Event('focus'));
  await waitFor(() => dom.window.__sidebarTest.bootstrapBodies.length === 2);
  await dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' });
  const state = dom.window.__sidebarTest;
  ok('切换可信客户后同一资源不继承前一客户的未知发送锁',
    state.bootstrapBodies.map((body) => body.external_userid).join('|') === 'ext-7|ext-8' &&
    state.sendIntentKeys.length === 2 && state.sendIntentKeys[0] !== state.sendIntentKeys[1] &&
    state.sendOutcomeBodies.filter((body) => body.outcome === 'outcome_unknown').length === 1 &&
    state.sendOutcomeBodies.filter((body) => body.outcome === 'client_executed').length === 1);
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=send_contact_no_focus' });
  dom.window.__sidebarTest.contactSwitched = true;
  const message = await dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' }).then(() => '', (error) => error.message);
  ok('无 focus 或 visibility 事件的客户切换在接受意图前由 SDK 重验阻止',
    message.includes('当前企微联系人已变化') && dom.window.__sidebarTest.sendIntentKeys.length === 0);
  dom.window.close();
}
{
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=send_accept_switch' });
  const state = dom.window.__sidebarTest;
  const sending = dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' });
  await waitFor(() => typeof state.releaseAccept === 'function');
  state.contactSwitched = true;
  state.releaseAccept();
  const message = await sending.then(() => '', (error) => error.message);
  ok('接受请求在途切换客户后不调用 JSSDK，并用原客户 token 关闭原 intent',
    message.includes('当前企微联系人已变化') &&
    state.wxMessages.filter((entry) => entry.method === 'sendChatMessage').length === 0 &&
    state.sendIntentTokens.length === 1 && state.sendIntentTokens[0].includes('-7-') &&
    state.sendOutcomeBodies.length === 1 && state.sendOutcomeBodies[0].outcome === 'final_failed' &&
    state.sendOutcomeTokens[0] === state.sendIntentTokens[0]);
  dom.window.close();
}
for (const scenario of ['send_callback_switch_ok', 'send_callback_switch_unknown']) {
  const dom = await loadPage('sidebar/index.html', { q: 'sidebar_case=' + scenario });
  const state = dom.window.__sidebarTest;
  const sending = dom.window.__AICRMSidebarBridge.send({ resource_kind: 'product', resource_id: '41' });
  await waitFor(() => typeof state.releaseSend === 'function');
  state.contactSwitched = true;
  state.releaseSend();
  await sending.catch(() => undefined);
  const expected = scenario === 'send_callback_switch_ok' ? 'client_executed' : 'outcome_unknown';
  ok(scenario + ' SDK 回调在途切换仍以原客户 token 回写原 intent',
    state.sendIntentTokens.length === 1 && state.sendIntentTokens[0].includes('-7-') &&
    state.sendOutcomeBodies.length === 1 && state.sendOutcomeBodies[0].outcome === expected &&
    state.sendOutcomeTokens[0] === state.sendIntentTokens[0]);
  dom.window.close();
}
for (const scenario of ['send_delayed', 'invoke_errmsg_fail', 'invoke_empty']) {
  const dom = await loadPage('sidebar/index.html', { q: `sidebar_case=${scenario}` });
  const d = dom.window.document;
  const state = dom.window.__sidebarTest;
  click(dom, d.querySelector('#tabs [data-tab="products"]'));
  await sleep(50);
  const send = d.querySelector('[data-product-send]');
  click(dom, send);
  click(dom, send);
  await sleep(50);
  if (scenario === 'send_delayed') {
    const held = state.sendIntentKeys.length === 1 && send.disabled && send.dataset.sending === 'true' && send.textContent.includes('发送中');
    state.releaseSend?.();
    await sleep(50);
    ok('发送中按钮禁止重复点击；同一 accepted intent 只记录一次成功回执',
      held && state.sendIntentKeys.length === 1 && state.sendOutcomeBodies.filter((body) => body.outcome === 'client_executed').length === 1 && !send.disabled);
  } else {
    click(dom, send);
    await sleep(50);
    ok(`${scenario} 没有明确 :ok 时只登记 outcome_unknown，禁止生成第二个发送意图`,
      state.sendIntentKeys.length === 1 && state.sendOutcomeBodies.filter((body) => body.outcome === 'client_executed').length === 0 &&
      state.sendOutcomeBodies.filter((body) => body.outcome === 'outcome_unknown').length === 1 &&
      d.querySelector('#toast')?.textContent.includes('上次发送结果未确认'));
  }
  dom.window.close();
}

console.log('admin/campaigns.html?history=1（七个真实GET只读历史）');
{
  const dom = await loadPage('admin/campaigns.html', { q: 'history=1', campaignHistoryHttp: {} });
  const d = dom.window.document, test = dom.window.__campaignHistoryHttpTest;
  const segments = d.querySelector('#campaign-history-segments');
  ok('Campaign历史列表读取分群与群发计划，不走当前任务接口', test.calls.length === 2 && segments?.querySelectorAll('tbody tr').length === 20 && d.querySelector('#campaign-history-plans tbody tr'));
  ok('源孤儿父关系明确标注且源文本转义', segments?.textContent.includes('missing_campaign') && segments.textContent.includes('<legacy>') && !segments.querySelector('legacy'));
  click(dom, segments.querySelector('[data-history-next]')); await sleep(30);
  ok('Campaign历史独立分页请求offset=20', segments.querySelectorAll('tbody tr').length === 1 && test.calls.at(-1).query === '?limit=20&offset=20');
  test.fail = '/api/admin/campaign-history/segments';
  click(dom, segments.querySelector('[data-history-retry]')); await sleep(30);
  ok('Campaign历史读取失败清空旧行并提供重试', !segments.querySelector('tbody') && segments.textContent.includes('未显示旧数据或演示数据'));
  test.fail = false;
  click(dom, segments.querySelector('[data-history-retry]')); await sleep(30);
  ok('Campaign历史重试保留失败页offset', segments.querySelectorAll('tbody tr').length === 1 && test.calls.at(-1).query === '?limit=20&offset=20');
  ok('Campaign历史所有请求均为携带会话的无body GET', test.calls.every((call) => call.method === 'GET' && call.credentials === 'include' && call.body === undefined));
  dom.window.close();

  const segment = await loadPage('admin/campaigns.html', { q: 'history=1&segment=11', campaignHistoryHttp: {} });
  const sd = segment.window.document, st = segment.window.__campaignHistoryHttpTest;
  ok('历史成员由服务端分群详情后按父ID查询', st.calls.length === 2 && st.calls[0].path.endsWith('/segments/11') && st.calls[1].query === '?limit=20&offset=0&segment_history_id=11' && sd.querySelector('#campaign-history-members tbody tr'));
  ok('缺失客户引用不猜配Customer360，负数源计数保留', !sd.querySelector('#campaign-history-members a') && sd.querySelector('#campaign-history-members').textContent.includes('retry：-6'));
  segment.window.close();

  const plan = await loadPage('admin/campaigns.html', { q: 'history=1&plan=31&recipient=41', campaignHistoryHttp: {} });
  const pd = plan.window.document, pt = plan.window.__campaignHistoryHttpTest;
  ok('历史群发计划、收件人、消息顺序使用三个独立GET', pt.calls.length === 3 && pt.calls[0].path.endsWith('/broadcast-plans/31') && pt.calls[1].path.endsWith('/broadcast-plans/31/recipients') && pt.calls[2].path.endsWith('/broadcast-recipients/41/messages'));
  ok('历史正文转义并保留原civil时间，已验证客户链接可见', pd.querySelector('#campaign-history-messages').textContent.includes('<old>') && pd.querySelector('#campaign-history-messages').textContent.includes('old civil') && !pd.querySelector('old') && pd.querySelector('#campaign-history-recipients a[href="customerDetail.html?id=7"]'));
  ok('历史页面没有审批、启动、发送写操作', [...pd.querySelectorAll('button')].every((button) => ['上一页', '下一页', '刷新本页'].includes(button.textContent.trim())));
  plan.window.close();

  const wrong = await loadPage('admin/campaigns.html', { q: 'history=1&plan=31&recipient=41', campaignHistoryHttp: { wrongPlan: true } });
  ok('消息跨计划不一致失败关闭，不展示正文', wrong.window.document.querySelector('#campaign-history-messages').textContent.includes('不一致') && !wrong.window.document.querySelector('#campaign-history-messages tbody'));
  wrong.window.close();
  const failed = await loadPage('admin/campaigns.html', { q: 'history=1&segment=11', campaignHistoryHttp: { fail: true } });
  ok('历史父详情失败时不读取子表', failed.window.__campaignHistoryHttpTest.calls.length === 1 && !failed.window.document.querySelector('#campaign-history-members'));
  failed.window.close();
  const empty = await loadPage('admin/campaigns.html', { q: 'history=1', campaignHistoryHttp: { empty: true } });
  ok('历史空页显示真实零条，不使用种子数据', empty.window.document.querySelector('#campaign-history-segments').textContent.includes('共 0 条'));
  empty.window.close();
  for (const q of ['history=1&segment=01', 'history=1&plan=31&segment=11', 'history=1&recipient=41', 'history=1&plan=31&plan=32', 'history=1&unknown=1']) {
    const invalid = await loadPage('admin/campaigns.html', { q, campaignHistoryHttp: {} });
    ok(`历史非法URL不发出请求: ${q}`, invalid.window.__campaignHistoryHttpTest.calls.length === 0 && !invalid.window.document.querySelector('#campaign-history-detail'));
    invalid.window.close();
  }
}

console.log('admin/campaigns.html（External Effects / Push Center 本地边界）');
{
  const effects = await loadPage('admin/campaigns.html', { q: 'view=external-effects' });
  await sleep(40);
  const doc = effects.window.document;
  const effectsText = doc.querySelector('#stage')?.textContent || '';
  ok('External Effects 显示本地事实边界与 outcome_unknown 人工确认',
    effectsText.includes('不证明 Provider 调用、外部发送或送达') &&
    effectsText.includes('outcome_unknown 存在') &&
    effectsText.includes('不得自动重试'));
  ok('Push Center 把 sent 标为本地状态，且未知结果没有重试操作',
    effectsText.includes('sent（本地状态）') &&
    effectsText.includes('结果未知：人工确认，禁止重试') &&
    !doc.querySelector('[data-push-retry="18"]'));
  ok('缺失 run-due 契约明确 backend_blocked', effectsText.includes('backend_blocked') && effectsText.includes('run-due'));
  effects.window.close();

  const detail = await loadPage('admin/campaigns.html', { q: 'view=external-effects&job=18' });
  await sleep(40);
  const detailDoc = detail.window.document;
  const detailText = detailDoc.querySelector('#stage')?.textContent || '';
  ok('Push Center job 详情只呈现本地 attempt/控制回执，不泄露收件人字段',
    detailText.includes('Push Center job 本地对账') &&
    detailText.includes('结果未知：需人工确认，禁止重试') &&
    !detailText.includes('customer_id') &&
    !detailText.includes('owner_staff_id'));
  detail.window.close();
}

console.log('admin/campaigns.html（运营计划全局本地审核列表）');
{
  const plans = await loadPage('admin/campaigns.html', { q: 'legacy_admin_path=%2Fadmin%2Fcloud-orchestrator%2Fplans' });
  await sleep(40);
  const doc = plans.window.document;
  let text = doc.querySelector('#stage')?.textContent || '';
  ok('运营计划入口读取真实全局计划索引并保留本地边界',
    text.includes('运营计划本地审核') && text.includes('spring-campaign') && text.includes('pending_review') &&
    text.includes('不代表发送、Provider 执行或送达') && plans.window.__planIndexCalls.some((url) => url.includes('/api/admin/cloud-orchestrator/plans')));
  input(plans, doc.querySelector('#plan-index-status'), 'pending_review');
  click(plans, doc.querySelector('#plan-index-refresh'));
  await sleep(40);
  text = doc.querySelector('#stage')?.textContent || '';
  ok('审核状态筛选传给真实计划索引且页面不展示收件人或消息正文',
    plans.window.__planIndexCalls.some((url) => url.includes('review_status=pending_review')) &&
    !text.includes('customer_ids') && !text.includes('message_override'));
  plans.window.close();
}

console.log('admin/campaigns.html（目标人员 Customer360 链接）');
{
  const planID = 'ctp_' + 'a'.repeat(64);
  const recipient = await loadPage('admin/campaigns.html', { q: `campaign=spring-campaign&plan=${planID}&recipient=7` });
  await sleep(40);
  const doc = recipient.window.document;
  const text = doc.querySelector('#stage')?.textContent || '';
  const customer360 = doc.querySelector('a[href="customerDetail.html?id=7"]');
  ok('已验证的 plan 目标只用 canonical OneID 链接既有 Customer360 档案',
    customer360?.textContent === '在 Customer360 查看档案' &&
    recipient.window.__recipientCalls.some((call) => call.url.endsWith('/recipients/7')));
  ok('计划与单客户审核展示真实本地 actor/time 审计且保持外部效果边界',
    doc.querySelector('[data-campaign-review-audit]')?.textContent.includes('actor #81') &&
    doc.querySelector('[data-campaign-review-audit]')?.textContent.includes('2026-08-27T00:00:00Z') &&
    doc.querySelector('[data-campaign-recipient-review-audit]')?.textContent.includes('actor #1') &&
    text.includes('不代表 Provider 发送、回执或送达') &&
    text.includes('按 trace/session 查看本地 audit 事实'));
  ok('目标人员列表没有可信状态投影时保持无成员状态筛选',
    text.includes('当前契约不含昵称、成员状态或消息任务') &&
    !doc.querySelector('[data-recipient-status]') &&
    recipient.window.__recipientCalls.every((call) => !call.url.includes('status=')));
  ok('只有计划、单客户审核通过且 handoff held 时展示受控发送动作',
    !!doc.querySelector('[data-recipient-dispatch-ready]') && text.includes('accepted/queued 不等于 Provider 发送或送达'));
  click(recipient, doc.querySelector('#recipient-dispatch'));
  await sleep(20);
  ok('单客户受控动作在发起前明确本地 EER 与外部边界',
    doc.querySelector('#fb-mask')?.hidden === false && doc.querySelector('#fb-body')?.textContent.includes('accepted/queued 不等于 Provider 发送或送达'));
  click(recipient, doc.querySelector('#fb-ok'));
  await sleep(40);
  const recipientDispatchCall = recipient.window.__recipientCalls.find((call) => call.url.endsWith('/recipients/7/dispatch'));
  ok('单客户 dispatch 使用真实 scoped POST，并只展示 handoff 本地汇总',
    recipientDispatchCall?.method === 'POST' && recipientDispatchCall.body?.external_gate === true &&
    doc.querySelector('#fb-toast')?.textContent.includes('handoff 本地汇总') && doc.querySelector('#fb-toast')?.textContent.includes('不等于送达'));
  recipient.window.close();
}

console.log('admin/campaigns.html（Campaign 成员状态本地投影）');
{
  const members = await loadPage('admin/campaigns.html', { q: 'campaign=spring-campaign', campaignHttp: {} });
  await sleep(40);
  const doc = members.window.document;
  click(members, doc.querySelector('#campaign-members-open'));
  await sleep(40);
  let text = doc.querySelector('#campaign-members-drawer')?.textContent || '';
  ok('成员抽屉读取真实本地状态与总数，不宣称发送结果',
    doc.querySelector('[data-campaign-members="ready"]') &&
    text.includes('共 51 人') && text.includes('pending_review') && text.includes('approved') && text.includes('rejected') &&
    text.includes('不代表 Provider 调用、发送或送达') &&
    members.window.__campaignHttpTest.calls.some((call) => call.path.endsWith('/members') && call.query === '?limit=50&offset=0' && call.method === 'GET'));
  click(members, doc.querySelector('#campaign-members-next'));
  await sleep(40);
  text = doc.querySelector('#campaign-members-drawer')?.textContent || '';
  ok('成员抽屉使用真实 offset 分页读取下一页',
    text.includes('51-51') && text.includes('51') &&
    members.window.__campaignHttpTest.calls.some((call) => call.path.endsWith('/members') && call.query === '?limit=50&offset=50'));
  members.window.close();

  const failed = await loadPage('admin/campaigns.html', { q: 'campaign=spring-campaign', campaignHttp: { failMembers: true } });
  await sleep(40);
  const failedDoc = failed.window.document;
  click(failed, failedDoc.querySelector('#campaign-members-open'));
  await sleep(40);
  const failedText = failedDoc.querySelector('#campaign-members-drawer')?.textContent || '';
  ok('成员读取失败时 fail closed 且不回退 Mock 或 Seed',
    failedDoc.querySelector('[data-campaign-members="error"]') &&
    failedText.includes('不会使用 Mock 或 Seed 补齐') &&
    !failedText.includes('pending_review'));
  failed.window.close();
}

console.log('admin/campaigns.html（trace/session 可观察性边界）');
{
  const observability = await loadPage('admin/campaigns.html', { q: 'view=observability' });
  await sleep(40);
  const doc = observability.window.document;
  let text = doc.querySelector('#stage')?.textContent || '';
  ok('无 trace/session 时刷新本地 Push 聚合，不发起无范围 audit 请求',
    text.includes('Push Center 保留全局本地聚合') && text.includes('本次不请求 audit 列表') &&
    observability.window.__observabilityCalls.length === 2 && observability.window.__observabilityCalls.every((call) => !call.url.includes('trace_id=')));
  input(observability, doc.querySelector('#observability-trace'), 'trace-audit-7');
  click(observability, doc.querySelector('#observability-filter'));
  await sleep(40);
  text = doc.querySelector('#stage')?.textContent || '';
  ok('trace_id 同时筛选真实 Push 聚合与 Cloud audit 本地事实',
    text.includes('Push Center 已按 trace_id trace-audit-7 筛选') && text.includes('campaign_recipient_dispatch_requested') &&
    observability.window.__observabilityCalls.length === 5 &&
    observability.window.__observabilityCalls.some((call) => call.url.includes('/cloud-orchestrator/audit?trace_id=trace-audit-7')) &&
    observability.window.__observabilityCalls.every((call) => !call.url.includes('session_id')));
  observability.window.close();

  const session = await loadPage('admin/campaigns.html', { q: 'view=observability&session_id=session-7' });
  await sleep(40);
  const sessionText = session.window.document.querySelector('#stage')?.textContent || '';
  ok('session_id 只筛选 Cloud audit，Push 保留真实全局本地聚合',
    sessionText.includes('Push Center 保留全局本地聚合') && sessionText.includes('session_id session-7') && sessionText.includes('campaign_recipient_dispatch_requested') &&
    session.window.__observabilityCalls.length === 3 &&
    session.window.__observabilityCalls.filter((call) => call.url.includes('/push-center/')).every((call) => !call.url.includes('session_id=')) &&
    session.window.__observabilityCalls.some((call) => call.url.includes('/cloud-orchestrator/audit?session_id=session-7')));
  session.window.close();
}

console.log('member-grid-share/index.html（公开会员网格 token fragment）');
{
  const token = `mgshare1.share_abcdefghijklmnopqrstuv.${'t'.repeat(43)}`;
  const shared = await loadMemberGridShare({
    token,
    response: {
      buckets: [
        { state: 'removed', count: 1 },
        { state: 'active', count: 8 },
        { state: 'expired', count: 2 },
      ],
      rows: [{ display_name: '李同学', state: 'active', source: 'manual', starts_at: '2026-08-01T00:00:00Z', expires_at: null, updated_at: '2026-08-27T09:00:00Z' }],
      limit: 50,
      next_cursor: '',
      has_more: false,
      as_of: '2026-08-27T10:00:00Z',
    },
  });
  const text = shared.dom.window.document.querySelector('#stage')?.textContent || '';
  const call = shared.trace.find((entry) => entry.kind === 'fetch');
  const request = call?.init || {};
  ok('公开汇总在首次请求前清除 fragment，并只以 JSON body 传 token',
    shared.trace[0]?.kind === 'replace' && shared.trace[0]?.url === '/member-grid-share/index.html' &&
    shared.dom.window.location.hash === '' && call?.url === '/api/public/member-grid-shares/summary' &&
    request.method === 'POST' && JSON.stringify(JSON.parse(String(request.body))) === JSON.stringify({ token }));
  ok('公开汇总请求不带凭据且禁止缓存', request.credentials === 'omit' && request.cache === 'no-store' && new Headers(request.headers).get('Content-Type') === 'application/json');
  ok('公开页只展示固定汇总与成员字段白名单', text.includes('有效') && text.includes('8') && text.includes('已过期') && text.includes('2') && text.includes('已移除') && text.includes('1') && text.includes('李同学') && text.includes('手工') && text.includes('汇总截至：2026-08-27T10:00:00Z'));
  shared.dom.window.close();

  const page = (name, hasMore, nextCursor) => ({
    buckets: [{ state: 'active', count: 2 }, { state: 'expired', count: 0 }, { state: 'removed', count: 0 }],
    rows: [{ display_name: name, state: 'active', source: 'paid_order', starts_at: '2026-08-01T00:00:00Z', expires_at: '2026-09-01T00:00:00Z', updated_at: '2026-08-27T09:00:00Z' }],
    limit: 50,
    next_cursor: nextCursor,
    has_more: hasMore,
    as_of: '2026-08-27T10:00:00Z',
  });
  const paged = await loadMemberGridShare({ token, responses: [page('第一位成员', true, 'mg2.opaque'), page('第二位成员', false, '')] });
  paged.dom.window.document.querySelector('button')?.click();
  await sleep(20);
  const pageCalls = paged.trace.filter((entry) => entry.kind === 'fetch');
  const pagedText = paged.dom.window.document.querySelector('#stage')?.textContent || '';
  ok('公开成员网格用 opaque cursor 加载后续页',
    pageCalls.length === 2 && JSON.parse(String(pageCalls[1].init.body)).cursor === 'mg2.opaque' && pagedText.includes('第一位成员') && pagedText.includes('第二位成员'));
  paged.dom.window.close();

  const failed = await loadMemberGridShare({ token, response: { message: 'internal detail must not appear' }, status: 503 });
  const leaked = await loadMemberGridShare({ token, response: { buckets: [{ state: 'active', count: 8 }, { state: 'expired', count: 2 }, { state: 'removed', count: 1 }], rows: [], limit: 50, next_cursor: '', has_more: false, as_of: '2026-08-27T10:00:00Z', customer_id: 7 } });
  const invalid = await loadMemberGridShare({ token: 'bad', response: {} });
  ok('公开页拒绝额外字段，失败统一收敛且不回退 Mock/本地数据',
    failed.dom.window.document.querySelector('#stage')?.textContent.includes('Member Grid 公开会员网格暂时无法读取分享网格。') &&
    leaked.dom.window.document.querySelector('#stage')?.textContent.includes('Member Grid 公开会员网格暂时无法读取分享网格。') &&
    invalid.dom.window.document.querySelector('#stage')?.textContent.includes('Member Grid 公开会员网格暂时无法读取分享网格。') &&
    failed.trace.filter((entry) => entry.kind === 'fetch').length === 1 && leaked.trace.filter((entry) => entry.kind === 'fetch').length === 1 && invalid.trace.filter((entry) => entry.kind === 'fetch').length === 0 &&
    failed.trace[0]?.kind === 'replace' && leaked.trace[0]?.kind === 'replace' && invalid.trace[0]?.kind === 'replace');
  failed.dom.window.close();
  leaked.dom.window.close();
  invalid.dom.window.close();
}

// Real entry routing must bypass current configuration and never invoke Mock.
{
  for (const [kind, route] of [['sop', 'sops'], ['config', 'configs'], ['prompt', 'prompts'], ['agent', 'agents']]) {
    for (const detail of [false, true]) {
      const dom = await loadPage('admin/config.html', { q: 'automation_history=1&history_kind=' + kind + (detail ? '&history_id=7' : ''), automationHistoryHttp: true });
      await sleep(20);
      const state = dom.window.__automationHistoryHttpTest;
      ok('自动化历史真实入口 ' + kind + (detail ? '详情' : '列表'),
        !!dom.window.document.querySelector('[data-automation-history]') && !dom.window.document.querySelector('[role="alert"]') &&
        state.calls.length === 1 && state.calls[0].path === '/api/admin/automation-history/' + route + (detail ? '/7' : '') &&
        state.calls[0].method === 'GET' && state.calls[0].body === undefined && !dom.window.document.querySelector('#setup-wizard-card'));
      dom.window.close();
    }
  }
  const invalid = await loadPage('admin/config.html', { q: 'automation_history=1&history_kind=agent&history_id=01', automationHistoryHttp: true });
  ok('自动化历史非法ID不读当前配置也不发请求', !!invalid.window.document.querySelector('[role="alert"]') && invalid.window.__automationHistoryHttpTest.calls.length === 0);
  invalid.window.close();
  const failed = await loadPage('admin/config.html', { q: 'automation_history=1', automationHistoryHttp: true });
  failed.window.__automationHistoryHttpTest.fail = true;
  failed.window.document.querySelector('[data-history-next]')?.removeAttribute('disabled');
  failed.window.document.querySelector('[data-history-next]')?.click();
  await sleep(20);
  ok('自动化历史入口失败清屏且无Mock回退', !!failed.window.document.querySelector('[role="alert"]') && !failed.window.document.querySelector('tbody'));
  failed.window.close();
}
await import('./automation-history-e2e.mjs');
await import('./survey-unresolved-history-contract.mjs');
await import('./survey-unresolved-history-http-e2e.mjs');
await import('./legacy-marketing-history-e2e.mjs');
await import('./broadcast-job-history-e2e.mjs');
await import('./outbound-task-history-e2e.mjs');
await import('./invalid-source-history-adapter-contract.mjs');
await import('./invalid-source-history-e2e.mjs');
await import('./product-edit-e2e.mjs');
execFileSync(process.execPath, [path.join(ROOT, 'v3', 'sidebarOneIDPresentation.test.mjs')], { stdio: 'inherit' });

console.log(`\n${pass} 通过 / ${fail} 失败`);
process.exit(fail ? 1 : 0);
