import { JSDOM } from 'jsdom';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

const directory = path.dirname(fileURLToPath(import.meta.url));
const bridge = fs.readFileSync(path.join(directory, 'static/admin_console/config_adminops_bridge.js'), 'utf8');
const token = 'a'.repeat(43);
const calls = [];
const wait = () => new Promise((resolve) => setTimeout(resolve, 0));
const response = (body, status = 200) => ({
  ok: status >= 200 && status < 300, status,
  json: async () => body,
});
const html = '<table><tbody><tr><td>runtime-diagnostics</td><td></td><td></td><td><span style="cursor:pointer"><span></span></span></td></tr></tbody></table><button>检查</button><button>保存</button>';
const dom = new JSDOM(html, {
  url: 'https://test.invalid/admin/configDetail.html?cat=runtime-diagnostics',
  runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.document.cookie = `aicrm_admin_csrf=${'c'.repeat(43)}`;
    window.alert = () => {};
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      calls.push({ path: url.pathname, method: init.method || 'GET', headers: new Headers(init.headers), body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (url.pathname.endsWith('/runtime-diagnostics') && (!init.method || init.method === 'GET')) return response({ admin_action_tokens: { check: token, enabled: `${token}e`, settings: `${token}s` }, enabled: true });
      if (url.pathname.endsWith('/runtime-diagnostics/check') && init.method === 'POST') return response({ ok: true, message: '检查通过' });
      return response({ error: 'unexpected' }, 500);
    };
  },
});
dom.window.eval(bridge);
let frozenControllerCalls = 0;
dom.window.document.addEventListener('click', () => { frozenControllerCalls += 1; }, true);
dom.window.document.querySelector('button').click();
await wait();
await wait();
if (frozenControllerCalls !== 0) throw new Error('host bridge did not prevent the unchanged donor check controller');
if (calls.length !== 2 || calls[0].path !== '/api/admin/config/categories/runtime-diagnostics' || calls[1].path !== '/api/admin/config/categories/runtime-diagnostics/check' || calls[1].method !== 'POST' || calls[1].body.admin_action_token !== token || calls[1].headers.get('X-CSRF-Token') !== 'c'.repeat(43)) throw new Error(`check adapter request mismatch: ${JSON.stringify(calls)}`);

const saveCalls = [];
const saveAdapterDom = new JSDOM('<button>保存</button>', {
  url: 'https://test.invalid/admin/configDetail.html?cat=runtime-diagnostics', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.document.cookie = `aicrm_admin_csrf=${'d'.repeat(43)}`;
    window.alert = () => {};
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      saveCalls.push({ path: url.pathname, method: init.method || 'GET', headers: new Headers(init.headers), body: init.body ? JSON.parse(String(init.body)) : undefined });
      if (url.pathname.endsWith('/runtime-diagnostics') && (!init.method || init.method === 'GET')) return response({ admin_action_tokens: { settings: token }, enabled: true });
      if (url.pathname.endsWith('/runtime-diagnostics/settings') && init.method === 'PUT') return response({ ok: true, saved: true });
      return response({ error: 'unexpected' }, 500);
    };
  },
});
saveAdapterDom.window.eval(bridge);
let frozenSaveControllerCalls = 0;
saveAdapterDom.window.document.addEventListener('click', () => { frozenSaveControllerCalls += 1; }, true);
saveAdapterDom.window.document.querySelector('button').click();
await wait();
await wait();
if (frozenSaveControllerCalls !== 0) throw new Error('host bridge did not prevent the unchanged donor readonly Save controller');
if (saveCalls.length !== 2 || saveCalls[1].path !== '/api/admin/config/categories/runtime-diagnostics/settings' || saveCalls[1].method !== 'PUT' || JSON.stringify(saveCalls[1].body) !== JSON.stringify({ settings: {}, admin_action_token: token }) || saveCalls[1].headers.get('X-CSRF-Token') !== 'd'.repeat(43) || !saveCalls[1].headers.get('Idempotency-Key')) throw new Error(`save adapter persistence request mismatch: ${JSON.stringify(saveCalls)}`);

const saveDom = new JSDOM('<button>保存</button>', { url: 'https://test.invalid/admin/configDetail.html?cat=app-settings', runScripts: 'outside-only' });
saveDom.window.eval(bridge);
let unchangedSaveControllerCalls = 0;
saveDom.window.document.addEventListener('click', () => { unchangedSaveControllerCalls += 1; }, true);
saveDom.window.document.querySelector('button').click();
if (unchangedSaveControllerCalls !== 1) throw new Error('bridge intercepted the donor app-settings Save controller');

dom.window.close();
saveDom.window.close();
saveAdapterDom.window.close();
console.log('config_adminops_bridge: PASS');
