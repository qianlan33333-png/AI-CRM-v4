import assert from "node:assert/strict";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { JSDOM } from "jsdom";
import { buildTestBrowserBundle } from "../scripts/test-browser-bundle.mjs";

const root = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "../..",
);
const host = await buildTestBrowserBundle(
  path.join(root, "web", "v3", "sidebar", "main.ts"),
);

const intentsByKey = new Map();
const unresolvedByTarget = new Map();
let nextIntentID = 1;
let accepts = 0;
let sendInvocations = 0;

function response(value, status = 200) {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function requestHeaders(init) {
  return new Headers(init?.headers || {});
}

function productScope(body) {
  return body.resource_kind === "product"
    ? String(body.product_type || "")
    : "";
}

function backend(customerID) {
  return async (input, init = {}) => {
    const url = new URL(
      typeof input === "string" ? input : input.url,
      "https://sidebar.test.invalid",
    );
    const method = String(init.method || "GET").toUpperCase();
    if (url.pathname === "/api/sidebar/jssdk-config") {
      return response({
        corp_id: "test-corp",
        agent_id: "test-agent",
        config: {
          timestamp: 1,
          nonceStr: "regular",
          signature: "regular-signature",
        },
        agent_config: {
          timestamp: 1,
          nonceStr: "agent",
          signature: "agent-signature",
        },
      });
    }
    if (url.pathname === "/api/sidebar/v2/bootstrap" && method === "POST") {
      return response({
        state: "ready",
        customer_id: customerID,
        context_token: `context-${customerID}`,
        workbench: { profile: { customer_id: customerID, oneid: `CID-${customerID}` } },
      });
    }
    if (url.pathname === "/api/sidebar/v2/send-intents" && method === "POST") {
      const body = JSON.parse(String(init.body || "{}"));
      const idempotencyKey = requestHeaders(init).get("Idempotency-Key");
      const target = `${customerID}:${body.resource_kind}:${productScope(body)}:${body.resource_id}`;
      const exact = intentsByKey.get(idempotencyKey);
      if (exact)
        return response(
          {
            intent_id: exact.id,
            state: exact.state,
            payload: exact.payload,
            replayed: true,
          },
          202,
        );
      const unresolved = unresolvedByTarget.get(target);
      if (unresolved)
        return response(
          {
            intent_id: unresolved.id,
            state: unresolved.state,
            payload: unresolved.payload,
            replayed: true,
          },
          202,
        );
      const intent = {
        id: nextIntentID++,
        key: idempotencyKey,
        target,
        state: "queued",
        grant: `grant-${nextIntentID}`,
        payload: { msgtype: "news", news: { title: "Safe sidebar send" } },
      };
      intentsByKey.set(idempotencyKey, intent);
      unresolvedByTarget.set(target, intent);
      accepts += 1;
      return response(
        {
          intent_id: intent.id,
          state: intent.state,
          grant: intent.grant,
          payload: intent.payload,
        },
        202,
      );
    }
    const completion = url.pathname.match(
      /^\/api\/sidebar\/v2\/send-intents\/(\d+)\/outcome$/,
    );
    if (completion && method === "POST") {
      const intent = [...intentsByKey.values()].find(
        (candidate) => candidate.id === Number(completion[1]),
      );
      const body = JSON.parse(String(init.body || "{}"));
      assert.ok(intent, "completion must reference the accepted intent");
      assert.equal(
        body.grant,
        intent.grant,
        "completion must retain the original one-time grant",
      );
      intent.state = body.outcome;
      if (body.outcome === "outcome_unknown")
        unresolvedByTarget.set(intent.target, intent);
      else unresolvedByTarget.delete(intent.target);
      return response({ intent_id: intent.id, state: intent.state });
    }
    return response({ code: "unexpected" }, 500);
  };
}

function createBridge(customerID, priorSession = [], options = {}) {
  const dom = new JSDOM('<div id="sidebar-workbench-root"></div>', {
    url: `https://sidebar.test.invalid/sidebar/bind-mobile?external_userid=external-${customerID}`,
    runScripts: "outside-only",
    pretendToBeVisual: true,
    beforeParse(window) {
      window.Response = Response;
      window.Headers = Headers;
      window.fetch = options.fetch || backend(customerID);
      window.wx = {
        config() {
          queueMicrotask(() => ready?.());
        },
        ready(callback) {
          ready = callback;
        },
        error() {},
        agentConfig(options) {
          queueMicrotask(() => options.success?.());
        },
        invoke(method, _payload, callback) {
          if (method === "getCurExternalContact") {
            queueMicrotask(() =>
              callback({
                err_msg: "getCurExternalContact:ok",
                external_userid: options.currentContact ? options.currentContact() : `external-${customerID}`,
              }),
            );
            return;
          }
          if (method === "sendChatMessage") {
            sendInvocations += 1;
            queueMicrotask(() => {
              callback({ err_msg: options.sdkSuccess ? "sendChatMessage:ok" : "sendChatMessage:fail" });
              // This deliberately runs after the SDK callback but before the
              // awaiting send continuation. A context refresh here must not
              // prevent the accepted intent from recording its original grant.
              options.afterSendCallback?.(window);
            });
            return;
          }
          queueMicrotask(() => callback({ err_msg: `${method}:fail` }));
        },
      };
      let ready;
    },
  });
  for (const [key, value] of priorSession)
    dom.window.sessionStorage.setItem(key, value);
  dom.window.eval(host);
  dom.window.document.dispatchEvent(new dom.window.Event("DOMContentLoaded"));
  const bridge = dom.window.__AICRMSidebarBridge;
  assert.ok(bridge, "actual SidebarBridge must mount from the compiled Host");
  return { dom, bridge };
}

function sessionEntries(dom) {
  return Array.from(
    { length: dom.window.sessionStorage.length },
    (_, index) => {
      const key = dom.window.sessionStorage.key(index);
      return [key, dom.window.sessionStorage.getItem(key)];
    },
  );
}

const first = createBridge(1);
const card = {
  resource_kind: "product",
  resource_id: "7",
  product_type: "standard",
};
const duplicate = await Promise.allSettled([
  first.bridge.send(card),
  first.bridge.send(card),
]);
assert.equal(
  duplicate.filter((result) => result.status === "rejected").length,
  2,
  "a failed SDK response must fail both duplicate clicks",
);
assert.equal(accepts, 1, "duplicate clicks must accept one durable intent");
assert.equal(sendInvocations, 1, "duplicate clicks must invoke JSSDK once");

const reloaded = createBridge(1, sessionEntries(first.dom));
await assert.rejects(() => reloaded.bridge.send(card), /执行凭据未返回/);
assert.equal(
  accepts,
  1,
  "a reloaded bridge must re-read the unknown intent instead of accepting a new key",
);
assert.equal(
  sendInvocations,
  1,
  "a reloaded bridge must not invoke JSSDK for outcome_unknown",
);

const switchedCustomer = createBridge(2, sessionEntries(reloaded.dom));
await assert.rejects(
  () => switchedCustomer.bridge.send(card),
  /sendChatMessage/,
);
assert.equal(
  accepts,
  2,
  "a customer switch must not inherit another customer's send lock",
);
assert.equal(
  sendInvocations,
  2,
  "the isolated customer may create its own SDK attempt",
);

await assert.rejects(
  () =>
    switchedCustomer.bridge.send({
      resource_kind: "product",
      resource_id: "7",
      product_type: "service_period",
    }),
  /sendChatMessage/,
);
assert.equal(
  accepts,
  3,
  "standard and service-period products with equal numeric IDs must remain separate bindings",
);
assert.equal(
  sendInvocations,
  3,
  "the separate product binding may invoke its own SDK attempt",
);

// A successful SDK callback belongs to the originally accepted intent, even
// if the visible WebView switches its current contact immediately afterwards.
// Completion must use that frozen grant and original scoped token, not the
// new context controller or a fresh outcome_unknown write.
let callbackContact = "external-6";
const callbackOutcomes = [];
let callbackBridge;
const callbackBackend = async (input, init = {}) => {
  const url = new URL(
    typeof input === "string" ? input : input.url,
    "https://sidebar.test.invalid",
  );
  if (/^\/api\/sidebar\/v2\/send-intents\/\d+\/outcome$/.test(url.pathname)) {
    const body = JSON.parse(String(init.body || "{}"));
    callbackOutcomes.push({
      body,
      token: requestHeaders(init).get("X-Sidebar-Context-Token"),
    });
  }
  return backend(6)(input, init);
};
const callbackFixture = createBridge(6, [], {
  fetch: callbackBackend,
  currentContact: () => callbackContact,
  sdkSuccess: true,
  afterSendCallback: (window) => {
    callbackContact = "external-6-refreshed";
    void window.__AICRMSidebarBridge.retry().catch(() => {});
  },
});
callbackBridge = callbackFixture.bridge;
await callbackBridge.send({
  resource_kind: "product",
  resource_id: "callback-7",
  product_type: "standard",
});
assert.equal(callbackOutcomes.length, 1, "a successful SDK callback records one outcome");
assert.equal(callbackOutcomes[0].body.outcome, "client_executed", "a refreshed contact cannot downgrade a confirmed SDK callback");
assert.equal(callbackOutcomes[0].token, "context-6", "completion uses the original accepted context token");
assert.equal(callbackOutcomes.filter((outcome) => outcome.body.outcome === "outcome_unknown").length, 0, "a successful callback must not add an outcome_unknown receipt");

// A forbidden intent is rejected before client execution. It is a real scoped
// authorization failure, so exactly one accept request occurs, JSSDK is never
// called, and the current Host context is revoked.
let forbiddenIntentRequests = 0;
const beforeForbiddenIntentSends = sendInvocations;
const forbiddenIntent = createBridge(9, [], {
  fetch: async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, "https://sidebar.test.invalid");
    if (url.pathname === "/api/sidebar/v2/send-intents" && String(init.method || "GET").toUpperCase() === "POST") {
      forbiddenIntentRequests += 1;
      return response({ code: "invalid_context" }, 403);
    }
    return backend(9)(input, init);
  },
  sdkSuccess: true,
});
await assert.rejects(
  () => forbiddenIntent.bridge.send({ resource_kind: "product", resource_id: "forbidden-9", product_type: "standard" }),
  /请求失败|上下文|403/,
);
assert.equal(forbiddenIntentRequests, 1, "a forbidden intent makes one real accept request");
assert.equal(sendInvocations, beforeForbiddenIntentSends, "a forbidden intent never reaches sendChatMessage");
assert.equal(forbiddenIntent.bridge.contextToken(), "", "a current send-intent 403 must revoke its current context before any later explicit retry");

