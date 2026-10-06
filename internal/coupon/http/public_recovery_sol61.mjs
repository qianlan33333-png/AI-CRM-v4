import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import {JSDOM, VirtualConsole} from 'jsdom';

const page = await fs.readFile(process.argv[2], 'utf8');
const wait = ms => new Promise(resolve => setTimeout(resolve, ms));
const failures = [];
for (const mode of ['fetch', 'body', 'without-abort']) {
  const navigations = [];
  const virtualConsole = new VirtualConsole();
  virtualConsole.on('jsdomError', error => {
    if (error.message.includes('navigation')) navigations.push(error.message);
    else failures.push(`${mode}: ${error.message}`);
  });
  const dom = new JSDOM(page, {url:'https://coupon.invalid/c/cp-sol61-recovery', runScripts:'outside-only', virtualConsole});
  const w = dom.window, calls = [];
  const schedule = w.setTimeout.bind(w);
  // Scale an existing finite transport wait; do not add a timeout to fetch.
  w.setTimeout = (fn, delay, ...args) => schedule(fn, delay >= 10_000 ? 5 : delay, ...args);
  if (mode === 'without-abort') w.AbortController = undefined;
  let release;
  w.fetch = async (url, init) => {
    calls.push({url, key:init.headers['Idempotency-Key'], body:init.body, binding:init.headers['X-AICRM-Coupon-Session-Binding']});
    if (calls.length > 1) return Response.json({ok:true, claim:{claim_id:1}});
    if (mode === 'body') return {ok:true, status:200, json:() => new Promise(resolve => { release = () => resolve({ok:true, claim:{claim_id:1}}); })};
    return new Promise(resolve => { release = () => resolve(Response.json({ok:true, claim:{claim_id:1}})); });
  };
  for (const script of w.document.scripts) w.eval(script.textContent);
  const button = w.document.getElementById('claimButton');
  try {
    button.click(); button.click();
    await wait(60);
    assert.equal(calls.length, 1, 'a second native click while submitting creates no new request');
    assert.equal(button.disabled, false, `${mode}: stalled transport must release the original manual recovery action`);
    assert.match(w.document.getElementById('claimMessage').textContent, /超时|未确认|暂未|稍后重试/);
    assert.equal(navigations.length, 0, 'a timeout cannot reload or replay automatically');
    release(); await wait(10);
    assert.equal(navigations.length, 0, 'late success cannot erase a timed-out original intent by reloading');
    assert.equal(button.disabled, false);
    button.click(); await wait(10);
    assert.equal(calls.length, 2, 'manual retry sends one request');
    assert.deepEqual(calls[1], calls[0], 'manual recovery retains the original key and body');
    assert.equal(navigations.length, 1, 'confirmed replay reloads once');
    assert.equal(w.sessionStorage.length, 0, 'confirmed replay clears pending before reload');
    console.log(`coupon-stalled-response ${mode}: PASS`);
  } catch (error) {
    failures.push(error.message);
    console.log(`coupon-stalled-response ${mode}: FAIL: ${error.message}`);
  } finally {
    w.close();
  }
}

