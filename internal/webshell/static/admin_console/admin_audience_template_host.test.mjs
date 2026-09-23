import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience_detail.html"), "utf8")
  .replace(/^\{\{define "admin_audience_detail"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const frozenController = fs.readFileSync(path.join(here, "template_parameter_form.js"), "utf8");
const detail = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const host = fs.readFileSync(path.join(here, "admin_audience_template_host.js"), "utf8");
const wait = (milliseconds = 100) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const ownerFields = [
  { name: "owner_scope", label: "负责人范围", type: "enum", required: true, enum: ["specified", "all"], default: "all" },
  { name: "owner_userids", label: "负责人 UserID", type: "string_list", default: [], visible_when: { owner_scope: "specified" } },
];
const templates = [
  { key: "wecom_contact_registration", label: "企微联系人与注册状态", template_version: 1, available: true, fields: [...ownerFields, { name: "contact_statuses", label: "联系人状态", type: "enum_list", required: true, enum: ["active", "deleted"], default: ["active"] }, { name: "registration_status", label: "注册状态", type: "enum", required: true, enum: ["any", "registered", "unregistered"], default: "any" }] },
  { key: "questionnaire_choice_answers", label: "问卷选择题答案", template_version: 1, available: true, fields: [{ name: "questionnaire", label: "问卷", type: "reference", required: true }, { name: "conditions", label: "题目条件", type: "condition_list", required: true, min_items: 1 }, ...ownerFields] },
  { key: "paid_order", label: "已支付订单", template_version: 1, available: true, fields: [{ name: "products", label: "商品", type: "reference_list", reference: "product", required: true }, { name: "paid_at_from", label: "支付时间起点", type: "datetime" }, { name: "paid_at_to", label: "支付时间终点（不含）", type: "datetime" }, ...ownerFields, { name: "require_active_wecom_contact", label: "要求有效企微联系人", type: "boolean", default: true }] },
  { key: "channel_entry", label: "渠道进入", template_version: 1, available: true, fields: [{ name: "channels", label: "渠道", type: "reference_list", reference: "channel", required: true }, { name: "entered_days_min", label: "最少天数", type: "integer", default: 0 }, { name: "entered_days_max", label: "最大天数", type: "integer" }, ...ownerFields, { name: "require_active_wecom_contact", label: "要求有效企微联系人", type: "boolean", default: true }] },
  { key: "radar_first_click_elapsed", label: "雷达首次点击距今", template_version: 1, available: true, fields: [{ name: "radars", label: "雷达", type: "reference_list", reference: "radar", required: true }, { name: "elapsed_min", label: "最小经过时间", type: "integer", default: 0 }, { name: "elapsed_max", label: "最大经过时间", type: "integer" }, { name: "elapsed_unit", label: "时间单位", type: "enum", enum: ["hour", "day"], default: "day" }, ...ownerFields] },
  { key: "member_usage_status", label: "会员与真实使用状态", template_version: 1, available: true, fields: [...ownerFields, { name: "service_period", label: "服务期", type: "enum", enum: ["any", "active", "expired"], default: "active" }, { name: "registration_status", label: "注册状态", type: "enum", enum: ["any", "registered", "unregistered"], default: "any" }, { name: "usage_status", label: "真实使用状态", type: "enum", enum: ["any", "used", "unused"], default: "any" }, { name: "membership_tiers", label: "会员层级", type: "string_list", default: [] }, { name: "membership_statuses", label: "会员状态", type: "string_list", default: [] }] },
];
templates.push({ key: "questionnaire_submissions", label: "问卷提交", template_version: 1, available: true, fields: [{name: "questionnaires", label: "问卷", type: "reference_list", reference: "questionnaire", required: true}, ...ownerFields, {name: "require_wecom_identity", label: "要求已识别企微身份", type: "boolean", default: true}] });
let config = { id: 4, package_id: 13, version: 1, refresh_cron_utc: "0 1 * * *", refresh_mode: "legacy_custom", definition: { schema_version: 1, template_key: "wecom_contact_registration", parameters: { owner_scope: "all", owner_staff_ids: [], contact_statuses: ["active"], registration_status: "any" } } };
let packageVersion = 3;
const writes = [];
const previewWrites = [];
const packageWrites = [];
const policyWrites = [];
const confirmations = [];
const policyArchives = [];
const bindingDeletes = [];
let templateReads = 0;
let broadcastRuns = [];
let broadcastPreviewCalls = 0;
let broadcastConfirmCalls = 0;
let previewFailure = false;
const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion/packages/13",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.AICRMConfirmation = { confirm: (options) => new Promise((resolve) => confirmations.push({ options, resolve })) };
    window.structuredClone = globalThis.structuredClone;
    window.AdminDateTime = {
      datetimeLocalValue: (value) => value === "2026-09-05T00:00:00.000Z" || value === "2026-09-05T00:00:00.611265Z" ? "2026-09-05T08:00:00" : value === "2026-09-05T01:00:00.000Z" || value === "2026-09-05T01:00:00.125Z" ? "2026-09-05T09:00:00" : "",
      shanghaiDateTimeLocalToRFC3339: (value) => value === "2026-09-05T08:00" || value === "2026-09-05T08:00:00" ? "2026-09-05T00:00:00.000Z" : value === "2026-09-05T09:00" || value === "2026-09-05T09:00:00" ? "2026-09-05T01:00:00.000Z" : undefined,
    };
    window.AdminFmt = { localTime: () => "2026-09-05 20:00:00", whenAdminDateTimeReady: (ready) => ready(window.AdminDateTime) };
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      if (url.pathname === "/api/admin/ai-audience/packages/13" && (!init.method || init.method === "GET")) return json({ package: { id: 13, name: "原人群", code: "legacy-audience", version: packageVersion, lifecycle: "paused" } });
      if (url.pathname === "/api/admin/ai-audience/package-groups") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/templates") {
        templateReads += 1;
        // bootDetail starts first. Hold only that request so the Host mounts
        // before the legacy renderer finishes and the MutationObserver has to
        // restore the frozen form without relying on a timeout race.
        if (templateReads === 1) await new Promise((resolve) => window.setTimeout(resolve, 180));
        return json({ items: templates });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/configuration" && (!init.method || init.method === "GET")) return json({ configuration: config });
      if (url.pathname === "/api/admin/ai-audience/packages/13/owner-references") return json({ owner_userids: ["bob"] });
      if (url.pathname === "/api/admin/ai-audience/packages/13/automation-binding" && (!init.method || init.method === "GET")) return json({ binding: { agent_id: 8 } });
      if (url.pathname === "/api/admin/ai-audience/packages/13/automation-binding" && init.method === "DELETE") {
        bindingDeletes.push({ body: init.body, headers: Object.fromEntries(new window.Headers(init.headers || {})) });
        return bindingDeletes.length === 1 ? json({ error: "not_ready" }, 503) : json({});
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/senders" || url.pathname === "/api/admin/ai-audience/packages/13/members") return json({ error: "not_found" }, 404);
      if (url.pathname === "/api/admin/automation-agents") return json({ items: [{ id: 8, agent_name: "冻结话术", automation_type: "fixed_script", status: "active" }] });
      if (url.pathname === "/api/admin/ai-audience/packages/13/direct-push" && (!init.method || init.method === "GET")) return json({ data: { enabled: true, max_per_customer_24h: 1, version: 1, client_id: "aicrm-audience-direct-push", webhook_path: "/api/automation/audience/webhooks/test-reference" } });
      if (url.pathname === "/api/admin/ai-audience/packages/13/precheck") return json({ precheck: { ready: false, reasons: [] } });
      if (url.pathname === "/api/admin/ai-audience/packages/13/refresh" && init.method === "POST") return json({ refresh_run: { id: 71, state: "queued" } }, 202);
      if (url.pathname === "/api/admin/ai-audience/packages/13/refresh-runs/71") return json({ refresh_run: { id: 71, state: "failed", error_code: "refresh_unavailable" } });
      if (url.pathname === "/api/admin/ai-audience/packages/13" && init.method === "PATCH") {
        const body = JSON.parse(init.body);
        packageWrites.push(body);
        packageVersion += 1;
        return json({ package: { id: 13, name: body.name, code: "legacy-audience", version: packageVersion, lifecycle: "paused" } });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/configuration" && init.method === "PUT") {
        const body = JSON.parse(init.body);
        writes.push(body);
        const parameters = { ...body.definition.parameters };
        if (body.definition.template_key === "paid_order" && parameters.paid_at_from === "2026-09-05T00:00:00.000Z" && parameters.paid_at_to === "2026-09-05T01:00:00.000Z") {
          // The storage contract may retain sub-second source precision even
          // though the editable control deliberately displays whole seconds.
          parameters.paid_at_from = "2026-09-05T00:00:00.611265Z";
          parameters.paid_at_to = "2026-09-05T01:00:00.125Z";
        }
        parameters.owner_staff_ids = parameters.owner_scope === "specified" ? ["9"] : [];
        delete parameters.owner_userids;
        config = { ...config, version: config.version + 1, refresh_cron_utc: body.refresh_cron_utc, refresh_mode: body.refresh_mode, definition: { ...body.definition, parameters } };
        return json({ configuration: config });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/preview") {
        if (previewFailure) return json({ error: "provider_unavailable（上游服务错误）" }, 503);
        const body = JSON.parse(init.body);
        previewWrites.push(body);
        return json({ preview: { member_count: 1, member_digest: "member", watermark_digest: "watermark" } });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/broadcast-previews" && init.method === "POST") {
        broadcastPreviewCalls += 1;
        return json({ snapshot_id: 77, target_count: 2, skipped_count: 0, agent_id: 8, agent_published_version: 3, expected_package_version: packageVersion, preview_digest: "a".repeat(64) });
      }
      if (url.pathname === "/api/admin/ai-audience/packages/13/runs" && init.method === "POST") {
        broadcastConfirmCalls += 1;
        broadcastRuns = [{ id: 91, package_id: 13, state: "pending_review", ai_plan_id: 44, ai_plan_state: "pending_review", target_count: 2, skipped_count: 0, outcome_unknown_count: 0, created_at: "2026-09-05T12:00:00Z" }];
        return json({ run: broadcastRuns[0] });
      }
      if (url.pathname === "/api/admin/automation-runs/92/generation-items") return json({ items: [
        { id: 1, customer_id: 101, sender_staff_id: 8, effect_id: "eer_201", state: "executed" },
        { id: 2, customer_id: 102, sender_staff_id: 8, effect_id: "eer_202", state: "final_failed", failure_code: "generation_response_invalid" },
        { id: 3, customer_id: 103, sender_staff_id: 8, effect_id: "eer_203", state: "outcome_unknown", failure_code: "generation_call_unknown" },
      ] });
      if (url.pathname === "/api/admin/automation-runs" && (!init.method || init.method === "GET")) return json({ items: broadcastRuns, next_cursor: "" });
      if (url.pathname === "/api/admin/automations" && init.method === "POST") {
        policyWrites.push(JSON.parse(init.body));
        return json({ policy: { id: 31 } });
      }
      if (url.pathname === "/api/admin/automations" && (!init.method || init.method === "GET")) return json({ items: [{ id: 31 }] });
      if (url.pathname === "/api/admin/automations/31") return json({ data: { policy: { id: 31, name: "留存策略", code: "retention", lifecycle: "paused", version: 9 }, version: { package_id: 13, version: 3, trigger_kind: "audience_member_entered", action_kind: "record" } } });
      if (url.pathname === "/api/admin/automations/31/archive" && init.method === "POST") {
        policyArchives.push({ body: init.body, headers: Object.fromEntries(new window.Headers(init.headers || {})) });
        return policyArchives.length === 1 ? json({ error: "not_ready" }, 503) : json({});
      }
      return json({ error: `unexpected ${url.pathname}` }, 500);
    };
  },
});

