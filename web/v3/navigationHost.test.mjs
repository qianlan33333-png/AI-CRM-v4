import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./navigationHost.ts', import.meta.url)));
const navigation = {
  version: 1,
  groups: [
    { key: 'overview', label: '总览', items: [{ key: 'overview', label: '经营总览', href: '/admin', active_prefixes: ['/admin'], required_permission: '' }] },
    { key: 'customers', label: '用户', items: [{ key: 'customers', label: '用户列表', href: '/admin/customers', active_prefixes: ['/admin/customers', '/admin/customerDetail.html'], required_permission: '' }] },
  ],
};
const dom = new JSDOM('<!doctype html><aside><nav class="side-nav"><div class="side-grp">旧导航</div><a class="nav-item on" href="customers.html"><svg><path d="M0 0"></path></svg><span>用户列表</span></a><a class="nav-item" href="login-access.html"><span>登录与权限</span></a></nav></aside>', {
  url: 'https://crm.example/admin/customerDetail.html?id=42', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    window.Response = Response;
    window.fetch = async (input) => {
      assert.equal(new URL(String(input), window.location.href).pathname, '/static/admin_console/admin-navigation.v3.json');
      return new Response(JSON.stringify(navigation), { status: 200, headers: { 'Content-Type': 'application/json' } });
    };
  },
});
dom.window.eval(bundle);
for (let attempt = 0; attempt < 100 && dom.window.document.querySelector('.side-nav')?.dataset.v3NavigationHost !== 'ready'; attempt += 1) await new Promise((resolve) => setTimeout(resolve, 5));
const links = [...dom.window.document.querySelectorAll('.side-nav a.nav-item')];
assert.deepEqual(links.map((link) => link.textContent?.trim()), ['经营总览', '用户列表']);
assert.equal(links[1].getAttribute('href'), '/admin/customers');
assert.equal(links[1].classList.contains('on'), true, 'active prefixes must work for detail pages');
assert.equal(links[1].querySelector('svg') !== null, true, 'existing donor icon should survive adaptation');
assert.equal(links[0].querySelector('svg') !== null, true, 'V3 overview link needs an accessible visual marker');
assert.equal(dom.window.document.body.textContent.includes('登录与权限'), false, 'a donor-only security link must stay hidden when it is not in the V3 navigation document');
dom.window.close();

const mountedAliases = [
  { path: '/admin/wechat-pay/productForm.html?id=9', expected: '商品管理' },
  { path: '/admin/productForm.html?id=9', expected: '商品管理' },
  { path: '/admin/groupops.html?history=1&id=9', expected: '群运营计划' },
  { path: '/admin/groupopsDetail.html?history=1&id=9', expected: '群运营计划' },
];
const routeNavigation = {
  version: 1,
  groups: [{
    key: 'operations', label: '运营', items: [{
      key: 'group_ops', label: '群运营计划', href: '/admin/automation-conversion/group-ops/ui',
      active_prefixes: ['/admin/automation-conversion/group-ops', '/admin/groupops.html', '/admin/groupopsDetail.html'], required_permission: '',
    }],
  }, {
    key: 'commerce', label: '交易', items: [{
      key: 'wechat_pay_products', label: '商品管理', href: '/admin/wechat-pay/products',
      active_prefixes: ['/admin/wechat-pay/products', '/admin/products.html', '/admin/wechat-pay/productForm.html', '/admin/productForm.html'], required_permission: '',
    }],
  }],
};
for (const { path, expected } of mountedAliases) {
  const aliasDOM = new JSDOM('<!doctype html><nav class="side-nav"><a class="nav-item" href="legacy.html"><span>旧入口</span></a></nav>', {
    url: 'https://crm.example' + path, runScripts: 'outside-only', pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response;
      window.fetch = async () => new Response(JSON.stringify(routeNavigation), { status: 200, headers: { 'Content-Type': 'application/json' } });
    },
  });
  aliasDOM.window.eval(bundle);
  for (let attempt = 0; attempt < 100 && aliasDOM.window.document.querySelector('.side-nav')?.dataset.v3NavigationHost !== 'ready'; attempt += 1) await new Promise((resolve) => setTimeout(resolve, 5));
  const active = aliasDOM.window.document.querySelector('.side-nav a.nav-item.on');
  assert.equal(active?.textContent?.trim(), expected, 'mounted alias must keep its navigation section active: ' + path);
  aliasDOM.window.close();
}

const restricted = new JSDOM('<!doctype html><nav class="side-nav"><a class="nav-item" href="config.html"><span>登录与权限</span></a></nav>', {
  url: 'https://crm.example/admin/config/login-access', runScripts: 'outside-only',
  beforeParse(window) {
    window.Response = Response;
    window.fetch = async () => new Response(JSON.stringify({ version: 1, groups: [{ key: 'system', label: '系统设置', items: [{ key: 'access', label: '登录与权限', href: '/admin/config/login-access', active_prefixes: ['/admin/config/login-access'], required_permission: 'api.admin_config_login_access' }] }] }), { status: 200 });
  },
});
restricted.window.eval(bundle);
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(restricted.window.document.querySelector('.side-nav')?.dataset.v3NavigationHost, undefined, 'a permission-mapped item must fail closed until an existing authorization result is available');
assert.equal(restricted.window.document.body.textContent.includes('登录与权限'), true, 'fail-closed adaptation must preserve the already server-protected donor navigation');
restricted.window.close();
console.log('navigation host: PASS');
