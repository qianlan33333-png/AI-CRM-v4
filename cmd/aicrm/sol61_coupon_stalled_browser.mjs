import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';

const e = process.env;
const {webkit, chromium, devices} = createRequire(path.join(e.SOL61_BROWSER_ROOT, 'package.json'))('playwright');
const browser = await (e.SOL61_BROWSER_KIND === 'chromium' ? chromium : webkit).launch({headless:true, executablePath:e.SOL61_BROWSER_DRIVER});
const base = e.SOL61_BROWSER_BASE, slugs = JSON.parse(e.SOL61_BROWSER_SLUGS), dir = e.SOL61_BROWSER_DIR;
const sleep = ms => new Promise(resolve => setTimeout(resolve, ms));
const until = async (read, message, ms = 18_000) => {
  const end = Date.now() + ms;
  while (Date.now() < end) { if (await read()) return; await sleep(100); }
  throw new Error(message);
};
const receipt = {engine:browser.version(), status:'PASS', results:[], errors:[], limits:['actual desktop browser engine with iPhone device parameters and WeChat UA; no physical phone', 'only loopback HTTP and PostgreSQL; no provider worker or payment mutation', 'response headers/body are held by the local server after actual claim commit']};
try {
  if (e.SOL61_BROWSER_KIND !== 'chromium') assert.equal(browser.version(), e.SOL61_BROWSER_ENGINE);
  for (const mode of ['fetch', 'body']) {
    const context = await browser.newContext({...devices['iPhone 12'], ignoreHTTPSErrors:true, userAgent:devices['iPhone 12'].userAgent+' MicroMessenger/8.0'});
    await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
    await context.addCookies([{name:e.SOL61_BROWSER_COOKIE, value:e.SOL61_BROWSER_SESSION, url:base, httpOnly:true}]);
    await context.addInitScript(() => {
      window.addEventListener('pageshow', event => {
        window.__sol61NativeShow = {persisted:event.persisted, navigationType:performance.getEntriesByType('navigation')[0]?.type || ''};
      });
    });
    // Fetch case also proves correctness when an embedded browser transport
    // cannot use AbortController. The independent deadline must settle it.
    if (mode === 'fetch') await context.addInitScript(() => {window.AbortController = undefined;});
    const page = await context.newPage(), calls = [], errors = [];
    page.on('pageerror', error => errors.push(error.message));
    page.on('request', request => {
      if (new URL(request.url()).pathname === `/api/h5/coupons/${slugs[mode]}/claim` && request.method() === 'POST') calls.push({key:request.headers()['idempotency-key'], body:request.postData(), binding:request.headers()['x-aicrm-coupon-session-binding']});
    });
    const button = page.locator('#claimButton');
    try {
      await page.goto(base+'/c/'+slugs[mode]);
      await button.waitFor({state:'visible'});
      assert.equal(await button.isEnabled(), true);
      await page.screenshot({path:path.join(dir, mode+'-01-ready.png'), fullPage:true});
      const target = await button.boundingBox();
      await button.tap();
      await page.touchscreen.tap(target.x+target.width/2, target.y+target.height/2);
      await until(() => calls.length === 1, 'first actual claim request was not observed', 5_000);
      await page.screenshot({path:path.join(dir, mode+'-02-pending.png'), fullPage:true});
      try {await until(() => button.isEnabled(), mode+': original manual action remains disabled after the finite wait');}
      finally {await page.screenshot({path:path.join(dir, mode+'-03-after-wait.png'), fullPage:true});}
      assert.equal(calls.length, 1, 'waiting does not automatically replay');
      const independentTab = await context.newPage(), independentPosts=[];
      independentTab.on('request', request=>{if(request.method()==='POST')independentPosts.push(request.url());});
      await independentTab.goto(base+'/c/'+slugs[mode]);
      assert.equal(await independentTab.evaluate(() => sessionStorage.length),0,'an independently opened tab has no unresolved intent to replay');
      assert.equal((await independentTab.locator('#claimButton').textContent()).trim(),'再领一张','a different tab may offer an explicit new claim under the existing rule');
      assert.deepEqual(independentPosts,[],'opening another tab never auto-posts');
      await independentTab.close();
      await context.request.post(base+'/__sol61__/release?slug='+slugs[mode]);
      await sleep(300);
      assert.equal(calls.length, 1);
      assert.equal(await button.isEnabled(), true);
      assert.doesNotMatch(await page.locator('#claimMessage').textContent(), /领取成功/, 'late success must not reload/complete after deadline');
      await page.reload();
      await button.waitFor({state:'visible'});
      assert.equal(await button.isEnabled(), true, 'a pending original intent must keep its manual confirmation action after refresh');
      await page.goto(base+'/__sol61__/away');
      await page.goBack();
      await until(() => button.isEnabled(), 'native history return lost the pending confirmation');
      const pendingNativeReturn = await page.evaluate(() => window.__sol61NativeShow);
      assert.equal(calls.length, 1, 'pending native history return never auto-posts');
      await page.screenshot({path:path.join(dir, mode+'-03b-pending-native-back.png'), fullPage:true});
      await button.tap();
      await until(async () => (await page.locator('#claimMessage').textContent()).includes('领取成功') && await button.isEnabled(), 'manual original-key replay did not recover real committed claim');
      assert.equal(calls.length, 2);
      assert.deepEqual(calls[1], calls[0], 'manual retry uses original key and body');
      await page.screenshot({path:path.join(dir, mode+'-04-recovered.png'), fullPage:true});
      await page.goto(base+'/__sol61__/away');
      await page.goBack();
      await until(async () => (await page.locator('#claimMessage').textContent()).includes('领取成功'), 'native history return lost the actual claim display');
      const nativeReturn = await page.evaluate(() => window.__sol61NativeShow);
      assert.equal(calls.length, 2, 'native back must not create a new claim');
      await page.screenshot({path:path.join(dir, mode+'-05-native-back.png'), fullPage:true});
      await page.reload();
      await until(async () => (await page.locator('#claimMessage').textContent()).includes('领取成功'), 'refresh failed to read actual claim display');
      assert.equal(calls.length, 2, 'refresh must not create a new claim');
      assert.equal(await button.isEnabled(), true, 'published rules still allow an explicit second claim');
      await button.tap();
      await until(async () => calls.length === 3 && (await page.locator('#claimMessage').textContent()).includes('领取成功') && await button.isEnabled(), 'explicit second claim did not complete');
      assert.notEqual(calls[2].key,calls[1].key,'an explicit new claim after confirmed completion gets a new key');
      assert.equal(calls[2].body,'{}');
      assert.equal(calls[2].binding,calls[1].binding);
      await page.screenshot({path:path.join(dir,mode+'-06-explicit-next-claim.png'),fullPage:true});
      assert.deepEqual(errors, []);
      receipt.results.push({mode, status:'PASS', requests:3, sameKey:true, sameBody:true, sameBinding:true, pendingReloadOriginalKey:true, secondNativeTapIgnored:true, independentTabDoesNotAutoPost:true, pendingNativeReturn, nativeReturn, refreshWithoutPost:true, explicitNextClaimNewKey:true, pageErrors:errors});
    } catch (error) {
      receipt.status = 'FAIL';
      receipt.results.push({mode, status:'FAIL', requests:calls.length, failure:error.message, pageErrors:errors});
    } finally {
      await context.request.post(base+'/__sol61__/release?slug='+slugs[mode]);
      await context.close();
    }
  }
  {
    const context = await browser.newContext({...devices['iPhone 12'], ignoreHTTPSErrors:true, userAgent:devices['iPhone 12'].userAgent+' MicroMessenger/8.0'});
    await context.route('**/*', route => new URL(route.request().url()).origin === base ? route.continue() : route.abort());
    await context.addCookies([{name:e.SOL61_BROWSER_COOKIE, value:e.SOL61_BROWSER_SESSION, url:base, httpOnly:true}]);
    const page = await context.newPage(), calls = [], errors = [], expectedNetworkAbortDiagnostics = [];
    const abortedURL = base+'/api/h5/coupons/'+slugs.switch+'/claim';
    const abortedTarget = new URL(abortedURL);
    const expectedAbortMessages = [abortedURL+'.', '/'+abortedTarget.host+abortedTarget.pathname+'.'];
    page.on('pageerror', error => {
      // Old WebKit reports the harness route.abort as this exact URL-only
      // diagnostic. Preserve it separately; unexpected script errors fail.
      if(expectedAbortMessages.includes(error.message) && calls.length === 1) expectedNetworkAbortDiagnostics.push(error.message);
      else errors.push(error.message);
    });
    page.on('request', request => {
      if (new URL(request.url()).pathname === `/api/h5/coupons/${slugs.switch}/claim` && request.method() === 'POST') calls.push({key:request.headers()['idempotency-key'], body:request.postData(), binding:request.headers()['x-aicrm-coupon-session-binding'], recoveryOnly:request.headers()['x-aicrm-coupon-recovery-only']});
    });
    await page.route('**/api/h5/coupons/'+slugs.switch+'/claim', route => calls.length === 1 ? route.abort('failed') : route.continue());
    try {
      await page.goto(base+'/c/'+slugs.switch);
      const button = page.locator('#claimButton');
      await button.tap();
      await until(async () => calls.length === 1 && await button.isEnabled(), 'local no-commit failure did not expose manual original-intent retry', 5_000);
      await context.addCookies([{name:e.SOL61_BROWSER_COOKIE, value:e.SOL61_BROWSER_OTHER_SESSION, url:base, httpOnly:true}]);
      await button.tap();
      await until(async () => (await page.locator('#claimMessage').textContent()).includes('授权已变化'), 'identity mismatch did not explain the preserved original intent', 5_000);
      assert.ok(calls.length <= 2);
      if (calls.length === 2) assert.deepEqual(calls[1], calls[0], 'a cookie change cannot replace the original key/body');
      await page.screenshot({path:path.join(dir, 'switch-01-original-intent-after-cookie-change.png'), fullPage:true});
      assert.doesNotMatch(await page.locator('#claimMessage').textContent(), /领取成功/, 'the original A intent must not be accepted for the replacement B cookie');
      assert.equal(await button.isEnabled(),true,'a changed session may explicitly query its existing receipt without creating a claim');
      await page.reload();
      await until(async () => (await page.locator('#claimMessage').textContent()).includes('授权已变化'), 'changed-session document lost the original binding guard');
      assert.equal(await button.isEnabled(),true);
      assert.equal(calls.length,2,'changed-session reload does not mint or replay');
      await button.tap();
      await until(async()=>calls.length===3&&(await page.locator('#claimMessage').textContent()).includes('原领取结果暂未确认'),'readonly missing receipt did not preserve the unknown result',5000);
      assert.equal(calls[2].key,calls[0].key);
      assert.equal(calls[2].body,calls[0].body);
      assert.equal(calls[2].binding,calls[0].binding);
      assert.equal(calls[2].recoveryOnly,'true');
      assert.deepEqual(errors, []);
      receipt.results.push({mode:'switch', status:'PASS', originalIntentRejected:true, changedSessionReloadDoesNotPost:true, explicitReadonlyMissDoesNotClaim:true, observedRequests:calls.length, pageErrors:errors, expectedNetworkAbortDiagnostics});
    } catch (error) {
      receipt.status = 'FAIL';
      receipt.results.push({mode:'switch', status:'FAIL', requests:calls.length, failure:error.message, pageErrors:errors});
    } finally {
      await context.close();
    }
  }
  for(const mode of ['storage-unavailable','storage-write-unavailable','storage-corrupt']) {
    const context=await browser.newContext({...devices['iPhone 12'],ignoreHTTPSErrors:true,userAgent:devices['iPhone 12'].userAgent+' MicroMessenger/8.0'});
    await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
    await context.addCookies([{name:e.SOL61_BROWSER_COOKIE,value:e.SOL61_BROWSER_SESSION,url:base,httpOnly:true}]);
    await context.addInitScript(storageMode=>{
      if(storageMode==='storage-unavailable')Object.defineProperty(window,'sessionStorage',{get(){throw new Error('fixture storage unavailable');}});
      else if(storageMode==='storage-write-unavailable')Storage.prototype.setItem=function(){throw new Error('fixture quota unavailable');};
      else if(location.pathname.startsWith('/c/'))sessionStorage.setItem('aicrm.coupon.claim.tab.v1:'+location.pathname.slice(3),'{broken');
    },mode);
    const page=await context.newPage(),posts=[],errors=[];
    page.on('request',request=>{if(request.method()==='POST')posts.push(request.url());});
    page.on('pageerror',error=>errors.push(error.message));
    try {
      await page.goto(base+'/c/'+slugs.fetch);
      const button=page.locator('#claimButton');
      if(mode==='storage-write-unavailable')await button.tap();
      await until(async()=> (await page.locator('#claimMessage').textContent()).includes('无法读取或保存'),mode+': no readable checkpoint explanation',5000);
      assert.equal(await button.isEnabled(),false);
      const target=await button.boundingBox();
      await page.touchscreen.tap(target.x+target.width/2,target.y+target.height/2);
      assert.deepEqual(posts,[],'storage failure must not send a new claim');
      assert.deepEqual(errors,[]);
      await page.screenshot({path:path.join(dir,mode+'.png'),fullPage:true});
      receipt.results.push({mode,status:'PASS',newClaims:0,readableBlockedState:true,pageErrors:errors});
    }catch(error){receipt.status='FAIL';receipt.results.push({mode,status:'FAIL',failure:error.message,posts,pageErrors:errors});}
    finally{await context.close();}
  }
  await fs.writeFile(path.join(dir, 'receipt.json'), JSON.stringify(receipt, null, 2));
  console.log(JSON.stringify(receipt));
  if (receipt.status !== 'PASS') process.exitCode = 1;
} finally {
  await browser.close();
}
