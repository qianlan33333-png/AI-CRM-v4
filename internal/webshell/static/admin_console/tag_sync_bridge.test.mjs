import { JSDOM } from "jsdom";
import { readFileSync } from "node:fs";
import assert from "node:assert/strict";
import { build } from "esbuild";
import { fileURLToPath } from "node:url";
const feedback = await build({
  entryPoints: [
    fileURLToPath(
      new URL("../../../../web/src/shared/ui/feedback.ts", import.meta.url),
    ),
  ],
  bundle: true,
  format: "iife",
  globalName: "tagFeedback",
  platform: "browser",
  write: false,
});
const code = readFileSync(
  new URL("./tag_sync_bridge.js", import.meta.url),
  "utf8",
);
assert.match(code, /标签同步未完成（\$\{tagSyncStateLabel\(sync\.state\)\}）/);
assert.match(code, /notice\("已完成核对，请查看结果。", true\)/);
assert.match(code, /final_failed: "执行失败"/);
const dom = new JSDOM(
  `<main id="stage"><button data-tag-group-card aria-pressed="true"><span>Group</span></button><button type="button">同步企微标签</button><table><tr><td>Known</td><td><button>复制 tag_id</button></td></tr><tr><td>Pending</td><td><button>复制 tag_id</button></td></tr></table><div><span>tag_id</span><span><code>22</code><button>复制</button></span></div></main>`,
  { url: "https://test.invalid/admin/wecom-tags", runScripts: "outside-only" },
);
const { window } = dom;
const copies = [];
const retries = [];
let generation = 1;
let retryState = "queued";
let syncState = { state: "idle", active: false };
Object.defineProperty(window.navigator, "clipboard", {
  value: { writeText: async (text) => copies.push(text) },
});
window.fetch = async (url, options) => {
  if (String(url).endsWith("/retry")) {
    retries.push(options);
    return {
      ok: true,
      json: async () => ({ ok: true, effect_state: retryState }),
    };
  }
  if (String(url).endsWith("/sync-status"))
    return {
      ok: true,
      json: async () => ({ sync: syncState }),
    };
  return {
    ok: true,
    json: async () => ({
      groups: [{ group_id: 11, group_name: "Group" }],
      tags: [
        {
          tag_id: 22,
          group_id: 11,
          tag_name: "Known",
          provider_tag_id: "provider-real-tag",
        },
        { tag_id: 23, group_id: 11, tag_name: "Pending", provider_tag_id: "" },
      ],
      mutation_recoveries: [
        {
          id: 3,
          generation,
          operation: "tag_create",
          name: "Pending",
          state: "final_failed",
        },
        {
          id: 4,
          operation: "tag_create",
          name: "Unknown",
          state: "outcome_unknown",
        },
      ],
    }),
  };
};
// Real donor runtime marks its own controls; install the real capture-layer
// feedback first, just as the production Admin entry does before the Host.
for (const button of window.document.querySelectorAll("button"))
  button.__dcBound = true;
window.eval(feedback.outputFiles[0].text + "\ntagFeedback.initFeedback();");
window.eval(code);
window.document.dispatchEvent(new window.Event("DOMContentLoaded"));
await new Promise((resolve) => setTimeout(resolve, 30));
const detail = window.document.querySelector("#stage code");
assert.equal(detail.textContent, "provider-real-tag");
assert.equal(detail.dataset.localTagId, "22");
const buttons = window.document.querySelectorAll("tr button");
buttons[0].click();
await Promise.resolve();
assert.deepEqual(copies, ["provider-real-tag"]);
buttons[1].click();
await Promise.resolve();
assert.equal(copies.length, 1, "unbound local ID must never be copied");
[...window.document.querySelectorAll("button")]
  .find((x) => x.textContent === "复制")
  .click();
