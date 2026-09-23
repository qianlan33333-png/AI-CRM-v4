import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><button id="trigger">选择员工</button>', { url: 'https://test.invalid' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Element: dom.window.Element, HTMLElement: dom.window.HTMLElement, KeyboardEvent: dom.window.KeyboardEvent, Event: dom.window.Event, DOMException: dom.window.DOMException, AbortController: dom.window.AbortController });
const bundle = await build({ stdin: { contents: "export { openStaffPicker, unresolvedStaffRecord } from './web/v3/shared/ui/staffPickerAdapter';", resolveDir: process.cwd(), sourcefile: 'staff-picker-test.ts' }, bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020' });
const { openStaffPicker, unresolvedStaffRecord } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
const source = 'owner_migration.operation_members';
const staff = [
  { source, staff_id: '11', user_id: 'inactive-source', display_name: '已停用原负责人', active: false },
  { source, staff_id: '12', user_id: 'active-target', display_name: '目标负责人', active: true },
];

const trigger = document.querySelector('#trigger'); trigger.focus();
let committed; let commits = 0;
openStaffPicker({ title: '选择负责人', source, scope: 'owner_migration', mode: 'single', limit: 1, selectedRecords: [staff[0]], directoryHint: '最多显示前 100 项。', loadPage: async () => ({ items: staff }), onCommit: async (result) => { commits += 1; committed = result; } });
await flush(); await flush();
const mask = document.querySelector('[data-v3-selection-session="staff"]');
assert.ok(mask); assert.equal(mask.dataset.selectionSource, source); assert.match(mask.textContent, /最多显示前 100 项/);
assert.equal(mask.querySelectorAll('.aicrm-v3-staff-picker__meta').length, 1, 'status and the optional directory hint share one fixed dialog grid row');
assert.equal(mask.querySelector('.aicrm-v3-staff-picker__meta')?.contains(mask.querySelector('[data-v3-staff-status]')), true, 'live status remains inside the fixed metadata row');
const search = mask.querySelector('[data-v3-picker-search-input]');
search.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
const imeEscape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' }); search.dispatchEvent(imeEscape);
assert.ok(document.querySelector('[data-v3-selection-session="staff"]'), 'IME Escape keeps the staff dialog open');
search.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true }));
const candidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' }); Object.defineProperty(candidateEnter, 'keyCode', { value: 229 }); search.dispatchEvent(candidateEnter);
assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate Enter does not submit a scoped read');
mask.querySelector('[data-v3-staff-key$=":12"]').click();
assert.equal(mask.querySelector('[data-v3-staff-key$=":12"]').getAttribute('aria-pressed'), 'true', 'current row exposes its selected state');
mask.querySelector('[data-v3-staff-confirm]').click(); mask.querySelector('[data-v3-staff-confirm]').click();
await flush(); await flush();
assert.equal(commits, 1, 'commit locks duplicate confirmation');
assert.deepEqual(committed.selected.map((item) => item.staff_id), ['12']);
assert.equal(document.querySelector('[data-v3-selection-session="staff"]'), null); assert.equal(document.activeElement, trigger, 'close restores opener focus');

let rejectOld403;
openStaffPicker({ title: '权限乱序', source, scope: 'stale-access', selectedRecords: [], mode: 'single', loadPage: ({ query }) => query === '旧' ? new Promise((_resolve, reject) => { rejectOld403 = reject; }) : Promise.resolve({ items: [staff[1]] }), onCommit: () => {}, accessLossMessage: (error) => error?.status === 403 ? '员工目录权限已失效；当前草稿仍保留。' : undefined });
await flush();
const stale = document.querySelector('[data-v3-selection-session="staff"]'); const staleSearch = stale.querySelector('[data-v3-picker-search-input]');
staleSearch.value = '旧'; staleSearch.dispatchEvent(new Event('input', { bubbles: true })); staleSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
staleSearch.value = '新'; staleSearch.dispatchEvent(new Event('input', { bubbles: true })); staleSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' })); await flush();
rejectOld403(Object.assign(new Error('forbidden'), { status: 403 })); await flush(); await flush();
assert.doesNotMatch(stale.textContent, /权限已失效/, 'late old 403 does not lock the newer successful staff directory');
stale.querySelector('[data-v3-staff-cancel]').click();

