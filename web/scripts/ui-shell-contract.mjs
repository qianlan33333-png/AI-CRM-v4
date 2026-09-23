import fs from 'node:fs';
import path from 'node:path';
import { JSDOM } from 'jsdom';

const root = path.resolve(import.meta.dirname, '..');
const read = (relative) => fs.readFileSync(path.join(root, relative), 'utf8');
const ok = (condition, message) => {
  if (!condition) throw new Error(message);
};

const nav = JSON.parse(read('src/admin/nav.json'));
ok(nav.some((item) => item.key === 'funnel' && item.label === '漏斗 / 数据看板'), '一级导航必须保留 Kimi 漏斗入口');
ok(!nav.some((item) => item.key === 'campaigns'), 'Cloud Campaign 不得占用 Kimi 一级导航');

const registry = JSON.parse(read('src/admin/registry.json'));
const campaign = registry.screens.find((item) => item.key === 'campaigns');
ok(campaign?.isNav === false, 'Cloud Campaign 只能作为隐藏路由保留');

const blockedPages = ['active', 'error', 'expired', 'pay', 'qr', 'signup'];
for (const page of blockedPages) {
  const dom = new JSDOM(`<body>${read(`src/h5/templates/${page}.html`)}</body>`);
  const banner = dom.window.document.querySelector('[data-h5-blocked]');
  ok(banner, `H5 ${page} 必须显示明确 blocked 状态`);
  ok(banner.parentElement === dom.window.document.body, `H5 ${page} blocked 提示不得塞入 44px 标题栏`);
  ok(dom.window.document.querySelector('button:not([disabled])') === null, `H5 ${page} 不得暴露可执行的伪业务按钮`);
}
const auth = new JSDOM(`<body>${read('src/h5/templates/auth.html')}</body>`);
ok(auth.window.document.querySelector('[data-h5-blocked]'), 'H5 auth 必须显示授权状态');
const authButtons = auth.window.document.querySelectorAll('button:not([disabled])');
ok(authButtons.length === 1 && authButtons[0].getAttribute('onClick') === '{{ act.authContinue }}', 'H5 auth 只允许真实微信授权按钮可执行');
ok(authButtons[0].closest('sc-if')?.getAttribute('value') === '{{ authRetry }}', 'H5 auth 仅授权失败后显示重试按钮，不拦截首次自动授权');
const loading = read('src/h5/templates/loading.html');
ok(!loading.includes('data-h5-blocked') && !loading.includes('blockedReason'), 'H5 loading 骨架屏不得渲染 blocked 横幅');
for (const page of ['qr']) {
  const html = read(`src/h5/templates/${page}.html`);
  ok(html.includes('data-h5-local-exit'), `H5 ${page} 必须提供非禁用的纯本地出口（链接/导航元素）`);
}

const all = read('src/h5/templates/all.html');
const one = read('src/h5/templates/one.html');
ok(all.includes('list="{{ questions }}"') && !all.includes('data-h5-receipt') && !all.includes('data-h5-result-link'), 'H5 整页问卷必须遍历真实题目，成功后不显示结果回执链接');
ok(one.includes('{{ qTitle }}') && one.includes('data-h5-progress') && !one.includes('data-h5-receipt') && !one.includes('data-h5-result-link'), 'H5 逐题页必须消费真实题干，成功后不显示结果回执链接');
const done = read('src/h5/templates/done.html');
ok(done.includes('data-h5-local-exit'), 'H5 done 冻结载体必须保留本地出口；完成页由 V3 Host 构建期替换');

const config = read('src/admin/templates/config.html');
ok(config.includes('open-setup-wizard') && config.includes('open-admin-access'), '接入和访问控制必须收进原配置类目表');
ok(!config.includes('setup-wizard-card') && !config.includes('admin-access-card'), '配置首屏不得再附加两张独立大卡');

const orders = read('src/admin/templates/orders.html');
ok(orders.includes('{{ orderPage.query }}') && orders.includes('{{ orderPage.clear }}'), '交易页必须保留 Kimi 查询/清空主流程');
ok(!orders.includes('共 486 条'), '交易页不得显示硬编码订单总数');

const automation = read('src/admin/sections/automationAgents.ts');
ok(automation.includes('客户管理后台 / 配置及后台') && automation.includes('grid-template-columns:226px'), '自动化 HTTP 运行态必须使用 Kimi 列表/编辑层级');

const legacyLoader = read('src/admin/legacy.ts');
ok(!legacyLoader.includes('sections/commerce'), '优惠券与 Member Grid 不得再由 sections/commerce 整页接管');
for (const page of ['couponForm', 'couponData', 'spProductData']) {
  const template = read(`src/admin/templates/${page}.html`);
  ok(template.includes('height:52px'), `${page} 必须保留 Kimi 52px 工作区头部层级`);
}
const couponFormTpl = read('src/admin/templates/couponForm.html');
ok(couponFormTpl.includes('id="coupon-target-refs"') && couponFormTpl.includes('id="option-search"') && couponFormTpl.includes('name="coupon-validity"'), '优惠券表单必须含真实 target_refs、商品选项目录与领取/有效期单选组');
const spDataTpl = read('src/admin/templates/spProductData.html');
ok(spDataTpl.includes('id="member-grid-apply"') && spDataTpl.includes('id="member-grid-share-toggle"'), '周期商品数据页必须含真实 Member Grid 筛选与分享控件');

const groupOps = read('src/admin/templates/groupops.html');
const groupOpsDetail = read('src/admin/templates/groupopsDetail.html');
ok(groupOps.includes('计划列表') && groupOps.includes('运营成员选项'), '群运营列表必须保留 Kimi 统计卡和计划表层级');
ok(groupOpsDetail.includes('grid-template-columns:226px') && groupOpsDetail.includes('Webhook 与执行投影（高级只读）'), '群运营详情必须保留 Kimi 四步层级并下沉技术投影');

console.log('ui shell contract: ok');
