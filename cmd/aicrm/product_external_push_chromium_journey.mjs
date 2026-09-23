import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_PRODUCT_PUSH_TEST_URL;
const username = process.env.AICRM_PRODUCT_PUSH_TEST_USERNAME;
const password = process.env.AICRM_PRODUCT_PUSH_TEST_PASSWORD;
const productID = process.env.AICRM_PRODUCT_PUSH_TEST_PRODUCT_ID;
const serviceProductID = process.env.AICRM_PRODUCT_PUSH_TEST_SERVICE_PRODUCT_ID;
const materialFirstID = process.env.AICRM_PRODUCT_PUSH_TEST_MATERIAL_FIRST_ID;
const materialLaterID = process.env.AICRM_PRODUCT_PUSH_TEST_MATERIAL_LATER_ID;
const historicalOrderReference = process.env.AICRM_PRODUCT_PUSH_TEST_HISTORICAL_ORDER;
const screenshotDirectory = process.env.AICRM_PRODUCT_PUSH_SCREENSHOT_DIR;
// The Product owner derives a stable per-product endpoint reference from the
// target submitted to its admin command. Browser checks must use the owner's
// readback value, rather than compare that derived reference to the target.
const productConfigurationReference = `product-endpoint:wechat_pay:${productID}`;
// The fixture input intentionally has a different key order. Go persists an
// object and returns its canonical map text. Keep this as literal JSON rather
// than parsing it in JavaScript: JSON.parse would round the 64-bit integer.
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "") || !/^[1-9][0-9]*$/.test(materialFirstID || "") || !/^[1-9][0-9]*$/.test(materialLaterID || "") || !/^[A-Za-z0-9._:-]{1,200}$/.test(historicalOrderReference || "")) {
  throw new Error("product external push Chromium journey requires HTTPS URL, credentials, ordinary and service-period product ids, historical order, and JSON");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
async function captureProductMaterialScreens(cdp, prefix) {
  if (!screenshotDirectory) return;
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  for (const width of [1280, 1440]) {
    await cdp.call('Emulation.setDeviceMetricsOverride', { width, height: 1000, deviceScaleFactor: 1, mobile: false });
    const image = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, `${prefix}-${width}.png`), Buffer.from(image.data, 'base64'), { mode: 0o600 });
  }
}

async function capturePaymentActionPanel(cdp, prefix) {
  if (!screenshotDirectory) return;
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  const image = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await fs.writeFile(path.join(screenshotDirectory, `${prefix}-1440x1100.png`), Buffer.from(image.data, 'base64'), { mode: 0o600 });
}
// Do not embed a regular expression in a Runtime.evaluate template string:
// JavaScript string escaping would turn `\s` into a literal `s`. Cookie order
// is arbitrary, so split exact names instead of relying on a position-specific
// substring.
const cookieNamePresent = (cookie, name) => String(cookie || '').split(';').some((part) => part.trim().startsWith(name + '='));
if (!cookieNamePresent('aicrm_admin_csrf=test; aicrm_csrf=test', 'aicrm_admin_csrf') ||
  !cookieNamePresent('aicrm_admin_csrf=test; aicrm_csrf=test', 'aicrm_csrf') ||
  cookieNamePresent('aicrm_admin_csrf=test; aicrm_csrf=test', 'csrf_token')) {
  throw new Error('cookie name fixture is invalid');
}
const browserBinary = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    if (candidate.includes("/")) {
      try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {}
    } else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) {
      return candidate;
    }
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
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error("CDP request failed"));
        else pending.resolve(message.result || {});
        return;
      }
      if (!message.method) return;
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
    return () => this.events.set(method, (this.events.get(method) || []).filter((item) => item !== listener));
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    this.events.clear();
    this.socket.close();
  }
}

const waitForPort = async (profile) => {
  const activePort = path.join(profile, "DevToolsActivePort");
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(activePort, "utf8")).split("\n")[0];
      if (/^\d+$/.test(port)) return "http://127.0.0.1:" + port;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
};

const evaluate = async (cdp, expression) => {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
};
const waitFor = async (cdp, expression, message) => {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
};

