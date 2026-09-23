import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const page = fs.readFileSync(path.join(root, 'web/dist/h5/auth.html'), 'utf8');
const host = await buildTestBrowserBundle(path.join(root, 'web/v3/h5AuthAdapter.ts'));
const runtime = await buildTestBrowserBundle(path.join(root, 'web/src/h5/main.ts'));
const dom = new JSDOM(page, { url: 'https://test.invalid/h5/auth.html?slug=survey', runScripts: 'outside-only' });

dom.window.eval(host);
const document = dom.window.document;
const template = document.getElementById('tpl');
assert.ok(template, 'frozen H5 runtime template must remain');
assert.equal(document.querySelector('.phone'), null, 'auth page must not retain the phone demo frame');
assert.equal(document.querySelector('a[href="index.html"]'), null, 'auth page must not retain the demo screen navigation');
assert.equal(template.content.querySelector('[data-h5-blocked]'), null, 'auth error binding must not remain an always-visible top warning');
assert.equal([...template.content.querySelectorAll('span,p')].some((node) => node.textContent?.includes('微信身份验证') || node.textContent?.includes('验证 UnionID')), false, 'auth page must not retain the demo identity explanation');
assert.ok([...template.content.querySelectorAll('h1')].some((node) => node.textContent?.includes('正在验证微信身份')), 'auth title must remain');
assert.ok(template.innerHTML.includes('重试微信授权'), 'failed OAuth must offer retry');
assert.ok(template.innerHTML.includes('当前不在微信内打开'), 'non-WeChat guidance must remain');
const errorTemplate = [...template.content.querySelectorAll('template[data-sc-if]')].find((node) => node.getAttribute('data-sc-if') === '{{ error }}');
assert.ok(errorTemplate?.content.querySelector('[data-h5-blocked][role="status"]'), 'frozen controller error binding must move into the auth card');
assert.ok(errorTemplate?.innerHTML.includes('{{ error }}'), 'moved auth error must retain the frozen controller binding');
dom.window.close();

const errorDom = new JSDOM(page, {
  url: 'https://test.invalid/h5/auth.html?slug=survey', runScripts: 'outside-only', pretendToBeVisual: true,
  beforeParse(window) {
    Object.defineProperty(window.navigator, 'userAgent', { configurable: true, value: 'MicroMessenger' });
    window.fetch = async () => new Response('', { status: 409 });
  },
});
errorDom.window.Response = Response;
errorDom.window.Headers = Headers;
errorDom.window.eval(host);
errorDom.window.eval(runtime);
await new Promise((resolve) => setTimeout(resolve, 20));
assert.ok(errorDom.window.document.querySelector('#screen [data-h5-blocked]')?.textContent?.includes('当前微信身份存在冲突'), 'frozen auth 409 must remain visible beside the OAuth action');
assert.equal(errorDom.window.document.querySelector('#screen').textContent?.includes('授权仅用于识别本次问卷所属客户，不会发送短信。'), false, 'ordinary WeChat notice must not occupy the cleaned page');
errorDom.window.close();
console.log('h5 auth Host presentation journey: PASS');
for (const name of ['all', 'one', 'result', 'done']) {
  const html = fs.readFileSync(path.join(root, `web/dist/h5/${name}.html`), 'utf8');
  const mobile = new JSDOM(html, {url:`https://test.invalid/h5/${name}.html?slug=survey`,runScripts:'outside-only'});
  const d = mobile.window.document;
  assert.equal(d.querySelector('.phone'),null, `${name}: release HTML must have no demo frame before scripts run`);
  assert.equal(d.querySelector('a[href="index.html"]'),null, `${name}: release HTML must have no demo navigation`);
  mobile.window.eval(host);
  const content = d.querySelector('#tpl').content;
  assert.equal(content.textContent.includes('增长诊断测评'),false, `${name}: fixed demo title must be removed`);
  if (name !== 'done') assert.ok(d.querySelector('#tpl').innerHTML.includes('data-h5-error'), `${name}: actual error feedback must remain`);
  assert.ok(d.querySelector('#screen').style.cssText.includes('width: 100%'), `${name}: mobile width must be fluid`);
  assert.equal(d.querySelector('#screen').style.height,'', `${name}: no fixed device-height crop`);
  assert.equal(d.querySelector('#screen').style.overflow,'', `${name}: long content must not be clipped`);
  if (name === 'done') {
    const completionTemplate = d.querySelector('#tpl').innerHTML;
    assert.ok(completionTemplate.includes('data-sc-if="{{ done }}"') && completionTemplate.includes('data-h5-done'), 'done: built completion content must be guarded by confirmed submission state');
    assert.ok(completionTemplate.includes('data-sc-if="{{ leadQR }}"') && completionTemplate.includes('data-h5-lead-qr'), 'done: built completion content must preserve the optional authorized channel QR branch');
    assert.equal(completionTemplate.includes('尚无可核验回执'), false, 'done: built completion content must replace the frozen unavailable receipt carrier');
    assert.equal(content.querySelector('[data-h5-blocked]'), null, 'done: completion page must not retain the obsolete blocked banner');
  } else {
    const binding = name === 'result' ? '{{ resultTitle }}' : '{{ title }}';
    assert.ok(d.querySelector('#tpl').innerHTML.includes(binding), `${name}: real questionnaire title binding must remain`);
  }
  mobile.window.close();
}
console.log('survey mobile release shell: PASS');


