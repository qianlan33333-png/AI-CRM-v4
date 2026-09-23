import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";
import { verifyPresentation } from './presentation_geometry.mjs';

const baseURL = process.env.AICRM_ADMIN_LAYOUT_TEST_URL;
const username = process.env.AICRM_ADMIN_LAYOUT_TEST_USERNAME;
const password = process.env.AICRM_ADMIN_LAYOUT_TEST_PASSWORD;
const productID = process.env.AICRM_ADMIN_LAYOUT_TEST_PRODUCT_ID;
const serviceProductID = process.env.AICRM_ADMIN_LAYOUT_TEST_SERVICE_PRODUCT_ID;
const archiveProductID = process.env.AICRM_ADMIN_LAYOUT_TEST_ARCHIVE_PRODUCT_ID;
const historicalOrderReference = process.env.AICRM_ADMIN_LAYOUT_TEST_HISTORICAL_ORDER;
const nativeOrderReference = process.env.AICRM_ADMIN_LAYOUT_TEST_NATIVE_ORDER;
const radarID = process.env.AICRM_ADMIN_LAYOUT_TEST_RADAR_ID;
const aiPlanID = process.env.AICRM_ADMIN_LAYOUT_TEST_AI_PLAN_ID;
const couponID = process.env.AICRM_ADMIN_LAYOUT_TEST_COUPON_ID;
const screenshotDirectory = process.env.AICRM_ADMIN_LAYOUT_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password || !/^[1-9][0-9]*$/.test(productID || "") || !/^[1-9][0-9]*$/.test(serviceProductID || "") || !/^[1-9][0-9]*$/.test(archiveProductID || "") || !/^[1-9][0-9]*$/.test(radarID || "") || !/^[1-9][0-9]*$/.test(aiPlanID || "") || !/^[1-9][0-9]*$/.test(couponID || "") || !/^[A-Za-z0-9._:-]{1,200}$/.test(historicalOrderReference || "") || !/^[A-Za-z0-9._:-]{1,200}$/.test(nativeOrderReference || "") || !screenshotDirectory) {
  throw new Error("admin layout Chromium journey requires HTTPS URL, test login, product/coupon ids, order fixtures, native AI plan id, and screenshot directory");
}

const delay = milliseconds => new Promise(resolve => setTimeout(resolve, milliseconds));
const chromium = () => {
  const candidates = [process.env.AICRM_CHROMIUM_BINARY, process.env.CHROME_BIN].filter(Boolean);
  if (process.platform === "darwin") candidates.push("/Applications/Google Chrome.app/Contents/MacOS/Google Chrome");
  candidates.push("google-chrome", "google-chrome-stable", "chromium", "chromium-browser");
  for (const candidate of candidates) {
    try {
      if (candidate.includes("/") ? spawnSync(candidate, ["--version"], { stdio: "ignore" }).status === 0 : spawnSync("which", [candidate], { stdio: "ignore" }).status === 0) return candidate;
    } catch (_) {}
  }
  throw new Error("Chromium binary is unavailable");
};

