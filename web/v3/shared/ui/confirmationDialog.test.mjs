import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><button id="trigger">归档</button>', { url: 'https://test.invalid', pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, KeyboardEvent: dom.window.KeyboardEvent });
const bundle = await build({
  stdin: { contents: "export * from './web/v3/shared/ui/confirmationDialog';", resolveDir: process.cwd(), sourcefile: 'confirmation-dialog-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const tick = () => new Promise((resolve) => setTimeout(resolve, 0));
const trigger = document.getElementById('trigger');

trigger.focus();
const required = mod.openConfirmationDialog({
  title: '归档记录', description: '归档后不可编辑。', confirmLabel: '确认归档', tone: 'danger',
  reason: { required: true, label: '归档原因' },
});
await tick();
let dialog = document.querySelector('[data-v3-confirmation-dialog]');
const textarea = dialog.querySelector('textarea');
const confirm = dialog.querySelector('[data-v3-confirmation-confirm]');
assert.equal(document.activeElement, textarea, 'required reason receives initial focus');
confirm.click();
assert.equal(dialog.isConnected, true, 'empty required reason cannot resolve or close the dialog');
assert.match(dialog.querySelector('[data-v3-confirmation-status]').textContent, /请填写确认原因/);
textarea.value = '重复联系人';
textarea.dispatchEvent(new dom.window.Event('input', { bubbles: true }));
confirm.click();
confirm.click();
assert.deepEqual(await required, { confirmed: true, reason: '重复联系人' }, 'confirm returns one trimmed, caller-owned temporary reason');
assert.equal(document.querySelector('[data-v3-confirmation-dialog]'), null, 'confirmed dialog is removed once');
assert.equal(document.activeElement, trigger, 'confirm restores the original trigger focus');

trigger.focus();
const structured = mod.openConfirmationDialog({
  title: '登记追回', description: '金额单位为分。', confirmLabel: '确认登记',
  fields: [
    { name: 'amount_minor', label: '追回金额（分）', kind: 'positive-integer', required: true },
    { name: 'evidence_reference', label: '凭证参考', kind: 'text', required: true },
  ],
});
await tick();
dialog = document.querySelector('[data-v3-confirmation-dialog]');
const amount = dialog.querySelector('input[name="amount_minor"]');
const evidence = dialog.querySelector('input[name="evidence_reference"]');
assert.equal(document.activeElement, amount, 'the first structured field receives initial focus');
amount.value = '0';
dialog.querySelector('[data-v3-confirmation-confirm]').click();
assert.match(dialog.querySelector('[data-v3-confirmation-status]').textContent, /正整数后再继续/);
amount.value = '1200'; evidence.value = 'receipt-1200';
dialog.querySelector('[data-v3-confirmation-confirm]').click();
assert.deepEqual(await structured, { confirmed: true, values: { amount_minor: '1200', evidence_reference: 'receipt-1200' } }, 'structured fields return caller-owned temporary values');

trigger.focus();
const cancelled = mod.openConfirmationDialog({ title: '删除分组', description: '仅删除空分组。', confirmLabel: '确认删除', tone: 'danger' });
await tick();
dialog = document.querySelector('[data-v3-confirmation-dialog]');
const escape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' });
dialog.querySelector('[data-v3-confirmation-cancel]').dispatchEvent(escape);
assert.equal(escape.defaultPrevented, true, 'plain Escape is consumed by the topmost confirmation dialog');
assert.deepEqual(await cancelled, { confirmed: false }, 'Escape cancels without a result payload');
assert.equal(document.activeElement, trigger, 'cancel restores trigger focus');

for (const [name, options] of [['readonly', { readonly: true }], ['busy', { busy: true }]]) {
  trigger.focus();
  const pending = mod.openConfirmationDialog({ title: '不可执行', description: '当前不能提交。', confirmLabel: '确认', ...options });
  await tick();
  dialog = document.querySelector('[data-v3-confirmation-dialog]');
  assert.equal(dialog.querySelector('[data-v3-confirmation-confirm]').disabled, true, `${name} state disables confirm`);
  dialog.querySelector('[data-v3-confirmation-cancel]').click();
  assert.deepEqual(await pending, { confirmed: false }, `${name} state still permits a safe cancel`);
}

dom.window.close();
console.log('confirmation dialog: reason validation, single confirm, cancel/Escape, readonly/busy and focus restore PASS');
