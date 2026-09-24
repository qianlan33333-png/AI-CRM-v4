import assert from "node:assert/strict";
import { JSDOM, VirtualConsole } from "jsdom";

const baseURL = String(process.env.AICRM_PUBLIC_CHECKOUT_JOURNEY_BASE_URL || "").replace(/\/$/, "");
const firstSession = String(process.env.AICRM_PUBLIC_CHECKOUT_JOURNEY_FIRST_COOKIE || "");
const secondSession = String(process.env.AICRM_PUBLIC_CHECKOUT_JOURNEY_SECOND_COOKIE || "");
if (!baseURL || !firstSession || !secondSession) throw new Error("public checkout journey requires base URL and two trusted cookies");

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));
let keySequence = 0;

class SharedStorage {
  constructor({ readFails = false, writeFails = false } = {}) {
    this.readFails = readFails;
    this.writeFails = writeFails;
    this.values = new Map();
    this.writeAttempts = 0;
  }

  getItem(key) {
    if (this.readFails) throw new Error("storage read unavailable");
    return this.values.has(key) ? this.values.get(key) : null;
  }

  setItem(key, value) {
    this.writeAttempts += 1;
    if (this.writeFails) throw new Error("storage write unavailable");
    this.values.set(String(key), String(value));
  }

  removeItem(key) {
    if (this.writeFails) throw new Error("storage write unavailable");
    this.values.delete(String(key));
  }
}

async function getPage(cookie, path = "/pay/course-7") {
  const response = await fetch(new URL(path, baseURL), { headers: { Cookie: cookie } });
  assert.equal(response.status, 200, `payment page status ${path}`);
  return response.text();
}

async function runPage(storage, cookie, bridge, path = "/pay/course-7") {
  const html = await getPage(cookie, path);
  const errors = [];
  const console = new VirtualConsole();
  console.on("jsdomError", (error) => errors.push(error));
  const dom = new JSDOM(html, {
    url: new URL(path, baseURL).toString(),
    pretendToBeVisual: true,
    runScripts: "dangerously",
    virtualConsole: console,
    beforeParse(window) {
      Object.defineProperty(window.navigator, "userAgent", { configurable: true, value: "MicroMessenger test" });
      Object.defineProperty(window, "sessionStorage", { configurable: true, value: storage });
      Object.defineProperty(window, "crypto", { configurable: true, value: { randomUUID: () => `checkout-journey-${++keySequence}` } });
      Object.defineProperty(window, "AbortController", { configurable: true, value: globalThis.AbortController });
      window.WeixinJSBridge = {
        invoke(_method, _handoff, callback) {
          callback({ err_msg: bridge.next() });
        },
      };
      window.fetch = async (input, init = {}) => {
        const requestURL = new URL(typeof input === "string" ? input : input.url, baseURL);
        const headers = new Headers(init.headers || {});
        headers.set("Cookie", cookie);
        return fetch(requestURL, { ...init, headers });
      };
      window.setTimeout = (callback, milliseconds = 0) => {
        if (milliseconds >= 12000) return globalThis.setTimeout(callback, milliseconds);
        queueMicrotask(callback);
        return 1;
      };
      window.clearTimeout = (handle) => { if (handle !== 1) globalThis.clearTimeout(handle); };
    },
  });
  for (let attempt=0; attempt<240 && !dom.window.document.getElementById("identityGate").hidden; attempt++) await sleep(5);
  assert.equal(dom.window.document.getElementById("identityGate").hidden,true,"trusted identity and purchase state resolved");
  assert.equal(errors.length, 0, errors.map((error) => error.stack || error.message).join("\n"));
  Object.defineProperty(dom, "checkoutJourneyErrors", { value: errors });
  return dom;
}

function closePage(dom) {
  assert.equal(dom.checkoutJourneyErrors.length, 0, dom.checkoutJourneyErrors.map((error) => error.stack || error.message).join("\n"));
  dom.window.close();
}

