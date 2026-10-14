import { resolveChromiumBinary } from "../../internal/webshell/chromium_binary.mjs";
import { createDownloadDirectory } from "../../internal/webshell/test_referral_downloads.mjs";
import { createCSVDownloadTracker } from "../../internal/webshell/test_referral_csv_download.mjs";
import { openBrowserDownloadSession } from "../../internal/webshell/test_referral_browser_cdp.mjs";
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';

process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
const base = process.env.AICRM_REFERRAL_BROWSER_URL;
const actors = JSON.parse(process.env.AICRM_REFERRAL_BROWSER_ACTORS || '[]');
assert.match(base || '', /^https:\/\/127\.0\.0\.1:/);
assert.ok(actors.length >= 3);
const sleep = ms => new Promise(r => setTimeout(r, ms));
const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'referral-browser-'));
const downloads = await createDownloadDirectory();
const csvDownloads = createCSVDownloadTracker(downloads);
const screenshots = process.env.AICRM_REFERRAL_SCREENSHOTS || path.join(profile, 'screenshots');
await fs.mkdir(screenshots, { recursive: true });
let chrome, socket, browserDownloads;
const pending = new Map(); let sequence = 0;
try {
  const binary = resolveChromiumBinary();
  assert.ok(binary, 'Chromium is required');
  chrome = spawn(binary, ['--headless=new','--no-sandbox','--remote-debugging-port=0',`--user-data-dir=${profile}`,'--no-first-run','--ignore-certificate-errors','--disable-background-networking','about:blank'], { stdio: 'ignore' });
  let port;
  for (let i=0; i<300&&!port; i++) { try {port=(await fs.readFile(path.join(profile,'DevToolsActivePort'),'utf8')).split('\n')[0];}catch{} if(!port)await sleep(100); }
  assert.ok(port,'browser startup');
  browserDownloads = await openBrowserDownloadSession(`http://127.0.0.1:${port}`, csvDownloads);
  const tab = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`,{method:'PUT'})).json();
  socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((r,j)=>{socket.addEventListener('open',r,{once:true});socket.addEventListener('error',j,{once:true});});
  const exceptions=[], networkRequests=[], networkMethods=[], networkResponses=[];
  socket.addEventListener('message',e=>{const m=JSON.parse(String(e.data));if(m.id){const p=pending.get(m.id);pending.delete(m.id);m.error?p?.reject(new Error(m.error.message)):p?.resolve(m.result);}else if(m.method==='Network.requestWillBeSent'){networkRequests.push(m.params.request.url);networkMethods.push({url:m.params.request.url,method:m.params.request.method});}else if(m.method==='Network.responseReceived')networkResponses.push({id:m.params.requestId,url:m.params.response.url});else if(m.method==='Runtime.exceptionThrown')exceptions.push(m.params.exceptionDetails.text);});
  const call=(method,params={})=>new Promise((resolve,reject)=>{const id=++sequence;pending.set(id,{resolve,reject});socket.send(JSON.stringify({id,method,params}));});
  const evaluate=async expression=>{const r=await call('Runtime.evaluate',{expression,returnByValue:true,awaitPromise:true});if(r.exceptionDetails)throw new Error(r.exceptionDetails.exception?.description||r.exceptionDetails.text);return r.result?.value;};
  const wait=async expression=>{for(let i=0;i<180;i++){if(await evaluate(expression))return;await sleep(75);}throw new Error('UI condition not reached: '+expression+'; page='+await evaluate('location.pathname+location.search+" "+document.body.innerText'));};
  const cookie=async(name,value)=>call('Network.setCookie',{name,value,url:base,secure:true,sameSite:'Lax'});
  await call('Page.enable');await call('Runtime.enable');await call('Network.enable');
  // A temporary profile does not isolate Chrome's download destination.
  // Fail before navigation if downloads cannot be confined to this test run.
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
  const missingNickname=await api(`/api/v1/referral/campaigns/${campaigns[0].c.id}/invite`,{},actors[3]);assert.equal('poster_nickname' in missingNickname,false,'missing trusted nickname never falls back to directory name');
  // A verified, assigned captain can inspect their empty invitation details
  // without a false 401, then must explicitly accept rules before the invite
  // panel becomes available. This is deliberately browser-driven rather than
  // only an API assertion.
  {
    const captain=actors[0], first=campaigns[0];
    await cookie('aicrm_distribution_session',captain.session);await cookie('aicrm_distribution_csrf','referral-fixture-csrf');
    await call('Emulation.setDeviceMetricsOverride',{width:375,height:850,deviceScaleFactor:1,mobile:true});
    await call('Page.navigate',{url:`${base}/referral?campaign=${first.c.id}`});
    await wait("document.querySelector('[data-testid=referral-invite]')?.textContent.includes('加入战队并邀请')");
    await evaluate("document.querySelector('[data-testid=referral-activity-info]').click()");
    await wait("document.body.textContent.includes('参加活动后可查看邀请记录')");
    const beforeDetail=networkRequests.length;
    assert.equal(await evaluate("!!document.querySelector('.referral-home')"),false,'removed home card stays absent');
    await evaluate("document.querySelector('[data-testid=referral-activity-info]').click();document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    assert.equal(networkRequests.slice(beforeDetail).some(url=>/\/invitations(?:\?|$)/.test(url)),false,'unjoined detail action must not request invitation details');
    await evaluate("[...document.querySelectorAll('dialog button')].find(b=>b.textContent==='暂不参加').click()");
    await wait("!document.querySelector('[data-testid=referral-accept-dialog]')");
    const unjoinedImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'captain-unjoined-375.png'),Buffer.from(unjoinedImage.data,'base64'));
    await evaluate("document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    await evaluate("document.querySelector('#referral-rule-check').click(); document.querySelector('[data-testid=referral-confirm-join]').click()");
    await wait("document.querySelector('[data-testid=referral-invite-dialog]') && document.querySelector('[data-testid=referral-invite]')?.textContent==='生成溯源码'");
    const captainURL=(await api(`/api/v1/referral/campaigns/${first.c.id}/invite`,{},captain)).url;
    assert.match(captainURL,/^https:\/\/127\.0\.0\.1:\d+\/referral\/invite\/rfi_[A-Za-z0-9_-]{43}$/);
    first.link={url:captainURL};
    const joinedImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'captain-joined-375.png'),Buffer.from(joinedImage.data,'base64'));
    await evaluate("document.querySelector('[data-testid=referral-invite-dialog]').close()");
  }
  // Publish artwork through the real Media and Referral owners in the
  // isolated database. All three slots intentionally use one fixture image.
  const form = new FormData();
  form.append('name', '邀请海报测试图片');
  const uploadBytes=await fs.readFile(process.env.AICRM_REFERRAL_POSTER_PATH);
  assert.ok(uploadBytes.byteLength>0&&uploadBytes.byteLength<=1048576,'actual manual-upload PNG must fit 1MiB policy');
  form.append('image', new Blob([uploadBytes], { type: 'image/png' }), 'poster.png');
  const upload = await fetch(base+'/api/admin/image-library/upload', { method: 'POST', headers: { Cookie: staffCookies.join('; '), Origin: base, 'X-CSRF-Token': adminCSRF, 'Idempotency-Key': 'referral-poster-browser-upload' }, body: form });
  const uploaded = await upload.json();
  assert.ok(upload.ok && uploaded.item?.id, `poster upload: ${JSON.stringify(uploaded)}`);
  for(let i=0;i<2;i++){const d=await api(`/api/v1/referral/campaigns/${campaigns[i].c.id}/invite`,{},actors[i]);assert.equal(d.poster_nickname,actors[i].poster_nickname);assert.notEqual(d.poster_nickname,actors[i].name);}
  const shareTexts = ['  一起参加 🎉\n第二行保持空格  ', '<b>第二张纯文本</b>\n🙂', ''];
  for(const {c} of campaigns) {
    const current = await api(`/api/admin/referral/campaigns/${c.id}`);
    await api(`/api/admin/referral/campaigns/${c.id}/posters`, { expected_version: current.version, posters: shareTexts.map((share_text,i)=>({ image_id: uploaded.item.id, description: `海报${i+1}`, share_text, ...(i===0?{qr_layout:{x:830,y:1952,size:330}}:{}) })) }, undefined, 'PUT');
    const readback = await api(`/api/admin/referral/campaigns/${c.id}`);
    assert.deepEqual(readback.posters.map(p=>p.share_text), shareTexts, 'published text readback');
    assert.deepEqual(readback.posters.map(p=>p.qr_layout),[{x:830,y:1952,size:330},{x:810,y:1932,size:370},{x:810,y:1932,size:370}],'explicit legacy layout survives new defaults');
    assert.ok(readback.posters.every(p=>p.image_width===1250&&p.image_height===2426));
    const imageURL=readback.posters[0].image_url;
    assert.match(imageURL,/\?v=[a-f0-9]{64}$/);
    const background=await fetch(base+imageURL);assert.equal(background.status,200);assert.equal(background.headers.get('content-type'),'image/jpeg');
    assert.ok((await background.arrayBuffer()).byteLength<uploadBytes.byteLength,'JPEG background reduces bytes');
    const conditional=await fetch(base+imageURL,{headers:{'If-None-Match':background.headers.get('etag')}});assert.equal(conditional.status,304);assert.equal((await conditional.arrayBuffer()).byteLength,0);
    assert.equal((await fetch(base+imageURL.replace(/v=[a-f0-9]{64}/,'v='+'0'.repeat(64)))).status,409);
    assert.equal((await fetch(base+imageURL.split('?')[0])).status,200,'legacy entry remains readable');
  }
  const draft=await api('/api/admin/referral/campaigns',{name:'私有草稿海报',starts_at:new Date(Date.now()+3600000).toISOString(),ends_at:new Date(Date.now()+86400000).toISOString()});
  const draftPosters=await api(`/api/admin/referral/campaigns/${draft.id}/posters`,{expected_version:draft.version,posters:[{image_id:uploaded.item.id}]},undefined,'PUT');
  assert.equal((await fetch(base+draftPosters.posters[0].image_url,{headers:{'If-None-Match':'*'}})).status,404,'draft never returns bytes or 304');
  await call('Browser.grantPermissions', { origin: base, permissions: ['clipboardReadWrite', 'clipboardSanitizedWrite'] });
  for(let index=0;index<2;index++){
    await cookie('aicrm_distribution_session',actors[2].session);await cookie('aicrm_distribution_csrf','referral-fixture-csrf');
    await call('Emulation.setDeviceMetricsOverride',{width:index?430:375,height:850,deviceScaleFactor:1,mobile:true});
    await call('Page.navigate',{url:campaigns[index].link.url});
    await wait("document.querySelector('[data-testid=referral-activity-info]')");
    await evaluate("document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-confirm-join]')");
    await evaluate("document.querySelector('#referral-rule-check').click(); document.querySelector('[data-testid=referral-confirm-join]').click()");
    await wait("!document.querySelector('[data-testid=referral-accept-dialog]')");
    await wait("document.querySelector('[data-testid=referral-invite-dialog][open]')");
    await evaluate("document.querySelector('[data-testid=referral-invite-dialog]').close()");
    await wait("!document.querySelector('[data-testid=referral-invite-dialog]')");
    await evaluate("document.querySelector('[data-testid=referral-activity-info]').click()");
    await wait("document.querySelector('[data-testid=referral-invitation-count]')?.textContent.includes('共 ')");
    assert.equal(await evaluate("!!document.querySelector('.referral-home') || !!document.querySelector('.referral-info-title h1')"),false);
    await evaluate("document.querySelector('[data-testid=referral-activity-info]').click()");
    await wait("document.querySelector('[data-testid=referral-invite]')?.textContent==='生成溯源码'");
    const pageImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`campaign-${index?430:375}.png`),Buffer.from(pageImage.data,'base64'));
  for(const period of ['day','total']) {
      await evaluate(`(()=>{const el=document.querySelector('[data-testid=referral-leaderboard-period]');el.value='${period}';el.dispatchEvent(new Event('change'));})()`);
      await sleep(200);
      await wait("document.querySelector('[data-testid=referral-leaderboard]')?.textContent.includes('1')");
      assert.equal(await evaluate("document.querySelector('[data-referral-message]')?.dataset.error === 'true'"),false,'period switch should not fail');
    }
    const beforeInvites=networkMethods.filter(r=>r.method==='POST'&&r.url===`${base}/api/v1/referral/campaigns/${campaigns[index].c.id}/invite`).length;
    await evaluate("(()=>{const b=document.querySelector('[data-testid=referral-invite]');b.click();b.click();b.click();})()");
    await wait("document.querySelector('[data-testid=referral-invite-dialog]')");
    assert.equal(await evaluate("document.querySelector('[data-testid=referral-invite-url]')===null"),true,'poster panel has no editable link action');
    await wait("document.querySelector('[data-testid=referral-poster-image]')?.naturalWidth===1250");
    assert.equal(await evaluate("document.querySelector('[data-testid=referral-poster-share-text]').textContent"),shareTexts[0]);
    const composedPNG = await evaluate("document.querySelector('[data-testid=referral-poster-image]').src");
    assert.match(composedPNG,/^data:image\/jpeg;base64,/);
    assert.equal(networkMethods.filter(r=>r.method==='POST'&&r.url===`${base}/api/v1/referral/campaigns/${campaigns[index].c.id}/invite`).length-beforeInvites,1,'pending triple click issues one invite');
    const inviteResponse = networkResponses.findLast(response=>response.url===`${base}/api/v1/referral/campaigns/${campaigns[index].c.id}/invite`);
    assert.ok(inviteResponse, 'personal invitation comes from authenticated HTTP');
    const invitationURL = JSON.parse((await call('Network.getResponseBody', {requestId:inviteResponse.id})).body).url;
    const personalPreview = await api(`/api/v1/referral/invitations/${new URL(invitationURL).pathname.split('/').at(-1)}`,undefined,actors[2]);
    assert.equal(personalPreview.inviter_display_name,actors[2].name,'personal artwork must use the current session actor');
    await fs.writeFile(path.join(screenshots,`composed-${index?430:375}.jpg`),Buffer.from(composedPNG.split(',')[1],'base64'));
    await fs.writeFile(path.join(screenshots,`composed-${index?430:375}.json`),JSON.stringify({expected_url:invitationURL}));
    const compareArtwork = async layout => {
      const differences = await evaluate(`(async()=>{const current=document.querySelector('[data-testid=referral-poster-image]');const source=new Image();source.src=${JSON.stringify(base+`/api/v1/referral/campaigns/${campaigns[index].c.id}/posters/1`)};await source.decode();const canvas=document.createElement('canvas');canvas.width=1250;canvas.height=2426;const ctx=canvas.getContext('2d',{willReadFrequently:true});ctx.drawImage(source,0,0);const before=ctx.getImageData(0,0,1250,2426).data;ctx.clearRect(0,0,1250,2426);ctx.drawImage(current,0,0);const after=ctx.getImageData(0,0,1250,2426).data;const box=${JSON.stringify(layout)};let squares=0,samples=0,changedInside=0;for(let y=0;y<2426;y++)for(let x=0;x<1250;x++){const i=(y*1250+x)*4;if(x>=902&&y>=40&&y<90)continue;if(x>=box.x&&x<box.x+box.size&&y>=box.y&&y<box.y+box.size){if(before[i]!==after[i]||before[i+1]!==after[i+1]||before[i+2]!==after[i+2])changedInside++;continue;}if(x>=box.x-8&&x<box.x+box.size+8&&y>=box.y-8&&y<box.y+box.size+8)continue;for(let c=0;c<3;c++){const d=before[i+c]-after[i+c];squares+=d*d;samples++;}}return{rmse:Math.sqrt(squares/samples),changedInside};})()`);
      // JPEG is lossy. Exclude its 8px QR boundary blocks, then require low
      // background error rather than the old PNG-only byte/pixel equality.
      assert.ok(differences.rmse<=8,`JPEG artwork error ${differences.rmse}`);
      assert.ok(differences.changedInside>0,'QR replaces source pixels');
      return differences;
    };
    const fixedPixels=await compareArtwork({x:830,y:1952,size:330});
    const posterImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`poster-${index?430:375}.png`),Buffer.from(posterImage.data,'base64'));
    const clickPoint = await evaluate("(()=>{const button=document.querySelector('[data-testid=referral-copy-share-text]');button.scrollIntoView({block:'center'});const r=button.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()");
    await call('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...clickPoint});
    await call('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...clickPoint});
    await wait("document.querySelector('[data-testid=referral-poster-copy-feedback]').textContent==='话术已复制'");
    assert.equal(await evaluate("navigator.clipboard.readText()"), shareTexts[0], 'clipboard retains exact newlines/emoji/spaces');
    await evaluate("document.querySelector('[data-testid=referral-poster-next]').click()");
    await wait("document.querySelector('[data-testid=referral-poster-image]')?.alt.startsWith('海报2')");
    assert.equal(await evaluate("document.querySelector('[data-testid=referral-poster-share-text]').textContent"),shareTexts[1]);
    assert.equal(await evaluate("document.querySelector('.referral-poster-share-text b')===null"),true,'copy is plain text');
    const swipePoint = await evaluate("(()=>{const preview=document.querySelector('[data-testid=referral-poster-preview]');preview.scrollIntoView({block:'center'});const r=preview.getBoundingClientRect();return{x:r.x+r.width*0.8,y:Math.max(r.y+20,Math.min(innerHeight-20,r.y+r.height/2))}})()");
    await call('Input.dispatchTouchEvent',{type:'touchStart',touchPoints:[swipePoint]});
    await call('Input.dispatchTouchEvent',{type:'touchMove',touchPoints:[{x:swipePoint.x-120,y:swipePoint.y+2}]});
    await call('Input.dispatchTouchEvent',{type:'touchEnd',touchPoints:[]});
    await wait("document.querySelector('.referral-poster-share').hidden");
    assert.equal(await evaluate("document.querySelector('[data-testid=referral-poster-next]').disabled"),true,'third poster does not wrap');

    await evaluate("document.querySelector('[data-testid=referral-invite-dialog]').close()");
    await wait("!document.querySelector('[data-testid=referral-invite-dialog]')");
    // Runtime metadata update, same server and same loaded page. No rebuild/reload.
    const currentConfig=await api(`/api/admin/referral/campaigns/${campaigns[index].c.id}`);
    const adjusted={x:810,y:1932,size:370};
    const updateKey=crypto.randomUUID();
    const updateBody={expected_version:currentConfig.version,posters:[{slot:1,...adjusted}]};
    const updated=await api(`/api/admin/referral/campaigns/${campaigns[index].c.id}/poster-layouts`,updateBody,undefined,'PUT',updateKey);
    const replay=await api(`/api/admin/referral/campaigns/${campaigns[index].c.id}/poster-layouts`,updateBody,undefined,'PUT',updateKey);
    assert.equal(replay.version,updated.version,'retry does not increment version');
    assert.deepEqual(updated.posters.map(p=>p.share_text),shareTexts,'layout API preserves paired copy');
    const reread=await api(`/api/admin/referral/campaigns/${campaigns[index].c.id}`);
    assert.equal(reread.version,updated.version);
    assert.deepEqual(reread.posters[0].qr_layout,adjusted);
    assert.deepEqual(reread.posters[1].qr_layout,{x:810,y:1932,size:370},'partial update preserves new default of other posters');
    await evaluate("document.querySelector('[data-testid=referral-invite]').click()");
    await wait("document.querySelector('[data-testid=referral-poster-image]')?.naturalWidth===1250");
    assert.equal(await evaluate("document.querySelector('[data-testid=referral-poster-share-text]').textContent"),shareTexts[0]);
    const adjustedPixels=await compareArtwork(adjusted);
    const adjustedPNG=await evaluate("document.querySelector('[data-testid=referral-poster-image]').src");
    assert.notEqual(adjustedPNG,composedPNG,'reopen uses changed runtime coordinates');
    const freshInviteResponse=networkResponses.findLast(response=>response.url===`${base}/api/v1/referral/campaigns/${campaigns[index].c.id}/invite`);
    const freshBody=JSON.parse((await call('Network.getResponseBody',{requestId:freshInviteResponse.id})).body);assert.equal(freshBody.poster_nickname,actors[2].poster_nickname);assert.deepEqual(Object.keys(freshBody).sort(),['expires_at','poster_nickname','url']);const freshURL=freshBody.url;
    await fs.writeFile(path.join(screenshots,`adjusted-${index?430:375}.jpg`),Buffer.from(adjustedPNG.split(',')[1],'base64'));
    await fs.writeFile(path.join(screenshots,`adjusted-${index?430:375}.json`),JSON.stringify({expected_url:freshURL,qr_layout:adjusted}));
    const traceImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`trace-poster-${index?430:375}.png`),Buffer.from(traceImage.data,'base64'));
    const widthCheck=await evaluate("(()=>{const image=document.querySelector('[data-testid=referral-poster-image]');return{image:image.getBoundingClientRect().width,preview:image.parentElement.getBoundingClientRect().width,overflow:document.documentElement.scrollWidth>innerWidth}})()");assert.ok(Math.abs(widthCheck.image-widthCheck.preview)<1,'poster fills preview width');assert.equal(widthCheck.overflow,false);
    await fs.writeFile(path.join(screenshots,`pixels-${index?430:375}.json`),JSON.stringify({fixed:fixedPixels,adjusted:adjustedPixels}));
    await evaluate("document.querySelector('[data-testid=referral-invite-dialog]').close()");
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
  // Operator entry belongs to the first-level list; it is a read-only generic
  // campaign URL, not an invitation issued for the staff account.
  const shareWriteStart=networkMethods.length;
  const shareBefore=await api(`/api/admin/referral/campaigns/${first.c.id}`);
  const sharingURLs=[];
  for(const index of [0,1]){
    const campaign=campaigns[index].c;
    await evaluate(`(()=>{const card=[...document.querySelectorAll('.referral-admin-campaign')].find(v=>v.querySelector('h3')?.textContent===${JSON.stringify(campaign.name)});const buttons=[...card.querySelectorAll('button')];if(buttons[0].textContent!=='查看活动详情'||buttons[1].textContent!=='分享')throw new Error('list share placement');buttons[1].focus();buttons[1].click();})()`);
    await wait("document.querySelector('dialog[data-referral-campaign-share] input')");
    const url=await evaluate("document.querySelector('dialog[data-referral-campaign-share] input').value");
    assert.equal(url,`${base}/referral?campaign=${campaign.id}`,'public share has only campaign scope');
    assert.equal(await evaluate("document.querySelector('dialog[data-referral-campaign-share] a').href"),url);
    sharingURLs.push(url);
    for(const width of [1440,375,430]){
      await call('Emulation.setDeviceMetricsOverride',{width,height:850,deviceScaleFactor:1,mobile:width<500});
      const box=await evaluate("(()=>{const d=document.querySelector('dialog[data-referral-campaign-share]'),r=d.getBoundingClientRect();return{left:r.left,right:r.right,width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth};})()");
      assert.ok(!box.overflow&&box.left>=0&&box.right<=box.width,'share dialog fits viewport');
      const shot=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,`operator-share-${index}-${width}.png`),Buffer.from(shot.data,'base64'));
    }
    const copyPoint=await evaluate("(()=>{const b=[...document.querySelectorAll('dialog[data-referral-campaign-share] button')].find(b=>b.textContent==='复制链接');b.scrollIntoView({block:'center'});const r=b.getBoundingClientRect();return{x:r.x+r.width/2,y:r.y+r.height/2}})()");
    await call('Input.dispatchMouseEvent',{type:'mousePressed',button:'left',clickCount:1,...copyPoint});
    await call('Input.dispatchMouseEvent',{type:'mouseReleased',button:'left',clickCount:1,...copyPoint});
    await wait("document.querySelector('dialog[data-referral-campaign-share]').textContent.includes('活动链接已复制')");
    assert.equal(await evaluate("navigator.clipboard.readText()"),url,'actual clipboard contains this campaign');
    if(index===0){
      await evaluate("(()=>{const click=HTMLAnchorElement.prototype.click;window.__shareQr=null;window.__restoreShareQr=()=>{HTMLAnchorElement.prototype.click=click;};HTMLAnchorElement.prototype.click=function(...args){if(this.download.endsWith('-二维码.png'))window.__shareQr={url:this.href,suggestedFilename:this.download};return Reflect.apply(click,this,args);};[...document.querySelectorAll('dialog[data-referral-campaign-share] button')].find(b=>b.textContent==='下载二维码').click();})()");
      await wait("window.__shareQr?.url.startsWith('blob:')");
      const anchor=await evaluate("window.__restoreShareQr();window.__shareQr");
      assert.equal(anchor.suggestedFilename,`裂变活动-${campaign.id}-二维码.png`);
      const download=await csvDownloads.waitFor(anchor);
      const bytes=await fs.readFile(download.path);assert.equal(bytes.subarray(0,8).toString('hex'),'89504e470d0a1a0a');
      await fs.writeFile(path.join(screenshots,'operator-entry-qr.png'),bytes);
      await fs.writeFile(path.join(screenshots,'operator-entry-qr.json'),JSON.stringify({expectedURL:url,downloadCompleted:download.state==='completed'}));
    }
    await evaluate("document.querySelector('dialog[data-referral-campaign-share]').close()");
    assert.equal(await evaluate("document.activeElement.textContent"),'分享','closing returns focus to its list share action');
  }
  assert.notEqual(sharingURLs[0],sharingURLs[1],'each campaign owns its public URL');
  assert.equal(networkMethods.slice(shareWriteStart).every(r=>r.method==='GET'),true,'sharing sends no business writes');
  const shareAfter=await api(`/api/admin/referral/campaigns/${first.c.id}`);
  assert.deepEqual([shareAfter.participant_count,shareAfter.invitation_count,shareAfter.version],[shareBefore.participant_count,shareBefore.invitation_count,shareBefore.version],'sharing does not change participation or configuration');
  // Follow the copied public entry as a guest, then as a trusted synthetic
  // customer who has never joined this campaign. No personal invite is needed.
  await call('Network.deleteCookies',{name:'aicrm_distribution_session',url:base});
  await call('Page.navigate',{url:sharingURLs[1]});
  await wait("[...document.querySelectorAll('a')].some(a=>a.textContent==='使用微信登录')");
  const loginReturn=await evaluate("[...document.querySelectorAll('a')].find(a=>a.textContent==='使用微信登录').getAttribute('href')");
  assert.equal(new URL(loginReturn,base).searchParams.get('return_url'),`/referral?campaign=${campaigns[1].c.id}`,'guest login retains the selected campaign');
  const fresh=await api(`/api/v1/referral/campaigns/${campaigns[1].c.id}/me`,undefined,actors[3]);
  assert.ok(fresh.participation == null,'first entry fixture is not already a member');
  await cookie('aicrm_distribution_session',actors[3].session);await cookie('aicrm_distribution_csrf','referral-fixture-csrf');
  await call('Page.navigate',{url:sharingURLs[1]});
  await wait("document.querySelector('[data-testid=referral-invite]')?.textContent==='参加活动'");
  assert.equal(await evaluate("document.body.textContent.includes('活动链接与二维码')"),false,'public page has no operator share entry');
  await evaluate("document.querySelector('[data-testid=referral-invite]').click()");
  await wait("document.querySelector('[data-testid=referral-accept-dialog]')");
  const initialEntry=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'operator-first-entry-430.png'),Buffer.from(initialEntry.data,'base64'));
  await evaluate("document.querySelector('[data-testid=referral-accept-dialog]').close()");
  await cookie('aicrm_distribution_session',actors[2].session);
  await call('Page.navigate',{url:base+'/admin/referral'});
  await wait(`location.pathname==='/admin/referral'&&[...document.querySelectorAll('.referral-admin-campaign')].some(c=>c.querySelector('h3')?.textContent===${JSON.stringify(first.c.name)})`);
  await call('Emulation.setDeviceMetricsOverride',{width:1440,height:1000,deviceScaleFactor:1,mobile:false});
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
  const expectedCSVName=`裂变活动-${first.c.name}-invitations.csv`;
  // Observe the anchor created by this exact export click. Keep the UI action
  // intact; the test only records its blob URL and logical download name.
  await evaluate(`(()=>{const original=HTMLAnchorElement.prototype.click;window.__referralCSVAnchors=[];window.__restoreReferralCSVAnchor=()=>{HTMLAnchorElement.prototype.click=original;};HTMLAnchorElement.prototype.click=function(...args){if(this.download===${JSON.stringify(expectedCSVName)})window.__referralCSVAnchors.push({url:this.href,suggestedFilename:this.download});return Reflect.apply(original,this,args);};})()`);
  const exportRequestStart=networkRequests.length;
  await evaluate("[...document.querySelectorAll('button')].find(b=>b.textContent==='导出当前筛选').click()");
  for(let i=0;i<80&&!networkRequests.slice(exportRequestStart).some(url=>url.includes('/export?')&&url.includes('participation_id='));i++)await sleep(50);
  const drilldownExport=networkRequests.slice(exportRequestStart).find(url=>url.includes('/export?')&&url.includes('participation_id='));
  assert.ok(drilldownExport,'drilldown export did not request a participation-scoped CSV');
  assert.equal(drilldownExport.includes('team_id='),false,'drilldown export must not retain a stale team filter');
  await wait("window.__referralCSVAnchors?.length>0");
  const anchorDownloads=await evaluate("window.__restoreReferralCSVAnchor();window.__referralCSVAnchors");
  assert.equal(anchorDownloads.length,1,'this scoped export must create exactly one CSV anchor');
  assert.equal(anchorDownloads[0].suggestedFilename,expectedCSVName,'CSV anchor must belong to the selected invitation campaign');
  let currentDownload;
  try {
    currentDownload=await csvDownloads.waitFor(anchorDownloads[0]);
  } catch (error) {
    const diagnostic={target:browserDownloads.targetKind,browser_version:browserDownloads.browserVersion,
      browser_connection:browserDownloads.connectionState,
      ...csvDownloads.diagnostics(anchorDownloads[0])};
    const diagnosticPath=path.join(downloads,'invitation-download-failure.json');
    await fs.writeFile(diagnosticPath,JSON.stringify(diagnostic));
    console.error('referral_download_diagnostics: '+JSON.stringify({path:diagnosticPath,...diagnostic}));
    throw error;
  }
  assert.equal(currentDownload.state,'completed','this CSV GUID must finish downloading');
  const exportedCSV=currentDownload.path;
  const csv=await fs.readFile(exportedCSV,'utf8');
  assert.ok(csv.startsWith('\ufeff邀请人,邀请人战队,参加人,参加人战队,'),'downloaded CSV must retain the invitation headers');
  assert.ok(csv.includes('队长阿青,追光战队,超长昵称的活动参与者用于手机页面换行验证,'),'downloaded CSV must contain the scoped fixture invitation');
  await fs.writeFile(path.join(downloads,'invitation-download.json'),JSON.stringify({...currentDownload,exportRequest:drilldownExport,csvValidated:true,
    browser_target:browserDownloads.targetKind,browser_version:browserDownloads.browserVersion,
    browser_connection:browserDownloads.connectionState,events:csvDownloads.diagnostics(anchorDownloads[0])}));
  console.log('referral_download: '+exportedCSV);
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
  await call('Page.navigate',{url:`${base}/admin/referral/settings?campaign=${first.c.id}`});
  await wait("document.querySelector('[aria-label=\"海报1配套话术\"]')");
  assert.equal(await evaluate("document.querySelector('[aria-label=\"海报1配套话术\"]').value"),shareTexts[0]);
  await evaluate("(()=>{const field=document.querySelector('[aria-label=\"海报1配套话术\"]');field.value='保存后读回 🚀\\n第二行';field.dispatchEvent(new Event('input',{bubbles:true}));[...document.querySelectorAll('button')].find(b=>b.textContent==='发布海报配置').click()})()");
  await wait("document.querySelector('[data-testid=referral-admin-form-message]').textContent.includes('已发布并读取确认')");
  const savedPosters = await api(`/api/admin/referral/campaigns/${first.c.id}`);
  assert.equal(savedPosters.posters[0].share_text,'保存后读回 🚀\n第二行');
  assert.ok(await evaluate("[...document.querySelectorAll('.referral-admin-poster-section button')].find(b=>b.textContent==='选择图片素材').getBoundingClientRect().height<80"), 'material picker action must not stretch alongside all poster rows');
  await evaluate("document.querySelector('.referral-admin-poster-list').scrollIntoView()");
  const settingsImage=await call('Page.captureScreenshot',{format:'png'});await fs.writeFile(path.join(screenshots,'poster-settings-1440.png'),Buffer.from(settingsImage.data,'base64'));
  assert.deepEqual(exceptions,[],'no uncaught browser errors');
  console.log('referral_chromium: PASS');
} finally {
  socket?.close();browserDownloads?.close();chrome?.kill('SIGTERM');await sleep(500);
  if(chrome&&chrome.exitCode===null){chrome.kill('SIGKILL');await sleep(300);}
  if(!process.env.AICRM_REFERRAL_SCREENSHOTS)await fs.rm(profile,{recursive:true,force:true,maxRetries:5,retryDelay:150});
}
