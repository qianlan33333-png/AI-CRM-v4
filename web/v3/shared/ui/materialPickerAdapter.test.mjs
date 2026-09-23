import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><button id="trigger">打开素材</button>', { url: 'https://test.invalid' });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Element: dom.window.Element, HTMLElement: dom.window.HTMLElement,
  HTMLInputElement: dom.window.HTMLInputElement, KeyboardEvent: dom.window.KeyboardEvent, Event: dom.window.Event,
  DOMException: dom.window.DOMException, AbortController: dom.window.AbortController,
});
const bundle = await build({
  stdin: { contents: "export { installMaterialPickerAdapter } from './web/v3/shared/ui/materialPickerAdapter';", resolveDir: process.cwd(), sourcefile: 'material-picker-adapter-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const calls = [];
const donorCalls = [];
let delayedMaterial403;
window.AICRMMaterialPicker = { open: (options) => donorCalls.push(options) };
mod.installMaterialPickerAdapter({
  source: 'radar-content', scope: 'radar-editor',
  accessLossMessage: (error) => error?.status === 403 ? '素材目录权限已失效；已选素材仍可查看，请取消后重新登录。' : undefined,
  async loadPage({ type, query, cursor, signal }) {
    calls.push({ type, query, cursor, aborted: signal.aborted });
    if (query === '故障') throw new Error('目录暂不可用');
    if (query === '权限') { const error = new Error('forbidden'); error.status = 403; throw error; }
    if (query === '旧权限') return new Promise((_resolve, reject) => { delayedMaterial403 = reject; });
    if (query === '新结果') return { items: [{ type, library_id: 22, title: '新结果素材', selectable: true }] };
    if (query === '异类') return { items: [{ type: 'attachment', library_id: 8, title: '错误类型' }] };
    if (cursor === 'next') return { items: [{ type, library_id: 3, title: '第三项', selectable: true }] };
    return { items: [
      { type, library_id: 1, title: '已失效素材', selectable: false, unavailable_reason: '素材已下架' },
      { type, library_id: 2, title: '可选素材', thumbnail_url: 'https://example.test/preview.png', selectable: true },
    ], nextCursor: 'next' };
  },
});

let committed;
let legacyAdded = [];
const trigger = document.getElementById('trigger');
trigger.focus();
window.AICRMMaterialPicker.open({ type: 'image', selectedIds: [1], limit: 2, onConfirm: (item) => legacyAdded.push(item.library_id), onCommit: (value) => { committed = value; } });
await flush();
assert.equal(calls.length, 1);
const mask = document.querySelector('[data-v3-selection-session="material"]');
assert.ok(mask, 'V3 dialog mounts instead of the frozen one');
assert.equal(mask.dataset.selectionSource, 'radar-content');
assert.equal(mask.dataset.selectionScope, 'radar-editor');
const search = mask.querySelector('[data-v3-picker-search-input]');
assert.equal(search.matches('[data-picker-search]'), false, 'V3 dialog has a separate query marker from the frozen picker');
let legacyPickerInputCalls = 0;
document.addEventListener('input', (event) => {
  const input = event.target instanceof dom.window.HTMLInputElement ? event.target : null;
  if (input?.matches('.aicrm-material-picker-mask [data-picker-search]')) legacyPickerInputCalls += 1;
}, true);
const callsBeforeCandidate = calls.length;
search.value = '中文候选';
search.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
search.dispatchEvent(new Event('input', { bubbles: true }));
search.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
const candidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter', code: 'Enter' });
Object.defineProperty(candidateEnter, 'keyCode', { value: 229 });
search.dispatchEvent(candidateEnter);
await flush();
assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate confirmation keeps its browser default');
assert.equal(calls.length, callsBeforeCandidate, 'composition completion and candidate Enter only maintain the native draft');
search.value = '草稿';
search.dispatchEvent(new Event('input', { bubbles: true }));
assert.equal(calls.length, callsBeforeCandidate, 'typing only maintains a native draft');
assert.equal(legacyPickerInputCalls, 0, 'a legacy picker capture selector cannot receive V3 draft input');
search.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' }));
await flush();
assert.deepEqual(calls.at(-1), { type: 'image', query: '草稿', cursor: undefined, aborted: false }, 'native Enter submits exactly the V3 draft');
assert.match(mask.textContent, /素材已下架/, 'unavailable initial item remains visible with its reason');
assert.match(mask.querySelector('[data-v3-material-key$="1"]').textContent, /素材已下架/, 'unavailable reason remains visible when the item also has a subtitle');
const selectable = mask.querySelector('[data-v3-material-key$="2"]');
const selectablePreview = selectable.querySelector('[data-v3-material-preview]');
selectablePreview.dispatchEvent(new Event('error'));
assert.equal(selectablePreview.hidden, true, 'a failed thumbnail hides the browser broken-image affordance');
assert.equal(selectable.querySelector('[data-v3-material-preview-unavailable]').hidden, false, 'a failed thumbnail gives the operator an explicit preview fallback');
assert.equal(selectable.disabled, false, 'a thumbnail failure does not make an otherwise selectable material unavailable');
selectable.focus();
const space = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: ' ' });
selectable.dispatchEvent(space);
await flush();
assert.equal(space.defaultPrevented, true, 'Space changes a focused temporary selection without a mouse click');
assert.equal(document.activeElement.dataset.v3MaterialKey.endsWith(':2'), true, 'grid focus survives its state rerender');
const arrow = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'ArrowRight' });
document.activeElement.dispatchEvent(arrow);
assert.equal(arrow.defaultPrevented, true, 'arrow navigation remains available after keyboard selection');
assert.equal(document.activeElement.matches('[data-v3-material-key]'), true, 'arrow navigation retains a usable option focus');
const tab = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab' });
document.activeElement.dispatchEvent(tab);
assert.equal(tab.defaultPrevented, false, 'Tab remains available to continue through dialog controls');
const removeInitial = mask.querySelector('[data-v3-material-remove]');
removeInitial.focus();
removeInitial.click();
await flush();
assert.equal(document.activeElement.dataset.v3MaterialRemove.endsWith(':2'), true, 'removing a selected item retains focus within the dialog');
mask.querySelector('[data-v3-picker-more]').click();
await flush();
assert.equal(calls.at(-1).cursor, 'next', 'paging preserves the committed query');
mask.querySelector('[data-v3-picker-reload]').click();
await flush();
assert.equal(calls.at(-1).query, '草稿', 'reload keeps the committed query');
search.value = '故障';
search.dispatchEvent(new Event('input', { bubbles: true }));
search.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' }));
await flush();
assert.match(mask.textContent, /目录暂不可用/);
assert.match(mask.textContent, /可选素材/, 'failure retains the last usable page');
search.value = '异类';
search.dispatchEvent(new Event('input', { bubbles: true }));
search.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter', code: 'Enter' }));
await flush();
assert.match(mask.textContent, /不匹配的素材类型/, 'a scoped loader cannot render a different material type under this picker');
assert.match(mask.textContent, /可选素材/, 'type mismatch preserves the prior usable page');
mask.querySelector('[data-v3-picker-confirm]').click();
await flush();
assert.deepEqual(committed.added.map((item) => item.library_id), [2]);
assert.deepEqual(committed.removed.map((item) => item.library_id), [1]);
assert.deepEqual(legacyAdded, [], 'a migrated onCommit caller is never double-applied through legacy onConfirm');
assert.equal(document.querySelector('[data-v3-selection-session="material"]'), null);
assert.equal(document.activeElement, trigger, 'dialog returns focus to its trigger');

