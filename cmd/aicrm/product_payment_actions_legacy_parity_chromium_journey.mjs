import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

const baseURL = process.env.AICRM_PAYMENT_ACTIONS_TEST_URL;
const username = process.env.AICRM_PAYMENT_ACTIONS_TEST_USERNAME;
const password = process.env.AICRM_PAYMENT_ACTIONS_TEST_PASSWORD;
const productID = process.env.AICRM_PAYMENT_ACTIONS_TEST_PRODUCT_ID;
const serviceProductID = process.env.AICRM_PAYMENT_ACTIONS_TEST_SERVICE_PRODUCT_ID;
const screenshotDirectory = process.env.AICRM_PAYMENT_ACTIONS_SCREENSHOT_DIR;

if (!/^https:\/\//.test(baseURL || '') || !username || !password || !/^[1-9][0-9]*$/.test(productID || '') || !/^[1-9][0-9]*$/.test(serviceProductID || '') || !screenshotDirectory) {
  throw new Error('payment-action journey requires HTTPS host credentials, product IDs, and a screenshot directory');
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

function browserBinary() {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === 'darwin') candidates.push('/Applications/Google Chrome.app/Contents/MacOS/Google Chrome');
  candidates.push('google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser');
  for (const candidate of candidates) {
    if (candidate.includes('/')) {
      try { if (spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status === 0) return candidate; } catch (_) {}
    } else if (spawnSync('which', [candidate], { stdio: 'ignore' }).status === 0) {
      return candidate;
    }
  }
  throw new Error('Chromium binary is unavailable');
}

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      if (!message.id || !this.pending.has(message.id)) return;
      const pending = this.pending.get(message.id);
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error('CDP request failed'));
      else pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error('CDP closed'));
    this.pending.clear();
    this.socket.close();
  }
}

async function waitForPort(profile) {
  const activePort = path.join(profile, 'DevToolsActivePort');
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(activePort, 'utf8')).split('\n')[0];
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await delay(50);
  }
  throw new Error('Chromium remote debugging did not become ready');
}

async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error('page evaluation failed');
  return result.result?.value;
}

async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
}

async function capturePanel(cdp, name, selector) {
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1440, height: 1100, deviceScaleFactor: 1, mobile: false });
  await evaluate(cdp, `(() => { const panel = document.querySelector(${JSON.stringify(selector)}); if (!(panel instanceof HTMLElement)) return false; const dimension = panel.closest('#product-action,#sp-action,#product-push,#sp-push'); const anchor = dimension?.id ? document.querySelector('a[href="#' + dimension.id + '"]') : null; if (anchor instanceof HTMLAnchorElement) anchor.click(); panel.scrollIntoView({ block: 'center' }); return true; })()`);
  await waitFor(cdp, `(() => { const panel = document.querySelector(${JSON.stringify(selector)}); if (!(panel instanceof HTMLElement) || panel.hidden) return false; const style = getComputedStyle(panel); const rect = panel.getBoundingClientRect(); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 0 && rect.height > 0; })()`, `payment-action screenshot panel unavailable: ${name}`);
  await delay(100);
  const image = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false });
  await fs.writeFile(path.join(screenshotDirectory, `${name}-1440x1100.png`), Buffer.from(image.data, 'base64'), { mode: 0o600 });
}

function actionReadyExpression() {
  return "Boolean(document.querySelector('[data-product-purchase-action]')) && Boolean(document.querySelector('[data-product-parity-push]')) && Boolean(document.querySelector('[data-product-parity-param-row]')) && document.querySelector('[data-product-parity-push-save]')?.disabled === false && !document.querySelector('[data-external-push-configuration]') && !document.querySelector('#product-v3-external-push-custom-params')";
}

async function configureH5Action(cdp, productPath, productAPIPath) {
  const configured = await evaluate(cdp, `(() => {
    const panel = document.querySelector('[data-product-purchase-action]');
    const enabled = panel?.querySelector('[data-product-purchase-enabled]');
    const redirect = panel?.querySelector('input[value="redirect"]');
    const h5 = panel?.querySelector('[data-product-purchase-h5-url]');
    const save = panel?.querySelector('[data-product-purchase-save]');
    if (!(enabled instanceof HTMLInputElement) || !(redirect instanceof HTMLInputElement) || !(h5 instanceof HTMLInputElement) || !(save instanceof HTMLButtonElement)) return false;
    enabled.checked = true; enabled.dispatchEvent(new Event('change', { bubbles: true }));
    redirect.checked = true; redirect.dispatchEvent(new Event('change', { bubbles: true }));
    h5.value = 'https://after.example.test/ordinary'; h5.dispatchEvent(new Event('input', { bubbles: true }));
    save.click(); return true;
  })()`);
  if (!configured) throw new Error('ordinary H5 purchase action controls unavailable');
  await waitFor(cdp, `fetch(${JSON.stringify(productAPIPath)}, {credentials:'same-origin'}).then((response) => response.ok ? response.json() : null).then((body) => { const product = body?.product || body; const action = product?.admin_projection || {}; return action.purchase_action_enabled === true && action.purchase_action_mode === 'redirect' && action.completion_target?.target_type === 'h5' && action.completion_target?.h5_url === 'https://after.example.test/ordinary'; })`, 'ordinary H5 action did not persist through the Product host');
  await capturePanel(cdp, 'ordinary-after-redirect', '[data-product-purchase-action]');
  await cdp.call('Page.navigate', { url: baseURL + productPath });
  await waitFor(cdp, actionReadyExpression(), 'ordinary action panel did not remount after reload');
  await waitFor(cdp, "document.querySelector('[data-product-purchase-enabled]')?.checked === true && document.querySelector('input[value=\"redirect\"]')?.checked === true && document.querySelector('[data-product-purchase-h5-url]')?.value === 'https://after.example.test/ordinary'", 'ordinary H5 action did not read back after reload');
}

