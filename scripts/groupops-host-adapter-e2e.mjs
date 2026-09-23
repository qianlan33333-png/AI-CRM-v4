import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { build } from "esbuild";
import { JSDOM, VirtualConsole } from "jsdom";
import { fileURLToPath } from "node:url";

const repository = new URL("..", import.meta.url);
const source = new URL("../web/v3/groupOpsHostAdapter.ts", import.meta.url);
const bundle = await build({
  entryPoints: [fileURLToPath(source)],
  bundle: true,
  write: false,
  format: "iife",
  platform: "browser",
  target: "es2020",
  logLevel: "silent",
});
const dom = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "https://groupops.test/admin/groupops.html",
  runScripts: "outside-only",
});
const { window } = dom;
window.Headers = Headers;
window.Response = Response;
Object.defineProperty(window, "crypto", { configurable: true, value: crypto });
window.document.cookie = "aicrm_admin_csrf=test-csrf";

const mutations = [];
let explicitRevisionWriteCalls = 0;
const foreignRequests = [];
const foreignPayload = { items: [{ staff_id: 5, sender_userid: "external-user", display_name: "External name" }] };
let nodes = [];
let savedOwner = [];
let operationMemberReads = 0;
let materialDetailReads = 0;
let ownerProjection = { staff_id: 7, sender_userid: "real-owner", display_name: "真实昵称 · 完整姓名", name_source: "wecom_profile", profile_read_state: "ready" };
const planPage = (items, total = items.length, offset = 0, hasMore = false) => ({ items, total, limit: 50, offset, has_more: hasMore });
let listPayload = planPage([{ plan_id: 41, name: "列表计划", revision: 7, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 3 }]);
const detail = () => ({
  plan: { plan_id: 41, name: "浏览器计划", revision: 7, status: "draft", plan_type: "standard", owner: ownerProjection },
  nodes,
  members: savedOwner,
});
window.fetch = async (input, init = {}) => {
  const url = new URL(String(input), window.location.href);
  if (url.origin !== window.location.origin) {
    foreignRequests.push({ input, init });
    return new Response(JSON.stringify(foreignPayload), { status: 200 });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && (!init.method || init.method === "GET")) {
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  if (url.pathname === "/api/admin/image-library/99" && (!init.method || init.method === "GET")) {
    materialDetailReads += 1;
    return new Response(JSON.stringify({ code: "NOT_FOUND" }), { status: 404, headers: { "content-type": "application/json" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && (!init.method || init.method === "GET")) {
    return new Response(JSON.stringify(listPayload), { status: 200, headers: { "content-type": "application/json" } });
  }
  if (url.pathname === "/api/admin/common/operation-members") {
    operationMemberReads += 1;
    return new Response(JSON.stringify({ items: [] }), { status: 200 });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups") {
    return new Response(JSON.stringify({ items: [], has_more: false }), { status: 200 });
  }
  if (/\/plans\/41\/nodes(?:\/\d+)?$/.test(url.pathname) && (init.method === "POST" || init.method === "PUT")) {
    mutations.push(JSON.parse(String(init.body || "{}")));
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/enable" && init.method === "POST") {
    explicitRevisionWriteCalls += 1;
    return new Response(JSON.stringify(detail()), { status: 200, headers: { "content-type": "application/json" } });
  }
  throw new Error(`unexpected request ${init.method || "GET"} ${url.pathname}`);
};
window.eval(bundle.outputFiles[0].text);

const foreignOptions = { method: "POST", headers: { "X-Original": "preserved" }, body: "original body" };
await window.fetch("https://external.test/api/admin/common/operation-members/sync", foreignOptions);
assert.equal(foreignRequests[0].init, foreignOptions, "foreign requests must retain the original options unchanged");
assert.equal(new Headers(foreignRequests[0].init.headers).has("X-CSRF-Token"), false);
assert.equal(new Headers(foreignRequests[0].init.headers).has("Idempotency-Key"), false);
assert.equal(foreignRequests[0].init.body, "original body");
const foreignResult = await (await window.fetch("https://external.test/api/admin/common/operation-members?scope=group_ops")).json();
assert.deepEqual(foreignResult, foreignPayload, "foreign same-path GET payload must not be projected");

const host = window.AdminApi;
assert.equal(typeof host?.requestJson, "function", "Group Ops Host bridge must expose requestJson");
let projectedList = await host.requestJson("/api/admin/automation-conversion/group-ops/plans");
assert.equal(projectedList.items[0].bound_group_count, 3, "list binding count must come from the List DTO without a detail read");
listPayload = planPage([{ plan_id: 41, name: "旧服务列表计划", revision: 7, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0 }]);
projectedList = await host.requestJson("/api/admin/automation-conversion/group-ops/plans");
assert.equal(projectedList.items[0].bound_group_count, null, "an older List DTO must remain readable as an explicit unknown");
listPayload = planPage([
  { plan_id: 41, name: "已渲染计划", revision: 8, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 3 },
  { plan_id: 42, name: "错误列表计划", revision: 9, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: -1 },
]);
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划绑定群数数据无效/, "a later negative List DTO count must reject the complete page before it publishes an earlier revision");
listPayload = planPage([{ plan_id: 41, name: "错误列表计划", revision: 10, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 1.5 }]);
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划绑定群数数据无效/, "fractional List DTO counts must not become zero");
listPayload = planPage([{ plan_id: "41", name: "字符串计划 ID", revision: 11, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 0 }]);
assert.equal((await host.requestJson("/api/admin/automation-conversion/group-ops/plans")).items[0].id, 41, "the documented string plan ID remains a valid projection");
listPayload = { ...listPayload, total: "1" };
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划列表总数数据无效/, "a string total must not become a valid numeric page");
listPayload = planPage([{ plan_id: 41, name: "错误版本", revision: true, status: "draft", plan_type: "standard", owner: ownerProjection, queue_count: 0, bound_group_count: 0 }]);
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans"), /计划列表版本数据无效/, "a boolean List revision must not become a CAS value");
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/enable", { method: "POST", body: { expected_revision: true } }), /计划版本数据无效/, "a boolean expected revision must fail before a write");
await assert.rejects(() => host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/enable", { method: "POST", body: { expected_revision: undefined } }), /计划版本数据无效/, "an explicitly undefined expected revision must fail before a write");
assert.equal(explicitRevisionWriteCalls, 0, "invalid explicit revisions must send zero writes");
await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/nodes", {
  method: "POST",
  body: {
    sort_order: 10,
    day_index: 2,
    scheduled_time: "09:30",
    action_title: "默认排序动作",
    text_content: "真实内容",
    status: "active",
  },
});
assert.deepEqual(mutations[0], {
  expected_revision: 7,
  position: 1,
  kind: "message",
  day_index: 2,
  scheduled_time: "09:30",
  trigger_time_label: "09:30",
  action_title: "默认排序动作",
  status: "active",
  message_text: "真实内容",
  delay_minutes: 0,
  material_plan: { references: [] },
});

nodes = [{ node_id: 88, position: 2 }];
await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/nodes/88", {
  method: "PUT",
  body: {
    sort_order: 10,
    day_index: 3,
    scheduled_time: "10:00",
    action_title: "编辑保留位置",
    text_content: "更新内容",
    status: "active",
  },
});
assert.equal(mutations[1].position, 2, "out-of-range donor edit order must retain the persisted V3 position");
assert.equal(mutations[1].expected_revision, 7);
assert.equal(mutations[1].action_title, "编辑保留位置");
nodes = [{ node_id: 99, position: 1, kind: "message", material_plan: { references: [{ kind: "image", id: 99 }] } }];
const unresolvedNode = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41/nodes");
assert.deepEqual(Array.from(unresolvedNode.items[0].content_package_json.image_library_ids), [99], "a missing Media detail cannot erase the persisted node reference");
assert.match(unresolvedNode.items[0].content_material_records[0].disabledReason, /待目录确认/, "a plan list must not fan out Media reads before an operator opens that node");
assert.equal(materialDetailReads, 0, "the donor projection and revision path do not wait on every historical Media record");
window.AICRMGroupOpsV3Content.openReadonly({ value: unresolvedNode.items[0].content_package_json, selectedRecords: unresolvedNode.items[0].content_material_records });
for (let attempt = 0; attempt < 20 && !window.document.querySelector('[data-v3-content-readonly]'); attempt += 1) await new Promise((resolve) => setTimeout(resolve, 0));
assert.match(window.document.querySelector('[data-v3-content-readonly]')?.textContent || '', /素材已删除，保留当前引用；可明确移除。/, "opening one node turns a 404 into an explicit retained state");
assert.equal(materialDetailReads, 1, "only the opened node resolves its Media details");
window.document.querySelector('[data-v3-content-readonly-close]').click();
savedOwner = [{ staff_id: 7 }];
const ownerReadsBeforeProjection = operationMemberReads;
let projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7");
assert.equal(projectedOwner.owner_name, "真实昵称 · 完整姓名", "overview reads the server-owned responsible-member projection");
assert.equal(operationMemberReads, ownerReadsBeforeProjection, "detail must not perform a second operation-member directory read");
ownerProjection = { staff_id: 7, display_name: "保留的历史姓名", name_source: "wecom_profile", profile_read_state: "unavailable", profile_read_error_code: "provider_unavailable" };
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "7", "directory outage must preserve the saved owner binding");
assert.equal(projectedOwner.owner_name, "负责人目录不可用", "directory outage remains distinct from an unconfigured owner");
ownerProjection = { staff_id: 7 };
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_name, "负责人目录未同步", "a missing directory row remains explicit");
ownerProjection = {};
projectedOwner = await host.requestJson("/api/admin/automation-conversion/group-ops/plans/41");
assert.equal(projectedOwner.owner_userid, "");
assert.equal(projectedOwner.owner_name, "未配置负责人", "an unconfigured owner stays distinct from directory states");
console.log("groupops-host-adapter: PASS");
dom.window.close();

// This uses the frozen picker and standard DOM together. The Group Ops API
// returns the local staff key together with the trusted WeCom sender identity.
// The Host keeps the staff key for plan commands while adapting the frozen
// picker to display the sender identity as its second line.
const pickerSource = await readFile(new URL("../internal/webshell/static/admin_console/operation_member_picker_dd8d60d.js", import.meta.url), "utf8");
const waitFor = async (condition, message) => {
  for (let attempt = 0; attempt < 120; attempt += 1) {
    if (condition()) return;
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  throw new Error(typeof message === "function" ? message() : message);
};
const groupOpsListDocument = (mode, planID = "") => `<!doctype html><html><body>
  <header class="admin-topbar"><div class="admin-topbar-head"><h1 class="admin-page-title">群运营计划</h1></div><div class="admin-topbar-meta"></div></header>
  <main id="group-ops-app" data-page-mode="${mode}"${planID ? ` data-plan-id="${planID}"` : ""}></main>
</body></html>`;
const groupOpsCreateAction = (view) => view.document.querySelector('[data-page-header-actions="groupops"] [data-page-header-action="create-plan"]');
// The Host lease is deliberately narrower than a plan-ID cache. These
// controlled responses exercise same-kind reentry and A -> B -> A: late A
// success/error/finally must not replace the current A view or its subsequent
// write/readback.
const pending = () => {
  let resolve;
  let reject;
  const promise = new Promise((nextResolve, nextReject) => {
    resolve = nextResolve;
    reject = nextReject;
  });
  return { promise, resolve, reject };
};
const raceResponse = (body, status = 200) =>
  new Response(JSON.stringify(body), {
    status,
    headers: { "content-type": "application/json" },
  });
const raceReads = [];
const raceWrites = [];
let raceEnableAttempts = 0;
const raceJourney = new JSDOM("<!doctype html><html><body></body></html>", {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/41",
  runScripts: "outside-only",
});
const raceWindow = raceJourney.window;
raceWindow.Headers = Headers;
raceWindow.Response = Response;
Object.defineProperty(raceWindow, "crypto", {
  configurable: true,
  value: crypto,
});
raceWindow.fetch = (input, init = {}) => {
  const url = new URL(String(input), raceWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (
    method === "GET" &&
    (/\/plans\/(41|42|88|90)$/.test(url.pathname) ||
      (url.pathname === "/api/admin/automation-conversion/group-ops/groups" &&
        url.searchParams.get("owner_userid") === null))
  ) {
    const request = pending();
    raceReads.push({ path: url.pathname + url.search, request });
    return request.promise;
  }
  if (
    url.pathname === "/api/admin/automation-conversion/group-ops/groups/sync" &&
    method === "POST"
  )
    return Promise.resolve(raceResponse({
      total: 1,
      items: [{
        chat_reference: "current-group",
        display_name: "同步后的当前群",
        owner_staff_id: 7,
        member_count: 22,
        external_member_count: 13,
      }],
    }));
  if (
    url.pathname ===
      "/api/admin/automation-conversion/group-ops/plans/41/enable" &&
    method === "POST"
  ) {
    raceWrites.push(JSON.parse(String(init.body || "{}")));
    raceEnableAttempts += 1;
    if (raceEnableAttempts === 1)
      return Promise.resolve(raceResponse({ code: "operations_conflict" }, 409));
    return Promise.resolve(raceResponse({ plan: { plan_id: 41, revision: 52 } }));
  }
  throw new Error(
    `unexpected race request ${method} ${url.pathname}${url.search}`,
  );
};
raceWindow.eval(bundle.outputFiles[0].text);
const raceHost = raceWindow.AdminApi;
const staleSuccessA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const staleErrorA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const interveningB = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/42",
);
const currentA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const currentAGroups = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41/groups",
);
await waitFor(
  () => raceReads.length === 4,
  "same-kind reentry did not create four independent scoped detail epochs",
);
const nextRaceRead = (expectedPath) => {
  const next = raceReads.shift();
  assert.equal(
    next.path,
    expectedPath,
    "race fixture must preserve request order",
  );
  return next.request;
};
const staleSuccessPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const staleErrorPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
const interveningBPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/42",
);
const currentPlan = nextRaceRead(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
currentPlan.resolve(
  raceResponse({
    plan: { plan_id: 41, name: "current", revision: 50 },
    group_assets: [{ asset_reference: "current-group" }],
  }),
);
const [currentPlanPayload, currentGroupPayload] = await Promise.all([
  currentA,
  currentAGroups,
]);
assert.equal(
  currentPlanPayload.revision,
  50,
  "the current A epoch must publish its own revision before old A completes",
);
assert.equal(
  currentPlanPayload.groups_summary,
  currentGroupPayload.summary,
  "the paired routes must retain one current summary view reference",
);
staleSuccessPlan.resolve(
  raceResponse({
    plan: { plan_id: 41, name: "stale", revision: 3 },
    group_assets: [{ asset_reference: "stale-group" }],
  }),
);
staleErrorPlan.resolve(raceResponse({ code: "operations_conflict" }, 409));
interveningBPlan.resolve(
  raceResponse({
    plan: { plan_id: 42, name: "B", revision: 4 },
    group_assets: [],
  }),
);
await staleSuccessA;
await assert.rejects(staleErrorA, /计划状态、版本或配置不满足要求/);
await interveningB;
assert.equal(
  currentGroupPayload.items[0].group_name,
  "群名称待同步",
  "late A success cannot overwrite the published current scoped binding view",
);
assert.equal(
  currentPlanPayload.groups_summary,
  currentGroupPayload.summary,
  "late A success/error/finally cannot replace the current summary view reference",
);

// This order is distinct: old A finishes while the newer A is still pending.
// Its finally must leave the newer epoch claim installed so that both newer
// routes publish the same donor view once their shared reads arrive.
const oldPendingA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/90",
);
const newPendingA = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/90",
);
const newPendingGroups = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/90/groups",
);
await waitFor(
  () => raceReads.length === 2,
  "pending-order fixture did not create distinct old and new A epochs",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/90").resolve(
  raceResponse({
    plan: { plan_id: 90, name: "old pending", revision: 1 },
    group_assets: [],
  }),
);
await oldPendingA;
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/90").resolve(
  raceResponse({
    plan: { plan_id: 90, name: "new pending", revision: 2 },
    group_assets: [],
  }),
);
const [newPendingPlan, newPendingGroupPayload] = await Promise.all([
  newPendingA,
  newPendingGroups,
]);
assert.equal(
  newPendingPlan.groups_summary,
  newPendingGroupPayload.summary,
  "old A finally must not delete the newer pending A epoch",
);

// A failed epoch cannot become a retained failed snapshot. The retry starts a
// fresh pair immediately and returns its own current DTOs.
const failed88 = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/88",
);
await waitFor(
  () => raceReads.length === 1,
  "failed detail did not start a new scoped lease",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/88").resolve(
  raceResponse({ code: "service_unavailable" }, 503),
);
await assert.rejects(failed88, /HTTP 503/);
const retry88 = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/88",
);
const retry88Groups = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/88/groups",
);
await waitFor(
  () => raceReads.length === 1,
  "retry did not start a fresh paired lease",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/88").resolve(
  raceResponse({
    plan: { plan_id: 88, name: "retry", revision: 6 },
    group_assets: [],
  }),
);
await Promise.all([retry88, retry88Groups]);

// Sync invalidates the initial epoch, reads fresh server data, and mutates the
// current donor view rather than an older A array. It does not silently discard
// the revision the operator saw before starting a write.
raceWindow.document.body.innerHTML =
  '<main id="group-ops-app" data-plan-id="41"></main>';
const sync = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/groups/sync",
  { method: "POST", body: { owner_userid: 7 } },
);
await waitFor(
  () => raceReads.length === 1,
  "sync readback did not force a fresh scoped plan read",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/41").resolve(
  raceResponse({
    plan: { plan_id: 41, name: "current", revision: 51 },
    group_assets: [{ asset_reference: "current-group" }],
  }),
);
await sync;
assert.equal(
  currentGroupPayload.items[0].group_name,
  "同步后的当前群",
  "late A must not replace the group view that sync mutates",
);
const conflictingEnable = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41/enable",
  { method: "POST" },
);
assert.equal(raceReads.length, 0, "a write must retain the operator-visible revision instead of silently rereading it");
await assert.rejects(conflictingEnable, /计划状态、版本或配置不满足要求/);
assert.equal(raceWrites[0].expected_revision, 50, "the first write must preserve revision 50 and let the server reject the unseen revision 51");
const explicitReread = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41",
);
await waitFor(
  () => raceReads.length === 1,
  "explicit reread did not request a fresh scoped plan after the real conflict",
);
nextRaceRead("/api/admin/automation-conversion/group-ops/plans/41").resolve(
  raceResponse({
    plan: { plan_id: 41, name: "current", revision: 51 },
    group_assets: [{ asset_reference: "current-group" }],
  }),
);
const refreshedPlan = await explicitReread;
assert.equal(refreshedPlan.revision, 51, "the explicit reread publishes the new server revision");
const retriedEnable = raceHost.requestJson(
  "/api/admin/automation-conversion/group-ops/plans/41/enable",
  { method: "POST" },
);
assert.equal(raceReads.length, 0, "the post-reread write must use the newly read revision directly");
await retriedEnable;
assert.equal(raceWrites[1].expected_revision, 51, "only an explicit reread may advance the next write to revision 51");
console.log("groupops-hydration-epoch: PASS");
raceJourney.window.close();

