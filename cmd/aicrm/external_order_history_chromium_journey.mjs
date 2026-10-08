import { resolveChromiumBinary } from "../../internal/webshell/chromium_binary.mjs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";

const baseURL = process.env.AICRM_EXTERNAL_HISTORY_TEST_URL;
const username = "groupops-browser-owner";
const password = "groupops-browser-owner-password";
const packageID = "1";
const screenshotDirectory = process.env.AICRM_EXTERNAL_HISTORY_SCREENSHOTS;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(packageID || "") || !path.isAbsolute(screenshotDirectory || "")) {
  throw new Error("audience confirmation Chromium journey requires HTTPS URL, credentials, package ID, and absolute screenshots");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const browserBinary = resolveChromiumBinary;

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const current = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) current.reject(new Error("CDP " + message.error.code));
        else current.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  on(method, listener) {
    const listeners = this.events.get(method) || [];
    listeners.push(listener);
    this.events.set(method, listeners);
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    this.socket.close();
  }
}

async function debuggingAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^[0-9]+$/.test(port)) return "http://127.0.0.1:" + port;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
}

let journeyPhase = "browser-launch";
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) {
    const details = result.exceptionDetails;
    const description = details.exception?.description || details.exception?.className || details.text || "unknown";
    let page = {};
    try {
      const context = await cdp.call("Runtime.evaluate", {
        expression: "({route:location.pathname,readyState:document.readyState,profileRoot:Boolean(document.querySelector('[data-customer-profile-root]')),linkCount:document.querySelectorAll('a[href]').length})",
        returnByValue: true,
      });
      page = context.result?.value || {};
    } catch (_) {}
    throw new Error("page evaluation failed phase=" + journeyPhase + " description=" + String(description) + " page=" + JSON.stringify(page));
  }
  return result.result?.value;
}

async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, "Boolean(document.body) && (" + expression + ")")) return;
    await delay(50);
  }
  throw new Error(message + " page=" + JSON.stringify(await evaluate(cdp,"({route:location.pathname,core:document.querySelector('#coreOperationsRoot')?.textContent,scripts:[...document.scripts].map(s=>s.src)})")));
}

function customerProfileURL(links, baseURL) {
  for (const href of links) {
    if (typeof href !== 'string' || !href.trim()) continue;
    const url = new URL(href, baseURL);
    if (/^\/admin\/customers\/[1-9][0-9]*$/.test(url.pathname)) return url.href;
  }
}

async function waitForBrowserExit(browser, timeoutMilliseconds) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    browser.once("exit", () => { clearTimeout(timer); resolve(true); });
  });
}

async function stopBrowser(browser) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return true;
  browser.kill("SIGTERM");
  if (await waitForBrowserExit(browser, 3000)) return true;
  if (browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGKILL");
    return waitForBrowserExit(browser, 1000);
  }
  return true;
}

async function removeProfile(profile) {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!['ENOTEMPTY', 'EBUSY', 'EPERM'].includes(error?.code)) return false;
      await delay(100);
    }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-external-history-chromium-"));
