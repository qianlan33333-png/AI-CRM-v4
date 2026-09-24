import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM} from 'jsdom';

const source = await readFile(new URL('./public.go', import.meta.url), 'utf8');
const start = source.indexOf('</main><script>');
const end = source.indexOf('</script></body></html>`))', start);
assert.ok(start >= 0 && end > start, 'public payment template script was not found');
const script = source.slice(start + '</main><script>'.length, end)
  .replaceAll('{{if .Payment}}', '')
  .replaceAll('{{end}}', '')
  .replaceAll('{{.Product.PriceMinor}}', '990')
  .replaceAll('{{.Product.ID}}', '7')
  .replaceAll('{{.Product.ProductKind}}', 'standard')
  .replaceAll('{{.Product.ContactCollectionLevel}}', 'mobile')
  .replaceAll('{{.Product.PromotionContext}}', '')
  .replaceAll('{{.Product.RegionOptionsJSON}}', '[]')
  .replaceAll('{{.AlipayEnabled}}', 'false')
  .replaceAll('{{.Product.CouponTargetRef}}', 'standard_product:7');

assert.equal(source.includes('id="grossAmount"'), false);
const storageKey = 'aicrm.checkout.tab.v2:7:standard';
const paidCheckpoint = () => JSON.stringify({
  key: 'checkout-key-0000001', merchant_order_no: 'M-paid-7',
  payload: {product_id: 7, product_kind: 'standard', beneficiary_selection: 'payer_self', coupon_claim_id: 0, contact_collection_level: 'mobile', mobile: '+8613812345678'},
  session_binding: 'a'.repeat(43), terminal_status: 'paid', create_attempted: true,
});

function response(body, status = 202) {
  return {ok: status >= 200 && status < 300, status, async json() { return body; }};
}

async function settle() {
  for (let i = 0; i < 8; i++) await new Promise(resolve => setImmediate(resolve));
}

function boot(store, completion, redirectFailure = false, sessionAuthorized = true, setup = '', purchase = {purchase_state:'available',can_purchase:true}, userAgent='MicroMessenger', renewal=false, details=false) {
  const calls = [], elements = new Map();
  const setGlobal = (name, value) => Object.defineProperty(globalThis, name, {value, configurable: true, writable: true});
  const element = () => ({hidden: false, disabled: false, dataset: {}, value: '0', checked: true, textContent: '', href: '', children: [], attributes: new Map(), addEventListener(type, listener) { this.listener ??= {}; this.listener[type] = listener; }, appendChild(child) { this.children.push(child); }, replaceChildren(...children) {this.children=children;this.textContent="";}, setAttribute(name, value) { this.attributes.set(name, String(value)); }, removeAttribute(name) { this.attributes.delete(name); }, set src(value) { this.source = value; queueMicrotask(() => this.listener?.load?.({target: this})); }, get src() { return this.source; }});
  for (const id of ['price', 'buy', 'status', 'coupon', 'couponPanel', 'couponStatus', 'refreshCoupons', 'wechatNotice', 'mobile', 'payableAmount', 'footerAmount', 'discountAmount', 'identityGate', 'identityTitle', 'identityMessage', 'authFeedback', 'authContinue', 'checkoutContent','paymentDetails','mobilePanel','paymentMethod','product','footer','productName','alipayGuide','alipayGuideMessage','alipayPaymentURL','alipayCopy','alipayPaid']) elements.set(id, element());
  elements.get('checkoutContent').querySelector=selector=>elements.get(({'.product':'product','.checkout-footer':'footer','.product h1':'productName'})[selector]||selector.slice(1));
  if(renewal)elements.set('renew',element());
  if(details){const detail=element(),img=element();detail.hidden=true;img.dataset.src='https://example.com/detail.png';detail.querySelectorAll=()=>[img];elements.set('detailContent',detail);elements.set('detailImage',img);elements.set('detailPrice',element());}
  elements.get('authContinue').hidden = true;
  elements.get('identityGate').parentElement = {dataset: {}};
  elements.get('checkoutContent').hidden = true;
  const paymentOption=element(); paymentOption.value='wechat_pay';
  setGlobal('document', {getElementById(id) { return elements.get(id); }, querySelector() { return paymentOption; }, querySelectorAll() { return [paymentOption]; }, addEventListener() {}, createElement() { return element(); }});
  setGlobal('navigator', {userAgent});
  setGlobal('sessionStorage', {getItem(key) { return store.get(key) ?? null; }, setItem(key,value) {store.set(key,String(value));}, removeItem(key) {store.delete(key);} });
  setGlobal('localStorage', {getItem(key) { return store.get(key) ?? null; }, setItem(key, value) { store.set(key, String(value)); }, removeItem(key) { store.delete(key); }});
  setGlobal('location', {href: '', pathname: '/pay/course-7', assign(url) { calls.push({redirect: url}); if (redirectFailure) throw new Error('redirect blocked'); }});
  setGlobal('crypto', {randomUUID() { return 'fresh-checkout-key'; }});
  setGlobal('WeixinJSBridge', {invoke() { throw new Error('paid reload must not invoke payment'); }});
  setGlobal('fetch', async (url, options = {}) => {
    calls.push({url: String(url), method: options.method ?? 'GET'});
    if (String(url) === '/api/v1/wechat-pay/checkout-session') return sessionAuthorized===503?response({code:'unavailable'},503):(typeof sessionAuthorized === 'function' ? sessionAuthorized() : sessionAuthorized) ? response({checkout_session_binding: 'a'.repeat(43),can_create_checkout:sessionAuthorized!=='consumed'}) : response({code: 'payment_session_required'}, 401);
    if (String(url).startsWith('/api/v1/wechat-pay/purchase-status')) return response(purchase);
    if (String(url).startsWith('/api/h5/coupons/available')) return response({items: []});
    assert.equal(String(url), '/api/v1/wechat-pay/checkouts/M-paid-7');
    return response(completion);
  });
  Function(script + '\n' + setup)();
  return {calls, elements};
}