function setPurchase(dom, couponID, mobile) {
  const document = dom.window.document;
  assert.equal(document.getElementById("beneficiarySelf"), null, "checkout needs no separate beneficiary confirmation");
  const coupon = document.getElementById("coupon");
  const option = document.createElement("option");
  option.value = String(couponID);
  option.textContent = `券 ${couponID}`;
  coupon.appendChild(option);
  coupon.value = String(couponID);
  const mobileField = document.getElementById("mobile");
  if (mobileField) mobileField.value = mobile;
}

async function waitFor(document, expected, label) {
  for (let attempt = 0; attempt < 240; attempt += 1) {
    const status = document.getElementById("status");
    if (expected === paidWithoutCompletionAction && status?.querySelector("h2")?.textContent === "支付完成" && status.textContent.includes(expected)) return;
    if (status?.textContent === expected) return;
    await sleep(5);
  }
  assert.equal(document.getElementById("status")?.textContent, expected, label);
}

const normalBridge = { next: () => "get_brand_wcpay_request:ok" };
const paidWithoutCompletionAction = "支付成功，后续指引暂不可用";

function requirePaidCheckpoint(storage, label) {
  assert.equal(storage.values.size, 1, `${label} keeps exactly one paid checkpoint`);
  const checkpoint = JSON.parse([...storage.values.values()][0]);
  assert.equal(checkpoint.terminal_status, "paid", `${label} marks the exact checkout terminal`);
  assert.equal(typeof checkpoint.merchant_order_no, "string", `${label} keeps its original merchant order`);
  assert.notEqual(checkpoint.merchant_order_no, "", `${label} does not permit a new checkout`);
  return checkpoint;
}

// The payment mutation succeeds but its first HTTP response is lost. The
// reloaded page must replay the saved key with the original coupon/mobile,
// rather than use the changed form values.
const promotionA = `dpc_${"A".repeat(43)}`;
const promotionB = `dpc_${"B".repeat(43)}`;
const replayStorage = new SharedStorage();
const lostResponse = await runPage(replayStorage, firstSession, normalBridge, `/pay/course-7?promotion_context=${promotionA}`);
setPurchase(lostResponse, 11, "13800138000");
lostResponse.window.document.getElementById("buy").click();
await waitFor(lostResponse.window.document, "订单创建结果尚未核实，原请求已保留。点击重试会复用同一请求，不会重复下单。", "lost response");
assert.equal(lostResponse.window.document.getElementById("buy").textContent, "重试原请求", "lost response offers the original request retry");
assert.equal(replayStorage.values.size, 1, "response loss must retain a recovery checkpoint");
closePage(lostResponse);

// Opening a different sharing link for the same product must recover the
// frozen first checkpoint. It cannot replace its opaque promotion context.
const replayed = await runPage(replayStorage, firstSession, normalBridge, `/pay/course-7?promotion_context=${promotionB}`);
setPurchase(replayed, 99, "13900139000");
replayed.window.document.getElementById("buy").click();
await waitFor(replayed.window.document, paidWithoutCompletionAction, "replayed checkout");
const replayedPaid = requirePaidCheckpoint(replayStorage, "replayed checkout");
assert.equal(replayedPaid.payload.promotion_context, promotionA, "replayed checkout keeps the original promotion context");
closePage(replayed);

// A terminal checkpoint is a read-only recovery record. Reloading the same
// promotion checkpoint from an ordinary page may read that exact paid order,
// but must not call the SDK or create a replacement order; ordinary products
// have no renewal button.
const replayedReload = await runPage(replayStorage, firstSession, normalBridge);
await waitFor(replayedReload.window.document, paidWithoutCompletionAction, "replayed terminal reload");
assert.equal(replayedReload.window.document.getElementById("buy").disabled, true, "terminal reload cannot initiate a new checkout");
assert.equal(replayedReload.window.document.getElementById("renew"), null, "ordinary product cannot be repurchased");
assert.equal(replayedReload.window.document.getElementById("buy").textContent, "已购买");
const regularReloadCheckpoint = requirePaidCheckpoint(replayStorage, "replayed terminal reload");
assert.equal(regularReloadCheckpoint.merchant_order_no, replayedPaid.merchant_order_no, "terminal reload reads the same merchant order");
assert.equal(regularReloadCheckpoint.payload.promotion_context, promotionA, "ordinary page keeps the frozen promotion context");
closePage(replayedReload);