async function configureURLLinkAction(cdp, productPath, productAPIPath) {
  const configured = await evaluate(cdp, `(() => {
    const panel = document.querySelector('[data-product-purchase-action]');
    const enabled = panel?.querySelector('[data-product-purchase-enabled]');
    const redirect = panel?.querySelector('input[value="redirect"]');
    const target = panel?.querySelector('[data-product-purchase-target-type]');
    const source = panel?.querySelector('[data-product-purchase-url-link-source]');
    const key = panel?.querySelector('[data-product-purchase-url-link-key]');
    const save = panel?.querySelector('[data-product-purchase-save]');
    if (!(enabled instanceof HTMLInputElement) || !(redirect instanceof HTMLInputElement) || !(target instanceof HTMLSelectElement) || !(source instanceof HTMLInputElement) || !(key instanceof HTMLInputElement) || !(save instanceof HTMLButtonElement)) return false;
    enabled.checked = true; enabled.dispatchEvent(new Event('change', { bubbles: true }));
    redirect.checked = true; redirect.dispatchEvent(new Event('change', { bubbles: true }));
    target.value = 'url_link'; target.dispatchEvent(new Event('change', { bubbles: true }));
    source.value = 'https://link.example.test/service-period'; source.dispatchEvent(new Event('input', { bubbles: true }));
    key.value = 'result.destination'; key.dispatchEvent(new Event('input', { bubbles: true }));
    save.click(); return true;
  })()`);
  if (!configured) throw new Error('service-period URL Link action controls unavailable');
  await waitFor(cdp, `fetch(${JSON.stringify(productAPIPath)}, {credentials:'same-origin'}).then((response) => response.ok ? response.json() : null).then((body) => { const product = body?.product || body; const action = product?.admin_projection || {}; return action.purchase_action_enabled === true && action.purchase_action_mode === 'redirect' && action.completion_target?.target_type === 'url_link' && action.completion_target?.url_link?.source_url === 'https://link.example.test/service-period' && action.completion_target?.url_link?.response_url_key === 'result.destination'; })`, 'service-period URL Link action did not persist through the Product host');
  await capturePanel(cdp, 'service-period-after-url-link', '[data-product-purchase-action]');
  await cdp.call('Page.navigate', { url: baseURL + productPath });
  await waitFor(cdp, actionReadyExpression(), 'service-period action panel did not remount after reload');
  await waitFor(cdp, "document.querySelector('[data-product-purchase-enabled]')?.checked === true && document.querySelector('input[value=\"redirect\"]')?.checked === true && document.querySelector('[data-product-purchase-target-type]')?.value === 'url_link' && document.querySelector('[data-product-purchase-url-link-source]')?.value === 'https://link.example.test/service-period' && document.querySelector('[data-product-purchase-url-link-key]')?.value === 'result.destination'", 'service-period URL Link action did not read back after reload');
}