// A new document may display an already committed claim and a disabled
// per-user-limit button. Its unresolved checkpoint must still recover the
// original request, rather than minting a second intent or disappearing.
const storageKey = 'aicrm.coupon.claim.tab.v1:cp-sol61-recovery';
const original = {key:'sol61-native-restore-original-0001',body:'{}',session_binding:'A'.repeat(43)};
for (const mode of ['same-session-limited','changed-session','storage-unavailable','storage-write-unavailable','storage-corrupt']) {
  const dom = new JSDOM(page,{url:'https://coupon.invalid/c/cp-sol61-recovery',runScripts:'outside-only'});
  const w = dom.window, calls=[];
  const main=w.document.querySelector('[data-public-slug]'), button=w.document.getElementById('claimButton');
  if (mode === 'storage-unavailable') Object.defineProperty(w,'sessionStorage',{get(){throw new Error('unavailable');}});
  else if(mode==='storage-write-unavailable')w.Storage.prototype.setItem=function(){throw new Error('quota unavailable');};
  else w.sessionStorage.setItem(storageKey,mode==='storage-corrupt'?'{broken':JSON.stringify(original));
  main.dataset.sessionBinding=mode==='changed-session'?'B'.repeat(43):original.session_binding;
  if(mode==='same-session-limited') button.disabled=true;
  w.fetch=async(_,init)=>{calls.push(init);return Response.json({ok:false,error:'coupon_unavailable'},{status:409});};
  for(const script of w.document.scripts) w.eval(script.textContent);
  try {
    assert.equal(calls.length,0,'document restoration never auto-posts');
    if(mode==='same-session-limited' || mode==='changed-session') {
      assert.equal(button.disabled,false,'pending confirmation remains available at SSR per-user limit');
      button.click();await wait(5);
      assert.equal(calls.length,1);
      assert.equal(calls[0].headers['Idempotency-Key'],original.key);
      assert.equal(calls[0].body,original.body);
      assert.equal(calls[0].headers['X-AICRM-Coupon-Session-Binding'],original.session_binding);
      if(mode==='changed-session')assert.equal(calls[0].headers['X-AICRM-Coupon-Recovery-Only'],'true','a changed token may only query an existing authorized receipt');
      else assert.equal(calls[0].headers['X-AICRM-Coupon-Recovery-Only'],undefined);
      assert.deepEqual(JSON.parse(w.sessionStorage.getItem(storageKey)),original);
    } else {
      if(mode==='storage-write-unavailable') {button.click();await wait(5);}
      assert.equal(button.disabled,true,'unverifiable checkpoint cannot create another intent');
      button.click();await wait(5);
      assert.equal(calls.length,0);
      if(mode==='changed-session') assert.deepEqual(JSON.parse(w.sessionStorage.getItem(storageKey)),original);
      if(mode==='storage-unavailable' || mode==='storage-write-unavailable' || mode==='storage-corrupt') assert.match(w.document.getElementById('claimMessage').textContent,/无法读取或保存.*核对/,'storage failures require a readable explanation');
    }
    console.log(`coupon-checkpoint-restoration ${mode}: PASS`);
  } catch(error) {failures.push(error.message);console.log(`coupon-checkpoint-restoration ${mode}: FAIL: ${error.message}`);}
  finally {w.close();}
}