// A cancelled WeChat sheet leaves the same merchant order recoverable. A
// later explicit click opens that order again and does not create another one.
let bridgeCalls = 0;
const cancelThenPayBridge = { next: () => (++bridgeCalls === 1 ? "get_brand_wcpay_request:cancel" : "get_brand_wcpay_request:ok") };
const cancelStorage = new SharedStorage();
const cancelled = await runPage(cancelStorage, firstSession, cancelThenPayBridge);
setPurchase(cancelled, 12, "13800138000");
cancelled.window.document.getElementById("buy").click();
await waitFor(cancelled.window.document, "支付未完成，请使用原订单继续支付", "cancelled checkout");
assert.equal(cancelled.window.document.getElementById("buy").disabled, false, "cancelled order remains actionable");
cancelled.window.document.getElementById("buy").click();
await waitFor(cancelled.window.document, paidWithoutCompletionAction, "resumed cancelled checkout");
assert.equal(bridgeCalls, 2, "an explicit second click reopens the same WeChat payment");
requirePaidCheckpoint(cancelStorage, "paid resumed order");
closePage(cancelled);

// An unresolved outcome remains tied to its saved merchant order, even after
// the browser's bounded polling window ends.
const unknownStorage = new SharedStorage();
const unknown = await runPage(unknownStorage, firstSession, normalBridge);
setPurchase(unknown, 14, "13800138000");
unknown.window.document.getElementById("buy").click();
await waitFor(unknown.window.document, "支付结果确认超时，请稍后刷新查看", "unknown checkout");
assert.equal(unknownStorage.values.size, 1, "unknown result keeps checkpoint for later status recovery");
closePage(unknown);

// A normal checkout remains normal when later opened through a sharing link.
// Its original key is restored instead of being replaced by the new page's
// promotion context before Payment can perform its authoritative read.
const ordinaryCheckpoint = JSON.parse([...unknownStorage.values.values()][0]);
const ordinaryThenPromotion = await runPage(unknownStorage, firstSession, normalBridge, `/pay/course-7?promotion_context=${promotionA}`);
await waitFor(ordinaryThenPromotion.window.document, "已恢复原订单，请继续确认支付。", "ordinary checkpoint restoration before a new click");
assert.equal(ordinaryThenPromotion.window.document.getElementById("buy").disabled, false, "restored checkpoint enables only the original-order continuation");
ordinaryThenPromotion.window.document.getElementById("buy").click();
await waitFor(ordinaryThenPromotion.window.document, "支付结果确认超时，请稍后刷新查看", "ordinary checkpoint from promotion page");
const recoveredOrdinaryCheckpoint = JSON.parse([...unknownStorage.values.values()][0]);
assert.equal(recoveredOrdinaryCheckpoint.key, ordinaryCheckpoint.key, "promotion page keeps original ordinary idempotency key");
assert.equal(recoveredOrdinaryCheckpoint.merchant_order_no, ordinaryCheckpoint.merchant_order_no, "promotion page keeps original ordinary merchant order");
assert.equal(recoveredOrdinaryCheckpoint.payload.promotion_context, undefined, "promotion page cannot attach attribution to an existing ordinary checkout");
closePage(ordinaryThenPromotion);

