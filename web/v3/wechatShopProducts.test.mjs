import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
const bundle = await build({ entryPoints: ['web/v3/wechatShopProducts.ts'], bundle: true, write: false, format: 'iife', globalName: 'ShopUI', target: 'es2022' });
let submits = 0; let resolveSubmit;
const idle = { id: 0, state: 'idle', added: 0, existing: 0, failed: 0 };
const dom = new JSDOM('<body data-page="products"><header class="admin-topbar"><h1>商品管理</h1></header><main id="stage"></main></body>', { url: 'https://test.invalid/admin/wechat-pay/products', runScripts: 'outside-only', pretendToBeVisual: true });
const w = dom.window;
w.Headers = Headers; w.Response = Response; w.Request = Request;
w.fetch = async (url, options = {}) => {
  if (options.method === 'POST') { submits++; return new Promise(resolve => { resolveSubmit = () => resolve(new Response(JSON.stringify({ ...idle, id: 1, state: 'queued' }), { headers: { 'Content-Type': 'application/json' } })); }); }
  return new Response(JSON.stringify(idle), { headers: { 'Content-Type': 'application/json' } });
};
w.eval(bundle.outputFiles[0].text + ';window.ShopUI = ShopUI;'); w.ShopUI.installShopRefresh();
await new Promise(resolve => setTimeout(resolve, 20));
const button = w.document.querySelector('[data-page-header-action="shop-refresh"]');
assert.equal(button.textContent, '刷新微信小店商品'); button.click(); button.click();
assert.equal(submits, 1, 'busy clicks must not send another refresh');
resolveSubmit(); await new Promise(resolve => setTimeout(resolve, 20));
assert.equal(w.document.querySelector('[data-page-header-action="shop-refresh"]').disabled, true);
assert.match(w.document.querySelector('[role="status"]').textContent, /刷新中/);
w.ShopUI.installShopRefresh(); assert.equal(w.document.querySelectorAll('[data-shop-refresh-status]').length, 1);
w.document.body.innerHTML = '<section id="product-sale"></section><section id="product-media"></section><section id="product-action"></section><section id="product-wecom"></section><section data-distribution-policy></section><nav><a href="#product-sale">基础</a><a href="#product-media">素材</a><a href="#product-action">购买动作</a><a href="#product-wecom">标签</a><a href="#product-push">外推</a></nav>';
w.ShopUI.presentShopEditor(true); w.ShopUI.presentShopEditor(true);
assert.equal(w.document.querySelector('[data-distribution-policy]'), null);
assert.equal(w.document.querySelectorAll('nav a').length, 2);
assert.equal(w.document.querySelector('#product-action').hidden, true);
assert.equal(w.document.querySelectorAll('[data-shop-source]').length, 1);
dom.window.close(); console.log('WeChat Shop product refresh click, replay, status and editor boundaries passed');
