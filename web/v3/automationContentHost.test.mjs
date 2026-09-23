import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { writeFile } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { JSDOM } from 'jsdom';

const dom = new JSDOM(`<!doctype html><body data-page="agentEdit"><main id="stage"><form data-agent-form><textarea id="agentRolePrompt">保留角色 Prompt</textarea><textarea id="agentTaskPrompt">保留任务 Prompt</textarea><section style="display:grid;grid-template-columns:226px minmax(0,1fr)"><h3>固定素材</h3><div data-agent-materials-readonly><p data-stale>旧的工程提示</p><label data-stale>固定文本<textarea name="content_text" disabled>旧内容</textarea></label></div></section></form></main></body>`, { url: 'https://test.invalid/admin/agentEdit.html?id=7' });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Element: dom.window.Element, HTMLElement: dom.window.HTMLElement,
  HTMLButtonElement: dom.window.HTMLButtonElement, Event: dom.window.Event, KeyboardEvent: dom.window.KeyboardEvent,
  MutationObserver: dom.window.MutationObserver, DOMException: dom.window.DOMException,
  location: dom.window.location,
});
// The released byte-frozen picker remains the public bridge. The V3 adapter
// wraps it only when this page actually opens a Media selection session.
dom.window.AICRMMaterialPicker = { open() { throw new Error('the V3 adapter must intercept fixed-content media selection'); } };
const flush = async () => { await new Promise((resolve) => setTimeout(resolve, 0)); await new Promise((resolve) => setTimeout(resolve, 0)); };
const waitFor = async (predicate, message, timeout = 1_000) => {
  const deadline = Date.now() + timeout;
  while (!predicate() && Date.now() < deadline) await flush();
  assert.ok(predicate(), `${message}: ${document.body.textContent}`);
};
let contentText = '初始固定话术';
let draftVersion = 1;
let putCount = 0;
let putRequests = 0;
const acceptedSaveKeys = new Set();
const saveKeys = [];
let loseAcceptedResponse = true;
let holdSave = false;
let releaseHeldSave;
let readbackFailure = false;
let readbackHang = false;
let retryRead = false;
const agent = (patch = {}) => ({
  id: 7, automation_type: 'fixed_script', status: 'paused', draft_version: draftVersion, published_version: 1,
  has_unpublished_changes: draftVersion !== 1,
  fixed_content_package: { content_text: contentText, image_library_ids: [11], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [] },
  draft_role_prompt: '保留角色 Prompt', draft_task_prompt: '保留任务 Prompt', legacy_configuration: { keep: 'legacy' }, ...patch,
});
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), dom.window.location.href);
  if (url.pathname === '/api/admin/automation-agents/7' && (!init.method || init.method === 'GET')) {
    if (readbackHang && !retryRead) return new Promise((_resolve, reject) => init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true }));
    if (readbackFailure && !retryRead) return new Response(JSON.stringify({ code: 'unavailable' }), { status: 503, headers: { 'Content-Type': 'application/json' } });
    return new Response(JSON.stringify({ ok: true, agent: agent() }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.pathname === '/api/admin/image-library/11') {
    return new Response(JSON.stringify({ image: { id: 11, name: '欢迎封面', enabled: true, thumb_320_url: '/api/admin/image-library/11/variants/thumb_320' } }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.pathname === '/api/admin/image-library/12') {
    return new Response(JSON.stringify({ image: { id: 12, name: '第二张图片', enabled: true, thumb_320_url: '/api/admin/image-library/12/variants/thumb_320' } }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.pathname === '/api/admin/image-library' && (!init.method || init.method === 'GET')) {
    return new Response(JSON.stringify({ items: [
      { id: 11, name: '欢迎封面', enabled: true, thumb_320_url: '/api/admin/image-library/11/variants/thumb_320' },
      { id: 12, name: '第二张图片', enabled: true, thumb_320_url: '/api/admin/image-library/12/variants/thumb_320' },
    ], has_more: false }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  if (url.pathname === '/api/admin/automation-agents/7/fixed-content' && init.method === 'PUT') {
    putRequests += 1;
    const payload = JSON.parse(String(init.body));
    const idempotencyKey = init.headers.get ? init.headers.get('Idempotency-Key') : (init.headers['Idempotency-Key'] || init.headers['idempotency-key']);
    assert.match(String(idempotencyKey), /^automation-fixed-content-/, 'fixed-content save uses one explicit existing-command idempotency key');
    assert.equal(payload.content_package.content_text, '更新后的固定话术');
    saveKeys.push(String(idempotencyKey));
    if (!acceptedSaveKeys.has(String(idempotencyKey))) {
      acceptedSaveKeys.add(String(idempotencyKey));
      putCount += 1;
      contentText = payload.content_package.content_text;
      draftVersion = 2;
    }
    if (loseAcceptedResponse) {
      loseAcceptedResponse = false;
      throw new TypeError('accepted response lost');
    }
    if (holdSave) await new Promise((resolve) => { releaseHeldSave = resolve; });
    readbackFailure = true;
    return new Response(JSON.stringify({ ok: true, agent: agent() }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  }
  throw new Error(`unexpected request ${init.method || 'GET'} ${url.pathname}`);
};

const bundle = await build({
  stdin: { contents: "import './web/v3/automationContentHost';", resolveDir: process.cwd(), sourcefile: 'automation-content-host-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const bundlePath = path.join('/tmp', `aicrm-automation-content-host-${process.pid}.mjs`);
await writeFile(bundlePath, bundle.outputFiles[0].text);
await import(pathToFileURL(bundlePath).href);
await flush();
const content = document.querySelector('[data-agent-materials-readonly]');
assert.ok(content instanceof HTMLElement, 'the verified frozen runtime material seam exists');
assert.ok(content.querySelector('[data-v3-automation-fixed-content]'), 'V3 Host attaches to the donor runtime content node');
assert.equal(content.closest('[data-v3-automation-fixed-content-layout]')?.tagName, 'SECTION', 'Host scopes the verified frozen Agent layout for its mobile projection without rewriting donor source');
assert.equal(content.querySelector('[data-stale]').hidden, true, 'old fixed-content engineering copy is hidden without editing donor source');
assert.equal(document.querySelector('#agentRolePrompt').value, '保留角色 Prompt', 'the donor ordinary Prompt control remains outside the fixed-content Host');
assert.equal(document.querySelector('#agentTaskPrompt').value, '保留任务 Prompt', 'the donor ordinary Prompt control remains outside the fixed-content Host');
assert.match(content.textContent, /草稿版本 1 \/ 已发布版本 1/);
assert.match(content.textContent, /初始固定话术/);
content.querySelector('[data-v3-automation-edit-fixed-content]').click();
await flush();
const composer = document.querySelector('[data-v3-content-composer]');
assert.ok(composer, 'paused fixed_script opens the shared composer');
assert.match(composer.textContent, /确认仅保存固定话术草稿，不会发布、启用或执行自动化/);
assert.equal(composer.querySelector('[data-v3-composer-confirm]').textContent, '保存草稿', 'fixed-script confirmation uses its actual persistence command label');
assert.match(composer.textContent, /确认后将保存固定话术草稿/);
const textarea = composer.querySelector('[data-v3-composer-text]');
const initialTextarea = textarea;
textarea.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true, data: '中' }));
textarea.value = '更新后的固定话术中';
textarea.dispatchEvent(new Event('input', { bubbles: true }));
assert.equal(composer.querySelector('[data-v3-composer-text]'), initialTextarea, 'IME input retains the real textarea node');
textarea.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true, data: '中' }));
textarea.value = '{{customer_name}}';
textarea.dispatchEvent(new Event('input', { bubbles: true }));
assert.equal(composer.querySelector('[data-v3-composer-confirm]').disabled, true, 'fixed_script blocks unsupported variable delimiters before its command boundary');
assert.match(composer.textContent, /不支持变量占位符/);
textarea.value = '更新后的固定话术';
textarea.dispatchEvent(new Event('input', { bubbles: true }));
composer.querySelector('[data-v3-composer-confirm]').click();
await waitFor(() => putCount === 1, 'fixed-content PUT completes');
assert.equal(putCount, 1, 'one confirmation issues one fixed-content PUT');
await waitFor(() => /保存结果暂未确认，草稿已保留/.test(composer.textContent), 'lost response keeps the local draft with an unknown-result message');
composer.querySelector('[data-v3-composer-confirm]').click();
await waitFor(() => putRequests === 2, 'same fixed-content package retries after a lost response');
assert.equal(putCount, 1, 'same-key retry does not accept a second Owner write');
assert.equal(saveKeys[0], saveKeys[1], 'same fixed-content package retries with the original idempotency key');
await waitFor(() => document.querySelector('[data-v3-content-composer]') === null, 'accepted fixed-content save closes the local composer draft');
assert.equal(document.querySelector('[data-v3-content-composer]'), null, 'accepted fixed-content save closes the local composer draft');
assert.match(content.textContent, /固定话术已保存，暂时无法刷新当前展示/);
assert.equal(content.querySelector('[data-v3-automation-edit-fixed-content]'), null, 'readback failure offers only a read retry, not another save');
retryRead = true;
content.querySelector('[data-v3-automation-retry-read]').click();
await flush();
assert.equal(putCount, 1, 'retrying a failed GET never submits a second PUT');
assert.match(content.textContent, /草稿版本 2 \/ 已发布版本 1/);
assert.match(content.textContent, /更新后的固定话术/);

readbackHang = true;
retryRead = false;
content.querySelector('[data-v3-automation-edit-fixed-content]').click();
await waitFor(() => document.querySelector('[data-v3-content-composer]'), 'fixed-content composer opens before its bounded readback test');
const hangingReadbackComposer = document.querySelector('[data-v3-content-composer]');
holdSave = true;
hangingReadbackComposer.querySelector('[data-v3-composer-confirm]').click();
hangingReadbackComposer.querySelector('[data-v3-composer-confirm]').click();
await waitFor(() => putCount === 2, 'second accepted fixed-content PUT completes exactly once');
assert.equal(putRequests, 3, 'repeated confirmation while the fixed-content request is pending sends one PUT');
holdSave = false;
releaseHeldSave();
await waitFor(() => document.querySelector('[data-v3-content-composer]') === null, 'accepted PUT closes even while its following Agent GET is still pending');
await waitFor(() => /固定话术已保存，暂时无法刷新当前展示/.test(content.textContent), 'bounded Agent readback exposes a read-only retry after its deadline', 4_000);
assert.equal(putCount, 2, 'a timed-out readback does not transform the accepted write into another PUT');
retryRead = true;
content.querySelector('[data-v3-automation-retry-read]').click();
await waitFor(() => /草稿版本 2 \/ 已发布版本 1/.test(content.textContent), 'retry after a timed-out GET reads only the accepted server state');
assert.equal(putCount, 2, 'retrying a timed-out GET never submits a third PUT');
readbackHang = false;

content.querySelector('[data-v3-automation-edit-fixed-content]').click();
await waitFor(() => document.querySelector('[data-v3-content-composer]'), 'reopened fixed-content composer');
const materialComposer = document.querySelector('[data-v3-content-composer]');
materialComposer.querySelector('[data-v3-composer-add="image"]').click();
await waitFor(() => document.querySelector('.aicrm-material-picker-mask'), 'V3 material picker opens through the real frozen public bridge');
await waitFor(() => document.querySelectorAll('[data-v3-material-key]').length === 2, 'authorised image directory is loaded');
const picker = document.querySelector('.aicrm-material-picker-mask');
picker.querySelectorAll('[data-v3-material-key]')[1].click();
picker.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => !document.querySelector('.aicrm-material-picker-mask'), 'adding material returns to the fixed-content local draft');
assert.match(materialComposer.textContent, /第二张图片/, 'caller atomically receives the selected record before any fixed-content save');

materialComposer.querySelector('[data-v3-composer-add="image"]').click();
await waitFor(() => document.querySelector('.aicrm-material-picker-mask'), 'same editor reopens the V3 picker');
await waitFor(() => document.querySelectorAll('[data-v3-picker-selected] [data-v3-material-remove]').length === 2, 'reopened picker receives complete selected records for local removal');
await waitFor(() => document.querySelectorAll('[data-v3-material-key]').length === 2, 'reopened picker finishes the scoped directory read before applying removal');
document.querySelector('[data-v3-picker-selected] [data-v3-material-remove]').click();
await waitFor(() => document.querySelectorAll('[data-v3-picker-selected] [data-v3-material-remove]').length === 1, 'selected material removal updates the temporary picker state');
document.querySelector('[data-v3-picker-confirm]').click();
await waitFor(() => !document.querySelector('.aicrm-material-picker-mask'), 'explicit removal returns to the caller draft');
assert.equal(materialComposer.querySelectorAll('[data-v3-composer-records] [data-v3-composer-remove]').length, 1, 'material removal stays in the caller draft until its own fixed-content confirmation');
materialComposer.querySelector('[data-v3-composer-cancel]').click();
await waitFor(() => !document.querySelector('[data-v3-content-composer]'), 'cancel discards the unsaved material draft');
assert.equal(putCount, 2, 'selection and cancellation do not persist or send content');

const replacement = document.createElement('section');
replacement.dataset.agentMaterialsReadonly = '';
replacement.innerHTML = '<p data-stale>旧内容</p>';
content.replaceWith(replacement);
const activeAgent = agent({ id: 7, status: 'active' });
const previousFetch = globalThis.fetch;
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), dom.window.location.href);
  if (url.pathname === '/api/admin/automation-agents/7') return new Response(JSON.stringify({ ok: true, agent: activeAgent }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  if (url.pathname === '/api/admin/image-library/11') return new Response(JSON.stringify({ image: { id: 11, name: '欢迎封面', enabled: true } }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  return previousFetch(input, init);
};
await flush();
assert.match(replacement.textContent, /当前 Agent 已启用，请先暂停，再修改固定话术/);
assert.equal(replacement.querySelector('[data-v3-automation-edit-fixed-content]'), null, 'active Agent never exposes a bypass edit action');
assert.equal(document.querySelector('#agentRolePrompt').value, '保留角色 Prompt', 'Host replacement continues to leave ordinary Prompt fields untouched');

const archived = document.createElement('section');
archived.dataset.agentMaterialsReadonly = '';
archived.innerHTML = '<p data-stale>旧内容</p>';
replacement.replaceWith(archived);
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), dom.window.location.href);
  if (url.pathname === '/api/admin/automation-agents/7') return new Response(JSON.stringify({ ok: true, agent: agent({ id: 7, status: 'archived' }) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  if (url.pathname === '/api/admin/image-library/11') return new Response(JSON.stringify({ image: { id: 11, name: '欢迎封面', enabled: true } }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  return previousFetch(input, init);
};
await waitFor(() => /该 Agent 已归档，固定话术不可编辑。/.test(archived.textContent), 'archived Agent explains its unavailable state without reusing the active message');
assert.equal(archived.querySelector('[data-v3-automation-edit-fixed-content]'), null, 'archived Agent never exposes a bypass edit action');

const malformed = document.createElement('section');
malformed.dataset.agentMaterialsReadonly = '';
malformed.innerHTML = '<p data-stale>旧内容</p>';
archived.replaceWith(malformed);
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), dom.window.location.href);
  if (url.pathname === '/api/admin/automation-agents/7') return new Response(JSON.stringify({ ok: true, agent: agent({ status: 'invalid-status' }) }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  return previousFetch(input, init);
};
await waitFor(() => /返回格式无效/.test(malformed.textContent), 'malformed Agent DTO is fail-closed instead of becoming editable paused state');
assert.equal(malformed.querySelector('[data-v3-automation-edit-fixed-content]'), null, 'malformed Agent DTO has no fixed-content edit action');

const slow = document.createElement('section');
slow.dataset.agentMaterialsReadonly = '';
slow.innerHTML = '<p data-stale>旧内容</p>';
malformed.replaceWith(slow);
const slowAgent = agent({ fixed_content_package: { content_text: '历史引用保留', image_library_ids: [13], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [] } });
globalThis.fetch = async (input, init = {}) => {
  const url = new URL(String(input), dom.window.location.href);
  if (url.pathname === '/api/admin/automation-agents/7') return new Response(JSON.stringify({ ok: true, agent: slowAgent }), { status: 200, headers: { 'Content-Type': 'application/json' } });
  if (url.pathname === '/api/admin/image-library/13') return new Promise((_resolve, reject) => init.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true }));
  return previousFetch(input, init);
};
await waitFor(() => /素材详情读取超时/.test(slow.textContent), 'one hanging metadata request becomes an explicit retained state', 4_000);
assert.ok(slow.querySelector('[data-v3-automation-edit-fixed-content]'), 'metadata timeout still presents the bounded read-only content and paused edit path');
dom.window.close();
console.log('automation content host: fixed-script save/readback, scoped Media selection, IME, DTO fail-closed, bounded metadata, and donor-form boundary PASS');
