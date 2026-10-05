import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM, VirtualConsole } from 'jsdom';

const bundle = (await build({entryPoints:['web/src/h5/controller.ts'],bundle:true,format:'iife',globalName:'ResumeTest',platform:'browser',write:false})).outputFiles[0].text;
const slug='resume-survey';
const key='survey.submit-resume:'+slug;
const definition={id:1,slug,version:1,title:'恢复测试',description:'',mode:'survey',assessment_config:{},answer_display_mode:'all_in_one',questions:[
  {id:1,type:'single_choice',title:'选择',required:true,minimum_selections:1,maximum_selections:1,options:[{id:11,option_text:'选项'}]},
  {id:2,type:'mobile',title:'手机号',required:true,options:[]},
  {id:3,type:'textarea',title:'说明',required:true,options:[]}
]};
const harness=({draft,query='',status=401,context='a'.repeat(64),version=1,readStatus=200,display='all_in_one',pending=false,readPending=false,ignoreAbort=false,bodyPending=false,authBodyPending=false,postBody,storageBroken=false,noAbort=false,freezeDeadline=false,pageDisplay=display}={})=>{
  const virtualConsole=new VirtualConsole();
  const navigations=[];
  virtualConsole.on('jsdomError',e=>{if(e.message.includes('navigation'))navigations.push(e);else throw e});
  const dom=new JSDOM('<body><div id="screen"><div data-h5-error></div></div></body>',{url:`https://test.invalid/h5/${pageDisplay==='one_by_one'?'one':'all'}.html?slug=${slug}${query}`,runScripts:'outside-only',virtualConsole});
  const {window}=dom;
  let revealed=0;
  window.HTMLElement.prototype.scrollIntoView=()=>{revealed++;};
  if(pending || readPending || bodyPending || authBodyPending){
    const schedule=window.setTimeout.bind(window);
    window.setTimeout=(fn,ms)=>schedule(fn,ms===15_000?1:ms);
  }
  if(noAbort)window.AbortController=undefined;
  if(freezeDeadline){const schedule=window.setTimeout.bind(window);window.setTimeout=(fn,ms)=>ms===15_000?0:schedule(fn,ms);}
  Object.defineProperty(window.navigator,'userAgent',{value:'MicroMessenger/8.0'});
  window.Headers=Headers;
  const posts=[];const latePosts=[];
  window.fetch=async(url,options={})=>{
    if(options.method==='POST'){
      posts.push(JSON.parse(options.body));
      if(pending)return new Promise((resolve,reject)=>{latePosts.push(resolve);if(!ignoreAbort)options.signal.addEventListener('abort',()=>reject(new window.DOMException('Aborted','AbortError')),{once:true});});
      if(bodyPending)return {status:201,headers:new Headers(),text:()=>new Promise(()=>{})};
      return Response.json(postBody || (status===201?{receipt:{questionnaire_id:1,questionnaire_slug:slug,definition_version:version,submission_id:1},result_token:'r'.repeat(43),completion_action:{type:'default'}}:{error:status===401?'survey_oauth_required':'survey_unavailable'}),{status});
    }
    if(authBodyPending)return {ok:true,status:200,json:()=>new Promise(()=>{})};
    if(readPending)return new Promise((_,reject)=>{if(!ignoreAbort)options.signal.addEventListener('abort',()=>reject(new window.DOMException('Aborted','AbortError')),{once:true});});
    return Response.json(readStatus===200?{...definition,version,answer_display_mode:display}:{error:'survey_oauth_required'},{status:readStatus,headers:{'X-Aicrm-Survey-Authorization-Context':context}});
  };
  if(draft)window.sessionStorage.setItem(key,draft);
  if(storageBroken)window.Storage.prototype.setItem=()=>{throw new window.DOMException('Quota','QuotaExceededError')};
  window.eval(bundle+';globalThis.ResumeTest=ResumeTest;');
  const controller=new window.ResumeTest.H5Controller(pageDisplay==='one_by_one'?'one':'all');
  const fill=()=>{
    const vals=controller.renderVals();
    // Advance one-by-one only through the real Controller's navigation.
    if(display==='one_by_one'){
      vals.questions[0].options[0].pick();
      controller.renderVals().act.nextQ();
      controller.renderVals().questions[0].input({target:{value:'13800000000'}});
      controller.renderVals().act.nextQ();
      controller.renderVals().questions[0].input({target:{value:'\n保留文字答案\n'}});
    }else{
      vals.questions[0].options[0].pick();
      controller.renderVals().questions[1].input({target:{value:'13800000000'}});
      controller.renderVals().questions[2].input({target:{value:'\n保留文字答案\n'}});
    }
  };
  return {dom,window,controller,posts,navigations,fill,releasePost:(index)=>latePosts[index](Response.json({receipt:{questionnaire_id:1,questionnaire_slug:slug,definition_version:version,submission_id:1},result_token:'r'.repeat(43),completion_action:{type:'default'}},{status:201})),get revealed(){return revealed;}};
};

