import assert from 'node:assert/strict';
import vm from 'node:vm';
import { JSDOM } from 'jsdom';

const origin = process.argv[2];
const promotion = 'dpc_' + 'a'.repeat(43);
const pagePath = '/s/term-31?promotion_context=' + promotion;
const page = await (await fetch(origin + pagePath)).text();
const tick = () => new Promise(resolve => setTimeout(resolve, 10));
const activeState = days => ({ok:true, available:true, authenticated:true, entitlement:{status:'active',remaining_days:days,end_at:'2027-07-09T00:00:00Z'}, cta_text:'立即续费', checkout_url:'/s/term-31/pay?promotion_context='+promotion});
const anonymousState = {ok:true,available:true,authenticated:false,entitlement:{status:'none'},cta_text:'立即报名'};

async function open({wechat=true, attempted=false, fetchState}={}) {
  const dom = new JSDOM(page, {url:origin+pagePath, runScripts:'outside-only', pretendToBeVisual:true});
  const events = new Map(), navigations=[], reads=[];
  const location = {pathname:'/s/term-31',search:'?promotion_context='+promotion,assign:url=>navigations.push(url)};
  const storage = dom.window.sessionStorage;
  if (attempted) storage.setItem('aicrm.oauth.auto:/s/term-31','1');
  const context = {
    window:{location,addEventListener:(name,fn)=>events.set(name,fn)},
    document:dom.window.document,navigator:{userAgent:wechat?'MicroMessenger':'Safari'},
    sessionStorage:storage,Intl,AbortController,setTimeout,clearTimeout,
    fetch:async (url,init)=>{
      reads.push({url,init});
      assert.equal(init.credentials,'same-origin');
      assert.equal(init.method,undefined,'detail cannot create an order');
      return fetchState ? fetchState(url,init) : fetch(origin+url,{...init,headers:{Cookie:'aicrm_payment_session=service-period-trusted'}});
    }
  };
  const script=Array.from(dom.window.document.scripts).find(script=>script.textContent.includes('const initialState'));
  vm.runInNewContext(script.textContent,context);
  const button=dom.window.document.getElementById('servicePeriodPayButton');
  const card=dom.window.document.getElementById('servicePeriodStateCard');
  for(let i=0;i<100 && card.getAttribute('aria-busy')==='true';i++) await tick();
  return {dom,events,navigations,reads,storage,button,card};
}

// Full rendered page reads the real Handler with a trusted session, and never
// asks the payment creation-readiness endpoint to display existing rights.
const trusted=await open();
assert.match(trusted.card.textContent,/286 天/);
assert.equal(trusted.button.textContent,'立即续费');
assert.equal(trusted.navigations.length,0);
assert.equal(trusted.reads.length,1);
assert.match(trusted.reads[0].url,/promotion_context=dpc_/);
trusted.dom.window.close();

const auto=await open({fetchState:async()=>Response.json(anonymousState)});
assert.equal(auto.navigations.length,1,'missing identity starts OAuth on detail entry');
assert.equal(new URL(auto.navigations[0],origin).searchParams.get('return_url'),pagePath,'OAuth returns to this detail with attribution');
assert.equal(auto.storage.getItem('aicrm.oauth.auto:/s/term-31'),'1');
assert.doesNotMatch(auto.card.textContent,/剩余有效期/);
auto.dom.window.close();

const refused=await open({attempted:true,fetchState:async()=>Response.json(anonymousState)});
assert.equal(refused.navigations.length,0,'failure return must not loop');
assert.equal(refused.button.textContent,'重新授权');
refused.button.click();
assert.equal(refused.navigations.length,1,'manual retry starts a new OAuth URL, never a callback replay');
assert.match(refused.navigations[0],/^\/api\/h5\/wechat-pay\/oauth\/start\?/);
refused.dom.window.close();

let payload=activeState(286), fail=true;
const retry=await open({fetchState:async()=>fail?new Response('unavailable',{status:503}):Response.json(payload)});
assert.equal(retry.button.textContent,'重新查询');
assert.doesNotMatch(retry.card.textContent,/剩余有效期/);
fail=false;retry.button.click();await tick();
assert.match(retry.card.textContent,/286 天/);
payload=activeState(376);
retry.events.get('pageshow')({persisted:true});await tick();
assert.match(retry.card.textContent,/376 天/,'BFCache return refreshes renewed entitlement without reopening');
payload={...activeState(0),entitlement:{status:'expired',end_at:'2026-09-25T00:00:00Z'},cta_text:'重新开通'};
retry.events.get('pageshow')({persisted:true});await tick();
assert.equal(retry.button.textContent,'重新开通');
assert.match(retry.card.textContent,/已过期/);
payload={...anonymousState,authenticated:true};
retry.events.get('pageshow')({persisted:true});await tick();
assert.equal(retry.button.textContent,'立即报名','verified identity without entitlement is distinct');
retry.dom.window.close();

const safari=await open({wechat:false,fetchState:async()=>Response.json(anonymousState)});
assert.equal(safari.navigations.length,0);
assert.equal(safari.button.disabled,true);
assert.match(safari.card.textContent,/请复制当前链接到微信/);
safari.dom.window.close();

let releaseRead,readCount=0;
const stale=await open({fetchState:async()=>++readCount===1 ? new Promise(resolve=>{releaseRead=resolve;}) : Response.json(activeState(376))});
stale.events.get('pagehide')();
stale.events.get('pageshow')({persisted:true});await tick();
releaseRead(Response.json(anonymousState));await tick();
assert.match(stale.card.textContent,/376 天/);
assert.equal(stale.navigations.length,0,'stale anonymous response cannot reauthorize the new state');
stale.dom.window.close();
console.log('service_period_identity: PASS');
