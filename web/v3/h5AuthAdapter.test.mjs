import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/h5/auth.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/h5AuthAdapter.ts'));
assert.equal(/<script type="module" src="\.\.\/assets\/h5-/.test(page), false, 'auth page must not mount the frozen auto-OAuth controller');

async function render({ search = '?slug=growth', status = 401, ua = 'MicroMessenger' } = {}) {
  const calls = [];
  const dom = new JSDOM(page, {
    url: `https://test.invalid/h5/auth.html${search}`,
    runScripts: 'outside-only',
    beforeParse(window) {
      Object.defineProperty(window.navigator, 'userAgent', { configurable: true, value: ua });
      window.fetch = async (url) => {
        calls.push(url);
        return { ok: status === 200, status };
      };
    },
  });
  dom.window.eval(host);
  await new Promise((resolve) => setTimeout(resolve, 0));
  return { dom, calls, screen: dom.window.document.getElementById('screen') };
}

const missing = await render();
assert.equal(missing.calls.length, 1, 'session check must be read-only');
assert.match(missing.screen.textContent, /登录才能填写问卷/);
assert.match(missing.screen.textContent, /不会自动提交/);
assert.equal(missing.screen.querySelector('.auth-button')?.getAttribute('href'), '/api/h5/surveys/oauth/start?slug=growth');
assert.equal(missing.screen.querySelectorAll('.auth-button').length, 1);
assert.equal(missing.screen.textContent.includes('退出'), false);
missing.dom.window.close();

const failed = await render({ search: '?slug=growth&oauth_error=%3Csvg%3E' });
assert.equal(failed.calls.length, 0, 'callback failure must wait for an explicit new authorization click');
assert.match(failed.screen.textContent, /微信授权未完成/);
assert.equal(failed.screen.querySelector('.auth-button')?.getAttribute('href'), '/api/h5/surveys/oauth/start?slug=growth');
assert.equal(failed.dom.window.document.documentElement.outerHTML.includes('<svg>'), false, 'query text must not enter markup');
failed.dom.window.close();

const conflicted = await render({ status: 409 });
assert.match(conflicted.screen.textContent, /身份存在冲突/);
assert.equal(conflicted.screen.querySelector('.auth-button')?.hidden, true, 'conflict cannot be bypassed by OAuth retry');
conflicted.dom.window.close();

const invalid = await render({ search: '?slug=javascript%3Aalert(1)&oauth_error=1' });
assert.match(invalid.screen.textContent, /链接无效/);
assert.equal(invalid.screen.querySelector('.auth-button')?.hidden, true);
invalid.dom.window.close();

const outside = await render({ ua: 'Safari' });
assert.equal(outside.calls.length, 0);
assert.match(outside.screen.textContent, /请在微信中打开/);
assert.equal(outside.screen.querySelector('.auth-button')?.hidden, true);
outside.dom.window.close();
console.log('required Survey login gate: PASS');
