import assert from 'node:assert/strict';
import { build } from 'esbuild';
import { JSDOM } from 'jsdom';

const bundle = await build({entryPoints:['web/v3/governanceOutcomes.ts'],bundle:true,format:'iife',globalName:'OutcomesLeaf',platform:'browser',write:false,target:'es2020'});
const flush = async () => { for(let i=0;i<5;i++) await new Promise(resolve=>setTimeout(resolve,0)); };
const at = new Date(Date.now()-3600000).toISOString();
const episode = (id, overrides={}) => ({id,issue_id:id,version:1,check_id:'identity.conflicts',code:'open_conflicts',detected_release:'a'.repeat(40),detection_origin:'new_observation',initial_status:'unknown',latest_status:'unknown',detected_at:at,last_observed_at:at,recovered_at:null,classification:'unclassified',escaped_defect:'unclassified',change_failure:'unclassified',caused_by_release_sequence:null,root_cause:'unknown',remediation:'unknown',fault_started_at:null,fault_start_basis:'unknown',effort_minutes:null,attributed_at:null,...overrides});
const page = (ids,more=false) => ({items:ids.map(id=>episode(id)),has_more:more,...(more?{next_before_id:ids.at(-1)}:{})});
const metrics = path => {const q=new URL(path,'https://fixture.invalid').searchParams;return {window:{from:q.get('from'),to:q.get('to')},episode_count:75,confirmed_defects:0,unclassified_episodes:75,open_count:75,mttd:{mean_seconds:null,sample_count:0,missing_count:0},detected_to_recovered:{mean_seconds:null,sample_count:0,missing_count:0},fault_to_recovered:{mean_seconds:null,sample_count:0,missing_count:0},escaped_defects:{ratio:null,numerator:0,denominator:0,unclassified:0},repeated_defects:{ratio:null,numerator:0,denominator:0,unclassified:75},effort_minutes_total:0,effort_known_count:0,effort_missing_count:75,deployments:{evidence_state:'unavailable',evidence_at:null,verified_successful_deployments:0,confirmed_failed_deployments:0,verified_success_cohort_failure_ratio:null,historical_sequence_gaps:null,unclassified_defect_attributions:0,revoked_release_attributions:0,releases:[],releases_truncated:false}};};
async function setup(first=page([75,74],true)){
 const dom=new JSDOM('<!doctype html><main id="root"></main>',{url:'https://fixture.invalid/admin/ops',runScripts:'outside-only'});
 dom.window.HTMLDialogElement.prototype.showModal=function(){this.open=true;};
 dom.window.HTMLDialogElement.prototype.close=function(){this.open=false;this.dispatchEvent(new dom.window.Event('close'));};
 dom.window.eval(bundle.outputFiles[0].text+'\nwindow.outcomesView=OutcomesLeaf.governanceOutcomesView;');
 const pending=[];const calls=[];let active=true;let refreshes=0;
 const request=(path,method='GET',body,key)=>new Promise((resolve,reject)=>{const call={path,method,body,key,resolve,reject};calls.push(call);pending.push(call);});
 const loading=dom.window.outcomesView(request);
 const m=pending.shift(),p=pending.shift();assert.equal(new URL(m.path,'https://fixture.invalid').searchParams.get('from'),new URL(p.path,'https://fixture.invalid').searchParams.get('from'));
 m.resolve(metrics(m.path));p.resolve(first);const view=await loading;const root=dom.window.document.querySelector('#root');root.innerHTML=view.html;view.bind(root,()=>active,async()=>{refreshes++;});
 return {dom,root,pending,calls,get refreshes(){return refreshes;},leave(){active=false;root.innerHTML='<p>新的报告标签</p>';},close(){active=false;dom.window.close();}};
}
const button=(h,selector)=>h.root.querySelector(selector);
async function respondLoad(h,next){const m=h.pending.shift(),p=h.pending.shift();assert.match(m.path,/\/outcomes\?/);assert.match(p.path,/\/episodes\?/);m.resolve(metrics(m.path));p.resolve(next);await flush();return p;}
async function detail(h,id,overrides={}){button(h,`[data-outcomes-episode="${id}"]`).click();const call=h.pending.shift();assert.equal(call.path,`/api/admin/ops-governance/episodes/${id}`);call.resolve(episode(id,overrides));await flush();return h.dom.window.document.querySelector('dialog');}
function setField(h,name,value){const f=h.dom.window.document.querySelector(`dialog [name="${name}"]`);f.value=String(value);f.dispatchEvent(new h.dom.window.Event('change',{bubbles:true}));return f;}
function submit(h){h.dom.window.document.querySelector('dialog form').dispatchEvent(new h.dom.window.Event('submit',{bubbles:true,cancelable:true}));}
function receipt(id,version=2,replay=false){return {episode_id:id,version,action_id:19,replay};}
let passed=0;

