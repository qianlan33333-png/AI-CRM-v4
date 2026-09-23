import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM(`<!doctype html><button id="trigger">打开</button><section id="dialog">
  <input id="search"><textarea id="message"></textarea><select id="scope"><option>全部</option></select>
  <div id="rich" contenteditable="true"></div><button id="disabled" disabled>不可用</button>
  <button id="disabled-tab" disabled tabindex="0">禁用且带 tabindex</button><fieldset disabled><input id="fieldset-disabled"></fieldset>
  <div id="rich-plain" contenteditable="plaintext-only"></div><div id="rich-off" contenteditable="false" tabindex="-1"></div>
  <div hidden><button id="hidden">隐藏</button></div><button id="inner-trigger">选择素材</button>
  <section id="inner" hidden><input id="inner-search"></section><button id="last">确认</button>
</section>`, { url: 'https://test.invalid' });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, HTMLElement: dom.window.HTMLElement, KeyboardEvent: dom.window.KeyboardEvent });

const bundle = await build({
  stdin: { contents: "export * from './web/v3/shared/ui/selectionDialog';", resolveDir: process.cwd(), sourcefile: 'selection-dialog-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const dialog = document.getElementById('dialog');
const search = document.getElementById('search');
const trigger = document.getElementById('trigger');

assert.deepEqual(mod.focusableElements(dialog).map((node) => node.id), ['search', 'message', 'scope', 'rich', 'rich-plain', 'inner-trigger', 'last'], 'dialog focus order includes textarea/select/contenteditable and excludes hidden, disabled, and explicit negative-tabindex controls');
trigger.focus();
let closed = 0;
const controller = mod.installSelectionDialog({ dialog, search, submit() {}, close() { closed += 1; } });
await new Promise((resolve) => setTimeout(resolve, 0));
assert.equal(document.activeElement, search, 'dialog starts from its search field');

document.getElementById('last').focus();
const forward = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab' });
document.getElementById('last').dispatchEvent(forward);
assert.equal(forward.defaultPrevented, true);
assert.equal(document.activeElement, search, 'Tab from the last focusable control cycles to search');

search.focus();
const reverse = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab', shiftKey: true });
search.dispatchEvent(reverse);
assert.equal(reverse.defaultPrevented, true);
assert.equal(document.activeElement.id, 'last', 'Shift+Tab cycles from search to the last focusable control');

let composerClosed = 0;
const composer = mod.installSelectionDialog({ dialog, initialFocus: document.getElementById('message'), submit() {}, close() { composerClosed += 1; } });
const message = document.getElementById('message');
message.dispatchEvent(new dom.window.Event('compositionstart', { bubbles: true }));
const imeEscape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' });
Object.defineProperty(imeEscape, 'keyCode', { value: 229 });
message.dispatchEvent(imeEscape);
assert.equal(imeEscape.defaultPrevented, false, 'IME Escape keeps native candidate handling');
assert.equal(composerClosed, 0, 'IME Escape in a textarea cannot close the composer');
message.dispatchEvent(new dom.window.Event('compositionend', { bubbles: true }));
composer.dispose();

controller.dispose();
assert.equal(closed, 0, 'disposing a child dialog does not close its owner dialog');
assert.equal(document.activeElement, trigger, 'closing an inner dialog restores its own trigger');

const outer = mod.installSelectionDialog({ dialog, search, submit() {}, close() { closed += 1; } });
const innerTrigger = document.getElementById('inner-trigger');
const inner = document.getElementById('inner');
const innerSearch = document.getElementById('inner-search');
inner.hidden = false;
innerTrigger.focus();
let innerClosed = 0;
const innerController = mod.installSelectionDialog({ dialog: inner, search: innerSearch, submit() {}, close() { innerClosed += 1; } });
const escape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' });
innerSearch.dispatchEvent(escape);
assert.equal(escape.defaultPrevented, true, 'plain Escape is consumed by the active inner dialog');
assert.equal(innerClosed, 1, 'Escape closes the topmost dialog');
assert.equal(closed, 0, 'inner Escape does not close the parent dialog');
innerController.dispose();
outer.dispose();
dom.window.close();
console.log('selection dialog: textarea/select/contenteditable focus cycle and nested Escape PASS');
