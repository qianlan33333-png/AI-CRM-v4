import assert from 'node:assert/strict';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
const root=path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const bundle=await buildTestBrowserBundle(path.join(root,'web/v3/standardComponentsHost.ts'));
for (const pathname of ['/admin/channels/17/edit','/admin/customers/9']) {
 const calls=[];
 const dom=new JSDOM('<!doctype html><body></body>',{url:'https://test.invalid'+pathname,runScripts:'dangerously',beforeParse(w){
  w.Request=Request;w.Response=Response;w.Headers=Headers;
  w.fetch=async (input,init)=>{calls.push({input,init});return new Response('{}',{status:200});};
 }});
 try {
  dom.window.document.cookie='aicrm_admin_csrf=test-csrf';
  dom.window.eval(bundle);
  const unscoped={method:'POST',headers:{Accept:'application/json'},credentials:'same-origin'};
  await dom.window.fetch('/api/admin/common/operation-members/sync',unscoped);
  assert.equal(calls.length,1);
  assert.equal(calls[0].init,unscoped,'the loader must not assign an unrelated caller the group_ops scope');
  assert.equal(calls[0].init.body,undefined,'the loader must not manufacture a refresh body for an unscoped caller');
  assert.equal(calls[0].init.headers['X-CSRF-Token'],undefined,'the loader must not add a cross-page CSRF envelope');
  const explicit={method:'POST',body:'{"scope":"custom"}',headers:{'Idempotency-Key':'existing'}};
  await dom.window.fetch('/api/admin/common/operation-members/sync',explicit);
  assert.equal(calls[1].init,explicit,'explicit V3 envelopes must not be changed');
  const foreign={method:'POST',headers:{Accept:'application/json'}};
  await dom.window.fetch('https://other.invalid/api/admin/common/operation-members/sync',foreign);
  assert.equal(calls[2].init,foreign,'never attach CSRF to another origin');
 } finally {dom.window.close();}
}
console.log('standard component loader leaves every staff refresh scope to its caller: PASS');

const fs = await import('node:fs/promises');
const picker = await fs.readFile(path.join(root,'internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js'),'utf8');

// The shared picker accepts an explicit selection contract. A multi-select
// limit must stay a finite, positive integer and the owner context is always
// single-select, regardless of an accidental caller-supplied limit.
{
 const dom=new JSDOM('<!doctype html><body></body>',{url:'https://test.invalid/admin/channels/17/edit',runScripts:'dangerously',beforeParse(w){
  w.Request=Request;w.Response=Response;w.Headers=Headers;
  w.AdminApi={responseErrorMessage:(_r,_d,f)=>f,errorMessage:(e,f)=>e?.message||f};
  w.fetch=async()=>new Response(JSON.stringify({items:[{staff_id:12,user_id:'alice',display_name:'Alice'}]}),{status:200});
 }});
 try {
  dom.window.eval(picker);
  for (const invalidMax of [Infinity, 1.5, 0, -1]) {
   await dom.window.OperationMemberPicker.open({context:'channel_assignees',selection:{mode:'multiple',max:invalidMax}});
   assert.match(dom.window.document.querySelector('[data-operation-member-description]').textContent,/本次最多选择 5 人/);
   assert.equal(dom.window.document.querySelectorAll('input[type="checkbox"]').length,1);
  }
  await dom.window.OperationMemberPicker.open({context:'group_ops_owner',selection:{mode:'single',max:8}});
  assert.equal(dom.window.document.querySelector('[data-operation-member-title]').textContent,'选择负责人');
  assert.match(dom.window.document.querySelector('[data-operation-member-description]').textContent,/选择一位负责人/);
  assert.doesNotMatch(dom.window.document.querySelector('[data-operation-member-description]').textContent,/最多选择/);
  assert.equal(dom.window.document.querySelectorAll('input[type="checkbox"]').length,0);
  assert.equal(dom.window.document.querySelector('[data-operation-member-confirm]').textContent,'确认负责人');
 } finally {dom.window.close();}
}
console.log('frozen staff selector selection contract: PASS');

