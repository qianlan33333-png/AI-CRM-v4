import { productSourceTestPage } from '../scripts/product-source-test-page.mjs';
import assert from 'node:assert/strict';
import fs from 'node:fs/promises';
import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { spawn } from 'node:child_process';
import { fileURLToPath } from 'node:url';
import { resolveChromiumBinary } from '../../internal/webshell/chromium_binary.mjs';
import { buildTestBrowserBundle } from '../scripts/test-browser-bundle.mjs';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../..');
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const stockFields = await Promise.all(['productForm.html', 'spProductForm.html'].map(async (name) => {
  const html = productSourceTestPage(name);
  const match = html.match(/<div style="display:grid;gap:6px"><label[^>]*>库存<\/label><input id="(?:pf|spf)Stock"[^>]*><\/div>/);
  assert.ok(match, `${name} contains the frozen donor stock field`);
  return match[0];
}));

const host = await buildTestBrowserBundle('web/v3/productAdapter.ts');
const pageHTML = `<!doctype html><html><body>
${stockFields.join('\n')}
<script>window.fetch = async (input, init = {}) => {
  const path = new URL(input instanceof Request ? input.url : String(input), location.href).pathname;
  const method = String(init.method || (input instanceof Request ? input.method : 'GET')).toUpperCase();
  const value = path === '/api/admin/service-period-products/10' && method === 'GET'
    ? {product:{service_product_id:10,duration_days:30,version:1}}
    : {path,method,body:init.body ? JSON.parse(init.body) : null};
  return new Response(JSON.stringify(value), {headers:{'Content-Type':'application/json'}});
};</script>
<input id="spfDurationDays" value="30">
<script>${host}</script>
<script>
setTimeout(() => {
  document.body.insertAdjacentHTML('beforeend', ${JSON.stringify(stockFields.join('\n'))});
  (async () => {
    await fetch('/api/admin/service-period-products/10');
    const writes = [
      ['/api/v1/products', 'POST', {product_code:'ordinary', stock_quantity:'unlimited'}],
      ['/api/v1/products/9', 'PUT', {name:'ordinary', stock_quantity:-19}],
      ['/api/admin/service-period-products', 'POST', {product_code:'period', stock_quantity:{legacy:true}}],
      ['/api/admin/service-period-products/10', 'PUT', {name:'period', stock_quantity:'not-a-number'}],
    ];
    const payloads = [];
    for (const [path, method, body] of writes) {
      const response = await fetch(path, {method, headers:{'Content-Type':'application/json'}, body:JSON.stringify(body)});
      payloads.push(await response.json());
    }
    await new Promise((resolve) => setTimeout(resolve, 50));
    const controlsGone = !document.getElementById('pfStock') && !document.getElementById('spfStock');
    const noInventoryCopy = ![...document.querySelectorAll('label')].some((label) => label.textContent.trim() === '库存');
    const requestsStockless = payloads.length === 4 && payloads.every(({body}) => body && !Object.hasOwn(body, 'stock_quantity'));
    document.documentElement.dataset.stockTest = controlsGone && noInventoryCopy && requestsStockless ? 'pass' : JSON.stringify({controlsGone,noInventoryCopy,requestsStockless,payloads});
  })().catch((error) => { document.documentElement.dataset.stockTest = String(error); });
}, 0);
</script></body></html>`;

class DevTools {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      if (message.error) pending.reject(new Error(`CDP ${message.error.code || 'error'}`));
      else pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, {resolve, reject});
      this.socket.send(JSON.stringify({id, method, params}));
    });
  }
  close() { this.socket.close(); }
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'product-stock-chromium-'));
const server = http.createServer((_request, response) => {
  response.writeHead(200, {'Content-Type':'text/html; charset=utf-8', 'Cache-Control':'no-store'});
  response.end(pageHTML);
});
await new Promise((resolve, reject) => {
  server.once('error', reject);
  server.listen(0, '127.0.0.1', resolve);
});
const browser = spawn(resolveChromiumBinary(), [
  '--headless=new', '--no-sandbox', '--remote-debugging-port=0', `--user-data-dir=${profile}`,
  '--no-first-run', '--no-default-browser-check', '--disable-background-networking',
  '--disable-component-update', '--disable-sync', '--disable-gpu', '--ignore-certificate-errors',
  '--allow-insecure-localhost', 'about:blank',
], {stdio:['ignore','ignore','pipe']});
let stderr = '';
browser.stderr?.on('data', (chunk) => { if (stderr.length < 4096) stderr += String(chunk).slice(0, 4096 - stderr.length); });
let devtools;
try {
  let devtoolsHTTP;
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (browser.exitCode !== null || browser.signalCode !== null) throw new Error(`Chromium exited before DevTools started: ${stderr.slice(-400)}`);
    try {
      const active = (await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0];
      if (/^\d+$/.test(active)) { devtoolsHTTP = `http://127.0.0.1:${active}`; break; }
    } catch {}
    await wait(50);
  }
  if (!devtoolsHTTP) throw new Error(`Chromium DevTools startup timed out: ${stderr.slice(-400)}`);
  const target = await (await fetch(`${devtoolsHTTP}/json/new?about:blank`, {method:'PUT'})).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener('open', resolve, {once:true});
    socket.addEventListener('error', () => reject(new Error('Chromium DevTools page connection failed')), {once:true});
  });
  devtools = new DevTools(socket);
  await devtools.call('Page.enable');
  await devtools.call('Runtime.enable');
  const address = server.address();
  await devtools.call('Page.navigate', {url:`http://127.0.0.1:${address.port}/product-stock-retirement`});
  let result;
  for (let attempt = 0; attempt < 180; attempt += 1) {
    const evaluated = await devtools.call('Runtime.evaluate', {expression:'document.documentElement?.dataset.stockTest || ""', returnByValue:true, awaitPromise:true});
    result = evaluated.result?.value;
    if (result) break;
    await wait(50);
  }
  assert.equal(result, 'pass', `Chromium Host DOM/write bridge failed: ${result || 'page did not settle'} ${stderr.slice(-300)}`);
  process.stdout.write('Product stock retirement in actual Chromium DOM and Host write bridge: PASS\n');
} finally {
  devtools?.close();
  server.close();
  if (browser.exitCode === null && browser.signalCode === null) {
    browser.kill('SIGKILL');
    await Promise.race([new Promise((resolve) => browser.once('exit', resolve)), wait(1500)]);
  }
  await fs.rm(profile, {recursive:true, force:true, maxRetries:3, retryDelay:100});
}
