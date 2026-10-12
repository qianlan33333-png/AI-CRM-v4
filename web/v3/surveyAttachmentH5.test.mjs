import assert from 'node:assert/strict';
import {build} from 'esbuild';
import {JSDOM} from 'jsdom';
const code=(await build({entryPoints:['web/src/h5/controller.ts'],bundle:true,write:false,format:'iife',globalName:'Survey'})).outputFiles[0].text;
for(const page of ['all','one']){
 const dom=new JSDOM('<body><main id="screen"></main></body>',{url:`https://crm.test/h5/${page}.html?slug=images`,runScripts:'outside-only'});const w=dom.window;w.Headers=Headers;w.URL.createObjectURL=()=>{throw new Error('full-size blob preview must not be created');};
 const crops=[];let closed=0;
 const decoded=()=>({width:200,height:100,close(){closed++;}});
 w.createImageBitmap=async()=>decoded();
 w.HTMLCanvasElement.prototype.getContext=()=>({fillRect(){},drawImage(...args){crops.push(args.slice(1));}});
 w.HTMLCanvasElement.prototype.toDataURL=function(type,quality){assert.equal(this.width,96);assert.equal(this.height,96);assert.equal(type,'image/jpeg');assert.equal(quality,0.5);return 'data:image/jpeg;base64,local-thumbnail';};
 const posts=[];w.fetch=async(url,options={})=>{if(options.method==='POST'){const request=JSON.parse(options.body.get('submission'));posts.push({request,file:options.body.get('attachment:1')});return Response.json({code:'attachment_upload_failed',message:'图片上传失败，请重试',question_id:1},{status:503});}return Response.json({id:9,slug:'images',version:1,title:'图片问卷',description:'',mode:'survey',assessment_config:{},answer_display_mode:page==='one'?'one_by_one':'all_in_one',questions:[{id:1,type:'attachment',title:'图片',required:true,sort_order:0,minimum_selections:0,maximum_selections:0,placeholder_text:'',assessment_dimension_key:'',options:[]}]},{headers:{'X-Aicrm-Survey-Authorization-Context':'a'.repeat(64)}});};
 w.eval(code+';globalThis.Survey=Survey;');const c=new w.Survey.H5Controller(page);await c.init();assert.equal(c.renderVals().questions[0].isChoice,false);await c.submit();assert.equal(posts.length,0);assert.match(w.document.querySelector('[data-survey-field-dialog]').textContent,/请上传图片/);w.document.querySelector('[data-survey-field-dialog] button').click();
 const choose=(name,size=3,type='image/png')=>c.renderVals().questions[0].selectImage({target:{files:[new w.File([new Uint8Array(size)],name,{type})],value:''}});
 await choose('large.png',2097152);assert.equal(c.images.size,0);w.document.querySelector('[data-survey-field-dialog] button').click();
 await choose('图片.png');assert.equal(c.renderVals().questions[0].hasImage,true);assert.match(c.renderVals().questions[0].imageURL,/^data:image\/jpeg;/);assert.equal(crops[0].join(','),'50,0,100,100,0,0,96,96');assert.equal(closed,1);await c.submit();assert.equal(posts.length,1);assert.equal(posts[0].file.name,'图片.png');assert.equal(posts[0].request.answers[0].attachment,true);assert.equal(c.images.size,1);assert.equal(c.definitionStale,false);w.document.querySelector('[data-survey-field-dialog] button').click();
 const key=posts[0].request.submission_key;await c.submit();assert.equal(posts[1].request.submission_key,key);w.document.querySelector('[data-survey-field-dialog] button').click();
 const draft=JSON.parse(w.sessionStorage.getItem('survey.submit-resume:images'));assert.equal(draft.hadImages,true);assert.equal(draft.pendingSubmit,false);assert.equal(JSON.stringify(draft).includes('data:image'),false);assert.equal(JSON.stringify(draft).includes('blob:'),false);assert.equal(JSON.stringify(draft).includes('图片.png'),false);
 const restored=new w.Survey.H5Controller(page);await restored.init();assert.equal(restored.images.size,0);assert.equal(restored.submissionKey,'');assert.match(restored.error,/重新选择图片/);
 await choose('replacement.png');await c.submit();assert.notEqual(posts[2].request.submission_key,key);w.document.querySelector('[data-survey-field-dialog] button').click();c.renderVals().questions[0].removeImage();assert.equal(c.images.size,0);assert.equal(c.submissionKey,'');
 const pending=new Map();w.createImageBitmap=(file)=>new Promise((resolve,reject)=>pending.set(file.name,{resolve,reject}));
 const first=choose('slow.png');const latest=choose('latest.png');const postCount=posts.length;
 await c.submit();assert.equal(posts.length,postCount,'preview generation must not submit');w.document.querySelector('[data-survey-field-dialog] button').click();
 pending.get('latest.png').resolve(decoded());await latest;pending.get('slow.png').resolve(decoded());await first;
 assert.equal(c.images.get(1).file.name,'latest.png','late decode must not overwrite newer choice');
 const broken=choose('broken.png');pending.get('broken.png').reject(new Error('decode failed'));await broken;
 assert.equal(c.images.get(1).file.name,'latest.png','failed replacement retains selected file');w.document.querySelector('[data-survey-field-dialog] button').click();
 const removed=choose('removed.png');c.renderVals().questions[0].removeImage();pending.get('removed.png').resolve(decoded());await removed;
 assert.equal(c.images.size,0,'late decode must not resurrect deleted image');assert.equal(c.previewRequests.size,0);
 const outdated=choose('outdated.png');await c.init();pending.get('outdated.png').resolve(decoded());await outdated;
 assert.equal(c.images.size,0,'late decode from an old definition must be discarded');
 // Older WeChat webviews can decode a local FileReader data URL without blob CSP or network exceptions.
 w.createImageBitmap=undefined;
 w.Image=class {naturalWidth=200;naturalHeight=100;set src(value){if(value){assert.match(value,/^data:image\/png/);queueMicrotask(()=>this.onload());}}};
 await choose('fallback.png');assert.equal(c.images.get(1).file.name,'fallback.png');assert.match(c.images.get(1).url,/^data:image\/jpeg;/);
 assert.equal(posts.length,postCount,'selection, deletion and local fallback do not upload');
 dom.window.close();
}
console.log('Survey attachment H5 all/one local crop, fallback, boundaries, retry, replace/delete races, stale definition and refresh: PASS');
