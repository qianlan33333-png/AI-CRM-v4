import fs from "node:fs/promises";
import { createHmac } from "node:crypto";
import os from "node:os";
import path from "node:path";
import { spawn } from "node:child_process";
import assert from "node:assert/strict";
import { chromiumStartupDiagnostic, chromiumStartupTimeoutMS } from "../../internal/webshell/chromium_launch.mjs";
import { resolveDataWorkspaceChromiumBinary } from "../../internal/webshell/data_workspace_chromium_binary.mjs";
const origin = process.env.AICRM_DATA_WORKSPACE_URL;
const session = process.env.AICRM_DATA_WORKSPACE_SESSION;
const csrf = process.env.AICRM_DATA_WORKSPACE_CSRF;
const product = process.env.AICRM_DATA_WORKSPACE_PRODUCT;
const output = process.env.AICRM_DATA_WORKSPACE_SCREENSHOTS;
const profile = await fs.mkdtemp(
  path.join(os.tmpdir(), "data-workspace-browser-"),
);
const executable = resolveDataWorkspaceChromiumBinary();
const child = spawn(
  executable,
  [
    "--headless=new",
    "--no-sandbox",
    "--disable-dev-shm-usage",
    "--no-first-run",
    "--no-default-browser-check",
    "--disable-background-networking",
    "--ignore-certificate-errors",
    "--allow-insecure-localhost",
    "--remote-debugging-port=0",
    "--user-data-dir=" + profile,
    "about:blank",
  ],
  { stdio: ["ignore", "ignore", "pipe"] },
);
let stderr = "";
let launchError;
child.stderr.on("data", chunk => { stderr = (stderr + String(chunk)).slice(-4096); });
child.on("error", error => { launchError = error; });
let ws;
let seq = 0;
const waiting = new Map();
const delay = () => new Promise((resolve) => setTimeout(resolve, 100));
async function poll(test, message) {
  for (let i = 0; i < 200; i++) {
    const result = await test();
    if (result) return result;
    await delay();
  }
  throw new Error(message);
}
try {
  let port;
  const startupDeadline = Date.now() + chromiumStartupTimeoutMS;
  while (Date.now() < startupDeadline) {
    try {
      const candidate = (await fs.readFile(path.join(profile, "DevToolsActivePort"), "utf8")).split("\n")[0];
      if (/^\d+$/.test(candidate)) { port = candidate; break; }
    } catch {}
    if (launchError || child.exitCode !== null || child.signalCode) break;
    await delay();
  }
  if (!port) throw new Error(chromiumStartupDiagnostic({profile, stderr, launchError, exitCode: child.exitCode, signalCode: child.signalCode}));
  const tab = await (
    await fetch(`http://127.0.0.1:${port}/json/new?about:blank`, {
      method: "PUT",
    })
  ).json();
  ws = new WebSocket(tab.webSocketDebuggerUrl);
  await new Promise((resolve) =>
    ws.addEventListener("open", resolve, { once: true }),
  );
  ws.addEventListener("message", (event) => {
    const msg = JSON.parse(event.data);
    if (msg.id) {
      const resolve = waiting.get(msg.id);
      waiting.delete(msg.id);
      resolve?.(msg);
    }
  });
  const call = (method, params = {}) =>
    new Promise((resolve, reject) => {
      const id = ++seq;
      waiting.set(id, (msg) =>
        msg.error ? reject(new Error(method + " failed")) : resolve(msg.result),
      );
      ws.send(JSON.stringify({ id, method, params }));
    });
  const evaluate = async (expression) => {
    const data = await call("Runtime.evaluate", {
      expression,
      awaitPromise: true,
      returnByValue: true,
    });
    if (data.exceptionDetails)
      throw new Error(
        data.exceptionDetails.exception?.description ||
          "page evaluation failed",
      );
    return data.result?.value;
  };
  await call("Page.enable");
  await call("Runtime.enable");
  await call("Network.enable");
  await call("Network.setCookies", {
    cookies: [
      {
        name: "aicrm_admin_session",
        value: session,
        url: origin,
        secure: true,
      },
      { name: "aicrm_admin_csrf", value: csrf, url: origin, secure: true },
    ],
  });
  await fs.mkdir(output, { recursive: true });
  for (const [name, url] of [
    ["product", `/admin/spProductData.html?id=${product}`],
    ["hxc", "/admin/hxc-dashboard"],
  ]) {
    await call("Emulation.setDeviceMetricsOverride", {
      width: 1440,
      height: 1000,
      deviceScaleFactor: 1,
      mobile: false,
    });
    await call("Page.navigate", { url: origin + url });
    await poll(
      () =>
        evaluate(
          `document.querySelectorAll('.dw-card').length>=4 && document.querySelector('[data-workspace-page=overview]')`,
        ),
      name + " workspace did not render",
    ).catch(async (err) => {
      const info = await evaluate(
        `JSON.stringify({path:location.pathname,text:document.body.innerText.slice(0,1400),scripts:[...document.scripts].map(s=>new URL(s.src||location.href).pathname)})`,
      );
      throw new Error(err.message + " " + info);
    });
    assert.equal(
      await evaluate(`document.querySelectorAll('.admin-topbar').length`),
      1,
      "one admin header",
    );
    const screenshot = await call("Page.captureScreenshot", { format: "png" });
    await fs.writeFile(
      path.join(output, name + "-desktop.png"),
      Buffer.from(screenshot.data, "base64"),
    );
    assert.equal(await evaluate(`document.querySelectorAll('.tabulator').length`),0,"overview does not mount a detail table");
    await evaluate(`document.querySelector('[data-workspace-tab=details]').click()`);
    await poll(()=>evaluate(`!!document.querySelector('.tabulator-header') && document.querySelectorAll('.tabulator-row:not(.tabulator-group)').length>0`),"detail page rows");
    assert.equal(await evaluate(`document.querySelector('.dw-overview').hidden`),true,"details does not show dashboard");
    assert.equal(await evaluate(`new URLSearchParams(location.search).get('tab')`),"details");
    const detailshot=await call("Page.captureScreenshot",{format:"png"});
    await fs.writeFile(path.join(output,name+"-details-desktop.png"),Buffer.from(detailshot.data,"base64"));
    await evaluate(`document.querySelector('.dw-config-button').click()`);
    assert.equal(await evaluate(`document.querySelector('dialog[open] h2').textContent`),"列设置");
    assert.equal(await evaluate(`document.querySelectorAll('dialog[open] input[data-metric]').length`),0,"column panel excludes metrics");
    await evaluate(`document.querySelector('dialog[open] button[aria-label="关闭设置"]').click();history.back()`);
    await poll(()=>evaluate(`document.querySelector('[data-workspace-page=overview]')`),"back restores overview");
    await evaluate(`document.querySelector('.dw-config-button').click()`);
    assert.equal(await evaluate(`document.querySelector('dialog[open] h2').textContent`),"配置看板");
    assert.equal(await evaluate(`document.querySelectorAll('dialog[open] input[data-column]').length`),0,"dashboard panel excludes columns");
    await evaluate(`document.querySelector('dialog[open] button[aria-label="关闭设置"]').click()`);
    if (name === "hxc") {
      await evaluate(
        `document.querySelector('#hxcStage').value='active_used';document.querySelector('#hxcApply').click()`,
      );
      await poll(
        () =>
          evaluate(
            `document.querySelector('#hxcMeta').textContent.includes('共 0 人')`,
          ),
        "filtered total not zero",
      );
      assert.equal(
        await evaluate(`document.querySelector('.dw-card strong').textContent`),
        "0",
      );
      await evaluate(
        `document.querySelector('#hxcStage').value='';document.querySelector('#hxcApply').click()`,
      );
      await poll(
        () =>
          evaluate(
            `document.querySelector('#hxcMeta').textContent.includes('共 30 人')`,
          ),
        "full dataset not restored",
      );
      await evaluate(
        `window.__originalFetch=window.fetch;window.__queries=[];window.fetch=async(...args)=>{if(String(args[0]).endsWith('/hxc-dashboard/query'))window.__queries.push(JSON.parse(args[1].body));return window.__originalFetch(...args);};const input=document.querySelector('#hxcTier');input.dispatchEvent(new CompositionEvent('compositionstart',{bubbles:true}));input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',isComposing:true,bubbles:true}));`,
      );
      await delay();
      assert.equal(
        await evaluate(`window.__queries.length`),
        0,
        "IME candidate Enter must not query",
      );
      await evaluate(
        `document.querySelector('#hxcTier').dispatchEvent(new CompositionEvent('compositionend',{bubbles:true}));window.fetch=async(...args)=>{if(String(args[0]).endsWith('/hxc-dashboard/query')){const q=JSON.parse(args[1].body);if(q.filters.stage?.[0]==='active_used'){const response=await window.__originalFetch(...args);await new Promise(resolve=>window.__releaseOld=resolve);return response;}}return window.__originalFetch(...args);};document.querySelector('#hxcStage').value='active_used';document.querySelector('#hxcApply').click();`,
      );
      await poll(
        () => evaluate(`!!window.__releaseOld`),
        "old response must be held",
      );
      await evaluate(
        `document.querySelector('#hxcStage').value='';document.querySelector('#hxcApply').click();`,
      );
      await poll(
        () =>
          evaluate(
            `document.querySelector('#hxcMeta').textContent.includes('共 30 人')`,
          ),
        "newer request result",
      );
      await evaluate(`window.__releaseOld();`);
      await delay();
      assert.ok(
        await evaluate(
          `document.querySelector('#hxcMeta').textContent.includes('共 30 人')`,
        ),
        "late response must not replace newer result",
      );
      await evaluate(
        `window.fetch=async(...args)=>{if(String(args[0]).endsWith('/hxc-dashboard/query'))throw new Error('fixture unavailable');return window.__originalFetch(...args);};document.querySelector('#hxcStage').value='active_unused';document.querySelector('#hxcApply').click();`,
      );
      await poll(
        () =>
          evaluate(
            `document.querySelector('#hxcMeta').textContent.includes('保留上次成功结果')`,
          ),
        "failed query retains data",
      );
      assert.equal(
        await evaluate(`document.querySelector('.dw-card strong').textContent`),
        "30",
      );
      await evaluate(
        `document.querySelector('#hxcStage').value='active_used';window.fetch=async(...args)=>{if(String(args[0]).endsWith('/hxc-dashboard/query'))window.__retried=JSON.parse(args[1].body);return window.__originalFetch(...args);};[...document.querySelectorAll('button')].find(b=>b.textContent==='重试上次查询').click();`,
      );
      await poll(
        () =>
          evaluate(
            `document.querySelector('#hxcMeta').textContent.includes('共 0 人')`,
          ),
        "retry response",
      );
      assert.deepEqual(
        await evaluate(`window.__retried.filters.stage`),
        ["active_unused"],
        "retry freezes failed request despite changed draft",
      );
      await evaluate(
        `window.fetch=window.__originalFetch;document.querySelector('#hxcStage').value='';document.querySelector('#hxcApply').click();`,
      );
      await poll(
        () =>
          evaluate(
            `document.querySelector('#hxcMeta').textContent.includes('共 30 人')`,
          ),
        "restore successful scope",
      );
    }
    await call("Emulation.setDeviceMetricsOverride", {
      width: 390,
      height: 844,
      deviceScaleFactor: 1,
      mobile: true,
    });
    await delay();
    const mobile = await call("Page.captureScreenshot", { format: "png" });
    await fs.writeFile(
      path.join(output, name + "-mobile.png"),
      Buffer.from(mobile.data, "base64"),
    );
    await evaluate(`document.querySelector('[data-workspace-tab=details]').click()`);
    await poll(()=>evaluate(`!!document.querySelector('.tabulator-header')`),"mobile details");
    assert.ok(await evaluate(`document.querySelector('.tabulator-tableholder').scrollWidth >= document.querySelector('.tabulator-tableholder').clientWidth`),"grid scroll surface");
    const detailMobile=await call("Page.captureScreenshot",{format:"png"});await fs.writeFile(path.join(output,name+"-details-mobile.png"),Buffer.from(detailMobile.data,"base64"));
    await call("Page.reload");
    await poll(()=>evaluate(`document.querySelector('[data-workspace-page=details]') && document.querySelectorAll('.tabulator-row:not(.tabulator-group)').length>0`),name+" direct detail reload").catch(async e=>{throw new Error(e.message+" "+await evaluate(`JSON.stringify({url:location.href,text:document.body.innerText.slice(0,1800),page:document.querySelector('[data-workspace-page]')?.dataset.workspacePage})`))});
  }
  // Snapshot-bound pagination, full aggregate and saved view CAS through the Host.
  const adminCall = (url, body, key, method = "POST") =>
    evaluate(
      `(async()=>{const r=await fetch(${JSON.stringify(url)},{method:${JSON.stringify(method)},headers:{'Content-Type':'application/json','X-CSRF-Token':${JSON.stringify(csrf)},'Idempotency-Key':${JSON.stringify(key)}},${method === "GET" ? "" : `body:JSON.stringify(${JSON.stringify(body)})`}});return {status:r.status,data:await r.json()};})()`,
    );
  const page = await adminCall(
    "/api/admin/hxc-dashboard/query",
    { filters: {}, limit: 10 },
    "page",
  );
  assert.equal(page.data.items.length, 10);
  assert.equal(page.data.total, 30);
  const second = await adminCall(
    "/api/admin/hxc-dashboard/query",
    { filters: {}, limit: 10, cursor: page.data.next_cursor },
    "page2",
  );
  assert.equal(second.data.items.length, 10);
  assert.equal(second.data.total, 30);
  assert.notEqual(second.data.items[0].user_ref, page.data.items[0].user_ref);
  const badCursor = await adminCall(
    "/api/admin/hxc-dashboard/query",
    {
      filters: { stage: ["active_used"] },
      limit: 10,
      cursor: page.data.next_cursor,
    },
    "page3",
  );
  assert.equal(badCursor.status, 400);
  const saved = await adminCall(
    "/api/admin/hxc-dashboard/views",
    {
      name: "浏览器验收视图",
      config: {
        query: { filters: { stage: ["active_used"] } },
        presentation: { metrics: ["total", "active_used"] },
      },
    },
    "browser-view-1",
  );
  assert.equal(saved.status, 200);
  const replay = await adminCall(
    "/api/admin/hxc-dashboard/views",
    {
      name: "浏览器验收视图",
      config: {
        query: { filters: { stage: ["active_used"] } },
        presentation: { metrics: ["total", "active_used"] },
      },
    },
    "browser-view-1",
  );
  assert.equal(replay.data.view.id, saved.data.view.id);
  const conflict = await adminCall(
    "/api/admin/hxc-dashboard/views",
    { ...saved.data.view, version: 99 },
    "browser-view-conflict",
  );
  assert.equal(conflict.status, 409);
  assert.ok(Number.isInteger(saved.data.view.id), "persisted view id");
  await call("Page.navigate", { url: "about:blank" });
  await poll(
    () => evaluate(`location.href==='about:blank'`),
    "leave previous document",
  );
  await call("Page.navigate", {
    url: origin + "/admin/hxc-dashboard#view=" + saved.data.view.id,
  });
  await poll(
    () =>
      evaluate(
        `document.querySelector('#hxcMeta')?.textContent.includes('共 0 人')`,
      ),
    "saved view did not restore",
  );
  assert.equal(
    await evaluate(`document.querySelectorAll('.dw-card').length`),
    2,
  );
  await evaluate(`document.querySelector('.dw-config-button').click();document.querySelector('input[data-metric="total"]').click();document.querySelector('dialog[open] button[aria-label="关闭设置"]').click()`);
  assert.equal(await evaluate(`document.querySelectorAll('.dw-card[data-metric="total"]').length`),0,"dashboard selection changes cards");
  await evaluate(`(()=>{const select=document.querySelector('.dw-bar > select');select.value='';select.dispatchEvent(new Event('change'))})()`);
  await poll(()=>evaluate(`!!document.querySelector('.dw-dialog[open]')`),"unsaved view guard");
  await evaluate(`[...document.querySelectorAll('.dw-dialog button')].find(b=>b.textContent==='取消').click()`);
  assert.equal(await evaluate(`document.querySelectorAll('.dw-card').length`),1,"cancel keeps current settings");
  await evaluate(`(()=>{const select=document.querySelector('.dw-bar > select');select.value='';select.dispatchEvent(new Event('change'))})()`);
  await poll(()=>evaluate(`!!document.querySelector('.dw-dialog[open]')`),"discard guard");
  await evaluate(`[...document.querySelectorAll('.dw-dialog button')].find(b=>b.textContent==='放弃修改').click()`);
  await poll(()=>evaluate(`document.querySelector('#hxcMeta')?.textContent.includes('共 30 人') && document.querySelectorAll('.dw-card').length===4`),"discard restores default view scope and presentation");
  const grouped = await adminCall(
    "/api/admin/hxc-dashboard/query",
    { filters: {}, group_by: "stage", limit: 10 },
    "browser-group",
  );
  assert.equal(
    grouped.data.groups[0].count,
    30,
    "group counts cover all pages",
  );
  const productShare = await adminCall(
    `/api/admin/service-period-products/${product}/member-grid/scoped-shares`,
    {
      mode: "details",
      fields: ["remaining_days"],
      config: {
        schema_version: 1,
        filter: { logic: "and", conditions: [] },
        sorts: [],
        groups: [],
      },
    },
    "browser-product-share",
  );
  assert.equal(productShare.status, 200);
  const publicCall = (url, body) =>
    evaluate(
      `(async()=>{const r=await fetch(${JSON.stringify(url)},{method:'POST',credentials:'omit',headers:{'Content-Type':'application/json'},body:JSON.stringify(${JSON.stringify(body)})});return {status:r.status,data:await r.json()};})()`,
    );
  const pq = {
    token: productShare.data.token,
    config: {
      schema_version: 1,
      filter: { logic: "and", conditions: [] },
      sorts: [],
      groups: [],
    },
  };
  const productPublic = await publicCall(
    "/api/public/service-period-member-grid/scoped-query",
    pq,
  );
  assert.equal(productPublic.status, 200);
  assert.equal(productPublic.data.total, 1);
  const internalGrid = await adminCall(
    `/api/admin/service-period-products/${product}/member-grid/query`,
    { config: pq.config, limit: 50 },
    "browser-product-internal",
  );
  const reference = internalGrid.data.rows[0].record_id;
  const visitorComputed = createHmac("sha256", productShare.data.token)
    .update(
      "dashboard-share-v2\0product-row\0" +
        productShare.data.share.id +
        "\0" +
        reference,
    )
    .digest("base64url")
    .slice(0, 22);
  assert.notEqual(
    productPublic.data.items[0].user_ref,
    visitorComputed,
    "public bearer must not be the pseudonym key",
  );
  assert.equal(
    (
      await publicCall(
        "/api/public/service-period-member-grid/scoped-query",
        pq,
      )
    ).data.items[0].user_ref,
    productPublic.data.items[0].user_ref,
    "opaque reference remains stable",
  );

  assert.deepEqual(
    Object.keys(productPublic.data.items[0]).sort(),
    ["__groupCounts", "__groupValues", "remaining_days", "user_ref"].sort(),
  );
  const forbidden = await publicCall(
    "/api/public/service-period-member-grid/scoped-query",
    {
      ...pq,
      config: {
        ...pq.config,
        filter: {
          logic: "and",
          conditions: [{ field: "member", operator: "contains", value: "a" }],
        },
      },
    },
  );
  assert.equal(forbidden.status, 400);
  const productRevoke = await adminCall(
    `/api/admin/service-period-products/${product}/member-grid/scoped-shares`,
    {
      id: productShare.data.share.id,
      version: productShare.data.share.version,
    },
    "browser-product-revoke",
    "DELETE",
  );
  assert.equal(productRevoke.status, 200);
  assert.equal(
    (
      await publicCall(
        "/api/public/service-period-member-grid/scoped-query",
        pq,
      )
    ).status,
    410,
  );
  // Exercise the actual anonymous route and its server field projection.
  const issued = await evaluate(
    `(async()=>{const r=await fetch('/api/admin/hxc-dashboard/shares',{method:'POST',headers:{'Content-Type':'application/json','X-CSRF-Token':${JSON.stringify(csrf)},'Idempotency-Key':'browser-hxc-share-1'},body:JSON.stringify({mode:'details',fields:['stage','subscription_tier'],config:{query:{filters:{stage:['registered_no_active_membership']}}}})});return {status:r.status,value:await r.json()};})()`,
  );
  assert.equal(issued.status, 200, "issue authenticated share");
  const token = issued.value.token;
  const metricsOnly = await adminCall('/api/admin/hxc-dashboard/shares', {mode:'metrics',fields:[],config:{query:{filters:{}}}}, 'browser-metrics-only');
  assert.equal(metricsOnly.status,200);
  await call("Network.clearBrowserCookies");
  await call("Page.navigate", {url:origin+'/shared/data-dashboard?tab=details#hxc:'+metricsOnly.data.token});
  await poll(()=>evaluate(`document.querySelector('.dw-card strong')?.textContent==='30'`),"metrics only view");
  assert.equal(await evaluate(`document.querySelector('[data-workspace-tab=details]').hidden`),true,"tampered tab cannot reveal detail navigation");
  assert.equal(await evaluate(`document.querySelectorAll('.tabulator').length`),0,"metrics only cannot mount rows");
  const metricsResponse=await publicCall('/api/public/hxc-dashboard/query',{token:metricsOnly.data.token});
  assert.equal((metricsResponse.data.items||[]).length,0,"metrics only API excludes detail rows");
  await call("Page.navigate", {
    url: origin + "/shared/data-dashboard#hxc:" + token,
  });
  await poll(
    () =>
      evaluate(`document.querySelector('.dw-card strong')?.textContent==='30'`),
    "anonymous dashboard failed",
  );
  assert.ok(
    await evaluate(`document.documentElement.scrollWidth<=innerWidth+1`),
    "public mobile must not overflow viewport",
  );
  await evaluate(`document.querySelector('[data-workspace-tab=details]').click()`);
  await evaluate(
    `document.querySelector('select[aria-label="分组"]').value='stage';[...document.querySelectorAll('button')].find(b=>b.textContent==='应用').click();`,
  );
  await poll(
    () =>
      evaluate(
        `document.querySelector('.tabulator-group')?.textContent.includes('30 人')`,
      ),
    "server group total shown",
  );
  await evaluate(`document.querySelector('.tabulator-group').click();`);
  assert.equal(
    await evaluate(
      `document.querySelector('.tabulator-group').classList.contains('tabulator-group-visible')`,
    ),
    false,
    "group collapses",
  );
  await evaluate(
    `document.querySelector('select[aria-label="分组"]').value='subscription_tier';[...document.querySelectorAll('button')].find(b=>b.textContent==='应用').click();`,
  );
  await poll(
    () =>
      evaluate(
        `document.querySelector('.tabulator-group')?.textContent.includes('未填写') && document.querySelector('.tabulator-group')?.textContent.includes('30 人')`,
      ),
    "empty tier has full group count",
  );
  const pubshot = await call("Page.captureScreenshot", { format: "png" });
  await fs.writeFile(
    path.join(output, "public-mobile.png"),
    Buffer.from(pubshot.data, "base64"),
  );
  assert.equal(
    await evaluate(`location.hash`),
    "",
    "credential removed from fragment",
  );
  assert.equal(
    await evaluate(`document.querySelectorAll('.admin-sidebar').length`),
    0,
    "public page has no admin shell",
  );
  const safe = await evaluate(
    `(async()=>{const r=await fetch('/api/public/hxc-dashboard/query',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:${JSON.stringify(token)},query:{filters:{stage:['active_used']}}})});return {status:r.status,data:await r.json()};})()`,
  );
  assert.equal(safe.status, 200);
  assert.equal(safe.data.total, 0, "extra filter must intersect frozen scope");
  await call("Network.setCookies", {
    cookies: [
      {
        name: "aicrm_admin_session",
        value: session,
        url: origin,
        secure: true,
      },
      { name: "aicrm_admin_csrf", value: csrf, url: origin, secure: true },
    ],
  });
  const revoked = await evaluate(
    `(async()=>{const r=await fetch('/api/admin/hxc-dashboard/shares',{method:'DELETE',headers:{'Content-Type':'application/json','X-CSRF-Token':${JSON.stringify(csrf)},'Idempotency-Key':'browser-hxc-revoke-1'},body:JSON.stringify({id:${issued.value.share.id},version:${issued.value.share.version}})});return r.status;})()`,
  );
  assert.equal(revoked, 200);
  const gone = await evaluate(
    `(async()=>{const r=await fetch('/api/public/hxc-dashboard/query',{method:'POST',credentials:'omit',headers:{'Content-Type':'application/json'},body:JSON.stringify({token:${JSON.stringify(token)}})});return r.status;})()`,
  );
  assert.equal(gone, 410);
  console.log(
    "data_workspace_chromium: PASS desktop mobile IME stale_response frozen_retry pagination saved_view groups anonymous_scope revocation",
  );
} finally {
  ws?.close();
  child.kill();
  await new Promise((resolve) => {
    if (child.exitCode !== null) resolve();
    else {
      child.once("exit", resolve);
      setTimeout(resolve, 1500);
    }
  });
  await fs.rm(profile, {
    recursive: true,
    force: true,
    maxRetries: 5,
    retryDelay: 100,
  });
}