{
 const h=await setup();assert.match(h.root.textContent,/证据不足/);assert.match(h.root.textContent,/不能报告全量变更失败率/);assert.equal(button(h,'[data-outcomes-page-action="previous"]').disabled,true);
 button(h,'[data-outcomes-page-action="next"]').click();let call=await respondLoad(h,page([50,49],true));assert.equal(new URL(call.path,'https://fixture.invalid').searchParams.get('before_id'),'74');
 let drawer=await detail(h,50);assert.ok(drawer.open);assert.ok(drawer.querySelector('form'),'second page retains native attribution drawer');drawer.querySelector('button').click();
 button(h,'[data-outcomes-page-action="next"]').click();call=await respondLoad(h,page([25],true));assert.equal(new URL(call.path,'https://fixture.invalid').searchParams.get('before_id'),'49');
 drawer=await detail(h,25);assert.ok(drawer.querySelector('[data-attribution-save]'));drawer.querySelector('button').click();
 button(h,'[data-outcomes-page-action="next"]').click();await respondLoad(h,page([1]));assert.match(button(h,'[data-outcomes-page]').textContent,/第 4 页/);assert.equal(button(h,'[data-outcomes-page-action="next"]').disabled,true);
 button(h,'[data-outcomes-page-action="previous"]').click();call=await respondLoad(h,page([25],true));assert.equal(new URL(call.path,'https://fixture.invalid').searchParams.get('before_id'),'49');assert.match(button(h,'[data-outcomes-page]').textContent,/第 3 页/);h.close();passed++;
}
{
 const h=await setup();button(h,'[data-outcomes-page-action="next"]').click();await respondLoad(h,{items:[episode(50)],has_more:true});
 assert.match(button(h,'[data-outcomes-status]').textContent,/游标缺失/);assert.ok(button(h,'[data-outcomes-episode="75"]'));assert.equal(button(h,'[data-outcomes-page-action="next"]').disabled,false);
 button(h,'[data-outcomes-page-action="next"]').click();await respondLoad(h,page([50]));assert.ok(button(h,'[data-outcomes-episode="50"]'));h.close();passed++;
}
{
 const h=await setup();let select=button(h,'[data-outcomes-days]');select.value='7';select.dispatchEvent(new h.dom.window.Event('change',{bubbles:true}));const old=h.pending.splice(0,2);
 select.value='90';select.dispatchEvent(new h.dom.window.Event('change',{bubbles:true}));const newest=h.pending.splice(0,2);
 const q=new URL(newest[0].path,'https://fixture.invalid').searchParams;assert.equal(Date.parse(q.get('to'))-Date.parse(q.get('from')),90*86400000);
 newest[0].resolve(metrics(newest[0].path));newest[1].resolve(page([9]));await flush();old[0].resolve(metrics(old[0].path));old[1].resolve(page([7]));await flush();
 assert.equal(button(h,'[data-outcomes-days]').value,'90');assert.ok(button(h,'[data-outcomes-episode="9"]'));assert.equal(button(h,'[data-outcomes-episode="7"]'),null);h.close();passed++;
}
{
 const h=await setup(page([1]));await detail(h,1);setField(h,'classification','confirmed_defect');setField(h,'root_cause','code');setField(h,'effort_minutes','0');submit(h);
 const first=h.pending.shift();assert.equal(first.method,'PUT');assert.equal(first.body.expected_version,1);assert.equal(first.body.effort_minutes,0);assert.equal(first.body.fault_started_at,null);assert.ok(first.key);assert.ok(Object.isFrozen(first.body));
 first.reject(new h.dom.window.Error('网络结果未知'));await flush();assert.match(h.dom.window.document.querySelector('[data-attribution-status]').textContent,/字段已冻结/);
 assert.ok([...h.dom.window.document.querySelectorAll('dialog input,dialog select')].every(f=>f.disabled));
 // Even programmatically mutating a disabled field cannot alter a replay body.
 setField(h,'root_cause','data');submit(h);const second=h.pending.shift();assert.equal(second.key,first.key);assert.equal(second.body,first.body);assert.equal(second.body.root_cause,'code');
 second.resolve(receipt(1,2,true));await flush();assert.match(h.dom.window.document.querySelector('[data-attribution-status]').textContent,/重放收据/);assert.equal(h.refreshes,0);await respondLoad(h,page([1]));assert.match(h.dom.window.document.querySelector('[data-attribution-status]').textContent,/已保存/);submit(h);assert.equal(h.pending.length,0);h.close();passed++;
}
{
 const h=await setup(page([1]));await detail(h,1);submit(h);const first=h.pending.shift();const conflict=new h.dom.window.Error('记录已更新');conflict.status=409;first.reject(conflict);await flush();
 h.dom.window.document.querySelector('[data-attribution-reload]').click();const read=h.pending.shift();assert.equal(read.method,'GET');read.resolve(episode(1,{version:4,root_cause:'configuration'}));await flush();
 assert.equal(h.dom.window.document.querySelector('[name="root_cause"]').value,'configuration');setField(h,'root_cause','code');submit(h);const changed=h.pending.shift();assert.notEqual(changed.key,first.key);assert.equal(changed.body.expected_version,4);assert.equal(changed.body.root_cause,'code');h.close();passed++;
}
{
 const h=await setup(page([1]));await detail(h,1);submit(h);const first=h.pending.shift();first.resolve({});await flush();
 assert.match(h.dom.window.document.querySelector('[data-attribution-status]').textContent,/结果尚未确认/);assert.doesNotMatch(h.dom.window.document.querySelector('[data-attribution-status]').textContent,/归因已保存/);submit(h);const retry=h.pending.shift();assert.equal(retry.key,first.key);assert.equal(retry.body,first.body);h.close();passed++;
}
for(const write of [false,true]){
 const h=await setup(page([1]));let call;
 if(write){await detail(h,1);submit(h);call=h.pending.shift();}else{button(h,'[data-outcomes-episode="1"]').click();call=h.pending.shift();}
 h.leave();await flush();call.resolve(write?receipt(1):episode(1));await flush();assert.equal(h.root.textContent,'新的报告标签');assert.equal(h.pending.length,0);assert.equal(h.dom.window.document.querySelector('dialog'),null);h.close();passed++;
}
{
 const h=await setup(page([1]));await detail(h,1);setField(h,'classification','confirmed_defect');setField(h,'fault_started_at','2026-09-18T01:00:00');setField(h,'fault_start_basis','operator_confirmed');submit(h);assert.equal(h.pending.length,0);assert.match(h.dom.window.document.querySelector('[data-attribution-status]').textContent,/须带时区/);
 setField(h,'escaped_defect','yes');setField(h,'change_failure','yes');setField(h,'caused_by_release_sequence','7');setField(h,'classification','legacy_backlog');
 assert.equal(h.dom.window.document.querySelector('[name="escaped_defect"]').value,'unclassified');assert.equal(h.dom.window.document.querySelector('[name="fault_started_at"]').value,'');assert.equal(h.dom.window.document.querySelector('[name="caused_by_release_sequence"]').value,'');
 submit(h);const call=h.pending.shift();assert.equal(call.body.classification,'legacy_backlog');assert.equal(call.body.escaped_defect,'unclassified');assert.equal(call.body.caused_by_release_sequence,null);assert.equal(call.body.fault_started_at,null);h.close();passed++;
}
{
 const h=await setup(page([1]));await detail(h,1,{code:'<script>secret</script>'});assert.equal(h.dom.window.document.querySelector('dialog script'),null);assert.match(h.dom.window.document.querySelector('dialog').textContent,/<script>secret<\/script>/);
 h.dom.window.document.querySelector('[data-attribution-reload]').click();h.pending.shift().reject(new h.dom.window.Error('暂不可用'));await flush();assert.equal(h.dom.window.document.querySelector('dialog'),null);assert.match(button(h,'[data-outcomes-status]').textContent,/暂不可用/);
 await detail(h,1);assert.ok(h.dom.window.document.querySelector('dialog form'),'a failed reload must not leave an inert old drawer');h.close();passed++;
}
{
 const h=await setup();button(h,'[data-outcomes-refresh]').click();const m=h.pending.shift(),p=h.pending.shift();const malformed=metrics(m.path);malformed.mttd.mean_seconds=0;m.resolve(malformed);p.resolve(page([1]));await flush();assert.match(button(h,'[data-outcomes-status]').textContent,/不能把未知当作零/);assert.ok(button(h,'[data-outcomes-episode="75"]'));h.close();passed++;
}
console.log(`governance outcomes DOM: ${passed} cases PASS`);