// An unauthenticated WeChat visitor sees the required login gate,
// without exposing checkout or creating an order.
{
  const store = new Map();
  const run = boot(store, {}, false, false);
  await settle();
  assert.equal(run.elements.get('identityGate').hidden, false);
  assert.equal(run.elements.get('checkoutContent').hidden, true);
  assert.equal(run.elements.get('authContinue').hidden, false);
  assert.equal(run.elements.get('authContinue').href, '/api/h5/wechat-pay/oauth/start?return_url=%2Fpay%2Fcourse-7');
  assert.equal(run.elements.get('identityTitle').textContent, '登录才能完成支付');
  assert.match(run.elements.get('identityMessage').textContent, /不会自动扣款/);
  assert.equal(run.calls.length, 1);
  assert.equal(run.calls[0].url, '/api/v1/wechat-pay/checkout-session');
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  assert.equal(run.calls.filter(call => call.redirect).length, 0);
}

// A QR action is re-read after a reload from the persisted paid checkpoint.
// The bootstrap contains no code path that starts a new checkout or invokes
// WeixinJSBridge for this already-paid order.
{
  const store = new Map([[storageKey, paidCheckpoint()]]);
  const run = boot(store, {status: 'paid', completion_action: {state: 'available', mode: 'qr', lead_qr: {url: 'https://work.weixin.qq.com/q/test', title: '添加客服', subtitle: '领取资料'}}});
  await settle();
  assert.equal(run.elements.get('identityGate').hidden, true);
  assert.equal(run.elements.get('checkoutContent').hidden, false);
  assert.equal(run.elements.get('buy').disabled, true);
  assert.equal(run.elements.get('refreshCoupons').disabled, true, 'paid checkpoint locks coupon refresh');
  assert.equal(run.elements.has('renew'), false);
  assert.equal(run.elements.get('buy').textContent,'已购买');
  assert.equal(run.elements.get('status').children.some(child=>child.textContent==='支付完成'),true);
  assert.equal(run.elements.get('footer').hidden,true);
  assert.equal(run.elements.get('couponPanel').hidden,true);
  assert.equal(run.elements.get('status').children.some(child=>child.className==='completion-qr'),true);
  assert.equal(JSON.parse(store.get(storageKey)).terminal_status, 'paid');
  assert.equal(run.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 1);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
}

