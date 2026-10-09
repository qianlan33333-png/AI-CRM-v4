import assert from "node:assert/strict";
import fs from "node:fs";
import vm from "node:vm";
import { test } from "node:test";

const source = fs.readFileSync(new URL("./static/sidebar_workbench/sidebar_ai_operations.js", import.meta.url), "utf8");
const runtime = { module: { exports: {} }, AbortController, setTimeout, clearTimeout };
vm.runInNewContext(source, runtime);
const { createSession } = runtime.SidebarAIOperations;
const frozen = () => ({ state: "ready", snapshot_token: "frozen", expires_at: new Date(Date.now() + 300000).toISOString(), generation_ready: true });
const suggestion = { suggestion: "参考话术", snapshot_token: "frozen", auto_sent: false };
const deferred = () => { let resolve; const promise = new Promise(r => { resolve = r; }); return { promise, resolve }; };
const tick = () => new Promise(r => setImmediate(r));
function make(request, extra = {}) { return createSession({ request, readURL: "/read", generateURL: "/generate", friendly: e => e.message, checkContext: async () => true, ...extra }); }

test("refresh only reads; explicit confirmation then one model request; duplicate click suppressed", async () => {
  const pending = deferred(), calls = [];
  const s = make(async (url, opts) => { calls.push({url, opts}); return url === "/read" ? frozen() : pending.promise; });
  await s.refresh(); await s.generate(); assert.equal(calls.length, 1);
  s.confirm(true); const first = s.generate(); await tick(); await s.generate(); assert.equal(calls.length, 2);
  assert.deepEqual(JSON.parse(calls[1].opts.body), { snapshot_token: "frozen", confirmed: true });
  pending.resolve(suggestion); await first; assert.equal(s.state().result, "参考话术"); s.dispose();
});
test("refresh invalidates confirmation and ignores an old model response even when abort is ignored", async () => {
  const pending = deferred(); const s = make(async url => url === "/read" ? frozen() : pending.promise);
  await s.refresh(); s.confirm(true); const old = s.generate(); await tick(); await s.refresh();
  pending.resolve(suggestion); await old; assert.equal(s.state().result, ""); assert.equal(s.state().confirmed, false); s.dispose();
});
test("newer read wins out of order; disposed customer cannot receive results", async () => {
  const reads = [deferred(), deferred()]; let n = 0; const s = make(() => reads[n++].promise);
  const first = s.refresh(); await tick(); const second = s.refresh(); await tick();
  reads[1].resolve({...frozen(), snapshot_token:"new"}); await second; reads[0].resolve(frozen()); await first;
  assert.equal(s.state().snapshot.snapshot_token, "new"); s.dispose(); assert.equal(s.state().snapshot, null);
});
test("customer identity change blocks generation and clears cached content", async () => {
  let matches = true, calls = 0; const s = make(async () => { calls++; return frozen(); }, { checkContext: async () => matches });
  await s.refresh(); s.confirm(true); matches = false; await s.generate(); assert.equal(calls, 1); assert.equal(s.state().snapshot, null); s.dispose();
});
test("contact change during warehouse read discards its projection", async () => {
  const pending=deferred();let matches=true;
  const s=make(()=>pending.promise,{checkContext:async()=>matches});
  const read=s.refresh();await tick();matches=false;pending.resolve(frozen());await read;
  assert.equal(s.state().snapshot,null);assert.equal(s.state().confirmed,false);s.dispose();
});
test("identity change during request discards response", async () => {
  const pending = deferred(); let matches = true; const s = make(async url => url === "/read" ? frozen() : pending.promise, { checkContext: async () => matches });
  await s.refresh(); s.confirm(true); const old = s.generate(); await tick(); matches=false; pending.resolve(suggestion); await old; assert.equal(s.state().result, ""); s.dispose();
});
test("expired/disabled context cannot be confirmed or generated", async () => {
  for (const data of [{...frozen(),expires_at:"2020-01-01T00:00:00Z"},{...frozen(),generation_ready:false},{state:"unconfigured"}]) {
    let calls=0; const s=make(async()=>{calls++;return data;});await s.refresh();s.confirm(true);await s.generate();assert.equal(calls,1);assert.equal(s.canGenerate(),false);s.dispose();
  }
});
test("response bound to another snapshot or auto-send result is rejected", async () => {
  for (const wrong of [{...suggestion,snapshot_token:"other"},{...suggestion,auto_sent:true}]) {
    const s=make(async url=>url==="/read"?frozen():wrong);await s.refresh();s.confirm(true);await s.generate();assert.equal(s.state().result,"");assert.equal(s.state().confirmed,false);s.dispose();
  }
});
test("failure clears approval; refresh still recovers", async () => {
  const s=make(async url=>{if(url==="/read")return frozen();throw new Error("生成结果未确认");});await s.refresh();s.confirm(true);await s.generate();assert.equal(s.state().busy,false);assert.equal(s.state().result,"");assert.equal(s.state().confirmed,false);await s.refresh();assert.equal(s.state().snapshot.state,"ready");s.dispose();
});
test("bounded client deadline aborts a hanging generation and permits a fresh read", async () => {
  const timers = new Map(); let sequence=0, signal;
  const bounded = { module:{exports:{}}, AbortController, setTimeout(fn){const id=++sequence;timers.set(id,fn);return id;}, clearTimeout(id){timers.delete(id);} };
  vm.runInNewContext(source, bounded);
  const s=bounded.SidebarAIOperations.createSession({readURL:"/read",generateURL:"/generate",checkContext:async()=>true,friendly:e=>e.message,request:async(url,options)=>{if(url==="/read")return frozen();signal=options.signal;return new Promise(()=>{});}});
  await s.refresh();s.confirm(true);const pending=s.generate();await tick();
  assert.equal(timers.size,1);for(const fn of timers.values())fn();await pending;
  assert.equal(signal.aborted,true);assert.equal(s.state().busy,false);assert.equal(s.state().confirmed,false);assert.equal(s.state().result,"");assert.match(s.state().snapshot.message,/结果未确认/);
  await s.refresh();assert.equal(s.state().snapshot.state,"ready");s.dispose();
});
