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
assert.equal(w.document.querySelector('[role="status"]').hidden, true, 'idle must not display synchronization statistics');
assert.equal(w.document.querySelector('[role="status"]').textContent, '');
assert.equal(button.textContent, '刷新微信小店商品'); button.click(); button.click();
assert.equal(submits, 1, 'busy clicks must not send another refresh');
resolveSubmit(); await new Promise(resolve => setTimeout(resolve, 20));
assert.equal(w.document.querySelector('[data-page-header-action="shop-refresh"]').disabled, true);
assert.match(w.document.querySelector('[role="status"]').textContent, /刷新中/);
assert.equal(w.document.querySelector('[role="status"]').hidden, false);
assert.doesNotMatch(w.document.body.textContent, /上次成功|新增 \d|已存在 \d|失败 \d/, 'busy state should only show useful activity');
w.ShopUI.installShopRefresh(); assert.equal(w.document.querySelectorAll('[data-shop-refresh-status]').length, 1);
w.document.body.innerHTML = '<section id="product-sale"><h3>售卖信息</h3><div><input id="pfBuyButtonText"></div><div><select id="pfRequireMobile"></select><select id="pfContactCollectionLevel"></select></div><div><label><input data-product-alipay-enabled></label></div></section><section id="product-media"></section><section id="product-action"></section><section id="product-wecom"></section><section data-distribution-policy></section><nav><a href="#product-sale">基础</a><a href="#product-media">素材</a><a href="#product-action">购买动作</a><a href="#product-wecom">标签</a><a href="#product-push">外推</a></nav>';
w.ShopUI.presentShopEditor(true);
const mutations=new w.MutationObserver(()=>{}); mutations.observe(w.document.body,{subtree:true,childList:true}); w.ShopUI.presentShopEditor(true);
assert.equal(mutations.takeRecords().length,0,"repeated presentation must not keep mutating children and block the page"); mutations.disconnect();
assert.equal(w.document.querySelector('[data-distribution-policy]'), null);
assert.equal(w.document.querySelectorAll('nav a').length, 2);
assert.equal(w.document.querySelector('#product-action').hidden, true);
assert.equal(w.document.querySelectorAll('[data-shop-source]').length, 1);
assert.equal(w.document.getElementById("pfRequireMobile").parentElement.style.display,"none");
assert.equal(w.document.querySelector("[data-product-alipay-enabled]").closest("label").parentElement.style.display,"none");
dom.window.close();
for (const scenario of [
  { name: 'success', response: { ...idle, id: 9, state: 'succeeded', added: 2, existing: 4, failed: 0, last_success_at: '2026-10-11T14:08:24Z' }, hidden: true, message: '' },
  { name: 'retrying', response: { ...idle, id: 9, state: 'retrying', failed: 1 }, hidden: false, message: '点击重试刷新' },
  { name: 'permission', code: 403, hidden: false, message: '需要商品编辑权限' },
  { name: 'unavailable', code: 503, hidden: false, message: '检查接入配置或重试' },
  { name: 'malformed', response: { ...idle, state: 'unknown' }, hidden: false, message: '状态不完整' },
]) {
  const stateDom = new JSDOM('<body data-page="products"><header class="admin-topbar"><h1>商品管理</h1></header><main id="stage"></main></body>', { url: 'https://test.invalid/admin/wechat-pay/products', runScripts: 'outside-only', pretendToBeVisual: true });
  const sw = stateDom.window;
  sw.Headers = Headers; sw.Response = Response; sw.Request = Request;
  let posts = 0;
  sw.fetch = async (_url, options = {}) => {
    if (options.method === 'POST') { posts++; return new Response(JSON.stringify({ ...idle, id: 9, state: 'queued' }), { status: 202 }); }
    return new Response(JSON.stringify(scenario.response || {}), { status: scenario.code || 200 });
  };
  sw.eval(bundle.outputFiles[0].text + ';window.ShopUI = ShopUI;'); sw.ShopUI.installShopRefresh();
  await new Promise(resolve => setTimeout(resolve, 20));
  const status = sw.document.querySelector('[role="status"]');
  assert.equal(status.hidden, scenario.hidden, scenario.name);
  assert.ok(status.textContent.includes(scenario.message), scenario.name);
  assert.doesNotMatch(sw.document.body.textContent, /上次成功|新增 \d|已存在 \d|失败 \d/, scenario.name);
  if (!scenario.hidden) {
    sw.document.querySelector('[data-page-header-action="shop-refresh"]').click();
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(posts, 1, scenario.name + ' remains actionable');
    assert.match(status.textContent, /刷新中/);
  }
  sw.dispatchEvent(new sw.Event('pagehide'));
  stateDom.window.close();
}
console.log('WeChat Shop product refresh click, replay, status, recovery and editor boundaries passed');