// If the browser cannot complete a redirect, the paid checkpoint remains.
// Reloading makes only the same authorized read; it never replaces the key,
// creates a second order, or calls the payment SDK.
{
  const store = new Map([[storageKey, paidCheckpoint()]]);
  const paidRedirect = {status: 'paid', completion_action: {state: 'available', mode: 'redirect', redirect_url: '/after-paid'}};
  const first = boot(store, paidRedirect, true);
  await settle();
  const second = boot(store, paidRedirect, true);
  await settle();
  assert.equal(JSON.parse(store.get(storageKey)).terminal_status, 'paid');
  assert.equal(first.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 1);
  assert.equal(second.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 1);
  assert.equal([...first.calls, ...second.calls].some(call => call.method === 'POST'), false);
  assert.equal([...first.calls, ...second.calls].filter(call => call.redirect === '/after-paid').length, 2);
}

// Unknown prepay must stop polling, keep the exact checkpoint, and never
// create another checkout, even when the buyer clicks again.
{
  const pending = JSON.parse(paidCheckpoint());
  delete pending.terminal_status;
  const original = JSON.stringify(pending);
  const store = new Map([[storageKey, original]]);
  const run = boot(store, {status: 'awaiting_prepay', ready: false, prepay_state: 'outcome_unknown'});
  await settle();
  await run.elements.get('buy').listener.click();
  assert.match(run.elements.get('status').textContent, /下单结果尚未确认/);
  assert.equal(run.elements.get('buy').disabled, false);
  assert.equal(store.get(storageKey), original);
  await run.elements.get('buy').listener.click();
  assert.equal(store.get(storageKey), original);
  assert.equal(run.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 3);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
}

// Bridge readiness has a deadline and removes its listener. A late bridge
// event after failure cannot unexpectedly launch a payment sheet.
{
  const timers = new Map();
  let timerID = 0, listener, removed = false, invoked = 0;
  const bridgeSource = script.slice(script.indexOf('function invokePay(handoff)'), script.indexOf('\nconst couponDiscounts'));
  const doc = {addEventListener(_, fn) { listener = fn; }, removeEventListener(_, fn) { assert.equal(fn, listener); removed = true; }};
  const invoke = Function('document', 'WeixinJSBridge', 'setTimeout', 'clearTimeout', bridgeSource + ';return invokePay;')(
    doc, undefined, (fn, ms) => { timers.set(++timerID, {fn, ms}); return timerID; }, id => timers.delete(id),
  );
  const promise = invoke({});
  assert.equal(timers.get(1).ms, 10000);
  timers.get(1).fn();
  await assert.rejects(promise, /微信支付未能打开/);
  assert.equal(removed, true);
  listener();
  assert.equal(timers.size, 0);
  assert.equal(invoked, 0);
}

// A stalled public read has a bounded deadline and returns a recoverable
// loading failure instead of leaving the identity gate pending forever.
{
  const requestSource = script.slice(script.indexOf('function requestFailure'), script.indexOf('\nfunction showCompletionAction'));
  let cleared = false;
  class FakeAbortController {
    constructor() { this.signal = {aborted: false}; }
    abort() { this.signal.aborted = true; }
  }
  const request = Function('fetch', 'AbortController', 'setTimeout', 'clearTimeout', requestSource + ';return requestJSON;')(
    async (_, options) => {
      if (options.signal.aborted) throw Object.assign(new Error('aborted'), {name: 'AbortError'});
      return new Promise(() => {});
    },
    FakeAbortController,
    (fn, ms) => { assert.equal(ms, 12000); fn(); return 1; },
    id => { assert.equal(id, 1); cleared = true; },
  );
  await assert.rejects(request('/slow'), error => error.code === 'request_timeout' && /网络连接超时/.test(error.message));
  assert.equal(cleared, true);
}