const fullJourneyErrors = [];
const fullJourneyConsole = new VirtualConsole();
fullJourneyConsole.on("jsdomError", (error) => fullJourneyErrors.push(String(error?.message || error)));
const calls = [];
let memberRefreshAttempts = 0;
let ownerDirectoryFailures = 0;
let groupSyncAttempts = 0;
let failGroupReadback = false;
let failPlanReadback = false;
let failNextPlanReadbackAfterWrite = false;
let wrongPlanIDOnce = false;
let returnWrongPlanIDAfterWrite = false;
let delayNextPlanRead = false;
let releaseDelayedPlanRead = null;
let delayNodeContentDetail = false;
const releaseNodeContentDetails = [];
let saveFailure = "";
let dropCommittedGroupResponse = "";
let rejectGroupOnce = "";
const groupSelectionCommands = [];
const state = {
  revision: 4,
  plan: { plan_id: 41, name: "标准群运营计划", revision: 4, status: "paused", plan_type: "standard", updated_at: "2026-09-08T00:00:00Z" },
  members: [{ staff_id: 7 }],
  group_assets: [],
  nodes: [],
  directory: [{ chat_reference: "group-9", owner_staff_id: 9, display_name: "九号运营群", member_count: 12, external_member_count: 8 }],
};
const clone = (value) => JSON.parse(JSON.stringify(value));
const response = (body, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
const fullJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="41"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/41",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  virtualConsole: fullJourneyConsole,
});
const fullWindow = fullJourney.window;
fullWindow.Headers = Headers;
fullWindow.Response = Response;
Object.defineProperty(fullWindow, "crypto", { configurable: true, value: crypto });
fullWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
fullWindow.confirm = () => true;
fullWindow.AICRMMaterialPicker = { open() { throw new Error("V3 material adapter did not install"); } };
fullWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), fullWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : null;
  calls.push({ path: url.pathname + url.search, method, body, idempotencyKey: init.headers?.get?.("Idempotency-Key") || "" });
  const ownerFor = (staffID) => ({
    staff_id: staffID,
    sender_userid: staffID === 9 ? "wecom-replacement" : "wecom-owner",
    display_name: staffID === 9 ? "九号运营" : "一号运营",
    name_source: "wecom_profile",
    profile_read_state: "ready",
  });
  const detailPayload = () => ({ plan: { ...clone(state.plan), owner: ownerFor(Number(state.members[0]?.staff_id || 0)) }, members: clone(state.members), group_assets: clone(state.group_assets), nodes: clone(state.nodes) });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") {
    return response({
      scope: "group_ops",
      page_size: Number(url.searchParams.get("page_size") || 100),
      items: [
        { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" },
        { staff_id: 9, sender_userid: "wecom-replacement", display_name: "九号运营" },
      ],
    });
  }
  if (url.pathname === "/api/admin/common/operation-members/sync" && method === "POST") {
    memberRefreshAttempts += 1;
    assert.deepEqual(body, { scope: "group_ops", page_size: 100 }, "frozen picker refresh must use the scoped V3 command body");
    assert.equal(init.headers.get("X-CSRF-Token"), "test-csrf", "picker refresh must carry CSRF");
    assert(init.headers.get("Idempotency-Key"), "picker refresh must carry an idempotency key");
    if (memberRefreshAttempts === 1) return response({ error: { code: "provider_read_unavailable" } }, 503);
    return response({ items: [], page_size: 100 });
  }
  if (url.pathname === "/api/admin/image-library" && method === "GET") return response({ items: [{ id: 23, name: "节点封面", variant_url: "/api/admin/image-library/23/variants/thumb_160", enabled: true }], has_more: false });
  if (url.pathname === "/api/admin/image-library/23" && method === "GET") {
    if (delayNodeContentDetail) return new Promise((resolve) => { releaseNodeContentDetails.push(() => resolve(response({ item: { id: 23, name: "节点封面", variant_url: "/api/admin/image-library/23/variants/thumb_160", enabled: true } }))); });
    return response({ item: { id: 23, name: "节点封面", variant_url: "/api/admin/image-library/23/variants/thumb_160", enabled: true } });
  }
  if (url.pathname === "/api/admin/attachment-library" && method === "GET") return response({ items: [{ id: 24, name: "节点说明.pdf", mime_type: "application/pdf", enabled: true }], has_more: false });
  if (url.pathname === "/api/admin/attachment-library/24" && method === "GET") return response({ item: { id: 24, name: "节点说明.pdf", mime_type: "application/pdf", enabled: true } });
  if (url.pathname === "/api/admin/miniprogram-library" && method === "GET") return response({ items: [], has_more: false });
  if (url.pathname === "/api/admin/group-invite-library" && method === "GET") return response({ items: [], has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && method === "GET") {
    if (failPlanReadback) throw new Error("详情读取中断");
    if (wrongPlanIDOnce) {
      wrongPlanIDOnce = false;
      return response({ ...detailPayload(), plan: { ...clone(state.plan), plan_id: 99 } });
    }
    if (delayNextPlanRead) {
      delayNextPlanRead = false;
      const captured = response(detailPayload());
      return new Promise((resolve) => { releaseDelayedPlanRead = () => resolve(captured); });
    }
    return response(detailPayload());
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41" && method === "PUT") {
    if (saveFailure === "network") throw new Error("网络连接中断");
    if (saveFailure) return response({ code: saveFailure === "409" ? "operations_conflict" : "service_unavailable" }, Number(saveFailure));
    if (body.expected_revision !== state.revision) return response({ code: "operations_conflict" }, 409);
    state.plan.name = body.name;
    state.plan.plan_type = body.plan_type;
    if (body.owner_staff_id) state.members = [{ staff_id: Number(body.owner_staff_id) }];
    state.revision += 1;
    state.plan.revision = state.revision;
    if (failNextPlanReadbackAfterWrite) {
      failNextPlanReadbackAfterWrite = false;
      failPlanReadback = true;
    }
    if (returnWrongPlanIDAfterWrite) {
      returnWrongPlanIDAfterWrite = false;
      wrongPlanIDOnce = true;
    }
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/enable" && method === "POST") {
    if (body.expected_revision !== state.revision) return response({ code: "operations_conflict" }, 409);
    state.revision += 1;
    state.plan.status = "active";
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/groups" && method === "POST") {
    groupSelectionCommands.push({ reference: body.asset_reference, body: clone(body), idempotencyKey: init.headers?.get?.("Idempotency-Key") || "" });
    if (body.expected_revision !== state.revision) return response({ code: "operations_conflict" }, 409);
    if (!state.group_assets.some((item) => item.asset_reference === body.asset_reference)) state.group_assets.push({ asset_reference: body.asset_reference });
    state.revision += 1;
    state.plan.revision = state.revision;
    if (dropCommittedGroupResponse === body.asset_reference) {
      dropCommittedGroupResponse = "";
      // Another actor changes the plan after the accepted write. The Host must
      // prove the dropped response by Owner readback instead of changing this
      // request body or minting a second idempotency key.
      state.group_assets.push({ asset_reference: "other-concurrent" });
      state.revision += 1;
      state.plan.revision = state.revision;
      throw new Error("网络连接中断");
    }
    if (rejectGroupOnce === body.asset_reference) {
      rejectGroupOnce = "";
      state.group_assets = state.group_assets.filter((item) => item.asset_reference !== body.asset_reference);
      state.revision -= 1;
      state.plan.revision = state.revision;
      return response({ code: "service_unavailable" }, 503);
    }
    return response({ plan: clone(state.plan) });
  }
  if (/\/api\/admin\/automation-conversion\/group-ops\/plans\/41\/groups\/.+$/.test(url.pathname) && method === "DELETE") {
    if (body.expected_revision !== state.revision) return response({ code: "operations_conflict" }, 409);
    const reference = decodeURIComponent(url.pathname.split("/").pop() || "");
    state.group_assets = state.group_assets.filter((item) => item.asset_reference !== reference);
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/groups/group-9" && method === "DELETE") {
    state.group_assets = state.group_assets.filter((item) => item.asset_reference !== "group-9");
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/41/nodes" && method === "POST") {
    if (body.expected_revision !== state.revision) return response({ code: "operations_conflict" }, 409);
    state.nodes.push({ node_id: 101, ...body });
    state.revision += 1;
    state.plan.revision = state.revision;
    return response({ plan: clone(state.plan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups/sync" && method === "POST") {
    groupSyncAttempts++;
    assert.equal(body.owner_staff_id, 7, "refresh uses unsaved selected local member, not the saved owner");
    const refreshTarget = state.directory.find((item) => item.chat_reference === "group-10") || state.directory[0];
    refreshTarget.display_name = `同步群名${groupSyncAttempts}`;
    refreshTarget.member_count = 300 + groupSyncAttempts;
    refreshTarget.external_member_count = 230 + groupSyncAttempts;
    return response({ items: clone(state.directory), total: state.directory.length, limit: 100, offset: 0, has_more: false });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
    if (failGroupReadback) return response({ code: "directory_unavailable" }, 503);
    if (url.searchParams.get("owner_userid") === "9" && ownerDirectoryFailures++ === 0) return response({ error: { code: "provider_read_unavailable" } }, 503);
    return response({ items: clone(state.directory), total: state.directory.length, limit: 200, offset: 0, has_more: false });
  }
  throw new Error(`unexpected Group Ops request ${method} ${url.pathname}${url.search}`);
};

try {
  // The static picker is loaded before the Host in the rendered page. Its
  // direct fetch must retain the trusted external display identity while the
  // selection still writes the local staff identifier.
  fullWindow.eval(pickerSource);
  fullWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => fullWindow.document.querySelector('[data-action="pick-plan-owner"]'), "standard Group Ops detail did not render");
  const directMembers = await (await fullWindow.fetch("/api/admin/common/operation-members?scope=group_ops&page_size=100")).json();
  assert.deepEqual(directMembers.items, [
    { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" },
    { staff_id: 9, sender_userid: "wecom-replacement", display_name: "九号运营" },
  ], "the Host must leave the authorised Owner response unchanged outside its scoped V3 picker");

  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelectorAll('[data-v3-selection-session="staff"] [data-v3-staff-key]').length === 2, "V3 owner picker did not render both local staff");
  assert.equal(fullWindow.document.querySelector('[data-v3-selection-session="staff"] h3')?.textContent, "选择负责人", "Group Ops must declare the owner-selection context");
  assert.match(fullWindow.document.querySelector('[data-v3-selection-session="staff"]')?.textContent || "", /每次最多显示 100 位员工，可搜索定位/, "Group Ops must disclose its bounded owner directory rather than imply pagination coverage");
  assert.equal(fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="9"] strong')?.textContent, "九号运营");
  assert.match(fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="9"] span')?.textContent || "", /wecom-replacement/, "V3 row must retain the trusted WeCom user ID beside the local key");
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-reload]').click();
  await waitFor(() => memberRefreshAttempts === 1 && !fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-status]')?.textContent.includes("正在刷新"), "member refresh did not settle");
  assert.match(fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-status]')?.textContent || "", /读取失败|刷新失败|暂不可用/, "member refresh error did not render a retryable message");
  assert.equal(fullWindow.document.body.textContent.includes("[object Object]"), false, "member refresh must not stringify an error object");
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-reload]').click();
  await waitFor(() => memberRefreshAttempts === 2 && fullWindow.document.querySelectorAll('[data-v3-selection-session="staff"] [data-v3-staff-key]').length === 2 && fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]')?.disabled === false, "member refresh retry did not recover the V3 picker");
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="9"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[name="owner_userid"]')?.value === "9", "owner picker did not retain the selected local staff id");
  await waitFor(() => fullWindow.document.body.textContent.includes("群目录读取失败，请重试"), "owner directory failure must remain explicit rather than appear as an empty group list");
  // Required plan ownership may not close a V3 dialog after the only temporary
  // choice is removed: the existing draft stays visible and the caller gets an
  // actionable validation error instead of a silent no-op.
  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="9"]')?.getAttribute('aria-pressed') === 'true', "required owner picker did not restore its current local staff");
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="9"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-status]')?.textContent.includes("请选择一位负责人后再确认"), "removing the required owner must keep the dialog open with a validation error");
  assert.equal(fullWindow.document.querySelector('[name="owner_userid"]')?.value, "9", "a rejected empty owner commit must preserve the existing field");
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-cancel]').click();
  await waitFor(() => !fullWindow.document.querySelector('[data-v3-selection-session="staff"]'), "cancel must close the rejected owner draft without applying it");
  const draftName = fullWindow.document.querySelector('[name="plan_name"]');
  const writesBeforeBlankName = calls.filter((call) => call.method === "PUT").length;
  draftName.value = "   ";
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("请输入计划名称后再保存"), "blank plan name must be rejected locally");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeBlankName, "blank plan name must not issue a PUT");
  assert.equal(fullWindow.document.querySelector('[name="plan_name"]')?.value, "", "blank name must remain blank after local rejection");
  assert.equal(fullWindow.document.querySelector('[name="owner_userid"]')?.value, "9", "blank name rejection must preserve the other drafted fields");
  const doubleClickName = fullWindow.document.querySelector('[name="plan_name"]');
  doubleClickName.value = "一次提交";
  const writesBeforeDoubleClick = calls.filter((call) => call.method === "PUT").length;
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  fullWindow.document.querySelector('[data-action="save-active-detail-panel"]').click();
  await waitFor(() => state.plan.name === "一次提交", "single save did not persist before the shared-lock assertion");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeDoubleClick + 1, "two save controls must share one in-flight PUT");
  assert.equal(fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, false, "both save controls unlock only after authoritative readback");
  fullWindow.document.querySelector('[name="plan_name"]').value = "失败重试保留草稿";
  for (const failure of ["409", "500", "network"]) {
    saveFailure = failure;
    const writesBefore = calls.filter((call) => call.method === "PUT").length;
    const action = failure === "500" ? "save-active-detail-panel" : "save-plan";
    fullWindow.document.querySelector(`[data-action="${action}"]`).click();
    await waitFor(() => calls.filter((call) => call.method === "PUT").length > writesBefore && fullWindow.document.querySelector('.group-ops__notice--error'), "real save rejection must be visible");
    const retainedName = fullWindow.document.querySelector('[name="plan_name"]');
    assert.equal(retainedName.value, "失败重试保留草稿", "failure must retain the draft after the standard page rerenders");
    assert.equal(fullWindow.document.querySelector('.group-ops__notice--error')?.getAttribute('role'), 'alert', "save failure must remain an accessible alert");
    assert.equal(fullWindow.document.querySelector('[name="owner_userid"]').value, "9");
    assert.equal(state.plan.name, "一次提交", "failed save must not pretend the server changed");
    assert.equal(fullWindow.document.body.textContent.includes("已保存"), false);
  }
  saveFailure = "";
  assert.equal(fullWindow.document.querySelector('[name="status"]').value, "disabled", "paused server state must remain stopped in the frozen form");
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => state.plan.name === "失败重试保留草稿" && fullWindow.document.querySelector('[name="plan_name"]') !== draftName, "successful paused save must persist then reload the original form");
  assert.equal(state.plan.status, "paused", "saving paused configuration must not activate the plan");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "paused save must persist the chosen owner");
  assert.equal(calls.some((call) => call.method === "POST" && /\/(enable|disable)$/.test(call.path)), false, "paused save must not trigger a lifecycle command");
  assert.equal(fullWindow.document.querySelector('.group-ops__notice--error'), null);
  fullWindow.document.querySelector('[name="plan_name"]').value = "已写待回读";
  const writesBeforeReadbackFailure = calls.filter((call) => call.method === "PUT").length;
  failNextPlanReadbackAfterWrite = true;
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => calls.filter((call) => call.method === "PUT").length === writesBeforeReadbackFailure + 1 && fullWindow.document.querySelector('[data-action="reload-plan-detail"]'), "a successful PUT followed by failed detail readback must lock for an explicit retry");
  assert.match(fullWindow.document.body.textContent, /已保存，但读取最新配置失败/, "a successful PUT followed by failed detail readback must be explicit");
  assert.equal(fullWindow.document.querySelector('.group-ops__notice--error')?.getAttribute('role'), 'alert', "readback failure must remain an accessible alert");
  assert.equal(fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, true, "readback-pending state must keep basic save locked");
  assert.equal(fullWindow.document.querySelector('[data-action="save-active-detail-panel"]')?.disabled, true, "readback-pending state must keep shared save locked");
  fullWindow.document.querySelector('[data-action="save-active-detail-panel"]').click();
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeReadbackFailure + 1, "readback-pending state must not issue a replacement PUT");
  failPlanReadback = false;
  fullWindow.document.querySelector('[data-action="reload-plan-detail"]').click();
  await waitFor(() => state.plan.name === "已写待回读" && !fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, "retrying detail readback must restore the authoritative editable plan");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeReadbackFailure + 1, "readback retry must not submit a second PUT");
  fullWindow.document.querySelector('[name="plan_name"]').value = "错误详情 ID 只读恢复";
  const writesBeforeWrongID = calls.filter((call) => call.method === "PUT").length;
  returnWrongPlanIDAfterWrite = true;
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => calls.filter((call) => call.method === "PUT").length === writesBeforeWrongID + 1 && fullWindow.document.querySelector('[data-action="reload-plan-detail"]'), "a mismatched 200 detail ID must enter readback-only recovery");
  assert.match(fullWindow.document.body.textContent, /与当前页面不一致/, "a mismatched 200 detail ID must be visible instead of silently leaving the save locked");
  fullWindow.document.querySelector('[data-action="reload-plan-detail"]').click();
  await waitFor(() => state.plan.name === "错误详情 ID 只读恢复" && !fullWindow.document.querySelector('[data-action="save-plan"]')?.disabled, "mismatched detail recovery must read the authoritative plan without a second write");
  assert.equal(calls.filter((call) => call.method === "PUT").length, writesBeforeWrongID + 1, "mismatched detail recovery must stay read-only");
  fullWindow.document.querySelector('[name="status"]').value = "active";
  fullWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => state.plan.status === "active", "saving the selected owner did not enable the existing plan");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "Host must write the selected staff id as owner_staff_id");
  assert.equal(state.plan.name, "错误详情 ID 只读恢复", "successful retry must submit the retained authoritative draft");
  assert.equal(fullWindow.document.querySelector('.group-ops__notice--error'), null, "successful retry clears the prior failure");

  await waitFor(() => fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]'), "detail did not reload after owner save");
  // Asset commands are draft-only at the Owner boundary. The selector must
  // make that state explicit, and this scoped bind journey proceeds from a
  // genuine draft plan rather than weakening the service rule.
  state.plan = { ...state.plan, status: "draft" };
  fullWindow.dispatchEvent(new fullWindow.CustomEvent("aicrm:groupops-detail-refresh", { detail: { planId: 41 } }));
  await waitFor(
    () => fullWindow.document.querySelector('[data-action="open-group-picker"]'),
    "draft-only group picker did not render after the scoped fixture reread a draft plan",
  );
  const groupOpen = fullWindow.document.querySelector('[data-action="open-group-picker"]');
  groupOpen.focus();
  groupOpen.click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key]'), "V3 scoped group picker did not render the authorised directory page");
  assert.equal(fullWindow.document.querySelector('[data-group-picker-search]'), null, 'the frozen per-keystroke group picker never opens beneath the V3 session');
  const pickerSearch = fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-picker-search-input]');
  const readsBeforePickerSearch = calls.length;
  pickerSearch.value = "九号";
  pickerSearch.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  pickerSearch.dispatchEvent(new fullWindow.KeyboardEvent("keydown", { bubbles: true, key: "Enter" }));
  await waitFor(() => calls.slice(readsBeforePickerSearch).some((call) => call.method === "GET" && call.path.includes("/group-ops/groups?") && new URL(call.path, fullWindow.location.href).searchParams.get("q") === "九号"), "V3 group picker did not send the server query");
  const ownerScopedPickerRead = calls.slice(readsBeforePickerSearch).find((call) => call.method === "GET" && call.path.includes("/group-ops/groups?") && new URL(call.path, fullWindow.location.href).searchParams.get("q") === "九号");
  assert.equal(new URL(ownerScopedPickerRead.path, fullWindow.location.href).searchParams.get("owner_userid"), "9", "picker search must retain the current Owner scope");
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]')?.disabled === false, "Owner-scoped search did not settle before confirmation");
  const groupRow = fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key]');
  assert.equal(groupRow.textContent.includes("group-9"), true, "picker displays the opaque GroupOps chat reference");
  groupRow.click();
  assert.equal(fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key]')?.getAttribute("aria-pressed"), "true", "Owner-scoped query row remains selectable");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]').click();
  await waitFor(() => state.group_assets.length === 1, "selected directory group was not bound through the existing GroupOps Owner command");
  assert.equal(state.group_assets[0].asset_reference, "group-9");
  assert.equal(fullWindow.document.querySelector('[data-v3-selection-session="group"]'), null, "successful commit closes the temporary selection session");

  state.directory.push(
    { chat_reference: "group-10", owner_staff_id: 9, display_name: "十号运营群", member_count: 13, external_member_count: 9 },
    { chat_reference: "group-11", owner_staff_id: 9, display_name: "十一号运营群", member_count: 14, external_member_count: 10 },
  );
  dropCommittedGroupResponse = "group-10";
  rejectGroupOnce = "group-11";
  fullWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key$="group-11"]'), "multi-step group picker did not load scoped results");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key$="group-10"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-key$="group-11"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-remove$="group-9"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]')?.textContent.includes("已实际保存：添加 group-10"), "partial group save did not preserve the actual accepted step");
  const group10Commands = groupSelectionCommands.filter((command) => command.reference === "group-10");
  assert.equal(group10Commands.length, 1, "dropped accepted response must not replay the confirmed step");
  assert(state.group_assets.some((item) => item.asset_reference === "group-10") && state.group_assets.some((item) => item.asset_reference === "other-concurrent"), "Owner readback must retain accepted and concurrent bindings");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]') === null && state.group_assets.some((item) => item.asset_reference === "group-11") && !state.group_assets.some((item) => item.asset_reference === "group-9"), "explicit retry did not finish only the remaining group differences");
  const group11Commands = groupSelectionCommands.filter((command) => command.reference === "group-11");
  assert.equal(group11Commands.length, 2, "only the unconfirmed step is retried");
  assert.equal(group11Commands[0].idempotencyKey, group11Commands[1].idempotencyKey, "retry preserves the step idempotency key");
  assert.deepEqual(group11Commands[0].body, group11Commands[1].body, "retry preserves the frozen CAS command body");
  assert(state.group_assets.some((item) => item.asset_reference === "other-concurrent"), "selection retry never removes another actor's concurrent binding");
  const cachedGroups = await fullWindow.AdminApi.requestJson("/api/admin/automation-conversion/group-ops/plans/41/groups");
  const cachedGroup10 = cachedGroups.items.find((item) => item.chat_id === "group-10");
  assert.deepEqual(
    { name: cachedGroup10.group_name, owner: cachedGroup10.owner_userid, internal: cachedGroup10.internal_member_count_snapshot, external: cachedGroup10.external_member_count_snapshot },
    { name: "十号运营群", owner: "9", internal: 4, external: 9 },
    "confirmed groups retain raw directory name, owner and member snapshots",
  );
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-action="open-group-picker"]'), "group panel did not return after a tab switch");
  fullWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]')?.textContent.includes("十号运营群"), "reopened group picker lost the confirmed directory name");
  fullWindow.document.querySelector('[data-v3-selection-session="group"] [data-v3-group-cancel]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="group"]') === null, "cancel must close without inventing a rollback");
  await waitFor(() => state.group_assets.some((item) => item.asset_reference === "group-10"), "cancel readback must preserve the actual saved binding");
  await waitFor(() => fullWindow.document.querySelector('[data-action="remove-group"][data-chat-id="group-11"]'), "successful selection did not redraw a native removable group row");
  fullWindow.document.querySelector('[data-action="remove-group"][data-chat-id="group-11"]').click();
  await waitFor(() => !state.group_assets.some((item) => item.asset_reference === "group-11"), "native row action did not remove the freshly rendered binding");
  assert(state.group_assets.some((item) => item.asset_reference === "other-concurrent"), "fresh native remove action preserves concurrent binding");

  await waitFor(() => fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]'), "detail did not reload after group bind");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  fullWindow.document.querySelector('[data-action="open-node-modal"]').click();
  await waitFor(() => fullWindow.document.querySelector('[name="node_action_title"]'), "node editor did not open");
  const callsBeforeContent = calls.length;
  fullWindow.document.querySelector('[data-action="configure-node-content"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-content-composer]'), "V3 node content composer did not open");
  assert.equal(calls.length, callsBeforeContent, "opening the editor is local and never saves/sends/previews content");
  const nodeText = fullWindow.document.querySelector('[data-v3-composer-text]');
  nodeText.value = " 前后空白 ";
  nodeText.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  assert.equal(fullWindow.document.querySelector('.aicrm-content-presentation__text').textContent, " 前后空白 ", "preview keeps a Group Ops whitespace-invalid draft visible instead of silently trimming a different value");
  assert.match(fullWindow.document.querySelector('[data-v3-content-composer]').textContent, /首尾不能包含空白字符/, "Group Ops must show its validText whitespace rule before any save");
  assert.equal(fullWindow.document.querySelector('[data-v3-composer-confirm]').disabled, true, "leading or trailing whitespace cannot be silently trimmed into a saved Group Ops message");
  nodeText.value = "   ";
  nodeText.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  assert.match(fullWindow.document.querySelector('[data-v3-content-composer]').textContent, /首尾不能包含空白字符/, "whitespace-only Group Ops text does not masquerade as an empty valid draft");
  nodeText.value = "\ud800";
  nodeText.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  assert.match(fullWindow.document.querySelector('[data-v3-content-composer]').textContent, /无效字符/, "Group Ops rejects a non-UTF-8 text value before its owner command");
  nodeText.value = "😀".repeat(1000);
  nodeText.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  assert.equal(fullWindow.document.querySelector('[data-v3-composer-confirm]').disabled, false, "exactly 1000 Unicode runes remain valid for Group Ops");
  nodeText.value = "😀".repeat(1001);
  nodeText.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  assert.match(fullWindow.document.querySelector('[data-v3-content-composer]').textContent, /不能超过 1000 个字符/, "more than 1000 Unicode runes is rejected before the Group Ops caller receives a draft");
  nodeText.value = "真实话术 {{历史变量}}";
  nodeText.dispatchEvent(new fullWindow.Event("input", { bubbles: true }));
  fullWindow.document.querySelector('[data-v3-composer-add="image"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":23"]'), "scoped image selector did not load");
  fullWindow.document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":23"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-confirm]').click();
  await waitFor(() => !fullWindow.document.querySelector('[data-v3-selection-session="material"]') && fullWindow.document.body.textContent.includes("节点封面"), "image selection did not return to the local composer draft");
  fullWindow.document.querySelector('[data-v3-composer-add="attachment"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":24"]'), "scoped attachment selector did not load");
  fullWindow.document.querySelector('[data-v3-selection-session="material"] [data-v3-material-key$=":24"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="material"] [data-v3-picker-confirm]').click();
  await waitFor(() => !fullWindow.document.querySelector('[data-v3-selection-session="material"]') && fullWindow.document.body.textContent.includes("节点说明.pdf"), "attachment selection did not return to the local composer draft");
  // The caller owns the actual persisted ordering contract. This local move is
  // carried to the Host and becomes the same material_plan.references order.
  fullWindow.document.querySelector('[data-v3-composer-move="0:1"]').click();
  fullWindow.document.querySelector('[data-v3-composer-confirm]').click();
  await waitFor(() => !fullWindow.document.querySelector('[data-v3-content-composer]'), "local composer confirmation did not return to node draft");
  assert.equal(calls.length, callsBeforeContent + 2, "only the two authorised Media reads occur before node save");
  assert.match(fullWindow.document.querySelector('[name="node_content_package_json"]').value, /真实话术/, "composer confirmation updates only the node form draft");
  assert.match(fullWindow.document.querySelector('[name="node_content_material_order_json"]').value, /attachment/, "confirmed local draft retains the user-selected material sequence");
  fullWindow.document.querySelector('[name="node_day_index"]').value = "2";
  fullWindow.document.querySelector('[name="node_scheduled_time"]').value = "09:30";
  fullWindow.document.querySelector('[name="node_action_title"]').value = "节点结果";
  fullWindow.document.querySelector('[data-action="save-node"]').click();
  await waitFor(() => state.nodes.length === 1, "node with selected material was not saved through the Host command");
  assert.deepEqual(state.nodes[0].material_plan, { references: [{ kind: "attachment", id: 24 }, { kind: "image", id: 23 }] }, "caller-confirmed material order must reach the V3 material-plan DTO exactly");
  assert.equal(state.nodes[0].message_text, "真实话术 {{历史变量}}", "historical token text is preserved rather than interpreted as a customer variable");
  assert.equal(state.nodes[0].day_index, 2);
  assert.equal(state.nodes[0].scheduled_time, "09:30");
  assert.equal(state.nodes[0].action_title, "节点结果");
  // A saved node resolves its own Media records on demand. While that bounded
  // read is pending, a repeated click starts no second session; closing the
  // node invalidates the old session so its eventual response cannot reopen a
  // composer detached from the caller's hidden draft fields.
  delayNodeContentDetail = true;
  const contentDetailReadsBeforeCancel = calls.filter((item) => item.path === '/api/admin/image-library/23' && item.method === 'GET').length;
  fullWindow.document.querySelector('[data-action="edit-node"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-action="configure-node-content"]'), "saved node editor did not reopen for slow-detail cancellation");
  const openingContent = fullWindow.document.querySelector('[data-action="configure-node-content"]');
  openingContent.click();
  openingContent.click();
  await waitFor(() => releaseNodeContentDetails.length === 1 && openingContent.disabled && openingContent.textContent.includes('正在读取素材详情'), "node material detail load did not lock one visible opener");
  assert.equal(calls.filter((item) => item.path === '/api/admin/image-library/23' && item.method === 'GET').length, contentDetailReadsBeforeCancel + 1, "a repeated content-editor click must not start a second Media detail read");
  fullWindow.document.querySelector('[data-action="cancel-node"]').click();
  assert.equal(fullWindow.document.querySelector('[name="node_content_package_json"]'), null, "closing the node removes its local draft fields before an old read can write them");
  releaseNodeContentDetails.splice(0).forEach((release) => release());
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(fullWindow.document.querySelector('[data-v3-content-composer]'), null, "a cancelled node never opens an old content dialog after delayed Media details return");
  delayNodeContentDetail = false;
  fullWindow.document.querySelector('[data-action="edit-node"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-action="configure-node-content"]'), "node editor did not allow a new content session after cancellation");
  fullWindow.document.querySelector('[data-action="configure-node-content"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-content-composer]'), "a fresh node session did not open after cancelling the old read");
  fullWindow.document.querySelector('[data-v3-composer-cancel]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-action="view-node-content"]'), "saved node did not render a readonly content action");
  // Readonly uses the same bounded metadata resolver. Repeated activation is
  // single-flight; changing the detail panel detaches the original action, so
  // a late directory response must not open content for a stale node/plan.
  delayNodeContentDetail = true;
  const readonlyOpen = fullWindow.document.querySelector('[data-action="view-node-content"]');
  const readonlyDetailReads = calls.filter((item) => item.path === '/api/admin/image-library/23' && item.method === 'GET').length;
  readonlyOpen.click();
  readonlyOpen.click();
  await waitFor(() => releaseNodeContentDetails.length === 1 && readonlyOpen.disabled && readonlyOpen.textContent.includes('正在读取素材详情'), "readonly material detail load did not lock the one current row action");
  assert.equal(calls.filter((item) => item.path === '/api/admin/image-library/23' && item.method === 'GET').length, readonlyDetailReads + 1, "a repeated readonly action must not start a second Media detail read");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="basic"]').click();
  assert.equal(readonlyOpen.isConnected, false, "switching the plan detail detaches the stale readonly opener");
  releaseNodeContentDetails.splice(0).forEach((release) => release());
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(fullWindow.document.querySelector('[data-v3-content-readonly]'), null, "a late readonly metadata response cannot open a stale node dialog");
  delayNodeContentDetail = false;
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-action="view-node-content"]'), "nodes panel did not recover after cancelling a stale readonly load");
  fullWindow.document.querySelector('[data-action="view-node-content"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-content-readonly]'), "saved node content did not use the shared readonly presenter");
  const readonlyText = fullWindow.document.querySelector('[data-v3-content-readonly]').textContent;
  assert(readonlyText.indexOf("节点说明.pdf") < readonlyText.indexOf("节点封面"), "readonly presentation preserves the owner material sequence after reload");
  fullWindow.document.querySelector('[data-v3-content-readonly-close]').click();
  assert(calls.some((item) => item.path.endsWith("/enable") && item.method === "POST"), "standard enable action did not call the V3 command");
  assert(fullWindow.document.body.textContent.includes("wecom-replacement"), "selected owner must show the trusted WeCom user ID");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="basic"]').click();
  fullWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="7"]'), "draft V3 owner picker missing");
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$="7"]').click();
  fullWindow.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]').click();
  await waitFor(() => fullWindow.document.querySelector('[name="owner_userid"]')?.value === "7", "draft owner missing");
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  const writesBeforeRefresh = calls.filter(item => item.method !== "GET" && !item.path.endsWith("/sync")).length;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("已刷新 3 个群聊"), "snapshot total notice missing");
  assert(fullWindow.document.querySelector('.group-ops__group-name').textContent.includes("同步群名1"), "bound group name must refresh without a page reload");
  assert(fullWindow.document.body.textContent.includes("other-concurrent"), "readback must retain another actor's concurrent binding");
  assert.equal(fullWindow.document.body.textContent.includes("231"), false, "an unknown concurrent binding must not fabricate an aggregate external count");
  assert.equal(fullWindow.document.querySelector('[name="owner_userid"]').value, "7", "refresh must preserve unsaved owner");
  assert.deepEqual(state.members, [{ staff_id: 9 }], "refresh must not save the draft owner");
  assert.equal(calls.filter(item => item.method !== "GET" && !item.path.endsWith("/sync")).length, writesBeforeRefresh, "refresh must not save or enable the plan");
  failGroupReadback = true;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.body.textContent.includes("群目录读取失败，请重试；已绑定群仍可查看"), "directory failure must preserve the bound projection and state its reason");
  assert.match(fullWindow.document.querySelector('.group-ops__group-name').textContent, /同步群名[12]/, "directory failure preserves a readable bound snapshot");
  assert.equal(fullWindow.document.body.textContent.includes("暂无绑定群"), false);
  failGroupReadback = false;
  fullWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => fullWindow.document.querySelector('.group-ops__group-name')?.textContent.includes("同步群名3"), "retry must update the bound projection");
  // A bound-group command increments the server revision before its detail
  // readback settles. A stale control must never write through that command;
  // its CAS conflict is explicit, and the delayed read cannot erase the
  // authoritative in-memory projection after the newer action generation.
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="basic"]').click();
  const planNameBeforeStaleControl = fullWindow.document.querySelector('[name="plan_name"]').value;
  fullWindow.document.querySelector('[name="plan_name"]').value = "新保存不会被旧读覆盖";
  const deferredSave = fullWindow.document.querySelector('[data-action="save-plan"]');
  fullWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  delayNextPlanRead = true;
  fullWindow.document.querySelector('[data-action="remove-group"]').click();
  await waitFor(() => releaseDelayedPlanRead && fullWindow.document.body.textContent.includes("加载中"), "old detail load did not pause for the stale-response assertion");
  const staleInput = fullWindow.document.createElement('input');
  staleInput.name = 'plan_name'; staleInput.value = '新保存不会被旧读覆盖';
  fullWindow.document.getElementById('group-ops-app').append(staleInput);
  deferredSave.click();
  await waitFor(
    () => /保存使用版本 v17；当前版本为 v18。草稿已保留/.test(fullWindow.document.body.textContent),
    () => `stale basic save must surface the authoritative revision conflict; text=${fullWindow.document.body.textContent} recent=${JSON.stringify(calls.slice(-8))}`,
  );
  assert.equal(state.plan.name, planNameBeforeStaleControl, "a stale basic save must not claim a later plan write succeeded");
  releaseDelayedPlanRead();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(state.plan.name, planNameBeforeStaleControl, "a delayed older detail response must not overwrite a later action generation");
  assert.equal(fullWindow.document.body.textContent.includes('新保存不会被旧读覆盖'), false, "the stale draft must not appear as an authoritative plan value");
  if (fullJourneyErrors.length) throw new Error(`Group Ops standard DOM errors: ${JSON.stringify(fullJourneyErrors)}`);
  console.log("groupops-standard-dom: PASS");
} finally {
  fullJourney.window.close();
}

