import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";
const bundle = await buildTestBrowserBundle(
  fileURLToPath(new URL("./referralAdmin.ts", import.meta.url)),
);
const item = (id) => ({
  id,
  name: `活动${id}`,
  state: "active",
  effective_state: "active",
  team_mode: "individual",
  starts_at: "2026-10-10T08:00:00Z",
  ends_at: "2026-12-01T08:00:00Z",
  activity_url: `https://h5.example/referral?campaign=${id}`,
});
const reads = [];
let response = item(5),
  gate;
let copied = "",
  copyFailure = false;
const dom = new JSDOM('<main id="referral-admin-root"></main>', {
  url: "https://admin.example/admin/referral",
  pretendToBeVisual: true,
  runScripts: "outside-only",
  beforeParse(w) {
    w.Response = Response;
    w.Headers = Headers;
    w.URL = URL;
    w.AbortController = AbortController;
    w.HTMLDialogElement.prototype.showModal = function () {
      this.setAttribute("open", "");
    };
    w.HTMLDialogElement.prototype.close = function () {
      this.removeAttribute("open");
      this.dispatchEvent(new w.Event("close"));
    };
    Object.defineProperty(w.navigator, "clipboard", {
      value: {
        writeText: async (v) => {
          if (copyFailure) throw Error("denied");
          copied = v;
        },
      },
    });
    w.document.execCommand = () => false;
    w.fetch = async (input, init = {}) => {
      const url = new URL(input, w.location.href);
      reads.push({ url, method: init.method || "GET", signal: init.signal });
      if (url.pathname.endsWith("/campaigns"))
        return new Response(JSON.stringify({ items: [item(5), item(8)] }), {
          headers: { "Content-Type": "application/json" },
        });
      if (gate) await gate;
      if (response instanceof Error) throw response;
      if (typeof response === "number")
        return new Response(
          JSON.stringify({
            error: response === 403 ? "forbidden" : "unavailable",
          }),
          { status: response, headers: { "Content-Type": "application/json" } },
        );
      return new Response(JSON.stringify(response), {
        headers: { "Content-Type": "application/json" },
      });
    };
  },
});
const wait = async (check) => {
  for (let i = 0; i < 200; i++) {
    if (check()) return;
    await new Promise((r) => setTimeout(r, 5));
  }
  throw Error("share render timeout");
};
const doc = dom.window.document;
const dialog = () => doc.querySelector("dialog[data-referral-campaign-share]");
const share = (id) =>
  [...doc.querySelectorAll(".referral-admin-campaign")]
    .find((c) => c.querySelector("h3").textContent === `活动${id}`)
    .querySelectorAll("button")[1];
const button = (text) =>
  [...dialog().querySelectorAll("button")].find((b) => b.textContent === text);
const close = () => button("关闭").click();
dom.window.eval(bundle);
await wait(() => doc.querySelectorAll(".referral-admin-campaign").length === 2);
for (const card of doc.querySelectorAll(".referral-admin-campaign"))
  assert.deepEqual(
    [...card.querySelectorAll("button")].map((v) => v.textContent),
    ["查看活动详情", "分享"],
  );
let release;
gate = new Promise((resolve) => (release = resolve));
share(5).focus();
share(5).click();
await wait(() => dialog());
assert.match(dialog().textContent, /正在读取/);
assert.equal(dialog().querySelector("input"), null);
close();
assert.equal(doc.activeElement, share(5));
assert.equal(reads.at(-1).signal.aborted, true);
release();
gate = undefined;
await new Promise((r) => setTimeout(r, 10));
assert.equal(dialog(), null);
response = { ...item(5), name: "活动5最新" };
share(5).click();
await wait(() => dialog()?.querySelector("input"));
assert.equal(dialog().getAttribute("aria-label"), "分享活动 · 活动5最新");
assert.equal(
  dialog().querySelector("input").value,
  "https://h5.example/referral?campaign=5",
);
assert.equal(
  dialog().querySelector("[data-qr-payload]").dataset.qrPayload,
  "https://h5.example/referral?campaign=5",
);
assert.equal(
  dialog().querySelector("a").href,
  "https://h5.example/referral?campaign=5",
);
assert.equal(dom.window.location.pathname, "/admin/referral");
button("复制链接").click();
await wait(() => copied);
assert.equal(copied, "https://h5.example/referral?campaign=5");
copyFailure = true;
button("复制链接").click();
await wait(() => dialog().querySelector("[role=alert]"));
assert.match(dialog().textContent, /手动复制/);
close();
copyFailure = false;
for (const state of ["draft", "scheduled", "ended", "disabled"]) {
  response = { ...item(5), state, effective_state: state };
  share(5).click();
  await wait(() => dialog()?.querySelector("[aria-busy=false]"));
  if (state === "draft") {
    assert.equal(dialog().querySelector("input"), null);
    assert.equal(button("复制链接"), undefined);
    assert.match(dialog().textContent, /尚未启用/);
  } else {
    assert.ok(dialog().querySelector("[data-qr-payload]"));
    assert.match(dialog().textContent, /不能参加/);
  }
  close();
}
for (const failure of [403, 503, new Error("offline")]) {
  response = failure;
  share(5).click();
  await wait(() => dialog()?.querySelector("[role=alert]"));
  assert.equal(dialog().querySelector("input"), null);
  assert.equal(dialog().querySelector("[data-qr-payload]"), null);
  response = item(5);
  button("重试").click();
  await wait(() => dialog()?.querySelector("input"));
  close();
}
// A stale campaign-5 request is discarded even if its fetch ignores cancellation.
gate = new Promise((resolve) => (release = resolve));
response = item(5);
share(5).click();
await wait(() => dialog());
const old = dialog();
release();
gate = undefined;
response = item(8);
share(8).focus();
share(8).click();
await wait(() => dialog()?.querySelector("input"));
assert.notEqual(dialog(), old);
assert.equal(
  dialog().querySelector("input").value,
  "https://h5.example/referral?campaign=8",
);
await new Promise((r) => setTimeout(r, 10));
assert.equal(
  dialog().querySelector("input").value,
  "https://h5.example/referral?campaign=8",
);
close();
assert.equal(
  doc.activeElement,
  share(8),
  "replacement dialog returns focus to its own trigger",
);
response = {
  ...item(5),
  activity_url: "https://h5.example/referral?campaign=8",
};
share(5).click();
await wait(() => dialog()?.querySelector("[role=alert]"));
assert.equal(dialog().querySelector("[data-qr-payload]"), null);
close();
assert.equal(
  reads.every((v) => v.method === "GET"),
  true,
  "sharing must never write business facts",
);
assert.equal(
  reads.some((v) => v.url.pathname.endsWith("/invite")),
  false,
);
assert.equal(
  doc.querySelector('[aria-label="活动公共链接"]'),
  null,
  "no permanent detail/setting entry",
);
dom.window.close();
console.log(
  "operator list share: configured H5 URL, states, failure/retry, cancellation, focus and no writes passed",
);