dom.window.eval(detail);
dom.window.eval(frozenController);
dom.window.eval(host);
await wait(350);
const document = dom.window.document;
const select = document.querySelector("#templateSelect");
if (templateReads < 3 || select.options.length !== 7 || !document.querySelector("#templateParameterForm [data-field-name]")) throw new Error("frozen renderer and V3 submission template were not restored after the delayed detail renderer");
if (!select.options[0].textContent.includes("企微联系人与注册状态 · 第 1 版") || select.options[0].textContent.includes("v1")) throw new Error(`template version label was not localized: ${select.options[0].textContent}`);
const ownerScopeLabels = [...document.querySelectorAll('[data-field-name="owner_scope"] option')].map((option) => option.textContent).join("/");
const contactStatusLabels = [...document.querySelectorAll('[data-field-name="contact_statuses"] option')].map((option) => option.textContent).join("/");
const registrationLabels = [...document.querySelectorAll('[data-field-name="registration_status"] option')].map((option) => option.textContent).join("/");
if (ownerScopeLabels !== "指定负责人/全部负责人" || contactStatusLabels !== "有效/已删除" || registrationLabels !== "不限/已注册/未注册") throw new Error(`template enum labels leaked protocol values: ${JSON.stringify({ownerScopeLabels, contactStatusLabels, registrationLabels})}`);
const initialOwnerScope = document.querySelector('[data-field-name="owner_scope"] select');
initialOwnerScope.value = "specified";
initialOwnerScope.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
if (!document.querySelector('[data-field-name="owner_userids"] label')?.textContent.includes("负责人标识")) throw new Error("owner identifier field leaked UserID wording");
initialOwnerScope.value = "all";
initialOwnerScope.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
if (document.querySelector("#dailySelect").value !== "off" || !document.querySelector("#summaryMode").textContent.includes("每日 09:00") || !document.querySelector("#refreshScheduleNote").textContent.includes("保留原规则") || document.querySelector("#refreshScheduleNote").textContent.includes("上海时间")) throw new Error("legacy custom schedule was not presented as its actual business time");
// The frozen renderer can finish a later asynchronous configuration read.
// Its old cron projection must not overwrite the V3-owned business schedule
// or make the two "off" selects look like a manual configuration.
document.querySelector("#summaryMode").textContent = "计划 0 1 * * *";
document.querySelector("#dailySelect").value = "daily_0200";
await wait(20);
if (document.querySelector("#dailySelect").value !== "off" || document.querySelector("#summaryMode").textContent !== "每日 09:00（历史自定义计划）") throw new Error("late frozen schedule render replaced the V3-owned legacy presentation");
if (document.querySelector("#policyTimezoneInput")) throw new Error("new quiet-hours must not expose a timezone field in the business UI");
document.querySelector("#savePackageBtn").click();
await wait(180);
if (writes.length !== 1 || writes[0]?.refresh_mode !== "legacy_custom" || writes[0]?.refresh_cron_utc !== "0 1 * * *") throw new Error(`the Host did not exclusively preserve the old save path: ${JSON.stringify(writes)}`);
const manualRefreshButton = document.querySelector("#replaceLegacyScheduleWithManualBtn");
if (!manualRefreshButton || manualRefreshButton.hidden) throw new Error("legacy custom schedule did not offer an explicit manual-refresh replacement");
manualRefreshButton.click();
if (!document.querySelector("#refreshScheduleNote").textContent.includes("已选择改为手动刷新")) throw new Error("manual refresh replacement was not made explicit before saving");
document.querySelector("#summaryMode").textContent = "计划 0 1 * * *";
document.querySelector("#dailySelect").value = "daily_0200";
await wait(20);
if (document.querySelector("#dailySelect").value !== "off" || document.querySelector("#summaryMode").textContent !== "手动") throw new Error("late frozen render overwrote the manual-refresh draft");
document.querySelector("#savePackageBtn").click();
await wait(180);
if (writes.length !== 2 || writes[1]?.refresh_mode !== "manual" || writes[1]?.refresh_cron_utc !== "") throw new Error(`the explicit manual-refresh replacement did not write manual with an empty cron: ${JSON.stringify(writes)}`);
document.querySelector("#dailySelect").value = "daily_0200";
document.querySelector("#dailySelect").dispatchEvent(new dom.window.Event("change", { bubbles: true }));
document.querySelector("#summaryMode").textContent = "计划 0 1 * * *";
document.querySelector("#dailySelect").value = "off";
await wait(20);
if (document.querySelector("#dailySelect").value !== "daily_0200" || document.querySelector("#summaryMode").textContent !== "每日 02:00") throw new Error("late frozen render overwrote the daily-refresh draft");
document.querySelector("#savePackageBtn").click();
await wait(180);
if (writes.length !== 3 || writes[2]?.refresh_mode !== "daily_0200" || writes[2]?.refresh_cron_utc !== "") throw new Error(`the daily-refresh draft was not saved as a refresh mode: ${JSON.stringify(writes)}`);
for (const template of templates) {
  select.value = template.key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  if (!document.querySelector("#templateParameterForm [data-field-name]")) throw new Error(`form did not render for ${template.key}`);
}
const fieldInput = (name) => document.querySelector(`[data-field-name="${name}"] input, [data-field-name="${name}"] textarea, [data-field-name="${name}"] select`);
const setSpecifiedOwner = () => {
  const ownerScope = fieldInput("owner_scope");
  ownerScope.value = "specified";
  ownerScope.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  fieldInput("owner_userids").value = "bob";
};
const reOpenTemplate = async (key) => {
  const alternate = templates.find((item) => item.key !== key);
  select.value = alternate.key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  select.value = key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
};
const saveTemplate = async (key, setValues, verifyWrite, verifyReopened) => {
  select.value = key;
  select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
  await wait(20);
  setValues();
  document.querySelector("#templatePreviewBtn").click();
  await wait(180);
  verifyWrite(previewWrites.at(-1).definition);
  if (!document.querySelector("#templatePreviewBox").textContent.includes("1 人")) throw new Error(`preview did not use ${key}`);
  document.querySelector("#templateSaveBtn").click();
  await wait(180);
  verifyWrite(writes.at(-1).definition);
  await reOpenTemplate(key);
  verifyReopened();
};
await saveTemplate("wecom_contact_registration", () => {
  const statuses = fieldInput("contact_statuses");
  statuses.options[0].selected = true;
  document.querySelector("#packageNameInput").value = "已更新的人群";
  document.querySelector("#dailySelect").value = "daily_0200";
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "wecom_contact_registration" || parameters.owner_scope !== "all" || parameters.owner_userids.length !== 0 || parameters.contact_statuses.join(",") !== "active" || parameters.registration_status !== "any") throw new Error(`WeCom parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("owner_scope").value !== "all" || !fieldInput("contact_statuses").options[0].selected) throw new Error("WeCom all-scope values did not reopen");
});
if (packageWrites[3]?.name !== "已更新的人群" || writes[3]?.refresh_mode !== "daily_0200" || writes[3]?.refresh_cron_utc !== "") throw new Error(`template save bypassed basic configuration or refresh mode: ${JSON.stringify({ packageWrites, writes })}`);
await saveTemplate("paid_order", () => {
  fieldInput("products").value = "course-v3";
  fieldInput("paid_at_from").value = "2026-09-05T08:00";
  fieldInput("paid_at_to").value = "2026-09-05T09:00";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "paid_order" || parameters.product_codes.join(",") !== "course-v3" || parameters.paid_at_from !== "2026-09-05T00:00:00.000Z" || parameters.paid_at_to !== "2026-09-05T01:00:00.000Z" || parameters.owner_userids.join(",") !== "bob") throw new Error(`paid parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("products").value !== "course-v3" || fieldInput("paid_at_from").value !== "2026-09-05T08:00" || fieldInput("paid_at_to").value !== "2026-09-05T09:00" || fieldInput("owner_userids").value !== "bob") throw new Error("paid references, Shanghai datetimes, or owner did not reopen");
});
// A datetime-local control displays whole seconds.  The stored response above
// carries fractional RFC3339 precision, so saving another condition must keep
// that existing instant byte-for-byte when its visible Shanghai value remains
// unchanged.
select.value = "paid_order";
select.dispatchEvent(new dom.window.Event("change", { bubbles: true }));
await wait(20);
if (fieldInput("paid_at_from").value !== "2026-09-05T08:00" || fieldInput("paid_at_to").value !== "2026-09-05T09:00") throw new Error("fractional paid datetimes did not reopen as Shanghai wall-clock values");
fieldInput("require_active_wecom_contact").checked = false;
document.querySelector("#templatePreviewBtn").click();
await wait(180);
if (previewWrites.at(-1)?.definition?.parameters?.paid_at_from !== "2026-09-05T00:00:00.611265Z" || previewWrites.at(-1)?.definition?.parameters?.paid_at_to !== "2026-09-05T01:00:00.125Z") throw new Error(`unchanged paid datetime preview lost fractional precision: ${JSON.stringify(previewWrites.at(-1))}`);
document.querySelector("#templateSaveBtn").click();
await wait(180);
if (writes.at(-1)?.definition?.parameters?.paid_at_from !== "2026-09-05T00:00:00.611265Z" || writes.at(-1)?.definition?.parameters?.paid_at_to !== "2026-09-05T01:00:00.125Z") throw new Error(`unchanged paid datetime save lost fractional precision: ${JSON.stringify(writes.at(-1))}`);
await saveTemplate("channel_entry", () => {
  fieldInput("channels").value = "渠道标题";
  fieldInput("entered_days_min").value = "2";
  fieldInput("entered_days_max").value = "3";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "channel_entry" || parameters.channel_codes.join(",") !== "渠道标题" || parameters.entered_days_min !== 2 || parameters.entered_days_max !== 3 || parameters.owner_userids.join(",") !== "bob") throw new Error(`channel parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("channels").value !== "渠道标题" || fieldInput("entered_days_min").value !== "2" || fieldInput("owner_userids").value !== "bob") throw new Error("channel references or owner did not reopen");
});
await saveTemplate("radar_first_click_elapsed", () => {
  fieldInput("radars").value = "雷达标题";
  fieldInput("elapsed_min").value = "3";
  fieldInput("elapsed_max").value = "4";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "radar_first_click_elapsed" || parameters.radar_ids.join(",") !== "雷达标题" || parameters.elapsed_min !== 3 || parameters.elapsed_max !== 4 || parameters.owner_userids.join(",") !== "bob") throw new Error(`radar parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("radars").value !== "雷达标题" || fieldInput("elapsed_max").value !== "4" || fieldInput("owner_userids").value !== "bob") throw new Error("radar references or owner did not reopen");
});
await saveTemplate("member_usage_status", () => {
  fieldInput("service_period").value = "expired";
  fieldInput("registration_status").value = "registered";
  fieldInput("usage_status").value = "used";
  fieldInput("membership_tiers").value = "pro";
  fieldInput("membership_statuses").value = "expired";
  document.querySelector("#incrementalSelect").value = "incremental_3m";
  document.querySelector("#dailySelect").value = "daily_0200";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "member_usage_status" || parameters.service_period !== "expired" || parameters.registration_status !== "registered" || parameters.usage_status !== "used" || parameters.membership_tiers.join(",") !== "pro" || parameters.membership_statuses.join(",") !== "expired" || parameters.owner_userids.join(",") !== "bob") throw new Error(`member parameters=${JSON.stringify(definition)}`);
}, () => {
  if (fieldInput("membership_tiers").value !== "pro" || fieldInput("membership_statuses").value !== "expired" || fieldInput("owner_userids").value !== "bob") throw new Error("member facts or owner did not reopen");
});
if (writes.find((item) => item.definition.template_key === "member_usage_status")?.refresh_mode !== "every_3m_plus_daily_0200") throw new Error(`combined refresh mode was downgraded: ${JSON.stringify(writes)}`);
const savedKeys = writes.map((item) => item.definition.template_key);
for (const key of ["wecom_contact_registration", "paid_order", "channel_entry", "radar_first_click_elapsed", "member_usage_status"]) {
  if (!savedKeys.includes(key)) throw new Error(`save did not use frozen form for ${key}: ${JSON.stringify(savedKeys)}`);
}
await saveTemplate("questionnaire_submissions", () => {
  fieldInput("questionnaires").value = "5\n7";
  fieldInput("require_wecom_identity").checked = true;
}, (definition) => {
  if (definition.template_key !== "questionnaire_submissions" || definition.parameters.questionnaire_ids.join(",") !== "5,7" || definition.parameters.require_wecom_identity !== true || "questionnaires" in definition.parameters) throw new Error("V3 host did not canonicalize submission references");
}, () => {
  if (fieldInput("questionnaires").value !== "5\n7" || !fieldInput("require_wecom_identity").checked) throw new Error("V3 host did not rehydrate submission references using frozen renderer");
});
await saveTemplate("questionnaire_choice_answers", () => {
  fieldInput("questionnaire").value = "客户调研";
  const conditionField = document.querySelector('[data-field-name="conditions"]');
  const first = conditionField.querySelector(".template-condition-row");
  first.querySelector("[data-condition-question]").value = "获客方式";
  first.querySelector("[data-condition-options]").value = "内容\n投放";
  conditionField.querySelector(".template-condition-list > button").click();
  const second = conditionField.querySelectorAll(".template-condition-row")[1];
  second.querySelector("[data-condition-question]").value = "成交方式";
  second.querySelector("[data-condition-options]").value = "咨询";
  setSpecifiedOwner();
}, (definition) => {
  const parameters = definition.parameters;
  if (definition.template_key !== "questionnaire_choice_answers" || parameters.owner_userids.join(",") !== "bob" || parameters.questionnaire_id !== "客户调研" || parameters.conditions.length !== 2 || parameters.conditions[0].question_id !== "获客方式" || parameters.conditions[0].option_ids.join(",") !== "内容,投放" || parameters.conditions[1].question_id !== "成交方式" || parameters.conditions[1].option_ids.join(",") !== "咨询") throw new Error(`questionnaire parameters=${JSON.stringify(definition)}`);
}, () => {
  const rows = document.querySelectorAll('[data-field-name="conditions"] .template-condition-row');
  if (fieldInput("questionnaire").value !== "客户调研" || rows.length !== 2 || rows[0].querySelector("[data-condition-options]").value !== "内容\n投放" || fieldInput("owner_userids").value !== "bob") throw new Error("questionnaire conditions or Access-backed owner did not reopen");
});
if (writes.length !== 11 || previewWrites.length !== 8 || packageWrites.length !== 11) throw new Error(`form save/preview contract incomplete: ${JSON.stringify({ saves: writes.length, previews: previewWrites.length, packages: packageWrites.length })}`);
document.querySelector("#policyCodeInput").value = "shanghai-quiet";
document.querySelector("#policyNameInput").value = "上海安静时段";
document.querySelector("#policyActionSelect").value = "record";
document.querySelector("#policyQuietHoursInput").value = "22:00-08:00";
document.querySelector("#createPolicyBtn").click();
await wait(180);
if (policyWrites.length !== 1 || policyWrites[0]?.quiet_hours?.timezone !== "Asia/Shanghai" || policyWrites[0]?.quiet_hours?.start !== "22:00" || policyWrites[0]?.quiet_hours?.end !== "08:00") throw new Error(`new quiet-hours did not use fixed Shanghai wall time: ${JSON.stringify(policyWrites)}`);
document.querySelector("#policyQuietHoursInput").value = "29:00-08:00";
document.querySelector("#createPolicyBtn").click();
await wait(40);
if (policyWrites.length !== 1) throw new Error("invalid quiet-hours input reached the automation write route");
// The frozen detail page owns this action: a user clicks the real preview and
// confirmation controls, then is taken to the existing AI review/recipients
// page instead of an Automation-only recipient drawer.
document.querySelector("#broadcastPreviewBtn").click();
await wait(180);
if (broadcastPreviewCalls !== 1 || document.querySelector("#broadcastConfirmBtn").disabled) throw new Error("manual broadcast preview did not enable confirmation");
document.querySelector("#broadcastConfirmBtn").click();
await wait(180);
if (broadcastConfirmCalls !== 1) throw new Error("manual broadcast confirmation did not create the AI-backed run");
const runRow = document.querySelector("#sendRecordRows tr");
const aiReviewLink = runRow?.querySelector('a[href="/admin/cloud-orchestrator/plans/44"]');
if (!aiReviewLink || !aiReviewLink.textContent.includes("AI 审阅与收件人") || runRow.querySelector("[data-run-id]")) throw new Error("manual run did not render the existing AI review and recipients handoff");
broadcastRuns = [{ id: 92, package_id: 13, state: "preparing", target_count: 3, skipped_count: 0, outcome_unknown_count: 1, created_at: "2026-09-05T12:00:00Z", generation: { total: 3, queued: 0, succeeded: 1, failed: 1, unknown: 1 } }];
document.querySelector('[data-panel="records"]').click();
await wait(180);
const dynamicProgress = document.querySelector("#sendRecordRows");
if (!dynamicProgress.textContent.includes("动态生成 3 项") || !dynamicProgress.textContent.includes("失败排除 1") || !dynamicProgress.textContent.includes("未知排除 1")) throw new Error("dynamic generation progress and exclusions were not rendered");
document.querySelector("[data-generation-run-id=\"92\"]").click();
await wait(180);
if (!document.querySelector("#sendRecordMeta").textContent.includes("生成结果无效") || !document.querySelector("#sendRecordMeta").textContent.includes("生成调用结果待核实") || document.querySelector("#sendRecordMeta").textContent.includes("generation_response_invalid") || document.querySelector("#sendRecordMeta").textContent.includes("generation_call_unknown") || !document.querySelector("#sendRecordContentDetail").textContent.includes("AI 审阅与收件人")) throw new Error("dynamic generation readback did not show durable exclusions and review handoff");
previewFailure = true;
document.querySelector("#templatePreviewBtn").click();
await wait(180);
const previewFailureText = document.querySelector("#templateStatusLine").textContent || "";
if (!previewFailureText.includes("人群配置服务暂不可用") || previewFailureText.includes("provider_unavailable") || previewFailureText.includes("上游服务错误")) throw new Error(`template request error leaked a technical message: ${previewFailureText}`);
document.querySelector('[data-panel="basic"]').click();
await wait(20);
document.querySelector("#manualRefreshBtn").click();
await wait(1700);
const refreshFailureText = document.querySelector("#capabilityStatus")?.textContent || "";
if (!refreshFailureText.includes("快照刷新失败：刷新服务暂不可用") || refreshFailureText.includes("refresh_unavailable")) throw new Error(`refresh failure leaked a raw error code: ${refreshFailureText}`);

