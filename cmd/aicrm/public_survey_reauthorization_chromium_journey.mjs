import { resolveChromiumBinary } from '../../internal/webshell/chromium_binary.mjs';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
const base=process.env.AICRM_PUBLIC_SURVEY_BROWSER_URL, session=process.env.AICRM_PUBLIC_SURVEY_BROWSER_SESSION, slug=process.env.AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG;
const screenshots=process.env.AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR;
const one=process.env.AICRM_PUBLIC_SURVEY_BROWSER_DISPLAY==='one_by_one';
const nativeInput=process.env.AICRM_PUBLIC_SURVEY_BROWSER_NATIVE_INPUT==='1';
const answerPath='/h5/'+(one?'one':'all')+'.html';
const text='\n保留完整文字答案\n';
if(!base?.startsWith('https://')||!/^[A-Za-z0-9_-]{43}$/.test(session||'')||!/^[a-z0-9-]+$/.test(slug||'')||!path.isAbsolute(screenshots||''))throw new Error('invalid controlled reauthorization journey');
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
class CDP {
  constructor(socket){this.socket=socket;this.id=0;this.pending=new Map();socket.addEventListener('message',event=>{const msg=JSON.parse(String(event.data));const pending=this.pending.get(msg.id);if(!pending)return;this.pending.delete(msg.id);msg.error?pending.reject(new Error('CDP failed')):pending.resolve(msg.result||{});});}
  call(method,params={}){return new Promise((resolve,reject)=>{const id=++this.id;this.pending.set(id,{resolve,reject});this.socket.send(JSON.stringify({id,method,params}));});}
}
const evaluate=async(cdp,expression)=>{const r=await cdp.call('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw new Error(r.exceptionDetails.text);return r.result?.value;};
const wait=async(cdp,expression,label)=>{for(let i=0;i<200;i++){if(await evaluate(cdp,expression))return;await delay(50);}const state=await evaluate(cdp,`({path:location.pathname,error:document.querySelector('#screen [data-h5-error]')?.textContent,submitDisabled:document.querySelector('[data-h5-submit]')?.disabled})`);throw new Error(label+': '+JSON.stringify(state));};
const profile=await fs.mkdtemp(path.join(os.tmpdir(),'survey-reauthorization-'));
let browser;
try{
 browser=spawn(resolveChromiumBinary(),['--headless=new','--no-sandbox','--remote-debugging-port=0','--user-data-dir='+profile,'--no-first-run','--ignore-certificate-errors','--no-proxy-server','--host-resolver-rules=MAP example.com 127.0.0.1','--user-agent=Mozilla/5.0 MicroMessenger/8.0','about:blank'],{stdio:'ignore'});
 let address;
 for(let i=0;i<160;i++){try{const port=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];if(/^\d+$/.test(port)){address='http://127.0.0.1:'+port;break;}}catch{}await delay(50);}
 if(!address)throw Object.assign(new Error('DevTools unavailable'),{skip:process.platform==='darwin'});
 const target=await(await fetch(address+'/json/new?about:blank',{method:'PUT'})).json();
 const socket=new WebSocket(target.webSocketDebuggerUrl);await new Promise((resolve,reject)=>{socket.addEventListener('open',resolve,{once:true});socket.addEventListener('error',reject,{once:true});});
 const cdp=new CDP(socket);await cdp.call('Page.enable');await cdp.call('Runtime.enable');await cdp.call('Network.enable');
 await cdp.call('Network.setBlockedURLs',{urls:['https://open.weixin.qq.com/*']});
 await cdp.call('Emulation.setDeviceMetricsOverride',{width:390,height:844,deviceScaleFactor:1,mobile:true});
 await cdp.call('Network.setCookie',{name:'__Host-aicrm_survey_identity',value:session,url:base,path:'/',secure:true,httpOnly:true,sameSite:'Lax'});
 await cdp.call('Page.navigate',{url:base+'/q/'+slug});
 await wait(cdp,one?"document.querySelectorAll('#screen [data-question-id]').length===1":"document.querySelectorAll('#screen [data-question-id]').length===8",'questionnaire did not render');
 const presentation=await evaluate(cdp,`(()=>{const card=document.querySelector('#screen [data-question-id]');const hint=card?.children[1];return {title:document.title,demoNavigation:document.querySelector('#screen svg')!==null,questionTitle:card?.firstElementChild?.textContent,cardBorder:getComputedStyle(card).borderTopWidth,hintBorder:getComputedStyle(hint).borderTopWidth};})()`);
 if(presentation.title!=='公开问卷体验'||presentation.demoNavigation||!presentation.questionTitle?.includes('必答')||presentation.cardBorder!=='1px'||presentation.hintBorder!=='0px')throw new Error('mobile questionnaire title or shared card hierarchy failed: '+JSON.stringify(presentation));
 if(one){
  for(let index=0;index<7;index++){
   const previous=await evaluate(cdp,"document.querySelector('#screen [data-question-id]').dataset.questionId");
   await evaluate(cdp,`(()=>{const choice=document.querySelector('#screen label');if(choice)choice.click();else{const input=document.querySelector('#screen input');input.value='13800000000';if(!${nativeInput})input.dispatchEvent(new Event('input',{bubbles:true}));}document.querySelector('[data-h5-next]').click();return true;})()`);
   await wait(cdp,`document.querySelector('#screen [data-question-id]')?.dataset.questionId!==${JSON.stringify(previous)}`,'step navigation did not advance');
  }
 }
 await evaluate(cdp,"document.querySelector('[data-h5-submit]').scrollIntoView();document.querySelector('[data-h5-submit]').click();true");
 await wait(cdp,"(()=>{const error=document.querySelector('#screen [data-h5-error]');const rect=error?.getBoundingClientRect();return error?.textContent&&rect.top>=0&&rect.bottom<=innerHeight&&document.querySelector('[data-h5-submit]')?.disabled===false;})()",'missing answer did not reveal the existing error or unlock submission');
 await evaluate(cdp,`(()=>{const set=(node,value)=>{if(!node)return;node.value=value;if(!${nativeInput})node.dispatchEvent(new Event('input',{bubbles:true}));};if(${nativeInput}){set(document.querySelector('#screen input'),'13800000000');set(document.querySelector('#screen textarea'),${JSON.stringify(text)});}for(const card of document.querySelectorAll('#screen [data-question-id]'))card.querySelector('label')?.click();set(document.querySelector('#screen input'),'13800000000');set(document.querySelector('#screen textarea'),${JSON.stringify(text)});return true;})()`);
 if(!nativeInput && await evaluate(cdp,"Boolean(document.querySelector('#screen [data-h5-error]'))"))throw new Error('corrected input left stale validation feedback');
 await evaluate(cdp,"document.querySelector('[data-h5-submit]').scrollIntoView();true");
 const capture=async(name)=>{await evaluate(cdp,'document.fonts.ready');const image=await cdp.call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,(nativeInput?'native-':'')+(one?'one-':'all-')+name),Buffer.from(image.data,'base64'),{mode:0o600});};
 await capture('survey-reauth-filled-390.png');
 // A normal refresh must restore both mobile/Unicode text and pagination,
 // without posting or requiring an OAuth callback marker.
 if(!nativeInput){
  await cdp.call('Page.reload');
  await wait(cdp,`document.querySelector('#screen textarea')?.value===${JSON.stringify(text)} && document.querySelector('[data-h5-submit]')?.disabled===false`,'ordinary reload lost answers or progress');
 }
 await evaluate(cdp,"document.querySelector('[data-h5-submit]').scrollIntoView();document.querySelector('[data-h5-submit]').click();true");
 if(process.env.AICRM_PUBLIC_SURVEY_BROWSER_RECOVERY==='lost-response'){
  await wait(cdp,"document.querySelector('#screen [data-h5-error]')?.textContent.includes('502') && document.querySelector('[data-h5-submit]')?.disabled===false",'unconfirmed committed response locked submission');
  if(await evaluate(cdp,`document.querySelector('#screen textarea')?.value!==${JSON.stringify(text)}`))throw new Error('lost response discarded input');
  await capture('survey-lost-response-390.png');
  if(nativeInput && await evaluate(cdp,"document.querySelector('#fb-toast')?.hidden!==false"))throw new Error('native-input submission failure did not provide visible feedback');
  await evaluate(cdp,"document.querySelector('[data-h5-submit]').click();document.querySelector('[data-h5-submit]')?.click();true");
  await wait(cdp,"location.pathname==='/h5/done.html' && document.querySelector('[data-h5-done] h1')?.textContent==='收到你的问卷'",'same-key retry failed to recover committed response');
  if(await evaluate(cdp,`Boolean(sessionStorage.getItem('survey.submit-resume:'+${JSON.stringify(slug)}))`))throw new Error('recovered completion did not clear draft');
  console.log('lost_response_chromium: PASS');socket.close();
 }else{

 await wait(cdp,`location.pathname===${JSON.stringify(answerPath)} && location.search.includes('oauth_error=1') && document.querySelector('#screen textarea')?.value===${JSON.stringify(text)} && document.querySelector('[data-h5-submit]')?.disabled===false`,'denial did not preserve the editable original form');
 const restored=await evaluate(cdp,`(()=>{const draft=JSON.parse(sessionStorage.getItem('survey.submit-resume:'+${JSON.stringify(slug)}));return {phone:(!${one}&&document.querySelector('#screen input')?.value)||draft.textAnswers.find(([,value])=>value==='13800000000')?.[1],choices:draft.answers.filter(([,ids])=>ids.length>0).length,newDialogs:document.querySelectorAll('[role="dialog"]').length};})()`);
 if(restored.phone!=='13800000000'||restored.choices!==6||restored.newDialogs!==0)throw new Error('denial answer or unchanged UI contract failed');
 if(await evaluate(cdp,"Boolean(document.querySelector('#screen [data-h5-error], #screen [data-v3-survey-reauthorize]'))"))throw new Error('authorization denial must not add a warning banner or separate login action');
 await evaluate(cdp,"document.querySelector('[data-h5-submit]').scrollIntoView();true");await capture('survey-reauth-denied-390.png');
 await evaluate(cdp,"document.querySelector('[data-h5-submit]').click();true");
 await wait(cdp,"location.pathname==='/h5/done.html' && document.querySelector('[data-h5-done] h1')?.textContent==='收到你的问卷'",'reauthorization did not continue the original submission');
 if(await evaluate(cdp,`Boolean(sessionStorage.getItem('survey.submit-resume:'+${JSON.stringify(slug)}))`))throw new Error('completed answers were not cleared');
 await capture('survey-reauth-completed-390.png');
 console.log('reauthorization_chromium: PASS');socket.close();
 }
}catch(error){
 if(error.skip)console.log('reauthorization_chromium: SKIP_DEVTOOLS');else throw error;
}finally{
 if(browser&&browser.exitCode===null){browser.kill('SIGTERM');await Promise.race([new Promise(resolve=>browser.once('exit',resolve)),delay(3000)]);if(browser.exitCode===null)browser.kill('SIGKILL');}
 await fs.rm(profile,{recursive:true,force:true,maxRetries:3,retryDelay:100});
}