// These are synthetic lifecycle events at the DOM contract layer only. The
// composed browser fixture separately records actual native pageshow values.
for(const mode of ['foreground-deadline','pagehide','replaced-checkpoint']) {
  const navigations=[], virtualConsole=new VirtualConsole();
  virtualConsole.on('jsdomError',error=>{if(error.message.includes('navigation'))navigations.push(error.message);else failures.push(error.message);});
  const dom=new JSDOM(page,{url:'https://coupon.invalid/c/cp-sol61-recovery',runScripts:'outside-only',virtualConsole});
  const w=dom.window,calls=[];
  const now=w.Date.now.bind(w.Date);
  w.setTimeout=(fn,delay)=>delay>=10_000?123:setTimeout(fn,delay);
  let release;
  w.fetch=async(_,init)=>{calls.push(init);return new Promise(resolve=>{release=()=>resolve(Response.json({ok:true,claim:{claim_id:1}}));});};
  for(const script of w.document.scripts)w.eval(script.textContent);
  const button=w.document.getElementById('claimButton');
  try {
    button.click();await wait(5);
    const checkpoint=JSON.parse(w.sessionStorage.getItem(storageKey));
    if(mode==='foreground-deadline') {w.Date.now=()=>now()+16_000;w.dispatchEvent(new w.Event('pageshow'));}
    else w.dispatchEvent(new w.Event('pagehide'));
    await wait(5);
    assert.equal(button.disabled,false,'foreground expiration or document suspension releases manual confirmation');
    assert.equal(calls.length,1);
    const replacement={...checkpoint,key:'sol61-replaced-new-intent-0001'};
    if(mode==='replaced-checkpoint')w.sessionStorage.setItem(storageKey,JSON.stringify(replacement));
    release();await wait(5);
    assert.equal(navigations.length,0,'a suspended/expired continuation never reloads');
    assert.deepEqual(JSON.parse(w.sessionStorage.getItem(storageKey)),mode==='replaced-checkpoint'?replacement:checkpoint,'late response cannot clear any pending intent');
    if(mode==='replaced-checkpoint') {button.click();await wait(5);assert.equal(calls.length,1,'a stale document does not overwrite a changed checkpoint');assert.equal(button.disabled,true);}
    console.log(`coupon-synthetic-lifecycle ${mode}: PASS`);
  }catch(error){failures.push(error.message);console.log(`coupon-synthetic-lifecycle ${mode}: FAIL: ${error.message}`);}
  finally{w.close();}
}
for(const mode of ['confirmed-storage-clear-unavailable','confirmed-storage-read-unavailable']) {
  const dom=new JSDOM(page,{url:'https://coupon.invalid/c/cp-sol61-recovery',runScripts:'outside-only'});
  const w=dom.window,storage=w.sessionStorage,calls=[];
  let committed=false;
  if(mode==='confirmed-storage-clear-unavailable')w.Storage.prototype.removeItem=function(){throw new Error('clear unavailable');};
  else Object.defineProperty(w,'sessionStorage',{get(){if(committed)throw new Error('read unavailable');return storage;}});
  w.fetch=async(_,init)=>{calls.push(init);return {ok:true,status:200,json:async()=>{committed=true;return {ok:true,claim:{claim_id:1}};}};};
  for(const script of w.document.scripts)w.eval(script.textContent);
  try {
    const button=w.document.getElementById('claimButton');
    button.click();await wait(5);
    assert.equal(calls.length,1);
    assert.equal(button.disabled,true,'a confirmed receipt with unreadable/unclearable checkpoint must immediately fail closed');
    assert.match(w.document.getElementById('claimMessage').textContent,/无法读取或保存.*核对/);
    assert.ok(storage.getItem(storageKey),'original checkpoint remains for verification');
    button.click();await wait(5);assert.equal(calls.length,1);
    console.log(`coupon-confirmed-storage ${mode}: PASS`);
  }catch(error){failures.push(error.message);console.log(`coupon-confirmed-storage ${mode}: FAIL: ${error.message}`);}
  finally{w.close();}
}
if (failures.length) throw new Error(failures.join('\n'));

// Current verified-subject authority can expire independently of an opaque
// cookie. A 401 must retain the intent and request authorization explicitly.
{
  const navigations=[],virtualConsole=new VirtualConsole();
  virtualConsole.on('jsdomError',error=>{if(error.message.includes('navigation'))navigations.push(error.message);else throw error;});
  const dom=new JSDOM(page,{url:'https://coupon.invalid/c/cp-sol61-recovery',runScripts:'outside-only',virtualConsole});
  const w=dom.window,calls=[];
  w.fetch=async(_,init)=>{calls.push(init);return Response.json({ok:false,error:'oauth_required'},{status:401});};
  for(const script of w.document.scripts)w.eval(script.textContent);
  try {
    const button=w.document.getElementById('claimButton');
    button.click();await wait(5);
    assert.equal(calls.length,1);
    assert.equal(w.document.getElementById('claimMessage').textContent,'请先使用原身份完成微信授权，再确认原领取结果');
    assert.equal(navigations.length,0,'authorization failure cannot redirect or post automatically');
    const original=JSON.parse(w.sessionStorage.getItem(storageKey));
    button.click();await wait(5);
    assert.equal(calls.length,2,'only a new explicit user action can retry');
    assert.equal(calls[1].headers['Idempotency-Key'],original.key);
    assert.equal(calls[1].body,original.body);
    assert.equal(calls[1].headers['X-AICRM-Coupon-Session-Binding'],original.session_binding);
    assert.deepEqual(JSON.parse(w.sessionStorage.getItem(storageKey)),original,'401 keeps the complete original intent');
    console.log('coupon-authority-oauth-required: PASS');
  }finally{w.close();}
}
