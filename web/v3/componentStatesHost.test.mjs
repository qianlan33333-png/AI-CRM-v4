import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><main id="root"></main>', { url: 'https://test.invalid' });
Object.assign(globalThis, {
  window: dom.window,
  document: dom.window.document,
  Element: dom.window.Element,
  HTMLElement: dom.window.HTMLElement,
  HTMLButtonElement: dom.window.HTMLButtonElement,
  HTMLInputElement: dom.window.HTMLInputElement,
  HTMLSelectElement: dom.window.HTMLSelectElement,
  HTMLTextAreaElement: dom.window.HTMLTextAreaElement,
  KeyboardEvent: dom.window.KeyboardEvent,
  Event: dom.window.Event,
  CompositionEvent: dom.window.CompositionEvent,
  DOMException: dom.window.DOMException,
  AbortController: dom.window.AbortController,
  getComputedStyle: dom.window.getComputedStyle.bind(dom.window),
});

const bundle = await build({
  stdin: { contents: "export { mountComponentStates } from './web/v3/componentStatesHost';", resolveDir: process.cwd(), sourcefile: 'component-states-host-test.ts' },
  bundle: true,
  format: 'esm',
  platform: 'node',
  write: false,
  target: 'es2020',
});
const { mountComponentStates } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const root = document.querySelector('#root');

mountComponentStates(root);
assert.equal(root.dataset.componentStatesReady, 'true');
for (const state of ['loading', 'empty', 'error', 'forbidden', 'readonly', 'invalid', 'ime']) {
  assert.ok(root.querySelector(`[data-state="${state}"]`), `state catalog lists ${state}`);
}

const mode = (name) => root.querySelector(`[data-component-states-mode="${name}"]`).click();
const groupTrigger = () => root.querySelector('[data-component-states-group-open]');
const materialTrigger = () => root.querySelector('[data-component-states-material-open]');
const formTrigger = () => root.querySelector('[data-component-states-form-open]');
const closeGroup = () => document.querySelector('[data-v3-selection-session="group"] [data-v3-group-cancel]').click();
const closeMaterial = () => document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-cancel]').click();

groupTrigger().focus();
groupTrigger().click();
await flush();
const readyGroup = document.querySelector('[data-v3-selection-session="group"]');
assert.ok(readyGroup, 'ready mode opens the real group session');
readyGroup.querySelector('[data-v3-group-key]').click();
readyGroup.querySelector('[data-v3-group-confirm]').click();
await flush();
await flush();
assert.equal(document.querySelector('[data-v3-selection-session="group"]'), null, 'confirmed group session closes');
assert.equal(document.activeElement, groupTrigger(), 'confirmed group session restores the original trigger');
assert.match(root.querySelector('[data-component-states-group-result]').textContent, /北区新品体验群/, 'confirmed selection updates only its summary node');

mode('error');
groupTrigger().focus();
groupTrigger().click();
await flush();
await flush();
let group = document.querySelector('[data-v3-selection-session="group"]');
assert.match(group.textContent, /群聊目录暂时不可用/, 'error mode comes from the live group loader');
group.querySelector('[data-v3-group-reload]').click();
await flush();
await flush();
assert.match(group.textContent, /北区新品体验群/, 'error mode refreshes in the same group session with its second local result');
assert.doesNotMatch(group.querySelector('[data-v3-group-status]').textContent, /暂时不可用/, 'successful retry clears the explicit error without reopening the dialog');
closeGroup();
assert.equal(document.activeElement, groupTrigger(), 'cancelled group session restores its trigger');

mode('loading');
groupTrigger().focus();
groupTrigger().click();
await flush();
group = document.querySelector('[data-v3-selection-session="group"]');
assert.match(group.textContent, /正在读取群目录/, 'loading mode leaves the real group loader pending');
closeGroup();
assert.equal(document.activeElement, groupTrigger(), 'cancelling the pending group loader restores focus');

mode('readonly');
groupTrigger().focus();
groupTrigger().click();
await flush();
group = document.querySelector('[data-v3-selection-session="group"]');
assert.match(group.textContent, /当前记录处于只读状态/, 'readonly mode preserves a local selected record');
assert.equal(group.querySelector('[data-v3-group-confirm]').disabled, true, 'readonly group session blocks confirmation');
closeGroup();

