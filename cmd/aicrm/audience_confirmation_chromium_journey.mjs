import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_URL;
const username = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_USERNAME;
const password = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_PASSWORD;
const packageID = process.env.AICRM_AUDIENCE_CONFIRMATION_TEST_PACKAGE_ID;
const screenshotDirectory = process.env.AICRM_AUDIENCE_CONFIRMATION_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(packageID || "") || !path.isAbsolute(screenshotDirectory || "")) {
  throw new Error("audience confirmation Chromium journey requires HTTPS URL, credentials, package ID, and absolute screenshots");
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
  throw new Error(message);
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
