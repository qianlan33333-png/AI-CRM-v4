import { JSDOM } from "jsdom";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const here = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(here, "../../../..");
const template = fs.readFileSync(path.join(root, "internal", "webshell", "templates", "admin_audience_detail.html"), "utf8")
  .replace(/^\{\{define "admin_audience_detail"\}\}/, "")
  .replace(/\{\{end\}\}\s*$/, "");
const adapter = fs.readFileSync(path.join(here, "admin_audience_detail.js"), "utf8");
const wait = (milliseconds = 120) => new Promise((resolve) => setTimeout(resolve, milliseconds));
const json = (body, status = 200) => ({ ok: status >= 200 && status < 300, status, json: async () => body });
const writes = [];
let pickerOptions = null;

const dom = new JSDOM(`<!doctype html><html><body>${template}</body></html>`, {
  url: "https://test.invalid/admin/automation-conversion/packages/12",
  runScripts: "outside-only",
  pretendToBeVisual: true,
  beforeParse(window) {
    window.Headers = globalThis.Headers;
    window.AdminFmt = { localTime: (value) => value || "" };
    window.OperationMemberPicker = { open: (options) => { pickerOptions = options; } };
    window.fetch = async (input, init = {}) => {
      const url = new URL(String(input), window.location.origin);
      const method = init.method || "GET";
      if (url.pathname === "/api/admin/ai-audience/packages/12" && method === "GET") return json({ package: { id: 12, name: "运行中人群包", lifecycle: "active", membership_mode: "recommendation", version: 3, member_count: 1 } });
      if (url.pathname === "/api/admin/ai-audience/package-groups") return json({ items: [] });
      if (url.pathname === "/api/admin/ai-audience/templates") return json({ items: [] });
      if (url.pathname === "/api/admin/automation-agents") return json({ items: [] });
      if (url.pathname.endsWith("/configuration") || url.pathname.endsWith("/automation-binding")) return json({ error: "not_found" }, 404);
      if (url.pathname.endsWith("/senders") && method === "GET") return json({ sender_set: { members: [] } });
      if (url.pathname.endsWith("/senders") && method === "PUT") { writes.push(JSON.parse(init.body)); return json({ sender_set: { members: [] } }); }
      if (url.pathname.endsWith("/members")) return json({ snapshot: { member_count: 1 }, items: [] });
      if (url.pathname.endsWith("/precheck")) return json({ precheck: { ready: false, reasons: [] } });
      if (url.pathname.endsWith("/direct-push") && method === "GET") return json({ data: { enabled: false, max_per_customer_24h: 1, version: 0, client_id: "aicrm-audience-direct-push", webhook_path: "" } });
      if (url.pathname.endsWith("/direct-push") && method === "PUT") {
        const body = JSON.parse(init.body);
        writes.push(body);
        return json({ data: { enabled: body.enabled, max_per_customer_24h: body.max_per_customer_24h, version: 1, client_id: "aicrm-audience-direct-push", webhook_path: "/api/automation/audience/webhooks/awh_test" } });
      }
      return json({ error: `unexpected ${method} ${url.pathname}` }, 500);
    };
  },
});

dom.window.eval(adapter);
await wait(300);
const document = dom.window.document;
for (const id of ["directPushEnabled", "directPushLimit", "directPushPath", "saveDirectPushBtn"]) {
  if (document.querySelector(`#${id}`)?.disabled) throw new Error(`${id} stayed disabled for an active package`);
}
if (!document.querySelector("#packageDefinitionInput")?.disabled) throw new Error("active audience definition unexpectedly became editable");
if (document.querySelector("#addSenderBtn")?.disabled || document.querySelector("#saveSendersBtn")?.disabled !== true) throw new Error("sender whitelist controls did not remain in the expected editable state");
document.querySelector("#addSenderBtn").click();
if (!pickerOptions || pickerOptions.title !== "选择企微客服" || pickerOptions.scope !== "audience_senders") throw new Error("shared WeCom member picker was not opened for audience senders");
pickerOptions.onConfirm([{ user_id: "QianLan", display_name: "QianLan" }]);
document.querySelector("#saveSendersBtn").click();
await wait(160);
const senderWrite = writes.find((item) => Array.isArray(item.provider_member_references));
if (!senderWrite || senderWrite.provider_member_references.join(",") !== "QianLan") throw new Error(`sender whitelist save did not submit selected userid: ${JSON.stringify(writes)}`);
document.querySelector("#directPushEnabled").checked = true;
document.querySelector("#directPushLimit").value = "2";
document.querySelector("#saveDirectPushBtn").click();
await wait(160);
const directWrite = writes.find((item) => Object.prototype.hasOwnProperty.call(item, "enabled"));
if (!directWrite || directWrite.enabled !== true || directWrite.max_per_customer_24h !== 2 || directWrite.expected_version !== 0) throw new Error(`direct push save did not preserve the independent command: ${JSON.stringify(writes)}`);
if (document.querySelector("#directPushPath").value !== "/api/automation/audience/webhooks/awh_test") throw new Error("saved webhook path was not rendered");

dom.window.close();
console.log("admin-audience-direct-push-controls: PASS");