// A manually abandoned legacy flow retains its original idempotency evidence.
// No replacement checkout or payment bridge invocation follows the readback.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const original = JSON.stringify(checkpoint);
  const store = new Map([[storageKey, original]]);
  const run = boot(store, {status: 'awaiting_prepay', prepay_state: 'outcome_unknown', checkout_abandoned: true});
  await settle();
  await run.elements.get('buy').listener.click();
  assert.equal(run.elements.get('buy').disabled, true);
  assert.equal(run.elements.has('renew'), false);
  assert.match(run.elements.get('status').textContent, /原支付流程已停止/);
  assert.equal(store.get(storageKey), original);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  assert.equal(run.calls.filter(call => call.url === '/api/v1/wechat-pay/checkouts/M-paid-7').length, 2);
}

// Only the server's reviewed, expired, never-delivered checkout release may
// remove the old checkpoint. Phone editing returns without an automatic order.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const store = new Map([[storageKey, JSON.stringify(checkpoint)]]);
  const run = boot(store, {status: 'awaiting_prepay', checkout_abandoned: true, checkout_restart_allowed: true});
  await settle();
  assert.equal(store.has(storageKey), false);
  assert.equal(run.elements.get('mobile').disabled, false);
  assert.equal(run.elements.get('coupon').disabled, false);
  assert.equal(run.elements.get('buy').disabled, false);
  assert.equal(run.elements.get('payableAmount').textContent, '¥9.90');
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  run.elements.get('mobile').value = '13800138000';
  let submissions = 0;
  globalThis.fetch = async (url, options = {}) => {
    if (url.endsWith('/checkout-session')) return response({checkout_session_binding: 'a'.repeat(43),can_create_checkout:true});
    if (options.method === 'POST') {
      const payload = JSON.parse(options.body);
      assert.equal(payload.mobile, '+8613800138000');
      assert.equal(options.headers['Idempotency-Key'], 'fresh-checkout-key');
      submissions++;
      return response({merchant_order_no: 'new-order'});
    }
    return response({status: 'paid', amount_minor: 990, currency: 'CNY'});
  };
  await run.elements.get('buy').listener.click();
  assert.equal(submissions, 1);
  assert.equal(JSON.parse(store.get(storageKey)).merchant_order_no, 'new-order');
}

// A delayed old-order release cannot remove a newer tab's checkpoint.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const store = new Map([[storageKey, JSON.stringify(checkpoint)]]);
  const run = boot(store, {status: 'awaiting_prepay'}, false, true, 'globalThis.testRelease = releaseExpiredCheckout;');
  await settle();
  checkpoint.merchant_order_no = 'new-order-in-other-tab';
  checkpoint.key = 'other-tab-checkout-key';
  const newer = JSON.stringify(checkpoint);
  store.set(storageKey, newer);
  assert.throws(() => globalThis.testRelease({checkout_restart_allowed: true}, 'M-paid-7'), /订单状态已更新/);
  assert.equal(store.get(storageKey), newer);
  delete globalThis.testRelease;
}

// Expiry between page load and the buyer's click returns to the explicit gate.
// No checkout key or order is created and the browser never starts OAuth itself.
{
  let authorized = true;
  const store = new Map();
  const run = boot(store, {}, false, () => authorized);
  await settle();
  assert.equal(run.elements.get('checkoutContent').hidden, false);
  authorized = false;
  run.elements.get('mobile').value = '13800138000';
  await run.elements.get('buy').listener.click();
  assert.equal(run.elements.get('identityGate').hidden, false);
  assert.equal(run.elements.get('checkoutContent').hidden, true);
  assert.equal(run.elements.get('authContinue').hidden, false);
  assert.equal(run.elements.get('identityTitle').textContent, '登录才能完成支付');
  assert.equal(store.has(storageKey), false);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  assert.equal(run.calls.filter(call => call.redirect).length, 0);
}

