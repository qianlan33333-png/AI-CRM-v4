import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import path from 'node:path';
import {JSDOM,VirtualConsole} from 'jsdom';
const settle=async()=>{for(let i=0;i<15;i++)await new Promise(r=>setImmediate(r));};
async function page(mode,storage,status){
 const html=await readFile(path.join(process.argv[2],mode+'.html'),'utf8');const posts=[],errors=[];
 const console=new VirtualConsole();console.on('jsdomError',e=>errors.push(e.message));
 const dom=new JSDOM(html,{url:'https://example.test/pay/course',runScripts:'dangerously',virtualConsole:console,beforeParse(w){
 Object.defineProperty(w.navigator,'userAgent',{value:'MicroMessenger'});
 if(storage)for(const [k,v] of Object.entries(storage))w.sessionStorage.setItem(k,v);
 w.fetch=async(url,opts={})=>{const route=String(url);
 if(opts.method==='POST'){posts.push({body:JSON.parse(opts.body),key:opts.headers['Idempotency-Key']});return{ok:false,status:503,json:async()=>({code:'unavailable'})}}
 const body=route.includes('checkout-session')?{checkout_session_binding:'a'.repeat(43),can_create_checkout:true}:route.includes('purchase-status')?{purchase_state:'available',can_purchase:true}:route.includes('coupons/available')?{items:[{claim_id:9,name:'百元券',discount_amount_minor:10000,currency:'CNY'}]}:status||{};
 return{ok:true,status:200,json:async()=>body};};}});await settle();assert.deepEqual(errors,[]);return{dom,posts};
}
const {dom,posts}=await page('multi');const d=dom.window.document;const choice=d.getElementById('variantChoice');
assert.equal(choice.value,'');d.getElementById('buy').click();await settle();assert.equal(posts.length,0);
choice.value='21';choice.dispatchEvent(new dom.window.Event('change'));await settle();assert.equal(d.getElementById('coupon').options.length,2);assert.equal(d.getElementById('payableAmount').textContent,'¥0.01');
choice.value='22';choice.dispatchEvent(new dom.window.Event('change'));await settle();assert.equal(d.getElementById('coupon').options.length,2);assert.equal(d.getElementById('payableAmount').textContent,'¥899.00');assert.match(d.getElementById('variantDescription').textContent,/独立定价/);
d.getElementById('buy').click();assert.equal(choice.disabled,true,'lock selection before awaiting session read');await settle();assert.equal(posts[0].body.variant_id,22);assert.equal(posts[0].body.variant_version,3);assert.equal(choice.disabled,true);
choice.value='21';choice.dispatchEvent(new dom.window.Event('change'));d.getElementById('buy').click();await settle();assert.deepEqual(posts[1],posts[0],'unknown response must reuse exact payload and key');
const saved={};for(let i=0;i<dom.window.sessionStorage.length;i++){const k=dom.window.sessionStorage.key(i);saved[k]=dom.window.sessionStorage.getItem(k)}dom.window.close();
const recovered=await page('off',saved);recovered.dom.window.document.getElementById('buy').click();await settle();assert.deepEqual(recovered.posts[0],posts[0],'disabled catalog must not rewrite pending request');recovered.dom.window.close();
const confirmed=Object.fromEntries(Object.entries(saved).map(([key,raw])=>[key,JSON.stringify({...JSON.parse(raw),merchant_order_no:'frozen-order'})]));
const frozenStatus={amount_minor:89900,currency:'CNY',status:'pending',variant:{id:22,version:3,name:'旧三人成行版',description:'历史说明',attributes:[{key:'版本',value:'旧B'}]}};
const frozen=await page('off',confirmed,frozenStatus);const frozenDoc=frozen.dom.window.document;
assert.equal(frozenDoc.getElementById('price').textContent,'¥899.00');assert.equal(frozenDoc.getElementById('payableAmount').textContent,'¥899.00');assert.equal(frozenDoc.getElementById('variantPanel').hidden,false);assert.equal(frozenDoc.getElementById('variantChoice').textContent,'旧三人成行版');assert.equal(frozenDoc.getElementById('variantChoice').disabled,true);frozen.dom.window.close();
const expired=await page('multi',confirmed,{...frozenStatus,checkout_restart_allowed:true});const expiredDoc=expired.dom.window.document;
assert.equal(expiredDoc.getElementById('variantChoice').disabled,false);assert.equal(expiredDoc.getElementById('variantChoice').options.length,3);assert.equal(expiredDoc.getElementById('price').textContent,'请选择规格');assert.equal(expiredDoc.getElementById('payableAmount').textContent,'请选择规格');expired.dom.window.close();
for(const mode of ['single','empty','off']){const {dom,posts}=await page(mode);const d=dom.window.document;if(mode==='empty'){assert.equal(d.getElementById('buy').disabled,true);assert.equal(posts.length,0)}else{if(mode==='single')assert.equal(d.getElementById('variantChoice').value,'21');d.getElementById('buy').click();await settle();assert.equal(posts.length,1);assert.equal(Object.hasOwn(posts[0].body,'variant_id'),mode==='single')}dom.window.close()}
console.log('variant selection, coupon, exact retry, disabled catalog recovery: PASS');
