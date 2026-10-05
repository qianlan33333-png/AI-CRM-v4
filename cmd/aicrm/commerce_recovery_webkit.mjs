import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';
const e=process.env,base=e.RECOVERY_BASE_URL,dir=e.RECOVERY_DIR;
const {webkit,devices}=createRequire(path.join(e.RECOVERY_ROOT,'package.json'))('playwright');
const browser=await webkit.launch({headless:true,executablePath:e.RECOVERY_DRIVER});
const errors=[],postCounts={checkout:0,claim:0,refund:0},results=[];
const sleep=ms=>new Promise(r=>setTimeout(r,ms));
async function until(f,message){for(let i=0;i<150;i++){if(await f())return;await sleep(100)}throw Error(message)}
try {
 assert.equal(browser.version(),e.RECOVERY_ENGINE);
 const context=await browser.newContext({...devices['iPhone 12'],ignoreHTTPSErrors:true,userAgent:devices['iPhone 12'].userAgent+' MicroMessenger/8.0'});
 await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
 const cookie=(name,value)=>({name,value,url:base});
 await context.addCookies([cookie(e.RECOVERY_PAYMENT_COOKIE,e.RECOVERY_A),cookie(e.RECOVERY_ADMIN_COOKIE,e.RECOVERY_ADMIN),cookie(e.RECOVERY_CSRF_COOKIE,e.RECOVERY_CSRF)]);
 // The only checkout mutation remains intercepted; all reads below use actual composition.
 await context.route('**/api/v1/wechat-pay/checkouts',route=>{postCounts.checkout++;return route.fulfill({status:503,contentType:'application/json',body:'{"code":"local_checkout_intercept"}'})});
 const page=await context.newPage();page.on('pageerror',x=>errors.push(x.message));
 await page.goto(base+'/pay/'+e.RECOVERY_STANDARD_CODE);
 await until(async()=>await page.locator('#buy').textContent()==='已购买','actual paid fact did not block standard purchase');
 assert.equal(await page.locator('#buy').isDisabled(),true);
 await page.reload();assert.equal(postCounts.checkout,0);results.push('standard paid/reload: disabled, no checkout POST');
 const r=await context.request.post(base+'/__fixture__/standard-refund');assert.equal(r.status(),204);
 await page.reload();await until(()=>page.locator('#buy').isEnabled(),'fully-refunded standard purchase unavailable');
 assert.equal(postCounts.checkout,0);results.push('standard full refund: actual PG purchase-status permits new explicit purchase, no automatic POST');
 await page.goto(base+'/s/'+e.RECOVERY_SERVICE_CODE);
 const button=page.locator('#servicePeriodPayButton'),card=page.locator('#servicePeriodStateCard');
 await until(async()=>await button.textContent()==='立即续费','active rights absent');assert.match(await card.textContent(),/30 天/);
 const refresh=()=>page.evaluate(()=>window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true})));
 for(const [state,text,days] of [['renewed','立即续费','120 天'],['expired','重新开通','已过期'],['refunded','重新开通','已过期']]) {
  assert.equal((await context.request.post(base+'/__fixture__/period?state='+state)).status(),204);
  await refresh();await until(async()=>await button.textContent()===text&&(await card.textContent()).includes(days),state+' rights recovery missing');
  results.push('period '+state+': real PG state re-read after dispatched persisted pageshow');
 }
 await context.request.post(base+'/__fixture__/period?state=active');await refresh();await until(async()=>await button.textContent()==='立即续费','active restoration absent');
 await context.addCookies([cookie(e.RECOVERY_PAYMENT_COOKIE,e.RECOVERY_B)]);await refresh();
 await until(async()=>await button.textContent()==='立即报名','other canonical customer inherited rights');
 assert.doesNotMatch(await card.textContent(),/剩余有效期|PRIVATE/);assert.equal(postCounts.checkout,0);
 results.push('identity A to B: original rights removed, no mutation/private remark');
 // A delayed old identity response cannot overwrite the newly read identity.
 await context.addCookies([cookie(e.RECOVERY_PAYMENT_COOKIE,e.RECOVERY_A)]);
 let captured,release;const capturedPromise=new Promise(r=>captured=r),gate=new Promise(r=>release=r);
 await page.route('**/api/h5/service-period-products/**',async route=>{const response=await context.request.fetch(route.request().url(), {method:route.request().method(),headers:await route.request().allHeaders(),data:route.request().postData()||undefined});captured();await gate;try{await route.fulfill({status:response.status(),headers:response.headers(),body:await response.body()})}catch{}});
 await refresh();await capturedPromise;
 await page.unroute('**/api/h5/service-period-products/**');
 await context.addCookies([cookie(e.RECOVERY_PAYMENT_COOKIE,e.RECOVERY_B)]);
 await page.evaluate(()=>{window.dispatchEvent(new Event('pagehide'));window.dispatchEvent(new PageTransitionEvent('pageshow',{persisted:true}))});
 await until(async()=>await button.textContent()==='立即报名','new identity response absent');release();await sleep(250);
 assert.equal(await button.textContent(),'立即报名');assert.doesNotMatch(await card.textContent(),/剩余有效期/);
 results.push('late A response fenced after pagehide/B read');await page.screenshot({path:path.join(dir,'identity-period.png'),fullPage:true});
 // Lost response after a real committed claim: manual retry must reuse the key.
 await context.addCookies([cookie(e.RECOVERY_PAYMENT_COOKIE,e.RECOVERY_A)]);
 await page.goto(base+'/c/'+e.RECOVERY_COUPON_SLUG);
 const claimRequests=[];await page.route('**/api/h5/coupons/*/claim',async route=>{
  postCounts.claim++;claimRequests.push({key:route.request().headers()['idempotency-key'],body:route.request().postData()});
  const response=await context.request.fetch(route.request().url(), {method:route.request().method(),headers:await route.request().allHeaders(),data:route.request().postData()||undefined});assert.equal(response.status(),200);
  if(postCounts.claim===1)return route.abort('failed');return route.fulfill({status:response.status(),headers:response.headers(),body:await response.body()});
 });
 await page.locator('#claimButton').tap();await until(async()=>await page.locator('#claimButton').textContent()==='重新领取','lost claim response not recoverable');
 assert.equal(postCounts.claim,1);await page.locator('#claimButton').tap();
 await until(async()=>await page.locator('#claimMessage').textContent().then(t=>t.includes('领取成功')),'claim replay did not recover');
 assert.equal(postCounts.claim,2);assert.deepEqual(claimRequests[0],claimRequests[1]);
 await page.reload();assert.equal(postCounts.claim,2);results.push('claim committed response loss/manual retry/reload: original key, one claim, no auto-loop');
 await context.addCookies([cookie(e.RECOVERY_PAYMENT_COOKIE,e.RECOVERY_B)]);await page.reload();
 assert.doesNotMatch(await page.locator('#claimMessage').textContent(),/领取成功/);assert.equal(await page.locator('#productSection').count(),0);
 results.push('coupon identity B does not inherit A claim display');
 // Actual admin refund accepted once, response lost: refresh offers readback only.
 const refundPath='/api/admin/wechat-pay/orders/'+e.RECOVERY_REFUND_MERCHANT+'/refunds';
 await page.route('**'+refundPath,async route=>{if(route.request().method()!=='POST')return route.continue();postCounts.refund++;const response=await context.request.fetch(route.request().url(), {method:route.request().method(),headers:await route.request().allHeaders(),data:route.request().postData()||undefined});assert.equal(response.status(),202, 'refund forwarding status: '+await response.text());await route.abort('failed')});
 await page.goto(base+'/admin/orderDetail.html?id='+encodeURIComponent(e.RECOVERY_REFUND_MERCHANT)+'&provider=wechat');
 try {await page.locator('[data-order-refund-amount]').waitFor({state:'visible',timeout:15000});} catch (error) {await fs.writeFile(path.join(dir,'refund-diagnostic.html'),await page.content());await page.screenshot({path:path.join(dir,'refund-diagnostic.png'),fullPage:true});throw error;}
 await page.locator('[data-order-refund-amount]').fill('1.00');await page.locator('[data-order-refund-transaction]').fill(e.RECOVERY_TXREF);
 await page.locator('[data-order-refund-checked]').check();await page.locator('button:has-text("确认提交退款申请")').tap();
 await until(()=>postCounts.refund===1,'refund request absent');
 await until(async()=>await page.locator('[data-order-refund-amount]').count()===0,'response-lost refund unlocked payload');
 await page.reload();await until(async()=>await page.locator('button:has-text("读取当前订单退款记录")').count()>0,'refund readback absent');
 await page.locator('button:has-text("读取当前订单退款记录")').tap();await sleep(300);
 assert.equal(postCounts.refund,1);assert.equal(await page.locator('[data-order-refund-amount]').count(),0);
 results.push('refund accepted response loss/reload/manual readback: one POST, no new editable intent');
 await page.screenshot({path:path.join(dir,'refund-response-loss.png'),fullPage:true});
 const injectedFailureMessages=new Set(['/'+new URL(base).host+'/api/h5/coupons/'+e.RECOVERY_COUPON_SLUG+'/claim.','/'+new URL(base).host+refundPath+'.']);
 const unexpectedErrors=errors.filter(message=>!injectedFailureMessages.has(message));
 assert.deepEqual(unexpectedErrors,[]);assert.equal(postCounts.checkout,0);
 const receipt={Status:'PASS',Engine:browser.version(),results,postCounts,pageErrors:unexpectedErrors,injectedAbortBrowserErrors:errors.filter(message=>injectedFailureMessages.has(message)),scope:'local composed production-page source + real PG reads/claim/refund acceptance; checkout intercepted, workers stopped; dispatched pageshow is not physical BFCache proof'};
 await fs.writeFile(path.join(dir,'receipt.json'),JSON.stringify(receipt,null,2));console.log(JSON.stringify(receipt));await context.close();
} finally {await browser.close()}
