import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import vm from "node:vm";

const html = readFileSync(0, "utf8");
const script = html.match(/<script>([\s\S]*?)<\/script>/)?.[1];
assert.ok(script, "bridge has an executable script");

const signed = "https://openapi.alipay.com/gateway.do?method=alipay.trade.wap.pay&app_id=example-app&sign=server-signature&biz_content=%7B%22out_trade_no%22%3A%22same-order%22%7D";

function visit(url, userAgent) {
  const ids = ["title", "message", "wechatSteps", "openPay", "copyPay", "fallbackURL", "note"];
  const elements = Object.fromEntries(ids.map((id) => [id, {
    hidden: true,
    textContent: "",
    href: "",
    value: "",
    classList: { add() {} },
    addEventListener(name, callback) { this[name] = callback; },
    focus() {},
    select() {},
  }]));
  let redirected = "";
  let copied = "";
  const parsed = new URL(url);
  vm.runInNewContext(script, {
    document: { getElementById: (id) => elements[id] },
    navigator: { userAgent, clipboard: { async writeText(text) { copied = text; } } },
    location: { hash: parsed.hash, replace(target) { redirected = target; } },
    URL,
    decodeURIComponent,
  });
  return { elements, redirected, get copied() { return copied; } };
}

const bridge = `https://crm.example.test/pay/alipay/continue#${encodeURIComponent(signed)}`;
const wechat = visit(bridge, "MicroMessenger Android");
assert.equal(wechat.redirected, "", "WeChat stays on the instruction page");
assert.equal(wechat.elements.wechatSteps.hidden, false);
assert.equal(wechat.elements.openPay.href, signed);
await wechat.elements.copyPay.click();
assert.equal(wechat.copied, signed, "fallback copies the original signed URL");

const browser = visit(bridge, "Chrome Android");
assert.equal(browser.redirected, signed, "external browser opens the same signed URL");
assert.equal(browser.elements.openPay.hidden, false, "manual continuation remains available");

for (const bad of [
  "https://evil.example/gateway.do?method=alipay.trade.wap.pay&app_id=a&sign=s&biz_content=x",
  signed.replace("https:", "http:"),
  signed.replace("alipay.trade.wap.pay", "alipay.trade.query"),
  signed.replace("&sign=server-signature", ""),
  signed + "&method=alipay.trade.wap.pay",
]) {
  const rejected = visit(`https://crm.example.test/pay/alipay/continue#${encodeURIComponent(bad)}`, "Chrome Android");
  assert.equal(rejected.redirected, "", `invalid URL must not redirect: ${bad}`);
  assert.match(rejected.elements.message.textContent, /返回微信中的原订单页/);
}
const missing = visit("https://crm.example.test/pay/alipay/continue", "Chrome Android");
assert.equal(missing.redirected, "", "lost fragment cannot produce a new order or redirect");