let browser;
let cdp;
let failed = false;
try {
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", "--user-data-dir=" + profile, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const target = await (await fetch((await debuggingAddress(profile)) + "/json/new?about:blank", { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");

  const capture=async name=>{const image=await cdp.call('Page.captureScreenshot',{format:'png',captureBeyondViewport:false});await fs.writeFile(path.join(screenshotDirectory,name+'.png'),Buffer.from(image.data,'base64'),{mode:0o600})};
  journeyPhase = "login-load";
  await cdp.call('Page.navigate',{url:baseURL+'/login?next=%2Fadmin%2Forders.html'});
  await waitFor(cdp,'Boolean(document.querySelector(\'input[name="login_csrf_token"]\'))','login missing');
  journeyPhase = "login-submit";
  await evaluate(cdp,`document.querySelector('input[name="username"]').value=${JSON.stringify(username)};document.querySelector('input[name="password"]').value=${JSON.stringify(password)};document.querySelector('form[action="/login"]').requestSubmit();true`);
  journeyPhase = "history-list";
  await waitFor(cdp,"location.pathname.includes('orders') && document.body.textContent.includes('external-browser-known')",'history list missing');
  const list=await evaluate(cdp,"document.body.textContent");
  if(list.includes('external-browser-unknown')||list.includes('external-browser-refund')||!list.includes('外部历史订单')||!list.includes('2024-01-02 03:04:05'))throw new Error('list original time/source missing');
  await capture('external-history-list');
  for(const [ref,time,amount] of [['external-browser-known','2024-01-02 03:04:05','¥123.45']]){
    journeyPhase = "history-detail";
    await cdp.call('Page.navigate',{url:baseURL+'/admin/orderDetail.html?id='+ref+'&provider=external_history'});
    await waitFor(cdp,"document.body.textContent.includes('原支付时间') && document.body.textContent.includes("+JSON.stringify(ref)+")",'history detail missing');
    const page=await evaluate(cdp,"({body:document.body.textContent,controls:[...document.querySelectorAll('button')].map(b=>b.textContent),links:[...document.querySelectorAll('a[href]')].map(a=>a.getAttribute('href')).filter(href=>typeof href==='string' && href.trim())})");
    if(!page.body.includes(time)||!page.body.includes(amount)||!page.body.includes('导入时间')||!page.body.includes('外部历史订单')||!page.body.includes('138****0077'))throw new Error('detail history facts missing');
    if(ref==='external-browser-known' && !page.body.includes('原组合商品'))throw new Error('refund/bundle summary missing');
    if(page.controls.some(t=>/退款|重发|重试推送/.test(t)))throw new Error('historical executable action present');
    const profileURL = customerProfileURL(page.links, baseURL);
    if (!profileURL || page.links.some(link => link.includes('customerDetail.html'))) throw new Error('canonical customer link missing');
    const customerID = new URL(profileURL).pathname.split('/').pop();
    journeyPhase = "retired-profile-fetch";
    const retired = await evaluate(cdp, `(async()=>{const r=await fetch('/admin/customerDetail.html?id=${customerID}',{redirect:'manual'});return {status:r.status,location:r.headers.get('Location')}})()`);
    if (retired.status !== 410 || retired.location) throw new Error('retired profile served or redirected');
    journeyPhase = "formal-profile-history-wait";
    await cdp.call('Page.navigate', {url: profileURL});
    await waitFor(cdp, "Boolean(document.querySelector('[data-customer-profile-root]')) && [...document.querySelectorAll('a[href]')].some(a=>new URL(a.href).searchParams.get('id') === " + JSON.stringify(ref) + ")", 'formal customer history link missing');
    journeyPhase = "formal-profile-order-link-read";
    const orderURL = await evaluate(cdp, "[...document.querySelectorAll('a[href]')].find(a=>new URL(a.href).searchParams.get('id') === " + JSON.stringify(ref) + ").href");
    if (new URL(orderURL).searchParams.get('provider') !== 'external_history') throw new Error('formal profile history provider changed');
    await capture('canonical-customer-history');
    journeyPhase = "order-profile-roundtrip";
    await cdp.call('Page.navigate', {url: orderURL});
    await waitFor(cdp, "Boolean(document.querySelector('a[href=\"/admin/customers/" + customerID + "\"]')) && document.body.textContent.includes(" + JSON.stringify(ref) + ")", 'order roundtrip changed Customer ID');
    await capture(ref);
  }
  journeyPhase = "history-export";
  const exported=await evaluate(cdp,`(async()=>{const cookie=document.cookie.split('; ').find(v=>v.startsWith('aicrm_admin_csrf='))||document.cookie.split('; ').find(v=>v.startsWith('csrf_token='));const token=cookie?decodeURIComponent(cookie.split('=').slice(1).join('=')):'';const r=await fetch('/api/admin/exports',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':token,'Idempotency-Key':'external-history-browser-export'},body:JSON.stringify({resource:'orders',format:'csv',filter:{provider:'external_history'}})});return {status:r.status,body:await r.text()}})()`);
  if(exported.status!==200||exported.body.includes('external-browser-unknown')||exported.body.includes('external-browser-refund')||!exported.body.includes('2024-01-02 03:04:05')||!exported.body.includes('原组合商品')||!exported.body.includes('123.45'))throw new Error('CSV original facts missing: '+exported.status);
  console.log('external_order_history_chromium: PASS screenshots='+screenshotDirectory);
} catch (error) {
  failed = true;
  console.error("external_order_history_chromium: FAIL phase=" + journeyPhase);
  throw error;
} finally {
  if (cdp) cdp.close();
  const browserExited = await stopBrowser(browser);
  if (!browserExited && !failed) throw new Error('Chromium did not exit before audience-confirmation profile cleanup');
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error('Chromium profile cleanup did not complete');
}
