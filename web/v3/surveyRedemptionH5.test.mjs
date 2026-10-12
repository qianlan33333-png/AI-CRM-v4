import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
const code=(await build({entryPoints:['web/src/h5/controller.ts'],bundle:true,write:false,format:'iife',globalName:'Survey'})).outputFiles[0].text;
for(const page of ['all','one']){
 const dom=new JSDOM('<body><main id="screen"></main></body>',{url:`https://crm.example/h5/${page}.html?slug=redemption`,runScripts:'outside-only'});
 const {window}=dom;window.Headers=Headers;const posts=[];
 window.fetch=async(url,options={})=>{
  if(options.method==='POST'){
   posts.push(JSON.parse(options.body));
   return Response.json({ok:false,code:'redemption_code_used',message:'该兑换码已使用，请更换兑换码',question_id:1},{status:409});
  }
  return Response.json({id:9,slug:'redemption',version:1,title:'兑换问卷',description:'',mode:'survey',assessment_config:{},answer_display_mode:page==='one'?'one_by_one':'all_in_one',questions:[{id:1,type:'redemption_code',title:'兑换码',required:true,sort_order:0,minimum_selections:1,maximum_selections:0,placeholder_text:'',assessment_dimension_key:'',options:[]}]},{headers:{'X-Aicrm-Survey-Authorization-Context':'a'.repeat(64)}});
 };
 window.eval(code+';globalThis.Survey=Survey;');
 const controller=new window.Survey.H5Controller(page);await controller.init();
 const question=controller.renderVals().questions[0];
 assert.equal(question.isRedemption,true);assert.equal(question.isChoice,false);
 await controller.submit();
 assert.equal(posts.length,0,'missing qualification never submits');
 assert.match(window.document.querySelector('[data-survey-field-dialog]').textContent,/请输入兑换码/);
 window.document.querySelector('[data-survey-field-dialog] button').click();
 question.input({target:{value:'00123'}});
 await controller.submit();
 assert.equal(posts.length,1);assert.equal(posts[0].answers[0].text_value,'00123');
 assert.match(window.document.querySelector('[data-survey-field-dialog]').textContent,/该兑换码已使用，请更换兑换码/);
 assert.equal(controller.renderVals().questions[0].inputValue,'00123','server rejection retains code input');
 assert.equal(controller.definitionStale,false,'used code is not a definition conflict');
 const key=posts[0].submission_key;window.document.querySelector('[data-survey-field-dialog] button').click();
 await controller.submit();assert.equal(posts[1].submission_key,key,'unchanged request retries original key');
 dom.window.close();
}
console.log('Survey redemption H5 all/one rejection and retry: PASS');
