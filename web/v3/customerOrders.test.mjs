import assert from "node:assert/strict";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";
const bundle = await buildTestBrowserBundle("web/v3/customerOrders.ts");
const flush = () => new Promise((resolve) => setImmediate(resolve));
const row = (id, name, eligible = true) => ({
  id,
  order_no: `ORDER-${id}`,
  product_names: [name],
  time: "2026-10-07T16:00:00Z",
  time_kind: "ordered",
  amount_minor: id * 1000,
  currency: "CNY",
  provider: "wechat_pay",
  state: "paid",
  refunded_minor: 0,
  refund_pending: false,
  invoice_eligible: eligible,
  invoice_reason: eligible ? "" : "unsupported_channel",
});
function fixture({
  enabled = true,
  expired = false,
  expireWrites = false,
} = {}) {
  const dom = new JSDOM('<main id="customer-orders-root"></main>', {
    url: "https://crm.test/my/orders",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  const { window: w } = dom;
  const calls = [];
  w.HTMLDialogElement.prototype.showModal = function () {
    this.open = true;
  };
  w.HTMLDialogElement.prototype.close = function () {
    this.open = false;
    this.dispatchEvent(new w.Event("close"));
  };
  w.fetch = async (path, options) => {
    calls.push({ path, options });
    if (expired || (expireWrites && options?.method === "POST"))
      return {
        ok: false,
        status: 401,
        json: async () => ({ code: "session_required" }),
      };
    if (path.startsWith("/api/h5/me/orders")) {
      const url = new URL(path, w.location.origin);
      let rows = [
        row(1, "共创营 <img src=x onerror=alert(1)>"),
        row(2, "成长课"),
        { ...row(3, "历史课", false), currency: "USD" },
      ];
      if (url.searchParams.get("eligible") === "true")
        rows = rows.filter((r) => r.invoice_eligible);
      if (url.searchParams.get("keyword"))
        rows = rows.filter((r) =>
          r.product_names[0].includes(url.searchParams.get("keyword")),
        );
      if (url.searchParams.get("order_no"))
        rows = rows.filter(
          (r) => r.order_no === url.searchParams.get("order_no"),
        );
      if (!enabled) rows.forEach((r) => (r.invoice_eligible = false));
      return {
        ok: true,
        status: 200,
        json: async () => ({
          items: rows,
          next_cursor: "",
          invoice_enabled: enabled,
        }),
      };
    }
    if (path === "/api/h5/me/invoices") {
      const body = JSON.parse(options.body);
      return {
        ok: true,
        status: 200,
        json: async () => ({
          id: "inv_" + "a".repeat(28),
          order_id: body.order_id,
          amount_minor: body.order_id * 1000,
          state: "awaiting_title",
          card_state: "pending",
          refund_confirmed: false,
          launch: {
            miniprogram_appid: "official",
            miniprogram_path: "pages/title",
            miniprogram_user_name: "gh_official",
          },
        }),
      };
    }
    throw Error(`unexpected fetch ${path}`);
  };
  w.eval(bundle);
  return {
    dom,
    w,
    calls,
    root: w.document.querySelector("main"),
    click: (text, parent = w.document) => {
      const b = [...parent.querySelectorAll("button")].find(
        (b) => b.textContent === text,
      );
      assert.ok(b, `button ${text}`);
      b.click();
    },
  };
}
{
  const f = fixture();
  await flush();
  assert.equal(f.root.querySelectorAll("article").length, 3);
  assert.match(
    f.root.querySelectorAll("article")[2].textContent,
    /USD 30.00/,
    "preserve recorded order currency",
  );
  assert.equal(
    f.root.querySelector("img"),
    null,
    "product text never becomes markup",
  );
  assert.match(f.root.textContent, /2026\/10\/08/, "Beijing display");
  f.click("筛选");
  const d = f.w.document.querySelector("dialog");
  const set = (name, value) => {
    d.querySelector(`[name="${name}"]`).value = value;
  };
  set("from", "2026-10-07");
  set("until", "2026-10-08");
  set("keyword", "共创");
  set("order_no", "ORDER-1");
  d.querySelector("form").dispatchEvent(
    new f.w.Event("submit", { cancelable: true }),
  );
  await flush();
  const q = new URL(f.calls.at(-1).path, "https://crm.test").searchParams;
  assert.equal(q.get("from"), "2026-10-07");
  assert.equal(q.get("until"), "2026-10-08");
  assert.equal(q.get("keyword"), "共创");
  assert.equal(q.get("order_no"), "ORDER-1");
  assert.equal(f.root.querySelectorAll("article").length, 1);
  f.click("清除");
  await flush();
  f.click("开发票", f.root.querySelector("header"));
  await flush();
  assert.equal(f.root.querySelectorAll("article").length, 2);
  assert.equal(f.root.querySelector("footer button").disabled, true);
  const radios = f.root.querySelectorAll("input[type=radio]");
  radios[0].checked = true;
  radios[0].dispatchEvent(new f.w.Event("change"));
  radios[1].checked = true;
  radios[1].dispatchEvent(new f.w.Event("change"));
  assert.equal(f.root.querySelectorAll("input:checked").length, 1);
  assert.match(f.root.querySelector("footer").textContent, /¥20.00/);
  f.click("去开票");
  await flush();
  const posted = f.calls.find((c) => c.path === "/api/h5/me/invoices");
  const body = JSON.parse(posted.options.body);
  assert.equal(body.order_id, 2);
  assert.equal(Object.hasOwn(body, "amount_minor"), false);
  assert.equal(Object.hasOwn(body, "customer_id"), false);
  assert.match(f.root.textContent, /待填抬头/);
  assert.match(f.root.textContent, /请在微信中打开此页面填写抬头/);
  f.dom.window.close();
}
{
  const f = fixture({ expired: true });
  await flush();
  assert.match(f.root.textContent, /请在微信中打开/);
  assert.equal(
    f.root.querySelector("article"),
    null,
    "expired session stays on authorization screen",
  );
  f.dom.window.close();
}
{
  const f = fixture({ enabled: false });
  await flush();
  f.click("开发票", f.root.querySelector("header"));
  await flush();
  assert.match(f.root.textContent, /自动开票暂未开通/);
  assert.equal(f.root.querySelector("footer button").disabled, true);
  f.dom.window.close();
}
{
  const f = fixture({ expireWrites: true });
  await flush();
  f.click("开发票", f.root.querySelector("header"));
  await flush();
  const r = f.root.querySelector("input[type=radio]");
  r.checked = true;
  r.dispatchEvent(new f.w.Event("change"));
  f.click("去开票");
  await flush();
  assert.match(
    f.root.textContent,
    /请在微信中打开/,
    "authorization state survives create finally",
  );
  f.dom.window.close();
}
console.log(
  "customerOrders journeys passed: combined filters, single selection, server amount, safe text, expired session, disabled provider",
);