let cancelled = 0;
window.AICRMMaterialPicker.open({ type: 'image', limit: 1, onCancel: () => { cancelled += 1; } });
await flush();
const second = document.querySelector('[data-v3-selection-session="material"]');
second.querySelector('[data-v3-material-key$="2"]').click();
assert.equal(second.querySelector('[data-v3-material-key$="2"]').getAttribute('aria-pressed'), 'true', 'selected material exposes its toggle state');
second.querySelector('[data-v3-picker-cancel]').click();
assert.equal(cancelled, 1, 'cancel never calls a material commit callback');
assert.equal(document.querySelector('[data-v3-selection-session="material"]'), null);

let resolveAtomicCommit;
let atomicCommitCalls = 0;
window.AICRMMaterialPicker.open({ type: 'image', onCommit: () => {
  atomicCommitCalls += 1;
  return new Promise((resolve) => { resolveAtomicCommit = resolve; });
} });
await flush();
const atomic = document.querySelector('[data-v3-selection-session="material"]');
atomic.querySelector('[data-v3-material-key$="2"]').click();
const atomicConfirm = atomic.querySelector('[data-v3-picker-confirm]');
atomicConfirm.click(); atomicConfirm.click();
await flush();
assert.equal(atomicCommitCalls, 1, 'a pending caller commit cannot be invoked twice by repeated confirmation');
assert.equal(atomicConfirm.disabled, true, 'confirmation locks while the caller applies the result');
assert.equal(atomic.querySelector('[data-v3-picker-reload]').disabled, true, 'refresh cannot redraw an in-flight caller commit');
atomic.querySelector('[data-v3-picker-cancel]').click();
assert.ok(document.querySelector('[data-v3-selection-session="material"]'), 'cancel cannot close a dialog while its caller commit is in flight');
resolveAtomicCommit();
await flush(); await flush();
assert.equal(document.querySelector('[data-v3-selection-session="material"]'), null, 'the session commits and closes only after the caller accepts it');