// An active Webhook plan can carry a valid ten-group execution definition.
// The detail page must make that lifecycle fact visible, never route either
// former Save action through PUT, and use the displayed revision for an
// explicit pause followed by a complete owner readback.
const activeLifecycleErrors = [];
const activeLifecycleConsole = new VirtualConsole();
activeLifecycleConsole.on("jsdomError", (error) => activeLifecycleErrors.push(String(error?.message || error)));
const activeLifecycleJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="12"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/12",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  virtualConsole: activeLifecycleConsole,
});
const activeLifecycleWindow = activeLifecycleJourney.window;
activeLifecycleWindow.Headers = Headers;
activeLifecycleWindow.Response = Response;
Object.defineProperty(activeLifecycleWindow, "crypto", { configurable: true, value: crypto });
activeLifecycleWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
activeLifecycleWindow.confirm = () => true;
const activeLifecycleCalls = [];
const activeLifecyclePlan = { plan_id: 12, name: "Webhook 十群计划", revision: 12, status: "active", plan_type: "webhook" };
const activeLifecycleAssets = Array.from({ length: 10 }, (_, index) => ({ asset_reference: `group-${index + 1}` }));
const activeLifecycleDetail = () => ({ plan: clone(activeLifecyclePlan), members: [{ staff_id: 7 }], group_assets: clone(activeLifecycleAssets), nodes: [] });
activeLifecycleWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), activeLifecycleWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : null;
  activeLifecycleCalls.push({ path: url.pathname + url.search, method, body });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "owner", display_name: "运营一号" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/12" && method === "GET") return response(activeLifecycleDetail());
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/12/groups" && method === "GET") return response({ items: clone(activeLifecycleAssets), summary: { bound_group_count: 10 } });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/12/webhook-descriptor" && method === "GET") return response({ configured: true, reference: "webhook-twelve", path: "/api/automation/group-ops/webhooks/webhook-twelve", signature_algorithm: "HMAC-SHA256" });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/12/disable" && method === "POST") {
    assert.equal(body.expected_revision, 12, "detail pause must use the revision the user saw");
    activeLifecyclePlan.status = "paused";
    activeLifecyclePlan.revision = 13;
    return response({ plan: clone(activeLifecyclePlan) });
  }
  throw new Error(`unexpected active lifecycle request ${method} ${url.pathname}`);
};
try {
  activeLifecycleWindow.eval(pickerSource);
  activeLifecycleWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => activeLifecycleWindow.document.querySelector('[data-action="pause-detail-plan"]'), "active detail did not render its explicit pause action");
  assert.match(activeLifecycleWindow.document.body.textContent, /已启用 · 当前版本 v12/, "active lifecycle and rendered revision must be visible");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="save-plan"]'), null, "active plan must not render a misleading basic save");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="save-active-detail-panel"]'), null, "active plan must not render the second misleading save");
  assert.equal(activeLifecycleWindow.document.querySelector('[name="plan_name"]')?.disabled, true, "active plan name must be read-only");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="open-group-picker"]'), null, "active bound groups must not expose a write action");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="save-webhook"]'), null, "active Webhook configuration must not expose a write action");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="pause-detail-plan"]')?.__dcBound, true, "explicit lifecycle action must declare shared-feedback ownership");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="pause-detail-plan"]')?.dataset.capabilityState, "real", "real lifecycle action must not be marked backend-blocked");
  assert.equal(activeLifecycleCalls.some((call) => call.method === "PUT" || /\/(groups|nodes|webhook-descriptor)$/.test(call.path) && call.method !== "GET"), false, "active detail hydration must issue zero configuration writes");
  activeLifecycleWindow.document.querySelector('[data-action="pause-detail-plan"]').click();
  await waitFor(() => activeLifecyclePlan.status === "paused" && activeLifecycleWindow.document.querySelector('[data-action="save-plan"]'), "confirmed pause did not complete the authoritative detail readback");
  assert.equal(activeLifecycleCalls.filter((call) => call.path.endsWith("/disable") && call.method === "POST").length, 1, "one explicit click sends one pause command");
  assert.equal(activeLifecycleCalls.filter((call) => call.method === "PUT").length, 0, "pause must not be preceded by a basic configuration PUT");
  assert.equal(activeLifecycleWindow.document.querySelector('[name="plan_name"]')?.disabled, false, "paused plan may edit its basic configuration after readback");
  assert.equal(activeLifecycleWindow.document.querySelector('[data-action="open-group-picker"]'), null, "paused plan must not expose draft-only group writes");
  assert.match(activeLifecycleWindow.document.body.textContent, /绑定群和标准编排只可在草稿计划中调整/, "paused plan must explain its remaining draft-only boundary");
  if (activeLifecycleErrors.length) throw new Error(`active lifecycle DOM errors: ${JSON.stringify(activeLifecycleErrors)}`);
  console.log("groupops-active-plan-lifecycle-dom: PASS");
} finally {
  activeLifecycleJourney.window.close();
}

