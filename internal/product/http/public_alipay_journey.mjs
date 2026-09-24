import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM} from 'jsdom';

const html = await readFile(process.argv[2], 'utf8');
const signedURL = 'https://virtual-alipay.example.test/pay?channel=wap&merchant=M-alipay-7&signature=synthetic';
const calls = [];
const records = new Map();
let copied = '';
let statusReads = 0;
const dom = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
const {window} = dom;
Object.defineProperty(window.navigator, 'userAgent', {value: 'MicroMessenger'});
Object.defineProperty(window.navigator, 'clipboard', {value: {async writeText(value) {copied = value;}}});
Object.defineProperty(window.crypto, 'randomUUID', {value: () => 'checkout-alipay-7'});
window.fetch = async (url, options = {}) => {
  const method = options.method || 'GET';
  const call = {url: String(url), method, body: options.body, headers: options.headers || {}};
  calls.push(call);
  const answer = (body, status = 200) => ({ok: status >= 200 && status < 300, status, async json() {return body;}});
  if (url === '/api/v1/wechat-pay/checkout-session') return answer({checkout_session_binding: 'a'.repeat(43), can_create_checkout: true});
  if (String(url).startsWith('/api/v1/wechat-pay/purchase-status')) return answer({purchase_state: 'available', can_purchase: true});
  if (String(url).startsWith('/api/h5/coupons/available')) return answer({items: []});
  if (url === '/api/v1/alipay/checkouts' && method === 'POST') {
    const key = call.headers['Idempotency-Key'];
    const payload = JSON.parse(call.body);
    if (!records.has(key)) records.set(key, {payload, merchant: 'M-alipay-7', creates: 0});
    const record = records.get(key);
    record.creates++;
    assert.deepEqual(payload, record.payload, 'retry replays the identical frozen checkout payload');
    if (record.creates === 1) {
      const timeout = new Error('response lost after server accepted the request');
      timeout.name = 'AbortError';
      throw timeout;
    }
    return answer({merchant_order_no: record.merchant}, 202);
  }
  if (url === '/api/v1/alipay/checkouts/M-alipay-7') {
    statusReads++;
    if (statusReads >= 3) return answer({status: 'paid', provider: 'alipay', channel: 'alipay_wap', completion_action: {state: 'none'}});
    return answer({
      status: 'awaiting_payment', provider: 'alipay', channel: 'alipay_wap', ready: true,
      amount_minor: 990, currency: 'CNY', handoff: {redirectUrl: signedURL},
    });
  }
  if (String(url).startsWith('/api/v1/wechat-pay/checkouts')) {
    assert.fail(`Alipay checkout must not use the WeChat route: ${method} ${url}`);
  }
  throw new Error(`unexpected request: ${method} ${url}`);
};
const settle = async () => {for (let i = 0; i < 16; i++) await new Promise(resolve => setTimeout(resolve, 0));};
window.eval(window.document.querySelector('script:last-of-type').textContent);
await settle();
const alipay = window.document.querySelector('input[name=paymentMethod][value=alipay]');
assert.ok(alipay, 'enabled Alipay option is rendered');
alipay.click();
window.document.getElementById('buy').click();
await settle();

