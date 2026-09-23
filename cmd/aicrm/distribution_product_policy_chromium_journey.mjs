import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

process.env.NODE_TLS_REJECT_UNAUTHORIZED = "0";
const base = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_URL;
const username = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_USERNAME;
const password = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_PASSWORD;
const productID = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_PRODUCT_ID;
const serviceProductID = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_SERVICE_PRODUCT_ID;
const screenshotDir = process.env.AICRM_DISTRIBUTION_POLICY_BROWSER_SCREENSHOT_DIR;
if (!/^https:\/\//.test(base || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "")) throw new Error("Distribution policy Chromium journey environment is incomplete");
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
function browser() { for (const item of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) if ((item.includes("/") ? spawnSync(item,["--version"],{stdio:"ignore"}) : spawnSync("which",[item],{stdio:"ignore"})).status === 0) return item; throw new Error("Chromium is unavailable"); }
class CDP { constructor(socket) { this.socket=socket; this.id=0; this.pending=new Map(); this.requests=[]; socket.addEventListener("message", event => { const m=JSON.parse(String(event.data)); if(m.method === "Network.requestWillBeSent") this.requests.push(m.params.request); const p=this.pending.get(m.id); if (!p) return; this.pending.delete(m.id); m.error?p.reject(new Error(`CDP ${m.error.code}`)):p.resolve(m.result||{}); }); } call(method,params={}) { return new Promise((resolve,reject)=>{const id=++this.id,timer=setTimeout(()=>{this.pending.delete(id);reject(new Error(`CDP ${method} timed out`));},8000);this.pending.set(id,{resolve:v=>{clearTimeout(timer);resolve(v)},reject});this.socket.send(JSON.stringify({id,method,params}));}); } }
async function endpoint(profile) { for(let i=0;i<160;i++){try { const port=(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]; if(/^\d+$/.test(port))return `http://127.0.0.1:${port}`; }catch{} await sleep(50);} throw new Error("Chromium DevTools did not start"); }
async function value(cdp,expression) { const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails)throw new Error(`page evaluation failed: ${result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'unknown error'}`); return result.result?.value; }
async function wait(cdp,expression,message) { for(let i=0;i<180;i++){if(await value(cdp,expression))return;await sleep(50);} throw new Error(message); }
async function addCookie(cdp,name,value) { await cdp.call("Network.setCookie",{url:base,name,value,secure:true}); }
async function capturePolicyForm(cdp, label) { if (!screenshotDir) return; await fs.mkdir(screenshotDir,{recursive:true}); for (const width of [1440,1280]) { await cdp.call("Emulation.setDeviceMetricsOverride",{width,height:1000,deviceScaleFactor:1,mobile:false}); const image=await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:true}); await fs.writeFile(path.join(screenshotDir,`distribution-policy-${label}-${width}.png`),Buffer.from(image.data,"base64")); } }
async function login(cdp) { const page=await fetch(`${base}/login`,{redirect:"manual"}); const html=await page.text(), csrf=/name="login_csrf_token" value="([^"]+)"/.exec(html)?.[1]; if(!csrf)throw new Error("login CSRF unavailable"); const cookies=(typeof page.headers.getSetCookie==="function"?page.headers.getSetCookie():[]).map(value=>value.split(";",1)[0]).join("; "); const response=await fetch(`${base}/login`,{method:"POST",redirect:"manual",headers:{"Content-Type":"application/x-www-form-urlencoded",Cookie:cookies},body:new URLSearchParams({username,password,login_csrf_token:csrf})}); if(response.status!==303)throw new Error(`admin login status=${response.status}`); for(const raw of response.headers.getSetCookie?.()||[]){const pair=raw.split(";",1)[0],index=pair.indexOf("=");if(index>0)await addCookie(cdp,pair.slice(0,index),pair.slice(index+1));} }
async function policySnapshot(cdp) {
  return value(cdp, `(() => { const host=document.querySelector('[data-distribution-policy]'); return {version:host?.dataset.distributionPolicyVersion,enabled:host?.querySelector('[data-distribution-policy-enabled]')?.checked,rate:host?.querySelector('[data-distribution-policy-rate]')?.value,wait_days:host?.querySelector('[data-distribution-policy-wait-days]')?.value,policy_parent:host?.parentElement?.id || '',has_application:Boolean(document.querySelector('[data-distribution-application-entry],[data-distribution-application-link],[data-distribution-application-qr],[data-distribution-application-pending]'))}; })()`);
}
async function reloadEditor(cdp, page) {
  await cdp.call("Page.reload", {ignoreCache:true});
  await wait(cdp,"document.readyState === 'complete'",`editor reload did not complete ${page}`);
}
async function waitForPolicy(cdp, expected, page) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    const actual = await policySnapshot(cdp);
    if (actual?.version === String(expected.version) && actual.enabled === expected.enabled && Number(actual.rate) === Number(expected.rate) && actual.wait_days === String(expected.waitDays) && actual.policy_parent === expected.salePanel && actual.has_application === false) return actual;
    await sleep(50);
  }
  throw new Error(`policy readback mismatch ${page} expected=${JSON.stringify(expected)} actual=${JSON.stringify(await policySnapshot(cdp))}`);
}
async function assertProductTagToggleStyle(cdp, page) {
  await wait(cdp, "Boolean(document.querySelector('[data-product-tag-enabled]'))", `product tag toggle did not load ${page}`);
  const state = await value(cdp, `(async () => {
    const enabled=document.querySelector('[data-product-tag-enabled]');
    if (!(enabled instanceof HTMLInputElement)) throw new Error('product tag toggle missing');
    enabled.checked=false; enabled.dispatchEvent(new Event('change',{bubbles:true}));
    await new Promise(resolve => setTimeout(resolve, 180));
    const uncheckedBackground=getComputedStyle(enabled).backgroundColor;
    const uncheckedTransform=getComputedStyle(enabled,'::before').transform;
    enabled.checked=true; enabled.dispatchEvent(new Event('change',{bubbles:true}));
    await new Promise(resolve => setTimeout(resolve, 180));
    const picker=enabled.closest('[data-product-standard-tag-picker]');
    const controls=picker?.querySelector('[data-product-tag-controls]');
    const label=picker?.querySelector('.product-standard-tag-picker__label');
    const action=picker?.querySelector('[data-product-tag-open]');
    const summary=picker?.querySelector('[data-product-tag-summary]');
    return {checked:enabled.checked,uncheckedBackground,uncheckedTransform,checkedBackground:getComputedStyle(enabled).backgroundColor,checkedTransform:getComputedStyle(enabled,'::before').transform,layout:{pickerDisplay:picker&&getComputedStyle(picker).display,controlsDisplay:controls&&getComputedStyle(controls).display,labelDisplay:label&&getComputedStyle(label).display,actionHeight:action&&getComputedStyle(action).height,actionBorderColor:action&&getComputedStyle(action).borderTopColor,summaryPadding:summary&&getComputedStyle(summary).paddingTop}};
  })()`);
  if (!state?.checked || state.uncheckedBackground !== 'rgb(203, 213, 225)' || state.checkedBackground !== 'rgb(51, 112, 255)' || state.uncheckedTransform !== 'none' || state.checkedTransform !== 'matrix(1, 0, 0, 1, 18, 0)' || state.layout?.pickerDisplay !== 'grid' || state.layout?.controlsDisplay !== 'flex' || state.layout?.labelDisplay !== 'flex' || state.layout?.actionHeight !== '36px' || state.layout?.actionBorderColor !== 'rgb(222, 224, 227)' || state.layout?.summaryPadding !== '12px') throw new Error(`product tag checked switch style did not apply ${page}: ${JSON.stringify(state)}`);
  await capturePolicyForm(cdp, `tag-toggle-checked-${page}`);
  await value(cdp, "(() => { const enabled=document.querySelector('[data-product-tag-enabled]'); enabled.checked=false; enabled.dispatchEvent(new Event('change',{bubbles:true})); return true; })()");
}
async function savePolicy(cdp, enabledState, rate, waitDays, page) {
  await value(cdp, `(() => {
    const enabled=document.querySelector('[data-distribution-policy-enabled]');
    const commission=document.querySelector('[data-distribution-policy-rate]');
    const days=document.querySelector('[data-distribution-policy-wait-days]');
    if (!enabled || !commission || !days) throw new Error('distribution policy controls missing');
    const toast=document.querySelector('#product-v3-toast'); if (toast) toast.textContent='';
    enabled.checked=${enabledState}; enabled.dispatchEvent(new Event('change',{bubbles:true}));
    commission.value=${JSON.stringify(rate)}; commission.dispatchEvent(new Event('input',{bubbles:true}));
    days.value=${JSON.stringify(String(waitDays))}; days.dispatchEvent(new Event('input',{bubbles:true}));
    const save=[...document.querySelectorAll('button')].find(button=>button.textContent.trim()==='保存当前维度' && !button.closest('#product-push') && !button.closest('#sp-push'));
    if(!save) throw new Error('product save control missing'); save.click(); return true;
  })()`);
  await wait(cdp,"document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度')",`product save did not finish ${page}`);
}
async function saveOtherDimension(cdp, id, page, expectedPolicy) {
  await value(cdp, `(() => { const link=document.querySelector('a[href="#${id}"]'); if (!link) throw new Error('dimension link missing: ${id}'); link.click(); return true; })()`);
  await wait(cdp, `document.querySelector('a[href="#${id}"]')?.getAttribute('aria-current') === 'step'`, `dimension did not become active: ${id}`);
  const visible = await value(cdp, `(() => { const panel=document.getElementById(${JSON.stringify(id)}); const policy=document.querySelector('[data-distribution-policy]'); return {has_policy_in_panel:Boolean(panel?.querySelector('[data-distribution-policy]')), policy_parent:policy?.parentElement?.id || '', has_application:Boolean(document.querySelector('[data-distribution-application-entry],[data-distribution-application-link],[data-distribution-application-qr],[data-distribution-application-pending]'))}; })()`);
  assert.equal(visible.has_policy_in_panel, false, `${id} must not render distribution controls`);
  assert.equal(visible.has_application, false, `${id} must not render a distributor application entry`);
  if (id.endsWith('wecom')) await assertProductTagToggleStyle(cdp, id);
  // External push owns separate configuration. With its disabled, unchanged
  // fixture it emits no Product command, so this policy journey verifies its
  // absence and policy readback without inventing a completed product save.
  if (id.endsWith('push')) {
    const serverPolicy = await value(cdp, "fetch(location.pathname.includes('spProductForm') ? '/api/admin/service-period-products/" + serviceProductID + "' : '/api/v1/products/" + productID + "').then(response => response.json()).then(value => value.product?.distribution_policy || value.distribution_policy)");
    assert.deepEqual(serverPolicy, expectedPolicy, `${id} rewrote the persisted policy`);
    return;
  }
  const saved = await value(cdp, `(() => { const node=document.querySelector('#product-v3-toast'); if (node) node.textContent=''; const save=[...document.querySelectorAll('button')].find(button=>button.textContent.trim()==='保存当前维度' && !button.hidden && !button.closest('#product-push,#sp-push')); if (!save) throw new Error('dimension save missing: ${id}'); save.click(); return true; })()`);
  assert.equal(saved, true, `${id} save action was not invoked`);
  try { await wait(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度')", `dimension save did not complete: ${id}`); }
  catch (error) { throw new Error(`${error instanceof Error ? error.message : error} toast=${await value(cdp,"document.querySelector('#product-v3-toast')?.textContent || document.querySelector('#fb-toast')?.textContent || ''")}`); }
  const serverPolicy = await value(cdp, "fetch(location.pathname.includes('spProductForm') ? '/api/admin/service-period-products/" + serviceProductID + "' : '/api/v1/products/" + productID + "').then(response => response.json()).then(value => value.product?.distribution_policy || value.distribution_policy)");
  assert.deepEqual(serverPolicy, expectedPolicy, `${id} save rewrote the persisted policy`);
}
async function saveAndReload(cdp, page, rate, waitDays) {
  await cdp.call("Page.navigate",{url:base+page});
  await wait(cdp,"Boolean(document.querySelector('[data-distribution-policy]'))",`policy controls did not load ${page}`);
  await value(cdp,"(() => { const prior=window.fetch; window.__distributionPolicyWrites=[]; window.fetch=(input,init) => { const request=input instanceof Request ? input : undefined; window.__distributionPolicyWrites.push({url:String(request?.url || input),method:String(init?.method || request?.method || 'GET'),body:String(init?.body || '')}); return prior(input,init); }; return true; })()");
  const before=await value(cdp,"document.querySelector('[data-distribution-policy]')?.dataset.distributionPolicyVersion");
  assert.equal(before,"0",`fresh fixture must load a revision-zero policy on ${page}`);
  const expectedType=page.includes('spProductForm')?'service_period':'standard_product';
  const salePanel=page.includes('spProductForm')?'sp-sale':'product-sale';

  await savePolicy(cdp, true, rate, waitDays, page);
  await reloadEditor(cdp, page);
  await waitForPolicy(cdp,{version:1,enabled:true,rate,waitDays,salePanel},page);

  const draftRate='23.45';
  await value(cdp, `(() => { const rate=document.querySelector('[data-distribution-policy-rate]'); if (!rate) throw new Error('sale commission input missing'); rate.value=${JSON.stringify(draftRate)}; rate.dispatchEvent(new Event('input',{bubbles:true})); return true; })()`);
  const otherDimensions=page.includes('spProductForm')?['sp-media','sp-action','sp-wecom','sp-push']:['product-media','product-action','product-wecom','product-push'];
  const initialPolicy={enabled:true,commission_rate_basis_points:Math.round(Number(rate)*100),wait_days:waitDays,version:1};
  for (const id of otherDimensions) await saveOtherDimension(cdp,id,page,initialPolicy);
  await value(cdp, `(() => { const sale=document.querySelector('a[href="#${salePanel}"]'); sale?.click(); return document.querySelector('[data-distribution-policy-rate]')?.value; })()`);
  assert.equal((await policySnapshot(cdp)).rate,draftRate,'returning to sale information must retain its unsaved commission draft');

  await savePolicy(cdp, true, draftRate, waitDays, page);
  await reloadEditor(cdp, page);
  await waitForPolicy(cdp,{version:2,enabled:true,rate:draftRate,waitDays,salePanel},page);

  await savePolicy(cdp, false, draftRate, waitDays, page);
  const writes = await value(cdp, "window.__distributionPolicyWrites");
  const serverPolicy = await value(cdp, "fetch(location.pathname.includes('spProductForm') ? '/api/admin/service-period-products/" + serviceProductID + "' : '/api/v1/products/" + productID + "').then(response => response.text())");
  await reloadEditor(cdp, page);
  await waitForPolicy(cdp,{version:3,enabled:false,rate:draftRate,waitDays,salePanel},page);
  await capturePolicyForm(cdp,expectedType);
  if (!String(serverPolicy).includes('"version":3') || !String(serverPolicy).includes('"enabled":false') || !String(serverPolicy).includes('"commission_rate_basis_points":2345')) throw new Error(`policy save/readback mismatch ${page} writes=${JSON.stringify(writes)} server=${serverPolicy}`);
}

async function createWithPolicy(cdp, page, code, rate, waitDays) {
  const prefix=page.includes('spProductForm')?'spf':'pf';
  await cdp.call("Page.navigate",{url:base+page});
  await wait(cdp,prefix==='spf'?"Boolean(document.querySelector('[data-distribution-policy]') && document.getElementById('spfDurationDays'))":"Boolean(document.querySelector('[data-distribution-policy]'))",`new-product policy controls did not load ${page}`);
  const subjectPath=prefix==='spf'?'/api/admin/service-period-products':'/api/v1/products';
  const expected={enabled:true,commission_rate_basis_points:Math.round(Number(rate)*100),wait_days:waitDays,version:0};
  await value(cdp,"(() => { const prior=window.fetch; window.__distributionNewProductWrites=[]; window.fetch=(input,init) => { const request=input instanceof Request ? input : undefined; const entry={url:String(request?.url || input),method:String(init?.method || request?.method || 'GET'),body:String(init?.body || ''),idempotency_key:new Headers(init?.headers || request?.headers).get('Idempotency-Key')}; window.__distributionNewProductWrites.push(entry); return prior(input,init).then(response=>response.clone().text().then(text=>{entry.status=response.status;entry.response=text;return response;})); }; return true; })()");
  await value(cdp, `(() => {
    const fields={name:document.getElementById(${JSON.stringify(prefix + 'Name')}),code:document.getElementById(${JSON.stringify(prefix + 'Code')}),price:document.getElementById(${JSON.stringify(prefix + 'Price')}),currency:document.getElementById(${JSON.stringify(prefix + 'Currency')}),stock:document.getElementById(${JSON.stringify(prefix + 'Stock')}),duration:document.getElementById('spfDurationDays'),enabled:document.querySelector('[data-distribution-policy-enabled]'),rate:document.querySelector('[data-distribution-policy-rate]'),days:document.querySelector('[data-distribution-policy-wait-days]')};
    if([fields.name,fields.code,fields.price,fields.currency,fields.stock,fields.enabled,fields.rate,fields.days].some(value=>!value)||(${JSON.stringify(prefix === 'spf')}&&!fields.duration)) throw new Error('new-product sale controls missing');
    const update=(field,value)=>{field.value=value;field.dispatchEvent(new Event('input',{bubbles:true}));};
    update(fields.name,${JSON.stringify(`新建${prefix}分销商品`)}); update(fields.code,${JSON.stringify(code)}); update(fields.price,'19.99'); update(fields.currency,'CNY'); update(fields.stock,'3'); if(${JSON.stringify(prefix === 'spf')}) update(fields.duration,'90');
    fields.enabled.checked=true; fields.enabled.dispatchEvent(new Event('change',{bubbles:true})); update(fields.rate,${JSON.stringify(rate)}); update(fields.days,${JSON.stringify(String(waitDays))});
    const save=[...document.querySelectorAll('button')].find(button=>button.textContent.trim()==='保存当前维度'&&!button.closest('#product-push,#sp-push'));
    if(!save) throw new Error('new-product sale save missing'); save.click(); return true;
  })()`);
  try { await wait(cdp,"/^[1-9][0-9]*$/.test(new URL(location.href).searchParams.get('id') || '')",`new product did not retain an ID ${page}`); }
  catch (error) { const detail=await value(cdp,"({toast:document.querySelector('#product-v3-toast')?.textContent||document.querySelector('#fb-toast')?.textContent||'',writes:window.__distributionNewProductWrites||[]})"); throw new Error(`${error instanceof Error ? error.message : error} detail=${JSON.stringify(detail)}`); }
  const verified=await value(cdp, `(() => { const id=Number(new URL(location.href).searchParams.get('id')); return fetch(${JSON.stringify(subjectPath)}+'/'+id).then(response=>response.json()).then(server=>({id,writes:window.__distributionNewProductWrites,server:server.product||server,host:{version:document.querySelector('[data-distribution-policy]')?.dataset.distributionPolicyVersion,enabled:document.querySelector('[data-distribution-policy-enabled]')?.checked,rate:document.querySelector('[data-distribution-policy-rate]')?.value,days:document.querySelector('[data-distribution-policy-wait-days]')?.value}})); })()`);
  assert.ok(Number.isSafeInteger(verified.id) && verified.id > 0, `new product ID is invalid ${page}`);
  const subjectWrites=verified.writes.filter(write=>new URL(write.url,base).pathname===subjectPath && write.method==='POST');
  assert.equal(subjectWrites.length,1,`new product must issue one Product create command ${page}`);
  assert.equal(verified.writes.filter(write=>write.method==='POST'&&new URL(write.url,base).pathname!==subjectPath).length,0,`new product must not issue an extra policy POST ${page}`);
  // The page-level recorder sees the donor request before the Host adapts it.
  // CDP observes the wire request, including the Product command key and the
  // service-period duration added by the Host.
  const finalSubjectWrites=cdp.requests.filter(request=>new URL(request.url).pathname===subjectPath && request.method==='POST');
  assert.equal(finalSubjectWrites.length,1,`new product must send one Product create command ${page}`);
  const finalBody=JSON.parse(finalSubjectWrites[0].postData || '{}');
  assert.deepEqual(finalBody.distribution_policy,expected,`new product wire command must carry its edited policy ${page}`);
  assert.match(String(finalSubjectWrites[0].headers['idempotency-key'] || ''),/^product-save-/,`new product Host save context did not supply the Product command key ${page}`);
  if(prefix==='spf') assert.equal(finalBody.duration_days,90,`new periodic product wire command must carry its explicit duration ${page}`);
  const persisted=verified.server.distribution_policy;
  assert.deepEqual(persisted,{...expected,version:1},`new product server readback lost policy ${page}`);
  assert.deepEqual(verified.host,{version:'1',enabled:true,rate:Number(rate).toFixed(2),days:String(waitDays)},`new product editor did not retain server policy ${page}`);
}

const profile=await fs.mkdtemp(path.join(os.tmpdir(),"aicrm-distribution-policy-chromium-")); let child,cdp;
try { child=spawn(browser(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:"ignore"}); const page=await (await fetch(`${await endpoint(profile)}/json/new?about:blank`,{method:"PUT"})).json(); const socket=new WebSocket(page.webSocketDebuggerUrl); await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",()=>reject(new Error("CDP connection failed")),{once:true});}); cdp=new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable"); await login(cdp); await saveAndReload(cdp,`/admin/wechat-pay/productForm.html?id=${productID}`,"12.34",8); await saveAndReload(cdp,`/admin/wechat-pay/spProductForm.html?id=${serviceProductID}`,"30.00",0); await createWithPolicy(cdp,'/admin/wechat-pay/productForm.html','browser-new-distribution-standard','12.34',8); await createWithPolicy(cdp,'/admin/wechat-pay/spProductForm.html','browser-new-distribution-period','23.45',9); console.log("distribution_product_policy_chromium: PASS"); } finally { if(cdp)cdp.socket.close(); if(child&&child.exitCode===null){child.kill("SIGTERM");await Promise.race([new Promise(resolve=>child.once("exit",resolve)),sleep(3000)]);if(child.exitCode===null)child.kill("SIGKILL");} await fs.rm(profile,{recursive:true,force:true}).catch(()=>{}); }