// A pending detail save freezes every plan mutation surface, including a
// previously rendered control whose listener is still reachable after the
// loading repaint. Directory sync is also held during that interval so an
// owner draft cannot race the same plan readback; outside the lock it remains
// the existing Provider-read/local-directory maintenance path.
async function assertPendingPlanMutationLock(planID, planType) {
  const errors = [];
  const console = new VirtualConsole();
  console.on("jsdomError", (error) => errors.push(String(error?.message || error)));
  const journey = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="${planID}"></main></body></html>`, {
    url: `https://groupops.test/admin/automation-conversion/group-ops/plans/${planID}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
    virtualConsole: console,
  });
  const view = journey.window;
  view.Headers = Headers;
  view.Response = Response;
  Object.defineProperty(view, "crypto", { configurable: true, value: crypto });
  view.document.cookie = "aicrm_admin_csrf=test-csrf";
  const calls = [];
  const plan = { plan_id: planID, name: `${planType} 锁定计划`, revision: 1, status: "draft", plan_type: planType };
  let releaseWrite;
  view.fetch = async (input, init = {}) => {
    const url = new URL(String(input), view.location.href);
    const method = String(init.method || "GET").toUpperCase();
    const body = init.body ? JSON.parse(String(init.body)) : null;
    calls.push({ path: url.pathname, method, body });
    if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "owner", display_name: "运营一号" }] });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}` && method === "GET") return response({ plan: clone(plan), members: [{ staff_id: 7 }], group_assets: [{ asset_reference: "group-a" }], nodes: planType === "standard" ? [{ node_id: 1, day_index: 1, scheduled_time: "09:00", action_title: "既有动作", status: "active" }] : [] });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}/groups` && method === "GET") return response({ items: [{ asset_reference: "group-a" }], summary: { bound_group_count: 1 } });
    if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [{ chat_reference: "group-b", owner_userid: "7", group_name: "可选群" }], total: 1, limit: 200, offset: 0, has_more: false });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}/webhook-descriptor` && method === "GET") return response({ configured: false });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}` && method === "PUT") {
      assert.equal(body.expected_revision, 1, "pending save starts from the displayed revision");
      return new Promise((resolve) => {
        releaseWrite = () => {
          plan.revision = 2;
          plan.name = body.name;
          resolve(response({ plan: clone(plan) }));
        };
      });
    }
    if (method !== "GET") return response({ plan: clone(plan) });
    throw new Error(`unexpected pending-lock request ${method} ${url.pathname}`);
  };
  try {
    view.eval(pickerSource);
    view.eval(bundle.outputFiles[0].text);
    await waitFor(() => view.document.querySelector('[data-action="save-plan"]'), `${planType} pending-lock fixture did not render`);
    const staleSave = view.document.querySelector('[data-action="save-plan"]');
    const staleRefresh = view.document.querySelector('[data-action="refresh-owner-groups"]');
    const staleOpenGroup = view.document.querySelector('[data-action="open-group-picker"]');
    const staleRemoveGroup = view.document.querySelector('[data-action="remove-group"]');
    let staleOpenNode = null;
    let staleSaveNode = null;
    let staleWebhook = null;
    if (planType === "standard") {
      staleOpenNode = view.document.querySelector('[data-action="open-node-modal"]');
      staleOpenNode.click();
      await waitFor(() => view.document.querySelector('[data-action="save-node"]'), "standard node editor did not render before the pending save");
      staleSaveNode = view.document.querySelector('[data-action="save-node"]');
    } else {
      staleWebhook = view.document.querySelector('[data-action="save-webhook"]');
    }
    staleSave.click();
    await waitFor(() => releaseWrite, `${planType} pending save did not reach the delayed PUT`);
    staleRefresh.click();
    staleOpenGroup.click();
    staleRemoveGroup.click();
    staleOpenNode?.click();
    staleSaveNode?.click();
    staleWebhook?.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
    const extraPlanWrites = calls.filter((call) => call.method !== "GET" && call.path !== `/api/admin/automation-conversion/group-ops/plans/${planID}`);
    assert.deepEqual(extraPlanWrites, [], `${planType} pending save must block stale group/node/Webhook mutations and directory sync`);
    assert.match(view.document.body.textContent, /正在保存计划|保存中/, `${planType} pending save must remain visibly locked`);
    releaseWrite();
    await waitFor(() => view.document.body.textContent.includes("已保存"), `${planType} pending save did not complete its authority readback`);
    if (errors.length) throw new Error(`${planType} pending mutation DOM errors: ${JSON.stringify(errors)}`);
  } finally {
    journey.window.close();
  }
}

await assertPendingPlanMutationLock(73, "standard");
await assertPendingPlanMutationLock(74, "webhook");
console.log("groupops-pending-plan-mutation-lock-dom: PASS");

// A stale activation request must retain its explicit version conflict. A
// newer server revision from another editor is not evidence that this browser
// saved its form, so the UI must not claim a saved-before-enable result.
const staleActivationJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="71"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/71",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const staleActivationWindow = staleActivationJourney.window;
staleActivationWindow.Headers = Headers;
staleActivationWindow.Response = Response;
Object.defineProperty(staleActivationWindow, "crypto", { configurable: true, value: crypto });
staleActivationWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
staleActivationWindow.confirm = () => true;
let staleActivationRead = 0;
const staleActivationWrites = [];
const staleActivationCalls = [];
const staleActivationPlan = { plan_id: 71, name: "被其他人编辑", revision: 10, status: "paused", plan_type: "webhook" };
staleActivationWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), staleActivationWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : null;
  staleActivationCalls.push({ path: url.pathname, method, body });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "owner", display_name: "运营一号" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/71" && method === "GET") {
    staleActivationRead += 1;
    if (staleActivationRead === 2) {
      staleActivationPlan.revision = 11;
      staleActivationPlan.status = "active";
    }
    return response({ plan: clone(staleActivationPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/71/groups" && method === "GET") return response({ items: [], summary: { bound_group_count: 0 } });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/71/webhook-descriptor" && method === "GET") return response({ configured: true, reference: "webhook-stale", path: "/api/automation/group-ops/webhooks/webhook-stale" });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/71" && method === "PUT") {
    staleActivationWrites.push(body);
    return response({ error: { code: "operations_conflict" } }, 409);
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/71/disable" && method === "POST") {
    assert.equal(body.expected_revision, 11, "pause after a conflict must use the authority-read revision");
    staleActivationPlan.revision = 12;
    staleActivationPlan.status = "paused";
    return response({ plan: clone(staleActivationPlan) });
  }
  throw new Error(`unexpected stale activation request ${method} ${url.pathname}`);
};
try {
  staleActivationWindow.eval(pickerSource);
  staleActivationWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => staleActivationWindow.document.querySelector('[data-action="save-plan"]'), "paused activation fixture did not render basic save");
  assert.equal(staleActivationWindow.document.querySelector('option[value="draft"]'), null, "paused plans must not expose a nonexistent paused-to-draft transition");
  staleActivationWindow.document.querySelector('[name="plan_name"]').value = "冲突后保留的草稿";
  staleActivationWindow.document.querySelector('[name="status"]').value = "active";
  staleActivationWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => staleActivationWindow.document.body.textContent.includes("当前计划已启用（版本 v11）"), "stale activation must show the current active revision without claiming persistence");
  assert.equal(staleActivationWrites.length, 1, "stale activation sends one PUT");
  assert.equal(staleActivationWrites[0].expected_revision, 10, "Host must not replace displayed revision with its later read");
  assert.equal(staleActivationWindow.document.body.textContent.includes("基础配置已保存，但暂不能启用"), false, "another editor's newer revision must not masquerade as this save");
  staleActivationWindow.document.querySelector('[data-action="pause-detail-plan"]').click();
  await waitFor(
    () => staleActivationPlan.status === "paused" && staleActivationWindow.document.body.textContent.includes("已停用，当前版本 v12"),
    () => `pause after a conflict did not complete its authority readback: status=${staleActivationPlan.status} revision=${staleActivationPlan.revision} calls=${JSON.stringify(staleActivationCalls)} text=${staleActivationWindow.document.body.textContent}`,
  );
  assert.equal(staleActivationWindow.document.querySelector('[name="plan_name"]')?.value, "冲突后保留的草稿", "a confirmed pause must preserve the stale-save draft for a manual follow-up");
  assert.match(staleActivationWindow.document.body.textContent, /已停用，当前版本 v12。草稿已保留/, "pause readback must explain the retained draft instead of silently discarding it");
  console.log("groupops-stale-activation-cas-dom: PASS");
} finally {
  staleActivationJourney.window.close();
}

