import assert from 'node:assert/strict';
import path from 'node:path';
import {createRequire} from 'node:module';
const e=process.env, base=e.SURVEY_BASE, one=e.SURVEY_DISPLAY==='one_by_one';
const {webkit,devices}=createRequire(path.join(e.SURVEY_ROOT,'package.json'))('playwright');
const browser=await webkit.launch({headless:true,executablePath:e.SURVEY_DRIVER});
const text='\n保留完整文字答案\n', other='其他内容🙂';
const until=async(f,message)=>{for(let i=0;i<100;i++){if(await f())return;await new Promise(r=>setTimeout(r,100));}throw Error(message)};
try {
 assert.equal(browser.version(),e.SURVEY_ENGINE);
 const context=await browser.newContext({...devices['iPhone 12'],ignoreHTTPSErrors:true,userAgent:devices['iPhone 12'].userAgent+' MicroMessenger/8.0'});
 await context.route('**/*',route=>new URL(route.request().url()).origin===base?route.continue():route.abort());
 await context.addCookies([{name:'__Host-aicrm_survey_identity',value:e.SURVEY_SESSION,url:base,secure:true,httpOnly:true,sameSite:'Lax'}]);
 const page=await context.newPage(),errors=[];let posts=0;
 page.on('pageerror',error=>errors.push(error.message));page.on('request',r=>{if(r.method()==='POST')posts++;});
 await page.goto(base+'/q/'+e.SURVEY_SLUG);
 await until(async()=>await page.locator('#screen [data-question-id]').count()===(one?1:9),'questionnaire not rendered');
 // Assign native values without input events, then use real touch navigation.
 const fillBasic=async()=>{if(one){
  for(let i=0;i<8;i++){
   const card=page.locator('#screen [data-question-id]'),id=await card.getAttribute('data-question-id');
   if(i<6)await card.locator('label').first().tap();
   else await card.locator(i===6?'input':'textarea').evaluate((n,value)=>{n.value=value},i===6?'13800000000':text);
   await page.locator('[data-h5-next]').tap();
   await until(async()=>await page.locator('#screen [data-question-id]').getAttribute('data-question-id')!==id,'next question stalled');
  }
 }else{
  await page.locator('#screen input[type=tel]').evaluate(n=>{n.value='13800000000'});
  await page.locator('#screen textarea').evaluate((n,value)=>{n.value=value},text);
  for(let i=0;i<6;i++)await page.locator('#screen [data-question-id]').nth(i).locator('label').first().tap();
 }};
 await fillBasic();
 const card=()=>page.locator('#screen [data-question-id]').last();
 await card().locator('label').nth(0).tap();await card().locator('label').nth(1).tap();
 await page.screenshot({path:path.join(e.SURVEY_DIR,'01-other-selected.png'),fullPage:true});
 assert.equal(await card().locator('[data-h5-other]').count(),1,'Other selected but no supplemental input exists');
 await page.locator('[data-h5-submit]').tap();
 await until(async()=>await page.locator('[data-h5-error]').count()>0,'missing Other text must have visible validation');assert.equal(posts,0);
 await card().locator('[data-h5-other]').evaluate((n,value)=>{n.value=value},other);
 // Choice remount must capture history/autofill values; toggling Other off
 // must erase only its obsolete supplemental answer, without losing others.
 await card().locator('label').nth(1).tap();assert.equal(await card().locator('[data-h5-other]').count(),0);
 await card().locator('label').nth(1).tap();assert.equal(await card().locator('[data-h5-other]').inputValue(),'');
 await card().locator('[data-h5-other]').evaluate((n,value)=>{n.value=value},other);
 if(one){await page.locator('[data-h5-previous]').tap();await page.locator('[data-h5-next]').tap();}
 else {await page.locator('#screen [data-question-id]').first().locator('label').first().tap();}
 await page.reload();await until(async()=>await card().locator('[data-h5-other]').count()===1,'refresh lost selected Other');
 assert.equal(await card().locator('[data-h5-other]').inputValue(),other);assert.equal(posts,0);
 await page.screenshot({path:path.join(e.SURVEY_DIR,'02-restored.png'),fullPage:true});
 await page.locator('[data-h5-submit]').tap();
 if(e.SURVEY_VERSION_CHANGE==='1') {
  await until(async()=>await page.locator('[data-h5-error]').textContent().then(s=>s?.includes('问卷内容已更新')),'real code conflict did not expose current-definition recovery');
  assert.equal(posts,1);assert.equal(await card().locator('[data-h5-other]').inputValue(),other);
  await page.screenshot({path:path.join(e.SURVEY_DIR,'03-definition-conflict.png'),fullPage:true});
  await card().locator('[data-h5-other]').fill('修改后仍须重读');await page.locator('[data-h5-submit]').tap();assert.equal(posts,1);
  await page.locator('[data-h5-error] button').tap();
  await until(async()=>await page.title()==='新版公开问卷体验' && await page.locator('#screen [data-question-id]').count()===(one?1:9),'reload did not read actual published v2');
  assert.equal(await page.locator('[data-h5-other]').count(),0,'old answers must not map to new question IDs');assert.equal(posts,1);
  await fillBasic();await card().locator('label').nth(0).tap();await card().locator('label').nth(1).tap();await card().locator('[data-h5-other]').fill(other);
  await page.locator('[data-h5-submit]').tap();
 }else{
 await until(async()=>await page.locator('[data-h5-error]').textContent().then(s=>s?.includes('502')),'committed response loss must unlock explicit retry');assert.equal(posts,1);
 assert.equal(await card().locator('[data-h5-other]').inputValue(),other);
 await page.screenshot({path:path.join(e.SURVEY_DIR,'03-lost-response.png'),fullPage:true});
 await page.locator('[data-h5-submit]').tap();
 }
 await until(async()=>new URL(page.url()).pathname==='/h5/done.html' && await page.locator('[data-h5-done] h1').textContent()==='收到你的问卷','same-key receipt recovery failed');
 await page.reload();await until(async()=>await page.locator('[data-h5-done] h1').count()===1,'completion reload failed');
 assert.equal(posts,2);assert.deepEqual(errors,[]);
 assert.equal(await page.evaluate(slug=>sessionStorage.getItem('survey.submit-resume:'+slug),e.SURVEY_SLUG),null);
 await page.screenshot({path:path.join(e.SURVEY_DIR,'04-done.png'),fullPage:true});
 console.log('survey_other_webkit: PASS');await context.close();
}finally{await browser.close();}
