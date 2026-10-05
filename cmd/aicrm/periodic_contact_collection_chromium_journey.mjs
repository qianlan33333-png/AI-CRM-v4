import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { resolveChromiumBinary } from '../../internal/webshell/chromium_binary.mjs';

const base=process.env.AICRM_CONTACT_BROWSER_URL;
const standard=process.env.AICRM_CONTACT_BROWSER_PRODUCT_ID;
const period=process.env.AICRM_CONTACT_BROWSER_PERIOD_ID;
const sessions=JSON.parse(process.env.AICRM_CONTACT_BROWSER_SESSIONS||'{}');
if (!/^https:\/\/127\.0\.0\.1:\d+$/.test(base||'') || !/^[1-9]\d*$/.test(standard||'') || !/^[1-9]\d*$/.test(period||'')) throw new Error('requires isolated HTTPS product fixture');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
class CDP {
  constructor(socket) {
    this.socket=socket;this.id=0;this.pending=new Map();this.errors=0;this.posts=[];this.responses=new Map();
    socket.addEventListener('message',event=>{
      const m=JSON.parse(String(event.data));if(m.method==='Runtime.exceptionThrown')this.errors++;
      if(m.method==='Network.requestWillBeSent'&&m.params.request.method==='POST'&&m.params.request.url===base+'/api/v1/alipay/checkouts')this.posts.push(m.params.requestId);
      if(m.method==='Network.responseReceived')this.responses.set(m.params.requestId,m.params.response.status);
      const p=this.pending.get(m.id);if(!p)return;this.pending.delete(m.id);m.error?p.reject(new Error(`CDP ${m.error.code}`)):p.resolve(m.result||{});
    });
  }
  call(method,params={}) {
    return new Promise((resolve,reject)=>{
      const id=++this.id;const timer=setTimeout(()=>{this.pending.delete(id);reject(new Error(`CDP ${method} deadline`));},10000);
      this.pending.set(id,{resolve:v=>{clearTimeout(timer);resolve(v)},reject:e=>{clearTimeout(timer);reject(e)}});this.socket.send(JSON.stringify({id,method,params}));
    });
  }
}
async function value(cdp,expression) {
  const r=await cdp.call('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});
  if(r.exceptionDetails)throw new Error(`page expression failed: ${r.exceptionDetails.text}`);return r.result?.value;
}
async function wait(cdp,expression,message) {
  for(let i=0;i<240;i++){if(await value(cdp,expression))return;await delay(50);}throw new Error(message);
}
async function navigate(cdp,url,expression) {
  await cdp.call('Page.navigate',{url});await wait(cdp,expression,'page not ready');
}
async function assertProductForm(cdp) {
  assert.equal(await value(cdp,"Boolean(document.getElementById('pfStock')||document.getElementById('spfStock')||[...document.querySelectorAll('label')].some(el=>el.textContent.trim()==='库存'))"),false,'retired inventory controls remain absent');
  assert.equal(await value(cdp,"document.querySelector('[data-product-alipay-enabled]') instanceof HTMLInputElement"),true,'packaged Product sale form mounts Alipay policy control');
}
async function read(cdp,prefix,id) {
  const endpoint=prefix==='spf'?'/api/admin/service-period-products/':'/api/v1/products/';
  return value(cdp,`fetch(${JSON.stringify(endpoint+id)}).then(async r=>{if(!r.ok)throw new Error('read status '+r.status);const v=await r.json();return v.product||v})`);
}
async function save(cdp,prefix,level) {
  const sale=prefix==='spf'?'sp-sale':'product-sale';
  await value(cdp,`(()=>{
    const select=document.getElementById(${JSON.stringify(prefix+'ContactCollectionLevel')});
    if(!select?.closest(${JSON.stringify('#'+sale)}) || !select.offsetWidth)throw new Error('visible sale collection missing');
    const toast=document.getElementById('product-v3-toast');if(toast)toast.textContent='';
    select.value=${JSON.stringify(level)};select.dispatchEvent(new Event('change',{bubbles:true}));
    const save=[...document.querySelectorAll(${JSON.stringify('#'+sale+' button')})].find(b=>b.textContent.trim()==='保存当前维度');
    if(!save)throw new Error('sale save missing');save.click();return true;
  })()`);
  await wait(cdp,"document.getElementById('product-v3-toast')?.textContent.includes('已保存当前维度')",'save did not succeed');
}
async function reload(cdp,prefix,level) {
  await cdp.call('Page.reload',{ignoreCache:true});
  await wait(cdp,`document.getElementById(${JSON.stringify(prefix+'ContactCollectionLevel')})?.value===${JSON.stringify(level)}`,'reload lost collection');
}
async function checkout(cdp,prefix,level) {
  assert.ok(sessions[prefix+'/'+level],'synthetic session missing');
  // Independent synthetic customers and tab checkpoints preserve the real
  // pending-order/session protections rather than bypassing them.
  await value(cdp,'sessionStorage.clear();true');
  await cdp.call('Network.setCookie',{name:'aicrm_payment_session',value:sessions[prefix+'/'+level],url:base,path:'/',secure:true,httpOnly:true});
  await cdp.call('Emulation.setUserAgentOverride',{userAgent:'Mozilla/5.0 (iPhone) MicroMessenger/8.0.50 Mobile'});
  const route=prefix==='spf'?'/s/browser-push-service-period/pay':'/pay/browser-push-product';
  await navigate(cdp,base+route,"Boolean(document.getElementById('checkoutContent')&&!document.getElementById('checkoutContent').hidden&&document.getElementById('buy')&&!document.getElementById('buy').disabled)");
  assert.equal(await value(cdp,"Boolean(document.getElementById('mobilePanel')&&!document.getElementById('mobilePanel').hidden)"),level!=='none');
  assert.equal(await value(cdp,"Boolean(document.getElementById('shippingPanel')&&!document.getElementById('shippingPanel').hidden)"),level==='shipping_address');
  await value(cdp,"document.querySelector('input[name=paymentMethod][value=alipay]').click();true");
  const before=cdp.posts.length;
  if(level!=='none') {
    await value(cdp,"document.getElementById('buy').click();true");
    await wait(cdp,"!document.getElementById('mobileError').hidden&&document.getElementById('mobileError').textContent.includes('请填写手机号')",'empty phone feedback missing');
    assert.equal(cdp.posts.length,before,'empty phone issued checkout POST');
    await value(cdp,"document.getElementById('mobile').value='123';document.getElementById('buy').click();true");
    await wait(cdp,"document.getElementById('mobileError').textContent.includes('正确的大陆')",'invalid phone feedback missing');
    assert.equal(cdp.posts.length,before,'invalid phone issued checkout POST');
    await value(cdp,"document.getElementById('mobile').value='13800138000';true");
  }
  if(level==='shipping_address') {
    await value(cdp,"document.getElementById('buy').click();true");
    await wait(cdp,"!document.getElementById('provinceError').hidden",'empty shipping feedback missing');
    assert.equal(cdp.posts.length,before,'incomplete shipping issued checkout POST');
    await value(cdp,"(()=>{for(const [id,v] of [['province','11'],['city','1101'],['district','110101']]){const el=document.getElementById(id);el.value=v;el.dispatchEvent(new Event('change',{bubbles:true}));if(el.value!==v)throw new Error('real address option missing');}document.getElementById('recipientName').value='synthetic';document.getElementById('detailAddress').value='synthetic street';return true})()");
  }
  await value(cdp,"document.getElementById('buy').click();true");
  for(let i=0;i<240&&!cdp.responses.has(cdp.posts[before]);i++)await delay(50);
  assert.equal(cdp.posts.length,before+1,'valid contact creates one HTTP checkout');
  const request=cdp.posts[before];assert.equal(cdp.responses.get(request),202,'checkout not accepted');
  let response;
  for(let i=0;i<100;i++){try{response=await cdp.call('Network.getResponseBody',{requestId:request});break;}catch{await delay(50);}}
  assert.ok(response,'checkout receipt body unavailable');
  const receipt=JSON.parse(response.base64Encoded?Buffer.from(response.body,'base64').toString():response.body);
  assert.match(receipt.merchant_order_no,/^[-A-Za-z0-9_]+$/);
  console.log('contact-order:'+level+':'+receipt.merchant_order_no);
  await cdp.call('Emulation.setUserAgentOverride',{userAgent:'Mozilla/5.0'});
}
const profile=await fs.mkdtemp(path.join(os.tmpdir(),'aicrm-contact-chromium-'));
let child,childClosed,cdp,journeyError;
async function stopChromium(child) {
  if(!child)return;
  async function waitForClose() {
    let timer;
    try{return await Promise.race([childClosed.then(()=>true),new Promise(resolve=>{timer=setTimeout(()=>resolve(false),3000);})]);}
    finally{clearTimeout(timer);}
  }
  child.kill('SIGTERM');
  if(!await waitForClose()) {
    child.kill('SIGKILL');
    assert.ok(await waitForClose(),'Chromium did not close after SIGKILL');
  }
}
try {
  // Test-only self-signed loopback server, matching existing Host journeys.
  child=spawn(resolveChromiumBinary(),['--headless=new','--no-sandbox','--ignore-certificate-errors','--allow-insecure-localhost','--remote-debugging-port=0',`--user-data-dir=${profile}`,'about:blank'],{stdio:'ignore'});
  childClosed=new Promise(resolve=>child.once('close',resolve));
  let port;
  for(let i=0;i<160;i++){try{port=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];if(/^\d+$/.test(port))break;}catch{}await delay(50);}
  if(!/^\d+$/.test(port||''))throw new Error('Chromium did not start');
  const target=await(await fetch(`http://127.0.0.1:${port}/json/new?about:blank`,{method:'PUT'})).json();
  const socket=new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{socket.addEventListener('open',resolve,{once:true});socket.addEventListener('error',()=>reject(new Error('CDP connection failed')),{once:true});});
  cdp=new CDP(socket);await cdp.call('Page.enable');await cdp.call('Runtime.enable');await cdp.call('Network.enable');
  await cdp.call('Emulation.setDeviceMetricsOverride',{width:1440,height:1100,deviceScaleFactor:1,mobile:false});
  await navigate(cdp,base+'/login',"Boolean(document.querySelector('input[name=username]'))");
  await value(cdp,"(()=>{document.querySelector('input[name=username]').value='product-browser-owner';document.querySelector('input[name=password]').value='product-browser-owner-password';document.querySelector('button[type=submit]').click();return true})()");
  await wait(cdp,"!location.pathname.startsWith('/login')",'login did not succeed');
  for(const [prefix,id] of [['spf',period],['pf',standard]]) {
    const page=prefix==='spf'?'/admin/spProductForm.html':'/admin/productForm.html';
    await navigate(cdp,base+page+'?id='+id,`Boolean(document.getElementById(${JSON.stringify(prefix+'ContactCollectionLevel')}))`);
    await assertProductForm(cdp);
    for(const level of ['none','mobile','shipping_address']) {
      const before=await read(cdp,prefix,id);
      await save(cdp,prefix,level);
      const after=await read(cdp,prefix,id);
      assert.equal(after.admin_projection.contact_collection_level,level);
      assert.equal(after.admin_projection.require_mobile,level!=='none');
      assert.ok(after.version>before.version,'server CAS version advances');
      await reload(cdp,prefix,level);
      await checkout(cdp,prefix,level);
      await navigate(cdp,base+page+'?id='+id,`document.getElementById(${JSON.stringify(prefix+'ContactCollectionLevel')})?.value===${JSON.stringify(level)}`);
    }
    await value(cdp,`document.getElementById(${JSON.stringify(prefix+'ContactCollectionLevel')}).value='none';document.querySelector(${JSON.stringify('a[href="#'+(prefix==='spf'?'sp-action':'product-action')+'"]')}).click();true`);
    const action=prefix==='spf'?'sp-action':'product-action';
    await value(cdp,`(()=>{const toast=document.getElementById('product-v3-toast');if(toast)toast.textContent='';[...document.querySelectorAll(${JSON.stringify('#'+action+' button')})].find(b=>b.textContent.trim()==='保存当前维度').click();return true})()`);
    await wait(cdp,"document.getElementById('product-v3-toast')?.textContent.includes('已保存当前维度')",'dimension save failed');
    assert.equal((await read(cdp,prefix,id)).admin_projection.contact_collection_level,'shipping_address','unsaved sale draft leaked into action');
    await reload(cdp,prefix,'shipping_address');
    const screenshot=await cdp.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:true});
    await fs.writeFile(path.join(profile,prefix+'-contact.png'),Buffer.from(screenshot.data,'base64'));
  }
  for(const level of ['none','mobile','shipping_address']) {
    await navigate(cdp,base+'/admin/spProductForm.html',"Boolean(document.getElementById('spfContactCollectionLevel'))");
    await assertProductForm(cdp);
    await value(cdp,`(()=>{for(const [id,v] of Object.entries({spfName:'synthetic contact',spfCode:${JSON.stringify('new-contact-'+level)},spfPrice:'9.90',spfCurrency:'CNY',spfDurationDays:'30'})){const el=document.getElementById(id);if(!el)throw new Error('new field missing');el.value=v;el.dispatchEvent(new Event('input',{bubbles:true}));}return true})()`);
    await save(cdp,'spf',level);
    await wait(cdp,"/^[1-9][0-9]*$/.test(new URL(location.href).searchParams.get('id')||'')",'new period ID unavailable');
    const id=await value(cdp,"new URL(location.href).searchParams.get('id')");
    assert.equal((await read(cdp,'spf',id)).admin_projection.contact_collection_level,level);
    await reload(cdp,'spf',level);
  }
  assert.equal(cdp.errors,0,'browser runtime exceptions');
  const evidence=await fs.mkdtemp(path.join(os.tmpdir(),'aicrm-contact-screenshots-'));
  for(const prefix of ['pf','spf'])await fs.copyFile(path.join(profile,prefix+'-contact.png'),path.join(evidence,prefix+'-contact.png'));
  console.log(`periodic_contact_collection_chromium: PASS screenshots=${evidence}`);
} catch(error) {
  journeyError=error;
  throw error;
} finally {
  try {
    cdp?.socket.close();
    await stopChromium(child);
    // Chromium subprocesses can release profile cache files just after close.
    await fs.rm(profile,{recursive:true,force:true,maxRetries:5,retryDelay:100});
  } catch(cleanupError) {
    if(journeyError)throw new AggregateError([journeyError,cleanupError],'contact journey and Chromium cleanup failed');
    if(child?.exitCode===null&&child?.signalCode===null)throw cleanupError;
    if(!['ENOTEMPTY','EBUSY','EACCES','EPERM'].includes(cleanupError.code))throw cleanupError;
    console.warn(`contact journey cleanup warning: ${cleanupError.code}`);
  }
}
