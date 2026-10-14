import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { JSDOM } from 'jsdom';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';
const bundle = await buildTestBrowserBundle(fileURLToPath(new URL('./referralCenter.ts', import.meta.url)));
const json = value => new Response(JSON.stringify(value));
const wait = async test => { for(let i=0;i<200;i++){if(test())return;await new Promise(r=>setTimeout(r,5));}throw new Error('poster did not settle'); };
const deferred = () => { let release;const promise=new Promise(r=>{release=r});return {promise,release}; };
async function fixture({delayInvite=false,delayDecode=false,delayExport=false,url,status=200,transparent=false,failConfiguration=false,nickname=""}={}) {
 const pending=deferred(), decode=deferred(), encode=deferred();
 const calls=[],draws=[],exports=[],canvases=[],names=[];
 let revision='v1',layout={x:830,y:1952,size:330},user='A',copy='first copy',failStatus=status;
 const dom=new JSDOM('<main id="referral-root"></main>',{url:'https://crm.example/referral?campaign=8',runScripts:'outside-only',pretendToBeVisual:true,beforeParse(w){
  w.Headers=Headers;w.Response=Response;w.Blob=Blob;
  w.HTMLDialogElement.prototype.showModal=function(){this.setAttribute('open','')};
  w.HTMLDialogElement.prototype.close=function(){this.removeAttribute('open');this.dispatchEvent(new w.Event('close'))};
  w.FileReader=class{readAsDataURL(blob){blob.arrayBuffer().then(b=>{this.result='data:'+blob.type+';base64,'+Buffer.from(b).toString('base64');this.onload?.()})}abort(){}};
  w.Image=function(){const img=w.document.createElement('img');Object.defineProperties(img,{naturalWidth:{value:1250},naturalHeight:{value:2426}});img.decode=()=>delayDecode&&Buffer.from(img.src.split(',')[1],'base64').toString().includes('/1?')?decode.promise:Promise.resolve();return img};
  w.HTMLCanvasElement.prototype.getContext=function(){canvases.push(this);return{drawImage:img=>{this.source=Buffer.from(img.src.split(',')[1],'base64').toString();draws.push(this.source)},fillRect(){},save(){},restore(){},measureText(text){return {width:Array.from(text).length*30}},fillText(text,x,y){names.push({text,x,y,font:this.font,align:this.textAlign,color:this.fillStyle})}}};
  w.HTMLCanvasElement.prototype.toDataURL=()=>{throw new Error('sync export forbidden')};
  w.HTMLCanvasElement.prototype.toBlob=function(cb,type,quality){exports.push({source:this.source,type,quality});const finish=()=>cb(new Blob([this.source],{type}));if(delayExport)encode.promise.then(finish);else queueMicrotask(finish)};
  w.fetch=async(input,init={})=>{const path=String(input);calls.push({path,method:init.method||'GET'});
   if(path.endsWith('/campaigns/8') && failConfiguration && calls.filter(c=>c.path.endsWith('/campaigns/8')).length>1)return new Response('{}',{status:503});
   if(path.endsWith('/campaigns/8'))return json({id:8,name:'测试',state:'active',effective_state:'active',team_mode:'individual',qualification_mode:'free_signup',starts_at:'2026-01-01T00:00:00Z',ends_at:'2099-01-01T00:00:00Z',posters:[1,2].map(slot=>({slot,image_url:`/api/v1/referral/campaigns/8/posters/${slot}?v=${revision}`,image_width:1250,image_height:2426,qr_layout:layout,share_text:copy+slot}))});
   if(path.endsWith('/me'))return json({participation:{joined_at:'2026-01-01T00:00:00Z'},invitation_available:true});
   if(path.endsWith('/invite')){if(delayInvite)await pending.promise;return json({url:url||'https://crm.example/referral/invite/rfi_'+user.repeat(43),poster_nickname:nickname})}
   if(path.includes('/posters/') && failStatus===0)throw new TypeError('Failed to fetch');
   if(path.includes('/posters/'))return new Response(new Blob([path],{type:transparent?'image/png':'image/jpeg'}),{status:failStatus});
   return json({items:[]});
  };
 }});dom.window.eval(bundle);const doc=dom.window.document;
 await wait(()=>doc.querySelector('[data-testid="referral-invite"]')?.disabled===false);
 const click=id=>doc.querySelector(`[data-testid="${id}"]`).click();
 const image=()=>doc.querySelector('[data-testid="referral-poster-image"]');
 return {dom,doc,calls,draws,exports,canvases,names,click,image,pending,decode,encode,setRevision:v=>revision=v,setLayout:v=>layout=v,setUser:v=>user=v,setNickname:v=>nickname=v,setCopy:v=>copy=v,setStatus:v=>failStatus=v};
}
{
 const f=await fixture({delayInvite:true});for(let i=0;i<3;i++)f.click('referral-invite');
 await wait(()=>f.calls.filter(c=>c.path.endsWith('/invite')).length===1);
 assert.equal(f.calls.filter(c=>c.path.endsWith('/campaigns/8')).length,2,'metadata starts before invite finishes');
 assert.equal(f.doc.querySelector('dialog'),null);
 f.pending.release();await wait(f.image);
 assert.equal(f.exports.length,1);assert.equal(f.exports[0].type,'image/jpeg');assert.equal(f.exports[0].quality,0.95);
 f.click('referral-poster-next');await wait(f.image);f.click('referral-poster-previous');await wait(f.image);
 assert.equal(f.exports.length,2,'return to composed poster reuses result');
 assert.equal(f.canvases.every(c=>c.width===0&&c.height===0),true,'encoded canvases release pixels');
 f.click('referral-close-poster');assert.equal(f.doc.querySelector('dialog'),null);
 f.setRevision('v2');f.setLayout({x:800,y:1900,size:350});f.setUser('B');f.setCopy('new copy');
 f.click('referral-invite');await wait(f.image);
 assert.equal(f.exports.length,3,'reopen never shares personal result');
 assert.ok(Buffer.from(f.image().src.split(',')[1],'base64').toString().includes('v=v2'));
 assert.equal(f.doc.querySelector('[data-testid="referral-poster-share-text"]').textContent,'new copy1');
 f.click('referral-close-poster');f.dom.window.close();
}
{
 const f=await fixture({delayInvite:true,failConfiguration:true});f.click('referral-invite');await wait(()=>f.calls.some(c=>c.path.endsWith('/invite')));await new Promise(r=>setTimeout(r,10));f.click('referral-invite');f.click('referral-invite');assert.equal(f.calls.filter(c=>c.path.endsWith('/invite')).length,1,'failed config cannot unlock a still-pending invite');f.pending.release();await wait(()=>f.doc.body.textContent.includes('HTTP 503'));assert.equal(f.doc.querySelector('dialog'),null);f.dom.window.close();
}
{
 const f=await fixture({delayDecode:true});f.click('referral-invite');await wait(()=>f.doc.querySelector('dialog[open]'));
 await wait(()=>f.calls.some(c=>c.path.includes('/posters/1?')));
 f.click('referral-poster-next');await wait(f.image);f.decode.release();await new Promise(r=>setTimeout(r,20));
 assert.equal(f.draws.length,1,'late first decode must cancel BEFORE drawing');assert.ok(f.draws[0].includes('/2?'));
 assert.equal(f.doc.querySelector('[data-testid="referral-poster-share-text"]').textContent,'first copy2');
 f.click('referral-close-poster');f.dom.window.close();
}
{
 const f=await fixture({delayDecode:true});f.click('referral-invite');await wait(()=>f.doc.querySelector('dialog[open]'));await wait(()=>f.calls.some(c=>c.path.includes('/posters/1?')));
 f.click('referral-close-poster');f.decode.release();await new Promise(r=>setTimeout(r,20));assert.equal(f.draws.length,0,'close cancels before drawing');assert.equal(f.doc.querySelector('dialog'),null);f.dom.window.close();
}
{
 const f=await fixture({delayExport:true});f.click('referral-invite');await wait(()=>f.exports.length===1);f.click('referral-close-poster');f.encode.release();await new Promise(r=>setTimeout(r,20));assert.equal(f.doc.querySelector('dialog'),null);assert.equal(f.canvases[0].width,0);f.dom.window.close();
}
for(const [status,text]of [[503,'繁忙'],[409,'刚刚更新'],[404,'加载失败'],[0,'加载失败']]){
 const f=await fixture({status});f.click('referral-invite');await wait(()=>f.doc.querySelector('[data-testid="referral-poster-preview"]')?.textContent.includes(text));
 await new Promise(r=>setTimeout(r,30));assert.equal(f.calls.filter(c=>c.path.includes('/posters/')).length,1,'no automatic retry');assert.equal(f.image(),null);
 f.setStatus(200);f.doc.querySelector('.referral-poster-choice').click();await wait(f.image);assert.equal(f.exports.length,1,'failure must not be cached');f.click('referral-close-poster');f.dom.window.close();
}
for(const url of ['https://evil.example/referral/invite/rfi_'+'A'.repeat(43),'https://crm.example/referral/invite/rfi_'+'A'.repeat(43)+'?customer_id=9','javascript:alert(1)']){
 const f=await fixture({url});f.click('referral-invite');await wait(()=>f.doc.body.textContent.includes('无效'));assert.equal(f.doc.querySelector('dialog'),null);assert.equal(f.draws.length,0);f.dom.window.close();
}
{
 const f=await fixture({transparent:true});f.click('referral-invite');await wait(f.image);assert.equal(f.exports[0].type,'image/png','PNG fallback preserves alpha');f.click('referral-close-poster');f.dom.window.close();
}
console.log('poster performance: pending dedup/parallel reads, dialog-only reuse, version/layout/user reopen, pre-draw cancellation, export cancellation, error/manual retry, invite validation, JPEG95/PNG fallback passed');

