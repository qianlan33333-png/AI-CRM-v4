import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const dom = new JSDOM('<!doctype html><button id="trigger">选择渠道</button>', { url: 'https://test.invalid', pretendToBeVisual: true });
Object.assign(globalThis, { window: dom.window, document: dom.window.document, Element: dom.window.Element, HTMLElement: dom.window.HTMLElement, KeyboardEvent: dom.window.KeyboardEvent, Event: dom.window.Event, DOMException: dom.window.DOMException, AbortController: dom.window.AbortController });
const bundle = await build({ stdin: { contents: "export { openChannelPicker } from './web/v3/shared/ui/channelPickerAdapter';", resolveDir: process.cwd(), sourcefile: 'channel-picker-test.ts' }, bundle: true, format: 'esm', platform: 'node', write: false, target: 'es2020' });
const { openChannelPicker } = await import(`data:text/javascript;base64,${Buffer.from(bundle.outputFiles[0].text).toString('base64')}`);
const waitFor = async (check, message) => {
  for (let attempt = 0; attempt < 80; attempt += 1) { const value = check(); if (value) return value; await new Promise((resolve) => setTimeout(resolve, 10)); }
  throw new Error(message);
};
const old = { id: 17, channel_name: '原渠道', channel_code: 'old', status: 'active' };
const target = { id: 61, channel_name: '99 元付款+黄小璨企微', channel_code: '2233444', status: 'active' };
const requests = [];
const loadPage = async ({ query, cursor }) => {
  requests.push({ query, cursor });
  if (query) return { items: [target] };
  if (cursor === 'page-two') return { items: [target] };
  return { items: Array.from({ length: 50 }, (_, index) => ({ id: index + 1, channel_name: `渠道 ${index + 1}`, status: 'active' })), nextCursor: 'page-two' };
};
const trigger = document.querySelector('#trigger');
trigger.focus();
let committed;
openChannelPicker({ selected: old, loadPage, onCommit: (item) => { committed = item; } });
let mask = await waitFor(() => document.querySelector('[data-v3-selection-session="channel"]'), 'channel dialog did not open');
await waitFor(() => mask.querySelector('[data-v3-channel-more]')?.hidden === false, 'first 50 channels did not expose the continuation');
assert.equal(mask.querySelector('[data-v3-channel-key$=":61"]'), null, 'later channel starts outside the first page');
mask.querySelector('[data-v3-channel-more]').click();
await waitFor(() => mask.querySelector('[data-v3-channel-key$=":61"]'), 'channel #61 did not load from the next page');
assert.deepEqual(requests.at(-1), { query: '', cursor: 'page-two' }, 'load more retains the empty submitted query');
mask.querySelector('[data-v3-channel-key$=":61"]').click();
mask.querySelector('[data-v3-channel-cancel]').click();
assert.equal(committed, undefined, 'cancel discards a selection from a later page');
assert.equal(document.activeElement, trigger, 'cancel restores focus to the Product opener');

openChannelPicker({ selected: old, loadPage, onCommit: (item) => { committed = item; } });
mask = await waitFor(() => document.querySelector('[data-v3-selection-session="channel"]'), 'reopened dialog did not mount');
const search = mask.querySelector('[data-v3-picker-search-input]');
search.value = '黄小璨';
search.dispatchEvent(new Event('input', { bubbles: true }));
mask.querySelector('[data-v3-channel-search]').click();
await waitFor(() => mask.querySelector('[data-v3-channel-key$=":61"]'), 'server name search did not find the later channel');
assert.deepEqual(requests.at(-1), { query: '黄小璨', cursor: undefined }, 'search restarts at the first page');
mask.querySelector('[data-v3-channel-key$=":61"]').click();
mask.querySelector('[data-v3-channel-confirm]').click();
assert.equal(committed?.id, 61, 'confirmation returns the selected later channel ID');
assert.equal(document.querySelector('[data-v3-selection-session="channel"]'), null);

openChannelPicker({ selected: target, loadPage: async () => { throw new Error('渠道目录读取失败（HTTP 403）'); }, onCommit: () => { throw new Error('forbidden dialog must not commit'); } });
mask = await waitFor(() => document.querySelector('[data-v3-selection-session="channel"]'), 'forbidden dialog did not mount');
await waitFor(() => mask.textContent.includes('无权读取渠道目录'), '403 did not lock the current selection');
assert.equal(mask.querySelector('[data-v3-channel-confirm]').disabled, true);
mask.querySelector('[data-v3-channel-cancel]').click();
dom.window.close();
console.log('channel picker: beyond-50 pagination, name search, cancel, commit and 403 PASS');
