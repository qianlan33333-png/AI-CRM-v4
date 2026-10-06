import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import path from 'node:path';
import {createRequire} from 'node:module';

const e=process.env,base=e.SOL61_BROWSER_BASE,slug=e.SOL61_BROWSER_SLUG,dir=e.SOL61_BROWSER_DIR;
const {webkit,chromium,devices}=createRequire(path.join(e.SOL61_BROWSER_ROOT,'package.json'))('playwright');
const browser=await(e.SOL61_BROWSER_KIND==='chromium'?chromium:webkit).launch({headless:true,executablePath:e.SOL61_BROWSER_DRIVER});
const context=await browser.newContext({...devices['iPhone 12'],ignoreHTTPSErrors:true,userAgent:devices['iPhone 12'].userAgent+' MicroMessenger/8.0'});
const calls=[],errors=[],expectedNetworkAbortDiagnostics=[];
const receipt={engine:browser.version(),status:'PASS',scope:'actual loopback HTTP/PG and native browser; trusted test session reissued for same verified subject, no external OAuth or provider call'};
const sleep=ms=>new Promise(resolve=>setTimeout(resolve,ms));
const until=async(read,message)=>{const end=Date.now()+5000;while(Date.now()<end){if(await read())return;await sleep(50);}throw new Error(message);};
try {
  if(e.SOL61_BROWSER_KIND!=='chromium')assert.equal(browser.version(),e.SOL61_BROWSER_ENGINE);
  await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
  await context.addCookies([{name:e.SOL61_BROWSER_COOKIE,value:e.SOL61_BROWSER_SESSION,url:base,httpOnly:true}]);
  await context.addInitScript(()=>window.addEventListener('pageshow',event=>{window.__sol61NativeShow={persisted:event.persisted,navigationType:performance.getEntriesByType('navigation')[0]?.type||''};}));
  const page=await context.newPage();
  const target=new URL(base+'/api/h5/coupons/'+slug+'/claim');
  const expectedAbortMessages=[target.href+'.','/'+target.host+target.pathname+'.'];
  page.on('pageerror',error=>{if(calls.length===1&&expectedAbortMessages.includes(error.message))expectedNetworkAbortDiagnostics.push(error.message);else errors.push(error.message);});
  page.on('request',request=>{if(new URL(request.url()).pathname===target.pathname&&request.method()==='POST')calls.push({key:request.headers()['idempotency-key'],body:request.postData(),binding:request.headers()['x-aicrm-coupon-session-binding'],recoveryOnly:request.headers()['x-aicrm-coupon-recovery-only']});});
  await page.goto(base+'/c/'+slug);
  const button=page.locator('#claimButton');
  await button.tap();
  await until(async()=>calls.length===1&&await button.isEnabled(),'committed lost response did not retain manual confirmation');
  await page.screenshot({path:path.join(dir,'renew-01-original-unknown.png'),fullPage:true});
  const renewed=await context.request.post(base+'/__sol61__/renew-same-subject');
  assert.equal(renewed.status(),204);
  const cookie=(await context.cookies(base)).find(item=>item.name===e.SOL61_BROWSER_COOKIE);
  assert.ok(cookie&&cookie.httpOnly&&cookie.value!==e.SOL61_BROWSER_SESSION,'actual HttpOnly token must be renewed');
  await page.reload();
  await page.goto(base+'/__sol61__/away');
  await page.goBack();
  await until(async()=>!!await page.locator('#claimMessage').textContent(),'native return has no result explanation');
  receipt.nativeReturn=await page.evaluate(()=>window.__sol61NativeShow);
  await page.screenshot({path:path.join(dir,'renew-02-same-subject-new-token-native-back.png'),fullPage:true});
  assert.equal(calls.length,1,'renew/reload/native return cannot auto-post');
  assert.equal(await button.isEnabled(),true,'same verified customer with a new token must retain explicit original-receipt confirmation');
  await button.tap();
  await until(async()=>calls.length===2&&(await page.locator('#claimMessage').textContent()).includes('领取成功')&&await button.isEnabled(),'new trusted token could not recover its original committed receipt');
  assert.equal(calls[1].key,calls[0].key);
  assert.equal(calls[1].body,calls[0].body);
  assert.equal(calls[1].binding,calls[0].binding,'original checkpoint marker must not be rewritten');
  assert.equal(calls[1].recoveryOnly,'true','changed-token recovery must be explicitly read-only');
  await page.screenshot({path:path.join(dir,'renew-03-recovered-original.png'),fullPage:true});
  assert.deepEqual(errors,[]);
  receipt.sameVerifiedSubjectNewTokenRecovered=true;
  receipt.sameKey=true;receipt.sameBody=true;receipt.originalBindingRetained=true;receipt.explicitRecoveryOnly=true;
}catch(error){receipt.status='FAIL';receipt.failure=error.message;}
finally {
  receipt.requests=calls.length;receipt.pageErrors=errors;receipt.expectedNetworkAbortDiagnostics=expectedNetworkAbortDiagnostics;
  await fs.writeFile(path.join(dir,'receipt.json'),JSON.stringify(receipt,null,2));
  console.log(JSON.stringify(receipt));
  await context.close();await browser.close();
  if(receipt.status!=='PASS')process.exitCode=1;
}