// An authorization failure while reading a thumbnail is a scoped read failure
// too. It clears the current trusted context, while a profile CAS conflict is
// kept as a profile-specific recovery message and leaves the context usable.
const forbiddenThumbnail = createBridge(7, [], {
  fetch: async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, "https://sidebar.test.invalid");
    if (url.pathname.endsWith("/variants/thumb_320")) return response({ code: "invalid_context" }, 403);
    return backend(7)(input, init);
  },
});
const thumbnail = forbiddenThumbnail.dom.window.document.createElement("img");
await assert.rejects(
  () => forbiddenThumbnail.bridge.loadThumbnail(thumbnail, "/api/sidebar/v2/materials/77/variants/thumb_320"),
  /预览不可用/,
);
assert.equal(forbiddenThumbnail.bridge.contextToken(), "", "a current thumbnail 403 must revoke its current context before any later explicit retry");

const profileConflict = createBridge(8, [], {
  fetch: async (input, init = {}) => {
    const url = new URL(typeof input === "string" ? input : input.url, "https://sidebar.test.invalid");
    if (url.pathname === "/api/sidebar/v2/profile" && String(init.method || "GET").toUpperCase() === "PUT") return response({ code: "conflict" }, 409);
    if (url.pathname === "/api/sidebar/v2/materials") return response({ items: [], total: 0, limit: 5, offset: 0 });
    return backend(8)(input, init);
  },
});
await assert.rejects(
  () => profileConflict.bridge.request("/api/sidebar/v2/profile", { method: "PUT", body: JSON.stringify({ source: "stale" }) }),
  /客户资料已更新/,
);
await profileConflict.bridge.request("/api/sidebar/v2/materials?limit=5&offset=0");

