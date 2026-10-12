import { resolveChromiumBinary } from "../../internal/webshell/chromium_binary.mjs";
import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const base = process.env.AICRM_VARIANT_BROWSER_URL;
const username = 'product-browser-owner';
const password = 'product-browser-owner-password';
const productID = process.env.AICRM_VARIANT_BROWSER_PRODUCT_ID;
const serviceProductID = '1';
const screenshotDir = process.env.AICRM_VARIANT_VISUAL_DIR;
if (!/^https:\/\//.test(base || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "")) throw new Error("Distribution policy Chromium journey environment is incomplete");
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const browser = resolveChromiumBinary;
class CDP { constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); this.requests=[]; socket.addEventListener("message", event => { const m=JSON.parse(String(event.data)); if(m.method === "Network.requestWillBeSent") this.requests.push(m.params.request); const p=this.pending.get(m.id); if (!p) return; this.pending.delete(m.id); m.error?p.reject(new Error(`CDP ${m.error.code}`)):p.resolve(m.result||{}); }); } call(method,params={}) { return new Promise((resolve,reject)=>{const id=++this.id,timer=setTimeout(()=>{this.pending.delete(id);reject(new Error(`CDP ${method} timed out`));},8000);this.pending.set(id,{resolve:v=>{clearTimeout(timer);resolve(v)},reject});this.socket.send(JSON.stringify({id,method,params}));}); } }
async function endpoint(profile) { for(let i=0;i<160;i++){try { const port=(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]; if(/^\d+$/.test(port))return `http://127.0.0.1:${port}`; }catch{} await sleep(50);} throw new Error("Chromium DevTools did not start"); }
async function value(cdp,expression) { const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails)throw new Error(`page evaluation failed: ${result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'unknown error'}`); return result.result?.value; }
async function wait(cdp,expression,message) { for(let i=0;i<180;i++){if(await value(cdp,expression))return;await sleep(50);} throw new Error(message); }
async function addCookie(cdp,name,value) { await cdp.call("Network.setCookie",{url:base,name,value,secure:true}); }
async function login(cdp) { const page=await fetch(`${base}/login`,{redirect:"manual"}); const html=await page.text(), csrf=/name="login_csrf_token" value="([^"]+)"/.exec(html)?.[1]; if(!csrf)throw new Error("login CSRF unavailable"); const cookies=(typeof page.headers.getSetCookie==="function"?page.headers.getSetCookie():[]).map(value=>value.split(";",1)[0]).join("; "); const response=await fetch(`${base}/login`,{method:"POST",redirect:"manual",headers:{"Content-Type":"application/x-www-form-urlencoded",Cookie:cookies},body:new URLSearchParams({username,password,login_csrf_token:csrf})}); if(response.status!==303)throw new Error(`admin login status=${response.status}`); for(const raw of response.headers.getSetCookie?.()||[]){const pair=raw.split(";",1)[0],index=pair.indexOf("=");if(index>0)await addCookie(cdp,pair.slice(0,index),pair.slice(index+1));} }
async function capture(cdp,name){if(!screenshotDir)return;await fs.mkdir(screenshotDir,{recursive:true});const shot=await cdp.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});await fs.writeFile(path.join(screenshotDir,name+'.png'),Buffer.from(shot.data,'base64'));}
const profile=await fs.mkdtemp(path.join(os.tmpdir(),'aicrm-variant-browser-'));let child,cdp;
try{
 child=spawn(browser(),['--headless=new','--no-sandbox','--remote-debugging-port=0',`--user-data-dir=${profile}`,'--ignore-certificate-errors','--allow-insecure-localhost','about:blank'],{stdio:'ignore'});
 const tab=await(await fetch(`${await endpoint(profile)}/json/new?about:blank`,{method:'PUT'})).json();const socket=new WebSocket(tab.webSocketDebuggerUrl);await new Promise((res,rej)=>{socket.addEventListener('open',res,{once:true});socket.addEventListener('error',rej,{once:true})});cdp=new CDP(socket);
 await cdp.call('Page.enable');await cdp.call('Runtime.enable');await cdp.call('Network.enable');await cdp.call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});await login(cdp);
 await cdp.call('Page.navigate',{url:base+`/admin/wechat-pay/productForm.html?id=${productID}`});await wait(cdp,`Boolean(document.querySelector('[data-product-variants]'))`,'variants editor missing');await capture(cdp,'admin-off');
 await value(cdp,`(()=>{const toggle=document.querySelector('[data-variants-enabled]');toggle.checked=true;toggle.dispatchEvent(new Event('change'));const add=[...document.querySelectorAll('[data-product-variants] button')].find(b=>b.textContent.includes('新增规格'));add.click();add.click();const rows=[...document.querySelectorAll('[data-variant-row]')];for(let i=0;i<2;i++){rows[i].querySelector('[data-variant-field="name"]').value=['单人版','三人成行版'][i];rows[i].querySelector('[data-variant-field="price"]').value=['99','999'][i];rows[i].querySelector('[data-variant-field="description"]').value=['个人学习版本','统一由购买人联系'][i]}const save=[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='保存当前维度');save.click();return true})()`);
 await wait(cdp,`[...document.querySelectorAll('[data-variant-row]')].length===2 && [...document.querySelectorAll('[data-variant-row]')].every(r=>Number(r.dataset.id)>0)`,'saved variant IDs missing');
 const saved=await value(cdp,`fetch('/api/v1/products/${productID}').then(r=>r.json())`);assert.equal(saved.variants_enabled,true);assert.deepEqual(saved.variants.map(v=>v.price_minor),[9900,99900]);assert.ok(saved.variants.every(v=>v.id>0&&v.version===1));
 await value(cdp,`(()=>{document.querySelector('[data-variant-field="name"]').value='';[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='保存当前维度').click()})()`);
 await wait(cdp,`document.body.textContent.includes('请为每个规格填写名称和有效售价')`,'invalid variant did not show actionable validation');
 const afterInvalid=await value(cdp,`fetch('/api/v1/products/${productID}').then(r=>r.json())`);assert.equal(afterInvalid.version,saved.version,'invalid draft must not save');
 await cdp.call('Page.reload',{ignoreCache:true});await wait(cdp,`document.querySelector('[data-variants-enabled]')?.checked===true && document.querySelectorAll('[data-variant-row]').length===2`,'variant readback missing');
 await wait(cdp,`document.body.textContent.includes('99.00～999.00')`,'saved variant price summary missing');
 await value(cdp,`document.querySelector('[data-product-variants]').scrollIntoView({block:'center'})`);await capture(cdp,'admin-variants');
 // Public checkout visual states use the server-rendered HTML and a read-only
 // synthetic authorization fixture. This does not claim a real Provider payment.
 if(screenshotDir){
 const html=await fs.readFile(path.join(screenshotDir,'multi.html'),'utf8');
 await cdp.call('Page.addScriptToEvaluateOnNewDocument',{source:`Object.defineProperty(navigator,'userAgent',{value:'MicroMessenger'});window.fetch=async(url)=>({ok:true,status:200,json:async()=>String(url).includes('checkout-session')?{checkout_session_binding:'a'.repeat(43),can_create_checkout:true}:String(url).includes('purchase-status')?{purchase_state:'available',can_purchase:true}:{items:[]}});`});
 await cdp.call('Emulation.setDeviceMetricsOverride',{width:393,height:852,deviceScaleFactor:1,mobile:true});
 await cdp.call('Page.navigate',{url:'data:text/html;charset=utf-8,'+encodeURIComponent(html)});await wait(cdp,`document.getElementById('variantChoice') && !document.getElementById('checkoutContent').hidden`,'public variants missing');
 await value(cdp,`(()=>{const s=document.getElementById('variantChoice');s.value='22';s.dispatchEvent(new Event('change'));return true})()`);await capture(cdp,'mobile-variants');
 }
 console.log('product_variants_chromium: PASS');
}finally{if(cdp)cdp.socket.close();if(child&&child.exitCode===null){child.kill('SIGTERM');await Promise.race([new Promise(r=>child.once('exit',r)),sleep(3000)]);if(child.exitCode===null)child.kill('SIGKILL')}await fs.rm(profile,{recursive:true,force:true}).catch(()=>{})}