mode('empty');
materialTrigger().focus();
materialTrigger().click();
await flush();
await flush();
let material = document.querySelector('[data-v3-selection-session="material"]');
assert.match(material.textContent, /没有可选素材/, 'empty mode comes from the live material loader');
closeMaterial();
assert.equal(document.activeElement, materialTrigger(), 'cancelled material session restores its trigger');

mode('forbidden');
materialTrigger().focus();
materialTrigger().click();
await flush();
await flush();
material = document.querySelector('[data-v3-selection-session="material"]');
assert.match(material.textContent, /目录权限已收回/, '403 mode stays explicit in the material session');
assert.match(material.textContent, /秋日活动封面/, '403 mode retains the selected local draft for inspection');
assert.equal(material.querySelector('[data-v3-picker-confirm]').disabled, true, '403 material session blocks confirmation');
closeMaterial();

mode('invalid');
materialTrigger().focus();
materialTrigger().click();
await flush();
material = document.querySelector('[data-v3-selection-session="material"]');
assert.match(material.textContent, /待目录确认的失效初选/, 'invalid selected record remains visible without being declared usable');
assert.match(material.textContent, /当前不可用/, 'invalid selected record exposes its reason');
closeMaterial();

formTrigger().focus();
formTrigger().click();
await flush();
await flush();
const form = document.querySelector('[data-v3-selection-session="component-states"]');
const search = form.querySelector('[data-component-states-ime-input]');
assert.ok(form.querySelector('[data-component-states-form-select]'), 'form demo contains a real select');
assert.ok(form.querySelector('[data-component-states-form-textarea]'), 'form demo contains a real textarea');
assert.ok(form.querySelector('[data-component-states-form-editable]'), 'form demo contains a real contenteditable control');
search.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
search.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
const candidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
Object.defineProperty(candidateEnter, 'keyCode', { value: 229 });
search.dispatchEvent(candidateEnter);
assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate Enter remains native');
await flush();
const ordinaryEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
search.dispatchEvent(ordinaryEnter);
assert.equal(ordinaryEnter.defaultPrevented, true, 'ordinary Enter submits the local session loader');
await flush();
form.querySelector('[data-component-states-choice]').click();
form.querySelector('[data-component-states-confirm]').click();
assert.match(form.textContent, /已提交本地示例会话/, 'form confirmation commits only the local SelectionSession');
form.querySelector('[data-component-states-close]').click();
assert.equal(document.activeElement, formTrigger(), 'form cancel restores focus to the still-mounted trigger');


const waitFor = async (predicate, message) => {
  for (let attempt = 0; attempt < 30; attempt += 1) {
    const value = predicate();
    if (value) return value;
    await flush();
  }
  assert.fail(message);
};
const tagTrigger = () => root.querySelector('[data-component-states-tag-open]');
const staffTrigger = () => root.querySelector('[data-component-states-staff-open]');
const composerTrigger = () => root.querySelector('[data-component-states-composer-open]');
const composerReadonlyTrigger = () => root.querySelector('[data-component-states-composer-readonly]');
const tagMask = () => document.querySelector('[data-v3-selection-session="tag"]');
const staffMask = () => document.querySelector('[data-v3-selection-session="staff"]');
const composerMask = () => document.querySelector('[data-v3-content-composer]');
const closeTag = () => tagMask().querySelector('[data-v3-tag-cancel]').click();
const closeStaff = () => staffMask().querySelector('[data-v3-staff-cancel]').click();
const closeComposer = () => composerMask().querySelector('[data-v3-composer-cancel]').click();

