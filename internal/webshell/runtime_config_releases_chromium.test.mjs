import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_RUNTIME_RELEASE_TEST_URL;
const username = process.env.AICRM_RUNTIME_RELEASE_TEST_USERNAME;
const password = process.env.AICRM_RUNTIME_RELEASE_TEST_PASSWORD;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) {
  throw new Error("runtime release Chromium journey requires HTTPS URL and test login credentials");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const chromeCandidates = () => {
  const explicit = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") explicit.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  return [...explicit, "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"];
};
const browserBinary = () => {
  for (const candidate of chromeCandidates()) {
    if (candidate.includes("/")) {
      try { if (spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate; } catch (_) {}
      continue;
    }
    if (spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
  }
  throw new Error("Chromium binary is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const { resolve, reject } = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) reject(new Error(`CDP ${message.error.code || "error"}`));
        else resolve(message.result || {});
        return;
      }
      if (!message.method) return;
      for (const listener of this.events.get(message.method) || []) listener(message.params || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  on(method, listener) {
    const listeners = this.events.get(method) || [];
    listeners.push(listener);
    this.events.set(method, listeners);
    return () => this.events.set(method, (this.events.get(method) || []).filter((candidate) => candidate !== listener));
  }
  nextEvent(method, predicate, timeoutMilliseconds, timeoutMessage) {
    return new Promise((resolve, reject) => {
      let unsubscribe = () => {};
      const timer = setTimeout(() => {
        unsubscribe();
        reject(new Error(timeoutMessage));
      }, timeoutMilliseconds);
      unsubscribe = this.on(method, (params) => {
        if (!predicate(params)) return;
        clearTimeout(timer);
        unsubscribe();
        resolve(params);
      });
    });
  }
  close() {
    for (const { reject } of this.pending.values()) reject(new Error("CDP browser closed"));
    this.pending.clear();
    this.events.clear();
    this.socket.close();
  }
}


const waitForPort = async (profile) => {
  const activePort = path.join(profile, "DevToolsActivePort");
  for (let attempt = 0; attempt < 160; attempt += 1) {
    try {
      const [port] = String(await fs.readFile(activePort, "utf8")).split("\n");
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
};

const waitFor = async (cdp, expression, message) => {
  for (let attempt = 0; attempt < 160; attempt += 1) {
    const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
    if (result.result?.value) return;
    await delay(50);
  }
  throw new Error(message);
};

const evaluate = async (cdp, expression) => {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
};

const waitForTopLevelNavigation = (cdp, message) => cdp.nextEvent(
  "Page.frameNavigated",
  (params) => Boolean(params.frame && !params.frame.parentId),
  8000,
  message,
);

const releaseHostDiagnostics = async (cdp, runtimeExceptions, responseStatuses) => {
  const markers = await evaluate(cdp, `(() => ({
    path: location.pathname,
    host: Boolean(document.querySelector('[data-runtime-release-host]')),
    draft: Boolean(document.querySelector('[data-runtime-release-create]')),
    error: document.querySelector('[data-runtime-release-status]')?.className || '',
    message: document.querySelector('[data-runtime-release-status]')?.textContent?.slice(0, 160) || ''
  }))()`);
  const resources = ["/static/admin_console/runtime_config_releases_host.js", "/api/admin/config/runtime-releases"]
    .map((pathname) => `${pathname}:${responseStatuses.get(pathname) || "unseen"}`).join(",");
  const exceptions = runtimeExceptions.length ? runtimeExceptions.join(",") : "none";
  return `path=${markers?.path || "unavailable"} host=${Boolean(markers?.host)} draft=${Boolean(markers?.draft)} page_error_class=${markers?.error || "none"} page_message=${markers?.message || "none"} resources=${resources} runtime_exceptions=${exceptions}`;
};

const waitForBrowserExit = async (child, timeoutMilliseconds) => {
  if (!child || child.exitCode !== null || child.signalCode !== null) return true;
  return new Promise((resolve) => {
    const timer = setTimeout(() => resolve(false), timeoutMilliseconds);
    child.once("exit", () => { clearTimeout(timer); resolve(true); });
  });
};

const removeProfile = async (profile) => {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!error || !["ENOTEMPTY", "EBUSY", "EPERM"].includes(error.code)) return false;
      await delay(100);
    }
  }
  return false;
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-runtime-config-chromium-"));
let browser;
let cdp;
let journeyFailed = false;
try {
  const binary = browserBinary();
  browser = spawn(binary, [
    "--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`,
    "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost",
    "about:blank",
  ], { stdio: ["ignore", "ignore", "ignore"] });
  const browserAddress = await waitForPort(profile);
  const created = await (await fetch(`${browserAddress}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  const runtimeExceptions = [];
  const responseStatuses = new Map();
  cdp.on("Runtime.exceptionThrown", (params) => {
    const details = params.exceptionDetails || {};
    const kind = String(details.exception?.className || details.text || "runtime_exception").replace(/[^a-zA-Z0-9_.-]/g, "_").slice(0, 96);
    if (kind && runtimeExceptions.length < 8) runtimeExceptions.push(kind);
  });
  cdp.on("Network.responseReceived", (params) => {
    try {
      const pathname = new URL(String(params.response?.url || "")).pathname;
      if (pathname === "/static/admin_console/runtime_config_releases_host.js" || pathname === "/api/admin/config/runtime-releases") {
        responseStatuses.set(pathname, Number(params.response?.status) || 0);
      }
    } catch (_) {}
  });

  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin%2Fconfig%2Freleases%2Fnew` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  const loginNavigation = waitForTopLevelNavigation(cdp, "login form did not complete top-level navigation");
  await evaluate(cdp, `(() => {
    document.querySelector('input[name="username"]').value = ${JSON.stringify(username)};
    document.querySelector('input[name="password"]').value = ${JSON.stringify(password)};
    document.querySelector('form[action="/login"]').requestSubmit();
    return true;
  })()`);
  const loginFrame = await loginNavigation;
  if (new URL(loginFrame.frame.url).pathname !== "/admin/config/releases/new") throw new Error("login did not redirect to the requested Config release route");

  const snapshot = () => evaluate(cdp, "fetch('/api/admin/config/runtime-releases', {credentials:'same-origin'}).then((response) => response.ok ? response.json() : null).then((body) => ({revision: body?.runtime_releases?.active_revision, limit: body?.runtime_releases?.effective?.automation_max_recipients_per_run}))");
  const releasePath = (id) => `/admin/config/releases/${id}`;
  const publish = async (limit, ordinal) => {
    try {
      await waitFor(cdp, "location.pathname === '/admin/config/releases/new' && Boolean(document.querySelector('[data-runtime-release-create] button[type=submit]:not([disabled])'))", `authenticated Config shell and Host did not render release ${ordinal}`);
    } catch (_) {
      throw new Error(`authenticated Config shell and Host did not render release ${ordinal}: ${await releaseHostDiagnostics(cdp, runtimeExceptions, responseStatuses)}`);
    }
    await evaluate(cdp, `(() => {
      const form = document.querySelector('[data-runtime-release-create]');
      form.querySelector('[name="max_recipients"]').value = ${JSON.stringify(String(limit))};
      form.querySelector('[name="confirm"]').checked = true;
      form.requestSubmit();
      return true;
    })()`);
    try {
      await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-validate]'))", `draft ${ordinal} was not persisted through actual HTTP API`);
    } catch (_) {
      throw new Error(`draft ${ordinal} was not persisted through actual HTTP API: ${await releaseHostDiagnostics(cdp, runtimeExceptions, responseStatuses)}`);
    }
    await evaluate(cdp, "document.querySelector('[data-runtime-release-validate]').click(); true");
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-publish]'))", `validation ${ordinal} was not persisted through actual HTTP API`);
    await evaluate(cdp, "document.querySelector('[data-runtime-release-publish]').click(); true");
    await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-rollback]'))", `publication ${ordinal} was not persisted through actual HTTP API`);
    const publishedID = Number(await evaluate(cdp, "document.querySelector('[data-runtime-release-rollback]')?.dataset.runtimeReleaseRollback"));
    const current = await snapshot();
    if (!Number.isSafeInteger(publishedID) || publishedID <= 0 || current?.revision !== publishedID || current.limit !== limit) throw new Error(`publication ${ordinal} did not change the actual effective value`);
    return publishedID;
  };
  const navigateToRelease = async (id) => {
    await cdp.call("Page.navigate", { url: `${baseURL}${releasePath(id)}` });
    await waitFor(cdp, `location.pathname === ${JSON.stringify(releasePath(id))} && document.querySelector('[data-runtime-release-rollback]')?.dataset.runtimeReleaseRollback === ${JSON.stringify(String(id))}`, `release ${id} detail did not render after navigation`);
  };

  // The first authenticated redirect must itself load the Host. A second
  // navigation would hide a broken login-to-Config route from this journey.
  const firstReleaseID = await publish(2, 1);
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/config/releases/new` });
  const secondReleaseID = await publish(3, 2);
  await navigateToRelease(firstReleaseID);
  await evaluate(cdp, "document.querySelector('[data-runtime-release-rollback]').click(); true");
  await waitFor(cdp, "document.body.textContent.includes('已创建并发布回滚记录') && document.body.textContent.includes('实际使用回读')", "rollback and actual-use readback were not rendered");
  const restored = await snapshot();
  if (restored.limit !== 2 || restored.revision === firstReleaseID || restored.revision === secondReleaseID) throw new Error("rollback did not publish a new revision with the older runtime value");

  // The Config Center keeps the donor's four-column category table. Only
  // categories with one explicit V3 primary enable owner render a switch;
  // other categories must not gain a fabricated aggregate provider command.
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/config` });
  await waitFor(cdp, "document.querySelectorAll('[data-category-row]').length === 5 && Boolean(document.querySelector('[data-category-row=\"wecom_base\"] .cc-switch input'))", "Config Center did not render the operational category list");
  const centerLayout = await evaluate(cdp, `(() => ({
    headers: [...document.querySelectorAll('.cc-category-table thead th')].map((cell) => cell.textContent.trim()),
    sidebarHasAggregateSwitch: Boolean(document.querySelector('[data-category-row="sidebar_identity"]')),
    categories: [...document.querySelectorAll('[data-category-row]')].map(row => row.dataset.categoryRow).sort(),
    hasModel: document.body.textContent.includes('大模型'),
    hasTechnicalHomepageColumn: document.body.textContent.includes('发布/应用状态')
  }))()`);
  if (JSON.stringify(centerLayout?.headers) !== JSON.stringify(["类目", "是否生效", "生效开关", "配置"]) || centerLayout?.sidebarHasAggregateSwitch || centerLayout?.hasTechnicalHomepageColumn || !centerLayout?.hasModel || JSON.stringify(centerLayout?.categories) !== JSON.stringify(["admin_access", "wechat_oauth", "wechat_pay", "wechat_shop", "wecom_base"])) {
    throw new Error("Config Center operational category-table contract failed");
  }

  // Config Center receives native JSON strings from the runtime-catalog API.
  // Opening this legacy-layout category and saving without a change must retain
  // both the existing AgentID and a mode from another category in the draft.
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/configDetail.html?cat=wecom_base` });
  await waitFor(cdp, "location.pathname === '/admin/configDetail.html' && document.querySelector('[data-runtime-setting=\"wecom.agent_id\"]')?.value === 'agent-preserved'", "Config Center did not render the effective AgentID");
  await evaluate(cdp, "document.querySelector('form')?.requestSubmit(); true");
  await waitFor(cdp, "location.pathname.startsWith('/admin/config/releases/') && Number.isSafeInteger(Number(location.pathname.split('/').pop()))", "Config Center did not create the preservation draft");
  const preservation = await evaluate(cdp, "fetch('/api/admin/config/runtime-releases/' + location.pathname.split('/').pop(), {credentials:'same-origin'}).then((response) => response.ok ? response.json() : null).then((body) => { const values = new Map((body?.runtime_release?.settings || []).map((item) => [item.key, item.value])); return { agentID: values.get('wecom.agent_id'), mode: values.get('automation.operations.provider_mode') }; })");
  if (preservation?.agentID !== "agent-preserved" || preservation?.mode !== "disabled") throw new Error("Config Center draft did not retain native string settings");
  console.log("runtime_config_releases_chromium: PASS");
} catch (error) {
  journeyFailed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  if (browser && browser.exitCode === null && browser.signalCode === null) {
    browser.kill("SIGTERM");
    if (!await waitForBrowserExit(browser, 3000) && browser.exitCode === null && browser.signalCode === null) {
      browser.kill("SIGKILL");
      await waitForBrowserExit(browser, 1000);
    }
  }
  // The profile belongs only to this test process. Cleanup is bounded so a
  // late Chromium child cannot hide a journey assertion or leave CI hanging.
  const removed = await removeProfile(profile);
  if (!removed && !journeyFailed) throw new Error("Chromium test profile cleanup did not complete");
}