await Promise.resolve();
assert.deepEqual(copies, ["provider-real-tag", "provider-real-tag"]);
// A newly opened unbound detail must not display/copy the local command ID.
const detailParent = detail.parentElement.parentElement;
detailParent.innerHTML =
  "<span>tag_id</span><span><code>23</code><button>复制</button></span>";
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(detailParent.querySelector("code").dataset.localTagId, "23");
assert.match(detailParent.querySelector("code").textContent, /未同步/);
detailParent.querySelector("button").__dcBound = true;
detailParent.querySelector("button").click();
await Promise.resolve();
assert.equal(copies.length, 2);
// Reopening the bound detail is painted by the same observer and still copies
// its real Provider ID, never the rendered label or the local command key.
detailParent.innerHTML =
  "<span>tag_id</span><span><code>22</code><button>复制</button></span>";
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(
  detailParent.querySelector("code").textContent,
  "provider-real-tag",
);
detailParent.querySelector("button").__dcBound = true;
detailParent.querySelector("button").click();
await Promise.resolve();
assert.deepEqual(copies, Array(3).fill("provider-real-tag"));
const recover = window.document.querySelectorAll(
  "[data-tag-mutation-recovery] button",
);
assert.equal(recover.length, 1, "unknown outcome must not have retry");
window.document.cookie = "aicrm_admin_csrf=csrf-value";
recover[0].click();
await new Promise((resolve) => setTimeout(resolve, 20));
recover[0].click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(retries.length, 2);
assert.equal(
  recover[0].__dcBound,
  true,
  "Host action must register with real donor feedback",
);
assert.equal(recover[0].dataset.capabilityState, "real");
assert.ok(
  !window.document
    .querySelector("#fb-toast")
    ?.textContent.includes("后端能力未就绪"),
  "real feedback must not report the retry as blocked",
);
assert.equal(
  retries[0].headers["Idempotency-Key"],
  retries[1].headers["Idempotency-Key"],
);
assert.equal(retries[0].headers["X-CSRF-Token"], "csrf-value");
assert.equal(
  window.document.querySelectorAll("[data-tag-mutation-recovery] button")
    .length,
  1,
);
// A completed retry may fail definitively again. That new attempt generation
// gets a new command key, while stale reads of the old generation retain the
// original key and cannot cause a second enqueue.
generation = 2;
window.document
  .getElementById("stage")
  .appendChild(window.document.createComment("new failed generation"));
await new Promise((resolve) => setTimeout(resolve, 650));
window.document.querySelector("[data-tag-mutation-recovery] button").click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(retries.length, 3);
assert.notEqual(
  retries[2].headers["Idempotency-Key"],
  retries[0].headers["Idempotency-Key"],
);
generation = 1;
window.document
  .getElementById("stage")
  .appendChild(window.document.createComment("stale read"));
await new Promise((resolve) => setTimeout(resolve, 650));
window.document.querySelector("[data-tag-mutation-recovery] button").click();
await new Promise((resolve) => setTimeout(resolve, 20));
assert.equal(
  retries[3].headers["Idempotency-Key"],
  retries[0].headers["Idempotency-Key"],
);
for (const [state, label] of Object.entries({
  executed: "原企微任务已完成",
  outcome_unknown: "原企微任务结果待核对，请勿重复创建",
  final_failed: "原企微任务尚未完成，请查看当前失败状态",
})) {
  retryState = state;
  window.document.querySelector("[data-tag-mutation-recovery] button").click();
  await new Promise((resolve) => setTimeout(resolve, 20));
  const notices = [
    ...window.document.querySelectorAll('[role="status"],[role="alert"]'),
  ];
  assert.equal(
    notices.at(-1).textContent,
    label,
    "replayed terminal state must not claim a new queued write",
  );
}
dom.window.close();

let reconciledState = { state: "queued", active: true, receipt_id: 17 };
const reconciledDom = new JSDOM(
  `<main id="stage"><button type="button">同步企微标签</button></main>`,
  { url: "https://test.invalid/admin/wecom-tags", runScripts: "outside-only" },
);
reconciledDom.window.fetch = async (url) => {
  if (String(url).endsWith("/sync-status")) return { ok: true, json: async () => ({ sync: reconciledState }) };
  return { ok: true, json: async () => ({ groups: [], tags: [], mutation_recoveries: [] }) };
};
reconciledDom.window.eval(code);
reconciledDom.window.document.dispatchEvent(new reconciledDom.window.Event("DOMContentLoaded"));
await new Promise((resolve) => setTimeout(resolve, 30));
reconciledState = { state: "reconciled", active: false, receipt_id: 17 };
await new Promise((resolve) => setTimeout(resolve, 900));
const reconciledNotice = reconciledDom.window.document.querySelector('[role="alert"]');
assert.equal(reconciledNotice?.textContent, "已完成核对，请查看结果。", "reconciled sync must not be described as unfinished or successful");
reconciledDom.window.close();
console.log("tag Host Provider ID / explicit safe recovery: PASS");