mode('ready');
tagTrigger().focus();
tagTrigger().click();
let tag = await waitFor(() => tagMask()?.querySelector('[data-v3-tag-key]'), 'ready mode opens the real tag session');
const tagSearch = tagMask().querySelector('[data-v3-picker-search-input]');
tagSearch.value = '服务';
tagSearch.dispatchEvent(new Event('input', { bubbles: true }));
tagSearch.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
tagSearch.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
const tagCandidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
Object.defineProperty(tagCandidateEnter, 'keyCode', { value: 229 });
tagSearch.dispatchEvent(tagCandidateEnter);
assert.equal(tagCandidateEnter.defaultPrevented, false, 'tag IME candidate Enter remains native');
await flush();
const tagOrdinaryEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
tagSearch.dispatchEvent(tagOrdinaryEnter);
assert.equal(tagOrdinaryEnter.defaultPrevented, true, 'tag ordinary Enter submits only the local loader');
await waitFor(() => tagMask()?.textContent.includes('需要跟进'), 'tag Enter query shows the committed local result');
tagMask().querySelector('[data-v3-tag-key]').click();
tagMask().querySelector('[data-v3-tag-confirm]').click();
await waitFor(() => !tagMask(), 'tag local confirmation closes its session');
assert.equal(document.activeElement, tagTrigger(), 'tag confirmation restores its trigger focus');
assert.match(root.querySelector('[data-component-states-tag-result]').textContent, /需要跟进/, 'tag confirmation updates only the local summary');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('需要跟进'), 'tag reopen rehydrates the confirmed local selection');
closeTag();
assert.equal(document.activeElement, tagTrigger(), 'tag cancellation restores its trigger focus');

mode('error');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('标签目录暂时不可用'), 'tag error mode exposes its local loader failure');
tagMask().querySelector('[data-v3-tag-reload]').click();
await waitFor(() => tagMask()?.textContent.includes('秋日活动'), 'tag retry keeps the session and uses the next local result');
closeTag();
mode('loading');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('正在读取标签目录'), 'tag loading remains cancellable');
closeTag();
mode('empty');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('暂无匹配标签'), 'tag empty state is explicit');
closeTag();
mode('forbidden');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('标签目录权限已收回'), 'tag 403 locks the active local draft');
assert.match(tagMask().textContent, /秋日活动/, 'tag 403 retains the selected local record');
assert.equal(tagMask().querySelector('[data-v3-tag-confirm]').disabled, true, 'tag 403 blocks confirmation');
closeTag();
mode('readonly');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('标签选择当前为只读'), 'tag readonly state preserves its reason');
assert.equal(tagMask().querySelector('[data-v3-tag-confirm]').disabled, true, 'tag readonly blocks confirmation');
closeTag();
mode('invalid');
tagTrigger().click();
await waitFor(() => tagMask()?.textContent.includes('待目录确认的失效标签'), 'tag invalid initial remains visible');
assert.match(tagMask().textContent, /该标签已不在当前完整目录中/, 'tag invalid initial includes the complete-directory reason');
assert.equal(tagMask().querySelector('[data-v3-tag-confirm]').disabled, true, 'tag invalid initial blocks confirmation');
closeTag();

mode('ready');
staffTrigger().focus();
staffTrigger().click();
let staffDialog = await waitFor(() => staffMask()?.querySelector('[data-v3-staff-key]'), 'ready mode opens the real staff session');
const staffSearch = staffMask().querySelector('[data-v3-picker-search-input]');
staffSearch.value = '增长';
staffSearch.dispatchEvent(new Event('input', { bubbles: true }));
staffSearch.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true }));
staffSearch.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true }));
const staffCandidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
Object.defineProperty(staffCandidateEnter, 'keyCode', { value: 229 });
staffSearch.dispatchEvent(staffCandidateEnter);
assert.equal(staffCandidateEnter.defaultPrevented, false, 'staff IME candidate Enter remains native');
await flush();
const staffOrdinaryEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' });
staffSearch.dispatchEvent(staffOrdinaryEnter);
assert.equal(staffOrdinaryEnter.defaultPrevented, true, 'staff ordinary Enter submits only the local loader');
await waitFor(() => staffMask()?.querySelector('[data-v3-staff-list]')?.textContent.includes('增长客服') && !staffMask()?.querySelector('[data-v3-staff-list]')?.textContent.includes('北区客服') && !staffMask()?.querySelector('[data-v3-staff-confirm]')?.disabled, 'staff Enter query shows the committed local result after initial loading settles');
staffMask().querySelector('[data-v3-staff-key]').click();
await waitFor(() => !staffMask()?.querySelector('[data-v3-staff-confirm]')?.disabled, 'staff selection becomes confirmable after its current page settles');
staffMask().querySelector('[data-v3-staff-confirm]').click();
await waitFor(() => !staffMask(), 'staff local confirmation closes its session');
assert.equal(document.activeElement, staffTrigger(), 'staff confirmation restores its trigger focus');
assert.match(root.querySelector('[data-component-states-staff-result]').textContent, /增长客服/, 'staff confirmation updates only the local summary');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('增长客服'), 'staff reopen rehydrates the confirmed local selection');
closeStaff();
assert.equal(document.activeElement, staffTrigger(), 'staff cancellation restores its trigger focus');