// A Host-marked partial success is the only case where the UI may say the
// basic fields saved before enable failed. The existing read-only preview then
// turns concrete owner validation codes into repairable labels without any
// second write or lifecycle retry.
const activationValidationJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="72"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/72",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const activationValidationWindow = activationValidationJourney.window;
activationValidationWindow.Headers = Headers;
activationValidationWindow.Response = Response;
Object.defineProperty(activationValidationWindow, "crypto", { configurable: true, value: crypto });
activationValidationWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
const activationValidationCalls = [];
const activationValidationPlan = { plan_id: 72, name: "待补齐配置", revision: 20, status: "paused", plan_type: "webhook" };
activationValidationWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), activationValidationWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : null;
  activationValidationCalls.push({ path: url.pathname, method, body });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "owner", display_name: "运营一号" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/72" && method === "GET") return response({ plan: clone(activationValidationPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/72/groups" && method === "GET") return response({ items: [], summary: { bound_group_count: 0 } });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/72/webhook-descriptor" && method === "GET") return response({ configured: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/72" && method === "PUT") {
    assert.equal(body.expected_revision, 20);
    activationValidationPlan.revision = 21;
    activationValidationPlan.name = body.name;
    return response({ plan: clone(activationValidationPlan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/72/enable" && method === "POST") return response({ error: { code: "operations_conflict" } }, 409);
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/72/content/preview" && method === "POST") return response({ valid: false, issue_codes: ["group_asset_required", "webhook_descriptor_required"] });
  throw new Error(`unexpected activation validation request ${method} ${url.pathname}`);
};
try {
  activationValidationWindow.eval(pickerSource);
  activationValidationWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => activationValidationWindow.document.querySelector('[data-action="save-plan"]'), "activation validation fixture did not render");
  activationValidationWindow.document.querySelector('[name="status"]').value = "active";
  activationValidationWindow.document.querySelector('[data-action="save-plan"]').click();
  await waitFor(() => activationValidationWindow.document.body.textContent.includes("至少绑定一个群、生成 Webhook 地址"), "activation failure must expose the existing concrete preview checks");
  assert.match(activationValidationWindow.document.body.textContent, /基础配置已保存，但暂不能启用/, "partial result must name its saved-before-enable boundary");
  assert.equal(activationValidationCalls.filter((call) => call.method === "PUT").length, 1, "partial activation performs one basic save");
  assert.equal(activationValidationCalls.filter((call) => call.path.endsWith("/enable") && call.method === "POST").length, 1, "partial activation performs one enable attempt");
  assert.equal(activationValidationCalls.filter((call) => call.path.endsWith("/content/preview") && call.method === "POST").length, 1, "repair labels use one existing read-only preview");
  console.log("groupops-activation-validation-dom: PASS");
} finally {
  activationValidationJourney.window.close();
}

// Once the Host has received a valid PUT receipt, an enable 5xx or transport
// error is not evidence that the basic fields were rejected. Both cases must
// re-read authority, never preview guessed validation or repeat either write.
async function assertPartialEnableFailure(kind, failInitialReadback) {
  const planID = kind === "network" ? 75 : 74;
  const journey = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="${planID}"></main></body></html>`, {
    url: `https://groupops.test/admin/automation-conversion/group-ops/plans/${planID}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  const view = journey.window;
  view.Headers = Headers;
  view.Response = Response;
  Object.defineProperty(view, "crypto", { configurable: true, value: crypto });
  view.document.cookie = "aicrm_admin_csrf=test-csrf";
  const calls = [];
  let planReads = 0;
  let releaseRecoveryReadback;
  const holdRecoveryReadback = kind === "network";
  const plan = { plan_id: planID, name: `${kind} 启用结果`, revision: 30, status: "paused", plan_type: "webhook" };
  view.fetch = async (input, init = {}) => {
    const url = new URL(String(input), view.location.href);
    const method = String(init.method || "GET").toUpperCase();
    const body = init.body ? JSON.parse(String(init.body)) : null;
    calls.push({ path: url.pathname, method, body });
    if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "owner", display_name: "运营一号" }] });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}` && method === "GET") {
      planReads += 1;
      // The Host first reads current owner metadata before its PUT. The third
      // detail request is the Standard controller's authoritative recovery
      // read after the Host has marked the accepted PUT/failed enable pair.
      if (failInitialReadback && planReads === 3) throw new Error("authority read unavailable");
      if (holdRecoveryReadback && planReads === 3) {
        return new Promise((resolve) => { releaseRecoveryReadback = resolve; });
      }
      return response({ plan: clone(plan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
    }
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}/groups` && method === "GET") return response({ items: [], summary: { bound_group_count: 0 } });
    if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}/webhook-descriptor` && method === "GET") return response({ configured: true, reference: `webhook-${planID}`, path: `/api/automation/group-ops/webhooks/webhook-${planID}` });
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}` && method === "PUT") {
      assert.equal(body.expected_revision, 30, `${kind} partial result must retain the displayed revision for its PUT`);
      plan.revision = 31;
      plan.name = body.name;
      return response({ plan: clone(plan) });
    }
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}/enable` && method === "POST") {
      assert.equal(body.expected_revision, 31, `${kind} enable follows the verified PUT receipt`);
      if (kind === "network") throw new Error("enable connection reset");
      return response({ error: { code: "service_unavailable" } }, 500);
    }
    if (url.pathname === `/api/admin/automation-conversion/group-ops/plans/${planID}/content/preview` && method === "POST") return response({ valid: false, issue_codes: ["group_asset_required"] });
    throw new Error(`unexpected ${kind} partial-result request ${method} ${url.pathname}`);
  };
  try {
    view.eval(pickerSource);
    view.eval(bundle.outputFiles[0].text);
    await waitFor(() => view.document.querySelector('[data-action="save-plan"]'), `${kind} partial-result fixture did not render`);
    view.document.querySelector('[name="status"]').value = "active";
    view.document.querySelector('[data-action="save-plan"]').click();
    if (holdRecoveryReadback) {
      await waitFor(
        () => typeof releaseRecoveryReadback === "function" && view.document.querySelector('[data-action="reload-plan-detail"]'),
        "partial-result recovery did not install its GET-only lock before the authority read",
      );
      const recoverySave = view.document.querySelector('[data-action="save-plan"]');
      const recoverySync = view.document.querySelector('[data-action="refresh-owner-groups"]');
      assert.equal(recoverySave?.disabled, true, "a recovery GET must disable the basic save control");
      assert.equal(recoverySync?.disabled, true, "a recovery GET must disable directory sync while the owner snapshot is stale");
      recoverySave?.dispatchEvent(new view.MouseEvent("click", { bubbles: true }));
      recoverySync?.dispatchEvent(new view.MouseEvent("click", { bubbles: true }));
      await new Promise((resolve) => setTimeout(resolve, 0));
      assert.equal(calls.filter((call) => call.method === "PUT").length, 1, "a delayed recovery read must block a second basic mutation");
      assert.equal(calls.filter((call) => call.path.endsWith("/operation-members/sync") && call.method === "POST").length, 0, "a delayed recovery read must block directory sync without changing the plan");
      releaseRecoveryReadback(response({ plan: clone(plan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] }));
    }
    if (failInitialReadback) {
      await waitFor(() => view.document.querySelector('[data-action="reload-plan-detail"]'), `${kind} partial result must lock after its authority readback fails`);
      assert.match(view.document.body.textContent, /基础配置已保存，但启用结果尚未确认/, `${kind} readback failure must not call the PUT rejected`);
      view.document.querySelector('[data-action="reload-plan-detail"]').click();
    }
    await waitFor(
      () => /基础配置已保存；启用结果(?:尚)?未确认/.test(view.document.body.textContent),
      () => `${kind} partial result did not explain its unresolved enable state: calls=${JSON.stringify(calls)} text=${view.document.body.textContent}`,
    );
    assert.equal(calls.filter((call) => call.method === "PUT").length, 1, `${kind} partial result never repeats its accepted PUT`);
    assert.equal(calls.filter((call) => call.path.endsWith("/enable") && call.method === "POST").length, 1, `${kind} partial result never repeats enable automatically`);
    assert.equal(calls.filter((call) => call.path.endsWith("/content/preview") && call.method === "POST").length, 0, `${kind} is not a proven configuration conflict and must not guess preview labels`);
    assert.equal(view.document.body.textContent.includes("基础配置未保存"), false, `${kind} partial result must not claim the accepted PUT was rejected`);
  } finally {
    journey.window.close();
  }
}

await assertPartialEnableFailure("500", true);
await assertPartialEnableFailure("network", false);
console.log("groupops-partial-enable-recovery-dom: PASS");

// The detail renderer owns the dependent group directory. A second owner
// choice (or an authoritative reread before navigation) must win over an old
// response; the Staff picker only protects its own member loader.
const ownerGroupsRaceJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="93"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/93",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const ownerGroupsRaceWindow = ownerGroupsRaceJourney.window;
ownerGroupsRaceWindow.Headers = Headers;
ownerGroupsRaceWindow.Response = Response;
Object.defineProperty(ownerGroupsRaceWindow, "crypto", { configurable: true, value: crypto });
const ownerPickerCalls = [];
ownerGroupsRaceWindow.AICRMStaffPicker = { open: (options) => ownerPickerCalls.push(options) };
const staleOwnerGroupReads = [];
const ownerGroupQueries = [];
let releaseStaleOwnerRefresh;
const ownerRaceMembers = [
  { staff_id: 6, user_id: "owner-six", display_name: "负责人六" },
  { staff_id: 7, user_id: "owner-seven", display_name: "负责人七" },
  { staff_id: 9, user_id: "owner-nine", display_name: "负责人九" },
];
const ownerRacePlan = () => ({
  id: 93, plan_name: "负责人竞态计划", revision: 1, status: "draft", plan_type: "standard", owner_userid: "6", owner_name: "负责人六",
});
ownerGroupsRaceWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), ownerGroupsRaceWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: clone(ownerRaceMembers) });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/93" && method === "GET") return response(ownerRacePlan());
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/93/groups" && method === "GET") return response({ items: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/93/nodes" && method === "GET") return response({ items: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups/sync" && method === "POST") {
    return new Promise((resolve) => { releaseStaleOwnerRefresh = () => resolve(response({ new_count: 1, updated_count: 0 })); });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
    const owner = url.searchParams.get("owner_userid");
    ownerGroupQueries.push(owner || "");
    if (owner === "7") return new Promise((resolve) => staleOwnerGroupReads.push(() => resolve(response({ items: [{ chat_id: `owner-seven-${staleOwnerGroupReads.length}`, group_name: "迟到的负责人七群", owner_userid: "7" }] }))));
    return response({ items: [{ chat_id: `owner-${owner || "none"}`, group_name: owner === "9" ? "负责人九群" : "负责人六群", owner_userid: owner || "6" }] });
  }
  throw new Error(`unexpected owner-race request ${method} ${url.pathname}${url.search}`);
};
try {
  ownerGroupsRaceWindow.AdminApi = {
    escapeHtml: (value) => String(value ?? ""),
    errorMessage: (error, fallback) => error?.message || fallback,
    responseErrorMessage: (_response, _body, fallback) => fallback,
    requestJson: async (url, options) => {
      const result = await ownerGroupsRaceWindow.fetch(url, options);
      const body = await result.json();
      if (!result.ok) throw new Error(`HTTP ${result.status}`);
      return body;
    },
  };
  ownerGroupsRaceWindow.eval(await readFile(new URL("../web/v3/groupOpsStandard.js", import.meta.url), "utf8"));
  await waitFor(() => ownerGroupsRaceWindow.document.querySelector('[data-action="pick-plan-owner"]'), "owner race detail did not render");
  const pick = (record) => {
    ownerGroupsRaceWindow.document.querySelector('[data-action="pick-plan-owner"]').click();
    const options = ownerPickerCalls.at(-1);
    assert(options, "plan owner action must call the V3 staff picker");
    options.onCommit({ selected: [record] });
  };

  // Refresh starts for the original local owner, then a new owner is selected
  // before the Owner refresh result is known. The old command remains issued,
  // but neither its success nor its finally may repaint or unlock over B.
  ownerGroupsRaceWindow.document.querySelector('[data-action="refresh-owner-groups"]').click();
  await waitFor(() => releaseStaleOwnerRefresh, "slow owner-six refresh command did not begin");
  pick(ownerRaceMembers[2]);
  await waitFor(() => ownerGroupQueries.at(-1) === "9", "new owner-nine selection did not read its own group directory");
  releaseStaleOwnerRefresh();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(ownerGroupsRaceWindow.document.querySelector('[name="owner_userid"]')?.value, "9", "late owner-six refresh must not replace the newer local owner field");
  assert.equal(ownerGroupsRaceWindow.document.body.textContent.includes("已刷新："), false, "late owner-six refresh must not announce completion for owner-nine");

  pick(ownerRaceMembers[1]);
  await waitFor(() => staleOwnerGroupReads.length === 1, "slow owner-seven group read did not begin");
  pick(ownerRaceMembers[2]);
  await waitFor(() => ownerGroupQueries.at(-1) === "9", "new owner-nine group projection did not request its current owner directory");
  ownerGroupsRaceWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  ownerGroupsRaceWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(
    () => ownerGroupsRaceWindow.document.body.textContent.includes("负责人九群"),
    () => `new owner-nine group projection did not render in the dependent group picker; queries=${JSON.stringify(ownerGroupQueries)} body=${ownerGroupsRaceWindow.document.body.textContent}`,
  );
  staleOwnerGroupReads.shift()();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(ownerGroupsRaceWindow.document.querySelector('[name="owner_userid"]')?.value, "9", "late owner-seven read must not replace the newer local owner field");
  assert.equal(ownerGroupsRaceWindow.document.body.textContent.includes("迟到的负责人七群"), false, "late owner-seven group rows must not repaint owner-nine detail");
  ownerGroupsRaceWindow.document.querySelector('[data-action="close-group-picker"]').click();

  pick(ownerRaceMembers[1]);
  await waitFor(() => staleOwnerGroupReads.length === 1, "second slow owner-seven group read did not begin");
  ownerGroupsRaceWindow.dispatchEvent(new ownerGroupsRaceWindow.CustomEvent("aicrm:groupops-detail-refresh", { detail: { planId: 93 } }));
  await waitFor(() => ownerGroupQueries.at(-1) === "6", "authoritative detail reread did not request its own owner directory before stale owner response");
  ownerGroupsRaceWindow.document.querySelector('[data-action="open-group-picker"]').click();
  await waitFor(() => ownerGroupsRaceWindow.document.body.textContent.includes("负责人六群"), "authoritative detail reread did not render before stale owner response");
  staleOwnerGroupReads.shift()();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(ownerGroupsRaceWindow.document.body.textContent.includes("迟到的负责人七群"), false, "an owner response that outlives the detail generation must not repaint the reread plan");
  console.log("groupops-owner-dependent-directory-race: PASS");
} finally {
  ownerGroupsRaceJourney.window.close();
}

// GroupOps treats every owner_userid form field as a local staff ID. This
// collision fixture proves a numeric external UserID cannot replace another
// staff record in create, plan-owner, or group-filter UI/reloads/requests.
const collisionMembers = [
  { staff_id: 1, sender_userid: "2", display_name: "外部 UserID 为 2 的一号员工" },
  { staff_id: 2, sender_userid: "wecom-staff-two", display_name: "本地二号员工" },
];
const collisionResponse = (body) => response(body);
const collisionWindow = (mode, planID = "") => {
  const journey = new JSDOM(groupOpsListDocument(mode, planID), {
    url: `https://groupops.test/admin/automation-conversion/group-ops${mode === "groups" ? "/groups" : planID ? `/plans/${planID}` : ""}`,
    runScripts: "outside-only", pretendToBeVisual: true,
  });
  const view = journey.window; view.Headers = Headers; view.Response = Response;
  Object.defineProperty(view, "crypto", { configurable: true, value: crypto });
  view.document.cookie = "aicrm_admin_csrf=test-csrf";
  const requests = [];
  view.fetch = async (input, init = {}) => {
    const url = new URL(String(input), view.location.href); const method = String(init.method || "GET").toUpperCase();
    requests.push({ path: url.pathname, query: url.search, method });
    if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return collisionResponse({ items: collisionMembers });
    if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return collisionResponse(planPage([]));
    if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return collisionResponse({ items: [] });
    if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52" && method === "GET") return collisionResponse({ plan: { plan_id: 52, name: "碰撞计划", revision: 1, status: "draft", plan_type: "standard", owner_userid: "2", owner_name: "本地二号员工" }, members: [{ staff_id: 2 }], group_assets: [], nodes: [] });
    if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52/groups" && method === "GET") return collisionResponse({ items: [] });
    throw new Error(`unexpected collision request ${method} ${url.pathname}${url.search}`);
  };
  view.eval(pickerSource); view.eval(bundle.outputFiles[0].text);
  return { journey, view, requests };
};
const chooseCollisionStaffTwo = async (view, message) => {
  await waitFor(() => view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":2"]'), message);
  const one = view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":1"]');
  const two = view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":2"]');
  assert.equal(one.getAttribute("aria-pressed"), "false", "numeric external UserID must not preselect local staff #1");
  two.click(); view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]').click();
};
const collisionCreate = collisionWindow("list");
try {
  await waitFor(() => groupOpsCreateAction(collisionCreate.view), "collision create page did not render");
  groupOpsCreateAction(collisionCreate.view).click();
  collisionCreate.view.document.querySelector('[data-action="pick-create-owner"]').click();
  await chooseCollisionStaffTwo(collisionCreate.view, "collision create picker did not render local staff #2");
  await waitFor(() => collisionCreate.view.document.querySelector('[name="create_owner_userid"]')?.value === "2", "create selection did not retain local staff #2");
  assert.match(collisionCreate.view.document.querySelector('[data-member-current="create_owner_userid"]')?.textContent || "", /本地二号员工/, "create selection must render the local staff #2 label");
  // Re-render the open draft without cancelling it. Cancelling a plan creation
  // intentionally discards the whole draft; a normal re-render must retain
  // the selected local staff ID and never substitute external UserID "2".
  groupOpsCreateAction(collisionCreate.view).click();
  assert.equal(collisionCreate.view.document.querySelector('[name="create_owner_userid"]')?.value, "2", "create rerender must retain the local staff ID rather than external UserID");
  assert.match(collisionCreate.view.document.querySelector('[data-member-current="create_owner_userid"]')?.textContent || "", /本地二号员工/);
} finally { collisionCreate.journey.window.close(); }
const collisionPlan = collisionWindow("detail", "52");
try {
  await waitFor(() => collisionPlan.view.document.querySelector('[data-action="pick-plan-owner"]'), "collision plan page did not render");
  assert.match(collisionPlan.view.document.querySelector('[data-member-current="owner_userid"]')?.textContent || "", /本地二号员工/, "plan owner rendering must use local staff #2");
  collisionPlan.view.document.querySelector('[data-action="pick-plan-owner"]').click();
  await waitFor(() => collisionPlan.view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":2"]')?.getAttribute("aria-pressed") === "true", "plan picker must preselect local staff #2");
  assert.equal(collisionPlan.view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":1"]')?.getAttribute("aria-pressed"), "false", "plan picker must not match external UserID 2 to staff #1");
  collisionPlan.view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-cancel]').click();
} finally { collisionPlan.journey.window.close(); }
const collisionFilter = collisionWindow("groups");
try {
  await waitFor(() => collisionFilter.view.document.querySelector('[data-action="pick-group-filter-owner"]'), "collision groups page did not render");
  collisionFilter.view.document.querySelector('[data-action="pick-group-filter-owner"]').click();
  await chooseCollisionStaffTwo(collisionFilter.view, "collision group filter picker did not render local staff #2");
  await waitFor(() => collisionFilter.requests.some((request) => request.path.endsWith("/groups") && request.query.includes("owner_userid=2")), "group filter request must carry local staff #2");
  assert.equal(collisionFilter.view.document.querySelector('[name="owner_userid"]')?.value, "2", "group filter rerender must retain the local staff ID");
  assert.match(collisionFilter.view.document.querySelector('[data-member-current="owner_userid"]')?.textContent || "", /本地二号员工/);
  collisionFilter.view.document.querySelector('[data-action="pick-group-filter-owner"]').click();
  await waitFor(() => collisionFilter.view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":2"]')?.getAttribute("aria-pressed") === "true", "optional group filter did not restore local staff #2");
  collisionFilter.view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":2"]').click();
  collisionFilter.view.document.querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]').click();
  await waitFor(() => collisionFilter.requests.filter((request) => request.path.endsWith("/groups")).at(-1)?.query === "", "empty optional group filter must reload without an owner query");
  assert.equal(collisionFilter.view.document.querySelector('[name="owner_userid"]')?.value, "", "empty optional group filter must clear the hidden local owner ID");
  collisionFilter.view.document.querySelector('[data-action="clear-group-filter-owner"]').click();
  await waitFor(() => collisionFilter.requests.filter((request) => request.path.endsWith("/groups")).at(-1)?.query === "", "clearing group filter must remove the local owner query");
  assert.equal(collisionFilter.view.document.querySelector('[name="owner_userid"]')?.value, "", "clearing group filter must not retain an external UserID fallback");
} finally { collisionFilter.journey.window.close(); }
console.log("groupops-owner-local-id-collision: PASS");

// A pre-existing detail read can legitimately become stale without any
// intervening command. The newer plan save must win once its authoritative
// readback completes; releasing the old response must not repaint its snapshot.
const saveRaceJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="61"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/61",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const saveRaceWindow = saveRaceJourney.window;
saveRaceWindow.Headers = Headers;
saveRaceWindow.Response = Response;
Object.defineProperty(saveRaceWindow, "crypto", { configurable: true, value: crypto });
saveRaceWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
let saveRacePlan = { plan_id: 61, name: "旧详情名称", revision: 5, status: "draft", plan_type: "standard" };
let delaySaveRaceRead = false;
let releaseSaveRaceRead = null;
const saveRaceDetail = () => ({
  plan: clone(saveRacePlan),
  members: [{ staff_id: 7 }],
  group_assets: [],
  nodes: [],
});
saveRaceWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), saveRaceWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  const body = init.body ? JSON.parse(String(init.body)) : null;
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") {
    return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/61" && method === "GET") {
    if (delaySaveRaceRead) {
      delaySaveRaceRead = false;
      const captured = response(saveRaceDetail());
      return new Promise((resolve) => { releaseSaveRaceRead = () => resolve(captured); });
    }
    return response(saveRaceDetail());
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/61" && method === "PUT") {
    assert.equal(body.expected_revision, 5, "a valid save must use the current Owner revision");
    assert.equal(body.name, "较新合法保存", "the saved form value must reach the Owner command");
    saveRacePlan = { ...saveRacePlan, name: body.name, revision: 6 };
    return response({ plan: clone(saveRacePlan) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/61/groups" && method === "GET") {
    return response({ items: [], summary: { bound_group_count: 0 } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
    return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/61/nodes" && method === "GET") {
    return response({ items: [] });
  }
  throw new Error(`unexpected save-race request ${method} ${url.pathname}`);
};
try {
  saveRaceWindow.eval(pickerSource);
  saveRaceWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => saveRaceWindow.document.querySelector('[data-action="save-plan"]'), "save race fixture did not render the basic form");
  saveRaceWindow.document.querySelector('[name="plan_name"]').value = "较新合法保存";
  const deferredSave = saveRaceWindow.document.querySelector('[data-action="save-plan"]');
  delaySaveRaceRead = true;
  saveRaceWindow.dispatchEvent(new saveRaceWindow.CustomEvent("aicrm:groupops-detail-refresh", { detail: { planId: 61 } }));
  await waitFor(() => releaseSaveRaceRead && saveRaceWindow.document.body.textContent.includes("加载中"), "older detail request did not begin before the valid save");
  const deferredDraft = saveRaceWindow.document.createElement("input");
  deferredDraft.name = "plan_name";
  deferredDraft.value = "较新合法保存";
  saveRaceWindow.document.getElementById("group-ops-app").append(deferredDraft);
  deferredSave.click();
  await waitFor(() => saveRacePlan.name === "较新合法保存", "the newer valid save did not complete before the old detail response resumed");
  releaseSaveRaceRead();
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(saveRacePlan.name, "较新合法保存", "the delayed old detail response must not overwrite a completed valid save");
  assert.equal(saveRaceWindow.document.body.textContent.includes("较新合法保存"), true, "the rendered detail must retain the newer authoritative plan name");
  console.log("groupops-save-race-dom: PASS");
} finally {
  saveRaceJourney.window.close();
}

// Webhook presentation is rendered by the same standard Host: it must expose
// the configured, callable URL and give a truthful copy receipt. A missing
// descriptor takes the explicit unavailable branch in the production code.
const copiedWebhook = [];
const webhookCalls = [];
let webhookPlan = { plan_id: 52, name: "Webhook 计划", revision: 2, status: "draft", plan_type: "webhook" };
let webhookDescriptor = { configured: false, reference: "", path: "", signature_algorithm: "HMAC-SHA256", signature_header: "X-Signature", timestamp_header: "X-Timestamp", nonce_header: "X-Nonce", client_id_header: "X-Client-ID" };
const webhookJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="52"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/52",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const webhookWindow = webhookJourney.window;
webhookWindow.Headers = Headers;
webhookWindow.Response = Response;
Object.defineProperty(webhookWindow, "crypto", { configurable: true, value: crypto });
Object.defineProperty(webhookWindow.navigator, "clipboard", { configurable: true, value: { writeText: async (value) => copiedWebhook.push(value) } });
webhookWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
webhookWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), webhookWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  webhookCalls.push({ path: url.pathname + url.search, method });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52" && method === "GET") return response({ plan: clone(webhookPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52/webhook-descriptor" && method === "GET") return response(clone(webhookDescriptor));
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/52/webhook-descriptor" && method === "PUT") {
    const body = JSON.parse(String(init.body));
    assert.equal(body.expected_revision, 2, "Webhook save must use the current plan revision");
    assert.match(body.reference, /^groupops-[0-9a-f-]{36}$/, "Webhook must generate its own opaque reference");
    webhookPlan = { ...webhookPlan, revision: 3 };
    webhookDescriptor = { ...webhookDescriptor, configured: true, reference: body.reference, path: `/api/automation/group-ops/webhooks/${body.reference}` };
    return response({ plan: clone(webhookPlan) });
  }
  throw new Error(`unexpected webhook request ${method} ${url.pathname}`);
};
try {
  webhookWindow.eval(pickerSource);
  webhookWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => webhookWindow.document.querySelector('[data-action="save-webhook"]'), "unconfigured webhook did not render its configuration action");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/plans/52").length, 1, "Webhook detail hydration must issue one plan read");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/groups?limit=200&offset=0").length, 0, "Webhook detail hydration must not crawl an unfiltered group directory to decorate persisted bindings");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/groups?owner_userid=7").length, 1, "Webhook owner picker directory remains an independent read");
  assert.equal(webhookCalls.filter((call) => call.method === "GET" && call.path === "/api/admin/automation-conversion/group-ops/plans/52/webhook-descriptor").length, 1, "Webhook descriptor remains an independent read");
  assert.equal(webhookWindow.document.querySelector('[name="webhook_reference"]'), null, "users must not enter technical webhook references");
  webhookWindow.document.querySelector('[data-action="save-webhook"]').click();
  await waitFor(() => webhookWindow.document.querySelector('[data-action="copy-webhook"]'), "saved webhook did not reread and render its copy action");
  const expectedWebhookURL = `https://groupops.test${webhookDescriptor.path}`;
  assert.equal(webhookWindow.document.querySelector(".group-ops__url")?.textContent, expectedWebhookURL, "Webhook presentation must show the configured callable URL");
  assert(webhookWindow.document.body.textContent.includes("无需预设节点") && webhookWindow.document.body.textContent.includes("已绑定群的子集") && webhookWindow.document.body.textContent.includes("签名验证（HMAC-SHA256）") && webhookWindow.document.querySelector(".group-ops__webhook-guide")?.textContent.includes("复制地址不包含凭据，也不能绕过签名验证") && webhookWindow.document.body.textContent.includes("X-Signature / X-Timestamp / X-Nonce / X-Client-ID"), "Webhook presentation must explain dynamic content, descriptor headers and signing without exposing a secret");
  webhookWindow.document.querySelector('[data-action="copy-webhook"]').click();
  await waitFor(() => copiedWebhook[0] === expectedWebhookURL, "Webhook copy did not reach the clipboard");
  await waitFor(() => webhookWindow.document.body.textContent.includes("Webhook 地址已复制"), "Webhook copy did not render a success receipt");
  console.log("groupops-webhook-dom: PASS");
} finally {
  webhookJourney.window.close();
}

