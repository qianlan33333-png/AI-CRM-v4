import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_COMPONENT_STATES_TEST_URL;
const username = process.env.AICRM_COMPONENT_STATES_TEST_USERNAME;
const password = process.env.AICRM_COMPONENT_STATES_TEST_PASSWORD;
const screenshotDirectory = process.env.AICRM_COMPONENT_STATES_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !path.isAbsolute(screenshotDirectory || "")) {
  throw new Error("component states Chromium journey requires HTTPS URL, credentials, and an absolute screenshot directory");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const browserBinary = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/") && spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate;
      if (!candidate.includes("/") && spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
    } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
};

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
    for (const current of this.pending.values()) current.reject(new Error("CDP closed"));
    this.pending.clear();
    this.socket.close();
  }
}

async function debuggingAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^[0-9]+$/.test(value)) return "http://127.0.0.1:" + value;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
}

async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
}

async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
}

async function stopBrowser(browser) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return;
  browser.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => browser.once("exit", resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGKILL");
    await Promise.race([new Promise((resolve) => browser.once("exit", resolve)), delay(1000)]);
  }
}

async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false;
      await delay(100);
    }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-component-states-chromium-"));
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
  const exceptions = [];
  const apiRequests = [];
  cdp.on("Runtime.exceptionThrown", (params) => {
    const kind = String(params.exceptionDetails?.exception?.className || params.exceptionDetails?.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (exceptions.length < 8) exceptions.push(kind);
  });
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const request = new URL(String(params.request?.url || ""));
      if (request.origin === new URL(baseURL).origin && request.pathname.startsWith("/api/")) apiRequests.push(request.pathname);
    } catch (_) {}
  });
  const capture = async (name) => {
    const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(image.data, "base64"), { mode: 0o600 });
  };
  const resize = (width, height = 900) => cdp.call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: height });
  await resize(1440);
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=%2Fadmin%2Fcomponent-states" });
  await waitFor(cdp, 'Boolean(document.querySelector(\'form[action="/login"] input[name="login_csrf_token"]\'))', "login shell did not render");
  await evaluate(cdp, "(() => { document.querySelector('input[name=\"username\"]').value=" + JSON.stringify(username) + "; document.querySelector('input[name=\"password\"]').value=" + JSON.stringify(password) + "; document.querySelector('form[action=\"/login\"]').requestSubmit(); return true; })()");
  await waitFor(cdp, 'Boolean(document.querySelector(\'[data-component-states-root][data-component-states-ready="true"]\'))', "authenticated component state Host did not mount");
  await waitFor(cdp, "document.fonts.ready", "component state fonts did not settle");
  const page = await evaluate(cdp, "(() => ({path:location.pathname,page:document.body.dataset.page,root:Boolean(document.querySelector('[data-component-states-root]')),tokens:Array.from(document.styleSheets).some(sheet=>String(sheet.href||'').includes('sharedVisualTokens')),host:Array.from(document.scripts).some(script=>String(script.src||'').includes('componentStatesHost')),presentation:Array.from(document.styleSheets).some(sheet=>String(sheet.href||'').includes('presentationStyles'))}))()");
  if (page.path !== "/admin/component-states" || page.page !== "component-states" || !page.root || !page.tokens || !page.host || !page.presentation) {
    throw new Error("authenticated component state route did not use its real resource closure");
  }

  for (const width of [1280, 1440, 360, 420]) {
    await resize(width);
    await evaluate(cdp, "new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
    const layout = await evaluate(cdp, "(() => { const root=document.querySelector('.component-states'); const grid=document.querySelector('.component-states__state-grid'); const intro=document.querySelector('.component-states__intro'); const actions=document.querySelector('.component-states__actions'); const columns=String(getComputedStyle(grid).gridTemplateColumns).trim().split(/\\s+/).filter(Boolean).length; const buttons=[...actions.querySelectorAll('button')].map(button=>button.getBoundingClientRect()); return {root:Boolean(root),cards:grid?.children.length||0,columns,overflow:document.documentElement.scrollWidth>innerWidth+1,introWidth:intro?.getBoundingClientRect().width||0,buttonWidths:buttons.map(box=>Math.round(box.width)),viewport:innerWidth}; })()");
    const expectedColumns = width <= 600 ? 1 : 4;
    if (!layout.root || layout.cards !== 7 || layout.columns !== expectedColumns || layout.overflow || layout.introWidth < 100 || layout.buttonWidths.some((value) => value < 60)) {
      throw new Error("component state responsive layout invalid at " + width + ": " + JSON.stringify(layout));
    }
    await capture("component-states-" + width);
  }

  await resize(1440);
  const click = (selector) => evaluate(cdp, "(() => { const node=document.querySelector(" + JSON.stringify(selector) + "); if (!(node instanceof HTMLElement)) return false; node.focus({preventScroll:true}); node.click(); return true; })()");
  if (!await click('[data-component-states-mode="error"]') || !await click('[data-component-states-group-open]')) throw new Error("group error controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"group\"]')?.textContent.includes('群聊目录暂时不可用')", "group error mode did not expose a real loader failure");
  await capture("component-states-error-retry");
  if (!await click('[data-v3-selection-session="group"] [data-v3-group-reload]')) throw new Error("group retry control was unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"group\"]')?.textContent.includes('北区新品体验群')", "group retry did not use the second local success result");
  if (!await click('[data-v3-selection-session="group"] [data-v3-group-key]') || !await click('[data-v3-selection-session="group"] [data-v3-group-confirm]')) throw new Error("group local commit controls were unavailable");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"group\"]')", "group dialog did not close after local confirmation");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-group-open]')")) throw new Error("group confirmation did not return focus to the mounted trigger");

  if (!await click('[data-component-states-mode="forbidden"]') || !await click('[data-component-states-material-open]')) throw new Error("material forbidden controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"material\"]')?.textContent.includes('目录权限已收回')", "403 material mode did not remain explicit");
  const forbidden = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"material\"]'); return {selected:mask?.textContent.includes('秋日活动封面'),disabled:mask?.querySelector('[data-v3-picker-confirm]')?.disabled}; })()");
  if (!forbidden.selected || !forbidden.disabled) throw new Error("403 material mode did not preserve the draft and lock confirmation");
  await capture("component-states-forbidden");
  if (!await click('[data-v3-selection-session="material"] [data-v3-picker-cancel]')) throw new Error("material cancel was unavailable");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-material-open]')")) throw new Error("material cancel did not return focus to the mounted trigger");

  if (!await click('[data-component-states-mode="ready"]') || !await click('[data-component-states-tag-open]')) throw new Error("tag controls were unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-key]'))", "tag local selector did not open");
  const tagCandidate = await evaluate(cdp, "(() => { const search=document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-picker-search-input]'); search.focus(); search.value='服务'; search.dispatchEvent(new Event('input',{bubbles:true})); search.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true})); search.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',isComposing:true}); search.dispatchEvent(event); return event.defaultPrevented; })()");
  if (tagCandidate) throw new Error("tag IME candidate Enter was intercepted");
  await delay(0);
  const tagEnter = await evaluate(cdp, "(() => { const search=document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-picker-search-input]'); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter'}); search.dispatchEvent(event); return event.defaultPrevented; })()");
  if (!tagEnter) throw new Error("tag ordinary Enter did not submit the local directory search");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"tag\"] [data-v3-tag-list]')?.textContent.includes('需要跟进')", "tag Enter query did not show the local search result");
  await resize(420);
  await capture("component-states-tag-420");
  await resize(1440);
  if (!await click('[data-v3-selection-session="tag"] [data-v3-tag-key]') || !await click('[data-v3-selection-session="tag"] [data-v3-tag-confirm]')) throw new Error("tag local confirmation controls were unavailable");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"tag\"]')", "tag local confirmation did not close its dialog");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-tag-open]')")) throw new Error("tag confirmation did not return focus to the mounted trigger");
  if (!await evaluate(cdp, "document.querySelector('[data-component-states-tag-result]')?.textContent.includes('需要跟进')")) throw new Error("tag confirmation did not update only the local summary");

  if (!await click('[data-component-states-mode="ready"]') || !await click('[data-component-states-staff-open]')) throw new Error("staff controls were unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"staff\"] [data-v3-staff-key]'))", "staff local selector did not open");
  const staffCandidate = await evaluate(cdp, "(() => { const search=document.querySelector('[data-v3-selection-session=\"staff\"] [data-v3-picker-search-input]'); search.focus(); search.value='增长'; search.dispatchEvent(new Event('input',{bubbles:true})); search.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true})); search.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true})); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',isComposing:true}); search.dispatchEvent(event); return event.defaultPrevented; })()");
  if (staffCandidate) throw new Error("staff IME candidate Enter was intercepted");
  await delay(0);
  const staffEnter = await evaluate(cdp, "(() => { const search=document.querySelector('[data-v3-selection-session=\"staff\"] [data-v3-picker-search-input]'); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter'}); search.dispatchEvent(event); return event.defaultPrevented; })()");
  if (!staffEnter) throw new Error("staff ordinary Enter did not submit the local directory search");
  await waitFor(cdp, "(() => { const list=document.querySelector('[data-v3-selection-session=\"staff\"] [data-v3-staff-list]')?.textContent||''; const confirm=document.querySelector('[data-v3-selection-session=\"staff\"] [data-v3-staff-confirm]'); return list.includes('增长客服') && !list.includes('北区客服') && !confirm?.disabled; })()", "staff Enter query did not settle to the local result");
  await capture("component-states-staff-1440");
  if (!await click('[data-v3-selection-session="staff"] [data-v3-staff-key]') || !await click('[data-v3-selection-session="staff"] [data-v3-staff-confirm]')) throw new Error("staff local confirmation controls were unavailable");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"staff\"]')", "staff local confirmation did not close its dialog");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-staff-open]')")) throw new Error("staff confirmation did not return focus to the mounted trigger");
  if (!await evaluate(cdp, "document.querySelector('[data-component-states-staff-result]')?.textContent.includes('增长客服')")) throw new Error("staff confirmation did not update only the local summary");

  if (!await click('[data-component-states-mode="invalid"]') || !await click('[data-component-states-staff-open]')) throw new Error("staff invalid controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"staff\"]')?.textContent.includes('待目录确认的失效员工')", "staff invalid selection was not retained");
  const invalidStaff = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"staff\"]'); return {reason:mask?.textContent.includes('缺少可信企微 UserID'),disabled:mask?.querySelector('[data-v3-staff-confirm]')?.disabled}; })()");
  if (!invalidStaff.reason || !invalidStaff.disabled) throw new Error("staff invalid selection was not explicit and locked");
  if (!await click('[data-v3-selection-session="staff"] [data-v3-staff-cancel]')) throw new Error("staff invalid cancellation was unavailable");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-staff-open]')")) throw new Error("staff invalid cancellation did not return focus to the mounted trigger");

  if (!await click('[data-component-states-mode="ready"]') || !await click('[data-component-states-composer-open]')) throw new Error("content composer controls were unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-content-composer] [data-v3-composer-text]'))", "content composer did not open");
  const unknownComposerVariable = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-content-composer]'); const text=mask.querySelector('[data-v3-composer-text]'); text.value='未授权变量 {{unknown_demo}}'; text.dispatchEvent(new Event('input',{bubbles:true})); return {disabled:mask.querySelector('[data-v3-composer-confirm]').disabled,reason:mask.textContent.includes('示例变量不在当前本地目录中')}; })()");
  if (!unknownComposerVariable.disabled || !unknownComposerVariable.reason) throw new Error("content composer did not block an unknown caller token");
  const composerDraft = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-content-composer]'); const text=mask.querySelector('[data-v3-composer-text]'); text.value='你好，{{customer_name}}，欢迎查看{{plan_name}}。'; text.dispatchEvent(new Event('input',{bubbles:true})); mask.querySelector('[data-v3-composer-variable]').click(); mask.querySelector('[data-v3-composer-move=\"1:-1\"]').click(); return {text:mask.querySelector('[data-v3-composer-text]').value,first:mask.querySelector('[data-v3-composer-records] strong')?.textContent,disabled:mask.querySelector('[data-v3-composer-confirm]').disabled}; })()");
  if (composerDraft.disabled || !composerDraft.text.includes('{{customer_name}}') || composerDraft.first !== '附件：活动说明 PDF') throw new Error("content composer did not retain caller-provided variable or local order");
  const composerNoThumbnailDesktop = await evaluate(cdp, "(() => { const item=document.querySelector('[data-v3-content-composer] [data-content-material-key=\"media-library:attachment:201\"]'); const details=item?.querySelector('.aicrm-content-presentation__material-details'); if (!(item instanceof HTMLElement) || !(details instanceof HTMLElement)) return {ready:false}; const columns=String(getComputedStyle(item).gridTemplateColumns).trim().split(/\s+/).filter(Boolean); const itemBox=item.getBoundingClientRect(); const detailsBox=details.getBoundingClientRect(); return {ready:true,singleColumn:columns.length===1,noVisual:!item.querySelector('.aicrm-content-presentation__visual'),fullDetails:detailsBox.width>=itemBox.width-24}; })()");
  if (!composerNoThumbnailDesktop.ready || !composerNoThumbnailDesktop.singleColumn || !composerNoThumbnailDesktop.noVisual || !composerNoThumbnailDesktop.fullDetails) throw new Error("desktop content presentation left a phantom thumbnail column: " + JSON.stringify(composerNoThumbnailDesktop));
  await resize(360);
  const composerNarrowPreview = await evaluate(cdp, "(() => { const dialog=document.querySelector('[data-v3-content-composer] .aicrm-content-composer'); const body=dialog?.querySelector('.aicrm-content-composer__body'); const preview=dialog?.querySelector('[data-v3-composer-preview]'); const footer=dialog?.querySelector('footer'); const item=preview?.querySelector('[data-content-material-key=\"media-library:attachment:201\"]'); const details=item?.querySelector('.aicrm-content-presentation__material-details'); if (!(body instanceof HTMLElement) || !(preview instanceof HTMLElement) || !(footer instanceof HTMLElement) || !(item instanceof HTMLElement) || !(details instanceof HTMLElement)) return {ready:false}; body.scrollTop=body.scrollHeight; const bodyBox=body.getBoundingClientRect(); const previewBox=preview.getBoundingClientRect(); const itemBox=item.getBoundingClientRect(); const detailsBox=details.getBoundingClientRect(); const confirm=footer.querySelector('[data-v3-composer-confirm]')?.getBoundingClientRect(); const columns=String(getComputedStyle(item).gridTemplateColumns).trim().split(/\s+/).filter(Boolean); return {ready:true,scrollable:body.scrollHeight>body.clientHeight+1,scrolled:body.scrollTop>0,previewFits:previewBox.height<=bodyBox.height+1,previewVisible:previewBox.top>=bodyBox.top-1&&previewBox.bottom<=bodyBox.bottom+1,confirmVisible:Boolean(confirm&&confirm.top>=0&&confirm.bottom<=innerHeight),singleColumn:columns.length===1,noVisual:!item.querySelector('.aicrm-content-presentation__visual'),fullDetails:detailsBox.width>=itemBox.width-24,overflow:document.documentElement.scrollWidth>innerWidth+1}; })()");
  if (!composerNarrowPreview.ready || !composerNarrowPreview.scrollable || !composerNarrowPreview.scrolled || !composerNarrowPreview.previewFits || !composerNarrowPreview.previewVisible || !composerNarrowPreview.confirmVisible || !composerNarrowPreview.singleColumn || !composerNarrowPreview.noVisual || !composerNarrowPreview.fullDetails || composerNarrowPreview.overflow) throw new Error("narrow content composer could not scroll to its complete no-thumbnail preview and fixed confirmation: " + JSON.stringify(composerNarrowPreview));
  await capture("component-states-composer-360");
  await resize(1440);
  if (!await click('[data-v3-content-composer] [data-v3-composer-confirm]')) throw new Error("content composer confirmation was unavailable");
  await waitFor(cdp, "!document.querySelector('[data-v3-content-composer]')", "content composer confirmation did not close");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-composer-open]')")) throw new Error("content composer confirmation did not return focus to the mounted trigger");
  if (!await evaluate(cdp, "(() => { const summary=document.querySelector('[data-component-states-composer-result]')?.textContent||''; const preview=document.querySelector('[data-component-states-composer-preview]')?.textContent||''; return summary.indexOf('活动说明 PDF') < summary.indexOf('秋日活动封面') && preview.includes('不会保存或发送'); })()")) throw new Error("content composer did not preserve local order or zero-effect preview");
  if (!await click('[data-component-states-composer-readonly]')) throw new Error("content readonly trigger was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-content-readonly]'))", "content readonly presentation did not open");
  const readonlyContent = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-content-readonly]'); return {editor:Boolean(mask?.querySelector('[data-v3-composer-text]')),note:mask?.textContent.includes('未保存、未发送')}; })()");
  if (readonlyContent.editor || !readonlyContent.note) throw new Error("content readonly presentation exposed editing or omitted its local-only notice");
  await capture("component-states-composer-readonly-1440");
  if (!await click('[data-v3-content-readonly] [data-v3-content-readonly-close]')) throw new Error("content readonly close was unavailable");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-composer-readonly]')")) throw new Error("content readonly close did not return focus to the mounted trigger");

  if (!await click('[data-component-states-form-open]')) throw new Error("form demo trigger was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"component-states\"] [data-component-states-form-textarea]'))", "form focus demo did not open");
  const form = await evaluate(cdp, "(() => { const mask=document.querySelector('[data-v3-selection-session=\"component-states\"]'); const search=mask?.querySelector('[data-component-states-ime-input]'); const candidate=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',isComposing:true}); search?.dispatchEvent(candidate); return {select:Boolean(mask?.querySelector('[data-component-states-form-select]')),textarea:Boolean(mask?.querySelector('[data-component-states-form-textarea]')),editable:Boolean(mask?.querySelector('[data-component-states-form-editable]')),candidatePrevented:candidate.defaultPrevented}; })()");
  if (!form.select || !form.textarea || !form.editable || form.candidatePrevented) throw new Error("form demo did not preserve IME or shared focus controls");
  await evaluate(cdp, "new Promise((resolve) => setTimeout(resolve, 0))");
  const normalEnter = await evaluate(cdp, "(() => { const search=document.querySelector('[data-component-states-ime-input]'); const event=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter'}); search.dispatchEvent(event); return event.defaultPrevented; })()");
  if (!normalEnter) throw new Error("ordinary form search Enter did not use the local SelectionSession loader");
  if (!await click('[data-v3-selection-session="component-states"] [data-component-states-choice]') || !await click('[data-v3-selection-session="component-states"] [data-component-states-confirm]')) throw new Error("form local commit controls were unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-selection-session=\"component-states\"]')?.textContent.includes('已提交本地示例会话')", "form confirmation did not commit the local SelectionSession");
  await capture("component-states-form");
  if (!await click('[data-v3-selection-session="component-states"] [data-component-states-close]')) throw new Error("form cancel was unavailable");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-component-states-form-open]')")) throw new Error("form cancel did not return focus to the mounted trigger");

  if (apiRequests.length || exceptions.length) throw new Error("local component demo issued API requests or exceptions: " + JSON.stringify({ apiRequests, exceptions }));
  console.log("component_states_chromium: PASS screenshots=" + screenshotDirectory);
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium profile cleanup did not complete");
}
