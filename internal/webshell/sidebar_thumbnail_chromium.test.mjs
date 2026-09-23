import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_URL;
const username = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_USERNAME;
const password = process.env.AICRM_SIDEBAR_THUMBNAIL_TEST_PASSWORD;
const jssdkFixturePath = process.env.AICRM_SIDEBAR_JSSDK_FIXTURE;
const weComJSSDKURL = "https://res.wx.qq.com/wwopen/js/jsapi/jweixin-1.0.0.js";
const screenshotDirectory = process.env.AICRM_SIDEBAR_SCREENSHOT_DIR || "";
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !jssdkFixturePath) throw new Error("sidebar Chromium journey requires HTTPS URL, credentials, and the official WeCom JSSDK fixture");
const jssdkFixture = await fs.readFile(jssdkFixturePath);
if (!jssdkFixture.includes(Buffer.from("agentConfig"))) throw new Error("sidebar Chromium journey JSSDK fixture lacks agentConfig");

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
function browserBinary() {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/")) { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; }
      else if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
    } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
}
class CDP {
  constructor(socket) {
    this.socket = socket; this.nextID = 0; this.pending = new Map(); this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id); this.pending.delete(message.id);
        message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  on(method, listener) { const listeners = this.events.get(method) || []; listeners.push(listener); this.events.set(method, listeners); }
  close() { for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed")); this.pending.clear(); this.socket.close(); }
}
function startupDiagnostic(browser, stderr) {
  const detail = String(stderr || "")
    .replace(/[\r\n]+/g, " ")
    .replace(/[^a-zA-Z0-9 .,:_+\-\/()]/g, "_")
    .slice(0, 600);
  const exit = browser?.exitCode;
  const signal = browser?.signalCode;
  return JSON.stringify({ exit_code: exit ?? null, signal: signal ?? null, stderr: detail || "none" });
}

async function port(profile, browser, stderr) {
  for (let attempt = 0; attempt < 600; attempt += 1) {
    if (browser?.exitCode !== null || browser?.signalCode !== null) {
      throw new Error(`Chromium exited before DevTools was ready: ${startupDiagnostic(browser, stderr())}`);
    }
    try { const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`; } catch (_) {}
    await delay(50);
  }
  throw new Error(`Chromium DevTools startup timed out after 30s: ${startupDiagnostic(browser, stderr())}`);
}
function evaluationFailure(stage, details) {
  const kind = String(details?.exception?.className || details?.text || "runtime_exception")
    .replace(/[^a-zA-Z0-9_.-]/g, "_")
    .slice(0, 96);
  return new Error(`${stage} page evaluation failed: ${kind || "runtime_exception"}`);
}
async function evaluate(cdp, expression, stage = "page") {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw evaluationFailure(stage, result.exceptionDetails);
  return result.result?.value;
}
async function waitFor(cdp, expression, message, stage = "wait") {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression, stage)) return;
    await delay(50);
  }
  throw new Error(message);
}
async function captureScreenshot(cdp, name) {
  if (!screenshotDirectory) return;
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  const shot = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  await fs.writeFile(path.join(screenshotDirectory, `${name}.png`), Buffer.from(shot.data, "base64"), { mode: 0o600 });
}
async function settleScrollAtTop(cdp) {
  // Wheel scrolling and resize anchoring finish on compositor frames. A single
  // scrollTo plus 50ms could measure at scrollY=30 and mistake sticky overlap
  // for a layout defect. Require a stable top before measuring the same bounds.
  const settled = await evaluate(cdp, `new Promise(resolve => {
    let frames = 0, stable = 0;
    const tick = () => {
      if (window.scrollY === 0) stable++; else stable = 0;
      window.scrollTo({ top: 0, left: 0, behavior: "instant" });
      if (stable >= 6) return resolve(true);
      if (++frames >= 120) return resolve(false);
      requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  })`);
  if (!settled) throw new Error("sidebar viewport did not settle at scroll top");
}
async function browserExit(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) { child.kill("SIGKILL"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(1000)]); }
}
async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; }
    catch (error) { if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false; await delay(100); }
  }
  return false;
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-sidebar-thumbnail-chromium-"));
let browser; let cdp; let failed = false; let browserStderr = "";
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
  browser.stderr?.on("data", (chunk) => { if (browserStderr.length < 4096) browserStderr += String(chunk).slice(0, 4096 - browserStderr.length); });
  const created = await (await fetch(`${await port(profile, browser, () => browserStderr)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  await cdp.call("Emulation.setUserAgentOverride", { userAgent: "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) wxwork/4.1.36 MicroMessenger/7.0.1", platform: "MacIntel" });

  let jssdkResourceMode = "serve";
  const materialFaults = new Map();
  const productFaults = [];
  let phoneBindingFault = null;
  await cdp.call("Page.addScriptToEvaluateOnNewDocument", { source: `(() => {
    const calls = [];
    const agentAPIs = [];
    let agentCalls = 0;
    Object.defineProperty(globalThis, "__sidebarNativeBridgeCalls", { value: calls, configurable: false });
    Object.defineProperty(globalThis, "__sidebarAgentAPIs", { value: agentAPIs, configurable: false });
    Object.defineProperty(globalThis, "__sidebarClipboardWrites", { value: [], configurable: false });
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText(value) { globalThis.__sidebarClipboardWrites.push(String(value)); return Promise.resolve(); } } });
    Object.defineProperty(globalThis, "WeixinJSBridge", { configurable: false, value: {
      invoke(method, payload, callback) {
        calls.push(method);
        const scenario = new URL(globalThis.location.href).searchParams.get("sidebar_case") || "success";
        if (method === "preVerifyJSAPI" && scenario === "regular_error") return setTimeout(() => callback({ err_msg: "preVerifyJSAPI:fail" }), 0);
        if (method === "agentConfig") {
          agentCalls += 1;
          agentAPIs.splice(0, agentAPIs.length, ...(Array.isArray(payload?.jsApiList) ? payload.jsApiList : []));
          if (scenario === "agent_error" || (scenario === "agent_retry" && agentCalls === 1)) return setTimeout(() => callback({ err_msg: "agentConfig:fail" }), 0);
        }
        if (method !== "preVerifyJSAPI" && method !== "agentConfig" && !agentAPIs.includes(method)) return setTimeout(() => callback({ err_msg: method + ":no permission" }), 0);
        if (method === "getCurExternalContact") {
          if (scenario === "contact_error") return setTimeout(() => callback({ err_msg: "getCurExternalContact:fail" }), 0);
          return setTimeout(() => callback({ err_msg: "getCurExternalContact:ok", external_userid: "sidebar-thumbnail-external" }), 0);
        }
        return setTimeout(() => callback({ err_msg: method + ":ok" }), 0);
      },
      on() {}, call() {},
    }});
  })();` });
  await cdp.call("Fetch.enable", { patterns: [
    { urlPattern: weComJSSDKURL },
    { urlPattern: "*://*/api/sidebar/v2/materials*" },
    { urlPattern: "*://*/api/sidebar/v2/products*" },
    { urlPattern: "*://*/api/sidebar/v2/phone-binding*" },
  ] });
  cdp.on("Fetch.requestPaused", (params) => {
    void (async () => {
      const requestURL = String(params.request?.url || "");
      if (requestURL === weComJSSDKURL) {
        if (jssdkResourceMode === "missing") {
          await cdp.call("Fetch.failRequest", { requestId: params.requestId, errorReason: "BlockedByClient" });
          return;
        }
        await cdp.call("Fetch.fulfillRequest", {
          requestId: params.requestId,
          responseCode: 200,
          responseHeaders: [{ name: "Content-Type", value: "application/javascript; charset=utf-8" }],
          body: jssdkFixture.toString("base64"),
        });
        return;
      }
      const materialURL = new URL(requestURL);
      if (materialURL.pathname === "/api/sidebar/v2/phone-binding" && phoneBindingFault) {
        const fault = phoneBindingFault;
        phoneBindingFault = null;
        await cdp.call("Fetch.fulfillRequest", {
          requestId: params.requestId,
          responseCode: fault.status,
          responseHeaders: [{ name: "Content-Type", value: "application/json; charset=utf-8" }],
          body: Buffer.from(JSON.stringify({ error: "forbidden" })).toString("base64"),
        });
        return;
      }
      if (materialURL.pathname === "/api/sidebar/v2/products" && productFaults.length) {
        const fault = productFaults.shift();
        if (fault.delayMs) await delay(fault.delayMs);
        await cdp.call("Fetch.fulfillRequest", {
          requestId: params.requestId,
          responseCode: 200,
          responseHeaders: [{ name: "Content-Type", value: "application/json; charset=utf-8" }],
          body: Buffer.from(JSON.stringify(fault.body)).toString("base64"),
        });
        return;
      }
      const materialFault = materialURL.pathname === "/api/sidebar/v2/materials" ? materialFaults.get(materialURL.searchParams.get("q") || "") : null;
      if (materialFault) {
        if (materialFault.delayMs) await delay(materialFault.delayMs);
        if (materialFault.status) {
          const status = materialFault.status;
          await cdp.call("Fetch.fulfillRequest", {
            requestId: params.requestId,
            responseCode: status,
            responseHeaders: [{ name: "Content-Type", value: "application/json; charset=utf-8" }],
            body: Buffer.from(JSON.stringify({ error: status === 403 ? "forbidden" : "dependency_unavailable" })).toString("base64"),
          });
          return;
        }
      }
      await cdp.call("Fetch.continueRequest", { requestId: params.requestId });
    })().catch(() => undefined);
  });

  const resources = new Map(); const successfulResources = new Set(); const requestURLs = []; const requestRecords = []; const exceptions = []; const loginResponses = new Map(); let sidebarCSP = "";
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  cdp.on("Network.requestWillBeSent", (params) => {
    requestURLs.push(String(params.request?.url || ""));
    requestRecords.push({ url: String(params.request?.url || ""), postData: String(params.request?.postData || "") });
  });
  cdp.on("Network.responseReceived", (params) => {
    try {
      const pathname = new URL(String(params.response?.url || "")).pathname;
      const status = Number(params.response?.status) || 0;
      if (pathname === "/sidebar/bind-mobile") sidebarCSP = String(params.response?.headers?.["content-security-policy"] || params.response?.headers?.["Content-Security-Policy"] || "");
      if (pathname === "/login" || pathname === "/admin" || pathname === "/admin/customers.html") loginResponses.set(pathname, status);
      if (pathname === "/api/sidebar/v2/bootstrap" || pathname === "/api/sidebar/v2/materials" || /^\/api\/sidebar\/v2\/materials\/\d+\/variants\/thumb_320$/.test(pathname) || /^\/sidebar-assets\/(sidebarHost|sidebarStandardOverlay|sidebarImageResourceLoader)-[A-Za-z0-9_-]+\.js$/.test(pathname)) {
        resources.set(pathname, status);
        if (status === 200) successfulResources.add(pathname);
      }
    } catch (_) {}
  });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  try {
    await waitFor(cdp, "location.pathname === '/admin' && !document.querySelector('form[action=\"/login\"]')", "login did not reach the authenticated admin Host");
  } catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), login: loginResponses.get("/login") || 0, admin: loginResponses.get("/admin") || 0, exceptions });
    throw new Error(`login did not establish the Access session: ${diagnostic}`);
  }
  const cookies = await cdp.call("Network.getAllCookies");
  const session = (cookies.cookies || []).find((cookie) => cookie.name === "aicrm_admin_session" && cookie.value);
  if (!session) throw new Error("Access session cookie was not issued");
  await cdp.call("Network.setCookie", { name: "aicrm_sidebar_session", value: session.value, url: baseURL, path: "/", secure: true, httpOnly: true, sameSite: "Lax" });

  const sidebarDocumentReadyExpression = (scenario) => `(() => { const body = document.body; return location.pathname === "/sidebar/bind-mobile" && new URL(location.href).searchParams.get("sidebar_case") === ${JSON.stringify(scenario)} && document.readyState !== "loading" && Boolean(body); })()`;
  async function openSidebar(scenario, resourceMode = "serve") {
    jssdkResourceMode = resourceMode;
    const requestStart = requestURLs.length;
    await cdp.call("Page.navigate", { url: `${baseURL}/sidebar/bind-mobile?sidebar_case=${encodeURIComponent(scenario)}` });
    await waitFor(cdp, sidebarDocumentReadyExpression(scenario), `${scenario} navigation did not reach its sidebar document`, `${scenario}_navigation`);
    return requestStart;
  }
  const bootstrapCountSince = (start) => requestURLs.slice(start).filter((url) => new URL(url).pathname === "/api/sidebar/v2/bootstrap").length;
  const jssdkCountSince = (start) => requestURLs.slice(start).filter((url) => new URL(url).pathname === "/api/sidebar/jssdk-config").length;
  const bridgeCalls = () => evaluate(cdp, "JSON.stringify(globalThis.__sidebarNativeBridgeCalls || [])").then((value) => JSON.parse(value || "[]"));

  for (const [scenario, message, resourceMode] of [
    ["sdk_missing", "企微 SDK 未载入", "missing"],
    ["regular_error", "企微 config 失败：config:fail", "serve"],
    ["agent_error", "agentConfig:fail", "serve"],
    ["contact_error", "getCurExternalContact:fail", "serve"],
  ]) {
    const start = await openSidebar(scenario, resourceMode);
    await waitFor(cdp, `(${sidebarDocumentReadyExpression(scenario)}) && Boolean(document.body?.textContent?.includes(${JSON.stringify(message)}) && document.querySelector('[data-retry-boot]'))`, `${scenario} did not render the standard overlay recovery action`, `${scenario}_recovery`);
    if (bootstrapCountSince(start) !== 0) throw new Error(`${scenario} requested sidebar bootstrap before a trusted contact`);
  }

  const retryStart = await openSidebar("agent_retry");
  await waitFor(cdp, "Boolean(document.querySelector('[data-retry-boot]'))", "agent failure did not render standard overlay retry action");
  await evaluate(cdp, "document.querySelector('[data-retry-boot]').click(); true");
  await waitFor(cdp, "Boolean(document.querySelector('#tabs button[data-tab=\"materials\"]:not([disabled])'))", "agent retry did not establish the trusted sidebar context");
  const retryCalls = await bridgeCalls();
  if (retryCalls.filter((call) => call === "preVerifyJSAPI").length !== 1 || retryCalls.filter((call) => call === "agentConfig").length !== 2 || jssdkCountSince(retryStart) !== 2 || bootstrapCountSince(retryStart) !== 1) throw new Error("agent retry did not reuse regular config or re-read the agent signature exactly once");

  const successStart = await openSidebar("success");
  try {
    await waitFor(cdp, `(${sidebarDocumentReadyExpression("success")}) && Boolean(document.querySelector('#tabs button[data-tab=\"materials\"]:not([disabled])'))`, "sidebar standard overlay did not complete the official JSSDK handshake", "success_handshake");
  } catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), document: await evaluate(cdp, "document.body ? 'ready' : 'missing'"), tabs: await evaluate(cdp, "Boolean(document.querySelector('#tabs'))"), host: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarHost-/.test(path) && status === 200), overlay: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarStandardOverlay-/.test(path) && status === 200), bootstrap: bootstrapCountSince(successStart), jssdk: jssdkCountSince(successStart), cspBlob: sidebarCSP.includes("img-src 'self' data: blob:"), exceptions });
    throw new Error(`sidebar standard overlay did not complete official JSSDK handshake: ${diagnostic}`);
  }
  await waitFor(cdp, '/^用户编号 [1-9][0-9]{6}$/.test(document.querySelector("#customer-oneid")?.textContent || "")', "sidebar public number was dropped by the legacy renderer");
  const successCalls = await bridgeCalls();
  const successAgentAPIs = JSON.parse(await evaluate(cdp, "JSON.stringify(globalThis.__sidebarAgentAPIs || [])"));
  if (successCalls.join("|") !== "preVerifyJSAPI|agentConfig|getCurExternalContact" || JSON.stringify(successAgentAPIs) !== JSON.stringify(["getCurExternalContact", "sendChatMessage"]) || bootstrapCountSince(successStart) !== 1) throw new Error(`official JSSDK success order/API mismatch: ${JSON.stringify({ calls: successCalls, agentAPIs: successAgentAPIs })}`);
  for (const width of [320, 360, 375, 420, 430, 768]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
    const geometry = JSON.parse(await evaluate(cdp, 'JSON.stringify((()=>{const t=[...document.querySelectorAll("#tabs [data-tab]")],r=t.map(n=>n.getBoundingClientRect());return {v:innerWidth,c:document.documentElement.clientWidth,s:document.documentElement.scrollWidth,n:t.length,rows:new Set(r.map(x=>Math.round(x.top))).size,cols:new Set(r.slice(0,3).map(x=>Math.round(x.left))).size,in:r.every(x=>x.left>=0&&x.right<=innerWidth+.5)}})())'));
    if (geometry.v!==width || geometry.s>geometry.c || geometry.n!==6 || geometry.rows!==2 || geometry.cols!==3 || !geometry.in) throw new Error("geometry "+width+" "+JSON.stringify(geometry));
  }
  await cdp.call("Emulation.setDeviceMetricsOverride", { width:360, height:900, deviceScaleFactor:1, mobile:false });
  await captureScreenshot(cdp, "profile-360");
  await cdp.call("Emulation.setDeviceMetricsOverride", { width:375, height:900, deviceScaleFactor:1, mobile:false });
  await captureScreenshot(cdp, "profile-375");
  await cdp.call("Emulation.setDeviceMetricsOverride", { width:430, height:900, deviceScaleFactor:1, mobile:false });
  const profileStart=requestRecords.length;
  await evaluate(cdp, '(()=>{const f=document.querySelector("[data-profile-field=source]");f.value="Chromium活动报名";f.dispatchEvent(new Event("input",{bubbles:true}));return true})()');
  for(let n=0;n<140&&requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile").length<1;n++) await delay(50);
  let writes=requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile"), body=JSON.parse(writes[0]?.postData||"{}");
  if(writes.length!==1||body.expected_profile_version!==0||body.source!=="Chromium活动报名") throw new Error("profile CAS 0 "+JSON.stringify(writes));
  await evaluate(cdp, '(()=>{const f=document.querySelector("[data-profile-field=industry]");f.value="教育";f.dispatchEvent(new Event("input",{bubbles:true}));return true})()');
  for(let n=0;n<140&&requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile").length<2;n++) await delay(50);
  writes=requestRecords.slice(profileStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/profile");body=JSON.parse(writes[1]?.postData||"{}");
  if(writes.length!==2||body.expected_profile_version!==1||body.industry!=="教育") throw new Error("profile CAS 1 "+JSON.stringify(writes));
  const timelineStart=requestRecords.length;
  await evaluate(cdp, 'document.querySelector("[data-profile-view=timeline]").click();true');
  await waitFor(cdp, 'document.querySelectorAll(".timeline-event").length===20', "business timeline first page");
  while(await evaluate(cdp, 'Boolean(document.querySelector("[data-load-more-timeline]"))')) {
    const before=await evaluate(cdp, 'document.querySelectorAll(".timeline-event").length');
    await evaluate(cdp, 'document.querySelector("[data-load-more-timeline]").click();true');
    await waitFor(cdp, 'document.querySelectorAll(".timeline-event").length>'+before, "business timeline cursor append");
  }
  const timeline=JSON.parse(await evaluate(cdp, 'JSON.stringify([...document.querySelectorAll(".timeline-event h3")].map(x=>x.textContent))'));
  for(const title of ["提交问卷：Sidebar bootstrap count", "创建订单（已退款）：浏览器外推商品", "历史渠道进入：Chromium渠道活动", "打开雷达内容：Chromium雷达介绍"]) {
    if(!timeline.includes(title)) throw new Error("missing business timeline title "+title);
  }
  const timelineReads=requestRecords.slice(timelineStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/timeline");
  if(timeline.length!==105||timeline.some(t=>t.includes("资料已同步"))||timelineReads.length!==6||timelineReads.slice(1).some(r=>!new URL(r.url).searchParams.get("cursor"))) throw new Error("business timeline pagination or sync noise "+JSON.stringify({count:timeline.length,reads:timelineReads.length}));
  const surveyStart=requestRecords.length;
  await evaluate(cdp, 'document.querySelector("#tabs [data-tab=questionnaires]").click();true');
  await waitFor(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length===20', "survey page 1");
  while(await evaluate(cdp, 'Boolean(document.querySelector("[data-load-more-questionnaires]"))')){
    const before=await evaluate(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length');
    await evaluate(cdp, 'document.querySelector("[data-load-more-questionnaires]").click();true');
    await waitFor(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length>'+before, "survey cursor append");
  }
  const surveyCount=await evaluate(cdp, 'document.querySelectorAll("[data-questionnaire-card]").length');
  const surveyReads=requestRecords.slice(surveyStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/questionnaires");
  if(surveyCount!==102||surveyReads.length!==6||surveyReads.slice(1).some(r=>!new URL(r.url).searchParams.get("cursor"))||surveyReads.some(r=>new URL(r.url).searchParams.has("offset"))) throw new Error("survey 102 cursor "+surveyCount+" "+JSON.stringify(surveyReads));
  await evaluate(cdp, 'document.querySelector("#tabs [data-tab=coupons]").click();true');
  await waitFor(cdp, 'document.body.textContent.includes("Chromium无链接券")', "coupon fixtures");
  const coupons=JSON.parse(await evaluate(cdp, 'JSON.stringify({buttons:[...document.querySelectorAll("[data-copy-url]")].map(b=>({disabled:b.disabled,url:b.dataset.copyUrl,text:b.closest(".card")?.textContent||""})),text:document.body.textContent})'));
  for(const x of ["可前往领取页确认","未到领取时间","领取已截止","已领完","已达到个人领取上限"]) if(!coupons.text.includes(x)) throw new Error("coupon state "+x);
  const active=coupons.buttons.find(x=>x.text.includes("Chromium可领取券"));
  if(!active||active.disabled||!active.url.endsWith("/c/chromium-active")||coupons.buttons.length!==6||coupons.buttons.some(x=>x.disabled!==!x.url)) throw new Error("coupon buttons "+JSON.stringify(coupons));
  await evaluate(cdp, `document.querySelector('[data-copy-url$="/c/chromium-active"]').click();true`);
  await waitFor(cdp, 'globalThis.__sidebarClipboardWrites.length===1', "coupon clipboard");
  if(await evaluate(cdp, 'globalThis.__sidebarClipboardWrites[0]')!==active.url) throw new Error("coupon URL");
  await evaluate(cdp, 'document.querySelector("#tabs [data-tab=orders]").click();true');
  await waitFor(cdp, 'document.body.textContent.includes("SIDEBAR-ORDER-001")', "order fixture");
  const regular=await evaluate(cdp, 'document.getElementById("content").textContent');
  if(!regular.includes("¥99.00")||!regular.includes("已退款")||regular.includes("支付时间")) throw new Error("order facts "+regular);
  await captureScreenshot(cdp, "orders-430");
  await evaluate(cdp, 'document.querySelector("[data-order-type=periodic]").click();true');
  await waitFor(cdp, 'document.body.textContent.includes("31日真实服务周期")', "periodic fixture");
  const periodic=await evaluate(cdp, 'document.getElementById("content").textContent');
  if(!periodic.includes("浏览器周期外推商品")||!periodic.includes("生效时间")||!periodic.includes("到期时间")) throw new Error("periodic facts "+periodic);
  const materialStart = requestRecords.length;
  await evaluate(cdp, "document.querySelector('#tabs button[data-tab=\"materials\"]')?.click(); true");
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===5', "material first page did not render five standard cards");
  await evaluate(cdp, '(()=>{const old=document.querySelector(".material-page-sentinel");if(!old)throw new Error("initial material pager missing");old.dataset.preSearchPager="true";const input=document.querySelector("[data-material-search-input]");input.value="Chromium";document.querySelector("[data-material-search-form]").requestSubmit();return true})()');
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===5&&Boolean(document.querySelector(".material-page-sentinel:not([data-pre-search-pager])"))', "filtered material first page did not replace the standard pager");
  await cdp.call("Input.dispatchMouseEvent", { type: "mouseWheel", x: 215, y: 820, deltaX: 0, deltaY: 700 });
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===7', "standard image loader did not append material offset 5");
  const materialReads=requestRecords.slice(materialStart).filter(r=>new URL(r.url).pathname==="/api/sidebar/v2/materials").map(r=>new URL(r.url));
  const filteredReads=materialReads.filter(url=>url.searchParams.get("q")==="Chromium");
  if(filteredReads.length!==2||filteredReads[0].searchParams.get("offset")!=="0"||filteredReads[1].searchParams.get("offset")!=="5"||filteredReads.some(url=>url.searchParams.get("limit")!=="5"||url.searchParams.has("type"))) throw new Error("material standard pager query "+JSON.stringify(materialReads.map(url=>url.pathname+url.search)));
  const ready = "(() => { const images=[...document.querySelectorAll('img[data-material-preview=\"ready\"]')]; return images.length===7 && images.every(image => image.src.startsWith('blob:') && image.complete && image.naturalWidth === 1 && image.naturalHeight === 1); })()";
  try { await waitFor(cdp, ready, "sidebar thumbnails did not load through blob URLs for both standard pages"); }
  catch (_) {
    const diagnostic = JSON.stringify({ path: await evaluate(cdp, "location.pathname"), host: [...resources.entries()].some(([path, status]) => /^\/sidebar-assets\/sidebarHost-/.test(path) && status === 200), bootstrap: resources.get("/api/sidebar/v2/bootstrap") || 0, materials: resources.get("/api/sidebar/v2/materials") || 0, thumbnail: [...resources.entries()].some(([path, status]) => /variants\/thumb_320$/.test(path) && status === 200), cspBlob: sidebarCSP.includes("img-src 'self' data: blob:"), exceptions });
    throw new Error(`sidebar thumbnail did not render: ${diagnostic}`);
  }
  await settleScrollAtTop(cdp);
  await captureScreenshot(cdp, "materials-430");
  await cdp.call("Emulation.setDeviceMetricsOverride", { width:420, height:900, deviceScaleFactor:1, mobile:false });
  await settleScrollAtTop(cdp);
  const materialGeometry = JSON.parse(await evaluate(cdp, 'JSON.stringify((()=>{const submit=document.querySelector("[data-material-search-form] button[type=submit]");const r=submit?.getBoundingClientRect();const top=document.querySelector(".top")?.getBoundingClientRect();const segment=document.querySelector(".material-seg")?.getBoundingClientRect();return {scrollY:window.scrollY,scroll:document.documentElement.scrollWidth,client:document.documentElement.clientWidth,visible:Boolean(r&&r.left>=0&&r.right<=innerWidth&&r.top>=0&&r.bottom<=innerHeight),height:Math.round(r?.height||0),topBottom:Math.round(top?.bottom||0),segmentTop:Math.round(segment?.top||0)}})())'));
  if (materialGeometry.scroll > materialGeometry.client || !materialGeometry.visible || materialGeometry.height < 36 || materialGeometry.segmentTop < materialGeometry.topBottom) throw new Error("material narrow geometry " + JSON.stringify(materialGeometry));
  await captureScreenshot(cdp, "materials-420");
  for (const width of [320, 360, 390, 430]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height:900, deviceScaleFactor:1, mobile:false });
    await settleScrollAtTop(cdp);
    const geometry = JSON.parse(await evaluate(cdp, `JSON.stringify((()=>{
      const card=document.querySelector('[data-material-card]');
      const thumb=card.querySelector('.thumb').getBoundingClientRect();
      const button=card.querySelector('.material-send').getBoundingClientRect();
      const main=card.querySelector('.material-main').getBoundingClientRect();
      const profile=document.querySelector('.profile-card').getBoundingClientRect();
      return {overflow:document.documentElement.scrollWidth>innerWidth, sameRow:button.top<thumb.bottom&&button.bottom>thumb.top, right:button.left>=main.right, title:card.querySelector('.material-title')?.textContent, profileHeight:profile.height};
    })())`));
    if (geometry.overflow || !geometry.sameRow || !geometry.right || !geometry.title || geometry.profileHeight > 135) throw new Error("compact sidebar " + width + " " + JSON.stringify(geometry));
    await captureScreenshot(cdp, "materials-compact-" + width);
  }


  await evaluate(cdp, `document.querySelector('[data-material-type="radar"]')?.click(); true`);
  try { await waitFor(cdp, `Boolean(document.querySelector('.material [data-copy-url]'))`, "radar material did not render"); } catch (error) { throw new Error(String(error) + " " + await evaluate(cdp, "document.querySelector('#content').textContent")); }
  for (const width of [320, 390, 430]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height:900, deviceScaleFactor:1, mobile:false });
    const radarGeometry = JSON.parse(await evaluate(cdp, `JSON.stringify((()=>{
      const button=document.querySelector('.material [data-copy-url]').getBoundingClientRect();
      const main=document.querySelector('.material .material-main').getBoundingClientRect();
      const thumb=document.querySelector('.material .thumb').getBoundingClientRect();
      return {overflow:document.documentElement.scrollWidth>innerWidth, right:button.left>=main.right, sameRow:button.top<thumb.bottom&&button.bottom>thumb.top};
    })())`));
    if (radarGeometry.overflow || !radarGeometry.right || !radarGeometry.sameRow) throw new Error("radar compact " + width + " " + JSON.stringify(radarGeometry));
    await captureScreenshot(cdp, "radar-compact-" + width);
  }
  await evaluate(cdp, `document.querySelector('[data-material-type="image"]')?.click(); true`);
  await waitFor(cdp, `Boolean(document.querySelector('[data-material-search-input]'))`, "image tab did not return");

  // Composition changes only the draft input. A request can happen only on the
  // explicit form submit after composition ends.
  const materialCompositionStart = requestRecords.length;
  const composition = JSON.parse(await evaluate(cdp, 'JSON.stringify((()=>{const input=document.querySelector("[data-material-search-input]");const initial=input;input.focus();input.value="中文候选";input.dispatchEvent(new CompositionEvent("compositionstart",{bubbles:true,data:""}));input.dispatchEvent(new InputEvent("input",{bubbles:true,data:"中",inputType:"insertCompositionText",isComposing:true}));input.dispatchEvent(new CompositionEvent("compositionupdate",{bubbles:true,data:"中文"}));input.dispatchEvent(new CompositionEvent("compositionend",{bubbles:true,data:"中文"}));return {same:document.querySelector("[data-material-search-input]")===initial,value:input.value};})())'));
  await delay(120);
  const compositionReads = requestRecords.slice(materialCompositionStart).filter(record => new URL(record.url).pathname === "/api/sidebar/v2/materials");
  if (!composition.same || composition.value !== "中文候选" || compositionReads.length !== 0) throw new Error("material composition must retain draft without a request " + JSON.stringify({ composition, compositionReads }));

  // A same-query refresh failure retains the already authorized list and its
  // committed query. A deliberate retry replaces it only after a successful read.
  materialFaults.set("Chromium", { status: 503 });
  const transientStart = requestRecords.length;
  await evaluate(cdp, '(()=>{const input=document.querySelector("[data-material-search-input]");input.value="Chromium";document.querySelector("[data-material-search-form]").requestSubmit();return true})()');
  await waitFor(cdp, 'Boolean(document.querySelector("[data-v3-sidebar-retained-error]")) && document.querySelectorAll("[data-material-card]").length===7 && document.querySelector("[data-material-search-input]")?.value==="Chromium"', "transient material refresh did not retain the authorized result");
  const transientReads = requestRecords.slice(transientStart).filter(record => new URL(record.url).pathname === "/api/sidebar/v2/materials");
  if (transientReads.length !== 1) throw new Error("transient material refresh request count " + JSON.stringify(transientReads));
  await settleScrollAtTop(cdp);
  await captureScreenshot(cdp, "materials-transient-420");
  materialFaults.delete("Chromium");
  const materialRetryStart = requestRecords.length;
  await evaluate(cdp, 'document.querySelector("[data-v3-retry-material-search]")?.click(); true');
  for (let attempt = 0; attempt < 80 && requestRecords.slice(materialRetryStart).filter(record => new URL(record.url).pathname === "/api/sidebar/v2/materials").length < 1; attempt += 1) await delay(50);
  const retryReads = requestRecords.slice(materialRetryStart).filter(record => new URL(record.url).pathname === "/api/sidebar/v2/materials");
  if (retryReads.length !== 1) throw new Error("material retry must issue one fresh committed-query read " + JSON.stringify(retryReads));
  await waitFor(cdp, '!document.querySelector("[data-v3-sidebar-retained-error]") && document.querySelectorAll("[data-material-card]").length>=5 && document.querySelector("[data-material-search-input]")?.value==="Chromium"', "material retry did not replace the retained failure state");

  // A delayed result from an obsolete query must never repaint the committed
  // current query, whether the obsolete read resolves or fails.
  for (const [obsoleteQuery, fault] of [["old-success", { status: 0, delayMs: 240 }], ["old-failure", { status: 503, delayMs: 240 }]]) {
    materialFaults.set(obsoleteQuery, fault);
    const obsoleteStart = requestRecords.length;
    await evaluate(cdp, `(()=>{const input=document.querySelector("[data-material-search-input]");input.value=${JSON.stringify(obsoleteQuery)};document.querySelector("[data-material-search-form]").requestSubmit();return true})()`);
    for (let attempt = 0; attempt < 80 && !requestRecords.slice(obsoleteStart).some((record) => new URL(record.url).pathname === "/api/sidebar/v2/materials" && new URL(record.url).searchParams.get("q") === obsoleteQuery); attempt += 1) await delay(50);
    if (!requestRecords.slice(obsoleteStart).some((record) => new URL(record.url).pathname === "/api/sidebar/v2/materials" && new URL(record.url).searchParams.get("q") === obsoleteQuery)) throw new Error(`obsolete material ${obsoleteQuery} request did not start`);
    await evaluate(cdp, '(()=>{const input=document.querySelector("[data-material-search-input]");input.value="Chromium";document.querySelector("[data-material-search-form]").requestSubmit();return true})()');
    await waitFor(cdp, 'document.querySelector("[data-material-search-input]")?.value==="Chromium" && document.querySelectorAll("[data-material-card]").length>=5', `current material query did not replace ${obsoleteQuery}`);
    await delay(320);
    const staleMaterial = JSON.parse(await evaluate(cdp, 'JSON.stringify({query:document.querySelector("[data-material-search-input]")?.value||"",notice:Boolean(document.querySelector("[data-v3-sidebar-retained-error]")),cards:document.querySelectorAll("[data-material-card]").length})'));
    if (staleMaterial.query !== "Chromium" || staleMaterial.notice || staleMaterial.cards < 5) throw new Error(`obsolete material ${obsoleteQuery} overwrote the current query ` + JSON.stringify(staleMaterial));
    materialFaults.delete(obsoleteQuery);
  }

  // Authorization loss is different from a transient dependency failure: the
  // V3 bridge invalidates its signed context and the UI must clear every card
  // and send affordance before offering a safe reopen action.
  materialFaults.set("Chromium", { status: 403 });
  await evaluate(cdp, '(()=>{const input=document.querySelector("[data-material-search-input]");input.value="Chromium";document.querySelector("[data-material-search-form]").requestSubmit();return true})()');
  await waitFor(cdp, 'document.body.textContent.includes("当前客户上下文已失效") && !document.body.textContent.includes("sidebar thumbnail customer") && document.querySelectorAll("[data-material-card]").length===0 && document.querySelectorAll("[data-material-send]").length===0 && [...document.querySelectorAll("#tabs button[data-tab]")].every((tab)=>tab.disabled)', "authorization failure did not clear sensitive material content");
  await settleScrollAtTop(cdp);
  await captureScreenshot(cdp, "materials-forbidden-420");
  materialFaults.delete("Chromium");

  // Retry in this same document. The Host's invalidation recovery is not a
  // navigation: it must establish a new signed scope, restore the phone
  // controls, and then permit a fresh panel fetch.
  const sameDocumentLocation = await evaluate(cdp, "location.href");
  await evaluate(cdp, 'document.querySelector("[data-v3-sidebar-retry-context]")?.click(); true');
  await waitFor(cdp, 'document.querySelector("#sidebar-workbench-root")?.dataset.v3SidebarContext==="ready" && Boolean(document.querySelector("#tabs button[data-tab=materials]:not([disabled])")) && !document.querySelector("#change-mobile-button")?.disabled', "same-document context retry did not recover the trusted sidebar");
  const retryLocation = await evaluate(cdp, "location.href");
  if (retryLocation !== sameDocumentLocation) throw new Error("sidebar context retry unexpectedly navigated the document");
  const mobileRecovered = JSON.parse(await evaluate(cdp, 'JSON.stringify((()=>{document.querySelector("#change-mobile-button")?.click();return {modal:!document.querySelector("#mobile-modal")?.classList.contains("hidden"),inputDisabled:Boolean(document.querySelector("#mobile-input")?.disabled),confirmDisabled:Boolean(document.querySelector("#confirm-mobile-button")?.disabled)} })())'));
  if (!mobileRecovered.modal || mobileRecovered.inputDisabled || mobileRecovered.confirmDisabled) throw new Error("same-document retry did not recover mobile controls " + JSON.stringify(mobileRecovered));
  await evaluate(cdp, 'document.querySelector("#close-mobile-modal")?.click(); true');
  await evaluate(cdp, 'document.querySelector("#tabs button[data-tab=materials]")?.click(); true');
  await waitFor(cdp, 'document.querySelectorAll("[data-material-card]").length===5', "same-document retry did not load fresh materials");

  // A successful panel response can outlive the customer that initiated it.
  // Start an old delayed product read, revoke the current scope with a phone
  // 403, retry in the same document, and start a distinct delayed read under
  // the new scope. While the old promise settles, reopening the panel must
  // join the new pending promise rather than create a third request or paint
  // the old product into the renewed customer surface.
  const productPayload = (id, name) => ({ items: [{ id, name, price_minor: 100, currency: "CNY", product_type: "standard", public_url: "" }], total: 1, next_cursor: "" });
  const latePanelStart = requestRecords.length;
  productFaults.push({ delayMs: 900, body: productPayload("old-panel", "旧上下文商品") });
  await evaluate(cdp, 'document.querySelector("#tabs button[data-tab=products]")?.click(); true');
  for (let attempt = 0; attempt < 80 && requestRecords.slice(latePanelStart).filter((record) => new URL(record.url).pathname === "/api/sidebar/v2/products").length < 1; attempt += 1) await delay(50);
  if (requestRecords.slice(latePanelStart).filter((record) => new URL(record.url).pathname === "/api/sidebar/v2/products").length !== 1) throw new Error("old product panel request did not start");
  phoneBindingFault = { status: 403 };
  await evaluate(cdp, '(()=>{document.querySelector("#change-mobile-button")?.click();const input=document.querySelector("#mobile-input");input.value="13800138000";document.querySelector("#confirm-mobile-button")?.click();return true})()');
  await waitFor(cdp, 'document.querySelector("#sidebar-workbench-root")?.dataset.v3SidebarContext==="invalid" && Boolean(document.querySelector("[data-v3-sidebar-retry-context]")) && document.querySelector("#mobile-modal")?.classList.contains("hidden") && document.querySelector("#mobile-input")?.disabled && document.querySelector("#confirm-mobile-button")?.disabled', "phone 403 did not clear the modal and current context");
  productFaults.push({ delayMs: 1200, body: productPayload("new-panel", "新上下文商品") });
  await evaluate(cdp, 'document.querySelector("[data-v3-sidebar-retry-context]")?.click(); true');
  await waitFor(cdp, 'document.querySelector("#sidebar-workbench-root")?.dataset.v3SidebarContext==="ready" && Boolean(document.querySelector("#tabs button[data-tab=products]:not([disabled])"))', "phone 403 same-document retry did not establish the new context");
  await evaluate(cdp, 'document.querySelector("#tabs button[data-tab=products]")?.click(); true');
  for (let attempt = 0; attempt < 80 && requestRecords.slice(latePanelStart).filter((record) => new URL(record.url).pathname === "/api/sidebar/v2/products").length < 2; attempt += 1) await delay(50);
  if (requestRecords.slice(latePanelStart).filter((record) => new URL(record.url).pathname === "/api/sidebar/v2/products").length !== 2) throw new Error("new-context product panel request did not start");
  await delay(980);
  await evaluate(cdp, 'document.querySelector("#tabs button[data-tab=profile]")?.click();document.querySelector("#tabs button[data-tab=products]")?.click(); true');
  const productReadsWhileNewPending = requestRecords.slice(latePanelStart).filter((record) => new URL(record.url).pathname === "/api/sidebar/v2/products");
  if (productReadsWhileNewPending.length !== 2) throw new Error("old panel finally removed the new pending request " + JSON.stringify(productReadsWhileNewPending));
  await waitFor(cdp, 'document.body.textContent.includes("新上下文商品") && !document.body.textContent.includes("旧上下文商品")', "late old product response polluted the renewed panel");

  // Preserve the complementary late-403 fact: a request started under the
  // old token that reports forbidden only after a fresh Bridge retry cannot
  // revoke the renewed scope.
  const old403Start = requestRecords.length;
  materialFaults.set("old-context", { status: 403, delayMs: 240 });
  await evaluate(cdp, 'document.querySelector("#tabs button[data-tab=materials]")?.click(); true');
  await waitFor(cdp, 'Boolean(document.querySelector("[data-material-search-input]"))', "late 403 material panel did not open");
  await evaluate(cdp, '(()=>{const input=document.querySelector("[data-material-search-input]");input.value="old-context";document.querySelector("[data-material-search-form]").requestSubmit();return true})()');
  for (let attempt = 0; attempt < 80 && !requestRecords.slice(old403Start).some((record) => new URL(record.url).pathname === "/api/sidebar/v2/materials" && new URL(record.url).searchParams.get("q") === "old-context"); attempt += 1) await delay(50);
  if (!requestRecords.slice(old403Start).some((record) => new URL(record.url).pathname === "/api/sidebar/v2/materials" && new URL(record.url).searchParams.get("q") === "old-context")) throw new Error("old-context 403 request did not start");
  await evaluate(cdp, 'window.__AICRMSidebarBridge.retry().then(()=>true)');
  await delay(320);
  const freshAfterOld403 = await evaluate(cdp, 'window.__AICRMSidebarBridge.request("/api/sidebar/v2/materials?limit=5&offset=0&q=Chromium").then(()=>true).catch(()=>false)');
  if (!freshAfterOld403) throw new Error("late old 403 revoked the renewed sidebar context");
  materialFaults.delete("old-context");

  if (!sidebarCSP.includes("img-src 'self' data: blob:")) throw new Error("sidebar CSP did not permit its scoped thumbnail blob URL");
  if (![...successfulResources].some((pathname) => /^\/sidebar-assets\/sidebarHost-/.test(pathname)) || ![...successfulResources].some((pathname) => /^\/sidebar-assets\/sidebarStandardOverlay-/.test(pathname)) || ![...successfulResources].some((pathname) => /^\/sidebar-assets\/sidebarImageResourceLoader-/.test(pathname)) || !successfulResources.has("/api/sidebar/v2/bootstrap") || !successfulResources.has("/api/sidebar/v2/materials") || ![...successfulResources].some((pathname) => /\/variants\/thumb_320$/.test(pathname))) throw new Error("sidebar Host/standard overlay resources did not use the actual scoped thumbnail route");
  if (requestURLs.slice(successStart).some((url) => /\/(other-staff-messages|chat-activity|chat_activity)(?:[/?]|$)/.test(new URL(url).pathname))) throw new Error("sidebar standard overlay attempted a removed chat route");
  await evaluate(cdp, '(()=>{localStorage.setItem("sidebar_tab","chat_activity");sessionStorage.setItem("sidebar_active_tab","other_staff_messages");return true})()');
  const negativeStart=requestURLs.length;
  await cdp.call("Page.navigate",{url:baseURL+"/sidebar/bind-mobile?sidebar_case=success&tab=chat_activity&view=other_staff_messages#other-staff-messages"});
  await waitFor(cdp, 'Boolean(document.querySelector("#tabs button[data-tab=materials]:not([disabled])"))', "chat deeplink ready");
  const negative=JSON.parse(await evaluate(cdp, 'JSON.stringify({tabs:[...document.querySelectorAll("#tabs [data-tab]")].map(n=>n.dataset.tab),text:document.body.textContent})'));
  if(negative.tabs.join("|")!=="profile|questionnaires|products|orders|coupons|materials"||/其他客服聊天|聊天动态/.test(negative.text)||requestURLs.slice(negativeStart).some(url=>{const pathname=new URL(url).pathname;return pathname.includes("other-staff-messages")||pathname.includes("chat-activity")||pathname.includes("chat_activity")})) throw new Error("chat deeplink "+JSON.stringify(negative));
  if (exceptions.length) throw new Error(`sidebar Host emitted runtime exceptions: ${exceptions.join(",")}`);
  console.log("sidebar_thumbnail_chromium: PASS");
} catch (error) { failed = true; throw error; }
finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); }
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete");
}
