import {
  createWeComAcquisitionLink,
  deleteWeComAcquisitionLink,
  getWeComAcquisitionLink,
  listWeComAcquisitionLinks,
  reconcileWeComAcquisitionLink,
  updateWeComAcquisitionLink,
} from "./wecomAcquisitionLinks";
import { ApiError } from "./transport";

const assert = (value: unknown, message: string): void => {
  if (!value) throw new Error(message);
};
const digest = "a".repeat(64);
const link = {
  link_id: "link-1",
  link_name: "获客链接",
  url: "https://work.weixin.qq.com/ca/link-1",
  user_ids: ["staff-1"],
  department_ids: [7],
  skip_verify: true,
};
const input = {
  link_name: "获客链接",
  user_ids: ["staff-1"],
  department_ids: [7],
  skip_verify: true,
};
const core = {
  receipt_id: 11,
  business_endpoint_dispatched: true,
  real_external_call_executed: true,
  outcome_digest: digest,
};

export async function runWeComAcquisitionLinksAdapterTests(): Promise<void> {
  const previousFetch = globalThis.fetch;
  const oldDocument = Object.getOwnPropertyDescriptor(globalThis, "document");
  Object.defineProperty(globalThis, "document", {
    configurable: true,
    value: { cookie: "aicrm_csrf=acquisition-csrf" },
  });
  const calls: Array<{ url: string; init: RequestInit }> = [];
  let body: unknown = { items: [{ link_id: "link-1" }], next_cursor: "next-1" };
  let status = 200;
  globalThis.fetch = async (request, init = {}) => {
    calls.push({ url: String(request), init });
    return new Response(JSON.stringify(body), { status });
  };
  const rejects = async (
    call: () => Promise<unknown>,
    statusCode?: number,
  ): Promise<void> => {
    try {
      await call();
    } catch (error) {
      assert(
        statusCode === undefined ||
          (error instanceof ApiError && error.status === statusCode),
        "unexpected rejection",
      );
      return;
    }
    throw new Error("invalid acquisition-link action or response accepted");
  };

  try {
    const page = await listWeComAcquisitionLinks("", 50);
    assert(
      page.items[0].link_id === "link-1" &&
        calls[0].url === "/api/admin/wecom-customer-acquisition-links?limit=50",
      "list uses generated bounded Provider-read route",
    );
    body = link;
    assert(
      (await getWeComAcquisitionLink("link-1")).link_id === "link-1" &&
        calls[1].url.endsWith("/link-1"),
      "detail is bound to requested link ID",
    );

    body = { ...core, state: "executed", link };
    const created = await createWeComAcquisitionLink(
      input,
      "acquisition-create-0001",
    );
    assert(
      created.outcome === "applied" &&
        !created.canReconcile &&
        calls[2].init.method === "POST",
      "executed is externally applied only with full evidence",
    );
    body = { ...core, state: "executed", link };
    assert(
      (
        await updateWeComAcquisitionLink(
          "link-1",
          input,
          "acquisition-update-0001",
        )
      ).outcome === "applied" && calls[3].init.method === "PATCH",
      "update uses generated scoped PATCH",
    );
    body = { ...core, state: "executed" };
    assert(
      (await deleteWeComAcquisitionLink("link-1", "acquisition-delete-0001"))
        .outcome === "applied" && calls[4].init.method === "DELETE",
      "delete uses generated scoped DELETE without fabricated link",
    );

    const writes = calls.slice(2);
    assert(
      writes.every(
        (call) =>
          new Headers(call.init.headers).get("X-CSRF-Token") ===
            "acquisition-csrf" && call.init.credentials === "include",
      ),
      "all writes use same-origin session and CSRF",
    );
    assert(
      writes
        .map((call) => new Headers(call.init.headers).get("Idempotency-Key"))
        .join(",") ===
        "acquisition-create-0001,acquisition-update-0001,acquisition-delete-0001",
      "caller stable scoped idempotency keys are preserved",
    );

    for (const state of ["accepted", "attempted"] as const) {
      body = {
        receipt_id: 11,
        state,
        business_endpoint_dispatched: false,
        real_external_call_executed: false,
      };
      const result = await createWeComAcquisitionLink(
        input,
        `acquisition-${state}-0001`,
      );
      assert(
        result.outcome === "pending" && !result.canReconcile,
        `${state} is pending, never success`,
      );
    }
    body = {
      ...core,
      state: "final_failed",
      link: undefined,
      business_endpoint_dispatched: false,
      real_external_call_executed: false,
    };
    delete (body as Record<string, unknown>).link;
    assert(
      (await createWeComAcquisitionLink(input, "acquisition-failed-0001"))
        .outcome === "failed",
      "final_failed is not success",
    );

    const beforeUnknown = calls.length;
    body = {
      ...core,
      state: "outcome_unknown",
      real_external_call_executed: false,
    };
    const unknown = await updateWeComAcquisitionLink(
      "link-1",
      input,
      "acquisition-unknown-0001",
    );
    assert(
      unknown.outcome === "unknown" &&
        unknown.canReconcile &&
        calls.length === beforeUnknown + 1,
      "outcome_unknown is returned for explicit reconciliation without automatic retry",
    );
    body = {
      ...core,
      state: "reconciled",
      resolution: "provider_applied",
      link,
    };
    const reconciled = await reconcileWeComAcquisitionLink(
      "link-1",
      11,
      "provider_applied",
      "b".repeat(64),
      "acquisition-reconcile-0001",
    );
    const reconcileCall = calls[calls.length - 1];
    assert(
      reconciled.outcome === "applied" &&
        !reconciled.canReconcile &&
        reconcileCall.url.endsWith("/link-1/reconcile"),
      "reconciled provider_applied binds link and receipt IDs",
    );
    assert(
      JSON.parse(String(reconcileCall.init.body)).receipt_id === 11 &&
        new Headers(reconcileCall.init.headers).get("Idempotency-Key") ===
          "acquisition-reconcile-0001",
      "reconcile carries scoped receipt and stable key",
    );
    body = { ...core, state: "reconciled", resolution: "provider_not_applied" };
    assert(
      (
        await reconcileWeComAcquisitionLink(
          "link-1",
          11,
          "provider_not_applied",
          "c".repeat(64),
          "acquisition-reconcile-0002",
        )
      ).outcome === "not_applied",
      "reconciled provider_not_applied is not success",
    );

    for (const invalidBody of [
      { ...core, state: "queued" },
      { ...core, state: "accepted" },
      { ...core, state: "executed", link: { ...link, link_id: "link-other" } },
      { ...core, state: "outcome_unknown", resolution: "provider_applied" },
      {
        ...core,
        receipt_id: 12,
        state: "reconciled",
        resolution: "provider_applied",
        link,
      },
      { ...core, state: "reconciled", resolution: null, link },
    ]) {
      body = invalidBody;
      await rejects(() =>
        invalidBody.state === "reconciled"
          ? reconcileWeComAcquisitionLink(
              "link-1",
              11,
              "provider_applied",
              "d".repeat(64),
              "acquisition-reconcile-invalid",
            )
          : updateWeComAcquisitionLink(
              "link-1",
              input,
              "acquisition-invalid-0001",
            ),
      );
    }

    body = {
      items: [{ link_id: "link-1" }, { link_id: "link-1" }],
      next_cursor: "",
    };
    await rejects(() => listWeComAcquisitionLinks("", 50));
    body = { ...link, link_id: "link-other" };
    await rejects(() => getWeComAcquisitionLink("link-1"));
    const beforeInvalid = calls.length;
    await rejects(() => getWeComAcquisitionLink("bad/id"));
    await rejects(() =>
      createWeComAcquisitionLink({ ...input, user_ids: [] }, "short"),
    );
    await rejects(() =>
      reconcileWeComAcquisitionLink(
        "link-1",
        0,
        "provider_applied",
        digest,
        "acquisition-reconcile-0003",
      ),
    );
    assert(
      calls.length === beforeInvalid,
      "invalid link, input, receipt and key fail before transport",
    );

    status = 503;
    body = { ok: false };
    await rejects(() => listWeComAcquisitionLinks(), 503);
    assert(
      !calls.some((call) => /enable|disable/.test(call.url)),
      "legacy 0548 enable/disable operations are never called",
    );
  } finally {
    globalThis.fetch = previousFetch;
    if (oldDocument) Object.defineProperty(globalThis, "document", oldDocument);
    else Reflect.deleteProperty(globalThis, "document");
  }
}