mode('error');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('员工目录暂时不可用'), 'staff error mode exposes its local loader failure');
staffMask().querySelector('[data-v3-staff-reload]').click();
await waitFor(() => staffMask()?.textContent.includes('北区客服'), 'staff retry keeps the session and uses the next local result');
closeStaff();
mode('loading');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('正在读取员工目录'), 'staff loading remains cancellable');
closeStaff();
mode('empty');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('暂无匹配员工'), 'staff empty state is explicit');
closeStaff();
mode('forbidden');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('员工目录权限已收回'), 'staff 403 locks the active local draft');
assert.match(staffMask().textContent, /北区客服/, 'staff 403 retains the selected local record');
assert.equal(staffMask().querySelector('[data-v3-staff-confirm]').disabled, true, 'staff 403 blocks confirmation');
closeStaff();
mode('readonly');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('员工选择当前为只读'), 'staff readonly state preserves its reason');
assert.equal(staffMask().querySelector('[data-v3-staff-confirm]').disabled, true, 'staff readonly blocks confirmation');
closeStaff();
mode('invalid');
staffTrigger().click();
await waitFor(() => staffMask()?.textContent.includes('待目录确认的失效员工'), 'staff invalid initial remains visible');
assert.match(staffMask().textContent, /缺少可信企微 UserID/, 'staff invalid initial exposes the missing trusted mapping');
assert.equal(staffMask().querySelector('[data-v3-staff-confirm]').disabled, true, 'staff invalid initial blocks confirmation');
closeStaff();

mode('ready');
composerTrigger().focus();
composerTrigger().click();
let composerDialog = await waitFor(() => composerMask()?.querySelector('[data-v3-composer-text]'), 'ready mode opens the real content composer');
assert.match(composerMask().textContent, /秋日活动封面/, 'composer displays its current local material records');
const unknownVariableText = composerMask().querySelector('[data-v3-composer-text]');
unknownVariableText.value = '未授权变量 {{unknown_demo}}';
unknownVariableText.dispatchEvent(new Event('input', { bubbles: true }));
await flush();
assert.match(composerMask().textContent, /示例变量不在当前本地目录中/, 'composer parses an unknown token and reports the caller policy reason');
assert.equal(composerMask().querySelector('[data-v3-composer-confirm]').disabled, true, 'unknown local variable blocks confirmation');
unknownVariableText.value = '你好，{{customer_name}}，欢迎查看{{plan_name}}。';
unknownVariableText.dispatchEvent(new Event('input', { bubbles: true }));
await waitFor(() => !composerMask()?.querySelector('[data-v3-composer-confirm]')?.disabled, 'restoring caller-approved variables enables local confirmation');
composerMask().querySelector('[data-v3-composer-variable]').click();
assert.match(composerMask().querySelector('[data-v3-composer-text]').value, /\{\{customer_name\}\}/, 'composer inserts only its caller-provided local variable token');
composerMask().querySelector('[data-v3-composer-move="1:-1"]').click();
assert.equal(composerMask().querySelector('[data-v3-composer-records] strong').textContent, '附件：活动说明 PDF', 'composer order changes inside the temporary local draft');
composerMask().querySelector('[data-v3-composer-confirm]').click();
await waitFor(() => !composerMask(), 'composer local confirmation closes the editor');
assert.equal(document.activeElement, composerTrigger(), 'composer confirmation restores the trigger focus');
assert.match(root.querySelector('[data-component-states-composer-result]').textContent, /活动说明 PDF.*秋日活动封面/, 'composer confirmation preserves caller-selected material ordering in the local summary');
assert.match(root.querySelector('[data-component-states-composer-preview]').textContent, /不会保存或发送/, 'same-page readonly presentation states its zero business effect');
composerTrigger().click();
await waitFor(() => composerMask()?.querySelector('[data-v3-composer-text]'), 'composer reopen uses the most recently confirmed local result');
const composerText = composerMask().querySelector('[data-v3-composer-text]');
composerText.value = '取消的本地草稿';
composerText.dispatchEvent(new Event('input', { bubbles: true }));
closeComposer();
assert.equal(document.activeElement, composerTrigger(), 'composer cancellation restores the trigger focus');
assert.doesNotMatch(root.querySelector('[data-component-states-composer-result]').textContent, /取消的本地草稿/, 'composer cancellation does not change the local confirmed summary');
composerReadonlyTrigger().focus();
composerReadonlyTrigger().click();
await waitFor(() => document.querySelector('[data-v3-content-readonly]'), 'readonly content presentation opens through the shared renderer');
assert.equal(document.querySelector('[data-v3-content-readonly] [data-v3-composer-text]'), null, 'readonly content presentation has no editable text field');
assert.match(document.querySelector('[data-v3-content-readonly]').textContent, /未保存、未发送/, 'readonly content presentation retains its local-only notice');
document.querySelector('[data-v3-content-readonly-close]').click();
assert.equal(document.activeElement, composerReadonlyTrigger(), 'readonly content close restores its trigger focus');