let refreshAbort = false; let resolveRefresh;
openStaffPicker({ title: '刷新可取消', source, scope: 'refresh-cancel', selectedRecords: [staff[1]], mode: 'single', loadPage: async () => ({ items: [staff[1]] }), refresh: ({ signal }) => new Promise((resolve) => { resolveRefresh = resolve; signal.addEventListener('abort', () => { refreshAbort = true; resolve(); }); }), onCommit: () => {} });
await flush(); await flush();
const refreshing = document.querySelector('[data-v3-selection-session="staff"]'); refreshing.querySelector('[data-v3-staff-reload]').click(); await flush();
assert.equal(refreshing.querySelector('[data-v3-staff-cancel]').disabled, false, 'refresh keeps cancellation available');
refreshing.querySelector('[data-v3-staff-cancel]').click(); await flush();
assert.equal(refreshAbort, true, 'cancel aborts the refreshing caller loader'); assert.equal(document.querySelector('[data-v3-selection-session="staff"]'), null);
resolveRefresh?.();

let failureCalls = 0;
openStaffPicker({ title: '失败保留', source, scope: 'failure', selectedRecords: [], mode: 'single', loadPage: async () => ({ items: [staff[1]] }), onCommit: () => { failureCalls += 1; throw new Error('调用方未应用；暂选仍保留。'); } });
await flush(); await flush();
const failure = document.querySelector('[data-v3-selection-session="staff"]'); failure.querySelector('[data-v3-staff-key]').click(); failure.querySelector('[data-v3-staff-confirm]').click(); await flush();
assert.match(failure.textContent, /调用方未应用；暂选仍保留/); assert.ok(failure.querySelector('[data-v3-staff-remove]'), 'caller failure preserves the draft');
failure.querySelector('[data-v3-staff-confirm]').click(); await flush(); assert.equal(failureCalls, 2, 'an explicit retry retains the same draft'); failure.querySelector('[data-v3-staff-cancel]').click();

openStaffPicker({ title: '未解析初选', source, scope: 'unresolved', selectedRecords: [unresolvedStaffRecord(source, 99, '', '目录前 100 项外，不能据此判定失效。')], mode: 'single', loadPage: async () => ({ items: staff }), onCommit: () => {} });
await flush(); await flush();
const unresolved = document.querySelector('[data-v3-selection-session="staff"]');
assert.match(unresolved.textContent, /不能据此判定失效/); assert.equal(unresolved.querySelector('[data-v3-staff-confirm]').disabled, true, 'unresolved initial selection cannot silently commit');
unresolved.querySelector('[data-v3-staff-remove]').click(); assert.equal(unresolved.querySelector('[data-v3-staff-confirm]').disabled, false, 'operator may explicitly remove an unresolved initial selection'); unresolved.querySelector('[data-v3-staff-cancel]').click();

openStaffPicker({ title: '目录字段不完整', source, scope: 'malformed-directory', selectedRecords: [], mode: 'single', loadPage: async () => ({ items: [{ source, staff_id: '77', user_id: '', display_name: '缺少 UserID 的目录行' }] }), onCommit: () => { throw new Error('malformed record must never commit'); } });
await flush(); await flush();
const malformed = document.querySelector('[data-v3-selection-session="staff"]');
assert.match(malformed.textContent, /缺少可信企微 UserID/); assert.equal(malformed.querySelector('[data-v3-staff-key]').disabled, true, 'a malformed live directory row is not selectable'); assert.equal(malformed.querySelector('[data-v3-staff-confirm]').disabled, false, 'no selection still permits a caller that explicitly supports an empty result'); malformed.querySelector('[data-v3-staff-cancel]').click();

dom.window.close();
console.log('staff picker adapter: scoped directory, IME, stale access, refresh cancel, commit failure and unresolved selection PASS');
