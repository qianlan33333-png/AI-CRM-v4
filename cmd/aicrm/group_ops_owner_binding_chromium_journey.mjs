import { resolveChromiumBinary } from "../../internal/webshell/chromium_binary.mjs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
const baseURL=process.env.AICRM_GROUPOPS_TEST_URL;
const planID=process.env.AICRM_GROUPOPS_TEST_PLAN_ID;
const screenshotDir=process.env.AICRM_GROUPOPS_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !/^[1-9][0-9]*$/.test(planID || "") || !screenshotDir) throw new Error("isolated installed group fixture required");
const delay=ms=>new Promise(resolve=>setTimeout(resolve,ms));
class CDP {
  constructor(socket) { this.socket = socket; this.nextID = 0; this.pending = new Map(); socket.addEventListener("message", (event) => { const message = JSON.parse(String(event.data)); const pending = this.pending.get(message.id); if (!pending) return; this.pending.delete(message.id); message.error ? pending.reject(new Error("CDP request failed")) : pending.resolve(message.result || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { for (const pending of this.pending.values()) pending.reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}
const waitForPort = async (profile) => { for (let i = 0; i < 160; i += 1) { try { const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch (_) {} await delay(50); } throw new Error("Chromium remote debugging did not become ready"); };
const evaluate = async (cdp, expression) => { const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error("page evaluation failed"); return result.result?.value; };
const waitFor = async (cdp, expression, message) => { for (let i = 0; i < 180; i += 1) { if (await evaluate(cdp, expression)) return; await delay(50); } throw new Error(message); };
const waitForExit = (child, ms) => new Promise((resolve) => { if (child.exitCode !== null || child.signalCode !== null) return resolve(true); const timer = setTimeout(() => resolve(false), ms); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
const asError = (error) => error instanceof Error ? error : new Error(String(error));
const stopBrowser = async (child) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return null;
  try {
    child.kill("SIGTERM");
    if (await waitForExit(child, 3000)) return null;
    if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
    if (await waitForExit(child, 3000)) return null;
    return new Error("Chromium process did not exit before profile cleanup");
  } catch (error) {
    return asError(error);
  }
};
const removeProfile = async (profile) => {
  let lastError;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return null; }
    catch (error) {
      lastError = asError(error);
      if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return lastError;
      await delay(100);
    }
  }
  return new Error(`Chromium test profile cleanup did not complete after 40 attempts: ${lastError?.code || lastError?.message || "unknown error"}`);
};
const profile=await fs.mkdtemp(path.join(os.tmpdir(),"groupops-binding-installed-"));
let browser,cdp,failed;
const responses=[];const deniedResponses=new Map();let searched=false;
try {
  browser=spawn(resolveChromiumBinary(),["--headless=new","--no-sandbox","--remote-debugging-port=0",`--user-data-dir=${profile}`,"--no-first-run","--disable-background-networking","--ignore-certificate-errors","--no-proxy-server","--host-resolver-rules=MAP example.com 127.0.0.1","about:blank"],{stdio:"ignore"});
  const address=await waitForPort(profile);
  const target=await(await fetch(`${address}/json/new?about:blank`,{method:"PUT"})).json();
  const socket=new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve,reject)=>{socket.addEventListener("open",resolve,{once:true});socket.addEventListener("error",reject,{once:true});});
  socket.addEventListener("message",async event=>{
    const message=JSON.parse(String(event.data));
    if(message.method==="Network.responseReceived"){
      const response=message.params.response;const url=new URL(response.url);
      if(url.pathname.startsWith('/api/admin/')){
        const row={path:url.pathname,status:response.status};responses.push(row);
        if(response.status===403)deniedResponses.set(message.params.requestId,row);
        if(url.pathname.endsWith('/group-ops/groups') && url.searchParams.get('q')==='真实群名' && response.status===200)searched=true;
      }
    }
    if(message.method==="Network.loadingFinished" && deniedResponses.has(message.params.requestId)){
      const row=deniedResponses.get(message.params.requestId);deniedResponses.delete(message.params.requestId);
      try{const body=await cdp.call('Network.getResponseBody',{requestId:message.params.requestId});const payload=JSON.parse(body.body);const code=typeof payload.error==='string'?payload.error:payload.error?.code;if(typeof code==='string' && /^[a-z0-9_]+$/.test(code))row.error_code=code;}catch{}
    }
  });
  cdp=new CDP(socket);await cdp.call("Page.enable");await cdp.call("Runtime.enable");await cdp.call("Network.enable");
  await cdp.call("Emulation.setDeviceMetricsOverride",{width:1622,height:1007,deviceScaleFactor:1,mobile:false});
  const planPath=`/admin/automation-conversion/group-ops/plans/${planID}`;
  await cdp.call("Page.navigate",{url:`${baseURL}/login?next=${encodeURIComponent(planPath)}`});
  await waitFor(cdp,`Boolean(document.querySelector('input[name="login_csrf_token"]'))`,"installed login not rendered");
  await evaluate(cdp,`(()=>{document.querySelector('input[name="username"]').value="public-survey-browser";document.querySelector('input[name="password"]').value="isolated-survey-admin-password";document.querySelector('form[action="/login"]').requestSubmit();return true;})()`);
  await waitFor(cdp,`Boolean(document.querySelector('[data-action="open-group-picker"]'))`,"installed group plan not loaded");
  await evaluate(cdp,`document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();true`);
  await evaluate(cdp,`document.querySelector('[data-action="open-group-picker"]').click();true`);
  await waitFor(cdp,`Boolean(document.querySelector('[data-v3-group-key$="binding-owned"]'))`,"owner group missing from picker");
  if(await evaluate(cdp,`Boolean(document.querySelector('[data-v3-group-key$="binding-other"]'))`))throw new Error("other owner escaped server scope");
  await evaluate(cdp,`(()=>{const input=document.querySelector('[data-v3-picker-search-input]');input.value="真实群名";input.dispatchEvent(new Event('input',{bubbles:true}));input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',bubbles:true}));return true;})()`);
  for(let attempt=0;attempt<180&&!searched;attempt+=1)await delay(50);
  if(!searched)throw new Error("server search response not observed");
  await waitFor(cdp,`document.querySelector('[data-v3-group-status]')?.textContent.includes("符合筛选 1 个群") && !document.querySelector('[data-v3-group-confirm]').disabled`,"server search total/ready state not shown");
  await evaluate(cdp,`document.querySelector('[data-v3-group-key$="binding-owned"]').click();true`);
  await waitFor(cdp,`document.querySelectorAll('[data-v3-group-remove]').length===1 && !document.querySelector('[data-v3-group-confirm]').disabled`,"selected group/confirmation readiness missing");
  await evaluate(cdp,`document.querySelector('[data-v3-group-confirm]').click();true`);
  await waitFor(cdp,`!document.querySelector('[data-v3-selection-session="group"]') && document.querySelector(".group-ops__group-name")?.textContent.includes("真实群名")`,"binding save/readback missing");
  await fs.mkdir(screenshotDir,{recursive:true});
  const shot=await cdp.call("Page.captureScreenshot",{format:"png",captureBeyondViewport:true});await fs.writeFile(path.join(screenshotDir,"group-binding-installed.png"),Buffer.from(shot.data,"base64"));
  await cdp.call("Page.navigate",{url:baseURL+planPath});
  await waitFor(cdp,`document.querySelector(".group-ops__group-name")?.textContent.includes("真实群名")`,"reopening lost group name");
  await evaluate(cdp,`document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();document.querySelector('[data-action="open-group-picker"]').click();true`);
  await waitFor(cdp,`Boolean(document.querySelector('[data-v3-group-remove]'))`,"selected group not prefilled on reopen");
  await evaluate(cdp,`document.querySelector('[data-v3-group-remove]').click();true`);
  await waitFor(cdp,`document.querySelectorAll('[data-v3-group-remove]').length===0 && !document.querySelector('[data-v3-group-confirm]').disabled`,"removal/confirmation readiness missing");
  await evaluate(cdp,`document.querySelector('[data-v3-group-confirm]').click();true`);
  await waitFor(cdp,`!document.querySelector('[data-v3-selection-session="group"]') && !document.querySelector(".group-ops__group-name")`,"removal save/readback missing");
  console.log("installed-group-binding-chromium PASS owner-scope search total add reopen name remove persistence");
} catch(error) {
  failed=error;
  if(cdp)try{
    const state=await evaluate(cdp,`(()=>({origin:location.origin,csrfPresent:document.cookie.split(';').some(item=>item.trim().startsWith('aicrm_admin_csrf=')),modal:!!document.querySelector('[data-v3-selection-session="group"]'),selected:document.querySelectorAll('[data-v3-group-remove]').length,rowDisabled:document.querySelector('[data-v3-group-key$="binding-owned"]')?.disabled,confirmDisabled:document.querySelector('[data-v3-group-confirm]')?.disabled,pickerStatus:document.querySelector('[data-v3-group-status]')?.textContent,notice:document.querySelector('.group-ops__notice')?.textContent,boundNames:Array.from(document.querySelectorAll('.group-ops__group-name')).map(row=>row.textContent)}))()`);
    console.error(JSON.stringify({fixture_dom:state,api_responses:responses.slice(-15)}));
    await fs.mkdir(screenshotDir,{recursive:true});const shot=await cdp.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:true});await fs.writeFile(path.join(screenshotDir,'group-binding-failure.png'),Buffer.from(shot.data,'base64'));
  }catch(diagnosticError){console.error('fixture diagnostic unavailable');}
} finally {
  cdp?.close();const stopped=await stopBrowser(browser);if(stopped&&!failed)failed=stopped;
  if(!stopped){const cleaned=await removeProfile(profile);if(cleaned)console.warn("temporary profile cleanup warning");}
}
if(failed)throw failed;