mode('error');
composerTrigger().click();
await waitFor(() => composerMask()?.querySelector('[data-v3-composer-add="image"]'), 'composer remains editable before a local selector failure');
composerMask().querySelector('[data-v3-composer-add="image"]').click();
await waitFor(() => composerMask()?.textContent.includes('内容素材目录暂时不可用'), 'composer selection failure retains its editor draft');
assert.ok(composerMask().querySelector('[data-v3-composer-text]'), 'composer failure keeps text draft visible');
closeComposer();
mode('loading');
composerTrigger().click();
await waitFor(() => composerMask()?.querySelector('[data-v3-composer-add="image"]'), 'composer loading mode opens the editor');
composerMask().querySelector('[data-v3-composer-add="image"]').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]')?.textContent.includes('正在加载图片'), 'composer uses the real nested material loader for loading state');
document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-cancel]').click();
await waitFor(() => !document.querySelector('[data-v3-selection-session="material"]'), 'nested composer material cancellation closes only the selector');
assert.ok(composerMask(), 'nested material cancellation retains the composer draft');
closeComposer();
mode('empty');
composerTrigger().click();
await waitFor(() => composerMask()?.querySelector('[data-v3-composer-add="image"]'), 'composer empty mode opens the editor');
composerMask().querySelector('[data-v3-composer-add="image"]').click();
await waitFor(() => document.querySelector('[data-v3-selection-session="material"]')?.textContent.includes('没有可选素材'), 'composer empty mode uses the real local material selector');
document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-cancel]').click();
await waitFor(() => !document.querySelector('[data-v3-selection-session="material"]'), 'empty nested selector cancellation completes');
closeComposer();
mode('forbidden');
composerTrigger().click();
await waitFor(() => composerMask()?.querySelector('[data-v3-composer-add="image"]'), 'composer forbidden mode opens the editor');
composerMask().querySelector('[data-v3-composer-add="image"]').click();
await waitFor(() => composerMask()?.textContent.includes('没有本地素材目录权限'), 'composer permission loss stays explicit and preserves its editor');
assert.ok(composerMask().querySelector('[data-v3-composer-text]'), 'composer permission loss keeps text draft visible');
closeComposer();
mode('invalid');
composerTrigger().click();
await waitFor(() => composerMask()?.textContent.includes('待目录确认的失效素材'), 'composer invalid material remains visible');
assert.match(composerMask().textContent, /素材已不可用/, 'composer invalid material exposes its reason');
assert.equal(composerMask().querySelector('[data-v3-composer-confirm]').disabled, true, 'composer invalid material blocks confirmation');
closeComposer();
mode('readonly');
composerTrigger().focus();
composerTrigger().click();
await waitFor(() => document.querySelector('[data-v3-content-readonly]'), 'composer readonly mode uses the shared readonly renderer');
assert.equal(document.querySelector('[data-v3-content-readonly] [data-v3-composer-text]'), null, 'readonly mode cannot expose an editor');
document.querySelector('[data-v3-content-readonly-close]').click();
assert.equal(document.activeElement, composerTrigger(), 'readonly composer mode restores the trigger focus');

dom.window.close();
console.log('component state Host: real state loaders, local commit, IME, and focus restoration PASS');
