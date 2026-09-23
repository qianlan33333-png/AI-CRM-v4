import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = String(process.env.AICRM_REMAINING_PAGES_URL || "").replace(/\/$/, "");
const session = process.env.AICRM_REMAINING_PAGES_ADMIN_SESSION || "";
const csrf = process.env.AICRM_REMAINING_PAGES_CSRF || "";
const customerID = process.env.AICRM_REMAINING_PAGES_ARCHIVE_CUSTOMER_ID || "";
const radarCode = process.env.AICRM_REMAINING_PAGES_RADAR_CODE || "";
const couponSlug = process.env.AICRM_REMAINING_PAGES_COUPON_SLUG || "";
const gridToken = process.env.AICRM_REMAINING_PAGES_GRID_TOKEN || "";
const screenshotDirectory = process.env.AICRM_REMAINING_PAGES_SCREENSHOT_DIR || "";
const revision = process.env.AICRM_REMAINING_PAGES_REVISION || "";
if (!/^https:\/\/127\.0\.0\.1:\d+$/.test(baseURL) || !session || !csrf || !/^[1-9]\d*$/.test(customerID) || !/^rd_[A-Za-z0-9_-]{16,64}$/.test(radarCode) || !/^[a-z0-9-]{3,80}$/.test(couponSlug) || !/^mgshare1\./.test(gridToken) || !path.isAbsolute(screenshotDirectory) || !/^[0-9a-f]{40}$/.test(revision)) {
  throw new Error("remaining-page Chromium journey environment is incomplete");
}

