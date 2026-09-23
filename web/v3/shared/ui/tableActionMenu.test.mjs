import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM(`<!doctype html><head><style>[hidden] { display:none !important; }</style></head><table><tbody><tr><td id="cell"><div id="actions"><button id="edit">编辑</button><button id="data">数据</button><button id="share">分享</button><button id="copy">复制</button><button id="disable">停用</button><button id="delete" disabled>删除</button></div></td></tr></tbody></table>`, { url: 'https://test.invalid', pretendToBeVisual: true });
Object.assign(globalThis, {
  window: dom.window, document: dom.window.document, Node: dom.window.Node,
  HTMLElement: dom.window.HTMLElement, HTMLButtonElement: dom.window.HTMLButtonElement,
  HTMLAnchorElement: dom.window.HTMLAnchorElement, KeyboardEvent: dom.window.KeyboardEvent,
});
const bundle = await build({
  stdin: { contents: "export * from './web/v3/shared/ui/tableActionMenu';", resolveDir: process.cwd(), sourcefile: 'table-action-menu-test.ts' },
  bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020',
});
const mod = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);

const container = document.getElementById('actions');
const share = document.getElementById('share');
const copy = document.getElementById('copy');
const disabledDelete = document.getElementById('delete');
let copied = 0;
copy.addEventListener('click', () => { copied += 1; });
const mounted = mod.mountTableActionMenu(container, { owner: 'product-list', primaryCount: 2 });
assert.ok(mounted, 'a six-action row receives the shared overflow control');
assert.equal(container.querySelectorAll(':scope > button').length, 0, 'source controls move under one stable presentation root');
assert.equal(container.querySelectorAll('[data-table-action-menu-trigger="product-list"]').length, 1, 'the row exposes one overflow trigger');
assert.equal(container.querySelector('#edit'), document.getElementById('edit'), 'primary source action identity is retained');
const trigger = container.querySelector('[data-table-action-menu-trigger="product-list"]');
const panel = document.querySelector('[data-table-action-menu-panel="product-list"]');
assert.equal(panel.hidden, true, 'overflow actions start closed');
assert.equal(disabledDelete.disabled, true, 'disabled source action remains disabled after being rehomed');
Object.defineProperties(dom.window, {
  innerWidth: { configurable: true, value: 640 },
  innerHeight: { configurable: true, value: 768 },
});
trigger.getBoundingClientRect = () => ({ left: 500, right: 560, top: 740, bottom: 766, width: 60, height: 26 });
panel.getBoundingClientRect = () => ({ left: 0, right: 160, top: 0, bottom: 180, width: 160, height: 180 });
const visiblyOpen = () => !panel.hidden && dom.window.getComputedStyle(panel).display !== 'none' && dom.window.getComputedStyle(panel).visibility !== 'hidden';

trigger.focus();
trigger.click();
assert.equal(panel.hidden, false, 'trigger opens the overflow panel');
assert.equal(visiblyOpen(), true, 'the opened panel has computed visibility even with the shell hidden rule');
assert.equal(panel.dataset.tableActionMenuPlacement, 'up', 'a lower-edge trigger opens the overflow panel upward');
assert.match(panel.style.bottom, /px$/, 'upward placement anchors the panel above its trigger');
assert.equal(panel.style.top, '', 'upward placement does not also pin a below-trigger top offset');
assert.match(panel.style.maxHeight, /px$/, 'the chosen viewport side bounds overflow height for scrolling');
assert.equal(document.activeElement, share, 'opening moves keyboard focus to the first available overflow action');
const escape = new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' });
document.dispatchEvent(escape);
assert.equal(escape.defaultPrevented, true, 'Escape is consumed while the action panel is open');
assert.equal(panel.hidden, true, 'Escape closes the panel');
assert.equal(visiblyOpen(), false, 'Escape removes computed visibility rather than only changing an attribute');
assert.equal(document.activeElement, trigger, 'Escape restores focus to the trigger');

trigger.click();
document.body.dispatchEvent(new dom.window.Event('pointerdown', { bubbles: true }));
assert.equal(panel.hidden, true, 'an outside pointer close does not leave an orphan popup');
const afterMenu = document.createElement('button');
afterMenu.textContent = '菜单后的焦点';
document.body.append(afterMenu);
trigger.click();
copy.focus();
await new Promise((resolve) => dom.window.setTimeout(resolve, 20));
assert.equal(panel.hidden, false, 'moving focus between overflow actions keeps the floating panel open');
copy.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Tab' }));
afterMenu.focus();
await new Promise((resolve) => dom.window.setTimeout(resolve, 20));
assert.equal(panel.hidden, true, 'Tab leaving the menu closes the detached floating panel after focus settles');
assert.equal(visiblyOpen(), false, 'Tab leave cannot retain a computed-visible panel without menu focus');
trigger.click();
copy.click();
await Promise.resolve();
assert.equal(copied, 1, 'the original owner click handler executes exactly once from the overflow panel');
assert.equal(panel.hidden, true, 'choosing an action closes the panel without changing its command semantics');

mounted.dispose();
assert.equal(document.querySelector('[data-table-action-menu-panel="product-list"]'), null, 'dispose removes the floating panel');
assert.equal(container.children.length, 6, 'dispose returns the original controls to their owner container');
assert.equal(container.querySelector('#copy'), copy, 'dispose retains original action identity and listeners');

const short = document.createElement('div');
short.append(document.createElement('button'), document.createElement('button'));
document.body.append(short);
assert.equal(mod.mountTableActionMenu(short, { owner: 'short-row', primaryCount: 2 }), undefined, 'permission-reduced rows keep their available actions directly visible');

const disabledOnly = document.createElement('div');
for (const label of ['编辑', '不可用复制', '不可用删除']) {
  const action = document.createElement('button'); action.textContent = label; action.disabled = label !== '编辑'; disabledOnly.append(action);
}
document.body.append(disabledOnly);
const disabledMenu = mod.mountTableActionMenu(disabledOnly, { owner: 'disabled-row', primaryCount: 1 });
const disabledTrigger = disabledOnly.querySelector('[data-table-action-menu-trigger="disabled-row"]');
assert.ok(disabledMenu && disabledTrigger, 'a row with disabled secondary actions retains its overflow trigger');
disabledTrigger.focus(); disabledTrigger.click();
assert.equal(document.activeElement, disabledTrigger, 'when every overflow action is disabled, opening preserves trigger focus');
document.dispatchEvent(new KeyboardEvent('keydown', { bubbles: true, cancelable: true, key: 'Escape' }));
assert.equal(disabledTrigger.getAttribute('aria-expanded'), 'false', 'Escape closes a disabled-only overflow menu through the trigger fallback');
disabledMenu.dispose();

dom.window.close();
console.log('table action menu: source actions, keyboard, Escape, outside close, focus restore and reduced-action rows PASS');
