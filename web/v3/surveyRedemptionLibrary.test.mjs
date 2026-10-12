import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';
const compiled = await build({ entryPoints: ['web/v3/surveyRedemptionLibrary.ts'], bundle: true, write: false, format: 'iife', globalName: 'Library',
 plugins: [{name:'transport',setup(b){b.onResolve({filter:/src\/api\/transport$/},()=>({path:'transport',namespace:'mock'}));b.onLoad({filter:/.*/,namespace:'mock'},()=>({contents:'export const request=(...args)=>window.mockRequest(...args);',loader:'js'}));}}] });
const dom = new JSDOM(`<body><main><button data-tab="codes">兑换码库</button><section data-panel="codes"><p data-code-message></p><p data-code-counts></p><table><tbody data-code-rows></tbody></table><button data-code-previous></button><button data-code-next></button><button data-code-import></button><input data-code-file type="file"><button data-code-refresh></button><a data-code-template></a></section></main></body>`,{url:'https://crm.example/admin/questionnaireOps.html?id=9',runScripts:'outside-only'});
const {window}=dom; const calls=[];let failImport=true;let denied=false;
window.mockRequest=async(path,options={})=>{
 calls.push({path,...options});
 if(options.method==='POST'){
  if(failImport){failImport=false;throw new Error('网络中断')}
  return {json:async()=>({imported:1,duplicates:2,empty_rows:1})};
 }
 if(denied)throw {details:{message:'当前账号无权读取兑换码库'}};
 return {json:async()=>({items:[{code:'<img src=x onerror=alert(1)>',used_submission_id:42,used_at:'2026-10-12T00:00:00Z'}],total:1,available:0,used:1})};
};
window.eval(compiled.outputFiles[0].text+';globalThis.Library=Library;');window.Library.mountRedemptionLibrary(window.document.querySelector('main'),9);
const flush=()=>new Promise(resolve=>setTimeout(resolve,0));
window.document.querySelector('[data-tab="codes"]').click();await flush();
assert.equal(window.document.querySelectorAll('[data-code-rows] img').length,0,'codes render as text');
assert.match(window.document.querySelector('[data-code-counts]').textContent,/已使用 1 个/);
assert.equal(window.document.querySelector('[data-code-template]').getAttribute('href'),'/api/admin/questionnaires/9/redemption-codes/template');
const file=new window.File(['xlsx'],'codes.xlsx');const input=window.document.querySelector('[data-code-file]');
Object.defineProperty(input,'files',{value:[file]});input.dispatchEvent(new window.Event('change'));
window.document.querySelector('[data-code-import]').click();await flush();
assert.match(window.document.querySelector('[data-code-message]').textContent,/网络中断/);
window.document.querySelector('[data-code-import]').click();await flush();await flush();
const imports=calls.filter(call=>call.method==='POST');
assert.equal(imports.length,2);assert.equal(imports[0].headers['Idempotency-Key'],imports[1].headers['Idempotency-Key'],'unknown outcome retains original import key');
assert.equal(imports[1].body.get('file').name,'codes.xlsx');
assert.match(window.document.querySelector('[data-code-message]').textContent,/新增 1 个，跳过重复 2 个、空行 1 行/);
denied=true;window.document.querySelector('[data-code-refresh]').click();await flush();
assert.equal(window.document.querySelector('[data-code-rows]').children.length,0,'authorization failure clears credential rows');
dom.window.close();console.log('Survey redemption library interactions: PASS');
