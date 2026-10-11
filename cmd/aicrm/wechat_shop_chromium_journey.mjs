import { resolveChromiumBinary } from '../../internal/webshell/chromium_binary.mjs';
import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import assert from 'node:assert/strict';

const base = process.env.AICRM_SHOP_TEST_URL;
const product = process.env.AICRM_SHOP_TEST_PRODUCT;
const screenshots = process.env.AICRM_SHOP_SCREENSHOT_DIR;
if (!/^https:\/\/(127\.0\.0\.1|localhost):[0-9]+$/.test(base || '') || !/^[1-9][0-9]*$/.test(product || '')) throw new Error('isolated Shop browser fixture required');
const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
class CDP {
  constructor(socket) {
    this.socket = socket; this.next = 0; this.pending = new Map();
    socket.addEventListener('message', event => {
      const message = JSON.parse(String(event.data));
      const pending = this.pending.get(message.id);
      if (pending) { this.pending.delete(message.id); message.error ? pending.reject(new Error('CDP call failed')) : pending.resolve(message.result || {}); }
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.next; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { this.socket.close(); }
}
const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-shop-browser-'));
let browser, cdp;
try {
  browser = spawn(resolveChromiumBinary(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', '--user-data-dir=' + profile, '--no-first-run', '--disable-background-networking', '--disable-component-update', '--disable-sync', '--ignore-certificate-errors', 'about:blank'], { stdio: 'ignore' });
  let address;
  for (let n = 0; n < 300; n++) {
    try { const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; if (/^[0-9]+$/.test(port)) { address = 'http://127.0.0.1:' + port; break; } } catch {}
    if (browser.exitCode !== null) throw new Error('Chromium exited at startup');
    await delay(100);
  }
  if (!address) throw new Error('Chromium startup timeout');
  const tab = await (await fetch(address + '/json/new?about:blank', { method: 'PUT' })).json();
  const socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', () => reject(new Error('Chromium connection failed')), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable');
  await cdp.call('Emulation.setDeviceMetricsOverride', { width: 2048, height: 664, deviceScaleFactor: 1, mobile: false });
  const evaluate = async expression => { const result = await cdp.call('Runtime.evaluate', { expression, returnByValue: true, awaitPromise: true }); if (result.exceptionDetails) throw new Error('browser evaluation failed'); return result.result?.value; };
  const wait = async (expression, message) => { for (let n = 0; n < 240; n++) { if (await evaluate(expression)) return; await delay(50); } throw new Error(message); };
  const capture = async name => { if (!screenshots) return; await fs.mkdir(screenshots, { recursive: true }); const screenshot = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: false }); await fs.writeFile(path.join(screenshots, name + '.png'), Buffer.from(screenshot.data, 'base64')); };
  const stat = () => evaluate(`fetch('/api/admin/products/wechat-shop/refresh-status',{credentials:'same-origin'}).then(r=>r.json())`);
  const noStats = async () => assert.equal(await evaluate(`/上次成功|新增 \\d|已存在 \\d|失败 \\d/.test(document.body.innerText)`), false, 'statistics still visible');
  await cdp.call('Page.navigate', { url: base + '/login?next=' + encodeURIComponent('/admin/wechat-pay/products') });
  await wait(`Boolean(document.querySelector('form[action="/login"] input[name="login_csrf_token"]'))`, 'real login form absent');
  await evaluate(`(()=>{document.querySelector('input[name="username"]').value='product-browser-owner';document.querySelector('input[name="password"]').value='product-browser-owner-password';document.querySelector('form[action="/login"]').requestSubmit();return true})()`);
  await wait(`location.pathname==='/admin/wechat-pay/products' && document.querySelector('[data-shop-refresh-status]')?.hidden && document.body.innerText.includes('微信小店浏览器课程')`, 'successful product page did not load');
  await noStats(); await capture('01-products-success');
  assert.equal((await stat()).state, 'succeeded');
  await evaluate(`fetch('/__shop_fixture__/failure',{method:'POST'})`);
  await evaluate(`document.querySelector('[data-page-header-action="shop-refresh"]').click()`);
  await wait(`document.querySelector('[data-shop-refresh-status]')?.textContent.includes('刷新中') && document.querySelector('[data-page-header-action="shop-refresh"]')?.disabled`, 'refresh progress not actionable');
  await evaluate(`fetch('/__shop_fixture__/run',{method:'POST'})`);
  await wait(`document.querySelector('[data-shop-refresh-status]')?.textContent.includes('点击重试') && !document.querySelector('[data-page-header-action="shop-refresh"]')?.disabled`, 'retry feedback absent');
  await noStats(); await capture('02-products-failure');
  const failed = await stat(); assert.equal(failed.state, 'retrying');
  await evaluate(`document.querySelector('[data-page-header-action="shop-refresh"]').click()`);
  await delay(200);
  assert.equal((await stat()).id, failed.id, 'retry replaced the same run');
  await evaluate(`fetch('/__shop_fixture__/run?recover=1',{method:'POST'})`);
  await wait(`document.querySelector('[data-shop-refresh-status]')?.hidden && document.body.innerText.includes('微信小店浏览器课程')`, 'successful refresh did not reload product list');
  const recovered = await stat(); assert.equal(recovered.state, 'succeeded'); assert.equal(recovered.existing, 1); assert.ok(recovered.last_success_at);
  await noStats(); await capture('03-products-recovered');
  await cdp.call('Page.navigate', { url: base + '/admin/orderDetail.html?id=90071992547409931234&provider=wechat_shop' });
  await wait(`document.body.innerText.includes('微信小店外部订单') && document.body.innerText.includes('权益需人工处理') && document.body.innerText.includes('90071992547409931234')`, 'external order or exact source ID unreadable');
  assert.equal(await evaluate(`document.body.innerText.includes('微信小店已退款金额') && document.body.innerText.includes('0.85')`), true, 'refunded money absent');
  await capture('04-order-refund');
  await cdp.call('Page.navigate', { url: base + '/admin/wechat-pay/productForm.html?id=' + product });
  await wait(`Boolean(document.querySelector('[data-shop-source]'))`, 'Shop editor source missing');
  assert.equal(await evaluate(`document.querySelector('[data-distribution-policy]')===null && document.getElementById('product-action')?.hidden && document.getElementById('product-media')?.hidden`), true, 'Shop editor exposes local sale/distribution');
  await capture('05-sku-editor');
  console.log('wechat_shop_chromium: PASS (real login/CSRF, refresh failure/recovery, retained receipt, exact external order/refund, SKU editor)');
} finally {
  cdp?.close();
  if (browser && browser.exitCode === null) {
    browser.kill('SIGTERM');
    await Promise.race([new Promise(resolve => browser.once('exit', resolve)), delay(3000)]);
    if (browser.exitCode === null && browser.signalCode === null) { browser.kill('SIGKILL'); await Promise.race([new Promise(resolve => browser.once('exit', resolve)), delay(3000)]); }
  }
  try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }); } catch { console.warn('synthetic browser profile cleanup warning'); }
}