// A cancelled payment resumes its immutable original amount, never a newly
// selected coupon. Bootstrap reads once and cannot open the cashier or POST.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const store = new Map([[storageKey, JSON.stringify(checkpoint)]]);
  const run = boot(store, {status: 'awaiting_payment', amount_minor: 990, currency: 'CNY', ready: true, handoff: {}}, false, true, 'couponDiscounts.set(123,100)');
  await settle();
  assert.equal(run.elements.get('footerAmount').textContent, '¥9.90');
  assert.equal(run.elements.get('coupon').disabled, true);
  assert.equal(run.elements.get('mobile').disabled, true);
  assert.equal(run.calls.filter(call => call.url.includes('/checkouts/')).length, 1);
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
  run.elements.get('coupon').value = '123';
  run.elements.get('coupon').listener.change();
  assert.equal(run.elements.get('footerAmount').textContent, '¥9.90');
  await run.elements.get('buy').listener.click();
  assert.equal(run.elements.get('footerAmount').textContent, '¥9.90');
  assert.equal(store.get(storageKey), JSON.stringify(checkpoint));
  assert.equal(run.calls.some(call => call.method === 'POST'), false);
}

// Missing historical amount is not replaced by today's product price.
{
  const checkpoint = JSON.parse(paidCheckpoint());
  delete checkpoint.terminal_status;
  const run = boot(new Map([[storageKey, JSON.stringify(checkpoint)]]), {status: 'awaiting_prepay'});
  await settle();
  assert.equal(run.elements.get('footerAmount').textContent, '待确认');
  assert.equal(run.elements.get('discountAmount').hidden, true);
}

// Server ownership survives missing local checkpoints, and blocks any new POST.
{
 const run=boot(new Map(),{},false,true,'',{purchase_state:'owned',can_purchase:false});
 await settle();
 assert.equal(run.elements.get('buy').disabled,true);
 assert.equal(run.elements.get('buy').textContent,'已购买');
 await run.elements.get('buy').listener.click();
 assert.equal(run.calls.some(call=>call.method==='POST'),false);
}
{
 // A server-side pending order no longer blocks a fresh checkout. The public
 // status contract stays on the available path so the browser can create a
 // new idempotent order while the old one remains queryable.
 const run=boot(new Map(),{},false,true,'',{purchase_state:'available',can_purchase:true});
 await settle();
 assert.equal(run.elements.get('buy').disabled,false);
 assert.equal(run.elements.get('checkoutContent').hidden,false);
 assert.equal(run.calls.some(call=>call.method==='POST'),false);
}
// Returning from unsuccessful OAuth cannot start a redirect loop.
{
 const store=new Map();
 const first=boot(store,{},false,false);await settle();
 assert.equal(first.calls.filter(call=>call.redirect).length,0);
 const again=boot(store,{},false,false);await settle();
 assert.equal(again.calls.some(call=>call.redirect),false);
 assert.equal(again.elements.get('authContinue').hidden,false);
}

// Server failure and outside-WeChat entry never initiate OAuth.
for(const [auth,ua] of [[503,'MicroMessenger'],[false,'Safari']]){
 const run=boot(new Map(),{},false,auth,'',undefined,ua);await settle();
 assert.equal(run.calls.some(call=>call.redirect||call.method==='POST'),false);
 assert.equal(run.elements.get('identityGate').hidden,false);
}
// Periodic products retain deliberate renewal, ordinary products have no reset.
{
 const store=new Map([[storageKey,paidCheckpoint()]]);
 const run=boot(store,{status:'paid',amount_minor:990,currency:'CNY'},false,true,'',undefined,'MicroMessenger',true);
 await settle();assert.equal(run.elements.get('renew').hidden,false);
 run.elements.get('renew').listener.click();await settle();
 assert.equal(store.has(storageKey),false);
 assert.equal(run.elements.get('buy').disabled,false);
 assert.equal(run.calls.some(call=>call.method==='POST'),false);
}