for(const display of ['all_in_one','one_by_one']){
  const restoredInput=harness({display,status:201});await restoredInput.controller.init();
  const input=(id,value)=>{
    const element=restoredInput.window.document.createElement(id===2?'input':'textarea');
    element.dataset.textQuestionId=String(id);element.value=value;
    restoredInput.window.document.getElementById('screen').append(element);return element;
  };
  if(display==='one_by_one'){
    restoredInput.controller.renderVals().questions[0].options[0].pick();restoredInput.controller.move(1);
    const mobile=input(2,'13800000000');restoredInput.controller.move(1);mobile.remove();
    assert.equal(restoredInput.controller.textAnswers.get(2),'13800000000','next captures restored native mobile without an input event');
  }else{
    input(2,'13800000000');
    // Selecting another question rerenders the all-in-one page; first capture native input.
    restoredInput.controller.renderVals().questions[0].options[0].pick();
    assert.equal(restoredInput.controller.textAnswers.get(2),'13800000000','choice rerender preserves native restored mobile without input event');
  }
  input(3,'\n输入法已完成的答案\n');
  await restoredInput.controller.submit();
  assert.equal(restoredInput.posts.length,1,'submit reads actual visible values without depending on input/change delivery');
  assert.equal(restoredInput.posts[0].answers.find(a=>a.question_id===3).text_value,'\n输入法已完成的答案\n');
  restoredInput.dom.window.close();

  for(const config of [{pending:true,ignoreAbort:true},{bodyPending:true},{pending:true,ignoreAbort:true,noAbort:true}]){
    const transport=harness({display,...config});await transport.controller.init();transport.fill();
    await Promise.race([transport.controller.submit(),new Promise((_,reject)=>setTimeout(()=>reject(new Error('non-cooperative transport remained locked')),250))]);
    assert.equal(transport.controller.renderVals().submitting,false);
    assert.match(transport.controller.renderVals().error,/结果暂未确认/);
    await transport.controller.submit();assert.equal(transport.posts.length,2);
    assert.deepEqual(transport.posts[0],transport.posts[1],'original key/payload survive non-cooperative fetch or body timeout');
    if(config.pending){transport.releasePost(0);await new Promise(r=>setTimeout(r,5));assert.equal(transport.controller.submitted,false,'late success cannot clear draft or redirect after a timed-out attempt');assert.ok(transport.window.sessionStorage.getItem(key));}
    transport.dom.window.close();
  }
  const read=harness({display,readPending:true,ignoreAbort:true});
  await Promise.race([read.controller.init(),new Promise((_,reject)=>setTimeout(()=>reject(new Error('non-cooperative read remained locked')),250))]);
  assert.equal(read.controller.renderVals().canRetry,true);
  assert.match(read.controller.renderVals().error,/读取超时/);read.dom.window.close();
}
for(const eventName of ['pageshow','visibilitychange']){
  const background=harness({pending:true,ignoreAbort:true,freezeDeadline:true});await background.controller.init();background.fill();
  const pending=background.controller.submit();await Promise.resolve();
  const now=background.window.Date.now();background.window.Date.now=()=>now+16_000;
  (eventName==='pageshow'?background.window:background.window.document).dispatchEvent(new background.window.Event(eventName));
  await pending;assert.equal(background.controller.renderVals().submitting,false,'foreground or BFCache restore expires overdue waits even with a frozen timer');
  assert.match(background.controller.renderVals().error,/结果暂未确认/);assert.equal(background.posts.length,1);
  background.releasePost(0);await new Promise(r=>setTimeout(r,5));assert.equal(background.controller.submitted,false);background.dom.window.close();
}
const auth=harness({authBodyPending:true});const authController=new auth.window.ResumeTest.H5Controller('auth');
await Promise.race([authController.init(),new Promise((_,reject)=>setTimeout(()=>reject(new Error('authorization response body remained locked')),250))]);
assert.match(authController.renderVals().error,/读取失败/);assert.equal(auth.navigations.length,0,'expired authorization read cannot start a new OAuth');auth.dom.window.close();
const legacy=harness({status:201,noAbort:true});await legacy.controller.init();legacy.fill();await legacy.controller.submit();assert.equal(legacy.controller.submitted,true,'missing AbortController does not leave a lock before the guarded request');legacy.dom.window.close();
for(const scroll of [undefined,()=>{throw new Error('embedded scroll failure')}]){
  const feedback=harness();await feedback.controller.init();feedback.window.HTMLElement.prototype.scrollIntoView=scroll;
  await feedback.controller.submit();assert.equal(feedback.controller.renderVals().canSubmit,true);assert.equal(feedback.window.document.querySelector('#fb-toast').hidden,false);assert.match(feedback.window.document.querySelector('#fb-toast').textContent,/请选择/);feedback.dom.window.close();
}
console.log('survey-native-input-and-hard-deadline: PASS');

