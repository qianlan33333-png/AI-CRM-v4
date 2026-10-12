import assert from 'node:assert/strict';
import {JSDOM} from 'jsdom';
let raw='';for await(const chunk of process.stdin)raw+=chunk;const pages=JSON.parse(raw);
const tick=()=>new Promise(r=>setTimeout(r,5));
for(const [mode,html] of Object.entries(pages))for(const failure of [false,true]){
 const dom=new JSDOM(html,{url:'https://crm.test/pay/contact-course',runScripts:'outside-only'});const w=dom.window;Object.defineProperty(w.navigator,'userAgent',{value:'MicroMessenger'});w.Headers=Headers;
 let resolve;const pending=new Promise(r=>resolve=r);const requests=[];w.fetch=async(url)=>{requests.push(String(url));if(String(url).includes('checkout-session'))return pending;if(String(url).includes('purchase-status'))return Response.json({purchase_state:'available',can_purchase:true});return Response.json({items:[]});};
 for(const script of w.document.querySelectorAll('script:not([src])'))w.eval(script.textContent);
 await tick();const request=requests.find(v=>v.includes('checkout-session'));assert.ok(request);
 assert.equal(request.includes('product_id=7'),mode!=='none');
 const phone=w.document.getElementById('mobile');if(phone){phone.value='13900000000';phone.dispatchEvent(new w.Event('input'));phone.value='';phone.dispatchEvent(new w.Event('input'));}
 resolve(Response.json({checkout_session_binding:'a'.repeat(43),can_create_checkout:true,...(failure?{contact_prefill_unavailable:true}:{contact_prefill:{mobile_e164:'+8613800000000',shipping_address:{mobile_e164:'+8613800000000',recipient_name:'收件人',province_code:'11',province_name:'北京市',city_code:'1101',city_name:'北京市',district_code:'110101',district_name:'东城区',detail_address:'测试路 1 号'}}})}));await tick();
 if(phone)assert.equal(phone.value,'','late prefill replaced explicit clear');
 if(mode==='shipping_address'){assert.equal(w.document.getElementById('recipientName').value,failure?'':'收件人');assert.equal(w.document.getElementById('district').value,failure?'':'110101');}
 if(mode==='none')assert.equal(phone,null);
 dom.window.close();
}
console.log('checkout prefill: none/mobile/shipping, late clear, region fill and read-failure fallback passed');