// Images are neither displayed nor requested until trusted authorization and
// purchase eligibility succeed. Purchased courses never mount their buy detail.
for(const [auth,purchase,visible] of [[false,{purchase_state:'available',can_purchase:true},false],[true,{purchase_state:'available',can_purchase:true},true],[true,{purchase_state:'owned',can_purchase:false},false]]){
 const run=boot(new Map(),{},false,auth,'',purchase,'MicroMessenger',false,true);await settle();
 assert.equal(run.elements.get('detailContent').hidden,!visible);
 assert.equal(run.elements.get('detailImage').src,visible?'https://example.com/detail.png':undefined);
 if(visible)assert.equal(run.elements.get('checkoutContent').hidden,true);
 assert.equal(run.calls.some(call=>call.method==='POST'),false);
}
// Former browser-wide checkpoints are ignored, not cleared or replayed.
{
 const oldKey='aicrm.checkout.v1:7:standard', oldValue=paidCheckpoint();
 const store=new Map([[oldKey,oldValue]]);
 const run=boot(store,{});await settle();
 assert.equal(store.get(oldKey),oldValue);
 assert.equal(run.calls.some(call=>call.url?.includes('/checkouts/')||call.method==='POST'),false);
}

// A consumed session can read ownership, but renews OAuth before allocating a new key.
{
 const store=new Map();const run=boot(store,{},false,'consumed');await settle();
 assert.equal(run.elements.get('checkoutContent').hidden,true);
 assert.equal(run.calls.filter(call=>call.redirect).length,0);
 assert.equal(store.has(storageKey),false);
 assert.equal(run.calls.some(call=>call.method==='POST'),false);
}
// A consumed session must not interfere with this tab's accepted/paid order readback.
{
 const store=new Map([[storageKey,paidCheckpoint()]]);
 const run=boot(store,{status:'paid'},false,'consumed');await settle();
 assert.equal(run.calls.some(call=>call.redirect),false);
 assert.equal(run.elements.get('buy').textContent,'已购买');
 assert.equal(store.get(storageKey),paidCheckpoint());
}

// Server ownership in a fresh tab supplies the configured paid guidance without a local order.
for(const action of [
 {state:'available',mode:'qr',lead_qr:{url:'https://work.weixin.qq.com/q/owned',title:'领取资料'}},
 {state:'available',mode:'redirect',redirect_url:'/owned-followup'},
 {state:'unavailable'}
]){
 const run=boot(new Map(),{},false,true,'',{purchase_state:'owned',can_purchase:false,completion_action:action});
 await settle();
 assert.equal(run.elements.get('buy').disabled,true);
 assert.equal(run.calls.some(call=>call.method==='POST'||call.url?.includes('/checkouts/')),false);
 if(action.mode==='qr')assert.equal(run.elements.get('status').children.find(child=>child.className==='completion-qr').src,action.lead_qr.url);
 else if(action.mode==='redirect')assert.equal(run.calls.filter(call=>call.redirect===action.redirect_url).length,1);
 else assert.match(run.elements.get('status').children.map(child=>child.textContent).join(''),/后续指引暂不可用/);
}
// A local paid checkpoint remains the sole guide source, avoiding duplicate redirects.
{
 const action={state:'available',mode:'redirect',redirect_url:'/single-followup'};
 const run=boot(new Map([[storageKey,paidCheckpoint()]]),{status:'paid',completion_action:action},false,true,'',{purchase_state:'owned',can_purchase:false,completion_action:action});
 await settle();
 assert.equal(run.calls.filter(call=>call.redirect===action.redirect_url).length,1);
 assert.equal(run.calls.filter(call=>call.url?.includes('/checkouts/')).length,1);
}