{
 const f=await fixture({nickname:'微信用户 👩‍💻'});
 assert.equal(f.doc.querySelector('[data-testid="referral-invite"]').textContent,'生成溯源码');
 f.click('referral-invite');await wait(f.image);
 assert.deepEqual(f.names.map(n=>n.text),['微信用户 👩‍💻']);
 assert.equal(f.names[0].x,1202);assert.equal(f.names[0].y,48);
 assert.equal(f.names[0].align,'right');assert.equal(f.names[0].color,'#F7F0DF');
 f.click('referral-poster-next');await wait(f.image);f.click('referral-poster-previous');await wait(f.image);
 assert.equal(f.names.length,2,'cached poster does not redraw decoration');
 f.click('referral-close-poster');f.setNickname('另一用户');f.click('referral-invite');await wait(f.image);
 assert.equal(f.names.at(-1).text,'另一用户','reopen refreshes nickname even when QR payload is unchanged');
 f.click('referral-close-poster');f.setNickname('');f.click('referral-invite');await wait(f.image);
 assert.equal(f.names.length,3,'missing nickname never prints a fallback');
 f.click('referral-close-poster');f.dom.window.close();
}
{
 const family='👨‍👩‍👧‍👦';const f=await fixture({nickname:'浅蓝'+family+'超长昵称用于验证完整字符截断'});
 f.click('referral-invite');await wait(f.image);
 assert.equal(f.names[0].text,'浅蓝'+family+'…','truncation keeps complete emoji grapheme');
 f.click('referral-close-poster');f.dom.window.close();
}
