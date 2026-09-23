import assert from 'node:assert/strict';
import {JSDOM} from 'jsdom';

const baseURL = String(process.env.AICRM_MEMBER_GRID_JOURNEY_BASE_URL || '').replace(/\/$/, '');
const token = String(process.env.AICRM_MEMBER_GRID_JOURNEY_SHARE_TOKEN || '');
if (!baseURL || !token) throw new Error('public summary journey requires base URL and share token');

const pause = (milliseconds = 0) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function eventually(predicate, message) {
  const deadline = Date.now() + 4000;
  let last;
  while (Date.now() < deadline) {
    try {
      last = await predicate();
      if (last) return last;
    } catch (error) {
      last = error;
    }
    await pause(15);
  }
  throw new Error(`${message}: ${last instanceof Error ? last.message : String(last || 'not ready')}`);
}
function installBrowserGaps(window) {
  window.HTMLElement.prototype.scrollIntoView ||= () => {};
  delete window.IntersectionObserver;
}
function click(window, element, label) {
  assert.ok(element, `${label} must exist`);
  element.dispatchEvent(new window.MouseEvent('click', {bubbles: true, cancelable: true}));
}
function namedTab(document, name) {
  return Array.from(document.querySelectorAll('[data-view-id]')).find((element) => element.textContent.trim().includes(name));
}

const response = await fetch(new URL(`/shared/service-period-member-grid#${encodeURIComponent(token)}`, `${baseURL}/`));
assert.equal(response.status, 200, 'public member grid must be served');
const dom = new JSDOM(await response.text(), {runScripts: 'outside-only', url: new URL(`/shared/service-period-member-grid#${encodeURIComponent(token)}`, `${baseURL}/`).toString()});
const {window} = dom;
const {document} = window;
installBrowserGaps(window);
window.fetch = (input, init) => {
  const target = typeof input === 'string' ? input : input.url;
  return fetch(new URL(target, window.location.href), init);
};
const scripts = Array.from(document.querySelectorAll('script[src]')).map((element) => element.getAttribute('src'));
assert.deepEqual(scripts, [
  '/service-period-member-grid-assets/member_grid_host.js',
  '/service-period-member-grid-assets/member_grid_state.js',
  '/service-period-member-grid-assets/member_grid.js',
], 'public Host must precede frozen scripts');
for (const src of scripts) {
  const scriptResponse = await fetch(new URL(src, window.location.href));
  assert.equal(scriptResponse.status, 200, `script ${src} must be served`);
  window.eval(await scriptResponse.text());
}

const rows = () => document.querySelectorAll('#spGridBody tr[data-record-id]').length;
const summary = () => document.getElementById('spResultSummary')?.textContent.trim();
await eventually(() => rows() >= 1, 'first cursor page');
if (rows() < 3) document.getElementById('spGridScroll').dispatchEvent(new window.Event('scroll', {bubbles: true}));
await eventually(() => rows() === 3, 'final cursor page with two rows');
await eventually(() => summary() === '当前显示 3 行', 'final cursor visible-row summary');

click(window, namedTab(document, '分组末页视图'), 'grouped saved view');
await eventually(() => document.querySelector('[data-group-key]'), 'group header');
await eventually(() => rows() >= 1, 'grouped first cursor page');
if (rows() < 3) document.getElementById('spGridScroll').dispatchEvent(new window.Event('scroll', {bubbles: true}));
await eventually(() => rows() === 3, 'grouped final cursor page');
await eventually(() => summary() === '当前显示 3 行', 'grouped visible-row summary');
click(window, document.querySelector('[data-group-key]'), 'collapse group');
await eventually(() => rows() === 0, 'collapsed rows');
await eventually(() => summary() === '当前显示 0 行', 'collapsed visible-row summary');
click(window, await eventually(() => document.querySelector('[data-group-key]'), 'collapsed group header'), 'expand group');
await eventually(() => rows() === 3, 'expanded rows');
await eventually(() => summary() === '当前显示 3 行', 'expanded visible-row summary');

console.log('frozen member-grid public summary journey passed');
