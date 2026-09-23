import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "../../internal/webshell/chromium_launch.mjs";

const baseURL = process.env.AICRM_AUTOMATION_CONTENT_TEST_URL;
const username = process.env.AICRM_AUTOMATION_CONTENT_TEST_USERNAME;
const password = process.env.AICRM_AUTOMATION_CONTENT_TEST_PASSWORD;
const agentID = Number(process.env.AICRM_AUTOMATION_CONTENT_TEST_AGENT_ID);
const imageID = Number(process.env.AICRM_AUTOMATION_CONTENT_TEST_IMAGE_ID);
const screenshotDirectory = process.env.AICRM_AUTOMATION_CONTENT_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !Number.isSafeInteger(agentID) || agentID < 1 || !Number.isSafeInteger(imageID) || imageID < 1 || !path.isAbsolute(screenshotDirectory || "")) {
  throw new Error("automation fixed-content Chromium journey requires HTTPS URL, credentials, IDs, and absolute screenshot directory");
}

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const browserBinary = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/") && spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0) return candidate;
      if (!candidate.includes("/") && spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
    } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (!message.id || !this.pending.has(message.id)) return;
      const pending = this.pending.get(message.id);
      this.pending.delete(message.id);
      message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) {
    return new Promise((resolve, reject) => {
      const id = ++this.nextID;
      this.pending.set(id, { resolve, reject });
      this.socket.send(JSON.stringify({ id, method, params }));
    });
  }
  async close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    if (this.socket.readyState !== WebSocket.OPEN && this.socket.readyState !== WebSocket.CONNECTING) return;
    const closed = new Promise((resolve) => this.socket.addEventListener("close", resolve, { once: true }));
    this.socket.close();
    await Promise.race([closed, delay(1_000)]);
  }
}

