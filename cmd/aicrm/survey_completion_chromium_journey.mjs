import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

const base = process.env.AICRM_SURVEY_BROWSER_URL;
const username = process.env.AICRM_SURVEY_BROWSER_USERNAME;
const password = process.env.AICRM_SURVEY_BROWSER_PASSWORD;
const questionnaireID = process.env.AICRM_SURVEY_BROWSER_QUESTIONNAIRE_ID;
const webhook = process.env.AICRM_SURVEY_BROWSER_WEBHOOK;
if (!/^https:\/\//.test(base || '') || !username || !password || !/^[1-9][0-9]*$/.test(questionnaireID || '') || !/^https:\/\//.test(webhook || '')) throw new Error('survey Chromium journey configuration is invalid');

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const chrome = () => {
  const candidates = process.platform === 'darwin' ? ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'google-chrome', 'chromium'] : ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'];
  for (const candidate of candidates) if ((candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status : spawnSync('which', [candidate], { stdio: 'ignore' }).status) === 0) return candidate;
  throw new Error('Chromium binary is unavailable');
};
class CDP {
  constructor(socket) { this.socket = socket; this.id = 0; this.saveRequests = 0; this.pending = new Map(); socket.addEventListener('message', (event) => { const message = JSON.parse(String(event.data)); if(message.method==='Network.requestWillBeSent' && message.params?.request?.method==='PUT' && message.params.request.url.endsWith('/external-push')) this.saveRequests++; const pending = this.pending.get(message.id); if (!pending) return; this.pending.delete(message.id); message.error ? pending.reject(new Error('CDP request failed')) : pending.resolve(message.result || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
}
const domEvidence = async (cdp) => {
  const result = await cdp.call('Runtime.evaluate', { expression: `(() => ({path:location.pathname, title:document.title, headings:[...document.querySelectorAll('h1,h2,h3')].map((item)=>item.textContent.trim()).slice(0,12), ids:[...document.querySelectorAll('[id]')].map((item)=>item.id).filter((id)=>/ops|configuration|toast|questionnaire/i.test(id)).slice(0,24), configuration_tag:document.querySelector('#opsConfigurationReference')?.tagName||'', toast:document.querySelector('[data-toast]')?.textContent||'', buttons:[...document.querySelectorAll('button')].map((item)=>item.textContent.trim()).filter(Boolean).slice(0,16), forms:[...document.forms].map((item)=>item.getAttribute('action')||'').slice(0,8)}))()`, returnByValue: true });
  return JSON.stringify(result.result?.value || {});
};
const evaluate = async (cdp, expression, step = 'evaluate') => {
  const result = await cdp.call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) {
    const detail = result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'Runtime.evaluate exception';
    throw new Error(step + ': ' + String(detail).slice(0, 400) + ' dom=' + (await domEvidence(cdp)).slice(0, 1800));
  }
  return result.result?.value;
};
const waitFor = async (cdp, expression, message) => { for (let i = 0; i < 400; i += 1) { if (await evaluate(cdp, expression, message + ' probe')) return; await delay(50); } throw new Error(message + ' dom=' + await domEvidence(cdp)); };
const portURL = async (profile) => { for (let i = 0; i < 160; i += 1) { try { const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; if (/^\d+$/.test(port)) return 'http://127.0.0.1:' + port; } catch (_) {} await delay(50); } throw new Error('Chromium DevTools did not start'); };

const waitForBrowserExit = async (child, timeoutMilliseconds) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    child.once('exit', () => { clearTimeout(timer); resolve(true); });
  });
};
const removeProfile = async (profile) => {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; }
    catch (error) { if (!error || !['ENOTEMPTY', 'EBUSY', 'EPERM'].includes(error.code)) return false; await delay(100); }
  }
  return false;
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-survey-chromium-'));
let browser;
let journeyFailed = false;
try {
  browser = spawn(chrome(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', '--user-data-dir=' + profile, '--no-first-run', '--ignore-certificate-errors', '--allow-insecure-localhost', '--no-proxy-server', '--host-resolver-rules=MAP example.com 127.0.0.1', 'about:blank'], { stdio: 'ignore' });
  let address;
  try { address = await portURL(profile); } catch (error) { if (process.platform === 'darwin') { console.log('survey_completion_chromium: SKIP_DEVTOOLS'); process.exit(0); } throw error; }
  const created = await (await fetch(address + '/json/new?about:blank', { method: 'PUT' })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  const cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable'); await cdp.call('Network.enable');
  const page = '/admin/questionnaireOps.html?id=' + questionnaireID;
  await cdp.call('Page.navigate', { url: base + '/login?next=' + encodeURIComponent(page) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"]'))", 'login did not render');
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`, 'submit login');
  await waitFor(cdp, "location.pathname === '/admin/questionnaireOps.html'", 'login did not reach questionnaire operations');
  await waitFor(cdp, "Boolean(document.querySelector('.qo-page')) && Boolean(document.querySelector('[data-param-name]')) && document.querySelector('[data-push-enabled]')?.checked === false", 'legacy-parity operations configuration did not finish loading');
  await evaluate(cdp, "document.querySelector('[data-tab=\"push\"]')?.click(); true", 'open external push tab');
  await waitFor(cdp, "Boolean(document.querySelector('#qo-push-url')) && Boolean(document.querySelector('[data-save-push]')) && Boolean(document.querySelector('[data-param-name]'))", 'legacy external push fields did not render');
  await evaluate(cdp, `(() => {
    const set=(selector,value)=>{const node=document.querySelector(selector);if(!node)throw new Error('missing '+selector);node.value=value;node.dispatchEvent(new Event('input',{bubbles:true}));};
    const toggle=document.querySelector('[data-push-enabled]'); toggle.checked=true; toggle.dispatchEvent(new Event('change',{bubbles:true}));
    set('[data-webhook]',${JSON.stringify(webhook)}); set('[data-push-type]','subscription'); set('[data-expires]','2147483000'); set('[data-day]','45'); set('[data-frequency]','2'); set('[data-remark]','browser parity');
    return true;
  })()`, 'enable legacy external push fields');
  await waitFor(cdp, "Boolean(document.querySelector('[data-param-name]')) && Boolean(document.querySelector('[data-param-value]'))", 'external push parameter row did not render');
  await evaluate(cdp, `(() => {
    const set=(selector,value)=>{const node=document.querySelector(selector);if(!node)throw new Error('missing '+selector);node.value=value;node.dispatchEvent(new Event('input',{bubbles:true}));};
    set('[data-param-name]','campaign'); set('[data-param-value]','survey-browser');
    document.querySelector('[data-save-push]').click(); return true;
  })()`, 'save legacy external push fields');
  await waitFor(cdp, "document.querySelector('[data-toast]')?.textContent === '外部推送已保存'", 'visible configuration save confirmation did not render');
  if(cdp.saveRequests!==1) throw new Error('header save request count='+cdp.saveRequests);
  const reloadMarker = 'survey-journey-reload';
  await evaluate(cdp, `window.__surveyJourneyReloadMarker=${JSON.stringify(reloadMarker)}; location.reload(); true`, "reload saved configuration");
  await waitFor(cdp, `document.readyState === 'complete' && window.__surveyJourneyReloadMarker !== ${JSON.stringify(reloadMarker)} && Boolean(document.querySelector('.qo-page'))`, 'saved configuration page did not reload');
  await evaluate(cdp, "document.querySelector('[data-tab=\"push\"]')?.click(); true", 'reopen external push tab');
  await waitFor(cdp, `document.querySelector('[data-webhook]')?.value === ${JSON.stringify(webhook)}`, 'saved webhook did not reload');
  const savedFields = await evaluate(cdp, `(() => ({enabled:document.querySelector('[data-push-enabled]')?.checked, webhook:document.querySelector('[data-webhook]')?.value, type:document.querySelector('[data-push-type]')?.value, expires:document.querySelector('[data-expires]')?.value, day:document.querySelector('[data-day]')?.value, frequency:document.querySelector('[data-frequency]')?.value, remark:document.querySelector('[data-remark]')?.value, paramName:document.querySelector('[data-param-name]')?.value, paramValue:document.querySelector('[data-param-value]')?.value}))()`, 'read saved legacy push fields');
  const expectedFields = { enabled:true, webhook, type:'subscription', expires:'2147483000', day:'45', frequency:'2', remark:'browser parity', paramName:'campaign', paramValue:'survey-browser' };
  if (JSON.stringify(savedFields) !== JSON.stringify(expectedFields)) throw new Error('saved legacy push fields mismatch=' + JSON.stringify(savedFields));
  await evaluate(cdp, "document.querySelector('[data-test]').click(); true", 'queue controlled test push');
  await waitFor(cdp, "document.querySelector('[data-toast]')?.textContent.startsWith('测试推送已排队')", 'controlled test receipt did not render');
  if(cdp.saveRequests!==2) throw new Error('test push save request count='+cdp.saveRequests);
  console.log('survey_completion_chromium: PASS');
  socket.close();
} catch (error) {
  journeyFailed = true;
  throw error;
} finally {
  if (browser && browser.exitCode === null && browser.signalCode === null) {
    browser.kill('SIGTERM');
    if (!await waitForBrowserExit(browser, 3000)) {
      browser.kill('SIGKILL');
      await waitForBrowserExit(browser, 1000);
    }
  }
  const removed = await removeProfile(profile);
  if (!removed && !journeyFailed) throw new Error('Chromium test profile cleanup did not complete');
}
