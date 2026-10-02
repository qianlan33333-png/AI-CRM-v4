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
const harness=({draft,query='',status=401,context='a'.repeat(64),version=1,readStatus=200,display='all_in_one',pending=false}={})=>{
  const virtualConsole=new VirtualConsole();
  const navigations=[];
  virtualConsole.on('jsdomError',e=>{if(e.message.includes('navigation'))navigations.push(e);else throw e});
  const dom=new JSDOM('<body><div id="screen"><div data-h5-error></div></div></body>',{url:`https://test.invalid/h5/${display==='one_by_one'?'one':'all'}.html?slug=${slug}${query}`,runScripts:'outside-only',virtualConsole});
  const {window}=dom;
  let revealed=0;
  window.HTMLElement.prototype.scrollIntoView=()=>{revealed++;};
  if(pending){
    const schedule=window.setTimeout.bind(window);
    window.setTimeout=(fn,ms)=>{assert.equal(ms,15_000);return schedule(fn,1);};
  }
  Object.defineProperty(window.navigator,'userAgent',{value:'MicroMessenger/8.0'});
  window.Headers=Headers;
  const posts=[];
  window.fetch=async(url,options={})=>{
    if(options.method==='POST'){
      posts.push(JSON.parse(options.body));
      if(pending)return new Promise((_,reject)=>options.signal.addEventListener('abort',()=>reject(new window.DOMException('Aborted','AbortError')),{once:true}));
      return Response.json(status===201?{completion_action:{type:'default'}}:{error:status===401?'survey_oauth_required':'survey_unavailable'},{status});
    }
    return Response.json(readStatus===200?{...definition,version,answer_display_mode:display}:{error:'survey_oauth_required'},{status:readStatus,headers:{'X-Aicrm-Survey-Authorization-Context':context}});
  };
  if(draft)window.sessionStorage.setItem(key,draft);
  window.eval(bundle+';globalThis.ResumeTest=ResumeTest;');
  const controller=new window.ResumeTest.H5Controller(display==='one_by_one'?'one':'all');
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
  return {dom,window,controller,posts,navigations,fill,get revealed(){return revealed;}};
};

for(const display of ['all_in_one','one_by_one']){
  const original=harness({display});await original.controller.init();original.fill();
  await original.controller.submit();
  assert.equal(original.posts.length,1);
  assert.equal(original.navigations.length,1,'401 starts direct official OAuth navigation');
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
  assert.equal(denied.controller.textAnswers.get(3),'\n保留文字答案\n');
  denied.controller.setText(denied.controller.definition.questions[2],{target:{value:'修改后的答案'}});
  await denied.controller.submit();
  assert.equal(denied.navigations.length,1,'the existing submit button restarts official OAuth');
  assert.equal(JSON.parse(denied.window.sessionStorage.getItem(key)).textAnswers[1][1],'修改后的答案');
  assert.match(JSON.parse(denied.window.sessionStorage.getItem(key)).submissionKey,/^[A-Za-z0-9_-]{43}$/);

  const expiredAgain=harness({draft,display,query:'&resume=submit'});await expiredAgain.controller.init();
  assert.equal(expiredAgain.posts.length,1);
  assert.equal(expiredAgain.navigations.length,0,'second 401 stops automatic reauthorization');

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
  invalid.fill();invalid.controller.setText(invalid.controller.definition.questions[2],{target:{value:' \n\t'}});await invalid.controller.submit();assert.equal(invalid.posts.length,0,'required whitespace-only text is rejected before sending');
  const hanging=harness({display,pending:true});await hanging.controller.init();hanging.fill();await hanging.controller.submit();
  assert.equal(hanging.controller.renderVals().submitting,false,'lost response releases the submit lock');
  assert.match(hanging.controller.renderVals().error,/结果暂未确认/);
  assert.equal(hanging.navigations.length,0,'unknown result never triggers authorization or automatic replay');
  await hanging.controller.submit();
  assert.deepEqual(hanging.posts[0],hanging.posts[1],'manual timeout retry retains the same payload and key');
  for(const item of [original,resumed,denied,expiredAgain,unavailable,invalid,hanging])item.dom.window.close();
}
console.log('survey-submission-resume: PASS');