const delay = ms => new Promise(resolve => setTimeout(resolve, ms));
const browserBinary = () => {
  for (const candidate of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) {
    if ((candidate.includes("/") ? spawnSync(candidate, ["--version"], { stdio: "ignore" }) : spawnSync("which", [candidate], { stdio: "ignore" })).status === 0) return candidate;
  }
  throw new Error("Chromium is unavailable");
};
class CDP {
  constructor(socket) { this.socket = socket; this.next = 0; this.pending = new Map(); this.events = new Map(); socket.addEventListener("message", event => { const message = JSON.parse(String(event.data)); if (message.id && this.pending.has(message.id)) { const pending = this.pending.get(message.id); this.pending.delete(message.id); message.error ? pending.reject(new Error(`CDP ${message.error.code}`)) : pending.resolve(message.result || {}); return; } for (const listener of this.events.get(message.method) || []) listener(message.params || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.next; const timer = setTimeout(() => { this.pending.delete(id); reject(new Error(`CDP ${method} timed out`)); }, 8000); this.pending.set(id, { resolve: value => { clearTimeout(timer); resolve(value); }, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  on(method, listener) { const listeners = this.events.get(method) || []; listeners.push(listener); this.events.set(method, listeners); }
  close() { for (const pending of this.pending.values()) pending.reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}
async function debuggingAddress(profile) { for (let attempt = 0; attempt < 160; attempt += 1) { try { const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch (_) {} await delay(50); } throw new Error("Chromium DevTools did not start"); }
async function evaluate(cdp, expression) { const response = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true }); if (response.exceptionDetails) throw new Error(`page evaluation failed: ${response.exceptionDetails.exception?.description || response.exceptionDetails.text || "unknown"}`); return response.result?.value; }
async function waitFor(cdp, expression, message) { for (let attempt = 0; attempt < 180; attempt += 1) { if (await evaluate(cdp, expression)) return; await delay(50); } throw new Error(message); }
async function waitForCondition(condition, message) { for (let attempt = 0; attempt < 180; attempt += 1) { if (condition()) return; await delay(50); } throw new Error(message); }
async function stopBrowser(browser) { if (!browser || browser.exitCode !== null || browser.signalCode !== null) return; browser.kill("SIGTERM"); await Promise.race([new Promise(resolve => browser.once("exit", resolve)), delay(3000)]); if (browser.exitCode === null && browser.signalCode === null) browser.kill("SIGKILL"); }

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-remaining-pages-chromium-"));
let browser; let cdp; let failed = false;
try {
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--ignore-certificate-errors", "--allow-insecure-localhost", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "about:blank"], { stdio: "ignore" });
  const target = await (await fetch(`${await debuggingAddress(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("CDP connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const exceptions = [];
  const radarEventResponses = [];
  cdp.on("Runtime.exceptionThrown", event => { if (exceptions.length < 8) exceptions.push(event.exceptionDetails?.exception?.description || event.exceptionDetails?.text || "runtime exception"); });
  cdp.on("Network.responseReceived", event => { try { const target = new URL(String(event.response?.url || "")); if (target.origin === baseURL && target.pathname === `/api/public/radar/${radarCode}/events`) radarEventResponses.push(Number(event.response?.status || 0)); } catch (_) {} });
  const resize = width => cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: width < 600, screenWidth: width, screenHeight: 900 });
  const settle = () => evaluate(cdp, "(document.fonts && document.fonts.ready ? document.fonts.ready : Promise.resolve()).then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))");
  const capture = async name => { await settle(); const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false }); await fs.writeFile(path.join(screenshotDirectory, `remaining-pages-${revision.slice(0, 12)}-${name}`), Buffer.from(image.data, "base64"), { mode: 0o600 }); };
  const noOverflow = async label => { const layout = await evaluate(cdp, "({viewport:innerWidth,scrollWidth:document.documentElement.scrollWidth,bodyWidth:document.body?.scrollWidth||0})"); if (layout.scrollWidth > layout.viewport + 1 || layout.bodyWidth > layout.viewport + 1) throw new Error(`${label} horizontal overflow ${JSON.stringify(layout)}`); };
  const visit = async pagePath => { await cdp.call("Page.navigate", { url: baseURL + pagePath }); await delay(80); };
  await cdp.call("Network.setCookies", { cookies: [
    { name: "aicrm_admin_session", value: session, url: baseURL, secure: true, httpOnly: true, sameSite: "Lax" },
    { name: "aicrm_csrf", value: csrf, url: baseURL, secure: true, sameSite: "Lax" },
  ] });

  for (const width of [1280, 1440]) {
    await resize(width); await visit("/admin/message-archive");
    await waitFor(cdp, "document.querySelector('[data-message-archive-entry]')", "message archive entry did not mount");
    assert.equal(await evaluate(cdp, "document.querySelectorAll('.admin-page-title').length"), 1, "archive entry must keep one shared page title");
    assert.equal(await evaluate(cdp, "document.querySelectorAll('[data-message-archive-entry] h2').length"), 0, "archive entry must not duplicate the shared title");
    assert.equal(await evaluate(cdp, "document.querySelector('.admin-topbar a[href=\"/admin/customers\"]')?.textContent?.trim()"), "选择用户", "archive entry must keep user selection in the shared header");
    assert.match(await evaluate(cdp, "document.querySelector('[data-message-archive-entry]')?.textContent || ''"), /先从现有用户目录选择用户/, "archive entry must not invent a global list");
    await noOverflow(`archive entry ${width}`); await capture(`archive-entry-${width}.png`);
  }
  for (const width of [1280, 1440]) {
    await resize(width); await visit(`/admin/message-archive/customers/${customerID}`);
    await waitFor(cdp, "document.querySelector('[data-message-archive-root]') && document.querySelector('#archive-message-list article')", "message archive customer detail did not load fixture message");
    assert.equal(await evaluate(cdp, "document.querySelectorAll('.admin-page-title').length"), 1, "archive detail must keep one shared page title");
    assert.equal(await evaluate(cdp, "document.querySelectorAll('[data-message-archive-root] h2').length"), 0, "archive detail must not duplicate the shared title");
    assert.match(await evaluate(cdp, "document.querySelector('#archive-message-list')?.textContent || ''"), /存档浏览器夹具消息/, "archive detail must render protected local message");
    if (width === 1440) { await evaluate(cdp, "document.querySelector('input[name=q]').value='存档浏览器';document.querySelector('#archive-search-form').requestSubmit();true"); await waitFor(cdp, "document.querySelector('#archive-message-list article')", "message archive explicit search lost result"); }
    await noOverflow(`archive detail ${width}`); await capture(`archive-detail-${width}.png`);
  }

  for (const width of [375, 390, 430]) {
    const previousRadarEvents = radarEventResponses.length;
    await resize(width); await visit(`/r/${radarCode}`);
    await waitFor(cdp, "document.querySelector('#root img.view')?.complete", `radar image did not render at ${width}`);
    assert.equal(await evaluate(cdp, "document.querySelector('#root img.view')?.naturalWidth > 0"), true, `radar image must load at ${width}`);
	assert.equal(await evaluate(cdp, "(()=>{const image=document.querySelector('#root img.view');if(!image)return false;const box=image.getBoundingClientRect();return image.naturalWidth>=320&&image.naturalHeight>=180&&box.width>=Math.min(320,innerWidth)&&box.height>=160})()"), true, `radar image must be visibly reviewable at ${width}`);
    await waitForCondition(() => radarEventResponses.length > previousRadarEvents && radarEventResponses.at(-1) === 200, `radar image-loaded event did not complete at ${width}`);
    await noOverflow(`radar ${width}`); await capture(`radar-${width}.png`);
  }
  assert.equal(radarEventResponses.some(status => status === 200), true, "radar image-loaded event must complete through the same-origin owner route");
  await visit("/r/rd_missing_remaining_pages");
  await waitFor(cdp, "location.pathname === '/r/rd_missing_remaining_pages' && /404|not[_ ]found/i.test(document.body?.textContent || '')", "radar missing route did not render a not-found boundary");
  assert.equal(await evaluate(cdp, "Boolean(document.querySelector('#root img.view'))"), false, "unknown radar code must not render content");

  for (const width of [375, 390, 430]) {
    await resize(width); await visit(`/c/${couponSlug}`);
    await waitFor(cdp, "document.querySelector('[data-route-owner=ai_crm_next]') && document.querySelector('#claimButton')", `coupon page did not render at ${width}`);
    assert.match(await evaluate(cdp, "document.querySelector('h1')?.textContent || ''"), /剩余页面公开优惠券/, "coupon page must show fixture coupon");
    assert.match(await evaluate(cdp, "document.querySelector('.rule')?.textContent || ''"), /领取时间：/, "coupon page must render its active claim-window disclosure");
    assert.match(await evaluate(cdp, "document.querySelector('.wechat-tip')?.textContent || ''"), /请使用微信打开此页面后领取优惠券/, "coupon page must disclose the non-WeChat boundary");
    assert.equal(await evaluate(cdp, "document.querySelector('#claimButton')?.disabled"), true, "non-WeChat coupon page must retain safe disabled claim");
    assert.equal(await evaluate(cdp, "document.querySelector('#claimButton')?.textContent?.trim()"), "请在微信中领取", "non-WeChat coupon page must not report an inactive coupon as its disabled reason");
    await noOverflow(`coupon ${width}`); await capture(`coupon-${width}.png`);
  }
  await visit("/c/missing-remaining-pages-coupon");
  await waitFor(cdp, "location.pathname === '/c/missing-remaining-pages-coupon' && /404|not[_ ]found/i.test(document.body?.textContent || '')", "coupon missing route did not render a not-found boundary");
  assert.equal(await evaluate(cdp, "Boolean(document.querySelector('[data-route-owner=ai_crm_next]'))"), false, "unknown coupon slug must not render a public coupon");

  for (const width of [375, 390, 430]) {
    await resize(width); await visit(`/shared/service-period-member-grid#${encodeURIComponent(gridToken)}`);
    await waitFor(cdp, "document.querySelector('#spGridBody tr[data-record-id]')", `member grid public share did not load at ${width}`);
    await waitFor(cdp, "document.querySelector('#spResultSummary')?.textContent?.trim() === '当前显示 1 行'", `member grid unknown total must show its visible row at ${width}`);
    assert.equal(await evaluate(cdp, `document.body.textContent.includes(${JSON.stringify(gridToken)})`), false, "member-grid token must not render into page text");
    await noOverflow(`member grid ${width}`); await capture(`member-grid-${width}.png`);
  }
  await visit("/shared/service-period-member-grid#mgshare1.invalid.invalid");
  // A fragment-only navigation keeps the already-mounted grid document alive.
  // Reload so the public host rereads the fragment as a fresh credential.
  await cdp.call("Page.reload", { ignoreCache: true });
  await waitFor(cdp, "(document.querySelector('#spResultSummary')?.textContent || '').includes('无法访问共享数据')", "member-grid invalid share boundary did not render");
  assert.equal(await evaluate(cdp, "Boolean(document.querySelector('#spGridBody tr[data-record-id]'))"), false, "invalid member-grid share must not render data");
  assert.match(await evaluate(cdp, "document.querySelector('#spResultSummary')?.textContent || ''"), /无法访问共享数据/, "invalid member-grid share must disclose unavailable shared data");
  if (exceptions.length) throw new Error(`runtime exceptions: ${exceptions.join('; ')}`);
  console.log(`remaining_pages_chromium: PASS revision=${revision} screenshots=${screenshotDirectory}`);
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  // Chromium can finish a late profile write after its root process exits.
  // Keep cleanup bounded and preserve the actual journey failure when present.
  try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 3, retryDelay: 100 }); } catch (error) { if (failed) console.error(`remaining_pages_chromium: profile cleanup failed after journey error: ${error?.code || error?.message || "unknown"}`); else throw error; }
}
