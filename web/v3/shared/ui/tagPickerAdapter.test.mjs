import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><button id="trigger">选择标签</button>', { url: 'https://test.invalid' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Element: dom.window.Element, HTMLElement: dom.window.HTMLElement, KeyboardEvent: dom.window.KeyboardEvent, Event: dom.window.Event, DOMException: dom.window.DOMException, AbortController: dom.window.AbortController });
const bundle = await build({ stdin: { contents: "export { createTagCatalogPageLoader, openTagPicker, parseCompleteTagCatalog, unresolvedTagRecord } from './web/v3/shared/ui/tagPickerAdapter';", resolveDir: process.cwd(), sourcefile: 'tag-picker-test.ts' }, bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020' });
const { createTagCatalogPageLoader, openTagPicker, parseCompleteTagCatalog, unresolvedTagRecord } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const flush = () => new Promise(resolve => setTimeout(resolve, 0));
const source = 'local_tag_catalog';
const catalog = (tags) => ({ read_model_status: 'ready', groups: [{ group_id: 1, group_name: '新客' }, { group_id: 2, group_name: '复购' }], items: tags, count: tags.length, total_tags: tags.length, tag_limit: 1000 });
const tags = [
  { tag_id: 1, group_id: 1, group_name: '新客', tag_name: '首次咨询' },
  { tag_id: 2, group_id: 2, group_name: '复购', tag_name: '跨页标签' },
  { tag_id: 3, group_id: 2, group_name: '复购', tag_name: '已购买' },
];

assert.throws(() => parseCompleteTagCatalog({ ...catalog(tags), total_tags: 4 }, source), /计数/);
assert.throws(() => parseCompleteTagCatalog({ ...catalog(tags), read_model_status: 'syncing' }, source), /尚未就绪/);
assert.equal(parseCompleteTagCatalog(catalog(tags), source)[1].group_id, '2', 'complete Owner snapshot keeps source/group/tag identity');
const records = parseCompleteTagCatalog(catalog(tags), source);

let resolveOld;
let calls = 0;
const raceLoader = createTagCatalogPageLoader(source, ({ signal }) => {
  calls += 1;
  if (calls === 1) return new Promise(resolve => { resolveOld = resolve; });
  return Promise.resolve(catalog([{ tag_id: 8, group_id: 2, tag_name: '新搜索结果' }, { tag_id: 9, group_id: 2, tag_name: '新搜索下一页' }]));
}, 1);
const oldAbort = new AbortController();
const newAbort = new AbortController();
const oldRead = raceLoader({ query: '旧', signal: oldAbort.signal });
oldAbort.abort();
const newRead = await raceLoader({ query: '新', signal: newAbort.signal });
resolveOld(catalog([{ tag_id: 7, group_id: 1, tag_name: '旧搜索结果' }, { tag_id: 6, group_id: 1, tag_name: '旧搜索下一页' }]));
await assert.rejects(oldRead, /替换/);
const nextRead = await raceLoader({ query: '新', cursor: newRead.nextCursor, signal: newAbort.signal });
assert.equal(nextRead.items[0].tag_name, '新搜索下一页', 'late aborted catalog never replaces the new paging snapshot');

const trigger = document.querySelector('#trigger');
trigger.focus();
let committed;
let commits = 0;
const loadPage = createTagCatalogPageLoader(source, async () => catalog(tags), 1);
openTagPicker({
  title: '选择商品标签', source, scope: 'product.purchase_after_tag', mode: 'multiple',
  selectedRecords: [unresolvedTagRecord(source, 2)], loadPage,
  onCommit: async (value) => { commits += 1; committed = value; },
});
await flush(); await flush();
const mask = document.querySelector('[data-v3-selection-session="tag"]');
assert.ok(mask);
assert.ok(mask.classList.contains('aicrm-v3-tag-picker-mask'), 'V3 dialog keeps its own class prefix instead of inheriting frozen tag picker selectors');
assert.equal(mask.querySelector('.aicrm-v3-tag-picker')?.classList.contains('aicrm-tag-picker'), false, 'V3 dialog does not retain the frozen tag picker class');
assert.equal(mask.dataset.selectionSource, source);
assert.match(mask.querySelector('[data-v3-tag-selected]').textContent, /跨页标签.*复购/, 'initial selection resolves from the complete snapshot even when its result page is elsewhere');
assert.match(mask.querySelector('[data-v3-tag-list]').textContent, /新客/, 'results are visibly partitioned by group');
const groupFilter = mask.querySelector('[data-v3-tag-group-filter]');
assert.match(groupFilter.textContent, /复购/, 'complete catalog exposes an actual group filter');
groupFilter.value = '2'; groupFilter.dispatchEvent(new Event('change', { bubbles: true })); await flush();
assert.match(mask.querySelector('[data-v3-tag-list]').textContent, /复购/);
assert.doesNotMatch(mask.querySelector('[data-v3-tag-list]').textContent, /首次咨询/);
const search = mask.querySelector('[data-v3-picker-search-input]');
search.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true }));
const imeEscape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' }); search.dispatchEvent(imeEscape);
assert.ok(document.querySelector('[data-v3-selection-session="tag"]'), 'IME Escape keeps the dialog open');
search.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true })); await flush();
search.value = '跨页'; search.dispatchEvent(new Event('input', { bubbles: true }));
const candidateEnter = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' }); Object.defineProperty(candidateEnter, 'keyCode', { value: 229 }); search.dispatchEvent(candidateEnter); await flush();
assert.equal(candidateEnter.defaultPrevented, false, 'IME candidate Enter does not submit search');
search.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Enter' })); await flush();
const selectedRow = mask.querySelector('[data-v3-tag-key]');
assert.equal(selectedRow.getAttribute('aria-pressed'), 'true', 'tag result exposes visible pressed state');
mask.querySelector('[data-v3-tag-confirm]').click();
mask.querySelector('[data-v3-tag-confirm]').click();
await flush(); await flush();
assert.equal(commits, 1, 'confirm is locked while the caller applies the draft');
assert.deepEqual(committed.selected.map(value => value.tag_id), ['2'], 'single group filter does not discard prior resolved selection');
assert.equal(document.querySelector('[data-v3-selection-session="tag"]'), null);
assert.equal(document.activeElement, trigger, 'close returns focus to the opener');

