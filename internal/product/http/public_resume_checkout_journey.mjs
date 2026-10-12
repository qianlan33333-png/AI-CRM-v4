import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM} from 'jsdom';

const html=await readFile(process.argv[2],'utf8');
const kind=process.argv[3];
const settle=async()=>{for(let i=0;i<16;i++)await new Promise(r=>setTimeout(r,5));};
for(const scenario of ['ready','alipay','paid','expired','unknown','wrong-product','unauthorized']){
 const dom=new JSDOM(html,{url:`https://crm.test/${kind==='standard'?'pay/course-7':'s/course-7/pay'}?resume_order=7`,pretendToBeVisual:true,runScripts:'outside-only'});
 const w=dom.window;Object.defineProperty(w.navigator,'userAgent',{value:'MicroMessenger'});
 const calls=[];let invoked=0,paid=scenario==='paid';
 const realTimeout=w.setTimeout.bind(w);w.setTimeout=(fn,ms,...args)=>realTimeout(fn,ms===1500?5:ms,...args);
 w.WeixinJSBridge={invoke(method,payload,callback){assert.equal(method,'getBrandWCPayRequest');assert.equal(payload.package,'prepay_id=original');invoked++;paid=true;callback({err_msg:'get_brand_wcpay_request:ok'});}};
 w.fetch=async(url,options={})=>{
  calls.push({url:String(url),method:options.method||'GET'});
  const response=(value,status=200)=>({ok:status>=200&&status<300,status,json:async()=>value});
  if(new URL(String(url), 'https://example.test').pathname==='/api/v1/wechat-pay/checkout-session')return scenario==='unauthorized'?response({code:'payment_session_required'},401):response({checkout_session_binding:'a'.repeat(43),can_create_checkout:false});
  if(url==='/api/h5/me/orders/7/payment')return response({order_id:7,merchant_order_no:'original-7',product_id:scenario==='wrong-product'?8:7,product_kind:kind,product_name:'冻结商品名称',provider:scenario==='alipay'?'alipay':'wechat_pay',channel:scenario==='alipay'?'alipay_wap':'h5_official_account',amount_minor:8800,currency:'CNY'});
  assert.equal(url,scenario==='alipay'?'/api/v1/alipay/checkouts/original-7':'/api/v1/wechat-pay/checkouts/original-7');
  if(scenario==='alipay')return response({status:'awaiting_payment',ready:true,amount_minor:8800,currency:'CNY',handoff:{redirectUrl:'https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay&app_id=synthetic&sign=synthetic&biz_content='+encodeURIComponent(JSON.stringify({out_trade_no:'original-7'}))}});
  if(scenario==='expired')return response({code:'conflict'},409);
  if(scenario==='unknown')return response({status:'awaiting_prepay',ready:false,prepay_state:'outcome_unknown',amount_minor:8800,currency:'CNY'});
  return response({status:paid?'paid':'awaiting_payment',ready:!paid,amount_minor:8800,currency:'CNY',handoff:paid?undefined:{package:'prepay_id=original'},completion_action:{state:'none'}});
 };
 for(const script of w.document.querySelectorAll('script'))if(!script.src)w.eval(script.textContent);
 await settle();const button=w.document.getElementById('buy'),status=w.document.getElementById('status');
 if(scenario==='ready'){
  assert.equal(button.textContent,'继续支付');assert.equal(w.document.getElementById('footerAmount').textContent,'¥88.00');assert.equal(w.document.querySelector('.product h1').textContent,'冻结商品名称');assert.equal(invoked,0,'page load must not open cashier');button.click();await settle();assert.equal(invoked,1);assert.equal(button.disabled,true);
 }else if(scenario==='alipay'){assert.equal(w.document.getElementById('alipayGuide').hidden,true);button.click();await settle();assert.equal(w.document.getElementById('alipayGuide').hidden,false);assert.match(w.document.getElementById('alipayPaymentURL').value,/openapi.alipay.com/);assert.equal(invoked,0);}
 else if(scenario==='paid'){assert.equal(button.disabled,true);assert.equal(invoked,0);}
 else if(scenario==='expired'){assert.match(status.textContent,/已失效或不可恢复/);assert.equal(button.disabled,true);}
 else if(scenario==='unknown'){button.click();await settle();assert.match(status.textContent,/结果尚未确认/);assert.equal(invoked,0);}
 else if(scenario==='wrong-product'){button.click();await settle();assert.match(status.textContent,/不匹配/);assert.equal(calls.some(c=>c.url.includes('/checkouts/')),false);}
 else {assert.equal(w.document.getElementById('authContinue').href,`https://crm.test/api/h5/wechat-pay/oauth/start?return_url=${encodeURIComponent(w.location.pathname+'?resume_order=7')}`);}
 assert.equal(calls.some(c=>c.method==='POST'),false,'resuming must never create a new checkout');
 assert.equal(calls.some(c=>c.url.includes('purchase-status')||c.url.includes('coupons/available')),false,'resuming must not recalculate purchase price or coupon');
 dom.window.close();
}
console.log(`original ${kind} checkout journeys passed: current session, frozen amount, WeChat and Alipay, paid, expired, unknown, wrong product and no new POST`);
