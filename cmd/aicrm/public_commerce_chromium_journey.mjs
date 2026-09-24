import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = String(process.env.AICRM_PUBLIC_COMMERCE_TEST_URL || "").replace(/\/$/, "");
const screenshots = process.env.AICRM_PUBLIC_COMMERCE_SCREENSHOT_DIR;
const standardCode = process.env.AICRM_PUBLIC_COMMERCE_STANDARD_CODE;
const serviceCode = process.env.AICRM_PUBLIC_COMMERCE_SERVICE_CODE;
const unavailableServiceCode = process.env.AICRM_PUBLIC_COMMERCE_UNAVAILABLE_SERVICE_CODE;
const trustedSession = String(process.env.AICRM_PUBLIC_COMMERCE_TRUSTED_SESSION || "");
if (!/^https:\/\/127\.0\.0\.1:\d+$/.test(baseURL) || !path.isAbsolute(screenshots || "") || !standardCode || !serviceCode || !unavailableServiceCode || !/^aicrm_payment_session=pays_[A-Za-z0-9_-]{20,}$/.test(trustedSession)) {
  throw new Error("public commerce Chromium journey environment is incomplete");
}
const [trustedCookieName, trustedCookieValue] = trustedSession.split("=", 2);
const browserUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) MicroMessenger/8.0.50 Mobile/15E148 Safari/604.1";
const nonWeChatUserAgent = "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 Version/17.0 Mobile/15E148 Safari/604.1";

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const browserBinary = () => {
  for (const candidate of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) {
    if ((candidate.includes("/") ? spawnSync(candidate, ["--version"], { stdio: "ignore" }) : spawnSync("which", [candidate], { stdio: "ignore" })).status === 0) return candidate;
  }
  throw new Error("Chromium is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.next = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", event => {
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
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 8000);
      this.pending.set(id, {
        resolve: value => { clearTimeout(timer); resolve(value); },
        reject,
      });
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
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium DevTools did not start");
}

async function evaluate(cdp, expression) {
  const response = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (response.exceptionDetails) throw new Error(`page evaluation failed: ${response.exceptionDetails.exception?.description || response.exceptionDetails.text || "unknown"}`);
  return response.result?.value;
}

async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
}