async function configurePush(cdp, configurationPath, label) {
  const configured = await evaluate(cdp, `(() => {
    const panel = document.querySelector('[data-product-parity-push]');
    const enabled = panel?.querySelector('[data-product-parity-push-enabled]');
    const url = panel?.querySelector('[data-product-parity-push-url]');
    const remark = panel?.querySelector('[data-product-parity-push-remark]');
    const type = panel?.querySelector('[data-product-parity-push-type]');
    const expires = panel?.querySelector('[data-product-parity-push-expires]');
    const day = panel?.querySelector('[data-product-parity-push-day]');
    const frequency = panel?.querySelector('[data-product-parity-push-frequency]');
    const row = panel?.querySelector('[data-product-parity-param-row]');
    const key = row?.querySelector('[data-product-parity-param-key]');
    const value = row?.querySelector('[data-product-parity-param-value]');
    const save = panel?.querySelector('[data-product-parity-push-save]');
    if (!(enabled instanceof HTMLInputElement) || !(url instanceof HTMLInputElement) || !(remark instanceof HTMLTextAreaElement) || !(type instanceof HTMLInputElement) || !(expires instanceof HTMLInputElement) || !(day instanceof HTMLInputElement) || !(frequency instanceof HTMLInputElement) || !(key instanceof HTMLInputElement) || !(value instanceof HTMLInputElement) || !(save instanceof HTMLButtonElement)) return false;
    enabled.checked = true; enabled.dispatchEvent(new Event('change', { bubbles: true }));
    url.value = 'https://commerce-browser.invalid'; remark.value = ${JSON.stringify(label + ' parity remark')}; type.value = ${JSON.stringify(label + '_paid')}; expires.value = '2147483647'; day.value = '30'; frequency.value = '1'; key.value = 'campaign'; value.value = ${JSON.stringify(label)};
    for (const input of [url, remark, type, expires, day, frequency, key, value]) input.dispatchEvent(new Event('input', { bubbles: true }));
    save.click(); return true;
  })()`);
  if (!configured) throw new Error(`${label} push controls unavailable`);
  await waitFor(cdp, `fetch(${JSON.stringify(configurationPath)}, {credentials:'same-origin'}).then((response) => response.ok ? response.json() : null).then((body) => body?.enabled === true && body?.webhook_url === 'https://commerce-browser.invalid' && body?.push_type === ${JSON.stringify(label + '_paid')} && body?.expires_at_ts === 2147483647 && body?.day === 30 && body?.frequency === 1 && body?.remark === ${JSON.stringify(label + ' parity remark')} && body?.custom_params?.campaign === ${JSON.stringify(label)})`, `${label} push configuration did not persist`);
  await capturePanel(cdp, `${label}-external-push`, '[data-product-parity-push]');
}

async function assertPushReadback(cdp, label) {
  await waitFor(cdp, `document.querySelector('[data-product-parity-push-enabled]')?.checked === true && document.querySelector('[data-product-parity-push-url]')?.value === 'https://commerce-browser.invalid' && document.querySelector('[data-product-parity-push-type]')?.value === ${JSON.stringify(label + '_paid')} && document.querySelector('[data-product-parity-push-expires]')?.value === '2147483647' && document.querySelector('[data-product-parity-push-day]')?.value === '30' && document.querySelector('[data-product-parity-push-frequency]')?.value === '1' && document.querySelector('[data-product-parity-push-remark]')?.value === ${JSON.stringify(label + ' parity remark')} && document.querySelector('[data-product-parity-param-key]')?.value === 'campaign' && document.querySelector('[data-product-parity-param-value]')?.value === ${JSON.stringify(label)}`, `${label} push configuration did not read back after reload`);
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-payment-actions-chromium-'));
let browser;
let cdp;
try {
  browser = spawn(browserBinary(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', '--ignore-certificate-errors', '--allow-insecure-localhost', 'about:blank'], { stdio: ['ignore', 'ignore', 'ignore'] });
  const address = await waitForPort(profile);
  const created = await (await fetch(`${address}/json/new?about:blank`, { method: 'PUT' })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', () => reject(new Error('Chromium page connection failed')), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call('Page.enable');
  await cdp.call('Runtime.enable');

  const ordinaryPath = `/admin/wechat-pay/productForm.html?id=${productID}`;
  await cdp.call('Page.navigate', { url: `${baseURL}/login?next=${encodeURIComponent(ordinaryPath)}` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", 'login page did not render');
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value = ${JSON.stringify(username)}; document.querySelector('input[name="password"]').value = ${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/productForm.html'", 'login did not reach ordinary product page');
  await waitFor(cdp, actionReadyExpression(), 'ordinary legacy parity panels did not mount');
  await configureH5Action(cdp, ordinaryPath, `/api/v1/products/${productID}`);
  await configurePush(cdp, `/api/admin/wechat-pay/products/${productID}/external-push`, 'ordinary');
  await cdp.call('Page.navigate', { url: baseURL + ordinaryPath });
  await waitFor(cdp, actionReadyExpression(), 'ordinary push panel did not remount after reload');
  await assertPushReadback(cdp, 'ordinary');

  const servicePath = `/admin/wechat-pay/spProductForm.html?id=${serviceProductID}`;
  await cdp.call('Page.navigate', { url: baseURL + servicePath });
  await waitFor(cdp, "location.pathname === '/admin/wechat-pay/spProductForm.html'", 'navigation did not reach service-period product page');
  await waitFor(cdp, actionReadyExpression(), 'service-period legacy parity panels did not mount');
  await configureURLLinkAction(cdp, servicePath, `/api/admin/service-period-products/${serviceProductID}`);
  await configurePush(cdp, `/api/admin/service-period-products/${serviceProductID}/external-push`, 'service');
  await cdp.call('Page.navigate', { url: baseURL + servicePath });
  await waitFor(cdp, actionReadyExpression(), 'service-period push panel did not remount after reload');
  await assertPushReadback(cdp, 'service');
  console.log(`product_payment_actions_legacy_parity: PASS screenshots=${screenshotDirectory}`);
} finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) browser.kill('SIGTERM');
  await delay(300);
  await fs.rm(profile, { recursive: true, force: true });
}
