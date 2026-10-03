import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
const here = path.dirname(fileURLToPath(import.meta.url));
const template = fs.readFileSync(path.resolve(here, "../../templates/admin_audience_detail.html"), "utf8").replace(/^\{\{define[^\n]+\n/, "").replace(/\{\{end\}\}\s*$/, "");
const script = fs.readFileSync(path.join(here,"admin_audience_detail.js"),"utf8");
const json = (body,status=200) => ({ok:status===200,status,json:async()=>body});
const runs = Array.from({length:205},(_,index)=>({id:205-index,package_id:33,state:"completed",target_count:1,skipped_count:0,created_at:"2026-10-02T12:00:00Z"}));
const calls = [];
let failNext=false, holdNext=false, releaseHeld;
const dom = new JSDOM(`<!doctype html><body>${template}</body>`,{
 url:"https://test.invalid/admin/automation-conversion/packages/33",runScripts:"outside-only",pretendToBeVisual:true,
 beforeParse(window){
  window.Headers=globalThis.Headers;
  window.fetch=async(input,init={})=>{
   const url=new URL(input,window.location.origin);
   calls.push({url,method:init.method||"GET"});
   if(url.pathname==="/api/admin/automation-runs"){
    assert.equal(url.searchParams.get("package_id"),"33","must never fetch the global first page");
    const cursor=Number(url.searchParams.get("cursor")||206);
    if(cursor<206&&failNext){failNext=false;return json({error:"invalid_automation_runtime_request"},400);}
    if(cursor<206&&holdNext){holdNext=false;await new Promise(resolve=>{releaseHeld=resolve;});}
    const matches=runs.filter(run=>run.id<cursor);
    const items=matches.slice(0,100);
    return json({items,total:runs.length,next_cursor:matches.length>100?String(items.at(-1).id):""});
   }
   if(url.pathname.endsWith("/direct-pushes"))return json({items:[]});
   if(url.pathname==="/api/admin/ai-audience/packages/33")return json({package:{id:33,name:"历史人群",membership_mode:"rule",version:6,lifecycle:"active",member_count:24}});
   if(url.pathname.endsWith("/configuration"))return json({configuration:{refresh_mode:"every_1h",definition:{template_key:"questionnaire_submissions",parameters:{}}}});
   if(url.pathname.endsWith("/members"))return json({snapshot:{member_count:24},items:[]});
   if(url.pathname.endsWith("/precheck"))return json({precheck:{ready:true,reasons:[]}});
   if(url.pathname.endsWith("/package-groups")||url.pathname.endsWith("/templates")||url.pathname.endsWith("/automation-agents"))return json({items:[]});
   return json({error:"not_found"},404);
  };
 }
});
const waitFor=async(check)=>{for(let n=0;n<100;n++){if(check())return;await new Promise(resolve=>setTimeout(resolve,10));}throw new Error("UI state did not settle");};
dom.window.eval(script);
const doc=dom.window.document;
await waitFor(()=>doc.querySelector("#capabilityStatus").dataset.capabilityState==="ready");
assert.equal(doc.querySelector("#summaryCount").textContent,"24");
assert.equal(doc.querySelector("#summaryMode").textContent,"每小时增量");
assert.ok(doc.body.textContent.includes("不是累计发送人数"));
doc.querySelector('[data-panel="records"]').click();
await waitFor(()=>doc.querySelector("#sendRecordPageLabel").textContent.includes("第 1 / 3 页"));
const rowIDs=()=>[...doc.querySelectorAll("#sendRecordRows tr")].map(row=>Number(row.cells[0].textContent.match(/^#(\d+)/)?.[1]));
assert.equal(rowIDs().length,100);
assert.equal(rowIDs()[0],205);
assert.ok(doc.querySelector("#sendRecordTotal").textContent.includes("累计 205 次运行"));
assert.equal(doc.querySelector("#sendRecordPrevBtn").disabled,true);
failNext=true;
doc.querySelector("#sendRecordNextBtn").click();
await waitFor(()=>doc.querySelector("#sendRecordStatusLine").dataset.state==="error");
assert.ok(doc.querySelector("#sendRecordPageLabel").textContent.includes("第 1 / 3 页"));
assert.equal(rowIDs()[0],205);
assert.equal(doc.querySelector("#sendRecordNextBtn").disabled,false);
doc.querySelector("#sendRecordNextBtn").click();
await waitFor(()=>doc.querySelector("#sendRecordPageLabel").textContent.includes("第 2 / 3 页"));
assert.equal(rowIDs()[0],105);
assert.equal(rowIDs().at(-1),6);
doc.querySelector("#sendRecordNextBtn").click();
await waitFor(()=>doc.querySelector("#sendRecordPageLabel").textContent.includes("第 3 / 3 页"));
assert.deepEqual(rowIDs(),[5,4,3,2,1]);
assert.equal(doc.querySelector("#sendRecordNextBtn").disabled,true);
assert.ok(doc.querySelector("#sendRecordTotal").textContent.includes("累计 205 次运行"));
doc.querySelector("#sendRecordPrevBtn").click();
await waitFor(()=>doc.querySelector("#sendRecordPageLabel").textContent.includes("第 2 / 3 页"));
assert.equal(rowIDs()[0],105);
doc.querySelector("#saveCurrentDimensionBtn").click();
await waitFor(()=>doc.querySelector("#sendRecordPageLabel").textContent.includes("第 1 / 3 页"));
holdNext=true;
doc.querySelector("#sendRecordNextBtn").click();
await waitFor(()=>Boolean(releaseHeld));
doc.querySelector("#saveCurrentDimensionBtn").click();
await waitFor(()=>doc.querySelector("#sendRecordNextBtn").disabled===false);
releaseHeld();
await new Promise(resolve=>setTimeout(resolve,30));
assert.equal(rowIDs()[0],205,"stale page must not overwrite a newer refresh");
assert.ok(doc.querySelector("#sendRecordPageLabel").textContent.includes("第 1 / 3 页"));
assert.equal(calls.filter(call=>call.method!=="GET").length,1,"history traversal must not send/refresh audience; only initial readonly precheck is POST");
dom.window.close();
console.log("admin-audience-send-history: PASS");
