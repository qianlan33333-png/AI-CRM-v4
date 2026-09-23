import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM, VirtualConsole} from 'jsdom';

const html = await readFile(process.argv[2], 'utf8');
const errors = [];
const calls = [];
const virtualConsole = new VirtualConsole();
virtualConsole.on('jsdomError', error => errors.push(error));
const page = new JSDOM(html, {
  url: 'https://example.test/pay/book',
  runScripts: 'dangerously',
  virtualConsole,
  beforeParse(window) {
    Object.defineProperty(window.navigator, 'userAgent', {value: 'MicroMessenger'});
    window.fetch = async (url, options = {}) => {
      const path = String(url);
      calls.push({path, options});
      const body = path === '/api/v1/wechat-pay/checkout-session'
        ? {checkout_session_binding: 'a'.repeat(43), can_create_checkout: true}
        : path.startsWith('/api/v1/wechat-pay/purchase-status')
          ? {purchase_state: 'available', can_purchase: true}
          : path.startsWith('/api/h5/coupons/available')
            ? {items: []}
            : path === '/api/v1/wechat-pay/checkouts' && options.method === 'POST'
              ? {merchant_order_no: 'M-shipping-1'}
              : path === '/api/v1/wechat-pay/checkouts/M-shipping-1'
                ? {status: 'paid', amount_minor: 990, currency: 'CNY', completion_action: {state: 'unavailable'}}
                : assert.fail(`unexpected checkout request: ${path}`);
      return {ok: true, status: 200, async json() { return body; }};
    };
  },
});
const {document, Event} = page.window;
const settle = async () => {
  for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve));
};
await settle();
const province = document.getElementById('province');
const city = document.getElementById('city');
const district = document.getElementById('district');

assert.ok(province && city && district, 'shipping address controls must render');
assert.equal(errors.length, 0, `checkout script failed: ${errors.map(error => error.message).join('; ')}`);
assert.ok(province.options.length > 2, 'province list must contain real options');
for (const option of [...province.options].slice(1)) {
  assert.ok(option.value && option.textContent.trim(), 'province option must have both code and name');
}

province.value = province.options[1].value;
province.dispatchEvent(new Event('change', {bubbles: true}));
assert.equal(city.disabled, false);
assert.ok(city.options.length > 1 && city.options[1].textContent.trim());
city.value = city.options[1].value;
city.dispatchEvent(new Event('change', {bubbles: true}));
assert.equal(district.disabled, false);
assert.ok(district.options.length > 1 && district.options[1].textContent.trim());

province.value = province.options[2].value;
province.dispatchEvent(new Event('change', {bubbles: true}));
assert.equal(city.value, '', 'changing province must clear city');
assert.equal(district.value, '', 'changing province must clear district');
assert.equal(district.disabled, true);

document.getElementById('mobile').value = '13812345678';
document.getElementById('buy').dispatchEvent(new Event('click', {bubbles: true}));
await settle();
assert.match(document.getElementById('status').textContent, /请完整填写收货信息/);
assert.equal(calls.filter(call => call.options.method === 'POST').length, 0, 'incomplete address must not create an order');

city.value = city.options[1].value;
city.dispatchEvent(new Event('change', {bubbles: true}));
district.value = district.options[1].value;
document.getElementById('recipientName').value = '测试收件人';
document.getElementById('detailAddress').value = '测试路 1 号';
document.getElementById('buy').dispatchEvent(new Event('click', {bubbles: true}));
await settle();
const posts = calls.filter(call => call.path === '/api/v1/wechat-pay/checkouts' && call.options.method === 'POST');
assert.equal(posts.length, 1, 'complete address should create one order');
const payload = JSON.parse(posts[0].options.body);
assert.equal(payload.contact_collection_level, 'shipping_address');
assert.equal(payload.mobile, '+8613812345678');
assert.equal(payload.recipient_name, '测试收件人');
assert.equal(payload.province_code, province.value);
assert.equal(payload.city_code, city.value);
assert.equal(payload.district_code, district.value);
assert.ok(payload.province_name && payload.city_name && payload.district_name);
assert.equal(payload.detail_address, '测试路 1 号');
page.window.close();
