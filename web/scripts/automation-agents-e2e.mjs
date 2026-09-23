import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from './test-browser-bundle.mjs';

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const bundle = await buildTestBrowserBundle(path.join(root, 'src/admin/main.ts'));
const calls = [];
const agent = (patch = {}) => ({
  id: 7, automation_type: 'agent', agent_code: 'welcome_agent', agent_name: '欢迎 Agent',
  bound_package_key: '', bound_package_id: null, bound_package_name: '', fixed_material_summary: { image_count: 1, miniprogram_count: 1, attachment_count: 1, group_invite_count: 1 },
  status: 'paused', execution_enabled: false, materials_configured: false, updated_at: '2026-08-30T00:00:00Z', automation_type_label: 'Agent 机器人',
  draft_role_prompt: '旧角色', draft_task_prompt: '旧任务', published_role_prompt: '旧角色', published_task_prompt: '旧任务',
  draft_version: 1, published_version: 1, has_unpublished_changes: false,
  fixed_content_package: { content_text: '固定内容包正文', image_library_ids: [101], miniprogram_library_ids: [202], attachment_library_ids: [303], group_invite_library_ids: [404] },
  fixed_content_package_preview: { content_text: '固定内容包正文', material_summary: { image_count: 1, miniprogram_count: 1, attachment_count: 1, group_invite_count: 1 }, materials: [] },
  legacy_configuration: { scenario_code: 'welcome' }, ...patch,
});
const createdAgent = agent({ id: 9, agent_code: 'new_agent', agent_name: '新 Agent' });
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, headers: new Headers({ 'Content-Type': 'application/json' }), text: async () => JSON.stringify(body) });

function page(name, query = '') {
  const html = fs.readFileSync(path.join(root, `dist/admin/${name}.html`), 'utf8');
  const dom = new JSDOM(html, {
    url: `http://localhost/admin/${name}.html${query}`, runScripts: 'outside-only', pretendToBeVisual: true,
    beforeParse(window) {
      window.__AICRM_TEST_MOCK__ = false;
      window.document.cookie = `aicrm_csrf=${'c'.repeat(43)}`;
      window.fetch = async (input, init = {}) => {
        const url = new URL(String(input), window.location.origin); const method = init.method || 'GET';
        const call = { path: url.pathname, method, credentials: init.credentials, headers: new Headers(init.headers), body: init.body ? JSON.parse(String(init.body)) : undefined };
        calls.push(call);
        if (url.pathname === '/api/admin/automation-agents' && method === 'GET') return json({ ok: true, items: [agent()], total: 1 });
        if (url.pathname === '/api/admin/automation-agents' && method === 'POST') return json({ ok: true, agent: createdAgent });
        if (url.pathname === '/api/admin/automation-agents/7' && method === 'GET') return json({ ok: true, agent: agent() });
        if (url.pathname === '/api/admin/automation-agents/7/precheck' && method === 'GET') return json({ ok: true, agent_id: 7, configuration_ready: true, materials_configured: false, execution_enabled: false, can_activate: false, reasons: ['material_unconfigured', 'execution_disabled'], real_external_call_executed: false });
        if (url.pathname === '/api/admin/automation-agents/7' && method === 'PATCH') return json({ ok: true, agent: agent({ agent_name: call.body.agent_name, draft_role_prompt: call.body.role_prompt, draft_task_prompt: call.body.task_prompt }) });
        return json({ ok: false, code: 'unexpected_request', path: url.pathname }, 500);
      };
    },
  });
  dom.window.eval(bundle);
  return dom;
}

const assertHeaders = (call) => {
  if (call.credentials !== 'include') throw new Error('请求缺少 credentials=include');
  if (call.headers.get('X-CSRF-Token') !== 'c'.repeat(43)) throw new Error('请求缺少 CSRF 头');
  if (!call.headers.get('Idempotency-Key')) throw new Error('请求缺少 Idempotency-Key');
};
const wait = () => new Promise((resolve) => setTimeout(resolve, 80));

const list = page('agents');
await wait();
if (!list.window.document.body.textContent.includes('欢迎 Agent')) throw new Error('列表未渲染 Agent 名称');
if (!list.window.document.body.textContent.includes('已暂停')) throw new Error('列表未渲染已暂停状态');
for (const action of ['edit', 'copy', 'precheck', 'pause', 'archive']) {
  if (!list.window.document.querySelector(`[data-agent-action="${action}"]`)) throw new Error(`列表缺少操作 ${action}`);
}
for (const type of ['agent', 'fixed_script']) {
  if (!list.window.document.querySelector(`[data-agent-create="${type}"]`)) throw new Error(`列表缺少创建入口 ${type}`);
}
list.window.document.querySelector('[data-agent-action="precheck"]').click();
await wait();
if (!calls.some((call) => call.path === '/api/admin/automation-agents/7/precheck' && call.method === 'GET')) throw new Error('启用前检查未发起 GET 请求');
if (!list.window.document.body.textContent.includes('material_unconfigured') || !list.window.document.body.textContent.includes('execution_disabled')) throw new Error('启用前检查结果未渲染');
list.window.close();

