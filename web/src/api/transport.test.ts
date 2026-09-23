import {
  apiRequestOptions,
  ApiError,
  request,
  unwrapGenerated,
} from "./transport";
import { sidebarApi } from "./sidebar";
import {
  profileReceiptSteps,
  SidebarBootstrapCoordinator,
} from "../sidebar/main";

function assert(ok: unknown, message: string): asserts ok {
  if (!ok) throw new Error(message);
}

export async function runTransportContractTests(): Promise<void> {
  const coordinator = new SidebarBootstrapCoordinator<string>();
  const signals: AbortSignal[] = [];
  let firstResolve: (value: string) => void = () => undefined;
  let secondResolve: (value: string) => void = () => undefined;
  const first = coordinator.run("external-1", (signal) => {
    signals.push(signal);
    return new Promise<string>((resolve) => {
      firstResolve = resolve;
    });
  });
  const duplicate = coordinator.run("external-1", () =>
    Promise.reject(new Error("duplicate request must not start")),
  );
  assert(
    first === duplicate && signals.length === 1,
    "same Sidebar customer bootstrap must be single-flight",
  );
  const second = coordinator.run("external-2", (signal) => {
    signals.push(signal);
    return new Promise<string>((resolve) => {
      secondResolve = resolve;
    });
  });
  assert(
    Number(signals.length) === 2 && signals[0].aborted,
    "Sidebar customer switch must abort the previous bootstrap",
  );
  firstResolve("stale");
  try {
    await first;
    throw new Error("stale Sidebar bootstrap was accepted");
  } catch (error) {
    assert(
      error instanceof Error && error.name === "AbortError",
      "stale Sidebar bootstrap response must be rejected",
    );
  }
  secondResolve("fresh");
  assert(
    (await second) === "fresh",
    "latest Sidebar bootstrap response must remain usable",
  );

  const options = apiRequestOptions(
    { method: "POST", headers: { "X-Request-ID": "test" } },
    "csrf_token=legacy; aicrm_csrf=token%201; session=x",
  );
  const headers = new Headers(options.headers);
  assert(
    options.credentials === "include",
    "same-origin cookie credentials must be included",
  );
  assert(
    !(options.headers instanceof Headers),
    "generated client options must use enumerable header records",
  );
  assert(
    headers.get("X-CSRF-Token") === "token 1",
    "OAuth session CSRF cookie must become X-CSRF-Token",
  );
  assert(
    headers.get("X-Request-ID") === "test",
    "caller headers must survive transport",
  );
  assert(
    unwrapGenerated({ status: 200, data: { cursor: "opaque" } }).cursor ===
      "opaque",
    "2xx generated response must unwrap",
  );
  try {
    unwrapGenerated({ status: 403, data: { code: "forbidden" } });
    throw new Error("403 was accepted");
  } catch (error) {
    assert(
      error instanceof ApiError && error.kind === "forbidden",
      "403 must be a structured forbidden error",
    );
  }

  const originalFetch = globalThis.fetch;
  const sidebarRequests: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    sidebarRequests.push({ input: String(input), init });
    const data = String(input).includes("other-staff-chats")
      ? {
          items: [
            {
              staff_userid: "staff-other",
              message_type: "text",
              content_masked: "已脱敏内容",
              sent_at: "2026-08-26T01:00:00Z",
            },
          ],
          safety: {
            local_only: true,
            provider_execution_eligible: false,
            real_external_call_executed: false,
          },
        }
      : String(input).includes("questionnaires")
        ? {
            items: [
              {
                submission_id: 11,
                questionnaire_id: 3,
                submitted_at: "2026-08-26T01:00:00Z",
                score: 8.5,
                choice_answers: [
                  {
                    question_id: 2,
                    question_type: "single_choice",
                    sort_order: 0,
                    option_ids: [9],
                  },
                ],
              },
            ],
            scan_truncated: false,
            result_truncated: false,
            safety: {
              local_only: true,
              provider_execution_eligible: false,
              real_external_call_executed: false,
            },
          }
        : String(input).includes("chat-activity")
          ? {
              items: [
                {
                  chat_type: "private",
                  message_type: "text",
                  sent_at: "2026-08-26T01:00:00Z",
                },
              ],
              safety: {
                local_only: true,
                provider_execution_eligible: false,
                real_external_call_executed: false,
              },
            }
          : {
              items: [
                {
                  id: 7,
                  event_type: "survey_submitted",
                  occurred_at: "2026-08-26T00:00:00Z",
                },
              ],
              next_cursor: "next-opaque",
              safety: {
                local_only: true,
                provider_execution_eligible: false,
                real_external_call_executed: false,
              },
            };
    return new Response(JSON.stringify(data), {
      status: 200,
      headers: { "Content-Type": "application/json" },
    });
  };
  try {
    const timeline = await sidebarApi.timeline("sidebar-context", {
      limit: 20,
    });
    const chat = await sidebarApi.chatActivity("sidebar-context", {
      chat_type: "private",
      limit: 10,
    });
    const otherStaffChats = await sidebarApi.otherStaffChats("sidebar-context");
    const questionnaires = await sidebarApi.questionnaires("sidebar-context", {
      limit: 100,
    });
    assert(
      timeline.items[0]?.event_type === "survey_submitted" &&
        timeline.next_cursor === "next-opaque",
      "Sidebar timeline response must retain safe DTO and cursor",
    );
    assert(
      chat.items[0]?.chat_type === "private",
      "Sidebar chat activity response must retain safe metadata DTO",
    );
    assert(
      otherStaffChats.items[0]?.staff_userid === "staff-other" &&
        otherStaffChats.items[0]?.content_masked === "已脱敏内容",
      "Sidebar other-staff chat adapter must retain only the masked local DTO",
    );
    assert(
      questionnaires.items[0]?.submission_id === 11 &&
        questionnaires.items[0]?.choice_answers[0]?.option_ids[0] === 9,
      "Sidebar questionnaire adapter must retain safe answer DTO",
    );
    assert(
      sidebarRequests[0]?.input === "/api/sidebar/v2/timeline?limit=20",
      "Sidebar timeline must use generated GET URL",
    );
    assert(
      sidebarRequests[1]?.input ===
        "/api/sidebar/v2/chat-activity?chat_type=private&limit=10",
      "Sidebar chat activity must use generated GET URL",
    );
    assert(
      sidebarRequests[2]?.input === "/api/sidebar/v2/other-staff-chats",
      "Sidebar other-staff chats must use the generated GET URL",
    );
    assert(
      sidebarRequests[3]?.input === "/api/sidebar/v2/questionnaires?limit=100",
      "Sidebar questionnaires must use generated GET URL",
    );
    for (const call of sidebarRequests) {
      assert(
        call.init?.method === "GET",
        "Sidebar activity reads must use GET",
      );
      assert(
        new Headers(call.init?.headers).get("X-Sidebar-Context-Token") ===
          "sidebar-context",
        "Sidebar activity reads must carry scoped context token",
      );
      assert(
        call.init?.credentials === "include",
        "Sidebar activity reads must include same-origin credentials",
      );
    }
  } finally {
    globalThis.fetch = originalFetch;
  }

  let seen: RequestInit | undefined;
  globalThis.fetch = async (_input, init) => {
    seen = init;
    return new Response(JSON.stringify({ code: "csrf_invalid" }), {
      status: 401,
      headers: { "Content-Type": "application/json" },
    });
  };
  try {
    await request("/api/v1/example", {
      method: "PUT",
      headers: { "X-CSRF-Token": "explicit" },
    });
    throw new Error("401 was accepted");
  } catch (error) {
    assert(
      error instanceof ApiError && error.kind === "unauthenticated",
      "401 must be a structured unauthenticated error",
    );
    assert(
      seen?.credentials === "include",
      "direct fetch must include credentials",
    );
    assert(
      new Headers(seen?.headers).get("X-CSRF-Token") === "explicit",
      "explicit CSRF header must not be overwritten",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }

  const sidebarWriteRequests: Array<{ input: string; init?: RequestInit }> = [];
  globalThis.fetch = async (input, init) => {
    sidebarWriteRequests.push({ input: String(input), init });
    if (String(input).includes("agent-config")) {
      return new Response(
        JSON.stringify({
          signature_type: "agent_config",
          corp_id: "corp-test",
          agent_id: 7,
          nonce: "nonce-test",
          timestamp: 1720000000,
          signature: "a".repeat(40),
          url: "https://app.test/sidebar/index.html",
          ticket_expires_at: "2026-08-26T03:00:00Z",
        }),
        { status: 200 },
      );
    }
    if (String(input).includes("/profile")) {
      return new Response(
        JSON.stringify({
          profile: {
            customer_id: 7,
            name: "测试客户",
            owner_staff_id: 9,
            source: "新来源",
            industry: "",
            description: "",
            needs: "",
            pain_points: "",
            updated_at: "2026-08-26T02:00:00Z",
          },
          safety: {
            local_only: true,
            effect_queued: true,
            provider_execution_eligible: true,
            real_external_call_executed: false,
          },
        }),
        { status: 200 },
      );
    }
    if (String(input).includes("/phone-binding")) {
      return new Response(
        JSON.stringify({
          status: "bound",
          safety: {
            local_only: true,
            provider_execution_eligible: false,
            real_external_call_executed: false,
          },
        }),
        { status: 200 },
      );
    }
    if (String(input).includes("/temporary-media")) {
      return new Response(
        JSON.stringify({
          image_id: 31,
          media_id: "media-temporary-31",
          media_expires_at: "2026-08-26T03:00:00Z",
          upload_state: "ready",
          provider_call_dispatched: true,
          real_external_call_executed: true,
          client_callback: "not_called",
          delivery_state: "not_sent_yet",
        }),
        { status: 200 },
      );
    }
    if (String(input).includes("/materials/image/31/preview")) {
      return new Response(new Blob(["image-bytes"], { type: "image/png" }), {
        status: 200,
        headers: { "Content-Type": "image/png", ETag: '"thumb"' },
      });
    }
    return new Response("", {
      status: 302,
      headers: { Location: "/sidebar/index.html?external_userid=ext-7" },
    });
  };
  try {
    const agentConfig = await sidebarApi.agentConfig(
      "https://app.test/sidebar/index.html",
    );
    assert(
      agentConfig.signature_type === "agent_config",
      "Sidebar agent config must use the generated V2 JSSDK DTO",
    );
    const profile = await sidebarApi.profile(
      "sidebar-context",
      {
        expected_updated_at: "2026-08-26T01:00:00Z",
        patch: { source: "新来源" },
      },
      "sidebar-profile-test-key",
    );
    assert(
      profile.profile.source === "新来源",
      "Sidebar profile adapter must retain the real update response",
    );
    const phone = await sidebarApi.bindPhone(
      "sidebar-context",
      { mobile: "+8613800138000" },
      "sidebar-phone-test-key",
    );
    assert(
      phone.status === "bound" && phone.safety.local_only,
      "Sidebar phone adapter must retain the local bind receipt",
    );
    const thumbnail = await sidebarApi.thumbnailPreview("sidebar-context", 31);
    assert(
      thumbnail.type === "image/png" && thumbnail.size > 0,
      "Sidebar thumbnail preview must read real binary bytes",
    );
    const temporaryMedia = await sidebarApi.prepareTemporaryImage(
      "sidebar-context",
      31,
      "sidebar-temporary-media-stable-key",
    );
    assert(
      temporaryMedia.upload_state === "ready" &&
        temporaryMedia.client_callback === "not_called" &&
        temporaryMedia.delivery_state === "not_sent_yet",
      "Temporary media must keep upload preparation separate from JSSDK and delivery",
    );
    const oauth = sidebarApi.oauthStartUrl({
      external_userid: "ext-7",
      next: "/sidebar/index.html",
    });
    assert(
      oauth.startsWith("/api/sidebar/v2/oauth/start?"),
      "Sidebar OAuth start must use the generated navigation URL",
    );
    const callback = sidebarApi.oauthCallbackUrl({
      code: "oauth-code",
      state: "state_abcdefghijklmnopqrstuvwxyz0123456789_",
    });
    assert(
      callback.startsWith("/api/sidebar/v2/oauth/callback?"),
      "Sidebar OAuth callback must use the generated navigation URL",
    );
    const agentCall = sidebarWriteRequests.find((call) =>
      call.input.includes("agent-config"),
    );
    assert(
      agentCall?.init?.credentials === "include",
      "JSSDK config must include the browser session",
    );
    const profileCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/profile"),
    );
    const profileHeaders = new Headers(profileCall?.init?.headers);
    assert(
      profileHeaders.get("X-Sidebar-Context-Token") === "sidebar-context",
      "Profile writes must carry the scoped context token",
    );
    assert(
      profileHeaders.get("Idempotency-Key") === "sidebar-profile-test-key",
      "Profile writes must carry the caller idempotency key",
    );
    assert(
      profileCall?.init?.credentials === "include",
      "Profile writes must include the browser session",
    );
    assert(
      profileCall?.init?.method === "PUT",
      "Profile writes must use the generated PUT operation",
    );
    const phoneCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/phone-binding"),
    );
    const phoneHeaders = new Headers(phoneCall?.init?.headers);
    assert(
      phoneCall?.init?.method === "POST" &&
        phoneHeaders.get("X-Sidebar-Context-Token") === "sidebar-context" &&
        phoneHeaders.get("Idempotency-Key") === "sidebar-phone-test-key",
      "Phone binding must use the generated operation with scoped idempotency headers",
    );
    const thumbnailCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/materials/image/31/preview"),
    );
    assert(
      thumbnailCall?.init?.method === undefined &&
        new Headers(thumbnailCall?.init?.headers).get(
          "X-Sidebar-Context-Token",
        ) === "sidebar-context",
      "Thumbnail preview must use the generated URL with scoped browser transport",
    );
    const temporaryMediaCall = sidebarWriteRequests.find((call) =>
      call.input.includes("/materials/image/31/temporary-media"),
    );
    const temporaryMediaHeaders = new Headers(
      temporaryMediaCall?.init?.headers,
    );
    assert(
      temporaryMediaCall?.init?.method === "POST" &&
        temporaryMediaHeaders.get("X-Sidebar-Context-Token") ===
          "sidebar-context" &&
        temporaryMediaHeaders.get("Idempotency-Key") ===
          "sidebar-temporary-media-stable-key",
      "Temporary media must forward the caller's stable scoped idempotency key",
    );
    assert(
      profileReceiptSteps({
        effect_queued: true,
        provider_execution_eligible: true,
      })
        .map((step) => step.key)
        .join(",") === "accepted,queued,outcome_unknown",
      "Queued provider effects must remain outcome_unknown until a receipt exists",
    );
  } finally {
    globalThis.fetch = originalFetch;
  }
}