const waitForBrowserExit = async (child, timeoutMilliseconds) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    child.once("exit", () => { clearTimeout(timer); resolve(true); });
  });
};
const removeProfile = async (profile) => {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!error || !["ENOTEMPTY", "EBUSY", "EPERM"].includes(error.code)) return false;
      await delay(100);
    }
  }
  return false;
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-product-push-chromium-"));
let browser;
let cdp;
let journeyFailed = false;
class DevToolsUnavailable extends Error {}
try {
  browser = spawn(browserBinary(), [
    "--headless=new", "--no-sandbox", "--remote-debugging-port=0", "--user-data-dir=" + profile,
    "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--ignore-certificate-errors",
    "--allow-insecure-localhost", "about:blank",
  ], { stdio: ["ignore", "ignore", "ignore"] });
  let address;
  try {
    address = await waitForPort(profile);
  } catch (error) {
    if (process.platform === "darwin") throw new DevToolsUnavailable();
    throw error;
  }
  const created = await (await fetch(address + "/json/new?about:blank", { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  const runtimeExceptions = [];
  // Keep only route, method and status. A browser journey failure needs enough
  // evidence to distinguish Host wiring, session/CSRF and HTTP rejection, but
  // never serializes request bodies, cookies, bearer values or receiver data.
  const requests = new Map();
  const responses = [];
  cdp.on("Runtime.exceptionThrown", (params) => {
    const details = params.exceptionDetails || {};
    const name = String(details.exception?.className || details.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (runtimeExceptions.length < 8) runtimeExceptions.push(name);
  });
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const pathname = new URL(String(params.request?.url || "")).pathname;
      if (pathname.includes("products") || pathname.includes("productForm") || pathname.includes("orderDetail") || pathname.includes("external-push") || pathname.includes("service-period-products") || pathname.startsWith("/api/admin/image-library") || pathname.startsWith("/assets/")) {
        requests.set(params.requestId, { pathname, method: String(params.request?.method || "GET") });
      }
    } catch (_) {}
  });
  cdp.on("Network.responseReceived", (params) => {
    const request = requests.get(params.requestId);
    if (!request || responses.length >= 32) return;
    responses.push(`${request.method} ${request.pathname}:${Number(params.response?.status) || 0}`);
  });
  const browserSaveDiagnostic = async () => {
    const page = await evaluate(cdp, `(() => {
      const panel = document.querySelector('[data-product-parity-push]');
      const save = panel?.querySelector('[data-product-parity-push-save]');
      const result = panel?.querySelector('[data-product-parity-push-result]')?.textContent || '';
      const toast = document.querySelector('#product-v3-toast');
      const hasCookie = (name) => String(document.cookie || '').split(';').some((part) => part.trim().startsWith(name + '='));
      return {
        path: location.pathname,
        result: result ? 'push_result_present' : 'push_result_empty',
        toast: String(toast?.textContent || '') ? 'toast_present' : 'toast_empty',
        saveDisabled: Boolean(save && save.disabled),
        adminCSRF: hasCookie('aicrm_admin_csrf'),
        compatCSRF: hasCookie('aicrm_csrf'),
        anchor: Boolean(document.querySelector(location.pathname.endsWith('/admin/wechat-pay/spProductForm.html') ? '#sp-push' : '#product-push')),
        hostPanel: Boolean(panel),
        retiredPanelAbsent: !document.querySelector('[data-external-push-configuration]') && !document.querySelector('#product-v3-external-push-custom-params'),
        productHostAsset: Array.from(document.scripts).some((script) => String(script.src || '').includes('/product-assets/')),
        frozenAdminEntry: Array.from(document.scripts).some((script) => String(script.src || '').includes('/assets/')),
      };
    })()`);
    const routes = responses.join(',') || 'none';
    return `path=${page?.path || 'unknown'} result=${page?.result || 'none'} toast=${page?.toast || 'none'} save_disabled=${page?.saveDisabled === true} csrf_admin=${page?.adminCSRF === true} csrf_compat=${page?.compatCSRF === true} anchor=${page?.anchor === true} host_panel=${page?.hostPanel === true} retired_panel_absent=${page?.retiredPanelAbsent === true} product_host_asset=${page?.productHostAsset === true} frozen_admin_entry=${page?.frozenAdminEntry === true} exceptions=${runtimeExceptions.join(',') || 'none'} responses=${routes}`;
  };

  const assertProductEditorHeader = async (kind, title, returnLabel) => {
    for (const width of [1280, 1440]) {
      await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
      const layout = await evaluate(cdp, `(() => {
        const visible = (node) => {
          if (!(node instanceof HTMLElement) || node.hidden) return false;
          const style = getComputedStyle(node);
          const rect = node.getBoundingClientRect();
          return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0;
        };
        const topbar = document.querySelector('.admin-topbar');
        const topbarRect = topbar?.getBoundingClientRect();
        const actions = Array.from(topbar?.querySelectorAll('[data-page-header-actions="product-editor"] button') || []);
        const duplicateTitles = Array.from(document.querySelectorAll('#stage *')).filter((node) => node.children.length === 0 && node.textContent?.trim() === ${JSON.stringify(title)} && visible(node));
        const bodyReturn = Array.from(document.querySelectorAll('#stage button')).some((button) => button.textContent?.trim() === ${JSON.stringify(returnLabel)} && visible(button));
        const frozenHeader = document.querySelector('#stage [data-v3-product-frozen-header="hidden"]');
        const frozenRect = frozenHeader?.getBoundingClientRect();
        return {
          topbars: document.querySelectorAll('.admin-topbar').length,
          shellTitles: topbar?.querySelectorAll('.admin-page-title').length || 0,
          shellTitle: topbar?.querySelector('.admin-page-title')?.textContent?.trim(),
          actions: actions.map((button) => button.textContent?.trim()),
          actionGeometry: actions.map((button) => { const rect = button.getBoundingClientRect(); return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, visible: visible(button) }; }),
          topbarGeometry: topbarRect ? { left: topbarRect.left, right: topbarRect.right, top: topbarRect.top, bottom: topbarRect.bottom, width: topbarRect.width, height: topbarRect.height } : null,
          duplicateTitles: duplicateTitles.length,
          bodyReturn,
          frozenHeaderHidden: frozenHeader instanceof HTMLElement && frozenHeader.hidden && Boolean(frozenRect && frozenRect.height === 0),
          width: window.innerWidth,
        };
      })()`);
      const topbarFits = layout?.topbarGeometry && layout.topbarGeometry.left >= 0 && layout.topbarGeometry.right <= width && layout.topbarGeometry.width > 0 && layout.topbarGeometry.height > 0;
      const actionsFit = layout?.actionGeometry?.every((action) => action.visible && action.left >= 0 && action.right <= width && action.top >= layout.topbarGeometry.top && action.bottom <= layout.topbarGeometry.bottom);
      if (!layout || layout.topbars !== 1 || layout.shellTitles !== 1 || layout.shellTitle !== title || layout.width !== width ||
        layout.actions.join('|') !== `${returnLabel}|保存当前维度` || !topbarFits || !actionsFit || layout.duplicateTitles !== 0 || layout.bodyReturn || !layout.frozenHeaderHidden) {
        throw new Error(`${kind} editor header layout invalid at ${width}: ${JSON.stringify(layout)}`);
      }
    }
  };

  const productPath = "/admin/wechat-pay/productForm.html?id=" + productID;
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=" + encodeURIComponent(productPath) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, "(() => { document.querySelector('input[name=\"username\"]').value=" + JSON.stringify(username) + "; document.querySelector('input[name=\"password\"]').value=" + JSON.stringify(password) + "; document.querySelector('form[action=\"/login\"]').requestSubmit(); return true; })()");
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html'", "login did not reach frozen product form");

  const hostReady = "Boolean(document.querySelector('[data-product-parity-push]')) && !document.querySelector('[data-external-push-configuration]') && !document.querySelector('#product-v3-external-push-custom-params')";
  const pushReady = hostReady + " && !document.querySelector('[data-product-parity-push-save]')?.disabled";
  try {
    await waitFor(cdp, hostReady, "product Host did not render");
  } catch (_) {
    throw new Error("product Host did not render " + await browserSaveDiagnostic());
  }
  if (!await evaluate(cdp, `(() => { const hasCookie = (name) => String(document.cookie || '').split(';').some((part) => part.trim().startsWith(name + '=')); return hasCookie('aicrm_admin_csrf') && hasCookie('aicrm_csrf'); })()`)) {
    throw new Error("product Host did not receive CSRF session bridge " + await browserSaveDiagnostic());
  }
  await assertProductEditorHeader('ordinary', '编辑普通商品', '返回商品管理');
  // The list Host owns the lifecycle buttons. Exercise the real browser
  // session, CSRF header and CAS endpoint once in each direction before the
  // form journey, leaving the seeded fixture enabled for its remaining steps.
  const productsPath = "/admin/products.html";
  const runProductLifecycleAction = async (label) => {
    const result = await evaluate(cdp, `((label) => {
      const row = Array.from(document.querySelectorAll('tbody tr')).find((item) => item.textContent.includes('browser-push-product'));
      if (!row) return { invoked: false };
      const trigger = row.querySelector('button[data-table-action-menu-trigger]');
      if (!(trigger instanceof HTMLButtonElement) || trigger.disabled || trigger.getClientRects().length === 0 || getComputedStyle(trigger).visibility === 'hidden') return { invoked: false };
      const panelID = trigger.getAttribute('aria-controls');
      const panel = panelID ? document.getElementById(panelID) : null;
      if (!(panel instanceof HTMLElement)) return { invoked: false };
      trigger.click();
      const menuVisible = !panel.hidden && panel.getClientRects().length > 0 && getComputedStyle(panel).display !== 'none' && getComputedStyle(panel).visibility === 'visible';
      const action = menuVisible ? Array.from(panel.querySelectorAll('button')).find((button) => button.textContent.trim() === label) : undefined;
      if (!(action instanceof HTMLButtonElement) || action.disabled || action.getClientRects().length === 0 || getComputedStyle(action).visibility === 'hidden') return { invoked: false, menuVisible };
      action.click();
      return { invoked: true, menuVisible, panelID };
    })(${JSON.stringify(label)})`);
    if (!result?.invoked || !result.menuVisible || typeof result.panelID !== 'string') throw new Error(`product lifecycle ${label} action was not invoked through its visible menu: ${JSON.stringify(result)}`);
    return result.panelID;
  };
  const waitForLifecycleMenuClosed = async (panelID, label) => {
    const encodedPanelID = JSON.stringify(panelID);
    await waitFor(cdp, `(() => { const panel = document.getElementById(${encodedPanelID}); return !panel || panel.hidden || panel.getClientRects().length === 0 || getComputedStyle(panel).display === 'none' || getComputedStyle(panel).visibility === 'hidden'; })()`, `product lifecycle ${label} action left its overflow menu open after completion`);
  };
  await cdp.call("Page.navigate", { url: baseURL + productsPath });
  await waitFor(cdp, "location.pathname === '/admin/products.html' && Array.from(document.querySelectorAll('tbody tr')).some((row) => { const trigger=row.querySelector('button[data-table-action-menu-trigger]'); return row.textContent.includes('browser-push-product') && trigger instanceof HTMLButtonElement && !trigger.disabled && trigger.getClientRects().length > 0 && getComputedStyle(trigger).visibility !== 'hidden' && Boolean(trigger.getAttribute('aria-controls')); })", "product list lifecycle Host did not render the seeded enabled action menu");
  const shareOpened = await evaluate(cdp, "(()=>{const row=[...document.querySelectorAll('tbody tr')].find(item=>item.textContent.includes('browser-push-product'));const trigger=[...(row?.querySelectorAll('button')||[])].find(button=>button.textContent.trim()==='分享');if(!(trigger instanceof HTMLButtonElement))return false;Object.defineProperty(navigator,'clipboard',{value:{writeText:async value=>{window.__productShareCopied=value;}},configurable:true});window.__productShareOpened=[];window.open=(url)=>{window.__productShareOpened.push(String(url));return null;};window.__productShareAnchorClick=HTMLAnchorElement.prototype.click;HTMLAnchorElement.prototype.click=function(){window.__productShareDownloaded={download:this.download,href:this.href};};trigger.focus();trigger.click();return true})()");
  if (!shareOpened) throw new Error('product list share entry was unavailable');
  await waitFor(cdp, "Boolean(document.querySelector('dialog[data-shared-qr-dialog=\"true\"][open]'))", "product share did not open the shared QR dialog");
  const shareDialog = await evaluate(cdp, "(()=>{const dialog=document.querySelector('dialog[data-shared-qr-dialog=\"true\"]');return {centered:dialog?.classList.contains('shared-detail-drawer--center')||false,svg:Boolean(dialog?.querySelector('.shared-qr-dialog__code svg')),url:dialog?.querySelector('input[readonly]')?.value||'',buttons:[...(dialog?.querySelectorAll('.shared-qr-dialog__actions button')||[])].map(button=>({label:button.textContent?.trim(),real:button.__dcBound===true})),closeFocused:document.activeElement===dialog?.querySelector('.shared-detail-drawer__close')}})()");
  if (!shareDialog?.centered || !shareDialog.svg || !/^https:\/\/.+\/p\/browser-push-product$/.test(shareDialog.url) || !shareDialog.closeFocused || JSON.stringify(shareDialog.buttons) !== JSON.stringify([{label:'复制链接',real:true},{label:'预览',real:true},{label:'保存二维码',real:true}])) throw new Error('product share dialog did not retain centered QR presentation or real callbacks ' + JSON.stringify(shareDialog || {}));
  await evaluate(cdp, "[...document.querySelector('dialog[data-shared-qr-dialog=\"true\"] .shared-qr-dialog__actions').querySelectorAll('button')].find(button=>button.textContent.trim()==='复制链接').click(); true");
  await waitFor(cdp, "window.__productShareCopied===document.querySelector('dialog[data-shared-qr-dialog=\"true\"] input[readonly]')?.value", 'product share copy callback did not receive the caller URL');
  await evaluate(cdp, "[...document.querySelector('dialog[data-shared-qr-dialog=\"true\"] .shared-qr-dialog__actions').querySelectorAll('button')].find(button=>button.textContent.trim()==='预览').click(); true");
  await waitFor(cdp, "window.__productShareOpened?.[0]===document.querySelector('dialog[data-shared-qr-dialog=\"true\"] input[readonly]')?.value", 'product share preview callback did not receive the caller URL');
  await evaluate(cdp, "[...document.querySelector('dialog[data-shared-qr-dialog=\"true\"] .shared-qr-dialog__actions').querySelectorAll('button')].find(button=>button.textContent.trim()==='保存二维码').click(); true");
  await waitFor(cdp, "typeof window.__productShareDownloaded?.download==='string' && window.__productShareDownloaded.download.endsWith('-qr.svg')", 'product share save callback did not retain its download action');
  await evaluate(cdp, "document.querySelector('dialog[data-shared-qr-dialog=\"true\"] .shared-detail-drawer__close').click(); true");
  await waitFor(cdp, "!document.querySelector('dialog[data-shared-qr-dialog=\"true\"]')", 'product share close did not remove the dialog');
  if (!await evaluate(cdp, "(()=>{const row=[...document.querySelectorAll('tbody tr')].find(item=>item.textContent.includes('browser-push-product'));return document.activeElement===[...(row?.querySelectorAll('button')||[])].find(button=>button.textContent.trim()==='分享')})()")) throw new Error('product share close did not restore the share trigger focus');
  await evaluate(cdp, "if(window.__productShareAnchorClick)HTMLAnchorElement.prototype.click=window.__productShareAnchorClick; true");
  const disablePanelID = await runProductLifecycleAction('停用');
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('商品已停用')", "product lifecycle disable did not complete through the Host");
  await waitForLifecycleMenuClosed(disablePanelID, '停用');
  await cdp.call("Page.navigate", { url: baseURL + productsPath });
  await waitFor(cdp, "Array.from(document.querySelectorAll('tbody tr')).some((row) => { const trigger=row.querySelector('button[data-table-action-menu-trigger]'); return row.textContent.includes('browser-push-product') && trigger instanceof HTMLButtonElement && !trigger.disabled && trigger.getClientRects().length > 0 && getComputedStyle(trigger).visibility !== 'hidden' && Boolean(trigger.getAttribute('aria-controls')); })", "product list did not read back the disabled lifecycle action menu");
  const enablePanelID = await runProductLifecycleAction('启用');
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('商品已启用')", "product lifecycle enable did not complete through the Host");
  await waitForLifecycleMenuClosed(enablePanelID, '启用');
  await cdp.call("Page.navigate", { url: baseURL + productPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html'", "product lifecycle return did not reach frozen product form");
  // Host mounting creates the editor before its configuration GET resolves.
  // Wait for the first revision rather than racing the closure that owns the
  // configuration snapshot used for CAS in the save handler.
  try {
    await waitFor(cdp, pushReady, "product configuration did not load");
  } catch (_) {
    throw new Error("product configuration did not load " + await browserSaveDiagnostic());
  }
  const configurationBeforeMaterial = await evaluate(cdp, "fetch('/api/admin/wechat-pay/products/" + productID + "/external-push',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>({enabled:body?.enabled===true,reference:body?.configuration_reference===" + JSON.stringify(productConfigurationReference) + "?'expected':body?.configuration_reference===''?'empty':'other'}))");
  if (!configurationBeforeMaterial?.enabled || configurationBeforeMaterial?.reference !== 'expected') {
    throw new Error('product lifecycle changed the seeded external-push configuration ' + JSON.stringify(configurationBeforeMaterial || {}));
  }
  // The V3 caller reads its own paged, authorised Media catalogue. Pick an
  // item from the first page, remove it in the temporary dialog, then confirm
  // an item outside that legacy page. The frozen product controller remains
  // the only owner of the browser draft and later Product save/readback.
  const productMediaTabOpened = await evaluate(cdp, "(()=>{const tab=document.querySelector('a[href=\"#product-media\"]');if(!(tab instanceof HTMLAnchorElement))return false;tab.click();return true})()");
  if (!productMediaTabOpened) throw new Error('product media tab was unavailable');
  await waitFor(cdp, "Boolean(Array.from(document.querySelectorAll('#product-media button')).find((button)=>button.textContent?.trim()==='从素材库选择'))", 'product material caller did not mount');
  await evaluate(cdp, "Array.from(document.querySelectorAll('#product-media button')).find((button)=>button.textContent?.trim()==='从素材库选择').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key$=\":" + materialFirstID + "\"]'))", 'product V3 material first page did not render');
  if (!await evaluate(cdp, "(()=>{const row=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key$=\":" + materialFirstID + "\"]');if(!row)return false;row.click();return true})()")) throw new Error('product V3 material first-page row was unavailable');
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-picker-more]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key$=\":" + materialLaterID + "\"]'))", 'product V3 material later page did not render');
  if (!await evaluate(cdp, "(()=>{const row=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-key$=\":" + materialLaterID + "\"]');if(!row)return false;row.click();const remove=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-remove$=\":" + materialFirstID + "\"]');if(!remove)return false;remove.click();return true})()")) throw new Error('product V3 material temporary multi-select/remove did not retain both pages');
  const selectedLaterOnly = await evaluate(cdp, "(()=>{const selected=document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-picker-selected]')?.textContent||'';return selected.includes('Chromium 商品后续页素材')&&!selected.includes('Chromium 商品首页素材')})()");
  if (!selectedLaterOnly) throw new Error('product V3 material removal did not remain a local draft');
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-picker-confirm]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"material\"]') && Array.from(document.querySelectorAll('#product-media img')).some((image)=>image.src.includes('/" + materialLaterID + "/variants/thumb_320'))", 'product V3 material confirmation did not update the original product draft');
  await evaluate(cdp, "Array.from(document.querySelectorAll('#product-media button')).find((button)=>button.textContent?.trim()==='从素材库选择').click(); true");
  await waitFor(cdp, `Boolean(document.querySelector('[data-v3-selection-session="material"] [data-v3-material-remove$=":${materialLaterID}"]'))`, 'product material reopening did not reconstruct the owner draft');
  await evaluate(cdp, "document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-material-remove$=\":" + materialLaterID + "\"]').click(); document.querySelector('[data-v3-selection-session=\"material\"] [data-v3-picker-cancel]').click(); true");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=\"material\"]') && Array.from(document.querySelectorAll('#product-media img')).some((image)=>image.src.includes('/" + materialLaterID + "/variants/thumb_320'))", 'product material cancellation changed the original draft');
  // Upload a real PNG through the same current media dimension. The V3 Host
  // must append the typed Media receipt, leave unsaved form state and the tab
  // intact, then persist the order only through the explicit owner save.
  const productUploadPath = path.join(profile, 'chromium-product-upload.png');
  await fs.writeFile(productUploadPath, Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII=', 'base64'));
  const productUploadPrepared = await evaluate(cdp, "(()=>{const description=document.querySelector('#pfDescription');if(!(description instanceof HTMLTextAreaElement))return false;description.value='Chromium material draft remains active';return true})()");
  if (!productUploadPrepared) throw new Error('product material upload input was unavailable');
  await evaluate(cdp, "(()=>{const input=document.querySelector('#pfImageUpload');if(!(input instanceof HTMLInputElement))return false;window.__productUploadChangeSeen=0;input.addEventListener('change',()=>{window.__productUploadChangeSeen=(window.__productUploadChangeSeen||0)+1},{capture:true});return true})()");
  const productDOM = await cdp.call('DOM.getDocument', { depth: 2 });
  const productUploadNode = await cdp.call('DOM.querySelector', { nodeId: productDOM.root.nodeId, selector: '#pfImageUpload' });
  if (!productUploadNode.nodeId) throw new Error('product material upload source input was unavailable');
  await cdp.call('DOM.setFileInputFiles', { files: [productUploadPath], nodeId: productUploadNode.nodeId });
  // DOM.setFileInputFiles dispatches the browser's native change event. The
  // product host clears the input while its async upload is in flight, so do not
  // infer whether it started from a later input.files inspection.
  await delay(250);
  const earlyProductUploadState = await evaluate(cdp, "(()=>({toast:document.querySelector('#product-v3-toast')?.textContent||'',rows:[...document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]')].map(row=>row.dataset.v3ProductMaterialKey)}))()");
  try {
    await waitFor(cdp, "(()=>{const rows=[...document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]')];const tab=document.querySelector('a[href=\"#product-media\"]');return rows.length===2&&tab?.getAttribute('aria-current')==='step'&&document.querySelector('#pfDescription')?.value==='Chromium material draft remains active'})()", 'product upload reset the current media dimension or did not append its typed receipt');
  } catch (_) {
    const uploadState = await evaluate(cdp, "(()=>({rows:[...document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]')].map(row=>row.dataset.v3ProductMaterialKey),tab:document.querySelector('a[href=\"#product-media\"]')?.getAttribute('aria-current')||'',description:document.querySelector('#pfDescription')?.value||'',toast:document.querySelector('#product-v3-toast')?.textContent||'',subtle:Boolean(globalThis.crypto&&globalThis.crypto.subtle),inputFiles:document.querySelector('#pfImageUpload')?.files?.length||0,changeSeen:Number(window.__productUploadChangeSeen||0)}))()");
    throw new Error('product upload reset the current media dimension or did not append its typed receipt ' + JSON.stringify({...uploadState,earlyProductUploadState,responses:responses.slice(-12),runtimeExceptions}));
  }
  const uploadedProduct = await evaluate(cdp, "(()=>{const rows=[...document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]')];return rows.find(row=>row.dataset.v3ProductMaterialKey!=='image:" + materialLaterID + "')?.dataset.v3ProductMaterialKey||''})()");
  if (!/^image:[1-9][0-9]*$/.test(uploadedProduct || '')) throw new Error('product upload did not expose a typed Media row');
  await captureProductMaterialScreens(cdp, 'ordinary-material-draft');
  const productSortMoved = await evaluate(cdp, "(()=>{const row=[...document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]')].find(item=>item.dataset.v3ProductMaterialKey===" + JSON.stringify(uploadedProduct) + ");const button=row?.querySelector('[data-v3-product-material-action=\"up\"]');if(!(button instanceof HTMLButtonElement))return false;button.focus();button.click();const active=document.activeElement;return active instanceof HTMLButtonElement&&!active.disabled&&active.closest('[data-v3-product-material-key]')?.dataset.v3ProductMaterialKey===" + JSON.stringify(uploadedProduct) + "})()");
  if (!productSortMoved) throw new Error('product material sort did not preserve the active row action');
  await evaluate(cdp, "Array.from(document.querySelectorAll('#product-media button')).find((button)=>button.textContent?.trim()==='保存当前维度').click(); true");
  await waitFor(cdp, "document.querySelector('#product-v3-toast')?.textContent.includes('已保存当前维度')", 'product material owner save did not complete');
  await waitFor(cdp, "fetch('/api/admin/wechat-pay/products/" + productID + "/external-push',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>Number(body?.revision)===1)", 'product material owner save changed the independent external-push revision');
  const preservedAfterMaterialSave = await evaluate(cdp, "fetch('/api/admin/wechat-pay/products/" + productID + "/external-push',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>({enabled:body?.enabled===true,reference:body?.configuration_reference===" + JSON.stringify(productConfigurationReference) + "?'expected':body?.configuration_reference===''?'empty':'other',url:body?.webhook_url==='https://commerce-browser.invalid',type:body?.push_type==='paid_notify',params:typeof body?.custom_params_json==='string'&&body.custom_params_json.includes('9007199254740993')&&!body.custom_params_json.includes('9007199254740992')&&body.custom_params_json.includes('inner')}))");
  if (preservedAfterMaterialSave?.reference !== 'expected') {
    throw new Error('product material owner save changed the external-push reference ' + JSON.stringify(preservedAfterMaterialSave || {}));
  }
  if (!preservedAfterMaterialSave?.enabled || !preservedAfterMaterialSave?.url || !preservedAfterMaterialSave?.type || !preservedAfterMaterialSave?.params) {
    throw new Error('product material owner save changed an external-push field ' + JSON.stringify(preservedAfterMaterialSave || {}));
  }
  const uploadedProductID = Number(String(uploadedProduct).slice('image:'.length));
  const expectedProductImages = [`/api/admin/image-library/${uploadedProductID}/variants/original`, `/api/admin/image-library/${materialLaterID}/variants/original`];
  await waitFor(cdp, "fetch('/api/v1/products/" + productID + "',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>JSON.stringify(body?.images)===" + JSON.stringify(JSON.stringify(expectedProductImages)) + ")",  'product material owner save/readback did not preserve the uploaded typed receipt and chosen order');
  // The legacy V3 mapping conversion/editor and its JSON textarea are retired.
  // The API may still contain historical mapping data, but the current Product
  // panel must not offer a second editor for it.
  const retiredMappingAbsent = await evaluate(cdp, "!document.querySelector('[data-mapping-conversion]') && !document.querySelector('[data-fm-rows]') && !document.querySelector('#product-v3-external-push-test') && !document.querySelector('[data-external-push-configuration]') && !document.querySelector('#product-v3-external-push-custom-params')");
  if (!retiredMappingAbsent) throw new Error('retired external-push mapping controls remained mounted');
  const productPushTabOpened = await evaluate(cdp, "(()=>{const tab=document.querySelector('a[href=\"#product-push\"]');if(!(tab instanceof HTMLAnchorElement))return false;tab.click();return Boolean(document.querySelector('[data-product-parity-push]'))})()");
  if (!productPushTabOpened) throw new Error('product external-push tab was unavailable');
  const highPrecisionBeforeEdit = await evaluate(cdp, "(()=>{const rows=[...document.querySelectorAll('[data-product-parity-param-row]')];const value=(key)=>rows.find(row=>row.querySelector('[data-product-parity-param-key]')?.value===key)?.querySelector('[data-product-parity-param-value]')?.value||'';return {count:value('count'),nested:value('nested'),flag:value('flag')}})()");
  if (highPrecisionBeforeEdit?.count !== '9007199254740993' || highPrecisionBeforeEdit?.nested !== '[{\"inner\":9007199254740993}]' || highPrecisionBeforeEdit?.flag !== 'false') throw new Error('legacy custom_params lost typed values before edit ' + JSON.stringify(highPrecisionBeforeEdit || {}));
  await capturePaymentActionPanel(cdp, 'ordinary-push');
  const ordinaryParamAdded = await evaluate(cdp, "(()=>{const panel=document.querySelector('[data-product-parity-push]');const add=panel?.querySelector('[data-product-parity-push-add]');if(!(add instanceof HTMLButtonElement))return false;add.click();const rows=[...panel.querySelectorAll('[data-product-parity-param-row]')];const row=rows.at(-1);const key=row?.querySelector('[data-product-parity-param-key]');const value=row?.querySelector('[data-product-parity-param-value]');if(!(key instanceof HTMLInputElement)||!(value instanceof HTMLInputElement))return false;key.value='campaign';key.dispatchEvent(new Event('input',{bubbles:true}));value.value='browser-control';value.dispatchEvent(new Event('input',{bubbles:true}));return true})()");
  if (!ordinaryParamAdded) throw new Error('ordinary key/value custom parameter entry was unavailable');
  await evaluate(cdp, "(()=>{const panel=document.querySelector('[data-product-parity-push]');const type=panel?.querySelector('[data-product-parity-push-type]');const day=panel?.querySelector('[data-product-parity-push-day]');const frequency=panel?.querySelector('[data-product-parity-push-frequency]');const expires=panel?.querySelector('[data-product-parity-push-expires]');const remark=panel?.querySelector('[data-product-parity-push-remark]');if(!(type instanceof HTMLInputElement)||!(day instanceof HTMLInputElement)||!(frequency instanceof HTMLInputElement)||!(expires instanceof HTMLInputElement)||!(remark instanceof HTMLTextAreaElement))return false;type.value='member_open';day.value='30';frequency.value='1';expires.value='2147483647';remark.value='browser preserves typed parameters';panel.querySelector('[data-product-parity-push-save]')?.click();return true})()");
  try {
    await waitFor(cdp, "document.querySelector('[data-product-parity-push-result]')?.textContent === '配置已保存'", 'browser configuration save did not finish');
  } catch (_) {
    throw new Error('browser configuration save did not finish ' + await browserSaveDiagnostic());
  }
  const highPrecisionSaved = await evaluate(cdp, "fetch('/api/admin/wechat-pay/products/" + productID + "/external-push',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>({revision:Number(body?.revision),raw:String(body?.custom_params_json||''),campaign:body?.custom_params?.campaign}))");
  if (highPrecisionSaved?.revision !== 2 || highPrecisionSaved?.campaign !== 'browser-control' || !highPrecisionSaved.raw.includes('9007199254740993') || highPrecisionSaved.raw.includes('9007199254740992') || highPrecisionSaved.raw.split('9007199254740993').length < 3) throw new Error('browser save rounded or replaced legacy typed custom_params ' + JSON.stringify(highPrecisionSaved || {}));

  await cdp.call('Page.navigate', { url: baseURL + productPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html' && " + pushReady, 'reloaded Product Host did not resolve external-push configuration');
  const highPrecisionAfterReload = await evaluate(cdp, "(()=>{const rows=[...document.querySelectorAll('[data-product-parity-param-row]')];const value=(key)=>rows.find(row=>row.querySelector('[data-product-parity-param-key]')?.value===key)?.querySelector('[data-product-parity-param-value]')?.value||'';return {count:value('count'),nested:value('nested'),campaign:value('campaign'),expires:document.querySelector('[data-product-parity-push-expires]')?.value||''}})()");
  if (highPrecisionAfterReload?.count !== '9007199254740993' || highPrecisionAfterReload?.nested !== '[{\"inner\":9007199254740993}]' || highPrecisionAfterReload?.campaign !== 'browser-control' || highPrecisionAfterReload?.expires !== '2147483647') throw new Error('reloaded Product panel changed legacy typed custom_params ' + JSON.stringify(highPrecisionAfterReload || {}));
  await evaluate(cdp, "document.querySelector('[data-product-parity-push-test]')?.click(); true");
  await waitFor(cdp, "document.querySelector('[data-product-parity-push-result]')?.textContent.includes('delivery_id: commerce_test_')", 'synthetic test did not return its legacy delivery_id through Product HTTP');
  const acceptedTestResult = await evaluate(cdp, "document.querySelector('[data-product-parity-push-result]')?.textContent||''");
  if (!acceptedTestResult.includes('测试推送') || acceptedTestResult.includes('业务已送达')) throw new Error('test push UI claimed delivery from local acceptance ' + acceptedTestResult);
  const terminalTestStatus = "fetch('/api/admin/wechat-pay/products/" + productID + "/external-push/test',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>{const item=body?.items?.[0];return item?.state==='outcome_unknown'&&item?.delivery_proven===false&&typeof item?.delivery_id==='string'&&item.delivery_id.startsWith('commerce_test_')})";
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (await evaluate(cdp, terminalTestStatus)) break;
    await delay(250);
  }
  if (!await evaluate(cdp, terminalTestStatus)) throw new Error('external-push test status did not reach the durable unknown result without a delivery claim');

  // The frozen service-period form has separate donor bindings and a separate
  // Host endpoint. Save and reload it through the outer application handler
  // too, so the ordinary form cannot mask a service-period adapter failure.
  const serviceProductPath = "/admin/wechat-pay/spProductForm.html?id=" + serviceProductID;
  await cdp.call("Page.navigate", { url: baseURL + serviceProductPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/spProductForm.html'", "navigation did not reach frozen service-period product form");
  const serviceHostReady = pushReady;
  try {
    await waitFor(cdp, serviceHostReady, "service-period product Host did not render");
  } catch (_) {
    throw new Error("service-period product Host did not render " + await browserSaveDiagnostic());
  }
  await assertProductEditorHeader('service-period', '编辑周期商品', '返回周期商品管理');
  // The service-period editor uses a separate frozen callback. Exercise its
  // real file input too: the receipt must remain in the active media draft and
  // must not reset this form before its owner explicitly saves.
  const serviceMediaOpened = await evaluate(cdp, "(()=>{const tab=document.querySelector('a[href=\"#sp-media\"]');if(!(tab instanceof HTMLAnchorElement))return false;tab.click();const description=document.querySelector('#spfDescription');if(!(description instanceof HTMLTextAreaElement))return false;description.value='Chromium service material draft remains active';return true})()");
  if (!serviceMediaOpened) throw new Error('service-period material dimension was unavailable');
  const serviceUploadPath = path.join(profile, 'chromium-service-upload.png');
  await fs.writeFile(serviceUploadPath, Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII=', 'base64'));
  const serviceDOM = await cdp.call('DOM.getDocument', { depth: 2 });
  const serviceUploadNode = await cdp.call('DOM.querySelector', { nodeId: serviceDOM.root.nodeId, selector: '#spfImageUpload' });
  if (!serviceUploadNode.nodeId) throw new Error('service-period material upload source input was unavailable');
  await cdp.call('DOM.setFileInputFiles', { files: [serviceUploadPath], nodeId: serviceUploadNode.nodeId });
  await waitFor(cdp, "(()=>{const rows=[...document.querySelectorAll('[data-v3-product-material-list] [data-v3-product-material-key]')];const tab=document.querySelector('a[href=\"#sp-media\"]');return rows.length===1&&tab?.getAttribute('aria-current')==='step'&&document.querySelector('#spfDescription')?.value==='Chromium service material draft remains active'})()", 'service-period upload reset the current media dimension or did not append its typed receipt');
  await captureProductMaterialScreens(cdp, 'service-material-draft');
  // The service-period form reuses the same frozen panel contract. Preserve
  // its pre-existing raw high-precision values while adding one independent
  // key/value and verify readback after a route reload.
  const servicePushOpened = await evaluate(cdp, "(()=>{const tab=document.querySelector('a[href=\"#sp-push\"]');if(!(tab instanceof HTMLAnchorElement))return false;tab.click();return Boolean(document.querySelector('[data-product-parity-push]'))})()");
  if (!servicePushOpened) throw new Error('service-period external-push tab was unavailable');
  await capturePaymentActionPanel(cdp, 'service-period-push');
  const serviceParamAdded = await evaluate(cdp, "(()=>{const panel=document.querySelector('[data-product-parity-push]');const count=[...panel.querySelectorAll('[data-product-parity-param-row]')].find(row=>row.querySelector('[data-product-parity-param-key]')?.value==='count')?.querySelector('[data-product-parity-param-value]')?.value;if(count!=='9007199254740993')return false;const add=panel.querySelector('[data-product-parity-push-add]');if(!(add instanceof HTMLButtonElement))return false;add.click();const row=[...panel.querySelectorAll('[data-product-parity-param-row]')].at(-1);const key=row?.querySelector('[data-product-parity-param-key]');const value=row?.querySelector('[data-product-parity-param-value]');if(!(key instanceof HTMLInputElement)||!(value instanceof HTMLInputElement))return false;key.value='service_campaign';key.dispatchEvent(new Event('input',{bubbles:true}));value.value='browser-service';value.dispatchEvent(new Event('input',{bubbles:true}));const type=panel.querySelector('[data-product-parity-push-type]');const expires=panel.querySelector('[data-product-parity-push-expires]');if(!(type instanceof HTMLInputElement)||!(expires instanceof HTMLInputElement))return false;type.value='member_renew';expires.value='2147483647';panel.querySelector('[data-product-parity-push-save]')?.click();return true})()");
  if (!serviceParamAdded) throw new Error('service-period key/value panel did not preserve or add typed parameters');
  try {
    await waitFor(cdp, "document.querySelector('[data-product-parity-push-result]')?.textContent === '配置已保存'", 'service-period browser configuration save did not finish');
  } catch (_) {
    throw new Error('service-period browser configuration save did not finish ' + await browserSaveDiagnostic());
  }
  await cdp.call('Page.navigate', { url: baseURL + serviceProductPath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/spProductForm.html' && " + pushReady, 'reloaded service-period Host did not resolve external-push configuration');
  const servicePrecisionAfterReload = await evaluate(cdp, "(()=>{const rows=[...document.querySelectorAll('[data-product-parity-param-row]')];const value=(key)=>rows.find(row=>row.querySelector('[data-product-parity-param-key]')?.value===key)?.querySelector('[data-product-parity-param-value]')?.value||'';return {count:value('count'),nested:value('nested'),campaign:value('service_campaign'),expires:document.querySelector('[data-product-parity-push-expires]')?.value||''}})()");
  if (servicePrecisionAfterReload?.count !== '9007199254740993' || servicePrecisionAfterReload?.nested !== '[{\"inner\":9007199254740993}]' || servicePrecisionAfterReload?.campaign !== 'browser-service' || servicePrecisionAfterReload?.expires !== '2147483647') throw new Error('service-period reload changed typed custom_params ' + JSON.stringify(servicePrecisionAfterReload || {}));


  const historicalOrderPath = "/admin/orderDetail.html?id=" + encodeURIComponent(historicalOrderReference);
  await cdp.call("Page.navigate", { url: baseURL + historicalOrderPath });
  await waitFor(cdp, "location.pathname === '/admin/orderDetail.html' && document.body?.textContent?.includes('外部处理记录') && document.body?.textContent?.includes('历史记录：外推成功')", "outer order-detail route did not render its mapped historical delivery");
  const orderEffects = await evaluate(cdp, "fetch('/api/admin/wechat-pay/orders/" + historicalOrderReference + "/external-push-deliveries',{credentials:'same-origin'}).then((response)=>response.ok?response.json():null).then((body)=>({source:body?.items?.[0]?.source,delivery:body?.items?.[0]?.legacy_delivery_id,status:body?.items?.[0]?.status}))");
  if (orderEffects?.source !== "history" || orderEffects?.delivery !== "browser-history-delivery-1" || orderEffects?.status !== "succeeded") throw new Error("outer order delivery route did not return mapped frozen history");
  console.log("product_external_push_chromium: PASS");
} catch (error) {
  if (error instanceof DevToolsUnavailable) {
    console.log("product_external_push_chromium: SKIP_DEVTOOLS");
  } else {
    journeyFailed = true;
    throw error;
  }
} finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGTERM");
    if (!await waitForBrowserExit(browser, 3000) && browser.exitCode === null && browser.signalCode === null) {
      browser.kill("SIGKILL");
      await waitForBrowserExit(browser, 1000);
    }
  }
  const removed = await removeProfile(profile);
  if (!removed && !journeyFailed) throw new Error("Chromium test profile cleanup did not complete");
}