// The shared dialog may resolve only a temporary choice.  These are the real
// detail-page bindings: cancel sends nothing; confirm preserves each Owner
// route's original CAS body and per-command idempotency behavior.
document.querySelector('[data-panel="automation"]').click();
await wait(40);
const unbind = () => document.querySelector("#unbindAutomationBtn")?.click();
unbind();
unbind();
await wait(20);
if (confirmations.length !== 1 || confirmations[0].options.title !== "解除话术智能体绑定" || !confirmations[0].options.description.includes("#8")) throw new Error(`unbind did not freeze its binding target: ${JSON.stringify(confirmations.map((entry) => entry.options))}`);
confirmations.shift().resolve({ confirmed: false });
await wait(40);
if (bindingDeletes.length) throw new Error("cancelled unbind made a mutation");
unbind();
await wait(20);
confirmations.shift().resolve({ confirmed: true });
await wait(100);
if (bindingDeletes.length !== 1 || bindingDeletes[0].body !== undefined || !bindingDeletes[0].headers["idempotency-key"] || !document.querySelector("#automationStatusLine")?.textContent.includes("能力尚未满足")) throw new Error(`unbind failure did not preserve its retryable Owner command: ${JSON.stringify({ bindingDeletes, status: document.querySelector("#automationStatusLine")?.textContent })}`);
unbind();
await wait(20);
confirmations.shift().resolve({ confirmed: true });
await wait(120);
if (bindingDeletes.length !== 2 || !bindingDeletes[1].headers["idempotency-key"]) throw new Error(`unbind retry did not submit exactly one original command: ${JSON.stringify(bindingDeletes)}`);