const pause = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
{
  const queries = [];
  const dom = new JSDOM('<!doctype html><body><div data-customer-directory-root></div></body>', { url: 'https://test.invalid/admin/channels/17/edit', runScripts: 'dangerously', pretendToBeVisual: true, beforeParse(window) {
    window.Request = Request; window.Response = Response; window.Headers = Headers;
    window.AdminApi = { responseErrorMessage: (_response, _body, fallback) => fallback, errorMessage: (error, fallback) => error?.message || fallback };
    window.fetch = async (input) => {
      const url = new URL(typeof input === 'string' ? input : input.url, window.location.href);
      queries.push(url.searchParams.get('q'));
      return new Response(JSON.stringify({ items: [{ staff_id: 12, user_id: 'alice', display_name: 'Alice' }] }), { status: 200 });
    };
  }});
  try {
    dom.window.eval(bundle);
    dom.window.eval(picker);
    await dom.window.OperationMemberPicker.open({ scope: 'channel_code' });
    await pause(30);
    const search = dom.window.document.querySelector('[data-operation-member-search]');
    search.focus(); search.value = '草稿';
    search.dispatchEvent(new dom.window.Event('input', { bubbles: true, cancelable: true }));
    await pause(300);
    assert.deepEqual(queries, [null], 'typing a frozen staff search only changes its local draft');
    search.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
    search.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
    const candidate = new dom.window.KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter', code: 'Enter' });
    Object.defineProperty(candidate, 'keyCode', { value: 229 });
    search.dispatchEvent(candidate);
    assert.equal(candidate.defaultPrevented, false, 'IME candidate Enter remains owned by the browser');
    await pause(30);
    search.dispatchEvent(new dom.window.KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter', code: 'Enter' }));
    await pause(30);
    assert.deepEqual(queries, [null, '草稿'], 'ordinary Enter forwards exactly the current committed frozen picker query');
  } finally { dom.window.close(); }
}
console.log('frozen staff selector uses the shared committed-query interaction: PASS');

const tagPickerArtifact = path.join(root, 'web/dist/assets/standard-components/wecom_tag_picker.js');
let tagPicker;
try {
  tagPicker = await fs.readFile(tagPickerArtifact, 'utf8');
} catch (error) {
  throw new Error(`missing manifest-verified standard tag-picker release artifact at ${tagPickerArtifact}; run npm run build and node scripts/build-v3-host-adapters.mjs before this suite`, { cause: error });
}

const componentSources = {
 operationMembers: '/assets/standard-components/operation_member_picker.js?v=1b12b405d7377948',
 groupChats: '/assets/standard-components/group_chat_picker.js',
 materials: '/assets/standard-components/material_picker.js',
 sendContent: '/assets/standard-components/send_content_composer.js',
 tags: '/assets/standard-components/wecom_tag_picker.js',
};
const componentGlobals = {
 [componentSources.operationMembers]: 'OperationMemberPicker',
 [componentSources.groupChats]: 'AICRMGroupChatPicker',
 [componentSources.materials]: 'AICRMMaterialPicker',
 [componentSources.sendContent]: 'AICRMSendContentComposer',
 [componentSources.tags]: 'AICRMWeComTagPicker',
};
const settleComponents = () => new Promise((resolve) => setTimeout(resolve, 0));
function loadHost(body, onScript, prepare) {
 const dom = new JSDOM(`<!doctype html><body>${body}</body>`, { url: 'https://test.invalid/admin/customers', runScripts: 'dangerously' });
 dom.window.Request = Request; dom.window.Response = Response; dom.window.Headers = Headers;
 dom.window.fetch = async () => new Response('{}', { status: 200 });
 const sources = [];
 const append = dom.window.document.head.append.bind(dom.window.document.head);
 dom.window.document.head.append = (...nodes) => {
  append(...nodes);
  for (const node of nodes) {
   if (!(node instanceof dom.window.HTMLScriptElement)) continue;
   const source = node.dataset.aicrmStandardComponent;
   if (!source) continue;
   sources.push(source);
   onScript(node, source, dom.window);
  }
 };
 prepare?.(dom.window);
 dom.window.eval(bundle);
 return { dom, sources };
}
function initializeComponent(window, source) {
 if (source === componentSources.tags) {
  window.eval(tagPicker);
  return;
 }
 window[componentGlobals[source]] = source === componentSources.sendContent ? { open() {}, mount() {} } : { open() {} };
}

// Only the native customer-directory marker opts into the tag-only preload.
// Other mounted pages retain the historical ordered default preload.
{
 const customer = loadHost('<div data-customer-directory-root></div>', (script, source, window) => queueMicrotask(() => { initializeComponent(window, source); script.dispatchEvent(new window.Event('load')); }), (window) => { window.AICRMWeComTagPicker = null; });
 try {
  await settleComponents();
  assert.deepEqual(customer.sources, [componentSources.tags], 'customer auto-start must load only the tag picker');
  await customer.dom.window.AICRMStandardComponents.readyFor(['tags']);
 } finally { customer.dom.window.close(); }
 const defaultPage = loadHost('<div data-message-history-root></div>', (script, source, window) => queueMicrotask(() => { initializeComponent(window, source); script.dispatchEvent(new window.Event('load')); }));
 try {
  await settleComponents();
  assert.deepEqual(defaultPage.sources, Object.values(componentSources), 'non-customer pages must retain the default dependency order');
 } finally { defaultPage.dom.window.close(); }
}

// A tag script remains pending until its actual global exists. Every tags
// caller, including the customer auto-start, shares that one pending request.
{
 const pending = loadHost(`<div data-customer-directory-root></div><script src="${componentSources.tags}" data-aicrm-standard-component="${componentSources.tags}" data-aicrm-standard-component-state="pending"></script>`, () => {}, (window) => { window.AICRMWeComTagPicker = { open() {}, legacy: true }; });
 try {
  const first = pending.dom.window.AICRMStandardComponents.readyFor(['tags']);
  const second = pending.dom.window.AICRMStandardComponents.readyFor(['tags']);
  assert.equal(pending.dom.window.document.querySelectorAll('script[data-aicrm-standard-component]').length, 1, 'a pre-existing pending canonical script must not be duplicated');
  let completed = false;
  void first.then(() => { completed = true; });
  await Promise.resolve();
  assert.equal(completed, false, 'a pending script must not be reported ready');
  initializeComponent(pending.dom.window, componentSources.tags);
  pending.dom.window.document.querySelector('script[data-aicrm-standard-component]')?.dispatchEvent(new pending.dom.window.Event('load'));
  await Promise.all([first, second]);
 } finally { pending.dom.window.close(); }
}

// A full caller started beside tags must reuse the tags in-flight request; the
// full caller only adds the four missing capabilities in its normal order.
{
 const shared = loadHost('<div data-customer-directory-root></div>', (script, source, window) => queueMicrotask(() => { initializeComponent(window, source); script.dispatchEvent(new window.Event('load')); }));
 try {
  await shared.dom.window.AICRMStandardComponents.ready();
  assert.equal(shared.sources.filter((source) => source === componentSources.tags).length, 1, 'full and tags callers must share the tag request');
  assert.equal(shared.sources.length, 5, 'full caller must complete the remaining standard capabilities');
 } finally { shared.dom.window.close(); }
}

// A load event without the real picker global fails, removes the bad node, and
// remains harmless when it came from auto-start. A later explicit retry sees a
// real rejection/success path rather than a stale ready state.
{
 let attempts = 0;
 const retry = loadHost('<div data-customer-directory-root></div>', (script, source, window) => queueMicrotask(() => {
  attempts += 1;
  if (attempts === 2) initializeComponent(window, source);
  script.dispatchEvent(new window.Event('load'));
 }));
 try {
  await settleComponents();
  assert.equal(retry.dom.window.document.querySelectorAll('script[data-aicrm-standard-component]').length, 0, 'failed auto-start must remove the broken script node');
  await retry.dom.window.AICRMStandardComponents.readyFor(['tags']);
  assert.equal(attempts, 2, 'explicit retry must create a fresh tag request');
 } finally { retry.dom.window.close(); }
}
console.log('standard component capability loader: PASS');