for(const display of ['all_in_one','one_by_one']){
  const original=harness({display});await original.controller.init();original.fill();
  assert.equal(original.window.document.title, definition.title, 'browser title is the actual questionnaire title');
  await original.controller.submit();
  assert.equal(original.posts.length,1);
  assert.equal(original.navigations.length,1,'401 starts direct official OAuth navigation');
  assert.equal(original.controller.renderVals().error, '', '401 goes directly to official authorization without a banner');
  const draft=original.window.sessionStorage.getItem(key);
  assert.ok(draft,'answers are saved before leaving the page');
  assert.deepEqual(JSON.parse(draft).textAnswers,[[2,'13800000000'],[3,'\n保留文字答案\n']]);

  const resumed=harness({draft,display,query:'&resume=submit',status:201});
  await resumed.controller.init();
  assert.equal(resumed.posts.length,1,'same respondent resumes a pending user submission once');
  assert.deepEqual(resumed.posts[0],original.posts[0],'same payload and idempotency key survive OAuth');
  assert.equal(resumed.window.sessionStorage.getItem(key),null,'completion clears temporary answers');

  const denied=harness({draft,display,query:'&resume=submit&oauth_error=1',readStatus:401});
  await denied.controller.init();
  assert.equal(denied.posts.length,0);
  assert.equal(denied.navigations.length,0,'denial never loops automatically');
  assert.equal(denied.controller.renderVals().error, '', 'canceling official authorization restores the form without an authorization warning');
  assert.equal(denied.window.document.title, definition.title);
  assert.equal(denied.controller.textAnswers.get(3),'\n保留文字答案\n');
  denied.controller.setText(denied.controller.definition.questions[2],{target:{value:'修改后的答案'}});
  await denied.controller.submit();
  assert.equal(denied.navigations.length,1,'the existing submit button restarts official OAuth');
  assert.equal(JSON.parse(denied.window.sessionStorage.getItem(key)).textAnswers[1][1],'修改后的答案');
  assert.match(JSON.parse(denied.window.sessionStorage.getItem(key)).submissionKey,/^[A-Za-z0-9_-]{43}$/);

  const expiredAgain=harness({draft,display,query:'&resume=submit'});await expiredAgain.controller.init();
  assert.equal(expiredAgain.posts.length,1);
  assert.equal(expiredAgain.navigations.length,0,'second 401 stops automatic reauthorization');
  assert.equal(expiredAgain.controller.renderVals().error, '', 'second 401 leaves the original submit action without a warning banner');
  assert.equal(expiredAgain.window.document.querySelector('#fb-toast').hidden,false,'second 401 gives existing lightweight feedback instead of silently stopping');
  const missingCallbackSession=harness({draft,display,query:'&resume=submit',readStatus:401});await missingCallbackSession.controller.init();assert.equal(missingCallbackSession.posts.length,0);assert.equal(missingCallbackSession.navigations.length,0);assert.equal(missingCallbackSession.controller.renderVals().error,'');assert.equal(missingCallbackSession.window.document.querySelector('#fb-toast').hidden,false);missingCallbackSession.dom.window.close();
  const deniedWithSession=harness({draft,display,query:'&resume=submit&oauth_error=1',status:201});
  await deniedWithSession.controller.init();
  assert.equal(deniedWithSession.posts.length,0,'denied callback never auto-submits even if another session still exists');
  assert.equal(deniedWithSession.controller.renderVals().error,'');
  deniedWithSession.controller.setText(deniedWithSession.controller.definition.questions[2],{target:{value:''}});
  await deniedWithSession.controller.submit();
  assert.equal(deniedWithSession.navigations.length,0,'edited missing answer is validated before restarting authorization');
  deniedWithSession.controller.setText(deniedWithSession.controller.definition.questions[2],{target:{value:'恢复的完整答案'}});
  await deniedWithSession.controller.submit();
  assert.equal(deniedWithSession.posts.length,1,'a valid existing session submits normally after cancellation without forcing another OAuth');
  assert.equal(deniedWithSession.controller.submitted,true);

  for(const config of [{context:'b'.repeat(64)},{version:2}]){
    const changed=harness({draft,display,query:'&resume=submit',status:201,...config});await changed.controller.init();
    assert.equal(changed.posts.length,0,'changed identity/definition must not receive the old submission');
    assert.equal(changed.controller.textAnswers.size,0);
  }
  const unavailable=harness({status:503,display});await unavailable.controller.init();unavailable.fill();await unavailable.controller.submit();await unavailable.controller.submit();
  assert.equal(unavailable.navigations.length,0,'503 keeps the existing transport retry');
  assert.equal(unavailable.posts[0].submission_key,unavailable.posts[1].submission_key);
  assert.ok(unavailable.revealed,'transport errors are brought into view');
  const invalid=harness({display});await invalid.controller.init();await invalid.controller.submit();
  assert.equal(invalid.posts.length,0,'validation still precedes submission');
  assert.ok(invalid.revealed,'missing required answers reveal the existing error');
  const inputNode=invalid.window.document.createElement('textarea');invalid.window.document.getElementById('screen').append(inputNode);
  invalid.fill();invalid.controller.setText(invalid.controller.definition.questions[2],{target:{value:''}});await invalid.controller.submit();invalid.controller.setText(invalid.controller.definition.questions[2],{target:{value:'修正后文字'}});assert.equal(invalid.window.document.querySelector('[data-h5-error]'),null,'editing removes stale feedback without remounting active input');assert.equal(inputNode.isConnected,true);invalid.controller.setText(invalid.controller.definition.questions[2],{target:{value:' \n\t'}});await invalid.controller.submit();assert.equal(invalid.posts.length,0,'required whitespace-only text is rejected before sending');
  const hanging=harness({display,pending:true});await hanging.controller.init();hanging.fill();await hanging.controller.submit();
  assert.equal(hanging.controller.renderVals().submitting,false,'lost response releases the submit lock');
  assert.match(hanging.controller.renderVals().error,/结果暂未确认/);
  assert.equal(hanging.navigations.length,0,'unknown result never triggers authorization or automatic replay');
  await hanging.controller.submit();
  assert.deepEqual(hanging.posts[0],hanging.posts[1],'manual timeout retry retains the same payload and key');

  const reading=harness({display,readPending:true});await reading.controller.init();
  assert.equal(reading.controller.renderVals().loading,false);
  assert.equal(reading.controller.renderVals().canRetry,true,'hanging definition read exposes existing retry');
  assert.match(reading.controller.renderVals().error,/读取超时/);

  const editing=harness({display,status:503});await editing.controller.init();editing.fill();
  const editDraft=editing.window.sessionStorage.getItem(key);
  assert.ok(editDraft,'ordinary filling saves before an OAuth request');
  const refresh=harness({display,draft:editDraft,status:201});await refresh.controller.init();
  assert.equal(refresh.posts.length,0,'refresh restores progress without auto-submitting');
  assert.equal(refresh.controller.textAnswers.get(3),'\n保留文字答案\n');
  if(display==='one_by_one')assert.equal(refresh.controller.questionIndex,2);
  await editing.controller.submit();
  const reload=harness({display,draft:editing.window.sessionStorage.getItem(key),status:503});await reload.controller.init();await reload.controller.submit();
  assert.deepEqual(reload.posts[0],editing.posts[0],'reload after failure retains exact payload/key');

  const doubleClick=harness({display,pending:true});await doubleClick.controller.init();doubleClick.fill();
  await Promise.all([doubleClick.controller.submit(),doubleClick.controller.submit(),doubleClick.controller.submit()]);
  assert.equal(doubleClick.posts.length,1,'rapid clicks produce one in-flight request');

  for(const brokenReceipt of [{}, {receipt:{submission_id:1,questionnaire_id:1,questionnaire_slug:'other-survey',definition_version:1},result_token:'r'.repeat(43)}]){
    const invalidReceipt=harness({display,status:201,postBody:brokenReceipt});await invalidReceipt.controller.init();invalidReceipt.fill();await invalidReceipt.controller.submit();
    assert.equal(invalidReceipt.controller.submitted,false,'malformed/wrong-questionnaire 2xx does not confirm completion');
    assert.ok(invalidReceipt.window.sessionStorage.getItem(key),'unconfirmed receipt keeps draft');
    assert.match(invalidReceipt.controller.renderVals().error,/响应不完整/);
    invalidReceipt.dom.window.close();
  }
  const storageDenied=harness({display,storageBroken:true});await storageDenied.controller.init();storageDenied.fill();await storageDenied.controller.submit();
  assert.equal(storageDenied.navigations.length,0,'failed draft write never navigates to OAuth and loses answers');
  assert.equal(storageDenied.controller.renderVals().canSubmit,true);
  assert.match(storageDenied.controller.renderVals().error,/未能保存答案/);
  const noStorageSuccess=harness({display,storageBroken:true,status:201});await noStorageSuccess.controller.init();noStorageSuccess.fill();await noStorageSuccess.controller.submit();
  assert.equal(noStorageSuccess.controller.submitted,true,'storage quota does not block ordinary authenticated submit');

  const corrupted=harness({display,draft:'{invalid'});await corrupted.controller.init();assert.equal(corrupted.controller.textAnswers.size,0);
  for(const damage of [d=>d.answers[0][1].push(9999),d=>d.textAnswers.push([9999,'stale field']),d=>d.answers.push(d.answers[0])]){
    const damagedDraft=JSON.parse(editDraft);damage(damagedDraft);
    const damaged=harness({display,draft:JSON.stringify(damagedDraft)});await damaged.controller.init();
    assert.equal(damaged.controller.textAnswers.size,0,'damaged foreign/duplicate IDs are discarded before exposing an unsubmitable draft');
    assert.equal(damaged.controller.renderVals().canNext || damaged.controller.renderVals().canSubmit,true);
    damaged.dom.window.close();
  }
  for(const invalidPhone of ['+86 13800000000','1380000000','１３８００００００００','1380000000a']){
    const mobile=harness({display});await mobile.controller.init();mobile.fill();
    mobile.controller.setText(mobile.controller.definition.questions[1],{target:{value:invalidPhone}});
    await mobile.controller.submit();assert.equal(mobile.posts.length,0,'invalid mobile text is explained before network request');
    assert.match(mobile.controller.renderVals().error,/11位中国大陆手机号/);
    mobile.dom.window.close();
  }
  const notPending=JSON.parse(editDraft);notPending.pendingSubmit=false;
  const forgedResume=harness({display,draft:JSON.stringify(notPending),query:'&resume=submit',status:201});await forgedResume.controller.init();
  assert.equal(forgedResume.posts.length,0,'editing drafts cannot auto-submit just because the URL contains resume');
  forgedResume.dom.window.close();
  const expiredDraft=JSON.parse(editDraft);expiredDraft.savedAt=Date.now()-25*60*60*1000;
  const expired=harness({display,draft:JSON.stringify(expiredDraft)});await expired.controller.init();assert.equal(expired.controller.textAnswers.size,0,'expired temporary draft is not restored');
  const stale=harness({display,status:409,postBody:{ok:false,code:'definition_version_conflict',message:'definition_version_conflict'}});await stale.controller.init();stale.fill();await stale.controller.submit();
  assert.equal(stale.controller.renderVals().canRetry,true,'version changes expose current retry instead of trapping stale submission');
  assert.equal(stale.controller.textAnswers.get(3),'\n保留文字答案\n','old answers stay visible until respondent elects to reload');
  stale.controller.setText(stale.controller.definition.questions[2],{target:{value:'修改文字也必须先重读新题'}});
  assert.equal(stale.controller.renderVals().canRetry,true,'editing does not dismiss definition-change recovery');
  await stale.controller.submit();assert.equal(stale.posts.length,1,'known stale definition does not keep reposting rejected answers');
  stale.window.fetch=async()=>Response.json({...definition,version:2,answer_display_mode:display},{headers:{'X-Aicrm-Survey-Authorization-Context':'a'.repeat(64)}});
  await stale.controller.init();assert.equal(stale.controller.textAnswers.size,0,'definition change never maps old question IDs silently');
  assert.match(stale.controller.renderVals().error,/内容已更新/);

  const wrongMode=harness({display,pageDisplay:display==='one_by_one'?'all_in_one':'one_by_one'});await wrongMode.controller.init();
  assert.equal(wrongMode.navigations.length,1,'old layout entry follows current Owner definition');
  assert.equal(wrongMode.posts.length,0);
  const unicode=harness({display,status:201});await unicode.controller.init();unicode.fill();
  const textarea=unicode.controller.definition.questions[2];textarea.minimum_length=2;textarea.maximum_length=2;
  unicode.controller.setText(textarea,{target:{value:'𠮷🙂'}});await unicode.controller.submit();
  assert.equal(unicode.posts[0].answers.find(a=>a.question_id===3).text_value,'𠮷🙂','Unicode scalar count matches Go rune validation');
  for(const item of [reading,editing,refresh,reload,doubleClick,storageDenied,noStorageSuccess,corrupted,expired,stale,wrongMode,unicode])item.dom.window.close();

  for(const item of [original,resumed,denied,deniedWithSession,expiredAgain,unavailable,invalid,hanging])item.dom.window.close();
}
console.log('survey-submission-resume: PASS');
