import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html>', { url: 'https://test.invalid' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, AbortController: dom.window.AbortController });
const bundle = await build({
  stdin: { contents: "export { SelectionSession, selectionKey } from './web/v3/shared/ui/selectionSession';", resolveDir: process.cwd(), sourcefile: 'selection-session-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const item = (id, label = `项目 ${id}`) => ({ kind: 'material.image', source: 'image-library', id, label, value: { id } });

assert.equal(mod.selectionKey('material.image', 'image-library', 7), 'material.image:image-library:7');
const session = new mod.SelectionSession([item(1)], { limit: 2 });
const key1 = mod.selectionKey('material.image', 'image-library', 1);
const key2 = mod.selectionKey('material.image', 'image-library', 2);
const key3 = mod.selectionKey('material.image', 'image-library', 3);
const key4 = mod.selectionKey('material.image', 'image-library', 4);
session.setDisabled(key1, '素材已停用');
assert.equal(session.toggle(key1), true, 'an unavailable selected item can only be explicitly removed');
assert.equal(session.snapshot().draft.length, 0);
session.cancel();
assert.equal(session.snapshot().draft[0].disabledReason, '素材已停用', 'cancel restores unavailable selections for review');

let oldResolve;
const old = session.submitSearch(() => new Promise((resolve) => { oldResolve = resolve; }));
session.setDraftQuery('新素材');
const current = session.submitSearch(async ({ query }) => ({ items: [item(2, query)], nextCursor: 'page-2' }));
assert.equal((await current).state, 'applied');
oldResolve({ items: [item(3, '旧素材')] });
assert.equal((await old).state, 'stale');
assert.equal(session.snapshot().items[0].label, '新素材', 'older settled reads cannot replace newer results');

session.setDraftQuery('未提交的草稿');
let reloadQuery = '';
await session.reload(async ({ query }) => { reloadQuery = query; return { items: [item(2, query)], nextCursor: 'page-2' }; });
assert.equal(reloadQuery, '新素材', 'reload retains submitted query instead of promoting a text draft');
assert.equal(session.snapshot().query.draft, '未提交的草稿');
let cursor = '';
await session.loadNextPage(async ({ query, cursor: next }) => { cursor = `${query}/${next}`; return { items: [item(3), item(4)] }; });
assert.equal(cursor, '新素材/page-2');
assert.deepEqual(session.snapshot().items.map((value) => value.value.id).sort(), [2, 3, 4]);

let refreshCursor = 'not loaded';
await session.reload(async ({ query, cursor: next }) => {
  refreshCursor = next || '';
  assert.equal(query, '新素材');
  return { items: [item(2, query)], nextCursor: 'page-2' };
});
assert.equal(refreshCursor, '', 'refresh restarts the submitted query from page one after pagination');
assert.deepEqual(session.snapshot().items.map((value) => value.value.id), [2], 'refresh no longer replaces page one with the last loaded page');
assert.deepEqual(session.snapshot().draft.map((value) => value.value.id), [1], 'refresh preserves selection even when it is outside the current page');

const detachedSnapshot = session.snapshot();
detachedSnapshot.items[0].label = '被宿主篡改';
detachedSnapshot.items[0].value.id = 99;
detachedSnapshot.draft[0].disabledReason = '被宿主篡改';
const detachedItem = session.item(key2);
detachedItem.label = '再次篡改';
assert.equal(session.snapshot().items[0].label, '新素材', 'snapshot records cannot mutate session state');
assert.equal(session.snapshot().items[0].value.id, 2, 'snapshot values cannot mutate session state');
assert.equal(session.snapshot().draft[0].disabledReason, '素材已停用', 'item views cannot mutate disabled state');

assert.equal(session.toggle(key1), true, 'the unavailable initial item may be explicitly removed');
assert.equal(session.toggle(key2), true);
assert.equal(session.toggle(key3), true);
assert.equal(session.toggle(key4), false, 'selection limit requires an explicit removal');
assert.equal(session.snapshot().notice, '最多选择 2 项。');
const commit = session.commit();
assert.deepEqual(commit.added.map((value) => value.value.id).sort(), [2, 3]);
assert.deepEqual(commit.selected.map((value) => value.value.id).sort(), [2, 3]);

assert.equal(session.toggle(key2), true);
session.setReadonly('无编辑权限');
assert.deepEqual(session.snapshot().draft.map((value) => value.value.id).sort(), [2, 3], 'readonly restores committed selection');
assert.deepEqual(session.commit().removed, [], 'readonly cannot commit a previously edited draft');

let preserveAccessSession;
const accessDraft = new mod.SelectionSession([item(1)], {
  onCurrentLoadFailure: error => { if (error?.status === 403) preserveAccessSession.lockReadonly('目录权限已失效'); },
});
preserveAccessSession = accessDraft;
await accessDraft.submitSearch(async () => ({ items: [item(2)] }));
assert.equal(accessDraft.toggle(mod.selectionKey('material.image', 'image-library', 1)), true, 'operator may draft-remove before access loss');
assert.equal(accessDraft.toggle(mod.selectionKey('material.image', 'image-library', 2)), true, 'operator may draft-add before access loss');
const currentForbidden = Object.assign(new Error('forbidden'), { status: 403 });
await accessDraft.submitSearch(async () => { throw currentForbidden; });
assert.deepEqual(accessDraft.snapshot().draft.map((value) => value.value.id), [2], 'current 403 locks but preserves the visible draft');
assert.deepEqual(accessDraft.snapshot().committed.map((value) => value.value.id), [1], 'current 403 leaves the original committed selection for cancel');
assert.equal(accessDraft.snapshot().readonlyReason, '目录权限已失效');
accessDraft.cancel();
assert.deepEqual(accessDraft.snapshot().draft.map((value) => value.value.id), [1], 'cancel alone restores the original selection after an access lock');

let closedResolve;
const closed = session.reload(() => new Promise((resolve) => { closedResolve = resolve; }));
session.cancel();
closedResolve({ items: [item(1)] });
assert.equal((await closed).state, 'stale', 'cancel aborts and invalidates an in-flight read');

const failed = await session.reload(async () => { throw new Error('network unavailable'); });
assert.equal(failed.state, 'failed');
assert.deepEqual(session.snapshot().items.map((value) => value.value.id), [2], 'failed reads retain the last successful first page');
assert.equal(session.snapshot().error, 'network unavailable');

let staleAccessFailures = 0;
const accessRace = new mod.SelectionSession([], { onCurrentLoadFailure: () => { staleAccessFailures += 1; } });
let rejectOldAccess;
const oldAccess = accessRace.submitSearch(() => new Promise((_resolve, reject) => { rejectOldAccess = reject; }));
const newerAccess = accessRace.submitSearch(async () => ({ items: [item(4, 'new authorized result')] }));
assert.equal((await newerAccess).state, 'applied');
const forbidden = Object.assign(new Error('forbidden'), { status: 403 });
rejectOldAccess(forbidden);
assert.equal((await oldAccess).state, 'stale');
assert.equal(staleAccessFailures, 0, 'a delayed stale 403 must not alter the newer selection session');
let rejectClosedAccess;
const closedAccess = accessRace.reload(() => new Promise((_resolve, reject) => { rejectClosedAccess = reject; }));
accessRace.cancel();
rejectClosedAccess(forbidden);
assert.equal((await closedAccess).state, 'stale');
assert.equal(staleAccessFailures, 0, 'a delayed 403 after close must not alter a cancelled session');

const single = new mod.SelectionSession([item(1)], { mode: 'single' });
single.setDisabled(key1, '素材已停用');
await single.submitSearch(async () => ({ items: [item(2)] }));
assert.equal(single.toggle(key2), true, 'single selection can replace an unavailable draft choice');
assert.deepEqual(single.snapshot().draft.map((value) => value.value.id), [2]);
single.cancel();
assert.deepEqual(single.snapshot().draft.map((value) => value.value.id), [1], 'cancel restores the original unavailable single choice');

let silentEmits = 0;
const silentDraft = new mod.SelectionSession();
silentDraft.subscribe(() => { silentEmits += 1; });
silentDraft.setDraftQuery('仅草稿', { silent: true });
assert.equal(silentEmits, 1, 'native search drafts can update without rerendering a picker');
assert.equal(silentDraft.snapshot().query.draft, '仅草稿');
const overLimit = new mod.SelectionSession([item(1), item(2)], { limit: 1 });
assert.equal(overLimit.snapshot().overLimit, true, 'initial selected records are never silently truncated');
assert.throws(() => overLimit.commit(), /超过单选限制/);
overLimit.toggle(key2);
assert.equal(overLimit.snapshot().overLimit, false, 'operator can reduce initial selections before confirmation');

dom.window.close();
console.log('selection session: query, draft, disabled, paging, readonly, single mode, race, and failure retention PASS');
