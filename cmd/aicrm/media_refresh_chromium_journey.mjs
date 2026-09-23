import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "../../internal/webshell/chromium_launch.mjs";

const baseURL = process.env.AICRM_MEDIA_REFRESH_TEST_URL;
const username = process.env.AICRM_MEDIA_REFRESH_TEST_USERNAME;
const password = process.env.AICRM_MEDIA_REFRESH_TEST_PASSWORD;
const screenshot = process.env.AICRM_MEDIA_REFRESH_SCREENSHOT;
const missingSourceRef = process.env.AICRM_MEDIA_REFRESH_MISSING_SOURCE_REF;
const xlsxPath = process.env.AICRM_MEDIA_REFRESH_XLSX;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !screenshot || !missingSourceRef || !xlsxPath) throw new Error("media refresh Chromium journey requires HTTPS URL, credentials, real XLSX, missing source, and screenshot path");
const xlsxBase64 = (await fs.readFile(xlsxPath)).toString("base64");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const asError = (error) => error instanceof Error ? error : new Error(String(error));
function binary() { for (const item of [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, process.platform === "darwin" ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" : "", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"].filter(Boolean)) { if (item.includes("/")) { try { if (spawnSync(item, ["--version"], { stdio: "ignore" }).status === 0) return item; } catch {} } else if (spawnSync("which", [item], { stdio: "ignore" }).status === 0) return item; } throw new Error("Chromium binary is unavailable"); }
class CDP { constructor(socket) { this.socket=socket; this.id=0; this.waiting=new Map(); socket.addEventListener("message", event => { const m=JSON.parse(String(event.data)); if (m.id && this.waiting.has(m.id)) { const p=this.waiting.get(m.id); this.waiting.delete(m.id); m.error ? p.reject(Object.assign(new Error(`CDP ${m.error.code}: ${m.error.message}`), {code:m.error.code})) : p.resolve(m.result||{}); } }); } call(method, params={}) { return new Promise((resolve,reject)=>{ const id=++this.id; this.waiting.set(id,{resolve,reject}); this.socket.send(JSON.stringify({id,method,params})); }); } close() { this.socket.close(); } }
let browser; let stderr="";
async function devtools(profile) { const until=Date.now()+chromiumStartupTimeoutMS; while(Date.now()<until) { try { const port=String(await fs.readFile(path.join(profile,"DevToolsActivePort"),"utf8")).split("\n")[0]; if(/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch {} if(browser?.exitCode!==null) break; await sleep(50); } throw new Error(chromiumStartupDiagnostic({profile,exitCode:browser?.exitCode,signalCode:browser?.signalCode,stderr})); }
async function value(cdp, expression) { const result=await cdp.call("Runtime.evaluate",{expression,returnByValue:true,awaitPromise:true}); if(result.exceptionDetails) throw new Error(`page evaluation exception: ${result.exceptionDetails.exception?.description || result.exceptionDetails.text}; expression=${expression.slice(0,180)}`); return result.result?.value; }
async function wait(cdp, expression, label) {
 for(let i=0;i<150;i++) {
  try { if(await value(cdp,expression)) return; }
  catch(error) {
   // Polling may race a document commit; never retry actions or assertions.
   if(error.code!==-32000 || !/context.*(destroyed|not found)|Cannot find context|Inspected target navigated or closed/i.test(error.message)) throw error;
  }
  await sleep(100);
 }
 throw new Error(`${label}: ${await value(cdp,"JSON.stringify({path:location.pathname,title:document.title,body:document.body.innerText.slice(-2400),requests:window.__mediaRefreshRequests||[]})")}`);
}
// Toolbars can rerender between two CDP round trips. Resolve and click in one
// synchronous page evaluation, like a locator click. A false result performed
// no action; any error after a possible click propagates without retrying it.
async function clickGroupButton(cdp, label) {
 for (let i=0;i<150;i++) {
  const clicked=await value(cdp, `(()=>{const b=[...document.querySelectorAll('button')].find(b=>b.textContent===${JSON.stringify(label)}&&!b.disabled&&b.getClientRects().length);if(!b)return false;b.click();return true})()`);
  if(clicked) return;
  await sleep(100);
 }
 throw new Error(`group button never became actionable: ${label}`);
}
async function waitForNewDocument(cdp, previous) {
 let committed=false;
 for(let i=0;i<150;i++) {
  const current=(await cdp.call('Page.getFrameTree')).frameTree.frame.loaderId;
  if(current && current!==previous) { committed=true; break; }
  await sleep(100);
 }
 if(!committed) throw new Error('navigation did not commit a new document');
 await wait(cdp,"document.readyState !== 'loading'",'new document ready');
}

// location.href can change while the old document still contains its buttons.
// Wait for a new loader before reading DOM after an action that navigates.
// The action itself is executed exactly once, never retried.
async function submitAndNavigate(cdp, expression) {
 const previous=(await cdp.call('Page.getFrameTree')).frameTree.frame.loaderId;
 await value(cdp, expression);
 await waitForNewDocument(cdp, previous);
}
async function navigate(cdp, url) {
 const previous=(await cdp.call('Page.getFrameTree')).frameTree.frame.loaderId;
 await cdp.call(url ? 'Page.navigate' : 'Page.reload', url ? {url} : {});
 await waitForNewDocument(cdp, previous);
}

async function assertImageLibraryLayout(cdp, width, height) {
 await cdp.call("Emulation.setDeviceMetricsOverride",{width,height,deviceScaleFactor:1,mobile:false});
 await wait(cdp,"Boolean(document.querySelector('[data-image-library-query]')&&document.querySelector('[data-image-library-cards]'))",`image library ${width}px controls`);
 const layout=await value(cdp,`(()=>{const root=document.documentElement;const toolbar=document.querySelector('[data-image-library-query]')?.closest('.admin-toolbar');const cards=document.querySelector('[data-image-library-cards]');const box=node=>{const rect=node?.getBoundingClientRect();return rect?{left:rect.left,right:rect.right,width:rect.width}:null};return {viewport:root.clientWidth,scrollWidth:root.scrollWidth,toolbar:box(toolbar),cards:box(cards)}})()`);
 if(!layout||layout.scrollWidth>layout.viewport+1||!layout.toolbar||!layout.cards||layout.toolbar.left<-.5||layout.toolbar.right>layout.viewport+1||layout.cards.left<-.5||layout.cards.right>layout.viewport+1) throw new Error(`image library ${width}px page overflow ${JSON.stringify(layout)}`);
 await value(cdp,"[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='上传图片').click();true");
 await wait(cdp,"Boolean(document.querySelector('form[data-image-library-dialog] [data-image-library-dialog-fields]'))",`image library ${width}px upload dialog`);
 const dialog=await value(cdp,`(()=>{const panel=document.querySelector('form[data-image-library-dialog]');const fields=panel?.querySelector('[data-image-library-dialog-fields]');const submit=panel?.querySelector('[data-image-library-dialog-submit]');if(!panel||!fields||!submit)return null;fields.scrollTop=fields.scrollHeight;const panelBox=panel.getBoundingClientRect();const submitBox=submit.getBoundingClientRect();return {viewport:window.innerHeight,panelTop:panelBox.top,panelBottom:panelBox.bottom,panelScrollHeight:panel.scrollHeight,panelClientHeight:panel.clientHeight,fieldsScrollHeight:fields.scrollHeight,fieldsClientHeight:fields.clientHeight,submitTop:submitBox.top,submitBottom:submitBox.bottom}})()`);
 if(!dialog||dialog.panelTop<-.5||dialog.panelBottom>dialog.viewport+.5||dialog.submitTop<-.5||dialog.submitBottom>dialog.viewport+.5||dialog.panelScrollHeight<dialog.panelClientHeight||dialog.fieldsScrollHeight<dialog.fieldsClientHeight) throw new Error(`image library ${width}px dialog overflow ${JSON.stringify(dialog)}`);
 await value(cdp,"document.querySelector('button[aria-label=关闭弹窗]').click();true");
 await wait(cdp,"!document.querySelector('form[data-image-library-dialog]')",`image library ${width}px close dialog`);
}
async function assertImageLibraryThumbnailStates(cdp) {
 await wait(cdp,"Boolean([...document.querySelectorAll('[data-image-library-thumbnail]')].find(node=>node.closest('tr')?.textContent?.includes('浏览器刷新素材'))?.querySelector('img'))",'image library thumbnail');
 const states=await value(cdp,`(()=>{const visual=[...document.querySelectorAll('[data-image-library-thumbnail]')].find(node=>node.closest('tr')?.textContent?.includes('浏览器刷新素材'));const image=visual?.querySelector('img');if(!(image instanceof HTMLImageElement)||!visual)return null;const box=node=>{const rect=node.getBoundingClientRect();return {width:rect.width,height:rect.height}};const state=()=>{const nodes={loading:visual.querySelector('.aicrm-material-thumbnail__loading'),image,fallback:visual.querySelector('.aicrm-material-thumbnail__fallback')};const visible=node=>Boolean(node)&&getComputedStyle(node).display!=='none'&&getComputedStyle(node).visibility!=='hidden';return {state:visual.dataset.materialThumbnailState,visual:box(visual),loading:{display:getComputedStyle(nodes.loading).display,visible:visible(nodes.loading),box:box(nodes.loading)},image:{display:getComputedStyle(nodes.image).display,visible:visible(nodes.image),box:box(nodes.image)},fallback:{display:getComputedStyle(nodes.fallback).display,visible:visible(nodes.fallback),box:box(nodes.fallback)}}};image.dispatchEvent(new Event('error'));const error=state();image.dispatchEvent(new Event('load'));const loaded=state();return {error,loaded};})()`);
 const sameBox=(left,right)=>Boolean(left&&right&&Math.abs(left.width-right.width)<=2&&Math.abs(left.height-right.height)<=2);
 const only=(snapshot, expected)=>snapshot&&snapshot.state===expected&&snapshot.loading.visible===false&&snapshot.image.visible===(expected==='loaded')&&snapshot.fallback.visible===(expected==='error')&&snapshot.loading.display==='none'&&snapshot.image.display===(expected==='loaded'?'block':'none')&&snapshot.fallback.display===(expected==='error'?'grid':'none')&&sameBox(snapshot.visual,expected==='loaded'?snapshot.image.box:snapshot.fallback.box);
 if(!only(states?.error,'error')||!only(states?.loaded,'loaded')) throw new Error(`image-library thumbnail computed states=${JSON.stringify(states)}`);
}
async function assertImageLibraryThumbnailLoadingGeometry(cdp) {
 await wait(cdp,"Boolean([...document.querySelectorAll('[data-image-library-thumbnail]')].find(node=>node.closest('tr')?.textContent?.includes('浏览器刷新素材')))",'image library thumbnail card');
 const state=await value(cdp,`(()=>{const visual=[...document.querySelectorAll('[data-image-library-thumbnail]')].find(node=>node.closest('tr')?.textContent?.includes('浏览器刷新素材'));const loading=visual?.querySelector('.aicrm-material-thumbnail__loading');const image=visual?.querySelector('img');const fallback=visual?.querySelector('.aicrm-material-thumbnail__fallback');if(!visual||!loading||!image||!fallback)return null;const box=node=>{const rect=node.getBoundingClientRect();return {width:rect.width,height:rect.height}};const visible=node=>getComputedStyle(node).display!=='none'&&getComputedStyle(node).visibility!=='hidden';return {state:visual.dataset.materialThumbnailState,visual:box(visual),loading:{visible:visible(loading),box:box(loading)},image:{visible:visible(image)},fallback:{visible:visible(fallback)}};})()`);
 const sameBox=(left,right)=>Boolean(left&&right&&Math.abs(left.width-right.width)<=2&&Math.abs(left.height-right.height)<=2);
 if(!state||state.state!=='loading'||!state.loading.visible||state.image.visible||state.fallback.visible||!sameBox(state.visual,state.loading.box)) throw new Error(`image-library thumbnail loading geometry=${JSON.stringify(state)}`);
}
async function captureImageLibraryViewport(cdp, width, height) {
 await cdp.call('Emulation.setDeviceMetricsOverride',{width,height,deviceScaleFactor:1,mobile:false});
 const output=path.join(path.dirname(screenshot),`media-refresh-chromium-${width}.png`);
 const image=await cdp.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});
 await fs.writeFile(output,Buffer.from(image.data,'base64'));
 return output;
}
const waitForExit = (child, ms) => new Promise((resolve) => { if (child.exitCode !== null || child.signalCode !== null) return resolve(true); const timer = setTimeout(() => resolve(false), ms); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
const stopBrowser = async (child) => {
 if (!child || child.exitCode !== null || child.signalCode !== null) return null;
 try {
  child.kill("SIGTERM");
  if (await waitForExit(child, 3000)) return null;
  if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  if (await waitForExit(child, 3000)) return null;
  return new Error("Chromium process did not exit before profile cleanup");
 } catch (error) { return asError(error); }
};
const removeProfile = async (profile) => {
 let lastError;
 for (let attempt = 0; attempt < 40; attempt += 1) {
  try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return null; }
  catch (error) {
   lastError = asError(error);
   if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return lastError;
   await sleep(100);
  }
 }
 return new Error(`Chromium test profile cleanup did not complete after 40 attempts: ${lastError?.code || lastError?.message || "unknown error"}`);
};
async function exerciseGroupManagement(cdp) {
 for(const [kind,tab] of [['image','images'],['attachment','attachments'],['miniprogram','miniprograms']]) {
  const prefix=`分组旅程-${kind}`;
  const api=`/api/admin/${kind}-library`;
  await navigate(cdp,`${baseURL}/admin/materials?tab=${tab}`);
  await wait(cdp,"Boolean([...document.querySelectorAll('button')].find(b=>b.textContent==='新增分组'&&!b.disabled))",`${kind} group controls`);
  await clickGroupButton(cdp,"新增分组");
  await submitAndNavigate(cdp,`document.querySelector('dialog[open] input').value=${JSON.stringify(prefix)};document.querySelector('dialog[open] form').requestSubmit();true`);
  await wait(cdp,`new URL(location.href).searchParams.get('material_group')===${JSON.stringify(prefix)} && Boolean([...document.querySelectorAll('button')].find(b=>b.textContent==='编辑组名'))`,`${kind} empty group retained`);
  const groupID=await value(cdp,`fetch(${JSON.stringify(api+'/groups')}).then(r=>r.json()).then(b=>b.items.find(g=>g.name===${JSON.stringify(prefix)}).id)`);
  for(let n=0;n<2;n++) {
   const action={image:'上传图片',attachment:'上传附件',miniprogram:'新增小程序'}[kind];
   await wait(cdp,`Boolean([...document.querySelectorAll('.admin-topbar button')].find(b=>!b.disabled&&(b.textContent.trim()===${JSON.stringify(action)}||(${JSON.stringify(kind)}==='miniprogram'&&/小程序/.test(b.textContent)))))`,`${kind} create toolbar ready`);
   // The existing mini-program title may use 创建 instead of 新增.
   await value(cdp,`(()=>{const b=[...document.querySelectorAll('.admin-topbar button')].find(b=>b.textContent.trim()===${JSON.stringify(action)}||(${JSON.stringify(kind)}==='miniprogram'&&/小程序/.test(b.textContent)));if(!b)throw Error('missing create');b.click();return true})()`);
   await wait(cdp,"Boolean(document.querySelector('[data-material-group-select]'))",`${kind} create group selector`);
   const selected=await value(cdp,"document.querySelector('[data-material-group-select]').value");if(Number(selected)!==groupID)throw Error(`${kind} upload default group mismatch ${selected}`);
   if(kind==='miniprogram') {
    await value(cdp,`(()=>{document.querySelector('#fMpName').value=${JSON.stringify(prefix+'-'+n)};document.querySelector('#fMpAppid').value='wxgroupfixture';document.querySelector('#fMpPath').value='pages/group';document.querySelector('#fMpTitle').value='分组验收';[...document.querySelectorAll('#stage button')].find(b=>b.textContent.trim()==='创建').click();return true})()`);
   } else {
    await value(cdp,`(()=>{const kind=${JSON.stringify(kind)};const bytes=kind==='image'?Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0)):new TextEncoder().encode('%PDF-1.4\\nfixture');const input=document.querySelector(kind==='image'?'#fImgUpFile':'#fAttUpFile');const dt=new DataTransfer();dt.items.add(new File([bytes],kind==='image'?'group.png':'group.pdf',{type:kind==='image'?'image/png':'application/pdf'}));input.files=dt.files;input.dispatchEvent(new Event('change',{bubbles:true}));document.querySelector(kind==='image'?'#fImgUpName':'#fAttUpName').value=${JSON.stringify(prefix+'-'+n)};[...document.querySelectorAll('#stage button')].find(b=>b.textContent.trim()==='上传').click();return true})()`);
   }
   await wait(cdp,"!document.querySelector('[data-material-group-select]')",`${kind} create saved`);
   await wait(cdp,`fetch(${JSON.stringify(api+'/groups')}).then(r=>r.json()).then(b=>b.items.some(g=>g.id===${groupID}&&g.count===${n+1}))`,`${kind} upload group readback`);
  }
  // Reload to exercise persisted memberships and select only the current page.
  await navigate(cdp);
  await wait(cdp,"document.querySelectorAll('input[aria-label^=\"选择素材 \"]').length===2 && !document.querySelector('[aria-label=\"选择当前页全部素材\"],[aria-label=\"全选当前页素材\"]')?.disabled",`${kind} selectable rows`);
  if(kind==='image') {
   await cdp.call('Emulation.setDeviceMetricsOverride',{width:1440,height:900,deviceScaleFactor:1,mobile:false});
   const layout=await value(cdp,`(()=>{const box=document.querySelector('input[aria-label^="选择素材 "]');const row=box.closest('tr');const bar=document.querySelector('[data-material-group-actions]');const search=document.querySelector('[data-image-library-query]');const transfer=[...bar.querySelectorAll('button')].find(b=>b.textContent==='转移分组');return {left:row.cells[0].contains(box)&&box.parentElement.firstElementChild===box,pure:!row.cells[1].querySelector('button,input,select'),inline:bar.parentElement===search.parentElement,disabled:transfer.disabled}})()`);
   if(!Object.values(layout).every(Boolean))throw Error('compact image selection layout '+JSON.stringify(layout));
   const compactImage=await cdp.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});
   await fs.writeFile(path.join(path.dirname(screenshot),'media-compact-selection.png'),Buffer.from(compactImage.data,'base64'));
   await value(cdp,`document.querySelector('[aria-label="全选当前页素材"]').click();true`);
   if(!await value(cdp,`[...document.querySelectorAll('input[aria-label^="选择素材 "]')].every(b=>b.checked)`))throw Error('select current image page failed');
   await value(cdp,`document.querySelector('[aria-label="全选当前页素材"]').click();true`);
   if(!await value(cdp,`[...document.querySelectorAll('input[aria-label^="选择素材 "]')].every(b=>!b.checked)`))throw Error('clear current image selection failed');
  }
  await value(cdp,"(()=>{const checkbox=document.querySelector('input[aria-label^=\"选择素材 \"]');const row=checkbox.closest('tr,[data-material-library-id]');[...row.querySelectorAll('button')].find(b=>b.textContent.trim()==='编辑').click();return true})()");
  await wait(cdp,"Boolean(document.querySelector('[data-material-group-select]'))",`${kind} edit group selector`);
  if(Number(await value(cdp,"document.querySelector('[data-material-group-select]').value"))!==groupID)throw Error(`${kind} edit lost existing group`);
  await value(cdp,"[...document.querySelectorAll('#stage button')].find(b=>b.textContent.trim()==='保存').click();true");
  await wait(cdp,"!document.querySelector('[data-material-group-select]')",`${kind} edit saved with group`);
  await navigate(cdp);
  await wait(cdp,"document.querySelectorAll('input[aria-label^=\"选择素材 \"]').length===2",`${kind} edited group readback`);
  await value(cdp,"document.querySelector('[aria-label=\"选择当前页全部素材\"],[aria-label=\"全选当前页素材\"]').click();[...document.querySelectorAll('button')].find(b=>['移动到分组','转移分组'].includes(b.textContent)&&!b.hidden).click();true");
  await wait(cdp,"Boolean(document.querySelector('dialog[open] select'))",`${kind} batch move dialog`);
  await submitAndNavigate(cdp,"document.querySelector('dialog[open] select').value='';document.querySelector('dialog[open] form').requestSubmit();true");
  await wait(cdp,`fetch(${JSON.stringify(api+'/groups')}).then(r=>r.json()).then(b=>b.items.some(g=>g.id===${groupID}&&g.count===0))`,`${kind} batch ungroup persisted`);
  await wait(cdp,"Boolean([...document.querySelectorAll('button')].find(b=>b.textContent==='编辑组名'))",`${kind} empty group edit`);
  await clickGroupButton(cdp,"编辑组名");
  await submitAndNavigate(cdp,`document.querySelector('dialog[open] input').value=${JSON.stringify(prefix+'-改名')};document.querySelector('dialog[open] form').requestSubmit();true`);
  await wait(cdp,`new URL(location.href).searchParams.get('material_group')===${JSON.stringify(prefix+'-改名')} && Boolean([...document.querySelectorAll('button')].find(b=>b.textContent==='删除分组'))`,`${kind} renamed group selected`);
  await clickGroupButton(cdp,"删除分组");
  await submitAndNavigate(cdp,"document.querySelector('dialog[open] form').requestSubmit();true");
  await wait(cdp,`new URL(location.href).searchParams.get('tab')===${JSON.stringify(tab)} && new URL(location.href).searchParams.get('material_group')==='' && !document.querySelector('dialog[open]')`,`${kind} deletion returns to same tab ungrouped`);
  await wait(cdp,`fetch(${JSON.stringify(api+'/groups')}).then(r=>r.json()).then(b=>!b.items.some(g=>g.id===${groupID}))`,`${kind} group deleted readback`);
 }
}