let browser;
let browserStderr = "";
async function debuggingAddress(profile) {
  const deadline = Date.now() + chromiumStartupTimeoutMS;
  while (Date.now() < deadline) {
    try {
      const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`;
    } catch (_) {}
    if (browser?.exitCode !== null || browser?.signalCode) break;
    await delay(50);
  }
  throw new Error(chromiumStartupDiagnostic({ profile, exitCode: browser?.exitCode, signalCode: browser?.signalCode, stderr: browserStderr }));
}
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(`page evaluation failed: ${expression.slice(0, 140)}`);
  return result.result?.value;
}
async function waitFor(cdp, expression, message) {
  for (let attempt = 0; attempt < 180; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  const state = await evaluate(cdp, "JSON.stringify({path:location.pathname,page:document.body?.dataset.page,scripts:[...document.scripts].map(script=>script.src).filter(Boolean),agentContent:Boolean(document.querySelector('#agent-content')),host:Boolean(document.querySelector('[data-v3-automation-fixed-content]')),body:document.body?.innerText.slice(-2600),dialogs:[...document.querySelectorAll('[role=dialog],dialog')].map(node=>node.textContent?.slice(0,300)),requests:window.__automationFixedContentRequests||[],bootRequests:window.__automationFixedContentBootRequests||[],bootErrors:window.__automationFixedContentBootErrors||[]})");
  throw new Error(`${message}: ${state}`);
}
async function pointerClick(cdp, selector, message) {
  const point = await evaluate(cdp, `(() => {
    const node = document.querySelector(${JSON.stringify(selector)});
    if (!(node instanceof HTMLElement)) return null;
    node.scrollIntoView({ block: 'center', inline: 'center' });
    const box = node.getBoundingClientRect();
    const top = document.elementFromPoint(box.left + box.width / 2, box.top + box.height / 2);
    return { x: box.left + box.width / 2, y: box.top + box.height / 2, visible: box.width > 2 && box.height > 2, receivesPointer: top === node || node.contains(top), disabled: node.matches(':disabled') };
  })()`);
  if (!point?.visible || !point.receivesPointer || point.disabled) throw new Error(`${message}: ${JSON.stringify(point)}`);
  await cdp.call("Input.dispatchMouseEvent", { type: "mousePressed", x: point.x, y: point.y, button: "left", clickCount: 1 });
  await cdp.call("Input.dispatchMouseEvent", { type: "mouseReleased", x: point.x, y: point.y, button: "left", clickCount: 1 });
}
async function resize(cdp, width, height = 900) {
  await cdp.call("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: height });
  // A native nested dialog can defer animation frames while a metric change is
  // being committed. The layout assertions below remain the verification; this
  // merely avoids leaving the Journey process stuck on a deferred frame.
  await evaluate(cdp, "Promise.race([new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(resolve))), new Promise((resolve) => setTimeout(resolve, 250))])");
}
async function capture(cdp, name) {
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  const target = path.join(screenshotDirectory, name);
  await fs.writeFile(target, Buffer.from(image.data, "base64"), { mode: 0o600 });
  console.log(`automation_fixed_content_chromium: SCREENSHOT ${target}`);
}
async function assertLayout(cdp, width, mode) {
  const layout = await evaluate(cdp, `(() => {
    const root = document.querySelector(${JSON.stringify(mode === "editor" ? ".aicrm-content-composer" : ".aicrm-automation-fixed-content")});
    const confirm = document.querySelector('[data-v3-composer-confirm]');
    const action = document.querySelector('[data-v3-automation-edit-fixed-content]');
    const box = node => { const rect = node?.getBoundingClientRect(); return rect ? { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: rect.width, height: rect.height } : null; };
    return { viewport: innerWidth, scrollWidth: document.documentElement.scrollWidth, root: box(root), confirm: box(confirm), action: box(action) };
  })()`);
  const control = mode === "editor" ? layout?.confirm : layout?.action;
  if (!layout || layout.viewport > width || layout.scrollWidth > layout.viewport + 1 || !layout.root || layout.root.left < -1 || layout.root.right > width + 1 || !control || control.left < -1 || control.right > width + 1) {
    throw new Error(`automation fixed-content ${mode} layout at ${width}px: ${JSON.stringify(layout)}`);
  }
}
async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
}
async function removeProfile(profile) {
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return; }
    catch (error) { if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) throw error; await delay(100); }
  }
  throw new Error("Chromium profile cleanup did not complete");
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-automation-fixed-content-"));
let cdp;
let journeyError;
try {
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
  browser.stderr.on("data", (chunk) => { browserStderr = (browserStderr + chunk).slice(-2048); });
  const tab = await (await fetch(`${await debuggingAddress(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", reject, { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Page.addScriptToEvaluateOnNewDocument", { source: `(() => {
    const nativeFetch = window.fetch.bind(window);
    window.__automationFixedContentBootRequests = [];
    window.__automationFixedContentBootErrors = [];
    addEventListener('error', event => window.__automationFixedContentBootErrors.push(String(event.error?.message || event.message || 'runtime error')));
    addEventListener('unhandledrejection', event => window.__automationFixedContentBootErrors.push(String(event.reason?.message || event.reason || 'unhandled rejection')));
    window.fetch = async (...args) => {
      const raw = args[0];
      const url = typeof raw === 'string' ? raw : raw?.url;
      const method = String(args[1]?.method || raw?.method || 'GET');
      try {
        const response = await nativeFetch(...args);
        window.__automationFixedContentBootRequests.push(method + ' ' + new URL(url, location.href).pathname + ' ' + response.status);
        return response;
      } catch (error) {
        window.__automationFixedContentBootRequests.push(method + ' ' + String(url) + ' ERROR');
        throw error;
      }
    };
  })();` });
  await resize(cdp, 1280);
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=${encodeURIComponent(`/admin/agentEdit.html?id=${agentID}`)}` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=login_csrf_token]'))", "login page");
  await evaluate(cdp, `(() => { document.querySelector('input[name=username]').value=${JSON.stringify(username)}; document.querySelector('input[name=password]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, `location.pathname==='/admin/agentEdit.html' && location.search===${JSON.stringify(`?id=${agentID}`)} && Boolean(document.querySelector('[data-agent-materials-readonly] [data-v3-automation-fixed-content]'))`, "real frozen Agent edit page and V3 content Host");
  const closure = await evaluate(cdp, `(() => ({ page:document.body.dataset.page, frozen:Boolean(document.querySelector('[data-agent-materials-readonly]')), host:Boolean([...document.scripts].find(script=>script.src.includes('automationContentHost'))), picker:Boolean([...document.scripts].find(script=>script.src.includes('standard-components/material_picker.js'))), role:document.querySelector('#agentRolePrompt')?.value, task:document.querySelector('#agentTaskPrompt')?.value }))()`);
  if (closure.page !== "agentEdit" || !closure.frozen || !closure.host || !closure.picker || closure.role !== "保留角色 Prompt" || closure.task !== "保留任务 Prompt") throw new Error(`incorrect fixed Agent runtime closure: ${JSON.stringify(closure)}`);
  await evaluate(cdp, "(() => { window.__automationFixedContentRequests=window.__automationFixedContentBootRequests; return true; })()");

  const contentTab = await evaluate(cdp, `(() => {
    const tab = [...document.querySelectorAll('nav button')].find(node => node.textContent?.replace(/\\s/g, '') === '4固定素材');
    if (!(tab instanceof HTMLButtonElement)) return false;
    tab.dataset.automationFixedContentTestTab = '1';
    return true;
  })()`);
  if (!contentTab) throw new Error('frozen Agent edit page is missing its fixed-content tab');
  await pointerClick(cdp, '[data-automation-fixed-content-test-tab="1"]', 'open frozen fixed-content tab');
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-automation-edit-fixed-content]')?.getClientRects().length)", 'visible fixed-content action');
  await pointerClick(cdp, "[data-v3-automation-edit-fixed-content]", "open fixed-content editor");
  await waitFor(cdp, "Boolean(document.querySelector('.aicrm-content-composer textarea[data-v3-composer-text]'))", "shared fixed-content editor");
  const editorContract = await evaluate(cdp, "(() => { const editor=document.querySelector('.aicrm-content-composer'); return {confirm:editor?.querySelector('[data-v3-composer-confirm]')?.textContent,note:editor?.querySelector('[data-v3-composer-confirmation-note]')?.textContent}; })()");
  if (editorContract.confirm !== '保存草稿' || editorContract.note !== '确认仅保存固定话术草稿，不会发布、启用或执行自动化。') throw new Error(`fixed-content editor used an inaccurate confirmation contract: ${JSON.stringify(editorContract)}`);
  await assertLayout(cdp, 1280, "editor");
  await capture(cdp, "automation-fixed-content-1280.png");
  const textareaSelector = ".aicrm-content-composer textarea[data-v3-composer-text]";
  const focus = await evaluate(cdp, `(() => { const textarea=document.querySelector(${JSON.stringify(textareaSelector)}); if (!(textarea instanceof HTMLTextAreaElement)) return false; textarea.dataset.chromiumIdentity='stable'; textarea.focus(); textarea.select(); return document.activeElement===textarea; })()`);
  if (!focus) throw new Error("fixed-content textarea cannot receive real browser focus");
  await cdp.call("Input.imeSetComposition", { text: "中文候选", selectionStart: 4, selectionEnd: 4, replacementStart: 0, replacementEnd: 0 });
  await waitFor(cdp, `document.querySelector(${JSON.stringify(textareaSelector)})?.dataset.chromiumIdentity==='stable' && Boolean(document.querySelector('.aicrm-content-composer'))`, "IME composition preserves the real composer textarea and modal");
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 229, nativeVirtualKeyCode: 229 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 229, nativeVirtualKeyCode: 229 });
  if (!await evaluate(cdp, "Boolean(document.querySelector('.aicrm-content-composer'))")) throw new Error("IME candidate Enter unexpectedly closed the fixed-content editor");
  await cdp.call("Input.imeSetComposition", { text: "", selectionStart: 0, selectionEnd: 0, replacementStart: 0, replacementEnd: 4 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "a", code: "KeyA", windowsVirtualKeyCode: 65, modifiers: 2 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "a", code: "KeyA", windowsVirtualKeyCode: 65, modifiers: 2 });
  await cdp.call("Input.insertText", { text: "浏览器确认的中文固定话术" });
  await waitFor(cdp, `document.querySelector(${JSON.stringify(textareaSelector)})?.value==='浏览器确认的中文固定话术'`, "real Chinese text input");
  await waitFor(cdp, "document.querySelector('[data-v3-composer-status]')?.textContent==='确认后将保存固定话术草稿。'", "fixed-content ready-to-save wording");

  await pointerClick(cdp, '[data-v3-composer-add="image"]', "open real Media picker");
  await waitFor(cdp, "Boolean(document.querySelector('[data-v3-selection-session=material] [data-v3-material-key]'))", "scoped V3 Media picker directory");
  const material = await evaluate(cdp, `(() => ({ item:[...document.querySelectorAll('[data-v3-material-key]')].map(node=>({key:node.dataset.v3MaterialKey,text:node.textContent})), picker:document.querySelector('[data-v3-selection-session=material]')?.dataset.selectionSource }))()`);
  if (!material.item.some((item) => item.key?.endsWith(`:${imageID}`)) || material.picker !== "automation-fixed-content") throw new Error(`fixed-content picker did not use its scoped Media record: ${JSON.stringify(material)}`);
  await pointerClick(cdp, `[data-v3-material-key$=":${imageID}"]`, "select authorised image");
  await pointerClick(cdp, "[data-v3-selection-session=material] [data-v3-picker-confirm]", "confirm local image selection");
  await waitFor(cdp, "!document.querySelector('[data-v3-selection-session=material]') && document.querySelector('.aicrm-content-composer')?.textContent.includes('自动化浏览器封面')", "image selection returns only to the local composer draft");

  await resize(cdp, 360, 860);
  await assertLayout(cdp, 360, "editor");
  await capture(cdp, "automation-fixed-content-360.png");
  await pointerClick(cdp, "[data-v3-composer-confirm]", "save fixed-content draft once");
  await waitFor(cdp, "!document.querySelector('.aicrm-content-composer') && document.querySelector('[data-v3-automation-fixed-content]')?.textContent.includes('草稿版本 2 / 已发布版本 1')", "saved fixed-content server readback");
  const writes = await evaluate(cdp, "window.__automationFixedContentRequests.filter(value=>value.includes('PUT /api/admin/automation-agents/')&&value.includes('/fixed-content 200'))");
  if (!Array.isArray(writes) || writes.length !== 1) throw new Error(`fixed-content confirmation did not issue exactly one accepted PUT: ${JSON.stringify(writes)}`);
  const display = await evaluate(cdp, "document.querySelector('[data-v3-automation-fixed-content]')?.textContent || ''");
  const pageText = await evaluate(cdp, "document.body.textContent || ''");
  if (!display.includes('浏览器确认的中文固定话术') || !display.includes('自动化浏览器封面') || pageText.includes('后端能力未就绪')) throw new Error(`saved fixed-content presentation is incomplete or exposed engineering copy: ${display}`);
  await resize(cdp, 420, 860);
  await evaluate(cdp, "document.querySelector('[data-v3-automation-fixed-content]')?.scrollIntoView({ block: 'start', inline: 'nearest' }); Promise.race([new Promise((resolve) => requestAnimationFrame(resolve)), new Promise((resolve) => setTimeout(resolve, 250))])");
  await assertLayout(cdp, 420, "readonly");
  await capture(cdp, "automation-fixed-content-420.png");
  await resize(cdp, 1440);
  await assertLayout(cdp, 1440, "readonly");
  await capture(cdp, "automation-fixed-content-1440.png");
  if (process.env.AICRM_AUTOMATION_LIFECYCLE_TEST === "true") {
    await cdp.call("Page.navigate", { url: `${baseURL}/admin/automation-agents` });
    await waitFor(cdp, "document.querySelector('[data-agent-action=pause]')?.textContent==='启用'", "paused agent exposes enable action");
    await pointerClick(cdp, '[data-agent-action=pause]', 'enable paused automation');
    await waitFor(cdp, "document.querySelector('#fb-ok')?.textContent==='启用'", "enable confirmation");
    await pointerClick(cdp, '#fb-ok', 'confirm enable');
    await waitFor(cdp, "document.querySelector('#fb-ok')?.textContent==='发布并启用'", "unpublished draft confirmation");
    await pointerClick(cdp, '#fb-ok', 'publish and enable');
    await waitFor(cdp, "document.querySelector('[data-agent-action=pause]')?.textContent==='暂停' && document.body.innerText.includes('启用中')", "active state readback");
    const active = await evaluate(cdp, `fetch('/api/admin/automation-agents/${agentID}').then(r=>r.json()).then(d=>d.agent)`);
    if (active.status !== 'active' || active.execution_enabled !== true || active.published_version !== 2) throw new Error('enable did not persist');
    await capture(cdp, 'automation-lifecycle-active.png');
    await pointerClick(cdp, '[data-agent-action=pause]', 'pause active automation');
    await waitFor(cdp, "document.querySelector('#fb-ok')?.textContent==='暂停'", "pause confirmation");
    await pointerClick(cdp, '#fb-ok', 'confirm pause');
    await waitFor(cdp, "document.querySelector('[data-agent-action=pause]')?.textContent==='启用' && document.body.innerText.includes('已暂停')", "paused state readback");
    const paused = await evaluate(cdp, `fetch('/api/admin/automation-agents/${agentID}').then(r=>r.json()).then(d=>d.agent)`);
    if (paused.status !== 'paused' || paused.execution_enabled !== false) throw new Error('pause did not persist');
  }
  console.log(`automation_fixed_content_chromium: PASS screenshots=${screenshotDirectory}`);
} catch (error) {
  journeyError = error instanceof Error ? error : new Error(String(error));
} finally {
  if (cdp) await cdp.close();
  await stopBrowser(browser);
  try { await removeProfile(profile); } catch (cleanupError) { if (!journeyError) journeyError = cleanupError; }
}
if (journeyError) throw journeyError;
