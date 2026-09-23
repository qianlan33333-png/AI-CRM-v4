import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM} from 'jsdom';

const html = await readFile(process.argv[2], 'utf8');
const signedURL = 'https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay&out_trade_no=M-alipay-7&sign=test';
const calls = [];
let copied = '';
let statusReads = 0;
const dom = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
const {window} = dom;
Object.defineProperty(window.navigator, 'userAgent', {value: 'MicroMessenger'});
Object.defineProperty(window.navigator, 'clipboard', {value: {async writeText(value) {copied = value;}}});
Object.defineProperty(window.crypto, 'randomUUID', {value: () => 'checkout-alipay-7'});
window.fetch = async (url, options = {}) => {
  const method = options.method || 'GET';
  calls.push({url: String(url), method, body: options.body});
  const answer = (body, status = 200) => ({ok: true, status, async json() {return body;}});
  if (url === '/api/v1/wechat-pay/checkout-session') return answer({checkout_session_binding: 'a'.repeat(43), can_create_checkout: true});
  if (String(url).startsWith('/api/v1/wechat-pay/purchase-status')) return answer({purchase_state: 'available', can_purchase: true});
  if (String(url).startsWith('/api/h5/coupons/available')) return answer({items: []});
  if (url === '/api/v1/wechat-pay/checkouts' && method === 'POST') return answer({merchant_order_no: 'M-alipay-7'}, 202);
  if (url === '/api/v1/wechat-pay/checkouts/M-alipay-7') {
    statusReads++;
    return answer(statusReads === 3 ? {status: 'paid', completion_action: {state: 'none'}} : {
      status: 'awaiting_payment', ready: true, amount_minor: 990, currency: 'CNY',
      handoff: {redirectUrl: signedURL},
    });
  }
  throw new Error(`unexpected request: ${method} ${url}`);
};
const settle = async () => {for (let i = 0; i < 12; i++) await new Promise(resolve => setTimeout(resolve, 0));};
window.eval(window.document.querySelector('script:last-of-type').textContent);
await settle();
const alipay = window.document.querySelector('input[name=paymentMethod][value=alipay]');
assert.ok(alipay, 'enabled Alipay option is rendered');
alipay.click();
window.document.getElementById('buy').click();
await settle();
const posts = () => calls.filter(call => call.method === 'POST');
assert.equal(posts().length, 1, 'one order is created');
assert.equal(JSON.parse(posts()[0].body).provider, 'alipay');
assert.equal(JSON.parse(posts()[0].body).channel, 'alipay_wap');
assert.equal(window.document.getElementById('alipayGuide').hidden, false);
assert.equal(window.document.getElementById('alipayPaymentURL').value, signedURL);
window.document.getElementById('alipayCopy').click();
await settle();
assert.equal(copied, signedURL, 'copy preserves the signed order URL');
window.document.getElementById('alipayPaid').click();
await settle();
assert.equal(posts().length, 1, 'unconfirmed payment does not create another order');
assert.equal(window.document.getElementById('alipayGuide').hidden, false);
window.document.getElementById('alipayPaid').click();
await settle();
assert.equal(posts().length, 1);
assert.equal(window.document.getElementById('alipayGuide').hidden, true);
assert.equal(window.document.getElementById('buy').textContent, '已购买');
const storageKey = 'aicrm.checkout.tab.v2:7:standard';
const saved = window.sessionStorage.getItem(storageKey);
assert.equal(JSON.parse(saved).merchant_order_no, 'M-alipay-7');

const reload = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
const after = reload.window;
Object.defineProperty(after.navigator, 'userAgent', {value: 'MicroMessenger'});
after.sessionStorage.setItem(storageKey, saved);
const reloadCalls = [];
after.fetch = async (url, options = {}) => {
  reloadCalls.push({url: String(url), method: options.method || 'GET'});
  const answer = (body) => ({ok: true, status: 200, async json() {return body;}});
  if (url === '/api/v1/wechat-pay/checkout-session') return answer({checkout_session_binding: 'a'.repeat(43), can_create_checkout: false});
  if (String(url).startsWith('/api/v1/wechat-pay/purchase-status')) return answer({purchase_state: 'available', can_purchase: true});
  if (url === '/api/v1/wechat-pay/checkouts/M-alipay-7') return answer({status: 'paid', completion_action: {state: 'none'}});
  throw new Error(`unexpected reload request: ${url}`);
};
after.eval(after.document.querySelector('script:last-of-type').textContent);
await settle();
assert.equal(after.document.getElementById('buy').textContent, '已购买');
assert.equal(reloadCalls.some(call => call.method === 'POST'), false, 'reload must not create another order');
