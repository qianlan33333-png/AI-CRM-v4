import assert from "node:assert/strict";
import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = String(process.env.AICRM_PUBLIC_CHECKOUT_CHROMIUM_BASE_URL || "").replace(/\/$/, "");
const sessionCookie = String(process.env.AICRM_PUBLIC_CHECKOUT_CHROMIUM_SESSION || "");
if (!/^http:\/\/127\.0\.0\.1:\d+$/.test(baseURL) || !/^aicrm_payment_session=.{20,}$/.test(sessionCookie)) throw new Error("public checkout Chromium journey environment is incomplete");

const promotionA = `dpc_${"A".repeat(43)}`;
const promotionB = `dpc_${"B".repeat(43)}`;
const sleep = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));

function browser() {
  for (const item of [process.env.AICRM_CHROMIUM_BINARY, "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome", "google-chrome", "chromium"].filter(Boolean)) {
    if ((item.includes("/") ? spawnSync(item, ["--version"], { stdio: "ignore" }) : spawnSync("which", [item], { stdio: "ignore" })).status === 0) return item;
  }
  throw new Error("Chromium is unavailable");
}

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.id = 0;
    this.pending = new Map();
    this.exceptions = [];
    socket.addEventListener("message", event => {
      const message = JSON.parse(String(event.data));
      if (message.method === "Runtime.exceptionThrown") {
        this.exceptions.push(message.params?.exceptionDetails?.exception?.description || message.params?.exceptionDetails?.text || "runtime exception");
        return;
      }
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      message.error ? pending.reject(new Error(`CDP ${message.error.code}`)) : pending.resolve(message.result || {});
    });
  }

  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.id;
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new Error(`CDP ${method} timed out`));
      }, 8000);
      this.pending.set(id, { resolve: value => { clearTimeout(timer); resolve(value); }, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
}

async function endpoint(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await sleep(50);
  }
  throw new Error("Chromium DevTools did not start");
}

async function evaluate(cdp, expression) {
  const response = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (response.exceptionDetails) throw new Error(`page evaluation failed: ${response.exceptionDetails.exception?.description || response.exceptionDetails.text || "unknown"}`);
  return response.result?.value;
}

async function wait(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await sleep(50);
  }
  throw new Error(`${message}; exceptions=${JSON.stringify(cdp.exceptions)}`);
}

async function visit(cdp, pagePath) {
  await cdp.call("Page.navigate", { url: `${baseURL}${pagePath}` });
  await wait(cdp, "document.getElementById('identityGate')?.hidden === true && document.getElementById('buy')", `checkout page did not load ${pagePath}`);
}

async function clickWithCoupon(cdp, couponID) {
  await evaluate(cdp, `(()=>{const select=document.getElementById('coupon');const option=document.createElement('option');option.value=${JSON.stringify(String(couponID))};option.textContent='fixture coupon';select.appendChild(option);select.value=option.value;const mobile=document.getElementById('mobile');if(mobile)mobile.value='13800138000';document.getElementById('buy').click();return true})()`);
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-public-checkout-chromium-"));
let child;
let cdp;
try {
  child = spawn(browser(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "about:blank"], { stdio: "ignore" });
  const page = await (await fetch(`${await endpoint(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(page.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("CDP connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  await cdp.call("Network.setUserAgentOverride", { userAgent: "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15 MicroMessenger/8.0.0" });
  const splitCookie = sessionCookie.indexOf("=");
  await cdp.call("Network.setCookie", { url: baseURL, name: sessionCookie.slice(0, splitCookie), value: sessionCookie.slice(splitCookie + 1), secure: false, httpOnly: true, sameSite: "Strict" });

  await visit(cdp, `/pay/course-7?promotion_context=${promotionA}`);
  assert.equal(await evaluate(cdp, "document.querySelector('#checkoutContent > .product')?.querySelector('img') === null"), true, "checkout product is text only");
  assert.equal(await evaluate(cdp, "document.getElementById('couponPanel')?.hidden === false"), true, "claimed coupon section is visible");
  await evaluate(cdp, "document.getElementById('buy').click();true");
  await wait(cdp, "document.getElementById('mobileError')?.textContent==='请填写手机号'", "missing phone feedback was not shown");
  assert.equal(await evaluate(cdp, "sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard')"), null, "invalid form must not create a checkout checkpoint");
  await clickWithCoupon(cdp, 11);
  await wait(cdp, "document.getElementById('status')?.textContent==='请求失败'", "lost response was not rendered");
  const firstCheckpoint = await evaluate(cdp, "JSON.parse(sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard') || 'null')");
  assert.equal(firstCheckpoint?.payload?.promotion_context, promotionA, "initial promotion checkpoint preserves the rendered context");
  assert.equal(typeof firstCheckpoint?.key, "string", "initial promotion checkpoint has an idempotency key");

  await visit(cdp, `/pay/course-7?promotion_context=${promotionB}`);
  await evaluate(cdp, "document.getElementById('buy').click();true");
  await wait(cdp, "document.getElementById('buy')?.textContent==='已购买'", "second promotion page did not recover the original order");
  const secondCheckpoint = await evaluate(cdp, "JSON.parse(sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard') || 'null')");
  assert.equal(secondCheckpoint?.key, firstCheckpoint.key, "second promotion page keeps the original idempotency key");
  assert.equal(secondCheckpoint?.payload?.promotion_context, promotionA, "second promotion page keeps the original attribution context");

  await visit(cdp, "/pay/course-7");
  await wait(cdp, "document.getElementById('buy')?.textContent==='已购买'", "ordinary page did not retain the terminal promotion checkpoint");
  const ordinaryCheckpoint = await evaluate(cdp, "JSON.parse(sessionStorage.getItem('aicrm.checkout.tab.v2:7:standard') || 'null')");
  assert.equal(ordinaryCheckpoint?.key, firstCheckpoint.key, "ordinary page keeps the original idempotency key");
  assert.equal(ordinaryCheckpoint?.payload?.promotion_context, promotionA, "ordinary page cannot replace paid attribution");
  assert.equal(await evaluate(cdp, "document.getElementById('couponPanel')?.hidden"), true, "paid result hides coupon selection");
  if (cdp.exceptions.length) throw new Error(`page exceptions=${JSON.stringify(cdp.exceptions)}`);
  console.log("public_checkout_chromium: PASS");
} finally {
  if (cdp) cdp.socket.close();
  if (child && child.exitCode === null) {
    child.kill("SIGTERM");
    await Promise.race([new Promise(resolve => child.once("exit", resolve)), sleep(3000)]);
    if (child.exitCode === null) child.kill("SIGKILL");
  }
  await fs.rm(profile, { recursive: true, force: true }).catch(() => {});
}
