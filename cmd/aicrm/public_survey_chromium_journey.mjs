import fs from 'node:fs/promises';
import os from 'node:os';
import path from 'node:path';
import { spawn, spawnSync } from 'node:child_process';

const base = process.env.AICRM_PUBLIC_SURVEY_BROWSER_URL;
const session = process.env.AICRM_PUBLIC_SURVEY_BROWSER_SESSION;
const secondSession = process.env.AICRM_PUBLIC_SURVEY_BROWSER_SECOND_SESSION;
const successSlug = process.env.AICRM_PUBLIC_SURVEY_BROWSER_SUCCESS_SLUG;
const failureSlug = process.env.AICRM_PUBLIC_SURVEY_BROWSER_FAILURE_SLUG;
const redirectSlug = process.env.AICRM_PUBLIC_SURVEY_BROWSER_REDIRECT_SLUG;
const leadQRSlug = process.env.AICRM_PUBLIC_SURVEY_BROWSER_LEAD_QR_SLUG;
const redirectTarget = process.env.AICRM_PUBLIC_SURVEY_BROWSER_REDIRECT_TARGET;
const leadQRURL = process.env.AICRM_PUBLIC_SURVEY_BROWSER_LEAD_QR_URL;
const screenshots = process.env.AICRM_PUBLIC_SURVEY_SCREENSHOT_DIR;
if (!/^https:\/\//.test(base || '') || !/^[A-Za-z0-9_-]{43}$/.test(session || '') || !/^[A-Za-z0-9_-]{43}$/.test(secondSession || '') || session === secondSession || !/^[a-z0-9-]{1,128}$/.test(successSlug || '') || !/^[a-z0-9-]{1,128}$/.test(failureSlug || '') || !/^[a-z0-9-]{1,128}$/.test(redirectSlug || '') || !/^[a-z0-9-]{1,128}$/.test(leadQRSlug || '') || !redirectTarget?.startsWith(base + '/') || !leadQRURL?.startsWith(base + '/') || !path.isAbsolute(screenshots || '')) throw new Error('public Survey Chromium journey configuration is invalid');

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const chrome = () => {
  const candidates = process.platform === 'darwin' ? ['/Applications/Google Chrome.app/Contents/MacOS/Google Chrome', 'google-chrome', 'chromium'] : ['google-chrome', 'google-chrome-stable', 'chromium', 'chromium-browser'];
  for (const candidate of candidates) if ((candidate.includes('/') ? spawnSync(candidate, ['--version'], { stdio: 'ignore' }).status : spawnSync('which', [candidate], { stdio: 'ignore' }).status) === 0) return candidate;
  throw new Error('Chromium binary is unavailable');
};
class CDP {
  constructor(socket) {
    this.socket = socket; this.id = 0; this.successSubmissions = 0; this.failureSubmissions = 0; this.redirectSubmissions = 0; this.leadQRSubmissions = 0; this.topLevelNavigations = []; this.pending = new Map();
    socket.addEventListener('message', (event) => {
      const message = JSON.parse(String(event.data));
      if (message.method === 'Network.requestWillBeSent' && message.params?.request?.method === 'POST') {
        const requestURL = message.params.request.url || '';
        if (requestURL.includes('/api/public/questionnaires/' + successSlug + '/submissions')) this.successSubmissions += 1;
        if (requestURL.includes('/api/public/questionnaires/' + failureSlug + '/submissions')) this.failureSubmissions += 1;
        if (requestURL.includes('/api/public/questionnaires/' + redirectSlug + '/submissions')) this.redirectSubmissions += 1;
        if (requestURL.includes('/api/public/questionnaires/' + leadQRSlug + '/submissions')) this.leadQRSubmissions += 1;
      }
      if (message.method === 'Page.frameNavigated' && !message.params?.frame?.parentId) this.topLevelNavigations.push(message.params.frame.url || '');
      const pending = this.pending.get(message.id); if (!pending) return;
      this.pending.delete(message.id); message.error ? pending.reject(new Error('CDP request failed')) : pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
}
const evaluate = async (cdp, expression, label) => {
  const result = await cdp.call('Runtime.evaluate', { expression, awaitPromise: true, returnByValue: true });
  if (result.exceptionDetails) throw new Error(`${label}: ${result.exceptionDetails.exception?.description || result.exceptionDetails.text || 'evaluation failed'}`);
  return result.result?.value;
};
const waitFor = async (cdp, expression, label) => {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression, `${label} probe`)) return;
    await delay(50);
  }
  const evidence = await evaluate(cdp, `(() => ({path: location.pathname, state: document.body?.dataset.v3PublicSurvey || '', text: document.querySelector('#screen')?.textContent?.trim().slice(0, 500) || ''}))()`, `${label} evidence`);
  throw new Error(`${label}: ${JSON.stringify(evidence)}`);
};
const portURL = async (profile) => {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try { const port = String(await fs.readFile(path.join(profile, 'DevToolsActivePort'), 'utf8')).split('\n')[0]; if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch (_) {}
    await delay(50);
  }
  throw new Error('Chromium DevTools did not start');
};
const closeBrowser = async (browser) => {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return;
  browser.kill('SIGTERM');
  await Promise.race([new Promise((resolve) => browser.once('exit', resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) browser.kill('SIGKILL');
};
const screenshot = async (cdp, width, filename) => {
  await cdp.call('Emulation.setDeviceMetricsOverride', { width, height: 844, deviceScaleFactor: 1, mobile: true });
  await evaluate(cdp, 'document.fonts?.ready || Promise.resolve()', `${filename} fonts`);
  const layout = await evaluate(cdp, `(() => { const screen=document.querySelector('#screen'); const submit=document.querySelector('[data-h5-submit]'); return { viewport: document.documentElement.clientWidth, scrollWidth: document.documentElement.scrollWidth, screen: screen?.getBoundingClientRect().width || 0, submit: submit?.getBoundingClientRect().height || 0 }; })()`, `${filename} layout`);
  if (!layout || layout.viewport !== width || layout.scrollWidth > width || layout.screen < width - 2 || (layout.submit && layout.submit < 44)) throw new Error(`${filename} layout=${JSON.stringify(layout)}`);
  const image = await cdp.call('Page.captureScreenshot', { format: 'png', captureBeyondViewport: true });
  await fs.writeFile(path.join(screenshots, filename), Buffer.from(image.data, 'base64'), { mode: 0o600 });
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), 'aicrm-public-survey-chromium-'));
let browser;
let failed = false;
try {
  await fs.mkdir(screenshots, { recursive: true, mode: 0o700 });
  // Keep every configured completion URL public-looking. This Chrome-only
  // resolver maps the controlled hostname to the local TLS fixture without
  // weakening production completion-target validation or application config.
  browser = spawn(chrome(), ['--headless=new', '--no-sandbox', '--remote-debugging-port=0', '--user-data-dir=' + profile, '--no-first-run', '--ignore-certificate-errors', '--allow-insecure-localhost', '--no-proxy-server', '--host-resolver-rules=MAP example.com 127.0.0.1', '--user-agent=Mozilla/5.0 MicroMessenger/8.0', 'about:blank'], { stdio: 'ignore' });
  let address;
  try { address = await portURL(profile); } catch (error) { if (process.platform === 'darwin') { console.log('public_survey_chromium: SKIP_DEVTOOLS'); process.exit(0); } throw error; }
  const target = await (await fetch(address + '/json/new?about:blank', { method: 'PUT' })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener('open', resolve, { once: true }); socket.addEventListener('error', reject, { once: true }); });
  const cdp = new CDP(socket);
  await cdp.call('Page.enable'); await cdp.call('Runtime.enable'); await cdp.call('Network.enable');

  // Exercise the real /q -> OAuth Owner -> auth failure route. The start
  // response sets its own short-lived return cookie; blocking the external
  // authorization navigation prevents an outbound request while preserving
  // the exact Owner failure path for Chromium.
  await cdp.call('Network.setBlockedURLs', { urls: ['https://open.weixin.qq.com/*'] });
  await cdp.call('Page.navigate', { url: `${base}/api/h5/surveys/oauth/start?slug=${successSlug}` });
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const cookies = await cdp.call('Network.getAllCookies');
    if ((cookies.cookies || []).some((cookie) => cookie.name === 'survey_oauth_return' && cookie.value === successSlug)) break;
    if (attempt === 39) throw new Error('Survey Owner OAuth start did not issue its return cookie');
    await delay(50);
  }
  await cdp.call('Network.setBlockedURLs', { urls: [] });
  await cdp.call('Page.navigate', { url: `${base}/api/h5/surveys/oauth/callback?state=${'x'.repeat(43)}&code=controlled-failure` });
  await waitFor(cdp, `(() => { const stop=document.querySelector('#screen [data-v3-survey-stop]'); const text=stop?.textContent || ''; return location.pathname === '/h5/auth.html' && document.body?.dataset.v3PublicSurvey === 'auth' && stop?.querySelector('h1')?.textContent === '授权失败' && !stop?.querySelector('button') && !text.includes('正在验证微信身份') && !text.includes('重试微信授权'); })()`, 'Owner OAuth failure did not render the public stopped authorization state');
  for (const width of [375, 390, 430]) await screenshot(cdp, width, `public-survey-auth-${width}.png`);

  await cdp.call('Network.deleteCookies', { name: 'survey_oauth_return', url: `${base}/api/h5/surveys/oauth/callback` });
  await cdp.call('Page.navigate', { url: `${base}/api/h5/surveys/oauth/callback?state=${'y'.repeat(43)}&code=controlled-failure` });
  await waitFor(cdp, `(() => { const stop=document.querySelector('#screen [data-v3-survey-stop]'); const text=stop?.textContent || ''; const title=stop?.querySelector('h1')?.textContent || ''; return location.pathname === '/h5/error.html' && ['暂时无法继续','授权失败','无法继续','链接无效'].includes(title) && !stop?.querySelector('button') && !/增长诊断测评|后端能力未就绪|演示题|全部屏幕|正在验证微信身份|重试微信授权/.test(text); })()`, 'Owner OAuth failure without a return target did not render the public stopped error state');
  for (const width of [375, 390, 430]) await screenshot(cdp, width, `public-survey-oauth-error-${width}.png`);

  await cdp.call('Network.setCookie', { name: '__Host-aicrm_survey_identity', value: session, url: base, path: '/', secure: true, httpOnly: true, sameSite: 'Lax' });

  await cdp.call('Page.navigate', { url: `${base}/q/${successSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/all.html' && document.body?.dataset.v3PublicSurvey === 'all'`, 'authorized public all-in-one route did not mount');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-question-id] label[data-option-id]')) && Boolean(document.querySelector('#screen [data-h5-submit]'))`, 'actual public answer form did not render');
  for (const width of [375, 390, 430]) await screenshot(cdp, width, `public-survey-answer-${width}.png`);
  await evaluate(cdp, `document.querySelector('#screen [data-h5-submit]').click(); true`, 'submit incomplete required answer');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-h5-error]')) && document.querySelector('#screen [data-h5-submit]')?.disabled === false`, 'required-answer validation did not retain an editable retry state');
  if (cdp.successSubmissions !== 0) throw new Error(`required-answer validation submitted=${cdp.successSubmissions}`);
  await evaluate(cdp, `document.querySelector('#screen label[data-option-id]').click(); true`, 'select public answer');
  await waitFor(cdp, `document.querySelector('#screen label[data-option-id]')?.getAttribute('aria-pressed') === 'true'`, 'selected answer did not remain visible');
  await evaluate(cdp, `(() => { const button=document.querySelector('#screen [data-h5-submit]'); button.click(); button.click(); return true; })()`, 'double click public submit');
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.body?.dataset.v3PublicSurvey === 'done' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷' && !document.querySelector('#screen [data-h5-lead-qr]')`, 'successful submission did not replace the answer page with the default completion page');
  if (cdp.successSubmissions !== 1) throw new Error(`success submission requests=${cdp.successSubmissions}`);
  for (const width of [375, 390, 430]) await screenshot(cdp, width, `public-survey-done-${width}.png`);
  await cdp.call('Page.navigate', { url: `${base}/q/${successSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷'`, 'revisiting a submitted survey did not bypass the answer route');
  if (cdp.successSubmissions !== 1) throw new Error(`submitted survey revisit created a second submission=${cdp.successSubmissions}`);
  await evaluate(cdp, 'history.back(); true', 'go back after default completion');
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷' && !document.querySelector('#screen [data-question-id], #screen [data-h5-submit]')`, 'browser back returned a submitted visitor to the answer form');
  if (cdp.successSubmissions !== 1) throw new Error(`browser back created a second success submission=${cdp.successSubmissions}`);
  await cdp.call('Network.deleteCookies', { name: '__Host-aicrm_survey_identity', url: base });
  await cdp.call('Network.setCookie', { name: '__Host-aicrm_survey_identity', value: secondSession, url: base, path: '/', secure: true, httpOnly: true, sameSite: 'Lax' });
  await cdp.call('Page.navigate', { url: `${base}/q/${successSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷' && !document.querySelector('#screen [data-question-id], #screen [data-h5-submit]')`, 'a second session for the same canonical customer bypassed the submission gate');
  if (cdp.successSubmissions !== 1) throw new Error(`second session created a second success submission=${cdp.successSubmissions}`);
  await cdp.call('Page.navigate', { url: `${base}/h5/all.html?slug=${successSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.body?.dataset.v3PublicSurvey === 'done' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷' && !document.querySelector('#screen [data-question-id], #screen [data-h5-submit]')`, 'direct all-in-one H5 route bypassed the submitted-session gate');

  await cdp.call('Page.navigate', { url: `${base}/q/${failureSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/one.html' && document.body?.dataset.v3PublicSurvey === 'one' && document.querySelector('#screen [data-h5-progress]')?.textContent?.includes('1 / 2')`, 'authorized one-by-one route or progress did not mount');
  await evaluate(cdp, `document.querySelector('#screen label[data-option-id]').click(); true`, 'select retry answer');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-next]').click(); true`, 'advance to final question');
  await waitFor(cdp, `document.querySelector('#screen [data-h5-submit]') && document.querySelector('#screen [data-h5-progress]')?.textContent?.includes('2 / 2')`, 'final one-by-one submit step did not render');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-submit]').click(); true`, 'submit controlled failure');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-v3-survey-submitting]')) && !document.querySelector('#screen [data-h5-submit]')`, 'one-by-one submission did not expose its stable pending feedback');
  await waitFor(cdp, `document.querySelector('#screen [data-v3-survey-recovery]')?.textContent === '暂时无法完成操作，请保留当前页面并稍后重试。' && document.querySelector('#screen [data-v3-survey-error-detail]')?.textContent === '问题详情：HTTP 503' && document.querySelector('#screen [data-h5-submit]')?.disabled === false`, 'failure did not expose a recoverable transport explanation and retry action');
  if (cdp.failureSubmissions !== 1) throw new Error(`first failure submission requests=${cdp.failureSubmissions}`);
  for (const width of [375, 390, 430]) await screenshot(cdp, width, `public-survey-failure-${width}.png`);
  await evaluate(cdp, `(() => { document.querySelector('#screen [data-h5-previous]')?.click(); return true; })()`, 'return to preserved answer');
  await waitFor(cdp, `document.querySelector('#screen label[data-option-id]')?.getAttribute('aria-pressed') === 'true'`, 'first failed submission cleared the selected answer');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-next]').click(); true`, 'return to final retry step');
  await waitFor(cdp, `Boolean(document.querySelector('#screen [data-h5-submit]'))`, 'final retry submit step did not restore');
  await evaluate(cdp, `document.querySelector('#screen [data-h5-submit]').click(); true`, 'retry reaches the real Survey Owner');
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷'`, 'recovered one-by-one submission did not finish at the default completion page');
  if (cdp.failureSubmissions !== 2) throw new Error(`recovery submission requests=${cdp.failureSubmissions}`);
  await cdp.call('Page.navigate', { url: `${base}/h5/one.html?slug=${failureSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.body?.dataset.v3PublicSurvey === 'done' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷' && !document.querySelector('#screen [data-question-id], #screen [data-h5-submit]')`, 'direct one-by-one H5 route bypassed the submitted-session gate');

  await cdp.call('Page.navigate', { url: `${base}/q/${redirectSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/all.html' && document.body?.dataset.v3PublicSurvey === 'all' && Boolean(document.querySelector('#screen [data-h5-submit]'))`, 'redirect questionnaire did not mount its public answer form');
  const redirectNavigationStart = cdp.topLevelNavigations.length;
  await evaluate(cdp, `document.querySelector('#screen label[data-option-id]').click(); document.querySelector('#screen [data-h5-submit]').click(); true`, 'submit redirect questionnaire');
  await waitFor(cdp, `location.href === ${JSON.stringify(redirectTarget)} && document.querySelector('#browser-completion-redirect')?.textContent === '受控完成跳转目标' && !document.querySelector('[data-h5-done]')`, 'redirect completion did not replace the answer page with its safe target');
  if (cdp.redirectSubmissions !== 1) throw new Error(`redirect submission requests=${cdp.redirectSubmissions}`);
  if (cdp.topLevelNavigations.slice(redirectNavigationStart).some((target) => target.includes('/h5/done.html'))) throw new Error(`redirect completion flashed default done page: ${JSON.stringify(cdp.topLevelNavigations.slice(redirectNavigationStart))}`);
  await evaluate(cdp, 'history.back(); true', 'go back after redirect completion');
  await waitFor(cdp, `!document.querySelector('[data-question-id], [data-h5-submit]') && (document.querySelector('[data-h5-done] h1')?.textContent === '收到你的问卷' || document.querySelector('#browser-completion-redirect')?.textContent === '受控完成跳转目标')`, 'browser back after redirect completion returned to an answer form');
  if (cdp.redirectSubmissions !== 1) throw new Error(`redirect browser back created a second submission=${cdp.redirectSubmissions}`);
  const redirectRevisitNavigationStart = cdp.topLevelNavigations.length;
  await cdp.call('Page.navigate', { url: `${base}/q/${redirectSlug}` });
  await waitFor(cdp, `location.href === ${JSON.stringify(redirectTarget)} && document.querySelector('#browser-completion-redirect')?.textContent === '受控完成跳转目标' && !document.querySelector('[data-h5-done], [data-question-id], [data-h5-submit]')`, 'revisiting a redirect completion did not use the safe target');
  if (cdp.redirectSubmissions !== 1) throw new Error(`redirect revisit created a second submission=${cdp.redirectSubmissions}`);
  if (cdp.topLevelNavigations.slice(redirectRevisitNavigationStart).some((target) => target.includes('/h5/done.html'))) throw new Error(`redirect revisit flashed default done page: ${JSON.stringify(cdp.topLevelNavigations.slice(redirectRevisitNavigationStart))}`);

  await cdp.call('Page.navigate', { url: `${base}/q/${leadQRSlug}` });
  await waitFor(cdp, `location.pathname === '/h5/all.html' && document.body?.dataset.v3PublicSurvey === 'all' && Boolean(document.querySelector('#screen [data-h5-submit]'))`, 'lead QR questionnaire did not mount its public answer form');
  await evaluate(cdp, `document.querySelector('#screen label[data-option-id]').click(); document.querySelector('#screen [data-h5-submit]').click(); true`, 'submit lead QR questionnaire');
  await waitFor(cdp, `location.pathname === '/h5/done.html' && document.body?.dataset.v3PublicSurvey === 'done' && document.querySelector('#screen [data-h5-done] h1')?.textContent === '收到你的问卷' && document.querySelector('#screen [data-h5-lead-qr] img')?.src === ${JSON.stringify(leadQRURL)} && document.querySelector('#screen [data-h5-lead-qr] img')?.complete === true && document.querySelector('#screen [data-h5-lead-qr] img')?.naturalWidth > 0`, 'lead QR completion did not render its authorized image');
  if (cdp.leadQRSubmissions !== 1) throw new Error(`lead QR submission requests=${cdp.leadQRSubmissions}`);
  for (const width of [375, 390, 430]) await screenshot(cdp, width, `public-survey-lead-qr-${width}.png`);
  console.log(JSON.stringify({ success_submission_requests: cdp.successSubmissions, recovery_submission_requests: cdp.failureSubmissions, redirect_submission_requests: cdp.redirectSubmissions, lead_qr_submission_requests: cdp.leadQRSubmissions }));
  console.log('public_survey_chromium: PASS');
  socket.close();
} catch (error) {
  failed = true;
  throw error;
} finally {
  await closeBrowser(browser);
  // Chromium can finish a late profile write after its root process exits. Retry
  // only this temporary-profile removal; a successful journey still reports an
  // actual cleanup failure once the bounded retry window is exhausted.
  try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }); } catch (error) { if (!failed) throw error; }
}
