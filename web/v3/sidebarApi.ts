// @ts-nocheck
import {
  bootstrapSidebar,
  getSidebarWorkbench,
  mintSidebarContext,
} from "../src/api/generated/p4-sidebar-core/p4-sidebar-core";
import type {
  BootstrapSidebarBody,
  CompleteSidebarOAuthParams,
  ListSidebarMaterialsParams,
  ListSidebarOrdersParams,
  ListSidebarPeriodicOrdersParams,
  ListSidebarQuestionnairesParams,
  ListSidebarShareableProductsParams,
  ListSidebarTimelineParams,
  MintSidebarContextBody,
  SidebarBootstrapResponse,
  SidebarContextResponse,
  SidebarMaterialResponse,
  SidebarOrderResponse,
  SidebarPeriodicOrderResponse,
  SidebarPeriodicRemarkResponse,
  SidebarPhoneBindingResponse,
  SidebarProfileUpdateResponse,
  SidebarQuestionnaireResponse,
  SidebarShareableProductResponse,
  SidebarTimelineResponse,
  SidebarWorkbenchResponse,
  StartSidebarOAuthParams,
  UpdateSidebarPeriodicRemarkBody,
} from "../src/api/generated/health.schemas";
import { apiRequestOptions, ApiError, request, unwrapGenerated } from "../src/api/transport";

function scopedOptions(
  contextToken: string,
  init: RequestInit = {},
): RequestInit {
  return apiRequestOptions({
    ...init,
    headers: { ...init.headers, "X-Sidebar-Context-Token": contextToken },
  });
}

