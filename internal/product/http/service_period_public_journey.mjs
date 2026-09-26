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
        return fetch(requestURL, { ...init, headers: cookie ? { Cookie: cookie } : {} });
      };
    },
  });
  for (let attempt = 0; attempt < 80 && refreshes === 0; attempt += 1) await sleep(10);
  await sleep(30);
  assert.equal(errors.length, 0, errors.map((error) => error.stack || error.message).join("\n"));
  assert.equal(refreshes, 1, "details refresh only the read-only service-period state");
  return dom;
}

const active = await runPage("/s/term-31", trustedCookie);
const activeDocument = active.window.document;
assert.equal(activeDocument.getElementById('identityGate'), null);
assert.equal(activeDocument.getElementById('checkoutContent'), null);
assert.equal(activeDocument.querySelector('h1')?.textContent, "服务 {{state_json}} \\ 标题");
assert.match(activeDocument.getElementById('servicePeriodStateCard').textContent, /16 天/);
assert.match(activeDocument.getElementById('servicePeriodStateCard').textContent, /2026-09-21/);
assert.equal(activeDocument.getElementById('servicePeriodPayButton').textContent, '立即续费');
assert.equal(activeDocument.getElementById('servicePeriodWecomAction').hidden, false);
active.window.close();

// A legacy fragment cannot mint an entitlement. Without the existing opaque
// Payment OAuth cookie the exact same page stays unregistered after refresh.
const untrusted = await runPage("/s/term-31#aicrm_ctx=untrusted-external-id", "");
const untrustedDocument = untrusted.window.document;
assert.equal(untrustedDocument.getElementById('identityGate'), null);
assert.equal(untrustedDocument.getElementById('checkoutContent'), null);
assert.equal(untrustedDocument.getElementById('servicePeriodPayButton').textContent, '立即报名');
assert.equal(untrustedDocument.getElementById('servicePeriodWecomAction').hidden, true);
assert.doesNotMatch(untrustedDocument.getElementById('servicePeriodStateCard').textContent, /剩余有效期/);
assert.doesNotMatch(untrustedDocument.documentElement.outerHTML, /aicrm_ctx/);
untrusted.window.close();