// List lifecycle controls must give a visible in-flight state, submit exactly
// once, and only show enabled after the V3 command response has been read.
let listPlan = { plan_id: 13, name: "授权测试群计划", revision: 8, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 2, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
let enableCalls = 0;
let releaseEnable;
let holdConflictReads = false;
const pendingConflictReads = [];
const listRequests = [];
const delayedConflictRead = (body) => new Promise((resolve) => pendingConflictReads.push(() => resolve(response(body))));
const listJourney = new JSDOM(groupOpsListDocument("list"), {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const listWindow = listJourney.window;
listWindow.Headers = Headers;
listWindow.Response = Response;
Object.defineProperty(listWindow, "crypto", { configurable: true, value: crypto });
listWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
listWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), listWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  listRequests.push({ path: url.pathname + url.search, method });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return holdConflictReads ? delayedConflictRead({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] }) : response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return holdConflictReads ? delayedConflictRead(planPage([clone(listPlan)])) : response(planPage([clone(listPlan)]));
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/13" && method === "GET") return holdConflictReads ? delayedConflictRead({ plan: clone(listPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] }) : response({ plan: clone(listPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/13/enable" && method === "POST") {
    enableCalls += 1;
    const body = JSON.parse(String(init.body));
    assert.equal(body.expected_revision, enableCalls === 1 ? 8 : 9, "enable must use the latest read revision");
    if (enableCalls === 1) {
      // Model a concurrent server mutation. The failure must cause reads only;
      // a second POST happens only after the next explicit click.
      listPlan = { ...listPlan, revision: 9 };
      holdConflictReads = true;
      return response({ error: { code: "operations_conflict" } }, 409);
    }
    return new Promise((resolve) => {
      releaseEnable = () => {
        listPlan = { ...listPlan, status: "active", revision: 10 };
        resolve(response({ plan: clone(listPlan) }));
      };
    });
  }
  throw new Error(`unexpected list request ${method} ${url.pathname}`);
};
try {
  listWindow.eval(pickerSource);
  listWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => listWindow.document.querySelector('[data-action="enable-plan"]'), "disabled plan did not render its enable control");
  assert.deepEqual(
    listRequests.filter((request) => request.method === "GET").map((request) => request.path).sort(),
    ["/api/admin/automation-conversion/group-ops/plans?limit=50&offset=0", "/api/admin/common/operation-members?scope=group_ops&page_size=100"].sort(),
    "an initial list must read only its page and the existing operation-member projection",
  );
  assert.equal(listWindow.document.body.textContent.includes("已绑定群（暂不可用）"), false, "a valid zero-or-positive List DTO count remains known");
  const enable = () => listWindow.document.querySelector('[data-action="enable-plan"]');
  enable().click();
  await waitFor(() => pendingConflictReads.length === 3 && enableCalls === 1 && enable()?.disabled, "conflict refresh did not keep lifecycle control locked");
  enable().click();
  assert.equal(enableCalls, 1, "an explicit click during conflict readback must not submit another POST");
  holdConflictReads = false;
  pendingConflictReads.splice(0).forEach((release) => release());
  await waitFor(() => listWindow.document.body.textContent.includes("计划状态、版本或配置不满足要求，请刷新后检查") && !enable()?.disabled, "failed enable must keep a retryable control and visible error");
  assert.equal(listPlan.revision, 9, "conflict fixture must expose a newer server revision");
  assert.equal(listWindow.document.querySelector(".group-ops__notice")?.classList.contains("group-ops__notice--error"), true, "failed enable notice must not use the green success style");
  enable().click();
  enable().click();
  await waitFor(() => enableCalls === 2 && enable()?.disabled && enable()?.textContent === "启用中", "enable must lock repeat clicks and show progress");
  releaseEnable();
  await waitFor(() => listWindow.document.querySelector('[data-action="disable-plan"]') && listWindow.document.body.textContent.includes("已启用"), "enable must read back active status and show a receipt");
  assert.equal(enableCalls, 2, "enable retry may submit once after failure but must ignore the concurrent repeat click");
  console.log("groupops-enable-dom: PASS");
} finally {
  listJourney.window.close();
}

// Offset pagination keeps an already rendered page authoritative for its own
// actions when another page fails. This mounts the real Host and Standard
// controller together so the assertions cover URL transport, DOM state and
// explicit row revisions as one contract.
const pagedPlan = {
  plan_id: 91,
  name: "第 1 页计划",
  revision: 50,
  status: "disabled",
  plan_type: "standard",
  queue_count: 3,
  bound_group_count: 2,
  owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" },
};
const firstPagePlans = Array.from({ length: 50 }, (_, index) => index === 0
  ? pagedPlan
  : { ...pagedPlan, plan_id: 91 + index, name: `第 1 页计划 ${index + 1}`, revision: 50 + index, queue_count: index % 3, bound_group_count: index % 2 });
const secondPagePlan = { ...pagedPlan, plan_id: 141, name: "第 2 页计划", revision: 6, queue_count: 1, bound_group_count: 1 };
let pageFiftyFails = true;
let pagedEnableWrites = 0;
let pagedArchiveWrites = 0;
let pagedWriteReply = "conflict";
let archiveTailRead = false;
let pagedDetailRevision = 50;
const pagedRequests = [];
const paginationJourney = new JSDOM(groupOpsListDocument("list"), {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const paginationWindow = paginationJourney.window;
paginationWindow.Headers = Headers;
paginationWindow.Response = Response;
Object.defineProperty(paginationWindow, "crypto", { configurable: true, value: crypto });
paginationWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
paginationWindow.confirm = () => true;
paginationWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), paginationWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  pagedRequests.push({ path: url.pathname + url.search, method, body: init.body ? JSON.parse(String(init.body)) : null });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    const offset = Number(url.searchParams.get("offset"));
    if (offset === 0) return response(planPage(clone(firstPagePlans), 51, 0, true));
    if (offset === 50 && pageFiftyFails) return response({ code: "service_unavailable" }, 503);
    if (offset === 50 && archiveTailRead) return response(planPage([], 50, 50, false));
    if (offset === 50) return response(planPage([clone(secondPagePlan)], 51, 50, false));
    throw new Error(`unexpected page offset ${offset}`);
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/91/enable" && method === "POST") {
    pagedEnableWrites += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 50, "a retained first-page row must submit its rendered revision, not a fresh detail revision");
    if (pagedWriteReply === "conflict") return response({ error: { code: "operations_conflict" } }, 409);
    if (pagedWriteReply === "empty") return response({});
    if (pagedWriteReply === "wrong-id") return response({ plan: { ...pagedPlan, plan_id: 999, revision: 51, status: "active" } });
    if (pagedWriteReply === "boolean-id") return response({ plan: { ...pagedPlan, plan_id: true, revision: 51, status: "active" } });
    return response({ plan: { ...pagedPlan, revision: 51, status: "disabled" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/91" && method === "DELETE") {
    pagedArchiveWrites += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 50, "archive must submit its rendered revision");
    return response({ plan: { ...pagedPlan, revision: 51, status: "disabled" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/141" && method === "DELETE") {
    pagedArchiveWrites += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 6, "tail-page archive must submit its rendered revision");
    archiveTailRead = true;
    return response({ plan: { ...secondPagePlan, revision: 7, status: "archived" } });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/91" && method === "GET") return response({ plan: { ...clone(pagedPlan), revision: pagedDetailRevision }, members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected paged request ${method} ${url.pathname}`);
};
try {
  paginationWindow.eval(pickerSource);
  paginationWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => paginationWindow.document.querySelector('[data-action="next-list-page"]'), "initial paged list did not render");
  assert.equal(paginationWindow.document.querySelectorAll("tbody tr").length, 50, "a first visible page must render exactly 50 rows from a 51-item fixture");
  assert(paginationWindow.document.body.textContent.includes("第 1–50 项，共 51 项"), "the first page range must describe 50 rendered rows");
  assert.equal(paginationWindow.document.querySelector('[data-action="previous-list-page"]').disabled, true, "the first page must disable previous");
  assert.equal(paginationWindow.document.querySelector('[data-action="next-list-page"]').disabled, false, "the first page must enable next when has_more");
  assert.deepEqual(
    pagedRequests.filter((request) => request.method === "GET").map((request) => request.path).sort(),
    ["/api/admin/automation-conversion/group-ops/plans?limit=50&offset=0", "/api/admin/common/operation-members?scope=group_ops&page_size=100"].sort(),
    "the initial visible page must issue only its page GET and the established member read",
  );
  paginationWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="retry-list-page"]'), "a failed second page did not render a retryable alert");
  assert.equal(paginationWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "a failed target page must lock pagination until its exact retry");
  assert.equal(paginationWindow.document.querySelector('[data-action="enable-plan"]').disabled, false, "a failed target page must not invalidate the retained row's explicit CAS action");
  assert(paginationWindow.document.querySelector('[role="alert"]').textContent.includes("读取当前页失败"), "the failed target page must remain visibly distinct from an empty list");
  pageFiftyFails = false;
  paginationWindow.document.querySelector('[data-action="retry-list-page"]').click();
  await waitFor(() => paginationWindow.document.body.textContent.includes("第 51–51 项，共 51 项"), "retry did not reread the exact second-page offset");
  assert.equal(paginationWindow.document.querySelector('[data-action="previous-list-page"]').disabled, false, "the last page must allow previous");
  assert.equal(paginationWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "the last page must disable next");
  assert.equal(paginationWindow.document.body.textContent.includes("本页通知排队"), true, "page-only metrics must be labeled as page-only");
  assert.equal(pagedRequests.filter((request) => request.path === "/api/admin/automation-conversion/group-ops/plans?limit=50&offset=50" && request.method === "GET").length, 2, "retry must resend only the original failed offset");
  paginationWindow.document.querySelector('[data-action="previous-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="enable-plan"]')?.dataset.planRevision === "50", "previous page did not restore its rendered action snapshot");
  pageFiftyFails = true;
  paginationWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="retry-list-page"]'), "second retained-page failure did not render");
  pagedDetailRevision = 51;
  await paginationWindow.AdminApi.requestJson("/api/admin/automation-conversion/group-ops/plans/91");
  assert.equal(paginationWindow.document.querySelector('[data-action="enable-plan"]')?.dataset.planRevision, "50", "a fresh Host detail cache must not replace the retained row snapshot");
  paginationWindow.document.querySelector('[data-action="enable-plan"]').click();
  await waitFor(() => pagedEnableWrites === 1 && paginationWindow.document.querySelector(".group-ops__notice--error"), "a retained row conflict did not surface after using its original revision");
  assert.equal(paginationWindow.document.body.textContent.includes("已启用"), false, "a 409 must not be rendered as an enable success");
  assert.equal(pagedEnableWrites, 1, "a 409 must not trigger an automatic second write");
  for (const reply of ["empty", "wrong-id", "boolean-id", "wrong-status"]) {
    pagedWriteReply = reply;
    paginationWindow.document.querySelector('[data-action="enable-plan"]').click();
    await waitFor(() => pagedEnableWrites === (reply === "empty" ? 2 : reply === "wrong-id" ? 3 : reply === "boolean-id" ? 4 : 5) && paginationWindow.document.body.textContent.includes("启用结果未确认"), `a 200 ${reply} enable reply was incorrectly accepted`);
  }
  paginationWindow.document.querySelector('[data-action="delete-plan"]').click();
  await waitFor(() => pagedArchiveWrites === 1 && paginationWindow.document.body.textContent.includes("删除结果未确认"), "a wrong-state archive reply was incorrectly accepted");
  pageFiftyFails = false;
  paginationWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="delete-plan"]')?.dataset.planId === "141", "the tail page did not render for archive fallback");
  const tailReadsBeforeArchive = pagedRequests.filter((request) => request.path === "/api/admin/automation-conversion/group-ops/plans?limit=50&offset=50" && request.method === "GET").length;
  paginationWindow.document.querySelector('[data-action="delete-plan"]').click();
  await waitFor(() => paginationWindow.document.querySelector('[data-action="enable-plan"]')?.dataset.planId === "91" && paginationWindow.document.body.textContent.includes("已删除"), "an empty tail page after archive did not return once to the previous page");
  assert.equal(pagedRequests.filter((request) => request.path === "/api/admin/automation-conversion/group-ops/plans?limit=50&offset=50" && request.method === "GET").length, tailReadsBeforeArchive + 1, "tail-page archive must reread its own offset once before fallback");
  console.log("groupops-list-pagination-dom: PASS");
} finally {
  paginationJourney.window.close();
}

// A successful write whose authority readback fails must not unlock any row
// until the retry performs only that GET. Two rows make a single-value lock
// regression observable.
let writeReadbackFails = false;
let writeReadbackPosts = 0;
let writeReadbackPlans = 0;
let writeFirst = { plan_id: 301, name: "写后回读 A", revision: 5, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 0, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
const writeSecond = { ...writeFirst, plan_id: 302, name: "写后回读 B", revision: 8 };
const writeReadbackJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const writeReadbackWindow = writeReadbackJourney.window;
writeReadbackWindow.Headers = Headers;
writeReadbackWindow.Response = Response;
Object.defineProperty(writeReadbackWindow, "crypto", { configurable: true, value: crypto });
writeReadbackWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
writeReadbackWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), writeReadbackWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    writeReadbackPlans += 1;
    if (writeReadbackFails) return response({ code: "service_unavailable" }, 503);
    return response(planPage([clone(writeFirst), clone(writeSecond)], 2, 0, false));
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/301/enable" && method === "POST") {
    writeReadbackPosts += 1;
    assert.equal(JSON.parse(String(init.body)).expected_revision, 5, "the write must use A's rendered revision");
    writeFirst = { ...writeFirst, revision: 6, status: "active" };
    return response({ plan: clone(writeFirst) });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/302/enable" && method === "POST") {
    writeReadbackPosts += 1;
    return response({ plan: { ...writeSecond, revision: 9, status: "active" } });
  }
  throw new Error(`unexpected write-readback request ${method} ${url.pathname}`);
};
try {
  writeReadbackWindow.eval(pickerSource);
  writeReadbackWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => writeReadbackWindow.document.querySelectorAll('[data-action="enable-plan"]').length === 2, "write-readback fixture did not render two rows");
  writeReadbackFails = true;
  writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="301"]').click();
  await waitFor(() => writeReadbackWindow.document.body.textContent.includes("操作已执行，但当前页未更新") && writeReadbackWindow.document.querySelector('[data-action="retry-list-page"]'), "successful write with failed readback did not retain a GET-only retry");
  assert.equal(writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="301"]').disabled, true, "failed readback must lock the written row");
  assert.equal(writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="302"]').disabled, true, "failed readback must also retain any earlier write lock instead of replacing it");
  writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="302"]').click();
  assert.equal(writeReadbackPosts, 1, "a locked second row must not submit another write");
  writeReadbackFails = false;
  writeReadbackWindow.document.querySelector('[data-action="retry-list-page"]').click();
  await waitFor(() => writeReadbackWindow.document.querySelector('[data-action="enable-plan"][data-plan-id="302"]')?.disabled === false, "a successful GET-only retry did not unlock the refreshed page");
  assert.equal(writeReadbackPosts, 1, "readback retry must not repeat the completed write");
  assert.equal(writeReadbackPlans, 3, "write readback must be initial GET, failed GET and one retry GET");
  console.log("groupops-list-write-readback-dom: PASS");
} finally {
  writeReadbackJourney.window.close();
}