let failedAtomicCalls = 0;
window.AICRMMaterialPicker.open({ type: 'image', onCommit: () => {
  failedAtomicCalls += 1;
  if (failedAtomicCalls === 1) throw new Error('调用方拒绝保存');
} });
await flush();
const failedAtomic = document.querySelector('[data-v3-selection-session="material"]');
failedAtomic.querySelector('[data-v3-material-key$="2"]').click();
failedAtomic.querySelector('[data-v3-picker-confirm]').click();
await flush(); await flush();
assert.match(failedAtomic.textContent, /调用方拒绝保存/, 'a synchronous caller failure is presented without closing the dialog');
assert.equal(failedAtomic.querySelector('[data-v3-material-key$="2"]').getAttribute('aria-pressed'), 'true', 'a caller failure preserves the temporary selection');
failedAtomic.querySelector('[data-v3-picker-confirm]').click();
await flush(); await flush();
assert.equal(failedAtomicCalls, 2, 'a later explicit retry recomputes the original diff instead of an already-committed empty diff');
assert.equal(document.querySelector('[data-v3-selection-session="material"]'), null);

let rejectedLegacyCalls = 0;
window.AICRMMaterialPicker.open({ type: 'image', onConfirm: async () => {
  rejectedLegacyCalls += 1;
  throw new Error('旧调用方结果未确认');
} });
await flush();
const rejectedLegacy = document.querySelector('[data-v3-selection-session="material"]');
rejectedLegacy.querySelector('[data-v3-material-key$="2"]').click();
rejectedLegacy.querySelector('[data-v3-picker-confirm]').click();
await flush(); await flush();
assert.equal(rejectedLegacyCalls, 1, 'a rejected legacy callback is caught once without an automatic replay');
assert.match(rejectedLegacy.textContent, /旧调用方结果未确认/, 'a rejected legacy callback keeps its explicit failure visible');
assert.equal(rejectedLegacy.querySelector('[data-v3-material-key$="2"]').getAttribute('aria-pressed'), 'true', 'a rejected legacy callback does not advance the session committed value');
rejectedLegacy.querySelector('[data-v3-picker-cancel]').click();

let multiLegacyCalls = 0;
window.AICRMMaterialPicker.open({ type: 'image', limit: 3, onConfirm: () => { multiLegacyCalls += 1; } });
await flush();
const multiLegacy = document.querySelector('[data-v3-selection-session="material"]');
multiLegacy.querySelector('[data-v3-material-key$="2"]').click();
multiLegacy.querySelector('[data-v3-picker-more]').click();
await flush();
multiLegacy.querySelector('[data-v3-material-key$="3"]').click();
multiLegacy.querySelector('[data-v3-picker-confirm]').click();
assert.equal(multiLegacyCalls, 0, 'legacy onConfirm never receives an unsafe partial multi-select sequence');
assert.match(multiLegacy.textContent, /多选素材需要调用方提供 onCommit/, 'multi-select requires an atomic caller commit');
assert.equal(multiLegacy.querySelectorAll('[data-v3-material-remove]').length, 2, 'the rejected legacy multi-select remains a visible draft');
multiLegacy.querySelector('[data-v3-picker-cancel]').click();

