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
  uncertain = false,
  awaitingTitle = false,
  wechat = false,
  pendingOrder = false,
  freeOrder = false,
  cardPending = false,
  cardFail = false,
} = {}) {
  const dom = new JSDOM('<main id="customer-orders-root"></main>', {
    url: "https://crm.test/my/orders",
    runScripts: "outside-only",
    pretendToBeVisual: true,
  });
  const { window: w } = dom;
  const calls = [];
  const applications = [];
  w.TextEncoder = TextEncoder;
  if (wechat) {
    Object.defineProperty(w.navigator, "userAgent", { value: "MicroMessenger" });
    let ready;
    w.wx = { ready(fn) { ready = fn; }, error() {}, config(config) { calls.push({ config }); ready(); }, openCard(value) { calls.push({ cardList:value.cardList }); cardFail ? value.fail() : value.success(); } };
  }
  let uncertainOnce = uncertain;
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
    if (path.startsWith("/api/h5/me/invoice-jssdk")) return { ok:true, status:200, json:async()=>({appId:"wx-test", timestamp:1,nonceStr:"test",signature:"test"}) };
    if (path.endsWith("/card")) return cardPending ? {ok:false,status:409,json:async()=>({code:"pending"})} : {ok:true,status:200,json:async()=>({cardId:"card-test",code:"card-code-test"})};
    if (path.startsWith("/api/h5/me/orders")) {
      const url = new URL(path, w.location.origin);
      let rows = [
        {
          ...row(1, "共创营 <img src=x onerror=alert(1)>"),
          ...(awaitingTitle
            ? {
                invoice: {
                  id: "42000000000000000000000000000001",
                  order_id: 1,
                  amount_minor: 1000,
                  state: "awaiting_title",
                  card_state: "pending",
                },
              }
            : {}),
        },
        { ...row(2, "成长课"), ...(freeOrder ? {provider:"no_payment",amount_minor:0,state:"paid",invoice_eligible:false}: {}), ...(pendingOrder ? {state:"pending_payment", invoice_eligible:false, payment_path:"/pay/course-7?resume_order=2"}: {}) },
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
    if (path === "/api/h5/me/invoices" && options?.method === "POST") {
      const body = JSON.parse(options.body);
      const invoice = {
        id: `4200000000000000000000000000000${body.order_id}`,
        order_id: body.order_id,
        amount_minor: body.order_id * 1000,
        state: "queued",
        card_state: "pending",
        refund_confirmed: false,
      };
      if (!applications.length)
        applications.push({
          ...invoice,
          order_no: `ORDER-${body.order_id}`,
          product_names: ["开票课程"],
          created_at: "2026-10-08T00:00:00Z",
        });
      if (uncertainOnce) {
        uncertainOnce = false;
        throw Error("network response lost");
      }
      return { ok: true, status: 200, json: async () => applications[0] };
    }
    if (path.startsWith("/api/h5/me/invoices?")) {
      return {
        ok: true,
        status: 200,
        json: async () => ({ items: applications, next_cursor: "" }),
      };
    }
    if (path.startsWith("/api/h5/me/invoices/420")) {
      return {
        ok: true,
        status: 200,
        json: async () =>
          applications[0] || {
            id: "42000000000000000000000000000001",
            order_id: 1,
            amount_minor: 1000,
            state: "awaiting_title",
            card_state: "pending",
            refund_confirmed: false,
          },
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
    applications,
    click: (text, parent = w.document) => {
      const b = [...parent.querySelectorAll("button")].find(
        (b) => b.textContent === text,
      );
      assert.ok(b, `button ${text}`);
      b.click();
    },
  };
}
function fillTitle(f, name = "测试单位", tax = "TEST-TAX") {
  for (const [field, value] of [
    ["name", name],
    ["taxpayer_id", tax],
  ]) {
    const input = f.root.querySelector(`[name="${field}"]`);
    input.value = value;
    input.dispatchEvent(new f.w.Event("input"));
  }
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
  assert.match(f.root.textContent, /填写信息/);
  assert.equal(
    f.calls.some((c) => c.path === "/api/h5/me/invoices"),
    false,
    "no draft/effect before confirmation",
  );
  fillTitle(f);
  f.click("提交");
  const confirm = f.w.document.querySelector("dialog");
  assert.match(confirm.textContent, /发票信息确认/);
  assert.match(confirm.textContent, /¥20.00/);
  assert.match(confirm.textContent, /微信卡包/);
  assert.equal(
    f.calls.some((c) => c.options?.method === "POST"),
    false,
  );
  f.click("返回修改", confirm);
  assert.equal(f.root.querySelector('[name="name"]').value, "测试单位");
  f.click("提交");
  f.click("确认提交", f.w.document.querySelector("dialog"));
  await flush();
  const posted = f.calls.find((c) => c.path === "/api/h5/me/invoices");
  const body = JSON.parse(posted.options.body);
  assert.equal(body.order_id, 2);
  assert.equal(body.expected_amount_minor, 2000);
  assert.deepEqual(body.buyer, {
    type: "ORGANIZATION",
    name: "测试单位",
    taxpayer_id: "TEST-TAX",
  });
  assert.equal(Object.hasOwn(body, "customer_id"), false);
  assert.match(f.root.textContent, /申请已提交，正在开票/);
  assert.doesNotMatch(f.root.textContent, /24小时|邮箱|XML|OFD|出行人/);
  f.click("查看发票");
  await flush();
  assert.match(f.root.textContent, /开票记录/);
  assert.match(f.root.textContent, /开票中/);
  assert.equal(f.root.querySelector('a[href*="/file"]'), null);
  f.applications[0].state = "issued";
  f.applications[0].card_state = "INSERTED";
  f.click("刷新记录");
  await flush();
  assert.match(f.root.textContent, /已开票/);
  assert.match(f.root.textContent, /已送达微信卡包/);
  assert.equal(f.root.querySelector('a[href*="/file"]'), null);
  f.click("查看发票"); await flush();
  assert.match(f.root.textContent, /请在微信中打开此页面/);
  f.dom.window.close();
}
{
  const f = fixture();
  await flush();
  f.applications.push({
    id: "42000000000000000000000000000001",
    order_id: 1,
    amount_minor: 9900,
    state: "failed",
    card_state: "INSERT_ACCEPTED",
    order_no: "ORDER-1",
    product_names: ["测试课程"],
    created_at: "2026-10-09T00:00:00Z",
  });
  f.click("开票记录");
  await flush();
  assert.match(f.root.textContent, /未出票，暂未交付卡包/);
  assert.doesNotMatch(f.root.textContent, /卡包交付中/);
  f.click("查看进度");
  await flush();
  assert.match(f.root.textContent, /开票失败/);
  assert.match(f.root.textContent, /未出票，暂未交付卡包/);
  assert.ok(
    [...f.root.querySelectorAll("button")].some(
      (b) => b.textContent === "重试开票",
    ),
  );
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
  fillTitle(f);
  f.click("提交");
  f.click("确认提交", f.w.document.querySelector("dialog"));
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

{
  const f = fixture({ awaitingTitle: true });
  await flush();
  f.click("待填抬头");
  await flush();
  assert.match(f.root.textContent, /填写信息/);
  assert.equal(f.root.querySelector('[name="title_type"]'), null);
  assert.doesNotMatch(f.root.textContent, /个人|抬头类型/);
  fillTitle(f);
  f.click("提交");
  f.click("确认提交", f.w.document.querySelector("dialog"));
  await flush();
  assert.equal(f.applications[0].id, "42000000000000000000000000000001");
  assert.equal(
    f.calls.some((c) => c.path.includes("jssdk") || c.path.includes("resume")),
    false,
    "self form needs no official launch",
  );
  f.click("继续开票");
  await flush();
  assert.match(f.root.textContent, /请选择一笔订单/);
  f.dom.window.close();
}
{
  const f = fixture({ uncertain: true });
  await flush();
  f.click("开发票", f.root.querySelector("article"));
  fillTitle(f);
  f.click("提交");
  f.click("确认提交", f.w.document.querySelector("dialog"));
  await flush();
  assert.match(f.root.textContent, /核对并继续提交/);
  assert.equal(f.root.querySelector('[name="name"]').disabled, true);
  f.click("核对并继续提交");
  await flush();
  const posts = f.calls.filter((c) => c.options?.method === "POST");
  assert.equal(posts.length, 2);
  assert.equal(
    posts[0].options.body,
    posts[1].options.body,
    "ambiguous response replays exact confirmed intent",
  );
  assert.match(f.root.textContent, /申请已提交/);
  f.dom.window.close();
}
console.log(
  "self-title flows passed: confirmation before write, company-only, awaitingTitle resume, exact ambiguous replay, invoice history, native card viewing, read-only refresh",
);

for (const opts of [{wechat:true},{wechat:true,cardPending:true},{wechat:true,cardFail:true}]) {
 const f=fixture(opts); await flush();
 f.applications.push({id:"42000000000000000000000000000001",order_id:1,amount_minor:1000,state:"issued",card_state:"INSERTED",order_no:"ORDER-1",product_names:["已开票课程"],created_at:"2026-10-08T00:00:00Z"});
 f.click("开票记录"); await flush(); f.click("查看发票"); await flush(); await flush();
 if(opts.cardPending) {assert.match(f.root.textContent,/正在送达微信卡包/);assert.equal(f.calls.some(c=>c.cardList),false);}
 else {assert.deepEqual(JSON.parse(JSON.stringify(f.calls.find(c=>c.cardList).cardList)),[{cardId:"card-test",code:"card-code-test"}]);assert.deepEqual(Array.from(f.calls.find(c=>c.config).config.jsApiList),["openCard"]);if(opts.cardFail)assert.match(f.root.textContent,/微信卡包未能打开/);}
 assert.equal(f.calls.some(c=>c.path?.includes("/file")),false); f.dom.window.close();
}
{
 const f=fixture({pendingOrder:true});await flush();const link=f.root.querySelector('a');assert.equal(link.textContent,"继续支付");assert.equal(link.href,"https://crm.test/pay/course-7?resume_order=2");assert.equal(f.calls.some(c=>c.options?.method==="POST"),false);f.dom.window.close();
}
console.log("native invoice card and original payment entry journeys passed");

{
 const f=fixture({freeOrder:true});await flush();
 const free=f.root.querySelectorAll('article')[1];
 assert.match(free.textContent,/领取成功/);assert.match(free.textContent,/无需支付/);assert.match(free.textContent,/¥0\.00/);
 assert.equal(free.querySelector('a'),null);assert.equal(Array.from(free.querySelectorAll('button')).some(b=>b.textContent==='开发票'),false);
 assert.equal(f.calls.some(c=>c.options?.method==='POST'),false);f.dom.window.close();
}