let applied = 0;
openTagPicker({ title: '缺失标签', source, scope: 'channel.entry_tag', mode: 'single', selectedRecords: [unresolvedTagRecord(source, 99)], loadPage: createTagCatalogPageLoader(source, async () => catalog(tags), 1), onCommit: () => { applied += 1; } });
await flush(); await flush();
const missing = document.querySelector('[data-v3-selection-session="tag"]');
assert.match(missing.querySelector('[data-v3-tag-selected]').textContent, /不在当前完整目录/);
assert.equal(missing.querySelector('[data-v3-tag-confirm]').disabled, true, 'a conclusively missing tag cannot be silently retained on confirm');
missing.querySelector('[data-v3-tag-remove]').click();
assert.equal(missing.querySelector('[data-v3-tag-confirm]').disabled, false, 'operator can remove a conclusively missing tag');
missing.querySelector('[data-v3-tag-cancel]').click();
assert.equal(applied, 0, 'cancel does not update channel form state');

let reject403;
openTagPicker({ title: '权限', source, scope: 'customer.filter.tag', mode: 'single', selectedRecords: [unresolvedTagRecord(source, 2)], loadPage: ({ query }) => query === '权限' ? new Promise((_resolve, reject) => { reject403 = reject; }) : Promise.resolve({ items: [records[0]], resolved: records, groups: [{ group_id: '1', group_name: '新客' }] }), onCommit: () => {}, accessLossMessage: error => error?.status === 403 ? '标签目录权限已失效；当前筛选条件仍保留。' : undefined });
await flush();
const access = document.querySelector('[data-v3-selection-session="tag"]');
const accessSearch = access.querySelector('[data-v3-picker-search-input]');
accessSearch.value = '权限'; accessSearch.dispatchEvent(new Event('input', { bubbles: true })); accessSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
const forbidden = new Error('forbidden'); forbidden.status = 403; reject403(forbidden); await flush();
assert.match(access.textContent, /权限已失效/);
assert.match(access.textContent, /跨页标签/, '403 preserves resolved current selection');
assert.equal(access.querySelector('[data-v3-tag-confirm]').disabled, true);
access.querySelector('[data-v3-tag-cancel]').click();

let rejectOld403;
openTagPicker({ title: '旧权限乱序', source, scope: 'stale-permission', mode: 'single', selectedRecords: [], loadPage: ({ query }) => query === '旧权限' ? new Promise((_resolve, reject) => { rejectOld403 = reject; }) : Promise.resolve({ items: [records[0]], resolved: records, groups: [{ group_id: '1', group_name: '新客' }] }), onCommit: () => {}, accessLossMessage: error => error?.status === 403 ? '标签目录权限已失效；当前筛选条件仍保留。' : undefined });
await flush();
const stalePermission = document.querySelector('[data-v3-selection-session="tag"]');
const staleSearch = stalePermission.querySelector('[data-v3-picker-search-input]');
staleSearch.value = '旧权限'; staleSearch.dispatchEvent(new Event('input', { bubbles: true })); staleSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' }));
staleSearch.value = '新结果'; staleSearch.dispatchEvent(new Event('input', { bubbles: true })); staleSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' })); await flush();
const oldForbidden = new Error('forbidden'); oldForbidden.status = 403; rejectOld403(oldForbidden); await flush();
assert.doesNotMatch(stalePermission.textContent, /权限已失效/, 'late old 403 cannot lock the newer successful tag search');
stalePermission.querySelector('[data-v3-tag-cancel]').click();

