import { resolveChromiumBinary } from "../../internal/webshell/chromium_binary.mjs";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";

const baseURL = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_URL;
const username = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME;
const password = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD;
const packageID = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID;
const screenshotDirectory = process.env.AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR;
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

async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed: " + String(result.exceptionDetails.text || "unknown"));
  return result.result?.value;
}

async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  const diagnostic = await evaluate(cdp, "({path:location.pathname,status:document.querySelector('#templateStatusLine')?.textContent,note:document.querySelector('#retiredQuestionnaireOwnerNote')?.textContent,noteHidden:document.querySelector('#retiredQuestionnaireOwnerNote')?.hidden,refreshDisabled:document.querySelector('#manualRefreshBtn')?.disabled,fields:[...document.querySelectorAll('#templateParameterForm [data-field-name]')].map(x=>x.dataset.fieldName)})");
  throw new Error(message + ": " + JSON.stringify(diagnostic));
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

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-audience-confirmation-chromium-"));
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
  const requests = [];
  const exceptions = [];
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const request = new URL(String(params.request?.url || ""));
      if (request.origin === new URL(baseURL).origin && request.pathname.startsWith("/api/")) requests.push({ path: request.pathname, method: params.request?.method || "GET" });
    } catch (_) {}
  });
  cdp.on("Runtime.exceptionThrown", (params) => {
    if (exceptions.length < 8) exceptions.push(String(params.exceptionDetails?.text || "runtime_exception"));
  });
  const resize = (width, height = 900) => cdp.call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: height });
  const capture = async (name) => {
    const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(image.data, "base64"), { mode: 0o600 });
  };
  const click = (selector) => evaluate(cdp, "(() => { const node=document.querySelector(" + JSON.stringify(selector) + "); if (!(node instanceof HTMLElement)) return false; node.focus({preventScroll:true}); node.click(); return true; })()");

  await resize(1440);
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=%2Fadmin%2Fautomation-conversion" });
  await waitFor(cdp, 'Boolean(document.querySelector(\'form[action="/login"] input[name="login_csrf_token"]\'))', "login shell did not render");
  await evaluate(cdp, "(() => { document.querySelector('input[name=\"username\"]').value=" + JSON.stringify(username) + "; document.querySelector('input[name=\"password\"]').value=" + JSON.stringify(password) + "; document.querySelector('form[action=\"/login\"]').requestSubmit(); return true; })()");
  await waitFor(cdp, "Boolean(window.AICRMConfirmation?.confirm) && Boolean(document.querySelector('[data-action=\"archive\"][data-package-id=\"" + packageID + "\"]'))", "authenticated Audience shell did not mount its real confirmation asset and package row");
  if (!await click('[data-audience-workspace-tab="packages"]')) throw new Error("audience management tab was unavailable");
  await waitFor(cdp, "!document.querySelector('#audiencePackagePanel').hidden && document.querySelector('#coreProductPanel').hidden", "audience management tab did not select its exclusive panel");
  const closure = await evaluate(cdp, "(() => ({css:[...document.styleSheets].some(sheet=>String(sheet.href||'').includes('confirmationDialogStyles-')),script:[...document.scripts].some(script=>String(script.src||'').includes('confirmationDialogHost-')),route:location.pathname}))()");
  if (!closure.css || !closure.script || closure.route !== "/admin/automation-conversion") throw new Error("Audience shell did not load the real confirmation asset closure: " + JSON.stringify(closure));

  if (process.env.AICRM_AUDIENCE_TEST_SCENARIO === "survey-owner-retirement") {
    await cdp.call("Page.navigate", { url: baseURL + "/admin/automation-conversion/packages/" + packageID });
    await waitFor(cdp, "document.querySelector('#retiredQuestionnaireOwnerNote')?.textContent.includes('已失效') && Boolean(document.querySelector('[data-field-name=questionnaires]'))", "retired owner configuration did not show an actionable explanation");
    if (!await evaluate(cdp, "!document.querySelector('[data-field-name=owner_scope]') && !document.querySelector('[data-field-name=owner_userids]') && document.querySelector('#manualRefreshBtn').disabled")) throw new Error("employee controls or refresh remain enabled for retired configuration: " + JSON.stringify(await evaluate(cdp,"({owner:!!document.querySelector('[data-field-name=owner_scope]'),user:!!document.querySelector('[data-field-name=owner_userids]'),disabled:document.querySelector('#manualRefreshBtn')?.disabled})")));
    if (!await evaluate(cdp, "document.querySelector('#packageDefinitionInput').closest('.ai-field').hidden")) throw new Error("retired raw employee filter editor remained visible");
    await evaluate(cdp, "document.querySelector('#retiredQuestionnaireOwnerNote').scrollIntoView({block:'center'})");
    await capture("survey-retired-owner-before");
    const countWrites = () => requests.filter((r) => ['PATCH','PUT'].includes(r.method) && r.path.includes('/packages/' + packageID)).length;
    const before = countWrites();
    await click('#savePackageBtn');
    await waitFor(cdp, "document.querySelector('#templateStatusLine')?.textContent.includes('请先选择重新配置')", "unchanged old employee configuration was not blocked before save");
    if (countWrites() !== before || requests.some((r) => r.path.includes('/owner-references'))) throw new Error("retired configuration was silently saved or required staff resolution");
    await click('#reconfigureQuestionnaireAudienceBtn');
    await waitFor(cdp, "document.querySelector('#retiredQuestionnaireOwnerNote')?.textContent.includes('已进入重新配置')", "explicit reconfiguration did not open");
    await click('#savePackageBtn');
    await waitFor(cdp, "document.querySelector('#templateStatusLine')?.textContent.includes('已保存') && document.querySelector('#retiredQuestionnaireOwnerNote')?.hidden && !document.querySelector('#manualRefreshBtn').disabled", "new respondent configuration was not saved or refresh was not restored");
    if (countWrites() !== before + 2) throw new Error("explicit reconfiguration did not use one package update and one immutable configuration save");
    const response = await evaluate(cdp, "fetch('/api/admin/ai-audience/packages/" + packageID + "/configuration',{credentials:'same-origin'}).then(r=>r.json()).then(x=>JSON.stringify(x.configuration.definition))");
    if (response.includes('owner_') || !response.includes('questionnaire_submissions')) throw new Error("new respondent definition still carries employee dependency");
    await resize(390); await capture("survey-retired-owner-after-390");
    if (exceptions.length) throw new Error("Survey retirement had browser exceptions: " + JSON.stringify(exceptions));
    console.log("survey_owner_retirement_chromium: PASS screenshots=" + screenshotDirectory);
  } else if (process.env.AICRM_AUDIENCE_TEST_SCENARIO === "send-history") {
    await cdp.call("Page.navigate", { url: baseURL + "/admin/automation-conversion/packages/" + packageID });
    await waitFor(cdp, "Boolean(document.querySelector('[data-panel=\"records\"]'))", "detail shell did not mount");
    await click('[data-panel="records"]');
    await waitFor(cdp, "document.querySelector('#sendRecordTotal')?.textContent.includes('累计 105 次运行') && !document.querySelector('#sendRecordNextBtn').disabled", "scoped first page and historical total did not load");
    const ids = () => evaluate(cdp, "[...document.querySelectorAll('#sendRecordRows [data-run-id]')].map(node=>Number(node.dataset.runId))");
    const first = await ids();
    if (first.length !== 100 || first[0] !== 105 || first.at(-1) !== 6) throw new Error("first page leaked, omitted or reordered history: " + JSON.stringify(first));
    await capture("audience-history-first");
    await click('#sendRecordNextBtn');
    await waitFor(cdp, "document.querySelector('#sendRecordPageLabel')?.textContent.includes('第 2 / 2 页')", "last page did not load");
    const last = await ids();
    if (JSON.stringify(last) !== '[5,4,3,2,1]') throw new Error("last page lost historical records: " + JSON.stringify(last));
    if (!await evaluate(cdp, "document.querySelector('#sendRecordNextBtn').disabled && document.querySelector('#sendRecordTotal').textContent.includes('累计 105 次运行')")) throw new Error("last page controls or total are incorrect");
    await capture("audience-history-last");
    await click('#sendRecordPrevBtn');
    await waitFor(cdp, "document.querySelector('#sendRecordPageLabel')?.textContent.includes('第 1 / 2 页')", "previous page did not load");
    if (JSON.stringify(await ids()) !== JSON.stringify(first)) throw new Error("previous page did not preserve run order");
    await click('#sendRecordNextBtn');
    await waitFor(cdp, "document.querySelector('#sendRecordPageLabel')?.textContent.includes('第 2 / 2 页')", "second next page did not load");
    await click('#saveCurrentDimensionBtn');
    await waitFor(cdp, "document.querySelector('#sendRecordPageLabel')?.textContent.includes('第 1 / 2 页')", "refresh did not reset history to first page");
    await resize(390);
    await capture("audience-history-390");
    const historyWrites = requests.filter(request => request.method !== 'GET' && (request.path.startsWith('/api/admin/automation-runs') || /\/(runs|refresh|direct-push)$/.test(request.path)));
    if (historyWrites.length || exceptions.length) throw new Error("history journey mutated runtime or threw: " + JSON.stringify({historyWrites,exceptions}));
    console.log("audience_send_history_chromium: PASS screenshots=" + screenshotDirectory);
  } else {
  if (!await click('[data-action="archive"][data-package-id="' + packageID + '"]')) throw new Error("archive control was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-confirmation-dialog][open]'))", "archive did not open the shared confirmation dialog");
  for (const width of [1440, 1280, 390]) {
    await resize(width);
    await evaluate(cdp, "new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
    const layout = await evaluate(cdp, "(() => { const dialog=document.querySelector('[data-v3-confirmation-dialog]'); const box=dialog?.getBoundingClientRect(); const confirm=dialog?.querySelector('[data-v3-confirmation-confirm]')?.getBoundingClientRect(); return {open:Boolean(dialog?.open),width:Math.round(box?.width||0),height:Math.round(box?.height||0),left:Math.round(box?.left||0),right:Math.round(box?.right||0),confirmWidth:Math.round(confirm?.width||0)}; })()");
    // The legacy admin shell itself retains a fixed-width navigation rail on
    // narrow viewports. Verify the shipped dialog surface instead of treating
    // that pre-existing shell overflow as a dialog regression.
    if (!layout.open || layout.width < 180 || layout.height < 120 || layout.left < -1 || layout.right > width + 1 || layout.confirmWidth < 60) throw new Error("confirmation layout invalid at " + width + ": " + JSON.stringify(layout));
    await capture("audience-confirmation-" + width);
  }
  if (!await click('[data-v3-confirmation-cancel]')) throw new Error("confirmation cancel control was unavailable");
  await waitFor(cdp, "!document.querySelector('[data-v3-confirmation-dialog]')", "confirmation cancel did not close dialog");
  if (requests.some((request) => request.method === "DELETE" && request.path.endsWith("/packages/" + packageID))) throw new Error("cancelled confirmation issued an Audience mutation");
  if (!await evaluate(cdp, "document.activeElement?.matches('[data-action=\"archive\"][data-package-id=\"" + packageID + "\"]')")) throw new Error("confirmation cancel did not restore focus to its original action");

  if (!await click('[data-action="archive"][data-package-id="' + packageID + '"]')) throw new Error("archive retry control was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-confirmation-dialog][open]'))", "archive retry did not open confirmation");
  if (!await click('[data-v3-confirmation-confirm]')) throw new Error("confirmation action was unavailable");
  await waitFor(cdp, "document.querySelector('[data-v3-confirmation-dialog]') === null && document.querySelector('[data-action=\"archive\"][data-package-id=\"" + packageID + "\"]') === null", "confirmed archive did not reload the real Audience read model");
  const archiveRequests = requests.filter((request) => request.method === "DELETE" && request.path.endsWith("/packages/" + packageID));
  if (archiveRequests.length !== 1) throw new Error("confirmed archive command count=" + archiveRequests.length + " requests=" + JSON.stringify(requests));
  if (exceptions.length) throw new Error("Audience confirmation had runtime exceptions: " + JSON.stringify(exceptions));
  console.log("audience_confirmation_chromium: PASS screenshots=" + screenshotDirectory);
  }
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  const browserExited = await stopBrowser(browser);
  if (!browserExited && !failed) throw new Error('Chromium did not exit before audience-confirmation profile cleanup');
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error('Chromium profile cleanup did not complete');
}
