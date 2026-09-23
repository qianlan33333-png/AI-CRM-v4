import fs from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { spawn, spawnSync } from "node:child_process";

const baseURL = process.env.AICRM_OVERVIEW_BROWSER_URL;
const username = process.env.AICRM_OVERVIEW_BROWSER_USERNAME;
const password = process.env.AICRM_OVERVIEW_BROWSER_PASSWORD;
const screenshotDirectory = process.env.AICRM_OVERVIEW_SCREENSHOT_DIR;
if (!/^https:\/\//.test(baseURL || "") || !username || !password) throw new Error("admin overview Chromium journey requires HTTPS URL and credentials");

const delay = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
function browserBinary() {
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
}

class CDP {
  constructor(socket) {
    this.socket = socket; this.nextID = 0; this.pending = new Map(); this.events = new Map();
    socket.addEventListener("message", (event) => {
      const message = JSON.parse(String(event.data));
      if (!message.id) {
        for (const listener of this.events.get(message.method) || []) listener(message.params || {});
        return;
      }
      const pending = this.pending.get(message.id);
      if (!pending) return;
      this.pending.delete(message.id);
      message.error ? pending.reject(new Error(`CDP ${message.error.code || "error"}`)) : pending.resolve(message.result || {});
    });
  }
  call(method, params = {}) { return new Promise((resolve, reject) => { const id = ++this.nextID; this.pending.set(id, { resolve, reject }); this.socket.send(JSON.stringify({ id, method, params })); }); }
  on(method, listener) { const listeners = this.events.get(method) || []; listeners.push(listener); this.events.set(method, listeners); }
  close() { for (const pending of this.pending.values()) pending.reject(new Error("CDP closed")); this.pending.clear(); this.socket.close(); }
}

async function port(profile) {
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
  for (let attempt = 0; attempt < 180; attempt += 1) { if (await evaluate(cdp, expression)) return; await delay(50); }
  throw new Error(message);
}
async function stopBrowser(child) {
  if (!child || child.exitCode !== null || child.signalCode !== null) return;
  child.kill("SIGTERM");
  await Promise.race([new Promise((resolve) => child.once("exit", resolve)), delay(3000)]);
  if (child.exitCode === null && child.signalCode === null) child.kill("SIGKILL");
}
async function captureOverview(cdp, name, width) {
  await cdp.call("Emulation.setDeviceMetricsOverride", { width, height: 900, deviceScaleFactor: 1, mobile: false });
  await delay(80);
  if (!screenshotDirectory) return;
  const image = await cdp.call("Page.captureScreenshot", { format: "png", captureBeyondViewport: true });
  await fs.mkdir(screenshotDirectory, { recursive: true });
  await fs.writeFile(path.join(screenshotDirectory, name), Buffer.from(image.data, "base64"));
}
async function assertDrawerGeometry(cdp, expectedViewport) {
  const geometry = JSON.parse(await evaluate(cdp, "(() => { const drawer=document.querySelector('.shared-detail-drawer'); const panel=drawer?.querySelector('.shared-detail-drawer__panel'); const rect=(node)=>{const box=node?.getBoundingClientRect();return box?{left:box.left,right:box.right,top:box.top,bottom:box.bottom,width:box.width,height:box.height}:null}; const style=drawer?getComputedStyle(drawer):null; return JSON.stringify({viewport:window.innerWidth,clientWidth:document.documentElement.clientWidth,drawer:rect(drawer),panel:rect(panel),open:drawer?.open===true,display:style?.display||'',position:style?.position||'',marginRight:Number.parseFloat(style?.marginRight||'0'),drawerStyleLoaded:[...document.styleSheets].some(sheet=>String(sheet.href||'').includes('sharedDetailDrawerStyles-'))}); })()") || "{}");
  const rightGap = geometry.clientWidth - geometry.drawer?.right;
  if (geometry.viewport !== expectedViewport || !geometry.open || !geometry.drawerStyleLoaded || geometry.display !== 'block' || geometry.position !== 'fixed' || !geometry.drawer || !geometry.panel || geometry.clientWidth <= 0 || geometry.drawer.left < 0 || geometry.drawer.right > geometry.clientWidth + 1 || Math.abs(rightGap - geometry.marginRight) > 1 || geometry.marginRight !== 16 || geometry.drawer.width < 400 || geometry.drawer.width > 640 || geometry.panel.left < geometry.drawer.left || geometry.panel.right > geometry.drawer.right + 1) throw new Error(`paid-record drawer did not use the shared right-side geometry: ${JSON.stringify(geometry)}`);
}
async function selectOverviewPeriod(cdp, label, period) {
  const encodedLabel = JSON.stringify(label);
  await evaluate(cdp, `(() => { const button = [...document.querySelectorAll('[data-page-header-actions="overview-range"] [data-page-header-action]')].find((node) => node.textContent.trim() === ${encodedLabel}); if (!button) return false; button.click(); return true; })()`);
  await waitFor(cdp, `document.querySelector('[data-page-header-actions="overview-range"] .is-active')?.textContent.trim() === ${encodedLabel} && performance.getEntriesByType('resource').some((entry) => String(entry.name).includes('/api/admin/overview?period=${period}'))`, `${label} did not load through the overview UI`);
}

const profile = await fs.mkdtemp(path.join(os.tmpdir(), "aicrm-admin-overview-chromium-"));
let browser; let cdp; let failed = false;
try {
  browser = spawn(browserBinary(), ["--headless=new", "--no-sandbox", "--remote-debugging-port=0", `--user-data-dir=${profile}`, "--no-first-run", "--no-default-browser-check", "--disable-background-networking", "--ignore-certificate-errors", "--allow-insecure-localhost", "about:blank"], { stdio: ["ignore", "ignore", "ignore"] });
  const target = await (await fetch(`${await port(profile)}/json/new?about:blank`, { method: "PUT" })).json();
  const socket = new WebSocket(target.webSocketDebuggerUrl);
  await new Promise((resolve, reject) => { socket.addEventListener("open", resolve, { once: true }); socket.addEventListener("error", () => reject(new Error("Chromium page connection failed")), { once: true }); });
  cdp = new CDP(socket); await cdp.call("Page.enable"); await cdp.call("Runtime.enable"); await cdp.call("Network.enable");
  const assetResponses = new Map();
  const orderDetailResponses = [];
  cdp.on("Network.responseReceived", (params) => {
    try {
      const responseURL = new URL(String(params.response?.url || ""));
      if (responseURL.origin !== new URL(baseURL).origin) return;
      const match = responseURL.pathname.match(/^\/assets\/(overviewAdmin|overviewStyles|sharedDetailDrawerStyles|navigationHost)-[A-Za-z0-9_-]+\.(?:js|css)$/);
      if (match) assetResponses.set(match[1], Number(params.response?.status) || 0);
      if (responseURL.pathname === "/api/admin/orders/M-OVERVIEW-BROWSER") orderDetailResponses.push({ provider: responseURL.searchParams.get("provider"), status: Number(params.response?.status) || 0 });
    } catch (_) {}
  });
  await cdp.call("Page.navigate", { url: `${baseURL}/login?next=%2Fadmin` });
  await waitFor(cdp, "Boolean(document.querySelector('form[action=\"/login\"] input[name=\"login_csrf_token\"]'))", "login shell did not render");
  await evaluate(cdp, `(() => { document.querySelector('input[name="username"]').value=${JSON.stringify(username)}; document.querySelector('input[name="password"]').value=${JSON.stringify(password)}; document.querySelector('form[action="/login"]').requestSubmit(); return true; })()`);
  await waitFor(cdp, "location.pathname === '/admin' && !document.querySelector('form[action=\"/login\"]')", "login did not reach the authenticated admin Host");
  try {
    await waitFor(cdp, "Boolean(document.querySelector('#overview-admin-root .overview-metrics--primary')) && document.body.textContent.includes('已确认支付')", "overview Host did not render the primary metrics");
  } catch (error) {
    const diagnostics = await evaluate(cdp, "JSON.stringify({root:document.querySelector('#overview-admin-root')?.outerHTML||'',scripts:[...document.scripts].map((script)=>script.src),text:document.body.textContent.slice(0,1200)})");
    throw new Error(`${error.message}: ${diagnostics}`);
  }
  for (const asset of ["overviewAdmin", "overviewStyles", "sharedDetailDrawerStyles"]) {
    if (assetResponses.get(asset) !== 200) throw new Error(`staged overview asset ${asset} HTTP status=${assetResponses.get(asset) || 0}`);
  }
  const overviewDOM = await evaluate(cdp, "JSON.stringify({primary:[...document.querySelectorAll('.overview-metrics--primary .overview-metric')].map((node)=>node.textContent),secondary:[...document.querySelectorAll('.overview-metrics--secondary .overview-metric')].map((node)=>node.textContent),today:performance.getEntriesByType('resource').some((entry)=>String(entry.name).includes('/api/admin/overview?period=today')),nav:[...document.querySelectorAll('.admin-nav-section-title')].map((node)=>node.textContent),topbars:document.querySelectorAll('header.admin-topbar').length,titles:document.querySelectorAll('.admin-topbar .admin-page-title').length,headerActions:[...document.querySelectorAll('.admin-topbar [data-page-header-actions=\"overview-range\"] [data-page-header-action]')].map((node)=>({label:node.textContent?.trim(),pressed:node.getAttribute('aria-pressed')})),themeActions:[...document.querySelectorAll('.admin-topbar [data-page-header-actions=\"overview-theme\"] [data-page-header-action]')].map((node)=>({label:node.textContent?.trim(),pressed:node.getAttribute('aria-pressed')})),theme:document.querySelector('#overview-admin-root')?.dataset.overviewTheme,bodyRangeControls:document.querySelectorAll('#overview-admin-root [data-overview-period]').length,repeatedHeading:document.body.textContent.includes('统计口径以各项数据的确认时间为准') || Boolean(document.querySelector('#overview-admin-root .overview-toolbar')),removedCopy:document.body.textContent.includes('来源待核实') || document.body.textContent.includes('最近读取：')})");
  const rendered = JSON.parse(overviewDOM || "{}");
  if (rendered.primary?.length !== 4 || !rendered.primary?.[0]?.includes("已确认支付") || !rendered.primary?.[1]?.includes("支付订单") || !rendered.primary?.[2]?.includes("支付用户") || !rendered.primary?.[3]?.includes("新增用户") || rendered.secondary?.length !== 2 || !rendered.secondary?.[0]?.includes("完成退款") || !rendered.secondary?.[1]?.includes("净收款") || !rendered.today || rendered.nav?.join("|") !== "总览|用户|运营|交易|分销|内容素材|系统设置" || rendered.topbars !== 1 || rendered.titles !== 1 || rendered.headerActions?.map((item)=>item.label).join("|") !== "今日|近 7 天|近 30 天|自定义" || rendered.headerActions?.[0]?.pressed !== "true" || rendered.themeActions?.map((item)=>item.label).join("|") !== "清爽|深色|科技" || rendered.themeActions?.[0]?.pressed !== "true" || rendered.theme !== "business" || rendered.bodyRangeControls !== 0 || rendered.repeatedHeading || rendered.removedCopy) throw new Error(`overview Host did not preserve its V3 metric, header-action, template and navigation contract: ${JSON.stringify(rendered)}`);
  const todayChartGeometry = JSON.parse(await evaluate(cdp, "(() => { const bar=document.querySelector('.overview-chart__bar'); const plot=document.querySelector('.overview-chart__plot'); return JSON.stringify({height:bar?.getBoundingClientRect().height||0,width:bar?.getBoundingClientRect().width||0,plotHeight:plot?.getBoundingClientRect().height||0}); })()") || "{}");
  if (todayChartGeometry.height < 100 || todayChartGeometry.width < 20 || todayChartGeometry.plotHeight < 100) throw new Error(`today overview chart bar is not visibly rendered: ${JSON.stringify(todayChartGeometry)}`);
  await captureOverview(cdp, "overview-today-1280.png", 1280);
  await captureOverview(cdp, "overview-today-1440.png", 1440);
  const overviewRequestsBeforeTemplate = await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length");
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-theme\"] [data-page-header-action=\"theme-dark\"]')?.click()")
  await waitFor(cdp, "document.querySelector('#overview-admin-root')?.dataset.overviewTheme === 'dark'", "dark overview template did not apply");
  if (await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length") !== overviewRequestsBeforeTemplate) throw new Error("switching to the dark template issued an overview read");
  await captureOverview(cdp, "overview-dark-today-1280.png", 1280);
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-theme\"] [data-page-header-action=\"theme-aurora\"]')?.click()")
  await waitFor(cdp, "document.querySelector('#overview-admin-root')?.dataset.overviewTheme === 'aurora'", "aurora overview template did not apply");
  if (await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length") !== overviewRequestsBeforeTemplate) throw new Error("switching to the aurora template issued an overview read");
  await captureOverview(cdp, "overview-aurora-today-1280.png", 1280);
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-theme\"] [data-page-header-action=\"theme-business\"]')?.click()")
  await waitFor(cdp, "document.querySelector('#overview-admin-root')?.dataset.overviewTheme === 'business'", "business overview template did not restore");
  const customBefore = await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview?period=custom')).length");
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-range\"] [data-page-header-action=\"period-custom\"]')?.click()")
  await waitFor(cdp, "Boolean(document.querySelector('[data-overview-custom].is-open')) && document.querySelector('[data-page-header-actions=\"overview-range\"] [data-page-header-action=\"period-custom\"]')?.getAttribute('aria-pressed') === 'true'", "custom-range draft did not open from the shell header");
  await captureOverview(cdp, "overview-custom-draft-1280.png", 1280);
  await captureOverview(cdp, "overview-custom-draft-1440.png", 1440);
  const customDraftState = await evaluate(cdp, "JSON.stringify({requests:performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview?period=custom')).length,bodyControls:document.querySelectorAll('#overview-admin-root [data-overview-period]').length})");
  const customDraft = JSON.parse(customDraftState || "{}");
  if (customDraft.requests !== customBefore || customDraft.bodyControls !== 0) throw new Error(`opening the custom draft must not issue a request or restore content buttons: ${customDraftState}`);
  const customTemplateBefore = await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length");
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-theme\"] [data-page-header-action=\"theme-aurora\"]')?.click()")
  await waitFor(cdp, "document.querySelector('#overview-admin-root')?.dataset.overviewTheme === 'aurora' && Boolean(document.querySelector('[data-overview-custom].is-open'))", "template switch interrupted the custom-range draft");
  if (await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length") !== customTemplateBefore) throw new Error("template switch issued an overview read while custom draft was open");
  await selectOverviewPeriod(cdp, "今日", "today");
  if (await evaluate(cdp, "Boolean(document.querySelector('[data-overview-custom].is-open'))")) throw new Error("switching to a preset did not discard the unsubmitted custom draft UI");
  const customAfterPreset = await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview?period=custom')).length");
  if (customAfterPreset !== customBefore) throw new Error("switching to a preset submitted an un-applied custom draft");
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-range\"] [data-page-header-action=\"period-custom\"]')?.click()")
  await waitFor(cdp, "Boolean(document.querySelector('[data-overview-custom].is-open'))", "custom range did not reopen");
  await evaluate(cdp, "(() => { const form=document.querySelector('[data-overview-custom]'); const from=form?.querySelector('[name=from]'); const to=form?.querySelector('[name=to]'); if (!form || !from || !to) return false; from.value='2000-01-01'; to.value='2000-01-02'; form.requestSubmit(); return true; })()");
  await waitFor(cdp, "performance.getEntriesByType('resource').some((entry)=>String(entry.name).includes('/api/admin/overview?period=custom&from=2000-01-01&to=2000-01-02'))", "applying a custom range did not use the existing overview read request");
  await waitFor(cdp, "!document.querySelector('#overview-admin-root .overview-snapshot') && document.querySelector('[data-page-header-actions=\"overview-range\"] [data-page-header-action=\"period-custom\"]')?.getAttribute('aria-pressed') === 'true'", "the applied custom range did not preserve the header state or remove redundant observation copy");
  await selectOverviewPeriod(cdp, "今日", "today");
  await waitFor(cdp, "Boolean(document.querySelector('[data-overview-paid-records]'))", "today range did not restore its ready payment action");
  const paidRecordsOpened = await evaluate(cdp, "(() => { const button=document.querySelector('[data-overview-paid-records]'); if (!button) return false; button.click(); return true; })()");
  if (!paidRecordsOpened) throw new Error("overview paid-record action is unavailable for a ready payment section");
  await waitFor(cdp, "document.querySelector('.shared-detail-drawer .overview-paid-records a[href=\"/admin/orderDetail.html?id=M-OVERVIEW-BROWSER&provider=wechat\"]')", "paid-record drawer did not form the provider-scoped order detail link");
  const paidDrawerTemplateBefore = await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length");
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-theme\"] [data-page-header-action=\"theme-dark\"]')?.click()")
  await waitFor(cdp, "document.querySelector('#overview-admin-root')?.dataset.overviewTheme === 'dark' && Boolean(document.querySelector('.shared-detail-drawer[open] .overview-paid-records'))", "template switch interrupted the paid-record drawer");
  if (await evaluate(cdp, "performance.getEntriesByType('resource').filter((entry)=>String(entry.name).includes('/api/admin/overview')).length") !== paidDrawerTemplateBefore) throw new Error("template switch issued an overview read while the paid-record drawer was open");
  await captureOverview(cdp, "overview-paid-records-1280.png", 1280);
  await assertDrawerGeometry(cdp, 1280);
  await captureOverview(cdp, "overview-paid-records-1440.png", 1440);
  await assertDrawerGeometry(cdp, 1440);
  await evaluate(cdp, "document.querySelector('[data-page-header-actions=\"overview-theme\"] [data-page-header-action=\"theme-business\"]')?.click()")
  const openedOrder = await evaluate(cdp, "(() => { const link=document.querySelector('.shared-detail-drawer .overview-paid-records a[href=\"/admin/orderDetail.html?id=M-OVERVIEW-BROWSER&provider=wechat\"]'); if (!link) return false; link.click(); return true; })()");
  if (!openedOrder) throw new Error("paid-record drawer link disappeared before navigation");
  try {
    await waitFor(cdp, "location.pathname === '/admin/orderDetail.html' && performance.getEntriesByType('resource').some((entry)=>String(entry.name).includes('/api/admin/orders/M-OVERVIEW-BROWSER?provider=wechat'))", "provider-scoped order detail did not request its exact Order API URL");
  } catch (error) {
    const diagnostics = await evaluate(cdp, "JSON.stringify({location:location.href,resources:performance.getEntriesByType('resource').map((entry)=>String(entry.name)),body:document.body.textContent.slice(-1200),scripts:[...document.scripts].map((script)=>script.src)})");
    throw new Error(`${error.message}: ${diagnostics}`);
  }
  if (!orderDetailResponses.some((value) => value.provider === "wechat" && value.status === 200)) throw new Error(`provider-scoped Order detail response missing: ${JSON.stringify(orderDetailResponses)}`);
  await cdp.call("Page.navigate", { url: `${baseURL}/admin` });
  await waitFor(cdp, "Boolean(document.querySelector('#overview-admin-root .overview-metrics--primary')) && document.body.textContent.includes('已确认支付')", "overview did not return after paid-record detail navigation");
  await selectOverviewPeriod(cdp, "近 7 天", "7d");
  await captureOverview(cdp, "overview-7d-1280.png", 1280);
  const sevenDayDOM = await evaluate(cdp, "JSON.stringify((() => { const chart=document.querySelector('.overview-chart:not(.overview-chart--dense)'); const labels=[...document.querySelectorAll('.overview-chart:not(.overview-chart--dense) .overview-chart__value')].filter((node)=>node.textContent.trim()).map((node)=>({text:node.textContent.trim(),clientWidth:node.clientWidth,scrollWidth:node.scrollWidth})); return {columns:document.querySelectorAll('.overview-chart__column').length,bars:document.querySelectorAll('.overview-chart__bar').length,details:document.querySelector('.overview-trend-details')?.open,labels,chart:{clientWidth:chart?.clientWidth||0,scrollWidth:chart?.scrollWidth||0},barWidths:[...document.querySelectorAll('.overview-chart__bar')].map((bar)=>bar.getBoundingClientRect().width),documentWidth:document.documentElement.scrollWidth,viewport:window.innerWidth}; })())");
  const sevenDay = JSON.parse(sevenDayDOM || "{}");
  if (sevenDay.columns !== 7 || sevenDay.bars !== 3 || !sevenDay.labels?.some((label)=>label.text === "¥12,345,678.90") || sevenDay.labels?.some((label)=>label.scrollWidth > label.clientWidth + 1) || sevenDay.barWidths?.some((width)=>width > 40.5) || sevenDay.documentWidth > sevenDay.viewport + 1) throw new Error(`seven-day money labels, bars or page width were clipped: ${sevenDayDOM}`);
  await captureOverview(cdp, "overview-7d-1440.png", 1440);
  await captureOverview(cdp, "overview-7d-390.png", 390);
  const sevenDayNarrowDOM = await evaluate(cdp, "JSON.stringify((() => { const chart=document.querySelector('.overview-chart:not(.overview-chart--dense)'); const labels=[...document.querySelectorAll('.overview-chart:not(.overview-chart--dense) .overview-chart__value')].filter((node)=>node.textContent.trim()).map((node)=>({text:node.textContent.trim(),clientWidth:node.clientWidth,scrollWidth:node.scrollWidth})); return {labels,chart:{clientWidth:chart?.clientWidth||0,scrollWidth:chart?.scrollWidth||0},documentWidth:document.documentElement.scrollWidth,viewport:window.innerWidth}; })())");
  const sevenDayNarrow = JSON.parse(sevenDayNarrowDOM || "{}");
  if (!sevenDayNarrow.labels?.some((label)=>label.text === "¥12,345,678.90") || sevenDayNarrow.labels?.some((label)=>label.scrollWidth > label.clientWidth + 1) || sevenDayNarrow.chart?.scrollWidth <= sevenDayNarrow.chart?.clientWidth || sevenDayNarrow.documentWidth > sevenDayNarrow.viewport + 1) throw new Error(`narrow seven-day chart did not contain long labels in its own scroll region: ${sevenDayNarrowDOM}`);
  const response = await evaluate(cdp, "fetch('/api/admin/overview?period=7d',{credentials:'same-origin'}).then(async (value)=>({status:value.status,body:await value.json()}))");
  const overview = response?.body;
  if (response?.status !== 200 || !overview || overview.range?.timezone !== "Asia/Shanghai" || overview.paid?.status !== "ready" || overview.paid?.order_count !== 3 || overview.paid?.gross?.[0]?.amount_minor !== 1234569490 || overview.customers?.new_canonical_customers !== 1 || overview.refunds?.completed_count !== 1 || overview.distribution?.current_unsettled_minor !== 120 || overview.todos?.items?.[0]?.href !== "/admin/distribution") throw new Error("browser overview response did not preserve owner facts");
  await selectOverviewPeriod(cdp, "近 30 天", "30d");
  await captureOverview(cdp, "overview-30d-1280.png", 1280);
  const thirtyDayDOM = await evaluate(cdp, "JSON.stringify((() => { const columns=[...document.querySelectorAll('.overview-chart__column')]; const baselines=columns.map((column)=>column.querySelector('.overview-chart__plot').getBoundingClientRect().bottom); const barOffsets=columns.map((column)=>{const plot=column.querySelector('.overview-chart__plot').getBoundingClientRect(); const bar=column.querySelector('.overview-chart__bar'); return bar ? Math.abs(bar.getBoundingClientRect().bottom-plot.bottom) : 0;}); return {columns:columns.length,bars:document.querySelectorAll('.overview-chart__bar').length,details:document.querySelector('.overview-trend-details')?.open,baselineSpread:Math.max(...baselines)-Math.min(...baselines),barOffset:Math.max(...barOffsets),visibleAmounts:[...document.querySelectorAll('.overview-chart__value')].filter((node)=>node.textContent.trim()).length,titles:[...document.querySelectorAll('.overview-chart__bar title')].map((node)=>node.textContent)}; })())");
  const thirtyDay = JSON.parse(thirtyDayDOM || "{}");
  if (thirtyDay.columns !== 30 || thirtyDay.bars !== 3 || thirtyDay.details !== false || thirtyDay.baselineSpread > 0.5 || thirtyDay.barOffset > 0.5 || thirtyDay.visibleAmounts !== 0 || thirtyDay.titles?.join("|") !== "¥4.00|¥12,345,678.90|¥12.00") throw new Error(`thirty-day overview chart geometry or dense-label contract failed: ${JSON.stringify(thirtyDay)}`);
  await captureOverview(cdp, "overview-30d-1440.png", 1440);
  await evaluate(cdp, "document.querySelector('.overview-trend-details > summary')?.click()")
  await waitFor(cdp, "document.querySelector('.overview-trend-details')?.open === true && document.querySelector('.overview-trend-table-wrap')?.textContent?.includes('¥12,345,678.90')", "expanded thirty-day detail table did not expose the complete long amount");
  const thirtyDayTableDOM = await evaluate(cdp, "JSON.stringify((() => { const table=document.querySelector('.overview-trend-table-wrap'); return {clientWidth:table?.clientWidth||0,scrollWidth:table?.scrollWidth||0,documentWidth:document.documentElement.scrollWidth,viewport:window.innerWidth}; })())");
  const thirtyDayTable = JSON.parse(thirtyDayTableDOM || "{}");
  if (thirtyDayTable.documentWidth > thirtyDayTable.viewport + 1) throw new Error(`expanded thirty-day table escaped the page width: ${thirtyDayTableDOM}`);
  await captureOverview(cdp, "overview-30d-expanded-1280.png", 1280);
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/wechat-pay/products` });
  await waitFor(cdp, "Boolean(document.querySelector('.admin-nav a[href=\"/admin/wechat-pay/products\"].is-active'))", "server-rendered product shell did not retain the V3 navigation state");
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/distribution` });
  await waitFor(cdp, "Boolean(document.querySelector('#distribution-admin-root')) && Boolean(document.querySelector('.admin-nav a[href=\"/admin/distribution\"].is-active'))", "server-rendered distribution shell did not retain the V3 navigation state");
  await cdp.call("Page.navigate", { url: `${baseURL}/admin/customerDetail.html?id=1` });
  await waitFor(cdp, "Boolean(document.querySelector('.side-nav[data-v3-navigation-host=\"ready\"]'))", "built customer document did not receive the shared V3 navigation Host");
  if (assetResponses.get("navigationHost") !== 200) throw new Error(`staged navigation Host HTTP status=${assetResponses.get("navigationHost") || 0}`);
  const customerNavigation = await evaluate(cdp, "JSON.stringify({groups:[...document.querySelectorAll('.side-nav .side-grp')].map((node)=>node.textContent),active:[...document.querySelectorAll('.side-nav .nav-item.on')].map((node)=>node.textContent?.trim()),access:document.body.textContent.includes('登录与权限')})");
  const customer = JSON.parse(customerNavigation || "{}");
  if (customer.groups?.join("|") !== "总览|用户|运营|交易|分销|内容素材|系统设置" || customer.active?.join("|") !== "用户激活 / 用户列表" || customer.access) throw new Error("built customer document did not apply the shared navigation safely");
  console.log("admin_overview_chromium: PASS");
} catch (error) {
  failed = true;
  throw error;
} finally {
  if (cdp) cdp.close();
  await stopBrowser(browser);
  await fs.rm(profile, { recursive: true, force: true, maxRetries: 2 }).catch((error) => { if (!failed) throw error; });
}
