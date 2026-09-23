#!/usr/bin/env node
import { JSDOM, VirtualConsole } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { buildTestBrowserBundle } from '../web/scripts/test-browser-bundle.mjs';

const repository = path.dirname(path.dirname(fileURLToPath(import.meta.url)));
const donorStatic = path.join(repository, 'web', 'donors', 'ai-assistant-production', 'static');
const fragment = fs.readFileSync(path.join(repository, 'web', 'dist', 'aiassistant', 'detail.html'), 'utf8').replaceAll('__PLAN_ID__', '7');
const bundle = await buildTestBrowserBundle(path.join(repository, 'web', 'v3', 'aiAssistantAdapter.ts'));
const donorScripts = new Set(['group_chat_picker.js', 'material_picker.js', 'send_content_composer.js', 'send_content_readonly_detail.js', 'cloud_plan_review.js']);

const requests = [];
const browserErrors = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', (error) => {
  const message = String(error?.message || error);
  if (!message.includes('navigation to another Document')) browserErrors.push(message);
});
const response = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, text: async () => JSON.stringify(body), json: async () => body });
const now = '2026-09-04T02:03:04Z';
let planVersion = 1;
let planState = 'pending_review';
let recipientVersion = 1;
let recipientReview = 'pending_review';
let executionState = 'not_accepted';
let contentBlocks = [
  { kind: 'text', text: '真实审阅话术' },
  { kind: 'image', material_kind: 'image', material_id: 101 },
  { kind: 'mini_program', material_kind: 'miniprogram', material_id: 202 },
  { kind: 'attachment', material_kind: 'attachment', material_id: 303 },
  { kind: 'link', material_kind: 'group_invite', material_id: 404 },
];
const plan = () => ({ id: 7, name: '九月复购计划', source_kind: 'automation', source_digest: `sha256:${'a'.repeat(64)}`, state: planState, version: planVersion, target_count: 1, pending_count: recipientReview === 'pending_review' ? 1 : 0, approved_count: recipientReview === 'approved' ? 1 : 0, rejected_count: 0, ineligible_count: 0, needs_attention_count: 0, created_by: 9, created_at: now, updated_at: now });
const recipient = () => ({ id: 77, plan_id: 7, customer_id: 42, customer_name: '安全客户名', oneid_label: 'OneID #42', staff_id: 9, staff_display_name: '运营同学', review_state: recipientReview, execution_state: executionState, version: recipientVersion, content_version_id: 88, updated_at: now });