const profile=await fs.mkdtemp(path.join(os.tmpdir(),"aicrm-media-refresh-chromium-")); let cdp; let journeyError;
try {
 browser=spawn(binary(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--no-first-run","--no-default-browser-check","--disable-background-networking","--ignore-certificate-errors","--allow-insecure-localhost","about:blank"],{stdio:["ignore","ignore","pipe"]}); browser.stderr.on("data",c=>{stderr=(stderr+c).slice(-2048);});
 const tab=await (await fetch(`${await devtools(profile)}/json/new?about:blank`,{method:"PUT"})).json(); const socket=new WebSocket(tab.webSocketDebuggerUrl); await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",reject,{once:true});}); cdp=new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable");
 await navigate(cdp,`${baseURL}/login?next=%2Fadmin%2Fimage-library`); await wait(cdp,"Boolean(document.querySelector('form[action=\"/login\"] input[name=login_csrf_token]'))","login page");
 await submitAndNavigate(cdp,`(()=>{document.querySelector('input[name=username]').value=${JSON.stringify(username)};document.querySelector('input[name=password]').value=${JSON.stringify(password)};document.querySelector('form[action="/login"]').requestSubmit();return true})()`);
 await wait(cdp,"location.pathname==='/admin/materials'&&document.body?.dataset.page==='images'&&document.title.includes('素材库')","material workspace title");
 await wait(cdp,"Boolean(document.querySelector('[data-image-library-query]')&&document.querySelector('[data-image-library-cards]'))",'V3 image library controls');
 await wait(cdp,"Boolean([...document.querySelectorAll('button')].find(b=>b.textContent==='新增分组'&&!b.disabled))",'group create ready');
 await clickGroupButton(cdp,"新增分组");
 await wait(cdp,"Boolean(document.querySelector('dialog[open] input'))",'group name dialog');
 await submitAndNavigate(cdp,"document.querySelector('dialog[open] input').value='浏览器分组';document.querySelector('dialog[open] form').requestSubmit();true");
 await wait(cdp,"new URL(location.href).searchParams.get('material_group')==='浏览器分组'&&Boolean(document.querySelector('[data-material-group-value=\"category:浏览器分组\"]'))",'empty group creation persists');
 await value(cdp,"document.querySelector('[data-material-group-value=\"\"]').click();true");
 await value(cdp,"(()=>{const fetcher=window.fetch.bind(window);window.__mediaRefreshRequests=[];window.fetch=async(...args)=>{const response=await fetcher(...args);window.__mediaRefreshRequests.push(`${args[1]?.method||'GET'} ${typeof args[0]==='string'?args[0]:args[0].url} ${response.status} ${await response.clone().text()}`);return response};return true})()");
 // The V3 image Host keeps this query as a draft until a deliberate Enter.
 // Its existing debounce/read handler remains the authoritative loader.
 const imageCandidatePrevented=await value(cdp,`(()=>{const field=document.querySelector('[data-image-library-query]');if(!(field instanceof HTMLInputElement))return null;field.focus();field.value='Chromium素材';field.dispatchEvent(new Event('input',{bubbles:true,cancelable:true}));field.dispatchEvent(new FocusEvent('blur',{bubbles:true}));field.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));field.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));const candidate=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter',isComposing:true});Object.defineProperty(candidate,'keyCode',{value:229});field.dispatchEvent(candidate);return candidate.defaultPrevented})()`);
 if(imageCandidatePrevented!==false)throw new Error('image-library IME candidate Enter was prevented');
 await sleep(320);
 const readsAfterImageCandidate=await value(cdp,"window.__mediaRefreshRequests.filter(value=>value.startsWith('GET /api/admin/image-library?')).length");
 if(readsAfterImageCandidate!==0)throw new Error(`image-library typing, blur, or IME candidate Enter read ${readsAfterImageCandidate} times`);
 await value(cdp,"document.querySelector('[data-image-library-query]').dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'}));true");
 await wait(cdp,"window.__mediaRefreshRequests.filter(value=>value.startsWith('GET /api/admin/image-library?')).length===1",'image-library ordinary Enter did not issue exactly one existing list read');
 const imageSearchFocus=await value(cdp,"(()=>{const field=document.querySelector('[data-image-library-query]');return Boolean(field&&document.activeElement===field&&field.value==='Chromium素材')})()");
 if(!imageSearchFocus)throw new Error('image-library ordinary Enter did not retain focused draft query');
 // The Enter check intentionally leaves a committed search active. Reset through
 // the existing Host control before asserting page-wide refresh facts so the
 // fixture's historical source is visible again.
 await value(cdp,"document.querySelector('button[data-image-library-reset=\"true\"]')?.click();true");
 await wait(cdp,"(()=>{const field=document.querySelector('[data-image-library-query]');return field instanceof HTMLInputElement&&field.value===''&&window.__mediaRefreshRequests.filter(value=>value.startsWith('GET /api/admin/image-library?')).length===2})()",'image-library reset did not restore the unfiltered list');
 // Operational screens omit technical diagnostics even when a source is missing.
 if(await value(cdp,"Boolean(document.querySelector('#material-refresh-panel'))||document.body.innerText.includes('刷新设置与明细')")) throw new Error('retired refresh diagnostics are visible');
 await value(cdp,"document.querySelector('[data-material-group-value=\"__ungrouped__\"]').click();true");
 await wait(cdp,"window.__mediaRefreshRequests.some(value=>value.startsWith('GET /api/admin/image-library?')&&value.includes('only_ungrouped=true'))",'ungrouped filter must reach server');
 await value(cdp,"document.querySelector('button[data-image-library-reset]').click();true");
 await assertImageLibraryLayout(cdp,1280,800); await assertImageLibraryLayout(cdp,1440,900); await assertImageLibraryLayout(cdp,780,700); await assertImageLibraryLayout(cdp,390,420); await cdp.call("Emulation.clearDeviceMetricsOverride");
 await value(cdp,"(()=>{const region=[...document.querySelectorAll('main#stage div')].find(n=>n.style.overflow==='auto');if(!region)return false;region.scrollTop=180;return region.scrollTop>=0})()");
 await value(cdp,"[...document.querySelectorAll('button')].find(b=>b.textContent.trim()==='上传图片').click();true"); await wait(cdp,"Boolean(document.querySelector('#fImgUpFile'))","image upload dialog");
 await value(cdp,`(()=>{const png=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0));const input=document.querySelector('#fImgUpFile');const dt=new DataTransfer();dt.items.add(new File([png],'browser-source.png',{type:'image/png'}));input.files=dt.files;input.dispatchEvent(new Event('change',{bubbles:true}));document.querySelector('#fImgUpName').value='浏览器刷新素材';document.querySelector('#fImgCategory').value=[...document.querySelector('#fImgCategory').options].find(o=>o.textContent==='浏览器分组').value;[...document.querySelectorAll('#stage button')].find(b=>b.textContent.trim()==='上传').click();return true})()`);
 await wait(cdp,"!document.querySelector('#fImgUpFile')&&document.querySelector('[data-image-library-cards]')?.textContent.includes('浏览器刷新素材')","image upload/readback");
 await value(cdp,"[...document.querySelectorAll('.admin-toolbar button')].find(b=>b.textContent==='刷新').click();true");
 await wait(cdp,"Boolean(document.querySelector('[data-material-group-value=\"category:浏览器分组\"]'))",'saved group refresh');
 await value(cdp,"document.querySelector('[data-material-group-value=\"category:浏览器分组\"]').click();true");
 await wait(cdp,"document.querySelector('[data-image-library-cards]')?.textContent.includes('浏览器刷新素材')&&window.__mediaRefreshRequests.some(value=>value.startsWith('GET /api/admin/image-library?')&&value.includes('category='))",'persisted group filter');
 await value(cdp,"document.querySelector('[data-material-group-value=\"__ungrouped__\"]').click();true");
 await wait(cdp,"!document.querySelector('[data-image-library-cards]')?.textContent.includes('浏览器刷新素材')",'grouped image excluded from ungrouped');
 await value(cdp,"document.querySelector('button[data-image-library-reset]').click();true");
 // Keep backend credential replacement coverage through the authenticated API,
 // without restoring technical controls to the operational page.
 await wait(cdp,"fetch('/api/admin/media-preparations').then(r=>r.json()).then(body=>body.items.some(item=>item.media_id==='fixture-media-1'))",'first fixture credential readback');
 const accepted=await value(cdp,`(async()=>{const csrf=decodeURIComponent(document.cookie.split(';').map(v=>v.trim()).find(v=>v.startsWith('aicrm_admin_csrf=')).slice('aicrm_admin_csrf='.length));const response=await fetch('/api/admin/media-preparations/refresh-rounds',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':csrf,'Idempotency-Key':'browser-media-refresh-round'},body:JSON.stringify({force:true})});return response.status})()`);
 if(accepted!==202)throw new Error('fixture refresh command was not accepted: '+accepted);
 await wait(cdp,"fetch('/api/admin/media-preparations').then(r=>r.json()).then(body=>body.items.some(item=>item.media_id==='fixture-media-2'&&item.credential_usable))",'replacement credential must be usable');
 await cdp.call('Network.enable'); await cdp.call('Network.setCacheDisabled',{cacheDisabled:true});
 await navigate(cdp,`${baseURL}/admin/image-library`); await assertImageLibraryThumbnailLoadingGeometry(cdp);
 if(await value(cdp,"Boolean(document.querySelector('#material-refresh-panel'))"))throw new Error('diagnostics returned after reload');
 await assertImageLibraryThumbnailStates(cdp);
 await captureImageLibraryViewport(cdp,1280,800); await captureImageLibraryViewport(cdp,1440,900); await cdp.call('Emulation.clearDeviceMetricsOverride');
 await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:true}).then(async result=>fs.writeFile(screenshot,Buffer.from(result.data,"base64")));
 // Real navigation must retain the selected material type (the former server redirect bug).
 for (const [tab, page] of [['attachments','attach'],['miniprograms','mpLib']]) {
   await navigate(cdp,`${baseURL}/admin/materials?tab=${tab}`);
   await wait(cdp, `document.body?.dataset.page===${JSON.stringify(page)} && Boolean(document.querySelector('[data-material-group-value="group:"]'))`, `${tab} group sidebar`);
   await value(cdp, `document.querySelector('[data-material-group-value="group:"]').click();true`);
   await wait(cdp, `document.body?.dataset.page===${JSON.stringify(page)} && new URL(location.href).searchParams.has('material_group') && document.querySelector('[data-material-group-value="group:"]')?.getAttribute('aria-pressed')==='true'`, `${tab} ungrouped navigation`);
   await navigate(cdp);
   await wait(cdp, `document.body?.dataset.page===${JSON.stringify(page)} && document.querySelector('[data-material-group-value="group:"]')?.getAttribute('aria-pressed')==='true'`, `${tab} ungrouped reload`);
   await value(cdp, `document.querySelector('[data-material-group-value="all"]').click();true`);
   await wait(cdp, `document.body?.dataset.page===${JSON.stringify(page)} && !new URL(location.href).searchParams.has('material_group') && document.querySelector('[data-material-group-value="all"]')?.getAttribute('aria-pressed')==='true'`, `${tab} all groups navigation`);
 }
 await navigate(cdp,`${baseURL}/admin/operation-cycles`); await wait(cdp,"Boolean([...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情'))","operation cycles");
 await value(cdp,"[...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情').click();true"); await wait(cdp,"Boolean([...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次'))","strategy detail");
 await value(cdp,"[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次').click();true"); await wait(cdp,"Boolean(document.querySelector('dialog[open] input[type=file]'))","new Excel draft dialog");
 await value(cdp,`(()=>{const input=document.querySelector('dialog[open] input[type=file]');const dt=new DataTransfer();const xlsx=Uint8Array.from(atob(${JSON.stringify(xlsxBase64)}),c=>c.charCodeAt(0));dt.items.add(new File([xlsx],'media-refresh.xlsx',{type:'application/vnd.openxmlformats-officedocument.spreadsheetml.sheet'}));input.files=dt.files;input.dispatchEvent(new Event('change'));const fresh=document.querySelector('dialog[open] input[type=checkbox]');fresh.checked=true;fresh.dispatchEvent(new Event('change'));[...document.querySelectorAll('dialog[open] button')].find(b=>b.textContent==='上传并开始审核').click();return true})()`);
 await wait(cdp,"!document.querySelector('dialog[open]')&&document.querySelector('.xeb-detail-main')?.textContent.includes('真实解析第一条草稿')","unapproved Excel draft");
 if(!(await value(cdp,"[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').disabled"))) throw new Error("draft without cover unexpectedly became approvable");
 await value(cdp,`(()=>{const input=document.querySelector('input[aria-label="统一封面图片"]');const png=Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0));const dt=new DataTransfer();dt.items.add(new File([png],'excel-cover.png',{type:'image/png'}));input.files=dt.files;[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='上传统一封面').click();return true})()`);
 await wait(cdp,"document.querySelector('.xeb-detail-main')?.textContent.includes('统一封面已更新')","Excel cover upload");
 if(await value(cdp,"document.querySelector('.xeb-detail-main')?.textContent.includes('企微任务意图已创建')")) throw new Error("draft created a message without approval");
 await exerciseGroupManagement(cdp);
 console.log(`media_refresh_chromium: PASS screenshot=${screenshot}`);
} catch(error) { journeyError=asError(error); } finally {
 const cleanupErrors=[];
 if(cdp) { try { cdp.close(); } catch(error) { cleanupErrors.push(asError(error)); } }
 const browserError=await stopBrowser(browser); if(browserError) cleanupErrors.push(browserError);
 const profileError=await removeProfile(profile); if(profileError) cleanupErrors.push(profileError);
 if(journeyError&&cleanupErrors.length) throw new AggregateError([journeyError,...cleanupErrors],"Media refresh Chromium journey and cleanup failed");
 if(journeyError) throw journeyError;
 if(cleanupErrors.length) throw new AggregateError(cleanupErrors,"Media refresh Chromium cleanup failed");
}