// A consumed-session OAuth failure must not loop or reveal either entry view.
for(const details of [false,true]){
 const store=new Map();
 const first=boot(store,{},false,'consumed','',undefined,'MicroMessenger',false,details);await settle();
 assert.equal(first.calls.filter(call=>call.redirect).length,0);
 if(details)assert.equal(first.elements.get('detailImage').src,undefined);
 const second=boot(store,{},false,'consumed','',undefined,'MicroMessenger',false,details);await settle();
 assert.equal(second.calls.some(call=>call.redirect),false);
 assert.equal(second.elements.get('checkoutContent').hidden,true);
 assert.equal(second.elements.get('authContinue').hidden,false);
}
// Owned visitors never renew the consumed session just to read their entitlement.
{
 const run=boot(new Map(),{},false,'consumed','',{purchase_state:'owned',can_purchase:false,completion_action:{state:'none'}});await settle();
 assert.equal(run.calls.some(call=>call.redirect),false);
 assert.equal(run.elements.get('buy').textContent,'已购买');
}
// Renewal checks fresh authorization before exposing an editable payment form.
{
 const store=new Map([[storageKey,paidCheckpoint()]]);
 const run=boot(store,{status:'paid'},false,'consumed','',undefined,'MicroMessenger',true);await settle();
 await run.elements.get('renew').listener.click();await settle();
 assert.equal(run.calls.filter(call=>call.redirect).length,0);
 assert.equal(run.elements.get('checkoutContent').hidden,true);
 assert.equal(store.has(storageKey),false);
 assert.equal(run.calls.some(call=>call.method==='POST'),false);
}

// Execute actual Go-rendered HTML against a real DOM. Parsing/evaluation here
// catches template JavaScript errors that a handwritten element map conceals.
if(process.argv[2]){
 for(const [index,path] of process.argv.slice(2).entries()){
  const html=await readFile(path,'utf8'),periodic=index===1;
  const dom=new JSDOM(html,{url:'https://example.test/pay/course-7',runScripts:'outside-only'}),w=dom.window;
  const requests=[];let invokes=0;
  Object.defineProperty(w.navigator,'userAgent',{value:'MicroMessenger'});
  w.WeixinJSBridge={invoke(){invokes++;throw new Error('must not repeat paid SDK')}};
  const action={state:'available',mode:'qr',lead_qr:{url:'https://wework.qpic.cn/real-image',title:'添加企微',subtitle:'领取资料'}};
  if(periodic){const stored=JSON.parse(paidCheckpoint());stored.payload.product_kind='service_period';w.sessionStorage.setItem('aicrm.checkout.tab.v2:7:service_period',JSON.stringify(stored))}
  w.fetch=async(url,options={})=>{requests.push({url:String(url),method:options.method||'GET'});if(String(url).includes('checkout-session'))return response({checkout_session_binding:'a'.repeat(43),can_create_checkout:true},200);if(String(url).includes('purchase-status'))return response(periodic?{purchase_state:'available',can_purchase:true}:{purchase_state:'owned',can_purchase:false,completion_action:action},200);if(String(url).includes('/coupons/'))return response({items:[]},200);return response({status:'paid',completion_action:action},200)};
  for(const tag of w.document.querySelectorAll('script'))w.eval(tag.textContent);
  await settle();
  const status=w.document.getElementById('status');
  assert.equal(status.querySelector('h2')?.textContent,'支付完成');
  assert.equal(status.querySelector('img.completion-qr')?.src,action.lead_qr.url);
  assert.equal(status.querySelectorAll('button,a').length,0,'inline QR must not need a second action');
  for(const selector of ['.product','#paymentDetails','#mobilePanel','#paymentMethod','.checkout-footer'])assert.equal(w.document.querySelector('#checkoutContent '+selector).hidden,true,selector);
  assert.equal(requests.some(r=>r.method==='POST'),false);assert.equal(invokes,0);
  if(periodic){w.document.getElementById('renew').click();await settle();for(const selector of ['.product','#paymentDetails','#mobilePanel','#paymentMethod','.checkout-footer'])assert.equal(w.document.querySelector('#checkoutContent '+selector).hidden,false,'renew '+selector);assert.equal(status.querySelector('.completion-qr'),null);assert.equal(requests.some(r=>r.method==='POST'),false)}
  dom.window.close();
 }
}
