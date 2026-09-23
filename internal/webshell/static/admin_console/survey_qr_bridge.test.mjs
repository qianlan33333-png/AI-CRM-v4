import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { build } from "esbuild";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const adapter = fs.readFileSync(path.join(here, "survey_operations.js"), "utf8");
const committedSearch = (await build({
  stdin: {
    contents: "import { installCommittedTextSearch } from './web/v3/shared/ui/committedTextSearch'; installCommittedTextSearch();",
    resolveDir: root,
    sourcefile: "survey-qr-bridge-committed-search.ts",
  },
  bundle: true,
  format: "iife",
  platform: "browser",
  target: "es2020",
  write: false,
  minify: true,
  logLevel: "warning",
})).outputFiles[0].text.replace(/<\/script/gi, "<\\/script");
const wait = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const enter = (window, input, properties = {}) => {
  const event = new window.KeyboardEvent("keydown", { bubbles: true, cancelable: true, key: "Enter", code: "Enter" });
  Object.entries(properties).forEach(([name, value]) => Object.defineProperty(event, name, { value }));
  input.dispatchEvent(event);
  return event;
};

function legacyOpsFixture() {
  return `<!doctype html><body data-page="questionnaireOps"><div id="stage">
    <div id="preserved-submit-actions"><button id="completion-nav">提交后动作</button><input id="completion-channel" value="channel-1"></div>
    <div id="external-card"><div><div><h3>外部推送绑定</h3></div></div><input id="opsConfigurationReference" value="push.v1"><button id="legacy-test">测试推送（仅本地记录）</button><button id="legacy-save">保存外部推送</button></div>
    <div id="legacy-log-card"><h3>当前问卷本地外推测试记录</h3><button id="legacy-global">全部问卷</button><table><tbody><tr><td>旧日志</td></tr></tbody></table></div>
  </div></body>`;
}

function qrPage() {
  const dom = new JSDOM('<!doctype html><body data-page="questionnaires"></body>', {
    url: 'https://test.invalid/admin/questionnaires',
    runScripts: 'outside-only',
    pretendToBeVisual: true,
  });
  dom.window.eval(committedSearch);
  dom.window.eval(adapter);
  return dom;
}

const qr = qrPage();
const box = qr.window.document.createElement('div');
box.id = 'shareQrBox';
qr.window.document.body.appendChild(box);
await wait(1300);
const fallback = box.querySelector('[data-survey-qr-fallback="true"][role="alert"]');
if (!fallback || !fallback.textContent.includes('复制')) throw new Error('empty QR mount did not expose the copy-link fallback');
qr.window.close();

const rendered = qrPage();
const renderedBox = rendered.window.document.createElement('div');
renderedBox.id = 'shareQrBox';
rendered.window.document.body.appendChild(renderedBox);
await wait(100);
renderedBox.appendChild(rendered.window.document.createElementNS('http://www.w3.org/2000/svg', 'svg'));
await wait(1200);
if (renderedBox.querySelector('[data-survey-qr-fallback="true"]')) throw new Error('fallback replaced a rendered QR SVG');
rendered.window.close();