const edit = page('agentEdit', '?id=7');
await wait();
const detail = edit.window.document;
const nameInput = detail.querySelector('#agentName');
const codeInput = detail.querySelector('#agentCode');
const typeInput = detail.querySelector('#agentType');
const roleInput = detail.querySelector('#agentRolePrompt');
const taskInput = detail.querySelector('#agentTaskPrompt');
if (!nameInput || !codeInput || !typeInput || !roleInput || !taskInput) throw new Error('编辑页缺少字段');
if (nameInput.value !== '欢迎 Agent' || codeInput.value !== 'welcome_agent' || roleInput.value !== '旧角色' || taskInput.value !== '旧任务') throw new Error('编辑页详情未回填');
const panel = detail.querySelector('[data-agent-materials-readonly]');
if (!panel) throw new Error('编辑页缺少素材只读面板');
for (const value of ['固定内容包正文', '101', '202', '303', '404']) {
  if (!panel.textContent.includes(value)) throw new Error(`素材只读面板缺少 ${value}`);
}
if (panel.querySelector('input, textarea, select, button')) throw new Error('素材只读面板包含可编辑控件');
if (!detail.querySelector('[data-agent-save]') || !detail.querySelector('[data-agent-precheck]')) throw new Error('编辑页缺少操作按钮');
nameInput.value = '欢迎 Agent 新版';
roleInput.value = '新角色';
taskInput.value = '新任务';
detail.querySelector('[data-agent-save]').click();
await wait();
const update = calls.find((call) => call.path === '/api/admin/automation-agents/7' && call.method === 'PATCH');
if (!update) throw new Error('编辑页未发起 PATCH 请求');
assertHeaders(update);
const patchKeys = Object.keys(update.body).sort();
if (JSON.stringify(patchKeys) !== JSON.stringify(['agent_name', 'automation_type', 'role_prompt', 'task_prompt'])) throw new Error('PATCH 请求体键不符合预期');
if (update.body.agent_name !== '欢迎 Agent 新版' || update.body.automation_type !== 'agent' || update.body.role_prompt !== '新角色' || update.body.task_prompt !== '新任务') throw new Error('PATCH 请求体值不符合预期');
edit.window.close();

const detailGetsBeforeCreate = calls.filter((call) => /\/api\/admin\/automation-agents\/\d+$/.test(call.path) && call.method === 'GET').length;
const create = page('agentEdit', '?type=agent');
await wait();
if (calls.filter((call) => /\/api\/admin\/automation-agents\/\d+$/.test(call.path) && call.method === 'GET').length !== detailGetsBeforeCreate) throw new Error('创建页意外发起数字详情请求');
const createDoc = create.window.document;
createDoc.querySelector('#agentName').value = '新 Agent';
createDoc.querySelector('#agentCode').value = 'new_agent';
createDoc.querySelector('#agentType').value = 'agent';
createDoc.querySelector('#agentRolePrompt').value = '新角色';
createDoc.querySelector('#agentTaskPrompt').value = '新任务';
createDoc.querySelector('[data-agent-save]').click();
await wait();
const created = calls.find((call) => call.path === '/api/admin/automation-agents' && call.method === 'POST');
if (!created) throw new Error('创建页未发起 POST 请求');
assertHeaders(created);
const postKeys = Object.keys(created.body).sort();
if (JSON.stringify(postKeys) !== JSON.stringify(['agent_code', 'agent_name', 'automation_type', 'role_prompt', 'task_prompt'])) throw new Error('POST 请求体键不符合预期');
if (created.body.agent_name !== '新 Agent' || created.body.agent_code !== 'new_agent' || created.body.automation_type !== 'agent' || created.body.role_prompt !== '新角色' || created.body.task_prompt !== '新任务') throw new Error('POST 请求体值不符合预期');
create.window.close();

if (calls.some((call) => /activate|send|dispatch|execute|provider|payment|refund/i.test(call.path))) throw new Error('Agent 页面意外触发外部效果请求');
if (calls.some((call) => !call.path.startsWith('/api/'))) throw new Error('存在非本地假 fetch 请求');
console.log('automation-agents-e2e: PASS');