// Abort reduces work, but an old fetch can still fulfill or reject. A write
// readback starts a newer generation while a user navigation is outstanding;
// neither the late success nor its late rejection may release the newer busy
// state or publish stale rows.
const racePlan = { plan_id: 211, name: "竞态旧页", revision: 12, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 0, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
const raceReadbackPlan = { ...racePlan, name: "权威回读页", revision: 13, status: "active" };
let releaseRaceWrite;
let releaseRaceReadback;
let releaseLatePage;
let releaseLateMember;
let raceNextSignal;
let racePlanZeroReads = 0;
let raceMemberReads = 0;
const paginationRaceJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const paginationRaceWindow = paginationRaceJourney.window;
paginationRaceWindow.Headers = Headers;
paginationRaceWindow.Response = Response;
Object.defineProperty(paginationRaceWindow, "crypto", { configurable: true, value: crypto });
paginationRaceWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
paginationRaceWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), paginationRaceWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") {
    raceMemberReads += 1;
    if (raceMemberReads === 2) return new Promise((resolve) => { releaseLateMember = resolve; });
    return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    const offset = Number(url.searchParams.get("offset"));
    if (offset === 50) {
      raceNextSignal = init.signal;
      return new Promise((resolve) => { releaseLatePage = resolve; });
    }
    racePlanZeroReads += 1;
    if (racePlanZeroReads === 1) return response(planPage([clone(racePlan)], 51, 0, true));
    return new Promise((resolve) => { releaseRaceReadback = resolve; });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/211/enable" && method === "POST") {
    assert.equal(JSON.parse(String(init.body)).expected_revision, 12, "write/readback race must retain the rendered CAS revision");
    return new Promise((resolve) => { releaseRaceWrite = resolve; });
  }
  throw new Error(`unexpected race request ${method} ${url.pathname}`);
};
try {
  paginationRaceWindow.eval(pickerSource);
  paginationRaceWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => paginationRaceWindow.document.querySelector('[data-action="enable-plan"]'), "race fixture did not render initial action");
  paginationRaceWindow.document.querySelector('[data-action="enable-plan"]').click();
  await waitFor(() => typeof releaseRaceWrite === "function", "race fixture did not start the write");
  paginationRaceWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => typeof releaseLatePage === "function" && typeof releaseLateMember === "function", "race fixture did not start the older navigation read");
  releaseRaceWrite(response({ plan: clone(raceReadbackPlan) }));
  await waitFor(() => typeof releaseRaceReadback === "function", "write did not begin its newer authoritative readback");
  assert.equal(raceNextSignal?.aborted, true, "a write readback must abort its superseded navigation request");
  releaseLatePage(response(planPage([{ ...racePlan, plan_id: 261, name: "过期第 2 页" }], 51, 50, false)));
  releaseLateMember(response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] }));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(paginationRaceWindow.document.body.textContent.includes("过期第 2 页"), false, "a late old success must not publish over the newer readback");
  assert.equal(paginationRaceWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "an old finally must not release the newer readback busy state");
  releaseRaceReadback(response(planPage([clone(raceReadbackPlan)], 51, 0, true)));
  await waitFor(() => paginationRaceWindow.document.querySelector('[data-action="disable-plan"]')?.dataset.planRevision === "13", "the authoritative readback did not publish its newer row");
  assert.equal(paginationRaceWindow.document.querySelector('[data-action="next-list-page"]').disabled, false, "the completed newer readback must unlock pagination after an old success finally");
  console.log("groupops-list-pagination-race-dom: PASS");
} finally {
  paginationRaceJourney.window.close();
}

// The rejection side is independent from the late-success case above: even
// after the newer readback has rendered, a superseded request can still fail.
const lateRejectPlan = { plan_id: 281, name: "旧请求", revision: 12, status: "disabled", plan_type: "standard", queue_count: 0, bound_group_count: 0, owner: { staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营", name_source: "wecom_profile", profile_read_state: "ready" } };
const lateRejectReadbackPlan = { ...lateRejectPlan, name: "新回读", revision: 13, status: "active" };
let releaseLateRejectWrite;
let releaseLateRejectReadback;
let rejectLatePage;
let lateRejectPlanZeroReads = 0;
const lateRejectJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const lateRejectWindow = lateRejectJourney.window;
lateRejectWindow.Headers = Headers;
lateRejectWindow.Response = Response;
Object.defineProperty(lateRejectWindow, "crypto", { configurable: true, value: crypto });
lateRejectWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
lateRejectWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), lateRejectWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [{ staff_id: 7, sender_userid: "wecom-owner", display_name: "一号运营" }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") {
    const offset = Number(url.searchParams.get("offset"));
    if (offset === 50) return new Promise((resolve, reject) => { rejectLatePage = reject; });
    lateRejectPlanZeroReads += 1;
    if (lateRejectPlanZeroReads === 1) return response(planPage([clone(lateRejectPlan)], 51, 0, true));
    return new Promise((resolve) => { releaseLateRejectReadback = resolve; });
  }
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/281/enable" && method === "POST")
    return new Promise((resolve) => { releaseLateRejectWrite = resolve; });
  throw new Error(`unexpected late-reject request ${method} ${url.pathname}`);
};
try {
  lateRejectWindow.eval(pickerSource);
  lateRejectWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => lateRejectWindow.document.querySelector('[data-action="enable-plan"]'), "late-reject fixture did not render initial action");
  lateRejectWindow.document.querySelector('[data-action="enable-plan"]').click();
  await waitFor(() => typeof releaseLateRejectWrite === "function", "late-reject fixture did not start its write");
  lateRejectWindow.document.querySelector('[data-action="next-list-page"]').click();
  await waitFor(() => typeof rejectLatePage === "function", "late-reject fixture did not start the superseded navigation read");
  releaseLateRejectWrite(response({ plan: clone(lateRejectReadbackPlan) }));
  await waitFor(() => typeof releaseLateRejectReadback === "function", "late-reject write did not start newer readback");
  releaseLateRejectReadback(response(planPage([clone(lateRejectReadbackPlan)], 51, 0, true)));
  await waitFor(() => lateRejectWindow.document.querySelector('[data-action="disable-plan"]')?.dataset.planRevision === "13", "newer readback did not render before late rejection");
  rejectLatePage(new Error("late page rejection"));
  await new Promise((resolve) => setTimeout(resolve, 0));
  assert.equal(lateRejectWindow.document.body.textContent.includes("late page rejection"), false, "a late old rejection must not replace a completed newer page");
  assert.equal(lateRejectWindow.document.querySelector('[data-action="next-list-page"]').disabled, false, "a late old finally must not re-lock the completed newer page");
  console.log("groupops-list-pagination-late-reject-dom: PASS");
} finally {
  lateRejectJourney.window.close();
}

// An initial read failure is unknown data, not a legitimate zero-plan page.
const initialFailureJourney = new JSDOM(groupOpsListDocument("list"), {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const initialFailureWindow = initialFailureJourney.window;
initialFailureWindow.Headers = Headers;
initialFailureWindow.Response = Response;
Object.defineProperty(initialFailureWindow, "crypto", { configurable: true, value: crypto });
initialFailureWindow.document.cookie = "aicrm_admin_csrf=test-csrf";
let initialFailureStatus = 503;
initialFailureWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), initialFailureWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response({ code: initialFailureStatus === 403 ? "forbidden" : "service_unavailable" }, initialFailureStatus);
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
  throw new Error(`unexpected initial-failure request ${method} ${url.pathname}`);
};
try {
  initialFailureWindow.eval(pickerSource);
  initialFailureWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => initialFailureWindow.document.querySelector('[role="alert"]'), "initial list failure did not render a visible alert");
  assert.equal(initialFailureWindow.document.querySelector(".group-ops__metric-value")?.textContent, "—", "an initial failure must not display a fabricated zero total");
  assert(initialFailureWindow.document.body.textContent.includes("尚未取得列表数据"), "an initial failure must remain distinct from an authoritative empty page");
  assert.equal(initialFailureWindow.document.querySelector('[data-action="next-list-page"]').disabled, true, "pagination must remain disabled until the failed first page is retried");
  groupOpsCreateAction(initialFailureWindow).click();
  assert.equal(initialFailureWindow.document.querySelector(".group-ops__metric-value")?.textContent, "—", "opening creation after an unknown page must not fabricate a zero total");
  initialFailureWindow.document.querySelector('[data-action="cancel-create-plan"]').click();
  assert.equal(initialFailureWindow.document.querySelector(".group-ops__metric-value")?.textContent, "—", "cancelling creation after an unknown page must not fabricate a zero total");
  initialFailureStatus = 403;
  initialFailureWindow.document.querySelector('[data-action="retry-list-page"]').click();
  await waitFor(() => initialFailureWindow.document.body.textContent.includes("当前账号无权读取运营计划"), "403 list read did not clear the unknown page into a visible access error");
  assert.equal(groupOpsCreateAction(initialFailureWindow).disabled, true, "a forbidden list read must disable list writes");
  console.log("groupops-list-initial-failure-dom: PASS");
} finally {
  initialFailureJourney.window.close();
}

// Creation uses the same frozen picker + V3 Host bridge as the production
// list. These requests are synthetic and assert client recovery semantics,
// not a PostgreSQL receipt or a deployed browser result.
function createDetail({
  id = 501,
  revision = 1,
  name = "保留的创建草稿",
  status = "draft",
  planType = "standard",
  members = [],
} = {}) {
  return {
    plan: {
      plan_id: id,
      name,
      revision,
      status,
      plan_type: planType,
      queue_count: 0,
      bound_group_count: 0,
    },
    members,
    group_assets: [],
    nodes: [],
  };
}

async function startCreateJourney(label, handlers = {}) {
  const errors = [];
  const navigations = [];
  const createConsole = new VirtualConsole();
  createConsole.on("jsdomError", (error) => {
    const message = String(error?.message || error);
    if (message.includes("Not implemented: navigation")) navigations.push(message);
    else errors.push(message);
  });
  const journey = new JSDOM(
    groupOpsListDocument("list"),
    {
      url: `https://groupops.test/admin/automation-conversion/group-ops/ui?fixture=${encodeURIComponent(label)}`,
      runScripts: "outside-only",
      pretendToBeVisual: true,
      virtualConsole: createConsole,
    },
  );
  const fixtureWindow = journey.window;
  fixtureWindow.Headers = Headers;
  fixtureWindow.Response = Response;
  Object.defineProperty(fixtureWindow, "crypto", {
    configurable: true,
    value: crypto,
  });
  fixtureWindow.document.cookie = "aicrm_admin_csrf=create-session-a";
  const requests = [];
  fixtureWindow.fetch = async (input, init = {}) => {
    const url = new URL(String(input), fixtureWindow.location.href);
    const method = String(init.method || "GET").toUpperCase();
    const body = init.body ? JSON.parse(String(init.body)) : null;
    const request = {
      path: url.pathname + url.search,
      pathname: url.pathname,
      method,
      body,
      key: new Headers(init.headers).get("Idempotency-Key") || "",
    };
    requests.push(request);
    if (
      url.pathname === "/api/admin/common/operation-members" &&
      method === "GET"
    )
      return response({
        items: [
          {
            staff_id: 7,
            sender_userid: "fixture-owner",
            display_name: "合成员",
          },
        ],
      });
    if (
      url.pathname === "/api/admin/automation-conversion/group-ops/plans" &&
      method === "GET"
    )
      return handlers.list
        ? handlers.list(request)
        : response(planPage([], 0, 0, false));
    if (
      url.pathname === "/api/admin/automation-conversion/group-ops/plans" &&
      method === "POST"
    )
      return handlers.post
        ? handlers.post(request)
        : response(createDetail({ name: request.body.name }));
    if (
      url.pathname === "/api/admin/automation-conversion/group-ops/plans/501" &&
      method === "PUT"
    )
      return handlers.configuration
        ? handlers.configuration(request)
        : response(
            createDetail({
              revision: 2,
              name: request.body.name,
              planType: "webhook",
              members: [{ staff_id: 7 }],
            }),
          );
    throw new Error(`unexpected ${label} request ${method} ${url.pathname}`);
  };
  fixtureWindow.eval(pickerSource);
  fixtureWindow.eval(bundle.outputFiles[0].text);
  await waitFor(
    () =>
      groupOpsCreateAction(fixtureWindow),
    `${label} list did not render`,
  );
  return { journey, fixtureWindow, requests, errors, navigations };
}

async function openCreate(
  fixture,
  { name = "保留的创建草稿", planType = "webhook", owner = true } = {},
) {
  const { fixtureWindow } = fixture;
  groupOpsCreateAction(fixtureWindow).click();
  await waitFor(
    () => fixtureWindow.document.querySelector('[name="create_plan_name"]'),
    "create panel did not render",
  );
  fixtureWindow.document.querySelector('[name="create_plan_name"]').value =
    name;
  fixtureWindow.document.querySelector('[name="create_plan_type"]').value =
    planType;
  if (!owner) return;
  fixtureWindow.document
    .querySelector('[data-action="pick-create-owner"]')
    .click();
  // The live GroupOps Host owns this field through the shared V3 staff
  // picker; exercise the same local staff-id selection path as the page.
  await waitFor(
    () => fixtureWindow.document.querySelector(
      '[data-v3-selection-session="staff"] [data-v3-staff-key$=":7"]',
    ),
    "create owner picker did not render",
  );
  fixtureWindow.document
    .querySelector('[data-v3-selection-session="staff"] [data-v3-staff-key$=":7"]')
    .click();
  fixtureWindow.document
    .querySelector('[data-v3-selection-session="staff"] [data-v3-staff-confirm]')
    .click();
  await waitFor(
    () =>
      fixtureWindow.document.querySelector('[name="create_owner_userid"]')
        ?.value === "7",
    "create owner was not selected",
  );
}

function creationPosts(requests) {
  return requests.filter(
    (request) =>
      request.path === "/api/admin/automation-conversion/group-ops/plans" &&
      request.method === "POST",
  );
}
function configurationPuts(requests) {
  return requests.filter(
    (request) =>
      request.path === "/api/admin/automation-conversion/group-ops/plans/501" &&
      request.method === "PUT",
  );
}
function closeCreateJourney(fixture, expectedNavigations = 0) {
  assert.deepEqual(
    fixture.errors,
    [],
    "create fixture emitted an unexpected browser error",
  );
  assert.equal(
    fixture.navigations.length,
    expectedNavigations,
    "create fixture navigation count did not match the confirmed outcome",
  );
  fixture.journey.window.close();
}

function pagedCreateList(request) {
  const page = new URL(request.path, "https://groupops.test");
  const offset = Number(page.searchParams.get("offset") || 0);
  return response(planPage([], 51, offset, offset === 0));
}

