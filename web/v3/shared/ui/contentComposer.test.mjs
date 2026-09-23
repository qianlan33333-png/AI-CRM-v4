import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><button id="trigger">编辑</button>', { url: 'https://test.invalid' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Element: dom.window.Element, HTMLElement: dom.window.HTMLElement, KeyboardEvent: dom.window.KeyboardEvent, Event: dom.window.Event });
const bundle = await build({
  stdin: { contents: "export * from './web/v3/shared/ui/contentComposer';", resolveDir: process.cwd(), sourcefile: 'content-composer-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const flush = () => new Promise((resolve) => setTimeout(resolve, 0));
let requests = 0;
globalThis.fetch = () => { requests += 1; throw new Error('composer must not fetch'); };
const trigger = document.getElementById('trigger');
trigger.focus();
let selectedRequest;
let committed;
mod.openContentComposer({
  title: '配置群运营动作内容', value: { content_text: '欢迎 {{历史变量}}', image_library_ids: [1], miniprogram_library_ids: [], attachment_library_ids: [], group_invite_library_ids: [] },
  selectedRecords: [{ source: 'media-library', kind: 'image', id: 1, label: '旧图', disabledReason: '素材已停用' }],
  materialOrder: 'caller_persisted', ordering: 'all_persisted', materialKinds: ['image', 'attachment'], limits: { image: 3, attachment: 2 }, totalLimit: 4,
  variablePolicy: { scan: (text) => text.match(/\{\{[^{}]+\}\}/g) || [], variables: [], unknownTokenReason: '当前场景未声明可用变量，将按原文保存和展示' },
  selectMaterials: async (request) => { selectedRequest = request; return request.kind === 'attachment' ? [{ source: 'media-library', kind: 'attachment', id: 9, label: '说明.pdf' }] : request.selectedRecords; },
  onConfirm: async (result) => { committed = result; },
});
await flush();
const mask = document.querySelector('[data-v3-content-composer]');
assert.ok(mask, 'V3 composer opens without any business request');
assert.equal(requests, 0);
assert.match(mask.textContent, /历史变量/);
assert.match(mask.textContent, /当前场景未声明可用变量/);
assert.match(mask.textContent, /确认仅更新当前页面草稿，实际发送由计划执行触发/);
assert.equal(mask.querySelector('[data-v3-composer-confirm]').textContent, '确认内容', 'generic callers retain the existing confirmation label by default');
assert.equal(mask.textContent.includes('页面保存前不会发送内容'), false, 'draft confirmation must not imply a page save will send content');
const text = mask.querySelector('[data-v3-composer-text]');
text.focus();
const originalTextarea = text;
text.dispatchEvent(new dom.window.CompositionEvent('compositionstart', { bubbles: true, data: '中' }));
text.value = '新的话术中';
text.dispatchEvent(new Event('input', { bubbles: true }));
assert.equal(mask.querySelector('[data-v3-composer-text]'), originalTextarea, 'composition input keeps the same textarea node instead of replacing an active IME control');
text.dispatchEvent(new dom.window.CompositionEvent('compositionend', { bubbles: true, data: '中' }));
text.value = '新的话术 {{历史变量}}';
text.setSelectionRange(2, 2);
text.dispatchEvent(new Event('input', { bubbles: true }));
await flush();
const refreshedText = mask.querySelector('[data-v3-composer-text]');
assert.equal(refreshedText, originalTextarea, 'ordinary input also retains the textarea identity');
assert.equal(document.activeElement, refreshedText, 'typing keeps the composer text focus');
assert.equal(refreshedText.selectionStart, 2, 'typing preserves the caller cursor instead of resetting it');
mask.querySelector('[data-v3-composer-add="attachment"]').click();
await flush();
assert.equal(selectedRequest.kind, 'attachment', 'caller owns the scoped selector request');
assert.match(mask.textContent, /说明.pdf/, 'returned temporary material updates only the composer draft');
const moveFirst = mask.querySelector('[data-v3-composer-move="0:1"]');
moveFirst.focus();
moveFirst.click();
await flush();
assert.ok(document.activeElement.closest('[data-v3-content-composer]'), 'reordering keeps keyboard focus inside the composer dialog');
mask.querySelector('[data-v3-composer-confirm]').click();
await flush();
assert.equal(committed.package.content_text, '新的话术 {{历史变量}}', 'confirm returns the edited local text once');
assert.deepEqual(committed.package.attachment_library_ids, [9], 'confirm returns established content fields once');
assert.deepEqual(committed.selectedRecords.map((record) => record.kind), ['attachment', 'image'], 'caller-persisted material order returns to the caller');
assert.equal(requests, 0, 'confirm has no save/send/Provider request');
assert.equal(document.querySelector('[data-v3-content-composer]'), null, 'successful local-draft callback closes the composer');
assert.equal(document.activeElement, trigger, 'composer close restores its trigger focus');
trigger.focus();
let excelDraft;
mod.openContentComposer({
  title: '编辑 Excel 行话术', value: { content_text: '原始话术' }, materialKinds: [],
  textRule: { normalize: (value) => value, validate: (value) => value.trim() ? undefined : '请输入话术' },
  presentationSupplements: [{ key: 'excel-row-8', kind: 'excel_card', title: 'Excel 标题', description: '路径：pages/excel/index', thumbnailURL: '/cover.png' }],
  onConfirm: (result) => { excelDraft = result; },
});
await flush();
const excelMask = document.querySelector('[data-v3-content-composer]');
assert.match(excelMask.textContent, /小程序卡片：Excel 标题/, 'text-only callers retain their Owner card in the shared preview');
assert.equal(excelMask.querySelectorAll('[data-v3-composer-add]').length, 0, 'Excel callers do not receive unpersistable generic material controls');
excelMask.querySelector('[data-v3-composer-text]').value = '更新后的 Excel 话术';
excelMask.querySelector('[data-v3-composer-text]').dispatchEvent(new Event('input', { bubbles: true }));
excelMask.querySelector('[data-v3-composer-confirm]').click();
await flush();
assert.equal(excelDraft.package.content_text, '更新后的 Excel 话术', 'text-only confirmation returns only a caller-owned local draft');
let finishSave;
mod.openContentComposer({
  title: '保存话术', value: { content_text: '草稿' }, materialKinds: [],
  readyHint: '确认后将保存草稿。', confirmLabel: '保存草稿', confirmingHint: '正在保存草稿…',
  onConfirm: () => new Promise((resolve) => { finishSave = resolve; }),
});
await flush();
const saveMask = document.querySelector('[data-v3-content-composer]');
assert.equal(saveMask.querySelector('[data-v3-composer-confirm]').textContent, '保存草稿', 'callers may name their own confirmed command');
assert.match(saveMask.textContent, /确认后将保存草稿/);
saveMask.querySelector('[data-v3-composer-confirm]').click();
await flush();
assert.match(saveMask.textContent, /正在保存草稿/);
finishSave();
await flush();
assert.equal(document.querySelector('[data-v3-content-composer]'), null, 'caller-specific pending wording does not alter the local-draft lifecycle');
trigger.focus();
mod.openReadonlyContentPresentation({ title: '已保存群运营内容', value: committed.package, selectedRecords: committed.selectedRecords, materialOrder: 'caller_persisted' });
await flush();
assert.match(document.body.textContent, /最近一次保存结果/);
const readonly = document.querySelector('[data-v3-content-readonly]');
readonly.querySelector('[data-v3-content-readonly-close]').click();
assert.equal(document.querySelector('[data-v3-content-readonly]'), null, 'readonly uses the same V3 dialog lifecycle');

let validatedCommit;
mod.openContentComposer({
  title: '素材与字数边界',
  value: {}, materialKinds: ['image'], limits: { image: 1 }, totalLimit: 3,
  textRule: { maximum: 2, count: (value) => Array.from(value).length },
  validateContent: ({ package: draftPackage, selectedRecords }) => !draftPackage.content_text && !selectedRecords.length ? '请填写话术或添加素材。' : undefined,
  selectMaterials: async () => [
    { source: 'media-library', kind: 'image', id: 1, label: '一号图' },
    { source: 'media-library', kind: 'image', id: 2, label: '二号图' },
  ],
  onConfirm: (result) => { validatedCommit = result; },
});
await flush();
const boundedMask = document.querySelector('[data-v3-content-composer]');
const boundedText = boundedMask.querySelector('[data-v3-composer-text]');
assert.equal(boundedText.maxLength, -1, 'the shared editor does not impose an unrelated UTF-16 maxLength');
boundedText.value = '😀😀😀';
boundedText.dispatchEvent(new Event('input', { bubbles: true }));
await flush();
assert.match(boundedMask.textContent, /话术不能超过 2 个字符/);
assert.equal(boundedMask.querySelector('[data-v3-composer-confirm]').disabled, true, 'caller rune limit blocks confirmation');
boundedText.value = '';
boundedText.dispatchEvent(new Event('input', { bubbles: true }));
await flush();
assert.match(boundedMask.textContent, /请填写话术或添加素材/);
boundedMask.querySelector('[data-v3-composer-add="image"]').click();
await flush();
assert.match(boundedMask.textContent, /图片不能超过 1 项/);
assert.equal(boundedMask.querySelector('[data-v3-composer-confirm]').disabled, true, 'an over-limit selector response is never silently truncated or confirmed');
const removeOverflow = boundedMask.querySelector('[data-v3-composer-remove="1"]');
removeOverflow.focus();
removeOverflow.click();
await flush();
assert.ok(document.activeElement.closest('[data-v3-content-composer]'), 'removing an over-limit material retains focus inside the dialog');
assert.equal(boundedMask.querySelector('[data-v3-composer-confirm]').disabled, false, 'removing the extra material restores the caller-approved limit');
boundedMask.querySelector('[data-v3-composer-confirm]').click();
await flush();
assert.deepEqual(validatedCommit.package.image_library_ids, [1], 'the valid retained material reaches the caller without a hidden truncation');

mod.openContentComposer({
  title: '同步失败可重试', value: { content_text: '保留草稿' },
  onConfirm: () => { throw new Error('调用方草稿更新失败'); },
});
await flush();
const rejectedMask = document.querySelector('[data-v3-content-composer]');
rejectedMask.querySelector('[data-v3-composer-confirm]').click();
await flush();
assert.ok(document.querySelector('[data-v3-content-composer]'), 'a synchronous caller failure keeps the editor open');
assert.match(rejectedMask.textContent, /应用内容失败：调用方草稿更新失败/);
assert.equal(rejectedMask.querySelector('[data-v3-composer-confirm]').disabled, false, 'a synchronous caller failure clears the confirming lock for retry');
assert.equal(rejectedMask.querySelector('[data-v3-composer-text]').value, '保留草稿', 'caller failure preserves the local draft');
rejectedMask.querySelector('[data-v3-composer-cancel]').click();

let resolveStaleSelection;
let staleSelectionCommit = 0;
mod.openContentComposer({
  title: '关闭后的选择结果', value: { content_text: '保留现有草稿' }, materialKinds: ['image'],
  selectMaterials: () => new Promise((resolve) => { resolveStaleSelection = resolve; }),
  onConfirm: () => { staleSelectionCommit += 1; },
});
await flush();
const closingMask = document.querySelector('[data-v3-content-composer]');
closingMask.querySelector('[data-v3-composer-add="image"]').click();
await flush();
assert.match(closingMask.textContent, /正在选择素材/, 'a pending scoped selector visibly owns the local composer session');
closingMask.querySelector('[data-v3-composer-cancel]').click();
resolveStaleSelection([{ source: 'media-library', kind: 'image', id: 33, label: '晚到图片' }]);
await flush();
assert.equal(document.querySelector('[data-v3-content-composer]'), null, 'a closed composer never reopens from a late scoped selection');
assert.equal(staleSelectionCommit, 0, 'a late selector result never calls the caller after its composer was closed');

mod.openContentComposer({
  title: '选择器类型边界', value: { content_text: '保留原选择' }, materialKinds: ['image'],
  selectMaterials: async () => [{ source: 'other-source', kind: 'image', id: 33, label: '错误来源' }],
  onConfirm: () => { throw new Error('invalid picker selection must not confirm'); },
});
await flush();
const mismatchMask = document.querySelector('[data-v3-content-composer]');
mismatchMask.querySelector('[data-v3-composer-add="image"]').click();
await flush();
assert.match(mismatchMask.textContent, /不匹配的来源或类型/, 'a selector source or kind mismatch is explicit and retains the existing draft');
assert.equal(mismatchMask.querySelector('[data-v3-composer-confirm]').disabled, false, 'a rejected picker result does not lock a valid unchanged draft');
mismatchMask.querySelector('[data-v3-composer-cancel]').click();
dom.window.close();
console.log('content composer: stable IME text, caller validation, material limits, selection lifecycle, retry, readonly presenter, and focus PASS');