let unsupportedRemovalCommits = 0;
window.AICRMMaterialPicker.open({ type: 'image', selectedIds: [9], limit: 2, onConfirm: () => { unsupportedRemovalCommits += 1; } });
await flush();
const unsupported = document.querySelector('[data-v3-selection-session="material"]');
assert.match(unsupported.textContent, /素材状态待目录确认/, 'unknown selected IDs stay visible but never become invented available records');
unsupported.querySelector('[data-v3-material-remove]').click();
unsupported.querySelector('[data-v3-picker-confirm]').click();
assert.ok(document.querySelector('[data-v3-selection-session="material"]'), 'a caller without onCommit cannot silently remove a prior selection');
assert.equal(unsupportedRemovalCommits, 0, 'unsupported removal performs no legacy add callback');
unsupported.querySelector('[data-v3-picker-confirm]').click();
assert.ok(document.querySelector('[data-v3-selection-session="material"]'), 'a second confirmation cannot bypass the removal capability check');
assert.equal(unsupportedRemovalCommits, 0, 'rejected removal never mutates the caller on repeated confirmation');
unsupported.querySelector('[data-v3-picker-cancel]').click();


let accessCommit = 0;
window.AICRMMaterialPicker.open({ type: 'image', selectedIds: [2], onCommit: () => { accessCommit += 1; } });
await flush();
const access = document.querySelector('[data-v3-selection-session="material"]');
const accessSearch = access.querySelector('[data-v3-picker-search-input]');
accessSearch.value = '权限'; accessSearch.dispatchEvent(new Event('input', { bubbles: true })); accessSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
await flush();
assert.match(access.textContent, /权限已失效/, 'caller-classified 403 keeps the access reason explicit');
assert.match(access.textContent, /可选素材/, '403 preserves committed selected records for inspection');
assert.equal(access.querySelector('[data-v3-picker-confirm]').disabled, true, 'lost directory access cannot commit old or new material selections');
access.querySelector('[data-v3-picker-cancel]').click();
assert.equal(accessCommit, 0);

window.AICRMMaterialPicker.open({ type: 'image', selectedIds: [22], onCommit: () => {} });
await flush();
const delayedMaterial = document.querySelector('[data-v3-selection-session="material"]');
const delayedMaterialSearch = delayedMaterial.querySelector('[data-v3-picker-search-input]');
delayedMaterialSearch.value = '旧权限'; delayedMaterialSearch.dispatchEvent(new Event('input', { bubbles: true })); delayedMaterialSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
delayedMaterialSearch.value = '新结果'; delayedMaterialSearch.dispatchEvent(new Event('input', { bubbles: true })); delayedMaterialSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
await flush();
delayedMaterial403(Object.assign(new Error('forbidden'), { status: 403 }));
await flush(); await flush();
assert.equal(delayedMaterial.querySelector('[data-v3-picker-confirm]').disabled, false, 'a late old 403 must not lock the newer material result');
assert.doesNotMatch(delayedMaterial.textContent, /权限已失效/, 'late old 403 has no visible effect after newer success');
delayedMaterialSearch.value = '旧权限'; delayedMaterialSearch.dispatchEvent(new Event('input', { bubbles: true })); delayedMaterialSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
delayedMaterial.querySelector('[data-v3-picker-cancel]').click();
window.AICRMMaterialPicker.open({ type: 'image', onCommit: () => {} });
await flush();
const reopenedMaterial = document.querySelector('[data-v3-selection-session="material"]');
delayedMaterial403(Object.assign(new Error('forbidden'), { status: 403 }));
await flush(); await flush();
assert.equal(reopenedMaterial.querySelector('[data-v3-picker-confirm]').disabled, false, 'a delayed closed material loader must not lock a reopened session');
reopenedMaterial.querySelector('[data-v3-picker-cancel]').click();

window.AICRMMaterialPicker.open({ type: 'group_invite', selectedIds: [9], onCommit: () => {} });
await flush();
const invite = document.querySelector('[data-v3-selection-session="material"]');
assert.ok(invite, 'Media-owned group invite uses the V3 temporary selector');
assert.equal(calls.at(-1).type, 'group_invite', 'group invite loader stays caller-scoped and read-only');
assert.equal(donorCalls.length, 0, 'group invite cannot fall through to a frozen ensure/create command');
invite.querySelector('[data-v3-picker-cancel]').click();
dom.window.close();
console.log('material picker adapter: scoped load, native IME search, paging, failure retention, draft commit, and cancel PASS');
