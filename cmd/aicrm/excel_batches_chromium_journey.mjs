import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import {
  chromiumStartupDiagnostic,
  chromiumStartupTimeoutMS,
} from "../../internal/webshell/chromium_launch.mjs";

const baseURL = process.env.AICRM_EXCEL_TEST_URL;
const username = process.env.AICRM_EXCEL_TEST_USERNAME;
const password = process.env.AICRM_EXCEL_TEST_PASSWORD;
const screenshotDir = process.env.AICRM_EXCEL_BROWSER_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password)
  throw new Error(
    "Excel batch Chromium journey requires HTTPS URL and test credentials",
  );
const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const candidates = () => {
  const explicit = [
    process.env.AICRM_CHROMIUM_BINARY,
    process.env.CHROME_BIN,
  ].filter(Boolean);
  if (process.platform === "darwin")
    explicit.push(
      "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
    );
  return [
    ...explicit,
    "google-chrome",
    "google-chrome-stable",
    "chromium",
    "chromium-browser",
  ];
};
function browserBinary() {
  for (const candidate of candidates()) {
    if (candidate.includes("/")) {
      try {
        if (
          spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0
        )
          return candidate;
      } catch (_) {}
    } else if (
      spawnSync("which", [candidate], { stdio: "ignore" }).status === 0
    )
      return candidate;
  }
  throw new Error("Chromium binary is unavailable");
}
class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        message.error
          ? pending.reject(new Error(`CDP ${message.error.code || "error"}`))
          : pending.resolve(message.result || {});
        return;
      }
      for (const listener of this.events.get(message.method) || [])
        listener(message.params || {});
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
    const list = this.events.get(method) || [];
    list.push(listener);
    this.events.set(method, list);
  }
  close() {
    for (const { reject } of this.pending.values())
      reject(new Error("CDP browser closed"));
    this.pending.clear();
    this.socket.close();
  }
}
async function port(profile) {
  const deadline = Date.now() + chromiumStartupTimeoutMS;
  while (Date.now() < deadline) {
    try {
      const value = String(
        await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8"),
      ).split("\n")[0];
      if (/^\d+$/.test(value)) return `http://127.0.0.1:${value}`;
    } catch {}
    if (browser?.exitCode !== null || browser?.signalCode) break;
    await delay(50);
  }
  throw new Error(
    chromiumStartupDiagnostic({
      profile,
      exitCode: browser?.exitCode,
      signalCode: browser?.signalCode,
      stderr: browserStderr,
    }),
  );
}
async function evaluate(cdp, expression) {
  const result = await cdp.call("Runtime.evaluate", {
    expression,
    returnByValue: true,
    awaitPromise: true,
  });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
}
async function waitFor(cdp, expression, message) {
  for (let i = 0; i < 180; i += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(
    message +
      " " +
      JSON.stringify(
        await evaluate(
          cdp,
          `({path:location.pathname,status:document.querySelector(".operation-excel-workspace [role=status]")?.textContent,root:document.querySelector(".operation-excel-workspace")?.textContent?.slice(0,1000)})`,
        ),
      ),
  );
}
async function waitForNetwork(check, message) {
  for (let i = 0; i < 180; i += 1) {
    if (check()) return;
    await delay(50);
  }
  throw new Error(message);
}
async function pointerClick(cdp, selector, message) {
  const point = await evaluate(
    cdp,
    `(() => {
      const node = document.querySelector(${JSON.stringify(selector)});
      if (!node) return null;
      const rect = node.getBoundingClientRect();
      const top = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, visible: rect.width > 1 && rect.height > 1, receivesPointer: top === node || node.contains(top) };
    })()`,
  );
  if (!point?.visible || !point.receivesPointer)
    throw new Error(`${message}: ${JSON.stringify(point)}`);
  await cdp.call("Input.dispatchMouseEvent", {
    type: "mousePressed", x: point.x, y: point.y, button: "left", clickCount: 1,
  });
  await cdp.call("Input.dispatchMouseEvent", {
    type: "mouseReleased", x: point.x, y: point.y, button: "left", clickCount: 1,
  });
}
async function pointerClickText(cdp, scopeSelector, text, message) {
  const marked = await evaluate(
    cdp,
    `(() => {
      document.querySelectorAll('[data-aicrm-chromium-pointer]').forEach((node) => node.removeAttribute('data-aicrm-chromium-pointer'));
      const scope = document.querySelector(${JSON.stringify(scopeSelector)});
      const target = [...(scope?.querySelectorAll('button') || [])].find((node) => node.textContent?.trim() === ${JSON.stringify(text)});
      if (!target) return false;
      target.setAttribute('data-aicrm-chromium-pointer', '1');
      target.scrollIntoView({ block: 'center', inline: 'center' });
      return true;
    })()`,
  );
  if (!marked) throw new Error(`${message}: control missing`);
  await delay(80);
  await pointerClick(cdp, '[data-aicrm-chromium-pointer="1"]', message);
}
async function keyboardText(cdp, selector, value, message) {
  await pointerClick(cdp, selector, message);
  const focused = await evaluate(
    cdp,
    `(() => { const node = document.querySelector(${JSON.stringify(selector)}); if (!(node instanceof HTMLTextAreaElement || node instanceof HTMLInputElement)) return false; node.focus(); node.select(); return document.activeElement === node && node.selectionStart ===0 && node.selectionEnd === node.value.length; })()`,
  );
  if (!focused) throw new Error(`${message}: focused editable target is unavailable`);
  // CDP inserts through Chromium's native input pipeline after the actual
  // pointer target has proven it is not inert behind a parent dialog.
  await cdp.call("Input.insertText", { text: value });
  await waitFor(cdp, `document.querySelector(${JSON.stringify(selector)})?.value===${JSON.stringify(value)}`, `${message}: keyboard text did not reach the target`);
}
async function pressEscape(cdp) {
  await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
  await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Escape", code: "Escape", windowsVirtualKeyCode: 27, nativeVirtualKeyCode: 27 });
}
async function setViewport(cdp, width, height = 900) {
  await cdp.call("Emulation.setDeviceMetricsOverride", {
    width,
    height,
    deviceScaleFactor: 1,
    mobile: false,
    screenWidth: width,
    screenHeight: height,
  });
  await delay(80);
}
async function captureExcelComposer(cdp, width) {
  if (!screenshotDir) return;
  await setViewport(cdp, width, width <= 420 ? 860 : 980);
  const layout = await evaluate(
    cdp,
    `(() => {
      const mask = document.querySelector('dialog[open][data-v3-content-composer][data-v3-content-top-layer="1"]');
      const composer = mask?.querySelector('.aicrm-content-composer');
      const confirm = mask?.querySelector('[data-v3-composer-confirm]');
      const thumbnail = mask?.querySelector('[data-content-presentation-supplement] img');
      const box = (node) => {
        if (!node) return null;
        const rect = node.getBoundingClientRect();
        return { left: rect.left, right: rect.right, top: rect.top, bottom: rect.bottom, width: rect.width, height: rect.height };
      };
      return {
        viewport: document.documentElement.clientWidth,
        documentWidth: Math.max(document.documentElement.scrollWidth, document.body.scrollWidth),
        mask: box(mask), composer: box(composer), confirm: box(confirm),
        thumbnail: box(thumbnail), thumbnailObjectFit: thumbnail ? getComputedStyle(thumbnail).objectFit : '',
        confirmVisible: Boolean(confirm && getComputedStyle(confirm).display !== 'none' && !confirm.disabled),
      };
    })()`,
  );
  if (!layout || layout.viewport > width || layout.viewport < width - 16 || layout.documentWidth > layout.viewport + 1 || !layout.mask || !layout.composer || !layout.confirmVisible || !layout.thumbnail || Math.abs(layout.thumbnail.width - 48) > 1 || Math.abs(layout.thumbnail.height - 48) > 1 || layout.thumbnailObjectFit !== 'cover' || layout.composer.left < -1 || layout.composer.right > width + 1 || layout.confirm.left < -1 || layout.confirm.right > width + 1)
    throw new Error(`Excel Composer viewport=${width} layout=${JSON.stringify(layout)}`);
  await fs.mkdir(screenshotDir, { recursive: true });
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  const target = path.join(screenshotDir, `operation-excel-composer-${width}.png`);
  await fs.writeFile(target, Buffer.from(image.data, "base64"));
  console.log(`excel_batches_chromium: SCREENSHOT ${target}`);
}
async function captureExcelHistory(cdp, filename, readonly = false) {
  if (!screenshotDir) return;
  await setViewport(cdp, 1280);
  const layout = await evaluate(
    cdp,
    `(() => {
      const dialog = document.querySelector('dialog[open]');
      const history = dialog?.querySelector('[data-excel-history-page]');
      const scroll = history?.querySelector('.xeb-scroll');
      const rect = (node) => { if (!node) return null; const value = node.getBoundingClientRect(); return { left: value.left, right: value.right, top: value.top, bottom: value.bottom }; };
      if (${readonly ? "true" : "false"}) return { dialog: rect(document.querySelector('dialog[open][data-v3-content-readonly][data-v3-content-top-layer=\"1\"]')), history: false };
      return { dialog: rect(dialog), history: Boolean(history?.textContent?.includes('表格可横向滚动查看完整状态和版本追溯。')), scroll: scroll ? { clientWidth: scroll.clientWidth, scrollWidth: scroll.scrollWidth, overflowX: getComputedStyle(scroll).overflowX, label: scroll.getAttribute('aria-label'), tabIndex: scroll.tabIndex } : null };
    })()`,
  );
  const valid = readonly
    ? Boolean(layout?.dialog)
    : Boolean(layout?.dialog && layout?.history && layout?.scroll && layout.scroll.scrollWidth > layout.scroll.clientWidth && /auto|scroll/.test(layout.scroll.overflowX) && layout.scroll.label === '历史内容行字段；可横向滚动查看完整状态和版本追溯' && layout.scroll.tabIndex === 0);
  if (!valid) throw new Error(`Excel history ${readonly ? 'readonly' : 'table'} evidence is incomplete: ${JSON.stringify(layout)}`);
  await fs.mkdir(screenshotDir, { recursive: true });
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
  const target = path.join(screenshotDir, filename);
  await fs.writeFile(target, Buffer.from(image.data, "base64"));
  console.log(`excel_batches_chromium: SCREENSHOT ${target}`);
}
async function assertExcelListViewport(cdp, width) {
  await setViewport(cdp, width);
  const layout = await evaluate(
    cdp,
    `(() => {
      const viewport = document.documentElement.clientWidth;
      const root = document.querySelector(".operation-excel-workspace");
      const rootRect = root?.getBoundingClientRect();
      const visible = node => { const style = getComputedStyle(node), rect = node.getBoundingClientRect(); return style.display !== "none" && style.visibility !== "hidden" && rect.width > 1 && rect.height > 1; };
      const escaping = Array.from(document.querySelectorAll(".operation-excel-workspace .admin-toolbar, .operation-excel-workspace .admin-field, .operation-excel-workspace .admin-table"))
        .filter(visible)
        .map(node => node.getBoundingClientRect())
        .some(rect => rect.left < -1 || rect.right > viewport + 1);
      const button = Array.from(document.querySelectorAll(".operation-excel-workspace .admin-button")).find(node => String(node.textContent).trim() === "查看详情");
      const style = button ? getComputedStyle(button) : null;
      return { viewport, documentWidth: Math.max(document.documentElement.scrollWidth, document.body?.scrollWidth || 0), root: rootRect ? { left: rootRect.left, right: rootRect.right } : null, escaping, button: style ? { background: style.backgroundColor, fontSize: style.fontSize, borderRadius: style.borderRadius, height: style.height } : null };
    })()`,
  );
  const standardButton = layout?.button?.background === "rgba(0, 0, 0, 0)" && layout?.button?.fontSize === "14px" && layout?.button?.borderRadius === "6px" && layout?.button?.height === "36px";
  if (!layout || layout.viewport > width || layout.documentWidth > layout.viewport + 1 || !layout.root || layout.root.left < -1 || layout.root.right > layout.viewport + 1 || layout.escaping || !standardButton)
    throw new Error(`Excel list viewport=${width} is not responsive or does not load standard controls: ${JSON.stringify(layout)}`);
}
async function assertExcelViewport(cdp, width, label) {
  await setViewport(cdp, width);
  const layout = await evaluate(
    cdp,
    `(() => {
      const viewport = document.documentElement.clientWidth;
      const visible = node => {
        const style = getComputedStyle(node), rect = node.getBoundingClientRect();
        return style.display !== "none" && style.visibility !== "hidden" && rect.width > 1 && rect.height > 1;
      };
      const root = document.querySelector(".operation-excel-workspace");
      const rootRect = root?.getBoundingClientRect();
      const escapingControls = Array.from(document.querySelectorAll(".operation-excel-workspace .admin-toolbar, .operation-excel-workspace .admin-field"))
        .filter(node => visible(node) && !node.closest(".xeb-scroll"))
        .map(node => ({ node, rect: node.getBoundingClientRect() }))
        .filter(({ rect }) => rect.left < -1 || rect.right > viewport + 1)
        .map(({ node, rect }) => ({ className: node.className, text: String(node.textContent || "").trim().slice(0, 80), left: rect.left, right: rect.right }));
      const scrolls = Array.from(document.querySelectorAll(".operation-excel-workspace .xeb-scroll"))
        .filter(visible)
        .map(node => ({ clientWidth: node.clientWidth, scrollWidth: node.scrollWidth }));
      const stats = document.querySelector(".operation-excel-workspace .xeb-grid");
      const standardPrimary = Array.from(document.querySelectorAll(".operation-excel-workspace .admin-button--primary"))[0];
      const history = document.querySelector('select[aria-label="历史批次"]');
      const coverInput = document.querySelector('input[aria-label="统一封面图片"]');
      const alert = document.querySelector(".operation-excel-workspace .admin-alert");
      const style = node => node ? (() => { const computed = getComputedStyle(node); return { background: computed.backgroundColor, color: computed.color, fontSize: computed.fontSize, borderRadius: computed.borderRadius, minHeight: computed.minHeight, height: computed.height }; })() : null;
      return {
        viewport,
        documentWidth: Math.max(document.documentElement.scrollWidth, document.body?.scrollWidth || 0),
        root: rootRect ? { left: rootRect.left, right: rootRect.right } : null,
        escapingControls,
        scrolls,
        statColumns: stats ? getComputedStyle(stats).gridTemplateColumns.trim().split(/\\s+/).filter(Boolean).length : 0,
        primary: style(standardPrimary),
        history: style(history),
        coverInput: style(coverInput),
        alert: style(alert),
      };
    })()`,
  );
  const hasLocalTableScroll = layout?.scrolls?.some(
    (item) => item.clientWidth > 0 && item.scrollWidth > item.clientWidth,
  );
  const standardControls = layout?.primary?.background === "rgb(51, 112, 255)" &&
    layout?.primary?.color === "rgb(255, 255, 255)" && layout?.primary?.fontSize === "14px" &&
    layout?.primary?.borderRadius === "6px" && layout?.primary?.height === "36px" &&
    layout?.history?.fontSize === "14px" && layout?.history?.height === "36px" &&
    layout?.history?.borderRadius === "6px" && layout?.alert?.minHeight === "42px" &&
    layout?.coverInput?.fontSize === "13px" && layout?.coverInput?.height === "34px" &&
    layout?.coverInput?.borderRadius === "6px" && layout?.alert?.fontSize === "13px" &&
    layout?.alert?.borderRadius === "8px";
  if (!layout || layout.viewport > width || layout.documentWidth > layout.viewport + 1 || !layout.root || layout.root.left < -1 || layout.root.right > layout.viewport + 1 || layout.escapingControls.length || layout.statColumns !== 2 || !hasLocalTableScroll || !standardControls)
    throw new Error(`Excel ${label} viewport=${width} is not responsive or does not load standard controls: ${JSON.stringify(layout)}`);
}
async function browserExit(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  await Promise.race([
    new Promise((resolve) => child.once("exit", resolve)),
    delay(3000),
  ]);
  if (child.exitCode === null && child.signalCode === null) {
    child.kill("SIGKILL");
    await Promise.race([
      new Promise((resolve) => child.once("exit", resolve)),
      delay(1000),
    ]);
  }
}
async function removeProfile(profile) {
  for (let i = 0; i < 40; i += 1) {
    try {
      await fs.rm(profile, { recursive: true, force: true, maxRetries: 0 });
      return true;
    } catch (error) {
      if (!["ENOTEMPTY", "EBUSY", "EPERM"].includes(error?.code)) return false;
      await delay(100);
    }
  }
  return false;
}
const profile = await fs.mkdtemp(
  path.join(os.tmpdir(), "aicrm-excel-batches-chromium-"),
);
let browser;
let cdp;
let failed = false;
let browserStderr = "";
try {
  browser = spawn(
    browserBinary(),
    [
      "--headless=new",
      "--no-sandbox",
      "--remote-debugging-port=0",
      `--user-data-dir=${profile}`,
      "--no-first-run",
      "--no-default-browser-check",
      "--disable-background-networking",
      "--disable-component-update",
      "--disable-sync",
      "--ignore-certificate-errors",
      "--allow-insecure-localhost",
      "about:blank",
    ],
    { stdio: ["ignore", "ignore", "pipe"] },
  );
  browser.stderr.on("data", (chunk) => {
    browserStderr = (browserStderr + chunk.toString()).slice(-2048);
  });
  const created = await (
    await fetch(`${await port(profile)}/json/new?about:blank`, {
      method: "PUT",
    })
  ).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener(
      "error",
      () => reject(new Error("Chromium page connection failed")),
      { once: true },
    );
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  await setViewport(cdp, 1280);

  const errors = [];
  const requests = { content: 0, preview: 0, approve: 0 };
  let currentBatchID = 0;
  cdp.on("Runtime.exceptionThrown", () => errors.push("page_exception"));
  cdp.on("Network.requestWillBeSent", (params) => {
    try {
      const request = params.request || {};
      const pathname = new URL(String(request.url || "")).pathname;
      const method = String(request.method || "GET").toUpperCase();
      if (currentBatchID > 0 && method === "GET" && pathname === `/api/admin/operation-batches/${currentBatchID}`) requests.content += 1;
      if (currentBatchID > 0 && method === "POST" && pathname === `/api/admin/operation-batches/${currentBatchID}/preview-approval`) requests.preview += 1;
      if (currentBatchID > 0 && method === "POST" && pathname === `/api/admin/operation-batches/${currentBatchID}/approve`) requests.approve += 1;
    } catch {}
  });
  await cdp.call("Page.navigate", {
    url: `${baseURL}/login?next=%2Fadmin%2Foperation-cycles`,
  });
  await waitFor(
    cdp,
    `Boolean(document.querySelector('form[action="/login"] input[name="login_csrf_token"]'))`,
    "login did not render",
  );
  await evaluate(
    cdp,
    `(()=>{document.querySelector('input[name="username"]').value=${JSON.stringify(username)};document.querySelector('input[name="password"]').value=${JSON.stringify(password)};document.querySelector('form[action="/login"]').requestSubmit();return true})()`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('.operation-excel-workspace button')&&[...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情'))`,
    "operation plan list missing",
  );
  await assertExcelListViewport(cdp, 780);
  await assertExcelListViewport(cdp, 390);
  await setViewport(cdp, 1280);
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.operation-excel-workspace button')].find(b=>b.textContent==='查看详情').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('.xeb-detail-main')&&[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次'))`,
    "operation detail did not open",
  );
  await assertExcelViewport(cdp, 780, "detail");
  await assertExcelViewport(cdp, 390, "detail");
  await setViewport(cdp, 1280);
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='新建发送批次').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open] input[type=file]'))`,
    "new batch dialog missing",
  );
  await evaluate(
    cdp,
    `(()=>{const input=document.querySelector('dialog[open] input[type=file]');const transfer=new DataTransfer();transfer.items.add(new File(['fixture'],'batch.xlsx'));input.files=transfer.files;input.dispatchEvent(new Event('change'));const fresh=document.querySelector('dialog[open] input[type=checkbox]');fresh.checked=true;fresh.dispatchEvent(new Event('change'));[...document.querySelectorAll('dialog[open] button')].find(b=>b.textContent==='上传并开始审核').click();return true})()`,
  );
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open]')&&document.querySelector('.xeb-detail-main')?.textContent.includes('当前批次 #2')&&document.querySelector('.xeb-detail-main')?.textContent.includes('第一条待审核话术')`,
    "controlled Excel upload did not open the selected batch",
  );
  currentBatchID = await evaluate(
    cdp,
    `(()=>{const match=document.querySelector('.xeb-detail-main')?.textContent.match(/当前批次 #(\\d+)/);return match?Number(match[1]):0})()`,
  );
  if (!Number.isSafeInteger(currentBatchID) || currentBatchID < 1)
    throw new Error("current batch id was not a positive integer");
  if (
    !(await evaluate(
      cdp,
      `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').disabled`,
    ))
  )
    throw new Error("cover required guard missing");
  await evaluate(
    cdp,
    `(()=>{const input=document.querySelector('input[aria-label="统一封面图片"]');const dt=new DataTransfer();dt.items.add(new File([Uint8Array.from(atob('iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAYAAABytg0kAAAAFElEQVR4nGL6z8DwnwEZAAIAAP//HxcCAa7PZcoAAAAASUVORK5CYII='),c=>c.charCodeAt(0))],'cover.png',{type:'image/png'}));input.files=dt.files;[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='上传统一封面').click();return true})()`,
  );
  await waitFor(
    cdp,
    `document.querySelector('[data-excel-feedback][role=status]')?.textContent.includes('统一封面已更新')`,
    "cover upload failed",
  );
  await pointerClickText(
    cdp,
    ".xeb-detail-main",
    "修改",
    "Excel row edit entry did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open] textarea'))`,
    "edit dialog missing",
  );
  await pointerClickText(
    cdp,
    "dialog[open]",
    "编辑话术与预览",
    "Excel shared Composer did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open][data-v3-content-composer][data-v3-content-top-layer="1"] textarea[data-v3-composer-text]'))`,
    "Excel shared Composer was not promoted to the native top layer",
  );
  await keyboardText(
    cdp,
    'dialog[open][data-v3-content-composer] textarea[data-v3-composer-text]',
    "人工修改后的话术",
    "Excel shared Composer textarea did not receive real keyboard input",
  );
  await evaluate(
    cdp,
    `(() => { const input = document.querySelector('dialog[open][data-v3-content-composer] textarea[data-v3-composer-text]'); input.focus(); input.dispatchEvent(new CompositionEvent('compositionstart', { bubbles: true, data: '中' })); return document.activeElement === input; })()`,
  );
  await pressEscape(cdp);
  await waitFor(
    cdp,
    `document.querySelector('dialog[open][data-v3-content-composer] textarea[data-v3-composer-text]')?.value==='人工修改后的话术'`,
    "an IME Escape closed the native top-layer Composer or discarded its draft",
  );
  await evaluate(
    cdp,
    `(() => { const input = document.querySelector('dialog[open][data-v3-content-composer] textarea[data-v3-composer-text]'); input.dispatchEvent(new CompositionEvent('compositionend', { bubbles: true, data: '中' })); return true; })()`,
  );
  await pressEscape(cdp);
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open][data-v3-content-composer]')&&Boolean(document.querySelector('dialog[open] textarea[readonly]'))`,
    "ordinary Escape did not close only the top-layer Composer",
  );
  await pointerClickText(
    cdp,
    "dialog[open]",
    "编辑话术与预览",
    "Excel Composer did not reopen after an IME candidate Escape",
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open][data-v3-content-composer][data-v3-content-top-layer="1"] textarea[data-v3-composer-text]'))`,
    "Excel Composer did not reopen in the native top layer",
  );
  await keyboardText(
    cdp,
    'dialog[open][data-v3-content-composer] textarea[data-v3-composer-text]',
    "人工修改后的话术",
    "reopened Excel Composer textarea did not receive real keyboard input",
  );
  for (const width of [1440, 1280, 420, 360]) await captureExcelComposer(cdp, width);
  await setViewport(cdp, 1280);
  await pointerClick(
    cdp,
    'dialog[open][data-v3-content-composer] button[data-v3-composer-confirm]',
    "Excel shared Composer confirmation did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open][data-v3-content-composer]')&&Boolean(document.querySelector('dialog[open] textarea[readonly]'))&&document.querySelector('dialog[open] textarea')?.value==='人工修改后的话术'`,
    "shared Composer confirmation did not return its local draft to the parent row dialog",
  );
  await pointerClickText(
    cdp,
    "dialog[open]",
    "查看已保存内容",
    "row readonly presentation did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open][data-v3-content-readonly][data-v3-content-top-layer="1"]'))`,
    "row readonly presentation was not promoted to the native top layer",
  );
  await pressEscape(cdp);
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open][data-v3-content-readonly]')&&Boolean(document.querySelector('dialog[open] textarea[readonly]'))`,
    "Escape did not close only the top readonly presentation",
  );
  await pointerClickText(
    cdp,
    "dialog[open]",
    "保存并重新审核",
    "Excel Owner save did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `(()=>{const exclude=[...document.querySelectorAll('.xeb-detail-main button')].filter(b=>b.textContent==='排除')[1];return !document.querySelector('dialog[open]')&&document.querySelector('.xeb-detail-main')?.textContent.includes('人工修改后的话术')&&exclude?.disabled===false})()`,
    "edited wording did not persist as an interactive row",
  );
  const contentReadsBeforeExclude = requests.content;
  const previewsBeforeExclude = requests.preview;
  const approvalsBeforeExclude = requests.approve;
  if (
    !(await evaluate(
      cdp,
      `fetch('/__fixture__/excel-arm-content-read?batch_id=${currentBatchID}',{method:'POST'}).then(response=>response.status===204)`,
    ))
  )
    throw new Error("content-read fixture gate did not arm");
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].filter(b=>b.textContent==='排除')[1].click();true`,
  );
  await waitFor(
    cdp,
    `(()=>{const approve=[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务');return document.querySelector('.xeb-detail-main')?.textContent.includes('已排除1')&&approve?.disabled===true})()`,
    "approval did not remain disabled during the controlled content readback",
  );
  await waitForNetwork(
    () => requests.content === contentReadsBeforeExclude + 1,
    "excluded row did not begin the controlled content readback",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').click();true`,
  );
  if (requests.preview !== previewsBeforeExclude || requests.approve !== approvalsBeforeExclude)
    throw new Error(`disabled approval issued a mutation preview=${requests.preview - previewsBeforeExclude} approve=${requests.approve - approvalsBeforeExclude}`);
  if (
    !(await evaluate(
      cdp,
      `fetch('/__fixture__/excel-release-content-read',{method:'POST'}).then(response=>response.status===204)`,
    ))
  )
    throw new Error("content-read fixture gate did not release");
  await waitFor(
    cdp,
    `(()=>{const approve=[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务');return approve?.disabled===false&&document.querySelector('.xeb-detail-main')?.textContent.includes('人工修改后的话术')})()`,
    "approval did not become interactive after content readback",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='审核通过并创建企微群发任务').click();true`,
  );
  await waitFor(
    cdp,
    `document.querySelector('[data-excel-feedback][role=status]')?.textContent.includes('企微任务意图已创建')&&!([...document.querySelectorAll('.xeb-detail-main button')].some(b=>b.textContent==='审核通过并创建企微群发任务'))`,
    "single approval did not queue one target",
  );
  if (requests.preview !== previewsBeforeExclude + 1 || requests.approve !== approvalsBeforeExclude + 1)
    throw new Error(`interactive approval request counts preview=${requests.preview - previewsBeforeExclude} approve=${requests.approve - approvalsBeforeExclude}`);
  await evaluate(
    cdp,
    `document.querySelector('.xeb-detail-nav button[data-tab="effects"]').click();true`,
  );
  await waitFor(
    cdp,
    `document.querySelector('.xeb-detail-main')?.textContent.includes('最近采集：2026-09-07 09:02:03')&&!document.querySelector('.xeb-detail-main')?.textContent.includes('2026-09-07T01:02:03')`,
    "report timestamp was not rendered as a Shanghai whole-second value",
  );
  await evaluate(
    cdp,
    `document.querySelector('.xeb-detail-nav button[data-tab="content"]').click();true`,
  );
  await waitFor(
    cdp,
    `Boolean([...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='查看旧版本'))`,
    "content tab did not restore the historical-version entry",
  );
  await evaluate(
    cdp,
    `[...document.querySelectorAll('.xeb-detail-main button')].find(b=>b.textContent==='查看旧版本').click();true`,
  );
  await waitFor(
    cdp,
    `/\\d{4}-\\d{2}-\\d{2} \\d{2}:\\d{2}:\\d{2}/.test(document.querySelector('dialog[open]')?.textContent||'')&&!/\\d{4}-\\d{2}-\\d{2}T\\d{2}:\\d{2}:\\d{2}/.test(document.querySelector('dialog[open]')?.textContent||'')`,
    "historical-version timestamp was not rendered as a Shanghai whole-second value",
  );
  await pointerClickText(
    cdp,
    "dialog[open]",
    "只读查看",
    "history version reader did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `(() => { const page = document.querySelector('dialog[open] [data-excel-history-page]'); return Boolean(page?.querySelector('button') && [...page.querySelectorAll('button')].find((button)=>button.textContent==='查看内容') && page.textContent.includes('分层：') && page.textContent.includes('审核：') && page.textContent.includes('执行：') && page.textContent.includes('发送时间：') && page.textContent.includes('行 #') && page.textContent.includes('内容版本 #')); })()`,
    "historical content rows did not preserve their traceable business facts",
  );
  await captureExcelHistory(cdp, "operation-excel-history-1280.png");
  await pointerClickText(
    cdp,
    "dialog[open] [data-excel-history-page]",
    "查看内容",
    "history row readonly presentation did not receive a real pointer click",
  );
  await waitFor(
    cdp,
    `Boolean(document.querySelector('dialog[open][data-v3-content-readonly][data-v3-content-top-layer="1"]'))`,
    "history row readonly presentation was not promoted to the native top layer",
  );
  await captureExcelHistory(cdp, "operation-excel-history-readonly-1280.png", true);
  await pressEscape(cdp);
  await waitFor(
    cdp,
    `!document.querySelector('dialog[open][data-v3-content-readonly]')&&Boolean(document.querySelector('dialog[open] [data-excel-history-page]'))`,
    "Escape did not return from history readonly presentation to its parent dialog",
  );
  await pointerClickText(cdp, "dialog[open]", "关闭", "history dialog close did not receive a real pointer click");
  await cdp.call("Page.reload");
  await waitFor(
    cdp,
    `document.querySelector('.xeb-detail-main')?.textContent.includes('人工修改后的话术')&&!([...document.querySelectorAll('.xeb-detail-main button')].some(b=>b.textContent==='审核通过并创建企微群发任务'))`,
    "state did not survive reload",
  );
  if (errors.length) throw new Error("runtime exceptions in Excel Host");
  console.log("excel_batches_chromium: PASS");
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) {
    // Ask Chromium to close its profile before dropping the DevTools socket.
    // A signal-only shutdown can leave the screenshot profile busy on macOS,
    // which keeps this otherwise-complete browser Journey alive until the Go
    // context kills it.
    try { await cdp.call("Browser.close"); } catch (_) {}
    cdp.close();
  }
  if (browser && browser.exitCode === null && browser.signalCode === null)
    await browserExit(browser);
  await removeProfile(profile);
  // Node's WebSocket close handshake can retain a handle after the browser
  // has exited. The Journey reached PASS only after every browser assertion,
  // artifact write, and cleanup above completed, so terminate the harness
  // rather than letting that idle handle consume the Go test context.
  if (!failed) process.exit(0);
}
