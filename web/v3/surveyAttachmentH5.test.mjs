import assert from 'node:assert/strict';
import {build} from 'esbuild';
import {JSDOM} from 'jsdom';
const code=(await build({entryPoints:['web/src/h5/controller.ts'],bundle:true,write:false,format:'iife',globalName:'Survey'})).outputFiles[0].text;
for(const page of ['all','one']){
 const dom=new JSDOM('<body><main id="screen"></main></body>',{url:`https://crm.test/h5/${page}.html?slug=images`,runScripts:'outside-only'});const w=dom.window;w.Headers=Headers;w.URL.createObjectURL=()=> 'blob:local-image';w.URL.revokeObjectURL=()=>{};
 const posts=[];w.fetch=async(url,options={})=>{if(options.method==='POST'){const request=JSON.parse(options.body.get('submission'));posts.push({request,file:options.body.get('attachment:1')});return Response.json({code:'attachment_upload_failed',message:'图片上传失败，请重试',question_id:1},{status:503});}return Response.json({id:9,slug:'images',version:1,title:'图片问卷',description:'',mode:'survey',assessment_config:{},answer_display_mode:page==='one'?'one_by_one':'all_in_one',questions:[{id:1,type:'attachment',title:'图片',required:true,sort_order:0,minimum_selections:0,maximum_selections:0,placeholder_text:'',assessment_dimension_key:'',options:[]}]},{headers:{'X-Aicrm-Survey-Authorization-Context':'a'.repeat(64)}});};
 w.eval(code+';globalThis.Survey=Survey;');const c=new w.Survey.H5Controller(page);await c.init();assert.equal(c.renderVals().questions[0].isChoice,false);await c.submit();assert.equal(posts.length,0);assert.match(w.document.querySelector('[data-survey-field-dialog]').textContent,/请上传图片/);w.document.querySelector('[data-survey-field-dialog] button').click();
 const choose=(name,size=3,type='image/png')=>c.renderVals().questions[0].selectImage({target:{files:[new w.File([new Uint8Array(size)],name,{type})],value:''}});
 choose('large.png',2097152);assert.equal(c.images.size,0);w.document.querySelector('[data-survey-field-dialog] button').click();
 choose('图片.png');assert.equal(c.renderVals().questions[0].hasImage,true);await c.submit();assert.equal(posts.length,1);assert.equal(posts[0].file.name,'图片.png');assert.equal(posts[0].request.answers[0].attachment,true);assert.equal(c.images.size,1);assert.equal(c.definitionStale,false);w.document.querySelector('[data-survey-field-dialog] button').click();
 const key=posts[0].request.submission_key;await c.submit();assert.equal(posts[1].request.submission_key,key);w.document.querySelector('[data-survey-field-dialog] button').click();
 const draft=JSON.parse(w.sessionStorage.getItem('survey.submit-resume:images'));assert.equal(draft.hadImages,true);assert.equal(draft.pendingSubmit,false);assert.equal(JSON.stringify(draft).includes('blob:'),false);assert.equal(JSON.stringify(draft).includes('图片.png'),false);
 const restored=new w.Survey.H5Controller(page);await restored.init();assert.equal(restored.images.size,0);assert.equal(restored.submissionKey,'');assert.match(restored.error,/重新选择图片/);
 choose('replacement.png');await c.submit();assert.notEqual(posts[2].request.submission_key,key);w.document.querySelector('[data-survey-field-dialog] button').click();c.renderVals().questions[0].removeImage();assert.equal(c.images.size,0);assert.equal(c.submissionKey,'');dom.window.close();
}
console.log('Survey attachment H5 all/one select, boundary, retry, replace, remove and refresh: PASS');