export function newSidebarIdempotencyKey(scope: string): string {
  const randomUUID = globalThis.crypto?.randomUUID?.();
  return randomUUID
    ? `${scope}-${randomUUID}`
    : `${scope}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
}

/**
 * 后端真实契约（internal/sidebar + internal/wecom），与生成客户端的 v2
 * 契约不同；以下常量与映射函数是唯一允许的拼接点，UI 层不得直接接触。
 */
const LOCAL_SAFETY = {
  local_only: true,
  provider_execution_eligible: false,
  real_external_call_executed: false,
} as const;

interface BackendSidebarCustomer {
  customer_id: number;
  display_name: string;
  avatar_url?: string;
  phone_masked?: string;
  status?: string;
  gender?: number;
  corp_name?: string;
  source?: string;
  version: number;
  updated_at: string;
}

function mapCustomerToProfile(
  customer: BackendSidebarCustomer,
): SidebarProfileUpdateResponse["profile"] {
  return {
    customer_id: customer.customer_id,
    name: customer.display_name,
    avatar_url: customer.avatar_url,
    phone_masked: customer.phone_masked,
    status: customer.status,
    gender: customer.gender,
    corp_name: customer.corp_name,
    source: customer.source,
    version: customer.version,
    updated_at: customer.updated_at,
  };
}

export interface UpdateLocalSidebarProfileBody {
  display_name: string;
  gender: number;
  corp_name: string;
  expected_version: number;
}

export interface BindLocalSidebarPhoneBody {
  /** 后端契约：11 位大陆手机号（非 E.164）。 */
  phone: string;
}

/** 后端 JSSDK 签名响应（internal/wecom JSSDKConfig）。 */
interface BackendJSSDKSignature {
  timestamp: number;
  nonceStr: string;
  signature: string;
  jsApiList?: string[];
}

interface BackendJSSDKConfig {
  corp_id: string;
  agent_id: string;
  config: BackendJSSDKSignature;
  agent_config: BackendJSSDKSignature;
}

// The formal JSSDK endpoint issues two signatures for the exact same
// no-fragment browser URL. Keep them together so a Host cannot accidentally
// configure an agent signature without first completing wx.config/wx.ready.
export interface SidebarJSSDKSignature {
  timestamp: number;
  nonce: string;
  signature: string;
  jsApiList: string[];
}

export interface SidebarJSSDKConfig {
  corpID: string;
  agentID: string;
  url: string;
  config: SidebarJSSDKSignature;
  agentConfig: SidebarJSSDKSignature;
}

function mapJSSDKSignature(value: BackendJSSDKSignature | undefined): SidebarJSSDKSignature {
  return {
    timestamp: value?.timestamp ?? 0,
    nonce: value?.nonceStr ?? "",
    signature: value?.signature ?? "",
    jsApiList: Array.isArray(value?.jsApiList) ? value.jsApiList : [],
  };
}

/** 后端发送意图受理回执（internal/outbound/port/sidebar_send.go）。 */
export interface SidebarSendIntentAcceptance {
  intent_id: number;
  effect_id?: string;
  state?: string;
  grant?: string;
  grant_expires_at?: string;
  /** 服务端封装的 JSSDK sendChatMessage 负载（含 mediaid）。 */
  payload?: unknown;
  replayed?: boolean;
}

export interface CreateSidebarSendIntentBody {
  resource_kind: "material";
  resource_id: string;
}

export interface CompleteSidebarSendIntentBody {
  grant: string;
  outcome: "client_executed" | "outcome_unknown" | "final_failed";
  evidence: string;
}

export const sidebarApi = {
  mintContext: async (body: MintSidebarContextBody) =>
    unwrapGenerated(
      await mintSidebarContext(body, apiRequestOptions()),
    ) as SidebarContextResponse,
  bootstrap: async (body: BootstrapSidebarBody, signal?: AbortSignal) =>
    unwrapGenerated(
      await bootstrapSidebar(body, apiRequestOptions({ signal })),
    ) as SidebarBootstrapResponse,
  jssdkConfig: async (url: string): Promise<SidebarJSSDKConfig> => {
    const response = await request(
      `/api/sidebar/jssdk-config?url=${encodeURIComponent(url)}`,
      apiRequestOptions(),
    );
    const config = (await response.json()) as BackendJSSDKConfig;
    return {
      corpID: config.corp_id,
      agentID: config.agent_id,
      url,
      config: mapJSSDKSignature(config.config),
      agentConfig: mapJSSDKSignature(config.agent_config),
    };
  },
  // 后端 OAuth 路由为 /api/sidebar/oauth/start|callback；start 只消费 next/mode，
  // external_userid 经 next 路径回传，不单独作为查询参数。
  oauthStartUrl: (params: StartSidebarOAuthParams) =>
    `/api/sidebar/oauth/start?next=${encodeURIComponent(params.next ?? "/sidebar/")}`,
  oauthCallbackUrl: (params: CompleteSidebarOAuthParams) =>
    `/api/sidebar/oauth/callback?code=${encodeURIComponent(params.code)}&state=${encodeURIComponent(params.state)}`,
  workbench: async (contextToken: string) =>
    unwrapGenerated(
      await getSidebarWorkbench(scopedOptions(contextToken)),
    ) as SidebarWorkbenchResponse,
  profile: async (
    contextToken: string,
    body: UpdateLocalSidebarProfileBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-profile"),
  ): Promise<SidebarProfileUpdateResponse> => {
    const response = await request(
      "/api/sidebar/v2/profile",
      scopedOptions(contextToken, {
        method: "PUT",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": idempotencyKey,
        },
        body: JSON.stringify(body),
      }),
    );
    const data = (await response.json()) as { customer: BackendSidebarCustomer };
    return {
      profile: mapCustomerToProfile(data.customer),
      safety: { ...LOCAL_SAFETY, effect_queued: false },
    };
  },
  bindPhone: async (
    contextToken: string,
    body: BindLocalSidebarPhoneBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-phone"),
  ): Promise<SidebarPhoneBindingResponse> => {
    try {
      const response = await request(
        "/api/sidebar/v2/phone-binding",
        scopedOptions(contextToken, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            "Idempotency-Key": idempotencyKey,
          },
          body: JSON.stringify({ phone: body.phone }),
        }),
      );
      const data = (await response.json()) as { status: string };
      const status =
        data.status === "attached"
          ? "bound"
          : data.status === "already_linked"
            ? "already_bound"
            : "rejected";
      return {
        status: status as SidebarPhoneBindingResponse["status"],
        safety: { ...LOCAL_SAFETY },
      };
    } catch (error) {
      // 后端对手机号归属冲突返回 409；UI 契约用 rejected 表达同一语义。
      if (error instanceof ApiError && error.status === 409) {
        return { status: "rejected", safety: { ...LOCAL_SAFETY } };
      }
      throw error;
    }
  },

  timeline: async (
    contextToken: string,
    params?: ListSidebarTimelineParams,
    signal?: AbortSignal,
  ) => {
    const { loadTimeline } = await import("./sidebar/tabs/timeline");
    return (await loadTimeline(
      contextToken,
      params,
      signal,
    )) as SidebarTimelineResponse;
  },
  questionnaires: async (
    contextToken: string,
    params?: ListSidebarQuestionnairesParams,
    signal?: AbortSignal,
  ) => {
    const { loadQuestionnaires } =
      await import("./sidebar/tabs/questionnaires");
    return (await loadQuestionnaires(
      contextToken,
      params,
      signal,
    )) as SidebarQuestionnaireResponse;
  },
  orders: async (
    contextToken: string,
    params?: ListSidebarOrdersParams,
    signal?: AbortSignal,
  ) => {
    const { loadOrders } = await import("./sidebar/tabs/orders");
    return (await loadOrders(
      contextToken,
      params,
      signal,
    )) as SidebarOrderResponse;
  },
  periodicOrders: async (
    contextToken: string,
    params?: ListSidebarPeriodicOrdersParams,
    signal?: AbortSignal,
  ) => {
    const { loadPeriodicOrders } =
      await import("./sidebar/tabs/periodic-orders");
    return (await loadPeriodicOrders(
      contextToken,
      params,
      signal,
    )) as SidebarPeriodicOrderResponse;
  },
  updateRemark: async (
    contextToken: string,
    serviceProductId: number,
    memberRef: string,
    body: UpdateSidebarPeriodicRemarkBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-periodic-remark"),
  ) => {
    const { savePeriodicRemark } =
      await import("./sidebar/tabs/periodic-orders");
    return (await savePeriodicRemark(
      contextToken,
      serviceProductId,
      memberRef,
      body,
      idempotencyKey,
    )) as SidebarPeriodicRemarkResponse;
  },
  materials: async (
    contextToken: string,
    params?: ListSidebarMaterialsParams,
    signal?: AbortSignal,
  ) => {
    const { loadMaterials } = await import("./sidebar/tabs/materials");
    return (await loadMaterials(
      contextToken,
      params,
      signal,
    )) as SidebarMaterialResponse;
  },
  shareableProducts: async (
    contextToken: string,
    params?: ListSidebarShareableProductsParams,
    signal?: AbortSignal,
  ) => {
    const { loadProducts } = await import("./sidebar/tabs/products");
    return (await loadProducts(
      contextToken,
      params,
      signal,
    )) as SidebarShareableProductResponse;
  },
  thumbnailPreview: async (
    contextToken: string,
    imageId: number,
    signal?: AbortSignal,
  ) => {
    const { loadThumbnailPreview } = await import("./sidebar/tabs/materials");
    return loadThumbnailPreview(contextToken, imageId, signal);
  },
  // 发送意图：服务端封装临时媒体与 payload，客户端仅执行 JSSDK 调用并
  // 用一次性 grant 回执结果；grant 过期或冲突由后端 409 表达。
  createSendIntent: async (
    contextToken: string,
    body: CreateSidebarSendIntentBody,
    idempotencyKey = newSidebarIdempotencyKey("sidebar-send-intent"),
  ): Promise<SidebarSendIntentAcceptance> => {
    const response = await request(
      "/api/sidebar/v2/send-intents",
      scopedOptions(contextToken, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          "Idempotency-Key": idempotencyKey,
        },
        body: JSON.stringify(body),
      }),
    );
    return (await response.json()) as SidebarSendIntentAcceptance;
  },
  completeSendIntent: async (
    contextToken: string,
    intentId: number,
    body: CompleteSidebarSendIntentBody,
  ): Promise<SidebarSendIntentAcceptance> => {
    const response = await request(
      `/api/sidebar/v2/send-intents/${intentId}/outcome`,
      scopedOptions(contextToken, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(body),
      }),
    );
    return (await response.json()) as SidebarSendIntentAcceptance;
  },
};