class CDP {
  constructor(socket) {
    this.socket = socket;
    this.nextID = 0;
    this.pending = new Map();
    this.events = new Map();
    socket.addEventListener("message", event => {
      const message = JSON.parse(String(event.data));
      if (message.id && this.pending.has(message.id)) {
        const pending = this.pending.get(message.id);
        this.pending.delete(message.id);
        if (message.error) pending.reject(new Error("CDP request failed"));
        else pending.resolve(message.result || {});
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
    return () => this.events.set(method, (this.events.get(method) || []).filter(value => value !== listener));
  }
  close() {
    for (const pending of this.pending.values()) pending.reject(new Error("CDP closed"));
    this.pending.clear();
    this.events.clear();
    this.socket.close();
  }
}

const waitForPort = async profile => {
  const active = path.join(profile, "DevToolsActivePort");
  // A cold CI runner can take longer than nine seconds to initialize Chromium.
  for (let attempt = 0; attempt < 600; attempt += 1) {
    try {
      const port = String(await fs.readFile(active, "utf8")).split("\n")[0];
      if (/^\d+$/.test(port)) return "http://127.0.0.1:" + port;
    } catch (_) {}
    await delay(50);
  }
  throw new Error("Chromium remote debugging did not become ready");
};
const evaluate = async (cdp, expression) => {
  const result = await cdp.call("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true });
  if (result.exceptionDetails) throw new Error("page evaluation failed");
  return result.result?.value;
};
const waitFor = async (cdp, expression, message) => {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (await evaluate(cdp, expression)) return;
    await delay(50);
  }
  throw new Error(message);
};
const waitForExit = async (child, milliseconds) => !child || child.exitCode !== null || child.signalCode !== null || new Promise(resolve => {
  const timer = setTimeout(() => resolve(false), milliseconds);
  child.once("exit", () => { clearTimeout(timer); resolve(true); });
});
const removeDirectory = async directory => {
  for (let attempt = 0; attempt < 20; attempt += 1) {
    try { await fs.rm(directory, { recursive: true, force: true }); return; } catch (_) { await delay(50); }
  }
};

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-admin-layout-chromium-"));
let child;
let cdp;
let failed = false;
let currentStep = "bootstrap";
const journeyStartedAt = Date.now();
const requests = new Map();
// Keep only same-origin admin requests and responses. Static assets can be
// numerous across the route matrix and must not evict a later business action
// such as the HXC refresh from the diagnostic window.
const requestEvents = [];
const responses = [];
const runtimeExceptions = [];
const appendBounded = (items, value, limit = 240) => {
  items.push(value);
  if (items.length > limit) items.splice(0, items.length - limit);
};
const waitForRecorded = async (items, predicate, message) => {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    if (items.some(predicate)) return;
    await delay(50);
  }
  throw new Error(message);
};
try {
  child = spawn(chromium(), [
    "--headless=new", "--no-sandbox", "--disable-gpu", "--no-first-run", "--no-default-browser-check", "--disable-background-networking",
    "--disable-component-update", "--disable-sync", "--ignore-certificate-errors", "--allow-insecure-localhost",
    "--remote-debugging-port=0", "--user-data-dir=" + profile, "about:blank",
  ], { stdio: ["ignore", "ignore", "ignore"] });
  const address = await waitForPort(profile);
  const created = await (await fetch(address + "/json/new?about:blank", { method: "PUT" })).json();
  const socket = new WebSocket(created.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true });
  });
  cdp = new CDP(socket);
  await cdp.call("Page.enable");
  await cdp.call("Runtime.enable");
  await cdp.call("Network.enable");
  // The production regression was observed in the desktop admin shell.  The
  // explicit viewport prevents Chrome's narrow responsive layout from hiding
  // the sidebar and turning a desktop geometry assertion into a false failure.
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  await fs.mkdir(screenshotDirectory, { recursive: true, mode: 0o700 });
  cdp.on("Runtime.exceptionThrown", params => {
    const value = String(params.exceptionDetails?.exception?.className || params.exceptionDetails?.text || "runtime_exception").replace(/[^A-Za-z0-9_.-]/g, "_").slice(0, 96);
    if (runtimeExceptions.length < 8) runtimeExceptions.push(value);
  });
  cdp.on("Network.requestWillBeSent", params => {
    try {
      const url = new URL(String(params.request?.url || ""));
      if (url.origin !== baseURL) return;
      const request = { method: String(params.request?.method || "GET"), path: url.pathname };
      requests.set(params.requestId, request);
      if (/^\/(?:admin|api\/admin)\//.test(request.path)) appendBounded(requestEvents, `${request.method} ${request.path}`);
    } catch (_) {}
  });
  cdp.on("Network.responseReceived", params => {
    const request = requests.get(params.requestId);
    if (request && /^\/(?:admin|api\/admin)\//.test(request.path)) appendBounded(responses, `${request.method} ${request.path}:${Number(params.response?.status) || 0}`);
  });

  const capture = async name => {
    const result = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
    await fs.writeFile(path.join(screenshotDirectory, name + ".png"), Buffer.from(result.data, "base64"), { mode: 0o600 });
  };
  const captureHeaderWidths = async (owner, label) => {
    for (const width of [1440, 1280]) {
      await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
      await delay(80);
      const header = await evaluate(cdp, `(() => { const action=document.querySelector('[data-page-header-actions="${owner}"]'); const rect=action?.getBoundingClientRect(); const style=action ? getComputedStyle(action) : null; return {width:innerWidth,documentWidth:document.documentElement.scrollWidth,titleCount:document.querySelectorAll('.admin-topbar .admin-page-title').length,actions:action?.querySelectorAll('a,button').length || 0,visible:Boolean(rect && style?.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.right <= innerWidth + 1)}; })()`);
      if (!header || header.width !== width || header.documentWidth > width + 1 || header.titleCount !== 1 || header.actions !== 1 || !header.visible) throw new Error(`${label} ${width}px topbar action layout invalid: ${JSON.stringify(header)}`);
      await capture(`${label}-${width}`);
    }
    await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  };
  const diagnosticMessage = error => String(error instanceof Error ? error.message : error || "browser assertion failed")
    .replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 240);
  const captureFailureEvidence = async (label, failureReason = "") => {
    const safeLabel = label.replace(/[^A-Za-z0-9_.-]/g, "_");
    let screenshotCaptured = false;
    try {
      const shot = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: false });
      await fs.writeFile(path.join(screenshotDirectory, "failure-" + safeLabel + ".png"), Buffer.from(shot.data, "base64"), { mode: 0o600 });
      screenshotCaptured = true;
    } catch (_) {}
    // Geometry and safe route/status diagnostics remain available even if a
    // browser screenshot command itself fails while handling an earlier page
    // error. No response body, credential, or customer data is persisted.
    let geometry = {
      path: "unavailable",
      current_step: currentStep,
      elapsed_ms: Date.now() - journeyStartedAt,
      failure_reason: diagnosticMessage(failureReason),
      screenshot_captured: screenshotCaptured,
      responses: responses.slice(-12),
      runtime_exceptions: runtimeExceptions.slice(-8),
    };
    try {
      const measured = await evaluate(cdp, `(() => {
        const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop,display:style.display}; };
        const visible = node => { if (!node) return false; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
        const token = value => String(value || '').replace(/[^A-Za-z0-9_-]/g, '_').slice(0, 96);
        const dom = [document.body, ...document.querySelectorAll('.admin-main-wrap,.admin-sidebar,.admin-topbar,.side,#stage,.order-host-layout,[data-runtime-release-host],[data-open-platform-host],.open-platform-header,.sec-funnel')].filter((node, index, all) => node instanceof Element && all.indexOf(node) === index).slice(0, 20).map(node => ({tag:node.tagName.toLowerCase(),id:token(node.id),classes:Array.from(node.classList).map(token).filter(Boolean).slice(0, 12),visible:visible(node)}));
        return {path:location.pathname,ready:document.readyState,sidebar:box('.admin-sidebar'),static_sidebar:box('.side'),main:box('.admin-main-wrap'),topbar:box('.admin-topbar'),static_header:box('.open-platform-header'),content:box('#stage') || box('.admin-main-wrap > .admin-page'),stage:box('#stage'),viewport:{width:innerWidth,height:innerHeight},overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,dom};
      })()`);
      geometry = {
        ...measured,
        current_step: currentStep,
        elapsed_ms: Date.now() - journeyStartedAt,
        failure_reason: diagnosticMessage(failureReason),
        screenshot_captured: screenshotCaptured,
        responses: responses.slice(-12),
        runtime_exceptions: runtimeExceptions.slice(-8),
      };
    } catch (_) {}
    await fs.writeFile(path.join(screenshotDirectory, "failure-" + safeLabel + "-geometry.json"), JSON.stringify(geometry), { mode: 0o600 });
  };
  const waitForFonts = async label => {
    const settled = await evaluate(cdp, "document.fonts ? document.fonts.ready.then(() => document.fonts.status === 'loaded') : true");
    if (!settled) throw new Error(label + " document fonts did not settle");
  };
  const geometryFailures = [];
  const interactionFailures = [];
  const recordGeometry = async (label, assertion, screenshot) => {
    try {
      await assertion();
      if (screenshot) await capture(label);
    } catch (error) {
      const message = String(error instanceof Error ? error.message : "geometry assertion failed").replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 160);
      try { await captureFailureEvidence(label, message); } catch (_) {}
      geometryFailures.push(label + ":" + message);
    }
  };
  const currentLayout = titleSelector => evaluate(cdp, String.raw`(() => {
    const isVisible = node => { if (!node) return false; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
    const box = node => { if (!node) return null; const value=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:value.left,top:value.top,right:value.right,bottom:value.bottom,width:value.width,height:value.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop,display:style.display}; };
    const renderedChildren = root => {
      const result=[];
      const visit=node => {
        if (!(node instanceof Element) || ['STYLE','SCRIPT','TEMPLATE'].includes(node.tagName)) return;
        if (getComputedStyle(node).display === 'contents') { for (const child of Array.from(node.children)) visit(child); return; }
        result.push(node);
      };
      for (const child of Array.from(root?.children || [])) visit(child);
      return result;
    };
    const stage=document.querySelector('#stage');
    const main=document.querySelector('.admin-main-wrap');
    const stageBox=stage?.getBoundingClientRect();
    const mainBox=main?.getBoundingClientRect();
    const roots=renderedChildren(stage).filter(isVisible);
    const selector=${JSON.stringify(titleSelector || 'h1,h2,[role="heading"],[class*="toolbar"],[class*="header"],[class*="head"],[class*="title"]')};
    const title=stage ? Array.from(stage.querySelectorAll(selector)).find(node => isVisible(node) && String(node.textContent || '').trim().length > 0) || null : null;
    const titleLineage=[]; for (let current=title; current && current !== stage; current=current.parentElement) titleLineage.push(current);
    const isHeaderEdge = node => {
      if (!stageBox || !mainBox || node === stage || getComputedStyle(node).display === 'contents') return false;
      const value=node.getBoundingClientRect();
      return Math.abs(value.left-stageBox.left) <= 1 && Math.abs(value.top-stageBox.top) <= 1 && Math.abs(value.right-mainBox.right) <= 1 && value.width >= Math.min(220, stageBox.width * 0.4);
    };
    const className = node => typeof node.className === 'string' ? node.className : '';
    const explicitHeader = node => node.tagName === 'HEADER' || /(?:^|[-_\s])(header|toolbar|topbar|page-head|page-header|title-bar)(?:$|[-_\s])/.test(className(node));
    const compactTopLevelHeader = node => {
      if (!roots.includes(node)) return false;
      const value=node.getBoundingClientRect();
      return value.height >= 40 && value.height <= 120 && String(node.textContent || '').trim().length > 0;
    };
    // A donor page can use a plain, inline-styled top-level div instead of a
    // semantic header.  That narrow case is accepted only for a compact
    // rendered root; the full workspace/root is never allowed as the bar.
    const headerCandidates=[];
    for (const node of [...roots, ...titleLineage]) {
      if (headerCandidates.includes(node) || !isVisible(node) || !isHeaderEdge(node)) continue;
      if (explicitHeader(node) || compactTopLevelHeader(node)) headerCandidates.push(node);
    }
    const innerBar=headerCandidates[0] || null;
    if (innerBar) globalThis.__aicrmAdminLayoutInnerBar = innerBar;
    const titleCount=stage ? Array.from(stage.querySelectorAll('h1')).filter(isVisible).filter(node => node.getBoundingClientRect().top < stage.getBoundingClientRect().top + 180).length : 0;
    const content=stage || document.querySelector('.admin-main-wrap > .admin-page');
    return {sidebar:box(document.querySelector('.admin-sidebar')),main:box(main),topbar:box(document.querySelector('.admin-topbar')),content:box(content),stage:box(stage),renderedRootCount:roots.length,innerBar:box(innerBar),innerBarText:String(innerBar?.textContent || '').trim(),headerCandidateCount:headerCandidates.length,title:box(title),titleCount,headers:document.querySelectorAll('header.admin-topbar').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,ready:document.readyState};
  })()`);
  const assertLayout = async (kind, label, titleSelector) => {
    const layout = await currentLayout(titleSelector);
    if (!layout.sidebar || !layout.main || layout.overflow || Math.abs(layout.sidebar.right - layout.main.left) > 1) throw new Error(`${label} shell geometry invalid`);
    if (kind === "standard") {
      if (!layout.topbar || layout.headers !== 1 || Math.abs(layout.topbar.left - layout.main.left) > 1 || Math.abs(layout.topbar.right - layout.main.right) > 1 || Math.abs(layout.topbar.top) > 1 || layout.topbar.height < 48 || !layout.content || layout.content.top + 1 < layout.topbar.bottom) throw new Error(`${label} standard topbar geometry invalid`);
      return;
    }
    if (layout.headers !== 0 || !layout.stage || layout.renderedRootCount < 1 || !layout.innerBar || !layout.innerBarText || !layout.title || layout.titleCount > 1 || Math.abs(layout.stage.left - layout.main.left) > 1 || Math.abs(layout.stage.top - layout.main.top) > 1 || Math.abs(layout.innerBar.left - layout.stage.left) > 1 || Math.abs(layout.innerBar.top - layout.stage.top) > 1 || Math.abs(layout.innerBar.right - layout.main.right) > 1 || layout.stage.paddingLeft !== "0px" || layout.stage.paddingTop !== "0px") throw new Error(`${label} embedded workspace/header geometry invalid`);
  };
  const assertHXCLayout = async label => {
    await assertLayout("standard", label, ".dw-cards");
    const hxc = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage.labs.sec-funnel');
      const crumb=stage?.querySelector(':scope > .crumb');
      const title=stage?.querySelector(':scope > .page-head > :first-child');
      const refresh=document.querySelector('.admin-topbar #hxcRefresh');
      const refreshBox=refresh?.getBoundingClientRect();
      const topbarBox=document.querySelector('.admin-topbar')?.getBoundingClientRect();
      const pageTitle=document.querySelector('.admin-topbar h1');
      const pageTitleVisible=Boolean(pageTitle && pageTitle.textContent?.trim() === '漏斗 / 数据看板' && pageTitle.getBoundingClientRect().height > 0);
      const style=stage ? getComputedStyle(stage) : null;
      return {pageTitleVisible, stage: Boolean(stage), paddingLeft: style?.paddingLeft || '', paddingTop: style?.paddingTop || '', crumbHidden: Boolean(crumb) && getComputedStyle(crumb).display === 'none', titleHidden: Boolean(title) && getComputedStyle(title).display === 'none', refreshVisible: Boolean(refresh) && getComputedStyle(refresh).display !== 'none', refreshInTopbar: Boolean(refreshBox && topbarBox && refreshBox.top >= topbarBox.top && refreshBox.bottom <= topbarBox.bottom)};
    })()`);
    if (!hxc?.stage || !hxc.pageTitleVisible || hxc.paddingLeft !== "0px" || hxc.paddingTop !== "0px" || !hxc.crumbHidden || !hxc.titleHidden || !hxc.refreshVisible || !hxc.refreshInTopbar) throw new Error(label + " HXC title/padding/action layout invalid");
  };
  const assertRadarListLayout = async label => {
    await assertLayout("standard", label, ".admin-page-title");
    const radar = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage.labs.sec-radar');
      const topbar=document.querySelector('.admin-topbar');
      const title=topbar?.querySelector('.admin-page-title');
      const action=topbar?.querySelector('[data-page-header-actions="radar-list"] a[href="/admin/radarForm.html"]');
      const search=stage?.querySelector('#fKeyword');
      const table=stage?.querySelector('#listRows');
      const style=stage ? getComputedStyle(stage) : null;
      const visible=node => { const rect=node?.getBoundingClientRect(), computed=node ? getComputedStyle(node) : null; return Boolean(node && computed?.display !== 'none' && computed?.visibility !== 'hidden' && rect && rect.width > 1 && rect.height > 1); };
      return {stage:Boolean(stage),paddingLeft:style?.paddingLeft || '',paddingTop:style?.paddingTop || '',title:String(title?.textContent || '').trim(),headers:document.querySelectorAll('header.admin-topbar').length,actionVisible:visible(action),pageHead:Boolean(stage?.querySelector(':scope > .page-head')),searchVisible:visible(search),rows:table?.querySelectorAll('tr').length || 0};
    })()`);
    const radarReason = {stage:Boolean(radar?.stage),padding:radar?.paddingLeft === "20px" && radar?.paddingTop === "16px",title:radar?.title === "内容雷达",headers:radar?.headers === 1,action:Boolean(radar?.actionVisible),pageHead:!radar?.pageHead,search:Boolean(radar?.searchVisible),rows:(radar?.rows || 0) >= 1};
    if (!Object.values(radarReason).every(Boolean)) throw new Error(label + " V3 radar list topbar/filter/table layout invalid: " + JSON.stringify(radarReason));
  };

  const assertRadarLayout = async (label, actionSelector, contentSelector = ".sec-radar .page-head") => {
    await assertLayout("standard", label, contentSelector);
    const radar = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage.labs.sec-radar');
      const crumb=stage?.querySelector(':scope > .crumb');
      const title=stage?.querySelector(':scope > .page-head > :first-child');
      const action=stage?.querySelector(${JSON.stringify(actionSelector)});
      const style=stage ? getComputedStyle(stage) : null;
      const page=String(document.body?.dataset.page || '');
      const pageHead=stage?.querySelector(':scope > .page-head');
      const hidden=node => { const rect=node?.getBoundingClientRect(); return !node || getComputedStyle(node).display === 'none' || !rect || rect.width < 1 || rect.height < 1; };
      return {stage:Boolean(stage),paddingLeft:style?.paddingLeft || '',paddingTop:style?.paddingTop || '',crumbHidden:Boolean(crumb) && hidden(crumb),titleHidden:Boolean(title) && hidden(title),emptyPageHeadHidden:!(page === 'radarDetail' || page === 'radarForm') || hidden(pageHead),actionVisible:Boolean(action) && getComputedStyle(action).display !== 'none'};
    })()`);
    if (!radar?.stage || radar.paddingLeft !== "20px" || radar.paddingTop !== "16px" || !radar.crumbHidden || !radar.titleHidden || !radar.emptyPageHeadHidden || !radar.actionVisible) throw new Error(label + " V3 title/action layout invalid");
  };
  const assertRadarDetailVisitorsHost = async () => {
    const detail = await evaluate(cdp, `(() => {
      const host=document.querySelector('[data-v3-radar-visitor-host]');
      const inputs=host ? Array.from(host.querySelectorAll('input')).map(node => node.type) : [];
      const text=String(host?.textContent || '');
      const headings=host ? Array.from(host.querySelectorAll('thead th')).map(node => String(node.textContent || '').trim()) : [];
      return {host:Boolean(host),inputs,headings,rows:host?.querySelectorAll('tbody tr').length || 0,oldRows:Boolean(document.querySelector('#dRows')),visitor:text.includes('雷达布局访客'),external:text.includes('external-radar-layout-001'),oneid:/[1-9][0-9]{6}/.test(host?.querySelector('tbody tr td:nth-child(3)')?.textContent || ''),time:text.includes('2026-09-07 09:02:03'),raw:text.includes('2026-09-07T01:02:03'),stage:text.includes('image_loaded'),exportVisible:Boolean(document.querySelector('#dExport')) && getComputedStyle(document.querySelector('#dExport')).display !== 'none'};
    })()`);
    const expectedHeadings=['昵称','外部联系人 ID','用户编号','打开时间'];
    if (!detail?.host || detail.inputs.join(',') !== 'text,datetime-local,datetime-local' || detail.headings?.join(',') !== expectedHeadings.join(',') || detail.rows !== 1 || detail.oldRows || !detail.visitor || !detail.external || !detail.oneid || !detail.time || detail.raw || detail.stage || !detail.exportVisible) throw new Error("radar detail visitor Host did not replace the frozen query surface: " + JSON.stringify(detail));
  };
  const assertRadarDetailNarrow = async () => {
    try {
      for (const width of [780, 390]) {
        await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 844, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 844 });
        await waitFor(cdp, "Boolean(document.querySelector('[data-v3-radar-visitor-host]'))", "radar detail Host disappeared at narrow width");
        const narrow = await evaluate(cdp, `(() => {
          const host=document.querySelector('[data-v3-radar-visitor-host]');
          const exportButton=document.querySelector('#dExport');
          const tableScroll=host?.querySelector('[data-radar-visitor-table-overflow]');
          const visible=node => { if (!node) return false; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
          const exportRect=exportButton?.getBoundingClientRect();
          return {overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,host:visible(host),exportVisible:visible(exportButton),exportInside:!exportRect || (exportRect.left >= -1 && exportRect.right <= innerWidth + 1),tableScrollable:Boolean(tableScroll) && getComputedStyle(tableScroll).overflowX !== 'visible'};
        })()`);
        if (!narrow?.host || !narrow.exportVisible || !narrow.exportInside || narrow.overflow || !narrow.tableScrollable) throw new Error(`radar detail ${width}px responsive geometry invalid`);
        await evaluate(cdp, "document.querySelector('[data-v3-radar-visitor-host]')?.scrollIntoView({block:'start'}); true");
        await capture(`radar-detail-${width}`);
      }
    } finally {
      await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
    }
  };
  const assertRadarDetailVisitorSearchAndExport = async () => {
    const enteredSearch = await evaluate(cdp, `(() => {
      const host=document.querySelector('[data-v3-radar-visitor-host]');
      const controls=host?.querySelector('.filter-bar');
      const search=controls?.querySelector('input');
      const query=controls?.querySelector('button');
      if (!(search instanceof HTMLInputElement) || !(query instanceof HTMLButtonElement)) return false;
      search.value='没有匹配的访客';
      search.dispatchEvent(new Event('input',{bubbles:true}));
      query.click();
      return true;
    })()`);
    if (!enteredSearch) throw new Error('radar detail visitor search controls unavailable');
    await waitFor(cdp, "document.querySelector('[data-v3-radar-visitor-host]')?.textContent?.includes('暂无符合条件的访问者')", 'radar detail visitor server search did not replace results');
    const restored = await evaluate(cdp, `(() => {
      const host=document.querySelector('[data-v3-radar-visitor-host]');
      const controls=host?.querySelector('.filter-bar');
      const reset=controls?.querySelectorAll('button')[1];
      if (!(reset instanceof HTMLButtonElement)) return false;
      reset.click();
      return true;
    })()`);
    if (!restored) throw new Error('radar detail visitor reset control unavailable');
    await waitFor(cdp, "document.querySelector('[data-v3-radar-visitor-host]')?.textContent?.includes('雷达布局访客')", 'radar detail visitor reset did not restore the server result');
    const exported = await evaluate(cdp, `(() => {
      const button=document.querySelector('#dExport');
      if (!(button instanceof HTMLButtonElement) || button.disabled) return false;
      button.click();
      return true;
    })()`);
    if (!exported) throw new Error('radar detail visitor CSV export control unavailable');
    await waitFor(cdp, "document.querySelector('[data-radar-visitor-feedback]')?.textContent?.includes('已导出 CSV')", 'radar detail visitor CSV export did not finish');
  };
  const assertOwnerHandoffLayout = async label => {
    await assertLayout("standard", label, "[data-owner-picker=\"source\"]");
    const owner = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage[data-owner-handoff-host]');
      const page=stage?.querySelector('[data-owner-migration-page]');
      const donorHeader=page?.querySelector(':scope > .owner-migration-header');
      const donorTitle=donorHeader?.querySelector(':scope > :first-child');
      const status=donorHeader?.querySelector('.owner-migration-status-bar');
      const style=stage ? getComputedStyle(stage) : null;
      return {stage:Boolean(stage),paddingLeft:style?.paddingLeft || '',paddingTop:style?.paddingTop || '',donorTitleHidden:Boolean(donorTitle) && getComputedStyle(donorTitle).display === 'none',statusVisible:Boolean(status) && getComputedStyle(status).display !== 'none',migrationActionVisible:Boolean(page?.querySelector('[data-owner-picker="source"]')) && getComputedStyle(page.querySelector('[data-owner-picker="source"]')).display !== 'none'};
    })()`);
    if (!owner?.stage || owner.paddingLeft !== "20px" || owner.paddingTop !== "16px" || !owner.donorTitleHidden || !owner.statusVisible || !owner.migrationActionVisible) throw new Error(label + " V3 title/status/action layout invalid");
  };
  const assertExternalEffectsLayout = async label => {
    await assertLayout("standard", label, "#stage h2");
    const effects = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage');
      const shell=stage?.querySelector(':scope > div');
      const localHeader=shell?.querySelector(':scope > :first-child');
      const localCrumb=localHeader?.querySelector(':scope > :first-child');
      const localTitle=localHeader?.querySelector(':scope > h1');
      const history=shell?.querySelector('a[href*="history=1"]');
      const refresh=stage?.querySelector('#effects-refresh');
      return {stage:Boolean(stage),localCrumbHidden:Boolean(localCrumb) && getComputedStyle(localCrumb).display === 'none',localTitleHidden:Boolean(localTitle) && getComputedStyle(localTitle).display === 'none',historyVisible:Boolean(history) && getComputedStyle(history).display !== 'none',refreshVisible:Boolean(refresh) && getComputedStyle(refresh).display !== 'none'};
    })()`);
    if (!effects?.stage || !effects.localCrumbHidden || !effects.localTitleHidden || !effects.historyVisible || !effects.refreshVisible) throw new Error(label + " V3 title/history/action layout invalid");
  };
  const assertInsetRegressionRejected = async (label, titleSelector) => {
    const prepared = await evaluate(cdp, `(() => {
      const target=globalThis.__aicrmAdminLayoutInnerBar;
      if (!(target instanceof Element) || !target.isConnected) return false;
      globalThis.__aicrmAdminLayoutInnerBarStyle=target.getAttribute('style');
      target.style.setProperty('position','relative','important');
      target.style.setProperty('left','20px','important');
      return true;
    })()`);
    if (!prepared) throw new Error(label + " did not expose a concrete inner title bar for the padding regression control");
    let rejected = false;
    try {
      await assertLayout("embedded", label, titleSelector);
    } catch (_) {
      rejected = true;
    } finally {
      await evaluate(cdp, `(() => {
        const target=globalThis.__aicrmAdminLayoutInnerBar;
        const original=globalThis.__aicrmAdminLayoutInnerBarStyle;
        if (!(target instanceof Element)) return false;
        if (original === null || original === undefined) target.removeAttribute('style');
        else target.setAttribute('style', original);
        return true;
      })()`);
    }
    if (!rejected) throw new Error(label + " accepted a 20px inset title bar regression");
    await assertLayout("embedded", label, titleSelector);
  };
  const recordRouteFailure = async (label, error) => {
    const message = String(error instanceof Error ? error.message : "route assertion failed").replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 160);
    try { await captureFailureEvidence(label, message); } catch (_) {}
    geometryFailures.push(label + ":" + message);
  };
  const clickNavigation = async (pathname, label) => {
    const destination = new URL(pathname, baseURL);
    const found = await evaluate(cdp, `(() => [...document.querySelectorAll('.admin-nav-link[href]')].some(node => { const target=new URL(node.href, location.href); return target.pathname === ${JSON.stringify(destination.pathname)} && target.search === ${JSON.stringify(destination.search)}; }))()`);
    if (!found) throw new Error(label + " menu link is absent or points to a fallback route");
    await evaluate(cdp, `(() => { const node=[...document.querySelectorAll('.admin-nav-link[href]')].find(value => { const target=new URL(value.href, location.href); return target.pathname === ${JSON.stringify(destination.pathname)} && target.search === ${JSON.stringify(destination.search)}; }); node.click(); return true; })()`);
  };
  const twoAnimationFrames = () => evaluate(cdp, 'new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)))');
  const pointerClick = async (selector, label, options = {}) => {
    const preservePosition = options.preservePosition === true;
    const prepared = await evaluate(cdp, `(() => {
      const node = document.querySelector(${JSON.stringify(selector)});
      if (!(node instanceof HTMLElement)) return false;
      if (!${preservePosition ? 'true' : 'false'}) node.scrollIntoView({ block: 'center', inline: 'center' });
      return true;
    })()`);
    if (!prepared) throw new Error(label + ' is unavailable before pointer interaction');
    // A fixed menu repositions on scroll. Wait for layout to settle, then take
    // the hit point immediately before dispatching real CDP mouse input.
    await twoAnimationFrames();
    const point = await evaluate(cdp, `(() => {
      const node = document.querySelector(${JSON.stringify(selector)});
      if (!(node instanceof HTMLElement)) return null;
      const rect = node.getBoundingClientRect();
      const target = document.elementFromPoint(rect.left + rect.width / 2, rect.top + rect.height / 2);
      return { x: rect.left + rect.width / 2, y: rect.top + rect.height / 2, visible: rect.width > 1 && rect.height > 1, receivesPointer: target === node || node.contains(target) };
    })()`);
    if (!point?.visible || !point.receivesPointer) throw new Error(label + ' is obscured or unavailable ' + JSON.stringify(point));
    await cdp.call('Input.dispatchMouseEvent', { type: 'mousePressed', x: point.x, y: point.y, button: 'left', clickCount: 1 });
    await cdp.call('Input.dispatchMouseEvent', { type: 'mouseReleased', x: point.x, y: point.y, button: 'left', clickCount: 1 });
  };
  const navigate = async (pathname, ready, label, kind, titleSelector, screenshot = false, fromMenu = false, finalPath = pathname) => {
    currentStep = label;
    try {
      if (fromMenu) await clickNavigation(pathname, label);
      else await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(finalPath.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready + " && Boolean((() => { const stage=document.querySelector('#stage'); if (!stage) return false; const visible=node => { const rect=node.getBoundingClientRect(), style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; }; return Array.from(stage.querySelectorAll(" + JSON.stringify(titleSelector) + ")).some(node => visible(node) && String(node.textContent || '').trim().length > 0); })())", label + " Host did not render a visible workspace title");
      await waitForFonts(label);
      await recordGeometry(label, () => assertLayout(kind, label, titleSelector), screenshot);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };

  const embeddedTitle = 'h1,h2,[role="heading"],[class*="toolbar"],[class*="header"],[class*="head"],[class*="title"]';
  // The frozen questionnaire list uses an inline-styled, classless title. This
  // exact selector records its source-backed visible title contract without
  // admitting arbitrary container text as a page header.
  const questionnaireTitle = '#stage div[style*="font-size:16px"][style*="font-weight:600"][style*="line-height:22px"]';
  // The frozen product and media list templates use the same source-backed
  // 52px toolbar but no semantic title class. Keep this narrow shape instead
  // of accepting arbitrary body text as a workspace heading.
  const frozenListToolbarTitle = '#stage > div[style*="display: contents"] > div[style*="height:52px"] div[style*="font-size:16px"][style*="font-weight:600"][style*="line-height:22px"]';
  // Image Library has a V3-owned workspace rather than the frozen media
  // toolbar. Its mount marker and direct header title prove that the current
  // Host, not a removed donor template, rendered the page before geometry is
  // measured.
  const imageLibraryHostTitle = '[data-image-library-title] h1';
  const navigateStandard = async (pathname, ready, label, screenshot = false, fromMenu = false) => {
    currentStep = label;
    try {
      if (fromMenu) await clickNavigation(pathname, label);
      else await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready, label + " Host did not become ready");
      await waitForFonts(label);
      await recordGeometry(label, () => assertLayout("standard", label, embeddedTitle), screenshot);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };
  const assertMaterialWorkspace = async (label, tab, actionLabel) => {
    await assertLayout("standard", label, ".admin-page-title");
    const material = await evaluate(cdp, `(() => {
      const topbar=document.querySelector('.admin-topbar');
      const stage=document.querySelector('#stage[data-material-library-workspace="true"]');
      const title=topbar?.querySelector('.admin-page-title');
      const tabs=Array.from(stage?.querySelectorAll('[data-material-library-tab]') || []);
      const action=Array.from(topbar?.querySelectorAll('button') || []).find(node => String(node.textContent || '').trim() === ${JSON.stringify(actionLabel)});
      const visible=node => { const rect=node?.getBoundingClientRect(), style=node ? getComputedStyle(node) : null; return Boolean(node && rect && rect.width > 1 && rect.height > 1 && style?.display !== 'none' && style.visibility !== 'hidden'); };
      const donorHeaders=Array.from(stage?.querySelectorAll('div[style*="height: 52px"],div[style*="height:52px"]') || []).filter(visible);
      const identityRows=Array.from(stage?.querySelectorAll('[data-material-library-id]') || []).filter(node => /^[0-9]+$/.test(String(node.dataset.materialLibraryId || '')));
      const actionBox=action?.getBoundingClientRect();
      return {title:String(title?.textContent || '').trim(),headers:document.querySelectorAll('header.admin-topbar').length,tabs:tabs.map(node => ({tab:node.dataset.materialLibraryTab,current:node.getAttribute('aria-current'),href:node.getAttribute('href')})),action:visible(action),actionBox:actionBox ? {left:Math.round(actionBox.left),right:Math.round(actionBox.right),width:Math.round(actionBox.width),height:Math.round(actionBox.height)} : null,overflow:document.documentElement.scrollWidth > innerWidth + 1,donorHeaders:donorHeaders.length,identityRows:identityRows.length};
    })()`);
    const expectedTabs = ['images', 'attachments', 'miniprograms'];
    const validTabs = material?.tabs?.length === expectedTabs.length && material.tabs.every((item, index) => item.tab === expectedTabs[index] && item.href === `/admin/materials?tab=${expectedTabs[index]}` && (item.tab === tab ? item.current === 'page' : item.current === null));
    if (!material || material.title !== '素材库' || material.headers !== 1 || !validTabs || !material.action || material.overflow || material.donorHeaders !== 0 || (tab !== 'images' && material.identityRows < 1)) {
      throw new Error(`${label} material_workspace title_ok=${material?.title === '素材库'} headers=${material?.headers} tabs_ok=${validTabs} action_visible=${material?.action} action_box=${JSON.stringify(material?.actionBox)} overflow=${material?.overflow} donor_headers=${material?.donorHeaders} identity_rows=${material?.identityRows}`);
    }
  };
  const navigateMaterialWorkspace = async (tab, label, actionLabel, ready, ownerPath, requiredText) => {
    const pathname = `/admin/materials?tab=${tab}`;
    currentStep = label;
    try {
      const requestStart = requestEvents.length;
      await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === '/admin/materials' && new URLSearchParams(location.search).get('tab') === ${JSON.stringify(tab)} && document.readyState !== 'loading'`, label + ' did not navigate');
      await waitFor(cdp, ready, label + ' Host did not become ready');
      const ownerReads = requestEvents.slice(requestStart).filter(value => /^GET \/api\/admin\/(?:image-library|attachment-library|miniprogram-library)$/.test(value));
      if (!ownerReads.includes(`GET ${ownerPath}`) || ownerReads.some(value => value !== `GET ${ownerPath}`)) {
        throw new Error(`${label} read a non-active material owner: ${JSON.stringify(ownerReads)}`);
      }
      if (requiredText) {
        await waitFor(cdp, `document.querySelector('#stage')?.textContent?.includes(${JSON.stringify(requiredText)})`, `${label} did not present fixture metadata ${JSON.stringify(requiredText)}`);
      }
      await waitForFonts(label);
      await recordGeometry(label, () => assertMaterialWorkspace(label, tab, actionLabel), false);
      for (const width of [1280, 1440]) {
        await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
        await delay(80);
        await assertMaterialWorkspace(label + `-${width}`, tab, actionLabel);
        if (tab === 'images') await assertImageDirectory(label + `-${width}`);
        await capture(`${label}-${width}`);
      }
      await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };
  const assertImageDirectory = async (label) => {
    const result = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage[data-material-library-workspace="true"]');
      const table=stage?.querySelector('[data-image-library-directory]');
      const rows=Array.from(table?.querySelectorAll('tbody [data-image-library-row]') || []);
      const names=['素材工作台横向缩略图','素材工作台纵向缩略图','素材工作台小尺寸缩略图'];
      const thumbnails=Array.from(table?.querySelectorAll('[data-image-library-thumbnail] img') || []);
      const overflow=Boolean(table && table.parentElement && (table.scrollWidth > table.parentElement.clientWidth + 1 || table.getBoundingClientRect().right > table.parentElement.getBoundingClientRect().right + 1));
      return {rowCount:rows.length,names:names.map(name => stage?.textContent?.includes(name)),objectFits:thumbnails.map(node => getComputedStyle(node).objectFit),overflow};
    })()`);
    if (!result || result.rowCount < 3 || result.names.some(value => !value) || result.objectFits.length < 3 || result.objectFits.some(value => value !== 'contain') || result.overflow) {
      throw new Error(`${label} compact image directory geometry invalid: ${JSON.stringify(result)}`);
    }
  };
  const assertMaterialAlias = async (pathname, tab, ready, label) => {
    currentStep = label;
    try {
      await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === '/admin/materials' && new URLSearchParams(location.search).get('tab') === ${JSON.stringify(tab)} && document.readyState !== 'loading'`, label + ' did not redirect to the selected workspace tab');
      await waitFor(cdp, ready, label + ' material Host did not become ready');
      await assertMaterialWorkspace(label, tab, tab === 'images' ? '上传图片' : tab === 'attachments' ? '上传附件' : '新建小程序卡片');
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };
  const assertMaterialHeaderActionOpens = async (owner, actionLabel, expectedInput, label) => {
    try {
      const opened = await evaluate(cdp, `(() => {
        const action=Array.from(document.querySelectorAll('[data-page-header-actions=${JSON.stringify(owner)}] button')).find(node => String(node.textContent || '').trim() === ${JSON.stringify(actionLabel)});
        if (!(action instanceof HTMLButtonElement) || action.disabled) return false;
        action.click(); return true;
      })()`);
      if (!opened) throw new Error(label + ' topbar action was not actionable');
      await waitFor(cdp, `document.querySelector(${JSON.stringify(expectedInput)}) instanceof HTMLInputElement`, label + ' original modal did not open');
      const closed = await evaluate(cdp, `(() => {
        const input=document.querySelector(${JSON.stringify(expectedInput)});
        const modal=input?.closest('div[style*="position: fixed"],div[style*="position:fixed"]');
        const cancel=Array.from(modal?.querySelectorAll('button') || []).find(node => String(node.textContent || '').trim() === '取消');
        if (!(cancel instanceof HTMLButtonElement)) return false;
        cancel.click(); return true;
      })()`);
      if (!closed) throw new Error(label + ' modal could not be cancelled without a write');
      await waitFor(cdp, `!document.querySelector(${JSON.stringify(expectedInput)})`, label + ' modal did not close after cancellation');
    } catch (error) {
      await recordRouteFailure(label, error);
    }
  };
  const assertMiniProgramCommittedSearchAndSecondPage = async () => {
    currentStep = 'materials-miniprograms-search-pagination';
    try {
      const ownerReadCount = () => requestEvents.filter(value => value === 'GET /api/admin/miniprogram-library').length;
      const beforeDraft = ownerReadCount();
      const drafted = await evaluate(cdp, `(() => {
        const input=document.querySelector('#fMpQuery');
        const stage=document.querySelector('#stage');
        if (!(input instanceof HTMLInputElement) || !(stage instanceof HTMLElement)) return false;
        input.focus(); input.value='输入法草稿';
        input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));
        input.dispatchEvent(new Event('input',{bubbles:true}));
        stage.append(document.createElement('aside'));
        return true;
      })()`);
      if (!drafted) throw new Error('mini-program search input was unavailable');
      await delay(100);
      if (ownerReadCount() !== beforeDraft) throw new Error('an IME draft or unrelated DOM mutation issued a mini-program read');
      const committed = await evaluate(cdp, `(() => {
        const input=document.querySelector('#fMpQuery');
        if (!(input instanceof HTMLInputElement)) return false;
        input.dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));
        input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,key:'Enter'}));
        return true;
      })()`);
      if (!committed) throw new Error('mini-program IME candidate input was unavailable');
      await delay(50);
      if (ownerReadCount() !== beforeDraft) throw new Error('an IME candidate Enter issued a mini-program read');
      await evaluate(cdp, `(() => {
        const input=document.querySelector('#fMpQuery');
        if (!(input instanceof HTMLInputElement)) return false;
        input.dispatchEvent(new KeyboardEvent('keydown',{bubbles:true,key:'Enter'})); return true;
      })()`);
      await waitFor(cdp, `document.querySelector('#stage')?.textContent?.includes('当前条件下没有小程序素材。')`, 'explicit committed mini-program search did not complete');
      const beforeContentChange = ownerReadCount();
      await evaluate(cdp, `window.dispatchEvent(new Event('aicrm:media-content-changed'))`);
      for (let attempt = 0; attempt < 40 && ownerReadCount() <= beforeContentChange; attempt += 1) await delay(50);
      if (ownerReadCount() <= beforeContentChange) throw new Error('saved-content event did not refresh the current mini-program page');
      const reset = await evaluate(cdp, `(() => { const button=document.querySelector('#mpReset'); if (!(button instanceof HTMLButtonElement)) return false; button.click(); return true; })()`);
      if (!reset) throw new Error('mini-program reset was unavailable');
      await waitFor(cdp, `document.querySelector('#stage')?.textContent?.includes('wx_material_layout') && document.querySelector('#mpNext') instanceof HTMLButtonElement`, 'mini-program reset did not restore the owner page');
      const next = await evaluate(cdp, `(() => { const button=document.querySelector('#mpNext'); if (!(button instanceof HTMLButtonElement) || button.disabled) return false; button.click(); return true; })()`);
      if (!next) throw new Error('mini-program second-page action was unavailable');
      await waitFor(cdp, `document.querySelector('#stage')?.textContent?.includes('wx_material_page_01') && /51\\s*[-–—]\\s*51/.test(document.querySelector('#stage')?.textContent || '')`, 'mini-program second page did not render its owner row');
      await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1280, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: 1280, screenHeight: 900 });
      await assertMaterialWorkspace('materials-miniprograms-page-2-1280', 'miniprograms', '新建小程序卡片');
      await capture('materials-miniprograms-page-2-1280');
      await cdp.call('Emulation.setDeviceMetricsOverride', { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
    } catch (error) {
      await recordRouteFailure('materials-miniprograms-search-pagination', error);
    }
  };
  const assertGroupOpsLayout = async label => {
    await assertLayout("standard", label, ".admin-page-title");
    const layout = await evaluate(cdp, `(() => {
      const box = node => { if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const visible = node => { if (!node) return false; const rect=node.getBoundingClientRect(), style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
      const stage=document.querySelector('#stage.admin-page[data-group-ops-standard-stage]');
      const topbar=document.querySelector('.admin-topbar');
      const title=topbar?.querySelector('.admin-page-title');
      const root=stage?.querySelector('#group-ops-app[data-group-ops-standard-host="true"]');
      const actions=topbar?.querySelector('[data-page-header-actions="groupops"]');
      const viewGroups=actions?.querySelector('a[href="/admin/automation-conversion/group-ops/groups/ui"]');
      const create=actions?.querySelector('button');
      return {stage:box(stage),topbar:box(topbar),titleText:String(title?.textContent || '').trim(),headers:document.querySelectorAll('header.admin-topbar').length,root:box(root),actions:box(actions),viewGroupsVisible:visible(viewGroups),createVisible:visible(create),workspaceToolbar:Boolean(root?.querySelector(':scope > .group-ops__bar')),pageH1Count:Array.from(document.querySelectorAll('h1')).filter(visible).length,syntheticWorkspaceHeadings:root?.querySelectorAll('.group-ops__page-heading').length ?? -1,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
    })()`);
    // The final dd8 standard shell cascade gives native pages a 20px/16px
    // content inset. Keep this exact source-backed value rather than the
    // earlier declaration that the final cascade overrides.
    const invalid = !layout.stage || !layout.topbar || layout.headers !== 1 || layout.titleText !== "群运营计划" || !layout.root || !layout.actions || !layout.viewGroupsVisible || !layout.createVisible || layout.workspaceToolbar || layout.pageH1Count !== 1 || layout.syntheticWorkspaceHeadings !== 0 || layout.overflow || layout.stage.paddingLeft !== "20px" || layout.stage.paddingTop !== "16px" || layout.root.top + 1 < layout.topbar.bottom || Math.abs(layout.root.left - layout.stage.left - 20) > 1 || Math.abs(layout.root.top - layout.stage.top - 16) > 1;
    if (invalid) throw new Error(label + " native Group Ops topbar/content geometry invalid");
  };
  const navigateGroupOps = async (pathname, label, screenshot = false, fromMenu = false, finalPath = pathname) => {
    currentStep = label;
    try {
      if (fromMenu) await clickNavigation(pathname, label);
      else await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(finalPath.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, "Boolean(document.querySelector('#stage.admin-page[data-group-ops-standard-stage] #group-ops-app[data-group-ops-standard-host=\"true\"]')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"groupops\"] a[href=\"/admin/automation-conversion/group-ops/groups/ui\"]')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"groupops\"] button'))", label + " native Group Ops Host did not become ready");
      await waitForFonts(label);
      await recordGeometry(label, () => assertGroupOpsLayout(label), screenshot);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };
  const assertAIAssistantLayout = async (label, detail) => {
    await assertLayout("standard", label, embeddedTitle);
    const layout = await evaluate(cdp, `(() => {
      const box = node => { if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const visible = node => { if (!node) return false; const rect=node.getBoundingClientRect(), style=getComputedStyle(node); return style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1; };
      const stage=document.querySelector('#stage.admin-workspace-stage--dynamic');
      const topbar=document.querySelector('.admin-topbar');
      const title=topbar?.querySelector('.admin-page-title');
      const root=document.querySelector('[data-cloud-plan-root]');
      const toolbar=root?.querySelector('.cloud-plan-toolbar');
      const refresh=toolbar?.querySelector('[data-plan-refresh]');
      const detailHead=root?.querySelector('.cloud-plan-detail-head');
      const detailState=root?.querySelector('[data-plan-detail-state]');
      const planActions=topbar?.querySelector('[data-page-header-actions="ai-plan-detail"]');
      const approveInHeader=planActions?.querySelector('[data-plan-approve]');
      const rejectInHeader=planActions?.querySelector('[data-plan-reject]');
      const backInHeader=planActions?.querySelector('a[href="/admin/cloud-orchestrator/plans"]');
      const style = node => node ? getComputedStyle(node) : null;
      const color = value => {
        const match=String(value || '').match(/^rgba?\\((\\d+),\\s*(\\d+),\\s*(\\d+)/);
        return match ? [Number(match[1]),Number(match[2]),Number(match[3])] : null;
      };
      const luminance = value => {
        const rgb=color(value); if (!rgb) return 0;
        const channels=rgb.map(channel => { const normalized=channel/255; return normalized <= .03928 ? normalized/12.92 : Math.pow((normalized+.055)/1.055,2.4); });
        return .2126*channels[0]+.7152*channels[1]+.0722*channels[2];
      };
      const contrast = (foreground, background) => {
        const one=luminance(foreground), two=luminance(background);
        return (Math.max(one,two)+.05)/(Math.min(one,two)+.05);
      };
      const readable = node => {
        const value=style(node); return Boolean(value) && contrast(value.color,value.backgroundColor) >= 4.5;
      };
      // This only probes the native disabled rendering and restores the exact
      // domain fact. It never invokes a plan command or a Provider write.
      const approveWasDisabled=approveInHeader instanceof HTMLButtonElement ? approveInHeader.disabled : false;
      if (approveInHeader instanceof HTMLButtonElement) approveInHeader.disabled=true;
      const disabledStyle=style(approveInHeader);
      const disabledVisible=Boolean(disabledStyle && disabledStyle.color !== 'rgba(0, 0, 0, 0)' && disabledStyle.backgroundColor !== 'rgba(0, 0, 0, 0)');
      if (approveInHeader instanceof HTMLButtonElement) approveInHeader.disabled=approveWasDisabled;
      // Relocated controls are intentionally no longer descendants of the
      // plan root. Inspect the original donor container directly so this
      // verifies its hidden state rather than treating a missing moved button
      // as proof that the duplicate toolbar disappeared.
      const sourceActions=detailHead?.querySelector('.cloud-plan-actions');
      return {stage:box(stage),topbar:box(topbar),titleText:String(title?.textContent || '').trim(),headers:document.querySelectorAll('header.admin-topbar').length,root:box(root),toolbar:box(toolbar),refreshVisible:visible(refresh),detailHead:box(detailHead),detailStateVisible:visible(detailState),planActions:box(planActions),approveVisible:visible(approveInHeader),rejectVisible:visible(rejectInHeader),backVisible:visible(backInHeader),approveReadable:readable(approveInHeader),rejectReadable:readable(rejectInHeader),backReadable:readable(backInHeader),disabledVisible,sourceActionsHidden:!visible(sourceActions),stageDuplicateActions:Boolean(root?.querySelector('[data-plan-approve],[data-plan-reject]')),overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
    })()`);
    const commonInvalid = !layout.stage || !layout.topbar || layout.headers !== 1 || layout.titleText !== "AI 助手" || !layout.root || layout.overflow || layout.stage.paddingLeft !== "20px" || layout.stage.paddingTop !== "16px" || layout.root.top + 1 < layout.topbar.bottom;
    if (commonInvalid) throw new Error(label + " native cloud-plan topbar/content geometry invalid");
    if (!detail && (!layout.toolbar || !layout.refreshVisible || Math.abs(layout.toolbar.left-layout.root.left) > 1 || Math.abs(layout.toolbar.top-layout.root.top) > 1)) throw new Error(label + " native cloud-plan toolbar is absent or misaligned");
    if (detail && (!layout.detailHead || !layout.detailStateVisible || !layout.planActions || !layout.approveVisible || !layout.rejectVisible || !layout.backVisible || !layout.approveReadable || !layout.rejectReadable || !layout.backReadable || !layout.disabledVisible || !layout.sourceActionsHidden || layout.stageDuplicateActions || Math.abs(layout.detailHead.left-layout.root.left) > 1 || Math.abs(layout.detailHead.top-layout.root.top) > 1 || layout.planActions.top + 1 < layout.topbar.top || layout.planActions.bottom > layout.topbar.bottom + 1)) throw new Error(label + " native cloud-plan detail actions/status are absent, unreadable or misaligned");
  };
  const assertHeaderActionWidths = async (label, owner, labels) => {
    try {
      for (const width of [1280, 1440]) {
        await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
        const layout = await evaluate(cdp, `(() => {
          const topbar=document.querySelector('.admin-topbar');
          const actions=topbar?.querySelector('[data-page-header-actions=${owner}]');
          const visible=node => { const rect=node?.getBoundingClientRect(); const style=node && getComputedStyle(node); return Boolean(node && style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1); };
          const top=topbar?.getBoundingClientRect();
          const controls=actions ? Array.from(actions.children) : [];
          return {titleCount:Array.from(document.querySelectorAll('.admin-topbar h1')).filter(visible).length,labels:controls.map(node => String(node.textContent || '').trim()),visible:controls.every(visible),inside:controls.every(node => { const box=node.getBoundingClientRect(); return top && box.left >= top.left - 1 && box.right <= top.right + 1 && box.top >= top.top - 1 && box.bottom <= top.bottom + 1; }),overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
        })()`);
        if (layout.titleCount !== 1 || layout.labels.join('|') !== labels.join('|') || !layout.visible || !layout.inside || layout.overflow) throw new Error(label + ' ' + width + 'px topbar action geometry invalid: ' + JSON.stringify(layout));
        await capture(`${label}-${width}`);
      }
    } finally {
      await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
    }
  };
  const assertTagsLayout = async label => {
    await assertLayout("standard", label, "#stage h2");
    const layout = await evaluate(cdp, `(() => {
      const stage=document.querySelector('#stage'); const topbar=document.querySelector('.admin-topbar');
      const visible=node => { const rect=node?.getBoundingClientRect(); const style=node && getComputedStyle(node); return Boolean(node && style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1); };
      const actions=topbar?.querySelector('[data-page-header-actions="wecom-tags"]');
      const localTitle=stage?.querySelector('[data-page-header-donor-title="tags"]');
      // The frozen source gives this row an inline flex display. Assert the
      // computed style, not only the hidden attribute, so an author rule can
      // never leave a second visible title behind.
      const localTitleHidden=Boolean(localTitle) && getComputedStyle(localTitle).display === 'none';
      return {title:String(topbar?.querySelector('.admin-page-title')?.textContent || '').trim(),titleCount:Array.from(document.querySelectorAll('.admin-topbar h1')).filter(visible).length,labels:Array.from(actions?.children || []).map(node => String(node.textContent || '').trim()),actionsVisible:Array.from(actions?.children || []).every(visible),sourceVisible:['同步企微标签','新增标签组','新增标签'].some(label => Array.from(stage?.querySelectorAll('button') || []).some(button => String(button.textContent || '').trim() === label && visible(button))),localTitleHidden,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
    })()`);
    if (layout.title !== '企微标签管理' || layout.titleCount !== 1 || layout.labels.join('|') !== '同步企微标签|新增标签组|新增标签' || !layout.actionsVisible || layout.sourceVisible || !layout.localTitleHidden || layout.overflow) throw new Error(label + ' V3 tag topbar action layout invalid: ' + JSON.stringify(layout));
  };
  const navigateAIAssistant = async (pathname, label, ready, detail, fromMenu = false) => {
    currentStep = label;
    try {
      if (fromMenu) await clickNavigation(pathname, label);
      else await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname)} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready, label + " native cloud-plan Host did not become ready");
      await waitForFonts(label);
      await recordGeometry(label, () => assertAIAssistantLayout(label, detail), true);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };
  const navigateTags = async () => {
    const mounted = await navigate(
      "/admin/wecom-tags",
      "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"wecom-tags\"]'))",
      "tags",
      "standard",
      "#stage h2",
      true,
      true,
    );
    if (!mounted) return false;
    await recordGeometry("tags-actions", () => assertTagsLayout("tags"), false);
    await assertHeaderActionWidths("tags", "wecom-tags", ["同步企微标签", "新增标签组", "新增标签"]);
    // This confirms that moving the original donor node preserves the donor's
    // existing form-opening listener. It is local UI only: it does not submit,
    // synchronize, or contact the Provider.
    const opened = await evaluate(cdp, `(() => {
      const button=document.querySelector('.admin-topbar [data-page-header-actions="wecom-tags"] button:last-child');
      if (!(button instanceof HTMLButtonElement) || button.disabled) return false;
      button.click(); return true;
    })()`);
    if (!opened) throw new Error("tags relocated create action is unavailable");
    await waitFor(cdp, "Boolean(document.querySelector('#fTagName'))", "tags relocated create action did not retain its donor listener");
    await recordGeometry("tags-action-redraw", () => assertTagsLayout("tags"), false);
    return true;
  };
  const assertStaticOpenLayout = async label => {
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const docs=Boolean(document.querySelector('[data-open-platform-docs="v1"]'));
      const rootSelector=docs ? '[data-open-platform-docs="v1"]' : '[data-open-platform-host="v1"]';
      const headerSelector=docs ? '.open-platform-docs-header' : '.open-platform-header';
      const titleSelector=docs ? '.open-platform-docs-title' : '.open-platform-header h1';
      const title=document.querySelector(titleSelector);
      return {docs,side:box('.side'),stage:box('#stage'),root:box(rootSelector),header:box(headerSelector),title:box(titleSelector),titleText:String(title?.textContent || '').trim(),headers:document.querySelectorAll(headerSelector).length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1};
    })()`);
    const commonInvalid = !layout.side || !layout.stage || !layout.root || !layout.header || !layout.title || !layout.titleText || layout.headers !== 1 || layout.overflow || Math.abs(layout.side.right-layout.stage.left) > 1 || Math.abs(layout.stage.top) > 1 || layout.stage.paddingLeft !== "0px" || layout.stage.paddingTop !== "0px" || layout.root.top + 1 < layout.stage.top || layout.root.left + 1 < layout.stage.left || layout.root.right > layout.stage.right + 1 || layout.header.left + 1 < layout.root.left || layout.header.right > layout.root.right + 1 || layout.header.height < 48;
    if (commonInvalid) throw new Error(label + " static topbar/sidebar geometry invalid");
    if (layout.docs) return;
    if (Math.abs(layout.root.left-layout.stage.left) > 1 || Math.abs(layout.root.top-layout.stage.top) > 1 || Math.abs(layout.header.left-layout.stage.left) > 1 || Math.abs(layout.header.top-layout.stage.top) > 1 || layout.root.paddingLeft !== "0px" || layout.root.paddingTop !== "0px" || Math.abs(layout.header.right-layout.stage.right) > 1) throw new Error(label + " caller management geometry invalid");
  };
  const navigateStaticHost = async (pathname, ready, label) => {
    currentStep = label;
    try {
      await cdp.call("Page.navigate", { url: baseURL + pathname });
      await waitFor(cdp, `location.pathname === ${JSON.stringify(pathname.split("?")[0])} && document.readyState !== 'loading'`, label + " did not navigate");
      await waitFor(cdp, ready, label + " V3 Host did not become ready");
      await assertStaticOpenLayout(label);
      return true;
    } catch (error) {
      await recordRouteFailure(label, error);
      return false;
    }
  };

  const assertRuntimeConfigLayout = async label => {
    await assertLayout("standard", label, embeddedTitle);
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); const style=getComputedStyle(node); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height,paddingLeft:style.paddingLeft,paddingTop:style.paddingTop}; };
      const root=document.querySelector('[data-runtime-release-host]');
      const card=root?.querySelector('.admin-card');
      const title=card ? [...card.querySelectorAll('h2')].find(node => String(node.textContent || '').trim().length > 0) : null;
      return {root:box('[data-runtime-release-host]'),card:box('[data-runtime-release-host] .admin-card'),title:box('[data-runtime-release-host] .admin-card h2'),topbar:box('.admin-topbar'),headers:document.querySelectorAll('header.admin-topbar').length,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,titleText:String(title?.textContent || '').trim()};
    })()`);
    if (!layout.root || !layout.card || !layout.title || !layout.titleText || !layout.topbar || layout.headers !== 1 || layout.overflow || layout.root.top + 1 < layout.topbar.bottom || layout.card.top + 1 < layout.root.top || layout.card.left + 1 < layout.root.left) throw new Error(label + " V3 topbar/release-card geometry invalid");
  };
  const navigateRuntimeConfig = async () => {
    currentStep = "runtime-config";
    try {
      await cdp.call("Page.navigate", { url: baseURL + "/admin/config/releases" });
      await waitFor(cdp, "location.pathname === '/admin/config/releases' && document.readyState !== 'loading'", "runtime config did not navigate");
      await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-host] .admin-card h2')) && document.body?.textContent?.includes('当前运行时配置')", "runtime config Host did not become ready");
      await waitForFonts("runtime-config");
      await recordGeometry("runtime-config", () => assertRuntimeConfigLayout("runtime-config"), true);
      return true;
    } catch (error) {
      await recordRouteFailure("runtime-config", error);
      return false;
    }
  };

  // These are route-level desktop acceptance captures, not a second page
  // harness. Each route stays on the composed Access session and its existing
  // Owner Host, then proves the requested viewport, shell geometry, a visible
  // page-specific operation or seeded fact, and no horizontal overflow.
  const captureDesktopEvidence = async ({ label, pathname, ready, kind, titleSelector, assertPage, expectedResponse = '', finalPath = pathname, assertShell = true }) => {
    for (const width of [1280, 1440]) {
      await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
      const step = `${label}-desktop-${width}`;
      try {
        const responseStart = responses.length;
        await cdp.call("Page.navigate", { url: baseURL + pathname });
        await waitFor(cdp, `location.pathname === ${JSON.stringify(finalPath.split("?")[0])} && document.readyState !== 'loading'`, step + " did not navigate");
        await waitFor(cdp, ready, step + " Host did not become ready");
        await waitForFonts(step);
        if (assertShell) await assertLayout(kind, step, titleSelector);
        const evidence = await evaluate(cdp, assertPage);
        if (!evidence?.ready || evidence.width !== width || evidence.overflow) throw new Error(step + " visible page evidence invalid " + JSON.stringify(evidence));
        if (expectedResponse && !responses.slice(responseStart).includes(expectedResponse)) throw new Error(step + " did not complete expected Owner read " + expectedResponse);
        await capture(step);
      } catch (error) {
        await recordRouteFailure(step, error);
      }
    }
    await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  };

  const assertConfigCenterLayout = async label => {
    await assertLayout("standard", label, "[data-runtime-release-host] .cc-card-h h2");
    const layout = await evaluate(cdp, `(() => {
      const box = selector => { const node=document.querySelector(selector); if (!node) return null; const rect=node.getBoundingClientRect(); return {left:rect.left,top:rect.top,right:rect.right,bottom:rect.bottom,width:rect.width,height:rect.height}; };
      const root=document.querySelector('[data-runtime-release-host].cc-page');
      const card=root?.querySelector('.cc-card');
      const title=card?.querySelector('.cc-card-h h2');
      const table=card?.querySelector('.cc-category-table');
      const headings=table ? Array.from(table.querySelectorAll('thead th')).map(node => String(node.textContent || '').trim()) : [];
      return {root:box('[data-runtime-release-host].cc-page'),card:box('[data-runtime-release-host].cc-page .cc-card'),title:box('[data-runtime-release-host].cc-page .cc-card-h h2'),table:box('[data-runtime-release-host].cc-page .cc-category-table'),topbar:box('.admin-topbar'),headers:document.querySelectorAll('header.admin-topbar').length,headings,rows:table?.querySelectorAll('tbody [data-category-row]').length || 0,overflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,titleText:String(title?.textContent || '').trim()};
    })()`);
    const expectedHeadings = ["类目", "是否生效", "生效开关", "配置"];
    if (!layout.root || !layout.card || !layout.title || !layout.table || layout.titleText !== "配置类目" || !layout.topbar || layout.headers !== 1 || layout.overflow || layout.rows !== 6 || layout.headings.length !== expectedHeadings.length || layout.headings.some((heading, index) => heading !== expectedHeadings[index]) || layout.root.top + 1 < layout.topbar.bottom || layout.card.top + 1 < layout.root.top || layout.table.top + 1 < layout.card.top || layout.card.left + 1 < layout.root.left) throw new Error(label + " V3 topbar/config-center-card geometry invalid");
  };
  const navigateConfigCenter = async () => {
    currentStep = "config";
    try {
      await cdp.call("Page.navigate", { url: baseURL + "/admin/config" });
      await waitFor(cdp, "location.pathname === '/admin/config' && document.readyState !== 'loading'", "config center did not navigate");
      await waitFor(cdp, "Boolean(document.querySelector('[data-runtime-release-host].cc-page .cc-category-table')) && document.querySelectorAll('[data-runtime-release-host] .cc-category-table thead th').length === 4 && document.querySelectorAll('[data-runtime-release-host] [data-category-row]').length === 6", "config center Host did not become ready");
      await waitForFonts("config");
      await recordGeometry("config", () => assertConfigCenterLayout("config"), true);
      return true;
    } catch (error) {
      await recordRouteFailure("config", error);
      return false;
    }
  };

  const initial = "/admin/automation-conversion";
  currentStep = "automation";
  await cdp.call("Page.navigate", { url: baseURL + "/login?next=" + encodeURIComponent(initial) });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin/automation-conversion'", "login did not establish the Access session");
  await waitFor(cdp, "Boolean(document.querySelector('.admin-topbar')) && Boolean(document.querySelector('.aud-layout'))", "automation shell did not load");
  await waitForFonts("automation");
  await recordGeometry("automation", () => assertLayout("standard", "automation", embeddedTitle), true);

  // The matrix follows every actual item in ADMIN_NAV_GROUPS. Embedded rows
  // retain a source-backed inner bar; native hosts retain the one Webshell
  // title and their standard content inset. An empty Host cannot pass either.
  await navigate("/admin/operation-cycles", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "cycles", "embedded", embeddedTitle, true, true);
  await recordGeometry("cycles-padding-regression-control", () => assertInsetRegressionRejected("cycles", embeddedTitle), false);
  await navigateGroupOps("/admin/automation-conversion/group-ops/ui", "groupops", true, true, "/admin/groupops.html");
  const channelsMounted = await navigateStandard("/admin/channels", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"channel-center\"] a[href=\"/admin/channels/new\"]')) && Boolean(document.querySelector('input[aria-label=\"搜索渠道名称\"]')) && Boolean(document.querySelector('#stage table'))", "channels", true, true);
  if (channelsMounted) await recordGeometry("channels", async () => {
    const channel = await evaluate(cdp, `(() => { const topbar=document.querySelector('.admin-topbar'); const title=topbar?.querySelector('.admin-page-title'); const action=topbar?.querySelector('[data-page-header-actions="channel-center"] a[href="/admin/channels/new"]'); const stage=document.querySelector('#stage'); const listHeading=Array.from(stage?.querySelectorAll('h2') || []).some(node => String(node.textContent || '').trim() === '渠道码列表'); const description=stage?.textContent?.includes('渠道码中心只管理渠道资产、渠道用户、欢迎语、标签和客服分配。'); const search=stage?.querySelector('input[aria-label="搜索渠道名称"]'); const rows=stage?.querySelectorAll('tbody tr').length || 0; return {headers:document.querySelectorAll('header.admin-topbar').length,title:String(title?.textContent || '').trim(),action:Boolean(action),listHeading,description,search:Boolean(search),rows}; })()`);
    if (channel.headers !== 1 || channel.title !== "渠道码中心" || !channel.action || channel.listHeading || channel.description || !channel.search) throw new Error("channels topbar/filter/table layout invalid");
  }, true);
  if (channelsMounted) await captureHeaderWidths("channel-center", "channels-header");
  await navigateAIAssistant("/admin/cloud-orchestrator/plans", "ai", "Boolean(document.querySelector('#stage.admin-workspace-stage--dynamic [data-cloud-plan-root] .cloud-plan-toolbar [data-plan-refresh]')) && document.querySelector('[data-plan-list]')?.textContent?.includes('AI layout detail fixture')", false, true);
  const aiDetailMounted = await navigateAIAssistant("/admin/cloud-orchestrator/plans/" + aiPlanID, "ai-detail", "Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"ai-plan-detail\"] [data-plan-approve]')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"ai-plan-detail\"] [data-plan-reject]')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"ai-plan-detail\"] a[href=\"/admin/cloud-orchestrator/plans\"]')) && document.querySelector('[data-plan-detail-state]')?.textContent?.trim().length > 0 && document.querySelector('[data-plan-name]')?.textContent?.includes('AI layout detail fixture')", true);
  if (aiDetailMounted) await assertHeaderActionWidths("ai-detail", "ai-plan-detail", ["返回一级页", "拒绝计划", "确认并发送"]);
  await navigateStandard("/admin/customers", "Boolean(document.querySelector('[data-customer-directory-root]'))", "customers", true, true);
  const hxcMounted = await navigate("/admin/hxc-dashboard", "Boolean(document.querySelector('#hxcRefresh')) && Boolean(document.querySelector('.sec-funnel')) && document.querySelectorAll('.sec-funnel .dw-card').length > 1", "hxc", "standard", ".dw-cards", false, true);
  if (hxcMounted) await recordGeometry("hxc", () => assertHXCLayout("hxc"), true);
  await navigate("/admin/questionnaires", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "questionnaires", "embedded", questionnaireTitle, true, true);
  const radarMounted = await navigateStandard("/admin/radar-links", "Boolean(document.querySelector('#stage.labs.sec-radar #listRows')) && Boolean(document.querySelector('.admin-topbar [data-page-header-actions=\"radar-list\"] a[href=\"/admin/radarForm.html\"]'))", "radar", true, true);
  if (radarMounted) await recordGeometry("radar", () => assertRadarListLayout("radar"), true);
  if (radarMounted) await captureHeaderWidths("radar-list", "radar-header");
  const radarNumericID = Number(radarID);
  const radarDetailMounted = await navigate("/admin/radarDetail.html?id=" + encodeURIComponent(String(radarNumericID)), "Boolean(document.querySelector('#stage.labs.sec-radar')) && Boolean(document.querySelector('#dEdit')) && Boolean(document.querySelector('[data-v3-radar-visitor-host]')) && document.querySelector('[data-v3-radar-visitor-host]')?.textContent?.includes('雷达布局访客') && document.querySelector('[data-v3-radar-visitor-host]')?.textContent?.includes('2026-09-07 09:02:03')", "radar-detail", "standard", "#dEdit", false);
  if (radarDetailMounted) {
    await recordGeometry("radar-detail", async () => { await assertRadarLayout("radar-detail", "#dEdit", "#dEdit"); await assertRadarDetailVisitorsHost(); }, true);
    await recordGeometry("radar-detail-search-export", assertRadarDetailVisitorSearchAndExport, false);
    await recordGeometry("radar-detail-narrow", assertRadarDetailNarrow, false);
  }
  const radarFormMounted = await navigate("/admin/radarForm.html", "Boolean(document.querySelector('#stage.labs.sec-radar')) && Boolean(document.querySelector('#fSave'))", "radar-form", "standard", "#fSave", false);
  if (radarFormMounted) await recordGeometry("radar-form", () => assertRadarLayout("radar-form", "#fSave", "#fSave"), true);
  await navigateTags();

  await navigate("/admin/orders", "Boolean(document.querySelector('.order-host-layout')) && Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "orders", "embedded", embeddedTitle, true, true);
  await navigate("/admin/wechat-pay/products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table'))", "products", "standard", "table", true, true);
  await navigate("/admin/service-period-products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table'))", "service-period-products", "standard", "table", true, true);
  const assertProductListPresentation = async (page, label) => {
    const presentation = await evaluate(cdp, `(() => {
      const title = ${JSON.stringify(page === 'products' ? '商品管理' : '周期商品管理')};
      const create = ${JSON.stringify(page === 'products' ? '创建商品' : '创建周期商品')};
      const topbar = document.querySelector('.admin-topbar');
      const shellTitle = topbar?.querySelector('.admin-page-title');
      const actionHost = topbar?.querySelector('[data-page-header-actions="product-list-' + ${JSON.stringify(page)} + '"]');
      const button = actionHost ? [...actionHost.querySelectorAll('button')].find(node => String(node.textContent || '').trim() === create) : undefined;
      const donorTitles = [...document.querySelectorAll('#stage div')].filter(node => node.children.length === 0 && String(node.textContent || '').trim() === title);
      const menu = document.querySelector('[data-table-action-menu-owner^="product-' + ${JSON.stringify(page)} + '-"]');
      const trigger = document.querySelector('[data-table-action-menu-trigger^="product-' + ${JSON.stringify(page)} + '-"]');
      const row = document.querySelector('tbody tr');
      const status = row?.children[3]?.textContent?.trim() || '';
      const updated = row?.children[5]?.textContent?.trim() || '';
      const topbarBox = topbar?.getBoundingClientRect();
      const createBox = button?.getBoundingClientRect();
      return { shellTitle:String(shellTitle?.textContent || '').trim(), shellTitleCount:document.querySelectorAll('.admin-topbar .admin-page-title').length, donorTitleCount:donorTitles.length, createInTopbar:Boolean(topbarBox && createBox && createBox.top >= topbarBox.top - 1 && createBox.bottom <= topbarBox.bottom + 1), menu:menu instanceof HTMLElement, triggerVisible:trigger instanceof HTMLElement && trigger.getBoundingClientRect().width > 1, bodyOverflow:document.documentElement.scrollWidth > document.documentElement.clientWidth + 1, status, updated };
    })()`);
    const invalid = presentation.shellTitle !== (page === 'products' ? '商品管理' : '周期商品管理') || presentation.shellTitleCount !== 1 || presentation.donorTitleCount !== 0 || !presentation.createInTopbar || !presentation.menu || !presentation.triggerVisible || presentation.bodyOverflow || ((page === 'spProducts' && presentation.status !== '已启用') || /T\d{2}:\d{2}/.test(presentation.updated));
    if (invalid) throw new Error(label + ' product list presentation invalid ' + JSON.stringify(presentation));
  };
  for (const width of [1440, 1280]) {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
    await navigate("/admin/wechat-pay/products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table'))", `products-actions-${width}`, "standard", "table", true);
    await assertProductListPresentation('products', `products-actions-${width}`);
    await navigate("/admin/service-period-products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table'))", `service-period-products-actions-${width}`, "standard", "table", true);
    await assertProductListPresentation('spProducts', `service-period-products-actions-${width}`);
  }
  const assertProductMenuAtViewportEdge = async (width) => {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 320, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 320 });
    await navigate("/admin/wechat-pay/products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table'))", `products-actions-edge-${width}`, "standard", "table", false);
    const marked = await evaluate(cdp, `(() => {
      const trigger = [...document.querySelectorAll('[data-table-action-menu-trigger^="product-products-"]')].at(-1);
      if (!(trigger instanceof HTMLButtonElement)) return false;
      trigger.scrollIntoView({ block: 'end' });
      trigger.setAttribute('data-aicrm-product-edge-menu', 'true');
      return true;
    })()`);
    if (!marked) throw new Error(`product lower-edge overflow trigger is unavailable at ${width}`);
    // The mark above deliberately scrolls this real table-row trigger to the
    // lower viewport edge. Do not let the generic click helper re-center it:
    // that would test a middle-of-viewport menu instead of the upward branch.
    await pointerClick('[data-aicrm-product-edge-menu=true]', `product lower-edge overflow trigger at ${width}`, { preservePosition: true });
    await waitFor(cdp, "document.querySelector('[data-aicrm-product-edge-menu=true]')?.getAttribute('aria-expanded') === 'true'", `product lower-edge overflow did not open at ${width}`);
    const edge = await evaluate(cdp, `(() => {
      const trigger = document.querySelector('[data-aicrm-product-edge-menu=true]');
      const panel = trigger instanceof HTMLButtonElement ? document.getElementById(trigger.getAttribute('aria-controls') || '') : null;
      if (!(panel instanceof HTMLElement) || !(trigger instanceof HTMLElement)) return { missing: true };
      const style = getComputedStyle(panel);
      const rect = panel.getBoundingClientRect();
      return {
        missing: false,
        visible: style.display !== 'none' && style.visibility !== 'hidden' && rect.width > 1 && rect.height > 1,
        placement: panel.dataset.tableActionMenuPlacement || '',
        inViewport: rect.top >= -1 && rect.bottom <= innerHeight + 1,
        triggerBottom: trigger.getBoundingClientRect().bottom,
        viewportHeight: innerHeight,
        belowTight: innerHeight - trigger.getBoundingClientRect().bottom - 8 - rect.height < 16,
        scrollable: panel.scrollHeight >= panel.clientHeight,
      };
    })()`);
    if (edge.missing || !edge.visible || edge.placement !== 'up' || !edge.inViewport || !edge.belowTight || !edge.scrollable) {
      throw new Error(`product lower-edge overflow presentation invalid at ${width}: ${JSON.stringify(edge)}`);
    }
    await capture(`products-actions-edge-${width}`);
    await waitFor(cdp, `(() => {
      const trigger=document.querySelector('[data-aicrm-product-edge-menu=true]');
      const panel=trigger instanceof HTMLButtonElement ? document.getElementById(trigger.getAttribute('aria-controls') || '') : null;
      return panel instanceof HTMLElement && panel.contains(document.activeElement);
    })()`, `product lower-edge overflow did not move keyboard focus into its menu at ${width}`);
    let tabLeftMenu = false;
    for (let attempt = 0; attempt < 5 && !tabLeftMenu; attempt += 1) {
      // Exercise the normal forward Tab path: it moves across the remaining
      // overflow controls and then leaves the detached panel.
      await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Tab", code: "Tab" });
      await cdp.call("Input.dispatchKeyEvent", { type: "keyUp", key: "Tab", code: "Tab" });
      await twoAnimationFrames();
      tabLeftMenu = await evaluate(cdp, `(() => {
        const trigger=document.querySelector('[data-aicrm-product-edge-menu=true]');
        const panel=trigger instanceof HTMLButtonElement ? document.getElementById(trigger.getAttribute('aria-controls') || '') : null;
        return trigger instanceof HTMLElement && panel instanceof HTMLElement && !trigger.contains(document.activeElement) && !panel.contains(document.activeElement);
      })()`);
    }
    if (!tabLeftMenu) throw new Error(`product lower-edge overflow keyboard Tab did not leave its action cluster at ${width}`);
    await waitFor(cdp, "document.querySelector('[data-aicrm-product-edge-menu=true]')?.getAttribute('aria-expanded') === 'false'", `product lower-edge overflow did not close after keyboard Tab exit at ${width}`);
    await pointerClick('[data-aicrm-product-edge-menu=true]', `product lower-edge overflow trigger reopen at ${width}`, { preservePosition: true });
    await waitFor(cdp, "document.querySelector('[data-aicrm-product-edge-menu=true]')?.getAttribute('aria-expanded') === 'true'", `product lower-edge overflow did not reopen for Escape at ${width}`);
    await cdp.call("Input.dispatchKeyEvent", { type: "keyDown", key: "Escape", code: "Escape" });
    await waitFor(cdp, `(() => { const trigger=document.querySelector('[data-aicrm-product-edge-menu=true]'); return trigger instanceof HTMLButtonElement && trigger.getAttribute('aria-expanded') === 'false' && document.activeElement === trigger; })()`, `product lower-edge overflow did not close on Escape and restore trigger focus at ${width}`);
    await evaluate(cdp, 'window.scrollTo(0, 0)');
  };
  for (const width of [1440, 1280]) await assertProductMenuAtViewportEdge(width);
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  const assertProductDimensions = async (prefix) => {
    const result = await evaluate(cdp, `(${function(prefix) {
      const ids = ['sale', 'media', 'action', 'wecom', 'push'].map(key => `${prefix}-${key}`);
      const input = document.getElementById(prefix === 'product' ? 'pfName' : 'spfName');
      const original = input.value; input.value = '未保存维度草稿';
      const visible = () => ids.filter(id => { const node = document.getElementById(id); return node && getComputedStyle(node).display !== 'none'; });
      const failures = [];
      for (const id of ids) {
        document.querySelector(`a[href="#${id}"]`)?.click();
        if (visible().join() !== id || input.value !== '未保存维度草稿') failures.push(id);
      }
      input.value = original; document.querySelector(`a[href="#${prefix}-sale"]`)?.click();
      return { failures, action: Boolean(document.querySelector(`#${prefix}-action [data-product-purchase-enabled]`)), tags: Boolean(document.querySelector(`#${prefix}-wecom [data-product-tag-open]`)) };
    }.toString()})(${JSON.stringify(prefix)})`);
    if (result.failures.length || !result.action || !result.tags) throw new Error(`product dimension switching: ${JSON.stringify(result)}`);
  };
  await navigate("/admin/productForm.html?id=" + productID, "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('#pfExternalPushEnabled')) && Boolean(document.querySelector('a[href=\"#product-sale\"][aria-current=\"step\"]'))", "product", "standard", embeddedTitle, true);
  await assertProductDimensions('product');
  await navigate("/admin/spProductForm.html?id=" + serviceProductID, "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded')) && Boolean(document.querySelector('#spfExternalPushEnabled')) && Boolean(document.querySelector('a[href=\"#sp-sale\"][aria-current=\"step\"]'))", "service-period-product", "standard", embeddedTitle, true);
  await assertProductDimensions('sp');
  currentStep = 'products-delete-menu';
  await navigate("/admin/wechat-pay/products", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table'))", "products-delete-menu", "standard", "table", false);
  const deleteTarget = await evaluate(cdp, `(() => {
    const row = [...document.querySelectorAll('#stage tbody tr')].find(node => node.textContent?.includes('admin-layout-delete-menu'));
    const trigger = row?.querySelector('[data-table-action-menu-trigger^="product-products-"]');
    return trigger instanceof HTMLElement ? 'admin-layout-delete-menu' : '';
  })()`);
  if (!deleteTarget) throw new Error('product delete fixture or overflow trigger is unavailable');
  const productDeleteRequest = 'DELETE /api/admin/wechat-pay/products/' + archiveProductID;
  requestEvents.length = 0;
  responses.length = 0;
  const markDeleteTrigger = async () => evaluate(cdp, `(() => {
    document.querySelectorAll('[data-aicrm-product-menu-trigger]').forEach(node => node.removeAttribute('data-aicrm-product-menu-trigger'));
    const row = [...document.querySelectorAll('#stage tbody tr')].find(node => node.textContent?.includes('admin-layout-delete-menu'));
    const trigger = row?.querySelector('[data-table-action-menu-trigger^="product-products-"]');
    if (!(trigger instanceof HTMLButtonElement)) return false;
    trigger.setAttribute('data-aicrm-product-menu-trigger', 'true');
    return true;
  })()`);
  const markDelete = async () => evaluate(cdp, `(() => {
    document.querySelectorAll('[data-aicrm-product-delete]').forEach(node => node.removeAttribute('data-aicrm-product-delete'));
    const trigger = document.querySelector('[data-aicrm-product-menu-trigger="true"]');
    const panel = trigger instanceof HTMLButtonElement ? document.getElementById(trigger.getAttribute('aria-controls') || '') : null;
    const target = [...(panel?.querySelectorAll('button') || [])].find(node => node.textContent?.trim() === '删除');
    if (!(target instanceof HTMLButtonElement)) return false;
    target.setAttribute('data-aicrm-product-delete', 'true');
    target.addEventListener('click', () => { document.documentElement.dataset.aicrmProductDeletePointer = String(Number(document.documentElement.dataset.aicrmProductDeletePointer || '0') + 1); }, { once: true });
    return !target.disabled;
  })()`);
  if (!await markDeleteTrigger()) throw new Error('product delete fixture trigger is unavailable');
  await pointerClick('[data-aicrm-product-menu-trigger="true"]', 'product overflow trigger');
  await waitFor(cdp, "Boolean(document.querySelector('[data-aicrm-product-menu-trigger=\"true\"]')?.getAttribute('aria-expanded') === 'true')", 'product overflow menu did not open');
  if (!await markDelete()) throw new Error('product delete action is absent from the visible overflow menu');
  // The menu action is already visible in the fixed panel. Scrolling it can
  // trigger the panel's viewport-position listener between hit testing and the
  // CDP event, so retain its verified in-panel position.
  await pointerClick('[data-aicrm-product-delete="true"]', 'product delete action', { preservePosition: true });
  await waitFor(cdp, "document.documentElement.dataset.aicrmProductDeletePointer === '1'", 'product delete action did not receive the real pointer click');
  await waitFor(cdp, "document.querySelector('#fb-mask')?.hidden === false && Boolean(document.querySelector('#fb-cancel'))", 'product delete confirmation did not open');
  await capture('products-delete-confirm');
  await pointerClick('#fb-cancel', 'product delete cancellation');
  if (requestEvents.some(value => value === productDeleteRequest)) throw new Error('cancelled product deletion issued a write');
  if (!await markDeleteTrigger()) throw new Error('product delete fixture trigger disappeared after cancellation');
  await pointerClick('[data-aicrm-product-menu-trigger="true"]', 'product overflow trigger after cancellation');
  await waitFor(cdp, "Boolean(document.querySelector('[data-aicrm-product-menu-trigger=\"true\"]')?.getAttribute('aria-expanded') === 'true')", 'product overflow menu did not reopen');
  if (!await markDelete()) throw new Error('product delete action disappeared after cancellation');
  await pointerClick('[data-aicrm-product-delete="true"]', 'product delete confirmation action', { preservePosition: true });
  await waitFor(cdp, "document.documentElement.dataset.aicrmProductDeletePointer === '2'", 'product delete action did not receive the second real pointer click');
  await waitFor(cdp, "document.querySelector('#fb-mask')?.hidden === false && Boolean(document.querySelector('#fb-ok'))", 'product delete confirmation could not reopen');
  await pointerClick('#fb-ok', 'product delete confirmation submit');
  await waitForRecorded(requestEvents, value => value === productDeleteRequest, 'confirmed product deletion did not issue the owner DELETE');
  await waitForRecorded(responses, value => value.startsWith(productDeleteRequest + ':'), 'confirmed product deletion did not settle');
  await waitFor(cdp, `(() => ![...document.querySelectorAll('#stage tbody tr')].some(row => row.textContent?.includes(${JSON.stringify(deleteTarget)})))()`, 'product owner readback still shows the deleted row');
  if (requestEvents.filter(value => value === productDeleteRequest).length !== 1) throw new Error('product deletion issued more than one owner write');
  await navigate("/admin/coupons", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "coupons", "embedded", embeddedTitle, true, true);

  await navigateMaterialWorkspace('images', 'materials-images', '上传图片', "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded[data-material-library-workspace=\"true\"][data-image-library-v3-root][data-image-library-host-mounted=\"true\"] [data-image-library-cards]'))", '/api/admin/image-library', '素材工作台横向缩略图');
  await navigateMaterialWorkspace('miniprograms', 'materials-miniprograms', '新建小程序卡片', "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded[data-material-library-workspace=\"true\"][data-material-library-presentation-mounted=\"true\"] [data-material-library-tabs]')) && Boolean(document.querySelector('#fMpQuery'))", '/api/admin/miniprogram-library', 'wx_material_layout');
  await assertMaterialHeaderActionOpens('material-library-mpLib', '新建小程序卡片', '#fMpAppid', 'materials-miniprograms-create');
  await assertMiniProgramCommittedSearchAndSecondPage();
  await navigateMaterialWorkspace('attachments', 'materials-attachments', '上传附件', "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded[data-material-library-workspace=\"true\"][data-material-library-presentation-mounted=\"true\"] [data-material-library-tabs]')) && Boolean(document.querySelector('input[data-material-library-query=\"attachment\"]'))", '/api/admin/attachment-library', '素材工作台附件');
  await assertMaterialHeaderActionOpens('material-library-attach', '上传附件', '#fAttUpFile', 'materials-attachments-upload');
  await assertMaterialAlias('/admin/image-library', 'images', "Boolean(document.querySelector('[data-image-library-host-mounted=\"true\"]'))", 'materials-image-alias');
  await assertMaterialAlias('/admin/images.html', 'images', "Boolean(document.querySelector('[data-image-library-host-mounted=\"true\"]'))", 'materials-image-html-alias');
  await assertMaterialAlias('/admin/attachment-library', 'attachments', "Boolean(document.querySelector('input[data-material-library-query=\"attachment\"]'))", 'materials-attachment-alias');
  await assertMaterialAlias('/admin/attach.html', 'attachments', "Boolean(document.querySelector('input[data-material-library-query=\"attachment\"]'))", 'materials-attachment-html-alias');
  await assertMaterialAlias('/admin/miniprogram-library', 'miniprograms', "Boolean(document.querySelector('#fMpQuery'))", 'materials-miniprogram-alias');
  await assertMaterialAlias('/admin/mpLib.html', 'miniprograms', "Boolean(document.querySelector('#fMpQuery'))", 'materials-miniprogram-html-alias');

  await navigate("/admin/automation-agents", "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded'))", "automation-agents", "embedded", embeddedTitle, true, true);
  const ownerMounted = await navigate("/admin/owner-migration", "Boolean(document.querySelector('[data-owner-handoff-host][data-owner-handoff-init=\"ready\"]')) && Boolean(document.querySelector('[data-owner-migration-page] .owner-migration-status-bar')) && Boolean(document.querySelector('[data-owner-migration-page] [data-owner-picker=\"source\"]'))", "owner-migration", "standard", "[data-owner-picker=\"source\"]", false, true);
  if (ownerMounted) await recordGeometry("owner-migration", () => assertOwnerHandoffLayout("owner-migration"), true);
  await navigateConfigCenter();
  await navigateRuntimeConfig();
  const removedOneID = await evaluate(cdp, `(async () => ({
    menuPresent: Boolean(document.querySelector('a[href="/admin/oneid"], a[href="/admin/oneid.html"]')),
    statuses: await Promise.all(['/admin/oneid','/admin/oneid.html'].map(async path => (await fetch(path, {credentials:'same-origin'})).status))
  }))()`);
  if (removedOneID.menuPresent || removedOneID.statuses.some(status => status !== 404)) {
    interactionFailures.push("oneid:retired frontend entry remains accessible");
  }
  currentStep = "api-docs";
  await clickNavigation("/admin/api-docs", "api-docs");
  await waitFor(cdp, "location.pathname === '/admin/apidocs.html' && document.readyState !== 'loading'", "api-docs did not canonicalize to its V3 Host document");
  // API docs are the default surface. They must be useful before the
  // super-admin-only management requests are made.
  await waitFor(cdp, `(() => {
    const root=document.querySelector('[data-open-platform-docs="v1"]');
    const title=root?.querySelector('.open-platform-docs-title');
    const management=root?.querySelector('button[data-open-platform-action="密钥管理"]');
    const rows=root?.querySelectorAll('#operations tbody tr') || [];
    return Boolean(root && title?.textContent?.trim() === 'AI-CRM 外部只读 API v1' && management && rows.length === 11);
  })()`, "api-docs default document did not become ready");
  await waitForFonts("api-docs");
  await recordGeometry("api-docs", () => assertStaticOpenLayout("api-docs"), true);
  const managementOpened = await evaluate(cdp, `(() => {
    const button=document.querySelector('[data-open-platform-docs="v1"] button[data-open-platform-action="密钥管理"]');
    if (!(button instanceof HTMLButtonElement)) return false;
    button.click();
    return true;
  })()`);
  if (!managementOpened) throw new Error("api-docs visible key-management entry was unavailable");
  // Keep the prior caller-management readiness assertion after the explicit
  // user transition so this geometry journey still covers the old controls.
  await waitFor(cdp, `(() => {
    const root=document.querySelector('[data-open-platform-host="v1"]');
    const title=root?.querySelector('.open-platform-header h1');
    const refresh=root?.querySelector('button[data-open-platform-action="刷新"]');
    const catalog=Array.from(root?.querySelectorAll('.open-platform-catalog') || []).find(node => node.querySelector('h2')?.textContent?.trim() === 'V1 能力目录');
    const rows=catalog?.querySelectorAll('tbody tr') || [];
    return Boolean(root && title?.textContent?.trim() === '开放平台调用方' && refresh && rows.length > 0);
  })()`, "api-docs key management did not become ready");
  await assertStaticOpenLayout("api-docs-key-management");

  // Remaining release-matrix desktop evidence. These intentionally read only
  // existing Owner surfaces; no visible button below is clicked when it would
  // create, publish, save, refresh or call a Provider.
  await captureDesktopEvidence({
    label: "coupons", pathname: "/admin/coupons",
    ready: "Boolean(document.querySelector('#stage table tbody tr')) && document.querySelector('#stage')?.textContent?.includes('后台页面验收优惠券')",
    kind: "embedded", titleSelector: frozenListToolbarTitle,
    assertPage: `(() => ({ready:Boolean(document.querySelector('#stage button')) && Array.from(document.querySelectorAll('#stage tbody tr')).some(row => row.textContent?.includes('后台页面验收优惠券')) && Array.from(document.querySelectorAll('#stage tbody tr')).some(row => row.textContent?.includes('数据')),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  await captureDesktopEvidence({
    label: "coupon-form", pathname: "/admin/couponForm.html?id=" + couponID,
    ready: `(() => { const stage=document.querySelector('#stage'); const amount=document.querySelector('#couponAmount'); const limit=document.querySelector('#couponIssueLimit'); const start=document.querySelector('#couponClaimStart'); const end=document.querySelector('#couponClaimEnd'); return Boolean(stage?.textContent?.includes('后台页面验收优惠券') && Array.from(document.querySelectorAll('#stage button')).some(button => button.textContent?.trim() === '保存优惠券') && amount instanceof HTMLInputElement && amount.value === '12.00' && limit instanceof HTMLInputElement && limit.value === '100' && stage.textContent?.replace(/\\s/g,'').includes('已选1个商品') && start instanceof HTMLInputElement && Boolean(start.value) && end instanceof HTMLInputElement && Boolean(end.value)); })()`,
    kind: "embedded", titleSelector: "#couponForm h2", assertShell: false,
    expectedResponse: "GET /api/admin/coupons/" + couponID + ":200",
    assertPage: `(() => { const text=String(document.querySelector('#stage')?.textContent || ''); const compact=text.replace(/\\s/g,''); const title=document.querySelector('#stage .coupon-editor-title-row h2'); const amount=document.querySelector('#couponAmount'); const limit=document.querySelector('#couponIssueLimit'); const start=document.querySelector('#couponClaimStart'); const end=document.querySelector('#couponClaimEnd'); return {ready:title?.textContent?.trim() === '编辑优惠券' && document.querySelectorAll('#stage .coupon-editor-title-row h2').length === 1 && compact.includes('后台页面验收优惠券') && compact.includes('已选1个商品') && amount instanceof HTMLInputElement && amount.value === '12.00' && limit instanceof HTMLInputElement && limit.value === '100' && start instanceof HTMLInputElement && Boolean(start.value) && end instanceof HTMLInputElement && Boolean(end.value) && Boolean(document.querySelector('#stage #saveCoupon')),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}; })()`
  });
  await captureDesktopEvidence({
    label: "coupon-data", pathname: "/admin/couponData.html?id=" + couponID,
    ready: "Boolean(document.querySelector('#stage table tbody tr')) && document.querySelector('#stage')?.textContent?.includes('领取与使用明细')",
    kind: "embedded", titleSelector: frozenListToolbarTitle,
    assertPage: `(() => { const text=String(document.querySelector('#stage')?.textContent || ''); const rows=Array.from(document.querySelectorAll('#stage tbody tr')); return {ready:text.includes('累计领取') && text.includes('领取时间') && text.includes('已领取 / 发行量') && /累计领取\\s*1\\s*发行 100/.test(text) && text.includes('指定商品（1项）') && rows.length >= 1 && rows.some(row => row.textContent?.includes('可用') && !row.textContent?.includes('claimed')) && !text.includes('claimed') && !text.includes('published') && !text.includes('standard_product:1') && !text.includes('当前页暂无领取记录'),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}; })()`
  });
  await captureDesktopEvidence({
    label: "service-period-products", pathname: "/admin/service-period-products",
    ready: "Boolean(document.querySelector('#stage.admin-workspace-stage--embedded table')) && document.querySelector('#stage')?.textContent?.includes('浏览器周期外推商品')",
    kind: "standard", titleSelector: "table",
    assertPage: `(() => ({ready:document.querySelectorAll('.admin-topbar .admin-page-title').length === 1 && Boolean(document.querySelector('[data-page-header-actions="product-list-spProducts"] button')) && Boolean(document.querySelector('#stage tbody tr')),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  await captureDesktopEvidence({
    label: "member-grid", pathname: "/admin/spProductData.html?id=" + serviceProductID,
    ready: "document.querySelectorAll('.dw-card').length >= 4 && document.querySelector('[role=status]')?.textContent?.includes('共 1 人')",
    kind: "standard", titleSelector: ".dw-cards",
    assertPage: `(() => ({ready:document.querySelectorAll('.admin-topbar .admin-page-title').length === 1 && document.querySelector('.admin-topbar .admin-page-title')?.textContent?.includes('周期商品') && document.querySelectorAll('.dw-card').length >= 4 && document.querySelector('[role=status]')?.textContent?.includes('共 1 人'),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  await captureDesktopEvidence({
    label: "channels-new", pathname: "/admin/channels/new",
    ready: "Boolean(document.querySelector('[data-channel-admission-page]')) && Array.from(document.querySelectorAll('#stage button')).some(button => button.textContent?.trim() === '保存当前维度')",
    kind: "embedded", titleSelector: "[data-channel-admission-page] h1", assertShell: false,
    assertPage: `(() => ({ready:document.body.dataset.page === 'channelForm' && document.querySelectorAll('[data-channel-admission-page] h1').length === 1 && document.querySelector('[data-channel-admission-page] h1')?.textContent?.trim() === '渠道码中心' && Boolean(document.querySelector('[data-channel-admission-page] input')) && document.querySelector('[data-channel-admission-page]')?.textContent?.includes('基础配置') && Array.from(document.querySelectorAll('#stage button')).some(button => button.textContent?.trim() === '保存当前维度'),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  await captureDesktopEvidence({
    label: "external-effects", pathname: "/admin/campaigns.html?view=external-effects",
    ready: "Boolean(document.querySelector('#effects-refresh')) && Boolean(document.querySelector('#stage h2'))",
    kind: "standard", titleSelector: "#stage h2",
    assertPage: `(() => ({ready:document.querySelectorAll('.admin-topbar .admin-page-title').length === 1 && document.querySelector('#effects-refresh')?.textContent?.includes('刷新真实本地投影') && document.querySelector('#stage')?.textContent?.includes('External Effects runtime / diagnostics'),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  await captureDesktopEvidence({
    label: "owner-migration", pathname: "/admin/owner-migration",
    ready: "Boolean(document.querySelector('[data-owner-handoff-host][data-owner-handoff-init=\"ready\"]')) && Boolean(document.querySelector('[data-owner-picker=\"source\"]'))",
    kind: "standard", titleSelector: "[data-owner-picker=\"source\"]",
    assertPage: `(() => ({ready:document.querySelectorAll('.admin-topbar .admin-page-title').length === 1 && Boolean(document.querySelector('[data-owner-picker="source"]')) && Boolean(document.querySelector('[data-owner-picker="target"]')),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  await captureDesktopEvidence({
    label: "runtime-config", pathname: "/admin/config/releases",
    ready: "Boolean(document.querySelector('[data-runtime-release-host] .admin-card h2')) && document.body?.textContent?.includes('当前运行时配置')",
    kind: "standard", titleSelector: "[data-runtime-release-host] .admin-card h2",
    assertPage: `(() => ({ready:document.querySelectorAll('.admin-topbar .admin-page-title').length === 1 && Boolean(document.querySelector('[data-runtime-release-host] .admin-card h2')) && document.querySelector('[data-runtime-release-host]')?.textContent?.includes('当前运行时配置'),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`
  });
  for (const width of [1280, 1440]) {
    const step = `api-docs-desktop-${width}`;
    await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false, screenWidth: width, screenHeight: 900 });
    try {
      await cdp.call("Page.navigate", { url: baseURL + "/admin/api-docs" });
      await waitFor(cdp, "location.pathname === '/admin/apidocs.html' && Boolean(document.querySelector('[data-open-platform-docs=\"v1\"] #operations tbody tr'))", step + " did not render docs");
      await assertStaticOpenLayout(step);
      const evidence = await evaluate(cdp, `(() => ({ready:document.querySelectorAll('[data-open-platform-docs="v1"] #operations tbody tr').length === 11 && Boolean(document.querySelector('[data-open-platform-docs="v1"] button[data-open-platform-action="密钥管理"]')),width:innerWidth,overflow:document.documentElement.scrollWidth>innerWidth+1}))()`);
      if (!evidence?.ready || evidence.width !== width || evidence.overflow) throw new Error(step + " visible page evidence invalid " + JSON.stringify(evidence));
      await capture(step);
    } catch (error) {
      await recordRouteFailure(step, error);
    }
  }
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });

  // Detail and frozen aliases remain on their business Host, including the
  // order history panel whose source mapping is independently seeded below.
  await navigate("/admin/orderDetail.html?id=" + encodeURIComponent(historicalOrderReference), "Boolean(document.querySelector('.order-host-layout')) && Boolean(document.body?.textContent?.includes('外部处理记录'))", "order-detail-history", "embedded", embeddedTitle, true);
  await navigate("/admin/orderDetail.html?id=" + encodeURIComponent(nativeOrderReference), "Boolean(document.querySelector('.order-refund-confirmation')) && Boolean(document.body?.textContent?.includes('订单信息')) && Boolean(document.body?.textContent?.includes('匿名退款演示商品'))", "order-detail-native", "embedded", embeddedTitle, true);
  const nativeOrderPresentation = await evaluate(cdp, `(() => ({
    hasCanonicalCustomer: document.body.textContent.includes('用户编号') && [...document.querySelectorAll('[data-order-detail-grid] span')].some(cell => /^[1-9][0-9]{6}$/.test(cell.textContent.trim())),
    hasMaskedPhone: Boolean(document.body?.textContent?.includes('130****1234')),
    hasChinesePayment: Boolean(document.body?.textContent?.includes('微信支付')),
    hasChineseStatus: Boolean(document.body?.textContent?.includes('已支付')),
    hasRefundForm: Boolean(document.querySelector('.order-refund-confirmation .input[data-order-refund-amount]')) && Boolean(document.querySelector('.order-refund-confirmation .select[data-order-refund-reason]')) && Boolean(document.querySelector('.order-refund-confirmation .btn.primary')),
    hasRawInternalCustomerKey: Boolean(document.body?.textContent?.includes('customer:')),
  }))()`);
  if (!nativeOrderPresentation?.hasCanonicalCustomer || !nativeOrderPresentation?.hasMaskedPhone || !nativeOrderPresentation?.hasChinesePayment || !nativeOrderPresentation?.hasChineseStatus || !nativeOrderPresentation?.hasRefundForm || nativeOrderPresentation?.hasRawInternalCustomerKey) {
    interactionFailures.push("order-detail-native:business presentation or guarded form is invalid " + JSON.stringify(nativeOrderPresentation));
  }
  // The mobile screenshot is intentionally a real narrow viewport, while the
  // rest of this layout matrix remains desktop-only. Historical orders must
  // stay read-only at either width and never expose an implementation-era label.
  await cdp.call("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 1, mobile: true, screenWidth: 390, screenHeight: 844 });
  currentStep = "order-detail-history-mobile";
  try {
    await cdp.call("Page.navigate", { url: baseURL + "/admin/orderDetail.html?id=" + encodeURIComponent(historicalOrderReference) });
    await waitFor(cdp, "location.pathname === '/admin/orderDetail.html' && Boolean(document.querySelector('.order-host-layout')) && Boolean(document.body?.textContent?.includes('历史订单，仅供查询'))", "order-detail-history-mobile did not render the historical read-only detail");
    await waitForFonts("order-detail-history-mobile");
    const historicalMobile = await evaluate(cdp, `(() => ({
      readOnly: Boolean(document.body?.textContent?.includes('历史订单，仅供查询')),
      implementationLabel: Boolean(document.body?.textContent?.includes('V1')) || Boolean(document.body?.textContent?.includes('V2')),
      refundForm: Boolean(document.querySelector('.order-refund-confirmation')),
      rawStatus: Boolean(document.body?.textContent?.includes('outcome_unknown')),
      sidebarHidden: Boolean(document.querySelector('.admin-sidebar')) && getComputedStyle(document.querySelector('.admin-sidebar')).display === 'none',
      detailPanelsStacked: (() => {
        const layout = document.querySelector('[data-order-detail-layout]');
        if (!layout) return false;
        const panels = Array.from(layout.children).filter(node => getComputedStyle(node).display !== 'none');
        const first = panels[0]?.getBoundingClientRect();
        return panels.length >= 2 && panels.every(node => {
          const box = node.getBoundingClientRect();
          return Boolean(first) && Math.abs(box.left - first.left) <= 1 && box.width >= 300;
        });
      })(),
      orderHeader: (() => {
        const header=document.querySelector('#stage div[style*="height:52px"]');
        const number=header?.querySelector('[style*="font-family"]');
        const status=number?.nextElementSibling;
        const headerBox=header?.getBoundingClientRect();
        const boxes=[number,status].filter(Boolean).map(node => node.getBoundingClientRect());
        return {
          number: number?.textContent?.trim(), status: status?.textContent?.trim(),
          complete: Boolean(headerBox) && header.scrollHeight <= header.clientHeight + 1 && boxes.length === 2 && boxes.every(box => box.top >= headerBox.top - 1 && box.bottom <= headerBox.bottom + 1),
        };
      })(),
      overflowsViewport: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
    }))()`);
    if (!historicalMobile?.readOnly || historicalMobile?.implementationLabel || historicalMobile?.refundForm || historicalMobile?.rawStatus || !historicalMobile?.sidebarHidden || !historicalMobile?.detailPanelsStacked || !historicalMobile?.orderHeader?.complete || historicalMobile?.orderHeader?.number !== historicalOrderReference || !historicalMobile?.orderHeader?.status || historicalMobile?.overflowsViewport) throw new Error("historical order mobile presentation is invalid");
    await capture("order-detail-history-mobile");
    currentStep = "order-detail-native-mobile";
    await cdp.call("Page.navigate", { url: baseURL + "/admin/orderDetail.html?id=" + encodeURIComponent(nativeOrderReference) });
    await waitFor(cdp, "location.pathname === '/admin/orderDetail.html' && Boolean(document.querySelector('.order-refund-confirmation'))", "order-detail-native-mobile did not render its refund confirmation form");
    await waitForFonts("order-detail-native-mobile");
    const nativeMobile = await evaluate(cdp, `(() => {
      const form=document.querySelector('.order-refund-confirmation');
      const amount=form?.querySelector('input[data-order-refund-amount]');
      const transaction=form?.querySelector('input[data-order-refund-transaction]');
      const reason=form?.querySelector('select[data-order-refund-reason]');
      const submit=form?.querySelector('button.btn.primary');
      const formBox=form?.getBoundingClientRect();
      const controls=[amount,transaction,reason,submit].filter(Boolean).map(node => node.getBoundingClientRect());
      const submitStyle=submit ? getComputedStyle(submit) : null;
      const header=document.querySelector('#stage div[style*="height:52px"]');
      const number=header?.querySelector('[style*="font-family"]');
      const status=number?.nextElementSibling;
      const headerBox=header?.getBoundingClientRect();
      const headerBoxes=[number,status].filter(Boolean).map(node => node.getBoundingClientRect());
      return {
        standardFields: Boolean(form?.classList.contains('labs')) && form?.querySelectorAll('.field').length === 4,
        standardPrimary: submitStyle?.backgroundColor === 'rgb(51, 112, 255)' && submitStyle.color === 'rgb(255, 255, 255)' && submitStyle.borderRadius === '6px',
        controlsFit: Boolean(formBox) && controls.length === 4 && controls.every(box => box.width >= 300 && box.right <= innerWidth + 1),
        orderHeader: {
          number: number?.textContent?.trim(), status: status?.textContent?.trim(),
          complete: Boolean(headerBox) && header.scrollHeight <= header.clientHeight + 1 && headerBoxes.length === 2 && headerBoxes.every(box => box.top >= headerBox.top - 1 && box.bottom <= headerBox.bottom + 1),
        },
        overflowsViewport: document.documentElement.scrollWidth > document.documentElement.clientWidth + 1,
      };
    })()`);
    if (!nativeMobile?.standardFields || !nativeMobile?.standardPrimary || !nativeMobile?.controlsFit || !nativeMobile?.orderHeader?.complete || nativeMobile?.orderHeader?.number !== nativeOrderReference || !nativeMobile?.orderHeader?.status || nativeMobile?.overflowsViewport) throw new Error("native order mobile refund form is not visually actionable");
    await capture("order-detail-native-mobile");
  } catch (error) {
    await recordRouteFailure(currentStep, error);
  } finally {
    await cdp.call("Emulation.setDeviceMetricsOverride", { width: 1622, height: 1007, deviceScaleFactor: 1, mobile: false, screenWidth: 1622, screenHeight: 1007 });
  }
  const effectsMounted = await navigate("/admin/campaigns.html?view=external-effects", "Boolean(document.querySelector('#stage')) && Boolean(document.querySelector('#effects-refresh')) && Boolean(document.querySelector('#stage h2'))", "external-effects", "standard", "#stage h2", false);
  if (effectsMounted) await recordGeometry("external-effects", () => assertExternalEffectsLayout("external-effects"), true);

  currentStep = "hxc-refresh";
  try {
    await cdp.call("Page.navigate", { url: baseURL + "/admin/hxc-dashboard?tab=details" });
    await waitFor(cdp, "location.pathname === '/admin/hxc-dashboard' && Boolean(document.querySelector('#hxcRefresh')) && document.querySelectorAll('.sec-funnel .tabulator-row:not(.tabulator-group)').length > 1", "HXC data rows did not return for refresh");
    const hxcContent = await evaluate(cdp, `(() => { const crumb=document.querySelector('.sec-funnel > .crumb'); const heading=document.querySelector('.sec-funnel > .page-head > :first-child'); const refresh=document.querySelector('#hxcRefresh'); const grid=document.querySelector('.sec-funnel .tabulator-tableholder'); return {crumbHidden: Boolean(crumb) && getComputedStyle(crumb).display === 'none', headingHidden: Boolean(heading) && getComputedStyle(heading).display === 'none', refreshVisible: Boolean(refresh) && getComputedStyle(refresh).display !== 'none', scrollable: Boolean(grid) && grid.scrollHeight > grid.clientHeight}; })()`);
    if (!hxcContent?.crumbHidden || !hxcContent?.headingHidden || !hxcContent?.refreshVisible || !hxcContent?.scrollable) throw new Error("HXC duplicate title/action/scroll layout invalid");
    await evaluate(cdp, "(() => { const grid=document.querySelector('.sec-funnel .tabulator-tableholder'); grid.scrollTop=grid.scrollHeight; return grid.scrollTop > 0; })()");
    if (!await evaluate(cdp, "document.querySelector('.sec-funnel .tabulator-tableholder')?.scrollTop > 0")) throw new Error("HXC grid did not retain a user scroll");
    // These arrays are bounded diagnostics for the full route matrix. Reset
    // them immediately before this one interaction so their window cannot be
    // invalidated by a later ring-buffer eviction.
    requestEvents.length = 0;
    responses.length = 0;
    await evaluate(cdp, "(() => { document.querySelector('#hxcRefresh').click(); return true; })()");
    await waitForRecorded(requestEvents, value => value === "POST /api/admin/hxc-dashboard/refreshes", "HXC refresh did not issue its configured POST");
    await waitForRecorded(responses, value => value === "POST /api/admin/hxc-dashboard/refreshes:503", "HXC refresh did not reach the disabled runtime contract");
    await waitFor(cdp, "document.querySelector('#hxcRefresh')?.disabled === false && document.querySelector('#hxcRefresh')?.textContent === '同步数据'", "HXC refresh action did not settle");
  } catch (error) {
    await recordRouteFailure("hxc-refresh", error);
    interactionFailures.push("hxc-refresh:" + String(error instanceof Error ? error.message : "refresh assertion failed").replace(/[^A-Za-z0-9_.: -]/g, "_").slice(0, 160));
  }
  await verifyPresentation({ cdp, evaluate, waitFor, capture, baseURL, productID, screenshotDirectory });
  if (runtimeExceptions.length) interactionFailures.push("runtime-exception:" + runtimeExceptions.join(","));
  if (geometryFailures.length || interactionFailures.length) throw new Error("admin layout failures=" + [...geometryFailures, ...interactionFailures].join(","));
  console.log("admin_shell_layout_chromium: PASS routes=" + responses.filter(value => value.includes("/admin/") || value.includes("/api/admin/hxc-dashboard")).length);
} catch (error) {
  failed = true;
  if (cdp) {
    try { await captureFailureEvidence(currentStep, error); } catch (_) {}
  }
  throw error;
} finally {
  if (cdp) cdp.close();
  if (child && child.exitCode === null && child.signalCode === null) child.kill("SIGTERM");
  await waitForExit(child, 3000);
  if (child && child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
  await waitForExit(child, 1000);
  await removeDirectory(profile);
  if (failed) process.exitCode = 1;
}