const dom = new JSDOM(`<!doctype html><html><body>${fragment}<script>${bundle}</script></body></html>`, {
  url: 'https://test.invalid/admin/cloud-orchestrator/plans/7',
  runScripts: 'dangerously',
  pretendToBeVisual: true,
  virtualConsole,
  beforeParse(window) {
	const append = window.Element.prototype.append;
	window.Element.prototype.append = function (...nodes) {
		for (const node of nodes) {
			if (node?.tagName === 'SCRIPT' && node.src) {
				const name = path.basename(new URL(node.src).pathname);
				if (donorScripts.has(name)) {
					window.eval(fs.readFileSync(path.join(donorStatic, name), 'utf8'));
					Promise.resolve().then(() => node.onload?.());
					continue;
				}
			}
			append.call(this, node);
		}
	};
    window.confirm = () => true;
    window.document.cookie = `aicrm_csrf=${'c'.repeat(43)}`;
    Object.defineProperty(window.crypto, 'randomUUID', { value: () => `00000000-0000-4000-8000-${String(requests.length + 1).padStart(12, '0')}` });
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = init.method || 'GET';
      const item = { path: url.pathname + url.search, method, headers: Object.fromEntries(new Headers(init.headers || {}).entries()), body: init.body ? JSON.parse(String(init.body)) : undefined };
      requests.push(item);
      if (method === 'GET' && url.pathname === '/api/admin/ai-assistant/plans/7') return response({ ok: true, plan: plan(), replayed: false, dispatch_ready: true });
      if (method === 'GET' && url.pathname === '/api/admin/ai-assistant/plans/7/recipients') return response({ ok: true, items: [recipient()], next_cursor: '' });
      if (method === 'GET' && url.pathname === '/api/admin/ai-assistant/plans/7/recipients/77') return response({ ok: true, recipient: recipient(), content: { id: 88, recipient_id: 77, version: 1, digest: `sha256:${'b'.repeat(64)}`, blocks: contentBlocks, created_at: now } });
      if (method === 'POST' && url.pathname === '/api/admin/send-content/preview') return response({ ok: true, preview: { materials: [
        { type: 'image', library_id: 101, title: '图片 101' }, { type: 'miniprogram', library_id: 202, title: '小程序 202' },
        { type: 'attachment', library_id: 303, title: 'PDF 303' }, { type: 'group_invite', library_id: 404, title: '群邀请 404' },
      ] } });
      if (method === 'POST' && url.pathname === '/api/admin/send-content/validate') return response({ ok: true, content_package: item.body.content_package });
      if (method === 'PATCH' && url.pathname === '/api/admin/ai-assistant/plans/7/recipients/77/content') {
        const pkg = item.body;
        if (!Array.isArray(pkg.blocks) || !pkg.blocks.some((block) => block.kind === 'attachment' && block.material_id === 303)) return response({ ok: false, error: 'attachment_not_preserved' }, 409);
        contentBlocks = pkg.blocks;
        recipientVersion++;
        return response({ ok: true, id: 89, recipient_id: 77, version: 2, digest: `sha256:${'c'.repeat(64)}`, blocks: contentBlocks, created_at: now });
      }
      if (method === 'POST' && url.pathname === '/api/admin/ai-assistant/plans/7/recipients/77/review') {
        if (item.body.expected_version !== recipientVersion || item.body.decision !== 'approved') return response({ ok: false, error: 'version_or_idempotency_conflict' }, 409);
        recipientReview = 'approved'; recipientVersion++; planVersion++;
        return response({ ok: true, recipient: recipient(), content: { id: 88, recipient_id: 77, version: 1, digest: `sha256:${'b'.repeat(64)}`, blocks: [{ kind: 'text', text: '真实审阅话术' }], created_at: now } });
      }
      if (method === 'POST' && url.pathname === '/api/admin/ai-assistant/plans/7/preview-approval') return response({ ok: true, plan_id: 7, plan_version: planVersion, eligible_count: 1, preview_digest: `sha256:${'d'.repeat(64)}` });
      if (method === 'POST' && url.pathname === '/api/admin/ai-assistant/plans/7/approve') {
        if (item.body.expected_version !== planVersion || item.body.preview_digest !== `sha256:${'d'.repeat(64)}`) return response({ ok: false, error: 'version_or_idempotency_conflict' }, 409);
        planState = 'dispatching'; executionState = 'queued'; planVersion++;
        return response({ ok: true, plan: plan(), replayed: false, dispatch_ready: true });
      }
      return response({ ok: false, error: `unexpected ${method} ${url.pathname}` }, 500);
    };
  },
});

try {
  await new Promise((resolve) => setTimeout(resolve, 700));
  const { document } = dom.window;
  if (!document.body.textContent.includes('九月复购计划') || !document.body.textContent.includes('安全客户名') || !document.body.textContent.includes('OneID #42')) throw new Error('detail page did not render plan and OneID-safe recipient data');
  if (document.body.textContent.includes('external-secret-id')) throw new Error('raw external identity leaked into the page');
  document.querySelector('[data-open-recipient]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 150));
  if (!document.querySelector('[data-recipient-drawer]')?.classList.contains('is-open') || !document.body.textContent.includes('真实审阅话术') || !document.body.textContent.includes('附件 1') || !document.body.textContent.includes('附件 #303')) throw new Error('recipient drawer did not show every frozen material');
  document.querySelector('[data-edit-task]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 100));
  if (!document.body.textContent.includes('PDF/附件 303') || !document.querySelector('[data-composer-confirm]')) throw new Error('composer did not round-trip the frozen PDF attachment');
  document.querySelector('[data-composer-confirm]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 150));
  const saved = requests.find((item) => item.method === 'PATCH' && item.path === '/api/admin/ai-assistant/plans/7/recipients/77/content');
  if (!saved?.body?.blocks?.some((block) => block.kind === 'attachment' && block.material_id === 303)) throw new Error(`attachment was lost while saving composer: ${JSON.stringify(saved)}`);
  document.querySelector('[data-drawer-approve]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 150));
  if (recipientReview !== 'approved' || executionState !== 'not_accepted') throw new Error('individual approval created or implied an external effect');
  document.querySelector('[data-plan-approve]')?.click();
  await new Promise((resolve) => setTimeout(resolve, 200));
  if (planState !== 'dispatching' || executionState !== 'queued') throw new Error('whole-plan approval did not reach the sole dispatch gate');
  const writes = requests.filter((item) => item.method !== 'GET');
  if (writes.length < 3 || writes.some((item) => !item.headers['idempotency-key'] || !item.headers['x-csrf-token'])) throw new Error(`write security headers missing: ${JSON.stringify(writes)}`);
  if (browserErrors.length) throw new Error(`browser errors: ${JSON.stringify(browserErrors)}`);
  console.log('AI Assistant frozen two-level review and dispatch Journey: PASS');
} finally {
  dom.window.close();
}
