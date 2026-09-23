import assert from 'node:assert/strict';
import { createServer } from 'node:http';
import { mkdtemp, readFile, rm } from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';
import { build } from 'esbuild';

const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const chrome = () => {
  for (const candidate of [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, process.platform === 'darwin' ? '/Applications/Google Chrome.app/Contents/MacOS/Google Chrome' : '', 'google-chrome', 'chromium'].filter(Boolean)) {
    if ((candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status : spawnSync('which', [candidate], { stdio: 'ignore' }).status) === 0) return candidate;
  }
  throw new Error('Chromium binary is unavailable');
};
class CDP {
  constructor(socket) {
    this.socket = socket; this.id = 0; this.pending = new Map();
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      message.error ? pending.reject(new Error(`CDP ${message.error.code}`)) : pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { this.socket.close(); }
}
const bundle = await build({
  stdin: { contents: "export { renderMaterialThumbnail } from './web/v3/shared/ui/materialThumbnailPresentation';", resolveDir: process.cwd(), sourcefile: 'material-thumbnail-presentation-browser.ts' },
  bundle: true, format: 'iife', globalName: 'MaterialThumbnail', platform: 'browser', write: false, target: 'es2020',
});
const [contentCSS, pickerCSS] = await Promise.all([
  readFile('web/v3/shared/ui/contentComposer.css', 'utf8'),
  readFile('web/v3/shared/ui/selectionDialog.css', 'utf8'),
]);
const body = String.raw`<!doctype html><style>${contentCSS}\n${pickerCSS}
#host { width:128px; height:128px; position:relative; overflow:hidden; }
#host .aicrm-material-thumbnail__loading, #host .aicrm-material-thumbnail__fallback { position:absolute; inset:0; display:grid; place-items:center; }
#host .aicrm-material-thumbnail__image { width:100%; height:128px; object-fit:cover; }
#host [hidden] { display:none !important; }
.aicrm-material-picker__thumb { display:block; width:64px; height:64px; }
</style><main><div id=host></div><div data-v3-selection-session=material><div data-v3-material-thumbnail id=picker></div></div><div class=aicrm-content-composer-mask><ol class=aicrm-content-presentation__materials><li class=aicrm-content-presentation__material><div class=aicrm-content-presentation__visual id=content></div><div>内容详情</div></li></ol></div></main><script>${bundle.outputFiles[0].text}</script><script>
const visible = node => { const style=getComputedStyle(node); return { display:style.display, visible:style.display !== 'none' && style.visibility !== 'hidden' }; };
const target = id => document.getElementById(id);
const report = {};
const render = (id) => MaterialThumbnail.renderMaterialThumbnail(target(id), { url:'data:image/gif;base64,R0lGODlhAQABAAAAACw=', imageDisplay:'block', loadingDisplay:'grid', fallbackDisplay:'grid', unavailableLabel:'读取失败' });
for (const id of ['host','picker','content']) {
 const result=render(id); const snapshot=()=>({loading:visible(result.loading),image:visible(result.image),fallback:visible(result.fallback),state:result.state});
 report[id]={loading:snapshot()}; result.image.dispatchEvent(new Event('load')); report[id].loaded=snapshot(); result.image.dispatchEvent(new Event('error')); report[id].error=snapshot(); if(id==='content'){const box=target(id).getBoundingClientRect();report[id].frame={width:box.width,height:box.height};}
}
const first=render('host'); const replacement=render('host'); first.image.dispatchEvent(new Event('error')); report.replacement={state:replacement.state,attribute:target('host').dataset.materialThumbnailState};
window.__materialThumbnailReport=report;
</script>`;
const server = createServer((_, response) => { response.writeHead(200, { 'Content-Type': 'text/html; charset=utf-8' }); response.end(body); });
await new Promise((resolve) => server.listen(0, '127.0.0.1', resolve));
const address = `http://127.0.0.1:${server.address().port}`;
const profile = await mkdtemp(path.join(os.tmpdir(), 'aicrm-material-thumbnail-chromium-'));
let browser; let cdp;
try {
  browser = spawn(chrome(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', `--user-data-dir=${profile}`, '--no-first-run', 'about:blank'], { stdio: ['ignore', 'ignore', 'ignore'] });
  let port;
  for (let attempt = 0; attempt < 120; attempt += 1) {
    try { port = String(await readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; if (/^\d+$/.test(port)) break; } catch {}
    await sleep(50);
  }
  assert.match(port || '', /^\d+$/, 'Chromium remote debugger must start');
  const tab = await (await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, { method: 'PUT' })).json();
  const socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable');
  await cdp.call('Page.navigate', { url: address });
  let report;
  for (let attempt = 0; attempt < 80; attempt += 1) {
    const evaluated = await cdp.call('Runtime.evaluate', { expression: 'window.__materialThumbnailReport', returnByValue: true });
    report = evaluated.result?.value;
    if (report) break;
    await sleep(50);
  }
  assert.ok(report, 'browser report must be available');
  for (const [surface, states] of Object.entries(report)) {
    if (surface === 'replacement') continue;
    assert.deepEqual(states.loading, { loading: { display: 'grid', visible: true }, image: { display: 'none', visible: false }, fallback: { display: 'none', visible: false }, state: 'loading' }, `${surface}: loading shows only the status`);
    assert.deepEqual(states.loaded, { loading: { display: 'none', visible: false }, image: { display: 'block', visible: true }, fallback: { display: 'none', visible: false }, state: 'loaded' }, `${surface}: loaded shows only the image`);
    assert.deepEqual(states.error, { loading: { display: 'none', visible: false }, image: { display: 'none', visible: false }, fallback: { display: 'grid', visible: true }, state: 'error' }, `${surface}: error shows only the fallback`);
  }
  assert.deepEqual(report.content.frame, { width: 48, height: 48 }, 'readonly and preview content share the 48px thumbnail frame');
  assert.deepEqual(report.replacement, { state: 'loading', attribute: 'loading' }, 'a detached renderer cannot change its replacement');
  console.log('material thumbnail presentation Chromium: host, picker, content computed states and replacement isolation PASS');
} finally {
  try { cdp?.close(); } catch {}
  if (browser?.exitCode === null) browser.kill('SIGTERM');
  await new Promise((resolve) => browser?.once('exit', resolve) || resolve());
  await rm(profile, { recursive: true, force: true });
  await new Promise((resolve) => server.close(resolve));
}
