import assert from "node:assert/strict";
import { JSDOM, VirtualConsole } from "jsdom";

const baseURL = String(process.env.AICRM_SERVICE_PERIOD_JOURNEY_BASE_URL || "").replace(/\/$/, "");
const trustedCookie = String(process.env.AICRM_SERVICE_PERIOD_JOURNEY_COOKIE || "");
if (!baseURL || !trustedCookie) throw new Error("service-period journey requires base URL and trusted cookie");

const sleep = (milliseconds) => new Promise((resolve) => setTimeout(resolve, milliseconds));

async function getPage(path, cookie) {
  const response = await fetch(new URL(path, baseURL), {
    headers: cookie ? { Cookie: cookie } : {},
  });
  assert.equal(response.status, 200, `page ${path}`);
  return response.text();
}

async function runPage(path, cookie) {
  const html = await getPage(path, cookie);
  const errors = [];
  let refreshes = 0;
  const console = new VirtualConsole();
  console.on("jsdomError", (error) => errors.push(error));
  const dom = new JSDOM(html, {
    url: new URL(path, baseURL).toString(),
    pretendToBeVisual: true,
    runScripts: "dangerously",
    virtualConsole: console,
    beforeParse(window) {
      Object.defineProperty(window.navigator,'userAgent',{value:cookie?'MicroMessenger':'Safari'});
      window.fetch = async (input, init = {}) => {
        refreshes += 1;
        const requestURL = new URL(typeof input === "string" ? input : input.url, baseURL);
        if(requestURL.pathname.endsWith('/checkout-session')) return {ok:true,status:200,json:async()=>({checkout_session_binding:'a'.repeat(43),can_create_checkout:true})};
        if(requestURL.pathname.endsWith('/purchase-status')) return {ok:true,status:200,json:async()=>({purchase_state:'available',can_purchase:true})};
        return {ok:true,status:200,json:async()=>({items:[]})};
      };
    },
  });
  for (let attempt = 0; attempt < 80 && refreshes === 0; attempt += 1) await sleep(10);
  await sleep(30);
  assert.equal(errors.length, 0, errors.map((error) => error.stack || error.message).join("\n"));
  assert.equal(refreshes, cookie?3:0, "trusted checkout reads session, ownership and coupons only");
  return dom;
}

const active = await runPage("/s/term-31", trustedCookie);
const activeDocument = active.window.document;
assert.equal(activeDocument.getElementById('identityGate').hidden,true);
assert.equal(activeDocument.getElementById('checkoutContent').hidden,false);
assert.equal(activeDocument.querySelector('#checkoutContent h1')?.textContent, "服务 {{state_json}} \\ 标题");
assert.equal(activeDocument.getElementById('detailContent'),null,"no material enters checkout directly");
assert.equal(activeDocument.getElementById('buy').textContent,'立即支付');
assert.ok(activeDocument.getElementById('renew'),"periodic checkout supports renewal");
active.window.close();

// A legacy fragment cannot mint an entitlement. Without the existing opaque
// Payment OAuth cookie the exact same page stays unregistered after refresh.
const untrusted = await runPage("/s/term-31#aicrm_ctx=untrusted-external-id", "");
const untrustedDocument = untrusted.window.document;
assert.equal(untrustedDocument.getElementById('identityGate').hidden,false);
assert.equal(untrustedDocument.getElementById('checkoutContent').hidden,true);
assert.doesNotMatch(untrustedDocument.documentElement.outerHTML, /aicrm_ctx/);
untrusted.window.close();