document.querySelector('[data-panel="policies"]').click();
await wait(100);
const archivePolicy = () => document.querySelector('[data-policy-action="archive"]')?.click();
archivePolicy();
archivePolicy();
await wait(20);
if (confirmations.length !== 1 || confirmations[0].options.title !== "归档触发策略" || !confirmations[0].options.description.includes("留存策略")) throw new Error(`policy archive did not freeze its visible target: ${JSON.stringify(confirmations.map((entry) => entry.options))}`);
confirmations.shift().resolve({ confirmed: false });
await wait(40);
if (policyArchives.length) throw new Error("cancelled policy archive made a mutation");
archivePolicy();
await wait(20);
confirmations.shift().resolve({ confirmed: true });
await wait(100);
if (policyArchives.length !== 1 || policyArchives[0].body !== JSON.stringify({ expected_version: 9 }) || !policyArchives[0].headers["idempotency-key"] || !document.querySelector("#policyStatusLine")?.textContent.includes("能力尚未满足")) throw new Error(`policy archive failure did not preserve its retryable Owner command: ${JSON.stringify({ policyArchives, status: document.querySelector("#policyStatusLine")?.textContent })}`);
archivePolicy();
await wait(20);
confirmations.shift().resolve({ confirmed: true });
await wait(120);
if (policyArchives.length !== 2 || !policyArchives[1].headers["idempotency-key"]) throw new Error(`policy archive retry did not submit exactly one original command: ${JSON.stringify(policyArchives)}`);
dom.window.close();
console.log("admin-audience-template-host-browser: PASS");