// A malformed persisted promotion context is neither replaced by the current
// URL nor sent to Payment. The original recovery record stays available for
// support rather than becoming a second checkout.
const malformedPromotionStorage = new SharedStorage();
const malformedPromotionCheckpoint = JSON.stringify({
  key: "malformed-promotion-checkpoint-0001",
  merchant_order_no: "",
  session_binding: "b".repeat(43),
  payload: { product_id: 7, product_kind: "standard", beneficiary_selection: "payer_self", coupon_claim_id: 17, mobile: "+8613800138000", promotion_context: "dpc_malformed" },
});
malformedPromotionStorage.setItem("aicrm.checkout.tab.v2:7:standard", malformedPromotionCheckpoint);
const malformedPromotion = await runPage(malformedPromotionStorage, firstSession, normalBridge, `/pay/course-7?promotion_context=${promotionA}`);
setPurchase(malformedPromotion, 17, "13800138000");
malformedPromotion.window.document.getElementById("buy").click();
await waitFor(malformedPromotion.window.document, "原订单恢复信息异常，已保留，请联系管理员核对", "malformed promotion checkpoint");
assert.equal(malformedPromotionStorage.values.get("aicrm.checkout.tab.v2:7:standard"), malformedPromotionCheckpoint, "malformed checkpoint remains unchanged");
closePage(malformedPromotion);

// A known merchant order may be read after OAuth renews the same trusted
// payer. The Host does not compare a stale browser binding or issue Create; the
// Payment HTTP owner authorizes the original persisted order and returns its
// terminal fact.
const switchStorage = new SharedStorage();
const originalSession = await runPage(switchStorage, firstSession, normalBridge);
setPurchase(originalSession, 13, "13800138000");
originalSession.window.document.getElementById("buy").click();
await waitFor(originalSession.window.document, "支付结果确认超时，请稍后刷新查看", "original session pending checkout");
closePage(originalSession);
const switchedSession = await runPage(switchStorage, secondSession, normalBridge);
setPurchase(switchedSession, 13, "13800138000");
switchedSession.window.document.getElementById("buy").click();
await waitFor(switchedSession.window.document, paidWithoutCompletionAction, "renewed same-payer session reads known merchant order");
requirePaidCheckpoint(switchStorage, "terminal known-order recovery");
closePage(switchedSession);

// Failing browser storage blocks the very first payment request, so an
// unknown effect is never created without a recovery identifier.
const unavailableStorage = new SharedStorage({ writeFails: true });
const unavailable = await runPage(unavailableStorage, firstSession, normalBridge);
setPurchase(unavailable, 15, "13800138000");
unavailable.window.document.getElementById("buy").click();
await waitFor(unavailable.window.document, "无法保存本次订单恢复信息，请检查浏览器存储后重试", "storage write unavailable");
assert.equal(unavailableStorage.writeAttempts, 1, "write failure attempts only the local checkpoint");
closePage(unavailable);

// A read failure is more dangerous than a write failure: the page cannot know
// whether a recoverable order exists. It must not mint a key or overwrite
// storage even when a later setItem would succeed.
const readUnavailableStorage = new SharedStorage({ readFails: true });
const readUnavailable = await runPage(readUnavailableStorage, firstSession, normalBridge);
setPurchase(readUnavailable, 18, "13800138000");
readUnavailable.window.document.getElementById("buy").click();
await waitFor(readUnavailable.window.document, "无法保存本次订单恢复信息，请检查浏览器存储后重试", "storage read unavailable");
assert.equal(readUnavailableStorage.writeAttempts, 0, "read failure cannot overwrite an unknown checkpoint");
closePage(readUnavailable);

// A checkpoint written by the immediately preceding Host version has no
// session binding. If its create response was lost, it must remain visible but
// never be treated as permission to generate a fresh idempotency key.
const legacyStorage = new SharedStorage();
legacyStorage.setItem("aicrm.checkout.tab.v2:7:standard", JSON.stringify({
  key: "legacy-checkpoint-0001",
  merchant_order_no: "",
  payload: { product_id: 7, product_kind: "standard", beneficiary_selection: "payer_self", coupon_claim_id: 16, mobile: "+8613800138000" },
}));
const legacy = await runPage(legacyStorage, firstSession, normalBridge);
setPurchase(legacy, 16, "13800138000");
legacy.window.document.getElementById("buy").click();
await waitFor(legacy.window.document, "旧版订单恢复标识缺少付款会话绑定，已保留原标识，请勿重新下单", "legacy response-lost checkpoint");
assert.equal(legacyStorage.values.size, 1, "legacy unbound recovery must remain and never create a new order");
closePage(legacy);