const dom = new JSDOM(legacyOpsFixture(), {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(dom.window.document, 'cookie', {value:'aicrm_admin_csrf=csrf-proof', configurable:true});
Object.defineProperty(dom.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
let rawPosts = 0; const rawCalls = []; const savedBodies = [];
dom.window.fetch = async (url, options = {}) => {
  const path = String(url); rawCalls.push({path, options});
  if (path.includes('/external-push/test')) { rawPosts += 1; if (rawPosts === 3) return {ok:false,status:503,json:async()=>({})}; return {ok:true,status:202,json:async()=>({questionnaire_id:7,test_run_id:'questionnaire-test-0123456789abcdef0123456789abcdef',effect_id:'eer_7',status:'queued',accepted:true,synthetic_data:true})}; }
  if (path.endsWith('/external-push')) { const body = JSON.parse(options.body); savedBodies.push(body); return {ok:true,json:async()=>({configuration_version: body.configuration_version + 1, external_push:{enabled:body.enabled,configuration_reference:body.configuration_reference,metadata:body.metadata}})}; }
  if (path.includes('/external-push-logs')) return {ok:true,json:async()=>({items:[
    {test_run_id:'questionnaire-test-executed',status:'executed',attempt_count:1,provider_result_received:true,updated_at:'2026-09-05T00:00:00Z'},
    {test_run_id:'questionnaire-test-unknown',status:'outcome_unknown',attempt_count:2,provider_result_received:false,updated_at:'2026-09-05T00:00:01Z'},
    {test_run_id:9,status:'disabled',updated_at:'2026-08-01T00:00:00Z',read_only_legacy:true},
  ]})};
  return {ok:true,json:async()=>({items:[{source_pk:'questionnaire-test-0123456789abcdef0123456789abcdef',status:'queued',occurred_at:'2026-09-05T00:00:00Z'}],target_catalog_available:true,available_configuration_references:['push.v1','push.v2'],configuration_version:3,external_push:{enabled:true,configuration_reference:'push.v1',metadata:{type:'old',custom_params:{legacy:'yes'}}}})};
};
dom.window.eval(committedSearch); dom.window.eval(adapter); dom.window.document.dispatchEvent(new dom.window.Event('DOMContentLoaded'));
await wait(40);
const frozenLogs = await dom.window.fetch('/admin/questionnaires/7/external-push-logs');
const frozenPayload = await frozenLogs.json();
if (!frozenPayload.local_only || frozenPayload.items.length !== 0 || rawCalls.some((item) => item.path === '/admin/questionnaires/7/external-push-logs')) throw new Error('queued-only donor mapper was not isolated');
if (!dom.window.document.querySelector('#preserved-submit-actions') || !dom.window.document.querySelector('#legacy-save') || !dom.window.document.querySelector('[data-survey-push-metadata]')) throw new Error('Host removed existing questionnaire operations controls');
let navClicks = 0; dom.window.document.querySelector('#completion-nav').addEventListener('click', () => { navClicks += 1; }); dom.window.document.querySelector('#completion-nav').click();
if (navClicks !== 1 || !dom.window.document.querySelector('#completion-channel')) throw new Error('submit actions navigation was not preserved');
const oldTest = dom.window.document.querySelector('#legacy-test');
async function confirmControlledTest() { oldTest.click(); await wait(5); const confirm = dom.window.document.querySelector('[data-survey-host-test-confirmation] [data-survey-host-test-confirm]'); if (!confirm) throw new Error('controlled test did not require Host confirmation'); confirm.click(); await wait(40); }
await confirmControlledTest(); await confirmControlledTest(); await confirmControlledTest();
if (!oldTest.textContent.includes('创建受控测试失败')) throw new Error('failed controlled test did not report its failure'); await confirmControlledTest();
if (rawPosts !== 4 || !oldTest.textContent.includes('受控外推测试已创建') || !dom.window.document.body.textContent.includes('等待处理') || dom.window.document.body.textContent.includes('仅本地记录')) throw new Error('Host controlled test was not confirmed or did not report the honest receipt boundary');
dom.window.document.querySelector('[data-survey-log-scope="global"]').click(); await wait(5);
const allText = dom.window.document.body.textContent;
if (!allText.includes('已收到处理结果') || !allText.includes('处理结果待确认（不会自动重复发送）') || !allText.includes('当时未启用外推配置') || !allText.includes('尝试 2 次')) throw new Error('global true statuses and legacy attempt count were not rendered');
let filter = dom.window.document.querySelector('[placeholder="测试记录 ID / 问卷 ID"]');
filter.focus();
filter.value = 'unknown';
filter.dispatchEvent(new dom.window.Event('input', {bubbles:true}));
filter.value = 'unknown-';
filter.dispatchEvent(new dom.window.Event('input', {bubbles:true}));
if (dom.window.document.activeElement !== filter || !dom.window.document.body.textContent.includes('处理结果待确认') || !dom.window.document.body.textContent.includes('已收到处理结果')) {
  throw new Error('draft survey filter changed rows or lost focus before a committed search');
}
filter.value = 'unknown';
filter.dispatchEvent(new dom.window.Event('input', {bubbles:true}));
filter.dispatchEvent(new dom.window.CompositionEvent('compositionstart', {bubbles:true}));
const candidateEnter = enter(dom.window, filter, {isComposing:true, keyCode:229});
filter.dispatchEvent(new dom.window.CompositionEvent('compositionend', {bubbles:true}));
if (candidateEnter.defaultPrevented || dom.window.document.activeElement !== filter || !dom.window.document.body.textContent.includes('已收到处理结果')) {
  throw new Error('IME candidate Enter committed the survey filter');
}
await wait(5);
enter(dom.window, filter);
await wait(5);
filter = dom.window.document.querySelector('[placeholder="测试记录 ID / 问卷 ID"]');
if (!filter || dom.window.document.activeElement !== filter || filter.value !== 'unknown' || !dom.window.document.body.textContent.includes('处理结果待确认') || dom.window.document.body.textContent.includes('已收到处理结果')) {
  throw new Error('ordinary Enter did not commit the survey filter and restore search focus');
}
const form = dom.window.document.querySelector('[data-survey-push-metadata]');
form.elements.type.value = 'new'; form.querySelector('[data-param-name]').value = 'campaign'; form.querySelector('[data-param-value]').value = 'autumn'; dom.window.document.querySelector('#opsConfigurationReference').value = 'push.v2';
form.dispatchEvent(new dom.window.Event('submit', {bubbles:true,cancelable:true})); await wait(25);
form.dispatchEvent(new dom.window.Event('submit', {bubbles:true,cancelable:true})); await wait(25);
if (savedBodies.length !== 2 || savedBodies[0].configuration_version !== 3 || savedBodies[1].configuration_version !== 4 || savedBodies[0].configuration_reference !== 'push.v2' || savedBodies[1].metadata.custom_params.campaign !== 'autumn' || savedBodies.some((body) => body.enabled !== true) || savedBodies.some((body) => !body || !body.metadata)) throw new Error('parameter save did not preserve the existing binding or advance its version');
// Editing/preview is local, preserves the original questionnaire body and
// rejects collisions before sending a request.
const beforePreviewCalls = rawCalls.length;
const preview = form.querySelector('[data-survey-push-preview]');
form.elements.type.dispatchEvent(new dom.window.Event('input', {bubbles:true}));
if (JSON.parse(preview.textContent).campaign !== 'autumn' || rawCalls.length !== beforePreviewCalls) throw new Error('preview made a request or lost configured fixed values');
if (!form.textContent.includes('原有问卷 JSON 保持不变') || form.textContent.includes('订单 ·')) throw new Error('survey inherited order variables or hid the preserved payload contract');
const addField = [...form.querySelectorAll('button')].find((b) => b.textContent.includes('添加字段'));
addField.click();
const rows = form.querySelectorAll('[data-survey-push-params] .survey-push-row');
const newKey = rows[rows.length - 1].querySelector('[data-param-name]');
for (const invalid of ['campaign', 'answers', ' bad', '__proto__']) {
 newKey.value = invalid;
 form.dispatchEvent(new dom.window.Event('submit', {bubbles:true,cancelable:true}));
 await wait(5);
 if (savedBodies.length !== 2 || newKey.getAttribute('aria-invalid') !== 'true') throw new Error('invalid or reserved key reached save');
}
rows[rows.length - 1].querySelector('button').click();
form.elements.day.value = '1.5';
form.dispatchEvent(new dom.window.Event('submit', {bubbles:true,cancelable:true}));
await wait(5);
if (savedBodies.length !== 2 || form.elements.day.getAttribute('aria-invalid') !== 'true') throw new Error('fractional day reached save');
dom.window.close();

const conflict = new JSDOM(legacyOpsFixture(), {url:'https://test.invalid/admin/questionnaireOps.html?id=7', runScripts:'outside-only', pretendToBeVisual:true});
Object.defineProperty(conflict.window.document, 'cookie', {value:'aicrm_admin_csrf=csrf-proof', configurable:true});
Object.defineProperty(conflict.window.crypto, 'randomUUID', {value:()=> '00000000-0000-4000-8000-000000000000'});
conflict.window.fetch = async (url, options = {}) => {
  const path = String(url); if (path.endsWith('/external-push')) return {ok:false,status:409,json:async()=>({})};
  if (path.includes('/external-push-logs')) return {ok:true,json:async()=>({items:[]})}; return {ok:true,json:async()=>({items:[],configuration_version:3,external_push:{enabled:true,configuration_reference:'push.v1',metadata:{}}})};
};
conflict.window.eval(adapter); conflict.window.document.dispatchEvent(new conflict.window.Event('DOMContentLoaded')); await wait(35); const conflictForm = conflict.window.document.querySelector('[data-survey-push-metadata]'); conflictForm.dispatchEvent(new conflict.window.Event('submit', {bubbles:true,cancelable:true})); await wait(20);
if (!conflictForm.querySelector('button[type="submit"]').textContent.includes('重新打开')) throw new Error('CAS conflict was shown as saved');
conflict.window.close();

console.log('survey-operations-preserved-shell-journey: PASS');
console.log('survey-qr-bridge-browser: PASS');