// Run the real auth controller with only its renderer/API imports isolated.
// A session miss auto-starts once; callback failure or a lost cookie cannot loop.
const {build} = await import('esbuild');
const {runInNewContext} = await import('node:vm');
const compiled = await build({entryPoints:[path.join(root,'web/src/h5/controller.ts')],bundle:true,write:false,format:'iife',globalName:'AuthController',plugins:[{name:'auth-controller-dependencies',setup(b){b.onResolve({filter:/^\.\.\//},args=>({path:args.path,namespace:'auth-dependency'}));b.onLoad({filter:/.*/,namespace:'auth-dependency'},()=>({contents:'export class PageBase {} export class ApiError extends Error {} export const toast=()=>{}; export const completionAction=(value)=>value||{type:"default"}; export const completionActionFromCarrier=(value)=>value?.completion_action||{type:"default"}; export const readPublicSurvey=()=>{}; export const readSurveyResult=()=>{}; export const submitSurvey=()=>{}; export const formatShanghaiDateTime=(value)=>value;',loader:'js'}));}}]});
const marker = new Map();
async function authRun(status, search='?slug=survey', userAgent='MicroMessenger', body={display:'one'}) {
  const redirects=[], calls=[];
  const sandbox={URLSearchParams,navigator:{userAgent},location:{search,origin:'https://test.invalid',replace(url){redirects.push(url)}},sessionStorage:{getItem:k=>marker.get(k),setItem:(k,v)=>marker.set(k,v),removeItem:k=>marker.delete(k)},fetch:async url=>{calls.push(url);return {ok:status===200,status,json:async()=>body}}};
  runInNewContext(compiled.outputFiles[0].text,sandbox);
  const controller=new sandbox.AuthController.H5Controller('auth');await controller.init();
  return {controller,redirects,calls};
}
const fresh=await authRun(401);
assert.deepEqual(fresh.redirects,['/api/h5/surveys/oauth/start?slug=survey']);
assert.equal(fresh.calls.length,1);
assert.equal(fresh.controller.renderVals().authRetry,false);
const lostCookie=await authRun(401);
assert.equal(lostCookie.redirects.length,0);
assert.match(lostCookie.controller.renderVals().error,/请重试/);
assert.equal(lostCookie.controller.renderVals().authRetry,true);
lostCookie.controller.renderVals().act.authContinue();
assert.equal(lostCookie.redirects.length,1);
const failed=await authRun(401,'?slug=survey&oauth_error=1');
assert.equal(failed.redirects.length,0);
assert.equal(failed.calls.length,0);
const known=await authRun(200);
assert.deepEqual(known.redirects,['/h5/one.html?slug=survey']);
assert.equal(marker.size,0);

const alreadyDone=await authRun(200,'?slug=survey','MicroMessenger',{submitted:true,completion_action:{type:'default'}});
assert.deepEqual(alreadyDone.redirects,['/h5/done.html?slug=survey'],'a trusted session with an earlier submission leaves the answer route through the default completion page');
const alreadyRedirected=await authRun(200,'?slug=survey','MicroMessenger',{submitted:true,completion_action:{type:'redirect',redirect_url:'https://completion.example/next'}});
assert.deepEqual(alreadyRedirected.redirects,['https://completion.example/next'],'a trusted session reuses its Owner-computed redirect completion action');

const outsideWeChat=await authRun(401,'?slug=survey&oauth_error=1','Mozilla/5.0');
assert.equal(outsideWeChat.controller.renderVals().authRetry,false);
outsideWeChat.controller.renderVals().act.authContinue();
assert.equal(outsideWeChat.redirects.length,0);
assert.equal(outsideWeChat.calls.length,0);