const buy = window.document.getElementById('buy');
const checkpoint = () => JSON.parse(window.sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard'));
const firstPost = () => calls.find(call => call.method === 'POST');
assert.equal(records.size, 1, 'the server accepted exactly one order before the response timed out');
assert.equal(records.values().next().value.creates, 1);
assert.equal(checkpoint().merchant_order_no, '', 'unknown response retains checkpoint without inventing an order number');
assert.equal(window.document.getElementById('payableAmount').textContent, '—', 'no-order state does not show an amount awaiting payment');
assert.equal(window.document.getElementById('footerAmount').textContent, '—');
assert.match(window.document.getElementById('status').textContent, /订单创建结果尚未核实/);
assert.match(buy.textContent, /重试原请求/);
assert.equal(window.document.getElementById('alipayGuide').hidden, true, 'no payment handoff exists before order readback');

buy.click();
await settle();
const posts = () => calls.filter(call => call.method === 'POST');
assert.equal(posts().length, 2, 'one retry uses the original request');
assert.equal(posts()[1].headers['Idempotency-Key'], firstPost().headers['Idempotency-Key']);
assert.equal(posts()[0].body, posts()[1].body, 'retry body bytes are identical');
assert.equal(records.size, 1, 'idempotent retry does not create a duplicate order');
assert.equal(records.values().next().value.creates, 2);
assert.equal(checkpoint().merchant_order_no, 'M-alipay-7');
assert.equal(window.document.getElementById('alipayGuide').hidden, false);
assert.equal(window.document.getElementById('alipayPaymentURL').value, signedURL);
assert.ok(calls.some(call => call.url === '/api/v1/alipay/checkouts/M-alipay-7'));
window.document.getElementById('alipayCopy').click();
await settle();
assert.equal(copied, signedURL, 'the synthetic signed payment URL is returned to the user unchanged');

window.document.getElementById('alipayPaid').click();
await settle();
assert.equal(records.size, 1, 'readback does not create a new order');
assert.equal(window.document.getElementById('alipayGuide').hidden, false, 'pending provider status keeps the link available');
window.document.getElementById('alipayPaid').click();
await settle();
assert.equal(window.document.getElementById('alipayGuide').hidden, true);
assert.equal(buy.textContent, '已购买');
assert.equal(calls.some(call => call.url.startsWith('/api/v1/wechat-pay/checkouts/')), false, 'status and handoff never poll the WeChat route');

const saved = window.sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard');
const reload = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
const after = reload.window;
Object.defineProperty(after.navigator, 'userAgent', {value: 'MicroMessenger'});
after.sessionStorage.setItem('aicrm.checkout.tab.v2:7:standard', saved);
const reloadCalls = [];
after.fetch = async (url, options = {}) => {
  reloadCalls.push({url: String(url), method: options.method || 'GET'});
  const body = url === '/api/v1/wechat-pay/checkout-session'
    ? {checkout_session_binding: 'a'.repeat(43), can_create_checkout: false}
    : String(url).startsWith('/api/v1/wechat-pay/purchase-status')
      ? {purchase_state: 'available', can_purchase: true}
      : url === '/api/v1/alipay/checkouts/M-alipay-7'
        ? {status: 'paid', provider: 'alipay', channel: 'alipay_wap', completion_action: {state: 'none'}}
        : assert.fail(`unexpected reload request: ${url}`);
  return {ok: true, status: 200, async json() {return body;}};
};
after.eval(after.document.querySelector('script:last-of-type').textContent);
await settle();
assert.equal(after.document.getElementById('buy').textContent, '已购买');
assert.equal(reloadCalls.some(call => call.method === 'POST'), false, 'reload reads the same Alipay payment without creating another order');
assert.ok(reloadCalls.some(call => call.url === '/api/v1/alipay/checkouts/M-alipay-7'));

const desktop = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
Object.defineProperty(desktop.window.navigator, 'userAgent', {value: 'Mozilla/5.0'});
const desktopCalls = [];
desktop.window.crypto.randomUUID = () => 'checkout-alipay-page-7';
desktop.window.fetch = async (url, options = {}) => {
  desktopCalls.push({url: String(url), options});
  const body = url === '/api/v1/wechat-pay/checkout-session'
    ? {checkout_session_binding: 'a'.repeat(43), can_create_checkout: true}
    : String(url).startsWith('/api/v1/wechat-pay/purchase-status')
      ? {purchase_state: 'available', can_purchase: true}
      : String(url).startsWith('/api/h5/coupons/available')
        ? {items: []}
        : url === '/api/v1/alipay/checkouts' && options.method === 'POST'
          ? {merchant_order_no: 'M-alipay-page-7'}
          : url === '/api/v1/alipay/checkouts/M-alipay-page-7'
            ? {status: 'awaiting_payment', provider: 'alipay', channel: 'alipay_page', ready: true, amount_minor: 990, currency: 'CNY', handoff: {redirectUrl: 'https://virtual-alipay.example.test/pay?channel=page'}}
            : assert.fail(`unexpected desktop request: ${url}`);
  return {ok: true, status: options.method === 'POST' ? 202 : 200, async json() {return body;}};
};
desktop.window.eval(desktop.window.document.querySelector('script:last-of-type').textContent);
await settle();
desktop.window.document.querySelector('input[name=paymentMethod][value=alipay]').click();
desktop.window.document.getElementById('buy').click();
await settle();
assert.equal(JSON.parse(desktopCalls.find(call => call.options.method === 'POST').options.body).channel, 'alipay_page');
assert.ok(desktopCalls.some(call => call.url === '/api/v1/alipay/checkouts/M-alipay-page-7'));
assert.equal(desktop.window.document.getElementById('alipayPaymentURL').value, 'https://virtual-alipay.example.test/pay?channel=page');

const rejected = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
const rejectedWindow = rejected.window;
Object.defineProperty(rejectedWindow.navigator, 'userAgent', {value: 'MicroMessenger'});
rejectedWindow.crypto.randomUUID = () => 'checkout-alipay-rejected-7';
let rejectedPosts = 0;
rejectedWindow.fetch = async (url, options = {}) => {
  const method = options.method || 'GET';
  if (url === '/api/v1/wechat-pay/checkout-session') return {ok: true, status: 200, async json() {return {checkout_session_binding: 'a'.repeat(43), can_create_checkout: true};}};
  if (String(url).startsWith('/api/v1/wechat-pay/purchase-status')) return {ok: true, status: 200, async json() {return {purchase_state: 'available', can_purchase: true};}};
  if (String(url).startsWith('/api/h5/coupons/available')) return {ok: true, status: 200, async json() {return {items: []};}};
  if (url === '/api/v1/alipay/checkouts' && method === 'POST') {
    rejectedPosts++;
    return {ok: false, status: 409, async json() {return {code: 'conflict'};}};
  }
  return assert.fail(`unexpected deterministic-rejection request: ${method} ${url}`);
};
rejectedWindow.eval(rejectedWindow.document.querySelector('script:last-of-type').textContent);
await settle();
rejectedWindow.document.querySelector('input[name=paymentMethod][value=alipay]').click();
rejectedWindow.document.getElementById('buy').click();
await settle();
assert.equal(rejectedPosts, 1, 'the server definitively rejected one business-conflict request before issuing an order number');
assert.equal(rejectedWindow.sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard'), null, 'a confirmed no-order rejection clears the recovery marker');
assert.equal(rejectedWindow.document.getElementById('payableAmountLabel').textContent, '应付金额');
assert.equal(rejectedWindow.document.getElementById('payableAmount').textContent, '¥9.90', 'the visible amount is the editable pre-order quote, not a pending order amount');
assert.doesNotMatch(rejectedWindow.document.getElementById('status').textContent, /待确认|尚未核实/);
assert.match(rejectedWindow.document.getElementById('status').textContent, /订单未创建/);
assert.equal(rejectedWindow.document.getElementById('alipayGuide').hidden, true);
assert.match(rejectedWindow.document.getElementById('buy').textContent, /立即支付/);
assert.equal(rejectedWindow.document.getElementById('coupon').disabled, false, 'clearing the marker lets the user edit and start a new request');

const ambiguousRetry = new JSDOM(html, {url: 'https://crm.example.test/pay/course-7', runScripts: 'outside-only'});
const ambiguousWindow = ambiguousRetry.window;
Object.defineProperty(ambiguousWindow.navigator, 'userAgent', {value: 'MicroMessenger'});
const ambiguousKey = 'checkout-alipay-earlier-unknown-7';
const ambiguousPayload = {product_id: 7, product_kind: 'standard', beneficiary_selection: 'payer_self', coupon_claim_id: 0, contact_collection_level: 'none', provider: 'alipay', channel: 'alipay_wap'};
ambiguousWindow.sessionStorage.setItem('aicrm.checkout.tab.v2:7:standard', JSON.stringify({key: ambiguousKey, merchant_order_no: '', create_attempted: true, payload: ambiguousPayload, session_binding: 'a'.repeat(43)}));
let ambiguousPosts = 0;
ambiguousWindow.fetch = async (url, options = {}) => {
  const method = options.method || 'GET';
  if (url === '/api/v1/wechat-pay/checkout-session') return {ok: true, status: 200, async json() {return {checkout_session_binding: 'a'.repeat(43), can_create_checkout: true};}};
  if (String(url).startsWith('/api/v1/wechat-pay/purchase-status')) return {ok: true, status: 200, async json() {return {purchase_state: 'available', can_purchase: true};}};
  if (url === '/api/v1/alipay/checkouts' && method === 'POST') {
    ambiguousPosts++;
    assert.equal(options.headers['Idempotency-Key'], ambiguousKey);
    return {ok: false, status: 400, async json() {return {code: 'invalid_request'};}};
  }
  return assert.fail(`unexpected ambiguous-retry request: ${method} ${url}`);
};
ambiguousWindow.eval(ambiguousWindow.document.querySelector('script:last-of-type').textContent);
await settle();
ambiguousWindow.document.getElementById('buy').click();
await settle();
assert.equal(ambiguousPosts, 1);
assert.equal(JSON.parse(ambiguousWindow.sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard')).key, ambiguousKey, 'a later rejection cannot erase an earlier ambiguous create attempt');
assert.equal(ambiguousWindow.document.getElementById('payableAmount').textContent, '—');
assert.match(ambiguousWindow.document.getElementById('status').textContent, /订单创建结果尚未核实/);
assert.match(ambiguousWindow.document.getElementById('buy').textContent, /重试原请求/);
