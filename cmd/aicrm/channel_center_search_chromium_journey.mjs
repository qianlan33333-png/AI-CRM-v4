import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "../../internal/webshell/chromium_launch.mjs";

const baseURL = process.env.AICRM_CHANNEL_CENTER_TEST_URL;
const username = process.env.AICRM_CHANNEL_CENTER_TEST_USERNAME;
const password = process.env.AICRM_CHANNEL_CENTER_TEST_PASSWORD;
const screenshotDirectory = process.env.AICRM_CHANNEL_CENTER_SCREENSHOT_DIR;
const expectEmptyDirectory = process.env.AICRM_CHANNEL_CENTER_EXPECT_EMPTY === "1";
if (!/^https:\/\//.test(baseURL || "") || !username || !password) throw new Error("Channel Center Chromium journey requires HTTPS URL and credentials");
if (!path.isAbsolute(screenshotDirectory || "")) throw new Error("Channel Center Chromium journey requires an absolute screenshot directory");
const sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const asError = (error) => error instanceof Error ? error : new Error(String(error));
function browserBinary() {
  for (const item of [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN, process.platform === "darwin" ? "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" : "", "google-chrome", "google-chrome-stable", "chromium", "chromium-browser"].filter(Boolean)) {
    if (item.includes("/")) { try { if (spawnSync(item, ["--version"], { stdio: "ignore" }).status === 0) return item; } catch {} }
    else if (spawnSync("which", [item], { stdio: "ignore" }).status === 0) return item;
  }
  throw new Error("Chromium binary is unavailable");
}
class CDP {
  constructor(socket) { this.socket = socket; this.id = 0; this.pending = new Map(); socket.addEventListener("message", (event) => { const message = JSON.parse(String(event.data)); const pending = this.pending.get(message.id); if (!pending) return; this.pending.delete(message.id); message.error ? pending.reject(new Error(`CDP ${message.error.code}: ${message.error.message}`)) : pending.resolve(message.result || {}); }); }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.id; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  close() { for (const pending of this.pending.values()) pending.reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}
async function devtools(profile) {
  const deadline = Date.now() + chromiumStartupTimeoutMS;
  while (Date.now() < deadline) {
    try { const port = String(await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0]; if (/^\d+$/.test(port)) return `http://127.0.0.1:${port}`; } catch {}
    if (browser?.exitCode !== null) break;
    await sleep(50);
  }
  throw new Error(chromiumStartupDiagnostic({ profile, exitCode: browser?.exitCode, signalCode: browser?.signalCode, stderr }));
}
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error(`page evaluation failed: ${expression.slice(0, 120)}`);
  return result.result?.value;
}
async function waitFor(cdp, expression, label) {
  for (let retry = 0; retry < 180; retry += 1) {
    if (await evaluate(cdp, expression)) return;
    await sleep(50);
  }
  throw new Error(`${label}: ${await evaluate(cdp, "JSON.stringify({path:location.pathname,text:document.body.innerText.slice(-1800),probe:window.__channelSearchProbe})")}`);
}
async function captureChannelCenter(cdp, width, state) {
  await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
  const layout = await evaluate(cdp, "(()=>{const root=document.querySelector('#stage');const input=document.querySelector('input[aria-label=\\\"搜索渠道名称\\\"]');const topbar=document.querySelector('.admin-topbar');return {viewport:innerWidth,scrollWidth:document.documentElement.scrollWidth,root:root?.getBoundingClientRect().width||0,input:input?.getBoundingClientRect().width||0,topbar:topbar?.getBoundingClientRect().width||0}})()");
  if (!layout || layout.viewport !== width || layout.scrollWidth > width + 1 || layout.root > width + 1 || layout.input <= 0 || layout.topbar <= 0) throw new Error(`Channel Center ${width}px layout=${JSON.stringify(layout)}`);
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  await fs.writeFile(path.join(screenshotDirectory, `channel-center-${state}-${width}.png`), Buffer.from(image.data, "base64"), { mode: 0o600 });
}
const waitForExit = (child, ms) => new Promise((resolve) => { if (child.exitCode !== null || child.signalCode !== null) return resolve(true); const timer = setTimeout(() => resolve(true), ms); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return null;
  try { child.kill("SIGTERM"); if (await waitForExit(child, 3000)) return null; child.kill("SIGKILL"); return await waitForExit(child, 3000) ? null : new Error("Chromium process did not exit"); } catch (error) { return asError(error); }
}
async function removeProfile(profile) {
  let lastError;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try { await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 }); return null; } catch (error) { lastError = asError(error); if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return lastError; await sleep(100); }
  }
  return lastError || new Error("Chromium profile cleanup failed");
}
const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-channel-search-chromium-"));
let browser; let stderr = ""; let cdp; let journeyError;
class DevToolsUnavailable extends Error {}
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "pipe"] });
  browser.stderr.on("data", (chunk) => { stderr = (stderr + chunk).slice(-2048); });
  let address;
  try { address = await devtools(profile); } catch (error) { if (process.platform === "darwin") throw new DevToolsUnavailable(); throw error; }
  const tab = await (await fetch(`${address}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket);
  await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Input.setIgnoreInputEvents", { ignore: false });
  await cdp.call("Page.addScriptToEvaluateOnNewDocument", { source: `(() => { window.__channelSearchProbe={events:[],fetches:[],mutations:0}; const originalFetch=window.fetch.bind(window); window.fetch=(input,init)=>{const value=typeof input==='string'?input:input&&input.url; window.__channelSearchProbe.fetches.push(String(value||'')); return originalFetch(input,init);}; document.addEventListener('input',(event)=>{const target=event.target; if(target instanceof HTMLInputElement&&target.getAttribute('aria-label')==='搜索渠道名称') window.__channelSearchProbe.events.push({trusted:event.isTrusted,value:target.value,selectionStart:target.selectionStart,selectionEnd:target.selectionEnd});},true); new MutationObserver((entries)=>{window.__channelSearchProbe.mutations+=entries.length;}).observe(document,{childList:true,subtree:true,characterData:true}); })();` });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=${encodeURIComponent("/admin/channels")}` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/channels'", "login did not reach Channel Center");
  if (expectEmptyDirectory) {
    await waitFor(cdp, "Boolean(document.querySelector('input[aria-label=\"搜索渠道名称\"]')) && Boolean(document.querySelector('[data-surface-table-read-state=\"empty\"]'))", "Channel Center did not present the actual empty directory state");
    const empty = await evaluate(cdp, "(()=>{const input=document.querySelector('input[aria-label=\\\"搜索渠道名称\\\"]');const state=document.querySelector('[data-surface-table-read-state=\\\"empty\\\"]');return {input:Boolean(input),message:state?.textContent||'',rows:[...document.querySelectorAll('#stage table tbody tr')].length}})()");
    if (!empty?.input || !empty.message.includes('当前已加载页暂无渠道') || empty.rows !== 1) throw new Error(`actual empty Channel Center state=${JSON.stringify(empty)}`);
    await captureChannelCenter(cdp, 1280, "empty");
    await captureChannelCenter(cdp, 1440, "empty");
    console.log("channel_center_search_chromium: PASS screenshots=" + screenshotDirectory);
  } else {
  await waitFor(cdp, "Boolean(document.querySelector('input[aria-label=\"搜索渠道名称\"]')) && document.body.textContent.includes('中文渠道 输入法验证') && document.body.textContent.includes('English control channel')", "Channel Center Host did not render its real list");
  await captureChannelCenter(cdp, 1280, "loaded");
  await captureChannelCenter(cdp, 1440, "loaded");
  await evaluate(cdp, `(() => { const input=document.querySelector('input[aria-label="搜索渠道名称"]'); input.focus(); input.dataset.channelSearchProbe='initial'; window.__channelSearchProbe.events=[]; window.__channelSearchProbe.fetches=[]; window.__channelSearchProbe.mutations=0; return true; })()`);

  await cdp.call("Input.imeSetComposition", { text: "中文渠道", selectionStart: 4, selectionEnd: 4, replacementStart: 0, replacementEnd: 0 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  // Chrome's headless CDP keeps the IME composition active after the candidate
  // key event. Commit that candidate through the same native IME text path,
  // then test the following ordinary Enter separately.
  await cdp.call("Input.insertText", { text: "中文渠道" });
  await sleep(80);
  const candidate = await evaluate(cdp, `(() => { const input=document.querySelector('input[aria-label="搜索渠道名称"]'); const probe=window.__channelSearchProbe; return {same:input?.dataset.channelSearchProbe==='initial',value:input?.value,containsChinese:document.body.textContent.includes('中文渠道 输入法验证'),containsEnglish:document.body.textContent.includes('English control channel'),syntheticInputs:probe.events.filter((event)=>!event.trusted).length,fetches:probe.fetches.length,mutations:probe.mutations}; })()`);
  if (!candidate?.same || candidate.value !== "中文渠道" || !candidate.containsChinese || !candidate.containsEnglish || candidate.syntheticInputs !== 0 || candidate.fetches !== 0 || candidate.mutations !== 0) throw new Error(`IME candidate Enter submitted or redrew Channel Center: ${JSON.stringify(candidate)}`);

  await sleep(30);
  await evaluate(cdp, `(() => { const input=document.querySelector('input[aria-label="搜索渠道名称"]'); input.setSelectionRange(1,2); return true; })()`);
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await waitFor(cdp, "document.body.textContent.includes('中文渠道 输入法验证')&&!document.body.textContent.includes('English control channel')", "ordinary Enter did not commit Channel Center filter");
  const committed = await evaluate(cdp, `(() => { const input=document.querySelector('input[aria-label="搜索渠道名称"]'); const probe=window.__channelSearchProbe; return {focused:document.activeElement===input,selectionStart:input?.selectionStart,selectionEnd:input?.selectionEnd,syntheticInputs:probe.events.filter((event)=>!event.trusted).length,fetches:probe.fetches.length,mutations:probe.mutations}; })()`);
  if (!committed?.focused || committed.selectionStart !== 1 || committed.selectionEnd !== 2 || committed.syntheticInputs !== 1 || committed.fetches !== 0 || committed.mutations < 1) throw new Error(`ordinary Enter did not perform exactly one local filter update with focus preserved: ${JSON.stringify(committed)}`);

  await cdp.call("Input.insertText", { text: "" });
  await evaluate(cdp, "(() => { const input=document.querySelector('input[aria-label=\"搜索渠道名称\"]'); if (!input) return false; input.value=''; return true; })()");
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await waitFor(cdp, "document.body.textContent.includes('中文渠道 输入法验证')&&document.body.textContent.includes('English control channel')", "empty Enter did not restore the loaded Channel Center page");
  await evaluate(cdp, "(()=>{const input=document.querySelector('input[aria-label=\\\"搜索渠道名称\\\"]');input.focus();input.value='';return true})()");
  await cdp.call("Input.insertText", { text: "不存在的渠道名称" });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Enter", code: "Enter", windowsVirtualKeyCode: 13, nativeVirtualKeyCode: 13 });
  await waitFor(cdp, "Boolean(document.querySelector('[data-surface-table-read-state=\"no-match\"]'))", "ordinary Enter did not present the actual no-match state");
  const noMatch = await evaluate(cdp, "(()=>{const input=document.querySelector('input[aria-label=\\\"搜索渠道名称\\\"]');const state=document.querySelector('[data-surface-table-read-state=\\\"no-match\\\"]');return {focused:document.activeElement===input,value:input?.value,message:state?.textContent||'',rows:[...document.querySelectorAll('#stage table tbody tr')].length}})()");
  if (!noMatch?.focused || noMatch.value !== "不存在的渠道名称" || !noMatch.message.includes('当前已加载页未找到') || noMatch.rows !== 1) throw new Error(`actual no-match Channel Center state=${JSON.stringify(noMatch)}`);
  await captureChannelCenter(cdp, 1280, "no-match");
  await captureChannelCenter(cdp, 1440, "no-match");
  console.log("channel_center_search_chromium: PASS screenshots=" + screenshotDirectory);
  }
} catch (error) {
  if (error instanceof DevToolsUnavailable) console.log("channel_center_search_chromium: SKIP_DEVTOOLS");
  else journeyError = asError(error);
} finally {
  const cleanup = [];
  if (cdp) { try { cdp.close(); } catch (error) { cleanup.push(asError(error)); } }
  const browserError = await stopBrowser(browser); if (browserError) cleanup.push(browserError);
  const profileError = await removeProfile(profile); if (profileError) cleanup.push(profileError);
  if (journeyError && cleanup.length) throw new AggregateError([journeyError, ...cleanup], "Channel Center Chromium journey and cleanup failed");
  if (journeyError) throw journeyError;
  if (cleanup.length) throw new AggregateError(cleanup, "Channel Center Chromium cleanup failed");
}
