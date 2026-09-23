import assert from 'node:assert/strict';
import fs from 'node:fs';
import {JSDOM} from 'jsdom';
const origin=process.env.AICRM_MODEL_TEST_URL;
const code=fs.readFileSync(new URL('./static/admin_console/config_center_host.js',import.meta.url),'utf8');
const dom=new JSDOM('<body data-runtime-config-page="runtimeConfigCategory"><main data-runtime-release-host></main>',{url:origin+'/admin/configDetail.html?cat=ai_models',runScripts:'outside-only'});
dom.window.Headers=Headers;dom.window.fetch=(input,init)=>fetch(new URL(String(input),origin),init);dom.window.document.cookie='aicrm_admin_csrf=test';
const waitFor=async fn=>{for(let i=0;i<100;i++){if(fn())return;await new Promise(r=>setTimeout(r,15));}throw Error('model form did not reach expected state')};
try{
 dom.window.eval(code);await waitFor(()=>dom.window.document.querySelector('button[type=submit]')?.disabled===false);
 const d=dom.window.document,form=d.querySelector('form'),provider=form.querySelector('select'),model=form.querySelector('input:not([type=password])'),key=form.querySelector('[type=password]');
 assert.deepEqual([...provider.options].map(x=>x.value),['deepseek','qwen','glm','kimi']);
 model.value='deepseek-chat';key.value='model-test-secret';form.dispatchEvent(new dom.window.Event('submit',{cancelable:true}));
 await waitFor(()=>d.body.textContent.includes('已保存。'));assert.equal(key.value,'');assert.ok(key.placeholder.includes('留空保留'));
 model.value='deepseek-reasoner';form.dispatchEvent(new dom.window.Event('submit',{cancelable:true}));await waitFor(()=>!form.querySelector('button[type=submit]').disabled);
 const read=await (await fetch(origin+'/api/admin/config/ai-model')).json();assert.equal(read.model,'deepseek-reasoner');assert.equal(read.version,2);assert.equal(read.api_key,undefined);
 const stale=await fetch(origin+'/api/admin/config/ai-model',{method:'PUT',body:JSON.stringify({provider:'deepseek',model:'stale',api_key:'',expected_version:1})});assert.equal(stale.status,409);
 provider.value='qwen';provider.dispatchEvent(new dom.window.Event('change'));assert.equal(key.required,true);assert.equal(key.value,'');
}finally{dom.window.close()}