// List pagination redraws the production creation panel. Editable values must
// survive the redraw; only a submitted pending intent is frozen.
{
  let listReads = 0;
  const fixture = await startCreateJourney("create-draft-pagination", {
    list: (request) => {
      listReads += 1;
      return pagedCreateList(request);
    },
  });
  try {
    await openCreate(fixture, { name: "翻页保留草稿", planType: "webhook" });
    const next = fixture.fixtureWindow.document.querySelector(
      '[data-action="next-list-page"]',
    );
    assert.equal(next.disabled, false, "the fixture must expose a next list page");
    next.click();
    await waitFor(
      () =>
        listReads === 2 &&
        fixture.fixtureWindow.document.querySelector('[name="create_plan_name"]')
          ?.value === "翻页保留草稿",
      "list pagination did not preserve the editable creation draft",
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[name="create_plan_type"]')
        ?.value,
      "webhook",
      "list pagination must retain the editable plan type",
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[name="create_owner_userid"]')
        ?.value,
      "7",
      "list pagination must retain the editable selected owner",
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// An explicit rejection returns the panel to editable mode. An operator can
// change the draft and paginate without reverting to the rejected snapshot.
{
  let listReads = 0;
  const fixture = await startCreateJourney("create-rejected-draft-pagination", {
    list: (request) => {
      listReads += 1;
      return pagedCreateList(request);
    },
    post: () => response({ code: "validation_failed" }, 400),
  });
  try {
    await openCreate(fixture, { name: "被拒绝的初稿", planType: "webhook" });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document
          .querySelector('.group-ops__modal-notice[role="alert"]')
          ?.textContent.includes("创建被拒绝"),
      "explicit rejection did not return the draft to editable mode",
    );
    fixture.fixtureWindow.document.querySelector('[name="create_plan_name"]').value =
      "被拒后修改的草稿";
    fixture.fixtureWindow.document
      .querySelector('[data-action="next-list-page"]')
      .click();
    await waitFor(
      () =>
        listReads === 2 &&
        fixture.fixtureWindow.document.querySelector('[name="create_plan_name"]')
          ?.value === "被拒后修改的草稿",
      "pagination after a rejected create reverted the operator's edited draft",
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// Client validation has no mutation and retains typed values through its render.
{
  const fixture = await startCreateJourney("create-client-validation");
  try {
    await openCreate(fixture, {
      name: "未选负责人草稿",
      planType: "webhook",
      owner: false,
    });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document
          .querySelector('.group-ops__modal-notice[role="alert"]')
          ?.textContent.includes("请选择运营成员"),
      "missing owner did not produce a persistent alert",
    );
    assert.equal(
      creationPosts(fixture.requests).length,
      0,
      "missing owner must issue zero POSTs",
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[name="create_plan_name"]')
        ?.value,
      "未选负责人草稿",
      "missing owner must retain the typed name",
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[name="create_plan_type"]')
        ?.value,
      "webhook",
      "missing owner must retain the typed type",
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// Explicit rejects are distinct from an ambiguous transport outcome. 401/403
// stop the flow; validation and 409 retain an editable draft for a new intent.
for (const [status, code, locked] of [
  [400, "validation_failed", false],
  [401, "unauthorized", true],
  [403, "forbidden", true],
  [409, "operations_conflict", false],
]) {
  const fixture = await startCreateJourney(`create-reject-${status}`, {
    post: () => response({ code }, status),
  });
  try {
    await openCreate(fixture, { name: `明确拒绝-${status}` });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document
          .querySelector('.group-ops__modal-notice[role="alert"]')
          ?.textContent.includes(
            status === 401 || status === 403 ? "当前账号无权" : "创建被拒绝",
          ),
      `HTTP ${status} outcome was not visible`,
    );
    assert.equal(
      creationPosts(fixture.requests).length,
      1,
      `HTTP ${status} must issue one initial POST`,
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[name="create_plan_name"]')
        ?.disabled,
      locked,
      `HTTP ${status} must ${locked ? "freeze" : "allow editing"} the draft`,
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelectorAll(
        '.group-ops__modal-notice[role="alert"]',
      ).length,
      1,
      `HTTP ${status} must not duplicate alerts`,
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// A 5xx is not evidence that creation failed. A manual same-key replay is the
// only next POST and preserves the frozen name; it is not an automatic retry.
{
  let postCalls = 0;
  const fixture = await startCreateJourney("create-5xx-retry", {
    post: (request) => {
      postCalls += 1;
      if (postCalls === 1)
        return response({ code: "service_unavailable" }, 503);
      return response(createDetail({ name: request.body.name }));
    },
  });
  try {
    await openCreate(fixture, { name: "五百重试草稿" });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document.querySelector(
          '[data-action="retry-create-plan"]',
        ),
      "5xx did not freeze an explicit same-key retry",
    );
    const first = creationPosts(fixture.requests)[0];
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[name="create_plan_name"]')
        ?.disabled,
      true,
      "unknown create must freeze its draft",
    );
    const ownerReadsBefore = fixture.requests.filter(
      (request) => request.pathname === "/api/admin/common/operation-members",
    ).length;
    const ownerButton = fixture.fixtureWindow.document.querySelector(
      '[data-action="pick-create-owner"]',
    );
    assert.equal(ownerButton.disabled, true, "unknown create must disable the frozen picker trigger");
    ownerButton.dispatchEvent(new fixture.fixtureWindow.Event("click"));
    assert.equal(
      fixture.requests.filter(
        (request) => request.pathname === "/api/admin/common/operation-members",
      ).length,
      ownerReadsBefore,
      "an unknown create must not reopen the frozen owner picker",
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[data-v3-selection-session="staff"]'),
      null,
      "an unknown create must not reopen the shared owner picker",
    );
    fixture.fixtureWindow.document
      .querySelector('[data-action="retry-create-plan"]')
      .click();
    await waitFor(
      () => configurationPuts(fixture.requests).length === 1,
      "same-key create replay did not continue to configuration",
    );
    const replay = creationPosts(fixture.requests)[1];
    assert.equal(
      replay.key,
      first.key,
      "a 5xx replay must reuse the original POST key",
    );
    assert.deepEqual(
      replay.body,
      first.body,
      "a 5xx replay must reuse the original POST payload",
    );
    assert.notEqual(
      configurationPuts(fixture.requests)[0].key,
      first.key,
      "configuration must use its own receipt key",
    );
    await waitFor(
      () => fixture.navigations.length === 1,
      "confirmed create did not complete with one navigation",
    );
  } finally {
    closeCreateJourney(fixture, 1);
  }
}

// A genuinely pending POST keeps the create action and picker frozen before
// any result exists; a second click must not create a second in-flight request.
{
  let releasePost;
  let pendingRequest;
  const fixture = await startCreateJourney("create-pending-single-flight", {
    post: (request) => {
      pendingRequest = request;
      return new Promise((resolve) => {
        releasePost = resolve;
      });
    },
  });
  try {
    await openCreate(fixture, { name: "等待中的创建" });
    const save = fixture.fixtureWindow.document.querySelector(
      '[data-action="create-plan"]',
    );
    save.click();
    save.click();
    await waitFor(
      () => typeof releasePost === "function",
      "pending create did not start its first POST",
    );
    assert.equal(creationPosts(fixture.requests).length, 1, "a pending create must be single-flight");
    const ownerReadsBefore = fixture.requests.filter(
      (request) => request.pathname === "/api/admin/common/operation-members",
    ).length;
    const ownerButton = fixture.fixtureWindow.document.querySelector(
      '[data-action="pick-create-owner"]',
    );
    assert.equal(ownerButton.disabled, true, "a pending create must disable the picker trigger");
    ownerButton.dispatchEvent(new fixture.fixtureWindow.Event("click"));
    assert.equal(
      fixture.requests.filter(
        (request) => request.pathname === "/api/admin/common/operation-members",
      ).length,
      ownerReadsBefore,
      "a pending create must reject an owner-picker action dispatch",
    );
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[data-v3-selection-session="staff"]'),
      null,
      "a pending create must not open the shared owner picker",
    );
    releasePost(response(createDetail({ name: pendingRequest.body.name })));
    await waitFor(
      () => fixture.navigations.length === 1,
      "a resolved pending create did not complete with one navigation",
    );
  } finally {
    closeCreateJourney(fixture, 1);
  }
}

// The accepted create snapshot is exactly revision 1, draft, and the frozen
// name. A success-shaped mismatch is unknown and cannot start owner PUT.
for (const [label, malformed] of [
  (request) => createDetail({ id: true, name: request.body.name }),
  (request) => createDetail({ revision: 2, name: request.body.name }),
  (request) => createDetail({ status: "active", name: request.body.name }),
  (request) => createDetail({ name: `${request.body.name}-错误` }),
].entries()) {
  const fixture = await startCreateJourney(`create-invalid-post-response-${label}`, {
    post: (request) => response(malformed(request)),
  });
  try {
    await openCreate(fixture, { name: `非法创建回包-${label}` });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document.querySelector(
          '[data-action="retry-create-plan"]',
        ),
      `invalid create response ${label} was accepted as success`,
    );
    assert.equal(
      configurationPuts(fixture.requests).length,
      0,
      `an invalid create response ${label} must not begin owner configuration`,
    );
    assert.equal(
      fixture.fixtureWindow.document.body.textContent.includes("创建成功"),
      false,
      `an invalid create response ${label} must not claim success`,
    );
    assert.equal(
      fixture.fixtureWindow.document.body.textContent.includes("response.text"),
      false,
      `invalid create response ${label} must reach contract validation, not fail as a transport TypeError`,
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// The first POST and then the configuration PUT can each be accepted by the
// server while their client response is lost. Both recover by their own key;
// neither path sends another POST.
{
  let postCalls = 0;
  let configurationCalls = 0;
  const fixture = await startCreateJourney("create-lost-response", {
    post: (request) => {
      postCalls += 1;
      if (postCalls === 1)
        throw new Error("connection dropped after create acceptance");
      return response(createDetail({ name: request.body.name }));
    },
    configuration: (request) => {
      configurationCalls += 1;
      if (configurationCalls === 1)
        throw new Error("connection dropped after configuration acceptance");
      return response(
        createDetail({
          revision: 2,
          name: request.body.name,
          planType: "webhook",
          members: [{ staff_id: 7 }],
        }),
      );
    },
  });
  try {
    await openCreate(fixture, { name: "丢响应草稿" });
    const firstSave = fixture.fixtureWindow.document.querySelector(
      '[data-action="create-plan"]',
    );
    firstSave.click();
    firstSave.click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document.querySelector(
          '[data-action="retry-create-plan"]',
        ),
      "lost POST response did not expose explicit recovery",
    );
    assert.equal(
      creationPosts(fixture.requests).length,
      1,
      "double-click must start only one create POST",
    );
    const postKey = creationPosts(fixture.requests)[0].key;
    fixture.fixtureWindow.document
      .querySelector('[data-action="retry-create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document.querySelector(
          '[data-action="retry-create-configuration"]',
        ),
      "lost PUT response did not expose configuration recovery",
    );
    const postReplay = creationPosts(fixture.requests)[1];
    const firstConfiguration = configurationPuts(fixture.requests)[0];
    assert.equal(
      postReplay.key,
      postKey,
      "lost POST response must replay the same POST key",
    );
    assert.equal(
      firstConfiguration.body.expected_revision,
      1,
      "configuration must retain the creation response revision",
    );
    assert.equal(firstConfiguration.body.plan_type, "webhook");
    assert.equal(firstConfiguration.body.owner_staff_id, 7);
    fixture.fixtureWindow.document
      .querySelector('[data-action="retry-create-configuration"]')
      .click();
    await waitFor(
      () => configurationPuts(fixture.requests).length === 2,
      "explicit configuration recovery did not issue its PUT replay",
    );
    const configurationReplay = configurationPuts(fixture.requests)[1];
    assert.equal(
      creationPosts(fixture.requests).length,
      2,
      "configuration recovery must not create another plan",
    );
    assert.equal(
      configurationReplay.key,
      firstConfiguration.key,
      "lost PUT response must replay the same configuration key",
    );
    assert.deepEqual(
      configurationReplay.body,
      firstConfiguration.body,
      "lost PUT response must retain the original configuration snapshot",
    );
    await waitFor(
      () => fixture.navigations.length === 1,
      "confirmed configuration recovery did not complete with one navigation",
    );
  } finally {
    closeCreateJourney(fixture, 1);
  }
}

// The configuration receipt must advance exactly one revision and echo the
// original draft state, name, type and owner.
for (const [label, malformed] of [
  [
    "wrong-id",
    (request) => createDetail({
      id: 502,
      revision: 2,
      name: request.body.name,
      planType: "webhook",
      members: [{ staff_id: 7 }],
    }),
  ],
  [
    "invalid-revision",
    (request) => createDetail({
      revision: "2",
      name: request.body.name,
      planType: "webhook",
      members: [{ staff_id: 7 }],
    }),
  ],
  [
    "wrong-name",
    (request) => createDetail({
      revision: 2,
      name: `${request.body.name}-错误`,
      planType: "webhook",
      members: [{ staff_id: 7 }],
    }),
  ],
  [
    "wrong-status",
    (request) => createDetail({
      revision: 2,
      name: request.body.name,
      status: "active",
      planType: "webhook",
      members: [{ staff_id: 7 }],
    }),
  ],
  [
    "jumped-revision",
    (request) => createDetail({
      revision: 3,
      name: request.body.name,
      planType: "webhook",
      members: [{ staff_id: 7 }],
    }),
  ],
]) {
  const fixture = await startCreateJourney(`create-${label}`, {
    post: (request) => response(createDetail({ name: request.body.name })),
    configuration: (request) => response(malformed(request)),
  });
  try {
    await openCreate(fixture, { name: `错误回包-${label}` });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document.querySelector(
          '[data-action="retry-create-configuration"]',
        ),
      `${label} configuration response was accepted as success`,
    );
    assert.equal(
      creationPosts(fixture.requests).length,
      1,
      `${label} must not create a second plan`,
    );
    assert.equal(
      fixture.fixtureWindow.document.body.textContent.includes("创建成功"),
      false,
      `${label} must not claim success`,
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// A changed CSRF session is not evidence of the same admin principal. The
// in-memory marker blocks replay before the Host receives another POST.
{
  const fixture = await startCreateJourney("create-session-change", {
    post: () => {
      throw new Error("connection dropped after create acceptance");
    },
  });
  try {
    await openCreate(fixture, { name: "会话变化草稿" });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document.querySelector(
          '[data-action="retry-create-plan"]',
        ),
      "session fixture did not reach unknown create state",
    );
    fixture.fixtureWindow.document.cookie = "aicrm_admin_csrf=create-session-b";
    fixture.fixtureWindow.document
      .querySelector('[data-action="retry-create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document
          .querySelector('.group-ops__modal-notice[role="alert"]')
          ?.textContent.includes("登录状态已变化"),
      "changed CSRF session did not block recovery",
    );
    assert.equal(
      creationPosts(fixture.requests).length,
      1,
      "a changed CSRF session must issue zero replay POSTs",
    );
  } finally {
    closeCreateJourney(fixture);
  }
}

// The initial POST can complete while the CSRF session changes before the
// Host starts the dependent PUT. The Host rechecks the private marker, keeps
// the known plan ID visible, and issues no configuration request.
{
  let fixture;
  fixture = await startCreateJourney("create-session-change-after-post", {
    post: (request) => {
      fixture.fixtureWindow.document.cookie = "aicrm_admin_csrf=create-session-b";
      return response(createDetail({ name: request.body.name }));
    },
  });
  try {
    await openCreate(fixture, { name: "配置前会话变化" });
    fixture.fixtureWindow.document
      .querySelector('[data-action="create-plan"]')
      .click();
    await waitFor(
      () =>
        fixture.fixtureWindow.document
          .querySelector('.group-ops__modal-notice[role="alert"]')
          ?.textContent.includes("登录状态已变化"),
      "a CSRF change after POST did not stop dependent configuration",
    );
    assert.equal(creationPosts(fixture.requests).length, 1, "the accepted create must retain its one POST");
    assert.equal(configurationPuts(fixture.requests).length, 0, "a changed session must prevent the dependent PUT");
    assert.equal(
      fixture.fixtureWindow.document.querySelector('[href="/admin/automation-conversion/group-ops/plans/501"]') !== null,
      true,
      "a known created plan must remain reachable after the session changes",
    );
  } finally {
    closeCreateJourney(fixture);
  }
}
console.log("groupops-create-recovery-dom: PASS");

// Archived plans are terminal in both projected list and detail views. The
// browser must not render an enable/delete path or a writable detail control.
const archivedPlan = {
  plan_id: 77,
  name: "已删除群运营计划",
  revision: 12,
  status: "archived",
  plan_type: "standard",
  queue_count: 0,
  owner: {
    staff_id: 7,
    sender_userid: "wecom-owner",
    display_name: "一号运营",
    name_source: "wecom_profile",
    profile_read_state: "ready",
  },
};
const archivedListJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="list"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/ui",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const archivedListWindow = archivedListJourney.window;
archivedListWindow.Headers = Headers;
archivedListWindow.Response = Response;
Object.defineProperty(archivedListWindow, "crypto", { configurable: true, value: crypto });
archivedListWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), archivedListWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response(planPage([clone(archivedPlan)]));
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/77" && method === "GET") return response({ plan: clone(archivedPlan), members: [{ staff_id: 7 }], group_assets: [], nodes: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [], total: 0, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected archived-list request ${method} ${url.pathname}`);
};
try {
  archivedListWindow.eval(pickerSource);
  archivedListWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => archivedListWindow.document.body.textContent.includes("已删除"), "archived list status did not render");
  assert(archivedListWindow.document.body.textContent.includes("已绑定群（暂不可用）"), "an older list response must make the missing binding metric visibly unknown");
  assert(archivedListWindow.document.body.textContent.includes("一号运营"), "list must render the same trusted owner projection as detail");
  assert.equal(archivedListWindow.document.querySelector('[data-action="enable-plan"]'), null, "archived list must not render an enable action");
  assert.equal(archivedListWindow.document.querySelector('[data-action="delete-plan"]'), null, "archived list must not offer a repeat archive action");
  console.log("groupops-archived-list-dom: PASS");
} finally {
  archivedListJourney.window.close();
}

const archivedWrites = [];
const archivedDetailJourney = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="detail" data-plan-id="77"></main></body></html>`, {
  url: "https://groupops.test/admin/automation-conversion/group-ops/plans/77",
  runScripts: "outside-only",
  pretendToBeVisual: true,
});
const archivedDetailWindow = archivedDetailJourney.window;
archivedDetailWindow.Headers = Headers;
archivedDetailWindow.Response = Response;
Object.defineProperty(archivedDetailWindow, "crypto", { configurable: true, value: crypto });
archivedDetailWindow.fetch = async (input, init = {}) => {
  const url = new URL(String(input), archivedDetailWindow.location.href);
  const method = String(init.method || "GET").toUpperCase();
  if (method !== "GET") archivedWrites.push({ path: url.pathname, method });
  if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/plans/77" && method === "GET") return response({ plan: clone(archivedPlan), members: [{ staff_id: 7 }], group_assets: [{ asset_reference: "archived-group" }], nodes: [{ node_id: 11, position: 1, kind: "message", day_index: 1, scheduled_time: "20:00", trigger_time_label: "20:00", action_title: "已归档动作", node_status: "active", message_text: "只读内容", delay_minutes: 0, material_plan: { references: [] } }] });
  if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") return response({ items: [{ chat_reference: "archived-group", owner_staff_id: 7, display_name: "已归档群", member_count: 2, external_member_count: 1 }], total: 1, limit: 200, offset: 0, has_more: false });
  throw new Error(`unexpected archived-detail request ${method} ${url.pathname}`);
};
try {
  archivedDetailWindow.eval(pickerSource);
  archivedDetailWindow.eval(bundle.outputFiles[0].text);
  await waitFor(() => archivedDetailWindow.document.querySelector('[name="plan_name"]'), "archived detail did not render");
  assert.equal(archivedDetailWindow.document.querySelector('[name="plan_name"]')?.disabled, true, "archived plan name must be read-only");
  assert.equal(archivedDetailWindow.document.querySelector('[name="status"]')?.value, "archived", "archived detail must keep the terminal status selected");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="save-plan"]'), null, "archived detail must not render a base save action");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="save-active-detail-panel"]')?.disabled, true, "archived detail must disable save-current-dimension");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="pick-plan-owner"]'), null, "archived detail must not offer owner changes");
  archivedDetailWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="groups"]').click();
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="open-group-picker"]'), null, "archived detail must not offer group binding");
  archivedDetailWindow.document.querySelector('[data-action="switch-detail-panel"][data-panel="nodes"]').click();
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="open-node-modal"]'), null, "archived detail must not offer node creation");
  assert.equal(archivedDetailWindow.document.querySelector('[data-action="edit-node"]'), null, "archived detail must not offer node edits");
  assert.deepEqual(archivedWrites, [], "archived UI navigation must not submit a write");
  console.log("groupops-archived-detail-dom: PASS");
} finally {
  archivedDetailJourney.window.close();
}

// The groups page must distinguish an unavailable directory from a confirmed
// empty result. A failed follow-up read keeps the last rows visible, while an
// initial failure has no rows to retain.
async function assertGroupsReadFailureJourney({ hasPreviousRows }) {
  let groupReads = 0;
  const journey = new JSDOM(`<!doctype html><html><body><main id="group-ops-app" data-page-mode="groups"></main></body></html>`, {
    url: "https://groupops.test/admin/automation-conversion/group-ops/groups/ui",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  const page = journey.window;
  page.Headers = Headers;
  page.Response = Response;
  Object.defineProperty(page, "crypto", { configurable: true, value: crypto });
  page.fetch = async (input, init = {}) => {
    const url = new URL(String(input), page.location.href);
    const method = String(init.method || "GET").toUpperCase();
    if (url.pathname === "/api/admin/automation-conversion/group-ops/groups" && method === "GET") {
      groupReads += 1;
      if (!hasPreviousRows || groupReads > 1)
        return response({ code: "directory_unavailable" }, 503);
      return response({
        items: [{ chat_reference: "known-group", display_name: "已读取群", owner_staff_id: 7 }],
        total: 1,
        limit: 200,
        offset: 0,
        has_more: false,
      });
    }
    if (url.pathname === "/api/admin/automation-conversion/group-ops/plans" && method === "GET") return response({ items: [], total: 0, limit: 50, offset: 0, has_more: false, queue_count: 0 });
    if (url.pathname === "/api/admin/common/operation-members" && method === "GET") return response({ items: [] });
    throw new Error(`unexpected group read request ${method} ${url.pathname}${url.search}`);
  };
  try {
    page.eval(pickerSource);
    page.eval(bundle.outputFiles[0].text);
    if (!hasPreviousRows) {
      await waitFor(
        () => page.document.body.textContent.includes("群聊列表暂不可读取"),
        "initial groups read failure did not identify an unavailable directory",
      );
      assert.equal(
        page.document.body.textContent.includes("暂无数据"),
        false,
        "an initial group read failure must not look like a confirmed empty directory",
      );
      return;
    }
    await waitFor(() => page.document.body.textContent.includes("已读取群"), "initial group rows did not render");
    const bindStatus = page.document.querySelector('select[name="bind_status"]');
    bindStatus.value = "bound";
    bindStatus.dispatchEvent(new page.Event("change", { bubbles: true }));
    await waitFor(
      () => page.document.body.textContent.includes("当前显示上次读取结果"),
      "failed follow-up groups read did not identify retained rows",
    );
    assert(
      page.document.body.textContent.includes("已读取群"),
      "failed follow-up read must retain the previous rows",
    );
  } finally {
    journey.window.close();
  }
}

await assertGroupsReadFailureJourney({ hasPreviousRows: false });
await assertGroupsReadFailureJourney({ hasPreviousRows: true });
console.log("groupops-groups-read-failure-dom: PASS");
