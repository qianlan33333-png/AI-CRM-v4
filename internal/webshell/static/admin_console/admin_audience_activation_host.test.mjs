import { JSDOM, VirtualConsole } from 'jsdom';
import fs from 'node:fs';
import assert from 'node:assert/strict';
const host=fs.readFileSync(new URL('./admin_audience_activation_host.js',import.meta.url),'utf8');
const frozen=fs.readFileSync(new URL('./admin_audience_detail.js',import.meta.url),'utf8');
const template=fs.readFileSync(new URL('../../templates/admin_audience.html',import.meta.url),'utf8');
const wait=()=>new Promise(r=>setTimeout(r,60));
for (const detail of [false,true]) {
 const calls=[]; let attempts=0;
 const dom=new JSDOM(detail?'<main><button id="manualRefreshBtn"></button><button data-policy-action="activate">策略</button><button id="broadcastConfirmBtn" disabled>发送</button><div id="capabilityStatus">不可执行</div></main>':template,{url:'https://test.invalid/admin/automation-conversion'+(detail?'/packages/13':''),runScripts:'outside-only',virtualConsole:new VirtualConsole()});
 const w=dom.window; w.Headers=Headers; w.document.cookie='aicrm_admin_csrf=test-csrf';
 w.fetch=async(url,init={})=>{
  const path=new URL(url,w.location.origin).pathname; const method=init.method||'GET'; calls.push({path,method,body:init.body,headers:init.headers});
  const pkg={id:13,name:'规则',code:'rule',lifecycle:'paused',version:7,member_count:0};
  let body={}; let status=200;
  if(path.endsWith('/packages/13')) body={package:pkg};
  else if(path.endsWith('/packages')) body={items:[pkg],total:1};
  else if(path.endsWith('/precheck')) body={precheck:{ready:false,reasons:['sender_set_missing']}};
  else if(path.endsWith('/activate')) { attempts++; if(attempts===1) throw Error('network uncertain'); body={package:{...pkg,lifecycle:'active'}}; }
  else body={items:[]};
  return {ok:status===200,status,json:async()=>body};
 };
 if(!detail) w.eval(frozen);
 w.eval(host); await wait();
 const button=w.document.querySelector(detail?'#activateAudienceRuleBtn':'button[data-action="activate"]'); assert.ok(button);
 button.click();await wait();button.click();await wait();
 const writes=calls.filter(x=>x.path.endsWith('/activate'));assert.equal(writes.length,2);
 assert.equal(writes[0].body,JSON.stringify({expected_version:7}));
 assert.equal(writes[0].body,writes[1].body);assert.equal(writes[0].headers['Idempotency-Key'],writes[1].headers['Idempotency-Key']);
 assert.equal(writes[0].headers['X-CSRF-Token'],'test-csrf');
 assert.equal(calls.filter(x=>x.path.endsWith('/precheck')).length,0,'activation must not use sending precheck');
 if(detail){ assert.equal(w.document.querySelector('#broadcastConfirmBtn').disabled,true);assert.equal(w.document.querySelector('#capabilityStatus').textContent,'不可执行'); w.document.querySelector('[data-policy-action]').click(); await wait();assert.equal(calls.filter(x=>x.path.endsWith('/activate')).length,2); }
 dom.window.close();
}
console.log('audience activation host: list frozen controller + detail, CAS/CSRF/replay and send isolation passed');
