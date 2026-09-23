import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
const base = process.env.AICRM_REFERRAL_BROWSER_URL;
const actors = JSON.parse(process.env.AICRM_REFERRAL_BROWSER_ACTORS || '[]');
assert.match(base || '', /^https:\/\/127\.0\.0\.1:/);
assert.ok(actors.length >= 3);
const sleep = ms => new Promise(r => setTimeout(r, ms));
const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'referral-browser-'));
const screenshots = process.env.AICRM_REFERRAL_SCREENSHOTS || path.join(profile, 'screenshots');
await fs.mkdir(screenshots, { recursive: true });
let chrome, socket;
const pending = new Map(); let sequence = 0;
try {
  const binary = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'chromium', 'google-chrome'].filter(Boolean).find(p => spawnSync(p, ['--version'], { stdio: 'ignore' }).status === 0);
  assert.ok(binary, 'Chromium is required');
  chrome = spawn(binary, ['--headless=new','--no-sandbox','--remote-debugging-port=0',`--user-data-dir=${profile}`,'--no-first-run','--ignore-certificate-errors','--disable-background-networking','about:blank'], { stdio: 'ignore' });
  let port;
  for (let i=0; i<300&&!port; i++) { try {port=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];}catch{} if(!port)await sleep(100); }
  assert.ok(port,'browser startup');
  const tab = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`,{method:'PUT'})).json();
  socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((r,j)=>{socket.addEventListener('open',r,{once:true});socket.addEventListener('error',j,{once:true});});
  const exceptions=[], networkRequests=[];
  socket.addEventListener('message',e=>{const m=JSON.parse(String(e.data));if(m.id){const p=pending.get(m.id);pending.delete(m.id);m.error?p?.reject(new Error(m.error.message)):p?.resolve(m.result);}else if(m.method==='Network.requestWillBeSent')networkRequests.push(m.params.request.url);else if(m.method==='Runtime.exceptionThrown')exceptions.push(m.params.exceptionDetails.text);});
  const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}));});
  const evaluate=async expression=>{const r=await call('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(r.exceptionDetails)throw new Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result?.value;};
  const wait=async expression=>{for(let i=0;i<180;i++){if(await evaluate(expression))return;await sleep(75);}throw new Error('UI condition not reached: '+expression+'; page='+await evaluate('location.pathname+location.search+" "+document.body.innerText'));};
  const cookie=async(name,value)=>call('Network.setCookie',{name,value,url:base,secure:true,sameSite:'Lax'});
  await call('Page.enable');await call('Runtime.enable');await call('Network.enable');
  const loginPage=await fetch(base+'/login');const html=await loginPage.text();const loginCSRF=/name="login_csrf_token" value="([^"]+)"/.exec(html)?.[1];assert.ok(loginCSRF);
  const login=await fetch(base+'/login',{method:'POST',redirect:'manual',headers:{'Content-Type':'application/x-www-form-urlencoded',Cookie:loginPage.headers.getSetCookie().map(c=>c.split(';')[0]).join('; ')},body:new URLSearchParams({username:'referral-admin',password:'referral-admin-password',login_csrf_token:loginCSRF})});assert.equal(login.status,303);
  const staffCookies=login.headers.getSetCookie().map(c=>c.split(';')[0]);
  for(const c of staffCookies){const n=c.indexOf('=');await cookie(c.slice(0,n),c.slice(n+1));}
  await call('Page.navigate',{url:base+'/admin/referral'});
  await wait("document.querySelector('#referral-admin-root') && document.body.textContent.includes('裂变活动')");
  const adminCSRF=await evaluate("decodeURIComponent(document.cookie.split('; ').find(v=>v.startsWith('aicrm_csrf='))?.split('=').slice(1).join('=')||'')");
  async function api(route, body, actor, method=body===undefined?'GET':'POST', key=crypto.randomUUID()) {
    const csrf=actor?'referral-fixture-csrf':adminCSRF;
    const cookies=actor?`aicrm_distribution_session=${actor.session}; aicrm_distribution_csrf=${csrf}`:staffCookies.join('; ');
    const response=await fetch(base+route,{method,headers:{Cookie:cookies,Origin:base,'Content-Type':'application/json','Idempotency-Key':key,'X-CSRF-Token':csrf,'X-Distribution-CSRF':csrf},body:body===undefined?undefined:JSON.stringify(body)});
    const value=await response.json();assert.ok(response.ok,`${method} ${route} -> ${response.status}: ${JSON.stringify(value)}`);return value;
  }
  const campaigns=[];
  for(let i=0;i<2;i++){
    const c=await api('/api/admin/referral/campaigns',{name:`同行邀请季 ${i+1}`,description:'邀请好友，和战队一起前进。',cover_url:'',reward_rules:'前 3 名可获得活动纪念礼物，由管理员核实后登记。',starts_at:new Date(Date.now()-3600000).toISOString(),ends_at:new Date(Date.now()+86400000).toISOString()});
    const team=await api(`/api/admin/referral/campaigns/${c.id}/teams`,{name:i?'向阳战队':'追光战队',logo_url:'',captain_customer_id:actors[i].id});
    await api(`/api/admin/referral/campaigns/${c.id}/state`,{expected_version:c.version,target:'active'});
    // Keep the first designated captain unjoined for the member-facing
    // journey below. A captain assignment is not participation: the browser
    // must show an explicit join action before it can issue an invitation.
    if(i===0) campaigns.push({c,team,link:null});
    else {
      await api(`/api/v1/referral/campaigns/${c.id}/participations`,{team_id:team.id},actors[i]);
      const link=await api(`/api/v1/referral/campaigns/${c.id}/invite`,{},actors[i]);
      campaigns.push({c,team,link});
    }
  }
  // Create more than one page of real, trusted-session participants without
  // manufacturing Referral facts. They join directly, so this does not change
  // the independent invitation-credit assertions below.
  for(const actor of actors.slice(3)) await api(`/api/v1/referral/campaigns/${campaigns[0].c.id}/participations`,{team_id:campaigns[0].team.id},actor);
  // A verified, assigned captain can inspect their empty invitation details
  // without a false 401, then must explicitly accept rules before the invite
  // panel becomes available. This is deliberately browser-driven rather than
  // only an API assertion.
  {
    const captain=actors[0], first=campaigns[0];
    await cookie('aicrm_distribution_session',captain.session);await cookie('aicrm_distribution_csrf','referral-fixture-csrf');
    await call('Emulation.setDeviceMetricsOverride',{width:375,height:850,deviceScaleFactor:1,mobile:true});
    await call('Page.navigate',{url:`${base}/referral?campaign=${first.c.id}`});
    await wait("document.querySelector('[data-testid=referral-join-captain-team]') && document.querySelector('[data-testid=referral-invite]')?.textContent.includes('加入战队并邀请')");
    await wait("[...document.querySelectorAll('button')].some(b=>b.textContent==='参加后查看')");
    const beforeDetail=networkRequests.length;
    await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='参加后查看').click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    assert.equal(networkRequests.slice(beforeDetail).some(url=>/\/invitations(?:\?|$)/.test(url)),false,'unjoined detail action must not request invitation details');
    await evaluate("[...document.querySelectorAll('dialog button')].find(b=>b.textContent==='暂不参加').click()");
    await wait("!document.querySelector('[data-testid=referral-accept-dialog]')");
    const unjoinedImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'captain-unjoined-375.png'),Buffer.from(unjoinedImage.data,'base64'));
    await evaluate("document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    await evaluate("document.querySelector('#referral-rule-check').click(); document.querySelector('[data-testid=referral-confirm-join]').click()");
    await wait("document.querySelector('[data-testid=referral-invite-dialog]') && document.querySelector('[data-testid=referral-invite]')?.textContent==='邀请好友'");
    const captainURL=await evaluate("document.querySelector('[data-testid=referral-invite-url]')?.value");
    assert.match(captainURL,/^https:\/\/127\.0\.0\.1:\d+\/referral\/invite\/rfi_[A-Za-z0-9_-]{43}$/);
    first.link={url:captainURL};
    const joinedImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'captain-joined-375.png'),Buffer.from(joinedImage.data,'base64'));
    await evaluate("document.querySelector('[data-testid=referral-invite-dialog]').close()");
  }
  for(let index=0;index<2;index++){
    await cookie('aicrm_distribution_session',actors[2].session);await cookie('aicrm_distribution_csrf','referral-fixture-csrf');
    await call('Emulation.setDeviceMetricsOverride',{width:index?430:375,height:850,deviceScaleFactor:1,mobile:true});
    await call('Page.navigate',{url:campaigns[index].link.url});
    await wait("[...document.querySelectorAll('button')].some(b=>b.textContent.includes('接受邀请'))");
    await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent.includes('接受邀请')).click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    await evaluate("document.querySelector('#referral-rule-check').click(); document.querySelector('[data-testid=referral-confirm-join]').click()");
    await wait("document.querySelector('[data-testid=referral-invite]')?.disabled === false && !document.querySelector('[data-testid=referral-accept-dialog]')");
    const pageImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`campaign-${index?430:375}.png`),Buffer.from(pageImage.data,'base64'));
  for(const period of ['day','total']) {
      await evaluate(`(()=>{const el=document.querySelector('[data-testid=referral-leaderboard-period]');el.value='${period}';el.dispatchEvent(new Event('change'));})()`);
      await sleep(200);
      await wait("document.querySelector('[data-testid=referral-leaderboard]')?.textContent.includes('1')");
      assert.equal(await evaluate("document.querySelector('[data-referral-message]')?.dataset.error === 'true'"),false,'period switch should not fail');
    }
    await evaluate("Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async value=>{window.__referralCopied=value;}}}); document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-copy-invite]')");
    await evaluate("document.querySelector('[data-testid=referral-copy-invite]').click()");
    await wait("typeof window.__referralCopied === 'string'");
    const copied = new URL(await evaluate('window.__referralCopied'));
    assert.equal(copied.origin,base);assert.match(copied.pathname,/^\/referral\/invite\/rfi_[A-Za-z0-9_-]{43}$/);
    assert.equal(await evaluate('document.documentElement.scrollWidth <= innerWidth'),true,'mobile does not horizontally overflow');
    const image=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`mobile-${index?430:375}.png`),Buffer.from(image.data,'base64'));
  }
  // Repeated acceptance preserves the first campaign and does not steal the latest relationship back.
  const first=campaigns[0], token=new URL(first.link.url).pathname.split('/').at(-1);
  await api(`/api/v1/referral/campaigns/${first.c.id}/participations`,{invitation_token:token},actors[2]);
  for(const {c} of campaigns){const board=await api(`/api/v1/referral/campaigns/${c.id}/leaderboard?kind=personal&period=total`,undefined,actors[2]);assert.equal(board.items.reduce((sum,x)=>sum+x.score,0),1,'one independent invitation credit per campaign');}
  const firstAdminView=await api(`/api/admin/referral/campaigns/${first.c.id}`);
  assert.equal(firstAdminView.team_summaries?.[0]?.captain_name,'队长阿青',`admin team summary must resolve its captain: ${JSON.stringify(firstAdminView.team_summaries)}`);
  await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
  await call('Page.navigate',{url:base+'/admin/referral'});
  // Navigation can return before body exists; wait for the loaded activity card.
  await wait(`location.pathname === '/admin/referral' && [...document.querySelectorAll('#referral-admin-root .referral-admin-campaign')].some(card=>card.textContent.includes(${JSON.stringify(first.c.name)}))`);
  assert.equal(await evaluate("document.querySelectorAll('main').length"),1,'one CRM main');
  const image=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'admin-1440.png'),Buffer.from(image.data,'base64'));
  // Open an activity before using its second-level operational controls.
  await evaluate(`(()=>{const card=[...document.querySelectorAll('.referral-admin-campaign')].find(v=>v.textContent.includes(${JSON.stringify(first.c.name)}));const button=card&&[...card.querySelectorAll('button')].find(b=>b.textContent==='查看活动详情');if(!button)throw new Error('first campaign detail action missing');button.click();})()`);
  await wait("document.body.textContent.includes('活动核心数据') && document.querySelector('[aria-busy=false]')");
  await wait("[...document.querySelectorAll('.referral-admin-table tbody tr')].length >= 50");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='加载更多')?.click()");
  await wait("[...document.querySelectorAll('.referral-admin-table tbody tr')].length > 50");
  const detailImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'admin-detail-1440.png'),Buffer.from(detailImage.data,'base64'));
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='战队').click()");
  await wait("document.querySelector('.referral-admin-table')?.textContent.includes('队长阿青')");
  const teamsImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'admin-teams-1440.png'),Buffer.from(teamsImage.data,'base64'));
  await evaluate("[...document.querySelectorAll('.referral-admin-table button')].find(b=>b.textContent==='查看队员').click()");
  await wait("document.querySelector('[aria-label=战队筛选]')?.value !== '0'");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='战队').click()");
  await wait("[...document.querySelectorAll('.referral-admin-table button')].some(b=>b.textContent==='队长参加入口')");
  await evaluate("[...document.querySelectorAll('.referral-admin-table button')].find(b=>b.textContent==='队长参加入口').click()");
  await wait("document.querySelector('dialog[data-shared-qr-dialog=true] input')?.value.includes('/referral?campaign=') && document.querySelector('[data-qr-payload]')");
  assert.equal(await evaluate("document.querySelector('dialog[data-shared-qr-dialog=true] input')?.value"),`${base}/referral?campaign=${first.c.id}`,'captain QR must use the same-origin campaign entry');
  const captainImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'admin-captain-entry-1440.png'),Buffer.from(captainImage.data,'base64'));
  await evaluate("document.querySelector('dialog[data-shared-qr-dialog=true]').close()");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='成员').click()");
  await wait("document.querySelector('[aria-label=战队筛选]') && document.querySelector('.referral-admin-table')");
  await evaluate("(()=>{const el=document.querySelector('[aria-label=战队筛选]');el.value=el.querySelector('option:not([value=\\\"0\\\"])')?.value||'0';el.dispatchEvent(new Event('change'));})()");
  await wait("[...document.querySelectorAll('.referral-admin-table button')].some(b=>b.textContent==='查看邀请对象')");
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='加载更多')?.click()");
  await wait("[...document.querySelectorAll('.referral-admin-table tbody tr')].some(row=>row.querySelector('td')?.textContent==='队长阿青' && [...row.querySelectorAll('button')].some(b=>b.textContent==='查看邀请对象'))");
  await evaluate("(()=>{const row=[...document.querySelectorAll('.referral-admin-table tbody tr')].find(row=>row.querySelector('td')?.textContent==='队长阿青');const button=row&&[...row.querySelectorAll('button')].find(b=>b.textContent==='查看邀请对象');if(!button)throw new Error('captain drilldown action missing');button.click();})()");
  await wait("document.body.textContent.includes('队长阿青带来的直接邀请') && document.body.textContent.includes('超长昵称的活动参与者')");
  const drilldownImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'admin-member-drilldown-1440.png'),Buffer.from(drilldownImage.data,'base64'));
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='导出当前筛选').click()");
  for(let i=0;i<80&&!networkRequests.some(url=>url.includes('/export?')&&url.includes('participation_id='));i++)await sleep(50);
  const drilldownExport=networkRequests.find(url=>url.includes('/export?')&&url.includes('participation_id='));
  assert.ok(drilldownExport,'drilldown export did not request a participation-scoped CSV');
  assert.equal(drilldownExport.includes('team_id='),false,'drilldown export must not retain a stale team filter');
  // Use staff controls against real HTTP/SQL: history lookup, reversal, reward review.
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='归属历史').click()");
  await wait("document.querySelector('.referral-admin-picker input')");
  await evaluate("document.querySelector('.referral-admin-picker input').value='超长昵称'; [...document.querySelectorAll('button')].find(b=>b.textContent==='搜索客户').click()");
  await wait("document.querySelector('.referral-admin-picker__option')");
  await evaluate("document.querySelector('.referral-admin-picker__option').click(); [...document.querySelectorAll('button')].find(b=>b.textContent==='查看归属历史').click()");
  await wait("document.querySelector('.referral-admin-table')?.textContent.includes('队长阿青') && document.querySelector('.referral-admin-table')?.textContent.includes('队长小夏')");
  const referrals=await api(`/api/admin/referral/referrals?campaign_id=${first.c.id}`);
  const credited=referrals.items.find(r=>r.score_event_id>0);assert.ok(credited);
  await api('/api/admin/referral/rewards',{campaign_id:first.c.id,customer_id:actors[0].id,score_event_id:credited.score_event_id,period:'total',reward:'活动纪念礼物',evidence_reference:'fixture:manual-award'});
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='邀请明细').click()");
  await wait("[...document.querySelectorAll('.referral-admin-table button')].some(b=>b.textContent==='撤销')");
  await evaluate("[...document.querySelectorAll('.referral-admin-table button')].find(b=>b.textContent==='撤销').click()");
  await wait("document.querySelector('[data-testid=referral-admin-dialog] input')");
  await evaluate("document.querySelector('[data-testid=referral-admin-dialog] input').value='测试核实无效邀请'; [...document.querySelectorAll('dialog button')].find(b=>b.textContent==='确认撤销').click()");
  await wait("!document.querySelector('dialog') && document.querySelector('.referral-admin-table')?.textContent.includes('已撤销')");
  const corrected=await api(`/api/v1/referral/campaigns/${first.c.id}/leaderboard?kind=personal&period=total`,undefined,actors[2]);
  assert.equal(corrected.items.reduce((sum,x)=>sum+x.score,0),0,'reversal corrects real leaderboard');
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='人工发奖').click()");
  await wait("document.querySelector('.referral-admin-table')?.textContent.includes('待核查')");
  assert.deepEqual(exceptions,[],'no uncaught browser errors');
  console.log('referral_chromium: PASS');
} finally {
  socket?.close();chrome?.kill('SIGTERM');await sleep(500);
  if(chrome&&chrome.exitCode===null){chrome.kill('SIGKILL');await sleep(300);}
  if(!process.env.AICRM_REFERRAL_SCREENSHOTS)await fs.rm(profile,{recursive:true,force:true,maxRetries:5,retryDelay:150});
}