// Exercise the actual Host's 202 preparation polling, with no executable
// grant until the durable material upload has completed.
function preparingBackend(customerID, onPending) {
  const readyBackend = backend(customerID);
  const keys = [];
  let requests = 0;
  return {
    keys,
    fetch: async (input, init = {}) => {
      const url = new URL(typeof input === "string" ? input : input.url, "https://sidebar.test.invalid");
      if (url.pathname === "/api/sidebar/v2/send-intents") {
        keys.push(requestHeaders(init).get("Idempotency-Key"));
        requests += 1;
        if (requests === 1) {
          onPending?.();
          return response({ state: "material_preparing" }, 202);
        }
      }
      return readyBackend(input, init);
    },
  };
}
const materialCard = { resource_kind: "material", resource_id: "image:91" };
const beforePreparedSends = sendInvocations;
const beforePreparedAccepts = accepts;
let pendingObserved;
const pending = new Promise((resolve) => { pendingObserved = resolve; });
const preparation = preparingBackend(3, pendingObserved);
const preparedCustomer = createBridge(3, [], { fetch: preparation.fetch, sdkSuccess: true });
const preparedSend = preparedCustomer.bridge.send(materialCard);
await pending;
assert.equal(sendInvocations, beforePreparedSends, "preparing response must not invoke the SDK");
assert.equal(accepts, beforePreparedAccepts, "preparing response must not create a chat intent");
await preparedSend;
assert.equal(preparation.keys.length, 2, "the Host polls after pending until ready");
assert.ok(preparation.keys[0], "preparation must use an idempotency key");
assert.equal(new Set(preparation.keys).size, 1, "pending and ready requests retain the same logical key");
assert.equal(accepts, beforePreparedAccepts + 1, "only ready material creates one chat intent");
assert.equal(sendInvocations, beforePreparedSends + 1, "pending then ready invokes the SDK exactly once");

let contact = "external-4";
let switchedPendingObserved;
const switchedPending = new Promise((resolve) => { switchedPendingObserved = resolve; });
const switchingPreparation = preparingBackend(4, switchedPendingObserved);
const switchingCustomer = createBridge(4, [], {
  fetch: switchingPreparation.fetch,
  currentContact: () => contact,
  sdkSuccess: true,
});
const beforeSwitchSends = sendInvocations;
const beforeSwitchAccepts = accepts;
const switchingSend = switchingCustomer.bridge.send(materialCard);
await switchedPending;
contact = "external-5";
await assert.rejects(() => switchingSend, /客户|上下文|切换/);
assert.equal(switchingPreparation.keys.length, 1, "a contact switch stops preparation before another request");
assert.equal(sendInvocations, beforeSwitchSends, "a contact switch during polling must never invoke the SDK");
assert.equal(accepts, beforeSwitchAccepts, "a contact switch cannot create a chat intent from prepared material");

for (const fixture of [first, reloaded, switchedCustomer, callbackFixture, forbiddenIntent, forbiddenThumbnail, profileConflict, preparedCustomer, switchingCustomer])
  fixture.dom.window.close();
console.log(
  "sidebar Host reload, duplicate-send, and customer-scope recovery: PASS",
);
