import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {JSDOM,VirtualConsole} from 'jsdom';
const [freeHTML,pricedHTML]=await Promise.all(process.argv.slice(2).map(p=>readFile(p,'utf8')));
const storageKey='aicrm.checkout.tab.v2:7:standard',binding='a'.repeat(43);
const completed={order_id:91,merchant_order_no:'free-order-91',provider:'no_payment',status:'paid',amount_minor:0,currency:'CNY',payment_required:false,completion_action:{state:'available',mode:'qr',lead_qr:{url:'https://example.test/qr.png',title:'领取资料',subtitle:'请添加客服'}}};
const waitFor=async(fn)=>{for(let i=0;i<100;i++){if(fn())return;await new Promise(r=>setTimeout(r,10))}assert.fail('browser state timed out')};
function boot(html,store,{loseFirst=false,priceChanged=false,owned=false}={}){
 const calls=[],errors=[];let posts=0,bridge=0;
 const dom=new JSDOM(html,{url:'https://checkout.example.test/pay/course-7',runScripts:'dangerously',pretendToBeVisual:true,virtualConsole:new VirtualConsole().on('jsdomError',e=>errors.push(e)),beforeParse(w){
  Object.defineProperty(w.navigator,'userAgent',{value:'MicroMessenger/8.0.0'});
  w.WeixinJSBridge={invoke(){bridge++;throw Error('free order invoked payment')}};
  for(const [key,value]of store)w.sessionStorage.setItem(key,value);
  w.fetch=async(url,options={})=>{
   calls.push({url:String(url),method:options.method||'GET',body:options.body,key:options.headers?.['Idempotency-Key']});
   if(String(url).includes('checkout-session'))return new Response(JSON.stringify({checkout_session_binding:binding,can_create_checkout:!owned}));
   if(String(url).includes('purchase-status'))return new Response(JSON.stringify({purchase_state:owned?'owned':'available',can_purchase:!owned,payment_required:owned?false:undefined,completion_action:completed.completion_action}));
   if(String(url).includes('coupons/available'))return new Response(JSON.stringify({items:[]}));
   if(options.method==='POST'){
    posts++;
    if(priceChanged)return new Response(JSON.stringify({code:'price_changed'}),{status:409});
    if(loseFirst&&posts===1)throw Error('simulated response loss');
    return new Response(JSON.stringify(completed));
   }
   assert.equal(String(url),'/api/v1/wechat-pay/checkouts/free-order-91');
   return new Response(JSON.stringify(completed));
  };
 }});
 return{dom,calls,errors,posts:()=>posts,bridge:()=>bridge,checkpoint:()=>JSON.parse(dom.window.sessionStorage.getItem(storageKey)||'null'),save(){store.set(storageKey,dom.window.sessionStorage.getItem(storageKey))}};
}
const store=new Map();
{
 const run=boot(freeHTML,store,{loseFirst:true}),doc=run.dom.window.document;
 await waitFor(()=>!doc.getElementById('buy').disabled&&doc.getElementById('checkoutContent').hidden===false);
 assert.equal(doc.getElementById('buy').textContent,'免费领取');
 assert.equal(doc.getElementById('couponPanel').hidden,true);
 assert.equal(doc.getElementById('paymentMethod').hidden,true);
 assert.equal(run.calls.some(c=>c.url.includes('coupons/available')),false);
 doc.getElementById('buy').click();await waitFor(()=>run.posts()===1&&!doc.getElementById('buy').disabled);
 const original=run.checkpoint();assert.equal(original.payload.expected_amount_minor,0);assert.equal(original.create_attempted,true);
 assert.match(doc.getElementById('buy').textContent,/重试原请求/);
 doc.getElementById('buy').click();await waitFor(()=>doc.querySelector('.completion-result h2'));
 assert.equal(doc.querySelector('.completion-result h2').textContent,'领取成功');
 assert.equal(doc.querySelector('.completion-amount').textContent,'无需支付');
 assert.equal(doc.querySelector('.completion-title').textContent,'领取资料');
 const posts=run.calls.filter(c=>c.method==='POST');assert.equal(posts[0].key,posts[1].key);assert.equal(posts[0].body,posts[1].body);
 assert.equal(run.bridge(),0);assert.equal(run.calls.some(c=>c.url.endsWith('/free-order-91')),false,'immediate result should not poll');
 assert.equal(run.errors.length,0);run.save();run.dom.window.close();
}
// Current product price cannot change the frozen successful result after reload.
{
 const run=boot(pricedHTML,store,{owned:true});await waitFor(()=>run.dom.window.document.querySelector('.completion-result h2'));
 assert.equal(run.dom.window.document.querySelector('.completion-result h2').textContent,'领取成功');
 assert.equal(run.dom.window.document.querySelector('.completion-amount').textContent,'无需支付');
 assert.equal(run.posts(),0);assert.equal(run.bridge(),0);assert.equal(run.errors.length,0);run.dom.window.close();
}
// A definitive difference clears the failed request, never auto-submits a new price.
{
 const run=boot(freeHTML,new Map(),{priceChanged:true}),doc=run.dom.window.document;
 await waitFor(()=>!doc.getElementById('buy').disabled);
 doc.getElementById('buy').click();await waitFor(()=>doc.getElementById('buy').textContent==='刷新金额');
 assert.equal(run.posts(),1);assert.equal(run.checkpoint(),null);assert.match(doc.getElementById('status').textContent,/重新确认/);assert.equal(run.bridge(),0);assert.equal(run.errors.length,0);run.dom.window.close();
}
console.log('zero_amount_journey: PASS');