openTagPicker({ title: '来源不匹配', source, scope: 'invalid-source', mode: 'single', selectedRecords: [{ ...records[0], source: 'foreign_tag_catalog' }], loadPage: async () => ({ items: records, resolved: records }), onCommit: () => {} });
await flush();
const invalidSource = document.querySelector('[data-v3-selection-session="tag"]');
assert.match(invalidSource.textContent, /来源与当前目录不一致/);
assert.equal(invalidSource.querySelector('[data-v3-tag-confirm]').disabled, true, 'cross-source initial selection is explicit configuration failure, never silently remapped');
invalidSource.querySelector('[data-v3-tag-cancel]').click();

let failureCalls = 0;
openTagPicker({ title: '失败保留', source, scope: 'failure', selectedRecords: [], loadPage: ({ query }) => { failureCalls += 1; if (query === '失败') throw new Error('标签目录暂不可用'); return Promise.resolve({ items: [records[0]], resolved: records, groups: [{ group_id: '1', group_name: '新客' }] }); }, onCommit: () => { throw new Error('应用失败，草稿仍保留'); } });
await flush();
const failure = document.querySelector('[data-v3-selection-session="tag"]');
failure.querySelector('[data-v3-tag-key]').click();
const failureSearch = failure.querySelector('[data-v3-picker-search-input]'); failureSearch.value = '失败'; failureSearch.dispatchEvent(new Event('input', { bubbles: true })); failureSearch.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, key: 'Enter' })); await flush();
assert.match(failure.textContent, /标签目录暂不可用/);
assert.match(failure.textContent, /首次咨询/, 'failed search keeps the last usable directory page');
failure.querySelector('[data-v3-tag-confirm]').click(); await flush();
assert.match(failure.textContent, /应用失败，草稿仍保留/);
assert.ok(failure.querySelector('[data-v3-tag-remove]'), 'caller rejection retains the selection draft');
failure.querySelector('[data-v3-tag-cancel]').click();

let movedCatalog = catalog(tags);
openTagPicker({ title: '移组回显', source, scope: 'moved-group', mode: 'multiple', selectedRecords: [unresolvedTagRecord(source, 2)], loadPage: createTagCatalogPageLoader(source, async () => movedCatalog, 50), onCommit: () => {} });
await flush(); await flush();
const moved = document.querySelector('[data-v3-selection-session="tag"]');
movedCatalog = catalog([{ tag_id: 1, group_id: 1, tag_name: '首次咨询' }, { tag_id: 2, group_id: 1, tag_name: '跨页标签' }]);
moved.querySelector('[data-v3-tag-reload]').click(); await flush(); await flush();
assert.equal(moved.querySelectorAll('[data-v3-tag-remove]').length, 1, 'a group move reconciles the same tag record instead of duplicating its selection');
assert.match(moved.querySelector('[data-v3-tag-selected]').textContent, /新客/, 'moved tag displays the new authoritative group');
moved.querySelector('[data-v3-tag-cancel]').click();

let refreshedCatalog = catalog(tags);
let refreshApplied = 0;
openTagPicker({ title: '刷新失效', source, scope: 'refresh-invalid', mode: 'multiple', selectedRecords: [], loadPage: createTagCatalogPageLoader(source, async () => refreshedCatalog, 50), onCommit: () => { refreshApplied += 1; } });
await flush(); await flush();
const refreshInvalid = document.querySelector('[data-v3-selection-session="tag"]');
refreshInvalid.querySelector('[data-v3-tag-key$=":1"]').click();
assert.match(refreshInvalid.querySelector('[data-v3-tag-selected]').textContent, /首次咨询/);
refreshedCatalog = catalog(tags.filter(tag => tag.tag_id !== 1));
refreshInvalid.querySelector('[data-v3-tag-reload]').click(); await flush(); await flush();
assert.match(refreshInvalid.querySelector('[data-v3-tag-selected]').textContent, /不在当前完整目录/);
assert.equal(refreshInvalid.querySelector('[data-v3-tag-confirm]').disabled, true, 'a newly selected tag that vanishes on refresh also blocks confirm');
assert.ok(refreshInvalid.querySelector('[data-v3-tag-remove]'), 'invalid refreshed selection remains removable');
refreshInvalid.querySelector('[data-v3-tag-cancel]').click();
assert.equal(refreshApplied, 0, 'cancel after a refreshed invalid selection does not apply or overwrite caller state');

dom.window.close();
console.log('tag picker adapter: complete catalog, groups, IME, stale cache, reconciliation, failure retention, commit, cancel PASS');