async function stopBrowser(browser) {
  if (!browser || browser.exitCode !== null || browser.signalCode !== null) return;
  browser.kill("SIGTERM");
  await Promise.race([new Promise(resolve => browser.once("exit", resolve)), delay(3000)]);
  if (browser.exitCode === null && browser.signalCode === null) browser.kill("SIGKILL");
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-public-commerce-chromium-"));
let browser;
let cdp;
try {
  await fs.mkdir(screenshots, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--ignore-certificate-errors", "--allow-insecure-localhost", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "about:blank"], { stdio: "ignore" });
  const target = await (await fetch(`${await debuggingAddress(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("CDP connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");

  const exceptions = [];
  const publicAssets = new Map();
  cdp.on("Runtime.exceptionThrown", event => {
    if (exceptions.length < 8) exceptions.push(event.exceptionDetails?.text || "runtime exception");
  });
  cdp.on("Network.responseReceived", event => {
    try {
      const resource = new URL(String(event.response?.url || ""));
      if (resource.origin === baseURL && resource.pathname.startsWith("/product-public-assets/")) publicAssets.set(resource.pathname, Number(event.response?.status || 0));
    } catch (_) {}
  });

  const resize = width => cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 860, deviceScaleFactor: 1, mobile: true, screenWidth: width, screenHeight: 860 });
  const setUserAgent = userAgent => cdp.call("Emulation.setUserAgentOverride", { userAgent });
  const settleLayout = () => evaluate(cdp, "(document.fonts && document.fonts.ready ? document.fonts.ready : Promise.resolve()).then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))");
  const capture = async filename => {
    await settleLayout();
    const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshots, filename), Buffer.from(image.data, "base64"), { mode: 0o600 });
  };

  const visitUnauthenticatedGate = async () => {
    await resize(375);
    await setUserAgent(nonWeChatUserAgent);
    await cdp.call("Page.navigate", { url: baseURL + `/pay/${encodeURIComponent(standardCode)}` });
    await waitFor(cdp, "document.querySelector('[data-v3-public-commerce]')?.dataset.publicCommerceMounted === 'true'", "presentation Host did not mount unauthenticated public payment");
    await waitFor(cdp, "document.querySelector('#identityGate:not([hidden])') && document.querySelector('#identityTitle')?.textContent === '请在微信中打开'", "non-WeChat Owner identity state did not settle");
    const gate = await evaluate(cdp, "(() => ({title:document.querySelector('#identityTitle')?.textContent,message:document.querySelector('#identityMessage')?.textContent,actionHidden:document.querySelector('#authContinue')?.hidden,checkoutHidden:document.querySelector('#checkoutContent')?.hidden}))()");
    assert.deepEqual(gate, { title: "请在微信中打开", message: "请复制当前链接到微信中打开，登录后才能完成支付。", actionHidden: true, checkoutHidden: true }, "non-WeChat identity gate must explain where to log in without offering an unusable authorization action");
  };

  const visit = async ({ pagePath, kind, route, width, file, name, price, detail = false, media = false, unavailable = false }) => {
    await resize(width);
    await cdp.call("Page.navigate", { url: baseURL + pagePath });
    const rootMounted = "document.querySelector('[data-v3-public-commerce]')?.dataset.publicCommerceMounted === 'true'";
    try {
      await waitFor(cdp, rootMounted, `presentation Host did not mount ${pagePath}`);
    } catch (error) {
      const mounting = await evaluate(cdp, "(() => { const root=document.querySelector('[data-v3-public-commerce]'); return {root:!!root,tag:root?.tagName||'',route:root?.dataset.publicCommerceRoute||'',mounted:root?.dataset.publicCommerceMounted||'',hostScripts:Array.from(document.scripts).map(script=>String(script.src||'')).filter(src=>src.includes('/product-public-assets/')),body:document.body?.innerHTML.slice(0,320)||''}; })()");
      throw new Error(`${error.message}; state=${JSON.stringify(mounting)}`);
    }
    await waitFor(cdp, "Array.from(document.styleSheets).some(sheet => String(sheet.href || '').includes('/product-public-assets/'))", `public stylesheet did not load ${pagePath}`);
    if (!unavailable) {
      const contentSelector = detail ? "#detailContent:not([hidden])" : "#checkoutContent:not([hidden])";
      await waitFor(cdp, `document.querySelector(${JSON.stringify(contentSelector)})`, `authorized business content did not appear ${pagePath}`);
      if (media) {
        await waitFor(cdp, "(() => { const image=document.querySelector('#detailContent .detail-image'); return image && image.complete && image.naturalWidth > 0; })()", `Product-owned detail media did not load ${pagePath}`);
      }
    }
    const state = await evaluate(cdp, "(() => { const root=document.querySelector('[data-v3-public-commerce]'); const detail=document.querySelector('#detailContent'); const checkout=document.querySelector('#checkoutContent'); const image=detail?.querySelector('.detail-image'); const tag=document.querySelector('.service-period-tag'); return {kind:root?.dataset.productKind,route:root?.dataset.publicCommerceRoute,view:root?.dataset.publicCommerceView,primaryAction:root?.dataset.publicCommercePrimaryAction,overflow:document.documentElement.scrollWidth>innerWidth+1,detailScrollable:document.documentElement.scrollHeight>innerHeight+1,host:Array.from(document.scripts).some(script=>String(script.src||'').includes('/product-public-assets/')),identityVisible:Boolean(document.querySelector('#identityGate:not([hidden])')),unavailableButton:Boolean(document.querySelector('#servicePeriodPayButton:disabled')),unavailableTagDot:tag?getComputedStyle(tag,'::before').backgroundColor:'',detailVisible:Boolean(detail&&!detail.hidden),detailSummaryPanel:Boolean(detail?.querySelector('.panel,h1,.desc')),detailFirstChild:detail?.firstElementChild?.className||'',detailPrice:document.querySelector('#detailPrice')?.textContent||'',detailAction:detail?.querySelector('.buy')?.textContent||'',detailImageLoaded:Boolean(image && image.complete && image.naturalWidth > 0),checkoutVisible:Boolean(checkout&&!checkout.hidden),checkoutName:checkout?.querySelector('.product h1')?.textContent||'',checkoutPrice:checkout?.querySelector('#price')?.textContent||'',payable:checkout?.querySelector('#payableAmount')?.textContent||'',footer:checkout?.querySelector('#footerAmount')?.textContent||'',buyDisabled:Boolean(checkout?.querySelector('#buy')?.disabled),buyText:checkout?.querySelector('#buy')?.textContent||''}; })()");
    assert.equal(state.kind, kind, `${pagePath} product kind`);
    assert.equal(state.route, route, `${pagePath} route kind`);
    assert.equal(state.host, true, `${pagePath} Host resource`);
    assert.equal(state.overflow, false, `${pagePath} ${width}px layout overflow`);
    // The frozen page owns the unavailable fact through its disabled action;
    // do not infer state from user-visible text or depend on an incidental CSS
    // class that the frozen state renderer does not preserve after load.
    if (unavailable) {
      assert.equal(state.unavailableButton, true, `${pagePath} unavailable Owner state`);
      assert.equal(state.primaryAction, 'disabled', `${pagePath} unavailable primary action marker`);
      assert.equal(state.unavailableTagDot, 'rgb(168, 173, 181)', `${pagePath} unavailable status dot remains neutral`);
    } else if (detail) {
      assert.equal(state.identityVisible, false, `${pagePath} trusted session must reveal product detail`);
      assert.equal(state.detailVisible, true, `${pagePath} visible product detail`);
      assert.equal(state.detailSummaryPanel, false, `${pagePath} immersive detail omits product summary chrome`);
      assert.equal(state.detailFirstChild, 'detail-image', `${pagePath} immersive detail starts with page material`);
      assert.equal(state.detailPrice, price, `${pagePath} product detail price`);
      assert.notEqual(state.detailAction, "", `${pagePath} product purchase action`);
      if (media) {
        assert.equal(state.detailImageLoaded, true, `${pagePath} Product-owned detail media`);
        assert.equal(state.detailScrollable, true, `${pagePath} Product-owned long media creates a real detail scroll`);
        await evaluate(cdp, "window.scrollTo(0, document.documentElement.scrollHeight);");
        await settleLayout();
        const bottomSafe = await evaluate(cdp, "(() => { const image=document.querySelector('#detailContent .detail-image'); const footer=document.querySelector('.checkout-footer'); if (!image || !footer) return false; const bottom=image.getBoundingClientRect().bottom; return bottom > 0 && bottom <= footer.getBoundingClientRect().top; })()");
        assert.equal(bottomSafe, true, `${pagePath} detail media remains above the fixed purchase bar when scrolled`);
        await capture('public-standard-detail-375-bottom.png');
        await evaluate(cdp, "window.scrollTo(0, 0);");
      }
    } else {
      assert.equal(state.identityVisible, false, `${pagePath} trusted session must reveal checkout`);
      assert.equal(state.checkoutVisible, true, `${pagePath} visible checkout content`);
      assert.equal(state.checkoutName, name, `${pagePath} checkout product name`);
      assert.equal(state.checkoutPrice, price, `${pagePath} checkout price`);
      assert.equal(state.payable, `¥${price}`, `${pagePath} Owner payable amount`);
      assert.equal(state.footer, `¥${price}`, `${pagePath} Owner payment recovery footer amount`);
      assert.equal(state.buyDisabled, false, `${pagePath} ready purchase action`);
      assert.equal(state.buyText, "立即支付", `${pagePath} purchase action text`);
    }
    await capture(file);
  };

  await visitUnauthenticatedGate();
  await setUserAgent(browserUserAgent);
  const cookie = await cdp.call("Network.setCookie", { url: baseURL, name: trustedCookieName, value: trustedCookieValue, secure: true, httpOnly: true, sameSite: "Lax" });
  assert.equal(cookie.success, true, "trusted Payment session cookie");
  await visit({ pagePath: `/p/${encodeURIComponent(standardCode)}`, kind: "standard", route: "detail", width: 375, file: "public-standard-detail-375.png", name: "浏览器外推商品", price: "99.00", detail: true, media: true });
  await visit({ pagePath: `/pay/${encodeURIComponent(standardCode)}`, kind: "standard", route: "payment", width: 390, file: "public-standard-payment-390.png", name: "浏览器外推商品", price: "99.00" });
  // Service-period's existing Owner selects the checkout presentation when a
  // product has no detail-media records. This fixture intentionally has no
  // synthetic media, so verify its real route output instead of treating the
  // URL alone as proof of a detail section.
  await visit({ pagePath: `/s/${encodeURIComponent(serviceCode)}`, kind: "service_period", route: "payment", width: 430, file: "public-service-available-430.png", name: "浏览器周期外推商品", price: "128.00" });
  await visit({ pagePath: `/s/${encodeURIComponent(serviceCode)}/pay`, kind: "service_period", route: "payment", width: 390, file: "public-service-available-payment-390.png", name: "浏览器周期外推商品", price: "128.00" });
  await visit({ pagePath: `/s/${encodeURIComponent(unavailableServiceCode)}`, kind: "service_period", route: "service-period-state", width: 375, file: "public-service-unavailable-detail-375.png", unavailable: true });
  await visit({ pagePath: `/s/${encodeURIComponent(unavailableServiceCode)}/pay`, kind: "service_period", route: "service-period-state", width: 430, file: "public-service-unavailable-payment-430.png", unavailable: true });

  const successfulAssets = [...publicAssets.entries()].filter(([, status]) => status === 200).map(([resource]) => resource);
  if (successfulAssets.length < 3 || !successfulAssets.some(resource => resource.endsWith(".css")) || !successfulAssets.some(resource => resource.endsWith(".js")) || !successfulAssets.some(resource => resource.includes("/chunks/"))) {
    throw new Error(`anonymous public asset closure did not load CSS, Host, and module chunk: ${JSON.stringify(Object.fromEntries(publicAssets))}`);
  }
  if (exceptions.length) throw new Error(`public commerce browser exceptions=${JSON.stringify(exceptions)}`);
  console.log(`public_commerce_chromium: PASS screenshots=${screenshots} assets=${successfulAssets.length}`);
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  await fs.rm(profile, { recursive: true, force: true }).catch(() => {});
}
