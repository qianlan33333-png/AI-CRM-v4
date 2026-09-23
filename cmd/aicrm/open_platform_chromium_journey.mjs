import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_OPEN_PLATFORM_TEST_URL;
const username = process.env.AICRM_OPEN_PLATFORM_TEST_USERNAME;
const password = process.env.AICRM_OPEN_PLATFORM_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) {
  throw new Error("Open Platform Chromium journey requires HTTPS URL and test credentials");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const browserBinary = () => {
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
};

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
  call(method, params = {}, timeout = 8000) { return new Promise((resolve, reject) => {
    const id = ++this.nextID;
    const timer = setTimeout(() => {
      if (!this.pending.has(id)) return;
      this.pending.delete(id);
      reject(new Error(`CDP ${method} timed out`));
    }, timeout);
    const settle = (callback) => (value) => { clearTimeout(timer); callback(value); };
    this.pending.set(id, { resolve: settle(resolve), reject: settle(reject) });
    try { this.socket.send(JSON.stringify({ id, method, params })); }
    catch (_) { clearTimeout(timer); this.pending.delete(id); reject(new Error(`CDP ${method} unavailable`)); }
  }); }
  on(method, listener) { const listeners = this.events.get(method) || []; listeners.push(listener); this.events.set(method, listeners); return () => this.events.set(method, (this.events.get(method) || []).filter((item) => item !== listener)); }
  nextEvent(method, predicate, timeout, message) { return new Promise((resolve, reject) => { let unsubscribe = () => {}; const timer = setTimeout(() => { unsubscribe(); reject(new Error(message)); }, timeout); unsubscribe = this.on(method, (params) => { if (!predicate(params)) return; clearTimeout(timer); unsubscribe(); resolve(params); }); }); }
  close() { for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed")); this.pending.clear(); this.events.clear(); this.socket.close(); }
}

async function debuggingAddress(profile) {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try { const value = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`; } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
}
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
}
async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    const value = await evaluate(cdp, expression);
    if (value) return value;
    await delay(50);
  }
  throw new Error(message);
}
async function waitForResource(resources, pathname, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (resources.has(pathname)) return resources.get(pathname);
    await delay(50);
  }
  throw new Error(message);
}
async function waitForRequestFinished(finishedRequests, requestID, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    const result = finishedRequests.get(requestID);
    if (result === "finished") return;
    if (result === "failed") throw new Error(message);
    await delay(50);
  }
  throw new Error(message);
}
async function activationFailureCategory(cdp, response) {
  const known = new Set(["authentication_required", "invalid_request", "open_platform_request_failed"]);
  try {
    const body = await cdp.call("Network.getResponseBody", { requestId: response.requestID });
    const parsed = JSON.parse(String(body.body || ""));
    return known.has(parsed?.error) ? parsed.error : "response_invalid";
  } catch (_) {
    return "response_unavailable";
  }
}
const navigation = (cdp, message) => cdp.nextEvent("Page.frameNavigated", (params) => Boolean(params.frame && !params.frame.parentId), 8000, message);
async function browserExit(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) { child.kill("SIGKILL"); await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(1000)]); }
}
async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return true; }
    catch (error) { if (!error || !["ENOTEMPTY", "EBUSY", "EPERM"].includes(error.code)) return false; await delay(100); }
  }
  return false;
}

const journeyStartedAt = Date.now();
const progress = (phase) => console.log(`open_platform_chromium: phase=${phase} elapsed_ms=${Date.now() - journeyStartedAt}`);
progress("node_started");
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-open-platform-chromium-"));
let browser; let cdp; let failed = false;
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const created = await (await fetch(`${await debuggingAddress(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const oauthAuthChallenges = { cancelled: 0, other: 0, continuationErrors: 0 };
  cdp.on("Fetch.requestPaused", (params) => {
    void cdp.call("Fetch.continueRequest", { requestId: params.requestId }).catch(() => { oauthAuthChallenges.continuationErrors += 1; });
  });
  cdp.on("Fetch.authRequired", (params) => {
    let isOwnOAuthToken = false;
    try {
      const target = new URL(String(params.request?.url || ""));
      isOwnOAuthToken = target.origin === new URL(baseURL).origin && target.pathname === "/oauth/token";
    } catch (_) {}
    if (isOwnOAuthToken) oauthAuthChallenges.cancelled += 1;
    else oauthAuthChallenges.other += 1;
    void cdp.call("Fetch.continueWithAuth", {
      requestId: params.requestId,
      authChallengeResponse: { response: isOwnOAuthToken ? "CancelAuth" : "Default" },
    }).catch(() => { oauthAuthChallenges.continuationErrors += 1; });
  });
  await cdp.call("Fetch.enable", { handleAuthRequests: true });
  progress("browser_ready");
  const resources = new Map(); const requests = new Map(); const finishedRequests = new Map(); const exceptions = [];
  let selectedDetailRequests = 0;
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const requestID=String(params.requestId || "");
      const method=String(params.request?.method || "");
      const pathname=new URL(String(params.request?.url || "")).pathname;
      requests.set(requestID, { method, pathname });
      if (method === "GET" && pathname === "/api/admin/open-platform/clients/browser-open-empty-cidr-probe") {
        selectedDetailRequests += 1;
        resources.set(`GET:${pathname}:${selectedDetailRequests}`, { requestID, status: 0 });
      }
    } catch (_) {}
  });
  cdp.on("Runtime.exceptionThrown", (params) => { const detail = params.exceptionDetails || {}; const kind = String(detail.exception?.className || detail.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96); if (exceptions.length < 8) exceptions.push(kind); });
  cdp.on("Network.loadingFinished", (params) => { finishedRequests.set(String(params.requestId || ""), "finished"); });
  cdp.on("Network.loadingFailed", (params) => { finishedRequests.set(String(params.requestId || ""), "failed"); });
  cdp.on("Network.responseReceived", (params) => { try {
    const pathname = new URL(String(params.response?.url || "")).pathname;
    const status = Number(params.response?.status) || 0;
    if (pathname.startsWith("/assets/")) resources.set("/assets/", status);
    const request = requests.get(String(params.requestId || ""));
    if (pathname === "/api/admin/open-platform/clients" || pathname === "/api/admin/open-platform/routes") resources.set(pathname, status);
    if (pathname === "/api/admin/open-platform/clients" && request?.method === "POST") resources.set(`POST:${pathname}`, { status, requestID: String(params.requestId || "") });
    if (pathname === "/api/admin/open-platform/clients/browser-open-agent/activate") resources.set(pathname, { status, requestID: String(params.requestId || "") });
    if (pathname === "/api/admin/open-platform/clients/browser-open-agent" && request?.method === "PATCH") resources.set(`PATCH:${pathname}`, { status, requestID: String(params.requestId || "") });
  } catch (_) {} });

  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin%2Fapidocs.html` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  const login = navigation(cdp, "login form did not complete top-level navigation");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  const frame = await login;
  if (new URL(frame.frame.url).pathname !== "/admin/apidocs.html") throw new Error("login did not redirect to the V1 caller page");
  await waitFor(cdp, `(() => {
    const root=document.querySelector('[data-open-platform-docs="v1"]');
    const title=root?.querySelector('.open-platform-docs-title');
    const management=root?.querySelector('button[data-open-platform-action="密钥管理"]');
    const rows=root?.querySelectorAll('#operations tbody tr') || [];
    return Boolean(root && title?.textContent?.trim() === 'AI-CRM 外部只读 API v1' && management && rows.length === 11);
  })()`, "login did not render the default API document");
  if (resources.has("/api/admin/open-platform/clients") || resources.has("/api/admin/open-platform/routes")) throw new Error("default API document requested caller management data");
  // Documentation filtering stays as a draft until an ordinary Enter. This is
  // purely local filtering: it must never open caller-management reads.
  const docSearchCandidate = await evaluate(cdp, `(() => {
    const input=document.querySelector('[data-open-platform-doc-search]');
    if (!(input instanceof HTMLInputElement)) return null;
    const visible=()=>document.querySelectorAll('.open-platform-doc-section:not([hidden])').length;
    const before=visible(); input.focus(); input.value='错误码';
    input.dispatchEvent(new Event('input',{bubbles:true,cancelable:true}));
    input.dispatchEvent(new FocusEvent('blur',{bubbles:true}));
    input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));
    input.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));
    const candidate=new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter',isComposing:true});
    Object.defineProperty(candidate,'keyCode',{value:229}); input.dispatchEvent(candidate);
    return {before,after:visible(),prevented:candidate.defaultPrevented};
  })()`);
  if (!docSearchCandidate || docSearchCandidate.prevented || docSearchCandidate.after !== docSearchCandidate.before) throw new Error('Open Platform documentation IME candidate altered the draft filter');
  await evaluate(cdp, "document.querySelector('[data-open-platform-doc-search]').dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,cancelable:true,key:'Enter',code:'Enter'})); true");
  await waitFor(cdp, "document.querySelectorAll('.open-platform-doc-section:not([hidden])').length===1", "Open Platform documentation ordinary Enter did not apply the draft filter");
  const docSearchFocus = await evaluate(cdp, "(()=>{const input=document.querySelector('[data-open-platform-doc-search]');return Boolean(input&&document.activeElement===input&&input.value==='错误码')})()");
  if (!docSearchFocus) throw new Error('Open Platform documentation Enter did not retain query focus');
  if (resources.has("/api/admin/open-platform/clients") || resources.has("/api/admin/open-platform/routes")) throw new Error("documentation filter unexpectedly requested caller management data");
  const managementOpened = await evaluate(cdp, `(() => {
    const button=document.querySelector('[data-open-platform-docs="v1"] button[data-open-platform-action="密钥管理"]');
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`);
  if (!managementOpened) throw new Error("default API document did not expose key management");
  // The fixture deliberately delays this selected detail GET. A writable
  // create form during that gap would let a real administrator type into a DOM
  // node that the eventual detail render replaces.
  const delayedDetailPath = "GET:/api/admin/open-platform/clients/browser-open-empty-cidr-probe";
  const firstDetailRequest = await waitForResource(resources, `${delayedDetailPath}:1`, "selected caller detail request did not begin");
  const createDuringDetail = await evaluate(cdp, "Boolean(document.querySelector('[data-open-platform-create=\"client_id\"]'))");
  if (createDuringDetail) throw new Error("Open Platform create form was writable before selected detail completed");
  // Re-read the same selected caller while its first response is delayed. The
  // fixture returns the second response first, exercising an actual stale
  // completion rather than a different-client selection path.
  if (!await evaluate(cdp, "Boolean([...document.querySelectorAll('[data-open-platform-action]')].find(node => node.dataset.openPlatformAction === '刷新'))")) throw new Error("Open Platform refresh action was unavailable during detail load");
  await evaluate(cdp, "[...document.querySelectorAll('[data-open-platform-action]')].find(node => node.dataset.openPlatformAction === '刷新').click(); true");
  await waitForResource(resources, `${delayedDetailPath}:2`, "same caller detail refresh did not begin");
  // Wait for the settled detail and catalog controls together; a later render
  // must never replace a form between filling it and its create action.
  const authenticatedHostReady = `(() => {
    const root=document.querySelector('[data-open-platform-host="v1"]');
    const client=root?.querySelector('[data-open-platform-client="browser-open-empty-cidr-probe"]');
    const form=root?.querySelector('[data-open-platform-create="client_id"]');
    const capabilities=root ? [...root.querySelectorAll('input[name="create-capability"]')].map(input=>input.value).sort() : [];
    const csrf=String(document.cookie||'').split(';').some(part=>part.trim().startsWith('aicrm_admin_csrf=')) || String(document.cookie||'').split(';').some(part=>part.trim().startsWith('aicrm_csrf='));
    const audienceCapabilities = ['audience.product.read', 'audience.member.read', 'audience.member.operations.read', 'audience.member.history.read', 'audience.push.write'];
    return Boolean(root && client && form && csrf && capabilities.length===18 && audienceCapabilities.every(capability => capabilities.includes(capability)) && capabilities.includes('platform.capabilities.read') && capabilities.includes('chat.read') && capabilities.includes('order.read'));
  })()`;
  try { await waitFor(cdp, authenticatedHostReady, "authenticated V1 caller Host did not finish loading"); }
  catch (_) {
    const state = await evaluate(cdp, "(() => ({path:location.pathname,stage:Boolean(document.querySelector('#stage')),host:Boolean(document.querySelector('[data-open-platform-host=\"v1\"]')),detail:Boolean(document.querySelector('[data-open-platform-client=\"browser-open-empty-cidr-probe\"]')),create:Boolean(document.querySelector('[data-open-platform-create=\"client_id\"]')),capabilities:document.querySelectorAll('input[name=\"create-capability\"]').length,csrf:Boolean(String(document.cookie||'').split(';').some(part=>part.trim().startsWith('aicrm_admin_csrf=')||part.trim().startsWith('aicrm_csrf=')))}))()");
    throw new Error(`authenticated V1 caller Host did not finish loading: path=${state?.path || "unavailable"} stage=${Boolean(state?.stage)} host=${Boolean(state?.host)} detail=${Boolean(state?.detail)} create=${Boolean(state?.create)} capabilities=${Number(state?.capabilities) || 0} csrf=${Boolean(state?.csrf)} assets=${resources.get("/assets/") || 0} clients=${resources.get("/api/admin/open-platform/clients") || 0} catalog=${resources.get("/api/admin/open-platform/routes") || 0} exceptions=${exceptions.length ? exceptions.join(",") : "none"}`);
  }
  progress("authenticated_host");

  const click = async (label) => evaluate(cdp, `(() => { const button=[...document.querySelectorAll('[data-open-platform-action]')].find((node)=>node.dataset.openPlatformAction===${JSON.stringify(label)}); if (!button) return false; button.click(); return true; })()`);
  await evaluate(cdp, `(() => {
    document.querySelector('[data-open-platform-create="client_id"]').value='browser-open-agent';
    document.querySelector('[data-open-platform-create="display_name"]').value='Browser Open Agent';
    document.querySelector('[data-open-platform-create="token_ttl_seconds"]').value='1800';
    document.querySelector('input[name="create-scope"][value="read"]').checked=true;
    document.querySelector('input[name="create-capability"][value="platform.capabilities.read"]').checked=true;
    return true;
  })()`);
  const releaseFirstDetail = await evaluate(cdp, "Promise.race([fetch('/__test__/release-open-platform-first-detail',{method:'POST'}).then(response=>({status:response.status})),new Promise(resolve=>setTimeout(()=>resolve({timeout:true,status:0}),8000))])");
  if (releaseFirstDetail?.timeout || releaseFirstDetail?.status !== 204) throw new Error(`first selected detail release status=${releaseFirstDetail?.status || 0}`);
  await waitForRequestFinished(finishedRequests, firstDetailRequest.requestID, "first selected detail did not finish after its explicit release");
  // A pair of animation frames ensures the Host receives the completed fetch
  // continuation and has a render opportunity. This is event-driven rather
  // than a fixed delay: the assertion is only meaningful after the stale
  // response actually arrived.
  await evaluate(cdp, "new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve)))");
  const preservedCreateForm = await evaluate(cdp, "(() => ({client_id:document.querySelector('[data-open-platform-create=\"client_id\"]')?.value || '',display_name:document.querySelector('[data-open-platform-create=\"display_name\"]')?.value || ''}))()");
  if (preservedCreateForm.client_id !== "browser-open-agent" || preservedCreateForm.display_name !== "Browser Open Agent") throw new Error("stale selected detail replaced a ready create form");
  const createPath = "POST:/api/admin/open-platform/clients";
  resources.delete(createPath);
  if (!await click("创建并显示一次密钥")) throw new Error("create action was unavailable");
  const createResponse = await waitForResource(resources, createPath, "create did not issue its POST request");
  if (createResponse.status !== 201) throw new Error(`create status=${createResponse.status} category=${await activationFailureCategory(cdp, createResponse)}`);
  await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret'))", "create succeeded but did not display a one-time credential");
  const firstSecret = await evaluate(cdp, "document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret')?.textContent || ''");
  if (!firstSecret) throw new Error("one-time credential was empty");
  progress("issued");
  const browserJSON = async (operation, expression) => {
    const result = await evaluate(cdp, `Promise.race([(${expression}),new Promise((resolve)=>setTimeout(()=>resolve({timeout:true,status:0,body:null}),8000))])`);
    if (result?.timeout) throw new Error(`${operation} request timed out`);
    return result;
  };
  const oauth = (secret, scope = "read") => browserJSON("oauth", `fetch('/oauth/token',{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/x-www-form-urlencoded','Authorization':'Basic '+btoa('browser-open-agent:'+${JSON.stringify(secret)})},body:new URLSearchParams({grant_type:'client_credentials',audience:'external_integration',scope:${JSON.stringify(scope)}})}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))`);
  const restCatalog = (token) => browserJSON("rest_catalog", `fetch('/open/v1/capabilities',{headers:{Authorization:'Bearer '+${JSON.stringify(token)}}}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))`);
  const mcpCatalog = (token) => browserJSON("mcp_catalog", `fetch('/mcp',{method:'POST',headers:{Authorization:'Bearer '+${JSON.stringify(token)},'Content-Type':'application/json'},body:JSON.stringify({jsonrpc:'2.0',id:'browser-catalog',method:'tools/list',params:{}})}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))`);
  const clientDetail = () => browserJSON("client_detail", "fetch('/api/admin/open-platform/clients/browser-open-agent',{credentials:'same-origin'}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))");

  const firstActivationPath = "/api/admin/open-platform/clients/browser-open-agent/activate";
  resources.delete(firstActivationPath);
  if (!await click("我已手动复制并确认启用")) throw new Error("manual credential confirmation was unavailable");
  const firstActivation = await waitForResource(resources, firstActivationPath, "manual confirmation did not issue an activation request");
  if (firstActivation.status !== 200) throw new Error(`manual confirmation activation status=${firstActivation.status} category=${await activationFailureCategory(cdp, firstActivation)}`);
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"]')?.textContent.includes('已启用')", "activation succeeded but the caller Host did not refresh as enabled");
  progress("activated");
  const expectedCatalogCapabilities = ["ai.review_plan.create", "audience.product.read", "audience.member.read", "audience.member.operations.read", "audience.member.history.read", "audience.push.write", "chat.read", "customer.activity.read", "customer.detail.read", "customer.read", "customer.resolve", "identity.read", "operation.read", "order.read", "platform.capabilities.read", "questionnaire.read", "radar.click.read", "radar.link.read"].sort();
  // Activation refreshes the selected detail and catalog separately. The caller
  // badge may be enabled while the create form is still withheld during loading.
  // Wait for the exact catalog, then retain the independent contents assertion.
  const catalogCapabilityValues = await waitFor(cdp, `(() => {
    if (document.querySelector('[data-open-platform-secret="browser-open-agent"]')) return null;
    const values = [...document.querySelectorAll('input[name="create-capability"]')].map(input=>input.value).sort();
    return JSON.stringify(values) === ${JSON.stringify(JSON.stringify(expectedCatalogCapabilities))} ? values : null;
  })()`, "administrator catalog did not finish refreshing after activation");
  if (!Array.isArray(catalogCapabilityValues) || catalogCapabilityValues.length !== expectedCatalogCapabilities.length || catalogCapabilityValues.some((value, index) => value !== expectedCatalogCapabilities[index])) throw new Error("administrator catalog did not expose the complete current V1 capability set");
  const operationIDs = (catalog) => Array.isArray(catalog?.body?.data?.operations) ? catalog.body.data.operations.map((item) => item?.operation_id).filter((item) => typeof item === "string").sort() : [];
  const toolNames = (catalog) => Array.isArray(catalog?.body?.result?.tools) ? catalog.body.result.tools.map((item) => item?.name).filter((item) => typeof item === "string").sort() : [];
  const sameStrings = (actual, expected) => actual.length === expected.length && actual.every((value, index) => value === expected[index]);
  const firstOAuth = await oauth(firstSecret); const firstToken = firstOAuth?.body?.access_token;
  if (firstOAuth?.status !== 200 || typeof firstToken !== "string" || !firstToken) throw new Error("OAuth did not issue an activated token");
  const firstCatalog = await restCatalog(firstToken);
  if (firstCatalog?.status !== 200 || !sameStrings(operationIDs(firstCatalog), ["platform.capabilities.list"])) throw new Error("REST V1 catalog did not restrict the initial caller to its single granted operation");
  const firstMCP = await mcpCatalog(firstToken);
  if (firstMCP?.status !== 200 || !sameStrings(toolNames(firstMCP), ["list_capabilities"])) throw new Error("MCP catalog did not restrict the initial caller to its single granted tool");
  progress("initial_catalog");

  const beforeGrant = await clientDetail();
  const beforeGrantVersion = beforeGrant?.body?.client?.auth_version;
  if (beforeGrant?.status !== 200 || !Number.isInteger(beforeGrantVersion)) throw new Error("grant baseline was unavailable");
  const grantPatchPath = "PATCH:/api/admin/open-platform/clients/browser-open-agent";
  resources.delete(grantPatchPath);
  await evaluate(cdp, "document.querySelector('input[name=\"edit-capability\"][value=\"customer.resolve\"]')?.click(); true");
  if (!await click("保存授权")) throw new Error("grant save action was unavailable");
  const grantPatch = await waitForResource(resources, grantPatchPath, "grant save did not issue its PATCH request");
  if (grantPatch.status !== 200) throw new Error(`grant save status=${grantPatch.status} category=${await activationFailureCategory(cdp, grantPatch)}`);
  const afterGrant = await clientDetail();
  const grantVersion = afterGrant?.body?.client?.auth_version;
  if (afterGrant?.status !== 200 || !Number.isInteger(grantVersion) || grantVersion <= beforeGrantVersion || !Array.isArray(afterGrant?.body?.client?.capabilities) || !afterGrant.body.client.capabilities.includes("customer.resolve")) throw new Error("grant save did not durably advance caller authorization");
  await waitFor(cdp, `(() => { const card=document.querySelector('[data-open-platform-client=\"browser-open-agent\"]'); const checkbox=card?.querySelector('input[name=\"edit-capability\"][value=\"customer.resolve\"]'); return Boolean(checkbox?.checked && card?.textContent.includes('OAuth 版本 ${grantVersion}')); })()`, "grant save completed but the caller Host did not reload its authorization revision");
  progress("grant_committed");
  if ((await restCatalog(firstToken))?.status !== 401) throw new Error("grant update did not revoke the prior OAuth token");
  const grantedOAuth = await oauth(firstSecret); const grantedToken = grantedOAuth?.body?.access_token;
  if (grantedOAuth?.status !== 200 || typeof grantedToken !== "string") throw new Error("credential did not issue a replacement token after grant update");
  const grantedCatalog = await restCatalog(grantedToken);
  if (grantedCatalog?.status !== 200 || !sameStrings(operationIDs(grantedCatalog), ["customer.resolve", "platform.capabilities.list"])) throw new Error("changed grant did not appear in the restricted REST catalog");
  const grantedMCP = await mcpCatalog(grantedToken);
  if (grantedMCP?.status !== 200 || !sameStrings(toolNames(grantedMCP), ["list_capabilities", "resolve_customer"])) throw new Error("changed grant did not appear in the restricted MCP catalog");
  progress("grant_catalog");

  if (!await click("轮换密钥")) throw new Error("rotation action was unavailable");
  await waitFor(cdp, "Boolean(document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret'))", "rotation did not display its one-time credential");
  const secondSecret = await evaluate(cdp, "document.querySelector('[data-open-platform-secret=\"browser-open-agent\"] .open-platform-secret')?.textContent || ''");
  if (!secondSecret || secondSecret === firstSecret) throw new Error("rotation did not issue a distinct one-time credential");
  progress("rotated");
  progress("rotation_old_secret_check");
  const rotatedOldOAuth = await oauth(firstSecret);
  if (rotatedOldOAuth?.status !== 401 || rotatedOldOAuth?.body?.error !== "invalid_client") throw new Error("rotation did not return the original invalid-client OAuth rejection");
  if (oauthAuthChallenges.cancelled !== 1 || oauthAuthChallenges.other !== 0 || oauthAuthChallenges.continuationErrors !== 0) throw new Error("unexpected OAuth authentication challenge handling");
  progress("rotated_secret_revoked");
  progress("rotation_detail_request");
  const rotated = await clientDetail();
  if (rotated?.status !== 200 || rotated?.body?.client?.enabled !== false) throw new Error("rotation did not return the caller to disabled handoff state");
  progress("rotated_detail");
  const secondActivationPath = "/api/admin/open-platform/clients/browser-open-agent/activate";
  resources.delete(secondActivationPath);
  if (!await click("我已手动复制并确认启用")) throw new Error("rotated credential confirmation was unavailable");
  const secondActivation = await waitForResource(resources, secondActivationPath, "rotated credential confirmation did not issue an activation request");
  if (secondActivation.status !== 200) throw new Error(`rotated credential confirmation activation status=${secondActivation.status} category=${await activationFailureCategory(cdp, secondActivation)}`);
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"]')?.textContent.includes('已启用')", "rotated activation succeeded but the caller Host did not refresh as enabled");
  progress("rotation_reactivated");
  const secondOAuth = await oauth(secondSecret); const secondToken = secondOAuth?.body?.access_token;
  if (secondOAuth?.status !== 200 || typeof secondToken !== "string") throw new Error("rotated credential did not issue an OAuth token");
  progress("rotation_oauth");
  const rotatedCatalog = await restCatalog(secondToken);
  if (rotatedCatalog?.status !== 200 || !sameStrings(operationIDs(rotatedCatalog), ["customer.resolve", "platform.capabilities.list"])) throw new Error("rotation did not preserve the approved restricted REST grant");
  progress("rotation_rest_catalog");
  const rotatedMCP = await mcpCatalog(secondToken);
  if (rotatedMCP?.status !== 200 || !sameStrings(toolNames(rotatedMCP), ["list_capabilities", "resolve_customer"])) throw new Error("rotation did not preserve the approved restricted MCP grant");
  progress("rotation_catalog");

  if (!await click("停用调用方")) throw new Error("disable action was unavailable");
  await waitFor(cdp, "document.querySelector('[data-open-platform-client=\"browser-open-agent\"]')?.textContent.includes('待启用或已停用')", "disable did not update the caller detail");
  progress("disabled_detail");
  if ((await restCatalog(secondToken))?.status !== 401) throw new Error("disable did not revoke the current OAuth token");
  progress("disabled_token_revoked");
  const audit = await browserJSON("audit", "fetch('/api/admin/open-platform/clients/browser-open-agent/audit?limit=20',{credentials:'same-origin'}).then(async(response)=>({status:response.status,body:await response.json().catch(()=>null)}))");
  if (audit?.status !== 200 || !Array.isArray(audit?.body?.items) || !audit.body.items.some((item) => item?.action === "machine_client_enabled" && item?.outcome === "disabled")) throw new Error("caller audit did not record final disable outcome");
  progress("disabled");
  console.log("open_platform_chromium: PASS");
} catch (error) { failed = true; throw error; }
finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) { browser.kill("SIGTERM"); await browserExit(browser); }
  const removed = await removeProfile(profile);
  if (!removed && !failed) throw new Error("Chromium test profile cleanup did not complete");
}
