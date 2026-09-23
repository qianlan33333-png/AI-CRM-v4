import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

const baseURL = String(process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_URL || '').replace(/\/$/, '');
const standardCode = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_STANDARD_CODE;
const serviceCode = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_SERVICE_CODE;
const serviceID = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_SERVICE_ID;
const h5Token = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_H5_TOKEN;
const linkToken = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_LINK_TOKEN;
const linkMerchant = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_LINK_MERCHANT;
const linkBinding = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_LINK_BINDING;
const screenshotDirectory = process.env.AICRM_PAYMENT_ACTIONS_PUBLIC_SCREENSHOT_DIR;
const h5Destination = 'https://after.example.test/frozen-h5';
const linkFallback = 'https://after.example.test/url-link-fallback';

if (!/^https:\/\/127\.0\.0\.1:\d+$/.test(baseURL) || !standardCode || !serviceCode || !/^[1-9][0-9]*$/.test(serviceID || '') || !/^pays_[A-Za-z0-9_-]{20,}$/.test(h5Token || '') || !/^pays_[A-Za-z0-9_-]{20,}$/.test(linkToken || '') || !linkMerchant || !/^[A-Za-z0-9_-]{43}$/.test(linkBinding || '') || !path.isAbsolute(screenshotDirectory || '')) {
  throw new Error('public paid Chromium journey environment is incomplete');
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const browserBinary = () => {
  for (const candidate of [process.env.AICRM_CHROMIUM_BINARY, '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'google-chrome', 'chromium'].filter(Boolean)) {
    if ((candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }) : spawnSync('which', [candidate], { stdio: 'ignore' })).status === 0) return candidate;
  }
  throw new Error('Chromium is unavailable');
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.next = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener('message', event => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        message.error ? pending.reject(new Error(`CDP ${message.error.code}`)) : pending.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.next;
      const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP ${method} timed out`)); }, 8000);
      this.pending.set(id, { resolve: result => { clearTimeout(timer); resolve(result); }, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  on(method, listener) {
    this.events.set(method, [...(this.events.get(method) || []), listener]);
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error('CDP closed'));
    this.pending.clear();
    this.socket.close();
  }
}

async function debuggerAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0];
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await delay(50);
  }
  throw new Error('Chromium DevTools did not start');
}

async function evaluate(cdp, expression) {
  const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(`page evaluation failed: ${result.exceptionDetails.text || 'unknown'}`);
  return result.result?.value;
}

async function waitFor(cdp, predicate, message) {
  for (let attempt = 0; attempt < 200; attempt += 1) {
    if (await predicate()) return;
    await delay(50);
  }
  throw new Error(message);
}

async function stopBrowser(browser) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return;
  browser.kill('SIGTERM');
  await Promise.race([new Promise(resolve => browser.once('exit', resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) browser.kill('SIGKILL');
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-payment-actions-public-chromium-'));
let browser;
let cdp;
try {
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ['--headless=new', '--no-sandbox', '--ignore-certificate-errors', '--allow-insecure-localhost', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', 'about:blank'], { stdio: 'ignore' });
  const target = await (await fetch(`${await debuggerAddress(profile)}/json/new?about:blank`, { method: 'PUT' })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, { once: true });
    socket.addEventListener('error', () => reject(new Error('CDP connection failed')), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call('Page.enable');
  await cdp.call('Runtime.enable');
  await cdp.call('Network.enable');
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 390, height: 860, deviceScaleFactor: 1, mobile: true, screenWidth: 390, screenHeight: 860 });
  await cdp.call('Emulation.setUserAgentOverride', { userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) MicroMessenger/8.0.50 Mobile/15E148 Safari/604.1' });

  const requests = [];
  const responses = new Map();
  cdp.on('Network.requestWillBeSent', event => requests.push({ url: String(event.request?.url || ''), method: String(event.request?.method || '') }));
  cdp.on('Network.responseReceived', event => responses.set(String(event.response?.url || ''), Number(event.response?.status || 0)));
  const setTrustedCookie = token => cdp.call('Network.setCookie', { url: baseURL, name: 'aicrm_payment_session', value: token, secure: true, httpOnly: true, sameSite: 'Lax' });
  assert.equal((await setTrustedCookie(h5Token)).success, true, 'H5 paid session cookie');

  // A paid standard purchase uses its frozen H5 action directly. Re-entering
  // the real public payment page represents a browser refresh and must only
  // issue reads; a terminal checkout can never create another POST.
  await cdp.call('Page.navigate', { url: `${baseURL}/pay/${encodeURIComponent(standardCode)}` });
  await waitFor(cdp, () => requests.some(request => request.url === h5Destination), 'public paid H5 page did not navigate to its frozen target');
  await cdp.call('Page.navigate', { url: `${baseURL}/pay/${encodeURIComponent(standardCode)}` });
  await waitFor(cdp, () => requests.filter(request => request.url === h5Destination).length >= 2, 'public paid H5 refresh did not reuse its frozen target');

  assert.equal((await setTrustedCookie(linkToken)).success, true, 'URL Link paid session cookie');
  const servicePath = `${baseURL}/s/${encodeURIComponent(serviceCode)}/pay`;
  // The first page boot would correctly reject a consumed session from a new
  // checkout. Establish the browser's own persisted paid checkpoint on an
  // inert same-origin API document before the public payment page boots.
  await cdp.call('Page.navigate', { url: `${baseURL}/api/v1/wechat-pay/checkout-session` });
  await waitFor(cdp, () => evaluate(cdp, `location.origin === ${JSON.stringify(baseURL)}`), 'same-origin session checkpoint document did not load');
  const checkpoint = { key: 'payment-actions-public-link-0001', merchant_order_no: linkMerchant, payload: { product_id: Number(serviceID), product_kind: 'service_period', beneficiary_selection: 'payer_self', coupon_claim_id: 0 }, session_binding: linkBinding, terminal_status: 'paid' };
  await evaluate(cdp, `sessionStorage.setItem('aicrm.checkout.tab.v2:${serviceID}:service_period', ${JSON.stringify(JSON.stringify(checkpoint))}); true`);
  await cdp.call('Page.navigate', { url: servicePath });
  const resolverURL = `${baseURL}/api/v1/wechat-pay/checkouts/${encodeURIComponent(linkMerchant)}/completion-target`;
  try {
    await waitFor(cdp, () => requests.some(request => request.url === resolverURL), 'paid URL Link public page did not request the same-origin resolver');
  } catch (error) {
    const state = await evaluate(cdp, `(() => ({href:location.href,checkpoint:sessionStorage.getItem('aicrm.checkout.tab.v2:${serviceID}:service_period'),status:document.querySelector('#status')?.textContent||'',root:document.querySelector('[data-v3-public-commerce]')?.dataset.publicCommerceMounted||''}))()`);
    throw new Error(`${error.message}; state=${JSON.stringify(state)} requests=${JSON.stringify(requests.slice(-16))}`);
  }
  await waitFor(cdp, () => requests.some(request => request.url === linkFallback), 'paid URL Link resolver did not navigate to Product fallback');
  await cdp.call('Page.navigate', { url: servicePath });
  await waitFor(cdp, () => requests.filter(request => request.url === resolverURL).length >= 2, 'paid URL Link refresh did not reuse the same resolver route');

  const duplicatePosts = requests.filter(request => request.method === 'POST' && new URL(request.url).origin === baseURL && request.url.endsWith('/api/v1/wechat-pay/checkouts'));
  assert.deepEqual(duplicatePosts, [], `paid public refresh created checkout POSTs: ${JSON.stringify(duplicatePosts)}`);
  console.log(`payment_actions_public_checkout: PASS screenshots=${screenshotDirectory}`);
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  await fs.rm(profile, { recursive: true, force: true }).catch(() => {});
}
